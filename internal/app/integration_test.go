package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/config"
	"github.com/SemSwitch/Nexis-Echo/internal/model"
	"github.com/SemSwitch/Nexis-Echo/internal/runtime"
	"github.com/SemSwitch/Nexis-Echo/internal/server"
)

func TestRegisterServiceBindsSourceProvider(t *testing.T) {
	cfg := config.Default()
	logger := slog.New(slog.NewTextHandler(testWriter{t: t}, nil))
	application := New(cfg, logger)

	provider := &stubRuntimeService{
		name: "buffer",
		sourceStates: []model.SourceState{
			{
				Source:     "default",
				Subject:    "npng.output.raw.default",
				LastSeenAt: time.Date(2026, time.March, 12, 23, 30, 0, 0, time.UTC),
				EventCount: 4,
				Active:     true,
			},
		},
	}

	application.RegisterService(provider)

	sources := application.sourcesProvider().Sources(context.Background())
	if len(sources) != 1 {
		t.Fatalf("expected 1 source snapshot, got %d", len(sources))
	}

	if sources[0].Source != "default" {
		t.Fatalf("expected source %q, got %q", "default", sources[0].Source)
	}
}

func TestRegisterServiceBindsLiveSink(t *testing.T) {
	cfg := config.Default()
	logger := slog.New(slog.NewTextHandler(testWriter{t: t}, nil))
	application := New(cfg, logger)

	producer := &stubRuntimeService{name: "buffer"}
	application.RegisterService(producer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := application.live.Subscribe(ctx)
	if err != nil {
		t.Fatalf("subscribe live broker: %v", err)
	}

	producer.publish("runtime.output.closed", map[string]string{"source": "default"})

	select {
	case event := <-ch:
		if event.Type != "runtime.output.closed" {
			t.Fatalf("expected live event %q, got %q", "runtime.output.closed", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestRegisterServiceBindsRuntimeEventSink(t *testing.T) {
	cfg := config.Default()
	logger := slog.New(slog.NewTextHandler(testWriter{t: t}, nil))
	application := New(cfg, logger)

	producer := &runtimeEventStubService{name: "buffer"}
	application.RegisterService(producer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := application.live.Subscribe(ctx)
	if err != nil {
		t.Fatalf("subscribe live broker: %v", err)
	}

	producer.publish(model.RuntimeEvent{
		Type:   "runtime.output.closed",
		Source: "default",
		TS:     time.Date(2026, time.March, 12, 23, 35, 0, 0, time.UTC),
	})

	select {
	case event := <-ch:
		if event.Type != "runtime.output.closed" {
			t.Fatalf("expected live event %q, got %q", "runtime.output.closed", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime event")
	}
}

func TestWireClosedBlockFlowConnectsProducerToConsumer(t *testing.T) {
	producer := &closedBlockProducerStub{}
	consumer := &outputBlockConsumerStub{}

	if ok := wireClosedBlockFlow(nil, producer, consumer); !ok {
		t.Fatal("expected closed block flow to be wired")
	}

	block := model.OutputBlock{
		ID:      "builder-1",
		Source:  "builder",
		Content: "build finished",
	}
	producer.publish(block)

	if consumer.lastBlock.ID != "builder-1" {
		t.Fatalf("expected block %q to reach consumer, got %+v", "builder-1", consumer.lastBlock)
	}
}

func TestWireNotificationFlowConnectsProducerToConsumer(t *testing.T) {
	producer := &notificationReadyProducerStub{}
	consumer := &notificationReadyConsumerStub{}

	if ok := wireNotificationFlow(nil, producer, consumer); !ok {
		t.Fatal("expected notification flow to be wired")
	}

	notification := model.NotificationReady{
		Source:  "builder",
		Summary: "Build completed successfully.",
	}
	producer.publish(notification)

	if consumer.last.Source != "builder" {
		t.Fatalf("expected source %q to reach consumer, got %+v", "builder", consumer.last)
	}
	if consumer.last.Summary != notification.Summary {
		t.Fatalf("expected summary %q, got %q", notification.Summary, consumer.last.Summary)
	}
}

func TestRuntimeFlowDeliversClosedBlockToPipeline(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(testWriter{t: t}, nil))
	buffer := runtime.NewBuffer(runtime.BufferConfig{}, logger)
	summarizer := runtime.NewSummarizer(runtime.SummarizerConfig{
		Provider:     "heuristic",
		MaxBatchSize: 8,
		CacheEntries: 8,
		QueueSize:    4,
	}, logger)
	pipeline := runtime.NewPipeline(runtime.PipelineConfig{Channels: []string{"local"}}, logger)

	if ok := wireClosedBlockFlow(logger, buffer, summarizer); !ok {
		t.Fatal("expected closed block flow to be wired")
	}
	if ok := wireNotificationFlow(logger, summarizer, pipeline); !ok {
		t.Fatal("expected notification flow to be wired")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := buffer.Start(ctx); err != nil {
		t.Fatalf("start buffer: %v", err)
	}
	if err := summarizer.Start(ctx); err != nil {
		t.Fatalf("start summarizer: %v", err)
	}
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("start pipeline: %v", err)
	}
	defer func() {
		_ = pipeline.Stop(context.Background())
		_ = summarizer.Stop(context.Background())
		_ = buffer.Stop(context.Background())
	}()

	base := time.Date(2026, time.March, 13, 2, 0, 0, 0, time.UTC)
	if err := buffer.HandleRawOutput(context.Background(), model.RawOutput{
		Source: "builder",
		Chunk:  "build succeeded\n",
		TS:     base,
	}); err != nil {
		t.Fatalf("handle raw output: %v", err)
	}
	if err := buffer.HandleOutputClosed(context.Background(), model.OutputClosed{
		Source: "builder",
		Status: "success",
		Final:  true,
		TS:     base.Add(time.Second),
	}); err != nil {
		t.Fatalf("handle output closed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ready := pipeline.ReadyNotifications(context.Background())
		if len(ready) == 1 {
			if ready[0].Source != "builder" {
				t.Fatalf("expected source %q, got %q", "builder", ready[0].Source)
			}
			if ready[0].Summary == "" {
				t.Fatal("expected non-empty summary")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("timed out waiting for notification-ready flow")
}

func TestNewWiresRuntimeFlowAndBridgesLiveEvents(t *testing.T) {
	cfg := config.Default()
	logger := slog.New(slog.NewTextHandler(testWriter{t: t}, nil))
	application := New(cfg, logger)

	buffer, summarizer, pipeline := requireRuntimeFlowServices(t, application.services)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	liveCh, err := application.live.Subscribe(ctx)
	if err != nil {
		t.Fatalf("subscribe live broker: %v", err)
	}

	if err := buffer.Start(ctx); err != nil {
		t.Fatalf("start buffer: %v", err)
	}
	if err := summarizer.Start(ctx); err != nil {
		t.Fatalf("start summarizer: %v", err)
	}
	if err := pipeline.Start(ctx); err != nil {
		t.Fatalf("start pipeline: %v", err)
	}
	defer func() {
		_ = pipeline.Stop(context.Background())
		_ = summarizer.Stop(context.Background())
		_ = buffer.Stop(context.Background())
	}()

	base := time.Date(2026, time.March, 13, 3, 0, 0, 0, time.UTC)
	if err := buffer.HandleRawOutput(context.Background(), model.RawOutput{
		Source: "builder",
		Chunk:  "tests passed\n",
		TS:     base,
	}); err != nil {
		t.Fatalf("handle raw output: %v", err)
	}
	if err := buffer.HandleOutputClosed(context.Background(), model.OutputClosed{
		Source: "builder",
		Status: "success",
		Final:  true,
		TS:     base.Add(time.Second),
	}); err != nil {
		t.Fatalf("handle output closed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	seen := map[string]bool{}
	for time.Now().Before(deadline) {
		select {
		case event := <-liveCh:
			seen[event.Type] = true
			if seen["runtime.notification.ready"] && seen["runtime.notification.accepted"] {
				ready := pipeline.ReadyNotifications(context.Background())
				if len(ready) != 1 {
					t.Fatalf("expected 1 ready notification, got %d", len(ready))
				}
				if ready[0].Source != "builder" {
					t.Fatalf("expected source %q, got %q", "builder", ready[0].Source)
				}
				return
			}
		case <-time.After(20 * time.Millisecond):
		}
	}

	t.Fatalf("timed out waiting for bridged live events, saw %v", seen)
}

type stubRuntimeService struct {
	name         string
	sourceStates []model.SourceState
	liveSink     func(string, interface{})
}

func (s *stubRuntimeService) Name() string { return s.name }

func (s *stubRuntimeService) Start(context.Context) error { return nil }

func (s *stubRuntimeService) Stop(context.Context) error { return nil }

func (s *stubRuntimeService) Health() model.ServiceHealth {
	return model.ServiceHealth{
		Name:      s.name,
		State:     model.StateRunning,
		CheckedAt: time.Now().UTC(),
	}
}

func (s *stubRuntimeService) RegisterLiveSink(sink func(string, interface{})) {
	s.liveSink = sink
}

func (s *stubRuntimeService) SourceStates(context.Context) []model.SourceState {
	return s.sourceStates
}

func (s *stubRuntimeService) publish(eventType string, data interface{}) {
	if s.liveSink != nil {
		s.liveSink(eventType, data)
	}
}

type runtimeEventStubService struct {
	name     string
	liveSink func(model.RuntimeEvent)
}

func (s *runtimeEventStubService) Name() string { return s.name }

func (s *runtimeEventStubService) Start(context.Context) error { return nil }

func (s *runtimeEventStubService) Stop(context.Context) error { return nil }

func (s *runtimeEventStubService) Health() model.ServiceHealth {
	return model.ServiceHealth{
		Name:      s.name,
		State:     model.StateRunning,
		CheckedAt: time.Now().UTC(),
	}
}

func (s *runtimeEventStubService) RegisterRuntimeEventSink(sink func(model.RuntimeEvent)) {
	s.liveSink = sink
}

func (s *runtimeEventStubService) publish(event model.RuntimeEvent) {
	if s.liveSink != nil {
		s.liveSink(event)
	}
}

type testWriter struct {
	t *testing.T
}

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

type closedBlockProducerStub struct {
	sink func(model.OutputBlock)
}

func (s *closedBlockProducerStub) RegisterClosedBlockSink(sink func(model.OutputBlock)) {
	s.sink = sink
}

func (s *closedBlockProducerStub) publish(block model.OutputBlock) {
	if s.sink != nil {
		s.sink(block)
	}
}

type outputBlockConsumerStub struct {
	lastBlock model.OutputBlock
}

func (s *outputBlockConsumerStub) HandleOutputBlock(_ context.Context, block model.OutputBlock) error {
	s.lastBlock = block
	return nil
}

type notificationReadyProducerStub struct {
	sink func(model.NotificationReady)
}

func (s *notificationReadyProducerStub) RegisterNotificationSink(sink func(model.NotificationReady)) {
	s.sink = sink
}

func (s *notificationReadyProducerStub) publish(notification model.NotificationReady) {
	if s.sink != nil {
		s.sink(notification)
	}
}

type notificationReadyConsumerStub struct {
	last model.NotificationReady
}

func (s *notificationReadyConsumerStub) HandleNotificationReady(_ context.Context, notification model.NotificationReady) error {
	s.last = notification
	return nil
}

func requireRuntimeFlowServices(t *testing.T, services []runtime.Service) (*runtime.Buffer, *runtime.Summarizer, *runtime.Pipeline) {
	t.Helper()

	var (
		buffer     *runtime.Buffer
		summarizer *runtime.Summarizer
		pipeline   *runtime.Pipeline
	)

	for _, svc := range services {
		switch typed := svc.(type) {
		case *runtime.Buffer:
			buffer = typed
		case *runtime.Summarizer:
			summarizer = typed
		case *runtime.Pipeline:
			pipeline = typed
		}
	}

	if buffer == nil {
		t.Fatal("expected app to register a runtime buffer")
	}
	if summarizer == nil {
		t.Fatal("expected app to register a runtime summarizer")
	}
	if pipeline == nil {
		t.Fatal("expected app to register a runtime pipeline")
	}

	return buffer, summarizer, pipeline
}

var _ server.SourcesProvider = sourceReadModel{}

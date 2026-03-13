package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/config"
	"github.com/SemSwitch/Nexis-Echo/internal/model"
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

var _ server.SourcesProvider = sourceReadModel{}

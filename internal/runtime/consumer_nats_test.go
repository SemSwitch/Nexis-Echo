package runtime

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestConsumerForwardsRawAndClosedEvents(t *testing.T) {
	srv := runTestNATSServer(t)
	t.Cleanup(srv.Shutdown)

	sink := &recordingSink{}
	consumer := NewConsumer(ConsumerConfig{
		URL:      srv.ClientURL(),
		Subjects: []string{"npng.output.raw.*", "npng.output.closed.*"},
		Sink:     sink,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	t.Cleanup(func() {
		_ = consumer.Stop(context.Background())
	})

	pub, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("connect publisher: %v", err)
	}
	defer pub.Close()

	rawTS := time.Date(2026, time.March, 12, 22, 30, 0, 0, time.UTC)
	closedTS := rawTS.Add(5 * time.Second)

	rawPayload := model.RawOutput{
		Chunk: "build started\n",
		TS:    rawTS,
	}
	if err := publishJSON(pub, "npng.output.raw.builder", rawPayload); err != nil {
		t.Fatalf("publish raw: %v", err)
	}

	closedPayload := model.OutputClosed{
		Status:     "success",
		DurationMS: 5000,
		Final:      true,
		TS:         closedTS,
	}
	if err := publishJSON(pub, "npng.output.closed.builder", closedPayload); err != nil {
		t.Fatalf("publish closed: %v", err)
	}
	if err := pub.Flush(); err != nil {
		t.Fatalf("flush publisher: %v", err)
	}

	raw, closed := sink.waitForCounts(t, 1, 1)

	if raw[0].Source != "builder" {
		t.Fatalf("expected raw source builder, got %q", raw[0].Source)
	}
	if raw[0].Subject != "npng.output.raw.builder" {
		t.Fatalf("expected raw subject to be preserved, got %q", raw[0].Subject)
	}
	if raw[0].TS != rawTS {
		t.Fatalf("expected raw timestamp %v, got %v", rawTS, raw[0].TS)
	}

	if closed[0].Source != "builder" {
		t.Fatalf("expected closed source builder, got %q", closed[0].Source)
	}
	if closed[0].Subject != "npng.output.closed.builder" {
		t.Fatalf("expected closed subject to be preserved, got %q", closed[0].Subject)
	}
	if closed[0].Status != "success" || !closed[0].Final {
		t.Fatalf("unexpected closed payload: %+v", closed[0])
	}
}

func TestConsumerIgnoresBadPayloadAndContinues(t *testing.T) {
	srv := runTestNATSServer(t)
	t.Cleanup(srv.Shutdown)

	sink := &recordingSink{}
	consumer := NewConsumer(ConsumerConfig{
		URL:      srv.ClientURL(),
		Subjects: []string{"npng.output.raw.*"},
		Sink:     sink,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := consumer.Start(ctx); err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	t.Cleanup(func() {
		_ = consumer.Stop(context.Background())
	})

	pub, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("connect publisher: %v", err)
	}
	defer pub.Close()

	if err := pub.Publish("npng.output.raw.builder", []byte("{not-json")); err != nil {
		t.Fatalf("publish invalid payload: %v", err)
	}
	if err := publishJSON(pub, "npng.output.raw.builder", model.RawOutput{
		Chunk: "build finished\n",
	}); err != nil {
		t.Fatalf("publish valid payload: %v", err)
	}
	if err := pub.Flush(); err != nil {
		t.Fatalf("flush publisher: %v", err)
	}

	raw, closed := sink.waitForCounts(t, 1, 0)
	if len(closed) != 0 {
		t.Fatalf("expected no closed events, got %d", len(closed))
	}
	if raw[0].Chunk != "build finished\n" {
		t.Fatalf("expected valid raw event after invalid payload, got %+v", raw[0])
	}

	health := consumer.Health()
	if health.State != model.StateRunning {
		t.Fatalf("expected consumer to remain running, got %s (%s)", health.State.String(), health.Message)
	}
}

type recordingSink struct {
	mu     sync.Mutex
	raw    []model.RawOutput
	closed []model.OutputClosed
}

func (s *recordingSink) HandleRaw(_ context.Context, payload model.RawOutput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.raw = append(s.raw, payload)
	return nil
}

func (s *recordingSink) HandleClosed(_ context.Context, payload model.OutputClosed) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = append(s.closed, payload)
	return nil
}

func (s *recordingSink) waitForCounts(t *testing.T, wantRaw int, wantClosed int) ([]model.RawOutput, []model.OutputClosed) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		raw := append([]model.RawOutput(nil), s.raw...)
		closed := append([]model.OutputClosed(nil), s.closed...)
		s.mu.Unlock()

		if len(raw) >= wantRaw && len(closed) >= wantClosed {
			return raw, closed
		}

		time.Sleep(20 * time.Millisecond)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t.Fatalf("timed out waiting for raw=%d closed=%d; got raw=%d closed=%d", wantRaw, wantClosed, len(s.raw), len(s.closed))
	return nil, nil
}

func publishJSON(nc *nats.Conn, subject string, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}

	return nc.Publish(subject, data)
}

func runTestNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()

	srv, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1",
		Port: -1,
	})
	if err != nil {
		t.Fatalf("new nats server: %v", err)
	}

	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		srv.Shutdown()
		t.Fatal("nats server did not become ready")
	}

	return srv
}

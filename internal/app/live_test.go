package app

import (
	"context"
	"testing"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/server"
)

func TestLiveBrokerReplaysLatestEventToNewSubscriber(t *testing.T) {
	broker := newLiveBroker()
	latest := server.LiveEvent{
		Type:      "runtime.snapshot",
		Timestamp: time.Date(2026, time.March, 12, 23, 0, 0, 0, time.UTC),
		Data: map[string]string{
			"status": "ok",
		},
	}

	broker.Publish(latest)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := broker.Subscribe(ctx)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	select {
	case event := <-ch:
		if event.Type != latest.Type {
			t.Fatalf("expected type %q, got %q", latest.Type, event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replayed event")
	}
}

func TestLiveBrokerPublishesToActiveSubscribers(t *testing.T) {
	broker := newLiveBroker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := broker.Subscribe(ctx)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	want := server.LiveEvent{
		Type:      "runtime.stopping",
		Timestamp: time.Date(2026, time.March, 12, 23, 5, 0, 0, time.UTC),
	}
	broker.Publish(want)

	select {
	case event := <-ch:
		if event.Type != want.Type {
			t.Fatalf("expected type %q, got %q", want.Type, event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published event")
	}
}

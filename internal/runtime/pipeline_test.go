package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

func TestPipelineAcceptsReadyNotificationsAndEmitsRuntimeEvents(t *testing.T) {
	pipeline := NewPipeline(PipelineConfig{Channels: []string{"local"}}, nil)

	events := make(chan model.RuntimeEvent, 1)
	pipeline.RegisterRuntimeEventSink(func(event model.RuntimeEvent) {
		events <- event
	})

	if err := pipeline.Start(context.Background()); err != nil {
		t.Fatalf("start pipeline: %v", err)
	}
	defer func() {
		_ = pipeline.Stop(context.Background())
	}()

	ready := model.NotificationReady{
		Source:   "builder",
		Summary:  "Build completed successfully.",
		Excerpt:  "all tests passed",
		Severity: model.SeveritySuccess,
		TS:       time.Date(2026, time.March, 12, 23, 58, 0, 0, time.UTC),
	}
	if err := pipeline.HandleNotificationReady(context.Background(), ready); err != nil {
		t.Fatalf("handle ready notification: %v", err)
	}

	select {
	case event := <-events:
		if event.Type != "runtime.notification.accepted" {
			t.Fatalf("expected runtime.notification.accepted, got %q", event.Type)
		}

		payload, ok := event.Data.(model.NotificationReady)
		if !ok {
			t.Fatalf("expected notification payload, got %T", event.Data)
		}
		if payload.EventID == "" {
			t.Fatal("expected pipeline to assign an event id")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime event")
	}

	stored := pipeline.ReadyNotifications(context.Background())
	if len(stored) != 1 {
		t.Fatalf("expected 1 stored notification, got %d", len(stored))
	}
	if stored[0].Summary != ready.Summary {
		t.Fatalf("expected stored summary %q, got %q", ready.Summary, stored[0].Summary)
	}
}

func TestPipelineRejectsNotificationsWhenStopped(t *testing.T) {
	pipeline := NewPipeline(PipelineConfig{}, nil)

	if err := pipeline.HandleNotification(context.Background(), model.NotificationReady{
		Source:  "builder",
		Summary: "Build completed successfully.",
	}); err == nil {
		t.Fatal("expected pipeline to reject notifications while stopped")
	}
}

func TestSeverityJSONUsesStringLabels(t *testing.T) {
	payload := model.NotificationReady{
		Source:   "builder",
		Summary:  "Build completed successfully.",
		Severity: model.SeverityWarning,
		TS:       time.Date(2026, time.March, 13, 0, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !strings.Contains(string(data), "\"severity\":\"warning\"") {
		t.Fatalf("expected severity to marshal as warning, got %s", data)
	}

	var decoded model.NotificationReady
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.Severity != model.SeverityWarning {
		t.Fatalf("expected severity warning, got %s", decoded.Severity.String())
	}
}

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

func TestBufferTracksSourceStateAndClosedBlocks(t *testing.T) {
	buffer := NewBuffer(BufferConfig{MaxClosedBlocks: 2}, nil)

	if err := buffer.Start(context.Background()); err != nil {
		t.Fatalf("start buffer: %v", err)
	}

	rawTS := time.Date(2026, time.March, 12, 23, 45, 0, 0, time.UTC)
	if err := buffer.HandleRawOutput(context.Background(), model.RawOutput{
		Source:  "builder",
		Subject: "npng.output.raw.builder",
		Chunk:   "running build\n",
		TS:      rawTS,
	}); err != nil {
		t.Fatalf("handle raw output: %v", err)
	}

	closedTS := rawTS.Add(2 * time.Minute)
	if err := buffer.HandleOutputClosed(context.Background(), model.OutputClosed{
		Source:     "builder",
		Subject:    "npng.output.closed.builder",
		Status:     "success",
		DurationMS: 120000,
		Final:      true,
		TS:         closedTS,
	}); err != nil {
		t.Fatalf("handle output closed: %v", err)
	}

	states := buffer.SourceStates(context.Background())
	if len(states) != 1 {
		t.Fatalf("expected 1 source state, got %d", len(states))
	}

	if states[0].Source != "builder" {
		t.Fatalf("expected source %q, got %q", "builder", states[0].Source)
	}
	if states[0].LastStatus != "success" {
		t.Fatalf("expected last status success, got %q", states[0].LastStatus)
	}
	if states[0].BufferedBytes != 0 {
		t.Fatalf("expected buffered bytes to reset, got %d", states[0].BufferedBytes)
	}
	if states[0].ClosedCount != 1 {
		t.Fatalf("expected closed count 1, got %d", states[0].ClosedCount)
	}
	if states[0].Active {
		t.Fatal("expected source to be inactive after close")
	}

	blocks := buffer.ClosedBlocks(context.Background())
	if len(blocks) != 1 {
		t.Fatalf("expected 1 closed block, got %d", len(blocks))
	}
	if blocks[0].Content != "running build\n" {
		t.Fatalf("expected closed block content to match buffered text, got %q", blocks[0].Content)
	}
}

func TestBufferPublishesRuntimeEvents(t *testing.T) {
	buffer := NewBuffer(BufferConfig{}, nil)
	events := make([]model.RuntimeEvent, 0, 4)

	buffer.RegisterRuntimeEventSink(func(event model.RuntimeEvent) {
		events = append(events, event)
	})

	if err := buffer.HandleRawOutput(context.Background(), model.RawOutput{
		Source: "builder",
		Chunk:  "step 1\n",
		TS:     time.Date(2026, time.March, 12, 23, 50, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("handle raw output: %v", err)
	}
	if err := buffer.HandleOutputClosed(context.Background(), model.OutputClosed{
		Source: "builder",
		Status: "success",
		Final:  true,
		TS:     time.Date(2026, time.March, 12, 23, 51, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("handle output closed: %v", err)
	}

	if len(events) != 4 {
		t.Fatalf("expected 4 runtime events, got %d", len(events))
	}
	if events[0].Type != "runtime.output.raw" {
		t.Fatalf("expected first event to be raw, got %q", events[0].Type)
	}
	if events[1].Type != "runtime.source.updated" {
		t.Fatalf("expected second event to be source updated, got %q", events[1].Type)
	}
	if events[2].Type != "runtime.output.closed" {
		t.Fatalf("expected third event to be closed, got %q", events[2].Type)
	}
	if events[3].Type != "runtime.source.updated" {
		t.Fatalf("expected fourth event to be source updated, got %q", events[3].Type)
	}
}

func TestBufferPublishesClosedBlocks(t *testing.T) {
	buffer := NewBuffer(BufferConfig{}, nil)
	blocks := make([]model.OutputBlock, 0, 1)

	buffer.RegisterClosedBlockSink(func(block model.OutputBlock) {
		blocks = append(blocks, block)
	})

	if err := buffer.HandleRawOutput(context.Background(), model.RawOutput{
		Source: "builder",
		Chunk:  "running build\n",
		TS:     time.Date(2026, time.March, 12, 23, 55, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("handle raw output: %v", err)
	}

	if err := buffer.HandleOutputClosed(context.Background(), model.OutputClosed{
		Source: "builder",
		Status: "success",
		Final:  true,
		TS:     time.Date(2026, time.March, 12, 23, 56, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("handle output closed: %v", err)
	}

	if len(blocks) != 1 {
		t.Fatalf("expected 1 closed block, got %d", len(blocks))
	}
	if blocks[0].Source != "builder" {
		t.Fatalf("expected block source %q, got %q", "builder", blocks[0].Source)
	}
	if blocks[0].Content != "running build\n" {
		t.Fatalf("expected block content to match buffered text, got %q", blocks[0].Content)
	}
}

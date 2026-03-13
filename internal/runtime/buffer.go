package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

const defaultMaxClosedBlocks = 128

// EventSink receives decoded terminal-output events from the NATS consumer.
type EventSink interface {
	HandleRaw(context.Context, model.RawOutput) error
	HandleClosed(context.Context, model.OutputClosed) error
}

// BufferConfig controls in-memory buffering of terminal output.
type BufferConfig struct {
	MaxClosedBlocks int
}

// Buffer stores raw terminal output by source and retains recent closed blocks.
type Buffer struct {
	cfg    BufferConfig
	logger *slog.Logger

	mu           sync.Mutex
	state        model.ServiceState
	liveSink     func(model.RuntimeEvent)
	closedSink   func(model.OutputBlock)
	sources      map[string]*bufferedSource
	closedBlocks []model.OutputBlock
}

type bufferedSource struct {
	state model.SourceState
	buf   []byte
}

// NewBuffer creates a Buffer with in-memory defaults suitable for the first
// event-pipeline slice.
func NewBuffer(cfg BufferConfig, logger *slog.Logger) *Buffer {
	if cfg.MaxClosedBlocks <= 0 {
		cfg.MaxClosedBlocks = defaultMaxClosedBlocks
	}

	return &Buffer{
		cfg:     cfg,
		logger:  ensureLogger(logger),
		state:   model.StateIdle,
		sources: make(map[string]*bufferedSource),
	}
}

func (b *Buffer) Name() string { return "buffer" }

func (b *Buffer) Start(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = model.StateRunning
	return nil
}

func (b *Buffer) Stop(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.state = model.StateStopped
	return nil
}

func (b *Buffer) Health() model.ServiceHealth {
	b.mu.Lock()
	defer b.mu.Unlock()

	return model.ServiceHealth{
		Name:      b.Name(),
		State:     b.state,
		CheckedAt: time.Now().UTC(),
	}
}

func (b *Buffer) HandleRaw(ctx context.Context, payload model.RawOutput) error {
	return b.HandleRawOutput(ctx, payload)
}

func (b *Buffer) HandleRawOutput(_ context.Context, payload model.RawOutput) error {
	b.mu.Lock()
	sourceName := normalizeSourceName(payload.Source)
	payload.Source = sourceName
	source := b.getOrCreateSource(sourceName)
	source.state.Source = sourceName
	source.state.Subject = payload.Subject
	source.state.LastSeenAt = nonZeroTime(payload.TS)
	source.state.EventCount++
	source.state.BufferedBytes += len(payload.Chunk)
	source.state.Active = true
	source.buf = append(source.buf, payload.Chunk...)
	snapshot := source.state
	b.mu.Unlock()

	b.emit(model.RuntimeEvent{
		Type:   "runtime.output.raw",
		Source: payload.Source,
		TS:     snapshot.LastSeenAt,
		Data:   payload,
	})
	b.emit(model.RuntimeEvent{
		Type:   "runtime.source.updated",
		Source: payload.Source,
		TS:     snapshot.LastSeenAt,
		Data:   snapshot,
	})

	return nil
}

func (b *Buffer) HandleClosed(ctx context.Context, payload model.OutputClosed) error {
	return b.HandleOutputClosed(ctx, payload)
}

func (b *Buffer) HandleOutputClosed(_ context.Context, payload model.OutputClosed) error {
	b.mu.Lock()
	sourceName := normalizeSourceName(payload.Source)
	payload.Source = sourceName
	source := b.getOrCreateSource(sourceName)
	content := string(source.buf)
	closedAt := nonZeroTime(payload.TS)

	block := model.OutputBlock{
		ID:         fmt.Sprintf("%s-%d", sourceName, closedAt.UnixNano()),
		Source:     sourceName,
		Subject:    payload.Subject,
		Content:    content,
		Status:     payload.Status,
		DurationMS: payload.DurationMS,
		ClosedAt:   closedAt,
		Final:      payload.Final,
	}

	source.state.Source = sourceName
	source.state.Subject = payload.Subject
	source.state.LastSeenAt = closedAt
	source.state.LastClosedAt = closedAt
	source.state.LastStatus = payload.Status
	source.state.EventCount++
	source.state.ClosedCount++
	source.state.BufferedBytes = 0
	source.state.Active = false
	source.buf = source.buf[:0]
	snapshot := source.state

	b.closedBlocks = append(b.closedBlocks, block)
	if len(b.closedBlocks) > b.cfg.MaxClosedBlocks {
		excess := len(b.closedBlocks) - b.cfg.MaxClosedBlocks
		b.closedBlocks = append([]model.OutputBlock(nil), b.closedBlocks[excess:]...)
	}
	b.mu.Unlock()

	b.emit(model.RuntimeEvent{
		Type:   "runtime.output.closed",
		Source: sourceName,
		TS:     closedAt,
		Data:   block,
	})
	b.emit(model.RuntimeEvent{
		Type:   "runtime.source.updated",
		Source: sourceName,
		TS:     closedAt,
		Data:   snapshot,
	})
	b.emitClosedBlock(block)

	return nil
}

func (b *Buffer) RegisterRuntimeEventSink(sink func(model.RuntimeEvent)) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.liveSink = sink
}

func (b *Buffer) RegisterClosedBlockSink(sink func(model.OutputBlock)) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closedSink = sink
}

func (b *Buffer) SourceStates(context.Context) []model.SourceState {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.sources) == 0 {
		return []model.SourceState{}
	}

	states := make([]model.SourceState, 0, len(b.sources))
	for _, source := range b.sources {
		states = append(states, source.state)
	}

	sort.Slice(states, func(i, j int) bool {
		if states[i].LastSeenAt.Equal(states[j].LastSeenAt) {
			return states[i].Source < states[j].Source
		}
		return states[i].LastSeenAt.After(states[j].LastSeenAt)
	})

	return states
}

func (b *Buffer) ClosedBlocks(context.Context) []model.OutputBlock {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.closedBlocks) == 0 {
		return []model.OutputBlock{}
	}

	blocks := make([]model.OutputBlock, len(b.closedBlocks))
	copy(blocks, b.closedBlocks)
	return blocks
}

func (b *Buffer) getOrCreateSource(name string) *bufferedSource {
	name = normalizeSourceName(name)

	source := b.sources[name]
	if source != nil {
		return source
	}

	source = &bufferedSource{
		state: model.SourceState{Source: name},
	}
	b.sources[name] = source
	return source
}

func (b *Buffer) emit(event model.RuntimeEvent) {
	b.mu.Lock()
	sink := b.liveSink
	b.mu.Unlock()

	if sink == nil {
		return
	}

	if event.TS.IsZero() {
		event.TS = time.Now().UTC()
	}

	sink(event)
}

func (b *Buffer) emitClosedBlock(block model.OutputBlock) {
	b.mu.Lock()
	sink := b.closedSink
	b.mu.Unlock()

	if sink == nil {
		return
	}

	sink(block)
}

func nonZeroTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}

	return value
}

func normalizeSourceName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}

	return value
}

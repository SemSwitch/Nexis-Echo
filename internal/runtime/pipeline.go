package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

const (
	defaultPipelineQueueSize   = 64
	defaultPipelineRecentLimit = 128
)

// PipelineConfig holds the settings needed to build a notification Pipeline.
type PipelineConfig struct {
	Channels []string
}

// Pipeline receives NotificationReady events, keeps a small in-memory ready
// queue for the current runtime slice, and emits runtime events for live
// consumers. Persistence and delivery remain later phases.
type Pipeline struct {
	cfg    PipelineConfig
	logger *slog.Logger

	mu          sync.Mutex
	state       model.ServiceState
	cancel      context.CancelFunc
	queue       chan model.NotificationReady
	done        chan struct{}
	recent      []model.NotificationReady
	runtimeSink func(model.RuntimeEvent)
}

// NewPipeline creates a notification Pipeline wired with primitive configuration.
func NewPipeline(cfg PipelineConfig, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		cfg:    cfg,
		logger: ensureLogger(logger),
		state:  model.StateIdle,
	}
}

func (p *Pipeline) Name() string { return "pipeline" }

func (p *Pipeline) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == model.StateRunning || p.state == model.StateStarting {
		return fmt.Errorf("pipeline already started")
	}

	p.state = model.StateStarting
	runCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.queue = make(chan model.NotificationReady, defaultPipelineQueueSize)
	p.done = make(chan struct{})
	p.state = model.StateRunning
	p.logger.Info("pipeline started", "channels", p.cfg.Channels)

	go p.run(runCtx, p.queue, p.done)
	return nil
}

func (p *Pipeline) Stop(ctx context.Context) error {
	p.mu.Lock()
	if p.state == model.StateIdle || p.state == model.StateStopped {
		p.mu.Unlock()
		return nil
	}

	p.state = model.StateStopping
	cancel := p.cancel
	done := p.done
	p.cancel = nil
	p.queue = nil
	p.done = nil
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	p.mu.Lock()
	p.state = model.StateStopped
	p.mu.Unlock()

	p.logger.Info("pipeline stopped")
	return nil
}

func (p *Pipeline) Health() model.ServiceHealth {
	p.mu.Lock()
	state := p.state
	p.mu.Unlock()

	return model.ServiceHealth{
		Name:      p.Name(),
		State:     state,
		CheckedAt: time.Now().UTC(),
	}
}

// HandleNotificationReady accepts a summarized notification from the
// summarizer and queues it for in-process consumption.
func (p *Pipeline) HandleNotificationReady(ctx context.Context, ready model.NotificationReady) error {
	ready = normalizeNotificationReady(ready)

	p.mu.Lock()
	if p.state != model.StateRunning {
		p.mu.Unlock()
		return fmt.Errorf("pipeline is not running")
	}
	queue := p.queue
	p.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case queue <- ready:
		return nil
	default:
		return fmt.Errorf("pipeline queue is full")
	}
}

// HandleNotification is an alias for callers that prefer the shorter name.
func (p *Pipeline) HandleNotification(ctx context.Context, ready model.NotificationReady) error {
	return p.HandleNotificationReady(ctx, ready)
}

// RegisterRuntimeEventSink allows the app layer to bridge runtime-owned events
// into transports such as WebSockets without coupling runtime to transport.
func (p *Pipeline) RegisterRuntimeEventSink(sink func(model.RuntimeEvent)) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.runtimeSink = sink
}

// ReadyNotifications returns the recent in-memory ready notifications held by
// the pipeline for the current phase.
func (p *Pipeline) ReadyNotifications(context.Context) []model.NotificationReady {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.recent) == 0 {
		return []model.NotificationReady{}
	}

	out := make([]model.NotificationReady, len(p.recent))
	copy(out, p.recent)
	return out
}

func (p *Pipeline) run(ctx context.Context, queue <-chan model.NotificationReady, done chan<- struct{}) {
	defer close(done)

	for {
		select {
		case <-ctx.Done():
			return
		case ready := <-queue:
			p.recordReady(ready)
		}
	}
}

func (p *Pipeline) recordReady(ready model.NotificationReady) {
	p.mu.Lock()
	p.recent = append(p.recent, ready)
	if len(p.recent) > defaultPipelineRecentLimit {
		excess := len(p.recent) - defaultPipelineRecentLimit
		p.recent = append([]model.NotificationReady(nil), p.recent[excess:]...)
	}
	sink := p.runtimeSink
	p.mu.Unlock()

	if sink != nil {
		sink(model.RuntimeEvent{
			Type:   "runtime.notification.accepted",
			Source: ready.Source,
			TS:     ready.TS,
			Data:   ready,
		})
	}
}

func normalizeNotificationReady(ready model.NotificationReady) model.NotificationReady {
	if ready.TS.IsZero() {
		ready.TS = time.Now().UTC()
	}
	if ready.EventID == "" {
		ready.EventID = fmt.Sprintf("%s-%d", normalizeSourceName(ready.Source), ready.TS.UnixNano())
	}
	if ready.Source == "" {
		ready.Source = "default"
	}
	return ready
}

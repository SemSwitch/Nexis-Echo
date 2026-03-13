package runtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

// PipelineConfig holds the settings needed to build a notification Pipeline.
type PipelineConfig struct {
	Channels []string
}

// Pipeline receives NotificationReady events, routes them through configured
// channels, and produces NotificationRecords. Delivery logic is deferred.
type Pipeline struct {
	cfg    PipelineConfig
	logger *slog.Logger

	mu     sync.Mutex
	state  model.ServiceState
	cancel context.CancelFunc
}

// NewPipeline creates a notification Pipeline wired with primitive configuration.
func NewPipeline(cfg PipelineConfig, logger *slog.Logger) *Pipeline {
	return &Pipeline{
		cfg:    cfg,
		logger: logger,
		state:  model.StateIdle,
	}
}

func (p *Pipeline) Name() string { return "pipeline" }

func (p *Pipeline) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.state = model.StateStarting
	_, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.state = model.StateRunning
	p.logger.Info("pipeline started", "channels", p.cfg.Channels)
	return nil
}

func (p *Pipeline) Stop(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.state = model.StateStopping
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.state = model.StateStopped
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

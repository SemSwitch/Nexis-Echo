package runtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

// ConsumerConfig holds the settings needed to build a Consumer.
type ConsumerConfig struct {
	Subjects []string
}

// Consumer ingests raw events from a NATS stream and emits RawOutput values.
type Consumer struct {
	cfg    ConsumerConfig
	logger *slog.Logger

	mu     sync.Mutex
	state  model.ServiceState
	cancel context.CancelFunc
}

// NewConsumer creates a Consumer. The caller supplies primitive configuration;
// wiring to real NATS connectivity is deferred to a later integration pass.
func NewConsumer(cfg ConsumerConfig, logger *slog.Logger) *Consumer {
	return &Consumer{
		cfg:    cfg,
		logger: logger,
		state:  model.StateIdle,
	}
}

func (c *Consumer) Name() string { return "consumer" }

func (c *Consumer) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.state = model.StateStarting
	_, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.state = model.StateRunning
	c.logger.Info("consumer started", "subjects", c.cfg.Subjects)
	return nil
}

func (c *Consumer) Stop(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.state = model.StateStopping
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	c.state = model.StateStopped
	c.logger.Info("consumer stopped")
	return nil
}

func (c *Consumer) Health() model.ServiceHealth {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()

	return model.ServiceHealth{
		Name:      c.Name(),
		State:     state,
		CheckedAt: time.Now().UTC(),
	}
}

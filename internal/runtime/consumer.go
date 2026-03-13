package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
	"github.com/nats-io/nats.go"
)

// ConsumerConfig holds the settings needed to build a NATS-backed consumer.
type ConsumerConfig struct {
	ClientName     string
	URL            string
	Queue          string
	ConnectTimeout time.Duration
	ReconnectWait  time.Duration
	MaxReconnects  int
	Subjects       []string
	RawSubject     string
	ClosedSubject  string
	Sink           EventSink
}

// Consumer ingests raw and closed events from NATS and forwards decoded payloads
// to an event sink.
type Consumer struct {
	cfg    ConsumerConfig
	logger *slog.Logger

	mu     sync.Mutex
	state  model.ServiceState
	cancel context.CancelFunc
	nc     *nats.Conn
	subs   []*nats.Subscription
}

// NewConsumer creates a NATS-backed consumer.
func NewConsumer(cfg ConsumerConfig, logger *slog.Logger) *Consumer {
	return &Consumer{
		cfg:    cfg,
		logger: ensureLogger(logger),
		state:  model.StateIdle,
	}
}

func (c *Consumer) Name() string { return "consumer" }

func (c *Consumer) Start(ctx context.Context) error {
	if c.cfg.Sink == nil {
		return fmt.Errorf("consumer requires an event sink")
	}
	if strings.TrimSpace(c.cfg.URL) == "" {
		return fmt.Errorf("consumer requires a NATS url")
	}

	c.mu.Lock()
	c.state = model.StateStarting
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.mu.Unlock()

	options := []nats.Option{
		nats.Name(c.clientName()),
		nats.Timeout(c.cfg.ConnectTimeout),
		nats.ReconnectWait(c.cfg.ReconnectWait),
		nats.MaxReconnects(c.cfg.MaxReconnects),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			c.logger.Warn("consumer disconnected", "error", err)
			c.setState(model.StateDegraded)
		}),
		nats.ReconnectHandler(func(conn *nats.Conn) {
			c.logger.Info("consumer reconnected", "url", conn.ConnectedUrl())
			c.setState(model.StateRunning)
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			c.logger.Info("consumer connection closed")
		}),
	}

	rawSubject := c.rawSubject()
	closedSubject := c.closedSubject()
	if rawSubject == "" && closedSubject == "" {
		c.setState(model.StateDegraded)
		return fmt.Errorf("consumer requires at least one subject")
	}

	nc, err := nats.Connect(c.cfg.URL, options...)
	if err != nil {
		c.setState(model.StateDegraded)
		return fmt.Errorf("connect nats: %w", err)
	}

	subs := make([]*nats.Subscription, 0, 2)
	if strings.TrimSpace(rawSubject) != "" {
		sub, err := c.subscribe(nc, rawSubject, func(msg *nats.Msg) error {
			return c.handleRaw(runCtx, msg)
		})
		if err != nil {
			nc.Close()
			c.setState(model.StateDegraded)
			return fmt.Errorf("subscribe raw subject: %w", err)
		}
		subs = append(subs, sub)
	}

	if strings.TrimSpace(closedSubject) != "" {
		sub, err := c.subscribe(nc, closedSubject, func(msg *nats.Msg) error {
			return c.handleClosed(runCtx, msg)
		})
		if err != nil {
			for _, sub := range subs {
				_ = sub.Unsubscribe()
			}
			nc.Close()
			c.setState(model.StateDegraded)
			return fmt.Errorf("subscribe closed subject: %w", err)
		}
		subs = append(subs, sub)
	}

	c.mu.Lock()
	c.nc = nc
	c.subs = subs
	c.state = model.StateRunning
	c.mu.Unlock()

	c.logger.Info("consumer started",
		"url", c.cfg.URL,
		"raw_subject", rawSubject,
		"closed_subject", closedSubject,
	)
	return nil
}

func (c *Consumer) Stop(_ context.Context) error {
	c.mu.Lock()
	c.state = model.StateStopping
	cancel := c.cancel
	nc := c.nc
	subs := c.subs
	c.cancel = nil
	c.nc = nil
	c.subs = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, sub := range subs {
		_ = sub.Unsubscribe()
	}
	if nc != nil {
		if err := nc.Drain(); err != nil {
			nc.Close()
		}
	}

	c.setState(model.StateStopped)
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

func (c *Consumer) subscribe(nc *nats.Conn, subject string, handler func(*nats.Msg) error) (*nats.Subscription, error) {
	wrapped := func(msg *nats.Msg) {
		if err := handler(msg); err != nil {
			c.logger.Warn("consumer message handling failed", "subject", msg.Subject, "error", err)
		}
	}

	if strings.TrimSpace(c.cfg.Queue) != "" {
		return nc.QueueSubscribe(subject, c.cfg.Queue, wrapped)
	}

	return nc.Subscribe(subject, wrapped)
}

func (c *Consumer) handleRaw(ctx context.Context, msg *nats.Msg) error {
	var output model.RawOutput
	if err := json.Unmarshal(msg.Data, &output); err != nil {
		return fmt.Errorf("decode raw output: %w", err)
	}
	if output.Source == "" {
		output.Source = sourceFromSubject(msg.Subject)
	}
	if output.Subject == "" {
		output.Subject = msg.Subject
	}
	if output.TS.IsZero() {
		output.TS = time.Now().UTC()
	}

	return c.cfg.Sink.HandleRaw(ctx, output)
}

func (c *Consumer) handleClosed(ctx context.Context, msg *nats.Msg) error {
	var output model.OutputClosed
	if err := json.Unmarshal(msg.Data, &output); err != nil {
		return fmt.Errorf("decode closed output: %w", err)
	}
	if output.Source == "" {
		output.Source = sourceFromSubject(msg.Subject)
	}
	if output.Subject == "" {
		output.Subject = msg.Subject
	}
	if output.TS.IsZero() {
		output.TS = time.Now().UTC()
	}

	return c.cfg.Sink.HandleClosed(ctx, output)
}

func (c *Consumer) clientName() string {
	if strings.TrimSpace(c.cfg.ClientName) != "" {
		return c.cfg.ClientName
	}

	return "nexis-echo"
}

func (c *Consumer) rawSubject() string {
	if strings.TrimSpace(c.cfg.RawSubject) != "" {
		return strings.TrimSpace(c.cfg.RawSubject)
	}
	if len(c.cfg.Subjects) > 0 {
		return strings.TrimSpace(c.cfg.Subjects[0])
	}

	return ""
}

func (c *Consumer) closedSubject() string {
	if strings.TrimSpace(c.cfg.ClosedSubject) != "" {
		return strings.TrimSpace(c.cfg.ClosedSubject)
	}
	if len(c.cfg.Subjects) > 1 {
		return strings.TrimSpace(c.cfg.Subjects[1])
	}

	return ""
}

func (c *Consumer) setState(state model.ServiceState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
}

func sourceFromSubject(subject string) string {
	parts := strings.Split(strings.TrimSpace(subject), ".")
	if len(parts) == 0 {
		return "default"
	}

	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return "default"
	}

	return last
}

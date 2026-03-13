package runtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

// SummarizerConfig holds the settings needed to build a Summarizer.
type SummarizerConfig struct {
	MaxBatchSize int
}

// Summarizer receives OutputClosed events, produces summaries, and emits
// NotificationReady values. The actual summarization logic is deferred.
type Summarizer struct {
	cfg    SummarizerConfig
	logger *slog.Logger

	mu     sync.Mutex
	state  model.ServiceState
	cancel context.CancelFunc
}

// NewSummarizer creates a Summarizer wired with primitive configuration.
func NewSummarizer(cfg SummarizerConfig, logger *slog.Logger) *Summarizer {
	return &Summarizer{
		cfg:    cfg,
		logger: logger,
		state:  model.StateIdle,
	}
}

func (s *Summarizer) Name() string { return "summarizer" }

func (s *Summarizer) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = model.StateStarting
	_, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.state = model.StateRunning
	s.logger.Info("summarizer started", "max_batch", s.cfg.MaxBatchSize)
	return nil
}

func (s *Summarizer) Stop(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = model.StateStopping
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.state = model.StateStopped
	s.logger.Info("summarizer stopped")
	return nil
}

func (s *Summarizer) Health() model.ServiceHealth {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()

	return model.ServiceHealth{
		Name:      s.Name(),
		State:     state,
		CheckedAt: time.Now().UTC(),
	}
}

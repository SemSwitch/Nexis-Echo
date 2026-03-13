package runtime

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

// TTSConfig holds the settings needed to build a TTS worker.
type TTSConfig struct {
	Voice     string
	MaxQueued int
}

// TTS converts NotificationReady or NotificationRecord text into speech.
// The actual synthesis logic is deferred; this skeleton manages lifecycle only.
type TTS struct {
	cfg    TTSConfig
	logger *slog.Logger

	mu     sync.Mutex
	state  model.ServiceState
	cancel context.CancelFunc
}

// NewTTS creates a TTS worker wired with primitive configuration.
func NewTTS(cfg TTSConfig, logger *slog.Logger) *TTS {
	return &TTS{
		cfg:    cfg,
		logger: logger,
		state:  model.StateIdle,
	}
}

func (t *TTS) Name() string { return "tts" }

func (t *TTS) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.state = model.StateStarting
	_, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.state = model.StateRunning
	t.logger.Info("tts started", "voice", t.cfg.Voice, "max_queued", t.cfg.MaxQueued)
	return nil
}

func (t *TTS) Stop(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.state = model.StateStopping
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
	t.state = model.StateStopped
	t.logger.Info("tts stopped")
	return nil
}

func (t *TTS) Health() model.ServiceHealth {
	t.mu.Lock()
	state := t.state
	t.mu.Unlock()

	return model.ServiceHealth{
		Name:      t.Name(),
		State:     state,
		CheckedAt: time.Now().UTC(),
	}
}

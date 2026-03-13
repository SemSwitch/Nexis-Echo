package runtime

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/config"
	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

const (
	defaultSummaryCacheEntries = 256
	defaultSummaryQueueSize    = 64
)

type SummaryProvider interface {
	Name() string
	Summarize(context.Context, model.OutputBlock) (SummaryResult, error)
}

type SummaryResult struct {
	Summary  string
	Excerpt  string
	Severity model.Severity
}

type summaryCacheEntry struct {
	Summary  string
	Excerpt  string
	Severity model.Severity
}

// SummarizerConfig holds the settings needed to build a Summarizer.
type SummarizerConfig = config.SummarizerConfig

// Summarizer receives closed output blocks, produces summaries, and emits
// NotificationReady values through internal runtime sinks.
type Summarizer struct {
	cfg      SummarizerConfig
	logger   *slog.Logger
	provider SummaryProvider

	queue chan model.OutputBlock

	mu             sync.Mutex
	state          model.ServiceState
	cancel         context.CancelFunc
	done           chan struct{}
	lastErr        string
	notifySink     func(model.NotificationReady)
	runtimeSink    func(model.RuntimeEvent)
	cache          map[string]summaryCacheEntry
	cacheOrder     []string
	lastProviderAt time.Time
}

// NewSummarizer creates a Summarizer wired with a provider selected from config.
func NewSummarizer(cfg SummarizerConfig, logger *slog.Logger) *Summarizer {
	return newSummarizer(cfg, logger, providerFromConfig(cfg))
}

func newSummarizer(cfg SummarizerConfig, logger *slog.Logger, provider SummaryProvider) *Summarizer {
	cfg = summarizerConfigWithDefaults(cfg)

	return &Summarizer{
		cfg:      cfg,
		logger:   ensureLogger(logger),
		provider: provider,
		queue:    make(chan model.OutputBlock, cfg.QueueSize),
		state:    model.StateIdle,
		cache:    make(map[string]summaryCacheEntry, cfg.CacheEntries),
	}
}

func (s *Summarizer) Name() string { return "summarizer" }

func (s *Summarizer) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.state == model.StateRunning || s.state == model.StateStarting {
		s.mu.Unlock()
		return fmt.Errorf("summarizer already started")
	}

	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	s.state = model.StateRunning
	s.lastErr = ""
	done := s.done
	s.mu.Unlock()

	go s.run(runCtx, done)
	s.logger.Info("summarizer started",
		"provider", s.provider.Name(),
		"queue_size", s.cfg.QueueSize,
		"cache_entries", s.cfg.CacheEntries,
	)
	return nil
}

func (s *Summarizer) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.state == model.StateStopped || s.state == model.StateIdle {
		s.state = model.StateStopped
		s.mu.Unlock()
		return nil
	}

	s.state = model.StateStopping
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()

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

	s.mu.Lock()
	s.state = model.StateStopped
	s.mu.Unlock()
	s.logger.Info("summarizer stopped")
	return nil
}

func (s *Summarizer) Health() model.ServiceHealth {
	s.mu.Lock()
	state := s.state
	message := s.lastErr
	s.mu.Unlock()

	return model.ServiceHealth{
		Name:      s.Name(),
		State:     state,
		Message:   message,
		CheckedAt: time.Now().UTC(),
	}
}

func (s *Summarizer) HandleClosedBlock(block model.OutputBlock) {
	s.mu.Lock()
	queue := s.queue
	state := s.state
	s.mu.Unlock()

	if queue == nil || (state != model.StateIdle && state != model.StateStarting && state != model.StateRunning) {
		return
	}

	select {
	case queue <- block:
	default:
		s.logger.Warn("summarizer queue full; dropping closed block", "source", block.Source, "block_id", block.ID)
		s.emitRuntimeEvent(model.RuntimeEvent{
			Type:   "runtime.summarizer.dropped",
			Source: block.Source,
			TS:     nonZeroTime(block.ClosedAt),
			Data: map[string]any{
				"block_id": block.ID,
				"source":   block.Source,
			},
		})
	}
}

func (s *Summarizer) RegisterNotificationSink(sink func(model.NotificationReady)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.notifySink = sink
}

func (s *Summarizer) RegisterRuntimeEventSink(sink func(model.RuntimeEvent)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.runtimeSink = sink
}

func (s *Summarizer) run(ctx context.Context, done chan struct{}) {
	defer close(done)

	for {
		select {
		case <-ctx.Done():
			return
		case block := <-s.queue:
			if err := s.processBlock(ctx, block); err != nil {
				s.logger.Warn("summarizer processing failed", "source", block.Source, "block_id", block.ID, "error", err)
				s.setErrorState(err)
				s.emitRuntimeEvent(model.RuntimeEvent{
					Type:   "runtime.summarizer.failed",
					Source: block.Source,
					TS:     time.Now().UTC(),
					Data: map[string]any{
						"block_id": block.ID,
						"error":    err.Error(),
						"source":   block.Source,
					},
				})
			}
		}
	}
}

func (s *Summarizer) processBlock(ctx context.Context, block model.OutputBlock) error {
	cached, ok := s.lookupCache(block)
	if !ok {
		if err := s.waitForRateLimit(ctx); err != nil {
			return err
		}

		result, err := s.provider.Summarize(ctx, block)
		if err != nil {
			return err
		}

		cached = summaryCacheEntry{
			Summary:  strings.TrimSpace(result.Summary),
			Excerpt:  strings.TrimSpace(result.Excerpt),
			Severity: result.Severity,
		}
		s.storeCache(block, cached)
		s.recordProviderCall()
	}

	ready := model.NotificationReady{
		EventID:  block.ID,
		Source:   block.Source,
		Summary:  cached.Summary,
		Excerpt:  cached.Excerpt,
		Severity: cached.Severity,
		TS:       nonZeroTime(block.ClosedAt),
	}

	s.emitNotification(ready)
	s.emitRuntimeEvent(model.RuntimeEvent{
		Type:   "runtime.notification.ready",
		Source: block.Source,
		TS:     ready.TS,
		Data:   ready,
	})
	s.clearErrorState()
	return nil
}

func (s *Summarizer) waitForRateLimit(ctx context.Context) error {
	s.mu.Lock()
	lastProviderAt := s.lastProviderAt
	minInterval := s.cfg.MinInterval.Std()
	s.mu.Unlock()

	if minInterval <= 0 || lastProviderAt.IsZero() {
		return nil
	}

	wait := minInterval - time.Since(lastProviderAt)
	if wait <= 0 {
		return nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Summarizer) recordProviderCall() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastProviderAt = time.Now().UTC()
}

func (s *Summarizer) lookupCache(block model.OutputBlock) (summaryCacheEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.cache[summaryCacheKey(block)]
	return entry, ok
}

func (s *Summarizer) storeCache(block model.OutputBlock, entry summaryCacheEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := summaryCacheKey(block)
	if _, exists := s.cache[key]; !exists {
		s.cacheOrder = append(s.cacheOrder, key)
	}
	s.cache[key] = entry

	for len(s.cacheOrder) > s.cfg.CacheEntries {
		evict := s.cacheOrder[0]
		s.cacheOrder = s.cacheOrder[1:]
		delete(s.cache, evict)
	}
}

func (s *Summarizer) emitNotification(ready model.NotificationReady) {
	s.mu.Lock()
	sink := s.notifySink
	s.mu.Unlock()

	if sink == nil {
		return
	}

	sink(ready)
}

func (s *Summarizer) emitRuntimeEvent(event model.RuntimeEvent) {
	s.mu.Lock()
	sink := s.runtimeSink
	s.mu.Unlock()

	if sink == nil {
		return
	}

	if event.TS.IsZero() {
		event.TS = time.Now().UTC()
	}

	sink(event)
}

func (s *Summarizer) setErrorState(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = model.StateDegraded
	s.lastErr = err.Error()
}

func (s *Summarizer) clearErrorState() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = model.StateRunning
	s.lastErr = ""
}

func summarizerConfigWithDefaults(cfg SummarizerConfig) SummarizerConfig {
	if cfg.Provider == "" {
		cfg.Provider = "heuristic"
	}
	if cfg.CacheEntries <= 0 {
		cfg.CacheEntries = defaultSummaryCacheEntries
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultSummaryQueueSize
	}
	if cfg.MaxBatchSize <= 0 {
		cfg.MaxBatchSize = 32
	}

	return cfg
}

func summaryCacheKey(block model.OutputBlock) string {
	sum := sha1.Sum([]byte(strings.Join([]string{
		block.Source,
		block.Subject,
		block.Content,
		block.Status,
		fmt.Sprintf("%d", block.DurationMS),
		fmt.Sprintf("%t", block.Final),
	}, "\x00")))

	return hex.EncodeToString(sum[:])
}

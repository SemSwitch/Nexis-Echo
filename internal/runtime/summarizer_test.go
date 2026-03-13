package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/config"
	"github.com/SemSwitch/Nexis-Echo/internal/model"
)

func TestSummarizerProducesNotificationReadyAndUsesCache(t *testing.T) {
	provider := &recordingSummaryProvider{
		result: SummaryResult{
			Summary:  "Builder completed successfully.",
			Excerpt:  "Build finished with 0 errors.",
			Severity: model.SeveritySuccess,
		},
	}
	summarizer := newSummarizer(config.SummarizerConfig{
		Provider:     "heuristic",
		MaxBatchSize: 8,
		CacheEntries: 8,
		QueueSize:    4,
	}, nil, provider)

	readyCh := make(chan model.NotificationReady, 2)
	runtimeCh := make(chan model.RuntimeEvent, 2)
	summarizer.RegisterNotificationSink(func(ready model.NotificationReady) {
		readyCh <- ready
	})
	summarizer.RegisterRuntimeEventSink(func(event model.RuntimeEvent) {
		runtimeCh <- event
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := summarizer.Start(ctx); err != nil {
		t.Fatalf("start summarizer: %v", err)
	}
	defer func() {
		_ = summarizer.Stop(context.Background())
	}()

	firstBlock := model.OutputBlock{
		ID:       "builder-1",
		Source:   "builder",
		Content:  "running build\nBuild finished with 0 errors.\n",
		Status:   "success",
		ClosedAt: time.Date(2026, time.March, 13, 1, 0, 0, 0, time.UTC),
	}
	secondBlock := firstBlock
	secondBlock.ID = "builder-2"
	secondBlock.ClosedAt = firstBlock.ClosedAt.Add(time.Second)

	summarizer.HandleClosedBlock(firstBlock)
	summarizer.HandleClosedBlock(secondBlock)

	firstReady := waitForNotificationReady(t, readyCh)
	secondReady := waitForNotificationReady(t, readyCh)
	_ = waitForRuntimeEvent(t, runtimeCh)
	_ = waitForRuntimeEvent(t, runtimeCh)

	if provider.CallCount() != 1 {
		t.Fatalf("expected provider to be called once due to cache hit, got %d", provider.CallCount())
	}

	if firstReady.EventID != "builder-1" || secondReady.EventID != "builder-2" {
		t.Fatalf("expected unique event ids, got %q and %q", firstReady.EventID, secondReady.EventID)
	}
	if firstReady.Summary != "Builder completed successfully." {
		t.Fatalf("unexpected first summary: %+v", firstReady)
	}
	if secondReady.Excerpt != "Build finished with 0 errors." {
		t.Fatalf("unexpected second excerpt: %+v", secondReady)
	}
}

func TestSummarizerRespectsRateLimit(t *testing.T) {
	provider := &recordingSummaryProvider{
		result: SummaryResult{
			Summary:  "Builder finished.",
			Excerpt:  "Done.",
			Severity: model.SeverityInfo,
		},
	}
	summarizer := newSummarizer(config.SummarizerConfig{
		Provider:     "heuristic",
		MaxBatchSize: 8,
		CacheEntries: 8,
		QueueSize:    4,
		MinInterval:  config.Duration(40 * time.Millisecond),
	}, nil, provider)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := summarizer.Start(ctx); err != nil {
		t.Fatalf("start summarizer: %v", err)
	}
	defer func() {
		_ = summarizer.Stop(context.Background())
	}()

	base := time.Date(2026, time.March, 13, 1, 5, 0, 0, time.UTC)
	summarizer.HandleClosedBlock(model.OutputBlock{
		ID:       "builder-1",
		Source:   "builder",
		Content:  "step 1 complete\n",
		Status:   "success",
		ClosedAt: base,
	})
	summarizer.HandleClosedBlock(model.OutputBlock{
		ID:       "builder-2",
		Source:   "builder",
		Content:  "step 2 complete\n",
		Status:   "success",
		ClosedAt: base.Add(time.Second),
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if provider.CallCount() >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	timestamps := provider.CallTimes()
	if len(timestamps) != 2 {
		t.Fatalf("expected 2 provider calls, got %d", len(timestamps))
	}

	if gap := timestamps[1].Sub(timestamps[0]); gap < 35*time.Millisecond {
		t.Fatalf("expected provider calls to respect rate limit, got gap %v", gap)
	}
}

func TestHeuristicSummaryProviderClassifiesFailure(t *testing.T) {
	provider := heuristicSummaryProvider{maxLines: 8}
	result, err := provider.Summarize(context.Background(), model.OutputBlock{
		Source:  "builder",
		Status:  "failed",
		Content: "step 1\nERROR: tests failed on package api\n",
	})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}

	if result.Severity != model.SeverityError {
		t.Fatalf("expected error severity, got %s", result.Severity.String())
	}
	if result.Summary != "Builder failed." {
		t.Fatalf("unexpected summary %q", result.Summary)
	}
	if result.Excerpt != "ERROR: tests failed on package api" {
		t.Fatalf("unexpected excerpt %q", result.Excerpt)
	}
}

type recordingSummaryProvider struct {
	mu     sync.Mutex
	result SummaryResult
	err    error
	calls  int
	times  []time.Time
}

func (p *recordingSummaryProvider) Name() string {
	return "recording"
}

func (p *recordingSummaryProvider) Summarize(context.Context, model.OutputBlock) (SummaryResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.calls++
	p.times = append(p.times, time.Now())
	return p.result, p.err
}

func (p *recordingSummaryProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.calls
}

func (p *recordingSummaryProvider) CallTimes() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()

	times := make([]time.Time, len(p.times))
	copy(times, p.times)
	return times
}

func waitForNotificationReady(t *testing.T, ch <-chan model.NotificationReady) model.NotificationReady {
	t.Helper()

	select {
	case ready := <-ch:
		return ready
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for notification ready")
		return model.NotificationReady{}
	}
}

func waitForRuntimeEvent(t *testing.T, ch <-chan model.RuntimeEvent) model.RuntimeEvent {
	t.Helper()

	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for runtime event")
		return model.RuntimeEvent{}
	}
}

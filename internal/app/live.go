package app

import (
	"context"
	"sync"

	"github.com/SemSwitch/Nexis-Echo/internal/server"
)

const liveBufferSize = 8

// liveBroker provides a minimal in-memory fanout for runtime events. It keeps
// the latest event so newly connected WebSocket clients receive current state
// without waiting for another runtime transition.
type liveBroker struct {
	mu     sync.Mutex
	subs   map[chan server.LiveEvent]struct{}
	latest *server.LiveEvent
}

func newLiveBroker() *liveBroker {
	return &liveBroker{
		subs: make(map[chan server.LiveEvent]struct{}),
	}
}

func (b *liveBroker) Subscribe(ctx context.Context) (<-chan server.LiveEvent, error) {
	ch := make(chan server.LiveEvent, liveBufferSize)

	b.mu.Lock()
	b.subs[ch] = struct{}{}
	if b.latest != nil {
		ch <- *b.latest
	}
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
		b.remove(ch)
	}()

	return ch, nil
}

func (b *liveBroker) Publish(event server.LiveEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	eventCopy := event
	b.latest = &eventCopy
	for ch := range b.subs {
		select {
		case ch <- eventCopy:
		default:
		}
	}
}

func (b *liveBroker) remove(ch chan server.LiveEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.subs[ch]; !ok {
		return
	}

	delete(b.subs, ch)
	close(ch)
}

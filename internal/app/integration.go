package app

import (
	"context"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/model"
	"github.com/SemSwitch/Nexis-Echo/internal/runtime"
	"github.com/SemSwitch/Nexis-Echo/internal/server"
)

// sourceStateProvider is implemented by runtime services that can expose a
// current read model of active or recently seen sources.
type sourceStateProvider interface {
	SourceStates(context.Context) []model.SourceState
}

// liveSinkRegistrar is implemented by runtime services that want to publish
// transport-neutral live events through the app-owned broker.
type liveSinkRegistrar interface {
	RegisterLiveSink(func(string, interface{}))
}

type runtimeEventSinkRegistrar interface {
	RegisterRuntimeEventSink(func(model.RuntimeEvent))
}

type runtimeEventLiveSinkRegistrar interface {
	RegisterLiveSink(func(model.RuntimeEvent))
}

func (a *App) bindRuntimeService(svc runtime.Service) {
	switch registrar := any(svc).(type) {
	case runtimeEventSinkRegistrar:
		registrar.RegisterRuntimeEventSink(a.publishModelRuntimeEvent)
	case runtimeEventLiveSinkRegistrar:
		registrar.RegisterLiveSink(a.publishModelRuntimeEvent)
	case liveSinkRegistrar:
		registrar.RegisterLiveSink(a.publishRuntimeEvent)
	}

	if provider, ok := svc.(sourceStateProvider); ok {
		a.sources = provider
	}
}

func (a *App) sourcesProvider() server.SourcesProvider {
	if a.sources == nil {
		return nil
	}

	return sourceReadModel{provider: a.sources}
}

type sourceReadModel struct {
	provider sourceStateProvider
}

func (r sourceReadModel) Sources(ctx context.Context) []server.SourceSnapshot {
	sourceStates := r.provider.SourceStates(ctx)
	if len(sourceStates) == 0 {
		return []server.SourceSnapshot{}
	}

	snapshots := make([]server.SourceSnapshot, 0, len(sourceStates))
	for _, state := range sourceStates {
		snapshots = append(snapshots, server.SourceSnapshot{
			Source:        state.Source,
			Subject:       state.Subject,
			LastSeenAt:    state.LastSeenAt,
			LastClosedAt:  state.LastClosedAt,
			LastStatus:    state.LastStatus,
			EventCount:    state.EventCount,
			BufferedBytes: state.BufferedBytes,
			ClosedCount:   state.ClosedCount,
			Active:        state.Active,
		})
	}

	return snapshots
}

func (a *App) publishModelRuntimeEvent(event model.RuntimeEvent) {
	if a.live == nil {
		return
	}

	data := event.Data
	if data == nil && event.Source != "" {
		data = map[string]any{
			"source": event.Source,
		}
	}

	timestamp := event.TS
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	a.live.Publish(server.LiveEvent{
		Type:      event.Type,
		Timestamp: timestamp,
		Data:      data,
	})
}

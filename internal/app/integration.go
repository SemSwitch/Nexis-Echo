package app

import (
	"context"
	"io"
	"log/slog"
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

type outputBlockSinkRegistrar interface {
	RegisterClosedBlockSink(func(model.OutputBlock))
}

type outputBlockHandler interface {
	HandleOutputBlock(context.Context, model.OutputBlock) error
}

type closedBlockHandler interface {
	HandleClosedBlock(context.Context, model.OutputBlock) error
}

type closedBlockHandlerSimple interface {
	HandleClosedBlock(model.OutputBlock)
}

type notificationReadySinkRegistrar interface {
	RegisterNotificationSink(func(model.NotificationReady))
}

type notificationReadyHandler interface {
	HandleNotificationReady(context.Context, model.NotificationReady) error
}

type notificationHandler interface {
	HandleNotification(context.Context, model.NotificationReady) error
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

func wireClosedBlockFlow(logger *slog.Logger, producer any, consumer any) bool {
	registrar, ok := producer.(outputBlockSinkRegistrar)
	if !ok {
		return false
	}

	handler, ok := closedBlockConsumer(consumer)
	if !ok {
		return false
	}

	log := ensureAppLogger(logger)
	registrar.RegisterClosedBlockSink(func(block model.OutputBlock) {
		if err := handler(context.Background(), block); err != nil {
			log.Warn("closed block delivery failed", "source", block.Source, "error", err)
		}
	})
	return true
}

func wireNotificationFlow(logger *slog.Logger, producer any, consumer any) bool {
	registrar, ok := producer.(notificationReadySinkRegistrar)
	if !ok {
		return false
	}

	handler, ok := notificationReadyConsumer(consumer)
	if !ok {
		return false
	}

	log := ensureAppLogger(logger)
	registrar.RegisterNotificationSink(func(notification model.NotificationReady) {
		if err := handler(context.Background(), notification); err != nil {
			log.Warn("notification-ready delivery failed", "source", notification.Source, "error", err)
		}
	})
	return true
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

func closedBlockConsumer(consumer any) (func(context.Context, model.OutputBlock) error, bool) {
	switch sink := consumer.(type) {
	case outputBlockHandler:
		return sink.HandleOutputBlock, true
	case closedBlockHandler:
		return sink.HandleClosedBlock, true
	case closedBlockHandlerSimple:
		return func(_ context.Context, block model.OutputBlock) error {
			sink.HandleClosedBlock(block)
			return nil
		}, true
	default:
		return nil, false
	}
}

func notificationReadyConsumer(consumer any) (func(context.Context, model.NotificationReady) error, bool) {
	switch sink := consumer.(type) {
	case notificationReadyHandler:
		return sink.HandleNotificationReady, true
	case notificationHandler:
		return sink.HandleNotification, true
	default:
		return nil, false
	}
}

func ensureAppLogger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}

	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

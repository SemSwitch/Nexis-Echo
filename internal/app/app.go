package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SemSwitch/Nexis-Echo/internal/config"
	"github.com/SemSwitch/Nexis-Echo/internal/model"
	"github.com/SemSwitch/Nexis-Echo/internal/runtime"
	"github.com/SemSwitch/Nexis-Echo/internal/server"
)

// App wires configuration, runtime services, and the HTTP server into a
// single lifecycle. Services are started before the HTTP server and stopped
// after it shuts down, in reverse registration order.
type App struct {
	cfg      config.Config
	logger   *slog.Logger
	services []runtime.Service
	health   *healthAggregator
	live     *liveBroker
	sources  sourceStateProvider
	srv      *server.Server
}

// New creates a ready-to-run application with the default runtime services
// registered. Call RegisterService to append additional runtime components.
func New(cfg config.Config, logger *slog.Logger) *App {
	app := &App{
		cfg:    cfg,
		logger: logger,
		live:   newLiveBroker(),
	}

	app.services = defaultServices(cfg, logger)
	app.health = &healthAggregator{services: app.services}
	for _, svc := range app.services {
		app.bindRuntimeService(svc)
	}
	return app
}

func defaultServices(cfg config.Config, logger *slog.Logger) []runtime.Service {
	subjectRoot := strings.Trim(cfg.NATS.SubjectRoot, ". ")
	subjects := []string{
		subjectRoot + ".output.raw.*",
		subjectRoot + ".output.closed.*",
	}

	buffer := runtime.NewBuffer(runtime.BufferConfig{}, logger)
	summarizer := runtime.NewSummarizer(runtime.SummarizerConfig{
		MaxBatchSize: cfg.Summarizer.MaxBatchSize,
		Provider:     cfg.Summarizer.Provider,
		CacheEntries: cfg.Summarizer.CacheEntries,
		QueueSize:    cfg.Summarizer.QueueSize,
		MinInterval:  cfg.Summarizer.MinInterval,
	}, logger)
	pipeline := runtime.NewPipeline(runtime.PipelineConfig{
		Channels: cfg.Pipeline.Channels,
	}, logger)
	tts := runtime.NewTTS(runtime.TTSConfig{
		Voice:     cfg.TTS.Voice,
		MaxQueued: cfg.TTS.MaxQueued,
	}, logger)

	log := ensureAppLogger(logger)
	if !wireClosedBlockFlow(log, buffer, summarizer) {
		log.Error("failed to wire closed block flow", "producer", buffer.Name(), "consumer", summarizer.Name())
	}
	if !wireNotificationFlow(log, summarizer, pipeline) {
		log.Error("failed to wire notification flow", "producer", summarizer.Name(), "consumer", pipeline.Name())
	}

	return []runtime.Service{
		buffer,
		summarizer,
		pipeline,
		tts,
		runtime.NewConsumer(runtime.ConsumerConfig{
			ClientName:     "nexis-echo",
			URL:            cfg.NATS.URL,
			Queue:          cfg.NATS.Queue,
			ConnectTimeout: cfg.NATS.ConnectTimeout.Std(),
			ReconnectWait:  cfg.NATS.ReconnectWait.Std(),
			MaxReconnects:  cfg.NATS.MaxReconnects,
			RawSubject:     subjects[0],
			ClosedSubject:  subjects[1],
			Sink:           buffer,
		}, logger),
	}
}

// RegisterService adds a runtime service to the application lifecycle.
// Services are started in registration order and stopped in reverse order.
func (a *App) RegisterService(svc runtime.Service) {
	a.services = append(a.services, svc)
	if a.health != nil {
		a.health.services = a.services
	}
	a.bindRuntimeService(svc)
}

// Run starts all registered services and the HTTP server, then blocks until
// ctx is cancelled. On cancellation it performs an orderly shutdown.
func (a *App) Run(ctx context.Context) error {
	if err := runtime.StartAll(ctx, a.logger, a.services); err != nil {
		return err
	}

	a.srv = server.New(server.Config{
		Addr:              a.cfg.HTTP.Addr,
		WebSocketPath:     a.cfg.HTTP.WebSocketPath,
		ReadHeaderTimeout: a.cfg.HTTP.ReadTimeout.Std(),
		IdleTimeout:       a.cfg.HTTP.IdleTimeout.Std(),
		WriteTimeout:      a.cfg.HTTP.WriteTimeout.Std(),
	}, server.Dependencies{
		Logger:  a.logger,
		Health:  a.health,
		Live:    a.live,
		Sources: a.sourcesProvider(),
	})
	a.publishRuntimeSnapshot()

	serverErr := make(chan error, 1)
	go func() {
		a.logger.Info("http server starting", "addr", a.cfg.HTTP.Addr)
		serverErr <- a.srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
	}

	return a.shutdown()
}

func (a *App) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.HTTP.ShutdownTimeout.Std())
	defer cancel()

	var firstErr error

	a.publishRuntimeEvent("runtime.stopping", a.health.Health(context.Background()))

	if a.srv != nil {
		a.logger.Info("shutting down http server")
		if err := a.srv.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("http server shutdown: %w", err)
		}
	}

	if err := runtime.StopAll(shutdownCtx, a.logger, a.services); err != nil {
		if firstErr == nil {
			firstErr = err
		}
	}

	if firstErr == nil {
		a.logger.Info("shutdown complete")
	}

	return firstErr
}

func (a *App) publishRuntimeSnapshot() {
	if a.health == nil {
		return
	}

	a.publishRuntimeEvent("runtime.snapshot", a.health.Health(context.Background()))
}

func (a *App) publishRuntimeEvent(eventType string, data interface{}) {
	if a.live == nil {
		return
	}

	a.live.Publish(server.LiveEvent{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      data,
	})
}

// healthAggregator implements server.HealthProvider by querying all
// registered services.
type healthAggregator struct {
	services []runtime.Service
}

func (h *healthAggregator) Health(_ context.Context) server.HealthReport {
	components := make(map[string]string, len(h.services)+1)
	components["server"] = "ok"

	overall := "ok"
	for _, svc := range h.services {
		sh := svc.Health()
		components[sh.Name] = sh.State.String()
		if sh.State != model.StateRunning && sh.State != model.StateIdle {
			overall = "degraded"
		}
	}

	return server.HealthReport{
		Status:     overall,
		Timestamp:  time.Now().UTC(),
		Components: components,
	}
}

package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	defaultAddr              = ":8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultWriteTimeout      = 10 * time.Second
	defaultPingInterval      = 25 * time.Second
	defaultWebSocketPath     = "/ws"
)

// Config controls the HTTP and WebSocket transport settings.
type Config struct {
	Addr              string
	WebSocketPath     string
	ReadHeaderTimeout time.Duration
	IdleTimeout       time.Duration
	WriteTimeout      time.Duration
	PingInterval      time.Duration
}

// HealthReport is the JSON shape returned from the health endpoint.
type HealthReport struct {
	Status     string            `json:"status"`
	Timestamp  time.Time         `json:"timestamp"`
	Components map[string]string `json:"components,omitempty"`
}

// HealthProvider supplies runtime health state to the HTTP layer.
type HealthProvider interface {
	Health(context.Context) HealthReport
}

// LiveEvent is the transport-neutral shape pushed over the live socket.
type LiveEvent struct {
	Type      string      `json:"type"`
	Timestamp time.Time   `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
}

// LiveSource supplies a stream of runtime events for each live client.
type LiveSource interface {
	Subscribe(context.Context) (<-chan LiveEvent, error)
}

// Dependencies keeps the server package decoupled from runtime concerns.
type Dependencies struct {
	Logger *slog.Logger
	Health HealthProvider
	Live   LiveSource
}

// Server exposes minimal HTTP and WebSocket transport for Nexis Echo.
type Server struct {
	cfg        Config
	logger     *slog.Logger
	health     HealthProvider
	live       LiveSource
	mux        *http.ServeMux
	httpServer *http.Server
}

// New builds a ready-to-start transport server with sensible defaults.
func New(cfg Config, deps Dependencies) *Server {
	cfg = cfg.withDefaults()

	srv := &Server{
		cfg:    cfg,
		logger: deps.logger(),
		health: deps.Health,
		live:   deps.Live,
		mux:    http.NewServeMux(),
	}

	srv.registerRoutes()
	srv.httpServer = &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	return srv
}

// Handler exposes the HTTP handler for integration tests or embedding.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// ListenAndServe starts the configured HTTP server.
func (s *Server) ListenAndServe() error {
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}

// Serve starts serving on a caller-provided listener.
func (s *Server) Serve(listener net.Listener) error {
	err := s.httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (c Config) withDefaults() Config {
	if c.Addr == "" {
		c.Addr = defaultAddr
	}
	if c.ReadHeaderTimeout <= 0 {
		c.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = defaultIdleTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = defaultWriteTimeout
	}
	if c.PingInterval <= 0 {
		c.PingInterval = defaultPingInterval
	}
	if c.WebSocketPath == "" {
		c.WebSocketPath = defaultWebSocketPath
	}

	return c
}

func (d Dependencies) logger() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}

	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

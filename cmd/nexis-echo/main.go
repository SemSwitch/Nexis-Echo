package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/SemSwitch/Nexis-Echo/internal/app"
	"github.com/SemSwitch/Nexis-Echo/internal/config"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	var configPath string
	var showVersion bool

	flag.StringVar(&configPath, "config", "", "optional path to a JSON config file")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return 0
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nexis-echo: load config: %v\n", err)
		return 1
	}

	logger := newLogger(cfg.Log)

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM,
	)
	defer stop()

	application := app.New(cfg, logger)

	if err := application.Run(ctx); err != nil {
		logger.Error("application exited with error", "error", err)
		return 1
	}

	return 0
}

func newLogger(cfg config.LogConfig) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch cfg.Format {
	case "json":
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}

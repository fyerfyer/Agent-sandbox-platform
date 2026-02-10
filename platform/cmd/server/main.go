package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"platform/internal/config"
	"platform/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg := config.Load()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	deps, err := server.InitDeps(ctx, cfg, logger)
	if err != nil {
		logger.Error("Failed to initialise dependencies", "error", err)
		os.Exit(1)
	}
	defer deps.Close()

	srv := server.NewServer(cfg, deps)
	if err := srv.Start(ctx); err != nil {
		logger.Error("Server error", "error", err)
		os.Exit(1)
	}
}

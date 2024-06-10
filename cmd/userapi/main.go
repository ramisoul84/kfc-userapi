package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/ramisoul84/kfc-userapi/internal/app"
	"github.com/ramisoul84/kfc-userapi/internal/config"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// Create application
	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("failed to create application: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Run transports until ctx is cancelled or a transport fails.
	if err := application.Start(ctx); err != nil {
		log.Printf("application error: %v", err)
	}

	// Give shutdown its own timeout, independent of the signal ctx.
	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		cfg.HTTP.ShutdownTimeout,
	)
	defer cancel()

	if err := application.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
}

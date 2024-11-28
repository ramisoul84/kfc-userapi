package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ramisoul84/kfc-userapi/internal/config"
	httpTransport "github.com/ramisoul84/kfc-userapi/internal/transport/http"
	"github.com/ramisoul84/kfc-userapi/pkg/cache"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// App wires together all application components.
type App struct {
	config *config.Config
	logger *logger.Logger
	server *httpTransport.Server
	redis  *cache.Redis
}

// New creates and wires the application.
func New(cfg *config.Config) (*App, error) {
	log := logger.New(&logger.Config{
		Level:    cfg.Logger.Level,
		Format:   cfg.Logger.Format,
		Output:   cfg.Logger.Output,
		FilePath: cfg.Logger.FilePath,
		Service:  cfg.Logger.Service,
	})

	log.Info("starting application",
		"name", cfg.App.Name,
		"version", cfg.App.Version,
		"environment", cfg.App.Environment,
	)

	// Redis
	redisClient, err := cache.NewRedis(&cfg.Redis)
	if err != nil {
		return nil, fmt.Errorf("redis: %w", err)
	}
	log.Info("redis connected", "host", cfg.Redis.Host, "port", cfg.Redis.Port)

	// HTTP server
	server := httpTransport.NewServer(cfg, log)

	return &App{
		config: cfg,
		logger: log,
		server: server,
		redis:  redisClient,
	}, nil
}

// Start begins serving HTTP. Blocks until the context is cancelled or a
// transport fails.
func (a *App) Start(ctx context.Context) error {
	a.logger.Info("starting transports")

	// Buffered so the goroutine never blocks on send if we've already
	// returned via ctx.Done().
	errCh := make(chan error, 1)

	go func() {
		if err := a.server.Start(); err != nil {
			errCh <- fmt.Errorf("http: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		a.logger.Info("shutdown signal received")
		return nil
	case err := <-errCh:
		a.logger.Error("transport failed", "error", err)
		return err
	}
}

// Shutdown gracefully stops all transports and releases resources.
func (a *App) Shutdown(ctx context.Context) error {
	a.logger.Info("shutting down application")

	var errs []error

	if err := a.server.Shutdown(ctx); err != nil {
		a.logger.Error("http shutdown failed", "error", err)
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}

	// Always release the Redis client, even if HTTP shutdown failed.
	if a.redis != nil {
		if err := a.redis.Close(); err != nil {
			a.logger.Error("redis close failed", "error", err)
			errs = append(errs, fmt.Errorf("redis close: %w", err))
		}
	}

	a.logger.Info("shutdown complete")
	return errors.Join(errs...)
}

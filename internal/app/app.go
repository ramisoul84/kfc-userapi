package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ramisoul84/kfc-userapi/internal/config"
	"github.com/ramisoul84/kfc-userapi/internal/repository"
	"github.com/ramisoul84/kfc-userapi/internal/service"
	grpcTransport "github.com/ramisoul84/kfc-userapi/internal/transport/grpc"
	httpTransport "github.com/ramisoul84/kfc-userapi/internal/transport/http"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/handler"
	"github.com/ramisoul84/kfc-userapi/pkg/cache"
	"github.com/ramisoul84/kfc-userapi/pkg/jwt"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// App wires together all application components.
type App struct {
	config     *config.Config
	logger     *logger.Logger
	httpServer *httpTransport.Server
	grpcServer *grpcTransport.Transport
	redis      *cache.Redis
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

	// Repos
	pairingRepo := repository.NewPairingRepository(redisClient)
	revocationRepo := repository.NewRevocationRepository(redisClient)

	// Token manager
	deviceTokens := jwt.NewDeviceTokenManager(
		cfg.JWT.DeviceSecret,
		cfg.JWT.Issuer,
		cfg.JWT.DeviceTTL,
	)

	// Services
	pairingSvc := service.NewPairingService(pairingRepo, log)
	deviceAuthSvc := service.NewDeviceAuthService(pairingRepo, revocationRepo, deviceTokens, log)

	// Handlers
	deviceAuthHandler := handler.NewDeviceAuthHandler(deviceAuthSvc)

	// HTTP transport
	httpServer := httpTransport.NewServer(cfg, log, deviceAuthHandler, deviceTokens, revocationRepo)

	// gRPC transport
	grpcSrv := grpcTransport.NewTransport(cfg, log, pairingSvc, deviceAuthSvc, deviceTokens)

	return &App{
		config:     cfg,
		logger:     log,
		httpServer: httpServer,
		grpcServer: grpcSrv,
		redis:      redisClient,
	}, nil
}

// Start begins serving HTTP. Blocks until the context is cancelled or a
// transport fails.
func (a *App) Start(ctx context.Context) error {
	a.logger.Info("starting transports")

	errCh := make(chan error, 2)

	go func() {
		if err := a.httpServer.Start(); err != nil {
			errCh <- fmt.Errorf("http: %w", err)
		}
	}()
	go func() {
		if err := a.grpcServer.Start(); err != nil {
			errCh <- fmt.Errorf("grpc: %w", err)
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

	if err := a.httpServer.Shutdown(ctx); err != nil {
		a.logger.Error("http shutdown failed", "error", err)
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if err := a.grpcServer.Shutdown(ctx); err != nil {
		a.logger.Error("grpc shutdown failed", "error", err)
		errs = append(errs, fmt.Errorf("grpc shutdown: %w", err))
	}
	if a.redis != nil {
		if err := a.redis.Close(); err != nil {
			a.logger.Error("redis close failed", "error", err)
			errs = append(errs, fmt.Errorf("redis close: %w", err))
		}
	}

	a.logger.Info("shutdown complete")
	return errors.Join(errs...)
}

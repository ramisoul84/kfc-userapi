package app

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ramisoul84/kfc-userapi/internal/client"
	"github.com/ramisoul84/kfc-userapi/internal/config"
	"github.com/ramisoul84/kfc-userapi/internal/consumer"
	"github.com/ramisoul84/kfc-userapi/internal/repository"
	"github.com/ramisoul84/kfc-userapi/internal/service"
	"github.com/ramisoul84/kfc-userapi/internal/sse"
	grpcTransport "github.com/ramisoul84/kfc-userapi/internal/transport/grpc"
	httpTransport "github.com/ramisoul84/kfc-userapi/internal/transport/http"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/handler"
	"github.com/ramisoul84/kfc-userapi/pkg/cache"
	"github.com/ramisoul84/kfc-userapi/pkg/jwt"
	"github.com/ramisoul84/kfc-userapi/pkg/kafka"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// App wires together all application components.
type App struct {
	config        *config.Config
	logger        *logger.Logger
	httpServer    *httpTransport.Server
	grpcServer    *grpcTransport.Transport
	redis         *cache.Redis
	crmClient     client.CRMClient
	orderClient   client.OrderClient
	kafkaConsumer *kafka.Consumer
	orderConsumer *kafka.Consumer
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

	sseHub := sse.NewHub(log)
	orderHub := sse.NewHub(log)

	// Kafka consumer
	var kafkaConsumer *kafka.Consumer
	if len(cfg.Kafka.Brokers) > 0 {
		menuConsumer := consumer.NewMenuConsumer(redisClient, sseHub, log)

		kafkaConsumer = kafka.NewConsumer(
			cfg.Kafka.Brokers,
			cfg.Kafka.MenuTopic,
			cfg.Kafka.GroupID,
			menuConsumer.Handle,
			log,
		)

		log.Info("kafka consumer initialized",
			"brokers", cfg.Kafka.Brokers,
			"topic", cfg.Kafka.MenuTopic,
			"group", cfg.Kafka.GroupID,
		)
	} else {
		log.Warn("kafka disabled; menu cache will only expire via TTL")
	}

	// Order events → SSE. Every instance needs every event (its SSE
	// clients may be watching any restaurant), so the group is unique per
	// instance and starts at the newest offset: screens refetch on
	// connect, they never need history.
	var orderConsumer *kafka.Consumer
	if len(cfg.Kafka.Brokers) > 0 {
		host, _ := os.Hostname()
		groupID := cfg.Kafka.GroupID + "-orders-" + host
		orderConsumer = kafka.NewConsumer(
			cfg.Kafka.Brokers,
			cfg.Kafka.OrdersTopic,
			groupID,
			consumer.NewOrderConsumer(orderHub, log).Handle,
			log,
			kafka.StartAtLatest(),
		)
		log.Info("order event consumer initialized", "topic", cfg.Kafka.OrdersTopic, "group", groupID)
	} else {
		log.Warn("kafka disabled; screens will not receive live order updates")
	}

	crmClient, err := client.NewCRMClient(&cfg.CRM)
	if err != nil {
		return nil, fmt.Errorf("crm grpc client: %w", err)
	}
	log.Info("crm grpc client initialized", "address", cfg.CRM.Address)

	orderClient, err := client.NewOrderClient(&cfg.Order)
	if err != nil {
		return nil, fmt.Errorf("order grpc client: %w", err)
	}
	log.Info("order grpc client initialized", "address", cfg.Order.Address)

	// Repos
	pairingRepo := repository.NewPairingRepository(redisClient)
	revocationRepo := repository.NewRevocationRepository(redisClient)
	menuCacheRepo := repository.NewMenuCacheRepository(redisClient)

	// Token manager
	deviceTokens := jwt.NewDeviceTokenManager(
		cfg.JWT.DeviceSecret,
		cfg.JWT.Issuer,
		cfg.JWT.DeviceTTL,
	)

	// Services
	pairingSvc := service.NewPairingService(pairingRepo, log)
	deviceAuthSvc := service.NewDeviceAuthService(pairingRepo, revocationRepo, deviceTokens, log)
	menuService := service.NewMenuService(menuCacheRepo, crmClient, log)
	orderService := service.NewOrderService(orderClient, crmClient, menuService, log)

	// Handlers
	deviceAuthHandler := handler.NewDeviceAuthHandler(deviceAuthSvc, crmClient)
	menuStreamHandler := handler.NewMenuStreamHandler(sseHub, log)
	menuHandler := handler.NewMenuHandler(menuService, log)
	orderHandler := handler.NewOrderHandler(orderService)
	orderStreamHandler := handler.NewOrderStreamHandler(orderHub, orderService, log)
	publicHandler := handler.NewPublicHandler(crmClient, orderService, log)

	// HTTP transport
	httpServer := httpTransport.NewServer(
		cfg,
		log,
		deviceAuthHandler,
		menuHandler,
		menuStreamHandler,
		orderHandler,
		orderStreamHandler,
		publicHandler,
		deviceTokens,
		revocationRepo,
	)

	// gRPC transport
	grpcSrv := grpcTransport.NewTransport(cfg, log, pairingSvc, deviceAuthSvc, deviceTokens)

	return &App{
		config:        cfg,
		logger:        log,
		httpServer:    httpServer,
		grpcServer:    grpcSrv,
		redis:         redisClient,
		crmClient:     crmClient,
		orderClient:   orderClient,
		kafkaConsumer: kafkaConsumer,
		orderConsumer: orderConsumer,
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

	if a.kafkaConsumer != nil {
		a.kafkaConsumer.Start(ctx)
	}
	if a.orderConsumer != nil {
		a.orderConsumer.Start(ctx)
	}

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

	if a.kafkaConsumer != nil {
		if err := a.kafkaConsumer.Stop(); err != nil {
			a.logger.Error("kafka consumer stop failed", "error", err)
			errs = append(errs, fmt.Errorf("kafka consumer: %w", err))
		}
	}

	if a.orderConsumer != nil {
		if err := a.orderConsumer.Stop(); err != nil {
			errs = append(errs, fmt.Errorf("order consumer: %w", err))
		}
	}
	if a.orderClient != nil {
		_ = a.orderClient.Close()
	}

	if err := a.httpServer.Shutdown(ctx); err != nil {
		a.logger.Error("http shutdown failed", "error", err)
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if a.crmClient != nil {
		if err := a.crmClient.Close(); err != nil {
			a.logger.Error("crm grpc client close failed", "error", err)
			errs = append(errs, fmt.Errorf("crm client: %w", err))
		}
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

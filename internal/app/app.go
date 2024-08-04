package app

import (
	"context"

	"errors"
	"fmt"
	"os"

	"github.com/prometheus/client_golang/prometheus"

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
	"github.com/ramisoul84/kfc-userapi/pkg/metrics"
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
	loyaltyClient *client.LoyaltyClient
	notifyClient  *client.NotificationClient
	consumers     []*kafka.Consumer
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

	menuCacheRepo := repository.NewMenuCacheRepository(redisClient)
	host, _ := os.Hostname()

	// Menu events: a durable group-wide consumer keeps the shared cache
	// correct; a per-instance consumer drives this replica's live screens
	// (see consumer.MenuConsumer).
	var consumers []*kafka.Consumer
	if len(cfg.Kafka.Brokers) > 0 {
		menuConsumer := consumer.NewMenuConsumer(menuCacheRepo, sseHub, log)
		consumers = append(consumers,
			kafka.NewConsumer(cfg.Kafka.Brokers, cfg.Kafka.MenuTopic, cfg.Kafka.GroupID,
				menuConsumer.Invalidate, log, kafka.RetryHandler(5)),
			kafka.NewConsumer(cfg.Kafka.Brokers, cfg.Kafka.MenuTopic, cfg.Kafka.GroupID+"-menu-"+host,
				menuConsumer.Broadcast, log, kafka.StartAtLatest()),
		)
		log.Info("menu event consumers initialized",
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
	if len(cfg.Kafka.Brokers) > 0 {
		groupID := cfg.Kafka.GroupID + "-orders-" + host
		consumers = append(consumers, kafka.NewConsumer(
			cfg.Kafka.Brokers,
			cfg.Kafka.OrdersTopic,
			groupID,
			consumer.NewOrderConsumer(orderHub, log).Handle,
			log,
			kafka.StartAtLatest(),
		))
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

	loyaltyClient, err := client.NewLoyaltyClient(&cfg.Loyalty)
	if err != nil {
		return nil, fmt.Errorf("loyalty grpc client: %w", err)
	}
	log.Info("loyalty grpc client initialized", "address", cfg.Loyalty.Address)

	notifyClient, err := client.NewNotificationClient(&cfg.Notification)
	if err != nil {
		return nil, fmt.Errorf("notification grpc client: %w", err)
	}
	log.Info("notification grpc client initialized", "address", cfg.Notification.Address)
	if cfg.Customer.DevEchoCode {
		log.Warn("CUSTOMER_DEV_ECHO_CODE is on: sign-in codes are returned by the API (demo only)")
	}

	// Repos
	pairingRepo := repository.NewPairingRepository(redisClient)
	revocationRepo := repository.NewRevocationRepository(redisClient)

	// Token manager
	deviceTokens := jwt.NewDeviceTokenManager(
		cfg.JWT.DeviceSecret,
		cfg.JWT.Issuer,
		cfg.JWT.DeviceTTL,
	)

	customerTokens := jwt.NewCustomerTokenManager(
		cfg.Customer.JWTSecret,
		cfg.JWT.Issuer,
		cfg.Customer.AccessTTL,
		cfg.Customer.KioskSessionTTL,
	)
	customerAuthRepo := repository.NewCustomerAuthRepository(redisClient)

	// Services
	pairingSvc := service.NewPairingService(pairingRepo, log)
	deviceAuthSvc := service.NewDeviceAuthService(pairingRepo, revocationRepo, deviceTokens, log)
	menuService := service.NewMenuService(menuCacheRepo, crmClient, log)
	orderService := service.NewOrderService(orderClient, crmClient, menuService, loyaltyClient, customerTokens, log)
	customerService := service.NewCustomerService(
		customerAuthRepo, loyaltyClient, notifyClient, orderClient, customerTokens, cfg.Customer, log)

	// Handlers
	deviceAuthHandler := handler.NewDeviceAuthHandler(deviceAuthSvc, crmClient)
	menuStreamHandler := handler.NewMenuStreamHandler(sseHub, log)
	menuHandler := handler.NewMenuHandler(menuService, log)
	orderHandler := handler.NewOrderHandler(orderService)
	orderStreamHandler := handler.NewOrderStreamHandler(orderHub, orderService, log)
	publicHandler := handler.NewPublicHandler(crmClient, orderService, log)
	customerHandler := handler.NewCustomerHandler(customerService, orderService, cfg.Customer)

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
		customerHandler,
		customerTokens,
		deviceTokens,
		revocationRepo,
	)

	httpServer.SetReadiness(func(ctx context.Context) error {
		if err := redisClient.Client.Ping(ctx).Err(); err != nil {
			return fmt.Errorf("redis: %w", err)
		}
		return nil
	})

	// Live screens: how many kiosks, KDS/hall displays and tracking pages
	// are connected right now. A drop to zero at opening time is an alert.
	metrics.Registry.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "sse_connections", Help: "Open SSE streams.", ConstLabels: prometheus.Labels{"stream": "menu"},
		}, func() float64 { return float64(menuStreamHandler.Connections()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "sse_connections", Help: "Open SSE streams.", ConstLabels: prometheus.Labels{"stream": "orders"},
		}, func() float64 { return float64(orderStreamHandler.Connections()) }),
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
		loyaltyClient: loyaltyClient,
		notifyClient:  notifyClient,
		consumers:     consumers,
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

	for _, c := range a.consumers {
		c.Start(ctx)
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

	for _, c := range a.consumers {
		if err := c.Stop(); err != nil {
			a.logger.Error("kafka consumer stop failed", "error", err)
			errs = append(errs, fmt.Errorf("kafka consumer: %w", err))
		}
	}
	if a.orderClient != nil {
		_ = a.orderClient.Close()
	}
	_ = a.loyaltyClient.Close()
	_ = a.notifyClient.Close()

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

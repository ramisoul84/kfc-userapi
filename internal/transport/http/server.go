package http

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/ramisoul84/kfc-userapi/internal/config"
	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/internal/repository"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/handler"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/middleware"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/response"
	"github.com/ramisoul84/kfc-userapi/pkg/jwt"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// Server wraps the Fiber app with lifecycle methods.
type Server struct {
	app               *fiber.App
	cfg               *config.Config
	logger            *logger.Logger
	deviceAuthHandler *handler.DeviceAuthHandler
	menuHandler       *handler.MenuHandler
	menuStreamHandler *handler.MenuStreamHandler
	orderHandler      *handler.OrderHandler
	orderStream       *handler.OrderStreamHandler
	publicHandler     *handler.PublicHandler
	customerHandler   *handler.CustomerHandler
	customerTokens    *jwt.CustomerTokenManager
	deviceTokens      *jwt.DeviceTokenManager
	revocationRepo    repository.RevocationRepository
}

// NewServer creates a Fiber app configured from cfg.
func NewServer(
	cfg *config.Config,
	log *logger.Logger,
	deviceAuthHandler *handler.DeviceAuthHandler,
	menuHandler *handler.MenuHandler,
	menuStreamHandler *handler.MenuStreamHandler,
	orderHandler *handler.OrderHandler,
	orderStream *handler.OrderStreamHandler,
	publicHandler *handler.PublicHandler,
	customerHandler *handler.CustomerHandler,
	customerTokens *jwt.CustomerTokenManager,
	deviceTokens *jwt.DeviceTokenManager,
	revocationRepo repository.RevocationRepository,
) *Server {
	app := fiber.New(fiber.Config{
		AppName:               cfg.App.Name,
		ReadTimeout:           cfg.HTTP.ReadTimeout,
		WriteTimeout:          cfg.HTTP.WriteTimeout,
		IdleTimeout:           cfg.HTTP.IdleTimeout,
		DisableStartupMessage: true,
		// Bodies here are small JSON; cap them well below Fiber's 4 MB.
		BodyLimit: 256 << 10,
		// Behind the web apps' nginx every request would otherwise come
		// from the nginx container, putting all customers in one rate-limit
		// bucket. nginx overwrites X-Real-IP with the connecting address;
		// it is honoured only from private-network proxies.
		ProxyHeader:             "X-Real-IP",
		EnableTrustedProxyCheck: true,
		TrustedProxies:          []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.1"},
	})

	s := &Server{
		app:               app,
		cfg:               cfg,
		logger:            log,
		deviceAuthHandler: deviceAuthHandler,
		menuHandler:       menuHandler,
		menuStreamHandler: menuStreamHandler,
		orderHandler:      orderHandler,
		orderStream:       orderStream,
		publicHandler:     publicHandler,
		customerHandler:   customerHandler,
		customerTokens:    customerTokens,
		deviceTokens:      deviceTokens,
		revocationRepo:    revocationRepo,
	}

	s.registerMiddleware()
	s.registerRoutes()

	return s
}

// App returns the underlying Fiber app.
func (s *Server) App() *fiber.App {
	return s.app
}

// Start begins listening for HTTP requests. Blocks until shutdown.
func (s *Server) Start() error {
	addr := ":" + s.cfg.HTTP.Port
	s.logger.Info("http server listening", "addr", addr)
	if err := s.app.Listen(addr); err != nil {
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.app.ShutdownWithContext(ctx)
}

// ═══════════════════════════════════════════════════════════════════
// MIDDLEWARE
// ═══════════════════════════════════════════════════════════════════

func (s *Server) registerMiddleware() {
	s.app.Use(recover.New())
	s.app.Use(cors.New(cors.Config{
		AllowOrigins:     s.cfg.HTTP.CORSAllowOrigins,
		AllowMethods:     "GET,POST,PUT,DELETE,PATCH,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization,X-Request-ID",
		AllowCredentials: true,
	}))
}

// ═══════════════════════════════════════════════════════════════════
// ROUTES
// ═══════════════════════════════════════════════════════════════════

func (s *Server) registerRoutes() {
	// Health check (public)
	s.app.Get("/health", s.healthCheck)

	api := s.app.Group("/api/v1")

	// ── Public: device pairing ──
	devices := api.Group("/devices")
	devices.Post("/pair", s.deviceAuthHandler.Pair)

	// Menu (HTTP): kiosk fetches the effective menu here.
	api.Get("/restaurants/:restaurant_id/menu", s.menuHandler.Get)

	// Menu stream (SSE): kiosk subscribes to menu updates here.
	// Declared exactly once.
	api.Get("/restaurants/:restaurant_id/menu/stream",
		s.menuStreamHandler.Stream)

	// ── Public: customer website ──
	public := api.Group("/public")
	public.Get("/regions", s.publicHandler.Regions)
	public.Get("/restaurants", s.publicHandler.Restaurants)
	public.Get("/restaurants/:id", s.publicHandler.Restaurant)
	public.Get("/orders/:id", s.publicHandler.Order)
	public.Get("/loyalty/program", s.customerHandler.Program)
	public.Get("/orders/:id/stream", s.orderStream.PublicStream)

	// ── Customers (website): phone + SMS code sign-in ──
	auth := api.Group("/customer/auth")
	auth.Post("/code", s.customerHandler.RequestCode)
	auth.Post("/verify", s.customerHandler.VerifyCode)
	auth.Post("/refresh", s.customerHandler.Refresh)
	auth.Post("/logout", s.customerHandler.Logout)

	customer := api.Group("/customer", middleware.CustomerAuth(s.customerTokens))
	customer.Get("/me", s.customerHandler.Me)
	customer.Patch("/me", s.customerHandler.UpdateMe)
	customer.Get("/me/points", s.customerHandler.Points)
	customer.Post("/me/code", s.customerHandler.Code)
	customer.Get("/me/orders", s.customerHandler.Orders)
	customer.Post("/restaurants/:id/orders",
		limiter.New(limiter.Config{
			Max:        s.cfg.HTTP.PublicOrderRateLimit,
			Expiration: time.Minute,
			KeyGenerator: func(c *fiber.Ctx) string {
				return middleware.CustomerClaims(c).MemberID.String()
			},
			LimitReached: func(c *fiber.Ctx) error {
				return response.ErrorResponse(c, fiber.StatusTooManyRequests,
					"rate_limited", "too many orders, please wait a minute")
			},
		}),
		s.customerHandler.PlaceOrder,
	)

	// ── Protected: device-authenticated routes ──
	device := middleware.DeviceAuth(s.deviceTokens, s.revocationRepo)
	kiosk := middleware.RequireDeviceType(domain.DeviceTypeKiosk)
	kds := middleware.RequireDeviceType(domain.DeviceTypeKDS)
	display := middleware.RequireDeviceType(domain.DeviceTypeOrderDisplay)
	screens := middleware.RequireDeviceType(domain.DeviceTypeKDS, domain.DeviceTypeOrderDisplay)

	devices.Get("/me", device, s.deviceAuthHandler.Whoami)

	// Kiosk: place an order (payment already taken on the terminal).
	api.Post("/kiosk/orders", device, kiosk, s.orderHandler.PlaceKioskOrder)
	api.Post("/kiosk/loyalty/identify", device, kiosk, s.customerHandler.IdentifyAtKiosk)

	// Kitchen display: work the queue.
	api.Get("/kitchen/orders", device, kds, s.orderHandler.KitchenOrders)
	api.Post("/kitchen/orders/:id/status", device, kds, s.orderHandler.UpdateKitchenStatus)

	// Hall display: numbers only.
	api.Get("/display/orders", device, display, s.orderHandler.DisplayOrders)

	// Live order events for the restaurant on the device token.
	api.Get("/device/orders/stream", device, screens, s.orderStream.DeviceStream)
}

// ═══════════════════════════════════════════════════════════════════
// HANDLERS
// ═══════════════════════════════════════════════════════════════════

func (s *Server) healthCheck(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

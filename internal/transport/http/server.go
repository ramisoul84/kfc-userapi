package http

import (
	"context"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/ramisoul84/kfc-userapi/internal/config"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// Server wraps the Fiber app with lifecycle methods.
type Server struct {
	app    *fiber.App
	cfg    *config.Config
	logger *logger.Logger
}

// NewServer creates a Fiber app configured from cfg.
func NewServer(
	cfg *config.Config,
	log *logger.Logger,
) *Server {
	app := fiber.New(fiber.Config{
		AppName:               cfg.App.Name,
		ReadTimeout:           cfg.HTTP.ReadTimeout,
		WriteTimeout:          cfg.HTTP.WriteTimeout,
		IdleTimeout:           cfg.HTTP.IdleTimeout,
		DisableStartupMessage: true,
	})

	s := &Server{
		app:    app,
		cfg:    cfg,
		logger: log,
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
	s.app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,DELETE,PATCH,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization,X-Request-ID",
	}))
}

// ═══════════════════════════════════════════════════════════════════
// ROUTES
// ═══════════════════════════════════════════════════════════════════

func (s *Server) registerRoutes() {
	// Health check (public)
	s.app.Get("/health", s.healthCheck)

	// API v1
	_ = s.app.Group("/api/v1")
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

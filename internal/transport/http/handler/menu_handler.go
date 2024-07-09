package handler

import (
	"errors"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/service"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// MenuHandler serves the effective menu for a restaurant.
type MenuHandler struct {
	svc    service.MenuService
	logger *logger.Logger
}

func NewMenuHandler(svc service.MenuService, log *logger.Logger) *MenuHandler {
	return &MenuHandler{svc: svc, logger: log}
}

// Get handles GET /api/v1/restaurants/:restaurant_id/menu.
//
// The response body is raw effective-menu JSON — no envelope. Devices
// and the kiosk consume it directly, and the payload is already JSON
// bytes from the cache or from the gRPC→JSON conversion, so we send it
// without re-encoding.
func (h *MenuHandler) Get(c *fiber.Ctx) error {
	restaurantID := c.Params("restaurant_id")
	if restaurantID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "restaurant_id is required",
		})
	}

	start := time.Now()

	payload, err := h.svc.GetEffectiveMenu(c.Context(), restaurantID)
	if err != nil {
		h.logger.Error("get effective menu failed",
			"restaurant_id", restaurantID,
			"error", err,
		)

		// Distinguish "downstream unavailable" from "not found".
		// The service wraps CRM/gRPC errors; a NotFound comes through
		// as gRPC codes.NotFound mapped to a sentinel error in the service.
		if errors.Is(err, service.ErrMenuNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "restaurant not found",
			})
		}

		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error": "could not fetch menu",
		})
	}

	h.logger.Debug("menu served",
		"restaurant_id", restaurantID,
		"bytes", len(payload),
		"duration_ms", time.Since(start).Milliseconds(),
	)

	c.Set("Content-Type", "application/json; charset=utf-8")
	c.Set("Cache-Control", "no-store") // userapi does the caching; the browser shouldn't
	return c.Send(payload)
}

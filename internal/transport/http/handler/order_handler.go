package handler

import (
	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/internal/service"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/middleware"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/response"
)

// OrderHandler serves the in-restaurant devices: kiosks place orders,
// the KDS works the queue, the hall display shows numbers.
type OrderHandler struct {
	svc service.OrderService
}

func NewOrderHandler(svc service.OrderService) *OrderHandler {
	return &OrderHandler{svc: svc}
}

// PlaceKioskOrder handles POST /api/v1/kiosk/orders.
func (h *OrderHandler) PlaceKioskOrder(c *fiber.Ctx) error {
	claims := middleware.DeviceClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "unauthorized", "missing device context")
	}

	var req service.PlaceOrderRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}

	order, err := h.svc.PlaceKioskOrder(c.UserContext(), claims, &req)
	if err != nil {
		return response.FromError(c, err)
	}
	return response.Created(c, order)
}

// KitchenOrders handles GET /api/v1/kitchen/orders — everything in
// flight at the device's restaurant, oldest first.
func (h *OrderHandler) KitchenOrders(c *fiber.Ctx) error {
	claims := middleware.DeviceClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "unauthorized", "missing device context")
	}

	orders, err := h.svc.ListActive(c.UserContext(), claims.RestaurantID.String())
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, fiber.Map{"orders": orders})
}

// UpdateStatusRequest is the body of POST /kitchen/orders/:id/status.
type UpdateStatusRequest struct {
	Status string `json:"status"`
}

// UpdateKitchenStatus handles POST /api/v1/kitchen/orders/:id/status.
func (h *OrderHandler) UpdateKitchenStatus(c *fiber.Ctx) error {
	claims := middleware.DeviceClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "unauthorized", "missing device context")
	}

	var req UpdateStatusRequest
	if err := c.BodyParser(&req); err != nil || req.Status == "" {
		return response.BadRequest(c, "status is required")
	}

	order, err := h.svc.UpdateFromKitchen(c.UserContext(), claims, c.Params("id"), req.Status)
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, order)
}

// DisplayOrders handles GET /api/v1/display/orders — numbers and
// statuses only, for the public screen in the dining hall.
func (h *OrderHandler) DisplayOrders(c *fiber.Ctx) error {
	claims := middleware.DeviceClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "unauthorized", "missing device context")
	}

	orders, err := h.svc.ListActive(c.UserContext(), claims.RestaurantID.String())
	if err != nil {
		return response.FromError(c, err)
	}
	out := make([]*domain.DisplayOrder, 0, len(orders))
	for _, o := range orders {
		out = append(out, o.ToDisplay())
	}
	return response.OK(c, fiber.Map{"orders": out})
}

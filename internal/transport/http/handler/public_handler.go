package handler

import (
	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/client"
	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/internal/service"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/response"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// PublicHandler serves the customer website: where to order from, how to
// check out, and how the order is going.
type PublicHandler struct {
	crm    client.CRMClient
	orders service.OrderService
	logger *logger.Logger
}

func NewPublicHandler(crm client.CRMClient, orders service.OrderService, log *logger.Logger) *PublicHandler {
	return &PublicHandler{crm: crm, orders: orders, logger: log}
}

type regionJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Code            string `json:"code"`
	Timezone        string `json:"timezone"`
	RestaurantCount int    `json:"restaurant_count"`
}

type restaurantJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Code       string `json:"code"`
	RegionID   string `json:"region_id"`
	RegionName string `json:"region_name"`
	Address    string `json:"address,omitempty"`
	City       string `json:"city,omitempty"`
	Phone      string `json:"phone,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

// Regions handles GET /api/v1/public/regions.
func (h *PublicHandler) Regions(c *fiber.Ctx) error {
	regions, err := h.crm.ListRegions(c.UserContext())
	if err != nil {
		h.logger.Error("list regions failed", "error", err)
		return response.ErrorResponse(c, fiber.StatusBadGateway, "upstream", "could not load cities")
	}
	out := make([]regionJSON, 0, len(regions))
	for _, r := range regions {
		out = append(out, regionJSON{
			ID: r.GetId(), Name: r.GetName(), Code: r.GetCode(),
			Timezone: r.GetTimezone(), RestaurantCount: int(r.GetRestaurantCount()),
		})
	}
	return response.OK(c, fiber.Map{"regions": out})
}

// Restaurants handles GET /api/v1/public/restaurants?region_id=.
func (h *PublicHandler) Restaurants(c *fiber.Ctx) error {
	restaurants, err := h.crm.ListRestaurants(c.UserContext(), c.Query("region_id"))
	if err != nil {
		h.logger.Error("list restaurants failed", "error", err)
		return response.ErrorResponse(c, fiber.StatusBadGateway, "upstream", "could not load restaurants")
	}
	out := make([]restaurantJSON, 0, len(restaurants))
	for _, r := range restaurants {
		out = append(out, restaurantJSON{
			ID: r.GetId(), Name: r.GetName(), Code: r.GetCode(),
			RegionID: r.GetRegionId(), RegionName: r.GetRegionName(),
			Address: r.GetAddress(), City: r.GetCity(), Phone: r.GetPhone(), Timezone: r.GetTimezone(),
		})
	}
	return response.OK(c, fiber.Map{"restaurants": out})
}

// Restaurant handles GET /api/v1/public/restaurants/:id.
func (h *PublicHandler) Restaurant(c *fiber.Ctx) error {
	r, err := h.crm.GetRestaurant(c.UserContext(), c.Params("id"))
	if err != nil {
		return response.NotFound(c, "restaurant not found")
	}
	return response.OK(c, restaurantJSON{
		ID: r.GetId(), Name: r.GetName(), Code: r.GetCode(),
		RegionID: r.GetRegionId(), RegionName: r.GetRegionName(),
		Address: r.GetAddress(), City: r.GetCity(), Phone: r.GetPhone(), Timezone: r.GetTimezone(),
	})
}

// Order handles GET /api/v1/public/orders/:id. The order id is a random
// UUID handed only to the customer who placed it, so it doubles as the
// tracking credential.
func (h *PublicHandler) Order(c *fiber.Ctx) error {
	order, err := h.orders.Get(c.UserContext(), c.Params("id"))
	if err != nil {
		if domain.IsValidationError(err) {
			return response.NotFound(c, "order not found")
		}
		return response.FromError(c, err)
	}
	return response.OK(c, order.ToPublic())
}

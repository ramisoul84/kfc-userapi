package handler

import (
	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/client"
	"github.com/ramisoul84/kfc-userapi/internal/service"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/middleware"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/response"
)

// DeviceAuthHandler handles device-side authentication endpoints.
type DeviceAuthHandler struct {
	svc service.DeviceAuthService
	crm client.CRMClient
}

func NewDeviceAuthHandler(svc service.DeviceAuthService, crm client.CRMClient) *DeviceAuthHandler {
	return &DeviceAuthHandler{svc: svc, crm: crm}
}

// ═══════════════════════════════════════════════════════════════════
// PAIR — POST /api/v1/devices/pair
// ═══════════════════════════════════════════════════════════════════

// PairRequest is the body of POST /api/v1/devices/pair.
type PairRequest struct {
	SerialNumber string `json:"serial_number"`
	Code         string `json:"code"`
}

// PairResponse is returned on a successful pairing.
type PairResponse struct {
	Token        string `json:"token"`
	TokenType    string `json:"token_type"` // "Bearer"
	ExpiresAt    int64  `json:"expires_at"` // unix seconds
	DeviceID     string `json:"device_id"`
	DeviceType   string `json:"device_type"`
	RestaurantID string `json:"restaurant_id"`
}

// Pair validates a pairing code and issues a long-lived device token.
//
// The device submits the serial number and the code that the restaurant
// manager generated in CRM. On success the device receives a JWT to use
// as a Bearer token on every subsequent request.
//
// Status codes:
//
//	200 OK            paired; token returned
//	400 Bad Request   malformed body or missing fields
//	401 Unauthorized  invalid or expired pairing code
//	500 Internal      unexpected failure
func (h *DeviceAuthHandler) Pair(c *fiber.Ctx) error {
	var req PairRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}
	if req.SerialNumber == "" || req.Code == "" {
		return response.BadRequest(c, "serial_number and code are required")
	}

	result, err := h.svc.Pair(c.Context(), req.SerialNumber, req.Code)
	if err != nil {
		return response.FromError(c, err)
	}

	return response.OK(c, PairResponse{
		Token:        result.Token,
		TokenType:    "Bearer",
		ExpiresAt:    result.ExpiresAt.Unix(),
		DeviceID:     result.DeviceID.String(),
		DeviceType:   result.DeviceType,
		RestaurantID: result.RestaurantID.String(),
	})
}

// ═══════════════════════════════════════════════════════════════════
// OPTIONAL — WHOAMI
// ═══════════════════════════════════════════════════════════════════

// WhoamiResponse describes the current authenticated device.
type WhoamiResponse struct {
	DeviceID       string `json:"device_id"`
	DeviceType     string `json:"device_type"`
	SerialNumber   string `json:"serial_number"`
	RestaurantID   string `json:"restaurant_id"`
	RestaurantName string `json:"restaurant_name,omitempty"`
	RestaurantCode string `json:"restaurant_code,omitempty"`
	Address        string `json:"address,omitempty"`
	ExpiresAt      int64  `json:"expires_at"`
}

// Whoami returns the identity of the calling device.
// Requires DeviceAuth middleware on the route.
//
// This is a useful smoke-test endpoint: hit it after pairing to confirm
// the token works, and hit it again after expiry to confirm the client
// handles 401 correctly.
func (h *DeviceAuthHandler) Whoami(c *fiber.Ctx) error {
	claims := middleware.DeviceClaims(c)
	if claims == nil {
		// Should not happen if DeviceAuth middleware ran first.
		return response.Unauthorized(c, "unauthorized", "missing device context")
	}

	out := WhoamiResponse{
		DeviceID:     claims.DeviceID.String(),
		DeviceType:   claims.DeviceType,
		SerialNumber: claims.SerialNumber,
		RestaurantID: claims.RestaurantID.String(),
		ExpiresAt:    claims.ExpiresAt.Unix(),
	}
	// Restaurant details are for the screen header; best-effort.
	if r, err := h.crm.GetRestaurant(c.UserContext(), out.RestaurantID); err == nil {
		out.RestaurantName = r.GetName()
		out.RestaurantCode = r.GetCode()
		out.Address = r.GetAddress()
	}
	return response.OK(c, out)
}

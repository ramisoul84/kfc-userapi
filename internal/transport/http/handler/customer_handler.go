package handler

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/config"
	"github.com/ramisoul84/kfc-userapi/internal/service"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/middleware"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/response"
)

// Refresh token cookie. httpOnly so scripts can't read it, scoped to the
// auth endpoints so it isn't sent anywhere else, SameSite=Strict against CSRF.
const (
	refreshCookie     = "kfc_rt"
	refreshCookiePath = "/api/v1/customer/auth"
)

// CustomerHandler serves website customers: sign-in, account, loyalty,
// order history and checkout.
type CustomerHandler struct {
	customers service.CustomerService
	orders    service.OrderService
	cfg       config.CustomerAuthConfig
}

func NewCustomerHandler(customers service.CustomerService, orders service.OrderService, cfg config.CustomerAuthConfig) *CustomerHandler {
	return &CustomerHandler{customers: customers, orders: orders, cfg: cfg}
}

// ═══════════════════════════════════════════════════════════════════
// AUTH
// ═══════════════════════════════════════════════════════════════════

// RequestCode handles POST /customer/auth/code {phone}.
func (h *CustomerHandler) RequestCode(c *fiber.Ctx) error {
	var req struct {
		Phone string `json:"phone"`
	}
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}
	sent, err := h.customers.RequestCode(c.UserContext(), req.Phone, c.IP())
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, sent)
}

// VerifyCode handles POST /customer/auth/verify {phone, code, first_name?}.
func (h *CustomerHandler) VerifyCode(c *fiber.Ctx) error {
	var req struct {
		Phone     string `json:"phone"`
		Code      string `json:"code"`
		FirstName string `json:"first_name"`
	}
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}
	sess, err := h.customers.VerifyCode(c.UserContext(), req.Phone, strings.TrimSpace(req.Code), req.FirstName)
	if err != nil {
		return response.FromError(c, err)
	}
	h.setRefreshCookie(c, sess.RefreshToken)
	return response.OK(c, sess)
}

// Refresh handles POST /customer/auth/refresh (cookie only). Rotates the
// refresh cookie and returns a new access token.
func (h *CustomerHandler) Refresh(c *fiber.Ctx) error {
	sess, err := h.customers.Refresh(c.UserContext(), c.Cookies(refreshCookie))
	if err != nil {
		h.clearRefreshCookie(c)
		return response.FromError(c, err)
	}
	h.setRefreshCookie(c, sess.RefreshToken)
	return response.OK(c, sess)
}

// Logout handles POST /customer/auth/logout.
func (h *CustomerHandler) Logout(c *fiber.Ctx) error {
	_ = h.customers.Logout(c.UserContext(), c.Cookies(refreshCookie))
	h.clearRefreshCookie(c)
	return response.OK(c, fiber.Map{"signed_out": true})
}

func (h *CustomerHandler) setRefreshCookie(c *fiber.Ctx, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshCookie,
		Value:    token,
		Path:     refreshCookiePath,
		MaxAge:   int(h.cfg.RefreshTTL.Seconds()),
		Expires:  time.Now().Add(h.cfg.RefreshTTL),
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
}

func (h *CustomerHandler) clearRefreshCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshCookie,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HTTPOnly: true,
		Secure:   h.cfg.CookieSecure,
		SameSite: fiber.CookieSameSiteStrictMode,
	})
}

// ═══════════════════════════════════════════════════════════════════
// ACCOUNT
// ═══════════════════════════════════════════════════════════════════

// Me handles GET /customer/me.
func (h *CustomerHandler) Me(c *fiber.Ctx) error {
	claims := middleware.CustomerClaims(c)
	m, err := h.customers.Me(c.UserContext(), claims.MemberID)
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, m)
}

// UpdateMe handles PATCH /customer/me {first_name, email}.
func (h *CustomerHandler) UpdateMe(c *fiber.Ctx) error {
	var req struct {
		FirstName string `json:"first_name"`
		Email     string `json:"email"`
	}
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}
	m, err := h.customers.UpdateMe(c.UserContext(), middleware.CustomerClaims(c).MemberID, req.FirstName, req.Email)
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, m)
}

// Points handles GET /customer/me/points — balance history.
func (h *CustomerHandler) Points(c *fiber.Ctx) error {
	hist, err := h.customers.Points(c.UserContext(), middleware.CustomerClaims(c).MemberID,
		c.QueryInt("limit", 20), c.QueryInt("offset", 0))
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, hist)
}

// Code handles POST /customer/me/code — a fresh kiosk code (QR + digits).
func (h *CustomerHandler) Code(c *fiber.Ctx) error {
	code, err := h.customers.IssueKioskCode(c.UserContext(), middleware.CustomerClaims(c).MemberID)
	if err != nil {
		return response.FromError(c, err)
	}
	c.Set("Cache-Control", "no-store")
	return response.OK(c, code)
}

// Orders handles GET /customer/me/orders.
func (h *CustomerHandler) Orders(c *fiber.Ctx) error {
	orders, total, err := h.customers.Orders(c.UserContext(), middleware.CustomerClaims(c).MemberID,
		c.QueryInt("limit", 10), c.QueryInt("offset", 0))
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, fiber.Map{"orders": orders, "total": total})
}

// PlaceOrder handles POST /customer/restaurants/:id/orders — checkout
// for a signed-in customer, optionally paying part with points.
func (h *CustomerHandler) PlaceOrder(c *fiber.Ctx) error {
	var req service.PlaceOrderRequest
	if err := c.BodyParser(&req); err != nil {
		return response.BadRequest(c, "invalid request body")
	}
	order, err := h.orders.PlaceWebOrder(c.UserContext(), middleware.CustomerClaims(c).MemberID, c.Params("id"), &req)
	if err != nil {
		return response.FromError(c, err)
	}
	return response.Created(c, order.ToPublic())
}

// Program handles GET /public/loyalty/program.
func (h *CustomerHandler) Program(c *fiber.Ctx) error {
	p, err := h.customers.Program(c.UserContext())
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, p)
}

// ═══════════════════════════════════════════════════════════════════
// KIOSK
// ═══════════════════════════════════════════════════════════════════

// IdentifyAtKiosk handles POST /kiosk/loyalty/identify {code}.
func (h *CustomerHandler) IdentifyAtKiosk(c *fiber.Ctx) error {
	claims := middleware.DeviceClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "unauthorized", "missing device context")
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil || strings.TrimSpace(req.Code) == "" {
		return response.BadRequest(c, "code is required")
	}
	sess, err := h.customers.IdentifyAtKiosk(c.UserContext(), claims, req.Code)
	if err != nil {
		return response.FromError(c, err)
	}
	return response.OK(c, sess)
}

package handler

import (
	"bufio"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/domain"
	"github.com/ramisoul84/kfc-userapi/internal/service"
	"github.com/ramisoul84/kfc-userapi/internal/sse"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/middleware"
	"github.com/ramisoul84/kfc-userapi/internal/transport/http/response"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// OrderStreamHandler pushes live order changes over SSE.
//
// Contract with clients: on every (re)connect, wait for the "connected"
// event, then fetch the full list over HTTP. Events after that are
// deltas. Each event carries the whole order, so applying it is a plain
// upsert keyed by id; clients ignore an event whose updated_at is older
// than what they already have.
type OrderStreamHandler struct {
	hub    *sse.Hub
	orders service.OrderService
	logger *logger.Logger

	connections atomic.Int64
}

func NewOrderStreamHandler(hub *sse.Hub, orders service.OrderService, log *logger.Logger) *OrderStreamHandler {
	return &OrderStreamHandler{hub: hub, orders: orders, logger: log}
}

// orderMessage is the SSE "order" event payload.
type orderMessage struct {
	Type  string `json:"type"`
	Order any    `json:"order"`
}

// DeviceStream handles GET /api/v1/device/orders/stream (KDS, hall display).
// The restaurant comes from the device token, never from the request.
func (h *OrderStreamHandler) DeviceStream(c *fiber.Ctx) error {
	claims := middleware.DeviceClaims(c)
	if claims == nil {
		return response.Unauthorized(c, "unauthorized", "missing device context")
	}

	display := claims.DeviceType == domain.DeviceTypeOrderDisplay
	return h.stream(c, claims.RestaurantID.String(), func(evt *domain.OrderEvent) (any, bool) {
		if display {
			return evt.Order.ToDisplay(), true
		}
		return evt.Order, true
	})
}

// PublicStream handles GET /api/v1/public/orders/:id/stream — the
// customer's tracking page. Only events for that one order are sent.
func (h *OrderStreamHandler) PublicStream(c *fiber.Ctx) error {
	// Copy: fasthttp reuses the request buffer after the handler returns,
	// and the stream writer below outlives it.
	orderID := strings.Clone(c.Params("id"))
	order, err := h.orders.Get(c.UserContext(), orderID)
	if err != nil {
		return response.FromError(c, err)
	}

	return h.stream(c, order.RestaurantID, func(evt *domain.OrderEvent) (any, bool) {
		if evt.Order.ID != orderID {
			return nil, false
		}
		return evt.Order.ToPublic(), true
	})
}

// stream subscribes to a restaurant's order events and writes those that
// pass `transform` until the client goes away.
func (h *OrderStreamHandler) stream(
	c *fiber.Ctx,
	restaurantID string,
	transform func(*domain.OrderEvent) (any, bool),
) error {
	c.Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Set("Cache-Control", "no-cache, no-transform")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Status(fiber.StatusOK)

	sub := h.hub.Subscribe(restaurantID)
	total := h.connections.Add(1)
	h.logger.Info("order stream connected", "restaurant_id", restaurantID, "ip", c.IP(), "total", total)

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// fasthttp has no per-request "client gone" signal; a failed write
		// or flush is how we learn the client disconnected. Clean up here,
		// not on ctx.Done (which only fires at server shutdown).
		defer func() {
			h.hub.Unsubscribe(restaurantID, sub)
			remaining := h.connections.Add(-1)
			h.logger.Debug("order stream closed", "restaurant_id", restaurantID, "remaining", remaining)
		}()

		if writeRetry(w, sseRetryHint) != nil {
			return
		}
		if writeEvent(w, "connected", map[string]any{
			"restaurant_id": restaurantID,
			"at":            time.Now().UTC(),
		}, "") != nil || w.Flush() != nil {
			return
		}

		ticker := time.NewTicker(sseHeartbeat)
		defer ticker.Stop()

		for {
			select {
			case payload, ok := <-sub:
				if !ok {
					return
				}
				var evt domain.OrderEvent
				if err := json.Unmarshal(payload, &evt); err != nil || evt.Order == nil {
					h.logger.Warn("skipping malformed order event", "error", err)
					continue
				}
				data, send := transform(&evt)
				if !send {
					continue
				}
				if writeEvent(w, "order", orderMessage{Type: evt.Type, Order: data}, "") != nil || w.Flush() != nil {
					return
				}

			case <-ticker.C:
				if writeComment(w, "ping") != nil || w.Flush() != nil {
					return
				}
			}
		}
	})

	return nil
}

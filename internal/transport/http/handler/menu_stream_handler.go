package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/ramisoul84/kfc-userapi/internal/sse"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// Tuning constants.
const (
	// How long to wait before the client should try reconnecting (ms).
	sseRetryHint = 3000

	// How often to send a comment line to keep the TCP connection alive.
	// Must be shorter than any intermediate proxy's idle timeout.
	sseHeartbeat = 25 * time.Second
)

type MenuStreamHandler struct {
	hub    *sse.Hub
	logger *logger.Logger

	// Live connection count, for metrics.
	connections atomic.Int64
}

func NewMenuStreamHandler(hub *sse.Hub, log *logger.Logger) *MenuStreamHandler {
	return &MenuStreamHandler{hub: hub, logger: log}
}

// Connections returns the number of currently open SSE streams.
func (h *MenuStreamHandler) Connections() int64 {
	return h.connections.Load()
}

// Stream handles GET /api/v1/restaurants/:restaurant_id/menu/stream.
func (h *MenuStreamHandler) Stream(c *fiber.Ctx) error {
	restaurantID := c.Params("restaurant_id")
	if restaurantID == "" {
		return c.Status(fiber.StatusBadRequest).
			JSON(fiber.Map{"error": "restaurant_id is required"})
	}

	// Last-Event-ID lets a reconnecting client resume. We don't replay
	// missed events, but we forward the value so clients can see it in
	// the "connected" payload and decide whether they need a full refetch.
	lastEventID := c.Get("Last-Event-ID")

	sub := h.hub.Subscribe(restaurantID)
	h.connections.Add(1)
	ctx := c.Context()

	h.logger.Info("sse subscriber connected",
		"restaurant_id", restaurantID,
		"ip", c.IP(),
		"total", h.connections.Load(),
	)

	// SSE headers. These must be set before the body writer starts.
	c.Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Set("Cache-Control", "no-cache, no-transform")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no") // tell nginx not to buffer
	c.Status(fiber.StatusOK)

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// A failed write is how fasthttp tells us the client left, so the
		// subscription is released when this writer returns — not on
		// ctx.Done, which only fires at server shutdown.
		defer func() {
			h.hub.Unsubscribe(restaurantID, sub)
			remaining := h.connections.Add(-1)
			h.logger.Debug("sse subscriber removed",
				"restaurant_id", restaurantID,
				"remaining", remaining,
			)
		}()

		// 1. Tell the browser how long to wait before reconnecting.
		if err := writeRetry(w, sseRetryHint); err != nil {
			return
		}

		// 2. Send the initial "connected" event.
		connected := map[string]any{
			"restaurant_id": restaurantID,
			"at":            time.Now().UTC(),
			"resumed":       lastEventID != "",
		}
		if err := writeEvent(w, "connected", connected, lastEventID); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}

		// 3. Stream loop.
		ticker := time.NewTicker(sseHeartbeat)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case payload, ok := <-sub:
				if !ok {
					return
				}
				// Use the Kafka offset as the event ID so reconnecting
				// clients can detect missed windows.
				if err := writeEvent(w, "menu-updated", json.RawMessage(payload), ""); err != nil {
					h.logger.Debug("sse write failed (menu-updated)", "error", err)
					return
				}
				if err := w.Flush(); err != nil {
					return
				}

			case <-ticker.C:
				if err := writeComment(w, "ping"); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	})

	return nil
}

// ── SSE primitives ──────────────────────────────────────────────

// writeRetry tells the client how many milliseconds to wait before
// reconnecting after a disconnect.
func writeRetry(w *bufio.Writer, ms int) error {
	_, err := fmt.Fprintf(w, "retry: %d\n\n", ms)
	return err
}

// writeEvent writes a named SSE event. If id is non-empty it is sent
// as the event ID, which the browser stores and echoes back on
// reconnect via the Last-Event-ID header.
func writeEvent(w *bufio.Writer, event string, data any, id string) error {
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal sse data: %w", err)
	}

	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
		return err
	}
	return nil
}

// writeComment writes an SSE comment line — a heartbeat that the
// browser ignores but keeps the TCP connection warm.
func writeComment(w *bufio.Writer, text string) error {
	_, err := fmt.Fprintf(w, ": %s %s\n\n",
		text, strconv.FormatInt(time.Now().Unix(), 10))
	return err
}

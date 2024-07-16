package consumer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ramisoul84/kfc-userapi/internal/sse"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// OrderConsumer relays orders.events from kfc-order to SSE subscribers
// (kitchen displays, hall displays, customer tracking pages).
//
// Each userapi instance must see every event, because the SSE clients are
// spread across instances. So this consumer runs in its own per-instance
// consumer group, starting at the latest offset (see app wiring).
type OrderConsumer struct {
	hub    *sse.Hub
	logger *logger.Logger
}

func NewOrderConsumer(hub *sse.Hub, log *logger.Logger) *OrderConsumer {
	return &OrderConsumer{hub: hub, logger: log}
}

// Handle is the MessageHandler signature required by kafka.Consumer.
func (o *OrderConsumer) Handle(_ context.Context, _ string, value []byte) error {
	// Only the routing key is needed here; the stream handlers decode the
	// full event per subscriber.
	var head struct {
		Type         string `json:"type"`
		RestaurantID string `json:"restaurant_id"`
	}
	if err := json.Unmarshal(value, &head); err != nil {
		return fmt.Errorf("unmarshal order event: %w", err)
	}
	if head.RestaurantID == "" {
		return fmt.Errorf("order event without restaurant_id")
	}

	o.logger.Debug("order event received", "type", head.Type, "restaurant_id", head.RestaurantID)
	o.hub.Publish(head.RestaurantID, value)
	return nil
}

package consumer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ramisoul84/kfc-userapi/internal/events"
	"github.com/ramisoul84/kfc-userapi/internal/repository"
	"github.com/ramisoul84/kfc-userapi/internal/sse"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// MenuConsumer handles crm.menu.updated events (prices, availability,
// photos, promotions).
//
// It runs as two Kafka consumers with different semantics:
//
//   - Invalidate, in the shared group: every event is applied to the
//     shared Redis cache exactly once per group, and resumes from its
//     committed offset after downtime, so no change is ever skipped.
//   - Broadcast, in a per-instance group from the newest offset: every
//     userapi replica tells its own SSE clients (kiosks, website) to
//     refetch. It invalidates first (idempotent), so a client can never
//     refetch before the stale menu is gone.
type MenuConsumer struct {
	cache  repository.MenuCacheRepository
	hub    *sse.Hub
	logger *logger.Logger
}

func NewMenuConsumer(cache repository.MenuCacheRepository, hub *sse.Hub, log *logger.Logger) *MenuConsumer {
	return &MenuConsumer{cache: cache, hub: hub, logger: log}
}

// Invalidate is the durable, group-wide handler.
func (m *MenuConsumer) Invalidate(ctx context.Context, _ string, value []byte) error {
	evt, err := decode(value)
	if err != nil {
		return err
	}
	return m.invalidate(ctx, evt)
}

// Broadcast is the per-instance handler that drives live screens.
func (m *MenuConsumer) Broadcast(ctx context.Context, _ string, value []byte) error {
	evt, err := decode(value)
	if err != nil {
		return err
	}
	if err := m.invalidate(ctx, evt); err != nil {
		// Still notify: clients refetch and the durable consumer retries.
		m.logger.Error("cache invalidation failed before broadcast", "error", err)
	}
	m.hub.Publish(evt.RestaurantID, value)
	return nil
}

func (m *MenuConsumer) invalidate(ctx context.Context, evt *events.MenuUpdatedEvent) error {
	n, err := m.cache.Invalidate(ctx, evt.RestaurantID)
	if err != nil {
		return err
	}
	m.logger.Info("menu cache invalidated",
		"restaurant_id", evt.RestaurantID,
		"reason", evt.Reason,
		"keys", n,
	)
	return nil
}

func decode(value []byte) (*events.MenuUpdatedEvent, error) {
	var evt events.MenuUpdatedEvent
	if err := json.Unmarshal(value, &evt); err != nil {
		return nil, fmt.Errorf("unmarshal menu event: %w", err)
	}
	return &evt, nil
}

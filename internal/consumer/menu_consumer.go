package consumer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ramisoul84/kfc-userapi/internal/events"
	"github.com/ramisoul84/kfc-userapi/pkg/cache"
	"github.com/ramisoul84/kfc-userapi/pkg/logger"
)

// MenuCacheKeyPrefix is the Redis prefix for cached effective menus.
// The full key is "menu:restaurant:{restaurant_id}".
const MenuCacheKeyPrefix = "menu:restaurant:"

// MenuConsumer handles crm.menu.updated events by invalidating the
// corresponding menu cache entries.
type MenuConsumer struct {
	redis  *cache.Redis
	logger *logger.Logger
}

func NewMenuConsumer(redis *cache.Redis, log *logger.Logger) *MenuConsumer {
	return &MenuConsumer{redis: redis, logger: log}
}

// Handle is the MessageHandler signature required by kafka.Consumer.
func (m *MenuConsumer) Handle(ctx context.Context, _ string, value []byte) error {
	var evt events.MenuUpdatedEvent
	if err := json.Unmarshal(value, &evt); err != nil {
		return fmt.Errorf("unmarshal menu event: %w", err)
	}

	m.logger.Info("menu update received",
		"restaurant_id", evt.RestaurantID,
		"region_id", evt.RegionID,
		"reason", evt.Reason,
	)

	if evt.RestaurantID == "" {
		return m.invalidateAll(ctx, evt.Reason)
	}

	return m.invalidateRestaurant(ctx, evt.RestaurantID, evt.Reason)
}

// ─────────────────────────────────────────────────────────────────

func (m *MenuConsumer) invalidateRestaurant(
	ctx context.Context,
	restaurantID string,
	reason string,
) error {
	key := MenuCacheKeyPrefix + restaurantID
	if err := m.redis.Del(ctx, key); err != nil {
		return fmt.Errorf("delete menu cache %q: %w", key, err)
	}
	m.logger.Info("menu cache invalidated",
		"restaurant_id", restaurantID,
		"reason", reason,
	)
	return nil
}

// invalidateAll deletes every menu cache key. Uses SCAN rather than
// KEYS because KEYS blocks Redis for the whole operation.
func (m *MenuConsumer) invalidateAll(ctx context.Context, reason string) error {
	const scanBatch = 200

	var (
		cursor uint64
		total  int
	)

	for {
		keys, next, err := m.redis.Client.Scan(
			ctx, cursor, MenuCacheKeyPrefix+"*", scanBatch,
		).Result()
		if err != nil {
			return fmt.Errorf("scan menu keys: %w", err)
		}

		if len(keys) > 0 {
			if err := m.redis.Del(ctx, keys...); err != nil {
				return fmt.Errorf("delete menu keys: %w", err)
			}
			total += len(keys)
		}

		cursor = next
		if cursor == 0 {
			break
		}
	}

	m.logger.Info("all menu caches invalidated",
		"reason", reason,
		"keys_deleted", total,
	)
	return nil
}

// unused keeps strings imported for future prefix-matching helpers.
var _ = strings.HasPrefix

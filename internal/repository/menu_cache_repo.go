package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ramisoul84/kfc-userapi/pkg/cache"
)

const (
	// MenuCacheMaxTTL is the safety net: even if an invalidation event
	// were missed, a cached menu is refreshed within this time. Events
	// (and promotion boundaries) normally expire it much sooner.
	MenuCacheMaxTTL = time.Hour

	menuKeyPrefix = "menu:restaurant:"
	// menuInvalidatedPrefix holds when a restaurant's menu (or "all") was
	// last invalidated, in unix nanoseconds. Used to refuse caching a
	// menu that was fetched before the latest change.
	menuInvalidatedPrefix = "menu:invalidated:"
	allRestaurants        = "all"
)

type MenuCacheRepository interface {
	Get(ctx context.Context, restaurantID string) ([]byte, error)
	// Set caches a menu fetched at fetchedAt, unless the menu was
	// invalidated after that moment (the payload would be stale).
	// Returns false when it skipped caching.
	Set(ctx context.Context, restaurantID string, payload []byte, ttl time.Duration, fetchedAt time.Time) (bool, error)
	// Invalidate drops a restaurant's cached menu ("" = every restaurant)
	// and records when, for the stale-write guard in Set.
	Invalidate(ctx context.Context, restaurantID string) (int, error)
}

type menuCacheRepo struct {
	redis *cache.Redis
}

func NewMenuCacheRepository(redis *cache.Redis) MenuCacheRepository {
	return &menuCacheRepo{redis: redis}
}

func (r *menuCacheRepo) Get(ctx context.Context, restaurantID string) ([]byte, error) {
	key := menuKeyPrefix + restaurantID
	data, err := r.redis.GetBytes(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("menu cache get %q: %w", key, err)
	}
	return data, nil // nil, nil = miss
}

func (r *menuCacheRepo) Set(
	ctx context.Context,
	restaurantID string,
	payload []byte,
	ttl time.Duration,
	fetchedAt time.Time,
) (bool, error) {
	if ttl <= 0 {
		return false, nil
	}
	ttl = min(ttl, MenuCacheMaxTTL)

	// Stale-write guard: if the menu was invalidated while we were
	// fetching it from CRM, what we hold may predate the change.
	vals, err := r.redis.Client.MGet(ctx,
		menuInvalidatedPrefix+restaurantID,
		menuInvalidatedPrefix+allRestaurants,
	).Result()
	if err != nil {
		return false, fmt.Errorf("menu cache read invalidation marks: %w", err)
	}
	for _, v := range vals {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if ns, err := strconv.ParseInt(s, 10, 64); err == nil && ns >= fetchedAt.UnixNano() {
			return false, nil
		}
	}

	key := menuKeyPrefix + restaurantID
	if err := r.redis.SetBytes(ctx, key, payload, ttl); err != nil {
		return false, fmt.Errorf("menu cache set %q: %w", key, err)
	}
	return true, nil
}

func (r *menuCacheRepo) Invalidate(ctx context.Context, restaurantID string) (int, error) {
	mark := restaurantID
	if mark == "" {
		mark = allRestaurants
	}
	// Mark first, then delete: a fetch racing with us either sees the
	// mark (and won't cache) or wrote before the delete (and is removed).
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := r.redis.Client.Set(ctx, menuInvalidatedPrefix+mark, now, 2*MenuCacheMaxTTL).Err(); err != nil {
		return 0, fmt.Errorf("mark menu invalidation: %w", err)
	}

	if restaurantID != "" {
		return 1, r.redis.Del(ctx, menuKeyPrefix+restaurantID)
	}

	// Every restaurant: SCAN rather than KEYS, which blocks Redis.
	var cursor uint64
	total := 0
	for {
		keys, next, err := r.redis.Client.Scan(ctx, cursor, menuKeyPrefix+"*", 200).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return total, fmt.Errorf("scan menu keys: %w", err)
		}
		if len(keys) > 0 {
			if err := r.redis.Del(ctx, keys...); err != nil {
				return total, fmt.Errorf("delete menu keys: %w", err)
			}
			total += len(keys)
		}
		cursor = next
		if cursor == 0 {
			return total, nil
		}
	}
}

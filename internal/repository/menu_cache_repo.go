package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/ramisoul84/kfc-userapi/pkg/cache"
)

const (
	// MenuCacheTTL is the safety net. Even without a Kafka event, a
	// cached menu expires after 24h.
	MenuCacheTTL = 24 * time.Hour

	menuKeyPrefix = "menu:restaurant:"
)

type MenuCacheRepository interface {
	Get(ctx context.Context, restaurantID string) ([]byte, error)
	Set(ctx context.Context, restaurantID string, payload []byte) error
	Del(ctx context.Context, restaurantID string) error
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

func (r *menuCacheRepo) Set(ctx context.Context, restaurantID string, payload []byte) error {
	key := menuKeyPrefix + restaurantID
	if err := r.redis.SetBytes(ctx, key, payload, MenuCacheTTL); err != nil {
		return fmt.Errorf("menu cache set %q: %w", key, err)
	}
	return nil
}

func (r *menuCacheRepo) Del(ctx context.Context, restaurantID string) error {
	key := menuKeyPrefix + restaurantID
	return r.redis.Del(ctx, key)
}

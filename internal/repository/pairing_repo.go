package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/ramisoul84/kfc-userapi/pkg/cache"
)

// PairingCodeData is the non-secret metadata stored alongside a code.
type PairingCodeData struct {
	DeviceID     uuid.UUID `json:"device_id"`
	RestaurantID uuid.UUID `json:"restaurant_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// PairingRepository stores pairing codes in Redis.
type PairingRepository interface {
	// SetCode stores a hashed pairing code and its metadata for a serial.
	SetCode(ctx context.Context, serial string, codeHash string, meta *PairingCodeData) error

	// GetCode returns the stored code hash for a serial.
	// Returns ("", nil, nil) if not found.
	GetCode(ctx context.Context, serial string) (hash string, meta *PairingCodeData, err error)

	// DeleteCode removes a serial's code and metadata.
	DeleteCode(ctx context.Context, serial string) error

	// ReplaceRestaurantCodes atomically replaces all codes for a restaurant
	// with the given set. Used by CachePairingCodes to supersede a prior batch.
	ReplaceRestaurantCodes(
		ctx context.Context,
		restaurantID uuid.UUID,
		entries []RestaurantCodeEntry,
		ttl time.Duration,
	) error
}

// RestaurantCodeEntry pairs a serial with its hashed code and metadata.
type RestaurantCodeEntry struct {
	Serial   string
	CodeHash string
	Meta     *PairingCodeData
}

type pairingRepo struct {
	redis *cache.Redis
}

func NewPairingRepository(redis *cache.Redis) PairingRepository {
	return &pairingRepo{redis: redis}
}

func (r *pairingRepo) SetCode(ctx context.Context, serial, codeHash string, meta *PairingCodeData) error {
	ttl := time.Until(meta.ExpiresAt)
	if ttl <= 0 {
		return fmt.Errorf("pairing code already expired")
	}

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}

	pipe := r.redis.Client.Pipeline()
	pipe.Set(ctx, codeKey(serial), codeHash, ttl)
	pipe.Set(ctx, metaKey(serial), metaJSON, ttl)
	pipe.SAdd(ctx, restaurantKey(meta.RestaurantID), serial)
	pipe.Expire(ctx, restaurantKey(meta.RestaurantID), ttl)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis pipeline set code: %w", err)
	}
	return nil
}

func (r *pairingRepo) GetCode(ctx context.Context, serial string) (string, *PairingCodeData, error) {
	pipe := r.redis.Client.Pipeline()
	codeCmd := pipe.Get(ctx, codeKey(serial))
	metaCmd := pipe.Get(ctx, metaKey(serial))
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return "", nil, fmt.Errorf("redis pipeline get code: %w", err)
	}

	hash, err := codeCmd.Result()
	if err == redis.Nil {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("get code hash: %w", err)
	}

	metaJSON, err := metaCmd.Bytes()
	if err != nil {
		return "", nil, fmt.Errorf("get code meta: %w", err)
	}

	var meta PairingCodeData
	if err := json.Unmarshal(metaJSON, &meta); err != nil {
		return "", nil, fmt.Errorf("unmarshal meta: %w", err)
	}

	return hash, &meta, nil
}

func (r *pairingRepo) DeleteCode(ctx context.Context, serial string) error {
	metaJSON, err := r.redis.Client.Get(ctx, metaKey(serial)).Bytes()
	if err == redis.Nil {
		// No code — nothing to delete beyond the key itself.
		return r.redis.Del(ctx, codeKey(serial), metaKey(serial))
	}
	if err != nil {
		return fmt.Errorf("get meta for delete: %w", err)
	}

	var meta PairingCodeData
	if err := json.Unmarshal(metaJSON, &meta); err == nil {
		_ = r.redis.Client.SRem(ctx, restaurantKey(meta.RestaurantID), serial).Err()
	}

	return r.redis.Del(ctx, codeKey(serial), metaKey(serial))
}

func (r *pairingRepo) ReplaceRestaurantCodes(
	ctx context.Context,
	restaurantID uuid.UUID,
	entries []RestaurantCodeEntry,
	ttl time.Duration,
) error {
	// 1. Read the old serial set and delete each old code.
	oldSerials, err := r.redis.Client.SMembers(ctx, restaurantKey(restaurantID)).Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("read old serials: %w", err)
	}

	if len(oldSerials) > 0 {
		keys := make([]string, 0, len(oldSerials)*2)
		for _, s := range oldSerials {
			keys = append(keys, codeKey(s), metaKey(s))
		}
		if err := r.redis.Del(ctx, keys...); err != nil {
			return fmt.Errorf("delete old codes: %w", err)
		}
	}

	// 2. Write the new batch inside a pipeline.
	pipe := r.redis.Client.Pipeline()
	for _, e := range entries {
		metaJSON, err := json.Marshal(e.Meta)
		if err != nil {
			return fmt.Errorf("marshal meta for %s: %w", e.Serial, err)
		}
		pipe.Set(ctx, codeKey(e.Serial), e.CodeHash, ttl)
		pipe.Set(ctx, metaKey(e.Serial), metaJSON, ttl)
	}
	// Replace the serial set atomically.
	pipe.Del(ctx, restaurantKey(restaurantID))
	if len(entries) > 0 {
		serials := make([]interface{}, len(entries))
		for i, e := range entries {
			serials[i] = e.Serial
		}
		pipe.SAdd(ctx, restaurantKey(restaurantID), serials...)
		pipe.Expire(ctx, restaurantKey(restaurantID), ttl)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis pipeline write batch: %w", err)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────
// Key helpers
// ─────────────────────────────────────────────────────────────────

func codeKey(serial string) string      { return "pair:code:" + serial }
func metaKey(serial string) string      { return "pair:meta:" + serial }
func restaurantKey(id uuid.UUID) string { return "pair:restaurant:" + id.String() }

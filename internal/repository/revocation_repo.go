package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ramisoul84/kfc-userapi/pkg/cache"
)

// RevocationRepository tracks revoked device tokens.
type RevocationRepository interface {
	// RevokeDevice blocks all tokens for a device until its tokens would
	// naturally expire. Called when a device is deleted or deactivated.
	RevokeDevice(ctx context.Context, deviceID uuid.UUID, ttl time.Duration) error

	// RevokeToken blocks a single token by its jti.
	RevokeToken(ctx context.Context, tokenID string, ttl time.Duration) error

	// IsDeviceRevoked reports whether a device is revoked.
	IsDeviceRevoked(ctx context.Context, deviceID uuid.UUID) (bool, error)

	// IsTokenRevoked reports whether a specific token is revoked.
	IsTokenRevoked(ctx context.Context, tokenID string) (bool, error)
}

type revocationRepo struct {
	redis *cache.Redis
}

func NewRevocationRepository(redis *cache.Redis) RevocationRepository {
	return &revocationRepo{redis: redis}
}

func (r *revocationRepo) RevokeDevice(ctx context.Context, deviceID uuid.UUID, ttl time.Duration) error {
	key := revokedDeviceKey(deviceID)
	if err := r.redis.Client.Set(ctx, key, "1", ttl).Err(); err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	return nil
}

func (r *revocationRepo) RevokeToken(ctx context.Context, tokenID string, ttl time.Duration) error {
	key := revokedTokenKey(tokenID)
	if err := r.redis.Client.Set(ctx, key, "1", ttl).Err(); err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	return nil
}

func (r *revocationRepo) IsDeviceRevoked(ctx context.Context, deviceID uuid.UUID) (bool, error) {
	n, err := r.redis.Client.Exists(ctx, revokedDeviceKey(deviceID)).Result()
	if err != nil {
		return false, fmt.Errorf("check device revocation: %w", err)
	}
	return n > 0, nil
}

func (r *revocationRepo) IsTokenRevoked(ctx context.Context, tokenID string) (bool, error) {
	n, err := r.redis.Client.Exists(ctx, revokedTokenKey(tokenID)).Result()
	if err != nil {
		return false, fmt.Errorf("check token revocation: %w", err)
	}
	return n > 0, nil
}

func revokedDeviceKey(id uuid.UUID) string { return "revoked:device:" + id.String() }
func revokedTokenKey(id string) string     { return "revoked:token:" + id }

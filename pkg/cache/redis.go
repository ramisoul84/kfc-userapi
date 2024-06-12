package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/ramisoul84/kfc-userapi/internal/config"
	"github.com/redis/go-redis/v9"
)

// Redis wraps a redis.Client
type Redis struct {
	Client *redis.Client
}

// NewRedis connects to Redis and verifies the connection.
func NewRedis(cfg *config.RedisConfig) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         fmt.Sprintf("%s:%s", cfg.Host, cfg.Port),
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConnectTimeout)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping %s:%s: %w", cfg.Host, cfg.Port, err)
	}

	return &Redis{Client: client}, nil
}

// Close closes the Redis connection. Safe to call on a nil *Redis.
func (r *Redis) Close() error {
	if r == nil || r.Client == nil {
		return nil
	}
	return r.Client.Close()
}

// GetBytes returns the raw value for key.
// Returns (nil, nil) when the key does not exist.
func (r *Redis) GetBytes(ctx context.Context, key string) ([]byte, error) {
	val, err := r.Client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis get %q: %w", key, err)
	}
	return val, nil
}

// SetBytes stores value under key with the given TTL.
func (r *Redis) SetBytes(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := r.Client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("redis set %q: %w", key, err)
	}
	return nil
}

// Del removes one or more keys.
func (r *Redis) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	if err := r.Client.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}

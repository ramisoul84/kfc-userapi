package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/ramisoul84/kfc-userapi/pkg/cache"
)

// LoginCode is a pending SMS sign-in challenge.
type LoginCode struct {
	Hash     string `json:"hash"`
	Attempts int    `json:"attempts"`
}

// CustomerAuthRepository keeps short-lived customer auth state in Redis:
// SMS codes, send throttles, refresh tokens and kiosk lookup failures.
type CustomerAuthRepository interface {
	// SMS codes
	SaveCode(ctx context.Context, phone string, code *LoginCode, ttl time.Duration) error
	GetCode(ctx context.Context, phone string) (*LoginCode, time.Duration, error)
	DeleteCode(ctx context.Context, phone string) error

	// Throttles. Allow reserves one use of a fixed window and reports
	// whether it was within the limit.
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error)

	// Refresh tokens, stored by hash. Take is atomic: a token works once.
	SaveRefresh(ctx context.Context, tokenHash string, memberID uuid.UUID, ttl time.Duration) error
	TakeRefresh(ctx context.Context, tokenHash string) (uuid.UUID, error)
	DeleteRefresh(ctx context.Context, tokenHash string) error
}

var ErrNotFound = errors.New("not found")

type customerAuthRepo struct {
	redis *cache.Redis
}

func NewCustomerAuthRepository(r *cache.Redis) CustomerAuthRepository {
	return &customerAuthRepo{redis: r}
}

func codeKeyFor(phone string) string   { return "cust:code:" + phone }
func refreshKeyFor(hash string) string { return "cust:refresh:" + hash }
func throttleKeyFor(key string) string { return "cust:throttle:" + key }

func (r *customerAuthRepo) SaveCode(ctx context.Context, phone string, code *LoginCode, ttl time.Duration) error {
	raw, err := json.Marshal(code)
	if err != nil {
		return err
	}
	return r.redis.SetBytes(ctx, codeKeyFor(phone), raw, ttl)
}

func (r *customerAuthRepo) GetCode(ctx context.Context, phone string) (*LoginCode, time.Duration, error) {
	key := codeKeyFor(phone)
	raw, err := r.redis.Client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("get login code: %w", err)
	}
	ttl, err := r.redis.Client.TTL(ctx, key).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("get login code ttl: %w", err)
	}
	var c LoginCode
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, 0, fmt.Errorf("decode login code: %w", err)
	}
	return &c, ttl, nil
}

func (r *customerAuthRepo) DeleteCode(ctx context.Context, phone string) error {
	return r.redis.Del(ctx, codeKeyFor(phone))
}

func (r *customerAuthRepo) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	k := throttleKeyFor(key)
	pipe := r.redis.Client.TxPipeline()
	incr := pipe.Incr(ctx, k)
	pipe.ExpireNX(ctx, k, window)
	ttl := pipe.TTL(ctx, k)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, fmt.Errorf("throttle %s: %w", key, err)
	}
	return incr.Val() <= int64(limit), ttl.Val(), nil
}

func (r *customerAuthRepo) SaveRefresh(ctx context.Context, tokenHash string, memberID uuid.UUID, ttl time.Duration) error {
	return r.redis.Client.Set(ctx, refreshKeyFor(tokenHash), memberID.String(), ttl).Err()
}

func (r *customerAuthRepo) TakeRefresh(ctx context.Context, tokenHash string) (uuid.UUID, error) {
	v, err := r.redis.Client.GetDel(ctx, refreshKeyFor(tokenHash)).Result()
	if errors.Is(err, redis.Nil) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("take refresh token: %w", err)
	}
	return uuid.Parse(v)
}

func (r *customerAuthRepo) DeleteRefresh(ctx context.Context, tokenHash string) error {
	return r.redis.Del(ctx, refreshKeyFor(tokenHash))
}

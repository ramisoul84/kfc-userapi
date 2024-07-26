package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ramisoul84/kfc-userapi/pkg/cache"
)

func newTestCache(t *testing.T) (MenuCacheRepository, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	r := &cache.Redis{Client: redis.NewClient(&redis.Options{Addr: mr.Addr()})}
	return NewMenuCacheRepository(r), mr
}

func TestSetCachesFreshMenu(t *testing.T) {
	repo, _ := newTestCache(t)
	ctx := context.Background()

	ok, err := repo.Set(ctx, "r1", []byte(`{"v":1}`), time.Minute, time.Now())
	if err != nil || !ok {
		t.Fatalf("expected menu to be cached, ok=%v err=%v", ok, err)
	}
	got, _ := repo.Get(ctx, "r1")
	if string(got) != `{"v":1}` {
		t.Fatalf("got %q", got)
	}
}

// A menu fetched before an invalidation must not be written to the cache,
// or it would pin the old prices until the TTL.
func TestSetSkipsMenuFetchedBeforeInvalidation(t *testing.T) {
	repo, _ := newTestCache(t)
	ctx := context.Background()

	fetchedAt := time.Now()
	time.Sleep(2 * time.Millisecond)
	if _, err := repo.Invalidate(ctx, "r1"); err != nil {
		t.Fatal(err)
	}

	ok, err := repo.Set(ctx, "r1", []byte(`{"v":"old"}`), time.Minute, fetchedAt)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("stale menu was cached")
	}
	if got, _ := repo.Get(ctx, "r1"); got != nil {
		t.Fatalf("cache should be empty, got %q", got)
	}
}

func TestGlobalInvalidationBlocksEveryRestaurant(t *testing.T) {
	repo, _ := newTestCache(t)
	ctx := context.Background()

	_, _ = repo.Set(ctx, "r1", []byte("a"), time.Minute, time.Now())
	_, _ = repo.Set(ctx, "r2", []byte("b"), time.Minute, time.Now())
	fetchedAt := time.Now()
	time.Sleep(2 * time.Millisecond)

	n, err := repo.Invalidate(ctx, "")
	if err != nil || n != 2 {
		t.Fatalf("expected 2 keys deleted, n=%d err=%v", n, err)
	}
	if ok, _ := repo.Set(ctx, "r2", []byte("old"), time.Minute, fetchedAt); ok {
		t.Fatal("stale menu cached after a global invalidation")
	}
}

func TestTTLIsCappedAndHonoured(t *testing.T) {
	repo, mr := newTestCache(t)
	ctx := context.Background()

	_, _ = repo.Set(ctx, "r1", []byte("a"), 48*time.Hour, time.Now())
	if ttl := mr.TTL(menuKeyPrefix + "r1"); ttl != MenuCacheMaxTTL {
		t.Fatalf("ttl should be capped at %v, got %v", MenuCacheMaxTTL, ttl)
	}

	// A promotion starting in 90 s: the menu must expire then.
	_, _ = repo.Set(ctx, "r2", []byte("b"), 90*time.Second, time.Now())
	if ttl := mr.TTL(menuKeyPrefix + "r2"); ttl != 90*time.Second {
		t.Fatalf("ttl should follow the next promotion change, got %v", ttl)
	}

	// Already past its change: don't cache at all.
	if ok, _ := repo.Set(ctx, "r3", []byte("c"), -time.Second, time.Now()); ok {
		t.Fatal("expired menu was cached")
	}
}

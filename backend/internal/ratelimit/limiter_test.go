package ratelimit

import (
	"context"
	"testing"
	"time"

	"nyctaxi/backend/internal/cache"
)

func testRedis(t *testing.T) *cache.Client {
	t.Helper()
	c := cache.New("localhost:6379", 5)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("redis not reachable at localhost:6379, skipping: %v", err)
	}
	return c
}

func TestLimiter_AllowsUpToLimit(t *testing.T) {
	redis := testRedis(t)
	l := New(redis, 5, 60)
	ctx := context.Background()
	id := "test-ip-allows-up-to-limit"

	for i := 1; i <= 5; i++ {
		if !l.Allow(ctx, id) {
			t.Fatalf("request %d should have been allowed (limit=5)", i)
		}
	}
}

func TestLimiter_RejectsAfterLimit(t *testing.T) {
	redis := testRedis(t)
	l := New(redis, 3, 60)
	ctx := context.Background()
	id := "test-ip-rejects-after-limit"

	for i := 1; i <= 3; i++ {
		if !l.Allow(ctx, id) {
			t.Fatalf("request %d should have been allowed (limit=3)", i)
		}
	}
	if l.Allow(ctx, id) {
		t.Error("4th request should have been rejected (limit=3)")
	}
	if l.Allow(ctx, id) {
		t.Error("5th request should also have been rejected")
	}
}

func TestLimiter_DifferentIdentifiersHaveSeparateBudgets(t *testing.T) {
	redis := testRedis(t)
	l := New(redis, 2, 60)
	ctx := context.Background()

	if !l.Allow(ctx, "ip-a") || !l.Allow(ctx, "ip-a") {
		t.Fatal("ip-a should get its full budget of 2")
	}
	if l.Allow(ctx, "ip-a") {
		t.Error("ip-a's 3rd request should be rejected")
	}
	// ip-b has its own separate budget, unaffected by ip-a's usage
	if !l.Allow(ctx, "ip-b") {
		t.Error("ip-b should still have its own budget available")
	}
}

func TestLimiter_ResetsAfterWindowExpires(t *testing.T) {
	redis := testRedis(t)
	l := New(redis, 2, 1) // 2 requests per 1-second window
	ctx := context.Background()
	id := "test-ip-window-reset"

	l.Allow(ctx, id)
	l.Allow(ctx, id)
	if l.Allow(ctx, id) {
		t.Fatal("3rd request within the window should be rejected")
	}

	time.Sleep(1200 * time.Millisecond) // let the window pass

	if !l.Allow(ctx, id) {
		t.Error("request in a new window should be allowed again")
	}
}

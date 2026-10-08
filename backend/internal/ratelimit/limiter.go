// Package ratelimit implements a fixed-window rate limiter backed by
// Redis, per spec §16: "100 requests/minute/IP... ưu tiên thiết kế dễ
// hiểu để giải thích trong phỏng vấn" (prioritize a design that's easy to
// explain in an interview) — so this is the simple fixed-window counter,
// not a sliding-window or token-bucket, with its trade-off named openly.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"nyctaxi/backend/internal/cache"
)

// Limiter allows N requests per identifier (typically client IP) per
// windowSeconds. Implementation: INCR a Redis key named for the
// identifier + current window; if this is the first increment in the
// window (result == 1), set its expiry to windowSeconds. Once the count
// exceeds the limit, further requests in that window are rejected.
//
// Known trade-off (worth naming in an interview): a fixed window allows
// up to 2x the limit across a window boundary (e.g. 100 requests in the
// last second of one window + 100 in the first second of the next). A
// sliding-window-log or token-bucket avoids this at the cost of more
// complexity — the simple version was the explicit spec priority here.
type Limiter struct {
	redis         *cache.Client
	limit         int
	windowSeconds int
}

func New(redis *cache.Client, limit, windowSeconds int) *Limiter {
	return &Limiter{redis: redis, limit: limit, windowSeconds: windowSeconds}
}

// Allow returns true if the request should proceed, false if the
// identifier has exceeded its limit for the current window. On Redis
// error, it fails OPEN (allows the request) — a cache/rate-limit outage
// should degrade gracefully, not take down the whole API.
func (l *Limiter) Allow(ctx context.Context, identifier string) bool {
	window := time.Now().Unix() / int64(l.windowSeconds)
	key := fmt.Sprintf("ratelimit:%s:%d", identifier, window)

	count, err := l.redis.Incr(ctx, key)
	if err != nil {
		return true // fail open
	}
	if count == 1 {
		// first request in this window for this identifier: set the
		// window's expiry so the key (and the counter) disappears on its
		// own once the window passes — no separate cleanup job needed.
		_ = l.redis.Expire(ctx, key, l.windowSeconds)
	}
	return count <= int64(l.limit)
}

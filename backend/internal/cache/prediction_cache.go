// prediction_cache.go builds cache keys from a prediction request and
// wraps the raw Redis client with prediction-specific Get/Set.
//
// Cache key design (per spec §15): pickup + dropoff + passenger_count +
// a TIME BUCKET, not the exact timestamp. Two requests for the same trip
// five seconds apart should hit the same cache entry; two requests an
// hour apart (different traffic conditions) should not. We round
// pickup_datetime down to the nearest 10-minute bucket.
package cache

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"nyctaxi/backend/internal/models"
)

const timeBucketMinutes = 10

type PredictionCache struct {
	redis *Client
	ttl   int // seconds
}

func NewPredictionCache(redis *Client, ttlSeconds int) *PredictionCache {
	return &PredictionCache{redis: redis, ttl: ttlSeconds}
}

// Key builds "prediction:{sha1 hash}" from the fields that actually affect
// the prediction, per spec §15 — NOT the raw request struct, so that two
// semantically-identical requests (e.g. differing only in JSON field
// order, or store_and_fwd_flag casing) still hit the same cache entry.
func Key(req models.PredictionRequest) string {
	bucket := timeBucket(req.PickupDatetime)
	raw := fmt.Sprintf("%.4f|%.4f|%.4f|%.4f|%d|%s",
		req.PickupLatitude, req.PickupLongitude,
		req.DropoffLatitude, req.DropoffLongitude,
		req.PassengerCount, bucket,
	)
	sum := sha1.Sum([]byte(raw))
	return "prediction:" + hex.EncodeToString(sum[:])
}

func timeBucket(pickupDatetime string) string {
	t, err := time.Parse(time.RFC3339, pickupDatetime)
	if err != nil {
		// unparsable datetime shouldn't happen (validated upstream), but
		// fail safe: fall back to the raw string so we still get SOME
		// cache key rather than erroring the whole request.
		return pickupDatetime
	}
	bucketed := t.Truncate(timeBucketMinutes * time.Minute)
	return bucketed.Format("2006-01-02T15:04")
}

// Get returns (response, true) on a cache hit, (nil, false) on a genuine
// miss. Any Redis-level error is treated as a miss (logged by the
// caller) — a cache outage should degrade to "always call the ML
// service", never take down predictions entirely.
func (pc *PredictionCache) Get(ctx context.Context, req models.PredictionRequest) (*models.PredictionResponse, bool) {
	raw, found, err := pc.redis.GetOrNil(ctx, Key(req))
	if err != nil || !found {
		return nil, false
	}
	var resp models.PredictionResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, false
	}
	return &resp, true
}

// Set stores the response with the configured TTL. Errors are returned
// (not swallowed) so the caller can log them — but a failed cache write
// should never fail the prediction request itself.
func (pc *PredictionCache) Set(ctx context.Context, req models.PredictionRequest, resp *models.PredictionResponse) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal for cache: %w", err)
	}
	return pc.redis.SetEX(ctx, Key(req), string(raw), pc.ttl)
}

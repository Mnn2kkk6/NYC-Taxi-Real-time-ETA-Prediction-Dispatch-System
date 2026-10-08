package cache

import (
	"context"
	"testing"
	"time"

	"nyctaxi/backend/internal/models"
)

// These tests require a real redis-server reachable at localhost:6379
// (see the package doc in redis_client.go for why we don't mock this:
// the whole point of hand-writing a RESP client is to prove it actually
// speaks the protocol correctly against the real thing).

func testClient(t *testing.T) *Client {
	t.Helper()
	c := New("localhost:6379", 5)
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := c.Ping(ctx); err != nil {
		t.Skipf("redis not reachable at localhost:6379, skipping: %v", err)
	}
	return c
}

func TestClient_SetAndGet(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	if err := c.SetEX(ctx, "test:key1", "hello world", 10); err != nil {
		t.Fatalf("SetEX failed: %v", err)
	}
	val, found, err := c.GetOrNil(ctx, "test:key1")
	if err != nil {
		t.Fatalf("GetOrNil failed: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if val != "hello world" {
		t.Errorf("got %q, want %q", val, "hello world")
	}
}

func TestClient_GetMissingKeyReturnsNotFound(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	_, found, err := c.GetOrNil(ctx, "test:does-not-exist-xyz123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected found=false for missing key")
	}
}

func TestClient_SetEXActuallyExpires(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()

	if err := c.SetEX(ctx, "test:short-lived", "value", 1); err != nil {
		t.Fatalf("SetEX failed: %v", err)
	}
	_, found, _ := c.GetOrNil(ctx, "test:short-lived")
	if !found {
		t.Fatal("expected key to exist immediately after SetEX")
	}

	time.Sleep(1200 * time.Millisecond)

	_, found, _ = c.GetOrNil(ctx, "test:short-lived")
	if found {
		t.Error("expected key to have expired after its TTL")
	}
}

func TestClient_IncrCreatesAndIncrements(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := "test:counter:incr"
	c.do(ctx, "DEL", key) // clean slate

	n1, err := c.Incr(ctx, key)
	if err != nil {
		t.Fatalf("Incr failed: %v", err)
	}
	if n1 != 1 {
		t.Errorf("first Incr: got %d, want 1", n1)
	}

	n2, err := c.Incr(ctx, key)
	if err != nil {
		t.Fatalf("Incr failed: %v", err)
	}
	if n2 != 2 {
		t.Errorf("second Incr: got %d, want 2", n2)
	}
}

func TestClient_ConcurrentIncrIsAtomic(t *testing.T) {
	c := testClient(t)
	ctx := context.Background()
	key := "test:counter:concurrent"
	c.do(ctx, "DEL", key)

	const n = 50
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			c.Incr(ctx, key)
			done <- struct{}{}
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}

	val, _, err := c.GetOrNil(ctx, key)
	if err != nil {
		t.Fatalf("GetOrNil failed: %v", err)
	}
	if val != "50" {
		t.Errorf("expected exactly 50 after %d concurrent INCRs, got %s", n, val)
	}
}

func TestPredictionCache_KeyIsDeterministic(t *testing.T) {
	req := models.PredictionRequest{
		PickupLatitude: 40.761, PickupLongitude: -73.982,
		DropoffLatitude: 40.730, DropoffLongitude: -73.995,
		PassengerCount: 2, PickupDatetime: "2016-03-15T18:34:00Z",
	}
	k1 := Key(req)
	k2 := Key(req)
	if k1 != k2 {
		t.Errorf("same request produced different keys: %s vs %s", k1, k2)
	}
}

func TestPredictionCache_TimeBucketingGroupsNearbyRequests(t *testing.T) {
	base := models.PredictionRequest{
		PickupLatitude: 40.761, PickupLongitude: -73.982,
		DropoffLatitude: 40.730, DropoffLongitude: -73.995,
		PassengerCount: 2,
	}
	req1 := base
	req1.PickupDatetime = "2016-03-15T18:31:00Z" // same 10-min bucket (18:30-18:40)
	req2 := base
	req2.PickupDatetime = "2016-03-15T18:38:00Z"
	req3 := base
	req3.PickupDatetime = "2016-03-15T19:05:00Z" // different bucket entirely

	if Key(req1) != Key(req2) {
		t.Error("requests 7 minutes apart in the same 10-min bucket should share a cache key")
	}
	if Key(req1) == Key(req3) {
		t.Error("requests in different time buckets should NOT share a cache key")
	}
}

func TestPredictionCache_SetThenGetRoundTrips(t *testing.T) {
	c := testClient(t)
	pc := NewPredictionCache(c, 5)
	ctx := context.Background()

	req := models.PredictionRequest{
		PickupLatitude: 40.7, PickupLongitude: -73.9,
		DropoffLatitude: 40.75, DropoffLongitude: -73.96,
		PassengerCount: 3, PickupDatetime: "2016-05-01T09:00:00Z",
	}
	resp := &models.PredictionResponse{
		PredictedDurationSeconds: 555,
		ModelVersion:             "test-v1",
	}

	if _, hit := pc.Get(ctx, req); hit {
		t.Fatal("expected a miss before any Set")
	}

	if err := pc.Set(ctx, req, resp); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, hit := pc.Get(ctx, req)
	if !hit {
		t.Fatal("expected a hit after Set")
	}
	if got.PredictedDurationSeconds != 555 {
		t.Errorf("got %d, want 555", got.PredictedDurationSeconds)
	}
}

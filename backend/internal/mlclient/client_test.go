package mlclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nyctaxi/backend/internal/models"
)

func sampleRequest() models.PredictionRequest {
	return models.PredictionRequest{
		PickupLatitude:   40.761,
		PickupLongitude:  -73.982,
		DropoffLatitude:  40.730,
		DropoffLongitude: -73.995,
		PassengerCount:   2,
		PickupDatetime:   "2016-03-15T18:30:00",
	}
}

func TestPredict_SuccessOnFirstAttempt(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		resp := models.PredictionResponse{PredictedDurationSeconds: 500, ModelVersion: "test-v1"}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	c := New(ts.URL, 1*time.Second, 3)
	resp, err := c.Predict(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.PredictedDurationSeconds != 500 {
		t.Errorf("got %d, want 500", resp.PredictedDurationSeconds)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected exactly 1 call, got %d", calls)
	}
}

func TestPredict_RetriesOnFailureThenSucceeds(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(models.PredictionResponse{PredictedDurationSeconds: 700})
	}))
	defer ts.Close()

	c := New(ts.URL, 1*time.Second, 5)
	resp, err := c.Predict(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.PredictedDurationSeconds != 700 {
		t.Errorf("got %d, want 700", resp.PredictedDurationSeconds)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected exactly 3 calls (2 failures + 1 success), got %d", calls)
	}
}

func TestPredict_GivesUpAfterMaxRetries(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := New(ts.URL, 500*time.Millisecond, 3)
	_, err := c.Predict(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected exactly 3 attempts (maxRetries), got %d", calls)
	}
}

func TestPredict_RespectsCallerContextCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // always fails, would normally retry
	}))
	defer ts.Close()

	c := New(ts.URL, 1*time.Second, 10) // 10 retries — would take a while if not cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	_, err := c.Predict(ctx, sampleRequest())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected fast return on cancelled context, took %v", elapsed)
	}
}

func TestPredict_TimesOutOnSlowServer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // slower than the client's timeout
		json.NewEncoder(w).Encode(models.PredictionResponse{PredictedDurationSeconds: 999})
	}))
	defer ts.Close()

	c := New(ts.URL, 200*time.Millisecond, 1)
	_, err := c.Predict(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestHealthCheck(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := New(ts.URL, 1*time.Second, 1)
	if !c.HealthCheck(context.Background()) {
		t.Error("expected HealthCheck to return true")
	}
}

func TestHealthCheck_ReturnsFalseWhenDown(t *testing.T) {
	c := New("http://localhost:1", 1*time.Second, 1) // nothing listening on port 1
	if c.HealthCheck(context.Background()) {
		t.Error("expected HealthCheck to return false when service is down")
	}
}

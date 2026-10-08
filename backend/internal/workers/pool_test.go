package workers

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nyctaxi/backend/internal/mlclient"
	"nyctaxi/backend/internal/models"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func sampleRequest() models.PredictionRequest {
	return models.PredictionRequest{
		PickupLatitude: 40.761, PickupLongitude: -73.982,
		DropoffLatitude: 40.730, DropoffLongitude: -73.995,
		PassengerCount: 2, PickupDatetime: "2016-03-15T18:30:00",
	}
}

// slowServer returns an httptest server that sleeps `delay` before
// responding, and counts concurrent in-flight requests via `concurrent`.
func slowServer(delay time.Duration, concurrent *int32, maxObserved *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(concurrent, 1)
		for {
			old := atomic.LoadInt32(maxObserved)
			if n <= old || atomic.CompareAndSwapInt32(maxObserved, old, n) {
				break
			}
		}
		time.Sleep(delay)
		atomic.AddInt32(concurrent, -1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"predicted_duration_seconds":100,"model_version":"test"}`))
	}))
}

func TestPool_BoundsConcurrencyToWorkerCount(t *testing.T) {
	var concurrent, maxObserved int32
	ts := slowServer(150*time.Millisecond, &concurrent, &maxObserved)
	defer ts.Close()

	ml := mlclient.New(ts.URL, 2*time.Second, 1)
	pool := NewPool(ml, 50, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx, 3) // exactly 3 workers

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resultCh := make(chan JobResult, 1)
			job := PredictionJob{ID: "job", Ctx: context.Background(), Request: sampleRequest(), ResultCh: resultCh}
			if err := pool.Submit(job); err != nil {
				t.Errorf("unexpected submit error: %v", err)
				return
			}
			<-resultCh
		}()
	}
	wg.Wait()

	if maxObserved > 3 {
		t.Errorf("expected at most 3 concurrent requests to the ML service, observed %d", maxObserved)
	}
	if maxObserved < 2 {
		t.Errorf("expected workers to actually run concurrently, observed max %d", maxObserved)
	}
}

func TestPool_SubmitReturnsQueueFullWhenSaturated(t *testing.T) {
	var concurrent, maxObserved int32
	ts := slowServer(500*time.Millisecond, &concurrent, &maxObserved)
	defer ts.Close()

	ml := mlclient.New(ts.URL, 2*time.Second, 1)
	// 1 worker, queue capacity 1: submit 3 jobs fast -> 3rd should be rejected
	pool := NewPool(ml, 1, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx, 1)

	results := make([]error, 3)
	for i := 0; i < 3; i++ {
		resultCh := make(chan JobResult, 1)
		job := PredictionJob{ID: "job", Ctx: context.Background(), Request: sampleRequest(), ResultCh: resultCh}
		results[i] = pool.Submit(job)
	}

	rejected := 0
	for _, err := range results {
		if err == ErrQueueFull {
			rejected++
		}
	}
	if rejected == 0 {
		t.Error("expected at least one job to be rejected with ErrQueueFull when queue+worker saturated")
	}
}

func TestPool_GracefulShutdownDrainsInFlightJobs(t *testing.T) {
	var concurrent, maxObserved int32
	ts := slowServer(100*time.Millisecond, &concurrent, &maxObserved)
	defer ts.Close()

	ml := mlclient.New(ts.URL, 2*time.Second, 1)
	pool := NewPool(ml, 10, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx, 2)

	resultChans := make([]chan JobResult, 5)
	for i := range resultChans {
		resultChans[i] = make(chan JobResult, 1)
		job := PredictionJob{ID: "job", Ctx: context.Background(), Request: sampleRequest(), ResultCh: resultChans[i]}
		if err := pool.Submit(job); err != nil {
			t.Fatalf("submit failed: %v", err)
		}
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	if err := pool.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("expected clean shutdown (all jobs drained), got: %v", err)
	}

	// after Shutdown returns, every job must have produced a result
	for i, ch := range resultChans {
		select {
		case res := <-ch:
			if res.Err != nil {
				t.Errorf("job %d: unexpected error: %v", i, res.Err)
			}
		default:
			t.Errorf("job %d: no result available after graceful shutdown claimed completion", i)
		}
	}
}

func TestPool_SubmitRejectsAfterShutdownBegins(t *testing.T) {
	ml := mlclient.New("http://localhost:1", 1*time.Second, 1)
	pool := NewPool(ml, 10, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx, 1)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	pool.Shutdown(shutdownCtx)

	resultCh := make(chan JobResult, 1)
	job := PredictionJob{ID: "late-job", Ctx: context.Background(), Request: sampleRequest(), ResultCh: resultCh}
	if err := pool.Submit(job); err != ErrPoolShuttingDown {
		t.Errorf("expected ErrPoolShuttingDown after shutdown, got: %v", err)
	}
}

func TestPool_StatsReflectsQueueAndWorkers(t *testing.T) {
	ml := mlclient.New("http://localhost:1", 1*time.Second, 1)
	pool := NewPool(ml, 5, testLogger())
	stats := pool.Stats()
	if stats.QueueCapacity != 5 {
		t.Errorf("expected queue capacity 5, got %d", stats.QueueCapacity)
	}
	if stats.QueueLength != 0 || stats.BusyWorkers != 0 {
		t.Errorf("expected empty/idle pool at start, got %+v", stats)
	}
}

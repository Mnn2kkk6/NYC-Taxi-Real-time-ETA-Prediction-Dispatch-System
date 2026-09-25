// Package workers implements a bounded worker pool for prediction jobs.
//
// This is the core concurrency piece: instead of the HTTP handler calling
// the ML service directly (blocking that goroutine for the full round
// trip), it enqueues a job and a fixed number of worker goroutines pull
// from the queue and do the actual work. This bounds concurrency to
// exactly N in-flight calls to the Python service, no matter how many
// HTTP requests arrive at once — the queue absorbs bursts, and a full
// queue fails fast instead of spawning unbounded goroutines.
package workers

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"nyctaxi/backend/internal/mlclient"
	"nyctaxi/backend/internal/models"
)

// ErrQueueFull is returned by Submit when the buffered job channel is at
// capacity — the caller (HTTP handler) should turn this into a 503,
// applying backpressure instead of blocking or spawning more goroutines.
var ErrQueueFull = errors.New("job queue is full")

// ErrPoolShuttingDown is returned by Submit after Shutdown has begun.
var ErrPoolShuttingDown = errors.New("worker pool is shutting down")

type JobResult struct {
	Response *models.PredictionResponse
	Err      error
}

type PredictionJob struct {
	ID        string
	Ctx       context.Context // derived from the originating HTTP request
	Request   models.PredictionRequest
	ResultCh  chan JobResult // buffered size 1 — worker never blocks sending
	CreatedAt time.Time
}

type Pool struct {
	jobs chan PredictionJob
	ml   *mlclient.Client
	log  *slog.Logger

	wg sync.WaitGroup

	busyWorkers int32 // atomic
	closed      int32 // atomic bool: 0=open, 1=closed
}

func NewPool(ml *mlclient.Client, queueSize int, log *slog.Logger) *Pool {
	return &Pool{
		jobs: make(chan PredictionJob, queueSize),
		ml:   ml,
		log:  log,
	}
}

// Start launches numWorkers goroutines that consume from the job queue
// until either the queue is closed (graceful shutdown) or ctx is
// cancelled (immediate shutdown, e.g. deadline exceeded during drain).
func (p *Pool) Start(ctx context.Context, numWorkers int) {
	for i := 0; i < numWorkers; i++ {
		p.wg.Add(1)
		go p.runWorker(ctx, i)
	}
}

func (p *Pool) runWorker(ctx context.Context, id int) {
	defer p.wg.Done()
	for {
		select {
		case job, ok := <-p.jobs:
			if !ok {
				return // channel closed and drained: graceful exit
			}
			p.process(job, id)
		case <-ctx.Done():
			return
		}
	}
}

func (p *Pool) process(job PredictionJob, workerID int) {
	atomic.AddInt32(&p.busyWorkers, 1)
	defer atomic.AddInt32(&p.busyWorkers, -1)

	start := time.Now()
	resp, err := p.ml.Predict(job.Ctx, job.Request)
	latency := time.Since(start)

	if err != nil {
		p.log.Error("worker: prediction failed",
			"worker_id", workerID, "job_id", job.ID, "latency_ms", latency.Milliseconds(), "error", err)
	} else {
		p.log.Info("worker: prediction completed",
			"worker_id", workerID, "job_id", job.ID, "latency_ms", latency.Milliseconds())
	}

	// Send result without blocking forever: if the caller's context is
	// already done (client disconnected / request timed out), there's
	// nobody left to receive — drop the result rather than leak this
	// goroutine's time waiting on a channel nobody reads.
	select {
	case job.ResultCh <- JobResult{Response: resp, Err: err}:
	case <-job.Ctx.Done():
		p.log.Info("worker: caller gone, dropping result", "job_id", job.ID)
	}
}

// Submit enqueues a job. Returns ErrQueueFull immediately if the buffered
// channel is at capacity — this is the backpressure mechanism: callers
// get a fast, explicit failure instead of the queue growing unbounded.
func (p *Pool) Submit(job PredictionJob) error {
	if atomic.LoadInt32(&p.closed) == 1 {
		return ErrPoolShuttingDown
	}
	select {
	case p.jobs <- job:
		return nil
	default:
		return ErrQueueFull
	}
}

// Shutdown stops accepting new jobs, closes the queue so workers drain
// remaining jobs and exit, then waits for all workers to finish — or
// until ctx's deadline, whichever comes first.
func (p *Pool) Shutdown(ctx context.Context) error {
	atomic.StoreInt32(&p.closed, 1)
	close(p.jobs)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats reports current queue depth, queue capacity, and busy worker
// count — backs the GET /api/v1/system/status endpoint.
type Stats struct {
	QueueLength   int   `json:"queue_length"`
	QueueCapacity int   `json:"queue_capacity"`
	BusyWorkers   int32 `json:"busy_workers"`
}

func (p *Pool) Stats() Stats {
	return Stats{
		QueueLength:   len(p.jobs),
		QueueCapacity: cap(p.jobs),
		BusyWorkers:   atomic.LoadInt32(&p.busyWorkers),
	}
}

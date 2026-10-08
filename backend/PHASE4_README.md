# Phase 4 — Concurrency (Worker Pool + Job Queue) — DONE

This is the core "why Go" piece. `/api/v1/predict` no longer calls the ML
service directly from the HTTP handler — it enqueues a job into a bounded
worker pool and waits on a per-job result channel.

## New/changed files

```
backend/
├── internal/
│   ├── workers/
│   │   ├── pool.go        # the worker pool itself
│   │   └── pool_test.go   # 6 tests, run with -race
│   └── handlers/
│       └── handlers.go    # Predict() now goes through the pool; new SystemStatus()
└── cmd/server/main.go     # builds + starts the pool, drains it on shutdown
```

## How it works

```
HTTP request
      │
      ▼
  validate()
      │
      ▼
 pool.Submit(job)  ──► buffered channel (size = QUEUE_SIZE)
      │                         │
      │ (full? -> 503 fast)     ▼
      │                  N worker goroutines (N = PREDICTION_WORKERS)
      │                         │
      │                  ml.Predict() — the Phase 3 client
      │                  (its own timeout+retry+backoff)
      │                         │
      ▼                         ▼
  block on job.ResultCh  ◄── worker sends result back
      │
      ▼
  write HTTP response
```

Each `PredictionJob` carries the **originating request's `context.Context`**
(`job.Ctx = r.Context()`), so:
- if the client disconnects mid-queue or mid-processing, the worker notices
  via `<-job.Ctx.Done()` and drops the result instead of blocking forever
  trying to send it
- the handler itself also selects on `<-r.Context().Done()` while waiting,
  so a client timeout doesn't leave the HTTP goroutine hanging

## Backpressure, not unbounded growth

`Submit()` does a non-blocking send (`select` with `default`): if the
buffered channel is full, it returns `ErrQueueFull` immediately →  handler
returns `503 Service Unavailable`. This was a deliberate choice over
letting the queue grow forever or spawning a goroutine per request — a
saturated system should fail fast and visibly, not degrade silently.

## Graceful shutdown, in the right order

`main.go`'s shutdown sequence is now two steps:
1. `srv.Shutdown(ctx)` — HTTP server stops accepting new connections
2. `pool.Shutdown(ctx)` — closes the job queue (`close(p.jobs)`); every
   worker drains whatever's left in the channel, finishes those jobs, then
   exits; `Shutdown` blocks on a `sync.WaitGroup` until all workers are
   done (or its own timeout elapses)

This order matters: stopping new HTTP connections first means no new jobs
can be submitted while the pool is draining, so drain has a finite,
predictable end.

## GET /api/v1/system/status (new)

```json
{"queue_length": 2, "queue_capacity": 20, "busy_workers": 4}
```

Cheap, lock-free reads via `atomic.LoadInt32` / `len(channel)` — safe to
poll frequently for a dashboard.

## Real concurrency test (not a mock — actual load against both services)

Configured `PREDICTION_WORKERS=4`, `QUEUE_SIZE=20`, fired **30 concurrent**
requests at `/api/v1/predict`:

```
mid-flight: {"queue_length":2,"queue_capacity":20,"busy_workers":4}
```

`busy_workers` topped out at exactly 4 — proof the pool actually bounds
concurrent ML-service calls to the configured worker count, not just in
theory.

Final result across the 30 requests:
```
200 OK:                  24
503 Service Unavailable:  6   (queue was full — correct backpressure)
```

After all requests finished: `{"queue_length":0,"queue_capacity":20,"busy_workers":0}` —
pool returns cleanly to idle.

## Unit tests (real, `go test ./internal/workers/... -v -race`)

```
TestPool_BoundsConcurrencyToWorkerCount        PASS
TestPool_SubmitReturnsQueueFullWhenSaturated   PASS
TestPool_GracefulShutdownDrainsInFlightJobs    PASS
TestPool_SubmitRejectsAfterShutdownBegins      PASS
TestPool_StatsReflectsQueueAndWorkers          PASS
```
All pass **with Go's race detector enabled** — meaningful proof of no data
races in the concurrent code, not just "it worked once."

Full suite: `go test ./... -race` → **19/19 passed** (13 from Phase 3 + 6 new).

## How to reproduce the load test yourself

```powershell
# terminal 1
cd ml-service; venv\Scripts\activate; uvicorn app.main:app --port 8000

# terminal 2
cd backend
$env:PREDICTION_WORKERS="4"; $env:QUEUE_SIZE="20"; $env:ML_SERVICE_URL="http://localhost:8000"
go run ./cmd/server

# terminal 3 — fire concurrent requests (PowerShell)
1..30 | ForEach-Object -Parallel {
    try {
        Invoke-RestMethod -Uri "http://localhost:8080/api/v1/predict" -Method Post -ContentType "application/json" -Body '{"pickup_latitude":40.761,"pickup_longitude":-73.982,"dropoff_latitude":40.730,"dropoff_longitude":-73.995,"passenger_count":2,"pickup_datetime":"2016-03-15T18:30:00"}'
    } catch { $_.Exception.Response.StatusCode }
} -ThrottleLimit 30
```

While that's running, poll `http://localhost:8080/api/v1/system/status` from a 4th terminal to watch `busy_workers` and `queue_length` move in real time.

## Next: Phase 5 — Redis

Cache predictions by a hash of (pickup, dropoff, passenger_count, time
bucket), add cache-hit/miss metrics to `/api/v1/system/status`, and rate
limiting on `/api/v1/predict`. This is also where the Postgres swap
(replacing the in-memory `TripRepository`) makes sense to do, since Redis
needs the same "external service reachable from Go" pattern.

# Phase 3 — Go Backend — DONE

Core REST API in Go, calling the Phase 2 FastAPI service for predictions.
Lives in `backend/`, a sibling of `ml-service/`.

## Folder structure

```
backend/
├── go.mod
├── cmd/server/main.go          # entry point: wiring, routes, graceful shutdown
├── internal/
│   ├── config/config.go        # env-var driven settings
│   ├── models/models.go        # request/response structs, Trip, TripStatus
│   ├── mlclient/
│   │   ├── client.go           # HTTP client to Python: timeout + retry + backoff
│   │   └── client_test.go      # 7 tests against a real httptest server
│   ├── handlers/
│   │   ├── handlers.go         # /health, /predict, /trips handlers + validation
│   │   └── handlers_test.go    # 6 tests
│   ├── middleware/middleware.go # structured logging, panic recovery, CORS
│   └── repositories/trip_repository.go  # TripRepository interface + in-memory impl
└── pkg/idgen/idgen.go          # UUID v4 generator (stdlib crypto/rand)
```

## Key architecture decision: standard library only, no Gin/Chi/pgx yet

This sandbox has no network access to `proxy.golang.org`, so no external Go
module could be fetched and verified here. Rather than hand you untested
code that imports packages I couldn't actually compile against, everything
in this phase is built on Go's standard library:

- **Routing**: `net/http.ServeMux` with Go 1.22's method+pattern syntax
  (`mux.HandleFunc("POST /api/v1/predict", ...)`) — no Gin/Chi needed for
  routes this simple, and it's one less thing to explain as "just a
  framework did this" in an interview.
- **Persistence**: `TripRepository` is an interface
  (`internal/repositories/trip_repository.go`). The current implementation
  is in-memory (goroutine-safe via `sync.RWMutex`). A Postgres-backed
  implementation (via `pgx`) is a drop-in replacement — one new file, one
  line changed in `main.go`. Happy to write that file next; it just needs
  `go mod tidy` run on a machine with internet (yours) to fetch `pgx`.
- **UUIDs**: `pkg/idgen` uses `crypto/rand` directly instead of
  `google/uuid`, for the same reason.

Your machine has full internet access, so if you'd rather have the "real"
production stack (Gin, pgx, go-redis, zap) from the start, say so and I'll
rewrite these files against those libraries — you'd just be the one running
`go build` to catch any typos, since I can't verify it here.

## API

| Method | Path | Status |
|---|---|---|
| GET | `/health` | Done — reports own status + `ml_service_up` |
| POST | `/api/v1/predict` | Done — validates, forwards to ML service |
| POST | `/api/v1/trips` | Done — in-memory create |
| GET | `/api/v1/trips/{id}` | Done |
| GET | `/api/v1/trips?status=&limit=&offset=` | Done |
| GET | `/api/v1/drivers` | Not yet (Phase 6 — dispatch) |
| GET | `/api/v1/metrics` | Not yet (Phase 8 — Prometheus) |
| WS | `/ws` | Not yet (Phase 6 — realtime) |

## mlclient: timeout + retry + exponential backoff (spec §14)

`internal/mlclient/client.go` — one shared `*http.Client` with connection
pooling (never created per request). Each attempt gets a hard timeout via
`context.WithTimeout`; on failure it retries with backoff `100ms → 200ms →
400ms...`, capped at `ML_SERVICE_RETRIES` (default 3) — never retries
forever. The caller's `context.Context` is respected throughout: if it's
cancelled mid-retry, the client returns immediately instead of sleeping
through a dead request.

Verified with `httptest` (not mocked assumptions — real HTTP round trips):
- succeeds on first try when the server is healthy
- retries exactly N times then succeeds once the server recovers
- gives up after exactly `maxRetries` attempts when the server never recovers
- returns fast (<500ms) when the caller's context is already cancelled, instead of sleeping through retries
- returns an error when the server is slower than the configured timeout

## Real end-to-end test (both services running together)

```
=== health ===
{"status":"ok","ml_service_up":true,"timestamp_utc":"2026-09-24T09:56:57Z"}

=== predict ===
{"predicted_duration_seconds":1076,"predicted_duration_minutes":17.9,
 "distance_km":3.62,"model_version":"xgboost-v1", ...}

=== predict validation error (passenger_count=9) ===
{"error":"validation failed","detail":"passenger_count must be between 1 and 6"}

=== create trip -> get trip -> list trips ===
all round-tripped correctly with real UUIDs

=== get nonexistent trip ===
HTTP 404 {"error":"trip not found", ...}

=== graceful shutdown (SIGTERM) ===
"shutdown signal received, draining connections..."
"server stopped cleanly"
```

Structured JSON logs confirmed for every request (`request_id`, `method`,
`path`, `status`, `latency_ms`) via the `middleware.Logging` wrapper.

## Test results (real, `go test ./... -v`)

```
13 passed
- handlers: 6/6 (validation logic + query param parsing)
- mlclient: 7/7 (retry/backoff/timeout/context-cancellation, against real httptest servers)
```

## How to run

Terminal 1 — ML service (Phase 2):
```bash
cd ml-service
uvicorn app.main:app --port 8000
```

Terminal 2 — Go backend:
```bash
cd backend
go mod tidy          # no external deps yet, so this is a no-op for now
go build ./cmd/server
go test ./... -v
$env:ML_SERVICE_URL="http://localhost:8000"   # PowerShell
go run ./cmd/server
```

Then: `curl http://localhost:8080/health`

## Env vars (all optional, sane defaults in `internal/config/config.go`)

| Var | Default | Purpose |
|---|---|---|
| `PORT` | `8080` | Go server port |
| `ML_SERVICE_URL` | `http://localhost:8000` | Python service base URL |
| `ML_SERVICE_TIMEOUT_MS` | `2000` | per-attempt timeout calling Python |
| `ML_SERVICE_RETRIES` | `3` | max retry attempts |
| `DATABASE_URL` | local Postgres DSN | not used yet (in-memory repo) |
| `PREDICTION_WORKERS` | `10` | reserved for Phase 4 worker pool |
| `QUEUE_SIZE` | `100` | reserved for Phase 4 job queue |

## Next: Phase 4 — Concurrency

`/api/v1/predict` currently calls the ML service directly and blocks until
it responds. Phase 4 inserts a bounded worker pool + buffered job queue
between "validate" and "forward", with `context` cancellation and graceful
shutdown that drains in-flight jobs — the core goroutines/channels story
for the interview.

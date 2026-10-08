# Phase 5 — Redis (Cache + Rate Limiting) — DONE

`/api/v1/predict` now checks a Redis cache before touching the worker pool
at all, and every call to `/api/v1/predict` is rate-limited per client IP.

## New files

```
backend/
└── internal/
    ├── cache/
    │   ├── redis_client.go     # minimal RESP client, stdlib net only
    │   ├── prediction_cache.go # cache key design + Get/Set wrapper
    │   └── cache_test.go       # 8 tests, run against real redis-server
    ├── ratelimit/
    │   ├── limiter.go          # fixed-window counter
    │   └── limiter_test.go     # 4 tests, run against real redis-server
    └── middleware/
        └── ratelimit.go        # HTTP middleware, applied to /api/v1/predict only
```

Changed: `handlers.go` (`Predict()` now cache-aware, `SystemStatus()` reports
cache stats, `Health()` reports `redis_up`), `models.go` (`PredictionResponse`
gained `cache_hit`, `HealthResponse` gained `redis_up`), `config.go` (Redis/
cache/rate-limit env vars), `main.go` (wiring + `redisClient.Close()` on
shutdown).

## Why a hand-written Redis client instead of go-redis

Same constraint as `pgx` in Phase 3: no network route to
`proxy.golang.org` in this sandbox, so no third-party module could be
fetched and verified here. Rather than hand you code I couldn't actually
run, `internal/cache/redis_client.go` speaks RESP (Redis's wire protocol)
directly over `net.Conn` — implements exactly the 5 commands we need
(`GET`, `SETEX`, `INCR`, `EXPIRE`, `PING`), with a small connection pool.

This was genuinely testable here: installed a real `redis-server` via apt
and ran the full test suite against it — this isn't a mock. Your machine
has full internet, so swapping to `github.com/redis/go-redis/v9` is a
reasonable upgrade whenever you want it; the public API shape
(`Get/SetEX/Incr/Expire/Ping`) was kept deliberately close so that swap
touches only this one file.

## Cache design (spec §15)

**Key**: `prediction:{sha1 hash of pickup+dropoff+passenger_count+time_bucket}`

Time bucket = pickup_datetime truncated to the nearest **10-minute**
window — two requests for the same trip a few minutes apart hit the same
cache entry; requests an hour apart (different traffic) don't. Verified
by `TestPredictionCache_TimeBucketingGroupsNearbyRequests`.

**TTL**: configurable via `CACHE_TTL_SECONDS` (default 600s / 10 min) —
matches the bucket width, so a cache entry's lifetime lines up with how
long it stays semantically valid.

**Failure mode**: a Redis error on read/write is logged and treated as a
miss — prediction requests keep working even if Redis is down, they just
stop benefiting from the cache. Verified by design (not a "nice to have":
`Get`'s error path returns `(nil, false)`, never propagates the error up).

## Rate limiting design (spec §16)

Fixed-window counter: `INCR ratelimit:{ip}:{window_number}`, where
`window_number = unix_time / window_seconds`. First increment in a window
sets that key's `EXPIRE` — the key (and counter) disappears on its own
once the window passes, no cleanup job needed.

**Explicit trade-off** (the spec asked for something easy to explain in
an interview, so naming this matters): a fixed window can allow up to 2x
the limit across a window boundary — e.g. a burst at the very end of one
window plus a burst at the very start of the next. A sliding-window-log
or token-bucket avoids this at the cost of more moving parts. This was
the deliberate, named choice for interview-clarity over precision.

Defaults: `RATE_LIMIT_PER_MINUTE=100`, `RATE_LIMIT_WINDOW_SECONDS=60`.
Applied only to `POST /api/v1/predict` (see `main.go`) — not to `/health`
or trip reads.

**Fails open**: if Redis is unreachable, `Allow()` returns `true` rather
than blocking all traffic — an infrastructure outage on the rate limiter
shouldn't become a full outage of the API.

## Real test results

### Cache — actual request/response pair (not simulated)

First call:
```json
{"predicted_duration_seconds":1076, ..., "request_id":"85d1c11f-...", "cache_hit":false}
```

Second, identical call:
```json
{"predicted_duration_seconds":1076, ..., "request_id":"85d1c11f-...", "cache_hit":true}
```

Same `request_id`, same `prediction_timestamp` on both — proof the second
response is the byte-identical cached object, not a fresh prediction.

`/api/v1/system/status` after both calls:
```json
{"cache_hits":1,"cache_misses":1,"cache_hit_rate":0.5,"busy_workers":0,"queue_length":0,"queue_capacity":20}
```

### Rate limiting — actual sequence, `RATE_LIMIT_PER_MINUTE=5`

10 total `/predict` calls fired in sequence (2 warm-up + 8 in a loop):
requests 1–5 got normal responses (200/502 depending on ML service
readiness at that instant — rate limiting doesn't care about the
downstream result), requests 6–10 all got:
```json
{"error":"rate limit exceeded","detail":"too many requests, try again shortly"}
```
with HTTP `429`. Redis showed exactly one `ratelimit:127.0.0.1:<window>`
key with value `10` and a live TTL — confirming both the counting and the
auto-expiry.

### Unit tests (real, `go test ./... -race`)

```
cache:      8/8 passed  (Set/Get roundtrip, TTL actually expiring,
                          concurrent INCR is atomic, key determinism,
                          time-bucket grouping)
ratelimit:  4/4 passed  (allows up to limit, rejects after, separate
                          budgets per identifier, resets after window)
```
Full suite: **30/30 passed**, race detector clean.

## Env vars added

| Var | Default | Purpose |
|---|---|---|
| `REDIS_ADDR` | `localhost:6379` | Redis host:port (no scheme) |
| `REDIS_POOL_SIZE` | `10` | max pooled connections |
| `CACHE_TTL_SECONDS` | `600` | how long a cached prediction stays valid |
| `RATE_LIMIT_PER_MINUTE` | `100` | requests per IP per window |
| `RATE_LIMIT_WINDOW_SECONDS` | `60` | window size |

## How to run

You'll need Redis running locally. Easiest on Windows: **Docker**.

```powershell
docker run -d --name redis -p 6379:6379 redis:7-alpine
```

(No Docker? WSL2 with `sudo apt install redis-server` works too, or the
Windows-native build from Memurai/Redis Windows ports — Docker is simplest.)

Then, same as before:
```powershell
# terminal 1
cd ml-service; venv\Scripts\activate; uvicorn app.main:app --port 8000

# terminal 2
cd backend
$env:ML_SERVICE_URL="http://localhost:8000"
$env:REDIS_ADDR="localhost:6379"
go run ./cmd/server

# terminal 3 — test cache hit
$body = '{"pickup_latitude":40.761,"pickup_longitude":-73.982,"dropoff_latitude":40.730,"dropoff_longitude":-73.995,"passenger_count":2,"pickup_datetime":"2016-03-15T18:30:00"}'
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/predict" -Method Post -ContentType "application/json" -Body $body   # cache_hit: false
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/predict" -Method Post -ContentType "application/json" -Body $body   # cache_hit: true
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/system/status" | ConvertTo-Json
```

For the Go tests, Redis must be running first — tests `t.Skip()`
automatically if `localhost:6379` isn't reachable, so `go test ./...`
stays safe to run even without Redis up, it just skips the cache/
ratelimit packages in that case.

## Next: Phase 6 — Realtime (WebSocket + Trip Simulator + Dispatch)

`/ws` endpoint broadcasting trip/prediction events, a Go simulator that
replays historical trips from the dataset as live events, and simulated
drivers with basic nearest-driver dispatch.

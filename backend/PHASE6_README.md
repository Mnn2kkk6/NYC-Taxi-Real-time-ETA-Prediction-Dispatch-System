# Phase 6 — Realtime: WebSocket + Trip Simulator + Driver Dispatch — DONE

The system is now genuinely "live": a WebSocket connection at `/ws`
broadcasts trip and prediction events, simulated drivers get dispatched
to trips by nearest-available search, and a standalone Trip Simulator
replays real historical NYC taxi trips as live traffic against the API.

## New files

```
backend/
├── internal/
│   ├── wsserver/
│   │   ├── websocket.go     # hand-rolled RFC 6455 (handshake + framing)
│   │   ├── hub.go           # client registry + broadcast
│   │   └── websocket_test.go # 5 tests, real TCP handshakes
│   └── dispatch/
│       ├── driver.go        # Driver model, in-memory repo, nearest-available search
│       └── driver_test.go   # 6 tests
├── pkg/geo/geo.go           # haversine (mirrors ml-service/app/features.py)
└── simulator/cmd/simulator/main.go  # replays train.csv as live traffic
```

Changed: `handlers.go` (`WS`, `Drivers` handlers; `CreateTrip` now
dispatches + broadcasts; `Predict` now broadcasts), `main.go` (wires hub +
driver repo, seeds drivers, new routes), `config.go` (`DRIVER_COUNT`),
**`middleware.go`** (bugfix — see below).

## Why a hand-written WebSocket server

Go's standard library has **no** WebSocket support at all (unlike HTTP,
which `net/http` covers) — every real Go project uses
`gorilla/websocket` or `golang.org/x/net/websocket` for this. Same
constraint as Redis and Postgres: no route to `proxy.golang.org` in this
sandbox to fetch either. So `internal/wsserver/websocket.go` implements
RFC 6455 directly over a hijacked TCP connection (`http.Hijacker`):
the opening handshake (SHA-1 + base64 of the client's key + a magic
GUID), and frame reading/writing (opcode, mask bit, 7/16/64-bit length
encoding, XOR-unmasking client frames).

This is verified against the protocol spec itself, not just "it seemed
to work": `TestComputeAccept_KnownRFC6455Example` checks the exact
key/accept pair from RFC 6455 §1.3's worked example, byte for byte.

Your machine has full internet, so swapping to `gorilla/websocket` is a
reasonable upgrade whenever you want the fragmented-message/compression
support this minimal version doesn't have. For a server-push broadcast
channel (which is all `/ws` needs to be), this implementation is
complete and tested.

## Bug found and fixed: middleware broke the WebSocket upgrade

First end-to-end test of `/ws` returned `400 Bad Request` instead of
`101 Switching Protocols`. Root cause: `middleware.Logging` wraps
`http.ResponseWriter` in a `responseRecorder` struct to capture the
status code for logging. Embedding `http.ResponseWriter` (an interface)
promotes only the methods *that interface* declares — `Header()`,
`Write()`, `WriteHeader()` — not `Hijack()`, even though the concrete
value underneath does implement `http.Hijacker`. So `w.(http.Hijacker)`
inside `wsserver.Upgrade()` failed for every request that passed through
the logging middleware, silently, until `/ws` was actually tested.

Fix: `responseRecorder` now has its own `Hijack()` method that forwards
to the underlying `ResponseWriter`'s `Hijacker` if it has one:
```go
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
    hj, ok := r.ResponseWriter.(http.Hijacker)
    if !ok {
        return nil, nil, fmt.Errorf("underlying ResponseWriter does not support http.Hijacker")
    }
    return hj.Hijack()
}
```
This is a real, general Go gotcha worth knowing: wrapping
`http.ResponseWriter` for logging/metrics is common, and it silently
breaks anything needing `Hijacker` (WebSockets, some streaming protocols)
unless you explicitly forward it.

## Driver Dispatch (spec §19)

`internal/dispatch/driver.go`: drivers have `AVAILABLE`/`BUSY`/`OFFLINE`
status. `FindNearestAvailable` is a linear scan computing haversine
distance to every `AVAILABLE` driver — deliberately not a spatial index,
matching the spec's explicit scope ("Không cần làm route optimization
quá phức tạp"). `POST /api/v1/trips` now calls this automatically: on
success the driver flips to `BUSY`, the trip's `driver_id` is set and its
status becomes `DISPATCHED`; if no driver is free, the trip stays
`PENDING` rather than failing the request.

## WebSocket events broadcast

| Event | Trigger | Payload |
|---|---|---|
| `trip_created` | `POST /api/v1/trips` | `{type, trip_id, status}` |
| `trip_prediction` | `POST /api/v1/predict` (cache hit or miss) | `{type, trip_id, status, eta_minutes, cache_hit, model_version}` |

## Trip Simulator (spec §18)

`simulator/cmd/simulator/main.go` — a separate binary (same Go module,
under `backend/simulator/`, rather than a fully separate module: simpler
build, still just as testable, worth knowing this deviates from the
original folder sketch). Reads the Kaggle CSV, replays rows against
`POST /api/v1/trips` at a configurable speed multiplier, using a bounded
pool of sender goroutines — the same "don't spawn one goroutine per unit
of work" discipline as the prediction worker pool.

**Bug found and fixed before the first real run**: the Kaggle dataset is
**not sorted by `pickup_datetime`** — confirmed by checking (rows span
January to June 2016 in arbitrary order even within the first few hundred
lines). Reading the file's first N rows and sorting only those would give
a random sample spanning 6 months, making any speed multiplier
meaningless (gaps between "consecutive" replayed trips would themselves
be random). Fixed by reading the **entire** file, sorting fully by
`pickup_datetime`, and only then slicing to `--limit` — giving a
real, tightly-clustered slice of history, exactly what the speed
multiplier is meant to compress.

```
go run ./simulator/cmd/simulator \
  --file ../data/train.csv \
  --speed 100 \
  --limit 500 \
  --target http://localhost:8080 \
  --workers 20
```

## Real end-to-end test results

### WebSocket (real TCP client, not the Go test suite)

A standalone client dialed `/ws`, completed the handshake
(`HTTP/1.1 101 Switching Protocols`), then 3 `POST /api/v1/trips` and 1
`POST /api/v1/predict` were fired. The client received, over the wire:

```
[msg 1] {"status":"DISPATCHED","trip_id":"8cd91c32-...","type":"trip_created"}
[msg 2] {"status":"DISPATCHED","trip_id":"132d2912-...","type":"trip_created"}
[msg 3] {"status":"DISPATCHED","trip_id":"c93763fe-...","type":"trip_created"}
[msg 4] {"cache_hit":false,"eta_minutes":17.9,"model_version":"xgboost-v1","status":"completed","trip_id":"55c34af5-...","type":"trip_prediction"}
```

### Trip Simulator against real data

30 real historical trips (sorted, contiguous slice of the actual
dataset), replayed at 50x speed:

```
loaded 30 trips ..., replaying at 50x speed against http://localhost:8080 with 5 workers
done: 30 sent, 0 failed, total wall time 8.201s
```

Result — dispatch working exactly as designed:
```
trip status distribution: {'DISPATCHED': 20, 'PENDING': 10}
driver status distribution: {'BUSY': 20}
```
20 drivers were seeded; exactly 20 trips got dispatched (one driver each,
all now `BUSY`); the remaining 10 trips correctly stayed `PENDING` once
the driver pool was exhausted — a realistic scarcity outcome, not a bug.

### Unit tests (real, `go test ./... -race`)

```
41 passed, 0 failed, 0 skipped
```
New this phase: `wsserver` 5/5 (real handshake, multi-client broadcast,
disconnect cleanup, client→server frame handling, RFC 6455 worked
example), `dispatch` 6/6 (nearest-driver selection, busy drivers
correctly skipped, no-drivers-available error, status transitions).

## Env vars added

| Var | Default | Purpose |
|---|---|---|
| `DRIVER_COUNT` | `20` | simulated drivers seeded at startup, randomly placed in the NYC bounding box |

## How to run

```powershell
# terminal 1 — ml-service
cd ml-service; venv\Scripts\activate; uvicorn app.main:app --port 8000

# terminal 2 — backend
cd backend
$env:ML_SERVICE_URL="http://localhost:8000"
$env:REDIS_ADDR="localhost:6379"
go run ./cmd/server

# terminal 3 — check drivers seeded
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/drivers" | Measure-Object | Select Count

# terminal 3 — run the simulator against real data
cd backend
go run ./simulator/cmd/simulator --file ..\data\train.csv --speed 50 --limit 30 --target http://localhost:8080 --workers 5

# terminal 3 — check results
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/trips?limit=30" | Group-Object status
Invoke-RestMethod -Uri "http://localhost:8080/api/v1/drivers" | Group-Object status
```

A real WebSocket client needs a bit more than `Invoke-RestMethod`
(PowerShell has no built-in WS client) — easiest is a one-line browser
console check:
```javascript
const ws = new WebSocket("ws://localhost:8080/ws");
ws.onmessage = (e) => console.log(e.data);
```
Open that in any browser's dev console while trips/predictions are
happening, and watch events arrive live.

## Next: Phase 7 — Frontend (React dashboard)

Trip Prediction page (form + result), Realtime Trips view (subscribing to
`/ws`), Monitoring charts (`/api/v1/system/status` polled or pushed), and
a map (Leaflet/OpenStreetMap) showing drivers and active trips — the
first phase where the WebSocket connection actually gets a UI consumer
instead of a throwaway test client.

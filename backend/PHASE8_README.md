# Phase 8 — Observability (Prometheus + Grafana) — DONE

`GET /api/v1/metrics` now exposes real Prometheus metrics. This is also
where the numbers the React dashboard (Phase 7) was missing — requests/sec,
p95 latency, error rate — actually come from.

## New files

```
backend/
├── internal/
│   ├── metrics/
│   │   ├── metrics.go        # hand-rolled Counter/Gauge/Histogram + exposition format
│   │   ├── app_metrics.go    # the actual metrics this app exposes (spec §21's list)
│   │   └── metrics_test.go   # 11 tests
│   └── middleware/
│       └── metrics.go        # per-route request count + latency middleware
└── infra/
    ├── prometheus/prometheus.yml           # scrape config
    └── grafana/
        ├── datasources/prometheus.yml       # auto-provisions the Prometheus datasource
        └── dashboards/
            ├── dashboard-provider.yml       # tells Grafana where to find dashboards
            └── nyc-taxi-dashboard.json      # the actual dashboard (7 panels)
```

Changed: `handlers.go` (cache counters now go through `metrics.App`, new
`Metrics()` handler), `workers/pool.go` (records prediction latency +
errors), `repositories/trip_repository.go` (new `ActiveCount()` method),
`main.go` (wires gauges, registers `/api/v1/metrics`, wraps routes with
the metrics middleware).

## Why a hand-written metrics library

Same constraint as every external dependency so far: no route to
`proxy.golang.org`, so `prometheus/client_golang` (the real library every
Go project uses for this) couldn't be fetched here. `internal/metrics`
implements the same core primitives — `Counter`, `Gauge`, `Histogram`,
and labeled vectors of each — and writes the real [Prometheus text
exposition format](https://prometheus.io/docs/instrumenting/exposition_formats/).

This was verified against the real thing, not just the docs: this
sandbox doesn't have internet access to `proxy.golang.org`, but
`prometheus` itself is a plain Ubuntu package (`apt install prometheus`)
with zero Go-module dependencies — installed it for real and had it
**scrape this exact server and answer real PromQL queries** (see results
below). Your machine has full internet, so swapping to
`prometheus/client_golang` is a reasonable upgrade any time — this
package's shape (`Counter.Inc()`, `Gauge.Set()`, `Histogram.Observe()`)
deliberately mirrors it so that swap is mostly mechanical.

One thing worth knowing: Prometheus's *histogram* type is specifically a
cumulative-bucket design (`histogram_bucket{le="0.1"}` counts everything
≤ 0.1, not just the 0.05–0.1 slice) — that's what makes
`histogram_quantile()` work in PromQL. Our `Histogram.Observe()` matches
that exactly (verified by `TestHistogram_BucketsAreCumulative`), not the
more intuitive-looking "count only this bucket" design.

## Metrics exposed (matching spec §21)

| Metric | Type | Labels | What it's for |
|---|---|---|---|
| `http_requests_total` | counter | `route, method, status` | request volume, error rate |
| `http_request_duration_seconds` | histogram | `route` | per-route latency, any percentile |
| `prediction_requests_total` | counter | `cache` (`hit`/`miss`) | cache effectiveness over time |
| `prediction_latency_seconds` | histogram | — | ML-service call latency, p50/p95/p99 |
| `prediction_errors_total` | counter | — | ML-service failures after retries exhausted |
| `cache_hits_total` / `cache_misses_total` | counter | — | same numbers `/system/status` already showed, now time-series |
| `queue_length` | gauge | — | worker pool queue depth, computed at scrape time |
| `worker_busy` | gauge | — | busy worker count, computed at scrape time |
| `active_trips` | gauge | — | trips currently PENDING or DISPATCHED |

`queue_length`, `worker_busy`, and `active_trips` are **computed gauges**
(`Gauge.SetFunc`) — read from the live worker pool / trip repository
state at the moment Prometheus scrapes, not kept in sync via a
background goroutine. One less moving part, and it can never drift stale.

## Route labels, not raw paths

`http_requests_total{route="predict"}`, not
`http_requests_total{path="/api/v1/predict"}`. Using the raw URL path
would create a new time series per trip ID for `/api/v1/trips/{id}`
(cardinality explosion) — `middleware.Metrics("route-name")` is applied
per-route in `main.go` with a fixed label instead, the same way
Prometheus's own best-practices guide recommends.

## Real test results (actual `prometheus` binary, actual PromQL)

Ran ml-service + Go backend + Redis + a real `prometheus` server (apt-
installed, scraping `/api/v1/metrics` every 2s), then: 6 unique
predictions (cache miss) → repeated the same 6 (cache hit) → 6 more new
ones (miss) → 1 trip created.

```
up{job="nyc-taxi-backend"}                                    = 1
cache_hits_total                                              = 6
http_requests_total{route="predict",method="POST",status="200"} = 12
active_trips                                                  = 1

histogram_quantile(0.95, rate(prediction_latency_seconds_bucket[2m]))
  = 0.00975   (9.75ms — real p95, computed by Prometheus from real buckets)

sum(rate(http_requests_total[2m]))
  = 0.1118    (requests/sec, computed by Prometheus)
```

**These two numbers — p95 latency and requests/sec — are exactly what
`PHASE7_README.md` said the React dashboard couldn't show** because the
backend didn't expose them yet. It does now; wiring the Grafana panels
(or adding a `/metrics`-polling widget to the React dashboard) is the
natural next step whenever you want it.

### An honest accident that doubled as a real test

Partway through testing, Redis was unreachable for one run — and sure
enough, `prediction_requests_total{cache="miss"}` showed **12** instead
of 6 for that run, with zero hits recorded. That's exactly the Phase 5
fail-open design working as intended (a cache outage degrades to "always
call the ML service," never breaks predictions) — caught by accident,
confirmed by the metrics themselves, not asserted by a test.

## Unit tests (real, `go test ./... -race`)

```
52 passed, 0 failed
```
New this phase — `internal/metrics`, 11/11: counter atomicity under
concurrent increments, gauge `SetFunc` laziness, label-combination
isolation in vectors, histogram bucket cumulativeness (the subtle part),
bucket sorting, and three tests asserting the actual exposition-format
text output is well-formed.

## Grafana

Not installed/tested in this sandbox (no `apt` package, and standing up
a browser-facing dashboard isn't verifiable without a browser here
anyway) — but the config is real and ready:

- `infra/grafana/datasources/prometheus.yml` auto-adds Prometheus as a
  datasource on Grafana startup (provisioning, not manual click-through)
- `infra/grafana/dashboards/nyc-taxi-dashboard.json` — 7 panels:
  requests/sec, p50/p95/p99 latency, error rate, queue/workers, cache hit
  rate (stat), active trips (stat), prediction hit-vs-miss rate

## How to run (Docker, since you already have Docker Desktop)

```powershell
# Prometheus
docker run -d --name prometheus -p 9090:9090 `
  -v "${PWD}\infra\prometheus\prometheus.yml:/etc/prometheus/prometheus.yml" `
  prom/prometheus

# Grafana, with datasource + dashboard auto-provisioned
docker run -d --name grafana -p 3000:3000 `
  -v "${PWD}\infra\grafana\datasources:/etc/grafana/provisioning/datasources" `
  -v "${PWD}\infra\grafana\dashboards:/etc/grafana/provisioning/dashboards" `
  grafana/grafana
```

Edit `infra/prometheus/prometheus.yml` first: since Prometheus runs
*inside* Docker and your Go server runs on the host, the target must be
`host.docker.internal:8080`, not `localhost:8080` (already set that way
in the committed file — only change it if you later run the backend in
Docker too, at which point `localhost` would be wrong in the other
direction).

Open Grafana at **http://localhost:3000** (default login `admin`/`admin`)
— the "NYC Taxi — Dispatch Console" dashboard should already be there
under Dashboards, no manual setup. Open Prometheus directly at
**http://localhost:9090/graph** to run ad-hoc PromQL, or
**http://localhost:9090/targets** to confirm the scrape target is `UP`.

Generate some traffic to see the panels move:
```powershell
cd backend
go run ./simulator/cmd/simulator --file ..\ml-service\data\train.csv --speed 50 --limit 100 --target http://localhost:8080
```

## Next: Phase 9 — Load Testing

k6 (or a Go benchmark) comparing the worker-pool implementation against a
naive synchronous one, at 10/100/500/1000 concurrent users — this is
where the "why Go" story gets its hard numbers (throughput, p95 under
load, CPU/memory), and where these new Prometheus metrics stop being
just nice-to-have and become the actual measurement tool for that
comparison.

# NYC Taxi Real-Time ETA Prediction & Dispatch System

A full-stack, containerized project for predicting NYC taxi trip duration, managing simulated drivers, dispatching the nearest available driver, and observing the system under load.

The project combines a Python machine-learning API, a Go backend, a React dashboard, PostgreSQL persistence, Redis caching/rate limiting, WebSockets, Prometheus, and Grafana.

## Project status

| Phase | Scope | Status |
|---|---|---|
| 1 | Data preparation and baseline ML workflow | Completed |
| 2 | Python FastAPI prediction service | Completed |
| 3 | Go REST API and ML-service client | Completed |
| 4 | Bounded worker pool and backpressure | Completed |
| 5 | Redis prediction cache and rate limiting | Completed |
| 6 | WebSocket events, simulator, and nearest-driver dispatch | Completed |
| 7 | React dispatch console | Completed |
| 8 | Prometheus metrics and Grafana dashboard | Completed |
| 9 | Load-testing tool and sync-vs-worker-pool mode | Completed |
| 10A | Docker Compose orchestration | Completed |
| 10B | PostgreSQL trip persistence | Completed |
| 10C | Persistent driver fleet and atomic dispatch | Merged; owner reports local startup/smoke checks successful |

Phase 10C was merged into the default main branch via [PR #3](https://github.com/Mnn2kkk6/NYC-Taxi-Real-time-ETA-Prediction-Dispatch-System/pull/3). The project owner reported that the local Docker stack and smoke checks succeeded on Windows.

## What it does

- **Trip ETA prediction:** sends pickup/drop-off coordinates, passenger count, and pickup time to a Python ML service and returns an estimated duration.
- **Bounded concurrent processing:** a configurable Go worker pool limits concurrent prediction calls. When its queue is full, the API can return HTTP 503 rather than queueing unlimited work.
- **Caching and rate limiting:** Redis caches matching predictions and enforces a configurable per-IP limit on the prediction endpoint.
- **Trip management:** create, retrieve, and list trips with lifecycle status.
- **Nearest-driver dispatch:** selects an available simulated driver using Haversine distance.
- **Persistent data:** PostgreSQL stores trip records and, in Phase 10C, driver IDs, locations, and statuses. The driver claim and corresponding trip update are performed in one database transaction.
- **Live UI updates:** a WebSocket channel broadcasts trip and prediction events to the frontend.
- **Observability:** Prometheus scrapes API metrics; Grafana is provisioned with a Prometheus data source and dashboard.
- **Historical-trip simulation:** an optional simulator profile can replay rows from a local train.csv file against the API.

## Architecture

~~~mermaid
flowchart LR
    U[Browser / React UI] -->|HTTP + WebSocket| N[Nginx in frontend container]
    N -->|REST / WS proxy| G[Go API]
    G -->|Prediction request| M[Python FastAPI ML service]
    G <--> R[(Redis)]
    G <--> P[(PostgreSQL)]
    G -->|metrics endpoint| PR[Prometheus]
    PR --> GR[Grafana]
    S[Optional trip simulator] -->|POST trips| G
~~~

### Technology stack

| Layer | Technologies | Responsibility |
|---|---|---|
| Frontend | React, Vite, JavaScript, Leaflet, Recharts | Prediction form, dashboard, trip table, driver/trip map |
| Backend API | Go, net/http, goroutines, worker pool | Validation, orchestration, trip APIs, dispatch, WebSockets |
| ML service | Python, FastAPI, Pandas, scikit-learn/XGBoost pipeline | Feature engineering and ETA inference |
| Persistence | PostgreSQL 16, pgx v5 | Durable trips and driver fleet |
| Cache / limits | Redis 7 | Prediction cache and fixed-window rate limiting |
| Monitoring | Prometheus, Grafana | Metrics, time series, operational dashboard |
| Local deployment | Docker, Docker Compose, Nginx | Build and run the services together |

## Quick start — Windows PowerShell

### 1. Open the project directory

If the repository is already cloned:

~~~powershell
cd C:\Users\ADMIN\Downloads\nyc-taxi-phase1-ml
~~~

### 2. Configure environment variables

The project root should contain .env. If it does not, create it from the example:

~~~powershell
Copy-Item .env.example .env
~~~

Open .env and check the host ports. If port 5432 is already used by another PostgreSQL instance, set this in .env:

~~~dotenv
POSTGRES_PORT=5433
~~~

Do not overwrite an existing .env that already contains your working local settings. Keep real passwords and other private settings out of Git.

### 3. Build and start the stack

~~~powershell
docker compose config
docker compose up --build -d
docker compose ps
~~~

Wait until the services with health checks report healthy. To inspect startup logs:

~~~powershell
docker compose logs --tail=100 backend postgres
~~~

### 4. Open the web UI and monitoring pages

Run these PowerShell commands to launch the pages in your default browser:

~~~powershell
Start-Process "http://localhost:8081"
Start-Process "http://localhost:8080/health"
Start-Process "http://localhost:9090"
Start-Process "http://localhost:3000"
~~~

| URL | Purpose |
|---|---|
| [http://localhost:8081](http://localhost:8081) | React dispatch console |
| [http://localhost:8080/health](http://localhost:8080/health) | Go backend health response |
| [http://localhost:8080/api/v1/drivers](http://localhost:8080/api/v1/drivers) | Driver list API |
| [http://localhost:8080/api/v1/trips](http://localhost:8080/api/v1/trips) | Trip list API |
| [http://localhost:8080/api/v1/system/status](http://localhost:8080/api/v1/system/status) | Worker-pool and cache status |
| [http://localhost:8080/api/v1/metrics](http://localhost:8080/api/v1/metrics) | Prometheus text metrics |
| [http://localhost:9090](http://localhost:9090) | Prometheus UI |
| [http://localhost:3000](http://localhost:3000) | Grafana UI |
| Host port configured by POSTGRES_PORT (5433 in this setup) | PostgreSQL access from Windows |

The frontend container proxies API and WebSocket requests to the Go backend. In Docker Compose, the Go backend reaches the ML service at http://ml-service:8000 and Redis at redis:6379; those are internal Docker-network addresses.

Grafana's default credentials when no .env overrides are present are admin / admin. Change the password before exposing the service beyond local development.

## How to use the web app

The frontend has four main pages:

| Page | Route | What to check |
|---|---|---|
| Prediction | / | Submit a prediction and inspect predicted minutes, distance, model version, request ID, and cache hit/miss |
| Dashboard | /dashboard | Queue length/capacity, busy workers, cache statistics, and live event feed |
| Trips | /trips | Created trips, statuses, and status filtering |
| Map | /map | Driver positions/statuses, pickup points, and trip lines |

Suggested manual smoke test:

1. Open the prediction page and submit a valid NYC trip.
2. Submit the same request again to exercise the cache; the second response may report a cache hit.
3. Open Trips and create a trip from the app or API.
4. Open Map and check driver markers and trip lines.
5. Open Dashboard while creating trips and watch the status/event feed update.
6. Restart the backend and confirm existing trips and driver IDs/statuses remain in PostgreSQL.

Map tiles require an internet connection in the browser. Exact prediction output depends on the model and supplied trip features.

## REST API

All endpoints below are served by the Go backend on port 8080 by default.

| Method | Path | Purpose |
|---|---|---|
| GET | /health | Backend health, ML-service status, Redis status |
| POST | /api/v1/predict | Validate input and return predicted trip duration |
| POST | /api/v1/trips | Create a trip and attempt dispatch |
| GET | /api/v1/trips | List trips; supports status, limit, offset |
| GET | /api/v1/trips/{id} | Retrieve one trip |
| GET | /api/v1/drivers | List drivers and their statuses |
| GET | /api/v1/system/status | Worker-pool and cache summary |
| GET | /api/v1/metrics | Prometheus metrics |
| GET (WebSocket upgrade) | /ws | Live trip/prediction events |

### Example: create a trip from PowerShell

~~~powershell
$body = @{
    pickup_lat = 40.761
    pickup_lng = -73.982
    dropoff_lat = 40.730
    dropoff_lng = -73.995
    passenger_count = 2
} | ConvertTo-Json

$trip = Invoke-RestMethod -Method Post -Uri "http://localhost:8080/api/v1/trips" -ContentType "application/json" -Body $body
$trip | Format-List
Invoke-RestMethod "http://localhost:8080/api/v1/trips/$($trip.id)"
~~~

A trip usually becomes DISPATCHED if an AVAILABLE driver can be claimed. Otherwise it remains PENDING. The current project does not yet expose a complete trip completion/cancellation workflow that returns a busy driver to AVAILABLE.

### Example: predict an ETA

~~~powershell
$body = @{
    pickup_latitude = 40.761
    pickup_longitude = -73.982
    dropoff_latitude = 40.730
    dropoff_longitude = -73.995
    passenger_count = 2
    pickup_datetime = "2016-03-15T18:30:00"
} | ConvertTo-Json

Invoke-RestMethod -Method Post -Uri "http://localhost:8080/api/v1/predict" -ContentType "application/json" -Body $body
~~~

Valid coordinates must be within the configured NYC bounding box, passenger count must be between 1 and 6, and pickup_datetime is required. POST /api/v1/predict is the endpoint currently protected by the configured rate limiter; it may return HTTP 429 if the request budget is exceeded or HTTP 503 if the prediction queue is full.

## Data persistence and database migrations

Compose uses named volumes for PostgreSQL, Redis, Prometheus, and Grafana. A regular docker compose down does not remove named volumes.

SQL migrations are mounted from:

- backend/infra/postgres/001_create_trips.sql
- backend/infra/postgres/002_create_drivers.sql

PostgreSQL executes files in its initialization directory only when its data directory is initialized for the first time. Therefore, adding a migration to the repo does not automatically run that migration on an existing volume. For an existing Phase 10B database, apply migration 002 once:

~~~powershell
Get-Content .\backend\infra\postgres\002_create_drivers.sql | docker compose exec -T postgres psql -U nyctaxi -d nyctaxi
~~~

Replace the database/user arguments if your .env uses different values. To inspect the stored data:

~~~powershell
docker compose exec postgres psql -U nyctaxi -d nyctaxi -c "SELECT status, COUNT(*) FROM trips GROUP BY status ORDER BY status;"
docker compose exec postgres psql -U nyctaxi -d nyctaxi -c "SELECT id, status, latitude, longitude FROM drivers ORDER BY id LIMIT 20;"
~~~

**Never run docker compose down -v unless you intend to delete the project's named-volume data.**

## Optional historical-trip simulator

The simulator requires a local CSV file at ml-service/data/train.csv by default. Start it with its Compose profile:

~~~powershell
docker compose --profile simulator up --build simulator
~~~

You can configure its input folder and replay settings via SIMULATOR_DATA_DIR, SIMULATOR_SPEED, SIMULATOR_LIMIT, and SIMULATOR_WORKERS in .env. The input directory must contain train.csv. If you just want to test the application, create trips with the API example above instead.

## Configuration

The main environment variables are documented in .env.example.

| Variable | Default | Description |
|---|---:|---|
| FRONTEND_PORT | 8081 | Host port for frontend |
| BACKEND_PORT | 8080 | Host port for Go API |
| POSTGRES_PORT | 5432 | Host port for PostgreSQL; set to 5433 if 5432 is occupied |
| POSTGRES_DB | nyctaxi | PostgreSQL database |
| POSTGRES_USER | nyctaxi | PostgreSQL user |
| POSTGRES_PASSWORD | example placeholder | Local-development password |
| DATABASE_URL | internal Docker DSN | Backend-to-Postgres connection string; internal port remains 5432 |
| PREDICTION_WORKERS | 10 | Maximum parallel prediction workers |
| QUEUE_SIZE | 100 | Buffered prediction queue capacity |
| ML_SERVICE_TIMEOUT_MS | 2000 | Timeout for an ML-service attempt |
| ML_SERVICE_RETRIES | 3 | Prediction client retry setting |
| DRIVER_COUNT | 20 | Initial simulated driver count for an empty fleet |
| CACHE_TTL_SECONDS | 600 | Redis prediction cache TTL |
| RATE_LIMIT_PER_MINUTE | 100 | Rate-limit budget per IP/window |
| RATE_LIMIT_WINDOW_SECONDS | 60 | Rate-limit window |
| PROMETHEUS_PORT | 9090 | Host port for Prometheus |
| GRAFANA_PORT | 3000 | Host port for Grafana |

Settings in .env override Compose defaults. Keep credentials local and do not commit the real .env file.

## Run tests

Run tests from the relevant service folder.

### Go backend

~~~powershell
cd backend
go test ./...
~~~

For race detection on concurrency-sensitive code:

~~~powershell
go test ./... -race
~~~

### Frontend

~~~powershell
cd frontend
npm install
npm run build
npm run lint
~~~

### ML service

Use the project's configured Python environment and installed dependencies:

~~~powershell
cd ml-service
python -m pytest
~~~

If test dependencies are not installed in the active Python environment, install the requirements listed by the ML service before running pytest. These test commands are provided as local verification steps; results depend on your environment.

## Monitoring and troubleshooting

Useful commands from the repository root:

~~~powershell
# Start or resume the project
docker compose up -d

# Rebuild images after code changes
docker compose up --build -d

# Check service state
docker compose ps

# Follow logs
docker compose logs -f backend
docker compose logs -f postgres

# Check backend health response
Invoke-RestMethod http://localhost:8080/health

# Restart only the backend
docker compose restart backend

# Stop services without deleting data
docker compose down
~~~

Common problems:

- **Backend unhealthy / restarting:** inspect docker compose logs --tail=200 backend; verify PostgreSQL, Redis, and ML-service are healthy.
- **Port 5432 already allocated:** add POSTGRES_PORT=5433 to .env. This changes the Windows host port only; containers still communicate with postgres:5432.
- **Missing drivers table:** apply migration 002 using the command above. Do not delete the volume.
- **UI loads but has no trips:** that is normal before any trips are created. Create one through the UI, API, or simulator.
- **Prediction returns 429:** wait for the current rate-limit window to reset or adjust local settings.
- **Prediction returns 503:** the worker queue may be full; check /api/v1/system/status and backend logs.
- **Map is missing tiles:** check browser internet access and the browser developer console.
- **Data is missing after a reset:** confirm that you did not run docker compose down -v or otherwise delete the named volume.

## Repository layout

~~~text
.
├── frontend/                     # React/Vite dispatch UI and Nginx config
├── backend/
│   ├── cmd/server/               # Go API entry point
│   ├── internal/
│   │   ├── cache/                # Redis client and prediction cache
│   │   ├── config/               # Environment configuration
│   │   ├── dispatch/              # Driver model and dispatch repositories
│   │   ├── handlers/              # REST handlers
│   │   ├── metrics/               # Prometheus exposition and metrics
│   │   ├── mlclient/              # Go-to-Python ML client
│   │   ├── middleware/            # Logging, CORS, metrics, rate limits
│   │   ├── models/                # API/domain types
│   │   ├── ratelimit/             # Rate-limit logic
│   │   ├── repositories/          # PostgreSQL trip repository
│   │   ├── workers/               # Prediction worker pool
│   │   └── wsserver/              # WebSocket hub and protocol handling
│   ├── infra/postgres/            # SQL migrations
│   ├── infra/prometheus/          # Prometheus scrape configuration
│   ├── infra/grafana/             # Provisioned Grafana datasource/dashboard
│   ├── simulator/                 # Historical trip simulator
│   └── tools/loadtest/            # Go HTTP load tester
├── ml-service/                    # Python FastAPI inference service
├── docker-compose.yml
├── .env.example
├── PHASE10A_README.md
├── PHASE10B_README.md
└── PHASE10C_README.md
~~~

## Phase-specific documentation

- [Phase 10A — Docker Compose](PHASE10A_README.md)
- [Phase 10B — PostgreSQL trip persistence](PHASE10B_README.md)
- [Phase 10C — persistent drivers and atomic dispatch](PHASE10C_README.md)
- [Phase 9 — load testing](backend/PHASE9_README.md)
- [Phase 8 — Prometheus and Grafana](backend/PHASE8_README.md)
- [Phase 7 — frontend](frontend/PHASE7_README.md)
- [Phase 6 — realtime and driver dispatch](backend/PHASE6_README.md)
- [Phase 5 — Redis cache and rate limiting](backend/PHASE5_README.md)
- [Phase 4 — worker pool](backend/PHASE4_README.md)
- [Phase 3 — Go backend](backend/PHASE3_README.md)
- ML-service phase notes are in ml-service/.

## Current limitations

- Driver assignment is based on straight-line Haversine distance, not road-network travel time or route optimization.
- The current API has no completed end-to-end trip lifecycle for marking trips completed/cancelled and returning drivers to AVAILABLE.
- The simulator is optional and needs an appropriate local CSV file.
- This is a local-development/demo setup. Review credentials, CORS, network exposure, and production deployment settings before public deployment.

## License

No license has been declared in this repository yet. Add a LICENSE file before redistributing the project under a specific open-source license.

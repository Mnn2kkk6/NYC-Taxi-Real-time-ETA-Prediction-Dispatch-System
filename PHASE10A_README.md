# Phase 10a — Docker Compose Integration

Phase 10a established Docker Compose service integration. Phase 10b now adds PostgreSQL persistence; see `PHASE10B_README.md` for database configuration, migration, and restart-persistence tests.

## Services

| Service | Purpose | Default host URL / port |
|---|---|---|
| frontend | React app served by Nginx; proxies API and WebSocket traffic | http://localhost:8081 |
| backend | Go API, worker pool, dispatch, metrics | http://localhost:8080 |
| ml-service | FastAPI + XGBoost ETA inference | internal port 8000 |
| redis | prediction cache and rate limiting | internal port 6379 |
| prometheus | scrapes backend metrics | http://localhost:9090 |
| grafana | provisioned dashboard and Prometheus datasource | http://localhost:3000 |
| simulator | optional historical trip replay | starts with the simulator profile |

The normal Compose startup does not start the simulator. A local Kaggle dataset file named **train.csv** is not committed to this repository.

## Start on Windows PowerShell

From the repository root:

```powershell
Copy-Item .env.example .env
docker compose config
docker compose up --build -d
docker compose ps
```

Edit **.env** to change host ports or the Grafana password. Defaults are intended for local development; change the Grafana password before exposing the service to another network. If a host port is already occupied, choose a free port in **.env** and rerun Compose.

Wait until the long-starting ML model and monitoring services become healthy:

```powershell
docker compose ps
docker compose logs --tail 100 ml-service
docker compose logs --tail 100 backend
```

## Smoke tests

The app uses same-origin requests in the production build. Nginx forwards **/api/** and **/health** to the Go backend and upgrades **/ws** for WebSocket.

1. Open the UI at http://localhost:8081.
2. Open Prometheus targets at http://localhost:9090/targets; **nyc-taxi-backend** should become **UP**.
3. Open Grafana at http://localhost:3000 and sign in using the credentials in **.env**. The dashboard and Prometheus datasource are provisioned from **backend/infra/grafana/**.
4. Run an API health check:

```powershell
Invoke-RestMethod http://localhost:8081/health
```

5. Create and list a trip through the same Nginx origin:

```powershell
$body = @{
  pickup_lat = 40.761
  pickup_lng = -73.982
  dropoff_lat = 40.730
  dropoff_lng = -73.995
  passenger_count = 2
} | ConvertTo-Json

Invoke-RestMethod -Uri "http://localhost:8081/api/v1/trips" -Method Post -ContentType "application/json" -Body $body
Invoke-RestMethod -Uri "http://localhost:8081/api/v1/trips" -Method Get
```

6. Open the Dashboard page and confirm the WebSocket connection is shown as connected. Make a prediction or create a trip to generate events and metrics.

### Run the simulator (optional)

1. Download the NYC taxi dataset used by the project and place **train.csv** in **ml-service/data**, or set **SIMULATOR_DATA_DIR** in **.env** to the directory containing the file.
2. Run:

```powershell
docker compose --profile simulator run --rm simulator
```

The simulator reads **/data/train.csv** in its container and sends trip requests to **http://backend:8080**. It is a one-shot job, so its container exits when the replay completes.

## Networking and health checks

- Container-to-container URLs use Compose service DNS: **http://ml-service:8000** and **redis:6379**.
- The browser calls relative paths in the production build. Nginx forwards those paths to the backend; browser code never tries to resolve Docker-only hostnames.
- Prometheus scrapes **backend:8080**, not **host.docker.internal**, because both services run on the Compose network.
- Backend startup waits for ML service and Redis health checks. The **/health** handler currently reports HTTP 200 even if a dependency is unavailable, so its Docker health check is a process/liveness smoke check rather than a complete dependency-readiness endpoint.
- Simulator is isolated behind the **simulator** profile because dataset files are local and are not committed.

## Known limitation for this phase

Trip persistence is handled by PostgreSQL in Phase 10b. Redis, Prometheus, Grafana, and PostgreSQL use named volumes. See `PHASE10B_README.md` for the current database behavior and validation steps.

## Stop / cleanup

```powershell
docker compose down
# Also delete persisted Redis/Prometheus/Grafana data (destructive):
docker compose down -v
```

Do not run **docker compose down -v** unless you intentionally want to delete the named volumes.

# Phase 10b — PostgreSQL persistence

Phase 10b replaces the Go API's in-memory trip repository with PostgreSQL. The existing HTTP endpoints remain the same. Trip records, nullable duration fields, `driver_id`, and lifecycle `status` are stored in the same row; assignment updates write `driver_id` and `status` together.

## What changes
- Add a PostgreSQL service and persistent named volume to Docker Compose.
- Initialize the `trips` table and indexes from `backend/infra/postgres/001_create_trips.sql` on first database initialization.
- Use pgx v5 and a PostgreSQL repository implementing the existing trip repository interface.
- Backend waits for PostgreSQL health before starting and pings the database during startup.
- Add `DATABASE_URL` to environment configuration.

## Run / upgrade on Windows PowerShell
From the repository root:

```powershell
docker compose config
docker compose up --build -d
docker compose ps
docker compose logs --tail=100 backend postgres
```

PostgreSQL data is stored in the named volume `postgres_data`. A normal `docker compose down` preserves it. **Do not use `docker compose down -v` unless you intend to delete all named-volume data.**

The SQL init script runs only when the Postgres data directory is first initialized. For a fresh development database, the table is created automatically. If you already created the volume before adding this migration, apply it manually:

```powershell
Get-Content .\backend\infra\postgres\001_create_trips.sql | docker compose exec -T postgres psql -U nyctaxi -d nyctaxi
```

## Persistence smoke test

1. Create a trip from the UI or call `POST /api/v1/trips`.
2. Save its returned `id`; confirm it appears in `GET /api/v1/trips`.
3. Restart only the backend: `docker compose restart backend`.
4. Call `GET /api/v1/trips` again. The same trip must still exist with the same status and `driver_id`.
5. Inspect rows directly:

```powershell
docker compose exec postgres psql -U nyctaxi -d nyctaxi -c "SELECT id,status,driver_id,created_at FROM trips ORDER BY created_at DESC LIMIT 10;"
```

## Scope and known limitation
Trip persistence is durable, but the simulated driver fleet and its AVAILABLE/BUSY statuses are still in memory. This phase persists the trip's assigned driver identifier and lifecycle status together; it does not yet make the driver fleet durable or implement a transactional, database-backed dispatch fleet. The current startup seeds simulated drivers, so their IDs can change after a backend restart even though historical trip records remain intact. A later extension could move drivers and dispatch assignment into PostgreSQL transactions.

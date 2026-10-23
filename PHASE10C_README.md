# Phase 10c — Persistent driver fleet and atomic dispatch

Phase 10c extends Phase 10b. PostgreSQL now stores both trips and simulated drivers. Driver IDs, coordinates, and statuses survive a backend/container restart; startup seeds a fleet only when the `drivers` table is empty.

## Scope

- Add `drivers` and an index for AVAILABLE drivers.
- Use a PostgreSQL-backed driver repository for `GET /api/v1/drivers`.
- Seed the initial fleet once, protected by a PostgreSQL advisory lock so simultaneous backend starts cannot create duplicate fleets.
- Assign drivers nearest-first using the existing Haversine distance.
- Claim a driver with a conditional `AVAILABLE → BUSY` update and change the associated trip from `PENDING → DISPATCHED` inside the same database transaction. Concurrent requests cannot claim the same driver; if another request claims a candidate first, the transaction tries the next-nearest candidate.
- Preserve old `driver_id` values from Phase 10b as OFFLINE placeholder rows when creating the persistent fleet, so historical trip references remain visible.

## Apply migration

The Compose init script automatically runs migrations only for a **new, empty PostgreSQL data volume**. Existing Phase 10b volumes need the new migration applied once. This does not drop or recreate data.

From the repository root in Windows PowerShell:

```powershell
docker compose up -d postgres
Get-Content .\backend\infra\postgres\002_create_drivers.sql | docker compose exec -T postgres psql -U nyctaxi -d nyctaxi
docker compose up --build -d
docker compose ps
docker compose logs --tail=100 backend postgres
```

If you changed `POSTGRES_USER` or `POSTGRES_DB` in your `.env`, substitute those values in the `psql` command. Do not run `docker compose down -v`; that removes named-volume data.

## Smoke tests

1. Check the driver fleet:

   ```powershell
   Invoke-RestMethod http://localhost:8080/api/v1/drivers
   ```

2. Create one trip, save its ID and driver ID, then check `GET /api/v1/trips/{id}`. A successful assignment should have `status = DISPATCHED` and a non-empty `driver_id`.

   ```powershell
   $body = @{
     pickup_lat = 40.761
     pickup_lng = -73.982
     dropoff_lat = 40.730
     dropoff_lng = -73.995
     passenger_count = 2
   } | ConvertTo-Json

   $trip = Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/v1/trips -ContentType "application/json" -Body $body
   $trip
   Invoke-RestMethod "http://localhost:8080/api/v1/trips/$($trip.id)"
   ```

3. Restart only the backend:

   ```powershell
   docker compose restart backend
   ```

   Re-run `GET /api/v1/drivers`. Existing driver IDs and statuses should remain unchanged; any driver assigned before restart should still be BUSY.

4. Check for duplicate assignments among persisted dispatched trips:

   ```powershell
   docker compose exec postgres psql -U nyctaxi -d nyctaxi -c "SELECT driver_id, COUNT(*) FROM trips WHERE status = 'DISPATCHED' AND driver_id IS NOT NULL GROUP BY driver_id HAVING COUNT(*) > 1;"
   ```

   The query should return no rows while the current lifecycle has no completion/release endpoint.

5. Review the backend logs for startup, migration, and dispatch errors:

   ```powershell
   docker compose logs --tail=100 backend postgres
   ```

## Validation status and limitations

This change is committed to a feature branch for review. Docker builds, Go tests, migration application, and concurrency tests have **not** been run in this authoring environment; run the commands above on the Windows/Docker machine before merging.

The current project exposes trip creation and driver listing, but does not yet expose trip-completion/cancellation endpoints that return a BUSY driver to AVAILABLE. The repository can update status, but the full lifecycle/release workflow is a future phase.

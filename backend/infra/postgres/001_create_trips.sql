-- Phase 10b: durable trip records. Mounted into PostgreSQL's init directory.
CREATE TABLE IF NOT EXISTS trips (
    id TEXT PRIMARY KEY,
    pickup_lat DOUBLE PRECISION NOT NULL,
    pickup_lng DOUBLE PRECISION NOT NULL,
    dropoff_lat DOUBLE PRECISION NOT NULL,
    dropoff_lng DOUBLE PRECISION NOT NULL,
    passenger_count INTEGER NOT NULL CHECK (passenger_count BETWEEN 1 AND 6),
    requested_at TIMESTAMPTZ NOT NULL,
    predicted_duration_sec INTEGER NULL CHECK (predicted_duration_sec IS NULL OR predicted_duration_sec >= 0),
    actual_duration_sec INTEGER NULL CHECK (actual_duration_sec IS NULL OR actual_duration_sec >= 0),
    status TEXT NOT NULL CHECK (status IN ('PENDING','DISPATCHED','IN_PROGRESS','COMPLETED','CANCELLED')),
    driver_id TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_trips_created_at ON trips (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trips_status_created_at ON trips (status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_trips_driver_id ON trips (driver_id) WHERE driver_id IS NOT NULL;

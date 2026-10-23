-- Phase 10c: durable simulated driver fleet.
CREATE TABLE IF NOT EXISTS drivers (
    id TEXT PRIMARY KEY,
    latitude DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN 40.5 AND 40.9),
    longitude DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -74.05 AND -73.70),
    status TEXT NOT NULL CHECK (status IN ('AVAILABLE', 'BUSY', 'OFFLINE')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_drivers_available
    ON drivers (status, id)
    WHERE status = 'AVAILABLE';

package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nyctaxi/backend/internal/models"
	"nyctaxi/backend/pkg/geo"
	"nyctaxi/backend/pkg/idgen"
)

// PostgresRepository stores the driver fleet durably and performs dispatch
// assignment atomically with the corresponding trip update.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(ctx context.Context, databaseURL string) (*PostgresRepository, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL for drivers: %w", err)
	}
	cfg.MaxConns = 10

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create driver postgres pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres for drivers: %w", err)
	}
	return &PostgresRepository{pool: pool}, nil
}

func (r *PostgresRepository) Close() {
	r.pool.Close()
}

// SeedIfEmpty inserts the initial simulated fleet only when the table is
// empty. The advisory transaction lock prevents two backend replicas from
// seeding duplicate fleets during simultaneous startup. Existing driver IDs,
// locations, and statuses are never reset on restart.
func (r *PostgresRepository) SeedIfEmpty(ctx context.Context, positions []Driver) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin driver seed transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(94123107)"); err != nil {
		return fmt.Errorf("lock driver seeding: %w", err)
	}

	var count int64
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM drivers").Scan(&count); err != nil {
		return fmt.Errorf("count existing drivers: %w", err)
	}
	if count > 0 {
		return tx.Commit(ctx)
	}

	for _, position := range positions {
		d := position
		if d.ID == "" {
			d.ID = idgen.NewID()
		}
		if d.Status == "" {
			d.Status = Available
		}
		_, err := tx.Exec(ctx,
			"INSERT INTO drivers (id, latitude, longitude, status) VALUES ($1, $2, $3, $4)",
			d.ID, d.Latitude, d.Longitude, string(d.Status),
		)
		if err != nil {
			return fmt.Errorf("seed driver %s: %w", d.ID, err)
		}
	}

	// Phase 10b may have persisted driver IDs while driver rows themselves
	// were still in memory. Preserve those historical references as OFFLINE
	// placeholders rather than allowing them to collide with the new fleet.
	_, err = tx.Exec(ctx, `
		INSERT INTO drivers (id, latitude, longitude, status)
		SELECT DISTINCT t.driver_id, 40.7128, -74.0060, 'OFFLINE'
		FROM trips t
		WHERE t.driver_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM drivers d WHERE d.id = t.driver_id)
		ON CONFLICT (id) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("preserve historical driver references: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit driver seed transaction: %w", err)
	}
	return nil
}

func (r *PostgresRepository) List(ctx context.Context) ([]*Driver, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT id, latitude, longitude, status FROM drivers ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list drivers: %w", err)
	}
	defer rows.Close()

	out := make([]*Driver, 0)
	for rows.Next() {
		var d Driver
		var status string
		if err := rows.Scan(&d.ID, &d.Latitude, &d.Longitude, &status); err != nil {
			return nil, fmt.Errorf("scan driver: %w", err)
		}
		d.Status = Status(status)
		out = append(out, &d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate drivers: %w", err)
	}
	return out, nil
}

func (r *PostgresRepository) FindNearestAvailable(ctx context.Context, lat, lon float64) (*Driver, float64, error) {
	drivers, err := r.List(ctx)
	if err != nil {
		return nil, 0, err
	}

	var nearest *Driver
	var nearestDist float64
	for _, d := range drivers {
		if d.Status != Available {
			continue
		}
		dist := geo.HaversineKm(lat, lon, d.Latitude, d.Longitude)
		if nearest == nil || dist < nearestDist || (dist == nearestDist && d.ID < nearest.ID) {
			copy := *d
			nearest = &copy
			nearestDist = dist
		}
	}
	if nearest == nil {
		return nil, 0, ErrNoAvailableDrivers
	}
	return nearest, nearestDist, nil
}

func (r *PostgresRepository) SetStatus(ctx context.Context, driverID string, status Status) error {
	tag, err := r.pool.Exec(ctx,
		"UPDATE drivers SET status = $2, updated_at = NOW() WHERE id = $1",
		driverID, string(status))
	if err != nil {
		return fmt.Errorf("update driver status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("driver %s not found", driverID)
	}
	return nil
}

type rankedDriver struct {
	driver   Driver
	distance float64
}

// AssignTrip tries AVAILABLE candidates in nearest-first order. Each claim is
// a conditional UPDATE, so concurrent requests cannot both change the same
// driver from AVAILABLE to BUSY. The driver status and trips row update share
// one transaction: either both persist or neither persists.
func (r *PostgresRepository) AssignTrip(ctx context.Context, tripID string, pickupLat, pickupLng float64) (*Driver, float64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("begin dispatch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	rows, err := tx.Query(ctx,
		"SELECT id, latitude, longitude FROM drivers WHERE status = $1 ORDER BY id",
		string(Available))
	if err != nil {
		return nil, 0, fmt.Errorf("find available drivers: %w", err)
	}

	candidates := make([]rankedDriver, 0)
	for rows.Next() {
		var d Driver
		if err := rows.Scan(&d.ID, &d.Latitude, &d.Longitude); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("scan available driver: %w", err)
		}
		candidates = append(candidates, rankedDriver{
			driver: d,
			distance: geo.HaversineKm(pickupLat, pickupLng, d.Latitude, d.Longitude),
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate available drivers: %w", err)
	}
	if len(candidates) == 0 {
		return nil, 0, ErrNoAvailableDrivers
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance == candidates[j].distance {
			return candidates[i].driver.ID < candidates[j].driver.ID
		}
		return candidates[i].distance < candidates[j].distance
	})

	for _, candidate := range candidates {
		var chosen Driver
		var status string
		err := tx.QueryRow(ctx, `
			UPDATE drivers
			SET status = $2, updated_at = NOW()
			WHERE id = $1 AND status = $3
			RETURNING id, latitude, longitude, status
		`, candidate.driver.ID, string(Busy), string(Available)).
			Scan(&chosen.ID, &chosen.Latitude, &chosen.Longitude, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			// Another request claimed this driver after our candidate read.
			continue
		}
		if err != nil {
			return nil, 0, fmt.Errorf("claim driver %s: %w", candidate.driver.ID, err)
		}
		chosen.Status = Status(status)

		tag, err := tx.Exec(ctx, `
			UPDATE trips
			SET driver_id = $2, status = $3
			WHERE id = $1 AND status = $4
		`, tripID, chosen.ID, string(models.TripDispatched), string(models.TripPending))
		if err != nil {
			return nil, 0, fmt.Errorf("persist trip assignment: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil, 0, fmt.Errorf("trip %s not found or no longer PENDING", tripID)
		}

		if err := tx.Commit(ctx); err != nil {
			return nil, 0, fmt.Errorf("commit dispatch assignment: %w", err)
		}
		return &chosen, candidate.distance, nil
	}

	// Every candidate was claimed by a concurrent request before its UPDATE.
	return nil, 0, ErrNoAvailableDrivers
}

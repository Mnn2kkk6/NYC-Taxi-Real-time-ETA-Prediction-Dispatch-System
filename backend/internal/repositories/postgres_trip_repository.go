package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"nyctaxi/backend/internal/models"
	"nyctaxi/backend/pkg/idgen"
)

// PostgresTripRepository stores trip lifecycle state durably in PostgreSQL.
type PostgresTripRepository struct { pool *pgxpool.Pool }

func NewPostgresTripRepository(ctx context.Context, databaseURL string) (*PostgresTripRepository, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil { return nil, fmt.Errorf("parse DATABASE_URL: %w", err) }
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil { return nil, fmt.Errorf("create postgres pool: %w", err) }
	if err := pool.Ping(ctx); err != nil { pool.Close(); return nil, fmt.Errorf("ping postgres: %w", err) }
	return &PostgresTripRepository{pool: pool}, nil
}

func (r *PostgresTripRepository) Close() { r.pool.Close() }

const tripColumns = `id, pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
	passenger_count, requested_at, predicted_duration_sec, actual_duration_sec,
	status, driver_id, created_at`

func scanTrip(row pgx.Row) (*models.Trip, error) {
	var t models.Trip
	var predicted, actual *int
	var driverID *string
	var status string
	if err := row.Scan(&t.ID, &t.PickupLat, &t.PickupLng, &t.DropoffLat, &t.DropoffLng,
		&t.PassengerCount, &t.RequestedAt, &predicted, &actual, &status, &driverID, &t.CreatedAt); err != nil {
		return nil, err
	}
	t.PredictedDurationSec, t.ActualDurationSec, t.DriverID = predicted, actual, driverID
	t.Status = models.TripStatus(status)
	return &t, nil
}

func (r *PostgresTripRepository) Create(ctx context.Context, t *models.Trip) error {
	if t.ID == "" { t.ID = idgen.NewID() }
	if t.RequestedAt.IsZero() { t.RequestedAt = time.Now().UTC() }
	if t.CreatedAt.IsZero() { t.CreatedAt = time.Now().UTC() }
	if t.Status == "" { t.Status = models.TripPending }
	query := `INSERT INTO trips (id,pickup_lat,pickup_lng,dropoff_lat,dropoff_lng,passenger_count,requested_at,predicted_duration_sec,actual_duration_sec,status,driver_id,created_at)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING ` + tripColumns
	saved, err := scanTrip(r.pool.QueryRow(ctx, query, t.ID, t.PickupLat, t.PickupLng, t.DropoffLat, t.DropoffLng,
		t.PassengerCount, t.RequestedAt, t.PredictedDurationSec, t.ActualDurationSec, string(t.Status), t.DriverID, t.CreatedAt))
	if err != nil { return fmt.Errorf("insert trip: %w", err) }
	*t = *saved
	return nil
}

func (r *PostgresTripRepository) GetByID(ctx context.Context, id string) (*models.Trip, error) {
	t, err := scanTrip(r.pool.QueryRow(ctx, "SELECT "+tripColumns+" FROM trips WHERE id=$1", id))
	if errors.Is(err, pgx.ErrNoRows) { return nil, fmt.Errorf("trip %s not found", id) }
	if err != nil { return nil, fmt.Errorf("get trip: %w", err) }
	return t, nil
}

func (r *PostgresTripRepository) List(ctx context.Context, status string, limit, offset int) ([]*models.Trip, error) {
	if limit <= 0 { limit = 100 }
	if limit > 500 { limit = 500 }
	if offset < 0 { offset = 0 }
	query := "SELECT "+tripColumns+" FROM trips WHERE ($1 = '' OR status = $1) ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3"
	rows, err := r.pool.Query(ctx, query, status, limit, offset)
	if err != nil { return nil, fmt.Errorf("list trips: %w", err) }
	defer rows.Close()
	out := make([]*models.Trip, 0)
	for rows.Next() {
		t, err := scanTrip(rows)
		if err != nil { return nil, fmt.Errorf("scan trip: %w", err) }
		out = append(out, t)
	}
	if err := rows.Err(); err != nil { return nil, fmt.Errorf("iterate trips: %w", err) }
	return out, nil
}

func (r *PostgresTripRepository) UpdateStatus(ctx context.Context, id string, status models.TripStatus) error {
	tag, err := r.pool.Exec(ctx, "UPDATE trips SET status=$2 WHERE id=$1", id, string(status))
	if err != nil { return fmt.Errorf("update trip status: %w", err) }
	if tag.RowsAffected() == 0 { return fmt.Errorf("trip %s not found", id) }
	return nil
}

// UpdateDispatch persists driver_id and status in the same SQL statement so
// the trip row cannot expose DISPATCHED without its assigned driver.
func (r *PostgresTripRepository) UpdateDispatch(ctx context.Context, id string, driverID *string, status models.TripStatus) error {
	tag, err := r.pool.Exec(ctx, "UPDATE trips SET driver_id=$2, status=$3 WHERE id=$1", id, driverID, string(status))
	if err != nil { return fmt.Errorf("update trip dispatch: %w", err) }
	if tag.RowsAffected() == 0 { return fmt.Errorf("trip %s not found", id) }
	return nil
}

func (r *PostgresTripRepository) ActiveCount(ctx context.Context) int {
	var count int
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM trips WHERE status IN ('PENDING','DISPATCHED')").Scan(&count); err != nil { return 0 }
	return count
}

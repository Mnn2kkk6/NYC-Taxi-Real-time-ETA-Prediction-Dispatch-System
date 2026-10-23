// Package dispatch implements simulated drivers and a simple
// nearest-available-driver assignment, per spec §19. Deliberately not
// route-optimized — straight-line (haversine) distance to the nearest
// AVAILABLE driver is the whole algorithm, matching the spec's explicit
// scope ("Không cần làm route optimization quá phức tạp").
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"nyctaxi/backend/pkg/geo"
	"nyctaxi/backend/pkg/idgen"
)

type Status string

const (
	Available Status = "AVAILABLE"
	Busy      Status = "BUSY"
	Offline   Status = "OFFLINE"
)

type Driver struct {
	ID        string  `json:"id"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Status    Status  `json:"status"`
}

// Store is the common driver-fleet interface used by HTTP handlers.
type Store interface {
	List(ctx context.Context) ([]*Driver, error)
	FindNearestAvailable(ctx context.Context, lat, lon float64) (*Driver, float64, error)
	SetStatus(ctx context.Context, driverID string, status Status) error
}

// TripAssigner is implemented by the PostgreSQL repository. It claims a
// driver and updates the trip row in one transaction, preventing concurrent
// requests from assigning the same AVAILABLE driver.
type TripAssigner interface {
	AssignTrip(ctx context.Context, tripID string, pickupLat, pickupLng float64) (*Driver, float64, error)
}

var ErrNoAvailableDrivers = errors.New("no available drivers")

// Repository is goroutine-safe (sync.RWMutex — reads, i.e. listing and
// nearest-driver search, are far more frequent than writes, so readers
// shouldn't block each other).
type Repository struct {
	mu      sync.RWMutex
	drivers map[string]*Driver
}

func NewRepository() *Repository {
	return &Repository{drivers: make(map[string]*Driver)}
}

// Seed populates n simulated drivers at random-ish positions within the
// NYC bounding box, all AVAILABLE. Used at startup so /api/v1/drivers and
// dispatch have something to work with immediately.
func (r *Repository) Seed(positions []Driver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range positions {
		d := d
		if d.ID == "" {
			d.ID = idgen.NewID()
		}
		if d.Status == "" {
			d.Status = Available
		}
		r.drivers[d.ID] = &d
	}
}

func (r *Repository) List(ctx context.Context) ([]*Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Driver, 0, len(r.drivers))
	for _, d := range r.drivers {
		copy := *d
		out = append(out, &copy)
	}
	return out, nil
}

// FindNearestAvailable scans all AVAILABLE drivers and returns the one
// with the smallest haversine distance to (lat, lon). O(n) linear scan —
// fine at the driver-fleet sizes a simulator generates; a real system at
// scale would use a spatial index (geohash bucket, R-tree), noted here
// rather than pre-optimized away.
func (r *Repository) FindNearestAvailable(ctx context.Context, lat, lon float64) (*Driver, float64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var nearest *Driver
	var nearestDist float64
	for _, d := range r.drivers {
		if d.Status != Available {
			continue
		}
		dist := geo.HaversineKm(lat, lon, d.Latitude, d.Longitude)
		if nearest == nil || dist < nearestDist {
			nearest = d
			nearestDist = dist
		}
	}
	if nearest == nil {
		return nil, 0, ErrNoAvailableDrivers
	}
	return nearest, nearestDist, nil
}

// SetStatus updates a driver's status (e.g. AVAILABLE -> BUSY on
// dispatch, BUSY -> AVAILABLE on trip completion).
func (r *Repository) SetStatus(ctx context.Context, driverID string, status Status) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.drivers[driverID]
	if !ok {
		return fmt.Errorf("driver %s not found", driverID)
	}
	d.Status = status
	return nil
}

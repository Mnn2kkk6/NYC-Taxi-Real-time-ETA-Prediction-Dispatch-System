// Package repositories defines the persistence interface for trips and
// provides an in-memory implementation.
//
// WHY in-memory for now: this sandbox has no network access to download
// the pgx Postgres driver (go mod tidy needs proxy.golang.org, which isn't
// reachable here). The interface below is exactly what a Postgres-backed
// implementation will satisfy in Phase 5 — swapping it in later is a
// one-file change, nothing else in the codebase needs to know.
// See postgres_trip_repository.go for that implementation, written against
// the real pgx v5 API — build and test it on your own machine where
// `go mod tidy` can reach the internet.
package repositories

import (
	"context"
	"fmt"
	"sync"

	"nyctaxi/backend/internal/models"
	"nyctaxi/backend/pkg/idgen"
)

type TripRepository interface {
	Create(ctx context.Context, t *models.Trip) error
	GetByID(ctx context.Context, id string) (*models.Trip, error)
	List(ctx context.Context, status string, limit, offset int) ([]*models.Trip, error)
	UpdateStatus(ctx context.Context, id string, status models.TripStatus) error
}

// MemoryTripRepository is a goroutine-safe in-memory store.
// sync.RWMutex is used because reads (List/GetByID) vastly outnumber
// writes (Create/UpdateStatus) under realistic load — RWMutex lets
// concurrent readers proceed without blocking each other.
type MemoryTripRepository struct {
	mu    sync.RWMutex
	trips map[string]*models.Trip
	order []string // preserves insertion order for List()
}

func NewMemoryTripRepository() *MemoryTripRepository {
	return &MemoryTripRepository{
		trips: make(map[string]*models.Trip),
	}
}

func (r *MemoryTripRepository) Create(ctx context.Context, t *models.Trip) error {
	if t.ID == "" {
		t.ID = idgen.NewID()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trips[t.ID] = t
	r.order = append(r.order, t.ID)
	return nil
}

func (r *MemoryTripRepository) GetByID(ctx context.Context, id string) (*models.Trip, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.trips[id]
	if !ok {
		return nil, fmt.Errorf("trip %s not found", id)
	}
	return t, nil
}

func (r *MemoryTripRepository) List(ctx context.Context, status string, limit, offset int) ([]*models.Trip, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*models.Trip
	// newest first
	for i := len(r.order) - 1; i >= 0; i-- {
		t := r.trips[r.order[i]]
		if status != "" && string(t.Status) != status {
			continue
		}
		result = append(result, t)
	}

	if offset >= len(result) {
		return []*models.Trip{}, nil
	}
	end := offset + limit
	if end > len(result) || limit <= 0 {
		end = len(result)
	}
	return result[offset:end], nil
}

func (r *MemoryTripRepository) UpdateStatus(ctx context.Context, id string, status models.TripStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.trips[id]
	if !ok {
		return fmt.Errorf("trip %s not found", id)
	}
	t.Status = status
	return nil
}

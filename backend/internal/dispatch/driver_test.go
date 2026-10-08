package dispatch

import (
	"context"
	"testing"
)

func TestFindNearestAvailable_PicksClosest(t *testing.T) {
	repo := NewRepository()
	repo.Seed([]Driver{
		{ID: "far", Latitude: 40.80, Longitude: -73.95, Status: Available},
		{ID: "near", Latitude: 40.761, Longitude: -73.982, Status: Available},
		{ID: "medium", Latitude: 40.78, Longitude: -73.96, Status: Available},
	})

	// pickup right on top of "near"
	driver, dist, err := repo.FindNearestAvailable(context.Background(), 40.7611, -73.9821)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if driver.ID != "near" {
		t.Errorf("expected nearest driver 'near', got %q (dist=%.3fkm)", driver.ID, dist)
	}
	if dist > 0.1 {
		t.Errorf("expected near-zero distance to 'near', got %.3fkm", dist)
	}
}

func TestFindNearestAvailable_IgnoresBusyDrivers(t *testing.T) {
	repo := NewRepository()
	repo.Seed([]Driver{
		{ID: "busy-but-closer", Latitude: 40.761, Longitude: -73.982, Status: Busy},
		{ID: "available-farther", Latitude: 40.80, Longitude: -73.95, Status: Available},
	})

	driver, _, err := repo.FindNearestAvailable(context.Background(), 40.761, -73.982)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if driver.ID != "available-farther" {
		t.Errorf("expected to skip busy driver and pick 'available-farther', got %q", driver.ID)
	}
}

func TestFindNearestAvailable_ErrorsWhenNoneAvailable(t *testing.T) {
	repo := NewRepository()
	repo.Seed([]Driver{
		{ID: "d1", Latitude: 40.76, Longitude: -73.98, Status: Busy},
		{ID: "d2", Latitude: 40.77, Longitude: -73.97, Status: Offline},
	})

	_, _, err := repo.FindNearestAvailable(context.Background(), 40.76, -73.98)
	if err == nil {
		t.Fatal("expected an error when no drivers are AVAILABLE")
	}
}

func TestSetStatus_UpdatesDriver(t *testing.T) {
	repo := NewRepository()
	repo.Seed([]Driver{{ID: "d1", Latitude: 40.76, Longitude: -73.98, Status: Available}})

	if err := repo.SetStatus(context.Background(), "d1", Busy); err != nil {
		t.Fatalf("SetStatus failed: %v", err)
	}

	drivers := repo.List(context.Background())
	if len(drivers) != 1 || drivers[0].Status != Busy {
		t.Errorf("expected driver d1 to be BUSY, got %+v", drivers)
	}
}

func TestSetStatus_ErrorsOnUnknownDriver(t *testing.T) {
	repo := NewRepository()
	if err := repo.SetStatus(context.Background(), "does-not-exist", Busy); err == nil {
		t.Fatal("expected an error for unknown driver ID")
	}
}

func TestSeed_AssignsIDsWhenMissing(t *testing.T) {
	repo := NewRepository()
	repo.Seed([]Driver{{Latitude: 40.76, Longitude: -73.98}}) // no ID given

	drivers := repo.List(context.Background())
	if len(drivers) != 1 {
		t.Fatalf("expected 1 driver, got %d", len(drivers))
	}
	if drivers[0].ID == "" {
		t.Error("expected Seed to auto-assign an ID")
	}
	if drivers[0].Status != Available {
		t.Errorf("expected default status AVAILABLE, got %s", drivers[0].Status)
	}
}

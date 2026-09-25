package models

import "time"

// PredictionRequest mirrors the Python FastAPI service's PredictionRequest
// schema exactly (field names and types), since Go forwards this JSON
// straight through. Feature engineering lives in the Python service —
// Go's job here is transport, validation, orchestration, not duplicating
// feature logic in two languages where it could drift out of sync.
type PredictionRequest struct {
	PickupLatitude   float64 `json:"pickup_latitude"`
	PickupLongitude  float64 `json:"pickup_longitude"`
	DropoffLatitude  float64 `json:"dropoff_latitude"`
	DropoffLongitude float64 `json:"dropoff_longitude"`
	PassengerCount   int     `json:"passenger_count"`
	PickupDatetime   string  `json:"pickup_datetime"` // RFC3339 string, passed through as-is
	VendorID         int     `json:"vendor_id,omitempty"`
	StoreAndFwdFlag  string  `json:"store_and_fwd_flag,omitempty"`
}

// PredictionResponse mirrors the Python service's PredictionResponse.
type PredictionResponse struct {
	PredictedDurationSeconds int     `json:"predicted_duration_seconds"`
	PredictedDurationMinutes float64 `json:"predicted_duration_minutes"`
	DistanceKm               float64 `json:"distance_km"`
	ModelVersion             string  `json:"model_version"`
	RequestID                string  `json:"request_id"`
	PredictionTimestamp      string  `json:"prediction_timestamp"`
}

// TripStatus enumerates the lifecycle of a trip record.
type TripStatus string

const (
	TripPending     TripStatus = "PENDING"
	TripDispatched  TripStatus = "DISPATCHED"
	TripInProgress  TripStatus = "IN_PROGRESS"
	TripCompleted   TripStatus = "COMPLETED"
	TripCancelled   TripStatus = "CANCELLED"
)

// Trip is the persisted record of a prediction/trip request.
type Trip struct {
	ID                    string     `json:"id"`
	PickupLat             float64    `json:"pickup_lat"`
	PickupLng             float64    `json:"pickup_lng"`
	DropoffLat            float64    `json:"dropoff_lat"`
	DropoffLng            float64    `json:"dropoff_lng"`
	PassengerCount        int        `json:"passenger_count"`
	RequestedAt           time.Time  `json:"requested_at"`
	PredictedDurationSec  *int       `json:"predicted_duration_sec,omitempty"`
	ActualDurationSec     *int       `json:"actual_duration_sec,omitempty"`
	Status                TripStatus `json:"status"`
	DriverID              *string    `json:"driver_id,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
}

// CreateTripRequest is the payload for POST /api/v1/trips.
type CreateTripRequest struct {
	PickupLat       float64 `json:"pickup_lat"`
	PickupLng       float64 `json:"pickup_lng"`
	DropoffLat      float64 `json:"dropoff_lat"`
	DropoffLng      float64 `json:"dropoff_lng"`
	PassengerCount  int     `json:"passenger_count"`
}

// HealthResponse for GET /health.
type HealthResponse struct {
	Status        string `json:"status"`
	MLServiceUp   bool   `json:"ml_service_up"`
	TimestampUTC  string `json:"timestamp_utc"`
}

// ErrorResponse is the standard error envelope returned by all handlers.
type ErrorResponse struct {
	Error   string `json:"error"`
	Detail  string `json:"detail,omitempty"`
}

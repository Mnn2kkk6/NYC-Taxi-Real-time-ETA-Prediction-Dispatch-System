package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"nyctaxi/backend/internal/mlclient"
	"nyctaxi/backend/internal/models"
	"nyctaxi/backend/internal/repositories"
)

type Handler struct {
	ml    *mlclient.Client
	trips repositories.TripRepository
	log   *slog.Logger
}

func New(ml *mlclient.Client, trips repositories.TripRepository, log *slog.Logger) *Handler {
	return &Handler{ml: ml, trips: trips, log: log}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string, detail string) {
	writeJSON(w, status, models.ErrorResponse{Error: msg, Detail: detail})
}

// GET /health
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	mlUp := h.ml.HealthCheck(r.Context())
	writeJSON(w, http.StatusOK, models.HealthResponse{
		Status:       "ok",
		MLServiceUp:  mlUp,
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
	})
}

// POST /api/v1/predict
//
// Flow: validate -> forward to ML service (with the mlclient's built-in
// timeout+retry+backoff) -> return result. Phase 4 will insert a worker
// pool + job queue between "validate" and "forward" so this handler stays
// non-blocking under concurrent load; for now it's a direct call so the
// end-to-end path is provable before adding concurrency machinery on top.
func (h *Handler) Predict(w http.ResponseWriter, r *http.Request) {
	var req models.PredictionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	if err := validatePredictionRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, "validation failed", err.Error())
		return
	}

	resp, err := h.ml.Predict(r.Context(), req)
	if err != nil {
		h.log.Error("prediction failed", "error", err)
		writeError(w, http.StatusBadGateway, "prediction failed", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func validatePredictionRequest(req models.PredictionRequest) error {
	const (
		nycLatMin, nycLatMax = 40.5, 40.9
		nycLonMin, nycLonMax = -74.05, -73.70
	)
	switch {
	case req.PickupLatitude < nycLatMin || req.PickupLatitude > nycLatMax:
		return errInvalid("pickup_latitude out of NYC bounding box")
	case req.PickupLongitude < nycLonMin || req.PickupLongitude > nycLonMax:
		return errInvalid("pickup_longitude out of NYC bounding box")
	case req.DropoffLatitude < nycLatMin || req.DropoffLatitude > nycLatMax:
		return errInvalid("dropoff_latitude out of NYC bounding box")
	case req.DropoffLongitude < nycLonMin || req.DropoffLongitude > nycLonMax:
		return errInvalid("dropoff_longitude out of NYC bounding box")
	case req.PassengerCount < 1 || req.PassengerCount > 6:
		return errInvalid("passenger_count must be between 1 and 6")
	case req.PickupDatetime == "":
		return errInvalid("pickup_datetime is required")
	}
	return nil
}

type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }
func errInvalid(msg string) error        { return &validationError{msg} }

// POST /api/v1/trips
func (h *Handler) CreateTrip(w http.ResponseWriter, r *http.Request) {
	var req models.CreateTripRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err.Error())
		return
	}

	trip := &models.Trip{
		PickupLat:      req.PickupLat,
		PickupLng:      req.PickupLng,
		DropoffLat:     req.DropoffLat,
		DropoffLng:     req.DropoffLng,
		PassengerCount: req.PassengerCount,
		RequestedAt:    time.Now().UTC(),
		Status:         models.TripPending,
		CreatedAt:      time.Now().UTC(),
	}

	if err := h.trips.Create(r.Context(), trip); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create trip", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, trip)
}

// GET /api/v1/trips/{id}
func (h *Handler) GetTrip(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	trip, err := h.trips.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "trip not found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, trip)
}

// GET /api/v1/trips?status=&limit=&offset=
func (h *Handler) ListTrips(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := parseIntDefault(r.URL.Query().Get("limit"), 20)
	offset := parseIntDefault(r.URL.Query().Get("offset"), 0)

	trips, err := h.trips.List(r.Context(), status, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list trips", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, trips)
}

func parseIntDefault(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

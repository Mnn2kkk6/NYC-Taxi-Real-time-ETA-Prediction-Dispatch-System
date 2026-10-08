package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"nyctaxi/backend/internal/cache"
	"nyctaxi/backend/internal/dispatch"
	"nyctaxi/backend/internal/metrics"
	"nyctaxi/backend/internal/mlclient"
	"nyctaxi/backend/internal/models"
	"nyctaxi/backend/internal/repositories"
	"nyctaxi/backend/internal/workers"
	"nyctaxi/backend/internal/wsserver"
	"nyctaxi/backend/pkg/idgen"
)

type Handler struct {
	ml       *mlclient.Client
	trips    repositories.TripRepository
	pool     *workers.Pool
	cache    *cache.PredictionCache
	redis    *cache.Client
	hub      *wsserver.Hub
	drivers  *dispatch.Repository
	log      *slog.Logger
	syncMode bool
}

func New(
	ml *mlclient.Client,
	trips repositories.TripRepository,
	pool *workers.Pool,
	predictionCache *cache.PredictionCache,
	redis *cache.Client,
	hub *wsserver.Hub,
	drivers *dispatch.Repository,
	syncMode bool,
	log *slog.Logger,
) *Handler {
	return &Handler{ml: ml, trips: trips, pool: pool, cache: predictionCache, redis: redis, hub: hub, drivers: drivers, syncMode: syncMode, log: log}
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
	redisUp := h.redis.Ping(r.Context()) == nil
	writeJSON(w, http.StatusOK, models.HealthResponse{
		Status:       "ok",
		MLServiceUp:  mlUp,
		RedisUp:      redisUp,
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
	})
}

// POST /api/v1/predict
//
// Flow: validate -> check Redis cache -> (hit: return immediately) ->
// (miss: enqueue into the worker pool) -> block on this job's own result
// channel until a worker finishes -> cache the result -> respond.
// The worker pool bounds how many concurrent calls hit the Python service
// to exactly PREDICTION_WORKERS, regardless of how many HTTP requests
// arrive at once; a full queue fails fast with 503 instead of queueing
// unboundedly or spawning a goroutine per request.
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

	if cached, hit := h.cache.Get(r.Context(), req); hit {
		metrics.App.CacheHitsTotal.Inc()
		metrics.App.PredictionRequestsTotal.WithLabelValues("hit").Inc()
		cached.CacheHit = true
		h.broadcastPredictionEvent(idgen.NewID(), cached)
		writeJSON(w, http.StatusOK, cached)
		return
	}
	metrics.App.CacheMissesTotal.Inc()
	metrics.App.PredictionRequestsTotal.WithLabelValues("miss").Inc()

	// Phase 9 benchmarking switch: SYNC_MODE=true bypasses the worker
	// pool entirely and calls the ML service directly from this HTTP
	// handler's own goroutine — the "naive synchronous" baseline the
	// load test compares against the worker-pool path below. Never set
	// in normal operation; see PHASE9_README.md.
	if h.syncMode {
		h.predictSync(w, r, req)
		return
	}

	resultCh := make(chan workers.JobResult, 1)
	job := workers.PredictionJob{
		ID:        idgen.NewID(),
		Ctx:       r.Context(),
		Request:   req,
		ResultCh:  resultCh,
		CreatedAt: time.Now(),
	}

	if err := h.pool.Submit(job); err != nil {
		h.log.Warn("predict: queue rejected job", "error", err)
		writeError(w, http.StatusServiceUnavailable, "server busy, try again shortly", err.Error())
		return
	}

	select {
	case result := <-resultCh:
		if result.Err != nil {
			h.log.Error("prediction failed", "error", result.Err)
			writeError(w, http.StatusBadGateway, "prediction failed", result.Err.Error())
			return
		}
		result.Response.CacheHit = false
		if err := h.cache.Set(r.Context(), req, result.Response); err != nil {
			// a failed cache write must never fail the request itself —
			// log it and move on, the next identical request just misses too.
			h.log.Warn("predict: failed to write cache entry", "error", err)
		}
		h.broadcastPredictionEvent(job.ID, result.Response)
		writeJSON(w, http.StatusOK, result.Response)
	case <-r.Context().Done():
		// client disconnected or request timed out while queued/processing;
		// nothing to write back, the worker will notice job.Ctx.Done() too.
		return
	}
}

// predictSync is the Phase 9 synchronous baseline: calls the ML service
// directly, with no queue and no concurrency bound — every simultaneous
// HTTP request becomes a simultaneous outbound call to ml-service. No
// backpressure, no 503s, nothing protecting the ML service from being
// overwhelmed. This is deliberately what the worker pool exists to avoid.
func (h *Handler) predictSync(w http.ResponseWriter, r *http.Request, req models.PredictionRequest) {
	start := time.Now()
	resp, err := h.ml.Predict(r.Context(), req)
	metrics.App.PredictionLatency.Observe(time.Since(start).Seconds())

	if err != nil {
		metrics.App.PredictionErrorsTotal.Inc()
		h.log.Error("prediction failed (sync mode)", "error", err)
		writeError(w, http.StatusBadGateway, "prediction failed", err.Error())
		return
	}
	resp.CacheHit = false
	if err := h.cache.Set(r.Context(), req, resp); err != nil {
		h.log.Warn("predict: failed to write cache entry", "error", err)
	}
	writeJSON(w, http.StatusOK, resp)
}

// broadcastPredictionEvent pushes a "trip_prediction" event to every
// connected WebSocket client, per spec §17's event example. Best-effort:
// marshal/broadcast errors are logged, never fail the HTTP response —
// the dashboard is a secondary consumer, not the source of truth.
func (h *Handler) broadcastPredictionEvent(jobID string, resp *models.PredictionResponse) {
	event := map[string]any{
		"type":          "trip_prediction",
		"trip_id":       jobID,
		"status":        "completed",
		"eta_minutes":   resp.PredictedDurationMinutes,
		"cache_hit":     resp.CacheHit,
		"model_version": resp.ModelVersion,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		h.log.Warn("failed to marshal ws event", "error", err)
		return
	}
	h.hub.Broadcast(payload)
}

// GET /api/v1/system/status
func (h *Handler) SystemStatus(w http.ResponseWriter, r *http.Request) {
	poolStats := h.pool.Stats()
	hits := metrics.App.CacheHitsTotal.Value()
	misses := metrics.App.CacheMissesTotal.Value()
	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"queue_length":   poolStats.QueueLength,
		"queue_capacity": poolStats.QueueCapacity,
		"busy_workers":   poolStats.BusyWorkers,
		"cache_hits":     hits,
		"cache_misses":   misses,
		"cache_hit_rate": hitRate,
	})
}

// GET /api/v1/metrics — Prometheus text exposition format.
func (h *Handler) Metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	metrics.Default.Gather(w)
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
//
// After creating the trip record, attempts to dispatch the nearest
// AVAILABLE driver (spec §19 — straight-line haversine distance, no
// route optimization). If a driver is found, it's marked BUSY and the
// trip's DriverID is set and status moves to DISPATCHED; if no driver is
// available, the trip stays PENDING — this is not a hard failure, the
// simulator or a later retry can dispatch it once a driver frees up.
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

	if driver, distKm, err := h.drivers.FindNearestAvailable(r.Context(), trip.PickupLat, trip.PickupLng); err == nil {
		if err := h.drivers.SetStatus(r.Context(), driver.ID, dispatch.Busy); err == nil {
			driverID := driver.ID
			trip.DriverID = &driverID
			trip.Status = models.TripDispatched
			_ = h.trips.UpdateStatus(r.Context(), trip.ID, models.TripDispatched)
			h.log.Info("dispatch: assigned nearest driver", "trip_id", trip.ID, "driver_id", driver.ID, "distance_km", distKm)
		}
	} else {
		h.log.Info("dispatch: no available driver, trip stays pending", "trip_id", trip.ID)
	}

	event, _ := json.Marshal(map[string]any{
		"type":    "trip_created",
		"trip_id": trip.ID,
		"status":  trip.Status,
	})
	h.hub.Broadcast(event)

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

// GET /api/v1/drivers
func (h *Handler) Drivers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.drivers.List(r.Context()))
}

// GET /ws
//
// Upgrades to a WebSocket connection and registers it with the hub.
// Hub.Register blocks for the lifetime of the connection (running its
// read/write pumps), so this handler's goroutine — one per connected
// client, spawned by net/http per request — simply parks here until the
// client disconnects.
func (h *Handler) WS(w http.ResponseWriter, r *http.Request) {
	conn, err := wsserver.Upgrade(w, r)
	if err != nil {
		h.log.Warn("ws: upgrade failed", "error", err)
		writeError(w, http.StatusBadRequest, "websocket upgrade failed", err.Error())
		return
	}
	h.hub.Register(conn)
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
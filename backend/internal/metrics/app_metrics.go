package metrics

// App is every metric this service exposes, matching spec §21's list.
// Gauges that reflect live system state (QueueLength, WorkerBusy,
// ActiveTrips) are wired up with SetFunc() in main.go once the worker
// pool and trip repository exist — they're computed at scrape time, not
// kept in sync via a background goroutine.
var App = struct {
	HTTPRequestsTotal       *CounterVec
	HTTPRequestDuration     *HistogramVec
	PredictionRequestsTotal *CounterVec
	PredictionLatency       *Histogram
	PredictionErrorsTotal   *Counter
	CacheHitsTotal          *Counter
	CacheMissesTotal        *Counter
	QueueLength             *Gauge
	WorkerBusy              *Gauge
	ActiveTrips             *Gauge
}{
	HTTPRequestsTotal: NewRegisteredCounterVec(
		"http_requests_total", "Total HTTP requests, by route/method/status.",
		"route", "method", "status",
	),
	HTTPRequestDuration: NewRegisteredHistogramVec(
		"http_request_duration_seconds", "HTTP request latency in seconds, by route.",
		DefaultLatencyBuckets, "route",
	),
	PredictionRequestsTotal: NewRegisteredCounterVec(
		"prediction_requests_total", "Total prediction requests, by cache hit/miss.",
		"cache",
	),
	PredictionLatency: NewRegisteredHistogram(
		"prediction_latency_seconds", "Latency of a worker's call to the ML service, in seconds.",
		DefaultLatencyBuckets,
	),
	PredictionErrorsTotal: NewCounter(
		"prediction_errors_total", "Total predictions that failed (ML service error, timeout, retries exhausted).",
	),
	CacheHitsTotal:   NewCounter("cache_hits_total", "Total prediction cache hits."),
	CacheMissesTotal: NewCounter("cache_misses_total", "Total prediction cache misses."),
	QueueLength:      NewRegisteredGauge("queue_length", "Current number of prediction jobs waiting in the queue."),
	WorkerBusy:       NewRegisteredGauge("worker_busy", "Current number of prediction workers actively processing a job."),
	ActiveTrips:      NewRegisteredGauge("active_trips", "Current number of trips that are PENDING or DISPATCHED."),
}

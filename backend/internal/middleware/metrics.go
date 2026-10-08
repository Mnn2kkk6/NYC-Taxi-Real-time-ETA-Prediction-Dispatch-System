package middleware

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"nyctaxi/backend/internal/metrics"
)

// Metrics wraps a handler with a fixed route label (e.g. "predict",
// "trips_list") — NOT r.URL.Path, which would blow up cardinality for
// paths like /api/v1/trips/{id} (a new time series per trip ID). The
// route name is supplied once at registration time in main.go, the same
// way Prometheus's own examples recommend labeling by route template
// rather than raw path.
func Metrics(route string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			start := time.Now()
			next.ServeHTTP(rec, r)
			duration := time.Since(start).Seconds()

			metrics.App.HTTPRequestsTotal.
				WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).
				Inc()
			metrics.App.HTTPRequestDuration.
				WithLabelValues(route).
				Observe(duration)
		})
	}
}

// statusRecorder is a second, metrics-only response wrapper — separate
// from Logging's responseRecorder so Metrics can be applied independently
// (e.g. skipped for /api/v1/metrics itself to avoid the endpoint scraping
// its own request in a slightly confusing way). Forwards Hijack for the
// same reason responseRecorder does: /ws must still be hijackable even if
// Metrics is ever applied to it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack forwards to the underlying ResponseWriter's Hijacker — same
// gotcha as middleware.go's responseRecorder (see that file's comment):
// embedding http.ResponseWriter does not promote Hijack().
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying ResponseWriter does not support http.Hijacker")
	}
	return hj.Hijack()
}

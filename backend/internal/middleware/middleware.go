package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"nyctaxi/backend/pkg/idgen"
)

// responseRecorder captures the status code so we can log it —
// http.ResponseWriter doesn't expose what was written.
type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Logging wraps a handler with structured request logging (request ID,
// method, path, status, latency) — every request is traceable, per the
// project's logging requirement.
func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := idgen.NewID()
			rec := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			w.Header().Set("X-Request-ID", requestID)

			start := time.Now()
			next.ServeHTTP(rec, r)
			latency := time.Since(start)

			log.Info("request",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"latency_ms", latency.Milliseconds(),
			)
		})
	}
}

// Recover turns a panic in any handler into a clean 500 instead of
// crashing the whole server — per the "no panic in production path" rule.
func Recover(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Error("panic recovered", "error", err, "path", r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal server error"}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS is intentionally permissive for local development. Tighten
// allowed origins via env var before deploying anywhere public.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

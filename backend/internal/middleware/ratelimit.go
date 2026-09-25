package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"nyctaxi/backend/internal/ratelimit"
)

// RateLimit wraps a handler, rejecting with 429 once the client IP has
// exceeded the configured limit for the current window. Applied to
// /api/v1/predict specifically (see main.go) rather than every route —
// health checks and static-ish reads don't need it.
func RateLimit(limiter *ratelimit.Limiter, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)
			if !limiter.Allow(r.Context(), ip) {
				log.Warn("rate limit exceeded", "ip", ip, "path", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate limit exceeded","detail":"too many requests, try again shortly"}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the caller's IP, preferring X-Forwarded-For (set by a
// reverse proxy/load balancer in front of this service) and falling back
// to the raw connection's RemoteAddr for direct connections.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		// X-Forwarded-For can be a comma-separated chain; the first entry
		// is the original client.
		if idx := strings.IndexByte(fwd, ','); idx != -1 {
			return fwd[:idx]
		}
		return fwd
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Package config centralizes all environment-driven settings.
// Nothing is hard-coded: every tunable (ports, timeouts, worker counts)
// comes from an env var with a sane default, per the project's engineering
// rules (no hard-coded paths/secrets, everything configurable).
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port string // HTTP port this Go server listens on

	MLServiceURL     string        // base URL of the Python FastAPI service
	MLServiceTimeout time.Duration // per-attempt timeout for calling the ML service
	MLServiceRetries int           // max retry attempts on failure/timeout

	DatabaseURL string // Postgres DSN, e.g. postgres://user:pass@host:5432/db

	PredictionWorkers int // size of the worker pool (Phase 4)
	QueueSize         int // buffered job queue capacity (Phase 4)
}

func Load() Config {
	return Config{
		Port: getEnv("PORT", "8080"),

		MLServiceURL:     getEnv("ML_SERVICE_URL", "http://localhost:8000"),
		MLServiceTimeout: getEnvDuration("ML_SERVICE_TIMEOUT_MS", 2000) * time.Millisecond,
		MLServiceRetries: getEnvInt("ML_SERVICE_RETRIES", 3),

		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/nyctaxi?sslmode=disable"),

		PredictionWorkers: getEnvInt("PREDICTION_WORKERS", 10),
		QueueSize:         getEnvInt("QUEUE_SIZE", 100),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallbackMillis int) time.Duration {
	return time.Duration(getEnvInt(key, fallbackMillis))
}

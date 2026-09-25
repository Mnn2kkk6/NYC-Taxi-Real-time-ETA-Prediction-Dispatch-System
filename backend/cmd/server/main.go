// cmd/server/main.go — entry point.
//
// Startup sequence: load config -> build dependencies (mlclient, repo,
// handlers) -> register routes -> start server in a goroutine -> block on
// signal -> graceful shutdown.
//
// Graceful shutdown matters here because in later phases the worker pool
// will have in-flight jobs; shutting down cleanly means "stop accepting
// new work, finish what's running, then exit" — not "kill -9 mid-request".
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"nyctaxi/backend/internal/config"
	"nyctaxi/backend/internal/handlers"
	"nyctaxi/backend/internal/middleware"
	"nyctaxi/backend/internal/mlclient"
	"nyctaxi/backend/internal/repositories"
	"nyctaxi/backend/internal/workers"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.Load()
	logger.Info("starting server",
		"port", cfg.Port,
		"ml_service_url", cfg.MLServiceURL,
		"prediction_workers", cfg.PredictionWorkers,
	)

	ml := mlclient.New(cfg.MLServiceURL, cfg.MLServiceTimeout, cfg.MLServiceRetries)
	tripRepo := repositories.NewMemoryTripRepository()
	// NOTE: swap NewMemoryTripRepository() for a Postgres-backed
	// implementation (see internal/repositories/postgres_trip_repository.go)
	// once `go mod tidy` has fetched pgx on a machine with internet access.

	// Worker pool: bounds concurrent calls to the ML service to exactly
	// PREDICTION_WORKERS, no matter how many HTTP requests arrive at once.
	// Started against its own context so it keeps draining in-flight jobs
	// during graceful shutdown even after the HTTP server has stopped
	// accepting new connections.
	poolCtx, poolCancel := context.WithCancel(context.Background())
	defer poolCancel()
	pool := workers.NewPool(ml, cfg.QueueSize, logger)
	pool.Start(poolCtx, cfg.PredictionWorkers)

	h := handlers.New(ml, tripRepo, pool, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.Health)
	mux.HandleFunc("POST /api/v1/predict", h.Predict)
	mux.HandleFunc("POST /api/v1/trips", h.CreateTrip)
	mux.HandleFunc("GET /api/v1/trips/{id}", h.GetTrip)
	mux.HandleFunc("GET /api/v1/trips", h.ListTrips)
	mux.HandleFunc("GET /api/v1/system/status", h.SystemStatus)

	var rootHandler http.Handler = mux
	rootHandler = middleware.Logging(logger)(rootHandler)
	rootHandler = middleware.Recover(logger)(rootHandler)
	rootHandler = middleware.CORS(rootHandler)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      rootHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// run server in background so main() can block on the shutdown signal
	go func() {
		logger.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutdown signal received, draining connections...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("http server stopped, draining worker pool...")

	poolShutdownCtx, poolShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer poolShutdownCancel()
	if err := pool.Shutdown(poolShutdownCtx); err != nil {
		logger.Error("worker pool did not drain in time", "error", err)
		os.Exit(1)
	}
	logger.Info("server stopped cleanly")
}

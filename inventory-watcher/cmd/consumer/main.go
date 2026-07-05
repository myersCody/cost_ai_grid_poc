package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/osac-project/cost-event-consumer/internal/authn"
	"github.com/osac-project/cost-event-consumer/internal/config"
	"github.com/osac-project/cost-event-consumer/internal/custommetrics"
	"github.com/osac-project/cost-event-consumer/internal/ingest"
	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metrics"
	"github.com/osac-project/cost-event-consumer/internal/metering"
	"github.com/osac-project/cost-event-consumer/internal/osac"
	"github.com/osac-project/cost-event-consumer/internal/rating"
	"github.com/osac-project/cost-event-consumer/internal/reconciler"
	"github.com/osac-project/cost-event-consumer/internal/summarizer"
	"github.com/osac-project/cost-event-consumer/internal/watcher"
)

func main() {
	cfg := config.Load()

	logLevel := parseLogLevel(cfg.LogLevel)
	var logHandler slog.Handler
	opts := &slog.HandlerOptions{Level: logLevel}
	if cfg.LogFormat == "json" {
		logHandler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		logHandler = slog.NewTextHandler(os.Stderr, opts)
	}
	logger := slog.New(logHandler)

	if err := cfg.Validate(); err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.InventoryDBURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Error("database not reachable", "error", err)
		os.Exit(1)
	}
	logger.Info("connected to inventory database")

	store := inventory.NewStore(pool, logger)
	if err := store.RunMigrations(ctx); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}
	logger.Info("database schema ready")

	if err := rating.SeedDefaultRates(ctx, store, logger); err != nil {
		logger.Error("failed to seed default rates", "error", err)
		os.Exit(1)
	}

	if err := rating.SeedDefaultQuotas(ctx, store, logger); err != nil {
		logger.Error("failed to seed default quotas", "error", err)
		os.Exit(1)
	}

	m := metering.New(store, 60*time.Second, logger)
	s := summarizer.New(store, cfg.SummarizeInterval, logger)
	rt := rating.New(store, 30*time.Second, logger)

	var w *watcher.Watcher
	var r *reconciler.Reconciler

	osacNeeded := !cfg.ComponentDisabled("watcher") || !cfg.ComponentDisabled("reconciler")
	if osacNeeded {
		osacClient, err := osac.NewClient(cfg.OSACBaseURL, cfg.OSACToken, cfg.OSACCACert, logger)
		if err != nil {
			logger.Error("failed to create OSAC client", "error", err)
			os.Exit(1)
		}
		w = watcher.New(osacClient, store, m, logger)
		r = reconciler.New(osacClient, store, w, cfg.ReconcileInterval, logger)
	}

	logger.Info("starting cost-event-consumer",
		"osac_url", cfg.OSACBaseURL,
		"reconcile_interval", cfg.ReconcileInterval,
		"summarize_interval", cfg.SummarizeInterval,
		"metering_interval", "60s",
		"rating_interval", "30s",
		"ingest_addr", cfg.IngestListenAddr,
		"disabled_components", cfg.DisabledComponents,
	)

	g, ctx := errgroup.WithContext(ctx)

	startComponent := func(name string, fn func() error) {
		if cfg.ComponentDisabled(name) {
			logger.Info("component disabled, skipping", "component", name)
			return
		}
		g.Go(safeGo(logger, name, fn))
	}

	if w != nil {
		startComponent("watcher", func() error { return w.Run(ctx) })
	}
	if r != nil {
		startComponent("reconciler", func() error { return r.Run(ctx) })
	}
	startComponent("summarizer", func() error { return s.Run(ctx) })
	startComponent("metering", func() error { return m.Run(ctx) })
	startComponent("rating", func() error { return rt.Run(ctx) })

	// Metrics server on a separate port (no auth).
	metricsMux := http.NewServeMux()
	metricsMux.Handle("GET /metrics", promhttp.Handler())
	metricsSrv := &http.Server{Addr: ":" + cfg.MetricsPort, Handler: metricsMux}
	g.Go(func() error {
		logger.Info("metrics endpoint listening", "addr", ":"+cfg.MetricsPort)
		if err := metricsSrv.ListenAndServe(); err != http.ErrServerClosed {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return metricsSrv.Shutdown(shutdownCtx)
	})

	var cmRegistry *custommetrics.Registry
	if cfg.CustomMetricsConfigPath != "" {
		var cmErr error
		cmRegistry, cmErr = custommetrics.LoadFromFile(cfg.CustomMetricsConfigPath, logger)
		if cmErr != nil {
			logger.Error("failed to load custom metrics config", "path", cfg.CustomMetricsConfigPath, "error", cmErr)
			os.Exit(1)
		}
	}

	if cfg.IngestListenAddr != "" {
		h := ingest.NewHandler(store, m, cfg, cmRegistry, logger)
		if r != nil {
			h.SetReconciler(r)
		}

		auth, err := authn.New(cfg.AuthIssuerURL, cfg.OSACCACert, logger)
		if err != nil {
			logger.Error("failed to create auth middleware", "error", err)
			os.Exit(1)
		}

		srv := &http.Server{
			Addr:           cfg.IngestListenAddr,
			Handler:        metrics.RequestLogger(logger, metrics.HTTPMiddleware(panicRecovery(logger, auth.Wrap(h.ServeMux())))),
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			MaxHeaderBytes: 1 << 20,
		}
		g.Go(func() error {
			logger.Info("ingest endpoint listening", "addr", cfg.IngestListenAddr)
			if err := srv.ListenAndServe(); err != http.ErrServerClosed {
				return err
			}
			return nil
		})
		g.Go(func() error {
			<-ctx.Done()
			logger.Info("shutting down ingest server, draining in-flight requests")
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()
			return srv.Shutdown(shutdownCtx)
		})
	}

	if err := g.Wait(); err != nil && ctx.Err() == nil {
		logger.Error("consumer exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("cost-event-consumer stopped")
}

func safeGo(logger *slog.Logger, name string, fn func() error) func() error {
	return func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("goroutine panic", "component", name,
					"error", r, "stack", string(debug.Stack()))
				err = fmt.Errorf("goroutine %s panicked: %v", name, r)
			}
		}()
		return fn()
	}
}

func panicRecovery(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				logger.Error("http handler panic", "error", err,
					"method", r.Method, "path", r.URL.Path,
					"stack", string(debug.Stack()))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal server error"}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

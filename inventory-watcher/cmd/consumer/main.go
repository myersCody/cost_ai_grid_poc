package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/osac-project/cost-event-consumer/internal/config"
	"github.com/osac-project/cost-event-consumer/internal/ingest"
	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/metering"
	"github.com/osac-project/cost-event-consumer/internal/osac"
	"github.com/osac-project/cost-event-consumer/internal/rating"
	"github.com/osac-project/cost-event-consumer/internal/reconciler"
	"github.com/osac-project/cost-event-consumer/internal/summarizer"
	"github.com/osac-project/cost-event-consumer/internal/watcher"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))

	cfg := config.Load()
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

	osacClient, err := osac.NewClient(cfg.OSACBaseURL, cfg.OSACToken, cfg.OSACCACert, logger)
	if err != nil {
		logger.Error("failed to create OSAC client", "error", err)
		os.Exit(1)
	}

	m := metering.New(store, 60*time.Second, logger)
	w := watcher.New(osacClient, store, m, logger)
	r := reconciler.New(osacClient, store, w, cfg.ReconcileInterval, logger)
	s := summarizer.New(store, cfg.SummarizeInterval, logger)
	rt := rating.New(store, 30*time.Second, logger)

	logger.Info("starting cost-event-consumer",
		"osac_url", cfg.OSACBaseURL,
		"reconcile_interval", cfg.ReconcileInterval,
		"summarize_interval", cfg.SummarizeInterval,
		"metering_interval", "60s",
		"rating_interval", "30s",
		"ingest_addr", cfg.IngestListenAddr,
	)

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error { return w.Run(ctx) })
	g.Go(func() error { return r.Run(ctx) })
	g.Go(func() error { return s.Run(ctx) })
	g.Go(func() error { return m.Run(ctx) })
	g.Go(func() error { return rt.Run(ctx) })

	if cfg.IngestListenAddr != "" {
		h := ingest.NewHandler(store, m, logger)
		srv := &http.Server{Addr: cfg.IngestListenAddr, Handler: h.ServeMux()}
		g.Go(func() error {
			logger.Info("ingest endpoint listening", "addr", cfg.IngestListenAddr)
			return srv.ListenAndServe()
		})
		g.Go(func() error {
			<-ctx.Done()
			return srv.Close()
		})
	}

	if err := g.Wait(); err != nil && ctx.Err() == nil {
		logger.Error("consumer exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("cost-event-consumer stopped")
}

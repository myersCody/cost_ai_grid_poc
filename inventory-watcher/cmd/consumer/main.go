package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/osac-project/cost-event-consumer/internal/config"
	"github.com/osac-project/cost-event-consumer/internal/inventory"
	"github.com/osac-project/cost-event-consumer/internal/osac"
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

	// Connect to inventory database.
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

	// Initialize store and run migrations.
	store := inventory.NewStore(pool, logger)
	if err := store.RunMigrations(ctx); err != nil {
		logger.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}
	logger.Info("database schema ready")

	// Create OSAC client.
	osacClient, err := osac.NewClient(cfg.OSACBaseURL, cfg.OSACToken, cfg.OSACCACert, logger)
	if err != nil {
		logger.Error("failed to create OSAC client", "error", err)
		os.Exit(1)
	}

	// Create components.
	w := watcher.New(osacClient, store, logger)
	r := reconciler.New(osacClient, store, w, cfg.ReconcileInterval, logger)
	s := summarizer.New(store, cfg.SummarizeInterval, logger)

	logger.Info("starting cost-event-consumer",
		"osac_url", cfg.OSACBaseURL,
		"reconcile_interval", cfg.ReconcileInterval,
		"summarize_interval", cfg.SummarizeInterval,
	)

	// Run all components concurrently.
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		return w.Run(ctx)
	})

	g.Go(func() error {
		return r.Run(ctx)
	})

	g.Go(func() error {
		return s.Run(ctx)
	})

	if err := g.Wait(); err != nil && ctx.Err() == nil {
		logger.Error("consumer exited with error", "error", err)
		os.Exit(1)
	}

	logger.Info("cost-event-consumer stopped")
}

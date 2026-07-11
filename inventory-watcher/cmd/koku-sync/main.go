package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/osac-project/cost-event-consumer/internal/kokusync"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	sourceDBURL := envOr("SOURCE_DB_URL", "postgres://user:pass@localhost:5434/costdb")
	kokuDBURL := envOr("KOKU_DB_URL", "postgres://postgres:postgres@localhost:15432/postgres")
	masuURL := envOr("KOKU_MASU_URL", "http://localhost:5042")

	syncDate := time.Now().UTC().Truncate(24 * time.Hour)
	if v := os.Getenv("SYNC_DATE"); v != "" {
		var err error
		syncDate, err = time.Parse("2006-01-02", v)
		if err != nil {
			logger.Error("invalid SYNC_DATE", "value", v, "error", err)
			os.Exit(1)
		}
	}

	ctx := context.Background()

	sourcePool, err := pgxpool.New(ctx, sourceDBURL)
	if err != nil {
		logger.Error("cannot connect to source DB", "error", err)
		os.Exit(1)
	}
	defer sourcePool.Close()

	kokuPool, err := pgxpool.New(ctx, kokuDBURL)
	if err != nil {
		logger.Error("cannot connect to Koku DB", "error", err)
		os.Exit(1)
	}
	defer kokuPool.Close()

	syncer, err := kokusync.New(sourcePool, kokuPool, masuURL, 0, logger)
	if err != nil {
		logger.Error("failed to initialize syncer", "error", err)
		os.Exit(1)
	}

	if err := syncer.SyncDate(ctx, syncDate); err != nil {
		logger.Error("sync failed", "error", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

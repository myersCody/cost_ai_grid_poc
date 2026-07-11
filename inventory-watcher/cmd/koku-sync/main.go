package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultSourceDBURL = "postgres://user:pass@localhost:5434/costdb"
const defaultKokuDBURL = "postgres://postgres:postgres@localhost:15432/postgres"
const osacProviderUUID = "00000000-0000-0000-0000-0a5ac0000001"
const osacClusterID = "osac-region-1"
const osacClusterAlias = "OSAC Sovereign Cloud"

var schemaRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	sourceDBURL := envOr("SOURCE_DB_URL", defaultSourceDBURL)
	kokuDBURL := envOr("KOKU_DB_URL", defaultKokuDBURL)

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

	// Discover the Koku schema from api_customer
	kokuSchema, err := discoverSchema(ctx, kokuPool)
	if err != nil {
		logger.Error("cannot discover Koku schema", "error", err)
		os.Exit(1)
	}
	if !schemaRE.MatchString(kokuSchema) {
		logger.Error("invalid schema name", "schema", kokuSchema)
		os.Exit(1)
	}

	logger.Info("connected",
		"source", sourceDBURL, "koku", kokuDBURL,
		"schema", kokuSchema, "sync_date", syncDate.Format("2006-01-02"))

	// Step 1: Ensure prerequisites in Koku DB
	if err := ensureKokuPrereqs(ctx, kokuPool, kokuSchema, syncDate, logger); err != nil {
		logger.Error("failed to set up prerequisites", "error", err)
		os.Exit(1)
	}

	// Step 2: Read aggregated cost data from our DB
	rows, err := fetchDailyCosts(ctx, sourcePool, syncDate)
	if err != nil {
		logger.Error("failed to fetch cost data", "error", err)
		os.Exit(1)
	}
	logger.Info("fetched cost data", "rows", len(rows))

	if len(rows) == 0 {
		logger.Info("no cost data to sync")
		return
	}

	// Step 3: Write into OSAC line items table (within a transaction)
	written, err := writeToKoku(ctx, kokuPool, kokuSchema, rows, syncDate, logger)
	if err != nil {
		logger.Error("failed to write to Koku", "error", err)
		os.Exit(1)
	}
	logger.Info("wrote to Koku OSAC table", "rows", written)

	// Step 4: Trigger Koku pipeline via Masu API
	if err := triggerKokuPipeline(kokuSchema, syncDate, logger); err != nil {
		logger.Warn("pipeline trigger failed (is Koku server running?)", "error", err)
		logger.Info("data is in the OSAC table — trigger manually with:")
		logger.Info(fmt.Sprintf(
			"  curl 'localhost:5042/api/cost-management/v1/report_data/?provider_uuid=%s&schema=%s&start_date=%s'",
			osacProviderUUID, kokuSchema, syncDate.Format("2006-01-02")))
	}

	logger.Info("sync complete", "date", syncDate.Format("2006-01-02"), "rows", written)
}

type costRow struct {
	tenantID     string
	resourceType string
	resourceID   string
	meterName    string
	totalValue   float64
	totalCost    float64
	costType     string
	kokuMetric   string
	currency     string
}

func fetchDailyCosts(ctx context.Context, pool *pgxpool.Pool, syncDate time.Time) ([]costRow, error) {
	nextDay := syncDate.Add(24 * time.Hour)
	rows, err := pool.Query(ctx, `
		SELECT
			ce.tenant_id,
			ce.resource_type,
			ce.resource_id,
			ce.meter_name,
			SUM(ce.metered_value) as total_value,
			SUM(ce.cost_amount) as total_cost,
			r.cost_type,
			COALESCE(r.koku_metric, '') as koku_metric,
			r.currency
		FROM cost_entries ce
		JOIN rates r ON r.id = ce.rate_id
		WHERE ce.period_start >= $1 AND ce.period_start < $2
			AND ce.resource_type != 'model'
		GROUP BY ce.tenant_id, ce.resource_type, ce.resource_id,
				 ce.meter_name, r.cost_type, r.koku_metric, r.currency
	`, syncDate, nextDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []costRow
	for rows.Next() {
		var r costRow
		if err := rows.Scan(&r.tenantID, &r.resourceType, &r.resourceID,
			&r.meterName, &r.totalValue, &r.totalCost,
			&r.costType, &r.kokuMetric, &r.currency); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func discoverSchema(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var schema string
	err := pool.QueryRow(ctx,
		"SELECT schema_name FROM public.api_customer ORDER BY id LIMIT 1").Scan(&schema)
	if err != nil {
		return "", fmt.Errorf("no customer found in api_customer: %w", err)
	}
	return schema, nil
}

func ensureKokuPrereqs(ctx context.Context, pool *pgxpool.Pool, schema string, syncDate time.Time, logger *slog.Logger) error {
	monthStart := time.Date(syncDate.Year(), syncDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Ensure TenantAPIProvider exists in tenant schema
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".reporting_tenant_api_provider (uuid, name, type)
		VALUES ($1, $2, $3)
		ON CONFLICT (uuid) DO NOTHING
	`, schema), osacProviderUUID, osacClusterAlias, "OCP")
	if err != nil {
		return fmt.Errorf("creating TenantAPIProvider: %w", err)
	}
	logger.Info("ensured TenantAPIProvider", "uuid", osacProviderUUID)

	// Ensure ReportPeriod exists
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".reporting_ocpusagereportperiod
			(cluster_id, cluster_alias, report_period_start, report_period_end, provider_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (cluster_id, report_period_start, provider_id) DO NOTHING
	`, schema), osacClusterID, osacClusterAlias, monthStart, monthEnd, osacProviderUUID)
	if err != nil {
		return fmt.Errorf("creating ReportPeriod: %w", err)
	}
	logger.Info("ensured ReportPeriod", "cluster", osacClusterID)

	// Ensure OSAC table partition exists for this month
	partitionName := fmt.Sprintf("openshift_osac_usage_line_items_daily_%s%02d",
		syncDate.Format("2006"), syncDate.Month())
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS "%s"."%s"
		PARTITION OF "%s".openshift_osac_usage_line_items_daily
		FOR VALUES FROM ('%s') TO ('%s')
	`, schema, partitionName, schema,
		monthStart.Format("2006-01-02"), monthEnd.Format("2006-01-02")))
	if err != nil {
		logger.Warn("partition creation failed (may already exist or table not migrated)", "error", err)
	} else {
		logger.Info("ensured OSAC table partition", "partition", partitionName)
	}

	return tx.Commit(ctx)
}

func writeToKoku(ctx context.Context, pool *pgxpool.Pool, schema string, rows []costRow, syncDate time.Time, logger *slog.Logger) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Advisory lock prevents concurrent koku-sync runs from duplicating data
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))",
		fmt.Sprintf("koku-sync-%s", syncDate.Format("2006-01-02")))
	if err != nil {
		return 0, fmt.Errorf("acquiring advisory lock: %w", err)
	}

	// Delete existing OSAC data for this date
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM "%s".openshift_osac_usage_line_items_daily
		WHERE source = $1 AND usage_start = $2
	`, schema), osacProviderUUID, syncDate)
	if err != nil {
		return 0, fmt.Errorf("deleting existing data: %w", err)
	}

	year := syncDate.Format("2006")
	month := fmt.Sprintf("%02d", syncDate.Month())
	intervalStart := time.Date(syncDate.Year(), syncDate.Month(), syncDate.Day(), 0, 0, 0, 0, time.UTC)

	written := 0
	for _, r := range rows {
		rowID := uuid.New().String()

		_, err := tx.Exec(ctx, fmt.Sprintf(`
			INSERT INTO "%s".openshift_osac_usage_line_items_daily (
				id, source, year, month, usage_start, interval_start, interval_end,
				resource_type, resource_id, tenant_id, project_id,
				meter_name, value, unit, cost_type, koku_metric,
				cost_amount, currency
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		`, schema),
			rowID, osacProviderUUID, year, month, syncDate, intervalStart, intervalStart.Add(24*time.Hour),
			r.resourceType, r.resourceID, r.tenantID, "",
			r.meterName, r.totalValue, unitForMeter(r.meterName), r.costType, r.kokuMetric,
			r.totalCost, r.currency,
		)
		if err != nil {
			return written, fmt.Errorf("inserting row %s/%s: %w", r.resourceID, r.meterName, err)
		}
		written++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return written, nil
}

func triggerKokuPipeline(schema string, syncDate time.Time, logger *slog.Logger) error {
	kokuURL := envOr("KOKU_MASU_URL", "http://localhost:5042")
	url := fmt.Sprintf(
		"%s/api/cost-management/v1/report_data/?provider_uuid=%s&schema=%s&start_date=%s&end_date=%s",
		kokuURL, osacProviderUUID, schema,
		syncDate.Format("2006-01-02"), syncDate.Format("2006-01-02"))

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("calling Masu API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Masu API returned %d", resp.StatusCode)
	}

	logger.Info("triggered Koku pipeline", "url", url)
	return nil
}

func unitForMeter(meter string) string {
	switch meter {
	case "vm_uptime_seconds", "cluster_uptime_seconds", "bm_uptime_seconds":
		return "seconds"
	case "vm_cpu_core_seconds":
		return "core_seconds"
	case "vm_memory_gib_seconds":
		return "gib_seconds"
	case "cluster_worker_node_seconds":
		return "node_seconds"
	case "maas_tokens_in", "maas_tokens_out", "maas_tokens_cached", "maas_tokens_reasoning":
		return "tokens"
	case "maas_requests":
		return "requests"
	default:
		return "units"
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

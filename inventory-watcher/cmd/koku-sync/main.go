package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultSourceDBURL = "postgres://user:pass@localhost:5434/costdb"
const defaultKokuDBURL = "postgres://postgres:postgres@localhost:15432/postgres"
const defaultKokuSchema = "org1"
const osacProviderUUID = "00000000-0000-0000-0000-osac00000001"
const osacClusterID = "osac-region-1"
const osacClusterAlias = "OSAC Sovereign Cloud"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	sourceDBURL := envOr("SOURCE_DB_URL", defaultSourceDBURL)
	kokuDBURL := envOr("KOKU_DB_URL", defaultKokuDBURL)
	kokuSchema := envOr("KOKU_SCHEMA", defaultKokuSchema)

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
		logger.Error("cannot connect to source DB", "url", sourceDBURL, "error", err)
		os.Exit(1)
	}
	defer sourcePool.Close()

	kokuPool, err := pgxpool.New(ctx, kokuDBURL)
	if err != nil {
		logger.Error("cannot connect to Koku DB", "url", kokuDBURL, "error", err)
		os.Exit(1)
	}
	defer kokuPool.Close()

	logger.Info("connected to databases",
		"source", sourceDBURL, "koku", kokuDBURL, "schema", kokuSchema, "sync_date", syncDate.Format("2006-01-02"))

	// Step 1: Ensure Koku prerequisites exist
	if err := ensureKokuPrereqs(ctx, kokuPool, kokuSchema, syncDate, logger); err != nil {
		logger.Error("failed to set up Koku prerequisites", "error", err)
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

	// Step 3: Write into Koku's daily summary
	written, err := writeToKoku(ctx, kokuPool, kokuSchema, rows, syncDate, logger)
	if err != nil {
		logger.Error("failed to write to Koku", "error", err)
		os.Exit(1)
	}
	logger.Info("wrote to Koku daily summary", "rows", written)

	// Step 4: Refresh UI summary tables
	if err := refreshUISummary(ctx, kokuPool, kokuSchema, syncDate, logger); err != nil {
		logger.Warn("UI summary refresh failed (may need Koku server running)", "error", err)
	}

	logger.Info("sync complete", "date", syncDate.Format("2006-01-02"), "rows_synced", written)
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

func ensureKokuPrereqs(ctx context.Context, pool *pgxpool.Pool, schema string, syncDate time.Time, logger *slog.Logger) error {
	monthStart := time.Date(syncDate.Year(), syncDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Ensure TenantAPIProvider exists in the tenant schema
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.reporting_tenant_api_provider (uuid, name, type)
		VALUES ($1, $2, $3)
		ON CONFLICT (uuid) DO NOTHING
	`, schema), osacProviderUUID, osacClusterAlias, "OCP")
	if err != nil {
		return fmt.Errorf("creating TenantAPIProvider: %w", err)
	}
	logger.Info("ensured TenantAPIProvider exists", "uuid", osacProviderUUID)

	// Ensure ReportPeriod exists
	_, err = pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.reporting_ocpusagereportperiod
			(cluster_id, cluster_alias, report_period_start, report_period_end, provider_id)
		SELECT $1, $2, $3, $4, p.uuid
		FROM %s.reporting_tenant_api_provider p
		WHERE p.uuid = $5
		ON CONFLICT (cluster_id, report_period_start, provider_id) DO NOTHING
	`, schema, schema), osacClusterID, osacClusterAlias, monthStart, monthEnd, osacProviderUUID)
	if err != nil {
		return fmt.Errorf("creating ReportPeriod: %w", err)
	}
	logger.Info("ensured ReportPeriod exists", "cluster", osacClusterID, "month", monthStart.Format("2006-01"))

	return nil
}

func writeToKoku(ctx context.Context, pool *pgxpool.Pool, schema string, rows []costRow, syncDate time.Time, logger *slog.Logger) (int, error) {
	// Delete existing OSAC data for this date
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s.reporting_ocpusagelineitem_daily_summary
		WHERE source_uuid = $1 AND usage_start = $2
	`, schema), osacProviderUUID, syncDate)
	if err != nil {
		return 0, fmt.Errorf("deleting existing data: %w", err)
	}

	// Get report_period_id
	var reportPeriodID int64
	monthStart := time.Date(syncDate.Year(), syncDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	err = pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT id FROM %s.reporting_ocpusagereportperiod
		WHERE cluster_id = $1 AND report_period_start = $2
	`, schema), osacClusterID, monthStart).Scan(&reportPeriodID)
	if err != nil {
		return 0, fmt.Errorf("looking up report_period_id: %w", err)
	}

	written := 0
	for _, r := range rows {
		entryUUID := uuid.New().String()

		// Map our meters to Koku columns
		var cpuCoreHours, memGiBHours, infraCost, cpuCost, memCost *float64
		var rateType, namespace, node string

		namespace = r.tenantID
		node = r.resourceID

		switch r.meterName {
		case "vm_cpu_core_seconds":
			v := r.totalValue / 3600
			cpuCoreHours = &v
			if r.costType == "Supplementary" {
				cpuCost = &r.totalCost
			}
		case "vm_memory_gib_seconds":
			v := r.totalValue / 3600
			memGiBHours = &v
			if r.costType == "Supplementary" {
				memCost = &r.totalCost
			}
		case "vm_uptime_seconds", "cluster_uptime_seconds", "cluster_worker_node_seconds", "bm_uptime_seconds":
			if r.costType == "Infrastructure" {
				infraCost = &r.totalCost
			}
		default:
			// MaaS and custom metrics — map to infrastructure_raw_cost
			infraCost = &r.totalCost
		}

		rateType = r.costType

		_, err := pool.Exec(ctx, fmt.Sprintf(`
			INSERT INTO %s.reporting_ocpusagelineitem_daily_summary (
				uuid, report_period_id, cluster_id, cluster_alias,
				data_source, namespace, node, resource_id,
				usage_start, usage_end,
				pod_request_cpu_core_hours,
				pod_request_memory_gigabyte_hours,
				infrastructure_raw_cost,
				cost_model_cpu_cost,
				cost_model_memory_cost,
				cost_model_rate_type,
				source_uuid,
				raw_currency
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		`, schema),
			entryUUID, reportPeriodID, osacClusterID, osacClusterAlias,
			"Pod", namespace, node, r.resourceID,
			syncDate, syncDate,
			cpuCoreHours,
			memGiBHours,
			infraCost,
			cpuCost,
			memCost,
			rateType,
			osacProviderUUID,
			r.currency,
		)
		if err != nil {
			logger.Error("failed to insert row", "resource", r.resourceID, "meter", r.meterName, "error", err)
			continue
		}
		written++
	}

	return written, nil
}

func refreshUISummary(ctx context.Context, pool *pgxpool.Pool, schema string, syncDate time.Time, logger *slog.Logger) error {
	nextDay := syncDate.Add(24 * time.Hour)

	// Refresh cost summary tables using the same pattern as Koku's
	// populate_ui_summary_tables() — DELETE/INSERT from daily summary.
	// For the spike, we refresh the main cost summary table only.
	summarySQL := fmt.Sprintf(`
		DELETE FROM %s.reporting_ocp_cost_summary_p
		WHERE usage_start >= $1 AND usage_start < $2 AND source_uuid = $3;

		INSERT INTO %s.reporting_ocp_cost_summary_p (
			id, cluster_id, cluster_alias, usage_start, usage_end,
			infrastructure_raw_cost, infrastructure_markup_cost,
			supplementary_usage_cost, supplementary_monthly_cost_json,
			source_uuid
		)
		SELECT
			gen_random_uuid(),
			cluster_id,
			cluster_alias,
			usage_start,
			usage_end,
			SUM(COALESCE(infrastructure_raw_cost, 0)),
			0,
			NULL,
			NULL,
			source_uuid
		FROM %s.reporting_ocpusagelineitem_daily_summary
		WHERE usage_start >= $1 AND usage_start < $2 AND source_uuid = $3
		GROUP BY cluster_id, cluster_alias, usage_start, usage_end, source_uuid
	`, schema, schema, schema)

	_, err := pool.Exec(ctx, summarySQL, syncDate, nextDay, osacProviderUUID)
	if err != nil {
		return fmt.Errorf("refreshing cost summary: %w", err)
	}
	logger.Info("refreshed reporting_ocp_cost_summary_p")

	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

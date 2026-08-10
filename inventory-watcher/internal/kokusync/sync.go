package kokusync

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ProviderUUID = "00000000-0000-0000-0000-0a5ac0000001"
	ClusterID    = "osac-region-1"
	ClusterAlias = "OSAC Sovereign Cloud"
)

var schemaRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type Syncer struct {
	sourcePool *pgxpool.Pool
	kokuPool   *pgxpool.Pool
	schema     string
	masuURL    string
	interval   time.Duration
	logger     *slog.Logger
}

func New(sourcePool, kokuPool *pgxpool.Pool, masuURL string, interval time.Duration, logger *slog.Logger) (*Syncer, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	schema, err := discoverSchema(ctx, kokuPool)
	if err != nil {
		return nil, fmt.Errorf("discovering Koku schema: %w", err)
	}
	if !schemaRE.MatchString(schema) {
		return nil, fmt.Errorf("invalid Koku schema name: %q", schema)
	}

	logger.Info("koku-sync initialized", "schema", schema, "interval", interval)

	return &Syncer{
		sourcePool: sourcePool,
		kokuPool:   kokuPool,
		schema:     schema,
		masuURL:    masuURL,
		interval:   interval,
		logger:     logger,
	}, nil
}

func (s *Syncer) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Sync immediately on startup
	s.syncToday(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			s.syncToday(ctx)
		}
	}
}

func (s *Syncer) syncToday(ctx context.Context) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if err := s.SyncDate(ctx, today); err != nil {
		s.logger.Error("koku-sync failed", "date", today.Format("2006-01-02"), "error", err)
	}
}

func (s *Syncer) SyncDate(ctx context.Context, syncDate time.Time) error {
	if err := ensureKokuPrereqs(ctx, s.kokuPool, s.schema, syncDate, s.logger); err != nil {
		return fmt.Errorf("prerequisites: %w", err)
	}

	rows, err := fetchDailyCosts(ctx, s.sourcePool, syncDate)
	if err != nil {
		return fmt.Errorf("fetching costs: %w", err)
	}

	if len(rows) == 0 {
		s.logger.Debug("koku-sync: no data", "date", syncDate.Format("2006-01-02"))
		return nil
	}

	written, err := writeToKoku(ctx, s.kokuPool, s.schema, rows, syncDate, s.logger)
	if err != nil {
		return fmt.Errorf("writing to Koku: %w", err)
	}

	if err := triggerPipeline(s.masuURL, s.schema, syncDate, s.logger); err != nil {
		s.logger.Warn("pipeline trigger failed", "error", err)
	}

	s.logger.Info("koku-sync complete", "date", syncDate.Format("2006-01-02"), "rows", written)
	return nil
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

	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".reporting_tenant_api_provider (uuid, name, type)
		VALUES ($1, $2, $3)
		ON CONFLICT (uuid) DO NOTHING
	`, schema), ProviderUUID, ClusterAlias, "OCP")
	if err != nil {
		return fmt.Errorf("creating TenantAPIProvider: %w", err)
	}

	_, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO "%s".reporting_ocpusagereportperiod
			(cluster_id, cluster_alias, report_period_start, report_period_end, provider_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (cluster_id, report_period_start, provider_id) DO NOTHING
	`, schema), ClusterID, ClusterAlias, monthStart, monthEnd, ProviderUUID)
	if err != nil {
		return fmt.Errorf("creating ReportPeriod: %w", err)
	}

	partitionName := fmt.Sprintf("openshift_osac_usage_line_items_daily_%s%02d",
		syncDate.Format("2006"), syncDate.Month())
	_, err = tx.Exec(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS "%s"."%s"
		PARTITION OF "%s".openshift_osac_usage_line_items_daily
		FOR VALUES FROM ('%s') TO ('%s')
	`, schema, partitionName, schema,
		monthStart.Format("2006-01-02"), monthEnd.Format("2006-01-02")))
	if err != nil {
		logger.Warn("partition creation failed (may already exist)", "error", err)
	}

	return tx.Commit(ctx)
}

func writeToKoku(ctx context.Context, pool *pgxpool.Pool, schema string, rows []costRow, syncDate time.Time, logger *slog.Logger) (int, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtext($1))",
		fmt.Sprintf("koku-sync-%s", syncDate.Format("2006-01-02")))
	if err != nil {
		return 0, fmt.Errorf("acquiring advisory lock: %w", err)
	}

	_, err = tx.Exec(ctx, fmt.Sprintf(`
		DELETE FROM "%s".openshift_osac_usage_line_items_daily
		WHERE source = $1 AND usage_start = $2
	`, schema), ProviderUUID, syncDate)
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
			rowID, ProviderUUID, year, month, syncDate, intervalStart, intervalStart.Add(24*time.Hour),
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

func triggerPipeline(masuURL, schema string, syncDate time.Time, logger *slog.Logger) error {
	url := fmt.Sprintf(
		"%s/api/cost-management/v1/report_data/?provider_uuid=%s&schema=%s&start_date=%s&end_date=%s",
		masuURL, ProviderUUID, schema,
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
	default:
		return "units"
	}
}

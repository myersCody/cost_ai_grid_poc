package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool           *pgxpool.Pool
	logger         *slog.Logger
	projectCache   sync.Map
}

func NewStore(pool *pgxpool.Pool, logger *slog.Logger) *Store {
	return &Store{pool: pool, logger: logger}
}

func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) DefaultProjectForTenant(ctx context.Context, tenantID string) string {
	if tenantID == "" {
		return "default"
	}
	if cached, ok := s.projectCache.Load(tenantID); ok {
		return cached.(string)
	}
	var projectID string
	err := s.pool.QueryRow(ctx,
		"SELECT project_id FROM inventory_project WHERE tenant = $1 LIMIT 1",
		tenantID).Scan(&projectID)
	if err != nil || projectID == "" {
		projectID = "default"
	}
	s.projectCache.Store(tenantID, projectID)
	return projectID
}

// RunMigrations creates the inventory tables if they don't exist.
func (s *Store) RunMigrations(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, schema); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, schemaEvolutions)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS raw_events (
    id             BIGSERIAL PRIMARY KEY,
    event_id       TEXT NOT NULL,
    event_type     TEXT NOT NULL,
    event_source   TEXT NOT NULL DEFAULT '',
    event_time     TIMESTAMPTZ NOT NULL,
    tenant_id      TEXT NOT NULL DEFAULT '',
    resource_type  TEXT NOT NULL DEFAULT '',
    resource_id    TEXT NOT NULL DEFAULT '',
    data           JSONB NOT NULL,
    received_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- No unique index on event_id by default — raw_events is an append-only
-- log and the unique check was 33% of ingest handler time (profiled).
-- Dedup that matters for correctness is at the metering/cost level.
-- To enable event dedup at the cost of throughput:
--   CREATE UNIQUE INDEX idx_raw_events_event_id ON raw_events (event_id);
CREATE INDEX IF NOT EXISTS idx_raw_events_event_id ON raw_events (event_id);
CREATE INDEX IF NOT EXISTS idx_raw_events_tenant_time ON raw_events (tenant_id, event_time DESC);
CREATE INDEX IF NOT EXISTS idx_raw_events_type_time ON raw_events (event_type, event_time DESC);

CREATE TABLE IF NOT EXISTS inventory_project (
    project_id     TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    tenant         TEXT NOT NULL DEFAULT '',
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_updated   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_proj_tenant ON inventory_project (tenant);

CREATE TABLE IF NOT EXISTS inventory_tenant (
    tenant_id      TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_updated   TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS inventory_compute_instance (
    instance_id    TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    tenant         TEXT NOT NULL DEFAULT '',
    project        TEXT NOT NULL DEFAULT '',
    cluster_id     TEXT NOT NULL DEFAULT '',
    instance_type  TEXT NOT NULL DEFAULT '',
    cores          INTEGER NOT NULL DEFAULT 0,
    memory_gib     INTEGER NOT NULL DEFAULT 0,
    state          TEXT NOT NULL DEFAULT '',
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT NOT NULL DEFAULT '',
    last_updated   TIMESTAMPTZ DEFAULT NOW(),
    last_metered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ci_alive ON inventory_compute_instance (deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_ci_tenant ON inventory_compute_instance (tenant);
CREATE INDEX IF NOT EXISTS idx_ci_period ON inventory_compute_instance (created_at, deleted_at);

CREATE TABLE IF NOT EXISTS inventory_cluster (
    cluster_id     TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    tenant         TEXT NOT NULL DEFAULT '',
    template       TEXT NOT NULL DEFAULT '',
    node_sets      JSONB DEFAULT '{}'::jsonb,
    state          TEXT NOT NULL DEFAULT '',
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT NOT NULL DEFAULT '',
    last_updated   TIMESTAMPTZ DEFAULT NOW(),
    last_metered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_cl_alive ON inventory_cluster (deleted_at) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS inventory_model (
    model_id       TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    model_name     TEXT NOT NULL DEFAULT '',
    tenant         TEXT NOT NULL DEFAULT '',
    project        TEXT NOT NULL DEFAULT '',
    template       TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT '',
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT NOT NULL DEFAULT '',
    last_updated   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_model_alive ON inventory_model (deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_model_tenant ON inventory_model (tenant);

CREATE TABLE IF NOT EXISTS inventory_bare_metal_instance (
    instance_id    TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    tenant         TEXT NOT NULL DEFAULT '',
    catalog_item   TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT '',
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT NOT NULL DEFAULT '',
    last_updated   TIMESTAMPTZ DEFAULT NOW(),
    last_metered_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_bm_alive ON inventory_bare_metal_instance (deleted_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_bm_tenant ON inventory_bare_metal_instance (tenant);

CREATE TABLE IF NOT EXISTS inventory_catalog_item (
    catalog_item_id TEXT PRIMARY KEY,
    item_type       TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL DEFAULT '',
    title           TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    template        TEXT NOT NULL DEFAULT '',
    published       BOOLEAN NOT NULL DEFAULT false,
    tenant          TEXT NOT NULL DEFAULT '',
    last_updated    TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS inventory_instance_type (
    instance_type_id TEXT PRIMARY KEY,
    name             TEXT NOT NULL DEFAULT '',
    cores            INTEGER NOT NULL DEFAULT 0,
    memory_gib       INTEGER NOT NULL DEFAULT 0,
    state            TEXT NOT NULL DEFAULT '',
    last_updated     TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS metering_entries (
    id             BIGSERIAL PRIMARY KEY,
    raw_event_id   BIGINT,
    resource_type  TEXT NOT NULL,
    resource_id    TEXT NOT NULL,
    tenant_id      TEXT NOT NULL DEFAULT '',
    meter_name     TEXT NOT NULL,
    value          NUMERIC(18,6) NOT NULL,
    unit           TEXT NOT NULL,
    period_start   TIMESTAMPTZ NOT NULL,
    period_end     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_me_tenant_meter ON metering_entries (tenant_id, meter_name, period_start, period_end);
CREATE INDEX IF NOT EXISTS idx_me_resource ON metering_entries (resource_id, meter_name);

CREATE TABLE IF NOT EXISTS rates (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      TEXT,
    resource_type  TEXT NOT NULL,
    meter_name     TEXT NOT NULL,
    koku_metric    TEXT NOT NULL DEFAULT '',
    cost_type      TEXT NOT NULL DEFAULT 'Infrastructure',
    price_per_unit NUMERIC(18,10) NOT NULL,
    currency       TEXT NOT NULL DEFAULT 'USD',
    tiers          JSONB,
    description    TEXT NOT NULL DEFAULT '',
    effective_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_rates_lookup ON rates (resource_type, meter_name, effective_from);

CREATE TABLE IF NOT EXISTS cost_entries (
    id                BIGSERIAL PRIMARY KEY,
    metering_entry_id BIGINT NOT NULL,
    rate_id           BIGINT NOT NULL,
    tenant_id         TEXT NOT NULL DEFAULT '',
    resource_type     TEXT NOT NULL,
    resource_id       TEXT NOT NULL,
    meter_name        TEXT NOT NULL,
    metered_value     NUMERIC(18,6) NOT NULL,
    cost_amount       NUMERIC(18,10) NOT NULL,
    currency          TEXT NOT NULL DEFAULT 'USD',
    period_start      TIMESTAMPTZ NOT NULL,
    period_end        TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ce_tenant_period ON cost_entries (tenant_id, period_start, period_end);
CREATE INDEX IF NOT EXISTS idx_ce_metering ON cost_entries (metering_entry_id);

CREATE TABLE IF NOT EXISTS quotas (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    project_id     TEXT NOT NULL DEFAULT '',
    resource_type  TEXT NOT NULL DEFAULT '',
    meter_name     TEXT NOT NULL,
    limit_value    NUMERIC(18,6) NOT NULL,
    unit           TEXT NOT NULL,
    period         TEXT NOT NULL DEFAULT 'monthly',
    effective_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_quotas_tenant ON quotas (tenant_id, meter_name);

CREATE TABLE IF NOT EXISTS alerts (
    id             BIGSERIAL PRIMARY KEY,
    tenant_id      TEXT NOT NULL,
    meter_name     TEXT NOT NULL,
    threshold_pct  NUMERIC NOT NULL,
    consumed       NUMERIC(18,6) NOT NULL,
    limit_value    NUMERIC(18,6) NOT NULL,
    period         TEXT NOT NULL,
    state          TEXT NOT NULL DEFAULT 'firing',
    fired_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(tenant_id, meter_name, threshold_pct, period)
);

CREATE INDEX IF NOT EXISTS idx_alerts_tenant ON alerts (tenant_id, period);

-- project dimension on metering and cost entries
ALTER TABLE metering_entries ADD COLUMN IF NOT EXISTS project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE cost_entries ADD COLUMN IF NOT EXISTS project_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_me_project ON metering_entries (project_id);
CREATE INDEX IF NOT EXISTS idx_ce_project ON cost_entries (project_id);

-- user dimension (MaaS per-user attribution)
ALTER TABLE metering_entries ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '';
ALTER TABLE cost_entries ADD COLUMN IF NOT EXISTS user_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_me_user ON metering_entries (user_id);
CREATE INDEX IF NOT EXISTS idx_ce_user ON cost_entries (user_id);
`

const schemaEvolutions = `
-- Columns added after initial release. ADD COLUMN IF NOT EXISTS is
-- idempotent — safe to run on both fresh and existing databases.
ALTER TABLE rates ADD COLUMN IF NOT EXISTS koku_metric TEXT NOT NULL DEFAULT '';
ALTER TABLE rates ADD COLUMN IF NOT EXISTS cost_type TEXT NOT NULL DEFAULT 'Infrastructure';
ALTER TABLE rates ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE rates ADD COLUMN IF NOT EXISTS effective_to TIMESTAMPTZ;

-- instance_type dimension on rates and metering for per-SKU pricing (REQ-3b)
ALTER TABLE rates ADD COLUMN IF NOT EXISTS instance_type TEXT NOT NULL DEFAULT '';
ALTER TABLE metering_entries ADD COLUMN IF NOT EXISTS instance_type TEXT NOT NULL DEFAULT '';
DROP INDEX IF EXISTS idx_rates_lookup;
CREATE INDEX IF NOT EXISTS idx_rates_lookup ON rates (resource_type, instance_type, meter_name, effective_from);

-- Drop the unique index on raw_events.event_id if it exists from an older
-- schema. The unique check was 33% of ingest handler time (profiled).
-- Replace with a regular index for lookups.
DROP INDEX IF EXISTS idx_raw_events_event_id;
CREATE INDEX IF NOT EXISTS idx_raw_events_event_id ON raw_events (event_id);

-- rated_at replaces the LEFT JOIN anti-pattern for finding unrated entries.
-- Query changes from "LEFT JOIN cost_entries WHERE ce.id IS NULL" (scans two
-- growing tables) to "WHERE rated_at IS NULL" (indexed, O(unrated)).
ALTER TABLE metering_entries ADD COLUMN IF NOT EXISTS rated_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_me_unrated ON metering_entries (id) WHERE rated_at IS NULL;

CREATE TABLE IF NOT EXISTS splunk_cursor (
    id             INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_sent_id   BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO splunk_cursor (id, last_sent_id) VALUES (1, 0) ON CONFLICT DO NOTHING;
`

// InsertRawEvent appends an event to the immutable audit log.
// By default raw_events has no unique constraint — dedup that matters
// for billing correctness is at the metering/cost level. To enable
// event-level dedup at the cost of ~33% ingest throughput, create a
// unique index: CREATE UNIQUE INDEX ON raw_events (event_id).
func (s *Store) InsertRawEvent(ctx context.Context, ev RawEvent) (bool, error) {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO raw_events
			(event_id, event_type, event_source, event_time, tenant_id, resource_type, resource_id, data, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
	`, ev.EventID, ev.EventType, ev.EventSource, ev.EventTime,
		ev.TenantID, ev.ResourceType, ev.ResourceID, ev.Data)

	if err != nil {
		return false, fmt.Errorf("insert raw event %s: %w", ev.EventID, err)
	}

	s.logger.Debug("stored raw event", "event_id", ev.EventID, "type", ev.EventType, "resource", ev.ResourceType)
	return true, nil
}

// InsertMeteringEntry stores a single metering record.
func (s *Store) InsertMeteringEntry(ctx context.Context, entry MeteringEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO metering_entries
			(raw_event_id, resource_type, resource_id, tenant_id, project_id, user_id, instance_type, meter_name, value, unit, period_start, period_end)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, entry.RawEventID, entry.ResourceType, entry.ResourceID, entry.TenantID,
		entry.ProjectID, entry.UserID, entry.InstanceType, entry.MeterName, entry.Value, entry.Unit, entry.PeriodStart, entry.PeriodEnd)

	if err != nil {
		return fmt.Errorf("insert metering entry %s/%s: %w", entry.ResourceID, entry.MeterName, err)
	}
	return nil
}

func (s *Store) InsertMeteringEntryBatch(ctx context.Context, entries []MeteringEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if len(entries) == 1 {
		return s.InsertMeteringEntry(ctx, entries[0])
	}

	query := "INSERT INTO metering_entries (raw_event_id, resource_type, resource_id, tenant_id, project_id, user_id, instance_type, meter_name, value, unit, period_start, period_end) VALUES "
	args := make([]interface{}, 0, len(entries)*12)
	for i, e := range entries {
		if i > 0 {
			query += ", "
		}
		base := i * 12
		query += fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7, base+8, base+9, base+10, base+11, base+12)
		args = append(args, e.RawEventID, e.ResourceType, e.ResourceID,
			e.TenantID, e.ProjectID, e.UserID, e.InstanceType, e.MeterName, e.Value, e.Unit, e.PeriodStart, e.PeriodEnd)
	}
	_, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("batch insert %d metering entries: %w", len(entries), err)
	}
	return nil
}

// BillableComputeInstances returns alive compute instances in billable states.
func (s *Store) BillableComputeInstances(ctx context.Context) ([]ComputeInstanceRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, name, tenant, project, cluster_id, instance_type, cores, memory_gib, state, labels,
		       created_at, deleted_at, last_event_id, last_updated, last_metered_at
		FROM inventory_compute_instance
		WHERE deleted_at IS NULL AND state = 'COMPUTE_INSTANCE_STATE_RUNNING'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ComputeInstanceRecord
	for rows.Next() {
		var r ComputeInstanceRecord
		if err := rows.Scan(&r.InstanceID, &r.Name, &r.Tenant, &r.Project, &r.ClusterID,
			&r.InstanceType, &r.Cores, &r.MemoryGiB, &r.State, &r.Labels,
			&r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated, &r.LastMeteredAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// UpdateComputeInstanceLastMetered sets last_metered_at for a compute instance.
func (s *Store) UpdateComputeInstanceLastMetered(ctx context.Context, instanceID string, t time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE inventory_compute_instance SET last_metered_at = $2 WHERE instance_id = $1
	`, instanceID, t)
	return err
}

// GetComputeInstance returns a single compute instance by ID.
func (s *Store) GetComputeInstance(ctx context.Context, instanceID string) (*ComputeInstanceRecord, error) {
	var r ComputeInstanceRecord
	err := s.pool.QueryRow(ctx, `
		SELECT instance_id, name, tenant, project, cluster_id, instance_type, cores, memory_gib, state, labels,
		       created_at, deleted_at, last_event_id, last_updated, last_metered_at
		FROM inventory_compute_instance WHERE instance_id = $1
	`, instanceID).Scan(&r.InstanceID, &r.Name, &r.Tenant, &r.Project, &r.ClusterID,
		&r.InstanceType, &r.Cores, &r.MemoryGiB, &r.State, &r.Labels,
		&r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated, &r.LastMeteredAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// BillableClusters returns alive clusters in billable states.
func (s *Store) BillableClusters(ctx context.Context) ([]ClusterRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT cluster_id, name, tenant, template, node_sets, state, labels,
		       created_at, deleted_at, last_event_id, last_updated, last_metered_at
		FROM inventory_cluster
		WHERE deleted_at IS NULL AND state IN ('CLUSTER_STATE_READY', 'CLUSTER_STATE_PROGRESSING')
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ClusterRecord
	for rows.Next() {
		var r ClusterRecord
		if err := rows.Scan(&r.ClusterID, &r.Name, &r.Tenant, &r.Template, &r.NodeSetsJSON,
			&r.State, &r.Labels, &r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated, &r.LastMeteredAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// UpsertModel inserts or updates a model deployment in the inventory.
func (s *Store) UpsertModel(ctx context.Context, rec ModelRecord) error {
	labelsJSON, err := marshalLabels(rec.Labels)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO inventory_model
			(model_id, name, model_name, tenant, project, template, state, labels, created_at, deleted_at, last_event_id, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		ON CONFLICT (model_id) DO UPDATE SET
			name = EXCLUDED.name,
			model_name = EXCLUDED.model_name,
			tenant = EXCLUDED.tenant,
			project = EXCLUDED.project,
			template = EXCLUDED.template,
			state = EXCLUDED.state,
			labels = EXCLUDED.labels,
			deleted_at = EXCLUDED.deleted_at,
			last_event_id = EXCLUDED.last_event_id,
			last_updated = NOW()
	`, rec.ModelID, rec.Name, rec.ModelName, rec.Tenant, rec.Project,
		rec.Template, rec.State, labelsJSON, rec.CreatedAt, rec.DeletedAt, rec.LastEventID)

	if err != nil {
		return fmt.Errorf("upsert model %s: %w", rec.ModelID, err)
	}

	s.logger.Debug("upserted model", "id", rec.ModelID, "model_name", rec.ModelName, "state", rec.State)
	return nil
}

// MarkModelDeleted sets the deleted_at timestamp on a model.
func (s *Store) MarkModelDeleted(ctx context.Context, modelID string, deletedAt time.Time, eventID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE inventory_model
		SET deleted_at = $2, last_event_id = $3, last_updated = NOW()
		WHERE model_id = $1 AND deleted_at IS NULL
	`, modelID, deletedAt, eventID)

	if err != nil {
		return fmt.Errorf("mark model deleted %s: %w", modelID, err)
	}

	s.logger.Debug("marked model deleted", "id", modelID)
	return nil
}

// UpdateClusterLastMetered sets last_metered_at for a cluster.
func (s *Store) UpdateClusterLastMetered(ctx context.Context, clusterID string, t time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE inventory_cluster SET last_metered_at = $2 WHERE cluster_id = $1
	`, clusterID, t)
	return err
}

// UpsertBareMetalInstance inserts or updates a bare metal instance.
func (s *Store) UpsertBareMetalInstance(ctx context.Context, rec BareMetalInstanceRecord) error {
	labelsJSON, err := marshalLabels(rec.Labels)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO inventory_bare_metal_instance
			(instance_id, name, tenant, catalog_item, state, labels, created_at, deleted_at, last_event_id, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
		ON CONFLICT (instance_id) DO UPDATE SET
			name = EXCLUDED.name,
			tenant = EXCLUDED.tenant,
			catalog_item = EXCLUDED.catalog_item,
			state = EXCLUDED.state,
			labels = EXCLUDED.labels,
			deleted_at = EXCLUDED.deleted_at,
			last_event_id = EXCLUDED.last_event_id,
			last_updated = NOW()
	`, rec.InstanceID, rec.Name, rec.Tenant, rec.CatalogItem,
		rec.State, labelsJSON, rec.CreatedAt, rec.DeletedAt, rec.LastEventID)

	if err != nil {
		return fmt.Errorf("upsert bare metal instance %s: %w", rec.InstanceID, err)
	}
	s.logger.Debug("upserted bare metal instance", "id", rec.InstanceID, "name", rec.Name, "state", rec.State)
	return nil
}

// MarkBareMetalInstanceDeleted sets the deleted_at timestamp.
func (s *Store) MarkBareMetalInstanceDeleted(ctx context.Context, instanceID string, deletedAt time.Time, eventID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE inventory_bare_metal_instance
		SET deleted_at = $2, last_event_id = $3, last_updated = NOW()
		WHERE instance_id = $1 AND deleted_at IS NULL
	`, instanceID, deletedAt, eventID)
	return err
}

// BillableBareMetalInstances returns alive bare metal instances in billable states.
func (s *Store) BillableBareMetalInstances(ctx context.Context) ([]BareMetalInstanceRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, name, tenant, catalog_item, state, labels,
		       created_at, deleted_at, last_event_id, last_updated, last_metered_at
		FROM inventory_bare_metal_instance
		WHERE deleted_at IS NULL AND state = 'BARE_METAL_INSTANCE_STATE_RUNNING'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BareMetalInstanceRecord
	for rows.Next() {
		var r BareMetalInstanceRecord
		if err := rows.Scan(&r.InstanceID, &r.Name, &r.Tenant, &r.CatalogItem, &r.State, &r.Labels,
			&r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated, &r.LastMeteredAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ListAliveBareMetalInstances returns all bare metal instances not deleted.
func (s *Store) ListAliveBareMetalInstances(ctx context.Context) ([]BareMetalInstanceRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, name, tenant, catalog_item, state, labels,
		       created_at, deleted_at, last_event_id, last_updated, last_metered_at
		FROM inventory_bare_metal_instance WHERE deleted_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []BareMetalInstanceRecord
	for rows.Next() {
		var r BareMetalInstanceRecord
		if err := rows.Scan(&r.InstanceID, &r.Name, &r.Tenant, &r.CatalogItem, &r.State, &r.Labels,
			&r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated, &r.LastMeteredAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// UpdateBareMetalInstanceLastMetered sets last_metered_at.
func (s *Store) UpdateBareMetalInstanceLastMetered(ctx context.Context, instanceID string, t time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE inventory_bare_metal_instance SET last_metered_at = $2 WHERE instance_id = $1
	`, instanceID, t)
	return err
}

// GetBareMetalInstance returns a single bare metal instance by ID.
func (s *Store) GetBareMetalInstance(ctx context.Context, instanceID string) (*BareMetalInstanceRecord, error) {
	var r BareMetalInstanceRecord
	err := s.pool.QueryRow(ctx, `
		SELECT instance_id, name, tenant, catalog_item, state, labels,
		       created_at, deleted_at, last_event_id, last_updated, last_metered_at
		FROM inventory_bare_metal_instance WHERE instance_id = $1
	`, instanceID).Scan(&r.InstanceID, &r.Name, &r.Tenant, &r.CatalogItem, &r.State, &r.Labels,
		&r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated, &r.LastMeteredAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// UpsertProject inserts or updates a project in the inventory.
func (s *Store) UpsertProject(ctx context.Context, rec ProjectRecord) error {
	labelsJSON, err := marshalLabels(rec.Labels)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO inventory_project
			(project_id, name, tenant, labels, created_at, deleted_at, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (project_id) DO UPDATE SET
			name = EXCLUDED.name,
			tenant = EXCLUDED.tenant,
			labels = EXCLUDED.labels,
			deleted_at = EXCLUDED.deleted_at,
			last_updated = NOW()
	`, rec.ProjectID, rec.Name, rec.Tenant, labelsJSON, rec.CreatedAt, rec.DeletedAt)

	if err != nil {
		return fmt.Errorf("upsert project %s: %w", rec.ProjectID, err)
	}

	s.projectCache.Delete(rec.Tenant)
	s.logger.Debug("upserted project", "id", rec.ProjectID, "name", rec.Name)
	return nil
}

// ListAliveProjects returns all projects not yet deleted.
func (s *Store) ListAliveProjects(ctx context.Context) ([]ProjectRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT project_id, name, tenant, labels, created_at, deleted_at, last_updated
		FROM inventory_project WHERE deleted_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ProjectRecord
	for rows.Next() {
		var r ProjectRecord
		if err := rows.Scan(&r.ProjectID, &r.Name, &r.Tenant, &r.Labels,
			&r.CreatedAt, &r.DeletedAt, &r.LastUpdated); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// UpsertTenant inserts or updates a tenant in the inventory.
func (s *Store) UpsertTenant(ctx context.Context, rec TenantRecord) error {
	labelsJSON, err := marshalLabels(rec.Labels)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO inventory_tenant
			(tenant_id, name, labels, created_at, deleted_at, last_updated)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (tenant_id) DO UPDATE SET
			name = EXCLUDED.name,
			labels = EXCLUDED.labels,
			deleted_at = EXCLUDED.deleted_at,
			last_updated = NOW()
	`, rec.TenantID, rec.Name, labelsJSON, rec.CreatedAt, rec.DeletedAt)

	if err != nil {
		return fmt.Errorf("upsert tenant %s: %w", rec.TenantID, err)
	}

	s.logger.Debug("upserted tenant", "id", rec.TenantID, "name", rec.Name)
	return nil
}

// ListAliveTenants returns all tenants not yet deleted.
func (s *Store) ListAliveTenants(ctx context.Context) ([]TenantRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tenant_id, name, labels, created_at, deleted_at, last_updated
		FROM inventory_tenant WHERE deleted_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []TenantRecord
	for rows.Next() {
		var r TenantRecord
		if err := rows.Scan(&r.TenantID, &r.Name, &r.Labels,
			&r.CreatedAt, &r.DeletedAt, &r.LastUpdated); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// UpsertComputeInstance inserts or updates a compute instance in the inventory.
func (s *Store) UpsertComputeInstance(ctx context.Context, rec ComputeInstanceRecord) error {
	labelsJSON, err := marshalLabels(rec.Labels)
	if err != nil {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO inventory_compute_instance
			(instance_id, name, tenant, project, cluster_id, instance_type, cores, memory_gib, state, labels, created_at, deleted_at, last_event_id, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
		ON CONFLICT (instance_id) DO UPDATE SET
			name = EXCLUDED.name,
			tenant = EXCLUDED.tenant,
			project = EXCLUDED.project,
			cluster_id = EXCLUDED.cluster_id,
			instance_type = EXCLUDED.instance_type,
			cores = EXCLUDED.cores,
			memory_gib = EXCLUDED.memory_gib,
			state = EXCLUDED.state,
			labels = EXCLUDED.labels,
			deleted_at = EXCLUDED.deleted_at,
			last_event_id = EXCLUDED.last_event_id,
			last_updated = NOW()
	`, rec.InstanceID, rec.Name, rec.Tenant, rec.Project, rec.ClusterID,
		rec.InstanceType, rec.Cores, rec.MemoryGiB, rec.State, labelsJSON,
		rec.CreatedAt, rec.DeletedAt, rec.LastEventID)

	if err != nil {
		return fmt.Errorf("upsert compute instance %s: %w", rec.InstanceID, err)
	}

	s.logger.Debug("upserted compute instance", "id", rec.InstanceID, "name", rec.Name, "state", rec.State)
	return nil
}

// MarkComputeInstanceDeleted sets the deleted_at timestamp on a compute instance.
func (s *Store) MarkComputeInstanceDeleted(ctx context.Context, instanceID string, deletedAt time.Time, eventID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE inventory_compute_instance
		SET deleted_at = $2, last_event_id = $3, last_updated = NOW()
		WHERE instance_id = $1 AND deleted_at IS NULL
	`, instanceID, deletedAt, eventID)

	if err != nil {
		return fmt.Errorf("mark compute instance deleted %s: %w", instanceID, err)
	}

	s.logger.Debug("marked compute instance deleted", "id", instanceID)
	return nil
}

// UpsertCluster inserts or updates a cluster in the inventory.
func (s *Store) UpsertCluster(ctx context.Context, rec ClusterRecord) error {
	labelsJSON, err := marshalLabels(rec.Labels)
	if err != nil {
		return err
	}

	nodeSetsJSON := rec.NodeSetsJSON
	if nodeSetsJSON == nil {
		nodeSetsJSON = json.RawMessage(`{}`)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO inventory_cluster
			(cluster_id, name, tenant, template, node_sets, state, labels, created_at, deleted_at, last_event_id, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())
		ON CONFLICT (cluster_id) DO UPDATE SET
			name = EXCLUDED.name,
			tenant = EXCLUDED.tenant,
			template = EXCLUDED.template,
			node_sets = EXCLUDED.node_sets,
			state = EXCLUDED.state,
			labels = EXCLUDED.labels,
			deleted_at = EXCLUDED.deleted_at,
			last_event_id = EXCLUDED.last_event_id,
			last_updated = NOW()
	`, rec.ClusterID, rec.Name, rec.Tenant, rec.Template, nodeSetsJSON,
		rec.State, labelsJSON, rec.CreatedAt, rec.DeletedAt, rec.LastEventID)

	if err != nil {
		return fmt.Errorf("upsert cluster %s: %w", rec.ClusterID, err)
	}

	s.logger.Debug("upserted cluster", "id", rec.ClusterID, "name", rec.Name)
	return nil
}

// MarkClusterDeleted sets the deleted_at timestamp on a cluster.
func (s *Store) MarkClusterDeleted(ctx context.Context, clusterID string, deletedAt time.Time, eventID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE inventory_cluster
		SET deleted_at = $2, last_event_id = $3, last_updated = NOW()
		WHERE cluster_id = $1 AND deleted_at IS NULL
	`, clusterID, deletedAt, eventID)

	if err != nil {
		return fmt.Errorf("mark cluster deleted %s: %w", clusterID, err)
	}

	s.logger.Debug("marked cluster deleted", "id", clusterID)
	return nil
}

// UpsertInstanceType inserts or updates an instance type (for cost lookups).
func (s *Store) UpsertInstanceType(ctx context.Context, rec InstanceTypeRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO inventory_instance_type
			(instance_type_id, name, cores, memory_gib, state, last_updated)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (instance_type_id) DO UPDATE SET
			name = EXCLUDED.name,
			cores = EXCLUDED.cores,
			memory_gib = EXCLUDED.memory_gib,
			state = EXCLUDED.state,
			last_updated = NOW()
	`, rec.InstanceTypeID, rec.Name, rec.Cores, rec.MemoryGiB, rec.State)

	if err != nil {
		return fmt.Errorf("upsert instance type %s: %w", rec.InstanceTypeID, err)
	}
	return nil
}

// GetInstanceType returns the specs for an instance type.
func (s *Store) GetInstanceType(ctx context.Context, id string) (*InstanceTypeRecord, error) {
	var rec InstanceTypeRecord
	err := s.pool.QueryRow(ctx, `
		SELECT instance_type_id, name, cores, memory_gib, state, last_updated
		FROM inventory_instance_type WHERE instance_type_id = $1
	`, id).Scan(&rec.InstanceTypeID, &rec.Name, &rec.Cores, &rec.MemoryGiB, &rec.State, &rec.LastUpdated)

	if err != nil {
		return nil, fmt.Errorf("get instance type %s: %w", id, err)
	}
	return &rec, nil
}

// ListAllInstanceTypes returns all instance types for batch lookups.
func (s *Store) ListAllInstanceTypes(ctx context.Context) ([]InstanceTypeRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_type_id, name, cores, memory_gib, state, last_updated
		FROM inventory_instance_type
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []InstanceTypeRecord
	for rows.Next() {
		var r InstanceTypeRecord
		if err := rows.Scan(&r.InstanceTypeID, &r.Name, &r.Cores, &r.MemoryGiB, &r.State, &r.LastUpdated); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// UpsertCatalogItem inserts or updates a catalog item (SKU definition).
func (s *Store) UpsertCatalogItem(ctx context.Context, rec CatalogItemRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO inventory_catalog_item
			(catalog_item_id, item_type, name, title, description, template, published, tenant, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (catalog_item_id) DO UPDATE SET
			name = EXCLUDED.name,
			title = EXCLUDED.title,
			description = EXCLUDED.description,
			template = EXCLUDED.template,
			published = EXCLUDED.published,
			tenant = EXCLUDED.tenant,
			last_updated = NOW()
	`, rec.CatalogItemID, rec.ItemType, rec.Name, rec.Title, rec.Description,
		rec.Template, rec.Published, rec.Tenant)

	if err != nil {
		return fmt.Errorf("upsert catalog item %s: %w", rec.CatalogItemID, err)
	}
	s.logger.Debug("upserted catalog item", "id", rec.CatalogItemID, "type", rec.ItemType, "title", rec.Title)
	return nil
}

// ListAliveComputeInstances returns all compute instances not yet deleted.
func (s *Store) ListAliveComputeInstances(ctx context.Context) ([]ComputeInstanceRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, name, tenant, project, cluster_id, instance_type, cores, memory_gib, state, labels,
		       created_at, deleted_at, last_event_id, last_updated, last_metered_at
		FROM inventory_compute_instance WHERE deleted_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ComputeInstanceRecord
	for rows.Next() {
		var r ComputeInstanceRecord
		if err := rows.Scan(&r.InstanceID, &r.Name, &r.Tenant, &r.Project, &r.ClusterID,
			&r.InstanceType, &r.Cores, &r.MemoryGiB, &r.State, &r.Labels,
			&r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated, &r.LastMeteredAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// ListAliveClusters returns all clusters not yet deleted.
func (s *Store) ListAliveClusters(ctx context.Context) ([]ClusterRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT cluster_id, name, tenant, template, node_sets, state, labels,
		       created_at, deleted_at, last_event_id, last_updated, last_metered_at
		FROM inventory_cluster WHERE deleted_at IS NULL
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ClusterRecord
	for rows.Next() {
		var r ClusterRecord
		if err := rows.Scan(&r.ClusterID, &r.Name, &r.Tenant, &r.Template, &r.NodeSetsJSON,
			&r.State, &r.Labels, &r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated, &r.LastMeteredAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// UpsertRate inserts or updates a rate definition.
func (s *Store) UpsertRate(ctx context.Context, rec RateRecord) (int64, error) {
	var tiersJSON []byte
	if rec.Tiers != nil {
		var err error
		tiersJSON, err = json.Marshal(rec.Tiers)
		if err != nil {
			return 0, err
		}
	}

	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO rates
			(tenant_id, resource_type, instance_type, meter_name, koku_metric, cost_type, price_per_unit, currency, tiers, description, effective_from, effective_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, rec.TenantID, rec.ResourceType, rec.InstanceType, rec.MeterName, rec.KokuMetric, rec.CostType,
		rec.PricePerUnit, rec.Currency, tiersJSON, rec.Description,
		rec.EffectiveFrom, rec.EffectiveTo).Scan(&id)

	if err != nil {
		// ON CONFLICT DO NOTHING means no row returned if it already exists.
		// That's fine — return 0 to indicate no insert.
		return 0, nil
	}
	return id, nil
}

// FindRate looks up the applicable rate for a meter. Prefers tenant-specific
// and instance-type-specific rates over global defaults.
func (s *Store) FindRate(ctx context.Context, tenantID, resourceType, instanceType, meterName string, at time.Time) (*RateRecord, error) {
	var rec RateRecord
	var tiersJSON []byte

	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, resource_type, instance_type, meter_name, koku_metric, cost_type,
		       price_per_unit, currency, tiers, description, effective_from, effective_to
		FROM rates
		WHERE resource_type = $1 AND meter_name = $2
		  AND effective_from <= $3
		  AND (effective_to IS NULL OR effective_to > $3)
		  AND (tenant_id = $4 OR tenant_id IS NULL OR tenant_id = '')
		  AND (instance_type = $5 OR instance_type = '')
		ORDER BY CASE WHEN tenant_id = $4 THEN 0 ELSE 1 END,
		         CASE WHEN instance_type = $5 THEN 0 ELSE 1 END
		LIMIT 1
	`, resourceType, meterName, at, tenantID, instanceType).Scan(
		&rec.ID, &rec.TenantID, &rec.ResourceType, &rec.InstanceType, &rec.MeterName,
		&rec.KokuMetric, &rec.CostType,
		&rec.PricePerUnit, &rec.Currency, &tiersJSON, &rec.Description,
		&rec.EffectiveFrom, &rec.EffectiveTo)

	if err != nil {
		return nil, err
	}

	if tiersJSON != nil {
		if err := json.Unmarshal(tiersJSON, &rec.Tiers); err != nil {
			return nil, fmt.Errorf("unmarshal tiers for rate %d: %w", rec.ID, err)
		}
	}

	return &rec, nil
}

// UnratedMeteringEntries returns metering entries not yet rated.
// Uses a partial index on (id) WHERE rated_at IS NULL — O(unrated), not O(total).
func (s *Store) UnratedMeteringEntries(ctx context.Context, limit int) ([]MeteringEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, raw_event_id, resource_type, resource_id, tenant_id,
		       project_id, user_id, instance_type, meter_name, value, unit, period_start, period_end
		FROM metering_entries
		WHERE rated_at IS NULL
		ORDER BY id
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MeteringEntry
	for rows.Next() {
		var r MeteringEntry
		if err := rows.Scan(&r.ID, &r.RawEventID, &r.ResourceType, &r.ResourceID,
			&r.TenantID, &r.ProjectID, &r.UserID, &r.InstanceType, &r.MeterName, &r.Value, &r.Unit, &r.PeriodStart, &r.PeriodEnd); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// MarkMeteringEntriesRated sets rated_at on the given entry IDs.
func (s *Store) MarkMeteringEntriesRated(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	query := "UPDATE metering_entries SET rated_at = NOW() WHERE id = ANY($1)"
	_, err := s.pool.Exec(ctx, query, ids)
	return err
}

// AllActiveRates returns all rates currently in effect.
func (s *Store) AllActiveRates(ctx context.Context, at time.Time) ([]RateRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, resource_type, instance_type, meter_name, koku_metric, cost_type,
		       price_per_unit, currency, tiers, description, effective_from, effective_to
		FROM rates
		WHERE effective_from <= $1
		  AND (effective_to IS NULL OR effective_to > $1)
		ORDER BY resource_type, meter_name, CASE WHEN tenant_id IS NOT NULL AND tenant_id != '' THEN 0 ELSE 1 END
	`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RateRecord
	for rows.Next() {
		var r RateRecord
		var tiersJSON []byte
		if err := rows.Scan(&r.ID, &r.TenantID, &r.ResourceType, &r.InstanceType, &r.MeterName,
			&r.KokuMetric, &r.CostType, &r.PricePerUnit, &r.Currency, &tiersJSON,
			&r.Description, &r.EffectiveFrom, &r.EffectiveTo); err != nil {
			return nil, err
		}
		if tiersJSON != nil {
			if err := json.Unmarshal(tiersJSON, &r.Tiers); err != nil {
				return nil, fmt.Errorf("unmarshal tiers for rate %d: %w", r.ID, err)
			}
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// InsertCostEntryBatch inserts multiple cost entries in a single statement.
func (s *Store) InsertCostEntryBatch(ctx context.Context, entries []CostEntry) error {
	if len(entries) == 0 {
		return nil
	}
	if len(entries) == 1 {
		return s.InsertCostEntry(ctx, entries[0])
	}

	query := "INSERT INTO cost_entries (metering_entry_id, rate_id, tenant_id, project_id, user_id, resource_type, resource_id, meter_name, metered_value, cost_amount, currency, period_start, period_end) VALUES "
	args := make([]interface{}, 0, len(entries)*13)
	for i, e := range entries {
		if i > 0 {
			query += ", "
		}
		base := i * 13
		query += fmt.Sprintf("($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6, base+7,
			base+8, base+9, base+10, base+11, base+12, base+13)
		args = append(args, e.MeteringEntryID, e.RateID, e.TenantID, e.ProjectID, e.UserID,
			e.ResourceType, e.ResourceID, e.MeterName, e.MeteredValue,
			e.CostAmount, e.Currency, e.PeriodStart, e.PeriodEnd)
	}
	_, err := s.pool.Exec(ctx, query, args...)
	return err
}

// InsertCostEntry stores a computed cost record.
func (s *Store) InsertCostEntry(ctx context.Context, entry CostEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO cost_entries
			(metering_entry_id, rate_id, tenant_id, project_id, user_id, resource_type, resource_id, meter_name,
			 metered_value, cost_amount, currency, period_start, period_end)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, entry.MeteringEntryID, entry.RateID, entry.TenantID, entry.ProjectID, entry.UserID, entry.ResourceType,
		entry.ResourceID, entry.MeterName, entry.MeteredValue, entry.CostAmount,
		entry.Currency, entry.PeriodStart, entry.PeriodEnd)

	return err
}

// RateCount returns the number of rates in the table.
func (s *Store) RateCount(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM rates`).Scan(&count)
	return count, err
}

// UpsertQuota inserts a quota definition.
func (s *Store) UpsertQuota(ctx context.Context, q QuotaRecord) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO quotas
			(tenant_id, project_id, resource_type, meter_name, limit_value, unit, period, effective_from, effective_to)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id
	`, q.TenantID, q.ProjectID, q.ResourceType, q.MeterName, q.LimitValue,
		q.Unit, q.Period, q.EffectiveFrom, q.EffectiveTo).Scan(&id)

	if err != nil {
		return 0, fmt.Errorf("upsert quota: %w", err)
	}
	return id, nil
}

// QuotasForTenant returns all active quotas for a tenant.
func (s *Store) QuotasForTenant(ctx context.Context, tenantID string, at time.Time) ([]QuotaRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, project_id, resource_type, meter_name, limit_value, unit, period, effective_from, effective_to
		FROM quotas
		WHERE tenant_id = $1
		  AND effective_from <= $2
		  AND (effective_to IS NULL OR effective_to > $2)
		ORDER BY meter_name
	`, tenantID, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []QuotaRecord
	for rows.Next() {
		var r QuotaRecord
		if err := rows.Scan(&r.ID, &r.TenantID, &r.ProjectID, &r.ResourceType, &r.MeterName,
			&r.LimitValue, &r.Unit, &r.Period, &r.EffectiveFrom, &r.EffectiveTo); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// MeteringSum returns the total metered value for a tenant + meter in a time range.
func (s *Store) MeteringSum(ctx context.Context, tenantID, meterName string, from, to time.Time) (float64, error) {
	var sum float64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(value), 0)
		FROM metering_entries
		WHERE tenant_id = $1 AND meter_name = $2
		  AND period_start >= $3 AND period_end <= $4
	`, tenantID, meterName, from, to).Scan(&sum)
	return sum, err
}

// CostSum returns the total cost for a tenant + meter in a time range.
func (s *Store) CostSum(ctx context.Context, tenantID, meterName string, from, to time.Time) (float64, error) {
	var sum float64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_amount), 0)
		FROM cost_entries
		WHERE tenant_id = $1 AND meter_name = $2
		  AND period_start >= $3 AND period_end <= $4
	`, tenantID, meterName, from, to).Scan(&sum)
	return sum, err
}

// QuotaCount returns the number of quotas in the table.
func (s *Store) QuotaCount(ctx context.Context) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM quotas`).Scan(&count)
	return count, err
}

// InsertAlert records a threshold breach. Returns false if already fired
// (UNIQUE constraint on tenant+meter+threshold+period).
func (s *Store) InsertAlert(ctx context.Context, alert AlertRecord) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO alerts
			(tenant_id, meter_name, threshold_pct, consumed, limit_value, period, state, fired_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
		ON CONFLICT (tenant_id, meter_name, threshold_pct, period) DO NOTHING
	`, alert.TenantID, alert.MeterName, alert.ThresholdPct, alert.Consumed,
		alert.LimitValue, alert.Period, "firing")

	if err != nil {
		return false, fmt.Errorf("insert alert: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// AlertsForTenant returns all alerts for a tenant in a period.
func (s *Store) AlertsForTenant(ctx context.Context, tenantID, period string) ([]AlertRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, meter_name, threshold_pct, consumed, limit_value, period, state, fired_at
		FROM alerts
		WHERE tenant_id = $1 AND period = $2
		ORDER BY meter_name, threshold_pct
	`, tenantID, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []AlertRecord
	for rows.Next() {
		var r AlertRecord
		if err := rows.Scan(&r.ID, &r.TenantID, &r.MeterName, &r.ThresholdPct,
			&r.Consumed, &r.LimitValue, &r.Period, &r.State, &r.FiredAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// AlertsForTenantMeter returns alerts for a specific tenant + meter + period.
func (s *Store) AlertsForTenantMeter(ctx context.Context, tenantID, meterName, period string) ([]AlertRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, meter_name, threshold_pct, consumed, limit_value, period, state, fired_at
		FROM alerts
		WHERE tenant_id = $1 AND meter_name = $2 AND period = $3
		ORDER BY threshold_pct
	`, tenantID, meterName, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []AlertRecord
	for rows.Next() {
		var r AlertRecord
		if err := rows.Scan(&r.ID, &r.TenantID, &r.MeterName, &r.ThresholdPct,
			&r.Consumed, &r.LimitValue, &r.Period, &r.State, &r.FiredAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// AllTenantsWithQuotas returns distinct tenant IDs that have active quotas.
func (s *Store) AllTenantsWithQuotas(ctx context.Context, at time.Time) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT tenant_id FROM quotas
		WHERE effective_from <= $1 AND (effective_to IS NULL OR effective_to > $1)
	`, at)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

func marshalLabels(labels json.RawMessage) ([]byte, error) {
	if labels == nil {
		return []byte(`{}`), nil
	}
	return labels, nil
}

// CostReport returns aggregated cost data grouped by the specified dimension.
// groupBy: "tenant", "resource_type", "meter", "resource", "project", "user".
// resolution: "" (aggregate) or "daily" (per-date breakdown).
func (s *Store) CostReport(ctx context.Context, tenantID, resourceType, groupBy, resolution string, from, to time.Time) ([]CostReportRow, error) {
	var groupCol string
	switch groupBy {
	case "resource_type":
		groupCol = "ce.resource_type"
	case "meter":
		groupCol = "ce.meter_name"
	case "resource":
		groupCol = "ce.resource_id"
	case "project":
		groupCol = "ce.project_id"
	case "user":
		groupCol = "ce.user_id"
	default:
		groupCol = "ce.tenant_id"
	}

	where := "WHERE ce.period_start >= $1 AND ce.period_end <= $2"
	args := []any{from, to}
	argN := 3

	if tenantID != "" {
		where += fmt.Sprintf(" AND ce.tenant_id = $%d", argN)
		args = append(args, tenantID)
		argN++
	}
	if resourceType != "" {
		where += fmt.Sprintf(" AND ce.resource_type = $%d", argN)
		args = append(args, resourceType)
	}

	var dateCol, dateSelect, dateGroup, dateOrder string
	if resolution == "daily" {
		dateCol = "ce.period_start::date"
		dateSelect = fmt.Sprintf("%s AS dt, ", dateCol)
		dateGroup = fmt.Sprintf("%s, ", dateCol)
		dateOrder = fmt.Sprintf("%s, ", dateCol)
	}

	query := fmt.Sprintf(`
		SELECT %s%s AS grp,
		       count(*)::int AS entries,
		       COALESCE(SUM(ce.cost_amount), 0) AS cost,
		       COALESCE(SUM(CASE WHEN r.cost_type = 'Infrastructure' THEN ce.cost_amount ELSE 0 END), 0) AS infra_cost,
		       COALESCE(SUM(CASE WHEN r.cost_type = 'Supplementary' THEN ce.cost_amount ELSE 0 END), 0) AS supp_cost,
		       ce.currency
		FROM cost_entries ce
		LEFT JOIN rates r ON ce.rate_id = r.id
		%s
		GROUP BY %s%s, ce.currency
		ORDER BY %scost DESC
	`, dateSelect, groupCol, where, dateGroup, groupCol, dateOrder)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cost report: %w", err)
	}
	defer rows.Close()

	var results []CostReportRow
	for rows.Next() {
		var r CostReportRow
		if resolution == "daily" {
			var dt time.Time
			if err := rows.Scan(&dt, &r.Group, &r.Entries, &r.Cost, &r.InfrastructureCost, &r.SupplementaryCost, &r.Currency); err != nil {
				return nil, err
			}
			r.Date = dt.Format("2006-01-02")
		} else {
			if err := rows.Scan(&r.Group, &r.Entries, &r.Cost, &r.InfrastructureCost, &r.SupplementaryCost, &r.Currency); err != nil {
				return nil, err
			}
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// CostBreakdown returns per-resource line items for a time range.
func (s *Store) CostBreakdown(ctx context.Context, tenantID, resourceType string, from, to time.Time, limit int) ([]CostBreakdownRow, error) {
	where := "WHERE ce.period_start >= $1 AND ce.period_end <= $2"
	args := []any{from, to}
	argN := 3

	if tenantID != "" {
		where += fmt.Sprintf(" AND ce.tenant_id = $%d", argN)
		args = append(args, tenantID)
		argN++
	}
	if resourceType != "" {
		where += fmt.Sprintf(" AND ce.resource_type = $%d", argN)
		args = append(args, resourceType)
		argN++
	}

	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT ce.period_start::date AS dt,
		       ce.tenant_id, ce.project_id, ce.user_id, ce.resource_type, ce.resource_id,
		       ce.meter_name, ce.metered_value, ce.cost_amount,
		       COALESCE(r.cost_type, '') AS cost_type,
		       ce.currency
		FROM cost_entries ce
		LEFT JOIN rates r ON ce.rate_id = r.id
		%s
		ORDER BY ce.period_start DESC, ce.cost_amount DESC
		LIMIT $%d
	`, where, argN)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("cost breakdown: %w", err)
	}
	defer rows.Close()

	var results []CostBreakdownRow
	for rows.Next() {
		var r CostBreakdownRow
		var dt time.Time
		if err := rows.Scan(&dt, &r.TenantID, &r.ProjectID, &r.UserID, &r.ResourceType, &r.ResourceID,
			&r.MeterName, &r.MeteredValue, &r.CostAmount, &r.CostType, &r.Currency); err != nil {
			return nil, err
		}
		r.Date = dt.Format("2006-01-02")
		results = append(results, r)
	}
	return results, rows.Err()
}

// PipelineSummary returns counts from all pipeline tables.
func (s *Store) PipelineSummary(ctx context.Context) (*PipelineSummary, error) {
	var ps PipelineSummary
	err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*)::int FROM raw_events),
			(SELECT count(*)::int FROM metering_entries),
			(SELECT count(*)::int FROM cost_entries),
			(SELECT count(*)::int FROM rates),
			(SELECT count(*)::int FROM inventory_compute_instance WHERE deleted_at IS NULL),
			(SELECT count(*)::int FROM inventory_cluster WHERE deleted_at IS NULL),
			(SELECT count(*)::int FROM inventory_model WHERE deleted_at IS NULL)
	`).Scan(&ps.RawEvents, &ps.MeteringEntries, &ps.CostEntries, &ps.Rates,
		&ps.LiveVMs, &ps.LiveClusters, &ps.LiveModels)
	if err != nil {
		return nil, fmt.Errorf("pipeline summary: %w", err)
	}
	return &ps, nil
}

// SplunkCursor returns the last-sent raw_events ID.
func (s *Store) SplunkCursor(ctx context.Context) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, "SELECT last_sent_id FROM splunk_cursor WHERE id = 1").Scan(&id)
	if err != nil {
		return 0, nil
	}
	return id, nil
}

// AdvanceSplunkCursor updates the cursor to the given ID.
func (s *Store) AdvanceSplunkCursor(ctx context.Context, lastSentID int64) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE splunk_cursor SET last_sent_id = $1, updated_at = NOW() WHERE id = 1",
		lastSentID)
	return err
}

// RawEventRow is a raw_events row with its BIGSERIAL id for cursor tracking.
type RawEventRow struct {
	ID           int64           `json:"id"`
	EventID      string          `json:"event_id"`
	EventType    string          `json:"event_type"`
	EventSource  string          `json:"event_source"`
	EventTime    time.Time       `json:"event_time"`
	TenantID     string          `json:"tenant_id"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Data         json.RawMessage `json:"data"`
	ReceivedAt   time.Time       `json:"received_at"`
}

// RawEventsSince returns raw events with id > afterID, ordered by id.
func (s *Store) RawEventsSince(ctx context.Context, afterID int64, limit int) ([]RawEventRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, event_id, event_type, event_source, event_time,
		       tenant_id, resource_type, resource_id, data, received_at
		FROM raw_events WHERE id > $1
		ORDER BY id LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []RawEventRow
	for rows.Next() {
		var r RawEventRow
		if err := rows.Scan(&r.ID, &r.EventID, &r.EventType, &r.EventSource,
			&r.EventTime, &r.TenantID, &r.ResourceType, &r.ResourceID,
			&r.Data, &r.ReceivedAt); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

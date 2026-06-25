package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewStore(pool *pgxpool.Pool, logger *slog.Logger) *Store {
	return &Store{pool: pool, logger: logger}
}

// RunMigrations creates the inventory tables if they don't exist.
func (s *Store) RunMigrations(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schema)
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_raw_events_event_id ON raw_events (event_id);
CREATE INDEX IF NOT EXISTS idx_raw_events_tenant_time ON raw_events (tenant_id, event_time DESC);
CREATE INDEX IF NOT EXISTS idx_raw_events_type_time ON raw_events (event_type, event_time DESC);

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

CREATE TABLE IF NOT EXISTS inventory_instance_type (
    instance_type_id TEXT PRIMARY KEY,
    name             TEXT NOT NULL DEFAULT '',
    cores            INTEGER NOT NULL DEFAULT 0,
    memory_gib       INTEGER NOT NULL DEFAULT 0,
    state            TEXT NOT NULL DEFAULT '',
    last_updated     TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS daily_usage_summary (
    id              BIGSERIAL PRIMARY KEY,
    usage_date      DATE NOT NULL,
    cluster_id      TEXT NOT NULL DEFAULT '',
    tenant          TEXT NOT NULL DEFAULT '',
    project         TEXT NOT NULL DEFAULT '',
    resource_id     TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    instance_type   TEXT NOT NULL DEFAULT '',
    cores           INTEGER NOT NULL DEFAULT 0,
    memory_gib      INTEGER NOT NULL DEFAULT 0,
    cpu_core_hours  NUMERIC(18,6) NOT NULL DEFAULT 0,
    memory_gb_hours NUMERIC(18,6) NOT NULL DEFAULT 0,
    duration_hours  NUMERIC(18,6) NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_dus_date_tenant ON daily_usage_summary (usage_date, tenant);
CREATE INDEX IF NOT EXISTS idx_dus_date_resource ON daily_usage_summary (usage_date, resource_id);

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
`

// InsertRawEvent stores an event immutably. Returns false if the event was
// already stored (duplicate event_id), true if it was inserted.
func (s *Store) InsertRawEvent(ctx context.Context, ev RawEvent) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO raw_events
			(event_id, event_type, event_source, event_time, tenant_id, resource_type, resource_id, data, received_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (event_id) DO NOTHING
	`, ev.EventID, ev.EventType, ev.EventSource, ev.EventTime,
		ev.TenantID, ev.ResourceType, ev.ResourceID, ev.Data)

	if err != nil {
		return false, fmt.Errorf("insert raw event %s: %w", ev.EventID, err)
	}

	inserted := tag.RowsAffected() > 0
	if inserted {
		s.logger.Debug("stored raw event", "event_id", ev.EventID, "type", ev.EventType, "resource", ev.ResourceType)
	} else {
		s.logger.Debug("duplicate event skipped", "event_id", ev.EventID)
	}
	return inserted, nil
}

// InsertMeteringEntry stores a single metering record.
func (s *Store) InsertMeteringEntry(ctx context.Context, entry MeteringEntry) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO metering_entries
			(raw_event_id, resource_type, resource_id, tenant_id, meter_name, value, unit, period_start, period_end)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, entry.RawEventID, entry.ResourceType, entry.ResourceID, entry.TenantID,
		entry.MeterName, entry.Value, entry.Unit, entry.PeriodStart, entry.PeriodEnd)

	if err != nil {
		return fmt.Errorf("insert metering entry %s/%s: %w", entry.ResourceID, entry.MeterName, err)
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

// ComputeInstancesAliveDuring returns instances that overlapped with [start, end).
func (s *Store) ComputeInstancesAliveDuring(ctx context.Context, start, end time.Time) ([]ComputeInstanceRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT instance_id, name, tenant, project, cluster_id, instance_type, cores, memory_gib, state, labels, created_at, deleted_at, last_event_id, last_updated
		FROM inventory_compute_instance
		WHERE created_at < $2 AND (deleted_at IS NULL OR deleted_at > $1)
	`, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ComputeInstanceRecord
	for rows.Next() {
		var r ComputeInstanceRecord
		if err := rows.Scan(&r.InstanceID, &r.Name, &r.Tenant, &r.Project, &r.ClusterID,
			&r.InstanceType, &r.Cores, &r.MemoryGiB, &r.State, &r.Labels,
			&r.CreatedAt, &r.DeletedAt, &r.LastEventID, &r.LastUpdated); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// InsertDailyUsageSummary writes a usage summary row.
func (s *Store) InsertDailyUsageSummary(ctx context.Context, summary DailyUsageSummary) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO daily_usage_summary
			(usage_date, cluster_id, tenant, project, resource_id, resource_type, instance_type, cores, memory_gib, cpu_core_hours, memory_gb_hours, duration_hours)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, summary.UsageDate, summary.ClusterID, summary.Tenant, summary.Project,
		summary.ResourceID, summary.ResourceType, summary.InstanceType,
		summary.Cores, summary.MemoryGiB,
		summary.CPUCoreHours, summary.MemoryGBHours, summary.DurationHours)

	return err
}

// DeleteDailyUsageSummaries removes summaries for a given date (to allow re-summarization).
func (s *Store) DeleteDailyUsageSummaries(ctx context.Context, date time.Time) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM daily_usage_summary WHERE usage_date = $1`, date)
	return err
}

func marshalLabels(labels json.RawMessage) ([]byte, error) {
	if labels == nil {
		return []byte(`{}`), nil
	}
	return labels, nil
}

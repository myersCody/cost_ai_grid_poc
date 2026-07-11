# Koku Integration Spike — Results

**Date:** 2026-07-11
**Status:** Data landed in Koku DB. API rendering blocked on provider registration.

## What We Achieved

1. **Koku running locally** — docker-compose with DB, server, worker, Valkey, Unleash
2. **OSAC table created** — `openshift_osac_usage_line_items_daily` in tenant schema `org1234567`
3. **57 rows synced** from our cost_entries into Koku's database via koku-sync
4. **Data verified** in 3 Koku tables:
   - `org1234567.openshift_osac_usage_line_items_daily` — 57 rows (our OSAC table)
   - `org1234567.reporting_ocpusagelineitem_daily_summary` — 57 rows (Koku's main fact table)
   - `org1234567.reporting_ocp_cost_summary_p` — 1 row, $0.42 infrastructure cost
5. **Koku UI running** — `http://localhost:9001/openshift/cost-management/explorer`
6. **Provider created** in both `api_provider` (public) and `reporting_tenant_api_provider` (tenant)

## What Doesn't Work Yet

The Koku report API returns zero values despite data being in the tables.
The issue: Koku's report query uses `ProviderAccessor` to filter by
registered sources. Our manually-created Provider is not visible through
the Sources API — it likely needs additional Sources-service integration
or specific `setup_complete` / `data_updated_timestamp` flags.

## How to Reproduce

### Prerequisites

```bash
# Our cost-event-consumer DB running on port 5434
docker ps | grep cost-db

# Some cost data in our DB
docker exec cost-db psql -U user -d costdb -c "SELECT count(*) FROM cost_entries"
```

### Step 1: Start Koku

```bash
cd ~/Projects/koku/koku
ONPREM=True docker compose up -d db valkey
ONPREM=True docker compose up -d koku-server koku-worker

# Verify
curl -sf http://localhost:8000/api/cost-management/v1/status/
```

### Step 2: Create OSAC table in Koku's tenant schema

```sql
-- Connect: docker exec -it koku-db psql -U postgres -d postgres

-- Find the tenant schema
SELECT schema_name FROM api_customer;
-- → org1234567

-- Create OSAC table (partitioned by usage_start)
CREATE TABLE IF NOT EXISTS org1234567.openshift_osac_usage_line_items_daily (
    id UUID DEFAULT gen_random_uuid(),
    report_period_start TIMESTAMPTZ,
    report_period_end TIMESTAMPTZ,
    interval_start TIMESTAMPTZ,
    interval_end TIMESTAMPTZ,
    usage_start DATE NOT NULL,
    source VARCHAR(64),
    year VARCHAR(4),
    month VARCHAR(2),
    manifestid VARCHAR(256),
    reportnumhours INTEGER,
    resource_type VARCHAR(64),
    resource_id VARCHAR(256),
    tenant_id VARCHAR(256),
    project_id VARCHAR(256),
    meter_name VARCHAR(128),
    value FLOAT,
    unit VARCHAR(64),
    cost_type VARCHAR(32),
    koku_metric VARCHAR(128),
    cost_amount FLOAT,
    currency VARCHAR(8),
    PRIMARY KEY (id, usage_start)
) PARTITION BY RANGE (usage_start);

-- Create monthly partition
CREATE TABLE IF NOT EXISTS org1234567.openshift_osac_usage_line_items_daily_202607
PARTITION OF org1234567.openshift_osac_usage_line_items_daily
FOR VALUES FROM ('2026-07-01') TO ('2026-08-01');
```

### Step 3: Run koku-sync

```bash
cd inventory-watcher
go build ./cmd/koku-sync/

SYNC_DATE=2026-07-10 \
KOKU_DB_URL="postgres://postgres:postgres@localhost:15432/postgres" \
./koku-sync
```

Expected output:
```
level=INFO msg=connected schema=org1234567 sync_date=2026-07-10
level=INFO msg="ensured TenantAPIProvider"
level=INFO msg="ensured ReportPeriod"
level=INFO msg="fetched cost data" rows=57
level=INFO msg="wrote to Koku OSAC table" rows=57
level=INFO msg="sync complete" date=2026-07-10 rows=57
```

### Step 4: Verify data in Koku DB

```sql
-- OSAC table
SELECT resource_type, meter_name, count(*), sum(cost_amount)
FROM org1234567.openshift_osac_usage_line_items_daily
GROUP BY resource_type, meter_name;

-- Expected:
-- compute_instance | vm_cpu_core_seconds   | 19 | 0.15
-- compute_instance | vm_memory_gib_seconds | 19 | 0.23
-- compute_instance | vm_uptime_seconds     | 19 | 0.04
```

### Step 5: Hack data into daily summary (bypasses pipeline)

```sql
-- Write aggregated data into Koku's main fact table
INSERT INTO org1234567.reporting_ocpusagelineitem_daily_summary (
    uuid, report_period_id, cluster_id, cluster_alias,
    data_source, namespace, node, resource_id,
    usage_start, usage_end,
    pod_request_cpu_core_hours,
    pod_request_memory_gigabyte_hours,
    infrastructure_raw_cost,
    cost_model_rate_type,
    source_uuid, raw_currency
)
SELECT
    gen_random_uuid(), 1,
    'osac-region-1', 'OSAC Sovereign Cloud',
    'OSAC', o.tenant_id, o.resource_id, o.resource_id,
    o.usage_start, o.usage_start,
    CASE WHEN o.meter_name = 'vm_cpu_core_seconds' THEN o.value / 3600.0 END,
    CASE WHEN o.meter_name = 'vm_memory_gib_seconds' THEN o.value / 3600.0 END,
    o.cost_amount, o.cost_type,
    '00000000-0000-0000-0000-0a5ac0000001'::uuid, o.currency
FROM org1234567.openshift_osac_usage_line_items_daily o;

-- Refresh cost summary UI table
INSERT INTO org1234567.reporting_ocp_cost_summary_p (
    id, cluster_id, cluster_alias, usage_start, usage_end,
    infrastructure_raw_cost, infrastructure_markup_cost, source_uuid
)
SELECT gen_random_uuid(), cluster_id, cluster_alias, usage_start, usage_end,
    SUM(COALESCE(infrastructure_raw_cost, 0)), 0, source_uuid
FROM org1234567.reporting_ocpusagelineitem_daily_summary
WHERE source_uuid = '00000000-0000-0000-0000-0a5ac0000001'::uuid
GROUP BY cluster_id, cluster_alias, usage_start, usage_end, source_uuid;
```

### Step 6: Start Koku UI (optional)

```bash
cd ~/Projects/koku/koku-ui
npm install
API_TOKEN=false API_PROXY_URL=http://localhost:8000/api/cost-management/v1 \
    npm run -w @koku-ui/koku-ui-onprem start
# UI at http://localhost:9001
```

## Shortcuts and Hacks

| Shortcut | What we did | Production approach |
|---|---|---|
| Manual table creation | SQL DDL instead of Django migration | Add model + `manage.py makemigrations` |
| Manual partition creation | SQL `CREATE TABLE ... PARTITION OF` | Use Koku's `get_or_create_postgres_partition()` |
| Direct daily summary INSERT | Bypassed summarization pipeline | Trigger via `/report_data/` Masu API |
| Manual Provider creation | Raw SQL INSERT into `api_provider` | Use Koku's Provider REST API |
| No cost model | Pre-rated costs from our pipeline | Create Koku cost model with VM rates |
| No pipeline trigger | Skipped Celery summarization | Call Masu API or trigger Celery task |
| No RBAC | Bypassed with `is_org_admin: true` | Proper RBAC setup |

## Architecture Proven

```
cost-event-consumer (Go)
    → metering_entries + cost_entries (our DB, port 5434)
    → koku-sync (Go binary)
        → openshift_osac_usage_line_items_daily (Koku DB, port 15432, org1234567 schema)
        → [hack] reporting_ocpusagelineitem_daily_summary
        → [hack] reporting_ocp_cost_summary_p

Koku (Python/Django)
    → API reads from UI summary tables
    → UI renders at localhost:9001
```

## Remaining Gaps

| Gap | Effort | Notes |
|---|---|---|
| Provider not visible via Sources API | S | Need proper Sources integration or mock |
| Pipeline not triggered | M | Masu API endpoint or Celery task |
| No Koku cost model for OSAC | S | Create via Cost Models API |
| OSAC SQL UNION in summarization template | Done (in code) | Not tested via pipeline |
| Django migration for OSAC table | S | `manage.py makemigrations` |
| koku-ui navigation to OSAC data | M | May need UI customization |

## Files Changed

### Our repo (cost_ai_grid_poc)
- `inventory-watcher/cmd/koku-sync/main.go` — sync binary
- `docs/research/koku-integration-strategy.md` — full strategy analysis
- `docs/research/koku-integration-review.md` — adversarial review
- `docs/research/koku-integration-spike.md` — this document
- `docs/diagrams/koku-ocp-flow.dot` + `.svg` — data flow diagram

### Koku repo (~/Projects/koku/koku/)
- `koku/reporting/provider/ocp/self_hosted_models.py` — OSACUsageLineItemDaily model
- `koku/masu/database/self_hosted_sql/openshift/reporting_ocpusagelineitem_daily_summary.sql` — OSAC UNION
- `koku/masu/database/ocp_report_db_accessor.py` — osac_exists wiring

### Not committed to Koku yet — changes are local only.

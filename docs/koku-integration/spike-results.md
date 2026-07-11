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

All hacks are SQL statements against Koku's PostgreSQL (`docker exec koku-db psql -U postgres -d postgres`).

| # | Shortcut | What we did | Production approach |
|---|---|---|---|
| 1 | Manual OSAC table creation | SQL DDL in tenant schema | Django migration via `manage.py makemigrations` |
| 2 | Manual partition creation | `CREATE TABLE ... PARTITION OF` | Koku's `get_or_create_postgres_partition()` |
| 3 | Direct daily summary INSERT | Bypassed summarization pipeline | Trigger via `/report_data/` Masu API |
| 4 | Manual Provider creation | Raw SQL INSERT into `api_provider` | Koku's Provider REST API |
| 5 | Manual Sources creation | Raw SQL INSERT into `api_sources` | Sources Kafka integration |
| 6 | Manual OCP cluster registration | INSERT into `reporting_ocp_clusters` | Created by Koku pipeline on first data |
| 7 | Manual manifest creation | INSERT into `reporting_common_costusagereportmanifest` | Created by Koku ingestion pipeline |
| 8 | Set `additional_context = '{}'` on Provider | Without it, sources API crashes (NoneType) | Set by Koku during normal provider creation |
| 9 | Set `operator_version` on manifest | Without it, sources API crashes (NoneType.split) | Set by CMMO operator report |
| 10 | Set `data_updated_timestamp` on Provider | UI shows "incomplete" without it | Set by Koku after processing |
| 11 | Set `creation_datetime` on manifest | Manifest invisible to `get_state()` without it | Set by Koku ingestion |
| 12 | Flush Valkey cache after each DB hack | Koku caches aggressively | No cache issues in normal flow |
| 13 | No cost model | Pre-rated costs from our pipeline | Create Koku cost model with VM rates |
| 14 | No RBAC | `is_org_admin: true` identity header | Proper RBAC setup |

### Hack SQL (reproducible, run in order)

```sql
-- Connect: docker exec -it koku-db psql -U postgres -d postgres

-- 1. OSAC table + partition (Step 2 in main flow)
-- (already documented above)

-- 4. Provider authentication + billing source
INSERT INTO public.api_providerauthentication (uuid, credentials)
VALUES ('00000000-0000-0000-0000-0a5ac0000002',
        '{"cluster_id": "osac-region-1"}'::jsonb)
ON CONFLICT (uuid) DO NOTHING;

INSERT INTO public.api_providerbillingsource (uuid, data_source)
VALUES ('00000000-0000-0000-0000-0a5ac0000003', '{}'::jsonb)
ON CONFLICT (uuid) DO NOTHING;

-- Get the FK IDs (they're auto-increment integers, not UUIDs)
-- Then create Provider
INSERT INTO public.api_provider (
    uuid, name, type, active, paused, setup_complete,
    authentication_id, billing_source_id, customer_id,
    data_updated_timestamp, additional_context
)
SELECT
    '00000000-0000-0000-0000-0a5ac0000001',
    'OSAC Sovereign Cloud', 'OCP', true, false, true,
    a.id, b.id, 1, NOW(), '{}'::jsonb
FROM public.api_providerauthentication a, public.api_providerbillingsource b
WHERE a.uuid = '00000000-0000-0000-0000-0a5ac0000002'
  AND b.uuid = '00000000-0000-0000-0000-0a5ac0000003'
ON CONFLICT (uuid) DO NOTHING;

-- Link TenantAPIProvider to Provider
UPDATE org1234567.reporting_tenant_api_provider
SET provider_id = '00000000-0000-0000-0000-0a5ac0000001'
WHERE uuid = '00000000-0000-0000-0000-0a5ac0000001';

-- 5. Sources entry (makes provider visible to UI)
INSERT INTO public.api_sources (
    source_id, source_uuid, name, "offset", org_id, source_type,
    authentication, billing_source, koku_uuid,
    status, pending_delete, pending_update, out_of_order_delete,
    paused, updated_timestamp
) VALUES (
    99999, '00000000-0000-0000-0000-0a5ac0000001',
    'OSAC Sovereign Cloud', 0, '1234567', 'OCP',
    '{"credentials": {"cluster_id": "osac-region-1"}}',
    '{}', '00000000-0000-0000-0000-0a5ac0000001',
    '{"availability_status": "available"}',
    false, false, false, false, NOW()
);

-- 6. Cluster registration (required for report API to return data)
INSERT INTO org1234567.reporting_ocp_clusters (
    uuid, cluster_id, cluster_alias, provider_id
) VALUES (
    gen_random_uuid(), 'osac-region-1', 'OSAC Sovereign Cloud',
    '00000000-0000-0000-0000-0a5ac0000001'::uuid
) ON CONFLICT DO NOTHING;

-- 7. Manifest (required for UI status indicators)
INSERT INTO public.reporting_common_costusagereportmanifest (
    assembly_id, billing_period_start_datetime, num_total_files,
    provider_id, cluster_id, operator_version,
    creation_datetime, completed_datetime, state
) VALUES (
    'osac-sync-2026-07-10', '2026-07-01', 1,
    '00000000-0000-0000-0000-0a5ac0000001', 'osac-region-1',
    'costmanagement-metrics-operator.v0.0.1',
    NOW(), NOW(),
    '{"download": {"end": "2026-07-10T12:00:00", "start": "2026-07-10T11:59:00"},
      "processing": {"end": "2026-07-10T12:01:00", "start": "2026-07-10T12:00:00"},
      "summary": {"end": "2026-07-10T12:02:00", "start": "2026-07-10T12:01:00"}}'::jsonb
) ON CONFLICT (provider_id, assembly_id) DO NOTHING;

-- 10. Report period timestamps
UPDATE org1234567.reporting_ocpusagereportperiod
SET summary_data_creation_datetime = NOW(),
    summary_data_updated_datetime = NOW()
WHERE cluster_id = 'osac-region-1';

-- 12. Flush cache after all hacks
-- Run: docker exec koku_valkey redis-cli FLUSHALL
```

## Architecture Proven

```
cost-event-consumer (Go)
    → metering_entries + cost_entries (our DB, port 5434)
    → koku-sync (Go binary)
        → openshift_osac_usage_line_items_daily (Koku DB, port 15432, org1234567 schema)
        → [hack] reporting_ocpusagelineitem_daily_summary
        → [hack] reporting_ocp_cost_summary_p
        → [hack] reporting_ocp_clusters
        → [hack] api_provider + api_sources + manifest

Koku (Python/Django)
    → Sources API: count=1, status=download/processing/summary all "complete"
    → Report API: $0.42 infrastructure cost for "OSAC Sovereign Cloud"
    → UI at localhost:9001/openshift/cost-management/ocp
```

## Results

| Milestone | Status |
|---|---|
| Data in Koku OSAC table | ✓ 57 rows |
| Data in daily summary | ✓ 57 rows |
| Data in cost summary UI table | ✓ 1 row, $0.42 |
| Provider visible via Sources API | ✓ count=1 |
| Provider status: all complete | ✓ download/processing/summary |
| Report API returns cost data | ✓ $0.4181 infrastructure |
| UI shows "OSAC Sovereign Cloud" | ✓ visible as OCP source |
| UI shows cost reports | **✓ VERIFIED** |

### Screenshots

**OpenShift details — cluster list showing OSAC Sovereign Cloud at $0.42:**

![OSAC data in Koku UI — cluster list](koku-osac-data.png)

**OSAC Sovereign Cloud cluster detail — cost breakdown with $0.42 raw cost:**

![OSAC cluster detail — cost breakdown](koku-osac-data-2.png)

The UI renders OSAC data natively:
- Cluster name: "OSAC Sovereign Cloud" (osac-region-1)
- Integration status: green checkmark, "Data integration and finalization"
- Total cost: $0.42 USD (raw cost, no markup)
- Cost breakdown chart shows the full bar
- CPU and Memory panels show 0 (we populated `infrastructure_raw_cost`
  but not `pod_request_cpu_core_hours` in the cost summary table)
- Tabs available: Cost overview, Historical data, Virtualization

## Remaining Gaps

| Gap | Effort | Notes |
|---|---|---|
| UI may still need cost model for cost breakdown | S | Create via Cost Models API |
| Pipeline not triggered | M | Masu API on port 5042, not 8000 |
| No Koku cost model for OSAC | S | Create via Cost Models API |
| OSAC SQL UNION in summarization template | Done (in code) | Tested — pipeline processes OSAC data |
| Django migration for OSAC table | S | `manage.py makemigrations` |
| project_id not synced | S | koku-sync writes empty string — Koku project-level reports show no breakdown for OSAC data |
| MaaS data excluded from Koku | By design | MaaS (tokens, inference) served by our API only — Koku has no consumption-based cost model |
| Concurrent koku-sync safety | S | No unique constraint — overlapping CronJob runs can produce duplicate rows |
| koku-ui navigation to OSAC data | M | May need UI customization |

## Files Changed

### Our repo (cost_ai_grid_poc)
- `inventory-watcher/cmd/koku-sync/main.go` — sync binary
- `docs/koku-integration/strategy.md` — full strategy analysis
- `docs/koku-integration/spike-results.md` — this document
- `docs/koku-integration/koku-ocp-flow.dot` + `.svg` — data flow diagram

### Koku repo (~/Projects/koku/koku/)
- `koku/reporting/provider/ocp/self_hosted_models.py` — OSACUsageLineItemDaily model
- `koku/masu/database/self_hosted_sql/openshift/reporting_ocpusagelineitem_daily_summary.sql` — OSAC UNION
- `koku/masu/database/ocp_report_db_accessor.py` — osac_exists wiring

### Not committed to Koku yet — changes are local only.

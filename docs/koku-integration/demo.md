# Koku Integration Demo

How to run the full pipeline: generate OSAC events → process through
our consumer → sync to Koku → see data in Koku UI.

## Related Documents

| Document | What it covers |
|---|---|
| [Integration Strategy](strategy.md) | 6 strategies analyzed, pipeline diagram, on-prem assessment |
| [Spike Results](spike-results.md) | End-to-end test results, 14 SQL hacks, screenshots |
| [Pipeline Diagram](koku-ocp-flow.svg) | Koku OCP data flow with OSAC integration points |
| [Implementation Status](../implementation-status.md) | Overall PoC requirements tracker |
| [ADR-004: Drop unique index](../decisions/004-raw-events-no-unique-index.md) | Performance: raw_events dedup trade-off |
| [ADR-005: Single binary](../decisions/005-single-binary-subcommands.md) | Proposed: subcommands for serve/koku-sync/simulate |
| [Performance Characteristics](../research/performance-characteristics.md) | Load estimation, profiling, scaling techniques |

### PRs

| PR | Repo | Status |
|---|---|---|
| [#44](https://github.com/myersCody/cost_ai_grid_poc/pull/44) | cost_ai_grid_poc | Draft — koku-sync + docs |
| [#6178](https://github.com/project-koku/koku/pull/6178) | koku | Draft — OSAC model + SQL UNION |

### Screenshots

| | |
|---|---|
| ![Cluster list](koku-osac-data.png) | ![Cluster detail](koku-osac-data-2.png) |
| Koku OpenShift page — OSAC Sovereign Cloud at $0.42 | Cluster detail — cost breakdown chart |

## Current Architecture

```
cost-event-consumer (Go, port 8020)
  ├── real-time: events → metering → rating → cost_entries
  ├── serves: quotas, balance checks, MaaS reports
  └── dashboard: http://localhost:8020/debug/dashboard

koku-sync (Go, runs on-demand or as CronJob)
  ├── reads: cost_entries from our DB (port 5434)
  ├── writes: openshift_osac_usage_line_items_daily in Koku DB (port 15432)
  └── triggers: Koku pipeline via Masu API (port 5042)

Koku (Python/Django, port 8000)
  ├── pipeline: OSAC UNION → daily summary → cost model → UI tables
  ├── serves: /reports/openshift/costs/ (VMs, clusters, bare metal)
  └── UI: http://localhost:9001 (koku-ui on-prem)

Split: VMs/clusters/BM → Koku reports | MaaS inference → our API
```

## Prerequisites

All of these must be running:

| Component | Port | How to start |
|---|---|---|
| Our PostgreSQL | 5434 | `docker start cost-db` |
| Koku PostgreSQL | 15432 | See "Start Koku" below |
| Koku API server | 8000 | See "Start Koku" below |
| Koku Masu server | 5042 | See "Start Koku" below |
| Koku Celery worker | — | See "Start Koku" below |
| Koku Valkey | 6379 | See "Start Koku" below |
| cost-event-consumer | 8020 | See "Start our consumer" below |
| Koku UI (optional) | 9001 | See "Start Koku UI" below |

## Start Koku

Koku must run from the `osac-integration-spike` branch which includes
the OSAC table model, SQL UNION template, and DB accessor wiring.

```bash
cd ~/Projects/koku/koku
git checkout osac-integration-spike

# Build and start all services
ONPREM=True docker compose up -d db valkey
ONPREM=True docker compose up -d koku-server masu-server koku-worker

# Verify
curl -sf http://localhost:8000/api/cost-management/v1/status/
# → {"status":"OK"}
```

### First-time setup (run once)

If this is the first time running the demo, you need to set up the
OSAC provider in Koku's database. See `spike-results.md` for the
full list of SQL setup statements ("Hack SQL" section).

The key steps:
1. Create Provider + auth + billing source in `api_provider`
2. Create Source in `api_sources`
3. Register cluster in `reporting_ocp_clusters`
4. Create manifest in `reporting_common_costusagereportmanifest`
5. Flush Valkey: `docker exec koku_valkey redis-cli FLUSHALL`

## Start our consumer

```bash
cd ~/Projects/cost_ai_grid_poc/inventory-watcher

# Build
go build -o cost-event-consumer ./cmd/consumer/
go build -o maas-simulator ./cmd/maas-simulator/

# Start (metrics on 9090 to avoid conflict with Koku's S4 on 9000)
OSAC_BASE_URL=http://localhost:8011 \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
INGEST_LISTEN_ADDR=localhost:8020 \
METRICS_PORT=9090 \
LOG_LEVEL=info \
DEBUG_DASHBOARD=true \
./cost-event-consumer &

# Verify
curl -sf http://localhost:8020/healthz
# → {"status":"ok"}
```

OSAC connection errors are expected if OSAC isn't running — the consumer
reconnects automatically. The ingest endpoint works independently.

## Start Koku UI (optional)

```bash
cd ~/Projects/koku/koku-ui
npm install  # first time only
API_TOKEN=false API_PROXY_URL=http://localhost:8000/api/cost-management/v1 \
    npm run -w @koku-ui/koku-ui-onprem start
# UI at http://localhost:9001
# Navigate to: OpenShift → click "OSAC Sovereign Cloud"
```

## Run the demo

### 1. Generate OSAC events

```bash
# From the repo root
./snippets/osac-event-stream.sh --duration 60 --rate 5 --vms 10
```

This generates a mix of events: 70% MaaS inference, 10% VM heartbeats,
8% cluster heartbeats, 7% VM creates, 5% VM deletes.

Options:
- `--duration 300` — run for 5 minutes
- `--rate 10` — 10 events/second
- `--vms 20` — start with 20 VMs
- `--models 5` — number of MaaS models

### 2. Wait for rating sweep

The metering sweep runs every 60s, the rating sweep every 30s. Wait
~90s after the event stream ends for all entries to be rated.

Check progress:
```bash
curl -sf http://localhost:8020/api/v1/reports/summary | python3 -m json.tool
```

### 3. Sync to Koku

```bash
cd inventory-watcher

SYNC_DATE=$(date +%Y-%m-%d) \
KOKU_DB_URL="postgres://postgres:postgres@localhost:15432/postgres" \
go run ./cmd/koku-sync/
```

### 4. Trigger Koku pipeline

```bash
curl "http://localhost:5042/api/cost-management/v1/report_data/?provider_uuid=00000000-0000-0000-0000-0a5ac0000001&schema=org1234567&start_date=$(date +%Y-%m-%d)"
```

This triggers Celery tasks that:
1. Run the summarization SQL (including OSAC UNION)
2. Apply cost model rates
3. Refresh UI summary tables

Wait ~20 seconds for Celery to finish.

### 5. Verify

**Via API:**
```bash
IDENTITY=$(echo -n '{"identity":{"org_id":"1234567","account_number":"1234567","type":"User","user":{"username":"test","email":"test@example.com","is_org_admin":true}},"entitlements":{"cost_management":{"is_entitled":true}}}' | base64)

curl -sf -H "x-rh-identity: $IDENTITY" \
  'http://localhost:8000/api/cost-management/v1/reports/openshift/costs/' \
  | python3 -m json.tool | head -20
```

**Via UI:**
Open `http://localhost:9001/openshift/cost-management/ocp` and look for
"OSAC Sovereign Cloud" in the cluster list.

**Via our dashboard:**
Open `http://localhost:8020/debug/dashboard` for real-time pipeline stats.

## Quick re-sync (after generating more events)

```bash
# One-liner: sync + trigger
SYNC_DATE=$(date +%Y-%m-%d) KOKU_DB_URL="postgres://postgres:postgres@localhost:15432/postgres" \
  go run ./cmd/koku-sync/ && \
curl -s "http://localhost:5042/api/cost-management/v1/report_data/?provider_uuid=00000000-0000-0000-0000-0a5ac0000001&schema=org1234567&start_date=$(date +%Y-%m-%d)" && \
echo " Pipeline triggered — wait ~20s then refresh UI"
```

## Troubleshooting

| Problem | Fix |
|---|---|
| Consumer exits with "bind: address already in use" | Koku S4 uses port 9000 — set `METRICS_PORT=9090` |
| Koku API returns empty data | Flush cache: `docker exec koku_valkey redis-cli FLUSHALL` |
| Masu API returns 404 | Use port 5042 (Masu server), not 8000 (API server) |
| Worker logs show SQL errors | Check `docker logs koku-koku-worker-1` — may need SQL template fix |
| UI shows "Still processing" | Ensure manifest has `creation_datetime` and `operator_version` set |
| Event stream shows "errors" | The simulator reports 204 as errors — data still goes through |

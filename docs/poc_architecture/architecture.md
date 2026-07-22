# Cost Management for AI Grid — Architecture

> **Status:** POC (Proof of Concept)
> **Goal:** A PoC-quality Cost Management on-premise instance integrated with OSAC for the AI Grid sovereign cloud blueprint.

---

## Background

The **AI Grid** is a sovereign cloud offering for customers, built on the Red Hat portfolio (OpenShift Container Platform, OpenShift Virtualization, OpenShift AI, Advanced Cluster Manager, Ansible Automation Platform).

**OSAC** (Open Sovereign AI Console) is the orchestrator: it provisions resources (clusters, VMs, models, bare metal), exposes a unified API, and emits/receives events and alerts. See: [osac-project on GitHub](https://github.com/osac-project).

**Cost Management** must integrate with OSAC to:
- Synchronize inventory (clusters, VMs, models, bare metal)
- Receive CloudEvents for resource lifecycle (replacing the Cost Management Metrics Operator)
- Perform capacity-based metering for CaaS/VMaaS
- Perform consumption-based metering for MaaS
- Manage budgets/quotas and emit threshold alerts back to OSAC

This POC explores that integration from the Cost Management side.

---

## System Context

```mermaid
flowchart TB
    subgraph ai_grid [AI Grid Sovereign Cloud]
        ocp["OpenShift\nContainer Platform"]
        ocpvirt["OpenShift\nVirtualization"]
        ocpai["OpenShift AI\n(MaaS)"]
        acm["Advanced Cluster\nManager"]
    end

    subgraph osac_sys [OSAC — Open Sovereign AI Console]
        osac_api["OSAC API\ngRPC + REST"]
        osac_ctrl["OSAC Controllers\n(cluster/VM/model/BMaaS)"]
        osac_db["PostgreSQL"]
        osac_api --- osac_ctrl
        osac_ctrl --- osac_db
    end

    subgraph poc [Cost AI Grid POC]
        watcher["inventory-watcher\n(Go)"]
        koku_db["PostgreSQL\n(POC)"]
        koku_api["Cost API"]
        ui["Data Grid UI\n(TBD)"]
        watcher --> koku_db
        koku_db --> koku_api
        koku_api --> ui
    end

    ai_grid -->|"provisions / manages"| osac_sys
    osac_api -->|"Watch stream\n(gRPC)"| watcher
    osac_api -->|"List endpoints\n(reconciler)"| watcher
    koku_api -.->|"quota alerts\n(TBD transport)"| osac_api
```

---

## Local Development Service Map

When running the full stack locally, services are assigned ports to avoid conflicts between Koku and OSAC:

| Service | Port | Notes |
|---|---|---|
| OSAC gRPC | 8010 | |
| OSAC REST gateway | 8011 | `/api/fulfillment/v1/`; inventory-watcher also uses `/api/private/v1/events/watch` |
| OSAC metrics | 8012 | |
| OSAC OIDC server | 8013 | Local JWT signing; `inventory-watcher/scripts/oidc_server.py` + `gen_token.py` |
| OSAC PostgreSQL | 5433 | |
| **inventory-watcher** | — | **Implemented** — Go binary (`inventory-watcher/cmd/consumer`); no dedicated listen port (HTTP ingest optional on `INGEST_LISTEN_ADDR`). Six concurrent workers — see [inventory-watcher Workers](#inventory-watcher-workers). Persists to POC PostgreSQL `:5434`. Auth: `OSAC_TOKEN` Bearer JWT against `:8011`. Go module logs as `cost-event-consumer`; built artifact is typically named `inventory-watcher`. |
| **POC PostgreSQL** | **5434** | Cost inventory DB (`costdb`); schema auto-migrated by inventory-watcher on startup |
| **POC HTTP Ingest** | **8020** | **Implemented** (optional) — `POST /api/v1/events`, `GET /api/v1/quotas/{tenant_id}`, `GET /api/v1/reports/costs`, `GET /api/v1/reports/breakdown`, `GET /healthz`, `GET /readyz`; activated when `INGEST_LISTEN_ADDR=:8020` |
| POC Kafka | 9092 | Optional — only needed for Option C |

### inventory-watcher configuration

| Env var | Default | Purpose |
|---|---|---|
| `OSAC_BASE_URL` | `http://localhost:8011` | REST gateway |
| `OSAC_GRPC_ADDRESS` | `localhost:8010` | gRPC address for the public Watch stream (default transport) |
| `OSAC_TOKEN` | (optional) | Bearer JWT |
| `OSAC_CA_CERT` | (optional) | Custom TLS root |
| `INVENTORY_DB_URL` | `postgres://user:pass@localhost:5434/costdb` | POC DB |
| `RECONCILE_INTERVAL` | `1h` | Reconciler ticker |
| `METERING_INTERVAL` | `60s` | Metering sweep ticker |
| `RATING_INTERVAL` | `30s` | Rating sweep ticker |
| `LOG_LEVEL` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |
| `LOG_FORMAT` | `text` | Log format (`text` or `json`) |
| `INGEST_LISTEN_ADDR` | (empty — disabled) | HTTP ingest server address (e.g. `:8020`) |
| `METRICS_PORT` | `9000` | Prometheus metrics (separate port, no auth) |
| `CUSTOM_METRICS_CONFIG` | (empty) | Path to custom metrics JSON config (REQ-13) |
| `AUTH_ISSUER_URL` | (empty) | OIDC issuer URL; auth disabled if empty |
| `SPLUNK_HEC_URL` | (empty) | Splunk HEC endpoint; forwarding disabled if empty |

---

## OSAC Resource Model

OSAC organizes resources in a hierarchy:

```
Tenant
  └── Project
        └── Resource
              ├── Cluster (CaaS — HCP / OCP)
              ├── ComputeInstance (VMaaS — OpenShift Virtualization)
              ├── Model (MaaS — OpenShift AI)
              └── BareMetalInstance (BMaaS — RHEL / Windows)
```

### PoC scope

The hierarchy above is the full OSAC model. **inventory-watcher** tracks only a subset today:

| Resource | Inventory | Metering |
|---|---|---|
| ComputeInstance (VMaaS) | Yes | Yes |
| Cluster (CaaS) | Yes | Yes |
| Project | Yes (reconcile + events) | N/A |
| InstanceType | Yes (lookup catalog) | N/A |
| Model (MaaS) | **Partial** — `inventory_model` table populated via HTTP ingest CloudEvents (`osac.model.lifecycle`, `inference.tokens.used`) | **Partial** — `maas_tokens_in`, `maas_tokens_out`, `maas_requests` emitted via `MeterMaaSEvent` on ingest; OSAC schema TBD |
| BareMetal (BMaaS) | Yes — `inventory_bare_metal_instance` via reconciler + Watch | Yes — `bm_uptime_seconds` via metering sweep |

MaaS inventory and consumption metering are operational via the HTTP ingest path when `INGEST_LISTEN_ADDR` is set and `osac.model.lifecycle` CloudEvents are posted to `POST /api/v1/events`. The `maas-simulator` tool can generate test events. BMaaS remains blocked on OSAC schema.

The OSAC fulfillment service exposes these via gRPC (`osac.public.v1`) with a REST gateway:

| gRPC Service | REST Endpoint | Used by inventory-watcher |
|---|---|---|
| `osac.public.v1.Events` | `GET /api/private/v1/events/watch` (NDJSON stream) | **Watcher** |
| `osac.public.v1.Projects` | `GET /api/fulfillment/v1/projects` | **Reconciler** |
| `osac.public.v1.ComputeInstances` | `GET /api/fulfillment/v1/compute_instances` | **Reconciler** |
| `osac.public.v1.Clusters` | `GET /api/fulfillment/v1/clusters` | **Reconciler** |
| `osac.public.v1.InstanceTypes` | `GET /api/fulfillment/v1/instance_types` | **Reconciler** |
| `osac.public.v1.ClusterTemplates` | `GET /api/fulfillment/v1/cluster_templates` | Not synced in PoC |
| `osac.public.v1.ClusterOrders` | `GET /api/fulfillment/v1/cluster_orders` | Not synced in PoC |

---

## Event Ingestion — Options

The OSAC fulfillment service exposes a gRPC streaming `Events` service (with a REST gateway watch endpoint) and REST List APIs for inventory. Three ingestion options are viable for the POC. **Option A is the current implementation** — see [ADR-002](../decisions/002-arguments-against-kafka.md) for why Kafka is deferred.

## Watch Stream

The underlying transport is `osac.public.v1.Events` (gRPC streaming watch). The default build uses the gRPC client directly against `OSAC_GRPC_ADDRESS`; a REST NDJSON fallback is available via `-tags rest_watch`. Per OSAC team guidance (Avishay, Jul 12), the public gRPC API is the recommended transport.

```mermaid
flowchart LR
    osac["OSAC REST gateway\nlocalhost:8011"]
    consumer["inventory-watcher\n(Go)"]
    db["POC PostgreSQL\n:5434"]
    osac -->|"gRPC Watch stream\n(osac.public.v1.Events)"| consumer
    osac -->|"GET /projects, /compute_instances,\n/clusters, /instance_types\n(reconciler)"| consumer
    consumer --> db
```

**Pros:** Real-time events; no additional infrastructure; works today against local OSAC; reconciler catches missed events; exponential backoff reconnect (1s → 30s cap) on stream disconnect.
**Cons:** Single-consumer pattern; requires JWT auth against OSAC; not suitable if multiple independent consumers need the same event stream.

### Option B — REST Polling (fallback)

```mermaid
flowchart LR
    osac["OSAC REST\nlocalhost:8011"]
    poller["Python Poller\n(httpx, every 60s)"]
    db["POC PostgreSQL"]
    osac -->|"GET /clusters\nGET /compute_instances"| poller
    poller --> db
```

**Pros:** Simple; matches the existing CaaS/VMaaS collector scripts.
**Cons:** Snapshot-based; misses events between polls; 60s granularity.

### Option C — Kafka (optional future)

See [ADR-002](../decisions/002-arguments-against-kafka.md) for why Kafka is deferred.

```mermaid
flowchart LR
    ctrl["OSAC Controllers"]
    kafka["Kafka\nlocalhost:9092"]
    consumer["Event Consumer"]
    db["POC PostgreSQL"]
    ctrl -->|"CloudEvents\nosac.events.*"| kafka
    kafka --> consumer
    consumer --> db
```

**Pros:** Decoupled fan-out; supports multiple independent consumers; event replay over long windows.
**Cons:** Requires OSAC to publish to Kafka (not implemented on OSAC side yet); adds operational overhead with no current multi-consumer requirement.

### Recommendation

Use **Option A** (Watch stream + reconciler) for the PoC and likely for production v1 — see [ADR-002](../decisions/002-arguments-against-kafka.md). The 60-second metering sweep interval is fixed by [ADR-001](../decisions/001-metering-sweep-interval.md).

Adopt **Option C** only if multiple independent consumers emerge or OSAC standardizes on Kafka as a first-class transport. Keep event ingestion behind an interface (`inventory-watcher/internal/osac/client.go` today) so the transport layer is swappable without changing the metering pipeline.

---

## PoC Database Schema

Schema is auto-migrated on inventory-watcher startup (inline SQL in `inventory-watcher/internal/inventory/store.go`;

| Table | Role |
|---|---|
| `raw_events` | Immutable event log; no unique constraint on `event_id` (throughput optimization — see [ADR-004](../decisions/004-raw-events-no-unique-index.md)) |
| `inventory_tenant` | Tenant inventory; upserted from Watch + reconciler |
| `inventory_project` | Project inventory |
| `inventory_compute_instance` | VM inventory; `last_metered_at` sweep cursor |
| `inventory_cluster` | Cluster inventory; `node_sets` JSONB |
| `inventory_instance_type` | Spec catalog for rate/summary lookup |
| `inventory_model` | Model (MaaS) inventory; populated via HTTP ingest CloudEvents |
| `inventory_bare_metal_instance` | Bare metal inventory; populated via reconciler + Watch |
| `inventory_catalog_item` | Catalog items (cluster, compute_instance, bare_metal) synced from OSAC |
| `metering_entries` | Per-sweep / per-event meter increments |
| `rates` | Rate records; seeded on startup; supports flat and tiered pricing (`tiers` JSONB column); `instance_type` dimension for per-SKU pricing |
| `cost_entries` | Rated cost rows linked to `metering_entries` and `rates`; produced by the Rater worker |
| `quotas` | Per-tenant quota limits; seeded on startup for demo tenants |
| `alerts` | Threshold alerts (50/70/90/100%); fired by the Rater |
| `splunk_cursor` | Splunk HEC forwarding cursor |

---

## inventory-watcher Workers

Goroutines run concurrently via `errgroup` in `cmd/consumer/main.go`:

| Worker | On startup | Interval | Role |
|---|---|---|---|
| **Watcher** | Connect immediately | Continuous stream | gRPC Watch events → raw log + inventory upsert |
| **Reconciler** | Full sync immediately | `RECONCILE_INTERVAL` (default 1h) | List API diff for missed events |
| **Meter** | Waits for first tick | `METERING_INTERVAL` (default 60s) | Capacity meters for billable VMs, clusters, bare metal |
| **Rater** | Waits for first tick | `RATING_INTERVAL` (default 30s) | Processes unrated `metering_entries` → `cost_entries` using `rates` table; evaluates quota thresholds |
| **Splunk** | Start if configured | `SPLUNK_INTERVAL` (default 10s) | Forwards `raw_events` to Splunk HEC for audit (opt-in via `SPLUNK_HEC_URL`) |
| **HTTP Ingest** | Start immediately (optional) | Continuous | CloudEvent receiver, quota API, report API, balance check; only started when `INGEST_LISTEN_ADDR` is set |
| **Metrics** | Start immediately | Continuous | Prometheus metrics on `METRICS_PORT` (default 9000) |

---

## Data Flow

### Watcher event handling

Events use OSAC protobuf-style types (`EVENT_TYPE_OBJECT_CREATED`, etc.) parsed into Go structs — not full CloudEvents envelopes. Dedup field is `event_id` (not `ce_id`).

**Inventory upsert (CREATE/UPDATE):** ComputeInstance, Cluster, InstanceType, Project, Tenant, BareMetalInstance, CatalogItem

**Raw log only (no inventory table):** HostType, ClusterTemplate, ComputeInstanceTemplate, Role, RoleBinding

**DELETE:**
- ComputeInstance → final metering (if billable) + soft delete
- Cluster → soft delete only (no final cluster metering)
- Other types → no-op

**PoC gaps:**
- VM events do not populate `project` or `cluster_id` columns (columns exist but stay empty)
- List API calls decode the first page only — reconciler may miss resources beyond page 1

### Inventory Sync

The reconciler diffs OSAC List endpoints against local inventory to catch events missed by the Watch stream. Runs on startup and every `RECONCILE_INTERVAL` (default 1h).

```
OSAC REST API (reconciler)
  GET /projects           →  UPSERT inventory_project (no deletion sync)
  GET /compute_instances  →  UPSERT missing; mark deleted if absent locally
  GET /clusters           →  UPSERT missing; mark deleted if absent locally
  GET /instance_types     →  UPSERT inventory_instance_type
```

**Not synced:** `cluster_templates`, `cluster_orders`.

**Reconcile vs Watch gap:** clusters created via reconcile omit `node_sets` and `labels` (the watcher path populates them).

### Metering Pipeline

```
OSAC Watch event (or Reconciler upsert for missed CREATED)
  │
  ├── parse OSAC Event struct
  ├── INSERT raw_events (dedup on event_id)
  ├── UPSERT inventory_* (compute_instance, cluster, instance_type, project)
  │
  ├── On DELETE (compute_instance, if billable): final INSERT metering_entries
  │
  └── [60s ticker] Meter sweep → INSERT metering_entries, UPDATE last_metered_at
        ↓
      [30s ticker] Rater → matchRate (4-way fallback) → INSERT cost_entries
        ↓
      Rater → evaluateThresholds → INSERT alerts (50/70/90/100%)
        ↓
      [optional] Splunk HEC forwarder → raw_events → Splunk
```

`metering_entries.raw_event_id` is always NULL for sweep-driven entries — set only when metering originates from an HTTP-ingested CloudEvent.

### Alert Flow

Alert transport back to OSAC is not yet decided. A likely shape:

```
quota_consumption > threshold (e.g. 70%)
  │
  └── emit alert (transport TBD: Kafka CloudEvent, HTTP webhook, etc.)
        │
        └── OSAC receives alert → applies OPA rate limit policy
```

---

## Metering Model

See [metering/metering-spec-draft.md](metering/metering-spec-draft.md) for the full capacity-based metering specification.

Cost Management must support three billing models:

| Resource Type | Metering | Target billing unit | PoC status |
|---|---|---|---|
| Cluster (CaaS) | Capacity-based | cluster-month | Metered |
| VM (VMaaS) | Capacity-based | VM-month | Metered |
| Model (MaaS) | Consumption-based | per-million-tokens, per-million-requests | **Partial** — ingest, inventory, and token meters implemented; OSAC event schema TBD |
| Bare Metal (BMaaS) | Capacity-based | BM-month | Implemented — `bm_uptime_seconds`; parked from Jul 31 demo scope |

### Capacity-Based (CaaS / VMaaS) — PoC implementation

Charge is based on what was provisioned, not what was used. The PoC emits these meters on each 60s sweep (see `inventory-watcher/internal/metering/`):

| Resource | Billable states | Meters emitted |
|---|---|---|
| VM | `COMPUTE_INSTANCE_STATE_RUNNING` | `vm_uptime_seconds`, `vm_cpu_core_seconds`, `vm_memory_gib_seconds` |
| Cluster | `CLUSTER_STATE_READY`, `CLUSTER_STATE_PROGRESSING` | `cluster_uptime_seconds`, `cluster_worker_node_seconds` (sum of `node_set.size × duration` across all node sets) |

**VM pricing model:** OSAC is removing `cores`/`memory_gib` from
`ComputeInstance` events. The recommended pricing model uses the
`instance_type` field to determine cost: each instance type gets its
own rate on `vm_uptime_seconds` (e.g. m5.xlarge = $0.50/hr). CPU and
memory meters are emitted for capacity reporting but priced at $0.
See [Rate Configuration Guide — Instance-Type Pricing](../rate-configuration-guide.md#option-1-flat-rate-per-instance-type-recommended) for setup.

Final metering on DELETE is implemented for VMs only (not clusters).

**Not implemented in PoC:**
- Per-`host_type` breakdown for control plane vs workers
- Koku-style aggregations: `SUM(duration_seconds)` where `host_type = _control_plane`, `MAX(node_count)` per host_type

Target Koku aggregation semantics for production cost reports are documented in [metering/metering-spec-draft.md](metering/metering-spec-draft.md) and [metering/cost_model_metric_feasibility.md](metering/cost_model_metric_feasibility.md).

### Consumption-Based (MaaS)

Charge is based on actual usage:
- Tokens in / tokens out / inference tokens
- Number of requests
- Per-million-token or per-million-request rates (tiered)

Blocked on OSAC MaaS CloudEvent schema.

---

## Open Questions

| # | Question | Owner | Status |
|---|---|---|---|
| 1 | What transport will OSAC use to send CloudEvents to Cost? | OSAC + Cost | **PoC decided:** Watch stream (Option A). Production Kafka only if multi-consumer fan-out is needed — see ADR-002 |
| 2 | What Kafka topic names will OSAC use? | OSAC | Open — relevant only if Option C is adopted |
| 3 | Will OSAC define CloudEvents for MaaS and BMaaS? | OSAC | Open |
| 4 | Where do quotas/budgets live — OSAC, Cost, or both? | OSAC + Cost | **Decided:** OSAC owns limits; Cost caches via List API — see [boundary_monitoring/alerting-osac-integration.md](boundary_monitoring/alerting-osac-integration.md) |
| 5 | Where do cost tiers live — OSAC, Cost, or both? | OSAC + Cost | Open |
| 6 | How will quota alerts be communicated to OSAC? (Kafka CloudEvents? HTTP callback?) | OSAC + Cost | **Leaning toward:** push + pull — see [boundary_monitoring/alerting-osac-integration.md](boundary_monitoring/alerting-osac-integration.md); wire formats in [boundary_monitoring/alerting-spec-draft.md](boundary_monitoring/alerting-spec-draft.md) |
| 7 | Does OSAC have a concept of projects within tenants that Cost needs to track? | OSAC + Cost | **Resolved:** yes — `inventory_project` populated; see REQ-3a |
| 8 | UI requirements for the data grid? | Cost | TBD |

---

## References

- [OSAC Project GitHub](https://github.com/osac-project)
- [OSAC Fulfillment Service](https://github.com/osac-project/fulfillment-service)
- [OSAC Metering Discover POC](https://github.com/masayag/osac-metering-discover-poc)
- [OSAC Console Mockups](https://heyethankim.github.io/osac-demo/)
- [docs/dev/local-dev-setup.md](../dev/local-dev-setup.md) — local dev setup guide
- [docs/demos/demo-scenario-1.md](../demos/demo-scenario-1.md) — end-to-end demo walkthrough
- [docs/requirements/poc_requirements_overview.md](../requirements/poc_requirements_overview.md) — consolidated requirements
- [inventory-watcher/internal/inventory/store.go](../../inventory-watcher/internal/inventory/store.go) — inline DB schema
- [snippets/test-inventory-watcher.sh](../../snippets/test-inventory-watcher.sh) — E2E test suite
- [metering/metering-spec-draft.md](metering/metering-spec-draft.md) — capacity-based metering specification
- [ADR-001: Metering sweep interval](../decisions/001-metering-sweep-interval.md)
- [ADR-002: Watch stream instead of Kafka](../decisions/002-arguments-against-kafka.md)
- [boundary_monitoring/alerting-osac-integration.md](boundary_monitoring/alerting-osac-integration.md) — quota integration options & ownership (REQ-9, REQ-10)
- [boundary_monitoring/alerting-spec-draft.md](boundary_monitoring/alerting-spec-draft.md) — API/schema draft (after route chosen)
- [boundary_monitoring/wallet-spec-draft.md](boundary_monitoring/wallet-spec-draft.md) — prepaid wallets ledger, deduction, status API (REQ-14)

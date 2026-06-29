# Cost AI Grid POC — Communications & Architecture Diagram

> **Status:** POC (Proof of Concept)
> **PoC Deadline:** July 31, 2026

This document provides a visual reference for all communications between systems in the AI Grid Cost Management POC. For narrative context see [architecture.md](./architecture.md) and the ADRs in [docs/decisions/](../decisions/).

---

## 1. System Context

High-level view of the three systems and how they relate.

```mermaid
flowchart TB
    subgraph ai_grid ["AI Grid Sovereign Cloud"]
        ocp["OpenShift Container Platform"]
        ocpvirt["OpenShift Virtualization"]
        ocpai["OpenShift AI (MaaS)"]
        acm["Advanced Cluster Manager"]
    end

    subgraph osac ["OSAC — Open Sovereign AI Console"]
        fulfillment["Fulfillment Service\nREST :8011  ·  gRPC :8010"]
        osac_db["PostgreSQL :5433"]
        collector["Metering Collector\nosac-metering-discover-poc\n(currently → OpenMeter)"]
        fulfillment --- osac_db
    end

    subgraph poc ["Cost AI Grid POC"]
        inv_watcher["inventory-watcher (Go)\n6 concurrent workers"]
        poc_db[("POC PostgreSQL\ncostdb :5434")]
        cost_api["Cost API"]
        ui["Data Grid UI (TBD)"]
        inv_watcher --> poc_db
        poc_db --> cost_api
        cost_api --> ui
    end

    ai_grid -->|"provisions / manages"| osac
    fulfillment -->|"Watch stream  NDJSON\nGET /api/private/v1/events/watch"| inv_watcher
    fulfillment -->|"List APIs (reconciler)\nGET /projects /clusters /compute_instances"| inv_watcher
    collector -. "CloudEvents  POST /api/v1/events\nPhase 4 — URL redirect only" .-> inv_watcher
    cost_api -. "quota alerts\ntransport TBD" .-> fulfillment
```

> **Solid arrows** = implemented today. **Dashed arrows** = planned / Phase 4.

---

## 2. Component Communication Detail

Internal breakdown of the `inventory-watcher` workers and every database interaction.

```mermaid
flowchart LR
    subgraph OSAC ["OSAC :8011"]
        direction TB
        watch_ep["GET /api/private/v1/events/watch\nNDJSON stream (keep-alive)"]
        list_ep["List APIs\nGET /projects\nGET /compute_instances\nGET /clusters\nGET /instance_types"]
        ingest_ep_osac["— —"]
    end

    subgraph Collector ["OSAC Metering Collector\n(osac-metering-discover-poc)"]
        col_script["Collector scripts\n~60s emit interval"]
    end

    subgraph IW ["inventory-watcher (Go binary)"]
        direction TB
        watcher["Watcher\ncontinuous stream"]
        reconciler["Reconciler\nevery 1h"]
        ingest["HTTP Ingest\nPOST /api/v1/events :8020\n(optional — INGEST_LISTEN_ADDR)"]
        meter["Meter Sweep\nevery 60s"]
        rater["Rater\nevery 30s"]
        summarizer["Summarizer\nevery 1h"]
    end

    subgraph DB ["POC PostgreSQL  costdb :5434"]
        direction TB
        raw_events[("raw_events")]
        inv_tables[("inventory_*\ncompute_instance\ncluster · project\ninstance_type · model")]
        meter_entries[("metering_entries")]
        cost_entries[("cost_entries")]
        rates[("rates")]
        quotas[("quotas")]
        summary[("daily_usage_summary")]
    end

    subgraph API ["Cost API"]
        cost_api_svc["REST endpoints\n(TBD)"]
    end

    watch_ep -->|"OSAC events\nEVENT_TYPE_OBJECT_CREATED\nEVENT_TYPE_OBJECT_UPDATED\nEVENT_TYPE_OBJECT_DELETED"| watcher
    list_ep -->|"JSON lists\n(startup + every 1h)"| reconciler
    col_script -. "CloudEvents 1.0 JSON\nosac.cluster.lifecycle\nosac.compute_instance.lifecycle\nosac.model.lifecycle\n(Phase 4)" .-> ingest

    watcher -->|"INSERT dedup\nevent_id"| raw_events
    watcher -->|"UPSERT\nCREATE/UPDATE/DELETE"| inv_tables
    reconciler -->|"UPSERT missing\nmark deleted if absent"| inv_tables
    ingest -->|"INSERT dedup\nce_id"| raw_events
    ingest -->|"UPSERT or auto-create"| inv_tables
    ingest -->|"INSERT pre-calc\nduration_seconds\ncpu_core_seconds etc."| meter_entries
    meter -->|"INSERT sweep rows\nvm_uptime_seconds\nvm_cpu_core_seconds\ncluster_uptime_seconds\ncluster_worker_node_seconds"| meter_entries
    rater -->|"SELECT unrated\nrows"| meter_entries
    rater -->|"SELECT flat /\ntiered rates"| rates
    rater -->|"INSERT rated\ncost_entries"| cost_entries
    summarizer -->|"UPSERT daily\nrollup (VMs only)"| summary
    quotas -->|"threshold check"| cost_api_svc
    cost_entries --> cost_api_svc
    summary --> cost_api_svc
```

---

## 3. Event Ingestion — Sequence Diagram

Step-by-step communication order from startup through a full metering cycle.

```mermaid
sequenceDiagram
    participant OSAC as OSAC Fulfillment Service :8011
    participant W as Watcher Worker
    participant R as Reconciler Worker
    participant DB as POC PostgreSQL :5434
    participant M as Meter Worker (60s)
    participant Ra as Rater Worker (30s)
    participant S as Summarizer (1h)

    note over W,DB: ── Startup ──
    W->>OSAC: GET /api/private/v1/events/watch  (Bearer JWT)
    OSAC-->>W: 200 OK  NDJSON keep-alive stream
    R->>OSAC: GET /projects
    R->>OSAC: GET /compute_instances
    R->>OSAC: GET /clusters
    R->>OSAC: GET /instance_types
    OSAC-->>R: JSON lists (page 1)
    R->>DB: UPSERT inventory_project, _compute_instance, _cluster, _instance_type

    note over OSAC,DB: ── Resource CREATE event ──
    OSAC->>W: EVENT_TYPE_OBJECT_CREATED  ComputeInstance {id, tenant, state, instance_type_id}
    W->>DB: INSERT raw_events  (dedup on event_id)
    W->>DB: UPSERT inventory_compute_instance  (last_metered_at = now())

    note over M,Ra: ── Steady-state metering cycle ──
    loop Every 60 s (hardcoded)
        M->>DB: SELECT inventory_compute_instance WHERE state = RUNNING AND deleted_at IS NULL
        M->>DB: SELECT inventory_cluster WHERE state IN (READY, PROGRESSING) AND deleted_at IS NULL
        M->>DB: INSERT metering_entries  (duration_seconds = now - last_metered_at, vm_uptime/cpu/memory/cluster_uptime/worker_node)
        M->>DB: UPDATE last_metered_at = now()
    end

    loop Every 30 s (hardcoded)
        Ra->>DB: SELECT metering_entries WHERE cost_entry_id IS NULL
        Ra->>DB: SELECT rates WHERE resource_type + meter_name matches
        Ra->>DB: INSERT cost_entries  (cost = value × rate)
    end

    loop Every 1 h (SUMMARIZE_INTERVAL)
        S->>DB: UPSERT daily_usage_summary  (previous UTC day, compute instances only)
    end

    note over OSAC,DB: ── Resource DELETE event ──
    OSAC->>W: EVENT_TYPE_OBJECT_DELETED  ComputeInstance
    W->>DB: INSERT metering_entries  (final — gap since last_metered_at)
    W->>DB: UPDATE inventory_compute_instance  (deleted_at = now())

    note over OSAC,DB: ── Stream disconnect / reconnect ──
    OSAC-xW: stream disconnect
    W->>W: exponential backoff  (1s → 30s cap)
    W->>OSAC: reconnect  GET /api/private/v1/events/watch
```

---

## 4. HTTP Ingest Path (Phase 4)

The OSAC metering collector currently sends CloudEvents to OpenMeter. Phase 4 requires only a URL redirect — the `POST /api/v1/events` endpoint already accepts the exact format the collector emits.

```mermaid
sequenceDiagram
    participant C as OSAC Metering Collector
    participant I as HTTP Ingest Server :8020
    participant DB as POC PostgreSQL :5434
    participant M as Meter Worker (60s)

    note over C,M: Phase 4 — change collector target from OpenMeter → Cost Management

    C->>I: POST /api/v1/events\nContent-Type: application/cloudevents+json\n{ "specversion":"1.0", "type":"osac.compute_instance.lifecycle",\n  "duration_seconds":60, "cpu_core_seconds":240, ... }
    I->>DB: INSERT raw_events  (dedup on ce_id)
    I->>DB: UPSERT inventory_compute_instance  (auto-create if absent)
    I->>DB: INSERT metering_entries  (pre-calc values written directly — no local recalculation)
    I->>DB: UPDATE last_metered_at = ce_time  (prevents sweep double-counting)
    I-->>C: 200 OK

    note over M,DB: Sweep skips resources whose last_metered_at is recent
    M->>DB: SELECT WHERE last_metered_at < now() - interval '60s'
    note right of M: Resource updated by ingest is excluded from sweep
```

**What's done / what's pending:**

| Item | Status |
|---|---|
| `POST /api/v1/events` endpoint | **Done** — accepts `osac.{cluster,compute_instance,model}.lifecycle` |
| Dedup on `ce_id` | **Done** |
| Pre-calculated value ingestion | **Done** — `duration_seconds`, `cpu_core_seconds`, `memory_gib_seconds`, `worker_node_seconds` |
| Sweep double-count prevention | **Done** — `last_metered_at` updated on ingest |
| MaaS (`osac.model.lifecycle`) | **Done** — `inventory_model` + `maas_tokens_in/out/requests` meters |
| Redirect collector target URL | **Pending** — OSAC action only (no Cost code changes needed) |
| Agree on transport & interval | **Pending** — HTTP push favored; interval (10–30s vs 60s) TBD |
| BMaaS CloudEvent schema | **Blocked** — OSAC must define schema first |

---

## 5. Quota Alert Flow

Alert transport back to OSAC is not yet decided. Likely shape once implemented:

```mermaid
flowchart LR
    cost_entries[("cost_entries")]
    quotas[("quotas")]
    checker["Quota Checker\n(not yet implemented)"]
    alert_emit["Alert Emitter\ntransport TBD"]
    osac_api["OSAC Fulfillment Service\n:8011"]
    opa["OPA Rate Limit Policy"]

    cost_entries -->|"aggregate cost by tenant"| checker
    quotas -->|"limit thresholds"| checker
    checker -->|"consumption > 70%\nor > 90%"| alert_emit
    alert_emit -. "CloudEvent alert\nor HTTP webhook" .-> osac_api
    osac_api --> opa
```

See [boundary_monitoring/alerting-osac-integration.md](./boundary_monitoring/alerting-osac-integration.md) and [boundary_monitoring/alerting-spec-draft.md](./boundary_monitoring/alerting-spec-draft.md) for the full design.

---

## 6. Metering Pipeline — Internal Data Flow

How raw events become cost entries.

```mermaid
flowchart TD
    osac_event["OSAC Watch Stream event\nEVENT_TYPE_OBJECT_*"]
    reconciler_upsert["Reconciler UPSERT\n(missed events)"]
    http_ingest_event["HTTP Ingest CloudEvent\nPOST /api/v1/events\n(Phase 4)"]

    raw_events[("raw_events\n(immutable log)")]
    inventory[("inventory_*\ncompute_instance\ncluster · project")]
    meter_sweep["Meter Sweep Worker\nevery 60s\ncalculates duration_seconds locally"]
    meter_ingest["Ingest Handler\npre-calc from event payload"]
    metering_entries[("metering_entries\nvm_uptime_seconds\nvm_cpu_core_seconds\nvm_memory_gib_seconds\ncluster_uptime_seconds\ncluster_worker_node_seconds\nmaas_tokens_in/out\nmaas_requests")]
    rates[("rates\nflat / tiered")]
    rater["Rater Worker\nevery 30s"]
    cost_entries[("cost_entries\ncost = value × rate")]
    summarizer["Summarizer\nevery 1h"]
    daily_summary[("daily_usage_summary\n(VMs — previous UTC day)")]
    quota_check["Quota Check\n(planned)"]
    quotas[("quotas")]

    osac_event --> raw_events
    osac_event --> inventory
    reconciler_upsert --> inventory
    http_ingest_event --> raw_events
    http_ingest_event --> inventory
    http_ingest_event --> meter_ingest

    inventory --> meter_sweep
    meter_sweep --> metering_entries
    meter_ingest --> metering_entries

    metering_entries --> rater
    rates --> rater
    rater --> cost_entries

    cost_entries --> summarizer
    summarizer --> daily_summary

    cost_entries --> quota_check
    quotas --> quota_check
```

---

## 7. Local Port Map (Development)

```mermaid
flowchart LR
    subgraph OSAC_ports ["OSAC (local)"]
        p8010["gRPC :8010"]
        p8011["REST gateway :8011\n/api/fulfillment/v1/\n/api/private/v1/events/watch"]
        p8012["Metrics :8012"]
        p8013["OIDC :8013\n(local JWT signing)"]
        p5433["PostgreSQL :5433"]
    end

    subgraph POC_ports ["Cost POC (local)"]
        p8020["HTTP Ingest :8020\nPOST /api/v1/events\nGET /api/v1/quotas/:tenant_id\nGET /api/v1/health"]
        p5434["POC PostgreSQL :5434\ncostdb"]
    end

    p8011 -->|"Watch stream + List APIs"| p8020
    p8013 -->|"JWT for auth"| p8011
    p8020 --> p5434
```

---

## References

- [architecture.md](./architecture.md) — full narrative architecture
- [ADR-001: Metering sweep interval](../decisions/001-metering-sweep-interval.md)
- [ADR-002: Watch stream vs. Kafka](../decisions/002-arguments-against-kafka.md)
- [ADR-003: Heartbeat events vs. local sweep](../decisions/003-heartbeat-emitter-vs-sweep.md)
- [event-types.md](./event-types.md) — CloudEvent schemas
- [metering/metering-spec-draft.md](./metering/metering-spec-draft.md) — capacity metering spec
- [boundary_monitoring/alerting-osac-integration.md](./boundary_monitoring/alerting-osac-integration.md) — quota alert design

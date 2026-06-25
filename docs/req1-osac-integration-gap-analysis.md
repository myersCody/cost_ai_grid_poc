# Requirement 1: OSAC Integration — Gap Analysis

> **Requirement:** Synchronize inventory (clusters, VMs, models) from OSAC into
> Cost Management. Consume resource metrics via CloudEvents. Billing is
> capacity-based — charge for what is provisioned, not what is used.
>
> **Source:** [requirements brief, section 1](https://github.com/martinpovolny/cost_ai_grid_poc/blob/main/docs/requirements/ai_grid_poc_requirements_brief.md#1-osac-integration-inventory--metrics--must-have)

## Current State: inventory-watcher

The `inventory-watcher` is a Go service that connects to the OSAC
fulfillment-service and maintains a cost inventory database. It has three
concurrent components:

- **Watcher** — connects to the OSAC REST gateway's event stream
  (`/api/private/v1/events/watch`) and processes CREATED/UPDATED/DELETED events
  in real time
- **Reconciler** — periodically calls OSAC List endpoints to catch any events
  missed during downtime (drift correction)
- **Summarizer** — calculates daily usage summaries (CPU-core-hours,
  memory-GB-hours) from inventory durations

Verified working end-to-end: reconciliation imports existing OSAC resources,
Watch stream captures real-time creates, inventory correctly tracks cores,
memory, tenant, labels, and lifecycle timestamps.

## Coverage vs Gaps

| Capability | Required | Status | Notes |
|---|---|---|---|
| Inventory sync (clusters, VMs) | Yes | **Done** | Reconciler calls OSAC List endpoints, upserts state |
| Real-time event ingestion | Yes | **Done** | Watch stream consumer with exponential backoff reconnect |
| Cluster tracking (state, node_sets, template) | Yes | **Done** | |
| Compute instance tracking (state, cores, memory) | Yes | **Done** | |
| Instance type catalog sync | Yes | **Done** | Syncs instance types for rate/spec lookups |
| Duration-based usage calculation | Yes | **Done** | Summarizer calculates CPU-core-hours, memory-GB-hours |
| CloudEvents envelope parsing | Yes | **Gap** | See "CloudEvents Envelope" below |
| Raw event storage (immutable log) | Yes | **Gap** | See "Raw Event Log" below |
| Metering entries (per meter per event) | Yes | **Gap** | See "Metering Entries" below |
| Billable state filtering | Yes | **Gap** | See "Billable State Filtering" below |
| Tenant → Project hierarchy | Yes | **Gap** | See "Project Entity" below |
| Model (MaaS) inventory tracking | No (req #2) | Not started | Outside scope of req #1 |
| Bare metal inventory tracking | No (req #8) | Not started | Outside scope of req #1 |

## Gap Details

### 1. Raw Event Log

**What's missing:** Every incoming event should be stored immutably in a
`raw_events` table before any processing. This provides an audit trail,
enables replay, and supports deduplication. Currently events go straight to
inventory upserts with no record of the original event.

**What's needed:**

- A `raw_events` table with columns: `id`, `ce_id` (unique for dedup),
  `ce_type`, `ce_source`, `ce_time`, `ce_data` (JSONB), `resource_type`,
  `resource_id`, `tenant_id`, `received_at`
- Insert into `raw_events` as the first step before inventory processing
- Deduplicate on `ce_id` (ON CONFLICT DO NOTHING)

**Effort:** Small — one new table, one insert call at the top of the event
handler.

### 2. Metering Entries

**What's missing:** After storing the raw event, the system should extract
meter values and insert them into a `metering_entries` table. Currently the
summarizer calculates daily aggregates from inventory timestamps, but doesn't
produce per-event metering records.

The defined meters are:

**CaaS (clusters):**
| Meter | Aggregation |
|---|---|
| `cluster_uptime_seconds` | SUM(duration_seconds) where host_type = _control_plane |
| `cluster_worker_node_seconds` | SUM(worker_node_seconds) |
| `cluster_worker_node_count` | MAX(node_count) per cluster per host_type |

**VMaaS (compute instances):**
| Meter | Aggregation |
|---|---|
| `vm_uptime_seconds` | SUM(duration_seconds) |
| `vm_cpu_core_seconds` | SUM(cpu_core_seconds) |
| `vm_memory_gib_seconds` | SUM(memory_gib_seconds) |

**What's needed:**

- A `metering_entries` table with columns: `id`, `raw_event_id` (FK),
  `resource_type`, `resource_id`, `tenant_id`, `meter_name`, `value`, `unit`,
  `period_start`, `period_end`
- A metering handler that extracts meter values from each event and inserts
  one row per meter
- If consuming the Watch stream (which doesn't carry `duration_seconds`),
  calculate duration from the time since the last event for the same resource

**Effort:** Medium — new table, new handler, meter extraction logic.

**Note on event source:** The requirements spec defines CloudEvents from a
metering collector with pre-calculated fields (`duration_seconds`,
`cpu_core_seconds`). This collector is still early-stage. Our Watch stream
consumer gets the same underlying data but must calculate durations itself.
The metering entries are functionally equivalent either way — the difference
is where the duration calculation happens (collector vs consumer).

### 3. Billable State Filtering

**What's missing:** Metering entries should only be produced when the resource
is in a billable state. Currently all state transitions are stored in
inventory but there's no distinction between billable and non-billable.

**Billable states:**
- Clusters: `CLUSTER_STATE_READY`, `CLUSTER_STATE_PROGRESSING`
- Compute instances: `COMPUTE_INSTANCE_STATE_RUNNING`

**Non-billable states (update inventory but don't meter):**
- Clusters: `CLUSTER_STATE_FAILED`, `CLUSTER_STATE_UNSPECIFIED`
- Compute instances: `COMPUTE_INSTANCE_STATE_STOPPED`,
  `COMPUTE_INSTANCE_STATE_PAUSED`, `COMPUTE_INSTANCE_STATE_FAILED`,
  `COMPUTE_INSTANCE_STATE_DELETING`

**What's needed:**

- A billable state check in the metering handler: only insert metering
  entries when `state` is in the billable set
- Inventory updates should still happen for all states (to track transitions)

**Effort:** Small — a state check before metering entry insertion.

### 4. CloudEvents Envelope

**What's missing:** The system currently consumes OSAC's internal protobuf
Watch events (with `id`, `type` as CREATED/UPDATED/DELETED, and resource
payload). The requirements define CloudEvents 1.0 format with `specversion`,
`type`, `source`, `id`, `time`, `subject`, `data` fields.

**Why it matters (and why it doesn't much):**

CloudEvents is an interoperability standard — a standardized envelope around
the same data. For the PoC, the Watch stream works and provides the same
information. The CloudEvents envelope becomes important when:
- Kafka is introduced as transport (Kafka messages would be CloudEvents)
- Other consumers need to process the same events
- The metering collector starts producing events in CloudEvents format

**What's needed:**

- A CloudEvents deserialization layer that can parse the standard envelope
  and extract `ce_id`, `ce_type`, `ce_source`, `ce_time`, `ce_subject`,
  and `data`
- Ability to handle both formats: Watch stream events (current) and
  CloudEvents (future) through a common interface

**Effort:** Small — a parser struct and a normalization step. No architectural
change.

### 5. Project Entity

**What's missing:** OSAC uses a Tenant → Project → Resource hierarchy. The
inventory-watcher stores `tenant` as a flat string field on resources but
does not track projects as separate entities.

**What's needed:**

- A `projects` table with `id`, `tenant_id` (FK), `external_id`, `name`
- FK columns on `inventory_compute_instance` and `inventory_cluster`
  pointing to `projects`
- Sync projects from OSAC via the Projects List endpoint (the OSAC client
  already supports this)
- Reconciler should sync projects alongside clusters and compute instances

**Effort:** Small — one new table, a few FK columns, one additional
reconciliation call.

## Processing Pipeline: Current vs Target

**Current (inventory-watcher):**

```
OSAC Watch stream event
  → dispatch by type (CREATED/UPDATED/DELETED)
  → upsert inventory (clusters, compute_instances, instance_types)

Periodic summarizer
  → query inventory for resources alive during period
  → calculate duration × cores = cpu_core_hours
  → write daily_usage_summary
```

**Target (with gaps closed):**

```
Event received (Watch stream or CloudEvents)
  → normalize to common event struct
  → INSERT into raw_events (dedup on ce_id)
  → upsert inventory (clusters, compute_instances, instance_types)
  → if billable state:
      → extract meters (vm_uptime_seconds, cpu_core_seconds, ...)
      → INSERT into metering_entries (one row per meter)

Metering entries feed downstream:
  → rate lookup → cost_entries (requirement #6)
  → quota check → alerts (requirement #5)
  → report API aggregation
```

The key difference: the current system calculates usage in a batch
summarization step from inventory timestamps. The target system produces
metering entries per event as they arrive, which enables real-time quota
checking and the 60-second processing SLA.

## Recommended Implementation Order

1. **Raw event log** — lowest risk, highest value (audit trail + replay).
   Add `raw_events` table and insert before processing.

2. **Billable state filtering** — trivial to add, prevents metering
   non-billable states.

3. **Metering entries** — the core gap. Transforms the system from
   batch-summarization to per-event metering. Prerequisite for rates,
   costs, and quotas.

4. **Project entity** — add projects table and sync. Small but needed for
   the Tenant → Project → Resource hierarchy.

5. **CloudEvents envelope** — add when the metering collector or Kafka
   transport is ready. Low priority for PoC since the Watch stream works.

## Summary

The inventory-watcher covers the **inventory synchronization** half of
requirement #1 well: cluster and compute instance tracking, reconciliation,
real-time event ingestion, and instance type catalog sync all work end-to-end.

The **metering** half has gaps: no raw event log, no per-event metering
entries, no billable state filtering, and no project hierarchy. These are
additions to the existing pipeline, not architectural changes. The most
impactful gap is metering entries — closing it transforms the system from a
batch inventory tracker into an event-driven metering pipeline, which is
what the downstream requirements (rates, costs, quotas, alerts) need.

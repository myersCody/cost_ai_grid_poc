# Requirement 8: Bare Metal Costing — Gap Analysis

> **Requirement:** Support bare metal nodes provisioned through OSAC (BMaaS).
> Consume bare metal service CloudEvents for capacity-based costing.
>
> **Source:** [poc_requirements_overview.md#req-8](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-8--bare-metal-costing-osac-bare-metal-service)

## OSAC State

### What exists

- **Proto definition:** [`baremetal_instance_type.proto`](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/baremetal_instance_type.proto)
  exists with `BareMetalInstance`, `BareMetalInstanceSpec`, `BareMetalInstanceStatus`
- **REST API:** `/api/fulfillment/v1/baremetal_instances` — CRUD endpoints
- **Spec fields:** `catalog_item`, `ssh_public_key`, `user_data`, `run_strategy`
- **Status fields:** `state` (STARTING, RUNNING, STOPPED, FAILED, DELETING, etc.)
- **OSAC team status:** bare metal service actively being built (confirmed Jun 24)

### What's missing in OSAC

**BareMetalInstance is NOT in the Watch stream `oneof payload`:**

```protobuf
// Current event_type.proto — no BareMetalInstance
oneof payload {
    Cluster cluster = 3;
    ClusterTemplate cluster_template = 4;
    HostType host_type = 5;
    ComputeInstanceTemplate compute_instance_template = 7;
    ComputeInstance compute_instance = 8;
    Role role = 9;
    RoleBinding role_binding = 10;
    Project project = 11;
    InstanceType instance_type = 12;
    Tenant tenant = 13;
    // BareMetalInstance is NOT here
}
```

This means the Watch stream will **not deliver** bare metal lifecycle events.
OSAC needs to add `BareMetalInstance bare_metal_instance = N;` to the `oneof`
for real-time event ingestion to work.

**No BMaaS metering collector** — the `osac-metering-discover-poc` only has
collectors for CaaS (`collect-caas.sh`) and VMaaS (`collect.sh`). No bare
metal collector exists.

**No BMaaS CloudEvent schema** — the event-types.md marks BMaaS as:
> "Status: Not yet defined by OSAC. Metering requirements still being scoped."

## What We Need to Implement (Our Side)

All follow the same pattern as ComputeInstance — copy and adapt:

| Component | File | What to add |
|---|---|---|
| Go types | `internal/osac/types.go` | `BareMetalInstance` struct |
| Inventory table | `internal/inventory/store.go` | `inventory_bare_metal_instance` |
| Model | `internal/inventory/models.go` | `BareMetalInstanceRecord` |
| Watch handler | `internal/watcher/watcher.go` | Handle BareMetalInstance in event dispatch |
| Reconciler | `internal/reconciler/reconciler.go` | `ListBareMetalInstances()` call |
| OSAC client | `internal/osac/client.go` | `ListBareMetalInstances()` method |
| Billable states | `internal/metering/billable.go` | `BARE_METAL_INSTANCE_STATE_RUNNING` |
| Metering | `internal/metering/metering.go` | Sweep for bare metal + meters |
| Ingest handler | `internal/ingest/handler.go` | `osac.bare_metal.lifecycle` event type |
| Default rates | `internal/rating/rating.go` | `bm_uptime_seconds`, `bm_cpu_core_seconds` rates |

### Meters

| Meter | Unit | Formula |
|---|---|---|
| `bm_uptime_seconds` | seconds | duration |
| `bm_cpu_core_seconds` | core_seconds | cores × duration |
| `bm_memory_gib_seconds` | gib_seconds | memory_gib × duration |

### Inventory Table

```sql
CREATE TABLE IF NOT EXISTS inventory_bare_metal_instance (
    instance_id    TEXT PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    tenant         TEXT NOT NULL DEFAULT '',
    project        TEXT NOT NULL DEFAULT '',
    catalog_item   TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT '',
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT NOT NULL DEFAULT '',
    last_updated   TIMESTAMPTZ DEFAULT NOW(),
    last_metered_at TIMESTAMPTZ
);
```

### BareMetalInstance Proto Fields

From [`baremetal_instance_type.proto`](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/baremetal_instance_type.proto):

```protobuf
message BareMetalInstance {
    string id = 1;
    Metadata metadata = 2;
    BareMetalInstanceSpec spec = 3;
    BareMetalInstanceStatus status = 4;
}

message BareMetalInstanceSpec {
    string catalog_item = 1;       // references BareMetalInstanceCatalogItem
    optional string ssh_public_key = 2;
    optional string user_data = 3;
    optional BareMetalInstanceRunStrategy run_strategy = 4;
}
```

**Note:** The spec references a `catalog_item` for hardware specs (CPU, RAM,
disk), not inline `cores`/`memory_gib` like ComputeInstance. We may need to
look up the catalog item to get the hardware specs for metering. This is a
difference from VMs where `spec.cores` and `spec.memory_gib` are directly
on the instance.

## Blockers

| Blocker | Owner | Impact |
|---|---|---|
| BareMetalInstance not in Watch stream `oneof` | OSAC team | No real-time events; must use reconciler only |
| No BMaaS metering collector | OSAC team | No heartbeat CloudEvents; must use local sweep |
| No BMaaS CloudEvent schema | OSAC team | Can't define ingest handler format |
| Hardware specs on catalog_item, not instance | OSAC team | Need catalog lookup for cores/memory |

## Workaround (Without OSAC Changes)

We can implement bare metal support using the same approach as MaaS:

1. **Reconciler** polls `GET /api/fulfillment/v1/baremetal_instances`
   periodically — this endpoint exists and works
2. **Metering sweep** calculates duration-based usage from inventory
3. **Ingest endpoint** accepts mock `osac.bare_metal.lifecycle` CloudEvents
4. No Watch stream events — inventory updates only on reconciliation
   (every 5 minutes by default)

This gives us bare metal costing with 5-minute granularity instead of
real-time. Sufficient for PoC.

## Open Questions

1. **Standalone bare metal** — do we need to support bare metal nodes
   outside of an OpenShift cluster? The proto supports it but the
   requirement is unclear.

2. **Hardware specs** — how do we get CPU/memory for a bare metal instance?
   The spec only has `catalog_item` reference. We may need to sync
   `BareMetalInstanceCatalogItem` to resolve specs for metering.

## Effort

**Our side:** Small — same pattern as VMs. 1-2 days of work.

**Blocked on:** OSAC adding BareMetalInstance to the Watch stream `oneof`
(for real-time events) and defining hardware specs resolution (catalog
lookup or inline fields).

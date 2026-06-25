# CloudEvents-Based Cost Management

## Motivation

Explore building a cost management system that consumes events from the OSAC
fulfillment-service for inventory tracking instead of the current Koku batch
report pipeline. The system would provide the same style of reports as Koku
(daily, monthly granularity with filtering and grouping) but build its inventory
state incrementally from real-time events produced by OSAC.

## OSAC Fulfillment Service

**Repository:** https://github.com/osac-project/fulfillment-service

The fulfillment-service is a Go-based infrastructure management platform that
manages bare-metal and virtual cloud resources. It already emits lifecycle
events for all managed resources.

### Event System

OSAC uses PostgreSQL NOTIFY/LISTEN with protobuf payloads. Events are also
available via a gRPC `Watch` streaming RPC:

```protobuf
service Events {
  rpc Watch(EventsWatchRequest) returns (stream EventsWatchResponse);
}

enum EventType {
  EVENT_TYPE_OBJECT_CREATED = 1;
  EVENT_TYPE_OBJECT_UPDATED = 2;
  EVENT_TYPE_OBJECT_DELETED = 3;
}
```

Events carry the full object state in a `oneof` payload — not just a
notification, but the complete resource spec and status.

### Entities Relevant to Cost Management

OSAC manages 35+ resource types. The ones most relevant to cost tracking:

| Entity | Key Fields | Cost Relevance |
|--------|-----------|----------------|
| `Cluster` | id, node sets, template | Top-level grouping |
| `ComputeInstance` | id, instance type, tenant, project | CPU/memory cost |
| `BareMetalInstance` | id, host type | Physical infrastructure cost |
| `HostType` | CPU, RAM, GPU specs | Rate basis for bare metal |
| `InstanceType` | vCPUs, memory, storage | Rate basis for compute |
| `VirtualNetwork` | subnets, security groups | Network cost |
| `PublicIP` / `ExternalIP` | IP address, attachment | IP cost |
| `Project` | id, organization, tenant | Project-level grouping |
| `Tenant` | id | Multi-tenancy isolation |

### OSAC Data Model

All resources follow a standard pattern:

```protobuf
message <Entity> {
  string id = 1;
  Metadata metadata = 2;       // creation/deletion timestamps, labels, tenant
  <Entity>Spec spec = 3;       // desired state
  <Entity>Status status = 4;   // observed state
}
```

Database storage uses JSONB columns with the full protobuf serialized as JSON,
plus indexed columns for id, name, tenants[], creators[], labels.

### Standard Metadata (Available on Every Event)

```protobuf
message Metadata {
  google.protobuf.Timestamp creation_timestamp;
  google.protobuf.Timestamp deletion_timestamp;
  string creator;
  string name;
  string tenant;
  map<string, string> labels;
  map<string, string> annotations;
  int32 version;
}
```

The `creation_timestamp` and `deletion_timestamp` on every entity provide the
duration tracking needed for cost calculations without any additional work.

## Architecture Overview

### Current Koku Flow

```
Operator collects metrics -> CSV reports -> Masu downloads/processes
-> Raw line items -> Daily summaries -> UI summaries -> Report API
```

### Proposed OSAC + Cost Management Flow

```
OSAC fulfillment-service (manages infrastructure)
    |
    | gRPC Watch stream (CREATED/UPDATED/DELETED events)
    v
Cost Event Consumer (new service)
    |
    | builds/updates inventory with timestamps
    v
Inventory State Store (PostgreSQL)
    |
    | periodic summarization job (hourly/daily)
    v
Daily Usage Summaries (Koku-compatible schema)
    |
    | cost model application (rates, markup, distribution)
    v
Cost Summary Tables -> Report API
```

### Running Locally Together

Reference: https://gist.github.com/myersCody/3c49a439c10539f5cefceb9abc77d07c

| Service | Port |
|---------|------|
| Koku API | 8000 |
| Koku masu | 5042 |
| Koku PostgreSQL | 15432 |
| OSAC gRPC | 8010 |
| OSAC REST | 8011 |
| OSAC PostgreSQL | 5433 |

Setup: clone fulfillment-service, start PostgreSQL on port 5433, build Go
binaries, generate TLS certs, start gRPC server + REST gateway + OIDC server.

## Cost Event Consumer

The new component that bridges OSAC events to cost data.

### Input: OSAC Watch Stream

Connect to the OSAC gRPC Watch endpoint and receive events for all resource
types. Each event contains:

- Event type (CREATED, UPDATED, DELETED)
- Full resource state (spec + status + metadata)
- Timestamps (creation, deletion)
- Tenant and project context
- Labels and annotations

### Inventory State Store

Track live and historical resources with duration information:

**`inventory_compute_instance`**:
- instance_id, name, tenant, project
- instance_type (references CPU/memory/storage specs)
- node, cluster
- created_at, deleted_at
- labels (JSONB)

**`inventory_bare_metal_instance`**:
- instance_id, name, tenant
- host_type (references CPU/RAM/GPU specs)
- created_at, deleted_at

**`inventory_cluster`**:
- cluster_id, name, tenant
- template, node_set_count
- created_at, deleted_at

**`inventory_network_resource`**:
- resource_id, resource_type (virtual_network, subnet, public_ip, etc.)
- tenant, project
- created_at, deleted_at

### Processing Logic

```
on OBJECT_CREATED:
    INSERT into inventory table with created_at = metadata.creation_timestamp

on OBJECT_UPDATED:
    UPDATE inventory row with latest spec/status
    (track spec changes that affect cost, e.g., instance type change)

on OBJECT_DELETED:
    SET deleted_at = metadata.deletion_timestamp on inventory row
```

## Summarization

A periodic job (hourly or daily) materializes usage from inventory into cost
summary tables.

### Duration Calculation

For each resource alive during a reporting period:

```
duration = min(period_end, deleted_at or now()) - max(period_start, created_at)

cpu_core_hours    = instance_type.vcpus   x duration_hours
memory_gb_hours   = instance_type.memory  x duration_hours
storage_gb_months = storage_capacity_gb   x duration_hours / hours_in_month
```

### Mapping to Koku-Compatible Summaries

The summarization output should match `OCPUsageLineItemDailySummary` structure
where applicable, enabling reuse of downstream Koku components:

- `cluster_id`, `cluster_alias` -> from OSAC Cluster
- `namespace` -> from OSAC Project
- `node` -> from OSAC ComputeInstance or BareMetalInstance
- `pod_request_cpu_core_hours` -> derived from instance type vCPUs x duration
- `pod_request_memory_gigabyte_hours` -> from instance type memory x duration
- `node_capacity_cpu_core_hours` -> from host type / instance type specs

### Entity Mapping: OSAC to Koku Concepts

| OSAC Entity | Koku Concept | Notes |
|-------------|-------------|-------|
| Tenant | Account / Customer | Top-level isolation |
| Project | Namespace / Project | Cost grouping unit |
| Cluster | Cluster | Direct mapping |
| ComputeInstance | Node / Pod | Billable compute unit |
| BareMetalInstance | Node | Physical infrastructure |
| InstanceType | Instance Type | Rate lookup key |
| HostType | Host specs | Capacity basis |
| VirtualNetwork | -- | New cost category |
| PublicIP / ExternalIP | -- | New cost category |
| Labels | Tags | Cost allocation tags |

## Reuse from Koku

### Can Be Reused

- **Report API** -- all `/reports/openshift/*` endpoints, serializers, query
  params, pagination
- **Cost model logic** -- rates, markup, distribution SQL (`usage_costs.sql`,
  `distribute_platform_cost.sql`)
- **UI summary tables** -- `OCPCostSummaryP`, `OCPPodSummaryByProjectP`, etc.
- **Cost categories** -- namespace-to-category mapping

### Koku Report API Reference

Reports support `daily` and `monthly` granularity with flexible filtering:

- **Cost reports**: `/reports/openshift/costs/`
- **Compute (CPU)**: `/reports/openshift/compute/`
- **Memory**: `/reports/openshift/memory/`
- **Volumes**: `/reports/openshift/volumes/`
- **Network**: `/reports/openshift/network/`
- **GPU**: `/reports/openshift/gpu/`

Query parameters:
- `filter[time_scope_value]`, `filter[time_scope_units]`, `filter[resolution]`
- `group_by[cluster]`, `group_by[node]`, `group_by[project]`, `group_by[tag:*]`
- `order_by[cost]`, `order_by[usage]`, `order_by[date]`
- `start_date` / `end_date` (ISO-8601)

### Koku Data Model Reference

The key model backing OCP reports is `OCPUsageLineItemDailySummary` with fields:

- **Pod metrics**: `pod_usage_cpu_core_hours`, `pod_request_cpu_core_hours`,
  `pod_effective_usage_cpu_core_hours`, `pod_usage_memory_gigabyte_hours`,
  `pod_request_memory_gigabyte_hours`, `pod_effective_usage_memory_gigabyte_hours`
- **Capacity**: `node_capacity_cpu_core_hours`, `node_capacity_memory_gigabyte_hours`,
  `cluster_capacity_cpu_core_hours`, `cluster_capacity_memory_gigabyte_hours`
- **Storage**: `persistentvolumeclaim_capacity_gigabyte`,
  `persistentvolumeclaim_usage_gigabyte_months`,
  `volume_request_storage_gigabyte_months`
- **Identifiers**: `cluster_id`, `cluster_alias`, `namespace`, `node`,
  `resource_id`, `data_source`
- **Cost fields**: `infrastructure_raw_cost`, `infrastructure_markup_cost`,
  `cost_model_cpu_cost`, `cost_model_memory_cost`, `cost_model_volume_cost`,
  `distributed_cost`

Cost model application happens via SQL templates in
`koku/masu/database/sql/openshift/cost_model/`.

## New Components

- **Cost Event Consumer** -- connects to OSAC gRPC Watch stream, processes
  CREATED/UPDATED/DELETED events into inventory state
- **Inventory State Store** -- PostgreSQL tables tracking live and historical
  resources with creation/deletion timestamps
- **Summarization Job** -- periodic job converting inventory durations into
  Koku-compatible daily summaries
- **Event Reliability Layer** -- idempotent processing, deduplication,
  reconciliation

## Event Reliability

### Challenge

If a DELETED event is lost, a resource looks like it's running forever,
inflating costs. OSAC's event system uses PostgreSQL NOTIFY which is
fire-and-forget (no persistence beyond the 1-minute notification table).

### Options

1. **Periodic reconciliation** -- periodically call OSAC's `List` RPCs to get
   the full current state and diff against inventory. Simplest approach.
   Equivalent to what the current Koku operator does with batch reports.

2. **Version-based catch-up** -- OSAC resources have a `version` field
   (auto-incremented). The consumer can track last-seen version per resource
   type and detect gaps.

3. **Heartbeat / full-sync events** -- OSAC could emit periodic full-state
   snapshots on a schedule.

Option 1 is recommended as the starting point. The OSAC `List` RPCs with CEL
filtering already support querying all resources of a type, so reconciliation
is straightforward:

```
# Periodically (e.g., every hour):
current_instances = OSAC.ComputeInstances.List()
known_instances   = SELECT * FROM inventory_compute_instance WHERE deleted_at IS NULL

# Mark any known instances not in current_instances as deleted
# Insert any current instances not yet known
```

## Consumer Design Sketch

The cost event consumer would be a Go service that connects to OSAC's gRPC
Watch stream and maintains the inventory state store.

### Component Structure

```
cost-event-consumer/
├── cmd/
│   └── consumer/
│       └── main.go              # entry point, starts all goroutines
├── internal/
│   ├── watcher/
│   │   └── watcher.go           # gRPC Watch stream consumer
│   ├── inventory/
│   │   ├── store.go             # inventory state CRUD operations
│   │   └── models.go            # inventory table models
│   ├── reconciler/
│   │   └── reconciler.go        # periodic full-state reconciliation
│   ├── summarizer/
│   │   ├── summarizer.go        # duration -> usage calculation
│   │   └── writer.go            # write summaries to cost DB
│   └── config/
│       └── config.go            # OSAC connection, DB config, intervals
├── migrations/
│   └── 001_inventory_tables.sql # inventory schema
└── proto/
    └── (generated OSAC client stubs)
```

### Event Watcher (core loop)

```go
// Connects to OSAC gRPC Watch stream and dispatches events
// to the inventory store.

func (w *Watcher) Run(ctx context.Context) error {
    stream, err := w.eventsClient.Watch(ctx, &EventsWatchRequest{})
    if err != nil {
        return err
    }

    for {
        resp, err := stream.Recv()
        if err != nil {
            // reconnect with backoff
            return w.reconnect(ctx, err)
        }

        event := resp.Event
        switch event.Type {
        case EVENT_TYPE_OBJECT_CREATED:
            w.handleCreated(ctx, event)
        case EVENT_TYPE_OBJECT_UPDATED:
            w.handleUpdated(ctx, event)
        case EVENT_TYPE_OBJECT_DELETED:
            w.handleDeleted(ctx, event)
        }
    }
}

func (w *Watcher) handleCreated(ctx context.Context, event *Event) {
    switch payload := event.Payload.(type) {
    case *Event_ComputeInstance:
        w.store.UpsertComputeInstance(ctx, InventoryComputeInstance{
            InstanceID:   payload.ComputeInstance.Id,
            Name:         payload.ComputeInstance.Metadata.Name,
            Tenant:       payload.ComputeInstance.Metadata.Tenant,
            InstanceType: payload.ComputeInstance.Spec.InstanceType,
            CreatedAt:    payload.ComputeInstance.Metadata.CreationTimestamp.AsTime(),
        })
    case *Event_Cluster:
        w.store.UpsertCluster(ctx, InventoryCluster{
            ClusterID: payload.Cluster.Id,
            Name:      payload.Cluster.Metadata.Name,
            Tenant:    payload.Cluster.Metadata.Tenant,
            CreatedAt: payload.Cluster.Metadata.CreationTimestamp.AsTime(),
        })
    // ... other resource types
    }
}

func (w *Watcher) handleDeleted(ctx context.Context, event *Event) {
    switch payload := event.Payload.(type) {
    case *Event_ComputeInstance:
        w.store.MarkDeleted(ctx, "compute_instance",
            payload.ComputeInstance.Id,
            payload.ComputeInstance.Metadata.DeletionTimestamp.AsTime(),
        )
    // ... other resource types
    }
}
```

### Reconciler (drift correction)

```go
// Periodically calls OSAC List RPCs to catch missed events.
// Runs every reconcileInterval (e.g., 1 hour).

func (r *Reconciler) Run(ctx context.Context) error {
    ticker := time.NewTicker(r.reconcileInterval)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return nil
        case <-ticker.C:
            r.reconcileAll(ctx)
        }
    }
}

func (r *Reconciler) reconcileAll(ctx context.Context) {
    // Get all compute instances from OSAC
    osacInstances, _ := r.computeClient.List(ctx, &ComputeInstanceListRequest{})

    // Get all known live instances from inventory
    knownInstances, _ := r.store.ListAlive(ctx, "compute_instance")

    osacSet := toIDSet(osacInstances)
    knownSet := toIDSet(knownInstances)

    // Instances in OSAC but not in inventory -> missed CREATED event
    for id := range osacSet.Difference(knownSet) {
        instance := osacInstances[id]
        r.store.UpsertComputeInstance(ctx, fromOSAC(instance))
    }

    // Instances in inventory but not in OSAC -> missed DELETED event
    for id := range knownSet.Difference(osacSet) {
        r.store.MarkDeleted(ctx, "compute_instance", id, time.Now())
    }
}
```

### Summarizer (usage calculation)

```go
// Runs periodically (e.g., hourly) to convert inventory durations
// into cost-ready usage summaries.

func (s *Summarizer) SummarizePeriod(ctx context.Context, start, end time.Time) error {
    // Query all compute instances alive during [start, end]
    instances, _ := s.store.AliveDuring(ctx, "compute_instance", start, end)

    for _, inst := range instances {
        effectiveStart := max(start, inst.CreatedAt)
        effectiveEnd := end
        if inst.DeletedAt != nil && inst.DeletedAt.Before(end) {
            effectiveEnd = *inst.DeletedAt
        }

        durationHours := effectiveEnd.Sub(effectiveStart).Hours()

        // Look up instance type specs
        specs, _ := s.instanceTypes.Get(ctx, inst.InstanceType)

        summary := UsageSummary{
            UsageStart:       start,
            UsageEnd:         end,
            ClusterID:        inst.ClusterID,
            Tenant:           inst.Tenant,
            Project:          inst.Project,
            ResourceID:       inst.InstanceID,
            CPUCoreHours:     float64(specs.VCPUs) * durationHours,
            MemoryGBHours:    float64(specs.MemoryMB) / 1024.0 * durationHours,
            InstanceType:     inst.InstanceType,
        }

        s.writer.Write(ctx, summary)
    }

    return nil
}
```

### Inventory Schema

```sql
CREATE TABLE inventory_compute_instance (
    instance_id    TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    tenant         TEXT NOT NULL,
    project        TEXT,
    cluster_id     TEXT,
    instance_type  TEXT NOT NULL,
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT,
    last_updated   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_ci_alive ON inventory_compute_instance (deleted_at)
    WHERE deleted_at IS NULL;
CREATE INDEX idx_ci_tenant ON inventory_compute_instance (tenant);
CREATE INDEX idx_ci_period ON inventory_compute_instance (created_at, deleted_at);

CREATE TABLE inventory_cluster (
    cluster_id     TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    tenant         TEXT NOT NULL,
    template       TEXT,
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT,
    last_updated   TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE inventory_bare_metal_instance (
    instance_id    TEXT PRIMARY KEY,
    name           TEXT NOT NULL,
    tenant         TEXT NOT NULL,
    host_type      TEXT NOT NULL,
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT,
    last_updated   TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE inventory_network_resource (
    resource_id    TEXT PRIMARY KEY,
    resource_type  TEXT NOT NULL,  -- virtual_network, subnet, public_ip, etc.
    name           TEXT NOT NULL,
    tenant         TEXT NOT NULL,
    project        TEXT,
    labels         JSONB DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL,
    deleted_at     TIMESTAMPTZ,
    last_event_id  TEXT,
    last_updated   TIMESTAMPTZ DEFAULT NOW()
);

-- Daily usage summaries (output of summarizer)
CREATE TABLE daily_usage_summary (
    id              BIGSERIAL PRIMARY KEY,
    usage_date      DATE NOT NULL,
    cluster_id      TEXT,
    tenant          TEXT NOT NULL,
    project         TEXT,
    resource_id     TEXT NOT NULL,
    resource_type   TEXT NOT NULL,
    instance_type   TEXT,
    cpu_core_hours  NUMERIC(18,6),
    memory_gb_hours NUMERIC(18,6),
    storage_gb_months NUMERIC(18,6),
    labels          JSONB DEFAULT '{}'::jsonb
) PARTITION BY RANGE (usage_date);

CREATE INDEX idx_dus_date_tenant ON daily_usage_summary (usage_date, tenant);
CREATE INDEX idx_dus_cluster ON daily_usage_summary (usage_date, cluster_id);
```

### Service Startup

```go
func main() {
    cfg := config.Load()

    // Connect to OSAC gRPC
    osacConn, _ := grpc.Dial(cfg.OSACAddress, grpc.WithTransportCredentials(...))
    eventsClient := osacpb.NewEventsClient(osacConn)
    computeClient := osacpb.NewComputeInstancesClient(osacConn)

    // Connect to inventory database
    inventoryDB, _ := pgxpool.New(ctx, cfg.InventoryDBURL)
    store := inventory.NewStore(inventoryDB)

    // Start components
    watcher := watcher.New(eventsClient, store)
    reconciler := reconciler.New(computeClient, store, 1*time.Hour)
    summarizer := summarizer.New(store, costDB, 1*time.Hour)

    g, ctx := errgroup.WithContext(ctx)
    g.Go(func() error { return watcher.Run(ctx) })
    g.Go(func() error { return reconciler.Run(ctx) })
    g.Go(func() error { return summarizer.Run(ctx) })
    g.Wait()
}
```

### Key Design Decisions

1. **Idempotent upserts** -- use `last_event_id` to deduplicate events. If the
   same event is received twice (e.g., after reconnection), the second write is
   a no-op.

2. **Separate inventory and cost databases** -- the inventory store is owned by
   the consumer. Cost summaries can be written to either a separate cost DB or
   to Koku's database directly (depending on integration depth).

3. **Instance type caching** -- HostType and InstanceType specs change rarely.
   Cache them locally and refresh on UPDATE events to avoid per-summary lookups.

4. **Reconnection with replay** -- on gRPC stream disconnect, reconnect and
   immediately trigger a reconciliation to catch any events missed during
   downtime.

5. **Partitioned summaries** -- `daily_usage_summary` is partitioned by date
   for efficient time-range queries and cleanup of old data.

## Implementation Status

A working Go consumer has been implemented at
`/Users/mpovolny/Projects/cost-event-consumer/`.

### Verified Working (2026-06-25)

- **Reconciliation**: consumer calls OSAC List endpoints on startup and imports
  all compute instances and instance types into the cost inventory database
- **Real-time event watching**: consumer receives CREATED/UPDATED/DELETED events
  via the Watch stream at `/api/private/v1/events/watch` and immediately updates
  the inventory
- **Field mapping**: cores, memory_gib, tenant, labels all correctly captured
  from the OSAC REST API (snake_case JSON format)

### Test Data Created

```
Compute Instances:
  worker-1:     4 cores,  16GB
  worker-2:     8 cores,  32GB
  worker-3:    12 cores,  48GB
  realtime-vm: 16 cores,  64GB  (created via Watch stream)

Instance Types:
  standard-2-8:   2 cores,  8GB
  standard-4-16:  4 cores, 16GB
  standard-8-32:  8 cores, 32GB
```

### Local Dev Tips

- OSAC without a controller is effectively "fake mode" — the gRPC server stores
  resources in PostgreSQL but no real infrastructure is provisioned
- Use the private API (`/api/private/v1/...`) to create resources with desired
  status states directly (e.g., COMPUTE_INSTANCE_STATE_RUNNING)
- Compute instances require: template, cores, memory_gib, network_attachments,
  boot_disk, image, run_strategy
- Prerequisites: create network_class (private API), virtual_network (set to READY
  via private API PATCH), subnet (private API), compute_instance_template (private API)

## Potential Cost Reports

Given OSAC's resource model (VMs, clusters, networking — not individual pods),
the reports we can provide:

1. **Compute cost by tenant** — CPU-core-hours and memory-GB-hours per tenant,
   derived from compute instance durations x specs
2. **Compute cost by project** — same breakdown per project within a tenant
3. **Compute cost by instance type** — which instance types are consuming most
4. **Cluster cost** — cost per cluster based on node set sizes x host type specs
5. **Network resource costs** — per-tenant VPN/subnet/IP allocation costs
   (duration-based billing)
6. **Daily/monthly usage trends** — time-series of resource consumption
7. **Instance type distribution** — breakdown of instance type usage across
   tenants/projects

### What We Can Reuse from Koku

- **Report API pattern**: filtering (group_by, filter, exclude), pagination,
  granularity (daily/monthly)
- **Cost model concept**: rates per resource type ($/cpu-core-hour, $/GB-hour)
- **Cost distribution**: distributing shared/platform costs to tenants by usage
- **Markup**: customer-defined markup on infrastructure costs

### What's Different from Koku

- **No pod-level tracking**: the billable unit is the ComputeInstance (VM), not
  individual pods running inside it
- **No cloud provider costs**: OSAC manages bare-metal/private cloud, so there's
  no AWS/Azure/GCP bill to reconcile against
- **Simpler ingestion**: real-time events instead of daily CSV report processing
- **Network billing**: new cost category that Koku doesn't have

## Open Questions

1. **Cost model definition** -- should rates live in the cost management service
   (like Koku's CostModel) or in OSAC (e.g., on InstanceType/HostType)?

2. **Granularity of compute tracking** -- OSAC manages ComputeInstances (VMs)
   and BareMetalInstances, not individual pods. The cost unit is the instance,
   not the pod. This is a simpler model than Koku's pod-level tracking but
   means no per-pod chargeback within an instance.

3. **Network and IP costing** -- OSAC tracks VirtualNetworks, Subnets, and
   IP addresses, which Koku doesn't cost today. Need to define rate models
   for these.

4. **Multi-tenancy** -- OSAC has built-in tenant isolation. The cost service
   should respect this, showing each tenant only their own costs.

5. **Event format** -- should OSAC events be wrapped in CloudEvents spec
   (https://cloudevents.io/) for interoperability, or consumed as-is via gRPC?

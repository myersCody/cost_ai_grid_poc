# gRPC Messages Catalog

> Messages consumed from the OSAC fulfillment-service via the
> [Events Watch stream](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/events_service.proto).

## Watch Stream Endpoint

| Protocol | Endpoint | Source |
|---|---|---|
| gRPC | `osac.public.v1.Events.Watch` | [events_service.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/events_service.proto) |
| REST (via gRPC-Gateway) | `GET /api/private/v1/events/watch` | [private events_service.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/private/osac/private/v1/events_service.proto) |

We consume via the REST gateway (NDJSON streaming over HTTP/1.1).
The client is in [`internal/osac/client.go`](../inventory-watcher/internal/osac/client.go) (`WatchEvents` method).

## Event Envelope

Every event has this structure, defined in
[event_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/event_type.proto):

```protobuf
message Event {
  string id = 1;
  EventType type = 2;       // CREATED, UPDATED, DELETED
  oneof payload { ... }     // one of the resource types below
}
```

Our Go mapping: [`internal/osac/types.go`](../inventory-watcher/internal/osac/types.go) (`Event` struct)

### Event Types

| Enum | JSON Value | Handled |
|---|---|---|
| `EVENT_TYPE_OBJECT_CREATED` | `"EVENT_TYPE_OBJECT_CREATED"` | Yes |
| `EVENT_TYPE_OBJECT_UPDATED` | `"EVENT_TYPE_OBJECT_UPDATED"` | Yes |
| `EVENT_TYPE_OBJECT_DELETED` | `"EVENT_TYPE_OBJECT_DELETED"` | Yes |

## Resource Messages Consumed

### ComputeInstance

| Field | Source |
|---|---|
| Proto definition | [compute_instance_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/compute_instance_type.proto) |
| Go type | [`internal/osac/types.go`](../inventory-watcher/internal/osac/types.go) → `ComputeInstance` |
| Handler | [`internal/watcher/watcher.go`](../inventory-watcher/internal/watcher/watcher.go) → `upsertComputeInstance` |
| Inventory table | `inventory_compute_instance` |
| Metering | `vm_uptime_seconds`, `vm_cpu_core_seconds`, `vm_memory_gib_seconds` |

**Key fields consumed:**
- `id` — instance UUID
- `metadata.name`, `metadata.tenant`, `metadata.labels`, `metadata.creation_timestamp`, `metadata.deletion_timestamp`
- `spec.cores`, `spec.memory_gib`, `spec.instance_type`, `spec.template`
- `status.state` — billable when `COMPUTE_INSTANCE_STATE_RUNNING`

### Cluster

| Field | Source |
|---|---|
| Proto definition | [cluster_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/cluster_type.proto) |
| Go type | [`internal/osac/types.go`](../inventory-watcher/internal/osac/types.go) → `Cluster` |
| Handler | [`internal/watcher/watcher.go`](../inventory-watcher/internal/watcher/watcher.go) → `upsertCluster` |
| Inventory table | `inventory_cluster` |
| Metering | `cluster_uptime_seconds`, `cluster_worker_node_seconds` |

**Key fields consumed:**
- `id` — cluster UUID
- `metadata.name`, `metadata.tenant`, `metadata.creation_timestamp`
- `spec.template`, `spec.node_sets` (map of `{host_type, size}`)
- `status.state` — billable when `CLUSTER_STATE_READY` or `CLUSTER_STATE_PROGRESSING`

### InstanceType

| Field | Source |
|---|---|
| Proto definition | [instance_type_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/instance_type_type.proto) |
| Go type | [`internal/osac/types.go`](../inventory-watcher/internal/osac/types.go) → `InstanceType` |
| Handler | [`internal/watcher/watcher.go`](../inventory-watcher/internal/watcher/watcher.go) → direct `UpsertInstanceType` |
| Inventory table | `inventory_instance_type` |

**Key fields consumed:**
- `id`, `metadata.name`
- `spec.cores`, `spec.memory_gib`, `spec.state`

### Project

| Field | Source |
|---|---|
| Proto definition | [project_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/project_type.proto) |
| Go type | [`internal/osac/types.go`](../inventory-watcher/internal/osac/types.go) → `Project` |
| Handler | [`internal/watcher/watcher.go`](../inventory-watcher/internal/watcher/watcher.go) → `UpsertProject` |
| Inventory table | `inventory_project` |

**Key fields consumed:**
- `id`, `metadata.name`, `metadata.tenant`, `metadata.labels`

### Tenant

| Field | Source |
|---|---|
| Proto definition | [tenant_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/tenant_type.proto) |
| Go type | [`internal/osac/types.go`](../inventory-watcher/internal/osac/types.go) → `Tenant` |
| Handler | Logged but no inventory table (tenants tracked implicitly via resource ownership) |

## Messages Received but Not Processed

These appear in the Watch stream `oneof payload` but we only log them — no
inventory or metering action:

| Message | Proto | Reason |
|---|---|---|
| `HostType` | [host_type_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/host_type_type.proto) | Used for cluster node set specs; no direct metering |
| `ClusterTemplate` | [cluster_template_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/cluster_template_type.proto) | Reference data; no metering |
| `ComputeInstanceTemplate` | [compute_instance_template_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/compute_instance_template_type.proto) | Reference data; no metering |
| `Role` | [role_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/role_type.proto) | RBAC; not cost-relevant |
| `RoleBinding` | [role_binding_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/role_binding_type.proto) | RBAC; not cost-relevant |

## Common Metadata

All resources share a common metadata structure, defined in
[metadata_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/metadata_type.proto):

```protobuf
message Metadata {
  google.protobuf.Timestamp creation_timestamp = 1;
  google.protobuf.Timestamp deletion_timestamp = 2;
  string creator = 3;
  string name = 4;
  string tenant = 5;
  map<string, string> labels = 7;
  map<string, string> annotations = 8;
  int32 version = 9;
}
```

## OSAC REST API Endpoints Used for Reconciliation

| Method | Endpoint | Client method | Purpose |
|---|---|---|---|
| GET | `/api/fulfillment/v1/compute_instances` | [`ListComputeInstances`](../inventory-watcher/internal/osac/client.go) | Reconcile VM inventory |
| GET | `/api/fulfillment/v1/clusters` | [`ListClusters`](../inventory-watcher/internal/osac/client.go) | Reconcile cluster inventory |
| GET | `/api/fulfillment/v1/instance_types` | [`ListInstanceTypes`](../inventory-watcher/internal/osac/client.go) | Sync instance type catalog |
| GET | `/api/fulfillment/v1/projects` | [`ListProjects`](../inventory-watcher/internal/osac/client.go) | Sync project hierarchy |

## Messages Not Yet in OSAC (Mock Only)

| Resource | Status | Our handling |
|---|---|---|
| Model (MaaS) | No proto, no API, no Watch stream events | Mock via HTTP ingest endpoint; see [req2 gap analysis](req2-maas-costing-gap-analysis.md) |
| BareMetalInstance | [Proto exists](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/baremetal_instance_type.proto), not yet in Watch stream `oneof` | Not implemented |

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

## OSAC Resource Type Overview

Consolidated view of all resource types relevant to the PoC: what exists
on the OSAC side, how we consume it, and current processing status.

| Resource Type | PoC Req | Public Watch | Private Watch | REST List | Proto | Our Processing | Notes |
|---|---|---|---|---|---|---|---|
| **ComputeInstance** | REQ-1 | Yes | Yes | Yes | Public | **Inventory + 3 meters** | Fully operational |
| **Cluster** | REQ-1, REQ-1a | Yes | Yes | Yes | Public | **Inventory + 2 meters** | Fully operational |
| **InstanceType** | REQ-3b | Yes | Yes | Yes | Public | **Inventory** (ref data) | Cores/memory lookup for VMs |
| **Project** | REQ-3a | Yes | Yes | Yes | Public | **Inventory** | Tenant/project attribution |
| **Tenant** | REQ-3a | Yes | Yes | — | Public | **Logged only** | Tracked implicitly via string columns on other tables |
| **BareMetalInstance** | REQ-8 (parked) | **No** | Yes (field 27) | Yes | Public | **Inventory + 1 meter** | Consumed via REST List; hw specs via catalog_item only |
| **CatalogItem** (×3) | REQ-3b | **No** | Yes | Yes | Public | **Inventory** (REST poll) | cluster / compute / bare_metal variants |
| **HostType** | indirect | Yes | Yes | — | Public | Logged only | Reference data for cluster node set specs |
| **ClusterTemplate** | — | Yes | Yes | Yes | Public | Logged only | Reference data |
| **ComputeInstanceTemplate** | — | Yes | Yes | — | Public | Logged only | Reference data |
| **Role** | — | Yes | Yes | — | Public | Logged only | RBAC; deferred post-PoC |
| **RoleBinding** | — | Yes | Yes | — | Public | Logged only | RBAC; deferred post-PoC |
| **ClusterOrder** | — | **No** | ? | Yes | Public | **Not needed** | Ordering workflow, not a running resource; we track the resulting Cluster instead (resolved, see [open question #15](requirements/osac-open-questions.md)) |
| **BareMetalInstanceTemplate** | indirect | **No** | Yes | ? | Public? | **Not consumed** | Needed for BM hardware spec resolution |
| **StorageBackend** | — | **No** | Yes | ? | ? | Not consumed | Not in any PoC requirement |
| **Model (MaaS)** | REQ-2a, REQ-4 | **No** | **No** | **No** | **None** | **Inventory + 3 meters** (via HTTP ingest) | `model_name` from CloudEvent payload; no OSAC entity — see below |

### Model (MaaS) — No OSAC Entity

OSAC does not define a Model proto, API, or Watch stream event. We receive
model data via two HTTP ingest event types:
- `osac.model.lifecycle` — our mock/simulator format
- `inference.tokens.used` — the real IPP external-metering plugin format
  ([source](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/main/pkg/plugins/external-metering/plugin.go))

Both are handled by `handleModelEvent` in `internal/api/handler.go` and
produce the same 3 meters (`maas_tokens_in`, `maas_tokens_out`, `maas_requests`).
See [CloudEvents catalog](cloudevents-catalog.md) for the full schema comparison.
The IPP external-metering plugin is an alternative path that bypasses the
need for an OSAC Model entity entirely.

### Key Gaps

- **BareMetalInstance + CatalogItems**: proto and REST exist, but not in the
  public Watch stream. Available in the private stream (open question #6:
  is the cost consumer authorized to use it?). Currently works via REST polling.

---

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
| Handler | [`internal/watcher/watcher.go`](../inventory-watcher/internal/watcher/watcher.go) → inline `UpsertTenant` |
| Inventory table | `inventory_tenant` |

**Key fields consumed:**
- `id` — tenant UUID
- `metadata.name`, `metadata.labels`, `metadata.creation_timestamp`

### BareMetalInstance

| Field | Source |
|---|---|
| Proto definition | [baremetal_instance_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/baremetal_instance_type.proto) |
| Go type | [`internal/osac/types.go`](../inventory-watcher/internal/osac/types.go) → `BareMetalInstance` |
| Handler | [`internal/watcher/watcher.go`](../inventory-watcher/internal/watcher/watcher.go) → `upsertBareMetalInstance` |
| Inventory table | `inventory_bare_metal_instance` |
| Metering | `bm_uptime_seconds` |

**Key fields consumed:**
- `id`, `metadata.name`, `metadata.tenant`, `metadata.labels`
- `spec.catalog_item` — reference to BareMetalInstanceCatalogItem
- `status.state` — billable when `RUNNING` or `BARE_METAL_INSTANCE_STATE_RUNNING`

**Note:** Not in the public Watch stream `oneof` — handled via REST List
reconciliation. Available in the private Watch stream (field 27).

## Catalog Items (Reconciler Only)

Catalog items are synced via REST List polling (not in any Watch stream).
All three types share the same Go struct (`CatalogItem`).

| Type | REST Endpoint | Client Method |
|---|---|---|
| Cluster | `/api/fulfillment/v1/cluster_catalog_items` | `ListClusterCatalogItems` |
| ComputeInstance | `/api/fulfillment/v1/compute_instance_catalog_items` | `ListComputeInstanceCatalogItems` |
| BareMetalInstance | `/api/fulfillment/v1/baremetal_instance_catalog_items` | `ListBareMetalInstanceCatalogItems` |

Inventory table: `inventory_catalog_item` (with `item_type` column to distinguish).

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
| GET | `/api/fulfillment/v1/tenants` | [`ListTenants`](../inventory-watcher/internal/osac/client.go) | Sync tenant inventory |
| GET | `/api/fulfillment/v1/baremetal_instances` | [`ListBareMetalInstances`](../inventory-watcher/internal/osac/client.go) | Reconcile bare metal inventory |
| GET | `/api/fulfillment/v1/cluster_catalog_items` | [`ListClusterCatalogItems`](../inventory-watcher/internal/osac/client.go) | Sync cluster catalog items |
| GET | `/api/fulfillment/v1/compute_instance_catalog_items` | [`ListComputeInstanceCatalogItems`](../inventory-watcher/internal/osac/client.go) | Sync compute catalog items |
| GET | `/api/fulfillment/v1/baremetal_instance_catalog_items` | [`ListBareMetalInstanceCatalogItems`](../inventory-watcher/internal/osac/client.go) | Sync bare metal catalog items |

## Messages Not Yet in OSAC

| Resource | OSAC Status | Our Handling |
|---|---|---|
| Model (MaaS) | No proto, no API, no Watch stream events | `model_name` from CloudEvent payload → `inventory_model` + 3 meters (`maas_tokens_in`, `maas_tokens_out`, `maas_requests`). See [OSAC Resource Type Overview](#osac-resource-type-overview) and [req2 gap analysis](requirements/req2-maas-costing-gap-analysis.md) |

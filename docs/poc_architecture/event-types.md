# OSAC CloudEvents — Event Types Reference

> **Status:** POC draft — schemas may change as OSAC and Cost Management align on transport and format.
> **CloudEvents spec:** [cloudevents.io](https://cloudevents.io/) v1.0

---

## Overview

The OSAC fulfillment service emits CloudEvents for resource lifecycle changes, currently covering **state transitions** (resource created/updated/deleted → inventory sync).

The OSAC team built a separate [periodic metering collector](https://github.com/masayag/osac-metering-discover-poc) that emits the same CloudEvent types on a timer, pre-populated with `duration_seconds` and metering quantities. These are referred to in the requirements as "heartbeat events".

The Cost Management PoC currently runs a 60-second sweep to stand in for that collector. See [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md) for the full explanation.


### Event Types

| CloudEvent `type` | Resource | Billing Model | Schema Status |
|---|---|---|---|
| `osac.cluster.lifecycle` | Cluster (CaaS) | Capacity-based | Defined — implemented in PoC |
| `osac.compute_instance.lifecycle` | VM / ComputeInstance (VMaaS) | Capacity-based | Defined — implemented in PoC |
| `osac.model.lifecycle` | Model / Inference (MaaS) | Consumption-based | Proposed — awaiting OSAC confirmation (R-1, R-2) |
| `osac.bare_metal.lifecycle` | Bare Metal (BMaaS) | TBD | Placeholder — blocked on OSAC schema (R-3, R-4) |

### CloudEvent Envelope (all types)

All OSAC CloudEvents share this outer envelope (CloudEvents 1.0 structured format):

```json
{
  "specversion": "1.0",
  "type": "<event-type>",
  "source": "osac.metering.collector",
  "id": "<uuid-v4>",
  "time": "<ISO8601>",
  "subject": "<tenant_id>",
  "datacontenttype": "application/json",
  "data": { ... }
}
```

| Field | Description |
|---|---|
| `specversion` | Always `"1.0"` |
| `type` | Event type (see table above) |
| `source` | Originating OSAC component |
| `id` | Unique event ID (UUID v4) |
| `time` | Event timestamp (ISO 8601 UTC) |
| `subject` | `tenant_id` — used for per-tenant grouping |
| `data` | Resource-specific payload (see below) |

---

## Required from OSAC

The table below tracks the open items Cost Management needs from OSAC before the full event integration can be completed. CaaS and VMaaS are unblocked; MaaS, BMaaS, and transport are pending.

| # | Item | Needed for | Status | Owner |
|---|---|---|---|---|
| R-1 | **MaaS CloudEvent schema** — confirm or revise the proposed fields in §3 (`tokens_in`, `tokens_out`, `inference_tokens`, `requests`, `duration_seconds`) | REQ-2a, MaaS metering | Proposed (see §3); awaiting OSAC confirmation | OSAC |
| R-2 | **MaaS billable states** — define the `MODEL_STATE_*` state machine and which states are billable (analogous to `CLUSTER_STATE_READY` / `COMPUTE_INSTANCE_STATE_RUNNING`) | REQ-2a, MaaS metering | Not defined | OSAC |
| R-3 | **BMaaS CloudEvent schema** — confirm field names, types, and any GPU/disk/network additions beyond the placeholder in §4 | REQ-8, BMaaS metering | Placeholder only (see §4) | OSAC |
| R-4 | **BMaaS billable states** — define the `BARE_METAL_STATE_*` state machine and which states are billable | REQ-8, BMaaS metering | Not defined | OSAC |
| R-5 | **Heartbeat collector delivery** — connect the OSAC metering collector to Cost Management over HTTP or Kafka; agree on emission interval (requirements: 10–30s; existing collector: 60s). **Not a PoC blocker** — the local sweep covers this for the demo. Required for production (Phase 4). See [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md). | REQ-1b, POC-ARCH Phase 4 | **Not a PoC blocker.** Collector exists; production delivery TBD | OSAC |
| R-6 | **MaaS event source** — confirm whether OSAC or OpenShift AI 5 emits MaaS CloudEvents, and whether Cost consumes them directly or via OSAC | REQ-2a | Open question | OSAC + Cost |

---

## 1. CaaS — Cluster Lifecycle

**Event type:** `osac.cluster.lifecycle`

Emitted periodically (every ~60s) for each active cluster, once per component (control plane + one per worker node set).

### Billable States

| OSAC State | Billed? |
|---|---|
| `CLUSTER_STATE_READY` | Yes |
| `CLUSTER_STATE_PROGRESSING` | Yes (resources committed) |
| `CLUSTER_STATE_FAILED` | No |
| `CLUSTER_STATE_UNSPECIFIED` | No |

### Control Plane Event

```json
{
  "specversion": "1.0",
  "type": "osac.cluster.lifecycle",
  "source": "osac.metering.collector",
  "id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "time": "2026-06-25T17:00:00Z",
  "subject": "tenant-fake",
  "data": {
    "duration_seconds": 60,
    "worker_node_seconds": 0,
    "node_count": 0,
    "tenant_id": "tenant-fake",
    "cluster_id": "019ed08d-e3c5-733d-b93a-bb6179286ea3",
    "template": "osac.templates.ocp_ci_small",
    "state": "CLUSTER_STATE_READY",
    "host_type": "_control_plane"
  }
}
```

### Worker Node Set Event

One event per node set (differentiated by `host_type`):

```json
{
  "specversion": "1.0",
  "type": "osac.cluster.lifecycle",
  "source": "osac.metering.collector",
  "id": "b2c3d4e5-f6a7-8901-bcde-f12345678901",
  "time": "2026-06-25T17:00:00Z",
  "subject": "tenant-acme",
  "data": {
    "duration_seconds": 60,
    "worker_node_seconds": 120,
    "node_count": 2,
    "tenant_id": "tenant-acme",
    "cluster_id": "019ed08d-e3c5-733d-b93a-bb6179286ea3",
    "template": "osac.templates.ocp_ci_small",
    "state": "CLUSTER_STATE_READY",
    "host_type": "ci-worker"
  }
}
```

### CaaS Data Fields

| Field | Type | Description |
|---|---|---|
| `duration_seconds` | int | Elapsed seconds since last event (poll interval) |
| `worker_node_seconds` | int | `node_count × duration_seconds` (0 for control plane) |
| `node_count` | int | Number of nodes in this node set (0 for control plane) |
| `tenant_id` | string | Tenant identifier |
| `cluster_id` | string | Unique cluster UUID |
| `template` | string | Cluster template ID (e.g. `osac.templates.ocp_ci_small`) |
| `state` | string | OSAC cluster state |
| `host_type` | string | `_control_plane` or worker node set name |

### CaaS Meters

| Meter Name | Aggregation | Group By |
|---|---|---|
| `cluster_uptime_seconds` | `SUM(duration_seconds)` where `host_type=_control_plane` | `tenant_id`, `cluster_id` |
| `cluster_worker_node_seconds` | `SUM(worker_node_seconds)` | `tenant_id`, `host_type` |
| `cluster_worker_node_count` | `MAX(node_count)` | `tenant_id`, `cluster_id`, `host_type` |
| `cluster_count` | `COUNT(events)` | `tenant_id`, `template`, `state` |

---

## 2. VMaaS — Compute Instance Lifecycle

**Event type:** `osac.compute_instance.lifecycle`

Emitted periodically for each running VM (ComputeInstance).

### Billable States

| OSAC State | Billed? |
|---|---|
| `COMPUTE_INSTANCE_STATE_RUNNING` | Yes |
| `COMPUTE_INSTANCE_STATE_STOPPED` | No |
| `COMPUTE_INSTANCE_STATE_DELETED` | No |

### Event Example

```json
{
  "specversion": "1.0",
  "type": "osac.compute_instance.lifecycle",
  "source": "osac.metering.collector",
  "id": "c3d4e5f6-a7b8-9012-cdef-012345678902",
  "time": "2026-06-25T17:00:00Z",
  "subject": "tenant-acme",
  "data": {
    "duration_seconds": 60,
    "cpu_core_seconds": 120,
    "memory_gib_seconds": 240,
    "tenant_id": "tenant-acme",
    "instance_id": "019eb257-8108-773f-99c4-5d7642e9e7d8",
    "template": "osac.templates.ocp_virt_vm",
    "catalog_item": "",
    "state": "COMPUTE_INSTANCE_STATE_RUNNING",
    "cores": 2,
    "memory_gib": 4
  }
}
```

### VMaaS Data Fields

| Field | Type | Description |
|---|---|---|
| `duration_seconds` | int | Elapsed seconds since last event |
| `cpu_core_seconds` | int | `cores × duration_seconds` |
| `memory_gib_seconds` | int | `memory_gib × duration_seconds` |
| `tenant_id` | string | Tenant identifier |
| `instance_id` | string | Unique VM UUID |
| `template` | string | VM template ID |
| `catalog_item` | string | Catalog item reference (if applicable) |
| `state` | string | OSAC compute instance state |
| `cores` | int | Number of vCPUs allocated |
| `memory_gib` | int | Memory allocated in GiB |

### VMaaS Meters

| Meter Name | Aggregation | Group By |
|---|---|---|
| `vm_uptime_seconds` | `SUM(duration_seconds)` | `tenant_id`, `instance_id` |
| `vm_cpu_core_seconds` | `SUM(cpu_core_seconds)` | `tenant_id`, `template` |
| `vm_memory_gib_seconds` | `SUM(memory_gib_seconds)` | `tenant_id`, `template` |
| `vm_count` | `COUNT(events)` | `tenant_id`, `template` |

---

## 3. MaaS — Model Lifecycle

**Event type:** `osac.model.lifecycle`

> **Status: Not yet defined by OSAC.** The schema below is a proposal based on expected RHOAI (OpenShift AI) metrics. To be confirmed.

### Event Example (proposed)

```json
{
  "specversion": "1.0",
  "type": "osac.model.lifecycle",
  "source": "osac.metering.collector",
  "id": "d4e5f6a7-b8c9-0123-def0-123456789003",
  "time": "2026-06-25T17:00:00Z",
  "subject": "tenant-acme",
  "data": {
    "tenant_id": "tenant-acme",
    "model_id": "019ec123-abcd-1234-abcd-ef5678901234",
    "model_name": "llama-3-8b",
    "template": "osac.templates.maas_small",
    "state": "MODEL_STATE_RUNNING",
    "tokens_in": 15000,
    "tokens_out": 8000,
    "inference_tokens": 23000,
    "requests": 42,
    "duration_seconds": 60
  }
}
```

### MaaS Data Fields (proposed)

| Field | Type | Description |
|---|---|---|
| `tenant_id` | string | Tenant identifier |
| `model_id` | string | Unique model deployment UUID |
| `model_name` | string | Model identifier (e.g. `llama-3-8b`) |
| `template` | string | MaaS template ID |
| `state` | string | Model deployment state |
| `tokens_in` | int | Input tokens processed in this interval |
| `tokens_out` | int | Output tokens generated in this interval |
| `inference_tokens` | int | Total inference tokens (in + out) |
| `requests` | int | Number of inference requests |
| `duration_seconds` | int | Elapsed seconds since last event |

### MaaS Meters (proposed)

| Meter Name | Aggregation | Group By |
|---|---|---|
| `maas_tokens_in` | `SUM(tokens_in)` | `tenant_id`, `model_name` |
| `maas_tokens_out` | `SUM(tokens_out)` | `tenant_id`, `model_name` |
| `maas_requests` | `SUM(requests)` | `tenant_id`, `model_name` |

---

## 4. BMaaS — Bare Metal Lifecycle

**Event type:** `osac.bare_metal.lifecycle`

> **Status: Not yet defined by OSAC.** Bare metal metering requirements are still being scoped.

### Expected Fields (TBD)

| Field | Type | Description |
|---|---|---|
| `tenant_id` | string | Tenant identifier |
| `instance_id` | string | Bare metal host UUID |
| `template` | string | BMaaS template (hardware profile) |
| `state` | string | Provisioning state |
| `duration_seconds` | int | Elapsed seconds since last event |
| `cpu_cores` | int | Physical core count |
| `memory_gib` | int | RAM in GiB |
| `disk_gib` | int | Total disk in GiB |

---

## 5. Proposed Kafka Topic Schema

> Topic naming and format are TBD — to be agreed between OSAC and Cost Management teams.

### Proposed Topics

| Topic | Event Type | Partitioned By |
|---|---|---|
| `osac.events.caas` | `osac.cluster.lifecycle` | `tenant_id` |
| `osac.events.vmaas` | `osac.compute_instance.lifecycle` | `tenant_id` |
| `osac.events.maas` | `osac.model.lifecycle` | `tenant_id` |
| `osac.events.bmaas` | `osac.bare_metal.lifecycle` | `tenant_id` |
| `osac.alerts.quota` | Cost → OSAC quota alerts | `tenant_id` |

### Message Format

Messages are CloudEvents in structured JSON format (i.e. the full CloudEvent envelope as the Kafka message value). The Kafka message key is the `tenant_id` to ensure per-tenant ordering.

```
key:   <tenant_id>
value: <CloudEvent JSON>
headers:
  ce-specversion: 1.0
  ce-type: osac.cluster.lifecycle
  ce-source: osac.metering.collector
  ce-id: <uuid>
  ce-time: <ISO8601>
```

---

## 6. OSAC REST API Reference

For REST polling (Option B) or inventory sync, the OSAC REST gateway exposes:

| Method | Endpoint | Description |
|---|---|---|
| `GET` | `/api/fulfillment/v1/cluster_templates` | List cluster templates |
| `GET` | `/api/fulfillment/v1/cluster_templates/{id}` | Get template by ID |
| `GET` | `/api/fulfillment/v1/clusters` | List clusters |
| `GET` | `/api/fulfillment/v1/clusters/{id}` | Get cluster by ID |
| `POST` | `/api/fulfillment/v1/clusters` | Create cluster |
| `DELETE` | `/api/fulfillment/v1/clusters/{id}` | Delete cluster |
| `GET` | `/api/fulfillment/v1/cluster_orders` | List cluster orders |

All endpoints require a `Authorization: Bearer <token>` header. See [local dev setup](../dev/local-dev-setup.md) for how to mint a local token.

### Cluster Response Shape (example)

```json
{
  "id": "019ed08d-e3c5-733d-b93a-bb6179286ea3",
  "metadata": {
    "name": "my-ocp-cluster"
  },
  "spec": {
    "template_id": "osac.templates.ocp_ci_small"
  },
  "status": {
    "state": "CLUSTER_STATE_READY",
    "node_sets": [
      {
        "host_type": "ci-worker",
        "node_count": 2
      }
    ]
  }
}
```

---

## 7. Current Collector Approach vs. Production Target

| | Current POC Collector | Production Target |
|---|---|---|
| Trigger | Timer (every 60s) | Controller state transition |
| Precision | ±60s granularity | Event-driven, sub-second |
| Missed events | Yes (between polls) | No |
| Transport | Direct HTTP to OpenMeter | Kafka → OpenMeter Sink |
| Implementation | Shell script | Native Go in OSAC controller |

The existing collector scripts (`collect-caas.sh`, `collect.sh`) can be used as a reference for the REST polling approach (Option B) in the POC. See: [osac-metering-discover-poc](https://github.com/masayag/osac-metering-discover-poc).

---

## 8. References

- [CloudEvents Specification v1.0](https://cloudevents.io/)
- [OSAC CaaS Metering README](https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README-caas.md)
- [OSAC VMaaS Metering README](https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README.md)
- [ADR-003: Heartbeat Events vs. Local Sweep](../decisions/003-heartbeat-emitter-vs-sweep.md) — what heartbeat events are, how the PoC works around them, and what OSAC must deliver for production
- [docs/poc_architecture/architecture.md](architecture.md)

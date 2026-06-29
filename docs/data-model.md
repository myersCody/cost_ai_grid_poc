# Data Model

## Overview

The inventory-watcher uses PostgreSQL (port 5434) with 11 tables organized
into two groups:

1. **Inventory & Events** — raw event log and current state of OSAC resources
2. **Metering, Rating & Quotas** — usage tracking, pricing, cost computation,
   and quota enforcement

**Schema source:** [`inventory-watcher/internal/inventory/store.go`](../inventory-watcher/internal/inventory/store.go)
**Go models:** [`inventory-watcher/internal/inventory/models.go`](../inventory-watcher/internal/inventory/models.go)
**Rating logic:** [`inventory-watcher/internal/rating/rating.go`](../inventory-watcher/internal/rating/rating.go)

## ERD: Inventory & Events

![Inventory & Events ERD](diagrams/erd-inventory.svg)

*Source: [`docs/diagrams/erd-inventory.dot`](diagrams/erd-inventory.dot)*

### Tables

| Table | Go Model | Purpose |
|---|---|---|
| `raw_events` | [`RawEvent`](../inventory-watcher/internal/inventory/models.go) | Immutable log of every Watch stream / ingest event. Deduplicated on `event_id`. |
| `inventory_project` | [`ProjectRecord`](../inventory-watcher/internal/inventory/models.go) | OSAC projects (Tenant → Project hierarchy). |
| `inventory_compute_instance` | [`ComputeInstanceRecord`](../inventory-watcher/internal/inventory/models.go) | VMs tracked from OSAC. `last_metered_at` for duration-based metering. |
| `inventory_cluster` | [`ClusterRecord`](../inventory-watcher/internal/inventory/models.go) | Clusters with `node_sets` JSONB for worker node tracking. |
| `inventory_model` | [`ModelRecord`](../inventory-watcher/internal/inventory/models.go) | MaaS model deployments (mock — OSAC doesn't have this yet). |
| `inventory_instance_type` | [`InstanceTypeRecord`](../inventory-watcher/internal/inventory/models.go) | Instance type catalog (cores, memory) synced from OSAC. |

### Relationships

- **Tenant** is a string field on all inventory tables (not a separate table —
  tenants are tracked implicitly via resource ownership)
- **Project** links to resources via the `tenant` field (same tenant scope)
- **InstanceType** is referenced by `inventory_compute_instance.instance_type`
- **raw_events** feeds all inventory tables via the watcher/ingest pipeline

## ERD: Metering, Rating & Quotas

![Metering, Rating & Quotas ERD](diagrams/erd-metering-cost.svg)

*Source: [`docs/diagrams/erd-metering-cost.dot`](diagrams/erd-metering-cost.dot)*

### Tables

| Table | Go Model | Purpose |
|---|---|---|
| `metering_entries` | [`MeteringEntry`](../inventory-watcher/internal/inventory/models.go) | Per-meter-per-interval usage records. Produced by the 60s metering sweep (VMs/clusters) or on event arrival (MaaS). |
| `rates` | [`RateRecord`](../inventory-watcher/internal/inventory/models.go) | Pricing definitions. Flat rate or tiered pricing via `tiers` JSONB. Tenant-specific overrides supported. |
| `cost_entries` | [`CostEntry`](../inventory-watcher/internal/inventory/models.go) | `metering × rate = cost`. Produced by the 30s rating sweep. |
| `quotas` | [`QuotaRecord`](../inventory-watcher/internal/inventory/models.go) | Resource limits per tenant per meter per period. Consumed via the quota status API. |

### Data Flow

```
raw_events
  → inventory tables (upsert state)
  → metering_entries (via metering sweep or event-driven)
      → cost_entries (via rating sweep: metering × rate)
      → quotas (SUM compared against limit via API)
```

### Metering Entries

Produced by [`internal/metering/metering.go`](../inventory-watcher/internal/metering/metering.go):

**Capacity-based (60s sweep):**

| meter_name | resource_type | unit | formula |
|---|---|---|---|
| `vm_uptime_seconds` | compute_instance | seconds | duration |
| `vm_cpu_core_seconds` | compute_instance | core_seconds | cores × duration |
| `vm_memory_gib_seconds` | compute_instance | gib_seconds | memory_gib × duration |
| `cluster_uptime_seconds` | cluster | seconds | duration |
| `cluster_worker_node_seconds` | cluster | node_seconds | Σ(node_set_size × duration) |

**Consumption-based (event-driven):**

| meter_name | resource_type | unit | source |
|---|---|---|---|
| `maas_tokens_in` | model | tokens | event.tokens_in |
| `maas_tokens_out` | model | tokens | event.tokens_out |
| `maas_requests` | model | requests | event.requests |

### Tiered Pricing

The `rates.tiers` JSONB column supports tiered pricing
([`Tier`](../inventory-watcher/internal/inventory/models.go) struct,
applied in [`rating.go`](../inventory-watcher/internal/rating/rating.go) → `applyTieredRate`):

```json
[
  {"up_to": 20,   "price_per_unit": 0.00},
  {"up_to": 120,  "price_per_unit": 0.08},
  {"up_to": null, "price_per_unit": 0.07}
]
```

Algorithm: iterate tiers, consume units at each tier's price until value
exhausted. `up_to: null` means "everything above the previous tier."

### Supplementary Table

| Table | Go Model | Purpose |
|---|---|---|
| `daily_usage_summary` | [`DailyUsageSummary`](../inventory-watcher/internal/inventory/models.go) | Legacy daily aggregates from the original summarizer. Superseded by `metering_entries` + `cost_entries`. |

## Rebuilding the Diagrams

```bash
dot -Tsvg docs/diagrams/erd-inventory.dot -o docs/diagrams/erd-inventory.svg
dot -Tsvg docs/diagrams/erd-metering-cost.dot -o docs/diagrams/erd-metering-cost.svg
```

Requires `graphviz` (`brew install graphviz`).

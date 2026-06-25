# Cost Management for AI Grid — Data Model

> **Status:** POC draft — schema will evolve as CloudEvent formats are finalized with OSAC.

---

## Overview

The POC PostgreSQL database (port `5434`) stores:
1. **Raw events** — immutable log of every CloudEvent received
2. **Inventory** — current state of OSAC resources (clusters, VMs, models, bare metal)
3. **Metering entries** — calculated usage increments per resource per interval
4. **Cost entries** — metering × rate = cost, per resource per period
5. **Quotas / budgets** — provider-defined limits per tenant/project
6. **Alerts** — threshold breach records

---

## Entity Relationship Diagram

```mermaid
erDiagram
    tenants {
        uuid id PK
        string external_id
        string name
        timestamp created_at
        timestamp updated_at
    }

    projects {
        uuid id PK
        uuid tenant_id FK
        string external_id
        string name
        timestamp created_at
    }

    cluster_templates {
        uuid id PK
        string external_id
        string title
        string description
        jsonb spec
        timestamp synced_at
    }

    clusters {
        uuid id PK
        uuid tenant_id FK
        uuid project_id FK
        string external_id
        string name
        string template_id
        string state
        jsonb node_sets
        timestamp state_changed_at
        timestamp synced_at
    }

    compute_instances {
        uuid id PK
        uuid tenant_id FK
        uuid project_id FK
        string external_id
        string template
        string state
        int cores
        int memory_gib
        timestamp state_changed_at
        timestamp synced_at
    }

    models {
        uuid id PK
        uuid tenant_id FK
        uuid project_id FK
        string external_id
        string model_name
        string template
        string state
        timestamp state_changed_at
        timestamp synced_at
    }

    raw_events {
        uuid id PK
        string ce_id
        string ce_type
        string ce_source
        string ce_subject
        timestamp ce_time
        jsonb ce_data
        string resource_type
        string resource_id
        string tenant_id
        timestamp received_at
    }

    metering_entries {
        uuid id PK
        uuid raw_event_id FK
        string resource_type
        string resource_id
        uuid tenant_id FK
        string meter_name
        numeric value
        string unit
        timestamp period_start
        timestamp period_end
    }

    rates {
        uuid id PK
        uuid tenant_id FK
        string resource_type
        string meter_name
        numeric price_per_unit
        string currency
        jsonb tiers
        timestamp effective_from
        timestamp effective_to
    }

    cost_entries {
        uuid id PK
        uuid metering_entry_id FK
        uuid rate_id FK
        uuid tenant_id FK
        string resource_type
        string resource_id
        numeric metered_value
        numeric cost_amount
        string currency
        timestamp period_start
        timestamp period_end
    }

    quotas {
        uuid id PK
        uuid tenant_id FK
        uuid project_id FK
        string resource_type
        string meter_name
        numeric limit_value
        string unit
        string period
        timestamp effective_from
        timestamp effective_to
    }

    alerts {
        uuid id PK
        uuid quota_id FK
        uuid tenant_id FK
        numeric threshold_pct
        numeric consumed_value
        numeric limit_value
        string state
        timestamp triggered_at
        timestamp acknowledged_at
    }

    tenants ||--o{ projects : "has"
    tenants ||--o{ clusters : "owns"
    tenants ||--o{ compute_instances : "owns"
    tenants ||--o{ models : "owns"
    tenants ||--o{ metering_entries : "billed to"
    tenants ||--o{ cost_entries : "charged to"
    tenants ||--o{ quotas : "subject to"
    tenants ||--o{ alerts : "notified"
    projects ||--o{ clusters : "contains"
    projects ||--o{ compute_instances : "contains"
    projects ||--o{ models : "contains"
    raw_events ||--o{ metering_entries : "produces"
    metering_entries ||--o{ cost_entries : "rated by"
    quotas ||--o{ alerts : "triggers"
```

---

## Table Definitions

### `tenants`

Tenant registry, synced from OSAC.

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | Internal UUID |
| `external_id` | `TEXT` UNIQUE | OSAC tenant ID |
| `name` | `TEXT` | Human-readable name |
| `created_at` | `TIMESTAMPTZ` | |
| `updated_at` | `TIMESTAMPTZ` | |

---

### `projects`

Sub-divisions within a tenant (OSAC concept; Koku does not have this today).

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | Internal UUID |
| `tenant_id` | `UUID` FK → `tenants` | Parent tenant |
| `external_id` | `TEXT` UNIQUE | OSAC project ID |
| `name` | `TEXT` | |
| `created_at` | `TIMESTAMPTZ` | |

---

### `cluster_templates`

Synced from `GET /api/fulfillment/v1/cluster_templates`. Defines available cluster flavors.

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | Internal UUID |
| `external_id` | `TEXT` UNIQUE | OSAC template ID (e.g. `osac.templates.ocp_ci_small`) |
| `title` | `TEXT` | Display name |
| `description` | `TEXT` | |
| `spec` | `JSONB` | Full template spec from OSAC |
| `synced_at` | `TIMESTAMPTZ` | Last sync from OSAC |

---

### `clusters`

Current state of each cluster, derived from inventory sync and event processing.

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | Internal UUID |
| `tenant_id` | `UUID` FK → `tenants` | |
| `project_id` | `UUID` FK → `projects` | Nullable — project may not be set |
| `external_id` | `TEXT` UNIQUE | OSAC cluster UUID |
| `name` | `TEXT` | |
| `template_id` | `TEXT` | Reference to `cluster_templates.external_id` |
| `state` | `TEXT` | Latest OSAC state (e.g. `CLUSTER_STATE_READY`) |
| `node_sets` | `JSONB` | Array of `{host_type, node_count}` |
| `state_changed_at` | `TIMESTAMPTZ` | When state last changed |
| `synced_at` | `TIMESTAMPTZ` | Last inventory sync |

---

### `compute_instances`

Current state of each VM (VMaaS).

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | Internal UUID |
| `tenant_id` | `UUID` FK → `tenants` | |
| `project_id` | `UUID` FK → `projects` | |
| `external_id` | `TEXT` UNIQUE | OSAC instance UUID |
| `template` | `TEXT` | VM template ID |
| `state` | `TEXT` | Latest OSAC state |
| `cores` | `INT` | vCPU count |
| `memory_gib` | `INT` | RAM in GiB |
| `state_changed_at` | `TIMESTAMPTZ` | |
| `synced_at` | `TIMESTAMPTZ` | |

---

### `models`

Current state of each model deployment (MaaS).

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | Internal UUID |
| `tenant_id` | `UUID` FK → `tenants` | |
| `project_id` | `UUID` FK → `projects` | |
| `external_id` | `TEXT` UNIQUE | OSAC model UUID |
| `model_name` | `TEXT` | Model identifier (e.g. `llama-3-8b`) |
| `template` | `TEXT` | MaaS template ID |
| `state` | `TEXT` | Model deployment state |
| `state_changed_at` | `TIMESTAMPTZ` | |
| `synced_at` | `TIMESTAMPTZ` | |

---

### `raw_events`

Immutable log of every CloudEvent received. Never updated after insert.

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | Internal UUID |
| `ce_id` | `TEXT` | CloudEvent `id` field (deduplicate on this) |
| `ce_type` | `TEXT` | CloudEvent `type` (e.g. `osac.cluster.lifecycle`) |
| `ce_source` | `TEXT` | CloudEvent `source` |
| `ce_subject` | `TEXT` | CloudEvent `subject` (tenant_id) |
| `ce_time` | `TIMESTAMPTZ` | CloudEvent `time` |
| `ce_data` | `JSONB` | CloudEvent `data` payload |
| `resource_type` | `TEXT` | Derived: `cluster`, `compute_instance`, `model`, `bare_metal` |
| `resource_id` | `TEXT` | Derived: resource UUID from `ce_data` |
| `tenant_id` | `TEXT` | Derived: from `ce_subject` / `ce_data.tenant_id` |
| `received_at` | `TIMESTAMPTZ` | Wall clock time when event was received |

**Index:** `UNIQUE(ce_id)` for deduplication.
**Partition:** Consider range partitioning by `ce_time` (monthly) for long-term retention.

---

### `metering_entries`

Calculated usage increments derived from raw events. One row per meter per event.

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | |
| `raw_event_id` | `UUID` FK → `raw_events` | Source event |
| `resource_type` | `TEXT` | `cluster`, `compute_instance`, `model`, `bare_metal` |
| `resource_id` | `TEXT` | Resource UUID |
| `tenant_id` | `UUID` FK → `tenants` | |
| `meter_name` | `TEXT` | e.g. `cluster_uptime_seconds`, `vm_cpu_core_seconds` |
| `value` | `NUMERIC` | Metered quantity |
| `unit` | `TEXT` | e.g. `seconds`, `core_seconds`, `gib_seconds`, `tokens` |
| `period_start` | `TIMESTAMPTZ` | `ce_time - duration_seconds` |
| `period_end` | `TIMESTAMPTZ` | `ce_time` |

---

### `rates`

Provider-defined rates for billing. Supports flat and tiered pricing.

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | |
| `tenant_id` | `UUID` FK → `tenants` | Nullable — null means global default |
| `resource_type` | `TEXT` | `cluster`, `compute_instance`, `model`, `bare_metal` |
| `meter_name` | `TEXT` | Meter this rate applies to |
| `price_per_unit` | `NUMERIC` | Flat rate price per unit |
| `currency` | `TEXT` | ISO 4217 (e.g. `USD`) |
| `tiers` | `JSONB` | Tiered pricing: `[{up_to, price_per_unit}, ...]` |
| `effective_from` | `TIMESTAMPTZ` | Rate validity start |
| `effective_to` | `TIMESTAMPTZ` | Rate validity end (null = no end) |

**Tiered pricing example:**
```json
[
  {"up_to": 20,   "price_per_unit": 0.00},
  {"up_to": 120,  "price_per_unit": 0.08},
  {"up_to": null, "price_per_unit": 0.07}
]
```

---

### `cost_entries`

Monetary cost derived by applying a rate to a metering entry.

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | |
| `metering_entry_id` | `UUID` FK → `metering_entries` | |
| `rate_id` | `UUID` FK → `rates` | Rate applied |
| `tenant_id` | `UUID` FK → `tenants` | |
| `resource_type` | `TEXT` | |
| `resource_id` | `TEXT` | |
| `metered_value` | `NUMERIC` | Quantity billed |
| `cost_amount` | `NUMERIC` | `metered_value × rate` |
| `currency` | `TEXT` | ISO 4217 |
| `period_start` | `TIMESTAMPTZ` | |
| `period_end` | `TIMESTAMPTZ` | |

---

### `quotas`

Provider-defined resource limits per tenant (and optionally per project).

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | |
| `tenant_id` | `UUID` FK → `tenants` | |
| `project_id` | `UUID` FK → `projects` | Nullable — tenant-level quota if null |
| `resource_type` | `TEXT` | e.g. `cluster`, `model` |
| `meter_name` | `TEXT` | Which meter this quota constrains |
| `limit_value` | `NUMERIC` | Maximum allowed quantity |
| `unit` | `TEXT` | Unit of the limit |
| `period` | `TEXT` | `daily`, `monthly`, `yearly` |
| `effective_from` | `TIMESTAMPTZ` | |
| `effective_to` | `TIMESTAMPTZ` | Nullable |

---

### `alerts`

Records of quota threshold breaches. Emitted back to OSAC via CloudEvent.

| Column | Type | Description |
|---|---|---|
| `id` | `UUID` PK | |
| `quota_id` | `UUID` FK → `quotas` | Which quota was breached |
| `tenant_id` | `UUID` FK → `tenants` | |
| `threshold_pct` | `NUMERIC` | Trigger threshold (e.g. `70.0`, `90.0`, `100.0`) |
| `consumed_value` | `NUMERIC` | Actual consumption at trigger time |
| `limit_value` | `NUMERIC` | Quota limit at trigger time |
| `state` | `TEXT` | `firing`, `acknowledged`, `resolved` |
| `triggered_at` | `TIMESTAMPTZ` | When alert was first generated |
| `acknowledged_at` | `TIMESTAMPTZ` | Nullable |

---

## Data Flow Through the Model

```
CloudEvent received
  │
  ├─► raw_events (insert, deduplicate on ce_id)
  │
  ├─► clusters / compute_instances / models (upsert state)
  │
  ├─► metering_entries (insert per meter per event)
  │         │
  │         └─► cost_entries (insert after rate lookup)
  │
  └─► quotas (query accumulated metering for tenant)
            │
            └─► alerts (insert if threshold crossed → emit CloudEvent)
```

---

## Indexes (Key)

```sql
-- Deduplication
CREATE UNIQUE INDEX ON raw_events (ce_id);

-- Event queries by tenant / time
CREATE INDEX ON raw_events (tenant_id, ce_time DESC);
CREATE INDEX ON raw_events (ce_type, ce_time DESC);

-- Metering aggregation
CREATE INDEX ON metering_entries (tenant_id, meter_name, period_start, period_end);
CREATE INDEX ON metering_entries (resource_id, meter_name);

-- Cost reporting
CREATE INDEX ON cost_entries (tenant_id, period_start, period_end);

-- Quota evaluation
CREATE INDEX ON quotas (tenant_id, resource_type, meter_name);
```

---

## References

- [Docs/architecture.md](architecture.md)
- [Docs/event-types.md](event-types.md)
- [CloudEvents Spec](https://cloudevents.io/)
- COST-6951 — Cost Tiers
- COST-7164 — MaaS Spike

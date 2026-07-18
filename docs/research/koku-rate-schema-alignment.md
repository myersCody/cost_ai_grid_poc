# Koku Rate Schema Alignment

> Planning for eventual merge of our rate definitions with Koku's cost model.

## Koku's Rate Model

Koku uses a three-level hierarchy:

```
cost_model (name, source_type, currency, markup, distribution)
  └── cost_model_rate (metric, metric_type, cost_type, default_rate, tag_key/values)
        └── cost_model_map (links cost model to a source/cluster)
```

Rates are also stored as a `rates` JSONB array on `cost_model`:

```json
{
  "rates": [
    {
      "metric": {"name": "cpu_core_request_per_hour"},
      "tiered_rates": [
        {"unit": "USD", "value": 0.2, "usage_start": null, "usage_end": null}
      ],
      "cost_type": "Supplementary"
    }
  ]
}
```

### Koku's On-Prem Cost Model Rates

From `openshift_on_prem_cost_model.json`:

| Metric | Rate | Cost Type | Our Meter |
|---|---|---|---|
| `cpu_core_request_per_hour` | $0.20 | Supplementary | `vm_cpu_core_seconds` (÷3600) |
| `memory_gb_request_per_hour` | $0.05 | Supplementary | `vm_memory_gib_seconds` (÷3600) |
| `node_cost_per_month` | $1000 | Infrastructure | `vm_uptime_seconds` (÷2592000) |
| `cluster_cost_per_month` | $10000 | Infrastructure | `cluster_uptime_seconds` (÷2592000) |
| `cluster_cost_per_hour` | $3.45 | Infrastructure | `cluster_uptime_seconds` (÷3600) |
| `vm_cost_per_month` | $0.50 | Infrastructure | `vm_uptime_seconds` (÷2592000) |
| `vm_cost_per_hour` | $0.007 | Supplementary | `vm_uptime_seconds` (÷3600) |
| `cpu_core_usage_per_hour` | $0.007 | Supplementary | Not feasible (needs Prometheus) |
| `memory_gb_usage_per_hour` | $0.009 | Supplementary | Not feasible (needs Prometheus) |
| `storage_gb_*_per_month` | $0.01 | Supplementary | Not feasible (no PVC in OSAC) |

## Schema Comparison

| Concept | Koku (`cost_model_rate`) | Ours (`rates`) | Alignment needed? |
|---|---|---|---|
| Primary key | `uuid` | `id` BIGSERIAL | No — internal |
| Metric name | `metric` VARCHAR | `meter_name` TEXT | **Done** — `koku_metric` column added |
| Metric type | `metric_type` VARCHAR | `resource_type` TEXT | Compatible |
| Cost type | `cost_type` VARCHAR | `cost_type` TEXT | **Done** |
| Rate value | `default_rate` NUMERIC | `price_per_unit` NUMERIC | Compatible |
| Tiered pricing | `tiered_rates` JSONB (on cost_model) | `tiers` JSONB | Different format — see below |
| Currency | on parent `cost_model` | `currency` TEXT | Compatible |
| Tenant scoping | via `cost_model_map` | `tenant_id` TEXT | Different approach |
| Tag-based rates | `tag_key`, `tag_values` | — | **Add later** if needed |
| Effective dates | via `price_list` | `effective_from`/`to` | Compatible |
| Markup | separate `markup` JSONB | — | **Add later** if needed |
| Custom name | `custom_name` VARCHAR | — | Optional |
| Description | `description` TEXT | `description` TEXT | **Done** |

## Tiered Pricing Format

**Koku:**
```json
"tiered_rates": [
  {"unit": "USD", "value": 0.00, "usage_start": null, "usage_end": 20},
  {"unit": "USD", "value": 0.08, "usage_start": 20, "usage_end": 120},
  {"unit": "USD", "value": 0.07, "usage_start": 120, "usage_end": null}
]
```

**Ours:**
```json
"tiers": [
  {"up_to": 20,   "price_per_unit": 0.00},
  {"up_to": 120,  "price_per_unit": 0.08},
  {"up_to": null, "price_per_unit": 0.07}
]
```

Same concept, different field names. Our format is simpler (no
`usage_start` since it's implied by the previous tier's `up_to`).

## Unit Conversion

Koku metrics are per-hour or per-month. Our meters are per-second.

| Koku unit | Conversion | Our unit |
|---|---|---|
| per_hour | ÷ 3600 | per_second |
| per_month | ÷ 2,592,000 (30 days) | per_second |

This conversion happens at rate sync time, not at query time.

## Recommended Schema Changes

For eventual merge compatibility, add to our `rates` table:

```sql
ALTER TABLE rates ADD COLUMN cost_type TEXT NOT NULL DEFAULT 'Infrastructure';
ALTER TABLE rates ADD COLUMN koku_metric TEXT;
ALTER TABLE rates ADD COLUMN description TEXT NOT NULL DEFAULT '';
```

- `cost_type` — enables Koku's Infrastructure/Supplementary split in reports
- `koku_metric` — maps our `meter_name` to Koku's `metric` name for sync
- `description` — matches Koku's field

## Rate Sync Flow

```
Koku DB (org1234567.cost_model + cost_model_rate)
    │
    │  read rates, convert units (per-hour → per-second)
    │
    ▼
Our rates table
    │
    │  meter_name = our name
    │  koku_metric = Koku's metric name
    │  cost_type = Infrastructure or Supplementary
    │  price_per_unit = Koku rate × conversion factor
    │
    ▼
Rating sweep uses our rates table (no change to existing logic)
```

The sync is a one-time copy with unit conversion. If Koku rates change,
re-sync. No real-time sync needed for PoC.

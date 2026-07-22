# Metric Calculation Reference

Complete reference for how raw resource state becomes dollar cost.
Covers every meter, the catalog fallback, rate matching, tiered
pricing, and worked examples.

**Code:**
- Metering: [`internal/metering/metering.go`](../inventory-watcher/internal/metering/metering.go)
- Rating: [`internal/rating/rating.go`](../inventory-watcher/internal/rating/rating.go)

**See also:** [Rate Configuration Guide](rate-configuration-guide.md) —
how to configure pricing models, per-SKU rates, and tenant overrides.

## Pipeline Overview

![Metric Calculation Pipeline](diagrams/metric-calculation-overview.svg)

*Source: [`docs/diagrams/metric-calculation-overview.dot`](diagrams/metric-calculation-overview.dot)*

## Capacity-Based Meters (60s sweep)

The metering sweep runs every 60 seconds. For each billable resource,
it calculates `duration = now - last_metered_at` and emits entries.

### Compute Instance (VM)

**Source table:** `inventory_compute_instance` (state = RUNNING)

| Meter | Formula | Unit | Notes |
|-------|---------|------|-------|
| `vm_uptime_seconds` | duration | seconds | Always emitted; **primary billing meter** |
| `vm_cpu_core_seconds` | cores × duration | core_seconds | 0 if cores unknown; capacity reporting only |
| `vm_memory_gib_seconds` | memory_gib × duration | gib_seconds | 0 if memory unknown; capacity reporting only |

**OSAC pricing model:** OSAC is removing `cores`/`memory_gib` from
`ComputeInstance` events. Cost is determined by the `instance_type`
field — each instance type gets its own rate on `vm_uptime_seconds`.
The CPU/memory meters are emitted for capacity dashboards but should
have $0 rates when using instance-type pricing. See
[Rate Configuration Guide — Option 1](rate-configuration-guide.md#option-1-flat-rate-per-instance-type-recommended).

**Catalog fallback (optional):** when `cores = 0` and `instance_type`
is set, the sweep looks up `inventory_instance_type` to resolve cores
and memory_gib. This populates the CPU/memory meters for capacity
reporting. If the instance type isn't in the catalog, these meters
produce 0 — this has no billing impact when using instance-type
pricing.

```
VM "vm-001" (instance_type: "m5.xlarge", cores: 0, memory_gib: 0)
  └─► catalog lookup: m5.xlarge → cores: 4, memory_gib: 16
  └─► last_metered_at: 60s ago
  └─► entries:
        vm_uptime_seconds      = 60.0
        vm_cpu_core_seconds    = 4 × 60 = 240.0
        vm_memory_gib_seconds  = 16 × 60 = 960.0
```

### Cluster

**Source table:** `inventory_cluster` (state = READY or PROGRESSING)

| Meter | Formula | Unit | Notes |
|-------|---------|------|-------|
| `cluster_uptime_seconds` | duration | seconds | Always emitted |
| `cluster_worker_node_seconds` | Σ(node_set_size × duration) | node_seconds | Across all node sets |
| `cluster_worker_node_count` | Σ(node_set_size) | nodes | Snapshot, not time-weighted |

The cluster's `node_sets` JSONB contains one or more node sets with
sizes and host types:

```json
{"workers": {"host_type": "m5.xlarge", "size": 3},
 "infra":   {"host_type": "m5.large",  "size": 2}}
```

```
Cluster "cluster-001" (5 total worker nodes, 60s since last metered)
  └─► entries:
        cluster_uptime_seconds        = 60.0
        cluster_worker_node_seconds   = (3 + 2) × 60 = 300.0
        cluster_worker_node_count     = 5
```

### Bare Metal Instance

**Source table:** `inventory_bare_metal_instance` (state = RUNNING)

| Meter | Formula | Unit |
|-------|---------|------|
| `bm_uptime_seconds` | duration | seconds |

## Consumption-Based Meters (event-driven)

MaaS inference events arrive via `POST /api/v1/events` as CloudEvents.
Metering entries are created immediately on arrival — no sweep needed.

### MaaS Inference

**Source:** CloudEvent from IPP gateway or scripts

| Meter | Formula | Unit | Source field |
|-------|---------|------|-------------|
| `maas_tokens_in` | prompt_tokens | tokens | `data.tokens_in` or `data.prompt_tokens` |
| `maas_tokens_out` | completion_tokens | tokens | `data.tokens_out` or `data.completion_tokens` |
| `maas_requests` | 1 per event | requests | implicit |

`cached_input_tokens` and `reasoning_tokens` are **subsets** of
`prompt_tokens` and `completion_tokens` respectively — they are not
metered separately to avoid double-counting. They are stored in
`raw_events` for observability.

## Rate Matching (4-way fallback)

When the rating sweep prices a metering entry, it looks up a rate
using a 4-way fallback. First match wins:

```
1. tenant_id + instance_type  →  negotiated SKU-specific rate
2. instance_type only         →  global SKU price
3. tenant_id only             →  tenant-wide override
4. global default             →  baseline rate for all
```

**Example:** metering entry for tenant-acme, instance_type m5.xlarge,
meter vm_uptime_seconds:

```
rates table:
  id=1  tenant=NULL     instance_type=""         meter=vm_uptime_seconds  price=0.01/3600   ← global default
  id=2  tenant=NULL     instance_type="m5.xlarge" meter=vm_uptime_seconds  price=0.50/3600   ← SKU price
  id=3  tenant="acme"   instance_type=""         meter=vm_uptime_seconds  price=0.008/3600  ← tenant override
  id=4  tenant="acme"   instance_type="m5.xlarge" meter=vm_uptime_seconds  price=0.40/3600   ← VIP SKU rate

tenant-acme + m5.xlarge → matches id=4 ($0.40/hr)
tenant-acme + c5.large  → no match for #1/#2, matches id=3 ($0.008/hr tenant override)
tenant-beta + m5.xlarge → matches id=2 ($0.50/hr SKU price)
tenant-beta + c5.large  → matches id=1 ($0.01/hr global default)
```

## Flat vs Tiered Pricing

### Flat Rate

Most rates use flat pricing: `cost = value × price_per_unit`.

```
vm_uptime_seconds = 3600 (1 hour)
rate.price_per_unit = 0.50 / 3600  (= $0.50/hr stored as per-second)
cost = 3600 × (0.50/3600) = $0.50
```

### Tiered Rate

Rates with a `tiers` JSONB array use tiered pricing. Each tier
defines a band with a ceiling (`up_to`) and a `price_per_unit`.
The last tier has `up_to: null` (unlimited).

```json
[
  {"up_to": 1000000,  "price_per_unit": 0.00},
  {"up_to": 10000000, "price_per_unit": 0.30e-6},
  {"up_to": null,     "price_per_unit": 0.20e-6}
]
```

**Algorithm:** walk tiers in order, consume units at each tier's
price until the value is exhausted.

```
maas_tokens_in = 15,000,000 tokens

Tier 1: 0 → 1M     @ $0.00/token   →  1M × $0.00  = $0.00
Tier 2: 1M → 10M   @ $0.30/M       →  9M × $0.30/M = $2.70
Tier 3: 10M → ∞    @ $0.20/M       →  5M × $0.20/M = $1.00

Total cost = $0.00 + $2.70 + $1.00 = $3.70
```

Tiered pricing resets per metering entry (every 60s for sweeps,
per-event for MaaS). For monthly volume tiers, the quota system
tracks cumulative usage.

## Cost Type Classification

Every rate has a `cost_type` that classifies the resulting cost:

| Cost Type | Meaning | Examples |
|-----------|---------|---------|
| **Infrastructure** | Base resource cost — what you pay for having the resource | VM uptime, cluster uptime, BM uptime, node hours |
| **Supplementary** | Usage-based cost — scales with consumption | CPU core-hours, memory GiB-hours, MaaS tokens |

The report API surfaces both types separately and as a total.

## Default Rate Seeds

Seeded on first startup if the `rates` table is empty:

| Resource Type | Meter | Koku Metric | Cost Type | Rate | Stored As |
|---------------|-------|-------------|-----------|------|-----------|
| compute_instance | vm_uptime_seconds | vm_cost_per_hour | Infrastructure | $0.01/hr | 0.01/3600 per second |
| compute_instance | vm_cpu_core_seconds | cpu_core_request_per_hour | Supplementary | $0.005/hr/core | 0.005/3600 per core-second |
| compute_instance | vm_memory_gib_seconds | memory_gb_request_per_hour | Supplementary | $0.002/hr/GiB | 0.002/3600 per GiB-second |
| cluster | cluster_uptime_seconds | cluster_cost_per_hour | Infrastructure | $0.50/hr | 0.50/3600 per second |
| cluster | cluster_worker_node_seconds | node_cost_per_hour | Infrastructure | $0.10/hr/node | 0.10/3600 per node-second |
| model | maas_tokens_in | — | Supplementary | $0.50/M tokens | 0.50/1M per token |
| model | maas_tokens_out | — | Supplementary | $1.50/M tokens | 1.50/1M per token |
| model | maas_requests | — | Supplementary | $5.00/M req | 5.00/1M per request |
| bare_metal | bm_uptime_seconds | node_cost_per_hour | Infrastructure | $0.05/hr | 0.05/3600 per second |

## Worked Example: Instance-Type Pricing (Recommended)

A tenant "globex" runs one m5.xlarge VM for 1 hour with
per-instance-type rates configured:

**Rates:**
```sql
('compute_instance', 'm5.xlarge', 'vm_uptime_seconds', 'Infrastructure', 0.50/3600, 'USD')
('compute_instance', '',          'vm_cpu_core_seconds', 'Supplementary', 0, 'USD')
('compute_instance', '',          'vm_memory_gib_seconds', 'Supplementary', 0, 'USD')
```

**1. Metering sweep** (60 sweeps × 60s each):

```
vm_uptime_seconds     = 60.0    (× 60 = 3600 total)
vm_cpu_core_seconds   = 0.0     (OSAC didn't send cores; catalog not populated)
vm_memory_gib_seconds = 0.0     (same — doesn't matter, rate is $0)
```

**2. Rating sweep** — matches m5.xlarge rate on `vm_uptime_seconds`:

```
vm_uptime_seconds: 60 × 0.50/3600 = $0.00833 per sweep
```

**3. Hourly total:**

| Meter | Value | Rate | Cost | Type |
|-------|-------|------|------|------|
| vm_uptime_seconds | 3,600 s | $0.50/hr (m5.xlarge) | **$0.500** | Infrastructure |
| vm_cpu_core_seconds | 0 | $0 | $0.000 | Supplementary |
| vm_memory_gib_seconds | 0 | $0 | $0.000 | Supplementary |
| | | **Total** | **$0.500** | |

No catalog lookup needed. No CPU/memory dependency. The instance type
alone determines the cost.

**4. With tenant-specific discount** ($0.40/hr for globex on m5.xlarge):

```sql
('tenant-globex', 'compute_instance', 'm5.xlarge', 'vm_uptime_seconds', 'Infrastructure', 0.40/3600, 'USD')
```

```
vm_uptime_seconds: 3600 × 0.40/3600 = $0.400  (saved $0.100)
```

## Worked Example: CPU/Memory Pricing (Legacy)

For environments where OSAC sends `cores`/`memory_gib` (or the
InstanceType catalog is populated), you can price by resource specs:

**Rates:**
```sql
('compute_instance', '', 'vm_uptime_seconds',       'Infrastructure', 0.01/3600, 'USD')
('compute_instance', '', 'vm_cpu_core_seconds',     'Supplementary',  0.005/3600, 'USD')
('compute_instance', '', 'vm_memory_gib_seconds',   'Supplementary',  0.002/3600, 'USD')
```

A 4-core, 16 GiB VM running for 1 hour:

| Meter | Value | Rate | Cost | Type |
|-------|-------|------|------|------|
| vm_uptime_seconds | 3,600 s | $0.01/hr | **$0.010** | Infrastructure |
| vm_cpu_core_seconds | 14,400 cs | $0.005/hr/core | **$0.020** | Supplementary |
| vm_memory_gib_seconds | 57,600 gs | $0.002/hr/GiB | **$0.032** | Supplementary |
| | | **Total** | **$0.062** | |

This model requires either OSAC to include CPU/memory on the event
or the `inventory_instance_type` catalog to be populated (via OSAC
reconciler). If neither is available, CPU/memory meters produce 0
and only `vm_uptime_seconds` contributes to cost.

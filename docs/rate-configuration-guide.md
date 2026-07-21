# Rate Configuration Guide

How to configure pricing: per-SKU rates, tenant overrides, tiered
pricing, and MaaS token pricing.

**See also:** [Metric Calculation Reference](metric-calculation-reference.md) —
how meters are computed, catalog fallback, and worked examples
showing the full path from resource to dollar amount.

The rate engine supports per-SKU pricing via the `instance_type`
dimension on the `rates` table. This enables three distinct pricing
models that can be mixed per resource type.

## Rate Matching Logic

When the rating sweep prices a metering entry, it looks up a rate
using a 4-way fallback:

1. **Tenant + instance_type** — e.g. a negotiated rate for tenant-acme on m5.xlarge
2. **Instance_type only** — e.g. a global SKU price for m5.xlarge
3. **Tenant only** — e.g. a tenant-wide override for all VM sizes
4. **Global default** — e.g. a baseline rate for all VMs

The first match wins. An empty `instance_type` on a rate means "applies
to all instance types" (same as an empty `tenant_id` means "applies to
all tenants").

## Pricing Models

### Option 1: Flat rate per catalog item (recommended for OSAC)

Price each VM size as a catalog item. No dependency on CPU/memory fields.

```sql
-- Per-SKU pricing: each instance type has its own hourly rate
INSERT INTO rates (resource_type, instance_type, meter_name, cost_type, price_per_unit, currency)
VALUES
  ('compute_instance', 'm5.xlarge',  'vm_uptime_seconds', 'Infrastructure', 0.50 / 3600, 'USD'),
  ('compute_instance', 'm5.4xlarge', 'vm_uptime_seconds', 'Infrastructure', 2.00 / 3600, 'USD'),
  ('compute_instance', 'c5.2xlarge', 'vm_uptime_seconds', 'Infrastructure', 1.20 / 3600, 'USD');

-- Suppress CPU/memory line items (set to $0 or don't seed them)
INSERT INTO rates (resource_type, instance_type, meter_name, cost_type, price_per_unit, currency)
VALUES
  ('compute_instance', '', 'vm_cpu_core_seconds',    'Supplementary', 0, 'USD'),
  ('compute_instance', '', 'vm_memory_gib_seconds',  'Supplementary', 0, 'USD');
```

**Result:** A tenant running one m5.xlarge for 1 hour pays $0.50.
CPU/memory meters still exist (for capacity tracking / reporting) but
produce $0 cost entries.

### Option 2: CPU/memory rates (pre-OSAC / traditional model)

Price based on provisioned resources. Works when OSAC sends
`cores`/`memory_gib` on the instance, or when the `InstanceType`
catalog is populated (catalog fallback resolves specs automatically).

```sql
-- Global resource-based rates (no instance_type dimension)
INSERT INTO rates (resource_type, meter_name, cost_type, price_per_unit, currency)
VALUES
  ('compute_instance', 'vm_uptime_seconds',       'Infrastructure',  0.01  / 3600, 'USD'),
  ('compute_instance', 'vm_cpu_core_seconds',     'Supplementary',   0.005 / 3600, 'USD'),
  ('compute_instance', 'vm_memory_gib_seconds',   'Supplementary',   0.002 / 3600, 'USD');
```

**Result:** A 4-core, 16 GiB VM running for 1 hour costs:
- Infrastructure: $0.01 (uptime)
- Supplementary: $0.02 (cores) + $0.032 (memory) = $0.052
- Total: $0.062

### Option 3: Per-tenant pricing overrides

Give specific tenants negotiated rates while others get the global
default.

```sql
-- Global default
INSERT INTO rates (resource_type, instance_type, meter_name, cost_type, price_per_unit, currency)
VALUES ('compute_instance', 'm5.xlarge', 'vm_uptime_seconds', 'Infrastructure', 0.50 / 3600, 'USD');

-- VIP tenant gets a discount
INSERT INTO rates (tenant_id, resource_type, instance_type, meter_name, cost_type, price_per_unit, currency)
VALUES ('tenant-vip', 'compute_instance', 'm5.xlarge', 'vm_uptime_seconds', 'Infrastructure', 0.30 / 3600, 'USD');
```

**Result:** tenant-vip pays $0.30/hr for m5.xlarge; everyone else
pays $0.50/hr.

## MaaS Rates

MaaS (token metering) rates don't use `instance_type` — they key on
`meter_name` only. Three meters are billed:

```sql
INSERT INTO rates (resource_type, meter_name, cost_type, price_per_unit, currency, description)
VALUES
  ('model', 'maas_tokens_in',  'Supplementary', 0.50 / 1000000, 'USD', 'Prompt/input tokens (includes cached)'),
  ('model', 'maas_tokens_out', 'Supplementary', 1.50 / 1000000, 'USD', 'Completion/output tokens (includes reasoning)'),
  ('model', 'maas_requests',   'Supplementary', 5.00 / 1000000, 'USD', 'API requests');
```

**Why only three meters:** `cached_input_tokens` and `reasoning_tokens`
from the OpenAI-compatible API are *subsets* of `prompt_tokens` and
`completion_tokens` respectively — not additive. Metering them
separately would double-count. We parse them from CloudEvents for
observability but don't create separate cost entries.

## Catalog Fallback

When OSAC removes `cores`/`memory_gib` from `ComputeInstance` (or
sends them as 0), the metering sweep automatically resolves hardware
specs from the `InstanceType` catalog (`inventory_instance_type`
table, synced via the reconciler). This means:

- `vm_cpu_core_seconds` and `vm_memory_gib_seconds` meters continue to
  produce correct values even without inline specs
- If the `InstanceType` is not in the catalog, these meters produce 0
- With Option 1 (per-SKU pricing), this is irrelevant — cost comes
  from the uptime rate, not CPU/memory meters

## Tiered Pricing

### Per-event tiers (MaaS)

Per-event tiers price each metering entry independently through the
tier ladder. Useful for MaaS where a single API call can be large
enough to cross tier boundaries.

```sql
INSERT INTO rates (resource_type, meter_name, cost_type, price_per_unit, currency, tiers)
VALUES (
  'model', 'maas_tokens_in', 'Supplementary', 0, 'USD',
  '[{"up_to": 1000000, "price_per_unit": 0},
    {"up_to": 10000000, "price_per_unit": 0.0000005},
    {"up_to": null, "price_per_unit": 0.0000003}]'
);
```

**Result:** Each request: first 1M tokens free, next 9M at $0.50/M,
above 10M at $0.30/M. Each request starts fresh at tier 1.

### Cumulative tiers (capacity and volume discounts)

Cumulative tiers accumulate usage over a billing period. The tier
position depends on how much the tenant has already consumed — not
just the current entry.

```sql
INSERT INTO rates (resource_type, meter_name, cost_type, price_per_unit, currency,
                   tier_mode, tier_period, tiers)
VALUES (
  'compute_instance', 'vm_memory_gib_seconds', 'Supplementary', 0, 'USD',
  'cumulative', 'monthly',
  '[{"up_to": 20, "price_per_unit": 0},
    {"up_to": 120, "price_per_unit": 0.08},
    {"up_to": null, "price_per_unit": 0.07}]'
);
```

**Result:** Per month: first 20 GiB free, 20–120 GiB at $0.08,
above 120 GiB at $0.07. A tenant using 200 GiB/month pays $13.60.

**Key fields:**
- `tier_mode` = `"cumulative"` — accumulate over the period (default
  `"per_event"` for backwards compatibility)
- `tier_period` — the accumulation window (default `""` = monthly)

### Windowed MaaS tiers

Use `tier_mode="cumulative"` with a short `tier_period` for
time-windowed free-then-paid bands:

```sql
INSERT INTO rates (resource_type, meter_name, cost_type, price_per_unit, currency,
                   tier_mode, tier_period, tiers)
VALUES (
  'model', 'maas_tokens_in', 'Supplementary', 0, 'USD',
  'cumulative', '5h',
  '[{"up_to": 1000000, "price_per_unit": 0},
    {"up_to": null, "price_per_unit": 0.00001}]'
);
```

**Result:** Every 5 hours: first 1M tokens free, then $10/M. The
window resets at the next 5h boundary (anchored to midnight UTC).

## Billing Periods

The `tier_period` field on rates and the `period` field on quotas
accept these values:

| Value | Window | Anchored to |
|-------|--------|-------------|
| `"monthly"` (default) | Calendar month | 1st of month 00:00 UTC |
| `"weekly"` | ISO week | Monday 00:00 UTC |
| `"daily"` | Calendar day | 00:00 UTC |
| `"Nh"` (e.g. `"5h"`, `"8h"`) | N-hour slots | Midnight UTC; last slot truncated if N doesn't divide 24 |
| `"Nd"` (e.g. `"7d"`, `"10d"`) | N-day slots | 1st of month; last slot truncated if N doesn't divide the month |

## Monetary Budgets

A budget is a quota with `unit` set to a currency code (`USD`, `EUR`,
etc.). Instead of tracking metered usage from `metering_entries`, the
quota status API reports consumed cost from `cost_entries` for that
tenant and period.

Setting `meter_name="*"` creates a tenant-wide spend budget that
covers all meters. This lets you set a single monthly (or any period)
spending cap regardless of which resources drive the cost.

```sql
-- Monthly $5,000 spend cap for tenant-acme across all meters
INSERT INTO quotas (name, tenant_id, meter_name, limit_value, unit, period)
VALUES ('Monthly spend cap', 'tenant-acme', '*', 5000, 'USD', 'monthly');
```

When the quota status API evaluates a budget quota:
- It queries `CostSum` / `TenantCostSum` (summing `cost_entries`) instead
  of `MeteringSum`
- Threshold checks and alerts work identically to usage quotas
- The `consumed` field in the response is the total cost in the quota's
  currency for the current period

## Rate Table Schema

```
rates
├── id              BIGSERIAL PRIMARY KEY
├── tenant_id       TEXT          -- empty/NULL = global
├── resource_type   TEXT NOT NULL -- compute_instance, cluster, model, bare_metal
├── instance_type   TEXT          -- empty = all instance types
├── meter_name      TEXT NOT NULL -- vm_uptime_seconds, maas_tokens_in, etc.
├── koku_metric     TEXT          -- Koku mapping (optional)
├── cost_type       TEXT          -- Infrastructure or Supplementary
├── price_per_unit  NUMERIC       -- per unit (seconds, tokens, etc.)
├── currency        TEXT          -- USD
├── tiers           JSONB         -- tiered pricing bands (optional)
├── tier_mode       TEXT          -- "per_event" (default) or "cumulative"
├── tier_period     TEXT          -- accumulation window: "", "monthly", "5h", "7d", etc.
├── description     TEXT
├── effective_from  TIMESTAMPTZ
└── effective_to    TIMESTAMPTZ   -- NULL = no expiry
```

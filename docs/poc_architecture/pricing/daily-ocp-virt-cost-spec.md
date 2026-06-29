# Daily OpenShift Virtualization Cost Specification

> **Status:** PoC draft
> **Requirements:** REQ-12
> **Priority:** TBD — pending Product Management confirmation
> **Related:** [metering-spec-draft.md](metering-spec-draft.md) · [cost-calculation-spec-draft.md](../pricing/cost-calculation-spec-draft.md) · [ai-grid-reporting-api.md](../reporting/ai-grid-reporting-api.md) · [architecture.md](../architecture.md)

---

## 1. Purpose

This spec defines how Cost Management produces **closed daily cost totals** for OpenShift Virtualization (VMaaS) workloads provisioned through OSAC. It is a companion to [metering-spec-draft.md](metering-spec-draft.md), which defines the 60-second capacity metering pipeline.

**This spec answers:** what did a tenant's OpenShift Virtualization workloads cost, per day, in a form suitable for billing statements and chargeback reports?

### REQ-12 vs REQ-2 — Two Distinct Products

These requirements serve different consumers and must coexist:

| | REQ-2 — Near-Real-Time Cost | REQ-12 — Daily OCP Virt Cost |
|---|---|---|
| **Latency** | ≤ 60s after OSAC event | Available by 02:00 UTC for previous day |
| **Mutability** | Updates every ~60s (running total) | Immutable after day close |
| **Use case** | Live dashboards; quota evaluation | Billing statements; chargeback export |
| **Consumer** | OSAC dashboard, OPA enforcement | Tenant billing portal, finance |
| **Source** | `metering_entries` (live) | `metering_entries` (aggregated) |
| **Output** | `cost_entries` (per sweep) | `daily_cost_summary` (per day per resource) |

Both are derived from the same `metering_entries` table. The daily pipeline is an aggregation over the intraday stream — not a separate metering mechanism.

---

## 2. Scope

### In Scope

| Resource | Service | Billing model | Daily meters |
|---|---|---|---|
| Compute Instance (VM) | VMaaS — OpenShift Virtualization | Capacity-based | `vm_uptime_hours`, `vm_cpu_core_hours`, `vm_memory_gib_hours` |

OpenShift Virtualization in this context means **OSAC-provisioned ComputeInstance resources** — VMs managed by OSAC's OpenShift Virtualization service (the VMaaS service type). These are distinct from pods and from bare-metal nodes.

### Out of Scope

| Item | Reason |
|---|---|
| CaaS (Cluster) daily costs | Different billing unit (cluster-month); shares the same pipeline pattern but is not REQ-12 scope |
| BMaaS (Bare Metal) daily costs | OSAC BMaaS schema not yet defined (REQ-8) |
| MaaS (Model/Token) daily costs | Consumption-based; daily aggregation of token counts is a separate concern (REQ-4) |
| Sub-day (hourly) cost breakdown | REQ-2 near-real-time path covers intraday views |
| Usage-based metrics (CPU/memory actual utilization) | Requires Prometheus — outside PoC scope |

---

## 3. Relationship to Existing Pipeline

The PoC already implements the early steps. This spec completes the pipeline through to daily cost entries.

```
[Implemented] 60s Meter sweep
    → metering_entries (vm_uptime_seconds, vm_cpu_core_seconds, vm_memory_gib_seconds)

[Implemented] Summarizer worker (SUMMARIZE_INTERVAL, default 1h)
    → daily_usage_summary (previous UTC day; compute instances only; no cost amounts)
    NOTE: the Summarizer derives hours from inventory lifetime overlap
    (ComputeInstancesAliveDuring — created_at / deleted_at window clipped to the billing day),
    not from aggregating metering_entries. The table stores pre-computed
    duration_hours, cpu_core_hours, memory_gb_hours per VM per day.
    Each run deletes and rewrites the day's rows (not append-only).

[This spec — Phase 2a] Daily cost calculation step
    → daily_cost_summary (previous UTC day; cost amounts applied from rates table)

[Planned — Phase 3] Reporting API
    → GET /costs/timeseries?granularity=day
    → GET /reports/chargeback
```

The existing `daily_usage_summary` table contains usage quantities (hours, core-hours, GiB-hours) but no cost amounts. This spec defines:
1. The schema extension or companion table (`daily_cost_summary`) that adds cost amounts.
2. The daily cost calculation step that joins `daily_usage_summary` against the `rates` table.
3. Day-boundary behavior and finalization semantics.

---

## 4. Day Boundary and Finalization

### 4.1 Billing Day Definition

A billing day is a **UTC calendar day** (00:00:00Z → 23:59:59.999Z). All timestamps in `metering_entries` (`period_start`, `period_end`) are stored as UTC.

### 4.2 Daily Aggregation Window

The Summarizer worker aggregates `metering_entries` rows for the **previous complete UTC day** — i.e., all rows where `period_end` falls within `[T_day_start, T_day_end)`. Rows whose `period_end` crosses midnight are split:

| Sweep start | Sweep end | Attribution |
|---|---|---|
| 23:59:00Z day D | 00:00:00Z day D+1 | Attributed to day D (60s window < midnight) |
| 23:59:30Z day D | 00:00:30Z day D+1 | **Split:** 30s to day D, 30s to day D+1 |

In practice, the 60-second sweep granularity means at most one sweep per VM crosses a midnight boundary. The Summarizer handles this by apportioning `duration_seconds` at the boundary:

```
seconds_in_day_D   = MIN(period_end, day_D_end) - period_start
seconds_in_day_D1  = period_end - MIN(period_end, day_D_end)
```

For PoC simplicity, boundary-crossing rows may be attributed entirely to the day containing `period_end` (i.e., no split). This introduces at most 60 seconds of attribution error per VM per day — acceptable for a PoC.

### 4.3 Finalization Semantics

**`daily_usage_summary` (current):** The Summarizer deletes and rewrites summary rows for the target day on each run — it is re-computable but not immutable. This is safe because the source data (inventory `created_at` / `deleted_at`) is itself immutable.

**`daily_cost_summary` (planned — Phase 2a):** Once a daily cost entry is written it should be treated as **immutable**:

- No re-rating after day close.
- Late-arriving events (e.g. from reconciler catching a missed CREATED) that fall within a closed day are logged to `metering_entries` with a `late_arrival` flag and included in the _next_ daily run, with `period_start`/`period_end` reflecting the original window. A correction record is issued — it does not overwrite the closed day.
- The Summarizer skips the current (open) day — it only aggregates `yesterday UTC`.

### 4.4 Timing

The daily cost calculation runs as a second step immediately after the Summarizer completes for a given day:

```
[~01:00 UTC] Summarizer aggregates yesterday → daily_usage_summary
[~01:01 UTC] Daily cost calculator joins rates → daily_cost_summary
[~02:00 UTC] daily_cost_summary available for reporting (target SLA)
```

The exact trigger depends on whether the Summarizer is timer-based (current implementation) or event-driven. For PoC, timer-based at a configurable hour (default 01:00 UTC) is acceptable.

---

## 5. Daily Meters and Aggregation

The daily meters are aggregated from `metering_entries` for the previous UTC day:

| Daily Meter | Unit | Aggregation from metering_entries | Group By |
|---|---|---|---|
| `vm_uptime_hours` | hours | `SUM(value) / 3600` where `meter_name = 'vm_uptime_seconds'` | `tenant_id`, `project_id`, `resource_id` |
| `vm_cpu_core_hours` | core-hours | `SUM(value) / 3600` where `meter_name = 'vm_cpu_core_seconds'` | `tenant_id`, `project_id`, `resource_id` |
| `vm_memory_gib_hours` | GiB-hours | `SUM(value) / 3600` where `meter_name = 'vm_memory_gib_seconds'` | `tenant_id`, `project_id`, `resource_id` |

Each row in `daily_usage_summary` (existing) represents one resource for one UTC day. The cost step joins this against the `rates` table per meter.

### 5.1 Maximum Theoretical Values

A VM running the full 24-hour day at 4 cores / 8 GiB produces:

| Meter | Maximum daily value |
|---|---|
| `vm_uptime_hours` | 24 hours |
| `vm_cpu_core_hours` | 96 core-hours |
| `vm_memory_gib_hours` | 192 GiB-hours |

Values exceeding 24 hours signal a data integrity error and should be flagged but not silently capped.

---

## 6. Daily Cost Schema

### 6.1 `daily_cost_summary` Table

One row per resource per billing day per meter. Written by the daily cost calculation step.

```
daily_cost_summary
  id                 UUID PK
  billing_date       DATE               -- UTC calendar day (e.g. '2026-06-26')
  resource_type      TEXT               -- 'compute_instance'
  resource_id        UUID               -- OSAC instance UUID
  tenant_id          TEXT               -- OSAC tenant identifier
  project_id         UUID NULL          -- OSAC project UUID (NULL if unmapped)
  meter_name         TEXT               -- 'vm_uptime_hours', 'vm_cpu_core_hours', 'vm_memory_gib_hours'
  metered_value      DECIMAL(18,6)      -- aggregated quantity
  unit               TEXT               -- 'hours', 'core_hours', 'gib_hours'
  rate_id            UUID               -- FK → rates.id (rate applied at calculation time)
  unit_price         DECIMAL(18,8)      -- snapshot of rate at calculation time
  cost_amount        DECIMAL(18,6)
  currency           TEXT               -- 'USD'
  calculated_at      TIMESTAMPTZ        -- when this row was written
  is_finalized       BOOLEAN DEFAULT TRUE
  is_correction      BOOLEAN DEFAULT FALSE  -- TRUE for late-arrival correction rows
  corrects_date      DATE NULL          -- for correction rows: the day being corrected
```

### 6.2 Cost Formula

Same as `cost-calculation-spec-draft.md` §5, applied at daily granularity:

```
cost_amount = metered_value × unit_price
```

Where `unit_price` is the hourly or per-unit rate from the `rates` table. For monthly flat rates, the daily equivalent is applied:

```
unit_price_daily = monthly_rate / days_in_month
cost_amount = vm_uptime_hours × (unit_price_daily / 24)
```

### 6.3 Rate Snapshot Policy

The `unit_price` column is a **snapshot** of the rate at calculation time, not a live FK. This ensures the daily record is immutable — a rate change the following month does not alter a closed day's cost. The FK `rate_id` preserves the audit link.

---

## 7. Daily Aggregation SQL

### 7.1 Step 1 — Summarizer (implemented)

The Summarizer worker does **not** aggregate from `metering_entries`. It reads inventory records directly, clips each VM's active window to the billing day boundary, and computes hours from the clipped duration. This avoids sweep granularity artefacts.

```go
// Pseudocode — see inventory-watcher/internal/summarizer/summarizer.go
instances = store.ComputeInstancesAliveDuring(dayStart, dayEnd)
for inst in instances:
    effectiveStart = max(inst.created_at, dayStart)
    effectiveEnd   = min(inst.deleted_at ?? dayEnd, dayEnd)
    durationHours  = (effectiveEnd - effectiveStart).Hours()
    cpuCoreHours   = inst.Cores    × durationHours
    memGiBHours    = inst.MemoryGiB × durationHours

    INSERT INTO daily_usage_summary (
        usage_date, resource_id, resource_type, tenant, project,
        cluster_id, instance_type, cores, memory_gib,
        duration_hours, cpu_core_hours, memory_gb_hours
    ) VALUES (...)
```

The actual schema uses `usage_date` (not `billing_date`), `tenant` (not `tenant_id`), and pre-computed aggregate columns (`duration_hours`, `cpu_core_hours`, `memory_gb_hours`) rather than per-meter rows. Each run deletes and rewrites the target day's rows.

### 7.2 Step 2 — Daily Cost Calculation (planned — Phase 2a)

The daily cost step reads `daily_usage_summary` and joins against the `rates` table. Note the actual column names differ from a per-meter model — rates for each quantity dimension are looked up individually:

```sql
-- Step 2: cost calculation (planned daily cost step)
INSERT INTO daily_cost_summary (
    billing_date, resource_type, resource_id, tenant_id, project_id,
    meter_name, metered_value, unit, rate_id, unit_price, cost_amount, currency, calculated_at
)
SELECT
    dus.usage_date                                         AS billing_date,
    dus.resource_type,
    dus.resource_id,
    dus.tenant                                             AS tenant_id,
    NULLIF(dus.project, '')                                AS project_id,
    m.meter_name,
    m.metered_value,
    m.unit,
    r.id                                                   AS rate_id,
    r.price_per_unit                                       AS unit_price,
    m.metered_value * r.price_per_unit                     AS cost_amount,
    r.currency,
    NOW()                                                  AS calculated_at
FROM daily_usage_summary dus
CROSS JOIN LATERAL (VALUES
    ('vm_uptime_seconds',     dus.duration_hours * 3600,  'seconds'),
    ('vm_cpu_core_seconds',   dus.cpu_core_hours * 3600,  'core_seconds'),
    ('vm_memory_gib_seconds', dus.memory_gb_hours * 3600, 'gib_seconds')
) AS m(meter_name, metered_value, unit)
JOIN rates r
    ON r.resource_type = dus.resource_type
    AND r.meter_name   = m.meter_name
    AND (r.tenant_id IS NULL OR r.tenant_id = dus.tenant)
    AND r.effective_from <= dus.usage_date
    AND (r.effective_to IS NULL OR r.effective_to > dus.usage_date)
WHERE
    dus.usage_date    = :yesterday
    AND dus.resource_type = 'compute_instance'
ORDER BY r.tenant_id NULLS LAST  -- tenant-specific rate wins over default
ON CONFLICT (billing_date, resource_id, meter_name) DO NOTHING;  -- idempotent
```

---

## 8. Implementation — Summarizer Worker Extension

The Summarizer worker in `inventory-watcher/cmd/consumer/main.go` runs at `SUMMARIZE_INTERVAL` (default 1h) and currently writes `daily_usage_summary` for compute instances only.

The daily cost step is a second pass immediately after the Summarizer completes for a given day. Both steps are idempotent — safe to re-run if the worker restarts mid-calculation.

### 8.1 Worker Configuration

| Env var | Default | Purpose |
|---|---|---|
| `SUMMARIZE_INTERVAL` | `1h` | How often the Summarizer checks whether yesterday is ready to aggregate |

The following env vars described in earlier drafts — `DAILY_COST_HOUR_UTC` and `DAILY_COST_LOOKBACK_DAYS` — are **not yet implemented** in `config.go`. The Summarizer always targets yesterday UTC on each tick; no configurable cutoff hour or catch-up window exists in the current code. These remain valid targets for Phase 2a.

### 8.2 Idempotency and Re-runs

**`daily_usage_summary`** is recomputed on each Summarizer run: the worker deletes existing rows for the target day and re-inserts them (`DeleteDailyUsageSummaries` → re-compute from inventory). This is safe and intentional — source data (inventory `created_at`/`deleted_at`) is immutable.

**`daily_cost_summary`** (planned Phase 2a) should use `ON CONFLICT … DO NOTHING` to be append-safe. A forced re-calculation after a rate correction requires manually deleting `daily_cost_summary` rows for the affected date range and triggering a re-run. This is an operator action — not automated for PoC.

### 8.3 Missing Rate Handling

If no rate row matches a `(resource_type, meter_name)` pair, the cost calculation step logs a warning and writes `daily_cost_summary` with `cost_amount = 0` and `rate_id = NULL`. This is preferable to silently skipping the row — a zero-cost record with a NULL rate is detectable and auditable.

---

## 9. API Surface

The `/costs/timeseries` endpoint defined in [ai-grid-reporting-api.md](../reporting/ai-grid-reporting-api.md) serves daily cost views directly from `daily_cost_summary`:

```
GET /costs/timeseries?tenant_id=<id>&granularity=day&period_start=2026-06-01&period_end=2026-06-30
```

Response shape (daily granularity for VMaaS):

```json
{
  "data": [
    {
      "bucket_start": "2026-06-25T00:00:00Z",
      "bucket_end":   "2026-06-26T00:00:00Z",
      "capacity_cost": 18.40,
      "maas_cost":      0.00,
      "total_cost":    18.40,
      "currency":      "USD",
      "breakdown": {
        "vm_uptime_hours":      { "value": 48.0,  "unit": "hours",      "cost": 4.80 },
        "vm_cpu_core_hours":    { "value": 192.0, "unit": "core_hours", "cost": 9.60 },
        "vm_memory_gib_hours":  { "value": 384.0, "unit": "gib_hours",  "cost": 3.84 }
      }
    }
  ],
  "meta": {
    "data_as_of":  "2026-06-26T01:05:00Z",
    "tenant_id":   "tenant-acme",
    "granularity": "day"
  }
}
```

The `data_as_of` field indicates when the most recent daily close ran. Callers can detect whether the current day is still open (i.e. today's bucket is absent from the response).

The `/reports/chargeback` endpoint (REQ-5) also reads from `daily_cost_summary` for its capacity cost columns.

---

## 10. Acceptance Criteria

| Criterion | Specification |
|---|---|
| Daily totals available | `daily_cost_summary` rows for day D are written by 02:00 UTC on day D+1 |
| Correct billing unit | Cost derived from provisioned capacity × duration (no Prometheus required) |
| Immutability | Rows for a closed day are not overwritten by subsequent rate changes |
| Idempotency | Running the summarizer + cost step twice for the same day produces identical results |
| Tenant/project attribution | Every `daily_cost_summary` row carries `tenant_id`; `project_id` populated when OSAC event data includes it |
| Rate snapshot | `unit_price` reflects the rate in effect on `billing_date`, not the current rate |
| Missing rate audit | Zero-cost rows with NULL `rate_id` written and logged when no rate is configured |
| No data loss on restart | `DAILY_COST_LOOKBACK_DAYS` catch-up ensures days missed during downtime are calculated on next start |
| Correction rows | Late-arrival metering entries produce `is_correction = TRUE` rows; closed days are not modified |

---

## 11. SLA

| Stage | Target |
|---|---|
| Summarizer writes `daily_usage_summary` | Within 1h of UTC midnight (01:00 UTC target) |
| Daily cost step writes `daily_cost_summary` | Within 1 minute of Summarizer completion |
| API response available | By 02:00 UTC for previous day |
| Report latency (timeseries query) | < 5s (served from pre-aggregated `daily_cost_summary`) |

---

## 12. PoC Phasing

| Phase | Deliverable | Status | Dependency |
|---|---|---|---|
| **1** | `daily_usage_summary` aggregation (Summarizer worker) | **Implemented** — VMs only; inventory lifetime overlap approach | — |
| **2a** | `daily_cost_summary` table + daily cost calculation step | **Planned** — dependency unblocked | `rates` table seeded ✅ (`rating.go` Phase 2 implemented) |
| **2b** | `is_correction` / late-arrival handling | Planned | Phase 2a |
| **3** | `GET /costs/timeseries?granularity=day` reads `daily_cost_summary` | Planned | Phase 2a |
| **4** | Extend to CaaS (cluster daily costs) | Post-PoC | Architecture confirmation |

---

## 13. Open Questions

| # | Question | Owner | Impact |
|---|---|---|---|
| 1 | **Product confirmation** — does REQ-12 mean daily VMaaS cost summaries as specified here, or something else (e.g. daily OCP Virt cost from pod-level metrics)? | Product Management | Scope of entire spec |
| 2 | **Monthly vs daily rate** — should the billing unit be VM-month (amortized daily) or VM-day (daily flat rate)? | Cost + OSAC | Formula in §6.2 |
| 3 | **CaaS daily costs** — should clusters be included in daily cost summaries alongside VMs, or is REQ-12 strictly about VMs (OpenShift Virtualization)? | Product Management | Scope of §2 |
| 4 | **Correction row surfacing** — should late-arrival correction rows appear in the API with a flag, or be silently merged into a revised daily total? | Cost + Product | API design |
| 5 | **Tenant billing day** — is UTC midnight always the billing day boundary, or should tenants be able to configure a fiscal day start? | Product Management | Day boundary logic §4.1 |

---

## 14. References

- [REQ-12](../../requirements/poc_requirements_overview.md) — Daily OpenShift Virtualization Costs
- [metering-spec-draft.md](metering-spec-draft.md) — 60s capacity metering pipeline (input to this spec)
- [cost-calculation-spec-draft.md](../pricing/cost-calculation-spec-draft.md) — rate schema and cost formula
- [ai-grid-reporting-api.md](../reporting/ai-grid-reporting-api.md) — `/costs/timeseries` and `/reports/chargeback` endpoints
- [ADR-001: Metering sweep interval](../../decisions/001-metering-sweep-interval.md)
- [Koku `monthly_cost_virtual_machine.sql`](https://github.com/project-koku/koku/blob/main/koku/masu/database/sql/openshift/cost_model/monthly_cost_virtual_machine.sql) — reference for VM cost aggregation patterns

# REQ-11: Cumulative Tier Pricing — Design Proposal

> **Status:** Proposal for PM review
> **Date:** 2026-07-18
> **Author:** Martin Povolny
> **Depends on:** [req11-cost-tiers-gap-analysis.md](req11-cost-tiers-gap-analysis.md)

## Problem

Our tiered pricing engine works correctly for MaaS tokens (per-event
pricing — each API call is independently priced through the tier
ladder). For capacity meters (VM uptime, CPU core-hours, GiB-month),
per-event tiering is silently wrong — the free tier never exhausts
because each 60-second sweep is too small to cross any tier boundary.

**Worked example:** Tier config = "first 20 GiB free, 20–120 GiB at
$0.08, 120+ at $0.07." A tenant runs VMs using 200 GiB over a month.
The metering sweep produces entries of ~0.07 GiB every 60 seconds.

| Approach | Result | Correct? |
|----------|--------|----------|
| Per-event (current) | Every 0.07 GiB entry falls within 20 GiB free tier → **$0.00 all month** | No — undercharged by $13.60 |
| Cumulative (proposed) | First 20 GiB free ($0) + next 100 GiB at $0.08 ($8.00) + 80 GiB at $0.07 ($5.60) = **$13.60** | Yes |

See [gap analysis](req11-cost-tiers-gap-analysis.md) for the full
breakdown and additional context.

## Proposed Design

### 1. `tier_mode` field on rates

Add a `tier_mode` column to the `rates` table:

| Value | Behavior | Use case |
|-------|----------|----------|
| `per_event` (default) | Current behavior — each metering entry priced independently through the tier ladder | MaaS tokens, per-request pricing |
| `cumulative` | Accumulate usage over the billing period; price the marginal delta at the correct tier position | Capacity meters (GiB-month, core-hours), monthly volume discounts |

This is **explicit per-rate**, not inferred from `resource_type`. The
rate author decides. This matters because:
- MaaS could need cumulative tiers (e.g. "first 1M tokens/month free")
- Capacity could theoretically have per-event tiers
- Mixing both on the same `resource_type` is possible

### 2. Billing period: monthly (calendar month)

The accumulation window for `cumulative` tiers is the calendar month
(1st 00:00 UTC to last day 23:59:59 UTC). This matches:

- The spec's "GiB-**month**" unit in the REQ-11 example
- Our existing quota period (`"monthly"` in `QuotaRecord`)
- How cloud providers (AWS, GCP, Azure) bill capacity reservations

**For the PoC**, monthly is hardcoded. Post-PoC, the period can be made
configurable per rate (daily, weekly, quarterly, annual) by adding a
`tier_period` column. We defer this to avoid designing the period
semantics (reset behavior, mid-period rate changes, partial-month
proration) before we have concrete requirements.

### 3. Graduated (marginal) pricing

When crossing a tier boundary, only the marginal units above the
boundary are priced at the higher rate. This is what `applyTieredRate`
already implements. No change needed.

The alternative — **volume pricing** (ALL usage in the period is
repriced at the rate for the highest tier reached) — is not proposed.
Graduated pricing is the standard for cloud infrastructure billing
(AWS, GCP, Azure all use graduated for storage/compute tiers).

### 4. Accumulation scope: per-tenant + per-meter

Tier position is determined by summing `metering_entries` for the same
`(tenant_id, meter_name)` within the billing period. This reuses the
existing `MeteringSum` query (already used for quota thresholds).

**Example:** Two projects under tenant-acme each use 15 GiB/month.
With a "20 GiB free" tier:
- **Per-tenant (proposed):** 15 + 15 = 30 GiB total, 10 GiB above
  free tier → $0.80 billable
- **Per-project (deferred):** each project under 20 GiB → $0.00

Per-project accumulation is deferred — it would require project-level
tier rules, adding complexity that isn't in the current spec.

## How It Works in the Sweep

```
For each unrated metering entry:
  1. Match rate (existing 4-way fallback)
  2. If rate has tiers AND tier_mode == "cumulative":
     a. Compute billing period: 1st of entry's month → 1st of next month
     b. Query MeteringSum(tenant, meter, period_start, period_end)
        → prior_usage (month-to-date BEFORE this entry)
     c. Call applyTieredRateCumulative(entry.Value, prior_usage, tiers)
        → cost for the marginal delta at the correct tier position
  3. If rate has tiers AND tier_mode == "per_event":
     → current behavior (applyTieredRate with entry.Value alone)
  4. If no tiers:
     → flat rate (entry.Value × price_per_unit)
```

### `applyTieredRateCumulative` — the key new function

Same graduated waterfall as `applyTieredRate`, but starts at
`priorUsage` instead of 0. Only the marginal `value` (this sweep's
delta) is priced; `priorUsage` determines which tier band the delta
falls into.

```
applyTieredRateCumulative(value=0.07, priorUsage=19.95, tiers):
  - Tier 1 (0–20, free): 20–19.95 = 0.05 remaining in tier → 0.05 free
  - Tier 2 (20–120, $0.08): 0.07–0.05 = 0.02 at $0.08 = $0.0016
  - Total: $0.0016
```

### Worked sweep example

Tier: first 20 GiB free, 20–120 GiB at $0.08, 120+ at $0.07.
Tenant accumulates 200 GiB over a month via ~2,880 sweeps of ~0.07
GiB each.

```
Sweep 1:    prior=0.00,    delta=0.07 → free tier        → $0.0000
Sweep 286:  prior=19.95,   delta=0.07 → crosses 20 GiB   → $0.0016
Sweep 1715: prior=119.98,  delta=0.07 → crosses 120 GiB  → $0.0051
Sweep 2880: prior=199.93,  delta=0.07 → tier 3            → $0.0049
─────────────────────────────────────────────────────────────────────
Total billed over the month:                               $13.60 ✓
```

## Implementation Scope

| Change | File | Effort |
|--------|------|--------|
| Add `tier_mode` column to `rates` table | `internal/inventory/store.go` | Small |
| Add `TierMode` field to `RateRecord` | `internal/inventory/models.go` | Small |
| Update `AllActiveRates`, `UpsertRate`, `FindRate` | `internal/inventory/store.go` | Small |
| Implement `applyTieredRateCumulative` | `internal/rating/rating.go` | Small |
| In sweep: if cumulative, call `MeteringSum` + new function | `internal/rating/rating.go` | Medium |
| Update `SeedDefaultRates` with cumulative examples | `internal/rating/rating.go` | Small |
| Tests: cumulative tier boundary crossing, free tier exhaustion | `internal/rating/rating_test.go` | Medium |
| Integration test: multi-sweep cumulative billing | `internal/rating/rating_integration_test.go` | Medium |
| Update rate configuration guide | `docs/rate-configuration-guide.md` | Small |
| Update REQ-11 gap analysis to note closure | `docs/requirements/req11-cost-tiers-gap-analysis.md` | Small |

**Total effort:** Medium (2–3 sessions)

## Questions for Pau (confirmation, not open-ended)

We propose the defaults above. If any are wrong, we adjust before
implementing.

1. **Monthly billing period** — we propose calendar month. Are there
   scenarios where a different period is needed for the PoC? (We can
   make it configurable post-PoC.)

2. **Per-tenant accumulation** — we propose tiers accumulate per
   tenant, not per project. If two projects under tenant-acme each
   use 15 GiB/month, they share the 20 GiB free tier (total 30 GiB,
   10 GiB billable). Is that correct?

3. **MaaS cumulative tiers** — should MaaS token rates also support
   cumulative tiers (e.g. "first 1M tokens/month free")? We're
   building the infrastructure either way; this is about whether to
   configure any MaaS rates as cumulative for the demo.

4. **Graduated pricing** — we use graduated/marginal pricing (only
   excess above tier boundary is charged at higher rate). Confirm this
   is correct vs. volume pricing (all usage repriced at highest tier
   reached).

## Related Documents

- [REQ-11 gap analysis](req11-cost-tiers-gap-analysis.md) — explains the per-event vs cumulative problem
- [Rate configuration guide](../rate-configuration-guide.md) — how to set up rates (per-SKU, CPU/memory, per-tenant)
- [poc_requirements_overview.md#req-11](poc_requirements_overview.md#req-11--cost-tiers) — canonical requirement

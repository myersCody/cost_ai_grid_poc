# Requirement 11: Cost Tiers — Gap Analysis

> **Requirement:** Tiered pricing support for both capacity-based and MaaS
> consumption-based rates. Example: first 20 GiB free, next 100 GiB at
> $0.08/GiB-month, next 1000 GiB at $0.07/GiB-month.
>
> **Source:** [poc_requirements_overview.md#req-11](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-11-cost-tiers)
>
> **Depends on:** REQ-2 (Near-Real-Time Cost Calculation) — **Done**,
> REQ-2a (MaaS token metering) — **In Progress**

## TL;DR

Tiered pricing is already implemented and works correctly for MaaS token rates — per-event semantics, no code changes needed. Capacity-based rates (GiB-month, core-hours) require a separate cumulative/period-accumulating tier logic that is not yet implemented. Tiers configured on capacity meters today would silently produce incorrect (undercharged) billing. Two open questions need sign-off before implementation proceeds: demo scope and tier ownership.

## What We Have

The rating engine already implements tiered pricing. `ApplyRate` checks for
tiers before falling back to flat-rate pricing:

```go
func ApplyRate(value float64, rate inventory.RateRecord) float64 {
    if len(rate.Tiers) > 0 {
        return applyTieredRate(value, rate.Tiers)
    }
    return value * rate.PricePerUnit
}
```

`applyTieredRate` uses a waterfall loop — `prev`/`remaining` tracking — that
correctly implements **graduated pricing**: only the marginal units in each
band are priced at that band's rate. The whole quantity is not repriced when
a boundary is crossed.

The data model supports tiers natively. `RateRecord.Tiers` is a `[]Tier`
stored as JSONB in the `rates` table:

```go
type Tier struct {
    UpTo         *float64 `json:"up_to"`   // nil = unbounded final tier
    PricePerUnit float64  `json:"price_per_unit"`
}
```

A `nil` `UpTo` means the tier is unbounded — it absorbs all remaining
quantity. This correctly handles the "above N units at $X" final band.

Tier configuration requires no code changes: tiers are stored in the DB and
loaded at rating time. A new tier structure is a SQL upsert or API call to
`POST /api/v1/rates`.

## Rate-to-Tier-Semantics Map

Every meter currently in `SeedDefaultRates` falls into one of two tier semantics. This table should be the authoritative reference for implementation decisions and demo scripting.

| Meter | Resource Type | Unit | Tier Semantics | Reason |
|---|---|---|---|---|
| `vm_uptime_seconds` | `compute_instance` | seconds | **Cumulative** | Capacity reservation; 60s sweeps are tiny fractions of monthly usage |
| `vm_cpu_core_seconds` | `compute_instance` | core_seconds | **Cumulative** | Capacity reservation; same accumulation model as GiB-month |
| `vm_memory_gib_seconds` | `compute_instance` | gib_seconds | **Cumulative** | Exactly the GiB-month pattern cited in the spec |
| `cluster_uptime_seconds` | `cluster` | seconds | **Cumulative** | Capacity reservation; cluster-hours accumulate over the billing period |
| `cluster_worker_node_seconds` | `cluster` | node_seconds | **Cumulative** | Node-hour accumulation; tiers represent volume discounts on sustained usage |
| `bm_uptime_seconds` | `bare_metal` | seconds | **Cumulative** | Capacity reservation; bare metal is leased, not consumed per-request |
| `maas_tokens_in` | `model` | tokens | **Per-Event** | Per-request pricing; matches cloud AI provider model (OpenAI, Anthropic) |
| `maas_tokens_out` | `model` | tokens | **Per-Event** | Per-request pricing; same rationale as `maas_tokens_in` |
| `maas_tokens_cached` | `model` | tokens | **Per-Event** | Per-request discount tier; tiers = contract level, not accumulation (rate seeded but not currently metered — no entries produced) |
| `maas_tokens_reasoning` | `model` | tokens | **Per-Event** | Per-request pricing for thinking tokens (rate seeded but not currently metered — no entries produced) |
| `maas_requests` | `model` | requests | **Per-Event** | Per-request pricing; each API call is independent |

**Summary:** All capacity meters (VM, cluster, bare metal) require cumulative tier semantics. All MaaS meters are correctly handled by the existing per-event implementation.

## Coverage vs Gaps

| Capability | Required | Status | Notes |
|---|---|---|---|
| Multiple tiers per rate record | Yes | **Done** | `[]Tier` in `RateRecord` |
| Graduated pricing (marginal units only) | Yes | **Done** | `applyTieredRate` waterfall |
| Unbounded final tier (`up_to: null`) | Yes | **Done** | `nil` pointer check |
| Free tier (`price_per_unit: 0.0`) | Yes | **Done** | Zero price is valid; no special case needed |
| MaaS per-event tiers (tokens, requests) | Yes | **Done** | Per-event is the correct model for per-request billing; matches how cloud AI providers price |
| Capacity cumulative tiers (GiB-month, core-hours) | Yes | **Gap** | Spec example implies monthly accumulation; per-event is incorrect for this case — see "Tier Semantics" below |
| Tier config without code changes | Yes | **Done** | DB-stored JSONB; manageable via API or SQL |
| Tier management UI or sync API | Out of scope | Not started | Manual config acceptable for PoC |
| OSAC → Cost tier sync | Post-PoC | Not started | Ownership unresolved; deferred |

## Gap Details

### 1. Tier Semantics: Two Billing Models, Two Different Correct Answers

The spec applies tiers to both MaaS and capacity workloads but does not
distinguish that they require different semantics. They are not the same
problem.

#### MaaS (tokens, requests) — Per-Event Is Correct

For MaaS token billing, **per-event is the right model**. Cloud AI providers price per API call at a fixed rate per token.
A tenant does not accumulate toward a discount tier just because they have
sent many small requests over a month. A tier in MaaS billing typically
reflects a contract commitment level (you get a lower rate because you signed
up for a volume plan), not progressive in-period accumulation.

The current implementation is correct for MaaS. Each token event is
independently priced through the tier ladder. If a single request is large
enough to cross a tier boundary (e.g., a batch job sending 2M tokens), the
graduated waterfall applies correctly within that event.

#### Capacity (GiB-month, core-hours) — Per-Event Is Wrong

The only concrete example in the requirements is:

> "first 20 GiB free, next 100 GiB at $0.08/**GiB-month**"

The `/month` suffix is unambiguous — this is a billing period aggregate.
Usage accumulates across all 60-second sweeps in the month and tiers fire
as the running total crosses thresholds. No individual sweep produces 20 GiB
of delta; per-event tiering means the free tier never exhausts and the tenant
pays $0 all month regardless of total consumption.

**Per-event example** — tenant accumulates 200 GiB over a month via sweeps
of ~0.07 GiB each. Tier: first 20 GiB free, above 20 GiB at $0.08/GiB-month.

```
Sweep 1:   ApplyRate(0.07 GiB) → within free tier → $0.00
Sweep 2:   ApplyRate(0.07 GiB) → within free tier → $0.00
...
Sweep N:   ApplyRate(0.07 GiB) → within free tier → $0.00
Total billed: $0.00   ← free tier never exhausts; tenant is undercharged by $14.40
```

**Cumulative example** — same 200 GiB accumulated over the month:

```
First 20 GiB  → $0.00 (free)
Next 100 GiB  → $8.00 (100 × $0.08)
Next 80 GiB   → $5.60 (80 × $0.07)
Total billed: $13.60   ← correct
```

This is the real gap. A per event tier system that works for MaaS may be silently incorrect for a capacity based rate.


### 2. Unresolved Ownership: Where Do Tiers Live?

The requirements list this as an open question: "Where do cost tiers live:
OSAC, Cost, or both synced?"

This is unresolved in the architectural decisions table. For the PoC, a practical answer is needed to unblock demo setup.

**Options:**

| Option | PoC complexity | Production complexity | Notes |
|---|---|---|---|
| Cost owns tiers (manual DB setup) | Trivial | Medium | No sync needed; tiers seeded via `SeedDefaultRates` or API |
| OSAC owns tiers, Cost reads them | Small | Medium | Requires OSAC to expose a tiers endpoint Cost can poll |
| Both synced | Medium | High | Bilateral sync; conflict resolution needed |

## Implementation Options for PoC

### Option A: Per-Event Only — MaaS Demo, Skip Capacity Tiers

Keep the current implementation. Demo REQ-11 exclusively through MaaS token
rates, where per-event is the correct model. Do not demo capacity tiers
(GiB-month) in the PoC; defer them to post-PoC alongside the rest of the
capacity rate engine work.

**Effort:** None — already implemented for MaaS.
**Constraint:** Capacity tier example from the spec is not demoed and must
be explicitly called out as post-PoC in the requirements. MaaS simulator
should fire batches of ≥1M tokens per event so tier boundaries visibly fire.

### Option B: Cumulative Tiers for Capacity

Before calling `ApplyRate` for capacity meters, query `MeteringSum` for the
tenant/meter/billing period to get the accumulated total. Determine which
tier band the current sweep's delta falls into given the accumulated
position, and price only the marginal contribution.

This requires careful delta accounting to avoid double-counting: the cost
entry records the marginal cost of this sweep's units at the correct tier
position, not the cost of the full accumulated total.

**Effort:** Medium — new accumulated-usage query, delta cost logic in the
rating sweep, updated cost entry attribution.
**Benefit:** Correct behavior for the GiB-month spec example; no demo
scripting constraints for capacity workloads.

## Open Questions

### 1. Tier ownership for PoC

Cost or OSAC? Needs sign-off.

### 2. Demo scope: MaaS only, or capacity tiers too?

If the demo must show capacity tiers (GiB-month), Option B is required and
the effort is medium. If the demo can show tiered pricing exclusively through
MaaS token rates, Option A is sufficient and no code changes are needed.
This decision drives the implementation path.

### 3. MaaS simulator event size

What token batch size does the MaaS simulator emit per event today? If
batches are already ≥1M tokens, tier boundaries fire naturally under Option A.
If batches are small (thousands of tokens), either the simulator needs
adjustment or tier boundaries must be set below the typical event value.

### 4. Seeded tiered rate example

`SeedDefaultRates` currently seeds only flat rates. At least one MaaS rate
(e.g., `maas_tokens_in`) should be seeded with a tiered structure so the
feature is visible in a default deployment without manual DB setup. Without
this, tier support exists in code but is invisible at demo time.

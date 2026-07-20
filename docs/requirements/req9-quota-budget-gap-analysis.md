# Requirement 9: Quota/Budget Status API — Gap Analysis

> **Requirement:** Provide a workflow to allow OSAC to check fleet-level
> quota and budget status before allowing resource creation. Is the tenant
> within quota? Within budget? OSAC should be able to check status of the
> tenant's projects/clusters/VMs/etc and roll up to the tenant level.
> Enforcement remains with OSAC; RHCM provides the data.
>
> **Definitions (from overview):**
> - **Quota** = dimensional limit (CPU core-hours, GiB RAM-hour, tokens,
>   requests, etc.) over a period
> - **Budget** = monetary quota (metered consumption × rates = budget consumed)
> - **Cost** = metered consumption × rates
>
> **Source:** [poc_requirements_overview.md#req-9](poc_requirements_overview.md#req-9-quotabudget-status-api)
> · [COST-7805](https://redhat.atlassian.net/browse/COST-7805)
>
> **Related:** REQ-10 (threshold push — parked) · REQ-11 (cost tiers —
> pricing, not ceilings) · REQ-14 (wallets — prepaid balance)
>
> **Depends on:** REQ-2 (Near-Real-Time Cost Calculation) — **Done**;
> metering entries available for consumption sums

## Boundary with REQ-11 (Cost Tiers) and REQ-14 (Wallets)

| Concept | Answers | Home | After limit is hit |
|---------|---------|------|--------------------|
| **Quota** | How much am I *allowed* to use? | **REQ-9** | OSAC may deny / throttle (Cost only reports status) |
| **Budget** | How much am I *allowed* to spend ($)? | **REQ-9** | Same — status + thresholds; enforcement OUT |
| **Cost tier** | What *rate* applies to usage? | **REQ-11** | Keep serving; charge next band (incl. windowed free→paid) — *if admin configures free→charge* |
| **Wallet** | How much prepaid balance remains? | **REQ-14** | Deduct spend; low-balance alerts |

Do **not** treat wallets as “budgets with no time limit.” That would (1) force
OSAC / tenant admins to operate Cost’s budget abstraction instead of a prepaid
wallet UX, and (2) mis-model settlement — wallet top-up money is already
collected at card charge time, whereas a budget is a ceiling on spend that is
still to be billed. See [REQ-14 in the overview](poc_requirements_overview.md#req-14-wallets-prepaid-balance).

The same windowed pattern (e.g. “1M tokens every 5 hours”) can be configured
as **free→charge** (REQ-11) or **allow→deny** (REQ-9) by the OSAC or Cost
Management administrator. Mode is per configuration, not a global product rule.

**Examples that belong here (REQ-9):**

- “Tenant may use at most 10M input tokens per calendar month”
- “Tenant may make at most 1,000 MaaS requests every 24 hours — then **deny** until the window resets”
- “Tenant budget $5,000/month — report % consumed at 50/70/90/100%”

**Examples that do *not* belong here (→ REQ-11):**

- “First 1M tokens free every 5 hours, **then $10/M**” (keep serving, charge overage)
- “First 20 GiB free, next 100 GiB at $0.08/GiB-month”

Same meters and windows can appear in both systems (a free pricing band *and*
a hard quota). They must not be conflated in the data model or APIs.

## TL;DR

Pull-model quota **status** for a single tenant is largely **Done**:
`GET /api/v1/quotas/{tenant_id}` returns per-meter limit, consumed,
percentage, fixed 50/70/90/100% threshold flags, and fired alerts.
Threshold evaluation also runs in the rating sweep and writes the `alerts`
table. **Jul 31 requires closing the remaining gaps**, not deferring them:
**HTTP CRUD**, **project→tenant roll-up with no project-limit overcommit**,
**fleet-level status**, **monetary budgets**, **configurable (incl. short)
quota windows**, and **configurable thresholds**. Enforcement correctly
remains out of scope (OSAC).

## What We Have

### Schema (`internal/inventory/store.go`)

**`quotas`** — definition of dimensional limits:

| Column | Notes |
|--------|--------|
| `tenant_id` | Required |
| `project_id` | Column exists (`DEFAULT ''`); not used for roll-up queries |
| `resource_type`, `meter_name` | Scope of the limit |
| `limit_value`, `unit` | Ceiling in meter units |
| `period` | Text, default `'monthly'` — not interpreted as a duration parser |
| `effective_from` / `effective_to` | Active window for the *definition* |

**`alerts`** — idempotent threshold firings per `(tenant_id, meter_name, threshold_pct, period)`.

No separate `budgets` table. Monetary budgets are expected to be quotas over
cost (or a dedicated meter); that path is not wired yet.

### Status API

`GET /api/v1/quotas/{tenant_id}` (`handleQuotaStatus` in
`internal/ingest/handler.go`):

```json
{
  "tenant_id": "tenant-acme",
  "period": "2026-07",
  "quotas": [
    {
      "meter_name": "maas_tokens_in",
      "unit": "tokens",
      "limit": 10000000,
      "consumed": 12345.67,
      "percentage": 0.12,
      "thresholds": {"50": false, "70": false, "90": false, "100": false},
      "alerts": []
    }
  ]
}
```

Consumption = `MeteringSum(tenant, meter, monthStart, monthEnd)` — **raw
meter values**, not `cost_entries`.

### IPP balance-check shim

`GET /api/v1/customers/{customerID}/entitlements/{featureKey}/value`
(`handleBalanceCheck`) returns `{hasAccess, balance, usage, overage}` for
MaaS/IPP compatibility. `featureKey` is ignored; all quotas for the tenant
are summed. Balance is in **meter units**, not currency.

### Threshold evaluation

`evaluateThresholds` in `internal/rating/rating.go` runs after a rating
sweep when there are no unrated entries. Fixed levels:
`ThresholdLevels = []float64{50, 70, 90, 100}`. Inserts into `alerts` with
`ON CONFLICT DO NOTHING`. Prometheus: `AlertsFiredTotal`.

### Seeding

`SeedDefaultQuotas` seeds six meters × four demo tenants when the table is
empty — all `period = "monthly"`, `project_id = ""`:

| meter_name | limit | unit |
|---|---|---|
| `vm_cpu_core_seconds` | 360,000 | core_seconds |
| `vm_memory_gib_seconds` | 1,440,000 | gib_seconds |
| `vm_uptime_seconds` | 86,400 | seconds |
| `maas_tokens_in` | 10,000,000 | tokens |
| `maas_tokens_out` | 5,000,000 | tokens |
| `maas_requests` | 100,000 | requests |

### Tests (representative)

| Test | Covers |
|------|--------|
| `TestQuotaStatus` | Response shape, thresholds map, alerts slice |
| `TestQuotaStatusWithConsumption` | Consumed > 0 after metering |
| `TestEvaluateThresholds_FiresAlerts` | Alert row after 90% consumption |
| `TestBalanceCheckResponseFormat` | IPP entitlement response fields |
| `TestThresholdLevels` | Constant is `[50,70,90,100]` |

Not covered: HTTP CRUD, project roll-up, monetary/`CostSum` budgets,
non-monthly windows, alert idempotency on second sweep, balance overage.

## Coverage vs Gaps

| Capability | Required | Status | Notes |
|---|---|---|---|
| Read-only quota status API (per tenant) | Yes | **Done** | `GET /api/v1/quotas/{tenant_id}` |
| Threshold flags 50/70/90/100% on status | Yes | **Done** | Computed inline; also persisted via `evaluateThresholds` |
| Alert history on status response | Nice-to-have | **Done** | `alerts` table + embed in `QuotaStatus` |
| Sub-second latency | Yes | **Partial** | No cache; N+1 `MeteringSum` per quota; no quota bench |
| CRUD API for RHCM to manage quotas/budgets | Yes | **Gap** | `UpsertQuota` in store only; no POST/PUT/DELETE HTTP |
| Configurable thresholds (not hardcoded) | Yes (overview) | **Gap** | `ThresholdLevels` compile-time constant |
| Project-scoped quotas + roll-up to tenant | Yes | **Gap** | `project_id` column unused; sums are tenant-only |
| Σ(project limits) ≤ tenant limit (no overcommit) | Yes | **Gap** | Product rule (Jul 20): project limits must not sum above tenant; not validated in API today |
| Fleet-level / cross-tenant status for OSAC | Yes | **Gap** | No list-all endpoint; `AllTenantsWithQuotas` is internal only |
| Monetary budgets (cost-based limits) | Yes (**Jul 31**) | **Gap** | Product: budget = monetary ceiling (same idea as usage quota); wiring TBD |
| Non-monthly quota periods (e.g. 5h / 24h) | Yes (**Jul 31**) | **Gap** | Status + sweeper always use calendar month UTC |
| Dimensional quota over tokens/requests (monthly) | Yes | **Done** | Seeded + status path works for MaaS meters |
| CRUD + thresholds + roll-up + budgets + short windows | Yes (**Jul 31**) | **Gap** | All in scope for PoC deadline — not deferred |
| Enforcement / hard stop | No (OUT) | N/A | Correctly left to OSAC (OPA / check-balance) |
| Grace periods | No (OUT) | N/A | Overview marks OUT |
| Budget/limit definition UI | No (OUT) | N/A | API/config acceptable |

## Gap Details

### 1. CRUD missing on the HTTP surface

Overview requires RHCM to implement quota definition regardless of OSAC.
Store layer can insert (`UpsertQuota`); ingest API cannot. Operators today
rely on `SeedDefaultQuotas` or direct SQL. Blocker for non-demo tenants and
for Professional Services sync from a third system.

**Pau's quota model (Jul 20, 2026):** A quota is created with 5 values:

| Field | Example | Maps to |
|-------|---------|---------|
| **Name** | "MaaS token allowance" | Display name (new field) |
| **Amount** | 20 | `limit_value` on `QuotaRecord` |
| **Metric** | Mtoken | `meter_name` + `unit` |
| **Cycle** | 5 hours, 24 hours, 7 days | `period` on `QuotaRecord` — parsed by `billing.ResolvePeriod` |
| **Policy** | "deny" or "charge extra" | "deny" = REQ-9 quota (OSAC enforces via status API); "charge extra" = REQ-11 cumulative tier (free band then paid overage) |

The **Policy** field is the key insight: quotas and tiers are the same
concept with different enforcement. An admin creates one "quota" and
chooses what happens when exceeded — deny (OSAC checks status and blocks)
or charge (rating applies overage tiers). Both use the same meter, the
same cycle/period, and the same amount as the free-tier boundary.

**Implementation note:** The CRUD API should accept this 5-field model.
Internally, "deny" creates a `QuotaRecord`; "charge extra" creates both
a `QuotaRecord` (for status reporting) and a `RateRecord` with
`tier_mode="cumulative"` and a free tier matching the amount.

### 2. Project → tenant roll-up (no limit overcommit)

Overview: quotas/budgets scoped to tenants and projects; projects roll up.
**Decision (Jul 20, 2026):** sum of project-level *limits* must not exceed
the tenant-level limit — no overcommit of limits across projects.
Consumption roll-up (Σ project usage ≤ tenant ceiling) still applies.

Code paths filter and sum by `tenant_id` only. `project_id` on `quotas` and
on `metering_entries` is not composed into a hierarchy response, and nothing
rejects a set of project limits that would exceed the tenant.

### 3. Monetary budgets vs usage quotas

Overview distinguishes **Quota** (dimensional) from **Budget** (monetary).
Ronnie (Jul 14): usage quotas need sync pre-check (pull API); budgets tolerate
eventual consistency (REQ-10 also viable).

**Product recommendation:** treat a budget as the same kind of thing as a
usage quota — a **ceiling** — just measured in money instead of tokens or
core-hours. Admins should not need a wholly separate product concept; they
configure “spend at most $N per period” the same way they configure “use at
most N tokens per period.” Exact storage/API shape is an engineering detail
(see Open Questions).

Today the status path only evaluates usage (meter) consumption, so “% of
budget consumed” is not yet available end-to-end — **Jul 31 gap**.

### 4. Period / window flexibility

~~Seeded and evaluated periods are **calendar month**.~~ **Updated (Jul 20):**
`billing.ResolvePeriod` now handles `”monthly”`, `”weekly”`, `”daily”`,
and hour durations (`”5h”`, `”24h”`, etc.). The rating sweep and quota
status handlers use per-quota/per-rate periods instead of hardcoded
monthly (PR #68).

**Still needed:** `”Nd”` day durations (e.g. `”7d”` = 7 days, for
Pau’s cycle model) — `ResolvePeriod` currently supports `”Nh”` (N
hours) but not `”Nd”` (N days). Adding it is a small extension.

**Concrete example — Codex rolling window (from Pau’s feedback):**
OpenAI Codex uses a **dual-window** model: a rolling 5-hour window
(tokens used are added back 5h later as they roll out) AND a weekly
allowance that fully resets on a fixed 7-day cycle. This maps to two
quota records on the same meter: one with `period: “5h”` (rolling) and
one with `period: “7d”` (fixed reset). The rolling semantics require
`MeteringSum` to use a sliding window (`now - 5h` to `now`) rather
than a calendar-aligned period start. The fixed weekly reset uses
`ResolvePeriod(“7d”)` which snaps to a 7-day boundary.

A REQ-9-style limit such as “1,000 requests every 24 hours then deny”
now works with `period: “24h”` on the quota record. Remaining gap is
`MeteringSum` bounds matching that window. This is the quota-side
counterpart to REQ-11’s windowed **pricing** bands — same clock idea,
different question (ceiling vs rate).

**PoC scope note (Pau, Jul 20):** “remember this is a PoC. We don’t
need to implement every single case.” The mechanism (configurable
periods on quota records) is the deliverable — not exhaustive coverage
of every window type.

### 5. Fleet-level status

“Fleet-level” in the overview implies OSAC can reason about many
projects/resources under a tenant (and possibly many tenants for a cloud
admin). Today only per-tenant GET exists. Extending to nested
project/resource breakdown and/or an admin list endpoint is unfinished.

### 6. Threshold configurability

Overview: thresholds “as defined by OSAC Cloud Administrator or Tenant
Administrator roles”. Code: fixed `[50,70,90,100]`. Per-tenant or per-meter
threshold lists are a gap (related to REQ-10 config notes).

## Relationship to Existing Code Paths

```
metering_entries ──MeteringSum──► handleQuotaStatus / evaluateThresholds
                                 (usage quotas — Done path)

cost_entries ──CostSum──► (unused) ──► monetary budgets — Gap

rates / applyTieredRate ──► cost_entries only (REQ-11)
                            no reads of quotas table
```

Quotas and tiers do not cross-call. Shared meter names (e.g.
`maas_tokens_in`) mean the same usage feeds both pricing and quota
consumption independently.

## Open Questions

### 1. Windowed MaaS: free→charge vs allow→deny — **resolved (Jul 20)**

Either mode is valid. The OSAC or Cost Management administrator chooses
per rate / per quota configuration. REQ-11 covers free→charge; REQ-9 covers
allow→deny. Both need configurable windows (incl. 5h / 24h) for Jul 31.

### 2. How is a monetary budget represented?

**Product lean:** budget = monetary ceiling (same idea as usage quota;
currency instead of meter units).

**Still open (implementation, not product):** whether that is modeled as the
same quota resource with a money unit, a sibling “budget” resource, or
another shape. Decide in design; do not block the Jul 31 requirement that
monetary spend limits must be queryable and manageable.

### 3. Project limit overcommit — **resolved (Jul 20)**

Sum of project-level limits **must not** exceed the tenant-level limit.
API/CRUD should reject or prevent overcommit; status should expose both
levels with that invariant.

### 4. Per-feature vs tenant-level balance / entitlements

Do administrators need separate balances or entitlements per feature/SKU
(e.g. different MaaS offerings), or is a single tenant-level (and
project-rolled-up) balance enough for the PoC? **Unresolved — product
decision.** Affects whether entitlement checks are feature-scoped.

### 5. Demo / Jul 31 scope — **resolved (Jul 20)**

**All** REQ-9 capabilities in the Coverage table that are marked Required
are in scope for Jul 31 (status, CRUD, roll-up with no overcommit, fleet
view, monetary budgets, short windows, configurable thresholds). Nothing
in that list is deferred for the PoC deadline.

## References

- Overview: [REQ-9](poc_requirements_overview.md#req-9-quotabudget-status-api)
- Threshold push (parked): [req10-threshold-notifications-analysis.md](req10-threshold-notifications-analysis.md)
- Pricing tiers: [req11-cost-tiers-gap-analysis.md](req11-cost-tiers-gap-analysis.md)
- Cody design: [boundary_monitoring/](../poc_architecture/boundary_monitoring/)
- Code: `internal/ingest/handler.go` (`handleQuotaStatus`, `handleBalanceCheck`),
  `internal/rating/rating.go` (`evaluateThresholds`, `SeedDefaultQuotas`),
  `internal/inventory/store.go` (`quotas`, `alerts`, `MeteringSum`, `CostSum`, `UpsertQuota`)

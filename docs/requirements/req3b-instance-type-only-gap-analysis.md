# REQ-3b Follow-up: `ComputeInstance` Dropping CPU/Memory — Gap Analysis

> **Requirement:** OSAC is removing `cores`/`memory_gib` from
> `ComputeInstanceSpec` entirely; the only measured/billable unit on a VM
> becomes `instance_type`. Cost Management's cost calculation must work
> purely from `instance_type` and must not silently break (e.g. degrade to
> $0 supplementary cost) once the fields disappear.
>
> **Source:** [poc_requirements_overview.md#req-3b](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-3b-service-catalog-sync-from-osac)
> · [osac-open-questions.md#23](osac-open-questions.md)
>
> **Action item (Jul 14, 2026 meeting):** Martin to verify RHCM's cost
> calculation works purely from `instance_type`. This doc is that
> verification.

## Implementation Progress (updated Jul 22, 2026)

> Gaps 1, 2, and 5 from this analysis have been addressed in PR #59:
> - **Gap 1 (metering catalog fallback):** Done — `computeInstanceMeters`
>   resolves cores/memory from `InstanceType` catalog when `cores == 0`
> - **Gap 2 (per-SKU rate dimension):** Done — `instance_type` dimension
>   on `rates` table with 4-way fallback in `matchRate`
> - **Fix 1 (tests):** Done — `TestComputeInstanceMeters_CatalogFallback`,
>   `TestComputeInstanceMeters_CatalogMissStaysZero`,
>   `TestMatchRate_InstanceTypeSpecificTakesPrecedence`
> - See [rate configuration guide](../rate-configuration-guide.md) for
>   the three pricing models now supported
>
> Remaining: Fix 3 (reconciler refresh), Fix 4 (guard metric), Fix 6
> (instance_type on heartbeat CloudEvents)
>
> **Open Question #1 resolved:** Instance-type pricing is the primary
> billing model. Cost = `vm_uptime_seconds` × per-instance-type rate.
> CPU/memory meters are emitted for capacity reporting at $0 rates.
> See [Rate Configuration Guide — Option 1](../rate-configuration-guide.md#option-1-flat-rate-per-instance-type-recommended).

## How Instance-Type Pricing Works

With OSAC dropping `cores`/`memory_gib` from `ComputeInstance`, the
billing model is:

```
OSAC event: ComputeInstance { instance_type: "m5.xlarge", ... }
                │
                ▼
Metering sweep: vm_uptime_seconds = duration (always works)
                vm_cpu_core_seconds = 0 (no cores from OSAC; catalog optional)
                vm_memory_gib_seconds = 0 (same)
                │
                ▼
Rating sweep: matchRate(tenant, "m5.xlarge", "compute_instance", "vm_uptime_seconds")
              → finds rate $0.50/hr for m5.xlarge
              → cost = duration × $0.50/3600
```

**No catalog lookup required for billing.** The `instance_type` string
on the metering entry is matched directly against the `instance_type`
column on the `rates` table. The 4-way fallback (tenant+SKU → SKU →
tenant → global) ensures a rate is always found as long as either a
per-SKU or global default rate exists.

**Setup:** one `INSERT INTO rates` per instance type. See
[Rate Configuration Guide](../rate-configuration-guide.md) for
complete examples including tenant overrides and tiered pricing.

**Catalog fallback (for capacity reporting only):** if the InstanceType
catalog is populated (via OSAC reconciler), the metering sweep resolves
cores/memory and emits non-zero `vm_cpu_core_seconds` /
`vm_memory_gib_seconds` entries. These are useful for dashboards
("how many total core-hours across the fleet?") but should be priced
at $0 when using instance-type pricing to avoid double-counting.

## TL;DR (written before PR #59 — some gaps below are now closed)

**It will break.** Today `vm_cpu_core_seconds` and `vm_memory_gib_seconds`
are computed in the metering sweep as `cores × duration` and
`memory_gib × duration`, where `cores`/`memory_gib` are read straight off
`ComputeInstance.spec` and stored on `inventory_compute_instance`. When
OSAC stops sending those fields, every VM's `cores`/`memory_gib` columns
go to `0` (nil pointers decode to the zero value), and those two meters —
currently ~40% of a VM's Infrastructure+Supplementary cost per
`SeedDefaultRates` — silently and permanently zero out. `vm_uptime_seconds`
is unaffected (duration-only).

There is a partial safety net already in the codebase: an `instance_type`
catalog (`inventory_instance_type`, populated from OSAC's `InstanceType`
Watch/List) already stores `cores`/`memory_gib` per instance type, and the
**summarizer** already falls back to it when `inst.Cores == 0`. But that
fallback exists in exactly one place — the daily-summary path — and not in
the metering sweep, which is what actually produces billed cost. Rating
also has no notion of "rate per `instance_type`" today; it only rates flat
meter names. REQ-3b's own acceptance criteria ("price the catalog item, not
a function of raw CPU/memory... a VM with 4 vCPUs might cost 3x a VM with
2 vCPUs") is marked **Done** in `implementation-status.md`, but that only
reflects catalog *sync*, not catalog-item-based *pricing* — the pricing
model REQ-3b actually requires does not exist yet. This gap analysis is as
much about that pre-existing gap as it is about the field removal itself.

## Background: What's Changing in OSAC

Today, `ComputeInstanceSpec` carries hardware size inline:

```protobuf
message ComputeInstanceSpec {
    string template = 1;
    string catalog_item = 2;
    optional int32 cores = 3;
    optional int32 memory_gib = 4;
    string instance_type = 5;
}
```

Per Moti (Jul 14 meeting), the plan is to drop `cores`/`memory_gib` from
this message. The only remaining sizing signal on a `ComputeInstance` is
`instance_type` (a string ID referencing an `InstanceType` catalog entry,
which itself has `spec.cores`/`spec.memory_gib`). This mirrors how
`BareMetalInstance` already works — see
[req8-bare-metal-gap-analysis.md](req8-bare-metal-gap-analysis.md), which
notes bare metal hardware specs live on a `catalog_item` reference, not
inline on the instance. OSAC appears to be moving VMs to the same pattern.

No PR/tracking link has been shared yet (Martin asked Moti for one — still
outstanding). This analysis assumes the shape above; revisit if the actual
change differs (e.g. if `cores`/`memory_gib` become deprecated-but-present
rather than removed, the impact is softer — see "Detection" below).

## Impact Map

```
OSAC ComputeInstance.spec.cores / memory_gib removed
        │
        ▼
types.go: Cores/MemoryGib become permanently nil
        │
        ├─ watcher.upsertComputeInstance()      → stores cores=0, memory_gib=0
        ├─ reconciler (new-VM path only)        → stores cores=0, memory_gib=0
        │
        ▼
inventory_compute_instance.cores = 0, memory_gib = 0   (for all VMs, old and new)
        │
        ├─ metering.computeInstanceMeters()
        │     vm_uptime_seconds      = duration            ← unaffected
        │     vm_cpu_core_seconds    = 0 × duration = 0     ← BROKEN
        │     vm_memory_gib_seconds  = 0 × duration = 0     ← BROKEN
        │
        ├─ rating.ApplyRate() on the two meters above → $0.00 forever
        │     (no error, no log, no metric — silent)
        │
        ├─ summarizer (daily_usage_summary)
        │     already falls back to inventory_instance_type when cores==0
        │     → CPU/memory-hours in the daily summary stay correct
        │     → but daily_usage_summary is NOT the billing path (see below)
        │
        └─ ingest.handleComputeInstanceEvent() (heartbeat CloudEvents)
              still expects cores/memory_gib inline on the event payload;
              unaffected by the Watch/REST spec change unless the metering
              collector emitting these events also changes independently
```

**Net effect if nothing changes:** VMs keep running, uptime is billed
correctly, but `cpu_core_request_per_hour` and `memory_gb_request_per_hour`
— the two Supplementary-cost line items — bill $0 for every VM, with no
error surfaced anywhere. This is the "silently break" scenario the action
item is explicitly worried about.

## Everything Touched

| Area | File | Current Behavior | Impact |
|---|---|---|---|
| OSAC types | `internal/osac/types.go` | `ComputeInstanceSpec.Cores *int32`, `.MemoryGib *int32` | Fields become permanently `nil`; can be deleted once safe, but removing them early breaks any code still dereferencing them |
| Watcher | `internal/watcher/watcher.go` (`upsertComputeInstance`) | Dereferences `ci.Spec.Cores`/`MemoryGib` (nil-safe today, defaults to 0) | Will store 0/0 for every VM create/update |
| Reconciler | `internal/reconciler/reconciler.go` | Same dereference, but **only for VMs missing from inventory** — never refreshes existing rows | Existing VMs already in inventory (if seeded from old-API cores/memory) never get overwritten to 0 by the reconciler; new VMs after the OSAC change come in as 0 immediately |
| Metering sweep | `internal/metering/metering.go` (`computeInstanceMeters`) | `cores := inst.Cores; memGiB := inst.MemoryGiB` → multiplies by duration | **Primary breakage.** No `InstanceType` catalog lookup exists here at all |
| Final/delete metering | `internal/metering/metering.go` (`MeterComputeInstanceFinal`) | Same formula, on delete | Same breakage on the last metering entry for a VM |
| Rating | `internal/rating/rating.go` (`SeedDefaultRates`, `ApplyRate`) | Flat rate per meter name only; no `instance_type` dimension | Continues to "work" (no errors) but rates a zero quantity → $0. No per-`instance_type` rate concept exists to price the catalog item instead |
| Inventory schema | `internal/inventory/store.go` / `models.go` | `inventory_compute_instance.cores`/`memory_gib` are `NOT NULL DEFAULT 0`; `instance_type` also stored | Schema doesn't need to change, but semantics of `cores`/`memory_gib` change from "measured value" to "always 0, dead column" |
| Instance type catalog | `inventory_instance_type` (already synced) | Has `cores`/`memory_gib` per `instance_type`, kept current by watcher + reconciler | **This is the fix** — already exists, just not wired into metering/rating |
| Summarizer | `internal/summarizer/summarizer.go` | Falls back to `inventory_instance_type` when `cores == 0` | Already correct, but feeds `daily_usage_summary`, a reporting table — not the metering/rating billing path |
| Ingest (heartbeats) | `internal/ingest/handler.go` (`ComputeInstanceEventData`, `handleComputeInstanceEvent`) | Expects `cores`/`memory_gib` inline in CloudEvent payload; stores them, and separately expects precomputed `cpu_core_seconds`/`memory_gib_seconds` | Independent of the Watch/REST spec — only breaks if the metering-collector CloudEvent schema also drops these fields. No `instance_type` field exists on this event schema at all today |
| Reports/API | `internal/inventory/store.go` (breakdown queries), `docs/api-reference.md` | `GET /reports/breakdown` surfaces `meter_name`/`metered_value`; quotas example references `vm_cpu_core_seconds` | Reports keep working structurally but show 0/near-0 values for CPU/memory meters, with no distinguishing signal that this is "expected" vs. a bug |
| Tests | `internal/metering/metering_test.go` (`TestComputeInstanceMeters`, `TestComputeInstanceMeters_ZeroCores`), `internal/ingest/handler_test.go`, `internal/rating/rating_integration_test.go` (`TestSeedDefaultRates`) | Construct fixtures with explicit `cores`/`memory_gib` values | `TestComputeInstanceMeters_ZeroCores` already documents current zero-cores behavior as *expected* for a single instance — it will need to become the *normal* case, and new tests are needed for the catalog-lookup fallback path once implemented |
| Simulators/scripts | `snippets/create-test-data.sh`, `setup-demo-data.sh`, `test-inventory-watcher.sh`, `demo-req1-record.sh`, `demo-rating-features.sh`, `demo-scenario-3-dashboard.sh`, `integration-test/test.sh` | All create VMs/instance types by posting explicit `cores`/`memory_gib` | Demo scripts that post inline `cores`/`memory_gib` on `ComputeInstance` creation calls will no longer reflect the real OSAC contract; scripts that only set `cores`/`memory_gib` on `InstanceType` (catalog) remain valid |
| Docs | `docs/grpc-messages-catalog.md`, `docs/data-model.md`, `docs/cloudevents-catalog.md`, `docs/poc_architecture/event-types.md`, `docs/poc_architecture/architecture.md`, `docs/poc_architecture/metering/metering-spec-draft.md`, `docs/poc_architecture/metering/cost_model_metric_feasibility.md`, `docs/poc_architecture/reporting/cost-reports-feasibility.md` | Document `spec.cores`/`spec.memory_gib` as consumed fields and describe meters as `cores × duration` | All need updates once the fix lands — see Documentation Maintenance section below |

## Root Cause: Two Separate Gaps, Not One

### Gap 1 — Metering never looks up the `InstanceType` catalog

`computeInstanceMeters` reads `inst.Cores`/`inst.MemoryGiB` directly off
`ComputeInstanceRecord`. The summarizer already demonstrates the fix
pattern:

```go
cores := inst.Cores
memGiB := inst.MemoryGiB
if cores == 0 && inst.InstanceType != "" {
    if it, ok := typeMap[inst.InstanceType]; ok {
        cores = it.Cores
        memGiB = it.MemoryGiB
    }
}
```

The metering sweep needs the equivalent: when `cores`/`memory_gib` are
zero (or, longer-term, unconditionally — see Gap 2) and `instance_type` is
set, resolve cores/memory via `GetInstanceType`/an in-memory catalog map
before computing `vm_cpu_core_seconds`/`vm_memory_gib_seconds`.

This is a **mechanical, low-risk fix**: the catalog sync, table, and Go
query (`GetInstanceType`, currently unused in application code per the
codebase scan) already exist. It's billing-critical code, so per
`CLAUDE.md`'s refactoring rules it needs test coverage of current behavior
first — `TestComputeInstanceMeters` and `TestComputeInstanceMeters_ZeroCores`
already exist and should keep passing; new tests are needed for the
catalog-fallback path (hit and miss cases: unknown `instance_type`, empty
`instance_type`, catalog not yet synced when VM event arrives).

### Gap 2 — Rating has no catalog-item-based pricing (REQ-3b's actual ask)

Even with Gap 1 fixed, cost is still computed as
`(cores from catalog) × core-seconds-rate` +
`(memory from catalog) × memory-seconds-rate` — i.e. still a **function of
raw CPU/memory**, just sourced from the catalog instead of the instance
spec. REQ-3b's acceptance criteria explicitly reject this model:

> Prices for catalog items must be set per catalog item, not based on the
> rates that constitute a catalog item — a VM with 4 vCPUs and 16 GiB RAM
> might cost 3x what a VM with 2 vCPUs and 8 GiB RAM [costs].

That is a flat/tiered price *per `instance_type`* (e.g. `m5.xlarge` = a
specific $/hour), not `cores × core_rate + memory_gib × memory_rate`. This
does not exist anywhere in `internal/rating/rating.go` today — rates key
only on `(tenant_id, resource_type, meter_name)`, with no `instance_type`
dimension. `implementation-status.md` marks REQ-3b **Done**, but the
"Done" note says "Catalog items synced via reconciler" — that's catalog
*sync*, which is real and working, but not catalog *pricing*, which is
what the acceptance criteria describe and what does not exist.

**This means the OSAC change doesn't just require a metering fallback
(Gap 1) — it's the forcing function that finally requires building the
catalog-item pricing model that REQ-3b always intended**, per Moti's own
framing in the meeting notes ("this validates the catalog-item-based
pricing approach already required above").

## Coverage vs Gaps

| Capability | Required (per REQ-3b) | Status | Notes |
|---|---|---|---|
| `InstanceType` catalog synced from OSAC | Yes | **Done** | Watch + reconciler, `inventory_instance_type` |
| Metering resolves cores/memory via catalog when absent on instance | Yes (post-change) | **Done** (PR #59) | `computeInstanceMeters` falls back to `InstanceType` catalog when `cores == 0` |
| Metering resolves cores/memory via catalog **unconditionally** (not just cores==0) | Recommended | **Gap** | Current fallback triggers only on `cores == 0`; see "Detection" below |
| Rate keyed on `instance_type` (catalog-item price) | Yes — this is REQ-3b's core ask | **Done** (PR #59) | 4-way fallback: tenant+instance_type > instance_type > tenant > global |
| Reconciler refreshes cores/memory/instance_type on already-known VMs | Implicit | **Gap** (pre-existing) | Reconciler only fills in VMs missing from inventory; stale rows never get corrected drift from the Watch stream outage |
| `instance_type` on ingest heartbeat CloudEvents | No, but relevant | **Missing** | `ComputeInstanceEventData` has no `instance_type` field; can't do catalog lookup from a heartbeat alone today |
| Detection/alerting when a meter silently zeroes out | Not required, strongly recommended | **Gap** | No metric, log, or guard exists today for "billable meter value is 0 for a running instance with no free tier" |
| Test coverage for catalog-fallback metering | Yes (billing-critical) | **Done** (PR #59) | `TestComputeInstanceMeters_CatalogFallback`, `TestComputeInstanceMeters_CatalogMissStaysZero`, `TestComputeInstanceMeters_InstanceTypePropagated` |

## Detection: How Do We Know When OSAC Actually Makes the Change?

There's no version negotiation visible in the current client — the
inventory-watcher doesn't check an OSAC API version. Two practical
signals to watch for once the change lands:

1. **`cores`/`memory_gib` become absent from the JSON payload entirely**
   (not just `null`) — `optional int32` fields already decode missing
   JSON keys to `nil`, so behavior is identical to today's "nil-safe"
   path. There is **no way to distinguish "OSAC removed the field" from
   "this particular VM happens to report 0 cores"** once this happens,
   which is exactly why Gap 1's fallback should trigger whenever
   `instance_type` is set and non-empty, not only when `cores == 0`
   (currently the only trigger condition the summarizer uses). Preferring
   catalog values whenever an `instance_type` is present — with inline
   `cores`/`memory_gib` only as an override if OSAC keeps sending them —
   is more future-proof than a zero-check.
2. **The proto file changes.** Ask Moti/OSAC for the PR (outstanding ask
   per meeting notes) and watch
   [compute_instance_type.proto](https://github.com/osac-project/fulfillment-service/blob/main/proto/public/osac/public/v1/compute_instance_type.proto)
   directly for the field removal landing.

## Recommended Fix (Ordered)

| # | Change | File(s) | Effort |
|---|---|---|---|
| 1 | Add regression tests capturing current `cores × duration` metering behavior (already mostly present) plus new tests for catalog-fallback (hit/miss/empty instance_type) | `internal/metering/metering_test.go` | Small |
| 2 | Metering sweep resolves cores/memory from `inventory_instance_type` via `instance_type`, preferring catalog over instance spec (not just as a `cores==0` fallback) | `internal/metering/metering.go` (`computeInstanceMeters`, `MeterComputeInstanceFinal`) | Small–Medium |
| 3 | Reconciler refreshes `cores`/`memory_gib`/`instance_type` on **existing** compute instance rows, not just newly-discovered ones, so drift (including this OSAC change) propagates without requiring a Watch-stream event | `internal/reconciler/reconciler.go` | Medium |
| 4 | Add a guard/metric: log or increment a counter when a billable, running compute instance produces a `vm_cpu_core_seconds`/`vm_memory_gib_seconds` metering entry of `0` and its `instance_type` isn't resolvable in the catalog — surfaces the "silently break" failure mode instead of hiding it | `internal/metering/metering.go`, `internal/metrics/metrics.go` | Small |
| 5 | **Design catalog-item pricing** (the actual REQ-3b gap): a rate keyed on `instance_type` (flat $/hour or tiered), applied instead of or alongside `cpu_core_seconds`/`memory_gib_seconds` rates. Needs a decision on whether this replaces the two existing meters or is additive (see Open Questions) | `internal/rating/rating.go`, `internal/inventory/store.go` (new rate lookup dimension), possibly a new `rates` structure | Medium–Large |
| 6 | Update `ComputeInstanceEventData` on heartbeat ingest to carry `instance_type` (if the metering-collector CloudEvent schema is going to be revisited anyway) so heartbeat-only paths can also resolve via catalog | `internal/ingest/handler.go` | Small |
| 7 | Documentation sweep per `CLAUDE.md`'s doc-maintenance rules | see below | Small |

## Documentation Maintenance (per `CLAUDE.md`)

Once the fix lands, per this repo's doc-sync rules:

- `internal/metering/metering.go` changes → update
  [req1-osac-integration-gap-analysis.md](req1-osac-integration-gap-analysis.md)
  (metering pipeline description) and
  [req2-maas-costing-gap-analysis.md](req2-maas-costing-gap-analysis.md)
  if shared code paths move
- `internal/osac/types.go` changes (if/when `Cores`/`MemoryGib` are
  actually deleted from `ComputeInstanceSpec`) → update
  [docs/grpc-messages-catalog.md](../grpc-messages-catalog.md) (drop
  `spec.cores`, `spec.memory_gib` from "Key fields consumed" for
  `ComputeInstance`)
- Schema/rating changes → update [docs/data-model.md](../data-model.md)
  and [docs/api-reference.md](../api-reference.md) if new rate lookup
  fields are exposed
- New architecture decision (catalog-item pricing model, meter
  replace-vs-additive) → add an ADR under `docs/decisions/` and link it
  from [docs/implementation-status.md](../implementation-status.md)
- Correct the REQ-3b status in
  [docs/implementation-status.md](../implementation-status.md) and
  [docs/requirements/requirements-comparison.md](requirements-comparison.md) —
  "Done" currently describes catalog *sync* only; catalog-item *pricing*
  is a gap and should be reflected once this doc is reviewed

## Open Questions

1. **Meter replace-vs-additive — RESOLVED:** Instance-type pricing
   *replaces* CPU/memory pricing as the billing model. CPU/memory meters
   are still emitted (for capacity dashboards) but priced at $0. Set up
   per-instance-type rates on `vm_uptime_seconds` — see
   [Rate Configuration Guide](../rate-configuration-guide.md). The
   CPU/memory meters are additive only for reporting, not billing.
2. **Timing/versioning:** Is there a target OSAC release or PR for this
   change? Martin asked Moti for a pointer — still outstanding. Without
   it we don't know if this needs to ship before the Jul 31 PoC or can
   land after.
3. **Existing inventory rows:** Do already-ingested VMs (created under
   the old API, with real `cores`/`memory_gib` values already stored)
   need a one-time backfill/reconciliation pass once the fix lands, or is
   letting them "self-heal" via the next Watch event / reconciler pass
   (once Fix #3 above lands) sufficient?
4. **Should `cores`/`memory_gib` columns be removed from
   `inventory_compute_instance` entirely**, or kept as a legacy/override
   field in case OSAC sends them for some instance types but not others
   during a transition period? Recommend keeping them (nullable-safe,
   already `DEFAULT 0`) and just changing which value wins in the
   catalog-vs-inline decision (Fix #2), to avoid a schema migration for a
   field that may still be useful as an override signal.
5. **Bare metal analogy:** `BareMetalInstanceSpec` already only has
   `catalog_item` (no inline `cores`/`memory_gib`) per
   [req8-bare-metal-gap-analysis.md](req8-bare-metal-gap-analysis.md#blockers).
   Bare metal metering uses `bm_uptime_seconds` only — no CPU/memory
   decomposition — so it's already aligned with the instance-type
   pricing model.

## Effort

**Metering fallback (Fixes 1–4):** Small–Medium, 1–3 days. Billing-critical
code — tests first, per `CLAUDE.md`.

**Catalog-item pricing model (Fix 5):** Medium–Large — this is net-new
rating design, not a mechanical port, and depends on Open Question #1
being resolved before implementation starts.

**Blocked on:** OSAC PR/timeline (Open Question #2) to know how urgent
this is; a pricing-model decision (Open Question #1) before Fix 5 can
start.

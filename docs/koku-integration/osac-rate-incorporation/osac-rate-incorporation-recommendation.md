# Should OSAC-Derived Costs Reuse Koku's Metric Catalog, or Be an Independent Price List?

**Date:** 2026-07-15
**Status:** Recommendation

> **Purpose:** [metric-calculation-comparison.md](metric-calculation-comparison.md)
> deliberately declined to answer this question — it "does not recommend
> whether to reuse Koku's existing metric names for OSAC data or to model
> OSAC rating as an independent price list concept." This document makes
> that call, using the comparison doc's per-metric formulas and the other
> price-list-gap docs as the evidence base.
>
> **Scope guardrail:** this doc is narrowly about the metric/rate **naming
> and semantic-modeling** question — not the broader pipeline/database
> integration question (where does OSAC data physically land, which
> service serves the report API) already covered by
> [../strategy.md](../strategy.md)'s Strategy A–F. It cross-references
> that doc where the two questions intersect but does not re-litigate it.
>
> **See also:**
> [koku_price_list_calculation.md](koku_price_list_calculation.md) —
> Koku's Price List / Rate / cost model architecture.
> [../../poc_architecture/metering/cost_model_metric_feasibility.md](../../poc_architecture/metering/cost_model_metric_feasibility.md) —
> OSAC feasibility split.
> [../../requirements/poc_requirements_overview.md](../../requirements/poc_requirements_overview.md) —
> REQ-3b (catalog pricing), REQ-13 (custom metrics), architectural
> decisions log.

---

## 1. Recommendation

**Extend Koku's `PriceList` model with a `source_type` discriminator:
`operator` (Prometheus/CMMO-fed) or `event_watcher` (OSAC-fed).** A given
`PriceList` is scoped to exactly one source when it's defined. This is
neither "reuse everything" nor "build a disconnected, bespoke price-list
system" — it's independence *at the catalog-scoping level*, while still
reusing Koku's existing `Rate`/`PriceList`/`cost_model`/markup/distribution/UI
machinery underneath.

Concretely, this buys two things directly:

1. **Prometheus-only metrics are structurally excluded from the
   `event_watcher` catalog.** `cpu_core_usage_per_hour`,
   `cpu_core_effective_usage_per_hour`, their memory equivalents, storage,
   and GPU metrics simply aren't offered when a `PriceList` is defined with
   `source_type: event_watcher` — this is enforced by the schema, not a
   convention someone has to remember.
2. **REQ-13 custom metrics get a clean home.** An `event_watcher`-scoped
   `PriceList` isn't constrained to Koku's fixed metric catalog
   (`koku/api/metrics/constants.py`) — arbitrary OSAC CloudEvent-dimension
   metrics can be defined there without touching the `operator` catalog or
   Koku's core metric definitions.

The per-metric formula comparison from `metric-calculation-comparison.md`
is still essential — it's the input that decides *which* metrics populate
the `event_watcher` catalog and in what form (§5–§6 below), rather than a
set of ad hoc naming conventions applied without a schema-level guardrail.

**One thing this recommendation does *not* solve on its own:** if a
customer runs both OSAC and the koku-metrics-operator against the same
underlying resource, `source_type` prevents an `event_watcher` rate from
being misapplied to `operator`-sourced data (and vice versa), but it does
not by itself stop the same resource from being metered — and billed —
twice by two independent pipelines. That's a separate, complementary
problem addressed in §8.

---

## 2. Two deployment scenarios this decision must account for

- **Scenario A — OSAC-only** (no CMMO/koku-metrics-operator deployed).
  This matches the PoC's stated project context — "No Cost Management
  Metrics Operator (CMMO) — OSAC is the sole metric source"
  (`poc_requirements_overview.md`, Project Context). For any given
  resource, exactly one pipeline produces usage/cost data, so the
  reuse-vs-independent question is purely about **semantic correctness**.
  §5–§7 below answer this cleanly — there's no collision risk to guard
  against.
- **Scenario B — OSAC + CMMO coexisting on the same resource** (e.g., a
  customer installs the koku-metrics-operator on an OSAC-provisioned
  cluster/VM that OSAC is also lifecycle-metering). This introduces a
  **double-counting risk that is independent of the naming question**: if
  both pipelines write a cost-contributing row for the same VM/node/day —
  say, both produce a `vm_cost_per_hour`-rated row for the same
  `vm_name` — Koku's cost model SQL sums both and double-bills, regardless
  of whether OSAC's row uses Koku's metric name or an independent one. The
  collision is at the **resource/pipeline level**, not the metric-name
  level.

**Implication:** `source_type` on `PriceList` is the correct and
sufficient mechanism for Scenario A. Scenario B needs an *additional*,
separate safeguard layered on top — a precedence/mutual-exclusivity rule
at the usage-row level (§8) — no matter which catalog scoping is chosen.

---

## 3. Proposed schema change: `source_type` on `PriceList`

Koku's current Price List architecture, per
`koku_price_list_calculation.md` §3:

```180:184:docs/koku-integration/osac-rate-incorporation/koku_price_list_calculation.md
- **`PriceList`** — tenant-scoped, versioned, has `effective_start_date`/`effective_end_date`, `enabled`, `currency`.
- **`Rate`** (`cost_model_rate`) — one row per rate: `metric`, `metric_type` (cpu/memory/storage/gpu/node), `cost_type` (Infrastructure/Supplementary), `default_rate`, optional `tag_key`/`tag_values`.
- **`PriceListCostModelMap`** — links a `CostModel` to 1+ price lists with a `priority`; lowest priority wins for a given calendar day (`PriceListManager.get_effective_price_list`).
- **Resolution**: `CostModelDBAccessor(price_list_effective_on=<month start>)` picks the effective list for that month once per month in `OCPCostModelCostUpdater.update_summary_cost_model_costs`.
- **Dual-write**: today's Cost Model UI still writes `CostModel.rates` (JSON); `CostModelManager` mirrors every change into the primary linked `PriceList` + `Rate` rows so the newer date-aware pipeline and the older UI stay in sync.
```

**Proposed change:** add a `source_type` field to `PriceList`
(`operator` | `event_watcher`), and validate `Rate.metric` against a
per-`source_type` allowed-metric list at write time:

```python
class PriceList(models.Model):
    ...
    SOURCE_OPERATOR = "operator"
    SOURCE_EVENT_WATCHER = "event_watcher"
    SOURCE_CHOICES = ((SOURCE_OPERATOR, "Operator (CMMO/Prometheus)"),
                       (SOURCE_EVENT_WATCHER, "Event Watcher (OSAC)"))
    source_type = models.CharField(max_length=32, choices=SOURCE_CHOICES, default=SOURCE_OPERATOR)
```

This is a natural extension of a model that already has a versioning and
priority-resolution mechanism (`PriceListCostModelMap.priority`,
`PriceListManager.get_effective_price_list`) — `source_type` slots into
that existing resolution path rather than requiring a parallel one. A
`CostModel` for an OSAC-integrated tenant would link to an
`event_watcher`-sourced `PriceList` (and, if that tenant also runs CMMO,
separately to an `operator`-sourced one) via the same
`PriceListCostModelMap` join Koku already has.

**Where the allowed-metric list per `source_type` comes from:** the
metric-by-metric classification in §6, seeded directly from
`cost_model_metric_feasibility.md`'s "Feasible with OSAC" /
"Not Feasible" tables (lines 116–143) and the formula-equivalence findings
in `metric-calculation-comparison.md` §2.

---

## 4. Decision criteria

For each metric family, four questions determine its `event_watcher`
classification:

| Question | If yes → |
|---|---|
| Is the OSAC formula the same shape and units as Koku's, with no ratio/allocation term? | **Include as-is** — same metric name/formula in the `event_watcher` catalog |
| Does the Koku formula ratio a shared resource's cost across co-tenant pods (node/cluster-level)? | **Divergent definition needed** — OSAC's flat per-instance accrual has no ratio term to inherit |
| Does REQ-3b's catalog-item pricing rule (no rate × capacity) apply — i.e., is `instance_type` the sole billable unit? | **New `metric_type` needed** — Koku's Price List Rate model has no flat SKU-priced concept today |
| Does the metric require live telemetry (actual usage, `MAX(usage,request)`, storage, GPU) that OSAC structurally cannot produce? | **Excluded** from the `event_watcher` catalog entirely, regardless of any of the above |

---

## 5. Why this beats a single unscoped catalog ("reuse everything")

Two concrete conflicts in the evidence show why letting `event_watcher`
rates freely reuse *every* Koku metric formula, unscoped, breaks down:

**Ratio-vs-flat structural mismatch.** Per `metric-calculation-comparison.md`
§2.2:

```87:97:docs/koku-integration/osac-rate-incorporation/metric-calculation-comparison.md
**Semantic note:** every OSAC-side formula here needs a **HostType catalog
join** to resolve `cores_per_node` from a node-type identifier (Open
Question 3 in `metering-spec-draft.md`) — this join has no Prometheus
equivalent, since Prometheus gets `node_capacity_cpu_cores` directly from
`kube_node_status_capacity`. More fundamentally, Koku's node/cluster-month
rates are **ratio-based allocations of a shared resource** (a node's cost
is split across the pods that use it, proportional to usage); OSAC's
node/cluster metering is **per-resource-instance flat accrual** — there's
no "other tenant's usage on this node" concept in the OSAC model, so the
ratio term in Koku's formula has nothing to divide against on the OSAC
side. This is a structural difference, not just a data-source difference.
```

If `node_cost_per_month` (say) were reused verbatim in an unscoped
catalog, its Koku definition — `usage_ratio × (monthly_rate /
days_in_month)` — would need a ratio term OSAC cannot supply. Scoping the
catalog by `source_type` is what makes reuse *safe* where the formulas
really are equivalent (§6), instead of forcing every metric through one
formula regardless of source.

**REQ-3b's catalog-item rule vs. core/memory-multiplied metrics.** Per
`metric-calculation-comparison.md` §4.4:

```282:294:docs/koku-integration/osac-rate-incorporation/metric-calculation-comparison.md
**Consequence for the naming-approach question (§5):** Koku's
`cpu_core_request_per_hour`/`memory_gb_request_per_hour`/`vm_core_cost_per_hour`
names all imply a granular core/memory quantity as the rating basis. OSAC's
`instance_type`-only billing is a catalog-item/SKU model, and
[REQ-3b](../../requirements/poc_requirements_overview.md)'s acceptance
criteria explicitly bans "rate × capacity" formulas for catalog items
("not a direct function of rates x capacity, i.e. a VM with 4 vCPUs and 16
GiB RAM might cost 3x what a VM with 2 vCPUs and 8 GiB RAM"). That's a
direct tension with reusing those specific Koku metric semantics for
VM-level allocation costing going forward — it doesn't block `vm_cost_per_hour`/
`vm_cost_per_month` (uptime-only, catalog-item-priced), but it does mean the
core/memory-multiplied metrics need either a catalog join added now to
work at all, or a decision to stop trying to feed them from OSAC.
```

A single unscoped catalog has no way to express "this metric family is
banned for catalog-item pricing" — `source_type` scoping (plus the new
`metric_type` from §4) is what gives OSAC's SKU-priced VMs a metric that
doesn't collide with this rule, without disturbing the `operator` catalog
that has no such constraint.

---

## 6. Metric-by-metric classification table

Classification legend: **Include-as-is** (same name/formula) ·
**Divergent** (needs its own formula under `event_watcher`) ·
**New type** (needs a catalog-item/SKU `metric_type` Koku doesn't have) ·
**Excluded** (Prometheus-only, structurally unavailable to OSAC).

| Metric | Classification | Justification | Citation |
|---|---|---|---|
| `vm_cost_per_hour` | **Include-as-is** | "The cleanest 1:1 mapping in the entire metric set" — both sides compute `uptime_hours × rate` from equivalent uptime signals | comparison doc §2.3 |
| `vm_cost_per_month` | **Include-as-is** | Same uptime-only basis as `vm_cost_per_hour`, just amortized; no core/memory multiplication, so REQ-3b's catalog-item rule doesn't conflict | comparison doc §2.3 |
| `cpu_core_request_per_hour` | **Include-as-is** (once catalog join lands) | "Conceptually the same thing" as Koku's `_request_` metric — a static spec value × elapsed time, just sourced from HostType/`instance_type` catalog instead of `kube_pod_container_resource_requests`. Currently rates to $0 because `ComputeInstance.Spec.Cores` is nil; needs the `instance_type` → `InstanceTypeRecord.Cores` join (`GetInstanceType`) that already exists in `store.go` but isn't wired into `upsertComputeInstance` | comparison doc §2.1, §4.4 |
| `memory_gb_request_per_hour` | **Include-as-is** (once catalog join lands) | Same as CPU request — same join gap | comparison doc §2.1, §4.4 |
| `vm_core_cost_per_hour` / `vm_core_cost_per_month` | **Include-as-is** (once catalog join lands) | "Mathematically identical formulas" to `cpu_core_request_per_hour`/`memory_gb_request_per_hour` (`allocated_cores × uptime_hours × rate`) — differ only in conventional Koku filing (Infrastructure "core cost" vs. Supplementary "core request"), not in shape | comparison doc §2.3 |
| `node_cost_per_month` | **Divergent** | Koku: `usage_ratio × (monthly_rate / days_in_month)` — ratio vs. shared node capacity. OSAC: flat `monthly_rate / days_in_month` per instance, no ratio term available | comparison doc §2.2 |
| `node_core_cost_per_month` | **Divergent** | Same ratio-vs-flat mismatch, at core granularity | comparison doc §2.2 |
| `node_core_cost_per_hour` | **Divergent** | Same; Koku's version is "allocated per pod, priced at node rate" — a ratio-derived allocation, not a flat per-instance quantity | comparison doc §2.2 |
| `cluster_cost_per_month` | **Divergent** | Koku: `usage_ratio × amortized_rate` vs. cluster capacity. OSAC: `sum(node_count × monthly_rate_per_node_type) / days_in_month` — different shape entirely, plus depends on `cluster_worker_node_count`, a meter named in design docs but never emitted by `metering.go` today | comparison doc §2.2, §4.2 |
| `cluster_cost_per_hour` | **Divergent** | Koku: two-level allocation fraction (`pod_share_of_node × node_share_of_cluster × rate`). OSAC: `sum(node_count × cores × rate_per_core_hour)` — no fractional-share concept | comparison doc §2.2 |
| `cluster_core_cost_per_hour` | **Divergent** | Same allocation-fraction basis as `cluster_cost_per_hour`, at core granularity | comparison doc §2.2 |
| VM/instance catalog-item rate (new metric_type; would slot alongside `vm_cost_per_hour`) | **New type** | REQ-3b bans rate × capacity for catalog items; needed once `instance_type` is the sole billable unit and no core/memory catalog join is added (or as the durable default even if it is) | comparison doc §4.4; REQ-3b |
| `project_per_month` | **Divergent** | Koku: `amortized_cost / node_count`, tag-only namespace match. OSAC: existence check (`vm_uptime_seconds`/`cluster_uptime_seconds` > 0 for the period), not a quantity sum — same intent, different mechanism | comparison doc §2.4 |
| `cpu_core_usage_per_hour` | **Excluded** | No live CPU consumption signal from OSAC | comparison doc §3 |
| `cpu_core_effective_usage_per_hour` | **Excluded** | Requires `MAX(usage, request)`; usage term unavailable | comparison doc §3 |
| `memory_gb_usage_per_hour` / `memory_gb_effective_usage_per_hour` | **Excluded** | Same as CPU usage/effective-usage | comparison doc §3 |
| `storage_gb_usage_per_month` / `storage_gb_request_per_month` / `pvc_cost_per_month` | **Excluded** | No PVC/storage concept in OSAC | comparison doc §2.4, §3 |
| `gpu_cost_per_month` | **Excluded** | OSAC doesn't expose GPU device/allocation info | comparison doc §2.4, §3 |
| Markup | **Include-as-is** (mechanism, not a metric) | "Same math would apply equally to OSAC-rated costs — no OSAC-specific blocker"; not yet implemented in the PoC rate/cost-entry schema either way | comparison doc §2.4 |
| Tag-based rates | **Include-as-is** (mechanism, not a metric) | OSAC resources have labels/tags per the feasibility doc's "Available" table; mechanism is source-agnostic, just unimplemented today (`RateRecord` has no `tag_key`/`tag_values`) | comparison doc §2.4 |
| Distribution (Worker/Platform/Storage/Network unallocated pools) | **Excluded** for now | Needs an "unallocated" resource concept OSAC doesn't have today (every resource belongs to a tenant/project from creation) | comparison doc §2.4 |

---

## 7. Why this beats a fully separate, disconnected price-list system

The opposite extreme — building OSAC rating as a wholly independent
system with its own schema, UI, and cost-model machinery, disconnected
from Koku's `PriceList`/`Rate`/`cost_model` — gives up real, already-built
functionality for the metrics that *do* line up cleanly:

- **Loses markup, distribution, and cost-model management UI** that Koku
  already has for the `Include-as-is` metrics in §6 (`vm_cost_per_hour`
  and friends). These would need to be reimplemented from scratch for a
  disconnected system, or left as gaps.
- **Breaks Strategy F's "zero Koku changes" benefit.** `strategy.md`'s
  recommended production path for VMs/clusters depends on OSAC quantities
  being semantically compatible with Koku's *existing* rate metrics so
  they can be written straight into Koku's VM/Pod line item tables and
  picked up by Koku's unmodified cost model SQL:

  ```510:512:docs/koku-integration/strategy.md
  **Koku changes needed for VMs/clusters/BM: ZERO.** The existing pipeline
  processes the data. Cost models with `vm_cost_per_hour` etc. apply
  automatically.
  ```

  A fully disconnected price-list system forecloses this path entirely,
  even for the metrics where it works today.
- **Contradicts the already-resolved naming decision.** Per
  `poc_requirements_overview.md`'s architectural decisions log:

  ```640:640:docs/requirements/poc_requirements_overview.md
  | **Naming and architecture conventions** | All design decisions keep eventual Koku/on-prem integration in mind. Where choices exist, prefer Koku conventions (field names, rate structure, report format). Broader convergence direction to be decided in a separate meeting (EMR). | Jul 2, 2026 meeting — Martin, Pau |
  ```

  A disconnected schema is the opposite of "prefer Koku conventions" —
  `source_type` scoping honors that decision by keeping one schema while
  still giving OSAC room to diverge where it structurally must.

---

## 8. What `source_type` does not solve: coexistence (Scenario B)

`source_type` on `PriceList` governs which metrics are **definable and
selectable** per source. It does not prevent the same physical resource
from being metered by both the `operator` and `event_watcher` pipelines
simultaneously and double-counted — that's a usage-row/pipeline-level
problem, not a catalog-scoping problem, and the two need separate fixes.

Koku already has a precedent for keeping two sources of cost for the same
infrastructure distinct rather than summing them blindly: correlated
cloud-provider cost (`infrastructure_raw_cost`) is kept separate from
cost-model-derived cost (`cost_model_cpu_cost`, `cost_model_memory_cost`,
etc.) rather than merged into one column. The same pattern — an explicit
discriminator that determines which pipeline's row is authoritative for a
given resource-day — is the right shape of fix here, e.g.:

- A `data_source`-style provenance tag on OSAC-derived usage rows
  (mirroring Koku's existing `data_source` column that already
  distinguishes `'Pod'` from other row types), and
- A precedence rule such as "CMMO wins when installed on a given
  cluster/VM; OSAC's metering for that resource is suppressed or kept
  informational-only" — detected at the cluster/VM registration level,
  not per-metric.

This is **flagged as an open item, not fully resolved here** — it's a new
problem not addressed by the existing price-list-gap docs, and the right
detection/precedence mechanism needs its own design pass. It's called out
explicitly so the `source_type` recommendation isn't mistaken for a
complete answer to Scenario B.

---

## 9. Prerequisite: fix the mapping-validation gap

`source_type` scoping fixes *which catalog a metric belongs to*. It does
not, by itself, fix unit or formula correctness *within* a catalog — the
comparison doc's §4 data-quality findings show that gap exists today and
would silently carry over into an `event_watcher` catalog if left
unaddressed:

- **Unit mismatch** — monthly-named metrics seeded with hourly divisors.
  `koku-rate-schema-alignment.md` documents the intended conversion for
  monthly metrics as `÷ 2,592,000` (30 days in seconds), but the actual
  seed data in `rating.go` divides by `3600` (hourly) instead, for a
  metric named `node_cost_per_month`:

  ```261:261:inventory-watcher/internal/rating/rating.go
  {ResourceType: "cluster", MeterName: "cluster_worker_node_seconds", KokuMetric: "node_cost_per_month", CostType: "Infrastructure", PricePerUnit: 0.10 / 3600, ...},
  ```

  ```186:191:docs/koku-integration/osac-rate-incorporation/metric-calculation-comparison.md
  (`PricePerUnit: 0.05 / 3600`). Functionally, `rating.go`'s flat
  `value × price_per_unit` (`ApplyRate`) has no amortization step at
  all — every seeded rate behaves like an hourly rate regardless of which
  Koku metric name (`_per_hour` or `_per_month`) it's filed under. This
  means the `koku_metric` column today is **descriptive metadata only**; it
  does not yet drive any actual unit-conversion or amortization logic.
  ```

- **A meter named in the design docs that is never emitted in code**
  (`cluster_worker_node_count`, comparison doc §4.2) — any rate keyed to
  it, under any `source_type`, has no quantity to apply against.
- **A Koku metric name in the feasibility doc that doesn't exist in
  Koku's real catalog** (`node_cost_per_hour`, comparison doc §4.3) — a
  concrete example of hand-mapping drift between two catalogs with no
  validation layer enforcing the mapping.

**Recommendation:** before populating the `event_watcher` catalog per §6,
add validation that checks `Rate.metric` (for `source_type: operator`
rows) against Koku's real metric catalog, and make `ApplyRate` unit-aware
(amortization for `_per_month` vs. flat multiply for `_per_hour`) rather
than treating `koku_metric` as descriptive metadata only. Otherwise the
`source_type` split cleanly separates two catalogs that each still
silently miscalculate.

---

## 10. Open questions / who decides what

| # | Question | Why it matters here |
|---|---|---|
| 1 | Who owns the `event_watcher` custom-metric catalog — Cost team or OSAC? | Ties directly to REQ-13's existing open question ("who defines new dimensions to collect: OSAC or Cost team?") — the answer determines who can add metrics to the `event_watcher` `PriceList` catalog this doc proposes |
| 2 | What's the Scenario B precedence mechanism (§8)? | Unresolved — needs its own design pass; blocks safe coexistence of OSAC and CMMO on the same resource |
| 3 | Can tenants override catalog/SKU pricing? | `poc_requirements_overview.md`'s unresolved "Catalog price override by tenant" decision affects how the new catalog-item `metric_type` (§6) is priced and by whom |
| 4 | Does the three-way convergence (SaaS Koku / on-prem Koku / OSAC PoC) change this? | `poc_requirements_overview.md`'s unresolved "Three-way convergence" decision could reshape whether `source_type` belongs on Koku's `PriceList` at all, or on a different shared layer — revisit after that EMR meeting |
| 5 | Who owns the `source_type` migration itself? | This is a Koku core schema change (`PriceList` model, migration, `PriceListManager` resolution logic) — needs Koku codebase ownership, not just Cost AI Grid PoC ownership |

---

## 11. References

- [metric-calculation-comparison.md](metric-calculation-comparison.md) — per-metric formula comparison and §4 data-quality findings (source for §5, §6, §9)
- [cost_model_metric_feasibility.md](../../poc_architecture/metering/cost_model_metric_feasibility.md) — OSAC feasibility split and meter mapping (source for §3, §6)
- [koku_price_list_calculation.md](koku_price_list_calculation.md) — Koku's `PriceList`/`Rate`/`PriceListCostModelMap` model (source for §3)
- [../strategy.md](../strategy.md) — pipeline/database integration strategies (Strategy A–F); Strategy F's VM/cluster reliance on existing Koku metric semantics (source for §7)
- [../../requirements/poc_requirements_overview.md](../../requirements/poc_requirements_overview.md) — REQ-3b (catalog-item pricing ban), REQ-13 (custom metrics), architectural decisions log (source for §5, §7, §10)
- [../../research/koku-rate-schema-alignment.md](../../research/koku-rate-schema-alignment.md) — `koku_metric` mapping column and unit-conversion intent (source for §9)
- [inventory-watcher/internal/rating/rating.go](../../../inventory-watcher/internal/rating/rating.go) — ground truth for what the PoC rater actually seeds today
- [inventory-watcher/internal/metering/metering.go](../../../inventory-watcher/internal/metering/metering.go) — ground truth for which meters are actually emitted today
- [inventory-watcher/internal/watcher/watcher.go](../../../inventory-watcher/internal/watcher/watcher.go) — ground truth for the missing `instance_type` → catalog join (§4.4/§6)

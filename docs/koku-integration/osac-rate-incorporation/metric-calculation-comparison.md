# Metric Calculation Comparison: Prometheus/CMMO Flow vs. OSAC Event-Driven Flow

**Date:** 2026-07-15
**Status:** Research / reference material

> **Purpose:** a neutral, metric-by-metric reference comparing how each Koku
> cost model metric is calculated today (Prometheus → CMMO → Price List rate
> engine) against how its allocation-based counterpart would be calculated
> under the OSAC event-driven flow. This document does not recommend whether
> to reuse Koku's existing metric names for OSAC data or to model OSAC rating
> as an independent price list concept — it lays out the formulas and
> semantic deltas so that decision can be made with the full picture in view.
>
> **See also:**
> [koku_price_list_calculation.md](koku_price_list_calculation.md) —
> full Prometheus/CMMO → Price List rate derivation (source for the left-hand
> column below).
> [../../poc_architecture/metering/cost_model_metric_feasibility.md](../../poc_architecture/metering/cost_model_metric_feasibility.md) —
> feasible/infeasible metric split for OSAC (source for the right-hand column).
> [../../poc_architecture/metering/metering-spec-draft.md](../../poc_architecture/metering/metering-spec-draft.md) —
> OSAC meter definitions and pipeline.
> [../../research/koku-rate-schema-alignment.md](../../research/koku-rate-schema-alignment.md) —
> existing `koku_metric` mapping column and unit-conversion notes.

---

## 1. Pipeline Integrations

There is a structural difference: Prometheus/CMMO derives usage from **live telemetry sampled every minute**, then aggregates seconds into
hours/GB-months before rating. OSAC's flow derives usage from **declared
allocation (HostType spec) × elapsed wall-clock time**, with no sampling
step at all — the quantity is computed directly from lifecycle state, not
observed.

---

## 2. Per-metric formula comparison

For each metric/mechanism in scope, four things are compared:

- **Koku/Prometheus formula** — from `koku_price_list_calculation.md` §7
- **OSAC formula** — from `cost_model_metric_feasibility.md`'s "Feasible with OSAC" table
- **OSAC meter(s) used** — from `metering-spec-draft.md` §5
- **PoC rater today** — whether `inventory-watcher/internal/rating/rating.go`'s `SeedDefaultRates` actually wires this metric up, and how (ground truth from code, not docs)

### 2.1 CPU / Memory allocation rates

| Metric | Koku/Prometheus formula | OSAC formula | OSAC meter(s) | PoC rater today |
|---|---|---|---|---|
| `cpu_core_request_per_hour` | `pod_request_cpu_core_hours × rate` (from `kube_pod_container_resource_requests`) | `allocated_cores × uptime_hours × rate` | `vm_cpu_core_seconds` (= `cores × vm_uptime_seconds`, computed in `computeInstanceMeters`) | **Seeded, but rates to $0** — `vm_cpu_core_seconds → cpu_core_request_per_hour` is wired up (`CostType: Supplementary`), but `allocated_cores` is always zero now that `ComputeInstance.Spec.Cores` no longer exists — see §4.4 |
| `memory_gb_request_per_hour` | `pod_request_memory_gigabyte_hours × rate` (byte-seconds → GiB-hours) | `allocated_memory_gib × uptime_hours × rate` | `vm_memory_gib_seconds` (= `memory_gib × vm_uptime_seconds`) | **Seeded, but rates to $0** — same cause, via `Spec.MemoryGib`; see §4.4 |
| `cpu_core_usage_per_hour` | `pod_usage_cpu_core_hours × rate` (live container CPU) | **Not feasible** — OSAC has no concept below the declared allocation | — | Not applicable |
| `cpu_core_effective_usage_per_hour` | `COALESCE(effective, MAX(usage, request))_hours × rate` | **Not feasible** — requires live usage to compute the max/coalesce | — | Not applicable |
| `memory_gb_usage_per_hour` / `_effective_usage_per_hour` | Same pattern as CPU, on memory columns (`pod_usage_/_effective_usage_memory_gigabyte_hours`) | **Not feasible** | — | Not applicable |

**Semantic note:** Koku's `_request_` metrics are already allocation-based
(the pod's declared `resources.requests`, not measured consumption) — this
is the one CPU/memory family where Prometheus and OSAC measure
*conceptually the same thing* (a static spec value × elapsed time), just
sourced differently (`kube_pod_container_resource_requests` vs. the
HostType spec resolved at VM creation). The `_usage_` and
`_effective_usage_` variants are a different concept entirely (live
consumption, or `MAX(usage, request)`) and have no OSAC analog under any
naming scheme — see §3.

**Current impact:** `cores`/`memory_gib` no longer exist on `ComputeInstance`'s
spec — `instance_type` is now the sole billable unit — see **§4.4** for the
full mechanism and code trace. Both meters in this table
(`vm_cpu_core_seconds`, `vm_memory_gib_seconds`) are always zero as a
result, which silently zeroes this table's two metrics plus
`vm_core_cost_per_hour`/`vm_core_cost_per_month` in §2.3, which share the
`vm_cpu_core_seconds` meter. `vm_cost_per_hour`/`vm_cost_per_month` (§2.3)
are unaffected — they key only off `vm_uptime_seconds`. This also feeds the
last row of §5's decision table.

### 2.2 Node / cluster monthly and hourly rates

| Metric | Koku/Prometheus formula | OSAC formula | OSAC meter(s) | PoC rater today |
|---|---|---|---|---|
| `node_cost_per_month` | `usage_ratio × (monthly_rate / days_in_month)`, ratio = `pod_effective_usage_cpu_core_hours / node_capacity_cpu_core_hours` | `monthly_rate / days_in_month` per instance (flat, no ratio — OSAC has no "shared node with other tenants" concept to ratio against) | `cluster_worker_node_seconds` | **Seeded, but with a naming/unit mismatch** — see §4.1 |
| `node_core_cost_per_month` | `usage_ratio × node_cores × amortized_rate` | `allocated_cores × rate / days_in_month` | `cluster_worker_node_seconds` + HostType `cores_per_node` join | **Not seeded** — no default rate maps to this metric today |
| `node_core_cost_per_hour` | `pod_effective_usage_cpu_core_hours × rate` (allocated per pod, priced at node rate) | `allocated_cores × uptime_hours × rate` | `cluster_worker_node_seconds` + `cores_per_node` | **Not seeded** |
| `cluster_cost_per_month` | `usage_ratio × amortized_rate` (ratio vs. cluster capacity, no tag variant) | `sum(node_count × monthly_rate_per_node_type) / days_in_month` | `cluster_worker_node_count` (snapshot) + HostType catalog | **Not seeded** — and `cluster_worker_node_count` isn't emitted by the metering sweep at all, see §4.2 |
| `cluster_cost_per_hour` | `(pod_share_of_node) × (node_share_of_cluster) × rate` — a two-level allocation fraction | `sum(node_count × cores × rate_per_core_hour)` | `cluster_worker_node_count` + HostType catalog | **Seeded, but under a different meter** — see §4.1 |
| `cluster_core_cost_per_hour` | Same allocation basis as `node_core_cost_per_hour`, priced at cluster scope | `sum(node_count × cores × uptime_hours) × rate` | `cluster_worker_node_count` + HostType catalog | **Not seeded** |

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

### 2.3 VM (KubeVirt) rates

| Metric | Koku/Prometheus formula | OSAC formula | OSAC meter(s) | PoC rater today |
|---|---|---|---|---|
| `vm_cost_per_month` | `amortized_rate` (flat, per matching VM-day, filtered on `vm_kubevirt_io_name` label) | `rate / days_in_month` per VM per active day | `vm_uptime_seconds` | **Not seeded** |
| `vm_cost_per_hour` | `uptime_hours × rate`, uptime from `openshift_vm_usage_line_items.vm_uptime_total_seconds` (KubeVirt) | `vm_uptime_hours × rate` | `vm_uptime_seconds` | **Seeded** — `vm_uptime_seconds → vm_cost_per_hour`, `CostType: Infrastructure`. The one metric where Koku's and OSAC's formulas are **identical in shape and units** |
| `vm_core_cost_per_hour` | `uptime_hours × vcpu_request_cores × rate` | `allocated_cores × vm_uptime_hours × rate` | `vm_cpu_core_seconds` | **Not seeded, and would rate to $0 even if it were** — the `vm_cpu_core_seconds` meter is always zero (§4.4); it's seeded against `cpu_core_request_per_hour` instead, which has the same problem |
| `vm_core_cost_per_month` | `uptime_ratio × vcpu_cores × amortized_rate` | `allocated_cores × rate / days_in_month` | `vm_cpu_core_seconds` | **Not seeded, and would rate to $0 even if it were** — same cause; see §4.4 |

**Semantic note:** `vm_cost_per_hour` is the cleanest 1:1 mapping in the
entire metric set — both sides compute `uptime_hours × rate` from the same
kind of uptime signal (KubeVirt's `kubevirt_vmi_info{phase='running'}` vs.
OSAC's lifecycle-derived `vm_uptime_seconds`). `vm_core_cost_per_hour` and
`cpu_core_request_per_hour` are **mathematically identical formulas**
(`allocated_cores × uptime_hours × rate`) that differ only in which Koku
metric name and `cost_type` they're conventionally filed under
(Infrastructure "core cost" vs. Supplementary "core request") — the PoC's
seed data picked `cpu_core_request_per_hour` for the `vm_cpu_core_seconds`
meter, so the same OSAC quantity could equally well have been named
`vm_core_cost_per_hour` with no formula change. That's a concrete,
direct illustration of the naming-choice question this document supplies
material for.

**Current impact:** `vm_core_cost_per_hour`/`vm_core_cost_per_month` share
the `allocated_cores` dependency called out in §2.1 (full detail in §4.4)
and are already rating to $0 for the same reason. `vm_cost_per_hour`/
`vm_cost_per_month` need only `vm_uptime_seconds` and are unaffected —
making them, along with a catalog-item/`instance_type`-keyed rate, the more
durable half of the VM metric family.

### 2.4 Project-level and other mechanisms

| Metric / mechanism | Koku/Prometheus formula | OSAC formula | OSAC meter(s) | PoC rater today |
|---|---|---|---|---|
| `project_per_month` | `amortized_cost / node_count` (tag-only; namespace-label match) | `rate / days_in_month` per project with any active resource in the window | `vm_uptime_seconds` or `cluster_uptime_seconds` > 0 (existence check, not a quantity) | **Not implemented** — needs a "does this project have ≥1 billable resource this period" query, not a metering-entry sum |
| `storage_gb_usage_per_month` / `storage_gb_request_per_month` / `pvc_cost_per_month` | PVC byte-seconds → GiB-months × rate | **Not feasible** — no PVC/storage concept in OSAC | — | Not applicable |
| `gpu_cost_per_month` | MIG-aware slice-uptime × rate | **Not feasible** — OSAC doesn't expose GPU device info | — | Not applicable |
| Markup | `infrastructure_raw_cost × (markup% / 100)`, flat percentage on top of any rated cost | Same math would apply equally to OSAC-rated costs — no OSAC-specific blocker | — | **Not implemented** — no markup concept in the PoC rate/cost-entry schema |
| Tag-based rates (any metric) | Same formula, flat `rate` swapped for a `CASE` over `pod_labels`/`volume_labels`/`namespace_labels` | Mechanism available (OSAC has `labels`/tags on resources per the feasibility doc's "Available" table), just unimplemented | — | **Not implemented** — `RateRecord` has no `tag_key`/`tag_values`; `matchRate` only keys on `(tenant, resource_type, meter_name)` |
| Distribution (Worker/Platform/Storage/Network unallocated pools) | Post-rate reallocation of unattributed cost pools, proportional to CPU/memory usage share | Would need an allocation-ratio basis; OSAC has no "unallocated" resource concept today (every resource belongs to a tenant/project from creation) | — | **Not implemented** |

---

## 3. Metrics that stay Prometheus-only regardless of naming approach

These metrics require live telemetry, storage monitoring, or GPU device
info that OSAC does not and — per the "Key Insight: Allocation = Request"
section of `cost_model_metric_feasibility.md` — structurally cannot
provide from lifecycle events alone. Whether OSAC-derived rates reuse
Koku's metric names or get their own independent price list, **this list
is unaffected either way**:

| Metric | Blocker |
|---|---|
| `cpu_core_usage_per_hour` | No live CPU consumption signal from OSAC |
| `cpu_core_effective_usage_per_hour` | Requires `MAX(usage, request)`; usage term unavailable |
| `memory_gb_usage_per_hour` | Same as CPU usage |
| `memory_gb_effective_usage_per_hour` | Same as CPU effective usage |
| `storage_gb_usage_per_month` / `storage_gb_request_per_month` | No PVC/storage concept in OSAC |
| `pvc_cost_per_month` | Same |
| `gpu_cost_per_month` | No GPU device/allocation exposure in OSAC |

---

## 4. Data quality notes (observed in current code, not the design docs)

These are concrete inconsistencies found while cross-referencing
`rating.go`'s actual `SeedDefaultRates` against the design docs — useful
signal for the naming-approach research because they show what happens
when Koku metric names get hand-mapped onto OSAC meters without a
validation layer enforcing the mapping.

### 4.1 Unit mismatch: monthly-named metrics seeded with hourly-style rates

`koku-rate-schema-alignment.md` documents the intended conversion for
monthly metrics as `÷ 2,592,000` (30 days in seconds):

```39:39:docs/research/koku-rate-schema-alignment.md
| `node_cost_per_month` | $1000 | Infrastructure | `vm_uptime_seconds` (÷2592000) |
```

But the actual seed data in `rating.go` divides by `3600` (hourly) instead,
for a metric named `node_cost_per_month`:

```261:261:inventory-watcher/internal/rating/rating.go
{ResourceType: "cluster", MeterName: "cluster_worker_node_seconds", KokuMetric: "node_cost_per_month", CostType: "Infrastructure", PricePerUnit: 0.10 / 3600, ...},
```

The same pattern recurs for `bm_uptime_seconds → node_cost_per_month`
(`PricePerUnit: 0.05 / 3600`). Functionally, `rating.go`'s flat
`value × price_per_unit` (`ApplyRate`) has no amortization step at
all — every seeded rate behaves like an hourly rate regardless of which
Koku metric name (`_per_hour` or `_per_month`) it's filed under. This
means the `koku_metric` column today is **descriptive metadata only**; it
does not yet drive any actual unit-conversion or amortization logic.

### 4.2 A meter named in the design docs that is never emitted in code

`metering-spec-draft.md` §5.2 lists `cluster_worker_node_count` as one of
the six core meters ("snapshot from inventory... 1 row per sweep/event"),
and `cost_model_metric_feasibility.md`'s mapping table drives
`cluster_core_cost_per_hour` from it. `clusterMeters()` in `metering.go`
only ever constructs `cluster_uptime_seconds` and
`cluster_worker_node_seconds` entries — a `cluster_worker_node_count`
metering entry does not exist anywhere in the codebase today. Any rate
keyed to that meter name, Koku-named or independent, has no quantity to
apply against yet.

### 4.3 A Koku metric name in the feasibility doc that doesn't exist in Koku

`cost_model_metric_feasibility.md`'s "OSAC Meters → Koku Metrics Mapping"
table lists `node_cost_per_hour` as one of the metrics driven by
`cluster_worker_node_seconds`. Koku's actual metric catalog (per
`koku_price_list_calculation.md` §7, sourced from
`koku/api/metrics/constants.py`) has no flat `node_cost_per_hour` — only
`node_core_cost_per_hour` (per-core) and `node_cost_per_month` (amortized
monthly) exist. This looks like a doc-only typo/shorthand rather than an
implementation gap, but it's worth flagging: it's a small, concrete
example of how easy it is for a hand-maintained mapping between two metric
catalogs to drift from either source of truth.

### 4.4 OSAC's `ComputeInstance` CPU/memory removal already breaks core/memory metrics

OSAC has removed `cores`/`memory_gib` from `ComputeInstance`'s spec —
`instance_type` is now the sole billable unit. This was flagged ahead of
time in the Jul 14, 2026 OSAC meeting note captured in
[poc_requirements_overview.md REQ-3b](../../requirements/poc_requirements_overview.md):

> Moti flagged an upcoming OSAC change removing CPU/memory from
> `ComputeInstance`'s spec entirely — the measured/billable unit becomes
> `instance_type` only... Action item: Martin to explicitly verify RHCM's
> cost calculation works purely from `instance_type` and doesn't silently
> break once CPU/memory fields disappear from the OSAC API.

That action item's silent-break failure mode is exactly what's happening
now. `upsertComputeInstance` in
[watcher.go](../../../inventory-watcher/internal/watcher/watcher.go) reads
`ComputeInstance.Spec.Cores`/`Spec.MemoryGib`, which are now always nil,
and defaults to zero — with no error, warning, or fallback:

```220:226:inventory-watcher/internal/watcher/watcher.go
var cores, memGiB int32
if ci.Spec.Cores != nil {
    cores = *ci.Spec.Cores
}
if ci.Spec.MemoryGib != nil {
    memGiB = *ci.Spec.MemoryGib
}
```

Because those fields are always nil now, every `ComputeInstanceRecord` gets
`Cores: 0, MemoryGiB: 0`. That flows straight into `computeInstanceMeters()`
in [metering.go](../../../inventory-watcher/internal/metering/metering.go)
(`Value: float64(cores) * durationSeconds`), so `vm_cpu_core_seconds` and
`vm_memory_gib_seconds` metering entries are written — as rows with
**value 0** — which then rate to **$0** for `cpu_core_request_per_hour`,
`memory_gb_request_per_hour`, `vm_core_cost_per_hour`, and
`vm_core_cost_per_month`, today. No error surfaces anywhere in the
pipeline; the cost simply, silently, is zero.

The fix already half-exists in the codebase. `models.go` already defines an
`InstanceTypeRecord` catalog (`InstanceTypeID`, `Cores`, `MemoryGiB`), and
`store.go` already implements `UpsertInstanceType`, `GetInstanceType`, and
`ListAllInstanceTypes` against it. `watcher.go`'s `handleCreateOrUpdate`
already populates that catalog whenever OSAC sends an `InstanceType`
resource event:

```143:149:inventory-watcher/internal/watcher/watcher.go
if it := event.InstanceType; it != nil {
    return w.store.UpsertInstanceType(ctx, inventory.InstanceTypeRecord{
        InstanceTypeID: it.ID,
        Name:           it.Metadata.Name,
        Cores:          it.Spec.Cores,
        MemoryGiB:      it.Spec.MemoryGib,
```

What's missing is the join: `upsertComputeInstance` never calls
`GetInstanceType(ci.Spec.InstanceType)` to resolve `cores`/`memory_gib` from
the catalog when the spec fields are absent. This is a code-level gap, not
a docs-level one — noted here because it's the concrete mechanism behind
the naming-approach tradeoffs above, not because this document is
proposing to fix it.

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

---

## 5. References

- [koku_price_list_calculation.md](koku_price_list_calculation.md) — full derivation of every Prometheus/CMMO-fed Price List rate formula
- [cost_model_metric_feasibility.md](../../poc_architecture/metering/cost_model_metric_feasibility.md) — feasibility split and OSAC meter → Koku metric mapping table
- [metering-spec-draft.md](../../poc_architecture/metering/metering-spec-draft.md) — OSAC meter definitions, pipeline, and phasing
- [koku-rate-schema-alignment.md](../../research/koku-rate-schema-alignment.md) — `koku_metric` mapping column and unit-conversion intent
- [poc_requirements_overview.md REQ-3b](../../requirements/poc_requirements_overview.md) — the OSAC `ComputeInstance` CPU/memory removal that motivates §4.4
- [inventory-watcher/internal/rating/rating.go](../../../inventory-watcher/internal/rating/rating.go) — ground truth for what the PoC rater actually implements today
- [inventory-watcher/internal/metering/metering.go](../../../inventory-watcher/internal/metering/metering.go) — ground truth for which meters are actually emitted today
- [inventory-watcher/internal/watcher/watcher.go](../../../inventory-watcher/internal/watcher/watcher.go) — ground truth for how `ComputeInstanceRecord.Cores`/`MemoryGiB` are populated today

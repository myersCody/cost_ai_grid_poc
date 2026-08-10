# Catalog & Metering Integration — Gap Analysis

Response to Moti Asayag's "Catalog & Metering Integration Discussion
Brief" (2026-07). Analyzes how his proposals surface gaps in our
current cost-event-consumer implementation.

**Source document:** [Catalog & Metering Integration — Discussion Brief](https://docs.google.com/document/d/15tubYSfhOF9FzQAtFxjAebOcmEPBizn4FVVi4Ca-UnE)

## What Already Works

| Moti's Proposal | Our Implementation | Status |
|---|---|---|
| Per-tenant pricing (same catalog item, different price per tenant) | `matchRate()` 4-way fallback: tenant+instance_type → instance_type → tenant → global | Working |
| Provider adapter model (OSAC owns catalog, pricing is external) | We ARE the external pricing adapter — consume Watch stream, apply rates, produce costs | Aligned |
| Catalog item reference on bare metal | `inventory_bare_metal_instance.catalog_item` populated from `bm.Spec.CatalogItem` | Working |

## Gap 1: Catalog Item Reference Dropped on Compute Instances

**Problem:** `upsertComputeInstance()` in `watcher.go:234-245` extracts
`ci.Spec.InstanceType` but ignores `ci.Spec.CatalogItem` — even though
the OSAC type (`osac/types.go:74`) already carries it. The
`inventory_compute_instance` table has no `catalog_item_id` column.
We're silently dropping data OSAC already sends.

**Impact:** Can't correlate VM costs to the catalog offering the user
purchased. Cost reports show "m5.xlarge" but not "RHEL 10 VM (Small)".

**Fix:**
- Add `catalog_item_id TEXT` column to `inventory_compute_instance`
  (schema evolution, 1 line)
- Pass `ci.Spec.CatalogItem` through in `upsertComputeInstance()`
  (2 lines in `watcher.go`)
- Carry `catalog_item_id` into `metering_entries` and `cost_entries`
  (add column + pass through, ~10 lines each)

**Effort:** S — straightforward column additions and pass-throughs.

## Gap 2: Mutable Catalog Items vs. Immutable Versioning

**Problem:** `UpsertCatalogItem()` in `store.go` does
`ON CONFLICT DO UPDATE`, overwriting name, title, description, template,
and published flag. Moti proposes immutable versioning — each catalog
item modification creates a new version. Resources retain references
to the specific version they were provisioned from.

**Impact:** If OSAC updates a catalog item (e.g., changes the template
from 4 cores to 8 cores), we lose the original definition. Historical
cost reports can't answer "what was this resource provisioned as?"

**Fix — Option A (minimal, recommended for now):**
- Add `version INTEGER` column to `inventory_catalog_item`
- Change upsert to use `(catalog_item_id, version)` as composite key
  instead of overwriting
- ~20 lines schema + store changes

**Fix — Option B (full versioning):**
- Separate `catalog_item_versions` table with immutable snapshots
- Resources reference `(catalog_item_id, version)` tuple
- Historical queries join through the version
- ~100 lines, needs migration for existing data

**Effort:** S for Option A, M for Option B.

## Gap 3: No Attribution Metadata

**Problem:** Moti proposes three attribution fields on every resource:
- `catalog_item_id` — what offering was provisioned
- `catalog_item_version` — which version of the offering
- `provisioning_group_id` — correlates all resources from one
  provisioning action (VM + boot volume + NIC = one group)

None of these exist in our OSAC types (`osac/types.go`), inventory
tables, metering entries, or cost entries. Go's JSON decoder silently
drops unknown fields, so if OSAC starts sending them, we lose them
without errors.

**Impact:** Can't group multi-resource costs under a single catalog
offering. Can't produce reports like "RHEL 10 VM (Small) = $70 compute
+ $20 storage + $30 license = $120 total."

**Fix:**
- Add `CatalogItemVersion` and `ProvisioningGroupID` to OSAC types
  in `osac/types.go` (~5 lines)
- Add columns to inventory tables, `metering_entries`, `cost_entries`
  (~15 lines schema evolutions)
- Pass through in watcher upserts and metering sweep (~20 lines)
- Add `provisioning_group` as a `group_by` option in the report API
  (~10 lines in `store.go` CostReport query)

**Effort:** M — touches many files but each change is mechanical.

## Gap 4: Hardcoded Meters vs. Catalog-Declared Billable Components

**Problem:** Moti proposes that catalog items declare their billable
components — what resource types they produce and what meters apply:

```
CatalogItem: "RHEL 10 VM (Small)"
├── BillableComponent: compute (meter: instance-type-seconds)
├── BillableComponent: storage (meter: block-storage-gib-seconds)
└── BillableComponent: license (meter: rhel-license-seconds)
```

Our meters are hardcoded in `metering.go`: `computeInstanceMeters()`
always emits uptime/cpu/memory, `clusterMeters()` always emits
uptime/node_seconds/node_count. There's no way to add a "license"
meter to a specific catalog item without code changes.

**Impact:** Can't support catalog items with non-standard billable
components (licenses, premium support, managed services). Every new
meter type requires a code change and deployment.

**Fix — Option A (config-driven, recommended):**
- Store billable component declarations in `inventory_catalog_item`
  as a `billable_components JSONB` column
- Extend the metering sweep to read billable components and emit
  additional meters dynamically
- Similar pattern to our existing REQ-13 custom metrics
  (`internal/custommetrics/`) — config-driven meter extraction
- ~80 lines new code, mostly in metering.go

**Fix — Option B (fully dynamic):**
- Billable components as a separate table with meter definitions
- Metering sweep becomes entirely data-driven — no hardcoded meters
- Rate seeds auto-generated from billable component declarations
- ~200 lines, significant refactor of metering.go

**Effort:** M for Option A, L for Option B. Option A is pragmatic
and leverages our existing custom metrics pattern.

## Gap 5: Rate Lookup Can't Differentiate by Catalog Item

**Problem:** `matchRate()` in `rating.go:146-172` keys on
`(tenant, instance_type, resource_type, meter_name)`. If the same
`instance_type` (e.g., m5.xlarge) appears in two different catalog
offerings at different prices (standard vs. premium), we can't
express that. There's no `catalog_item_id` dimension in the `rates`
table.

**Impact:** Can't price "RHEL 10 VM (Small)" differently from
"Ubuntu 24 VM (Small)" if both use the same m5.xlarge instance type.

**Fix:**
- Add `catalog_item_id TEXT` column to `rates` table
- Extend `matchRate()` fallback chain from 4-way to 5-way:
  1. tenant + catalog_item + instance_type
  2. catalog_item + instance_type
  3. tenant + instance_type
  4. instance_type only
  5. global default
- ~15 lines in rating.go, ~5 lines schema

**Effort:** S — the rate matching logic is already structured for
easy extension.

## Gap 6: No Multi-Resource Provisioning Group

**Problem:** Moti's `provisioning_group_id` concept lets one catalog
item provisioning action create multiple resources (VM + storage +
NIC) that are grouped for cost reporting. We have no concept of
resource grouping — each resource is independent.

**Impact:** Can't produce a unified cost line item for a catalog
offering that bundles multiple resource types.

**Note:** As Moti's doc acknowledges, this concept doesn't exist in
OSAC today either. Current provisioning creates single resources.
This becomes relevant when catalog items become true "bundles."

**Fix (when needed):**
- Carry `provisioning_group_id` through the pipeline (covered in
  Gap 3 above)
- Add `provisioning_group` as a report API `group_by` dimension
- The grouping itself is a reporting concern, not a metering concern

**Effort:** S (once Gap 3 attribution metadata is in place).

## Summary

| Gap | Severity | Effort | When |
|-----|----------|--------|------|
| 1. Catalog item reference dropped | **High** — losing data now | S | Now |
| 2. Mutable catalog items | Medium — no versioning | S–M | When OSAC adds versioning |
| 3. No attribution metadata | Medium — forward-looking | M | When OSAC enriches events |
| 4. Hardcoded meters | Medium — limits flexibility | M | When catalog items declare components |
| 5. Rate lookup ignores catalog | Medium — pricing limitation | S | When catalog-aware pricing needed |
| 6. No provisioning groups | Low — OSAC doesn't have it either | S | When bundles exist |

**Recommended priority:** Fix Gap 1 now (we're dropping data). Gaps
2–5 can wait for OSAC to implement the corresponding features, but
design decisions should account for them. Gap 6 is not urgent.

**Total effort if all gaps addressed:** ~M–L depending on options
chosen. Most changes are mechanical column additions and
pass-throughs. Gap 4 (dynamic meters) is the only one requiring
significant new logic.

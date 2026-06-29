# OSAC Meeting — Discussion Points
**Date:** June 29, 2026
**Audience:** OSAC team
**Goal:** Align on open questions, confirm Phase 4 handoff scope, and unblock blocked work

---

## 1. DELETE Gap — Final Heartbeat on Resource Deletion

**Context:** The current local sweep handles deletion cleanly: when a DELETE event arrives, Cost Management writes a final metering entry covering the gap from `last_metered_at` to the exact deletion timestamp. The OSAC metering collector has no equivalent — if a resource is deleted between two poll cycles, the collector never sees it again and that interval goes unmetered.

**For Phase 4, one of the following must be agreed:**

| Option | Description |
|---|---|
| **Final heartbeat on DELETE** | Collector emits one last CloudEvent when a resource is deleted, timestamped to the moment of deletion. Cost writes the final metering entry from that payload. (Preferred) |
| **Reconciliation sweep (DELETE only)** | Cost keeps a lightweight sweep that detects deletions via the Watch stream and fills the final gap. The full sweep is retired but deletion handling stays local. (Fallback) |

**Discussion points:**
- Can the OSAC collector emit a final CloudEvent on DELETE with the resource's last known spec?
- If not, is the fallback (Cost-side deletion sweep) acceptable?

---

## 2. MaaS (OpenShift AI) — CloudEvent Schema

**Context:** The Cost Management HTTP ingest endpoint already handles `osac.model.lifecycle` events and emits `maas_tokens_in`, `maas_tokens_out`, and `maas_requests` meters. However, the OSAC CloudEvent schema for MaaS events is not yet finalized — the payload field names and token dimension definitions are TBD on the OSAC side.

**Discussion points:**
- What fields will `osac.model.lifecycle` CloudEvents carry? Specifically: token counts (`tokens_in`, `tokens_out`, `cached_tokens`), request count, and model identifier.
- **Who is responsible for collecting RHOAI metrics?** Does OSAC pull them from OpenShift AI (RHOAI) and include them in the CloudEvent, or does Cost Management need a separate integration with RHOAI?
- Is `gpu_vram_gib_seconds` a metric OSAC can provide, or is that out of scope?
- What are the billable states for a model (equivalent to `COMPUTE_INSTANCE_STATE_RUNNING` for VMs)?
- Should MaaS token quotas use the same threshold-event mechanism as compute quotas?

---

## 3. Bare Metal (BMaaS) — CloudEvent Schema

**Context:** Bare metal metering is in-scope (REQ-8) but is completely blocked — the OSAC CloudEvent schema for BMaaS instances does not yet exist. Cost Management is ready to implement metering once the schema is defined; the pipeline would treat BMaaS identically to VMs.

**Discussion points:**
- Is there a target date for OSAC to define the BMaaS CloudEvent schema?
- Expected fields: `resource_id`, `tenant_id`, `project_id`, `cpu_cores`, `memory_gib`, `duration_seconds`, `state`.
- Proposed meters (for alignment): `bm_uptime_seconds`, `bm_cpu_core_seconds`, `bm_memory_gib_seconds`.
- What are the billable states for a bare metal instance?

---

## 4. Quota and Budget Integration

**Context:** Cost Management owns consumption measurement and threshold evaluation. OSAC owns limit definitions and enforcement (via OPA). The proposed model is **push + pull**: Cost pushes threshold events to OSAC for async notifications; OSAC pulls quota status from Cost synchronously at resource creation time to gate provisioning. For the PoC demo, mock limits seeded in Cost unblock the demo today.

**Items that need OSAC to build (for production v1):**

| Component | Purpose |
|---|---|
| Quota/Budget resource CRUD + List API | Cost must sync limits from OSAC as the source of truth |
| Inbound alert webhook | Cost delivers threshold-crossed events (70%, 90%, 100%) |
| Alert state store | Console warnings, firing history |
| OPA extension | Deny/throttle based on alert state or pulled status |
| Pre-create gate calling Cost pull API | Synchronous enforcement at provisioning time |

**Discussion points:**
- **Confirm approach:** push + pull (Option 1) vs. pull-only (Option 2) vs. mock limits for PoC only (Option 4)?
- **Limit List API:** What will the resource shape look like — `Quota` object vs `Limit` object? What are the REST paths?
- **Threshold ownership:** Do thresholds (50%/70%/90%/100%) live on the OSAC limit object, or does Cost own defaults and OSAC can optionally override?
- **Project-scoped limits:** Will v1 support project-level quotas in addition to tenant-level?
- **Alert webhook:** What path and auth mechanism does OSAC plan to expose for the alert receiver?
- **Grace period:** At 100% utilization, is there a grace period before hard enforcement, or is it immediate block?

---

## 5. Rate / Pricing Tier Ownership

**Context:** Cost Management stores rate cards (`rates` table) mapping resource type + meter → price per unit. Tiered pricing is schema-defined and the evaluation logic is implemented. For the PoC, rates are manually seeded. In production, rates need to come from somewhere authoritative.

**Discussion points:**
- **Where do rates live long-term?** Does OSAC maintain a service catalog with pricing, and should Cost Management sync from it via an API? Or does Cost Management own rates independently?
- **Tenant-specific pricing:** OSAC supports instance types and templates. Can OSAC expose per-tenant rate overrides through the service catalog, or will that be a Cost Management concern?
- **Tiered rates (COST-6951):** The tier schema is built and active in Cost. Does OSAC have a concept of tiered pricing in its service catalog, or would Cost Management own the tier definitions entirely?

---

## 6. HostType Catalog Enrichment for Cluster Node Costing

**Context:** Cluster cost metrics (`node_core_cost_per_hour`, etc.) require knowing `cores_per_node` for each node set's host type. The OSAC `instance_types` List API is synced by the reconciler, but the join between a cluster's `node_sets` and the host type spec is not yet working end-to-end for per-node-type cost breakdowns.

**Discussion points:**
- Does the OSAC `InstanceType` resource carry `cpu_cores` and `memory_gib` fields accessible via the List API today?
- Is there a cluster template object that pre-associates node sets to their host type specs, or must Cost Management resolve this itself?

---

## 7. Reporting API — Auth and Tenant Scoping

**Context:** The Cost Management reporting API is designed as a new independent API (not reusing legacy Koku endpoints). All report endpoints are scoped to a `tenant_id`. The quota status endpoint (`GET /quotas/status`) has a hard < 500ms SLA since OSAC calls it synchronously at resource creation time.

**Discussion points:**
- Does OSAC need a **provider-scoped token** to call `/quotas/status` on behalf of any tenant (i.e., cross-tenant reads), or will it always be calling on behalf of the tenant whose resource is being created?
- Are there report formats or groupings OSAC's console will need that aren't covered by: `/costs/summary`, `/costs/breakdown`, `/costs/timeseries`, `/metering/usage`, `/reports/chargeback`, `/quotas/status`?
- Will OSAC's console embed Cost Management reports directly, or consume the API and render independently?


---

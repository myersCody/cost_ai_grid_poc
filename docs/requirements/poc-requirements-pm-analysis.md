# PoC Requirements — PM Analysis

> **Purpose:** A single, plain-language walkthrough of every core PoC
> requirement — acceptance criteria, where implementation actually stands,
> what's missing, and what needs a decision or an answer from someone
> (usually OSAC, sometimes us).
>
> **Source of truth:** [poc_requirements_overview.md](poc_requirements_overview.md)
> (v1.5, Jul 20, 2026). This doc synthesizes that spec together with
> [implementation-status.md](../implementation-status.md),
> [requirements-comparison.md](requirements-comparison.md), the per-requirement
> gap analyses (req1, req2, req8, req9, req10, req11), and
> [osac-open-questions.md](osac-open-questions.md). It does not re-derive
> status from the codebase — it explains and consolidates what those docs
> already say.
>
> **Scope:** The 19 core PoC requirements (POC-ENV through REQ-14, in spec
> priority-rank order). Future Work (REQ-6) and the formal Out-of-Scope list
> are intentionally excluded — see the overview doc for those.
>
> **Last updated:** Jul 20, 2026.
>
> **Acceptance criteria key:** ✅ met today &nbsp;·&nbsp; ⚠️ partially met / met with a caveat worth knowing about &nbsp;·&nbsp; ❌ not met yet

---

## Executive Summary

| Rank | Req | Priority | Title | Status | One-line gap |
|---|---|---|---|---|---|
| 1 | POC-ENV | CRITICAL | On-premise deployment | Partial | CRC (dev/test) deployment done; production Helm/OLM packaging not started — owned by the RHCM platform team, not this component |
| 2 | POC-ARCH | CRITICAL | Capacity-based charging model | Done | Catalog fallback implemented (PR #59) — metering resolves cores/memory from InstanceType catalog when OSAC removes them from ComputeInstance |
| 3 | REQ-1 | CRITICAL | OSAC integration | Done | No known gaps |
| 4 | REQ-1b | CRITICAL | Heartbeat event ingestion | Done | Satisfied via a local 60s sweep, not an actual OSAC-emitted heartbeat event — functionally equivalent, mechanically different |
| 5 | REQ-2 | CRITICAL | Near-real-time cost calculation | Done | No known gaps |
| 6 | REQ-1a | HIGH | Cluster lifecycle via cluster orders | Done | No known gaps |
| 7 | REQ-3a | HIGH | Tenant/project attribution | Partial | Cost attribution works; project-level quotas with roll-up (and no project-limit overcommit) aren't built; Cost Management UI now preferred provider surface; RBAC stays project-scoped |
| 8 | REQ-3 | HIGH | Granular cost tracking | Done | Filterable by tenant/project/user/resource today; spec now also wants any OSAC CloudEvent property (incl. tags) — tag/arbitrary-property filtering not built; user dimension has open PII question (Pau) |
| 9 | REQ-9 | HIGH | Quota/budget status API | Partial | Tenant status API works; Jul 31 scope now also requires CRUD, project→tenant roll-up (no limit overcommit), fleet-level status, monetary budgets, configurable thresholds/windows — see [req9 gap analysis](req9-quota-budget-gap-analysis.md) |
| 10 | REQ-10 | HIGH → Parked | Threshold notification back-channel | Done (pull) | Push/webhook notifications parked — OSAC has no receiver to act on them yet; also the alert path for REQ-14 low-balance thresholds |
| 11 | REQ-13 | HIGH | Custom metrics / custom rates | Done | Only supports "creative math" on existing metrics today; a real backchannel to ask OSAC for brand-new meters doesn't exist |
| 12 | REQ-2a | HIGH | MaaS CloudEvents & token metering | Done (emulated) | Real OSAC/RHOAI MaaS events don't exist yet; we're validated against an IPP plugin + echo LLM, not production inference traffic |
| 13 | REQ-3b | MEDIUM | Service catalog sync | Done | Catalog sync + per-SKU pricing via `instance_type` rate dimension (PR #59) + catalog fallback; rate auto-derivation from catalog not built (manual seeding acceptable for PoC) |
| 14 | REQ-5 | MEDIUM | Chargeback reporting | Done | Project grouping, breakdown endpoint, daily resolution, date filtering (PR #42); CronJob export pattern documented and verified; CSV/JSON via API explicitly in scope |
| 15 | REQ-7 | MEDIUM | Audit trail | Done | No known gaps for PoC scope |
| 16 | REQ-11 | LOW | Cost tiers | Partial | Tiers work correctly for MaaS (per-event); capacity-based cumulative tiers (GiB-month, core-hours) are not implemented and would silently undercharge if configured today |
| 17 | REQ-12 | LOW | Daily OpenShift Virtualization costs | TBD | Still underspecified; PM added neocloud-style finer granularity as desirable and multi-tenant/project rates on shared clusters as a hard constraint |
| 18 | REQ-8 | HIGH → Parked | Bare metal costing | Done (ahead of schedule) | Built already, parked for Jul 31; BMaaS is an Aug 31 OSAC deliverable; standalone (non-OCP) bare metal confirmed IN for post-PoC |
| 19 | REQ-14 | HIGH | Wallets (prepaid balance) | Not started | New in v1.5 (AI Grid MB-005 / COST-7939) — must not be modeled as open-ended budgets; no wallet ledger exists today |

**At a glance (updated Jul 20):** 13 of 19 are Done, 4 are Partial (POC-ENV, REQ-3a, REQ-9, REQ-11), 1 is TBD (REQ-12), and 1 is Not started (REQ-14). Two requirements (REQ-10, REQ-8) remain explicitly parked by joint decision. The biggest Jul 20 impact is REQ-9 scope expansion (all listed gaps in scope for Jul 31) plus brand-new REQ-14 wallets. REQ-3a and REQ-9 still share the project→tenant quota roll-up gap; product rule is now **no overcommit of project limits** above the tenant limit (was previously framed as overcommit-allowed).

**Key changes in v1.5 (Jul 20):** REQ-14 wallets added; REQ-9 reframed as fleet-level status + CRUD + no project-limit overcommit (grace periods OUT); Cost Management UI preferred for provider cost views; CaaS/VMaaS CloudEvents marked available; POC-ARCH explicitly OUT bare metal for PoC.

---

## Critical Priority

### 1. POC-ENV — On-Premise Deployment
**Status: Partial** &nbsp;·&nbsp; Out of scope for this component (owned by the RHCM/Cost Management platform team)

Demonstrate Cost Management running on-premise, driven by OSAC heartbeat events, tuned for a demo rather than feature completeness.

**Acceptance Criteria**
- ⚠️ Cost Management deployed on-premise in a single cluster *(demo-grade via CRC; not yet a production-packaged deployment)*
- ✅ Tuned for performance, not feature completeness
- ✅ Can demonstrate end-to-end flow: consumption → event → ingestion → cost report

**Current Implementation Status**
- CRC (CodeReady Containers) deployment is documented and tested: [deployment checklist](../dev/crc-deployment-checklist.md), [full stack guide](../dev/crc-full-deployment.md), [OSAC-on-CRC guide](../dev/crc-osac-deployment.md), [dev setup guide](../dev/crc-dev-setup.md)
- The RHCM Helm chart / OLM packaging for a true production on-prem deployment has **not started** — this is explicitly RHCM platform team scope, not the consumer component built in this PoC

**Gap Summary**
The demo-capable path (CRC) is done. The production packaging path (Helm/OLM) isn't, but that's a different deliverable, not a gap in this component's work.

**Action Items / Open Questions**
- Need to determine which cluster/environment will host the actual demo
- No open questions raised in the spec for this requirement

---

### 2. POC-ARCH — Capacity-Based Charging Model
**Status: Done**

Charge based on what was provisioned (VM size, cluster config) and for how long — no metric scraping, no CSV pipeline rework. A standalone new data path driven by OSAC heartbeat events. Bare metal charging is explicitly OUT of this PoC path (OSAC does not support it yet) even though bare metal remains an AI Grid PoC requirement tracked under parked REQ-8.

**Acceptance Criteria**
- ✅ Costs calculated from provisioned capacity (instance type, duration) *(catalog fallback implemented — see below)*
- ✅ Heartbeat events from OSAC drive cost calculation
- ✅ No dependency on workload cluster metrics
- ✅ Existing SQL queries adapted to support capacity-based model
- ✅ Demo-ready: show cost for a provisioned cluster/VM within SLA

**Current Implementation Status**
- Standalone Go component (`inventory-watcher`) built specifically for this data path
- Cost calculated from provisioned capacity via `internal/metering/metering.go` (`computeInstanceMeters`, `clusterMeters`)
- Driven by the Watch stream + 60-second metering sweep, not workload-cluster metrics ([ADR-001](../decisions/001-metering-sweep-interval.md))
- Meets the SLA comfortably: under 1ms per event, cost entries produced within 30 seconds

*July 14 meeting: OSAC removes cores/memory_gib from ComputeInstance*
- **Resolved (PR #59, Jul 16):** `computeInstanceMeters` now includes a catalog fallback — when `cores == 0` and `instance_type` is set, specs are resolved from the `InstanceType` catalog (`inventory_instance_type` table, kept in sync by the reconciler). Additionally, the rate engine now supports per-SKU pricing via `instance_type` dimension, so operators can price per catalog item (e.g. $0.50/hr for m5.xlarge) instead of `cores × rate`. See [rate configuration guide](../rate-configuration-guide.md) and [gap analysis](req3b-instance-type-only-gap-analysis.md).

**Gap Summary**
No remaining gaps. The catalog fallback and per-SKU rate dimension ensure cost calculation works correctly regardless of whether OSAC sends inline cores/memory or only `instance_type`.

**Action Items / Open Questions**
- ~~Action item: Martin to verify cost calculation works purely from `instance_type`~~ — **Done** (PR #59): catalog fallback + per-SKU pricing implemented and tested
- OSAC PR timeline for removing cores/memory fields still outstanding (asked Moti, no pointer yet)

---

### 3. REQ-1 — OSAC Integration via Region Management Cluster
**Status: Done** &nbsp;·&nbsp; [COST-7793](https://redhat.atlassian.net/browse/COST-7793)

Connect to the OSAC Region Management Cluster (gRPC/REST) to read inventory, resource state, and the tenant/project hierarchy — not individual workload clusters.

**Acceptance Criteria**
- ✅ Connects to OSAC Region Management Cluster APIs (gRPC/REST)
- ✅ Can read inventory and resource state from OSAC
- ✅ Account/tenant lifecycle synced between OSAC and RHCM; tenant/metering/cost data is never deleted just because a tenant stops sending data or is deleted in OSAC — deletion only happens via a configurable retention policy
- ✅ Workload-level info includes OSAC tenant ID, project ID, resource ID
- ✅ Integration does not degrade the orchestrator's UX

**Current Implementation Status**
- Connected via gRPC Watch stream + REST List endpoints ([`internal/osac/client.go`](../../inventory-watcher/internal/osac/client.go))
- Real-time event consumption plus periodic reconciliation for drift correction ([`internal/reconciler/reconciler.go`](../../inventory-watcher/internal/reconciler/reconciler.go))
- Verified end-to-end: reconciliation imports existing OSAC resources, Watch stream captures real-time creates, inventory correctly tracks cores, memory, tenant, labels, lifecycle timestamps
- All inventory records carry tenant, project, and resource IDs

**Gap Summary**
No functional gaps for PoC scope. The only deferred item (CloudEvents-standard envelope parsing, vs. our current native Watch-stream protobuf parsing) is intentionally low priority — see [req1 gap analysis](req1-osac-integration-gap-analysis.md) — because it only matters if Kafka is introduced later, which we've argued against ([ADR-002](../decisions/002-arguments-against-kafka.md)).

**Action Items / Open Questions**
- CloudEvent types: CaaS and VMaaS schemas are available; BMaaS and MaaS still open
- Consolidated action items: learn the CloudEvents standard; validate CaaS/VMaaS CloudEvents for capacity-based metering; agree on CloudEvents transport with OSAC (Kafka, HTTP, NATS) — see [event transport open questions #13-14](osac-open-questions.md#event-transport)

---

### 4. REQ-1b — OSAC Heartbeat Event Ingestion
**Status: Done** &nbsp;·&nbsp; [COST-7795](https://redhat.atlassian.net/browse/COST-7795) &nbsp;·&nbsp; (with a mechanical caveat worth PM awareness)

Receive periodic heartbeat events from OSAC at configurable intervals (10-30s), auto-registering tenants on first contact.

**Acceptance Criteria**
- ⚠️ Can receive periodic lifecycle CloudEvents via HTTP or Kafka *(satisfied via a local sweep instead — see caveat below)*
- ✅ Events parsed for tenant ID, project ID, resource ID, hardware config, duration
- ✅ First event auto-creates the tenant/project if not already registered
- ✅ Events processed and cost calculated within the target SLA

**Current Implementation Status**
- Functionally satisfied today via a **local 60-second sweep** rather than an actual heartbeat CloudEvent emitted by OSAC's metering collector — see [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md) for the full rationale
- The OSAC metering collector ([osac-metering-discover-poc](https://github.com/masayag/osac-metering-discover-poc)) exists but is not yet connected to Cost Management
- `POST /api/v1/events` auto-creates tenant/project on first event
- Under 1ms per event to process

**Gap Summary**
The letter of the requirement ("receive heartbeat events") isn't literally true — we generate the equivalent data ourselves on a timer instead of receiving it from OSAC. The output is functionally identical, and the requirements doc itself confirms this satisfies the requirement for PoC purposes. Worth flagging to the PM only because it's a subtlety that could resurface if OSAC's real collector lands and behaves differently than expected.

**Action Items / Open Questions**
- Transport mechanism (Kafka, HTTP, NATS?) still not agreed with OSAC
- Interval: 10s vs. 30s was proposed but needs to be finalized and made configurable
- **New (Jul 20):** should the local sweep be shorter (e.g. every 50s instead of 60s) so 30s OSAC emit + sweep + processing stays under the 90s end-to-end SLA in the worst case?
- Consolidated action item: agree on CloudEvents transport with OSAC — see [open question #13](osac-open-questions.md#event-transport)

---

### 5. REQ-2 — Near-Real-Time Cost Calculation
**Status: Done** &nbsp;·&nbsp; [COST-7796](https://redhat.atlassian.net/browse/COST-7796)

Process OSAC heartbeat events and calculate costs within 60 seconds of receipt; 90-second end-to-end SLA (OSAC send + our processing).

**Acceptance Criteria**
- ✅ Processes OSAC heartbeat events within 60 seconds of receipt
- ✅ End-to-end latency under 90 seconds
- ✅ Cost report available in the dashboard after processing
- ✅ Demonstrated with at least one workload type

**Current Implementation Status**
- Under 1ms per event to process; metering sweep every 60s, rating sweep every 30s — comfortably inside the 90s SLA under typical conditions
- Cost entries queryable via [`snippets/query-costs.sh`](../../snippets/query-costs.sh) and the report API
- Demonstrated with both VMs and MaaS models

**Gap Summary**
No functional gaps for the happy path. The Jul 20 open question on shortening the sweep (see REQ-1b) is about worst-case SLA headroom if OSAC emit latency approaches 30s — not a current demo blocker.

**Action Items / Open Questions**
- Same sweep-interval question as REQ-1b (50s vs 60s for 90s E2E headroom)

---

## High Priority

### 6. REQ-1a — OSAC Cluster Lifecycle via Cluster Orders
**Status: Done** &nbsp;·&nbsp; [COST-7794](https://redhat.atlassian.net/browse/COST-7794)

Monitor OSAC "cluster orders" for state changes and calculate cost from provisioned capacity and duration.

**Acceptance Criteria**
- ✅ Monitors cluster orders via the OSAC management layer
- ✅ Captures state changes (create, stop, start, destroy)
- ✅ Cluster rate set per cluster order
- ✅ Cost calculated from provisioned capacity and duration
- ✅ No dependency on internal workload cluster data

**Current Implementation Status**
- Clarified and confirmed: "ClusterOrder" is OSAC's purchase/ordering workflow; the resulting **Cluster** is the actual provisioned resource that incurs cost — we track the Cluster ([resolved open question](osac-open-questions.md#cluster-lifecycle-req-1a))
- State changes captured via the Watch stream (CREATED/UPDATED/DELETED)
- Cluster rates configured (`cluster_uptime_seconds`, `cluster_worker_node_seconds`) in [`internal/rating/rating.go`](../../inventory-watcher/internal/rating/rating.go)

**Gap Summary**
No known gaps — this was a naming/conceptual question that's been resolved, not an implementation gap.

**Action Items / Open Questions**
None outstanding.

---

### 7. REQ-3a — OSAC Tenant/Project Attribution
**Status: Partial** &nbsp;·&nbsp; [COST-7799](https://redhat.atlassian.net/browse/COST-7799)

Map OSAC's Tenant → Project hierarchy into RHCM so all costs attribute correctly, including quota/budget rollup from project to tenant.

**Acceptance Criteria**
- ✅ Cost data attributed to the correct OSAC tenant
- ✅ Cost data drillable to project level within a tenant
- ✅ Tenant/project hierarchy read from OSAC
- ✅ Multi-tenant attribution works even on shared infrastructure
- ❌ Quotas/budgets tracked per project and per tenant, with project consumption rolling up to the tenant; **sum of project-level limits must not exceed the tenant-level limit** (no overcommit of limits across projects — Jul 20 clarification; previously framed as overcommit-allowed)

**Current Implementation Status**
- `inventory_project` table tracks the OSAC Tenant → Project hierarchy; all metering entries carry `tenant_id`
- **Decision (Jul 2, 2026):** RBAC scope for PoC is tenant + project level only; fine-grained Insights RBAC deferred post-PoC
- **Jul 20 clarification:** Cost Management UI is the preferred provider cost surface. RBAC remains project-scoped only — project access ⇒ full visibility within that project; users with multiple projects see only the projects they can access
- **Gap confirmed in code:** the `quotas` table already has a `project_id` column, but `QuotasForTenant` (the query behind the quota API — [`store.go:1218`](../../inventory-watcher/internal/inventory/store.go)) filters by `tenant_id` only and ignores it. Quota tracking today is tenant-level exclusively; no project-level rows, roll-up logic, or overcommit validation exist

**Gap Summary**
Cost attribution itself (tenant + project drill-down) is solid. What's not built yet is the quota/budget half of this requirement: project-level quotas that roll up to the tenant limit with no project-limit overcommit. The column is there; the query and validation logic aren't. This is the same underlying gap called out under REQ-9 — one fix satisfies both ACs. Provider UI preference has shifted toward Cost Management UI; remaining open item is the long-term Insights RBAC vs Keycloak model (deferred post-PoC if project-within-tenant lands).

**Action Items / Open Questions**
- Build project-scoped quota records, roll-up-to-tenant aggregation, and Σ(project limits) ≤ tenant limit validation — see REQ-9 / [req9 gap analysis](req9-quota-budget-gap-analysis.md)
- ~~Will providers view cost in the Cost Management UI or in OSAC's UI?~~ — **Leaning (Jul 20):** Cost Management UI preferred
- RBAC for cross-project visibility: as of Jul 20, no RBAC beyond project access (see above). Final Insights RBAC vs Keycloak-native model still open long-term ([open question #18](osac-open-questions.md#tenantproject-attribution-req-3a))
- Consolidated action items: implement OSAC project entities in Cost Management (done); determine RBAC needs for cross-project visibility (clarified for PoC; long-term model open)

---

### 8. REQ-3 — Granular Cost Tracking
**Status: Done** &nbsp;·&nbsp; [COST-7798](https://redhat.atlassian.net/browse/COST-7798)

A single system of record for cost, drillable by tenant, project, model, and user, spanning both capacity-based and consumption-based dimensions.

**Acceptance Criteria**
- ⚠️ Cost data filterable by tenant, model/SKU, application, user — and in general by any property available on OSAC CloudEvents, including tags *(core dimensions built; arbitrary/tag filtering not yet a first-class report dimension; user dimension has an open PII question — see below)*
- ✅ Dashboard shows near-real-time token consumption, compute hours, and estimated costs
- ✅ Reporting supports CSV and JSON export
- ✅ Financial data decoupled from infrastructure state

**Current Implementation Status**
- Filterable by tenant (`?group_by=tenant`), resource/model (`?group_by=resource`), project (`?group_by=project`, Jul 4), and user (`?group_by=user`, PR #59) — all **Done**
- Date filtering (`?from=&to=`), daily resolution (`?resolution=daily`), per-resource breakdown (`GET /api/v1/reports/breakdown`) — all added in PR #42
- Debug dashboard + Grafana in place; CSV/JSON export works; cost entries are a separate table from inventory state (decoupled, as required)
- MaaS token metering uses 3 meters: `maas_tokens_in`, `maas_tokens_out`, `maas_requests` — `cached_input_tokens` and `reasoning_tokens` from the OpenAI API are subsets of input/output (not additive), so they're parsed for observability but not billed separately

**Gap Summary**
Core dimensions are Done. Jul 20 expanded the filterability AC to "any CloudEvent property, including tags" — labels/tags on inventory events are stored, but report `group_by` does not yet expose arbitrary tag keys. The "application" dimension from the spec still doesn't map to a clear OSAC entity (may be project labels). Neither is a Jul 31 demo blocker if the named dimensions suffice.

**Action Items / Open Questions**
- **PII concern (raised Jul 14, 2026 meeting):** per-user MaaS cost attribution is now **built** (PR #59) — `user_id` flows end-to-end from IPP CloudEvent → metering → cost → report. **Pau still needs to confirm** whether this is acceptable or needs to be gated behind a config flag. See [osac-open-questions.md #21](osac-open-questions.md#data-privacy--pii-maas)
- Product call: which CloudEvent/tag properties beyond tenant/project/user/resource must be filterable for the PoC demo?

---

### 9. REQ-9 — Quota/Budget Status API
**Status: Partial** &nbsp;·&nbsp; [COST-7805](https://redhat.atlassian.net/browse/COST-7805)
&nbsp;·&nbsp; [gap analysis](req9-quota-budget-gap-analysis.md)

Give OSAC a fleet-level way to check quota/budget status (tenant, plus projects/clusters/VMs rolled up to the tenant) before allowing resource creation. Enforcement stays with OSAC; we provide the data — and we must also expose CRUD so RHCM can manage quotas/budgets itself.

**Definitions (from overview)**
- **Quota** = dimensional limit (CPU core-hours, GiB RAM-hour, tokens, etc.)
- **Budget** = monetary quota (metered consumption × rates)
- **Cost** = metered consumption × rates
- Distinct from **wallets** (REQ-14): a budget is a spending *ceiling*; a wallet is prepaid *balance*

**Acceptance Criteria**
- ✅ Sub-second API latency *(for the existing per-tenant status path)*
- ⚠️ OSAC can query tenant within-quota / % budget consumed, plus status of the tenant's projects/clusters/VMs rolled up to tenant *(tenant-level status Done; fleet/project/cluster/VM roll-up Gap)*
- ⚠️ Threshold checks at 50%, 70%, 90%, 100%… as defined by OSAC Cloud Admin or Tenant Admin roles *(fixed 50/70/90/100% today; not admin-configurable)*
- ✅ We implement the quota concept regardless of whether OSAC also does
- ❌ Quotas/budgets scoped to tenants and projects, rolling up from project to tenant
- ❌ Σ(project-level limits) ≤ tenant-level limit (no project overcommit) — **Jul 20 product rule**
- ❌ CRUD API for RHCM to manage quotas/budgets *(store `UpsertQuota` only; no HTTP POST/PUT/DELETE)*
- ~~Grace period requirements verified~~ — **OUT of PoC scope (Jul 20):** nice-to-have, not a requirement

**Current Implementation Status**
- `GET /api/v1/quotas/{tenant_id}` implemented, sub-second via a single indexed SUM query; threshold flags and alerts included
- **Source of truth (Jul 20):** "who owns limits" does not matter for PoC — RHCM must implement quotas anyway (also for non-OSAC customers); SOT often resolved later via Professional Services / a third system
- **Gap confirmed in code:** `QuotasForTenant` and `MeteringSum` ([`store.go:1218, 1245`](../../inventory-watcher/internal/inventory/store.go)) both query by `tenant_id` only. No project roll-up, no overcommit validation, no monetary/`CostSum` budgets, no non-monthly windows, no configurable thresholds, no fleet list endpoint — full Done-vs-Gap table in [req9 gap analysis](req9-quota-budget-gap-analysis.md)
- **Jul 31 = full gap list in scope** (not deferred)

**Gap Summary**
The per-tenant pull status API is solid and meets its latency target. v1.5 substantially expanded what "Done" means for Jul 31: CRUD, project→tenant roll-up with no limit overcommit, fleet-level status, monetary budgets, and configurable thresholds/windows. Grace periods are explicitly out. This is the same roll-up gap called out under REQ-3a; fixing project-scoped rows + aggregation + overcommit validation once advances both.

**Action Items / Open Questions**
- Implement the Jul 31 gap list from [req9 gap analysis](req9-quota-budget-gap-analysis.md): CRUD, project roll-up + no overcommit, fleet status, monetary budgets, configurable thresholds/windows
- ~~Do AI Grid requirements include grace periods?~~ — **Resolved (Jul 20):** not required (nice-to-have only)
- How should a monetary budget be represented relative to usage quotas? (Recommendation: same ceiling concept, denominated in currency)
- Do balance/entitlement checks need to be per feature/SKU, or is one tenant-level balance enough?
- **Budget vs. usage quotas need different mechanisms (Jul 14, Ronnie):** usage quotas need this synchronous pull check; monetary budgets can also use push (REQ-10) — the two aren't fully interchangeable
- Consolidated action items: calculate quota/budget consumption (done at tenant level); expose the quota status API (done); investigate/implement the quota/budget concept generally (partial — usage quotas only); close remaining Jul 31 gaps (open)

---

### 10. REQ-10 — Threshold Notification Back-Channel to OSAC
**Status: Done (pull model only)** &nbsp;·&nbsp; **Priority downgraded to Parked** &nbsp;·&nbsp; [COST-7807](https://redhat.atlassian.net/browse/COST-7807)

Proactively notify OSAC when cost/quota consumption crosses defined thresholds, so OSAC can trigger OPA-enforced rate limiting.

**Acceptance Criteria**
*(all four criteria below describe the push/webhook mechanism specifically — none are built, by joint decision; the pull model that satisfies REQ-9 instead is a different mechanism, not a substitute for these)*
- ❌ Sends notifications to OSAC at thresholds configurable by OSAC administrators (implying threshold sync too)
- ❌ Notifications include tenant ID, resource/project context, threshold level, current consumption
- ❌ Transport mechanism agreed between OSAC and RHCM
- ❌ Reliable delivery (no silent drops)

**Current Implementation Status**
- **Pull model implemented:** `GET /api/v1/quotas/{tenant_id}` returns threshold flags — this is the mechanism actually in use for the PoC
- **Push (webhook) model:** designed but not built — see [req10 analysis](req10-threshold-notifications-analysis.md) for the alerts-table design, webhook payload shape, and delivery-reliability options already scoped out and ready to build

**Gap Summary**
**Decision (Jul 2, 2026):** Parked. The pull model is accepted as sufficient for the Jul 31 PoC — OSAC's existing check-balance API for MaaS quota enforcement removes the urgency for a separate push alert. **Jul 14, 2026 update:** still parked — OSAC has no receiver today to act on a push notification (it would be audit-only at best). Cost's side of push delivery is low effort to add ("by end of week or sooner" per Martin) **once OSAC defines a receiver and a concrete CloudEvent schema** — the blocker is entirely on OSAC's side now, not ours.

**Action Items / Open Questions**
- Does OSAC have (or plan) an alerting mechanism to receive pushed notifications? Deferred
- **Action item:** if/when OSAC defines a receiver, Cost team prepares an example CloudEvent spec for budget threshold alerts for OSAC to review
- Low-balance alerts for wallets (REQ-14) are expected to pair with this same notification path once unparked
- Note: budget quotas (vs. usage quotas) are a better near-term candidate for a push model since they tolerate eventual consistency — see REQ-9 above
- Grace periods are OUT of PoC scope for REQ-9 (nice-to-have only); same applies if/when push alerts land

---

### 19. REQ-14 — Wallets (Prepaid Balance)
**Status: Not started** &nbsp;·&nbsp; [COST-7939](https://redhat.atlassian.net/browse/COST-7939)
&nbsp;·&nbsp; **New in v1.5** &nbsp;·&nbsp; Rank 19 (HIGH) — listed here next to REQ-9/REQ-10 because it is the prepaid complement to budgets/alerts

Prepaid wallets so providers can move from post-payment to pre-payment. Customers top up a monetary balance; metered spend is deducted; low-balance thresholds trigger alerts. Source: AI Grid MB-005. Product feature [COST-7938](https://redhat.atlassian.net/browse/COST-7938).

**Why this is not “a budget with no time limit”**
1. Admin UX should stay in prepaid-wallet terms (top up, remaining balance, low-balance alerts) — not Cost’s quota/budget machinery.
2. Settlement already happened at card top-up; a budget models a *future* spend ceiling on usage still to be billed. Treating draw-down as budget consumption implies the wrong commercial semantics.

Budgets (REQ-9) and wallets may coexist on the same tenant.

**Acceptance Criteria**
- ❌ RHCM can create, top up, and query wallet balances scoped to tenant (and optionally project)
- ❌ Metered cost is deducted from the wallet balance as spend accrues
- ❌ OSAC (or other consumers) can query remaining balance / % remaining via API (same latency expectations as REQ-9)
- ❌ Configurable low-balance thresholds trigger alerts (pairs with REQ-10)
- ❌ Wallet operations are auditable (top-ups, deductions, adjustments) in the Cost Management audit log

**Current Implementation Status**
- No wallet / prepaid-balance concept exists in RHCM today
- Closest capability is budgets/quotas (REQ-9), which model spending *limits*, not prepaid credits
- AI Grid MB-005 was marked Out of Scope for the trial/product cut in HIGHTP tracking (Jul 2026); Cost still needs the capability for prepaid provider models

**Gap Summary**
Entirely greenfield. Do not implement by stretching REQ-9 budgets into open-ended monetary ceilings — product explicitly rejected that shortcut. Ledger mechanics may be shared under the hood, but the user model and settlement semantics must stay distinct.

**Action Items / Open Questions**
- Design and implement prepaid wallets (consolidated action item #23): top-up, deduct metered spend, low-balance alerts
- Wallet scope: tenant-only, or tenant + project (and can projects share a tenant wallet)?
- Who owns top-up UX / payment capture — Cost UI, OSAC, or an external billing system (Lago/Zuora/etc.) with Cost as balance ledger?
- On zero/insufficient balance: status-only (like REQ-9), or participation in hard stop (enforcement still expected to be OSAC's)?
- Relationship to reserved allocations / multipliers under MB-005 (remain customer billing-system responsibilities per AI Grid notes)
- OUT of scope for this requirement: payment gateway / card capture; hard-stop enforcement; reserved allocations and billing multipliers

---

### 11. REQ-13 — Custom Metrics / Custom Rates
**Status: Done** &nbsp;·&nbsp; [COST-7808/COST-7810](https://redhat.atlassian.net/browse/COST-7810)

Let a rate be defined from an arbitrary metric dimension emitted by OSAC CloudEvents, without hardcoding new dimensions into the codebase.

**Acceptance Criteria**
- ✅ Can consume arbitrary CloudEvent dimensions as rate inputs
- ✅ New dimensions configurable with an ID, classification, and rate name
- ✅ Custom dimension data stored and available for cost calculation and reporting

**Current Implementation Status**
- Config-driven extraction implemented in [`internal/custommetrics/custommetrics.go`](../../inventory-watcher/internal/custommetrics/custommetrics.go); new dimensions configured via a JSON file (`CUSTOM_METRICS_CONFIG` env var), no recompile needed
- Custom metering entries flow through the existing rating and reporting pipeline unmodified
- Design write-up: [req13-custom-metrics-design.md](../research/req13-custom-metrics-design.md)

**Gap Summary**
Solid for what it was scoped to do: composing new rates from metrics OSAC *already* emits. It does not yet support asking OSAC to emit a brand-new meter/metric that doesn't exist today — that would require an actual backchannel to OSAC, which is unbuilt and only loosely discussed.

**Action Items / Open Questions**
- Who defines new dimensions to collect — OSAC or the Cost team? Today it's effectively OSAC (we can only work with what they emit); to add genuinely new meters we'd need a backchannel to OSAC (see [OSAC PRD-78](https://github.com/osac-project/enhancement-proposals/pull/78))
- ID/classification/naming scheme for custom rates still needs to be formally agreed
- How should custom rate formulas be expressed — a scripting language (à la CloudKitty), a JSON decision-model format (à la GoRules/JDM), or something else? Still open — see [rating engine research](../research/rating-engine-options.md) for the current evaluation (CloudKitty, GoRules/Zen, Drools)

---

### 12. REQ-2a — Cloud Events from OpenShift AI (MaaS) & Token Metering
**Status: Done (emulated / mock)** &nbsp;·&nbsp; [COST-7797](https://redhat.atlassian.net/browse/COST-7797)

Consume CloudEvents from OpenShift AI/OSAC for MaaS token metering (input, output, inference tokens, requests) and compute consumption-based cost within 60 seconds.

**Acceptance Criteria**
- ⚠️ Can receive and process MaaS CloudEvents from OpenShift AI/OSAC *(pipeline works; real OSAC/RHOAI events don't exist yet, so this runs on mock/simulated events)*
- ✅ Events ingested within 30 seconds of emission
- ✅ CloudEvents format parsed and stored
- ✅ MaaS cost computed within 60 seconds of event receipt, feeding quota/budget updates (REQ-9)
- ⚠️ Validated with at least one MaaS workload type *(validated against the IPP plugin + an echo-mode LLM, not a real model)*
- ✅ Ingests `prompt_tokens`, `completion_tokens` from vLLM/OSAC MaaS CloudEvents *(3 billing meters: `maas_tokens_in`, `maas_tokens_out`, `maas_requests`; `cached_input_tokens` and `reasoning_tokens` are parsed for observability but not billed separately — they're subsets of input/output tokens per the [OpenAI API spec](https://platform.openai.com/docs/api-reference/chat/object), billing them separately would double-count)*
- ⚠️ Tracks hardware compute: GPU SKU, VRAM (GB-seconds), queue wait *(GPU VRAM/compute-seconds are demoable via the REQ-13 custom-metrics config (`deploy/custom-metrics-example.json`); there's no dedicated GPU SKU classification or queue-wait meter)*
- ✅ Token data available for cost calculation and visible in the dashboard
- ✅ MaaS rate structure defined (tokens in/out, inference tokens, requests), priced per million units

**Current Implementation Status**
- Ingest endpoint (`POST /api/v1/events`) processes MaaS CloudEvents end-to-end; under 1ms per event; sustained throughput tested at 850-1,700 events/s ([stress test](../dev/ipp-stress-test-2026-07-05.md))
- **What's real:** the IPP external-metering plugin (PR #320 build), Istio `ext_proc` wiring, our `checkBalance`/`reportUsage` endpoints
- **What's emulated:** the LLM backend itself (llm-katan echo mode, not a real model), and the `X-MaaS-*` identity headers (manually injected — no Authorino integration yet)
- Default rates seeded: $0.50/M input tokens, $1.50/M output tokens, $5.00/M requests

**Gap Summary**
The pipeline, IPP plugin integration, and rate engine are genuinely proven. What's not yet proven is production reality: real OSAC/RHOAI MaaS CloudEvents don't exist yet (OSAC has no Model entity in its fulfillment-service proto), so we've built against a proposed schema and validated with an echo LLM rather than live inference traffic. See [req2 gap analysis](req2-maas-costing-gap-analysis.md) for full detail.

**Action Items / Open Questions**
- **Who collects RHOAI MaaS metrics — Cost or OSAC?** Moti is drafting an OSAC-side metering service to be the single collection point (adapters to Cost, M360, OpenMeter), which would make Cost a pure consumer — early, unreviewed draft, not confident it lands by end of July. For the PoC, the current direct real-time path (Martin ↔ Noy) continues in parallel as the fallback
- What fields will OSAC's MaaS CloudEvents actually contain? Still unconfirmed (are tokens per-interval or cumulative? is `model_name` stable enough for rate lookups?)
- **Tenant/project attribution:** Noy's PR (adding project/tenant attributes to the relevant OSAC entity) is merged; Martin's follow-on PR is being resubmitted. Mapping confirmed: OSAC `cost_center` → Cost `project`, OSAC `tenant` → Cost `tenant`
- Transport for MaaS events (HTTP, Kafka, other) still undecided
- Who defines the MaaS rate structure — Cost, OSAC, or jointly? Still not delivered — see REQ-11 for the related "Pau owes the rate/tier spec" action item
- Consolidated action items: define the MaaS rate structure (open — see REQ-11); accept RHOAI/OSAC MaaS CloudEvents and compute cost within 60s (done, pending real events)

---

## Medium Priority

### 13. REQ-3b — Service Catalog Sync from OSAC
**Status: Done** &nbsp;·&nbsp; [COST-7800](https://redhat.atlassian.net/browse/COST-7800)

Read OSAC's service catalog for pricing; manual setup is acceptable for PoC, API sync deferred.

**Acceptance Criteria**
- ✅ Adds service catalog capability
- ✅ Can read and synchronize OSAC catalog items (instance types, cluster sizes, storage tiers, etc.)
- ✅ Price lists correspond to OSAC catalog offerings
- ⚠️ Cost calculations use catalog-item-based rates *(per-SKU pricing now supported via `instance_type` rate dimension (PR #59); rates are still manually seeded rather than auto-derived from catalog — acceptable for PoC)*

**Current Implementation Status**
- Catalog items synced via the reconciler for all three types: cluster, compute_instance, bare_metal_instance, each linked to a hardware-profile template
- **New (PR #59):** Rate engine supports per-SKU pricing via `instance_type` dimension on the `rates` table with 4-way fallback (tenant+instance_type > instance_type > tenant > global). Three pricing models documented in the [rate configuration guide](../rate-configuration-guide.md)
- **New (PR #59):** Catalog fallback in metering — when OSAC removes `cores`/`memory_gib` from `ComputeInstance`, metering resolves specs from the `InstanceType` catalog automatically
- Rates still seeded manually (not auto-derived from catalog) — acceptable for PoC per the spec ("manual setup acceptable")

**Gap Summary**
The core REQ-3b capability — price per catalog item, not per raw CPU/memory — is now implemented. The OSAC `ComputeInstance` change (removing cores/memory) is no longer a risk. Rate auto-derivation from catalog pricing is not built but is explicitly deferred (PoC accepts manual setup).

**Action Items / Open Questions**
- ~~Action item: Martin to verify cost calculation works purely from `instance_type`~~ — **Done** (PR #59)
- **Bare metal and catalog items are both missing from OSAC's public gRPC stream** (private-only today). Martin confirmed with Aishay this should be fixed on the public API; being addressed separately ([open questions #3, #4, #6](osac-open-questions.md))
- Unresolved: can tenants override catalog prices, or create their own priced sub-offerings for their users? Raised by Moti, no answer yet ([open question #22](osac-open-questions.md#catalog-pricing-model))

---

### 14. REQ-5 — Chargeback Reporting
**Status: Done** &nbsp;·&nbsp; [COST-7801](https://redhat.atlassian.net/browse/COST-7801)

Export chargeback reports covering both capacity-based and consumption-based dimensions, per tenant/project. Jul 20: export via API as CSV or JSON is explicitly in scope; FOCUS remains desirable but OUT if we have to choose.

**Acceptance Criteria**
- ✅ Reports map provisioned compute hours and GPU hours to token consumption per tenant/project
- ✅ Exportable via API in CSV and JSON
- ✅ Accurate and consistent with the cost tracking dashboard

**Current Implementation Status**
- `GET /api/v1/reports/costs` supports `group_by=tenant/resource_type/meter/resource/project/user`, date filtering (`?from=&to=`), daily resolution (`?resolution=daily`), and CSV/JSON export with Koku-compatible envelope — all added in PR #42
- `GET /api/v1/reports/breakdown` provides per-resource line-item drill-down (PR #42)
- Scheduled export documented as a [Kubernetes CronJob pattern](../dev/scheduled-chargeback-export.md) calling the report API on a schedule — verified on k3d, pending PM sign-off on whether this satisfies the acceptance criterion vs. a built-in scheduler
- Debug dashboard consumes the same endpoint, so dashboard and export are consistent by construction

**Gap Summary**
No functional gaps. The scheduled export is a documented operational pattern (CronJob calling the API), not a built-in feature — whether that's sufficient depends on PM's reading of "exportable."

**Action Items / Open Questions**
- **Clarified (Jul 14, 2026 meeting):** there's no formal export requirement beyond "export the data so it can be used to generate bills," confirmed by Pau
- A FOCUS-style export spike branch exists; the gap is missing fields tied to the service catalog (SKUs, product family — see REQ-3b), not implementation complexity
- ~~Action item: Martin to double-check CSV export covers every field currently tracked~~ — open-ended, no specific gap identified

---

### 15. REQ-7 — Audit Trail
**Status: Done** &nbsp;·&nbsp; [COST-7802](https://redhat.atlassian.net/browse/COST-7802)

Zero-leakage reconciliation, immutable audit logs, and support for billing dispute resolution.

**Acceptance Criteria**
- ✅ Billing ledgers match consumption logs with zero financial variance
- ✅ Tamper-resistant audit trail for admin changes
- ✅ Human-readable error logging for dispute resolution

**Current Implementation Status**
- `raw_events` table stores every incoming event immutably before any processing, deduplicated on event ID — this is the audit trail
- Forwarded to Splunk for long-term retention and search ([Splunk audit forwarding](../splunk-audit-forwarding.md))

**Gap Summary**
No gap identified for PoC scope. More extensive audit-trail requirements exist under separate tickets (COST-575, COST-3358) and on-prem audit work is tracked under POC-ENV (COST-7541, COST-7328) — those are broader, longer-term efforts, not PoC gaps.

**Action Items / Open Questions**
None outstanding for PoC scope.

---

## Low Priority

### 16. REQ-11 — Cost Tiers
**Status: Partial** &nbsp;·&nbsp; [COST-7808](https://redhat.atlassian.net/browse/COST-7808)

Tiered pricing for both capacity-based and MaaS consumption-based rates (e.g., first 1M tokens free, next 10M at $0.50/M; first 20 GiB free, next 100 GiB at $0.08/GiB-month).

**Acceptance Criteria**
- ✅ Rate engine supports multiple pricing tiers per resource type
- ❌ Tiers apply to both capacity-based rates (cluster/VM) and MaaS consumption rates *(correct for MaaS; would silently undercharge if applied to capacity meters today — see below)*
- ✅ Tier configuration is manageable without code changes
- ✅ We implement cost tiers regardless of whether OSAC also implements them; source-of-truth decided at implementation time

**Current Implementation Status**
- Tiered pricing engine implemented and correct for **MaaS** rates: graduated ("waterfall") pricing, unbounded final tier, free tiers, all stored as configurable JSON — no code changes needed to add a new tier structure
- **Capacity meters (VM/cluster/bare-metal uptime, core-seconds, GiB-seconds) are a real gap:** the current per-event tiering logic is *correct* for MaaS but would be *silently wrong* for capacity meters, because the spec's own example ("20 GiB free, next 100 GiB at $0.08/GiB-**month**") implies monthly accumulation, not per-60-second-sweep accumulation. If a capacity tier were configured today, the free tier would effectively never exhaust and the tenant would be permanently undercharged. See [req11 gap analysis](req11-cost-tiers-gap-analysis.md) for the worked example ($0.00 billed vs. the correct $13.60)

**Gap Summary**
This is not a "not started" gap — it's a "would produce incorrect billing if used" gap, which is more important to flag than a typical TBD item. MaaS tiers are safe to demo today; capacity tiers are not, until cumulative/period-accumulating logic is added (estimated medium effort).

**Action Items / Open Questions**
- **Still outstanding as of Jul 14, 2026:** Pau owns writing the actual rules/spec for MaaS quotas, budgets, tiers, and rates (free tier → next tier → combining metrics/events) — carried over from the prior week, still not delivered
- Martin has a quick spike integrating GoRules for some rates (branch marked "spike") to validate feasibility ahead of Pau's spec landing; will reconcile against it once delivered
- **Boundary with REQ-9 (Jul 20):** same windowed pattern can be **free→charge** (this requirement — keep serving, next price band) or **allow→deny** (REQ-9 hard quota). Mode is per configuration, not a global product rule — see [req9 gap analysis](req9-quota-budget-gap-analysis.md) and [req11 gap analysis](req11-cost-tiers-gap-analysis.md)
- Decision needed: is tier ownership Cost-only, OSAC-only, or both-synced? Unresolved — see Cross-Cutting Unresolved Items below
- Decision needed: does the PoC demo need to show capacity tiers, or is a MaaS-only demo sufficient? This determines whether the cumulative-tier work (medium effort) needs to happen before Jul 31

---

### 17. REQ-12 — Daily OpenShift Virtualization Costs
**Status: TBD**

Daily cost calculation for OpenShift Virtualization workloads (VMs) provisioned through OSAC.

**Acceptance Criteria**
- ❌ Daily (or even hourly) cost for every resource type (CaaS, VMaaS, etc.) is highly desirable — not yet a hard requirement, but described as "just a matter of time." Jul 20 note: neoclouds (QuickPod, Runpod, Vast.ai, etc.) sometimes bill per-30-minute, per-minute, or even per-second; finer granularity within reasonable effort is desirable
- ❌ VMs on one cluster may span multiple projects/tenants and must be costed separately — possibly at different rates for the same VM type on the same cluster

**Current Implementation Status**
- Not started as a dedicated deliverable. This overlaps with the broader [PRD-13 OpenShift Virtualization fit & finish](https://github.com/project-koku/enhancements/pull/11) epic
- Partial overlap with work we already have: metering/cost entries already carry `tenant_id` / `project_id`, and report resolution can be daily — but that does not yet equal a productized "daily OCP Virt cost" feature or per-tenant rate overrides on shared clusters

**Gap Summary**
Still underspecified as a hard acceptance bar, but Jul 20 added two concrete constraints: (1) finer-than-daily granularity is desirable directionally, and (2) multi-tenant/project costing (with different rates) on a shared cluster is required when this lands.

**Action Items / Open Questions**
- Needs PM definition/confirmation of the hard acceptance bar (daily vs hourly vs finer) before this can move past TBD
- Relationship to the pre-existing PRD-13 epic needs to be clarified (is this PoC delivering a subset of PRD-13, or something adjacent?)
- Confirm whether per-project/tenant rate overrides for the same VM type are in PoC scope or post-PoC

---

## Deprioritized / Parked (still worth PM visibility)

### 18. REQ-8 — Bare Metal Costing (OSAC Bare Metal Service)
**Status: Done (built ahead of schedule) — but Priority downgraded to Parked for Jul 31** &nbsp;·&nbsp; [COST-7811](https://redhat.atlassian.net/browse/COST-7811)

Support bare metal nodes provisioned through OSAC (BMaaS), consuming bare metal CloudEvents for capacity-based costing — including standalone nodes outside OpenShift clusters (Windows, RHEL, Oracle Exadata, etc.).

> **Decision (Jul 2, 2026):** Deferred from the Jul 31 PoC scope. Owner: Moti.
> **Jul 20 update:** BMaaS is part of the Aug 31 OSAC deliverable; standalone (non-OCP) bare metal is confirmed IN for post-PoC scope. POC-ARCH also marks bare metal charging OUT of the capacity PoC path until OSAC supports it.

**Acceptance Criteria**
- ⚠️ Receives and processes bare metal service CloudEvents from OSAC *(via REST reconciler polling, not real-time Watch-stream events — OSAC gap, not ours)*
- ✅ Costs calculated for bare metal nodes based on provisioned capacity *(uptime-based; cores/memory not yet meterable — see below)*
- ⚠️ Standalone bare metal nodes (not attached to OpenShift) supported *(confirmed IN for post-PoC; mechanism is likely agnostic but not explicitly verified against Windows/RHEL/Exadata-style nodes)*

**Current Implementation Status**
- Despite being parked, this was actually built: `inventory_bare_metal_instance` table, reconciler-based polling of OSAC's REST List API, uptime metering (`bm_uptime_seconds`) via the sweep + a final metering entry on delete, default rates seeded
- **Important caveat:** BareMetalInstance is **not** in OSAC's public Watch stream `oneof` (private-stream only), so this runs on 5-minute reconciler polling rather than real-time events — same-day accuracy, not sub-minute
- CPU/memory hardware specs live on the catalog item, not the instance itself, so metering today only covers uptime, not core/memory consumption

**Gap Summary**
Functionally ahead of where the Jul 31 park decision assumed — our side is done via REST polling. Remaining gaps are on OSAC (public-stream visibility, BMaaS CloudEvents schema, Aug 31 service readiness) plus verifying standalone non-OCP node types for post-PoC.

**Action Items / Open Questions**
- **Martin confirmed with Aishay** that bare metal events are missing from the public gRPC API (private-only) — same issue as REQ-3b's catalog items. Path forward: Martin will file a PR upstream or coordinate with OSAC's team to add it to the public stream ([open question #3](osac-open-questions.md#bare-metal-req-8))
- ~~Do we need to support standalone bare metal nodes outside an OpenShift cluster?~~ — **Resolved (Jul 20):** yes, IN for post-PoC
- Hardware specs (cores/memory) require a catalog-item → template lookup chain — is that the intended path, or will specs move onto the instance directly?
- Align demo narrative with Aug 31 OSAC BMaaS timeline rather than Jul 31 re-elevation unless OSAC public-stream gaps close earlier

---

## Cross-Cutting Unresolved Items

A handful of open decisions touch multiple requirements above and are the ones most likely to need explicit PM/stakeholder sign-off rather than engineering effort:

| Decision | Affects | Status |
|---|---|---|
| **Cost tier ownership** — Cost only, OSAC only, or both synced | REQ-11, REQ-13 | Unresolved |
| **Provider UI surface** — Cost Management UI vs. OSAC-hosted UI vs. a generic backend-agnostic abstraction layer | REQ-3a, REQ-9 | **Leaning (Jul 20):** Cost Management UI preferred. Abstraction layer for metering/catalog/UI (any billing backend inside OSAC) still unresolved for Jul 31/Aug 31 |
| **MaaS metric collection ownership** — Cost collects directly from RHOAI, or OSAC collects and forwards | REQ-2a | Leaning toward OSAC forwarding long-term, but PoC builds the direct path as fallback; Moti's design not confirmed to land by Jul 31 |
| **Per-user PII exposure** — does per-user MaaS cost attribution expose sensitive personal data | REQ-3 | Not resolved; action item with Pau, no timeline given |
| **MaaS rate/tier spec** — the actual rules for MaaS quotas, budgets, tiers, and combining metrics | REQ-2a, REQ-11 | Owned by Pau, carried over |
| **RBAC model** — Insights RBAC vs. a simpler Keycloak-native tenant/project model | REQ-3a | PoC clarified (Jul 20): project access only, no finer RBAC. Long-term Insights vs Keycloak still open; deferred post-PoC if project-within-tenant lands |
| **Three-way convergence** — SaaS Cost Management, on-prem Koku, and this OSAC PoC can't stay separate long-term | Cuts across most requirements | EMR meeting expected to set direction; outcome affects RBAC approach and long-term architecture |
| **Catalog price override by tenant** — can tenants override CSP prices or create their own priced sub-offerings | REQ-3b, REQ-13 | Raised by Moti, no answer from OSAC yet |
| **Wallet vs budget modeling** — prepaid balance must not be an open-ended monetary budget | REQ-14, REQ-9 | **Resolved in spec (Jul 20):** distinct concepts; wallets get their own ledger/UX even if some mechanics are shared |
| **Quota/budget source of truth** — OSAC vs RHCM vs third system | REQ-9 | **Closed as "does not matter" (Jul 20):** RHCM implements quotas/CRUD anyway; SOT often decided later via Professional Services |
| **Project limit overcommit** — may project limits sum above the tenant limit? | REQ-3a, REQ-9 | **Resolved (Jul 20):** no — Σ(project limits) ≤ tenant limit |
| **~~`ComputeInstance` dropping CPU/memory fields~~** | POC-ARCH, REQ-3b | **Resolved (PR #59):** catalog fallback + per-SKU pricing implemented. Cost calculation works from `instance_type` alone. Only outstanding item: OSAC PR timeline from Moti |

---

## Related Documents

| Document | What it covers |
|---|---|
| [poc_requirements_overview.md](poc_requirements_overview.md) | Canonical requirements spec (v1.5) — the source for everything above |
| [implementation-status.md](../implementation-status.md) | Code-link-heavy status tracker (engineering-facing) |
| [requirements-comparison.md](requirements-comparison.md) | Older comparison of the spec vs. an earlier implementation snapshot (partially superseded by this doc) |
| [osac-open-questions.md](osac-open-questions.md) | The consolidated open questions we need OSAC to answer |
| [req1 gap analysis](req1-osac-integration-gap-analysis.md) | OSAC integration detail |
| [req2 gap analysis](req2-maas-costing-gap-analysis.md) | MaaS costing detail |
| [req8 gap analysis](req8-bare-metal-gap-analysis.md) | Bare metal costing detail |
| [req9 gap analysis](req9-quota-budget-gap-analysis.md) | Quota/budget — CRUD, project roll-up, fleet status, monetary budgets (Jul 31 scope) |
| [req10 analysis](req10-threshold-notifications-analysis.md) | Threshold notifications detail |
| [req11 gap analysis](req11-cost-tiers-gap-analysis.md) | Cost tiers detail, including the undercharging example |

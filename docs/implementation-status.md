# Implementation Status

> Cross-referenced with the
> [consolidated requirements spec v1.2](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md)
> (replaces both the csv_poc_requirements_summary and the original brief).

## Summary

| Priority | Total | Done | Partial | Not Started |
|---|---|---|---|---|
| CRITICAL | 5 | 4 | 0 | 1 |
| HIGH | 8 | 6 | 1 | 1 |
| MEDIUM | 2 | 1 | 1 | 0 |
| Must Have | 1 | 1 | 0 | 0 |
| **Total** | **16** | **12** | **2** | **2** |

## Full Requirements Status

| Req | Priority | Title | Status | Blocker | Details |
|---|---|---|---|---|---|
| POC-ENV | CRITICAL | On-prem deployment | Not started | RHCM team scope | N/A — deployment, not consumer |
| POC-ARCH | CRITICAL | Capacity-based charging | **Done** | — | Standalone Go component, heartbeat-driven |
| REQ-1 | CRITICAL | OSAC integration | **Done** | — | [req1 gap analysis](requirements/req1-osac-integration-gap-analysis.md) |
| REQ-1a | HIGH | Cluster lifecycle | **Done** | Verify "cluster orders" = Cluster | Tracking clusters with node sets |
| REQ-1b | CRITICAL | Heartbeat ingestion | **Done** | — | Local 60s sweep = heartbeat equivalent ([ADR-003](decisions/003-heartbeat-emitter-vs-sweep.md)) |
| REQ-2 | CRITICAL | Real-time cost calc | **Done** | — | <1ms/event, cost within 30s |
| REQ-2a | HIGH | MaaS CloudEvents | **Done** (mock) | OSAC Model entity | [req2 gap analysis](requirements/req2-maas-costing-gap-analysis.md) |
| REQ-3 | HIGH | Granular cost tracking | **Done** | — | Report API: `GET /api/v1/reports/costs` (JSON + CSV) |
| REQ-3a | HIGH | Tenant/project attribution | **Done** | Authz/RBAC open | [Open questions](#req-3a--tenantproject-attribution); [roadmap RBAC note](roadmap.md#osac-projects--rbac-from-pau) |
| REQ-3b | MEDIUM | Service catalog sync | **Done** | — | Instance types + catalog items synced via reconciler |
| REQ-4 | HIGH | Token metering | **Done** (mock) | OSAC MaaS schema | [req2 gap analysis](requirements/req2-maas-costing-gap-analysis.md) |
| REQ-5 | MEDIUM | Chargeback reporting | Partial | — | Report API done (JSON + CSV); no scheduled export yet |
| REQ-8 | HIGH | Bare metal costing | **Done** | Watch `oneof` gap — uses reconciler | [req8 gap analysis](requirements/req8-bare-metal-gap-analysis.md) |
| REQ-9 | HIGH | Quota/budget status API | **Done** | — | `GET /api/v1/quotas/{tenant_id}` |
| REQ-10 | HIGH | Threshold notifications | **Done** (pull) | Webhook push deferred | [req10 analysis](requirements/req10-threshold-notifications-analysis.md) |
| REQ-11 | MUST HAVE | Cost tiers | **Done** | — | Tiered pricing in rate engine |
| REQ-12 | TBD | Daily OCP Virt costs | TBD | PM definition | Not scoped |
| REQ-13 | HIGH | Custom rate dimensions | Not started | — | [Research done](research/rating-engine-options.md) |

**Post-PoC:**

| Req | Priority | Title | Status | Notes |
|---|---|---|---|---|
| REQ-6 | STANDARD | Security & access control | N/A | In-product |
| REQ-7 | STANDARD | Reconciliation & auditing | Partial | `raw_events` = immutable audit trail |

---

## Detailed Breakdown

### CRITICAL Requirements

### POC-ENV — On-Premise Deployment
**Status:** Not started
**Spec:** [poc_requirements_overview.md#poc-env](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#poc-env--on-premise-deployment)

Deployment concern — outside scope of our consumer component. Requires
Helm chart / OLM work for RHCM on-prem.

---

### POC-ARCH — Capacity-Based Charging Model
**Status:** Done
**Spec:** [poc_requirements_overview.md#poc-arch](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#poc-arch--capacity-based-charging-model)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Costs calculated from provisioned capacity | Done | [`internal/metering/metering.go`](../inventory-watcher/internal/metering/metering.go) — `computeInstanceMeters`, `clusterMeters` |
| Heartbeat events drive cost calculation | Done | Watch stream + 60s metering sweep ([ADR-001](decisions/001-metering-sweep-interval.md)) |
| No dependency on workload cluster metrics | Done | All data from OSAC management layer |
| Demo-ready: show cost within SLA | Done | <1ms per event; cost entries within 30s |

**Related docs:** [req1 gap analysis](requirements/req1-osac-integration-gap-analysis.md)

---

### REQ-1 — OSAC Integration via Region Management Cluster
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-1](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-1--osac-integration-via-region-management-cluster)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Connect to OSAC APIs (gRPC/REST) | Done | [`internal/osac/client.go`](../inventory-watcher/internal/osac/client.go) |
| Read inventory and resource state | Done | [`internal/reconciler/reconciler.go`](../inventory-watcher/internal/reconciler/reconciler.go) |
| Tenant lifecycle synced | Done | Watch stream + reconciler |
| Workload info includes tenant/project/resource IDs | Done | All inventory records have tenant, project fields |

**Related docs:** [req1 gap analysis](requirements/req1-osac-integration-gap-analysis.md), [gRPC messages catalog](grpc-messages-catalog.md)

---

### REQ-1b — OSAC Heartbeat Event Ingestion
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-1b](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-1b--osac-heartbeat-event-ingestion)

> **Clarification:** "Heartbeat events" are CloudEvents emitted periodically
> by the OSAC metering collector — same schema as lifecycle events, just fired
> on a timer with pre-calculated `duration_seconds`. Our local 60s metering
> sweep produces functionally identical data. The spec confirms this satisfies
> the requirement. See [ADR-003](decisions/003-heartbeat-emitter-vs-sweep.md).

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Receive periodic lifecycle CloudEvents via HTTP | Done | [`internal/ingest/handler.go`](../inventory-watcher/internal/ingest/handler.go) — `POST /api/v1/events` |
| Parse tenant/project/resource/hardware/duration | Done | MaaS CloudEvents parsed; VM data via Watch stream |
| First event auto-creates tenant/project | Done | `UpsertModel` / `UpsertComputeInstance` create on first event |
| Events processed within SLA | Done | <1ms per event; local sweep every 60s |

---

### REQ-2 — Near-Real-Time Cost Calculation
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-2](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-2--near-real-time-cost-calculation)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Process events within 60 seconds | Done | <1ms per event |
| End-to-end latency under 90 seconds | Done | Metering: 60s sweep + Rating: 30s sweep |
| Cost report available after processing | Done | `cost_entries` table populated; [`snippets/query-costs.sh`](../snippets/query-costs.sh) |
| Demonstrated with at least one workload | Done | VMs + MaaS models |

---

## HIGH Requirements

### REQ-1a — Cluster Lifecycle via Cluster Orders
**Status:** Partial
**Spec:** [poc_requirements_overview.md#req-1a](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-1a--osac-cluster-lifecycle-via-cluster-orders)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Monitor cluster orders for state changes | Partial | We track `Cluster` objects, not "cluster orders" specifically |
| State changes captured | Done | Watch stream CREATED/UPDATED/DELETED |
| Cluster rate configured per cluster order | Done | [`internal/rating/rating.go`](../inventory-watcher/internal/rating/rating.go) — `cluster_uptime_seconds`, `cluster_worker_node_seconds` rates |
| Cost based on provisioned capacity + duration | Done | [`internal/metering/metering.go`](../inventory-watcher/internal/metering/metering.go) — `clusterMeters` |

**Gap:** Need to verify whether "cluster orders" in OSAC map to the `Cluster`
entity we already track or are a separate concept.

---

### REQ-3 — Granular Cost Tracking
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-3](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-3--granular-cost-tracking)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Cost filterable by tenant, model, user | Done | `GET /api/v1/reports/costs?group_by=tenant\|resource_type\|meter\|resource&tenant_id=X` |
| Reporting supports CSV and JSON export | Done | `?format=csv` or `Accept: text/csv`; JSON default |
| Financial data decoupled from infra state | Done | `cost_entries` table independent of inventory |

---

### REQ-3a — Tenant/Project Attribution
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-3a](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-3a--osac-tenantproject-attribution)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Cost attributed to correct tenant | Done | `tenant_id` on all metering + cost entries |
| Drill-down to project level | Done | `inventory_project` table; [`internal/inventory/store.go`](../inventory-watcher/internal/inventory/store.go) |
| Tenant/project read from OSAC | Done | Reconciler syncs projects |
| Multi-tenant on shared infra | Done | Per-tenant metering and cost isolation |

---

### REQ-8 — Bare Metal Costing
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-8](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-8--bare-metal-costing-osac-bare-metal-service)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Track BareMetalInstance lifecycle | Done | Reconciler polls REST List API; watcher handles events if present |
| Inventory table for bare metal | Done | `inventory_bare_metal_instance` table with catalog_item, state, labels |
| Metering for bare metal | Done | `bm_uptime_seconds` via sweep + final metering on delete |
| Default rates | Done | `bm_uptime_seconds` rate seeded in [`rating.go`](../inventory-watcher/internal/rating/rating.go) |

**Note:** BareMetalInstance is not in the public Watch stream oneOf but IS
available via the public REST List API. The reconciler polls periodically
(same pattern as InstanceTypes and Projects). Real-time events available
via the private Watch stream if we switch to it later.

**Open question:** Hardware specs (cores/memory) are not on the
BareMetalInstance proto — they're on the catalog item/template. Currently
metering uptime only. CPU/memory metering requires catalog item → template
resolution (see [meeting questions](meeting-questions-osac.md#bare-metal-req-8)).

**Related docs:** [req8 gap analysis](requirements/req8-bare-metal-gap-analysis.md)

---

### REQ-9 — Quota/Budget Status API
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-9](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-9--quotabudget-status-api)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Sub-second latency | Done | Single SUM query with indexes |
| OSAC can query quota status | Done | [`GET /api/v1/quotas/{tenant_id}`](api-reference.md#get-apiv1quotastenant_id) |
| Threshold checks (50/70/90/100%) | Done | `thresholds` map in response |
| Source of truth agreed | Partial | RHCM provides data; enforcement is OSAC's responsibility |

---

### REQ-10 — Threshold Notifications to OSAC
**Status:** Not started
**Spec:** [poc_requirements_overview.md#req-10](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-10--threshold-notification-back-channel-to-osac)

Depends on REQ-9 (done). Next step: when a threshold is crossed, emit a
webhook/event to OSAC. Needs transport agreement (webhook vs CloudEvent).

---

### REQ-2a — Cloud Events from OpenShift AI (MaaS)
**Status:** Done (mock)
**Spec:** [poc_requirements_overview.md#req-2a](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-2a--cloud-events-from-openshift-ai-maas)

> **Note:** Previously deferred from PoC; now in-scope per v1.1 spec.

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Receive and process MaaS CloudEvents | Done (mock) | [`internal/ingest/handler.go`](../inventory-watcher/internal/ingest/handler.go) |
| Events ingested within 30 seconds | Done | <1ms per event |
| CloudEvents format parsed and stored | Done | `raw_events` table |
| MaaS cost computed within 60s | Done | Rating sweep every 30s |

Blocked on real OSAC Model entity and MaaS CloudEvents schema.
See [req2 gap analysis](requirements/req2-maas-costing-gap-analysis.md).

---

### REQ-4 — Token Metering (MaaS)
**Status:** Done (mock)
**Spec:** [poc_requirements_overview.md#req-4](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-4--token-metering-maas)

> **Note:** Previously deferred from PoC; now in-scope per v1.1 spec.

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Ingest token dimensions | Done (mock) | `maas_tokens_in`, `maas_tokens_out` |
| Token data available for cost calculation | Done | Metering entries → cost entries via rating sweep |
| MaaS rate structure defined | Done | Default rates seeded: $0.50/M tokens_in, $1.50/M tokens_out, $5.00/M requests |

---

### REQ-13 — Custom Rate Dimensions
**Status:** Not started
**Spec:** [poc_requirements_overview.md#req-13](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-13--custom-rate-dimensions-custom-metrics)

Ability to create custom rates from arbitrary CloudEvent dimensions.
See [rating engine research](research/rating-engine-options.md) — GoRules/Zen
recommended for post-PoC programmable rating.

---

## MUST HAVE Requirements

### REQ-11 — Cost Tiers
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-11](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-11--cost-tiers)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Multiple pricing tiers per resource type | Done | `rates.tiers` JSONB column; [`internal/rating/rating.go`](../inventory-watcher/internal/rating/rating.go) → `applyTieredRate` |
| Tiers apply to capacity and MaaS rates | Done | Same rate engine for both |
| Tier config without code changes | Done | JSON in `rates` table; no recompile needed |

---

## MEDIUM Requirements

### REQ-3b — Service Catalog Sync from OSAC
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-3b](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-3b--service-catalog-sync-from-osac)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Read OSAC catalog items | Done | Instance types + 3 catalog item types synced via reconciler |
| Price lists correspond to catalog | Done | Default rates seeded; [`internal/rating/rating.go`](../inventory-watcher/internal/rating/rating.go) |
| Cost calculations use catalog-based rates | Done | Rate lookup by `meter_name` + `resource_type` |

Catalog items (`inventory_catalog_item` table) synced for all three types:
cluster, compute_instance, bare_metal_instance. Each links to a template
(hardware profile) and carries title, description, published flag.

**Remaining gap:** Catalog item → rate mapping is not automated. Rates are
still seeded as defaults. Future: auto-create rates from catalog item pricing.

---

### REQ-5 — Chargeback Reporting
**Status:** Partial
**Spec:** [poc_requirements_overview.md#req-5](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-5--chargeback-reporting)

Cost data exists and is queryable. No export mechanism or formatted reports.
See [`snippets/query-costs.sh`](../snippets/query-costs.sh) for demo queries.

---

## Future Work (Post-PoC)

| Req | Title | Status | Notes |
|---|---|---|---|
| [REQ-6](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-6--platform-security--access-control) | Security & Access Control | N/A | In-product, no gap |
| [REQ-7](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-7--reconciliation-auditing--dispute-tracing) | Reconciliation & Auditing | Partial | `raw_events` provides immutable audit trail |
| [REQ-12](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-12--daily-openshift-virtualization-costs) | Daily OCP Virt Costs | TBD | Pending PM confirmation |

---

## Architecture Decisions

| ADR | Title | Link |
|---|---|---|
| ADR-001 | Metering sweep interval (60s) | [001-metering-sweep-interval.md](decisions/001-metering-sweep-interval.md) |
| ADR-002 | Arguments against Kafka | [002-arguments-against-kafka.md](decisions/002-arguments-against-kafka.md) |
| ADR-003 | Heartbeat events vs local sweep | [003-heartbeat-emitter-vs-sweep.md](decisions/003-heartbeat-emitter-vs-sweep.md) |

## Related Documentation

| Document | Description |
|---|---|
| [gRPC Messages Catalog](grpc-messages-catalog.md) | OSAC proto messages we consume |
| [API Reference](api-reference.md) | HTTP endpoints we expose |
| [Rating Engine Options](research/rating-engine-options.md) | CloudKitty, GoRules, Drools evaluation |
| [req1 Gap Analysis](requirements/req1-osac-integration-gap-analysis.md) | OSAC integration implementation details |
| [req2 Gap Analysis](requirements/req2-maas-costing-gap-analysis.md) | MaaS costing implementation details |
| [req8 Gap Analysis](requirements/req8-bare-metal-gap-analysis.md) | Bare metal costing — OSAC blockers and implementation plan |
| [req10 Analysis](requirements/req10-threshold-notifications-analysis.md) | Threshold notifications — delivery models, open questions |
| [Requirements Comparison](requirements/requirements-comparison.md) | Updated spec vs original brief |
| [Demo Scenario 1](demo-scenario-1.md) | Infrastructure metering demo |
| [Demo Scenario 2](demo-scenario-2-maas.md) | MaaS metering + cost demo |
| [Local Dev Setup](local-dev-setup.md) | How to run everything |

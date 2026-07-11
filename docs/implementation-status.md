# Implementation Status

> Cross-referenced with the
> [consolidated requirements spec v1.1](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md)
> (replaces both the csv_poc_requirements_summary and the original brief).

## Summary

| Priority | Total | Done | Partial | TBD |
|---|---|---|---|---|
| CRITICAL | 5 | 4 | 1 | 0 |
| HIGH | 8 | 6 | 2 | 0 |
| MEDIUM | 3 | 1 | 2 | 0 |
| LOW | 2 | 1 | 0 | 1 |
| **Total** | **18** | **12** | **5** | **1** |

## Full Requirements Status

| Rank | Req | JIRA | Priority | Title | Status | Notes |
|---|---|---|---|---|---|---|
| 1 | POC-ENV | — | CRITICAL | On-prem deployment | Partial | [CRC guides](dev/crc-full-deployment.md) |
| 2 | POC-ARCH | [COST-7792](https://redhat.atlassian.net/browse/COST-7792) | CRITICAL | Capacity-based charging | **Done** | Standalone Go component |
| 3 | REQ-1 | [COST-7793](https://redhat.atlassian.net/browse/COST-7793) | CRITICAL | OSAC integration | **Done** | [gap analysis](requirements/req1-osac-integration-gap-analysis.md) |
| 4 | REQ-1b | [COST-7795](https://redhat.atlassian.net/browse/COST-7795) | CRITICAL | Heartbeat ingestion | **Done** | Local 60s sweep ([ADR-003](decisions/003-heartbeat-emitter-vs-sweep.md)) |
| 5 | REQ-2 | [COST-7796](https://redhat.atlassian.net/browse/COST-7796) | CRITICAL | Real-time cost calc | **Done** | <1ms/event, cost within 30s |
| 6 | REQ-1a | [COST-7794](https://redhat.atlassian.net/browse/COST-7794) | HIGH | Cluster lifecycle | **Done** | ClusterOrder is the ordering workflow; we track the resulting Cluster (verified) |
| 7 | REQ-3a | [COST-7799](https://redhat.atlassian.net/browse/COST-7799) | HIGH | Tenant/project attribution | **Done** | Authz/RBAC open |
| 8 | REQ-3 | [COST-7798](https://redhat.atlassian.net/browse/COST-7798) | HIGH | Granular cost tracking | Partial | Report API done; project + user dimensions missing |
| 9 | REQ-9 | [COST-7801](https://redhat.atlassian.net/browse/COST-7801) | HIGH | Quota/budget status API | **Done** | `GET /api/v1/quotas/{tenant_id}` |
| 10 | REQ-10 | [COST-7807](https://redhat.atlassian.net/browse/COST-7807) | HIGH | Threshold notifications | **Done** (pull) | Webhook push deferred |
| 11 | REQ-13 | [COST-7810](https://redhat.atlassian.net/browse/COST-7810) | HIGH | Custom rate dimensions | **Done** | [Design](research/req13-custom-metrics-design.md) |
| 12 | REQ-2a | [COST-7797](https://redhat.atlassian.net/browse/COST-7797) | HIGH | MaaS CloudEvents + tokens | **Done** (emulator) | IPP verified with real plugin + echo LLM. [Stress test](dev/ipp-stress-test-2026-07-05.md) |
| 13 | REQ-3b | [COST-7800](https://redhat.atlassian.net/browse/COST-7800) | MEDIUM | Service catalog sync | **Done** | Catalog items synced via reconciler |
| 14 | REQ-5 | [COST-7801](https://redhat.atlassian.net/browse/COST-7801) | MEDIUM | Chargeback reporting | Partial | Report API done; scheduled export TBD |
| 15 | REQ-7 | [COST-7802](https://redhat.atlassian.net/browse/COST-7802) | MEDIUM | Audit trail | **Done** | `raw_events` + [Splunk forwarding](splunk-audit-forwarding.md) |
| 16 | REQ-11 | [COST-7808](https://redhat.atlassian.net/browse/COST-7808) | LOW | Cost tiers | **Partial** | [req11 gap analysis](requirements/req11-cost-tiers-gap-analysis.md) — MaaS tiers done; capacity cumulative tiers gap |
| 17 | REQ-12 | [COST-7808](https://redhat.atlassian.net/browse/COST-7808) | LOW | Daily OCP Virt costs | TBD | PM definition pending |
| 18 | REQ-8 | [COST-7801](https://redhat.atlassian.net/browse/COST-7801) | HIGH | Bare metal costing | **Done** | [gap analysis](requirements/req8-bare-metal-gap-analysis.md) |

**Post-PoC:**

| Req | JIRA | Title | Status | Notes |
|---|---|---|---|---|
| REQ-6 | — | Security & access control | N/A | In-product |

---

## Detailed Breakdown

### CRITICAL Requirements

### POC-ENV — On-Premise Deployment
**Status:** Partial
**Spec:** [csv_poc_requirements_summary.md#poc-env](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md#poc-env--on-premise-deployment)

CRC deployment is documented and tested. Full RHCM on-prem (Helm/OLM)
is a separate concern owned by the RHCM team.

| Step | Status | Reference |
|---|---|---|
| CRC deployment checklist | Done | [crc-deployment-checklist.md](dev/crc-deployment-checklist.md) |
| Full stack guide (OSAC + consumer + DB) | Done | [crc-full-deployment.md](dev/crc-full-deployment.md) |
| OSAC on CRC (cert-manager, CNPG, OIDC) | Done | [crc-osac-deployment.md](dev/crc-osac-deployment.md) |
| Dev setup guide | Done | [crc-dev-setup.md](dev/crc-dev-setup.md) |
| Deployment plan (CRC → production path) | Done | [crc-full-deployment.md](dev/crc-full-deployment.md) |
| RHCM Helm chart / OLM | Not started | RHCM team scope |

---

### POC-ARCH — Capacity-Based Charging Model
**Status:** Done
**Spec:** [csv_poc_requirements_summary.md#poc-arch](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md#poc-arch--capacity-based-charging-model)

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
**Spec:** [csv_poc_requirements_summary.md#req-1](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md#req-1--osac-integration-via-region-management-cluster)

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
**Spec:** [csv_poc_requirements_summary.md#req-2](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md#req-2--near-real-time-cost-calculation)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Process events within 60 seconds | Done | <1ms per event |
| End-to-end latency under 90 seconds | Done | Metering: 60s sweep + Rating: 30s sweep |
| Cost report available after processing | Done | `cost_entries` table populated; [`snippets/query-costs.sh`](../snippets/query-costs.sh) |
| Demonstrated with at least one workload | Done | VMs + MaaS models |

---

## HIGH Requirements

### REQ-1a — Cluster Lifecycle via Cluster Orders
**Status:** Done
**Spec:** [csv_poc_requirements_summary.md#req-1a](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md#req-1a--osac-cluster-lifecycle-via-cluster-orders)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Monitor cluster orders for state changes | Done | We track `Cluster` objects — ClusterOrder is the ordering workflow, the Cluster is the provisioned resource ([resolved](requirements/osac-open-questions.md#cluster-lifecycle-req-1a)) |
| State changes captured | Done | Watch stream CREATED/UPDATED/DELETED |
| Cluster rate configured per cluster order | Done | [`internal/rating/rating.go`](../inventory-watcher/internal/rating/rating.go) — `cluster_uptime_seconds`, `cluster_worker_node_seconds` rates |
| Cost based on provisioned capacity + duration | Done | [`internal/metering/metering.go`](../inventory-watcher/internal/metering/metering.go) — `clusterMeters` |

---

### REQ-3 — Granular Cost Tracking
**Status:** Partial
**Spec:** [poc_requirements_overview.md#req-3](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-3-granular-cost-tracking)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Cost filterable by tenant | Done | `?group_by=tenant&tenant_id=X` |
| Cost filterable by model/SKU | Done | `?group_by=resource` shows per-resource costs |
| Cost filterable by project | Gap | Projects synced to `inventory_project` but not available as report dimension |
| Cost filterable by user | Gap | No user tracking — IPP events have `user` field but we don't extract it |
| Dashboard with near-real-time consumption | Done | Debug dashboard + Grafana |
| Reporting supports CSV and JSON export | Done | `?format=csv`; JSON default |
| Financial data decoupled from infra state | Done | `cost_entries` table independent of inventory |

**Gaps:**
- **Project dimension:** `inventory_project` exists but report API has no `?group_by=project`
- **User dimension:** IPP CloudEvents carry a `user` field that we discard during ingestion. Need to store user on metering/cost entries and add `?group_by=user` to reports
- **Application dimension:** No concept of "application" — may map to OSAC project labels

---

### REQ-3a — Tenant/Project Attribution
**Status:** Done
**Spec:** [csv_poc_requirements_summary.md#req-3a](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md#req-3a--osac-tenantproject-attribution)

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
resolution (see [OSAC open questions](requirements/osac-open-questions.md#bare-metal-req-8)).

**Related docs:** [req8 gap analysis](requirements/req8-bare-metal-gap-analysis.md)

---

### REQ-9 — Quota/Budget Status API
**Status:** Done
**Spec:** [csv_poc_requirements_summary.md#req-9](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md#req-9--quotabudget-status-api)

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
| Receive and process MaaS CloudEvents | Done | [`internal/ingest/handler.go`](../inventory-watcher/internal/ingest/handler.go) |
| Events ingested within 30 seconds | Done | <1ms per event |
| CloudEvents format parsed and stored | Done | `raw_events` table |
| MaaS cost computed within 60s | Done | Rating sweep every 30s |
| IPP integration (checkBalance + reportUsage) | Done (emulator) | Verified with real IPP plugin (PR #320) + llm-katan echo LLM. [Stress test: 850 req/s](dev/ipp-stress-test-2026-07-05.md) |

**What is verified vs emulated:**
- **Real:** IPP external-metering plugin (PR #320 build), Istio ext_proc
  wiring, our checkBalance and reportUsage endpoints
- **Emulated:** LLM backend (llm-katan echo mode), X-MaaS-* identity
  headers (manually injected, no Authorino)

**Open questions:**
- It is unclear whether OSAC will add a formal Model entity to the
  fulfillment-service or keep models as identifiers in CloudEvents only.
  Our implementation works either way (see [open question #9](requirements/osac-open-questions.md#maas-req-2a--req-4)).
- MaaS tenant attribution: subscription → tenant mapping needs
  confirmation (see [open question #19](requirements/osac-open-questions.md#maas-tenant-attribution-ipp-integration)).
- See [MaaS flow](maas-flow.md), [IPP overview](research/ipp-overview.md),
  [k3d deployment guide](dev/k3d-ipp-deployment.md),
  [tenant attribution](research/maas-tenant-attribution.md).

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
**Status:** Done
**Spec:** [poc_requirements_overview.md#req-13](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-13--custom-rate-dimensions-custom-metrics)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Consume arbitrary CloudEvent dimensions as rate inputs | Done | [`internal/custommetrics/custommetrics.go`](../inventory-watcher/internal/custommetrics/custommetrics.go) — config-driven extraction |
| New dimensions configured with ID, classification, rate name | Done | JSON config file via `CUSTOM_METRICS_CONFIG` env var |
| Custom dimension data stored and available for cost/reporting | Done | Metering entries flow through existing rating + reporting pipeline |

**Design:** [req13-custom-metrics-design.md](research/req13-custom-metrics-design.md)
**Related Jira:** [COST-3549](https://redhat.atlassian.net/browse/COST-3549)
**Phase 2:** GoRules/Zen for complex rating logic — see [rating engine research](research/rating-engine-options.md)

---

## MUST HAVE Requirements

### REQ-11 — Cost Tiers
**Status:** Partial
**Gap Analysis:** [req11-cost-tiers-gap-analysis.md](requirements/req11-cost-tiers-gap-analysis.md)
**Spec:** [poc_requirements_overview.md#req-11](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-11--cost-tiers)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Multiple pricing tiers per resource type | Done | `rates.tiers` JSONB column; [`internal/rating/rating.go`](../inventory-watcher/internal/rating/rating.go) → `applyTieredRate` |
| Tiers apply to capacity and MaaS rates | **Gap** | Per-event logic is correct for MaaS; capacity meters require cumulative/period-accumulating tier logic — not yet implemented |
| Tier config without code changes | Done | JSON in `rates` table; no recompile needed |

---

## MEDIUM Requirements

### REQ-3b — Service Catalog Sync from OSAC
**Status:** Done
**Spec:** [csv_poc_requirements_summary.md#req-3b](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md#req-3b--service-catalog-sync-from-osac)

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
**Spec:** [poc_requirements_overview.md#req-5](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-5-chargeback-reporting)

| Acceptance Criterion | Status | Implementation |
|---|---|---|
| Reports map compute hours + tokens per tenant | Done | `GET /api/v1/reports/costs?group_by=tenant` covers both capacity and consumption cost types |
| Reports per project | Gap | No `project_id` column on `cost_entries`, no `group_by=project` (same schema gap as REQ-3) |
| Exportable CSV | Done | `?format=csv` — sets `Content-Type: text/csv` and `Content-Disposition: attachment` |
| Exportable JSON | Done | Default format with `meta`/`data` structure and Infrastructure/Supplementary split |
| Consistent with dashboard | Done | Debug dashboard uses same `/api/v1/reports/costs` endpoint |
| Scheduled/periodic export | Gap | On-demand API only — no cron, no automated report generation |

**Gaps:**
- **Project dimension:** same as REQ-3 — `cost_entries` has no `project_id`, report can't group by project
- **Scheduled export:** no automated report delivery (e.g., daily CSV to S3 or email). API exists but nothing triggers it periodically
- **No test coverage** for `handleCostReport` — untested in `handler_test.go`

**What works:** The report API is functional for on-demand use. Groups by 4 dimensions, filters by tenant/resource_type/period, exports CSV and JSON with Koku-compatible cost type split.

See also: [`snippets/query-costs.sh`](../snippets/query-costs.sh) for demo queries, [Bruno collection](../bruno-collection/) for interactive testing.

---

## Future Work (Post-PoC)

| Req | Title | Status | Notes |
|---|---|---|---|
| [REQ-6](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-6--platform-security--access-control) | Security & Access Control | N/A | In-product, no gap |
| [REQ-7](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-7--reconciliation-auditing--dispute-tracing) | Reconciliation & Auditing | **Done** | `raw_events` + [Splunk HEC forwarding](splunk-audit-forwarding.md) |
| [REQ-12](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md#req-12--daily-openshift-virtualization-costs) | Daily OCP Virt Costs | TBD | Pending PM confirmation |
| — | RBAC / Access Control for cost data | Not started | Track separately. Insights RBAC (Koku) vs Keycloak (OSAC). See [open question #18](requirements/osac-open-questions.md). Affects REQ-3a and REQ-6. |

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
| [Observability Plan](observability.md) | Metrics, logging, probes, shutdown (P1+P2 done) |
| [Rating Engine Options](research/rating-engine-options.md) | CloudKitty, GoRules, Drools evaluation |
| [req1 Gap Analysis](requirements/req1-osac-integration-gap-analysis.md) | OSAC integration implementation details |
| [req2 Gap Analysis](requirements/req2-maas-costing-gap-analysis.md) | MaaS costing implementation details |
| [req8 Gap Analysis](requirements/req8-bare-metal-gap-analysis.md) | Bare metal costing — OSAC blockers and implementation plan |
| [req10 Analysis](requirements/req10-threshold-notifications-analysis.md) | Threshold notifications — delivery models, open questions |
| [Requirements Comparison](requirements/requirements-comparison.md) | Updated spec vs original brief |
| [Demo Scenario 1](demos/demo-scenario-1.md) | Infrastructure metering demo |
| [Demo Scenario 2](demos/demo-scenario-2-maas.md) | MaaS metering + cost demo |
| [Local Dev Setup](dev/local-dev-setup.md) | How to run everything |

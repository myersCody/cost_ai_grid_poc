# Requirements Comparison:

> Comparing the [requirements overview](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md)
> against what we've built in the `osac-cost-consumer` branch.

## Key Changes from Original Requirements Brief

The updated spec **refines and reprioritizes** the original requirements:

1. **MaaS is now explicitly in scope for the PoC** — token metering (REQ-4)
   and OpenShift AI cloud events (REQ-2a) are no longer deferred; v1.3 states
   "MaaS ... Cost Tiers, and Custom Metrics are included as in-scope PoC
   requirements." Our MaaS work (built ahead of schedule under the old
   "deferred" framing) is now core PoC scope, not a bonus.

2. **Heartbeat events are the primary data source** (REQ-1b). Heartbeat
   events are HTTP-based with configurable intervals (10-30s) containing
   tenant/project/resource/hardware info — the same CloudEvent types OSAC's
   metering collector produces. Our HTTP ingest endpoint now accepts these
   directly (`osac.cluster.lifecycle`, `osac.compute_instance.lifecycle`),
   in the collector's exact format; only an OSAC-side URL redirect remains.
   See [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md).

3. **90-second end-to-end SLA** (POC-ARCH) — OSAC sends within 30s, Cost
   processes within 60s. We meet this (<1ms per event).

4. **Cluster Orders** are the OSAC ordering workflow for clusters (REQ-1a).
   ClusterOrder is the purchase request; the resulting Cluster is the
   provisioned resource we track for cost. Resolved — see
   [open question #15](osac-open-questions.md#cluster-lifecycle-req-1a).

5. **REQ-9 (Quota API) shipped; REQ-10 (Notifications) parked.** Both were
   elevated to HIGH in the prior revision. REQ-9 is now **Done**. Per the
   Jul 2 decision, REQ-10 is **parked** — OSAC's existing MaaS check-balance
   API plus our pull-model quota endpoint are sufficient for Jul 31; the
   push/webhook mechanism has no timeline.

6. **REQ-8 (Bare Metal) parked (post-PoC)** — deferred from Jul 31 scope per the Jul 2 decision.

7. **REQ-3a RBAC scope decided** — tenant + project level only for the PoC;
   fine-grained InsightsRBAC deferred post-PoC.

8. **Three ingestion modes are documented** (direct Watch/reconciliation,
   temporary Kafka experiment, and batch API). Batch ingestion is the primary
   delivery path; direct Watch/reconciliation remains available for local and
   fallback operation. See [Ingestion modes](../ingestion-modes.md). MaaS event
   transport (HTTP vs Kafka vs OSAC-as-intermediary) is still an [open
   question](osac-open-questions.md#event-transport) for the Jul 7 OSAC
   sync. Separately, MaaS **tenant attribution** is now resolved: the
   `organization_id` field flows end-to-end from MaaSSubscription
   TokenMetadata to `tenant_id` on our metering entries, verified in a
   [tenant attribution experiment](../dev/tenant-attribution-experiment-2026-07-08.md)
   and merged (PR #39, PR #47). Production auto-injection of the header via
   Authorino/maas-api is still pending upstream. MaaS **project**
   attribution remains an open product decision (subscription vs. model
   namespace).

9. **Report API gained date filtering, daily resolution, and a
   line-item breakdown endpoint** (PR #42, merged Jul 8) —
   `GET /api/v1/reports/breakdown` returns per-resource rows
   (tenant/project/resource/meter/cost) for a date range, `from`/`to`
   params replace month-only periods, and `?resolution=daily` adds a
   date column to the cost report. Response envelope is now
   Koku-compatible (`meta.total` with nested `cost`/`infrastructure`/
   `supplementary` blocks). This closes most of the REQ-3/REQ-5 gap —
   `group_by=user` added in PR #59, closing this gap.

10. **REQ-7 (Audit trail) is now Done** — a Splunk HEC forwarder
    (PR #46) streams the immutable `raw_events` log to Splunk for
    compliance search and dispute resolution, closing the gap noted in
    the prior revision. See [Splunk audit forwarding](../splunk-audit-forwarding.md).

11. **REQ-11 (Cost Tiers)** is **Partial** — done for MaaS per-event
    rates, gap remains for capacity cumulative tiers (GiB-month, core-hours).

12. REQ-12 (Daily OpenShift Virtualization Costs) is **TBD** — blocked on PM
    defining concrete acceptance criteria; no implementation started.

## Requirement-by-Requirement Status

### CRITICAL Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| POC-ENV | On-premise deployment | **Partial** | CRC deployment guides done; RHCM Helm/OLM chart not our scope |
| POC-ARCH | Capacity-based charging model | **Done** | Standalone component, heartbeat-driven, capacity-based. Matches exactly. |
| REQ-1 | OSAC integration via Region Management Cluster | **Done** | Connected via gRPC Watch + REST. Reads inventory, state, tenant. |
| REQ-1b | Heartbeat event ingestion | **Done** | `POST /api/v1/events` accepts the OSAC metering collector's heartbeat CloudEvents (`osac.cluster.lifecycle`, `osac.compute_instance.lifecycle`) directly, writing pre-calculated `duration_seconds`/`cpu_core_seconds`/etc. to `metering_entries`. Remaining work is OSAC-side only (collector URL redirect). See [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md). |
| REQ-2 | Near-real-time cost calculation | **Done** | <1ms per event, cost entries within 30s. Exceeds 60s SLA. |

### HIGH Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| REQ-1a | Cluster lifecycle via cluster orders | **Done** | ClusterOrder is the ordering workflow; we track the resulting Cluster (verified — [open question #15](osac-open-questions.md)) |
| REQ-3 | Granular cost tracking | **Done** | Report API with tenant/project/user/resource dimensions, date range (`from`/`to`) + daily resolution, `GET /api/v1/reports/breakdown` for per-resource drill-down. `group_by=user` added in PR #59 |
| REQ-3a | Tenant/project attribution | **Done** | Tenant → Project hierarchy in inventory; costs attributed per tenant. RBAC scope now decided (tenant + project only) but not yet built |
| REQ-8 | Bare metal costing | **Done, but parked (post-PoC)** | Fully implemented ([gap analysis](req8-bare-metal-gap-analysis.md)) — reconciler + inventory + metering + rates. Explicitly deferred from Jul 31 demo scope by the Jul 2 decision, independent of our implementation status |
| REQ-9 | Quota/budget status API | **Done** | `GET /api/v1/quotas/{tenant_id}` — sub-second, threshold flags at 50/70/90/100% |
| REQ-10 | Threshold notifications to OSAC | **Done (pull), parked (push)** | Pull model shipped via quota API's `alerts` field; push/webhook deferred indefinitely per Jul 2 decision — no longer a PoC gap |
| REQ-13 | Custom rate dimensions | **Done** | Config-driven extraction of arbitrary CloudEvent fields as metering entries. [Design](../research/req13-custom-metrics-design.md) |
| REQ-2a | Cloud events from OpenShift AI (MaaS) & token metering | **Done (mock/emulator)** | IPP verified with real external-metering plugin + echo LLM. [Stress test](../dev/ipp-stress-test-2026-07-05.md). MaaS tenant attribution now implemented via `organization_id` end-to-end ([experiment report](../dev/tenant-attribution-experiment-2026-07-08.md)); production Authorino/maas-api wiring still pending upstream. Project attribution for MaaS still needs a product decision |

### MEDIUM Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| REQ-3b | Service catalog sync from OSAC | **Done** | Catalog sync + per-SKU pricing via `instance_type` rate dimension (PR #59) + catalog fallback for cores/memory; rate auto-derivation from catalog not built (manual seeding acceptable for PoC) |
| REQ-5 | Chargeback reporting | **Done** | Report API with all dimensions, date filtering, daily resolution, breakdown drill-down, CSV/JSON export. [CronJob export](../dev/scheduled-chargeback-export.md) pattern documented and verified on k3d |
| REQ-7 | Audit trail | **Done** | `raw_events` table provides immutable audit log; [Splunk HEC forwarder](../splunk-audit-forwarding.md) streams events for compliance search and dispute resolution |

### LOW Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| REQ-11 | Cost tiers | **Partial** | Tiered pricing engine done and correct for MaaS (per-event). Capacity meters (GiB-month, core-hours) need cumulative/period-accumulating tier logic — not yet implemented. See [gap analysis](req11-cost-tiers-gap-analysis.md) |
| REQ-12 | Daily OpenShift Virtualization costs | **TBD** | Blocked on PM definition of acceptance criteria; not yet started |

### Parked / Deferred (per Jul 2, 2026 decisions)

| Req | Title | Our Status | Notes |
|---|---|---|---|
| REQ-8 | Bare metal costing | **Done**, deferred from demo | See HIGH table above — implementation complete, just not in the Jul 31 demo script |
| REQ-10 | Threshold notification push/webhook | **Not planned for PoC** | Pull model (REQ-9) accepted as sufficient; no timeline for push |

### Future Work / Out of Scope for Consumer Component

| Req | Title | Our Status | Notes |
|---|---|---|---|
| REQ-6 | Security & access control | N/A | In-product, no gap. On-prem RBAC/security review tracked under POC-ENV |
| POC-ENV | On-premise deployment | **Partial** (see CRITICAL table) | RHCM-owned Helm/OLM chart, not consumer scope |

## Remaining Gaps (updated Jul 18)

All CRITICAL, HIGH, and MEDIUM requirements are **Done**. The only
remaining open items are LOW priority:

1. **REQ-11 — Capacity cumulative tiers** (Partial) — MaaS per-event
   tiers work; capacity meters (GiB-month, core-hours) need
   period-accumulating logic. Blocked on PM spec from Pau. See
   [design proposal](req11-cumulative-tiers-design-proposal.md).

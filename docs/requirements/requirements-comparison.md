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

8. **No Kafka needed for PoC** (transport question raised in REQ-1b, REQ-2a,
   REQ-10) — gRPC Watch stream + 60s reconciler is the transport for
   VM/cluster events; Kafka is deferred and only warranted if multiple
   independent consumers need the same event stream. Aligns with our
   [ADR-002](../decisions/002-arguments-against-kafka.md). MaaS event
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
   only the `group_by=user` dimension remains open.

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
| POC-ENV | On-premise deployment | **Not started** | Out of scope for our component — this is about deploying RHCM on-prem, not the consumer |
| POC-ARCH | Capacity-based charging model | **Done** | Standalone component, heartbeat-driven, capacity-based. Matches exactly. |
| REQ-1 | OSAC integration via Region Management Cluster | **Done** | Connected via gRPC Watch + REST. Reads inventory, state, tenant. |
| REQ-1b | Heartbeat event ingestion | **Done** | `POST /api/v1/events` accepts the OSAC metering collector's heartbeat CloudEvents (`osac.cluster.lifecycle`, `osac.compute_instance.lifecycle`) directly, writing pre-calculated `duration_seconds`/`cpu_core_seconds`/etc. to `metering_entries`. Remaining work is OSAC-side only (collector URL redirect). See [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md). |
| REQ-2 | Near-real-time cost calculation | **Done** | <1ms per event, cost entries within 30s. Exceeds 60s SLA. |

### HIGH Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| REQ-1a | Cluster lifecycle via cluster orders | **Done** | ClusterOrder is the ordering workflow; we track the resulting Cluster (verified — [open question #15](osac-open-questions.md)) |
| REQ-3 | Granular cost tracking | **Partial** | Report API done, filterable by tenant/resource, CSV+JSON export, date range (`from`/`to`) + daily resolution. `group_by=project` shipped Jul 4; `GET /api/v1/reports/breakdown` (Jul 8) adds per-resource line-item drill-down. Missing: `group_by=user` dimension |
| REQ-3a | Tenant/project attribution | **Done** | Tenant → Project hierarchy in inventory; costs attributed per tenant. RBAC scope now decided (tenant + project only) but not yet built |
| REQ-8 | Bare metal costing | **Done, but parked (post-PoC)** | Fully implemented ([gap analysis](req8-bare-metal-gap-analysis.md)) — reconciler + inventory + metering + rates. Explicitly deferred from Jul 31 demo scope by the Jul 2 decision, independent of our implementation status |
| REQ-9 | Quota/budget status API | **Done** | `GET /api/v1/quotas/{tenant_id}` — sub-second, threshold flags at 50/70/90/100% |
| REQ-10 | Threshold notifications to OSAC | **Done (pull), parked (push)** | Pull model shipped via quota API's `alerts` field; push/webhook deferred indefinitely per Jul 2 decision — no longer a PoC gap |
| REQ-13 | Custom rate dimensions | **Done** | Config-driven extraction of arbitrary CloudEvent fields as metering entries. [Design](../research/req13-custom-metrics-design.md) |
| REQ-2a | Cloud events from OpenShift AI (MaaS) & token metering | **Done (mock/emulator)** | IPP verified with real external-metering plugin + echo LLM. [Stress test](../dev/ipp-stress-test-2026-07-05.md). MaaS tenant attribution now implemented via `organization_id` end-to-end ([experiment report](../dev/tenant-attribution-experiment-2026-07-08.md)); production Authorino/maas-api wiring still pending upstream. Project attribution for MaaS still needs a product decision |

### MEDIUM Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| REQ-3b | Service catalog sync from OSAC | **Done** | Catalog items (cluster, compute_instance, bare_metal_instance) synced via reconciler; rates still seeded as defaults, not auto-derived from catalog pricing |
| REQ-5 | Chargeback reporting | **Partial** | Report API covers capacity + MaaS cost types, CSV/JSON export, `group_by=project`, date filtering + daily resolution, breakdown drill-down. Scheduled export documented as a [Kubernetes CronJob pattern](../dev/scheduled-chargeback-export.md) calling the report API (verified on k3d) — pending PM sign-off on whether that satisfies the acceptance criterion. Missing: `group_by=user` dimension (same schema gap as REQ-3) |
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

## Remaining Gaps to Close for PoC

### 1. Chargeback Reporting / Export (REQ-3/REQ-5) — MEDIUM

**What the spec says:** Cost data filterable by tenant/project/model/user.
Export in CSV and JSON.

**What we have:** Report API (`GET /api/v1/reports/costs`) covers
capacity + MaaS cost types with CSV/JSON export, filterable by
tenant/resource, and groupable by tenant/resource_type/meter/resource/
**project** (project dimension wired end-to-end Jul 4 — schema, metering
sweep, rating sweep, custom metrics, and report API). As of Jul 8
(PR #42), it also supports `from`/`to` date filtering, `?resolution=daily`,
a Koku-compatible response envelope, and a new
`GET /api/v1/reports/breakdown` endpoint for per-resource line-item
drill-down. Periodic export is documented as a
[Kubernetes CronJob pattern](../dev/scheduled-chargeback-export.md)
calling the existing report API on a schedule — verified on k3d,
pending PM confirmation that this satisfies the "exportable" acceptance
criterion (vs. a built-in scheduler).

**Gap:** Missing `group_by=user` dimension — IPP CloudEvents carry a
`user` field we currently discard during ingestion.

**Effort:** Small — extend the existing query builder with a user
dimension (requires storing `user` on metering/cost entries first).

## Recommended Priority Order

Based on the updated spec priorities, the only PoC-scope item still open
is:

1. **User report dimension** (REQ-3/REQ-5, MEDIUM) — store `user` on
   metering/cost entries and add `group_by=user` to the existing report
   API

Everything else in the original priority list is already resolved:
REQ-1b (heartbeat ingestion), REQ-9 (quota status API), REQ-10 (pull
notifications), REQ-7 (audit trail via Splunk forwarding), and the
REQ-3/REQ-5 project dimension + breakdown/date-filtering enhancements
are **Done**; REQ-8 (bare metal) is fully implemented but deferred from
Jul 31 demo scope by the Jul 2, 2026 decision.


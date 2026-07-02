# Requirements Comparison: Updated Spec vs Our Implementation

> Comparing the [requirements overview](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md)
> against what we've built in the `osac-cost-consumer` branch.

## Key Changes from Original Requirements Brief

The updated spec **refines and reprioritizes** the original requirements:

1. **MaaS is explicitly out of scope for PoC** — token metering (REQ-4) and
   OpenShift AI events (REQ-2a) are deferred to a separate MaaS workstream.
   Our MaaS implementation is ahead of schedule — useful for the MaaS
   workstream when it starts, but not needed for PoC deadline.

2. **Heartbeat events are the primary data source** (REQ-1b) — not the Watch
   stream we're using. Heartbeat events are HTTP-based with configurable
   intervals (10-30s) containing tenant/project/resource/hardware info. This
   is essentially what the OSAC metering collector produces.

3. **90-second end-to-end SLA** (POC-ARCH) — OSAC sends within 30s, Cost
   processes within 60s. We meet this (<1ms per event).

4. **Cluster Orders** are the OSAC entity for clusters (REQ-1a) — not raw
   Cluster objects. We track clusters but not cluster orders specifically.

5. **Quota API** (REQ-9) and **Notifications** (REQ-10) are HIGH priority —
   elevated from the original brief.

6. **No Kafka needed for PoC** — HTTP heartbeat events are the transport.
   Aligns with our ADR-002 arguments.

## Requirement-by-Requirement Status

### CRITICAL Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| POC-ENV | On-premise deployment | **Not started** | Out of scope for our component — this is about deploying RHCM on-prem, not the consumer |
| POC-ARCH | Capacity-based charging model | **Done** | Standalone component, heartbeat-driven, capacity-based. Matches exactly. |
| REQ-1 | OSAC integration via Region Management Cluster | **Done** | Connected via gRPC Watch + REST. Reads inventory, state, tenant. |
| REQ-1b | Heartbeat event ingestion | **Partial** | We use Watch stream, not HTTP heartbeats. Ingest endpoint exists but accepts CloudEvents, not heartbeat format. See "Gaps" below. |
| REQ-2 | Near-real-time cost calculation | **Done** | <1ms per event, cost entries within 30s. Exceeds 60s SLA. |

### HIGH Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| REQ-1a | Cluster lifecycle via cluster orders | **Partial** | We track clusters but not "cluster orders" as a concept. Need to verify OSAC's cluster order API. |
| REQ-3 | Granular cost tracking | **Partial** | Cost data exists with drill-down by tenant, resource type, meter. No export API (CSV/JSON) yet. |
| REQ-3a | Tenant/project attribution | **Done** | Tenant → Project hierarchy in inventory. Costs attributed per tenant. |
| REQ-8 | Bare metal costing | **Not started** | OSAC bare metal service is being built. No BMaaS events defined yet. |
| REQ-9 | Quota/budget status API | **Not started** | No API endpoint. Metering data exists to compute quota consumption. |
| REQ-10 | Threshold notifications to OSAC | **Not started** | No notification mechanism. Would need webhook/event emitter. |

### MEDIUM Priority

| Req | Title | Our Status | Gap |
|---|---|---|---|
| REQ-3b | Service catalog sync from OSAC | **Partial** | We sync instance types. Manual rate setup done. No catalog API sync. |
| REQ-5 | Chargeback reporting | **Partial** | Cost data queryable. No export mechanism or formatted reports. |

### Deferred (MaaS workstream, not PoC)

| Req | Title | Our Status | Notes |
|---|---|---|---|
| REQ-2a | Cloud events from OpenShift AI | **Done (mock)** | Ahead of schedule — built for future MaaS workstream |
| REQ-4 | Token metering | **Done (mock)** | 4 token meters working with simulator |

### Out of Scope / Standard

| Req | Title | Our Status | Notes |
|---|---|---|---|
| REQ-6 | Security & access control | N/A | In-product, no gap |
| REQ-7 | Reconciliation & auditing | **Partial** | Raw event log provides immutable audit trail |

## Critical Gaps to Close for PoC

### 1. Heartbeat Event Format (REQ-1b) — CRITICAL

**What the spec says:** HTTP endpoint receiving heartbeat events with
tenant_id, project_id, resource_id, hardware config at 10-30s intervals.
Auto-register tenants on first event.

**What we have:** Watch stream consumption + HTTP ingest endpoint that
accepts CloudEvents format.

**Gap:** Our ingest endpoint accepts MaaS CloudEvents, not the heartbeat
format described in the spec. We need to either:
- Adapt the ingest endpoint to accept heartbeat events too
- Verify that the heartbeat format matches what the OSAC metering collector
  produces (likely yes — the collector script already exists)

**Effort:** Small — add a heartbeat event handler to the ingest endpoint.

### 2. Quota/Budget Status API (REQ-9) — HIGH

**What the spec says:** Fast API (sub-second) for OSAC to query:
"is this tenant within quota? What % of budget consumed?"
Threshold checks at 50%, 70%, 90%, 100%.

**What we have:** Metering entries and cost entries exist. No API to query
aggregated consumption against limits.

**Gap:** Need:
- `quotas` table (defined in data model but not implemented)
- API endpoint: `GET /api/v1/quotas/{tenant_id}/status`
- Aggregate metering by tenant + meter for current period
- Compare against quota limit, return percentage

**Effort:** Medium — new table, API endpoint, aggregation query.

### 3. Threshold Notifications (REQ-10) — HIGH

**What the spec says:** Send notifications to OSAC when consumption hits
50%, 70%, 90%, 100% of quota/budget.

**What we have:** Nothing.

**Gap:** Need:
- Alert evaluation in the rating sweep or quota check
- `alerts` table (defined in data model but not implemented)
- Webhook or event emitter to notify OSAC
- Deduplication (don't fire the same threshold alert repeatedly)

**Effort:** Medium — depends on REQ-9 (needs quota tracking first).

### 4. Bare Metal Costing (REQ-8) — HIGH

**What the spec says:** Consume bare metal service cloud events from OSAC.

**What we have:** Nothing.

**What's needed from OSAC:** BMaaS event schema definition. OSAC has a
BareMetalInstance entity in the fulfillment-service proto, so Watch stream
events should exist. We'd add a handler + meters like we did for VMs.

**Effort:** Small-Medium — same pattern as VM metering, just different entity.

### 5. Chargeback Reporting / Export (REQ-3/REQ-5) — MEDIUM

**What the spec says:** Cost data filterable by tenant/project/model/user.
Export in CSV and JSON.

**What we have:** Cost data in PostgreSQL, queryable via `snippets/query-costs.sh`.
No API endpoint or export mechanism.

**Gap:** Need a report API endpoint. Could be simple:
`GET /api/v1/reports/costs?tenant_id=X&group_by=meter_name&format=csv`

**Effort:** Medium — new API handler, query builder, CSV/JSON serializer.

## Recommended Priority Order

Based on the updated spec priorities:

1. **Heartbeat event format** (REQ-1b, CRITICAL) — adapt ingest endpoint
2. **Bare metal handler** (REQ-8, HIGH) — add BareMetalInstance to Watch
   stream dispatcher + meters
3. **Quota status API** (REQ-9, HIGH) — quotas table + status endpoint
4. **Threshold notifications** (REQ-10, HIGH) — alerts + webhook emitter
5. **Report/export API** (REQ-3/REQ-5, MEDIUM) — cost query endpoint

## What We Built That's Ahead of Schedule

- **MaaS metering** (REQ-2a/REQ-4) — explicitly deferred from PoC but
  we have it working with simulator and cost calculation
- **Rate engine with tiered pricing** — not a named requirement but
  essential for cost calculation
- **Immutable audit trail** (REQ-7) — raw_events table provides this
- **Arguments against Kafka** (ADR-002) — the updated spec confirms
  HTTP heartbeats, not Kafka, for the PoC

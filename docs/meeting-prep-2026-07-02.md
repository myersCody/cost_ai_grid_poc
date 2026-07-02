# Meeting Prep — July 2, 2026

## Work Summary (June 25 – July 1)

### Week 1 (June 25–27): Foundation

Built the **inventory-watcher** — a Go service consuming OSAC events and
producing cost data. End-to-end pipeline working:

- **OSAC integration** (REQ-1) — Watch stream for real-time events,
  reconciler for drift correction, projects/tenants/instance types synced
- **Metering** — 60-second sweep producing `vm_uptime_seconds`,
  `vm_cpu_core_seconds`, `vm_memory_gib_seconds`, `cluster_uptime_seconds`,
  `cluster_worker_node_seconds` for all billable resources
- **MaaS metering** (REQ-2a/REQ-4) — consumption-based token metering via
  ingest endpoint + simulator (1,700 events/s sustained)
- **Rate engine** (REQ-11) — flat + tiered pricing, default rates seeded,
  cost entries with dollar amounts
- **Quota status API** (REQ-9) — `GET /api/v1/quotas/{tenant_id}` with
  threshold checks at 50/70/90/100%
- **Threshold alerts** (REQ-10, pull model) — fired alerts included in
  quota API response
- **OpenMeter-compatible ingest** — `POST /api/v1/events` accepts the exact
  CloudEvents format the OSAC metering collector produces. Switching the
  collector from OpenMeter to us is a URL change.
- **Koku schema alignment** — rates table extended with `cost_type`
  (Infrastructure/Supplementary) and `koku_metric` for future rate sync

### Weekend (June 28–29): Hardening + Bare Metal

- **Adversarial code review** — 17 findings across 8 dimensions, 9 fixed:
  JWT auth, error handling, input validation, HTTP limits, pagination,
  scanner buffer, N+1 query, JSON injection, division by zero
- **JWT authentication** (#1) — OSAC-compatible, same `golang-jwt` library,
  same token works for both OSAC and our endpoints
- **OSAC List pagination** (#3) — offset/limit loop, prevents phantom
  deletes at scale
- **Bare metal costing** (REQ-8) — full implementation via reconciler
  polling + metering sweep

### Monday–Tuesday (June 30 – July 1): Reports + IPP

- **Report API** (REQ-3/REQ-5) — `GET /api/v1/reports/costs` and
  `/reports/summary`
- **IPP-compatible MaaS format** — 5 token dimensions matching the
  Inference Performance Protocol
- **Balance check endpoint** — IPP-compatible quota check
- **Dashboard demo** — interactive HTML dashboard for cost visualization
- **OpenShift/CRC deployment** — containerized and deployed on CRC:
  - Multi-stage UBI10 container image (164MB), published to quay.io
  - Kubernetes manifests (namespace, PostgreSQL StatefulSet, consumer Deployment)
  - Health probes (`/healthz`, `/readyz`) working
  - PostgreSQL connected, tables auto-created, HTTP API listening
  - OSAC integration pending (fulfillment-service not yet deployed in CRC)
- **Observability plan** — structured logging, metrics, health checks

### Demo (July 1)

Live demo to Cody showing the full pipeline:
- OSAC Watch stream → inventory sync in real-time
- VM creation → appears in cost DB within 1-2 seconds
- 60-second metering sweep → usage records
- 30-second rating sweep → dollar costs
- Quota API with threshold alerts
- MaaS simulator firing 500 events at 200/s
- OpenMeter-compatible CloudEvent ingestion
- Service running on CRC/OpenShift with health probes passing

---

## Current Implementation Status

**10 done / 4 partial / 2 not started** out of 16 requirements.

| Req | Status | What |
|---|---|---|
| POC-ARCH | **Done** | Capacity-based charging, standalone Go component |
| REQ-1 | **Done** | OSAC integration (Watch + reconciler + inventory) |
| REQ-1a | **Done** | Cluster lifecycle tracking |
| REQ-1b | **Done** | Heartbeat ingestion (local sweep = equivalent) |
| REQ-2 | **Done** | Real-time cost calc (<1ms/event) |
| REQ-2a | **Done** (mock) | MaaS CloudEvents (IPP format) |
| REQ-4 | **Done** (mock) | Token metering (5 dimensions) |
| REQ-8 | **Done** | Bare metal costing |
| REQ-9 | **Done** | Quota/budget status API |
| REQ-10 | **Done** (pull) | Threshold alerts in quota response |
| REQ-11 | **Done** | Cost tiers (tiered pricing) |
| REQ-3 | Partial | Report API implemented, export TBD |
| REQ-3a | **Done** | Tenant/project attribution |
| REQ-3b | Partial | Instance types synced, rates manual |
| REQ-5 | Partial | Report API, formatted export TBD |
| REQ-13 | Not started | Custom rate dimensions (GoRules research done) |
| POC-ENV | Not started | On-prem deployment (RHCM team scope) |

---

## Key Architectural Decisions

1. **No Kafka** — gRPC Watch stream + reconciler (Kubernetes pattern).
   See [ADR-002](decisions/002-arguments-against-kafka.md).

2. **Local metering sweep** — replicates what OSAC heartbeat collector
   would provide. Both produce identical `metering_entries`. The ingest
   endpoint is ready for real collector events.
   See [ADR-003](decisions/003-heartbeat-emitter-vs-sweep.md).

3. **OpenMeter compatibility** — our ingest endpoint accepts the same
   CloudEvents format. Switching the OSAC collector to us = URL change.

4. **Koku naming alignment** — `cost_type`, `koku_metric` on rates.
   Preparing for eventual merge with Koku.

---

## Open Questions for OSAC

See [meeting-questions-osac.md](meeting-questions-osac.md) for the full
list (18 questions across bare metal, catalog, private API, MaaS,
notifications, transport, tenancy).

**Top priority for this meeting:**

1. **Can we use the private Watch stream?** — unlocks real-time bare metal
   events and catalog item sync

2. **MaaS metrics ownership** — does OSAC collect from RHOAI or do we?

3. **Alert transport** — webhook endpoint on OSAC side for push
   notifications?

---

## Next Steps

1. **Deploy OSAC in CRC** — deploy fulfillment-service alongside our
   consumer for full integration testing on OpenShift
2. **Connect to real OSAC collector** — redirect from OpenMeter to our
   ingest endpoint
3. **Koku rate sync** — read rates from Koku's `cost_model` table
4. **Report API export** — CSV/JSON export for REQ-5
5. **Helm chart** — extract from working K8s manifests
6. **Custom rate dimensions** (REQ-13) — if time permits before July 31

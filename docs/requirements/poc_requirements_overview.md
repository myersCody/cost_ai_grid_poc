# AI Grid PoC — Cost Management Requirements

**Version:** 1.2
**Date:** June 26, 2026
**Status:** Hardening (Still in Flux)

This document is the consolidated requirements reference for the Cost Management AI Grid Proof of Concept. It merges the initial requirements brief with the detailed requirements summary. MaaS (token metering and OpenShift AI cloud events), Cost Tiers, and Custom Metrics are included as in-scope PoC requirements.

---

## Project Context

- Sovereign cloud built on OCP, OCP Virtualization, OpenShift AI, ACM, Ansible
- **OSAC** (Open Sovereign AI Console) is the orchestrator — provisions clusters (HCP), VMs (OpenShift Virtualization), models (MaaS), and bare metal
- OSAC emits **CloudEvents** for resource lifecycle and metrics; Kafka is the likely transport
- Billing model: **capacity-based** for clusters/VMs; **consumption-based** (token/request) for MaaS
- No Cost Management Metrics Operator (CMMO) — OSAC is the sole metric source
- Data freshness SLA: OSAC emits within 30 sec of event; **Cost must process within 60 sec of receipt**
- Tenancy model: `Tenant → Project → Resource (cluster/VM/bare metal/model)`

---

## Requirements

### POC-ENV — On-Premise Deployment
**Priority:** CRITICAL
**Rank:** 1
**Component Scope:** OUT OF SCOPE — This requirement covers deploying RHCM on-premise; it is owned by the RHCM/Cost Management team, not the consumer component.

On-prem RHCM deployment to demo capacity-based charging with OSAC heartbeat events. Not all components may be needed.

**Acceptance Criteria:**
- Cost Management deployed on-premise in a single cluster
- Tuned for performance, not feature completeness
- Can demonstrate end-to-end flow: consumption → event → ingestion → cost report

**Current State:**
- On-premise deployment is possible but not the standard deployment model
- Need to determine which cluster/environment to use

**Scope:**
- IN: Minimal on-prem deployment focused on data ingestion demo
- OUT: Full SaaS features, resource optimization, multi-cluster

---

### POC-ARCH: Capacity-Based Charging Model
**Priority:** CRITICAL
[COST-7792](https://redhat.atlassian.net/browse/COST-7792)
Rank: 2

Charge based on what was provisioned (VM size, cluster config) and for how long. No metric scraping, no CSV pipeline changes; existing cost models may partially work. This is a standalone PoC component — a new data path driven by heartbeat events from OSAC.

**Acceptance Criteria:**
- Costs calculated from provisioned capacity (instance type, duration)
- Heartbeat events from OSAC drive cost calculation
- No dependency on workload cluster metrics for PoC
- Existing SQL queries adapted to support capacity-based model
- Demo-ready: show cost for a provisioned cluster/VM within SLA

**Current State:**
- Per-cluster and per-VM cost models already exist in RHCM
- SQL queries may already partially support this
- Different pipeline than existing CSV ingestion
- CloudEvents = fundamentally different architecture from batch processing
- Cost Team must decide to build standalone PoC component

**Scope:**
- IN: Capacity-based charging for clusters and VMs
- OUT: Usage-based metering; per-workload metric scraping (token metering addressed by REQ-2a and REQ-4)

---

### REQ-1: OSAC Integration via Region Management Cluster
**Priority:** CRITICAL
[COST-7793](https://redhat.atlassian.net/browse/COST-7793)
Rank: 3

Connect RHCM to the OSAC Region Management Cluster (gRPC/REST APIs) to read inventory, resource state, and tenant/project hierarchy. All data flows through the management layer, not individual workload clusters.

**Acceptance Criteria:**
- RHCM connects to OSAC Region Management Cluster APIs (gRPC/REST)
- Can read inventory and resource state from OSAC
- Account/tenant lifecycle synced between OSAC and RHCM
- Workload-level info includes tenant ID, project ID, resource ID
- Integration does not degrade orchestrator UX

**Current State:**
- OSAC integration has never been attempted
- Previous integration exists as reference
- Region Management Cluster confirmed as integration point (Jun 23)
- OSAC uses gRPC and REST (not the Kubernetes API)
- Ecosystem/Flight Path team may contribute code

**Open Questions:**
- Full list of CloudEvent types OSAC will produce (CaaS, VMaaS, BMaaS, MaaS)

**References:**
- [VMaaS CloudEvents schema](https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README.md#cloudevents-schema)
- [CaaS CloudEvents schema](https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README-caas.md#cloudevents-schema)

**Scope:**
- IN: Connection to Region Management Cluster; reading inventory and state
- OUT: Quota enforcement; installing collectors on workload clusters (not needed for the capacity model)

---

### REQ-1b: OSAC Heartbeat Event Ingestion
**Priority:** CRITICAL
[COST-7795](https://redhat.atlassian.net/browse/COST-7795)
Rank: 4

Receive heartbeat events from OSAC via HTTP or Kafka (transport TBD per Jun 24 meeting) at configurable intervals (10s–30s). Events contain tenant ID, project ID, resource ID, and hardware config. The first event auto-registers the tenant.

> **What "heartbeat events" means:** CloudEvents emitted periodically by OSAC metering collector (`osac-metering-discover-poc`) — same schema as state transition events but fired on a timer and pre-populated with `duration_seconds` and metering quantities. The PoC satisfies this today via a local 60-second sweep; the collector is not required for the demo. See [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md) for the full explanation.

**Acceptance Criteria:**
- RHCM can receive heartbeat events (periodic lifecycle CloudEvents) via HTTP or Kafka
- Events parsed for: tenant ID, project ID, resource ID, hardware config, duration
- First event auto-creates tenant/project if not already registered
- Events processed and cost calculated within target SLA

**Current State:**
- PoC satisfies this requirement functionally via the local 60s sweep
- OSAC metering collector exists ([osac-metering-discover-poc](https://github.com/masayag/osac-metering-discover-poc)) but not yet connected to Cost Management
- Transport and delivery of heartbeat CloudEvents to Cost Management not yet agreed (R-5, R-6 in event-types.md)

**Open Questions:**
- Transport mechanism: Kafka, HTTP, NATS?
- Interval: every 10s, every 30s proposed on the Jun 23rd meeting

**Scope:**
- IN: Receive and process periodic lifecycle CloudEvents for capacity-based charging
- OUT: Usage-based metrics ingestion (not needed for capacity model)

---

### REQ-2: Near-Real-Time Cost Calculation
**Priority:** CRITICAL
[COST-7796](https://redhat.atlassian.net/browse/COST-7796)
Rank: 5

Process OSAC heartbeat events and calculate costs within 60 seconds of receipt. End-to-end SLA is 90 seconds. This is a new HTTP data path, not a rework of the CSV pipeline.

> **What "heartbeat events" means:** CloudEvents emitted periodically by OSAC metering collector (`osac-metering-discover-poc`) — same schema as state transition events but fired on a timer and pre-populated with `duration_seconds` and metering quantities. The PoC satisfies this today via a local 60-second sweep; the collector is not required for the demo. See [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md) for the full explanation.

**Acceptance Criteria:**
- RHCM processes OSAC heartbeat events within 60 seconds of receipt
- End-to-end latency under 90 seconds (OSAC send + RHCM process)
- Cost report available in dashboard after processing
- Demonstrated with at least one workload type in PoC

**Current State:**
- This is a NEW data path (HTTP events), not a rework of the CSV pipeline
- OpenShift-only data enables cheaper/faster SQL queries compared to cloud data
- Ingress component may not be needed for this flow

**Scope:**
- IN: Sub-60-second processing of OSAC heartbeat events
- OUT: Reworking the existing hourly CSV pipeline; production SLA guarantees; enforcement signals

---

### REQ-1a: OSAC Cluster Lifecycle via Cluster Orders
**Priority:** HIGH
[COST-7794](https://redhat.atlassian.net/browse/COST-7794)
Rank: 6

Monitor OSAC "cluster orders" for state changes (created, running, stopped, destroyed) and calculate cost based on provisioned capacity and duration.

**Acceptance Criteria:**
- RHCM monitors cluster orders via the OSAC management layer
- State changes (create, stop, start, destroy) are captured
- Cluster rate set in Cost Management per cluster order
- Cluster cost calculated based on provisioned capacity and duration
- No dependency on internal workload cluster data

**Current State:**
- OSAC APIs confirmed (gRPC/REST) at Region Management Cluster
- "Cluster orders" is the OSAC equivalent of a provisioned cluster
- Confirmed architecture in Jun 23 meeting

**Scope:**
- IN: Cluster order monitoring and capacity-based cost calculation
- OUT: Internal cluster metrics scraping (not needed for capacity model)

---

### REQ-3a: OSAC Tenant/Project Attribution
**Priority:** HIGH
[COST-7799](https://redhat.atlassian.net/browse/COST-7799)
Rank: 7

Map OSAC's `Tenant → Project` hierarchy to RHCM's organizational model. All costs attributed to the correct tenant and project.

**Acceptance Criteria:**
- Cost data attributed to the correct OSAC tenant
- Cost data can be drilled down to project level within a tenant
- Tenant/project hierarchy read from OSAC Region Management Cluster
- Multi-tenant attribution works even when all workloads run on shared infrastructure

**Current State:**
- RHCM currently attributes data per organization/cluster
- OSAC tenant/project hierarchy is documented
- Mapping OSAC tenants to RHCM organizations needs design

**Open Questions:**
- Will providers view cost in the Cost Management UI or in OSAC?
- Are quotas/budgets scoped per OSAC project?
- Is RBAC needed for providers viewing cross-project cost data?

**Scope:**
- IN: Tenant and project-level cost attribution
- OUT: Full tenant lifecycle management (onboarding, offboarding)

---

### REQ-3: Granular Cost Tracking
**Priority:** HIGH
[COST-7798](https://redhat.atlassian.net/browse/COST-7798)
Rank: 8

Single system of record for cost data with drill-down by tenant, project, model, and user. Covers both capacity-based (compute hours) and consumption-based (token/request) dimensions.

**Acceptance Criteria:**
- Cost data filterable by: tenant, model/SKU, application, user
- Dashboard shows near-real-time token consumption, compute hours, and estimated costs
- Reporting supports export in CSV and JSON
- Financial data decoupled from infrastructure state

**Current State:**
- Core RHCM cost tracking and reporting capability exists in-product
- Dashboard and export functionality are in-product

**Scope:**
- IN: Granular cost breakdowns at listed dimensions for both capacity and MaaS workloads
- OUT: Account hierarchy management

---

### REQ-9: Quota/Budget Status API
**Priority:** HIGH
[COST-7801](https://redhat.atlassian.net/browse/COST-7801)
Rank: 9

Provide a workflow to allow OSAC to check quota and budge status before allowing resource creation.

(e.g., "Is this tenant within quota?"). Enforcement remains with OSAC; RHCM provides the data.

**Definitions:**
- **Quota** = dimensional limit (CPU core-hours, GiB RAM-hour, tokens, etc.). Providers set quotas for tenants based on accumulated metered consumption over a period.
- **Budget** = monetary quota. Cost applied to metering = budget consumed.

**Acceptance Criteria:**
- API responds with sub-second latency
- OSAC can query: is tenant within quota? What % of budget consumed?
- Supports threshold checks (50%, 70%, 90%, 100%)
- Source of truth for quota data agreed between OSAC and RHCM
- Grace period requirements verified

**Current State:**
- No quota/budget API exists in RHCM today
- Open question: is RHCM or OSAC the source of truth for quota/budget data?
- Enforcement is OSAC responsibility; RHCM provides the data

**Open Questions:**
- Single source of truth for quotas: Cost, OSAC, or both (synchronized)?
- Are quotas/budgets scoped to tenant projects?
- Do AI Grid requirements include grace periods?

**Scope:**
- IN: Read-only quota/budget status API for OSAC consumption
- OUT: Quota enforcement (OSAC's responsibility); budget/limit definition UI

---

### REQ-10: Threshold Notification Back Channel to OSAC
**Priority:** HIGH
[COST-7807](https://redhat.atlassian.net/browse/COST-7807)
Rank: 10

Send threshold notifications from RHCM to OSAC when cost/quota consumption hits defined levels (50%, 70%, 90%, 100%). OSAC consumes these notifications to trigger OPA-enforced rate limiting. Transport and format TBD.

**Acceptance Criteria:**
- RHCM sends notifications to OSAC at configurable thresholds
- Notifications include: tenant ID, resource/project context, threshold level, current consumption
- Transport mechanism agreed between OSAC and RHCM
- Notifications delivered reliably (no silent drops)

**Current State:**
- No back channel from RHCM to OSAC exists today
- Jun 24 meeting: transport options discussed (webhook, Kafka, cloud events)
- OSAC architect to consult with OSAC working group architect about OSAC alerting capabilities
- Grace periods for budget overages may be required

**Open Questions:**
- Does OSAC have an existing alerting mechanism?
- Transport: Kafka, HTTP, NATS, CloudEvents?

**Scope:**
- IN: Threshold notification mechanism from RHCM to OSAC
- OUT: Alert UI in RHCM; grace period enforcement (OSAC's responsibility)

---

### REQ-13: Custom Metrics / Custom Rates
**Priority:** HIGH
[COST-7808](https://redhat.atlassian.net/browse/COST-7810)
Rank: 11

Ability to create a custom rate from an arbitrary metric dimension emitted by OSAC CloudEvents. Allows new dimensions to be metered without hardcoded support in RHCM.

**Acceptance Criteria:**
- RHCM can consume arbitrary CloudEvent dimensions as rate inputs
- New dimensions can be configured with an ID, classification, and rate name
- Custom dimension data is stored and available for cost calculation and reporting

**Current State:**
- No custom metric rate support exists in RHCM today
- Requires investigation of CloudEvent schema extensibility

**Open Questions:**
- Who defines new dimensions to collect: OSAC or Cost team?
- ID, classification, and rate naming scheme to be agreed

**Related Ticket:** COST-3549

**Scope:**
- IN: Configurable ingestion of arbitrary CloudEvent dimensions as billable rate inputs
- OUT: UI for custom metric management (API/config acceptable for PoC)

---

### REQ-2a: Cloud Events from OpenShift AI (MaaS) & Token Metering
**Priority:** HIGH
[COST-7797](https://redhat.atlassian.net/browse/COST-7797)
Rank: 12

Consume CloudEvents from OpenShift AI 5 for token metering. OSAC emits CloudEvents with token counts (input, output, inference) and request counts. Track token dimensions (input, output, cached, reasoning) and GPU compute metrics for MaaS workloads provisioned via OSAC. Define MaaS rate structure priced per million units. Cost must compute MaaS cost within 60 seconds of receiving data.

**Acceptance Criteria:**
- RHCM can receive and process CloudEvents from OpenShift AI / OSAC for MaaS workloads
- Events ingested within 30 seconds of emission
- JSON/CloudEvents format parsed and stored
- MaaS cost computed within 60 seconds of event receipt
- Validated with at least one MaaS workload type
- Ingest `prompt_tokens`, `completion_tokens`, `cached_tokens` from vLLM / OSAC MaaS CloudEvents
- Track hardware compute: GPU SKU, VRAM (GB-seconds), queue wait
- Token data available for cost calculation and visible in dashboard
- MaaS rate structure defined: tokens in/out, inference tokens, requests — priced per million units

**Current State:**
- OpenShift AI CloudEvents capability is upcoming (v5)
- Spike in progress investigating metrics for MaaS chargeback
- Hardware compute metrics covered
- Token details partially available via vLLM usage API
- Custom IPP plugins may be needed

**Open Questions:**
- Who collects RHOAI MaaS metrics — Cost or OSAC?
- What fields will OSAC MaaS CloudEvents contain?
- Transport for MaaS events: HTTP, Kafka, other?
- Who defines the MaaS rate structure: Cost team, OSAC, or agreed jointly?

**Related Ticket:** COST-7164

**Scope:**
- IN: Receive and process MaaS CloudEvents; token and request metering; MaaS rate definition; consumption-based cost calculation
- OUT: Per-workload GPU-second metric scraping; real-time inference monitoring

---

### REQ-3b: Service Catalog Sync from OSAC
**Priority:** MEDIUM
[COST-7800](https://redhat.atlassian.net/browse/COST-7800)
Rank: 13

Read OSAC service catalog for pricing. Manual setup acceptable for PoC; API sync deferred to a later phase.

**Acceptance Criteria:**
- RHCM can read OSAC catalog items (instance types, storage tiers)
- Price lists in RHCM correspond to OSAC catalog offerings
- Cost calculations use catalog-based rates (capacity charging)

**Current State:**
- RHCM does not have a service catalog feature today
- Existing per-cluster/per-VM cost models may partially work
- Catalog lives in OSAC; RHCM is a downstream consumer
- OSAC core team owns catalog

**Scope:**
- IN: Read OSAC catalog and apply capacity-based rates
- OUT: Building a service catalog UI in RHCM; bilateral catalog sync

---

### REQ-5: Chargeback Reporting
**Priority:** MEDIUM
[COST-7801](https://redhat.atlassian.net/browse/COST-7801)
Rank: 14

Export chargeback reports covering both capacity-based (provisioned compute hours) and consumption-based (GPU hours, token consumption) dimensions per tenant/project.

**Acceptance Criteria:**
- Reports map provisioned compute hours and GPU hours to token consumption per tenant/project
- Exportable in standard formats (CSV, JSON)
- Accurate and consistent with the cost tracking dashboard

**Current State:**
- Core RHCM chargeback capability exists in-product

**Scope:**
- IN: Chargeback reports for all PoC workloads (capacity and MaaS)
- OUT: Integration with external billing systems

---

### REQ-7: Audit Trail
**Priority:** MEDIUM
[COST-7802](https://redhat.atlassian.net/browse/COST-7802)
Rank: 15

Zero-leakage reconciliation, immutable audit logs, and dispute resolution support.

**Acceptance Criteria:**
- Billing ledgers match consumption logs with zero financial variance
- Tamper-resistant audit trail for all admin changes
- Human-readable error logging for billing dispute resolution

**Current State:**
- In-product; no gap identified
- On-prem audit work tracked under POC-ENV (COST-7541, COST-7328)

---

### REQ-11: Cost Tiers
**Priority:** LOW
[COST-7808](https://redhat.atlassian.net/browse/COST-7808)
Rank: 16

Tiered pricing support for both capacity-based and MaaS consumption-based rates. Example: first 20 GiB free, next 100 GiB at $0.08/GiB-month, next 1000 GiB at $0.07/GiB-month.

**Acceptance Criteria:**
- Rate engine supports multiple pricing tiers per resource type
- Tiers apply to both capacity-based rates (cluster/VM) and MaaS consumption rates (tokens, requests)
- Tier configuration is manageable without code changes

**Current State:**
- No tiered pricing support in RHCM today
- Existing flat-rate cost models would need extension
- Depends on Price List Lifecycle work (COST-575, COST-7327, COST-7328)

**Open Questions:**
- Where do cost tiers live: OSAC, Cost, or both synced?

**Related Ticket:** COST-6951

**Scope:**
- IN: Tiered pricing for capacity-based and MaaS rates
- OUT: Building a tier management UI; bilateral tier sync with OSAC (manual setup acceptable for PoC)

---

### REQ-12: Daily OpenShift Virtualization Costs
**Priority:** LOW
[COST-7808](https://redhat.atlassian.net/browse/COST-7808)
Rank: 17

Daily cost calculation for OpenShift Virtualization workloads provisioned through OSAC.

**Acceptance Criteria:**
- TBD — pending confirmation from Product Management

**Current State:**
- Requirement pending confirmation; scope and acceptance criteria not yet defined

**Scope:**
- TBD — Confirm with Product Management

---

### REQ-8: Bare Metal Costing (OSAC Bare Metal Service)
**Priority:** HIGH
[COST-7801](https://redhat.atlassian.net/browse/COST-7801)
Rank: 18

Support bare metal nodes provisioned through OSAC (BMaaS), including potential standalone nodes outside OpenShift clusters. Consume bare metal service CloudEvents for capacity-based costing.

**Acceptance Criteria:**
- RHCM receives and processes bare metal service CloudEvents from OSAC
- Costs calculated for bare metal nodes based on provisioned capacity
- Standalone bare metal nodes (not attached to OpenShift) supported if required by AI Grid

**Current State:**
- OSAC bare metal service is actively being built (confirmed Jun 24)
- RHCM already supports OpenShift bare metal costing
- Open question: do we need to support nodes outside OpenShift clusters?
- Standalone bare metal requirements under investigation

**Open Questions:**
- OSAC needs to define the BMaaS CloudEvents schema first
- Do we need to support nodes outside of an OCP cluster?

**Scope:**
- IN: Bare metal capacity-based costing via OSAC CloudEvents
- OUT: Standalone bare metal support TBD pending requirements review

---

### PoC Simplifications

- No Prometheus metric scraping from workload clusters
- No rework of the hourly CSV ingestion pipeline
- No installing collectors on individual workload clusters; ingress component may not be needed
- API-only is acceptable for PoC (UI is nice-to-have)
- Manual catalog/tier configuration acceptable for PoC; API-driven sync is post-PoC

---

## Out of Scope

| Item | Reason |
|------|--------|
| Usage-based metering (non-event) | Capacity-based charging adopted for PoC. Heartbeat events from OSAC replace the hourly CSV pipeline. |
| Quota enforcement / budget cutoff | RHCM provides quota status data only (REQ-9); enforcement is OSAC's responsibility via OPA. |
| Token/budget limit definitions | Limits are defined and owned by OSAC at the tenant/project level; RHCM notifies when thresholds are met. |
| Service catalog ownership | The catalog (instance types, storage tiers) lives in OSAC. RHCM reads it for pricing; no bilateral sync needed for PoC. |
| Cost Management Operator | Helm chart used for on-prem deployment; full OLM-based operator is post-PoC. |
| Full UI | API-only is acceptable for PoC. OSAC provides user-facing consoles; RHCM may provide admin-level UI only. |
| Full SaaS deployment | On-premise only. No SaaS features or resource optimization. |

---

## Future Work (Post-PoC)

### REQ-6: Platform Security & Access Control
**Priority:** STANDARD

MFA, granular RBAC for billing admins, and short-lived auth tokens.

**Acceptance Criteria:**
- MFA enforced on administrative consoles
- RBAC governs access to rate structures and limit overrides
- All API endpoints use modern crypto transport and short-lived tokens

**Current State:**
- In-product; no gap identified
- On-prem RBAC and security review work tracked under POC-ENV (COST-7570, COST-7670, COST-7544, COST-7547)

---

## Consolidated Cost Team Action Items

| # | Area | Priority | Action Item |
|---|------|----------|-------------|
| 1 | OSAC Integration | Must Have | Learn CloudEvents standard |
| 2 | OSAC Integration | Must Have | Validate CaaS/VMaaS CloudEvents for capacity-based metering |
| 3 | OSAC Integration | Must Have | Agree on CloudEvents transport with OSAC (Kafka, HTTP, NATS) |
| 4 | MaaS Costing | Must Have | Define MaaS rate structure (tokens in/out, inference tokens, requests — priced per million units) |
| 5 | MaaS Costing | Must Have | Accept RHOAI/OSAC MaaS CloudEvents and compute cost within 60 sec |
| 6 | Rate Limiting | Must Have | Calculate quota/budget consumption |
| 7 | Rate Limiting | Must Have | Expose "am I in quota or exceeded?" API (REQ-9) |
| 8 | Budgets/Quotas | Must Have | Investigate and implement quota/budget concept in Cost Management |
| 9 | Budgets/Quotas | Must Have | Verify if AI Grid requirements include grace periods |
| 10 | Notifications | Must Have | Implement user-configurable threshold rules for alerts |
| 11 | Notifications | Must Have | Agree on alert format and transport with OSAC |
| 12 | Cost Tiers | Must Have | Implement cost tiers for capacity-based and MaaS rates (REQ-11) |
| 13 | Custom Metrics | Must Have | Investigate consuming arbitrary CloudEvent dimensions as rate inputs (REQ-13) |
| 14 | Bare Metal | Should Have | Confirm existing OCP bare metal coverage |
| 15 | Bare Metal | Should Have | Investigate node-outside-OCP gap |
| 16 | Tenancy | Should Have | Implement OSAC project entities in Cost Management |
| 17 | Tenancy | Should Have | Determine RBAC needs for cross-project visibility |

---

## Key Architectural Decisions

### Resolved

| Decision | Resolution | Reference |
|----------|------------|-----------|
| **CloudEvents transport** | Watch stream (gRPC NDJSON) + periodic reconciler against OSAC List endpoints for PoC and likely v1. Kafka deferred — only warranted if multiple independent consumers need the same event stream. | [ADR-002](../decisions/002-arguments-against-kafka.md) |
| **Quota/Budget source of truth** | OSAC owns and defines limits (source of truth). Cost caches limits via the OSAC List API (read-only). Cost owns metering, consumption aggregation, and threshold evaluation. | [alerting-osac-integration.md](../poc_architecture/boundary_monitoring/alerting-osac-integration.md) |
| **Tenant/Project hierarchy** | OSAC `Tenant → Project` model is tracked in Cost (`inventory_project`). All metering entries carry `tenant_id`; costs drill down to project level. No pre-provisioning required — first event auto-registers the tenant. | REQ-3a; [architecture.md](../poc_architecture/architecture.md) |
| **Metering sweep interval** | 60-second sweep satisfies the processing SLA and matches the planned OSAC metering collector cadence. On DELETE, a final metering entry closes the gap to the deletion timestamp. | [ADR-001](../decisions/001-metering-sweep-interval.md) |

### Leaning / Pending Confirmation

| Decision | Direction | Open Items |
|----------|-----------|------------|
| **Quota alert transport (REQ-10)** | Push + pull together (Option 1): Cost pushes async threshold events to an OSAC webhook; OSAC pulls quota status synchronously at create-gate time. Pull-only (Option 2) is an acceptable PoC interim. | Alert webhook path and auth on OSAC side not yet agreed; OSAC architect to confirm with working group. |

### Unresolved

| Decision | Options | Impact |
|----------|---------|--------|
| **Cost tier ownership** | Cost only / OSAC only / Both synced | Shapes rate engine design and sync complexity (REQ-11) |
| **Provider UI surface** | Cost Management UI / OSAC fetches from Cost API | Determines whether Cost needs project-scoped RBAC and project entity management |
| **MaaS metric collection** | Cost collects directly from RHOAI / OSAC collects and forwards to Cost | Defines integration boundary and data pipeline ownership (REQ-2a, REQ-4); currently blocked on OSAC MaaS CloudEvent schema |

---

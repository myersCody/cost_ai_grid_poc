# AI Grid PoC — Cost Management Requirements Summary

---

## Overview

This document summarizes the requirements, new work, and scope boundaries for the Cost Management AI Grid Proof of Concept. The PoC integrates Red Hat Cost Management (RHCM) with OSAC (Open Sovereign AI Console) to demonstrate capacity-based charging for provisioned resources on a sovereign cloud platform.

MaaS-related requirements (token metering, OpenShift AI cloud events) have been moved to a separate post-PoC backlog. See [Future Work (Post-PoC)](#future-work-post-poc).

---

## Requirements

### POC-ENV — On-Premise Deployment
**Priority:** CRITICAL

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

### POC-ARCH — Capacity-Based Charging Model
**Priority:** CRITICAL

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
- Different pipeline than existing CSV ingestion;
- cloud events = fundamentally different architecture from batch processing
- Cost Team must decide build standalone PoC component

**Scope:**
- IN: Capacity-based charging for clusters and VMs
- OUT: Usage-based metering, GPU-second granularity, token-level metering (separate MaaS workstream)

---

### REQ-1 — OSAC Integration via Region Management Cluster
**Priority:** CRITICAL

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

**Scope:**
- IN: Connection to Region Management Cluster; reading inventory and state
- OUT: Quota enforcement; installing collectors on workload clusters (not needed for the capacity model)

---

### REQ-1a — OSAC Cluster Lifecycle via Cluster Orders
**Priority:** HIGH

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

### REQ-1b — OSAC Heartbeat Event Ingestion
**Priority:** CRITICAL

Receive heartbeat events from OSAC via HTTP or Kafka (transport TBD per Jun 24 meeting) at configurable intervals (10s–30s). Events contain tenant ID, project ID, resource ID, and hardware config. The first event auto-registers the tenant.

**Acceptance Criteria:**
- RHCM can receive heartbeat events via HTTP
- Events parsed for: tenant ID, project ID, resource ID, hardware config
- First event auto-creates tenant/project if not already registered
- Events processed and cost calculated within target SLA

**Current State:**
- Event contract between OSAC and RHCM not yet defined

**Scope:**
- IN: Receive and process heartbeat events for capacity-based charging
- OUT: Usage-based metrics ingestion (not needed for capacity model)

---

### REQ-2 — Near-Real-Time Cost Calculation
**Priority:** CRITICAL

Process OSAC heartbeat events and calculate costs within 60 seconds of receipt. End-to-end SLA is 90 seconds. This is a new HTTP data path, not a rework of the CSV pipeline.

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

### REQ-3 — Granular Cost Tracking
**Priority:** HIGH

Single system of record for cost data with drill-down by tenant, project, model, and user.

**Acceptance Criteria:**
- Cost data filterable by: tenant, model/SKU, application, user
- Dashboard shows near-real-time token consumption, compute hours, estimated costs
- Reporting supports export in CSV and JSON
- Financial data decoupled from infrastructure state

**Current State:**
- Core RHCM cost tracking and reporting capability exists in-product
- Dashboard and export functionality are in-product

**Scope:**
- IN: Granular cost breakdowns at listed dimensions
- OUT: Account hierarchy management

> **TODO (Product Management):** The acceptance criterion "Dashboard shows near-real-time token consumption, compute hours, estimated costs" references token consumption, which is out of scope for the capacity-based PoC (token metering belongs to the MaaS workstream). Please confirm whether this bullet should be removed or reworded to reflect capacity-based cost tracking only (e.g., compute hours and estimated costs).

---

### REQ-3a — OSAC Tenant/Project Attribution
**Priority:** HIGH

Map OSAC's Tenant → Project hierarchy to RHCM's organizational model. All costs attributed to the correct tenant and project.

**Acceptance Criteria:**
- Cost data attributed to the correct OSAC tenant
- Cost data can be drilled down to project level within a tenant
- Tenant/project hierarchy read from OSAC Region Management Cluster
- Multi-tenant attribution works even when all workloads run on shared infrastructure

**Current State:**
- RHCM currently attributes data per organization/cluster
- OSAC tenant/project hierarchy is documented
- Mapping OSAC tenants to RHCM organizations needs design

**Scope:**
- IN: Tenant and project-level cost attribution
- OUT: Full tenant lifecycle management (onboarding, offboarding)

---

### REQ-3b — Service Catalog Sync from OSAC
**Priority:** MEDIUM

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

### REQ-5 — Chargeback Reporting
**Priority:** MEDIUM

Export chargeback reports mapping GPU hours to token consumption per business unit.

**Acceptance Criteria:**
- Reports map GPU hours to token consumption per business unit
- Exportable in standard formats
- Accurate and consistent with the cost tracking dashboard

**Current State:**
- Core RHCM chargeback capability exists in-product

**Scope:**
- IN: Chargeback reports for PoC workloads
- OUT: Integration with external billing systems

> **TODO (Product Management):** The description and acceptance criteria reference "GPU hours to token consumption per business unit." Token metering (REQ-4) is out of scope for the capacity-based PoC. Please confirm whether REQ-5 should be re-scoped to capacity-based chargeback only (e.g., provisioned compute hours per tenant/project), or deferred entirely to the MaaS workstream alongside REQ-4.

---

### REQ-8 — Bare Metal Costing (OSAC Bare Metal Service)
**Priority:** HIGH

Support bare metal nodes provisioned through OSAC, including potential standalone nodes outside OpenShift clusters (standalone rail/bare metal). Consume bare metal service cloud events for capacity-based costing.

**Acceptance Criteria:**
- RHCM receives and processes bare metal service cloud events from OSAC
- Costs calculated for bare metal nodes based on provisioned capacity
- Standalone bare metal nodes (not attached to OpenShift) supported if required by AI Grid

**Current State:**
- OSAC bare metal service is actively being built (confirmed Jun 24)
- RHCM already supports OpenShift bare metal costing
- Open question: do we need to support nodes outside OpenShift clusters?
- Pau to investigate standalone bare metal requirements

**Scope:**
- IN: Bare metal capacity-based costing via OSAC cloud events
- OUT: Standalone bare metal support TBD pending requirements review

---

### REQ-9 — Quota/Budget Status API
**Priority:** CRITICAL

Expose a fast API for OSAC to check quota and budget status before allowing resource creation (e.g., "Is this tenant within quota?"). Enforcement remains with OSAC; RHCM provides the data.

**Acceptance Criteria:**
- API responds with sub-second latency
- OSAC can query: is tenant within quota? What % of budget consumed?
- Supports threshold checks (50%, 70%, 90%, 100%)
- Source of truth for quota data agreed between OSAC and RHCM

**Current State:**
- No quota/budget API exists in RHCM today
- Open question: is RHCM or OSAC the source of truth for quota/budget data?
- Enforcement is OSAC responsibility; RHCM provides the data

**Scope:**
- IN: Read-only quota/budget status API for OSAC consumption
- OUT: Quota enforcement (OSAC's responsibility); budget/limit definition UI

---

### REQ-10 — Threshold Notification Back Channel to OSAC
**Priority:** HIGH

Send threshold notifications from RHCM to OSAC when cost/quota consumption hits defined levels (50%, 70%, 90%, 100%). Transport and format TBD (webhook, Kafka, or cloud events).

**Acceptance Criteria:**
- RHCM sends notifications to OSAC at configurable thresholds
- Notifications include: tenant ID, resource/project context, threshold level, current consumption
- Transport mechanism agreed between OSAC and RHCM
- Notifications delivered reliably (no silent drops)

**Current State:**
- No back channel from RHCM to OSAC exists today
- Jun 24 meeting: transport options discussed (webhook, Kafka, cloud events)
- OSAC archtiect to consult with OSAC working group architect about OSAC alerting capabilities
- Grace periods for budget overages may be required

**Scope:**
- IN: Threshold notification mechanism from RHCM to OSAC
- OUT: Alert UI in RHCM; grace period enforcement (OSAC's responsibility)

---

## New PoC Work (Net-New Engineering)

The following items require new epics or stories and have no existing implementation in RHCM:

| # | Req ID | Title | Summary |
|---|--------|-------|---------|
| 1 | REQ-1 + REQ-1a | OSAC Integration via Region Management Cluster | Connect RHCM to OSAC Region Management Cluster APIs; read inventory, resource state, cluster orders, and tenant/project hierarchy. |
| 2 | REQ-1b | Heartbeat Event Ingestion (HTTP endpoint) | Build HTTP endpoint to receive OSAC heartbeat events. Parse tenant ID, project ID, resource ID, and hardware config. Auto-register tenants on first event. |
| 3 | POC-ARCH | Capacity-Based Charging Model | Standalone PoC component built outside Koku. Calculate cost from provisioned capacity and duration. Target: 90s end-to-end SLA. |
| 4 | REQ-3a | OSAC Tenant/Project Mapping | Map OSAC's Projects-within-Tenants hierarchy to RHCM's organizational model. All costs attributed to the correct tenant and project. |
| 5 | REQ-3b | Service Catalog Sync from OSAC | Read OSAC catalog items for pricing. Manual setup for PoC; API sync deferred. |
| 6 | REQ-8 | Bare Metal Costing (OSAC Bare Metal Service) | Consume bare metal service cloud events from OSAC. Investigate support for standalone bare metal nodes outside OpenShift clusters. |
| 7 | REQ-9 | Quota/Budget Status API | Expose a fast API for OSAC to check tenant quota/budget status before resource creation. Source of truth to be agreed. |
| 8 | REQ-10 | Notification/Alert Back Channel to OSAC | Send threshold notifications (50%, 70%, 90%, 100%) from RHCM to OSAC. Transport (webhook, Kafka, cloud events) and format TBD. |

### PoC Simplifications

- No Prometheus metric scraping from workload clusters
- No rework of the hourly CSV ingestion pipeline
- No GPU-second granularity or token-level metering (separate MaaS workstream — see Future Work)
- No installing collectors on individual workload clusters; ingress component may not be needed
- API-only is acceptable for PoC (UI is nice-to-have)

---

## Out of Scope

| Item | Reason |
|------|--------|
| Usage-based metering | Capacity-based charging adopted for PoC. Heartbeat events from OSAC replace the hourly CSV pipeline. |
| Token metering & OpenShift AI cloud events | Moved to post-PoC MaaS workstream (REQ-2a, REQ-4). Not required for the capacity-based PoC. |
| Quota enforcement / budget cutoff | RHCM provides quota status data only (REQ-9); enforcement is OSAC's responsibility. |
| Token/budget limit definitions | Limits are defined and owned by OSAC at the tenant/project level; RHCM notifies when thresholds are met. |
| Service catalog ownership | The catalog (instance types, storage tiers) lives in OSAC. RHCM reads it for pricing; no bilateral sync needed for PoC. |
| Cost Management Operator | Helm chart used for on-prem deployment; full OLM-based operator is post-PoC. |
| Full UI | API-only is acceptable for PoC. OSAC provides user-facing consoles; RHCM may provide admin-level UI only. |
| Full SaaS deployment | On-premise only. No SaaS features or resource optimization. |

---

## Future Work (Post-PoC)

The following items were moved out of PoC scope by Product Management.

---

### REQ-2a — Cloud Events from OpenShift AI (MaaS workstream)
**Priority:** HIGH

Consume Cloud Events from OpenShift AI 5 for token metering. Separate from the capacity-based PoC (MaaS workstream).

**Acceptance Criteria:**
- RHCM can receive and process Cloud Events from OpenShift AI
- Events ingested within 30 seconds of emission
- JSON/CloudEvents format parsed and stored
- Validated with at least one MaaS workload type

**Current State:**
- OpenShift AI Cloud Events capability is upcoming (v5)
- This is a separate workstream from the capacity-based PoC
- Spike in progress investigating metrics for MaaS chargeback

**Scope:**
- Moved from PoC Requirements
- NOT needed for the capacity-based PoC
- Future MaaS workstream

---

### REQ-4 — Token Metering (MaaS workstream)
**Priority:** HIGH

Track token dimensions (input, output, cached, reasoning) and GPU compute metrics. Separate from the capacity-based PoC (MaaS workstream).

**Acceptance Criteria:**
- Ingest prompt_tokens, completion_tokens, cached_tokens from vLLM
- Track hardware compute: GPU SKU, VRAM (GB-seconds), queue wait
- Token data available for cost calculation and in dashboard

**Current State:**
- Hardware compute metrics covered
- Token details partially available via vLLM usage API
- Custom IPP plugins may be needed
- This is Yaron's workstream, separate from capacity PoC

**Scope:**
- Moved from PoC Requirements
- NOT required for the capacity-based PoC
- Future MaaS workstream

---

### REQ-6 — Platform Security & Access Control
**Priority:** STANDARD

MFA, granular RBAC for billing admins, and short-lived auth tokens.

**Acceptance Criteria:**
- MFA enforced on administrative consoles
- RBAC governs access to rate structures and limit overrides
- All API endpoints use modern crypto transport and short-lived tokens

**Current State:**
- In-product; no gap identified
- On-prem RBAC and security review work tracked under POC-ENV (COST-7570, COST-7670, COST-7544, COST-7547)

**Scope:**
- IN: All listed security controls
- OUT: N/A

---

### REQ-7 — Reconciliation, Auditing & Dispute Tracing
**Priority:** STANDARD

Zero-leakage reconciliation, immutable audit logs, and dispute resolution support.

**Acceptance Criteria:**
- Billing ledgers match consumption logs with zero financial variance
- Tamper-resistant audit trail for all admin changes
- Human-readable error logging for billing dispute resolution

**Current State:**
- In-product; no gap identified
- On-prem audit work tracked under POC-ENV (COST-7541, COST-7328)

**Scope:**
- IN: All listed audit/reconciliation controls
- OUT: N/A

---

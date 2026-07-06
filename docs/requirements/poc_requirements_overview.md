# AI Grid PoC — Cost Management Requirements

**Version:** 1.3
**Date:** July 2, 2026
**Status:** Hardening (Still in Flux)

This document is the consolidated requirements reference for the Cost Management AI Grid Proof of Concept. It merges the initial requirements brief with the detailed requirements summary. MaaS (token metering and OpenShift AI cloud events), Cost Tiers, and Custom Metrics are included as in-scope PoC requirements.

---

## Project Context

- Sovereign cloud built on OCP, OCP Virtualization, OpenShift AI, ACM, Ansible
- **OSAC** (Open Sovereign AI Cloud) is the orchestrator — provisions clusters (HCP), VMs (OpenShift Virtualization), models (MaaS), and bare metal
- OSAC emits **CloudEvents** for resource lifecycle and metrics; transport for VM/cluster is gRPC Watch stream + 60s reconciler (Kafka deferred — see [ADR-002](../decisions/002-arguments-against-kafka.md)); MaaS transport pending verification (Martin/Noy)
- Billing model: **capacity-based** for clusters/VMs; **consumption-based** (token/request) for MaaS
- No Cost Management Metrics Operator (CMMO) — OSAC is the sole metric source
- Data freshness SLA: OSAC emits within 30 sec of event; **Cost must process within 60 sec of receipt**
- Tenancy model: `Tenant → Project → Resource (cluster/VM/bare metal/model)`

---

## Requirements

### POC-ENV — On-Premise Deployment
**Priority:** CRITICAL &nbsp;·&nbsp; **Rank:** 1

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
**Priority:** CRITICAL &nbsp;·&nbsp; [COST-7792](https://redhat.atlassian.net/browse/COST-7792) &nbsp;·&nbsp; **Rank:** 2

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
**Priority:** CRITICAL &nbsp;·&nbsp; [COST-7793](https://redhat.atlassian.net/browse/COST-7793) &nbsp;·&nbsp; **Rank:** 3

Connect RHCM to the OSAC Region Management Cluster (gRPC/REST APIs) to read inventory, resource state, and tenant/project hierarchy. All data flows through the management layer, not individual workload clusters.

**Acceptance Criteria:**
- RHCM connects to OSAC Region Management Cluster APIs (gRPC/REST)
- Can read inventory and resource state from OSAC
- Account/tenant lifecycle synced between OSAC and RHCM. Even if a tenant is deleted, or not sending data from OSAC, RHCM will not delete the metering and cost data for that tenant. The only way Cost Management deletes data is when the data becomes older than the retention period (which must be configurable).
- Workload-level info includes OSAC tenant ID, project ID, resource ID
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
**Priority:** CRITICAL &nbsp;·&nbsp; [COST-7795](https://redhat.atlassian.net/browse/COST-7795) &nbsp;·&nbsp; **Rank:** 4

Receive heartbeat events from OSAC via HTTP or Kafka (transport TBD per Jun 24 meeting) at configurable intervals (10s–30s). Events contain OSAC tenant ID, project ID, resource ID, and hardware config (or service catalog item?). The first event auto-registers the tenant.

> **What "heartbeat events" means:** CloudEvents emitted periodically by OSAC metering collector (`osac-metering-discover-poc`) — same schema as state transition events but fired on a timer and pre-populated with `duration_seconds` and metering quantities. The PoC satisfies this today via a local 60-second sweep (is this enough in the worst case?); the collector is not required for the demo. See [ADR-003](../decisions/003-heartbeat-emitter-vs-sweep.md) for the full explanation.

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
- Interval: every 10s, every 30s proposed on the Jun 23rd meeting. It should be configurable.

**Scope:**
- IN: Receive and process periodic lifecycle CloudEvents for capacity-based charging
- OUT: Usage-based metrics ingestion (not needed for capacity model)

---

### REQ-2: Near-Real-Time Cost Calculation
**Priority:** CRITICAL &nbsp;·&nbsp; [COST-7796](https://redhat.atlassian.net/browse/COST-7796) &nbsp;·&nbsp; **Rank:** 5

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
**Priority:** HIGH &nbsp;·&nbsp; [COST-7794](https://redhat.atlassian.net/browse/COST-7794) &nbsp;·&nbsp; **Rank:** 6

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
**Priority:** HIGH &nbsp;·&nbsp; [COST-7799](https://redhat.atlassian.net/browse/COST-7799) &nbsp;·&nbsp; **Rank:** 7

Map OSAC's `Tenant → Project` hierarchy to RHCM's organizational model. All costs attributed to the correct tenant and project.

**Acceptance Criteria:**
- Cost data attributed to the correct OSAC tenant
- Cost data can be drilled down to project level within a tenant
- Tenant/project hierarchy read from OSAC Region Management Cluster
- Multi-tenant attribution works even when all workloads run on shared infrastructure
- Quotas/budgets tracked per OSAC project, per tenant, and projects roll up to tenant, i. e. sum of all project consumptions cannot exceed tenant quota/budget. This also allows for overcomitting quotas/budgets at the project level.


**Current State:**
- RHCM currently attributes data per organization/cluster
- OSAC tenant/project hierarchy is documented
- Mapping OSAC tenants to RHCM organizations needs design

**Open Questions:**
- Will providers view cost in the Cost Management UI or in OSAC?
- Is RBAC needed for providers viewing cross-project cost data?

> **Decision (Jul 2, 2026):** RBAC scope for PoC is **tenant + project level only**.
> Fine-grained InsightsRBAC is deferred post-PoC provided that Cost Management implements the concept of project within a tenant, mimicking OSAC (e.g. via Keycloak). Project + tenant attribution is
> already tracked on the event-driven side. Owners: Pau, Moti, Cody. (~00:17:18).

**Scope:**
- IN: Tenant and project-level cost attribution. Implementing projects in Cost Management PoC.
- OUT: Full tenant lifecycle management (onboarding, offboarding); fine-grained InsightsRBAC (post-PoC)

---

### REQ-3: Granular Cost Tracking
**Priority:** HIGH &nbsp;·&nbsp; [COST-7798](https://redhat.atlassian.net/browse/COST-7798) &nbsp;·&nbsp; **Rank:** 8

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
- OUT: Account hierarchy management. FOCUS export (but desirable if we have to choose something).

---

### REQ-9: Quota/Budget Status API
**Priority:** HIGH &nbsp;·&nbsp; [COST-7801](https://redhat.atlassian.net/browse/COST-7801) &nbsp;·&nbsp; **Rank:** 9

Provide a workflow to allow OSAC to check quota and budget status before allowing resource creation.

(e.g., "Is this tenant within quota?"). Enforcement remains with OSAC; RHCM provides the data.

**Definitions:**
- **Quota** = dimensional limit (CPU core-hours, GiB RAM-hour, tokens, etc.). Providers set quotas for tenants based on accumulated metered consumption over a period.
- **Budget** = monetary quota. Cost applied to metering = budget consumed.
- **Cost** = metered consumption x rates.

**Acceptance Criteria:**
- API responds with sub-second latency
- OSAC can query: is tenant within quota? What % of budget consumed?
- Supports threshold checks (50%, 70%, 90%, 100%)
- Source of truth for quota data agreed between OSAC and RHCM
- Grace period requirements verified
- RHCM implements quota definition, irregardless of OSAC implementing them or not.
- Quotas/budgets are scoped to tenants and tenant projects and rolled up from projects to tenants.

**Current State:**
- No quota/budget API exists in RHCM today
- Enforcement is OSAC responsibility; RHCM provides the data

**Open Questions:**
- Do AI Grid requirements include grace periods?
- Open question: is RHCM or OSAC the source of truth for quota/budget data? It does not matter: RHCM must implement quotas anyway (for non-OSAC customers) and typically who the source of truth is is resolved at implementation time via Professional Services (the source of truth could be a third system, e.g. ServiceNow, that is synchornized to both OSAC and RHCM).

**Scope:**
- IN: Read-only quota/budget status API for OSAC consumption
- OUT: Quota enforcement (OSAC's responsibility); budget/limit definition UI

---

### REQ-10: Threshold Notification Back Channel to OSAC
**Priority:** HIGH → **Parked** &nbsp;·&nbsp; [COST-7807](https://redhat.atlassian.net/browse/COST-7807) &nbsp;·&nbsp; **Rank:** 10

Send threshold notifications from RHCM to OSAC when cost/quota consumption hits defined levels (50%, 70%, 90%, 100%). OSAC consumes these notifications to trigger OPA-enforced rate limiting. Transport and format TBD.

> **Decision (Jul 2, 2026):** Parked for now. For MaaS quota enforcement,
> OSAC already exposes a check-balance API — no separate alert mechanism is needed
> for the July 31 deadline. The pull model (REQ-9 quota status API) is sufficient.
> Owners: Moti, Ronnie. (~00:51–00:53)

**Acceptance Criteria:**
- RHCM sends notifications to OSAC at configurable (by OSAC administrators) thresholds, therefore synchornization of those thresholds is also required.
- Notifications include: tenant ID, resource/project context, threshold level, current consumption
- Transport mechanism agreed between OSAC and RHCM
- Notifications delivered reliably (no silent drops)

**Current State:**
- Pull model implemented: `GET /api/v1/quotas/{tenant_id}` returns threshold flags
- Push (webhook) model deferred — transport not yet agreed with OSAC
- Jun 24 meeting: transport options discussed (webhook, Kafka, cloud events)
- Jul 2 meeting: parked; pull model accepted as sufficient for PoC

**Open Questions:**
- Does OSAC have an existing alerting mechanism? (deferred)
- Transport: Kafka, HTTP, NATS, CloudEvents? (deferred)

**Scope:**
- IN: Threshold notification mechanism from RHCM to OSAC (**parked, no timeline set**)
- OUT (PoC): Push/webhook mechanism; alert UI in RHCM; grace period enforcement (OSAC's responsibility)

---

### REQ-13: Custom Metrics / Custom Rates
**Priority:** HIGH &nbsp;·&nbsp; [COST-7808](https://redhat.atlassian.net/browse/COST-7810) &nbsp;·&nbsp; **Rank:** 11

Ability to create a custom rate from an arbitrary metric dimension emitted by OSAC CloudEvents. Allows new dimensions to be metered without hardcoded support in RHCM.

**Acceptance Criteria:**
- RHCM can consume arbitrary CloudEvent dimensions as rate inputs
- New dimensions can be configured with an ID, classification, and rate name
- Custom dimension data is stored and available for cost calculation and reporting

**Current State:**
- No custom metric rate support exists in RHCM today
- Requires investigation of CloudEvent schema extensibility

**Open Questions:**
- Who defines new dimensions to collect: OSAC or Cost team? As of today, it's OSAC, so for now, RHCM may define new rates based on existing metrics (i. e. "creative math" defined in RHCM). To collect new meters/metrics (per OSAC vocabulary in (OSAC PRD-78)[https://github.com/osac-project/enhancement-proposals/pull/78]), RHCM would need a backchannel to communicate with OSAC.
- ID, classification, and rate naming scheme to be agreed
- How to express those custom rates, i.e. RHCM would need to say "I want meter/metric X and meter/metric Y and multiply them by 24 and divide them by 3600 and sum X + Y". Should we use a programming language (e.g. PyScript, like CloudKitty), JSON (e.g. JDM, like GoRules) or what?

**Related Ticket:** COST-3549

**Scope:**
- IN: Configurable ingestion of arbitrary ¿existing? ¿custom? CloudEvent dimensions as billable rate inputs
- OUT: UI for custom metric management (API/config acceptable for PoC)

---

### REQ-2a: Cloud Events from OpenShift AI (MaaS) & Token Metering
**Priority:** HIGH &nbsp;·&nbsp; [COST-7797](https://redhat.atlassian.net/browse/COST-7797) &nbsp;·&nbsp; **Rank:** 12

Consume CloudEvents from OpenShift AI 3.5 for token metering. OSAC emits CloudEvents with token counts (input, output, inference) and request counts. Track token dimensions (input, output, cached, reasoning) and GPU compute metrics for MaaS workloads provisioned via OSAC. Define MaaS rate structure priced per million units. Cost must compute MaaS cost within 60 seconds of receiving data.

**Acceptance Criteria:**
- RHCM can receive and process CloudEvents from OpenShift AI / OSAC for MaaS workloads
- Events ingested within 30 seconds of emission
- JSON/CloudEvents format parsed and stored
- MaaS cost computed within 60 seconds of event receipt and quotas/budget updates (REQ-9)
- Validated with at least one MaaS workload type
- Ingest `prompt_tokens`, `completion_tokens`, `cached_tokens` from vLLM / OSAC MaaS CloudEvents
- Track hardware compute: GPU SKU, VRAM (GB-seconds), queue wait
- Token data available for cost calculation and visible in dashboard
- MaaS rate structure defined: tokens in/out, inference tokens, requests — priced per million units

**Current State:**
- OpenShift AI CloudEvents capability is upcoming (v3.5)
- Spike in progress investigating metrics for MaaS chargeback
- Hardware compute metrics covered
- Token details partially available via vLLM usage API
- Custom IPP plugins may be needed

**Open Questions:**
- Who collects RHOAI MaaS metrics — Cost or OSAC?
- What fields will OSAC MaaS CloudEvents contain?
- **Do events include `tenant_id` and `project_id` attribution?** — Martin verifying via Noy's emulator (action from Jul 2 meeting). If missing, OSAC may need to act as middleman. (~00:28:50–00:32:02)
- Transport for MaaS events: HTTP, Kafka, other?
- Who defines the MaaS rate structure: Cost team, OSAC, or agreed jointly?

**Related Ticket:** COST-7164

**Scope:**
- IN: Receive and process MaaS CloudEvents; token and request metering; MaaS rate definition; consumption-based cost calculation
- OUT: Per-workload GPU-second metric scraping; real-time inference monitoring

---

### REQ-3b: Service Catalog Sync from OSAC
**Priority:** MEDIUM &nbsp;·&nbsp; [COST-7800](https://redhat.atlassian.net/browse/COST-7800) &nbsp;·&nbsp; **Rank:** 13

Read OSAC service catalog for pricing. Manual setup acceptable for PoC; API sync deferred to a later phase.

**Acceptance Criteria:**
- RHCM adds service catalog capabilities
- RHCM can read OSAC catalog items (instance types,cluster sizes, storage tiers, etc) and synchronize
- Price lists in RHCM correspond to OSAC catalog offerings
- Cost calculations use catalog-based rate. Prices for catalog items must be set per catalog item, not based on the rates that constitue a catalog item, i. e. not a direct function of rates x capacity, i. e. a VM with 4 vCPUs and 16 GiB RAM might cost 3x what a VM with 2 vCPUs and 8 GiB RAM.

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
**Priority:** MEDIUM &nbsp;·&nbsp; [COST-7801](https://redhat.atlassian.net/browse/COST-7801) &nbsp;·&nbsp; **Rank:** 14

Export chargeback reports covering both capacity-based (provisioned compute hours) and consumption-based (GPU hours, token consumption) dimensions per tenant/project.

**Acceptance Criteria:**
- Reports map provisioned compute hours and GPU hours to token consumption per tenant/project
- Exportable in standard formats (CSV, JSON)
- Accurate and consistent with the cost tracking dashboard

**Current State:**
- Core RHCM chargeback capability exists in-product

**Scope:**
- IN: Chargeback reports for all PoC workloads (capacity and MaaS)
- OUT: Integration with external billing systems. Export in FOCUS format (though highly desirable).

---

### REQ-7: Audit Trail
**Priority:** MEDIUM &nbsp;·&nbsp; [COST-7802](https://redhat.atlassian.net/browse/COST-7802) &nbsp;·&nbsp; **Rank:** 15

Zero-leakage reconciliation, immutable audit logs, and dispute resolution support.

**Acceptance Criteria:**
- Billing ledgers match consumption logs with zero financial variance
- Tamper-resistant audit trail for all admin changes
- Human-readable error logging for billing dispute resolution

**Current State:**
- In-product; no gap identified. COST-575 and COST-3358 have more extensive requirements for the audit trail.
- On-prem audit work tracked under POC-ENV (COST-7541, COST-7328)

---

### REQ-11: Cost Tiers
**Priority:** LOW &nbsp;·&nbsp; [COST-7808](https://redhat.atlassian.net/browse/COST-7808) &nbsp;·&nbsp; **Rank:** 16

Tiered pricing support for both capacity-based and MaaS consumption-based rates. Example: first 1M tokens free, next 10M tokens at $0.50/M (MaaS); first 20 GiB free, next 100 GiB at $0.08/GiB-month (capacity — post-PoC).

**Acceptance Criteria:**
- Rate engine supports multiple pricing tiers per resource type
- Tiers apply to both capacity-based rates (cluster/VM) and MaaS consumption rates (tokens, requests)
- Tier configuration is manageable without code changes
- Irregardless of OSAC implementing cost tiers, RHCM must implement cost tiers. At implementation time, Mr Customer will decide which one is the source of truth: OSAC, RHCM or a third tool (eg. ServiceNow).

**Current State:**
- Tiered pricing implemented in the PoC rate engine for MaaS token rates (per-event semantics)
- Capacity-based cumulative tier logic (GiB-month, core-hours) is a gap — requires period-accumulating semantics not yet implemented
- See [req11 gap analysis](req11-cost-tiers-gap-analysis.md) for full breakdown and implementation options

**Open Questions:**
- None

**Related Ticket:** COST-6951

**Scope:**
- IN: Tiered pricing for capacity-based and MaaS rates
- OUT: Building a tier management UI; bilateral tier sync with OSAC (manual setup acceptable for PoC)

---

### REQ-12: Daily OpenShift Virtualization Costs
**Priority:** LOW &nbsp;·&nbsp; [COST-7808](https://redhat.atlassian.net/browse/COST-7808) &nbsp;·&nbsp; **Rank:** 17

Daily cost calculation for OpenShift Virtualization workloads (VMs) provisioned through OSAC.

**Acceptance Criteria:**
- Daily, even hourly, cost for every resource (CaaS, VMaaS, etc) is highly desirable. While not an explicity requirement yet, it feels like it will be just a matter of time.

**Current State:**
- This is one of the epics comprised in [PRD13 OpenShift Virtualization fit & finish](https://github.com/project-koku/enhancements/pull/11)

**Scope:**
- Daily cost for OpenShift Virtualization VMs

---

### REQ-8: Bare Metal Costing (OSAC Bare Metal Service)
**Priority:** HIGH → **Parked (post-PoC)** &nbsp;·&nbsp; [COST-7801](https://redhat.atlassian.net/browse/COST-7801) &nbsp;·&nbsp; **Rank:** 18

Support bare metal nodes provisioned through OSAC (BMaaS), including potential standalone nodes outside OpenShift clusters. Consume bare metal service CloudEvents for capacity-based costing.

> **Decision (Jul 2, 2026):** Deferred from July 31 PoC scope. Owner: Moti. (~00:37:25)

**Acceptance Criteria:**
- RHCM receives and processes bare metal service CloudEvents from OSAC
- Costs calculated for bare metal nodes based on provisioned capacity
- Standalone bare metal nodes (not attached to OpenShift, i. e. Windows bare metal, RHEL bare metal, Oracle Exadata, etc) supported if required by AI Grid

**Current State:**
- OSAC bare metal service is actively being built (confirmed Jun 24)
- BareMetalInstance not yet in the Watch stream `oneof`; available via REST List API
- Implementation follows same reconciler pattern as VMs — ready to pick up post-PoC
- Jul 2 meeting: deferred from PoC scope

**Open Questions:**
- OSAC needs to define the BMaaS CloudEvents schema first
- Do we need to support nodes outside of an OCP cluster?

**Scope:**
- IN: Bare metal capacity-based costing via OSAC CloudEvents (**post-PoC**)
- OUT (PoC): Bare metal costing; standalone bare metal support TBD

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
- Using Insights RBAC is NOT mandatory. We may want to move to a simpler model, e.g. per tenant and project, like OSAC does, where authentication is what matters and authorization hardly exists.

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
| 10 | Notifications | ~~Must Have~~ **Parked** | ~~Implement user-configurable threshold rules for alerts~~ Parked for now; pull model (REQ-9) sufficient for Jul 31 |
| 11 | Notifications | ~~Must Have~~ **Parked** | ~~Agree on alert format and transport with OSAC~~ Parked for now |
| 12 | Cost Tiers | Must Have | Implement cost tiers for capacity-based and MaaS rates (REQ-11) |
| 13 | Custom Metrics | Must Have | Investigate consuming arbitrary CloudEvent dimensions as rate inputs (REQ-13) |
| 14 | Bare Metal | ~~Should Have~~ **Parked** | ~~Confirm existing OCP bare metal coverage~~ Deferred from Jul 31 PoC scope |
| 15 | Bare Metal | ~~Should Have~~ **Parked** | ~~Investigate node-outside-OCP gap~~ Deferred from Jul 31 PoC scope |
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
| **RBAC scope for PoC** | Tenant + project level is sufficient. Fine-grained InsightsRBAC deferred post-PoC. Project + tenant attribution already tracked on the event-driven side. | Jul 2, 2026 meeting — Pau, Moti, Cody |
| **Quota alert transport (REQ-10)** | Pull-only (REQ-9 quota status API) is sufficient for the July 31 PoC. REQ-10 push/webhook mechanism parked for now. OSAC already exposes a check-balance API for MaaS quota enforcement. | Jul 2, 2026 meeting — Moti, Ronnie |
| **Naming and architecture conventions** | All design decisions keep eventual Koku/on-prem integration in mind. Where choices exist, prefer Koku conventions (field names, rate structure, report format). Broader convergence direction to be decided in a separate meeting (EMR). | Jul 2, 2026 meeting — Martin, Pau |

### Leaning / Pending Confirmation

| Decision | Direction | Open Items |
|----------|-----------|------------|
| **MaaS event attribution** | OSAC forwards MaaS events to Cost with `tenant_id` + `project_id` fields included. | Martin verifying via Noi's MaaS emulator (action from Jul 2 meeting). If fields missing, OSAC may need to act as middleman. |

### Unresolved

| Decision | Options | Impact |
|----------|---------|--------|
| **Cost tier ownership** | Cost only / OSAC only / Both synced | Shapes rate engine design and sync complexity (REQ-11) |
| **Provider UI surface** | Cost Management UI / OSAC fetches from Cost API | Determines whether Cost needs project-scoped RBAC and project entity management |
| **MaaS metric collection** | Cost collects directly from RHOAI / OSAC collects and forwards to Cost | Defines integration boundary and data pipeline ownership (REQ-2a, REQ-4); currently blocked on OSAC MaaS CloudEvent schema |
| **Three-way convergence** | SaaS cost management, on-prem Koku, OSAC PoC cannot be maintained separately long-term. | EMR meeting expected next week to set direction. Outcome affects RBAC approach (InsightsRBAC vs Keycloak) and long-term architecture. |

---

**Changelog — v1.3 (Jul 2, 2026 meeting):**
- REQ-8 (bare metal): deferred from July 31 PoC scope
- REQ-10 (threshold notifications): parked for now; pull model (REQ-9) sufficient
- REQ-3a RBAC scope: tenant + project level confirmed for PoC; InsightsRBAC deferred
- REQ-2a: Martin to verify MaaS cloud event tenant/project attribution fields
- Design principle confirmed: prefer Koku conventions for all naming and architecture choices
- Architectural decisions table updated accordingly

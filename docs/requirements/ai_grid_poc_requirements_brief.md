# AI Grid PoC — Cost Management Requirements Brief

Summary of the essential context, requirements, and action items for planning the Cost Management PoC spike.

> **Hard Deadline: July 31, 2026** — PoC only, not production-grade GA software.

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

## Requirements by Area

### 1. OSAC Integration (Inventory + Metrics) — Must Have

Synchronize inventory (clusters, VMs, models) from OSAC into Cost Management. Consume resource metrics via CloudEvents. Billing is capacity-based — charge for what is provisioned, not what is used.

**Cost Team Actions:**
- [ ] Learn CloudEvents standard
- [ ] Validate OSAC CaaS/VMaaS CloudEvents are sufficient for capacity-based metering (`cluster-month`, `VM-month`)
- [ ] Agree on CloudEvents transport with OSAC (Kafka?)

**Open Questions:**
- Transport mechanism: Kafka, HTTP, NATS?
- Full list of CloudEvent types OSAC will produce (CaaS, VMaaS, BMaaS, MaaS)

**References:**
- [VMaaS CloudEvents schema](https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README.md#cloudevents-schema)
- [CaaS CloudEvents schema](https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README-caas.md#cloudevents-schema)

---

### 2. MaaS Costing — Must Have

Model-as-a-Service consumption-based rating. OSAC emits CloudEvents with token counts (in, out, inference) and request counts. Cost must compute cost within 60 seconds of receiving data.

**Cost Team Actions:**
- [ ] Define MaaS rate structure: tokens in/out, inference tokens, requests — priced per million units
- [ ] Accept RHOAI/OSAC MaaS CloudEvents and compute cost within 60 sec

**Open Questions:**
- Who collects RHOAI MaaS metrics — Cost or OSAC?
- What fields will OSAC MaaS CloudEvents contain?
- Transport for MaaS events: HTTP, Kafka, other?

**Related Ticket:** COST-7164

---

### 3. Rate Limiting / Quota Enforcement — Must Have

OSAC enforces rate limits via OPA policies. Cost Management must notify OSAC when quota/budget thresholds are approached or exceeded so OPA can act.

**Cost Team Actions:**
- [ ] Calculate quota/budget consumption
- [ ] Expose API endpoint: "am I in quota or exceeded?"

**Open Questions:**
- Single source of truth for quotas: Cost, OSAC, or both (synchronized)?

---

### 4. Budgets / Quotas — Must Have

- **Quota** = dimensional limit (CPU core-hours, GiB RAM-hour, tokens, etc.). Providers set quotas for tenants based on accumulated metered consumption over a period.
- **Budget** = monetary quota. Cost applied to metering = budget consumed.

**Cost Team Actions:**
- [ ] Investigate and implement quota/budget concept in Cost Management
- [ ] Verify if AI Grid requirements include grace periods

**Open Questions:**
- Which system is source of truth: Cost, OSAC, or both synced?
- Are quotas/budgets scoped to tenant projects?

---

### 5. Notifications / Alerts — Must Have

Fire alerts when a tenant approaches a threshold percentage of their quota/budget for a period (e.g. 70% of monthly quota). OSAC consumes these alerts to trigger OPA-enforced rate limiting.

**Cost Team Actions:**
- [ ] Create user-configurable threshold rules for notifications/alerts
- [ ] Agree on alert format (CloudEvents?) and transport with OSAC

**Open Questions:**
- Does OSAC have an existing alerting mechanism?
- Transport: Kafka, HTTP, NATS?

---

### 6. Cost Tiers — Must Have

Tiered pricing support: e.g. first 20 GiB free, next 100 GiB at $0.08/GiB-month, next 1000 GiB at $0.07/GiB-month. Required for both capacity-based and MaaS consumption-based rates.

**Cost Team Actions:**
- [ ] Implement cost tiers for capacity-based and MaaS rates

**Open Questions:**
- Where do cost tiers live: OSAC, Cost, or both synced?

**Related Ticket:** COST-6951

---

### 7. Custom Metrics — Must Have

Ability to create a custom rate from an arbitrary metric emitted by OSAC CloudEvents. Allows new dimensions to be metered without hardcoded support.

**Cost Team Actions:**
- [ ] Investigate consuming arbitrary custom CloudEvent dimensions as rate inputs (ID, classification, rate name scheme?)

**Open Questions:**
- Who defines new dimensions to collect: OSAC or Cost?

**Related Ticket:** COST-3549

---

### 8. Bare Metal Costing — Should Have

Costing for RHEL and Windows bare metal provisioned via OSAC (BMaaS). May require Cost Management to support nodes outside of an OpenShift/OCP Virtualization cluster.

**Cost Team Actions:**
- [ ] Confirm whether OCP bare metal is already covered by existing CMMO support
- [ ] Investigate whether Cost Management has a concept of a node not parented by an OCP cluster; identify the gap if not

**Open Questions:**
- OSAC needs to define the BMaaS CloudEvents schema first

---

### 9. Tenancy / Projects — Should Have

OSAC model: `Tenant → Project → Resource`. Cost Management has tenancy but not projects. Needs project entity support, or OSAC fetches cost data from the Cost API and renders it in the right tenant/project context.

**Cost Team Actions:**
- [ ] Create project entities in Cost Management synchronized from OSAC
- [ ] Determine if RBAC is needed for providers viewing cross-project cost data

**Open Questions:**
- Will providers view cost in the Cost Management UI or in OSAC?
- Are quotas/budgets scoped per OSAC project?

---

## Consolidated Cost Team Action Items

| # | Area | Priority | Action Item |
|---|------|----------|-------------|
| 1 | OSAC Integration | Must Have | Learn CloudEvents standard |
| 2 | OSAC Integration | Must Have | Validate CaaS/VMaaS CloudEvents for capacity-based metering |
| 3 | OSAC Integration | Must Have | Agree on CloudEvents transport with OSAC |
| 4 | MaaS Costing | Must Have | Define MaaS rate structure (tokens, requests per million) |
| 5 | MaaS Costing | Must Have | Accept MaaS CloudEvents and compute cost within 60 sec |
| 6 | Rate Limiting | Must Have | Calculate quota/budget consumption |
| 7 | Rate Limiting | Must Have | Expose "am I in quota or exceeded?" API |
| 8 | Budgets/Quotas | Must Have | Implement quota/budget concept in Cost Management |
| 9 | Budgets/Quotas | Must Have | Verify grace period requirements |
| 10 | Notifications | Must Have | Implement user-configurable threshold rules for alerts |
| 11 | Notifications | Must Have | Agree on alert format and transport with OSAC |
| 12 | Cost Tiers | Must Have | Implement cost tiers for capacity-based and MaaS rates |
| 13 | Bare Metal | Should Have | Confirm existing OCP bare metal coverage |
| 14 | Bare Metal | Should Have | Investigate node-outside-OCP gap |
| 15 | Tenancy | Should Have | Implement OSAC project entities in Cost Management |
| 16 | Tenancy | Should Have | Determine RBAC needs for cross-project visibility |
| 17 | Custom Metrics | Nice to Have | Investigate arbitrary CloudEvent dimension ingestion |

---

## Key Architectural Decisions (Unresolved)

| Decision | Options | Impact |
|----------|---------|--------|
| CloudEvents transport | Kafka / HTTP / NATS | Determines ingestion architecture and Kafka dependency for self-managed Cost |
| Quota/Budget source of truth | Cost only / OSAC only / Both synced | Defines sync API, conflict resolution, and enforcement flow |
| Cost tier ownership | Cost only / OSAC only / Both synced | Shapes rate engine and sync complexity |
| Provider UI surface | Cost Management UI / OSAC fetches from Cost API | Determines if Cost needs project RBAC and project entities |
| MaaS metric collection | Cost collects from RHOAI / OSAC collects and forwards | Defines integration boundary and data pipeline ownership |

---

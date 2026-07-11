# Adversarial Review — Koku Integration Strategy & Spike

**Version:** 1.1 | **Date:** 2026-07-08 | **Reviewer:** AI-assisted

---

## Executive Summary

The integration strategy report and koku-sync spike were reviewed
adversarially against the Koku codebase and service docs.

**Critical finding:** OSAC resources are OCP resources. OSAC provisions
OpenShift Virtualization VMs, OCP clusters, and bare metal nodes — all of
which have dedicated Koku line item tables already. Only MaaS (inference
tokens) is genuinely new.

This opens two integration paths:

### Path 1: Use existing OCP tables (preferred for VMs/clusters/BM)

Write our data into Koku's existing self-hosted tables:
- **VMs** → `openshift_vm_usage_line_items_daily` (has `vm_uptime_total_seconds`, `vm_cpu_request_core_seconds`, etc.)
- **Clusters/Nodes** → `openshift_pod_usage_line_items_daily` (has `node_capacity_cpu_cores`, cluster capacity, etc.)
- **MaaS** → new table needed (no existing OCP equivalent for inference tokens)

**Pros:** Uses Koku's existing summarization SQL, cost model, and UI summary
tables without modification. No new SQL templates.

**Cons:** Must map our CloudEvent fields to OCP column names. Some columns
don't have exact equivalents (e.g., OSAC tenant → OCP namespace).

### Path 2: New OSAC table (generic, covers everything)

Add `openshift_osac_usage_line_items_daily` with our native column names.
Add a UNION in the summarization SQL to process it.

**Pros:** Clean data model, no field name shoehorning.

**Cons:** Requires Koku SQL template change. Koku's cost model SQL
(which is separate from summarization) won't know about OSAC meters.

### Mapping: Our meters → Koku VM columns

| Our meter | Koku column (`OCPVMUsageLineItemDaily`) | Conversion |
|---|---|---|
| `vm_uptime_seconds` | `vm_uptime_total_seconds` | direct |
| `vm_cpu_core_seconds` | `vm_cpu_request_core_seconds` | direct |
| `vm_memory_gib_seconds` | `vm_memory_request_byte_seconds` | × 1073741824 |
| VM `cores` | `vm_cpu_request_cores` | direct |
| VM `memory_gib` | `vm_memory_request_bytes` | × 1073741824 |

| Our meter | Koku column (`OCPPodUsageLineItemDaily`) | Conversion |
|---|---|---|
| `cluster_uptime_seconds` | `node_capacity_cpu_core_seconds` | used for infra cost |
| `cluster_worker_node_seconds` | (aggregate across nodes) | needs per-node split |
| `bm_uptime_seconds` | `node_capacity_cpu_core_seconds` | same as cluster |

| Our meter | Koku equivalent | Status |
|---|---|---|
| `maas_tokens_in` | **none** | New table needed |
| `maas_tokens_out` | **none** | New table needed |
| `maas_tokens_cached` | **none** | New table needed |
| `maas_tokens_reasoning` | **none** | New table needed |
| `maas_requests` | **none** | New table needed |

### Recommendation for the spike

**Hybrid approach:**
1. Write VM data into `openshift_vm_usage_line_items_daily` (existing table)
2. Write cluster/node data into `openshift_pod_usage_line_items_daily` (existing table)
3. Create `openshift_osac_usage_line_items_daily` only for MaaS data
4. Trigger Koku's pipeline via `/report_data/` — existing VM/pod SQL handles items 1+2 automatically
5. Add OSAC UNION only for MaaS (item 3)

This minimizes Koku changes while handling all resource types.

---

## Previous Findings (retained from v1.0)

### Strategy Document Findings

| # | Finding | Severity |
|---|---|---|
| S1 | "On-prem uses public schema" is WRONG — always `org{id}` | Critical |
| S2 | Missed Masu internal API endpoints | High |
| S3 | Missed tenant auto-provisioning | Medium |
| S4 | Missed staging table pattern | Medium |
| S5 | Strategy A effort underestimated | High |

### Spike Implementation Findings

| # | Finding | Severity |
|---|---|---|
| K1 | Invalid UUID constant (`osac` is not hex) | Critical |
| K2 | SQL injection via schema name | High |
| K3 | Missing partition management | Critical |
| K4 | UI summary SQL doesn't match Koku | High |
| K5 | No transaction wrapping | Medium |
| K6 | Schema hardcoded to wrong value | High |
| K7 | source_uuid FK on UI summary tables | Medium |

### Key insight: OSAC = OCP

| # | Finding | Severity |
|---|---|---|
| N1 | OSAC VMs map to existing `OCPVMUsageLineItemDaily` | High |
| N2 | OSAC clusters/BM map to existing `OCPPodUsageLineItemDaily` | High |
| N3 | Only MaaS needs a new table | Medium |

# Input: Cost Model Metric Feasibility — Cody

> **Source:** [cost_model_metric_feasibility.md](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/poc_architecture/metering/cost_model_metric_feasibility.md)

## Summary

Cody mapped every Koku cost model metric to OSAC meters and determined
which are feasible with lifecycle data alone (no Prometheus).

## Key Finding

**Allocation = Request in OSAC.** In traditional OCP, Koku distinguishes
between requested resources (pod spec) and actual usage (Prometheus). OSAC
collapses this — the HostType spec IS the allocation. Every allocation-based
metric is computable. Usage-based metrics are not.

## Feasible Metrics (13)

All capacity/allocation-based metrics work with our 6 OSAC meters:

| Metric | Our Meter | Formula |
|---|---|---|
| `cpu_core_request_per_hour` | `vm_cpu_core_seconds` | cores × hours × rate |
| `memory_gb_request_per_hour` | `vm_memory_gib_seconds` | GiB × hours × rate |
| `vm_cost_per_hour` | `vm_uptime_seconds` | hours × rate |
| `vm_cost_per_month` | `vm_uptime_seconds` | rate / days_in_month |
| `vm_core_cost_per_hour` | `vm_cpu_core_seconds` | cores × hours × rate |
| `vm_core_cost_per_month` | `vm_cpu_core_seconds` | cores × rate / days |
| `node_cost_per_month` | `vm_uptime_seconds` | monthly_rate / days |
| `node_core_cost_per_hour` | `vm_cpu_core_seconds` | cores × hours × rate |
| `node_core_cost_per_month` | `vm_cpu_core_seconds` | cores × rate / days |
| `cluster_cost_per_hour` | `cluster_uptime_seconds` | hours × rate |
| `cluster_cost_per_month` | `cluster_uptime_seconds` | rate / days |
| `cluster_core_cost_per_hour` | `cluster_worker_node_seconds` | nodes × cores × hours × rate |
| `project_per_month` | any active meter | rate / days (presence-based) |

## Not Feasible (8)

All require telemetry OSAC doesn't capture:

- `cpu_core_usage_per_hour` — needs Prometheus
- `cpu_core_effective_usage_per_hour` — needs `min(actual, request)`
- `memory_gb_usage_per_hour` — needs Prometheus
- `memory_gb_effective_usage_per_hour` — needs Prometheus
- `storage_gb_usage/request_per_month` — no PVC in OSAC
- `pvc_cost_per_month` — no PVC concept
- `gpu_cost_per_month` — no GPU info in OSAC

## Relevance to Our Report API

When building `GET /api/v1/reports/costs`, the Koku metrics we can
replicate map to our existing meters and cost_entries. The rate engine
already computes `rate × metering_value` — we just need to present the
results in Koku's response format.

See [koku-report-api-schema.md](../research/koku-report-api-schema.md)
for the response structure to match.

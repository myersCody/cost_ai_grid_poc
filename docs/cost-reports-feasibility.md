# Cost Reports Feasibility with OSAC

## Context

OSAC fulfillment-service manages bare-metal and virtual infrastructure — compute
instances (VMs), clusters, networking resources (VNets, subnets, IPs). It emits
lifecycle events (CREATED/UPDATED/DELETED) for all managed resources via a gRPC
Watch stream. Each event carries the full resource spec and status, including
timestamps and tenant/project context.

The question: what cost reports and analysis can we provide by consuming these
events, and how does that map to what Koku provides today?

## Koku Report Structure

Koku reports return a hierarchical JSON response:

```json
{
  "meta":  { "filter": {}, "group_by": {}, "order_by": {} },
  "data":  [{ "date": "YYYY-MM-DD", "<group>": [{ "<key>": "value", "values": [...] }] }],
  "total": { "<metric>": { "value": N, "units": "..." } }
}
```

Reports support filtering, grouping, ordering, and daily/monthly granularity.
The key reports and their metrics:

| Report | Key Metrics | Group By |
|--------|------------|----------|
| Costs | cost_total, infra_raw/markup, cost_model_cpu/memory/volume_cost, distributed_cost | cluster, node, project, tag |
| CPU | usage/request/limit/capacity (core-hours), usage_efficiency, wasted_cost | cluster, node, project |
| Memory | usage/request/limit/capacity (GiB-hours), usage_efficiency | cluster, node, project |
| Volumes | usage/request/capacity (GiB-months) | cluster, project, storageclass, PVC |
| Network | data_transfer_in/out (GB) | cluster, node, project |
| GPU | gpu_count, gpu_memory | cluster, node, project |

Cost breakdown has three layers:
- **Infrastructure**: cloud provider bill (raw + markup)
- **Cost model**: rate x usage (CPU/memory/volume/GPU costs)
- **Supplementary**: fixed platform overhead

## What We Can Provide with OSAC

### Directly Feasible

These reports are feasible because OSAC provides the necessary data: resource
specs (cores, memory), lifecycle timestamps, and tenant/project context.

**1. Compute Cost Reports**

The core use case. Duration-based billing for compute instances.

- Metrics: `cpu_core_hours`, `memory_gb_hours`, `duration_hours`, `cost_total`
- Group by: `tenant`, `project`, `cluster`, `instance_type`, labels
- Granularity: daily, monthly
- Cost calculation: instance_type rate x duration

This covers what Koku's OCP cost report does, at the VM level instead of pod
level. Example response:

```json
{
  "data": [{
    "date": "2026-06-24",
    "tenants": [{
      "tenant": "acme-corp",
      "values": [{
        "cpu_core_hours": {"value": 960.0, "units": "Core-Hours"},
        "memory_gb_hours": {"value": 3840.0, "units": "GiB-Hours"},
        "instance_count": 4,
        "cost_total": {"value": 245.00, "units": "USD"}
      }]
    }]
  }],
  "total": {
    "cpu_core_hours": {"value": 960.0, "units": "Core-Hours"},
    "cost_total": {"value": 245.00, "units": "USD"}
  }
}
```

**2. Capacity / Allocation Reports**

How many cores/GB each tenant is consuming vs total available capacity.
Instance type distribution across tenants.

- Metrics: `allocated_cores`, `allocated_memory_gb`, `instance_count`,
  `capacity_cores`, `capacity_memory_gb`
- Group by: `tenant`, `project`, `instance_type`
- Analogous to Koku's CPU/Memory request metrics

**3. Cluster Cost Reports**

Cost per cluster based on node set composition (count x host type specs).

- Metrics: `node_count`, `total_cores`, `total_memory_gb`, `cost_total`
- Group by: `cluster`, `tenant`, `host_type`
- Derived from ClusterSpec.node_sets and HostType specs

**4. Network Resource Billing**

Duration-based cost for networking resources. New capability not in Koku.

- Metrics: `virtual_network_hours`, `subnet_hours`, `public_ip_hours`,
  `network_cost_total`
- Group by: `tenant`, `project`, `resource_type`
- Cost: flat rate per resource per hour of existence

**5. Cost Distribution**

Distribute shared/platform costs to tenants proportionally by usage.

- Same concept as Koku's `distributed_cost` and
  `cost_platform_distributed`
- Distribute cluster overhead, networking costs, shared infrastructure
  to tenants based on their CPU/memory consumption ratio

**6. Markup**

Apply customer-defined markup percentages on base rates.
Straightforward to implement — same as Koku's markup model.

### Not Feasible Without Additional Data Sources

| Koku Feature | Why Not Available | What Would Be Needed |
|---|---|---|
| Usage efficiency / wasted cost | OSAC tracks allocation, not actual utilization | Prometheus/metrics agent inside VMs |
| Pod-level chargeback | OSAC manages VMs, not pods | Koku's OCP operator for pod metrics |
| Cloud infrastructure costs | OSAC is private cloud, no AWS/Azure/GCP bill | Not applicable |
| Network data transfer | OSAC tracks network resources, not traffic | Network flow monitoring |
| Storage usage | OSAC tracks disk allocation, not actual I/O | Storage monitoring agent |
| GPU-specific metrics | OSAC doesn't expose GPU details per instance | GPU monitoring / device plugin |

### Future: Combining OSAC + Pod Metrics

If the clusters managed by OSAC run OpenShift, we could combine:
- OSAC events → infrastructure-level costs (VM hours, cluster costs)
- Koku operator → pod-level metrics within those clusters

This would give a full-stack cost view: how much infrastructure a cluster
costs (from OSAC) plus how that cost distributes across workloads running
inside (from pod metrics). This is the "OCP on infrastructure" model that
Koku already supports for AWS/Azure/GCP, but applied to OSAC-managed infra.

## Koku Components to Reuse

### Directly Reusable

- **Report API response format** — meta/data/total structure, hierarchical
  grouping, pagination
- **Query parameter system** — filter, group_by, order_by, time_scope,
  start_date/end_date, resolution
- **Cost model concept** — rates table mapping instance_type -> $/hour
- **Cost distribution SQL** — platform cost distribution by usage ratio
- **Markup calculation** — percentage-based markup on base costs
- **Summary table pattern** — pre-aggregated daily tables for fast queries

### Adapt (simpler versions)

- **Provider map** — mapping report type -> SQL aggregations (fewer types)
- **Query handler** — Django ORM aggregation pattern (or equivalent in Go)
- **Serializers** — response formatting and validation

### Skip

- Masu ingestion pipeline (replaced by event consumer)
- Pod-level line items and raw report processing
- Cloud provider cost reconciliation
- Exchange rate / multi-currency (unless needed later)
- OCP-on-cloud hybrid reports

## Recommended Phasing

**Phase 1**: Compute cost by tenant/project (most immediate value)
**Phase 2**: Cluster cost reports, instance type breakdown
**Phase 3**: Network resource billing, cost distribution
**Phase 4**: Integration with pod-level metrics for full-stack view

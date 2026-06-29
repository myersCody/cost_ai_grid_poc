# Koku Report API Schema Reference

> Research for REQ-3/REQ-5: matching Koku's report format in our API.
> Source: Koku codebase at `/Users/mpovolny/Projects/koku/`

## Response Structure

Every Koku report endpoint returns:

```json
{
  "meta": {
    "count": 5,
    "limit": 100,
    "offset": 0,
    "others": 0,
    "total": { /* aggregated totals across entire result set */ },
    "filter": { /* echo of request filters */ },
    "order_by": { /* echo of ordering */ }
  },
  "links": {
    "first": "/api/v1/reports/openshift/costs/?limit=100&offset=0",
    "next": null,
    "previous": null,
    "last": "/api/v1/reports/openshift/costs/?limit=100&offset=0"
  },
  "data": [ /* time-grouped entries */ ]
}
```

## Cost Report: `GET /reports/openshift/costs/`

### Total Block (in meta.total)

Three cost layers, each with `raw`, `markup`, `usage`, `total`:

```json
{
  "cost": {
    "raw":    {"value": 125.50, "units": "USD"},
    "markup": {"value": 12.55, "units": "USD"},
    "usage":  {"value": 45.00, "units": "USD"},
    "total":  {"value": 183.05, "units": "USD"}
  },
  "infrastructure": {
    "raw":    {"value": 125.50, "units": "USD"},
    "markup": {"value": 12.55, "units": "USD"},
    "usage":  {"value": 20.00, "units": "USD"},
    "total":  {"value": 158.05, "units": "USD"}
  },
  "supplementary": {
    "raw":    {"value": 0.00, "units": "USD"},
    "markup": {"value": 0.00, "units": "USD"},
    "usage":  {"value": 25.00, "units": "USD"},
    "total":  {"value": 25.00, "units": "USD"}
  },
  "cost_units": "USD"
}
```

When grouped by project, additional distributed cost fields appear:
- `cost.platform_distributed`
- `cost.worker_unallocated_distributed`
- `cost.network_unattributed_distributed`
- `cost.storage_unattributed_distributed`
- `cost.gpu_unallocated_distributed`
- `cost.distributed` (sum of all above)

### Data Block

Array of time-grouped entries (daily or monthly):

```json
{
  "data": [
    {
      "date": "2026-06-01",
      "projects": [
        {
          "project": "my-app",
          "values": [
            {
              "date": "2026-06-01",
              "project": "my-app",
              "cost": { "raw": {...}, "markup": {...}, "usage": {...}, "total": {...} },
              "infrastructure": { "raw": {...}, "markup": {...}, "usage": {...}, "total": {...} },
              "supplementary": { "raw": {...}, "usage": {...}, "total": {...} },
              "clusters": ["cluster-1"],
              "source_uuid": ["uuid-1"]
            }
          ]
        }
      ]
    }
  ]
}
```

The grouping key in the data array matches the `group_by` parameter:
- `group_by[project]=*` → each entry has `"projects": [...]`
- `group_by[cluster]=*` → each entry has `"clusters": [...]`
- `group_by[node]=*` → each entry has `"nodes": [...]`

### Query Parameters

**Filtering:**
```
filter[time_scope_value]=-30    # last 30 days
filter[time_scope_units]=day    # day or month
filter[resolution]=daily        # daily or monthly
filter[limit]=100               # pagination
filter[offset]=0
filter[project]=my-app          # specific project
filter[cluster]=cluster-1       # specific cluster
```

**Alternative date range:**
```
start_date=2026-06-01
end_date=2026-06-30
```

**Grouping:**
```
group_by[project]=*             # group by project
group_by[cluster]=*             # group by cluster
group_by[node]=*                # group by node
group_by[tag:env]=*             # group by tag
```

**Ordering:**
```
order_by[cost]=desc
order_by[date]=asc
```

### Filterable Dimensions (OCP)

| Dimension | Filter key | Group-by key |
|---|---|---|
| Project/namespace | `filter[project]` | `group_by[project]` |
| Cluster | `filter[cluster]` | `group_by[cluster]` |
| Node | `filter[node]` | `group_by[node]` |
| PVC | `filter[persistentvolumeclaim]` | `group_by[persistentvolumeclaim]` |
| Storage class | `filter[storageclass]` | `group_by[storageclass]` |
| Tags | `filter[tag:key]` | `group_by[tag:key]` |
| Cost category | `filter[category]` | — |

### SQL Aggregations (from provider_map.py)

```python
# cost = infrastructure + cost model
cost_total = cloud_infra_cost + markup_cost + cost_model_cost

# infrastructure = cloud provider bill + markup
infra_total = cloud_infra_cost + markup_cost + cost_model_infra_cost

# supplementary = fixed platform costs
sup_total = cost_model_supplementary_cost

# cost model = CPU + memory + volume + GPU
cost_model_cost = Sum(cost_model_cpu_cost + cost_model_memory_cost +
                      cost_model_volume_cost + cost_model_gpu_cost)
```

## What We Need for Our Report API

### Simplified for OSAC PoC

We don't have cloud provider costs (no AWS/Azure/GCP bill), so:
- **infrastructure** layer is empty (no cloud provider)
- **supplementary** layer is empty (no platform overhead)
- **cost** layer is everything: `rate × metering_value`

Our simplified total block:

```json
{
  "cost": {
    "total": {"value": 183.05, "units": "USD"}
  },
  "cost_units": "USD"
}
```

Or, if we want compatibility with the Koku format, map our single cost layer:

```json
{
  "cost": {
    "raw":    {"value": 0, "units": "USD"},
    "markup": {"value": 0, "units": "USD"},
    "usage":  {"value": 183.05, "units": "USD"},
    "total":  {"value": 183.05, "units": "USD"}
  },
  "infrastructure": {
    "raw": {"value": 0, "units": "USD"}, "markup": {"value": 0, "units": "USD"},
    "usage": {"value": 0, "units": "USD"}, "total": {"value": 0, "units": "USD"}
  },
  "supplementary": {
    "raw": {"value": 0, "units": "USD"}, "markup": {"value": 0, "units": "USD"},
    "usage": {"value": 0, "units": "USD"}, "total": {"value": 0, "units": "USD"}
  },
  "cost_units": "USD"
}
```

### Our Groupable Dimensions

| Koku dimension | Our equivalent | Source |
|---|---|---|
| `project` | `tenant` (or `project`) | `cost_entries.tenant_id` |
| `cluster` | `cluster_id` | `inventory_cluster.cluster_id` |
| `node` | `instance_id` | `inventory_compute_instance.instance_id` |
| `tag:key` | labels | `inventory_*.labels` JSONB |

### Our Filterable Dimensions

| Dimension | Filter | Source |
|---|---|---|
| Tenant | `filter[tenant]` | `cost_entries.tenant_id` |
| Resource type | `filter[resource_type]` | `cost_entries.resource_type` |
| Meter | `filter[meter_name]` | `cost_entries.meter_name` |
| Resource ID | `filter[resource_id]` | `cost_entries.resource_id` |
| Date range | `start_date`, `end_date` | `cost_entries.period_start/end` |

### Implementation Priority

1. **Cost by tenant** — `group_by[tenant]=*` with daily/monthly resolution
2. **Cost by resource type** — `group_by[resource_type]=*`
3. **Cost by meter** — `group_by[meter_name]=*`
4. **Time filtering** — `start_date`, `end_date`, `filter[resolution]`
5. **CSV/JSON export** — `Accept: text/csv` header

## Source Files in Koku

| File | What it does |
|---|---|
| `koku/api/report/ocp/query_handler.py` | Runs SQL, packs response |
| `koku/api/report/ocp/provider_map.py` | Defines aggregations per report type |
| `koku/api/report/ocp/view.py` | `OCPCostView` — Django REST endpoint |
| `koku/api/report/ocp/serializers.py` | Query parameter validation |
| `koku/api/common/pagination.py` | `ReportPagination` — response wrapper |

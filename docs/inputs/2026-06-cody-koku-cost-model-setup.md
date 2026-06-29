# Input: Populating Koku Cost Models — Cody

## Quick Setup

To create a cost model in the local Koku instance:

1. In the Koku repo, edit `scripts/load-test-customer-data.sh`
2. Comment out everything in `build_onprem_data` except:
   ```bash
   add_cost_models 'Test OCP on Premises' openshift_on_prem_cost_model.json "$KOKU_API_HOSTNAME":"$KOKU_PORT"
   ```
3. Run:
   ```bash
   make create-test-customer && make load-test-customer-data test_source=onprem
   ```

This creates a test customer and loads the on-prem OCP cost model with
predefined rates for CPU, memory, node, and cluster metrics.

## Cost Model File

The rate definitions are in `scripts/openshift_on_prem_cost_model.json`
in the Koku repo. This JSON defines per-metric rates that Koku uses for
cost calculation.

## Integration with Our PoC

Once populated, the cost model rates are in Koku's `cost_model` and
`cost_model_rate` tables (schema `org1234567` on `koku-db:15432`). Our
consumer could read these rates and map them to our meters using the
metric mapping from [Cody's feasibility analysis](2026-06-cody-cost-model-metric-feasibility.md):

| Koku metric | Our meter |
|---|---|
| `cpu_core_request_per_hour` | `vm_cpu_core_seconds` (÷3600) |
| `memory_gb_request_per_hour` | `vm_memory_gib_seconds` (÷3600) |
| `node_cost_per_month` | `vm_uptime_seconds` |
| `cluster_cost_per_month` | `cluster_uptime_seconds` |

## Koku DB Access

```
Host: localhost:15432
User: postgres
Password: postgres
Database: postgres
Tenant schema: org1234567
Key tables: cost_model, cost_model_rate, price_list
```

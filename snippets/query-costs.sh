#!/bin/bash
# Query cost data from the inventory-watcher database.
# Useful for demos and ad-hoc cost analysis.

set -uo pipefail

DB_CONTAINER=cost-db
DB_NAME=costdb

BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

section() {
  echo ""
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${BOLD}${BLUE}  $1${RESET}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
}

q() {
  docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -c "$1" 2>/dev/null
}

section "Rate definitions"
q "SELECT resource_type, meter_name, price_per_unit, currency FROM rates ORDER BY resource_type, meter_name;"

section "Total cost by tenant"
q "SELECT tenant_id, round(sum(cost_amount)::numeric, 4) as total_cost, currency
   FROM cost_entries GROUP BY tenant_id, currency ORDER BY total_cost DESC;"

section "Cost by resource type"
q "SELECT resource_type, round(sum(cost_amount)::numeric, 4) as total_cost, currency
   FROM cost_entries GROUP BY resource_type, currency ORDER BY total_cost DESC;"

section "Cost by meter"
q "SELECT meter_name, count(*) as entries, round(sum(cost_amount)::numeric, 6) as total_cost, currency
   FROM cost_entries GROUP BY meter_name, currency ORDER BY total_cost DESC;"

section "MaaS cost by model"
q "SELECT m.model_name, ce.meter_name,
          count(*) as entries,
          round(sum(ce.cost_amount)::numeric, 6) as cost, ce.currency
   FROM cost_entries ce
   JOIN inventory_model m ON ce.resource_id = m.model_id
   WHERE ce.resource_type = 'model'
   GROUP BY m.model_name, ce.meter_name, ce.currency
   ORDER BY m.model_name, ce.meter_name;"

section "VM cost by instance"
q "SELECT ci.name, ce.meter_name,
          round(sum(ce.cost_amount)::numeric, 6) as cost, ce.currency
   FROM cost_entries ce
   JOIN inventory_compute_instance ci ON ce.resource_id = ci.instance_id
   WHERE ce.resource_type = 'compute_instance'
   GROUP BY ci.name, ce.meter_name, ce.currency
   ORDER BY ci.name, ce.meter_name;"

section "Pipeline summary"
q "SELECT
     (SELECT count(*) FROM raw_events) as raw_events,
     (SELECT count(*) FROM metering_entries) as metering_entries,
     (SELECT count(*) FROM cost_entries) as cost_entries,
     (SELECT count(*) FROM rates) as rates,
     (SELECT count(*) FROM inventory_compute_instance WHERE deleted_at IS NULL) as live_vms,
     (SELECT count(*) FROM inventory_cluster WHERE deleted_at IS NULL) as live_clusters,
     (SELECT count(*) FROM inventory_model WHERE deleted_at IS NULL) as live_models;"

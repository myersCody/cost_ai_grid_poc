#!/bin/bash
# Set up a realistic demo environment with VMs, models, and metering data.
#
# Creates infrastructure in OSAC (VMs) and mock MaaS data, then runs the
# consumer long enough to produce metering entries and cost entries.
#
# Prerequisites:
#   - OSAC running (gRPC + REST gateway)
#   - cost-db container running
#   - inventory-watcher and maas-simulator built
#   - Valid token in /tmp/osac_token.txt
#
# Usage:
#   ./snippets/setup-demo-data.sh

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
WATCHER_BIN="$REPO_DIR/inventory-watcher/inventory-watcher"
SIM_BIN="$REPO_DIR/inventory-watcher/maas-simulator"
TOKEN=$(cat /tmp/osac_token.txt)
BASE=http://localhost:8011
DB_CONTAINER=cost-db
DB_NAME=costdb

BLUE='\033[0;34m'
GREEN='\033[0;32m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

section() {
  echo ""
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${BOLD}${BLUE}  $1${RESET}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
}

info() { echo -e "  ${DIM}▸${RESET} $1"; }

db_exec() {
  docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -c "$1" 2>/dev/null
}

# ── Preflight ──
section "Preflight"
[ -f "$WATCHER_BIN" ] || { echo "ERROR: build inventory-watcher first"; exit 1; }
[ -f "$SIM_BIN" ] || { echo "ERROR: build maas-simulator first"; exit 1; }
curl -s "$BASE/api/fulfillment/v1/instance_types" -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1 \
  || { echo "ERROR: OSAC not reachable"; exit 1; }
info "All checks passed"

# ── Reset cost database ──
section "Resetting cost database"
db_exec "DROP TABLE IF EXISTS raw_events, inventory_compute_instance, inventory_cluster, inventory_instance_type, inventory_project, inventory_model, daily_usage_summary, metering_entries, rates, cost_entries CASCADE;" > /dev/null
info "Tables dropped"

# ── Ensure OSAC has clean instance types ──
section "OSAC instance types"
for spec in "standard-2-8:2:8" "standard-4-16:4:16" "standard-8-32:8:32" "standard-16-64:16:64"; do
  IFS=: read -r name cores mem <<< "$spec"
  curl -s -X POST "$BASE/api/fulfillment/v1/instance_types" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d "{\"metadata\":{\"name\":\"$name\"},\"spec\":{\"cores\":$cores,\"memory_gib\":$mem,\"description\":\"$cores cores, ${mem}GB\",\"state\":\"INSTANCE_TYPE_STATE_ACTIVE\"}}" > /dev/null 2>&1
  info "$name ($cores cores, ${mem}GB)"
done

# ── Ensure OSAC has networking prereqs ──
section "OSAC networking"

# Network class
NC_EXISTS=$(curl -s "$BASE/api/fulfillment/v1/network_classes" -H "Authorization: Bearer $TOKEN" | jq '.size // 0')
if [ "$NC_EXISTS" = "0" ]; then
  curl -s -X POST "$BASE/api/private/v1/network_classes" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d '{"metadata":{"name":"default-nc"},"title":"Default Network","implementation_strategy":"ovn-kubernetes","is_default":true}' > /dev/null
  info "Created network class"
else
  info "Network class exists"
fi

# Virtual network
VN_EXISTS=$(curl -s "$BASE/api/fulfillment/v1/virtual_networks" -H "Authorization: Bearer $TOKEN" | jq '.size // 0')
if [ "$VN_EXISTS" = "0" ]; then
  VN_RESP=$(curl -s -X POST "$BASE/api/fulfillment/v1/virtual_networks" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d '{"metadata":{"name":"demo-vnet"},"spec":{"ipv4_cidr":"10.0.0.0/16"}}')
  VN_ID=$(echo "$VN_RESP" | jq -r '.id')
  NC_ID=$(curl -s "$BASE/api/fulfillment/v1/network_classes" -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')
  curl -s -X PATCH "$BASE/api/private/v1/virtual_networks/$VN_ID" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d "{\"id\":\"$VN_ID\",\"spec\":{\"ipv4_cidr\":\"10.0.0.0/16\",\"region\":\"default\",\"network_class\":\"$NC_ID\"},\"status\":{\"state\":\"VIRTUAL_NETWORK_STATE_READY\"}}" > /dev/null
  info "Created virtual network (READY)"
else
  info "Virtual network exists"
fi

# Subnet
SN_EXISTS=$(curl -s "$BASE/api/fulfillment/v1/subnets" -H "Authorization: Bearer $TOKEN" | jq '.size // 0')
if [ "$SN_EXISTS" = "0" ]; then
  VN_ID=$(curl -s "$BASE/api/fulfillment/v1/virtual_networks" -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')
  curl -s -X POST "$BASE/api/private/v1/subnets" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d "{\"metadata\":{\"name\":\"demo-subnet\"},\"spec\":{\"virtual_network\":\"$VN_ID\",\"ipv4_cidr\":\"10.0.1.0/24\"},\"status\":{\"state\":\"SUBNET_STATE_READY\"}}" > /dev/null
  info "Created subnet (READY)"
else
  info "Subnet exists"
fi

SUBNET_ID=$(curl -s "$BASE/api/fulfillment/v1/subnets" -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')

# Template
TPL_EXISTS=$(curl -s "$BASE/api/private/v1/compute_instance_templates" -H "Authorization: Bearer $TOKEN" | jq '.size // 0')
if [ "$TPL_EXISTS" = "0" ]; then
  curl -s -X POST "$BASE/api/private/v1/compute_instance_templates" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d '{"metadata":{"name":"standard-vm"},"title":"Standard VM","description":"Standard virtual machine"}' > /dev/null
  info "Created compute instance template"
else
  info "Template exists"
fi
TPL_ID=$(curl -s "$BASE/api/private/v1/compute_instance_templates" -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')

# ── Create compute instances (tenant-acme infrastructure) ──
section "Creating compute instances"

create_vm() {
  local name=$1 cores=$2 mem=$3 env=$4 role=$5
  EXISTING=$(curl -s "$BASE/api/fulfillment/v1/compute_instances" -H "Authorization: Bearer $TOKEN" | jq -r ".items[] | select(.metadata.name == \"$name\") | .id")
  if [ -n "$EXISTING" ] && [ "$EXISTING" != "null" ]; then
    info "$name already exists"
    return
  fi
  curl -s -X POST "$BASE/api/private/v1/compute_instances" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d "{
      \"metadata\":{\"name\":\"$name\",\"labels\":{\"env\":\"$env\",\"role\":\"$role\"}},
      \"spec\":{
        \"template\":\"$TPL_ID\",\"cores\":$cores,\"memory_gib\":$mem,
        \"network_attachments\":[{\"subnet\":\"$SUBNET_ID\"}],
        \"boot_disk\":{\"size_gib\":100},
        \"image\":{\"source_type\":\"registry\",\"source_ref\":\"quay.io/fedora/fedora:latest\"},
        \"run_strategy\":\"Always\"
      },
      \"status\":{\"state\":\"COMPUTE_INSTANCE_STATE_RUNNING\"}
    }" > /dev/null 2>&1
  info "$name ($cores cores, ${mem}GB) — $role"
}

# Production cluster VMs
create_vm "prod-api-1"     4  16 production api-server
create_vm "prod-api-2"     4  16 production api-server
create_vm "prod-worker-1"  8  32 production worker
create_vm "prod-worker-2"  8  32 production worker
create_vm "prod-worker-3"  8  32 production worker
create_vm "prod-gpu-1"    16  64 production gpu-worker

# Staging VMs
create_vm "staging-api"    2   8 staging    api-server
create_vm "staging-worker" 4  16 staging    worker

# ── Start consumer and let it run ──
section "Running inventory-watcher (90 seconds for metering + rating)"
info "This will reconcile inventory, run metering sweep, and rate entries..."

OSAC_BASE_URL="$BASE" \
OSAC_TOKEN="$TOKEN" \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/$DB_NAME \
RECONCILE_INTERVAL=5m SUMMARIZE_INTERVAL=5m \
INGEST_LISTEN_ADDR=localhost:8020 \
"$WATCHER_BIN" > /tmp/demo-setup.log 2>&1 &
PID=$!
sleep 5

# ── Fire MaaS events ──
section "Generating MaaS traffic"
info "Simulating inference traffic across 4 models, 3 tenants..."
"$SIM_BIN" -target http://localhost:8020 -count 500 -rate 200 -workers 8

# Wait for metering sweep (60s) + rating sweep (30s after metering)
info "Waiting for metering sweep (60s) + rating sweep (30s)..."
sleep 95

kill $PID 2>/dev/null; wait $PID 2>/dev/null || true
info "Consumer stopped"

# ── Show results ──
section "Demo data summary"

echo ""
echo -e "  ${BOLD}Infrastructure inventory:${RESET}"
db_exec "SELECT name, cores, memory_gib, state FROM inventory_compute_instance WHERE deleted_at IS NULL ORDER BY name;"

echo ""
echo -e "  ${BOLD}Model inventory:${RESET}"
db_exec "SELECT model_id, model_name, tenant, state FROM inventory_model ORDER BY model_name;"

echo ""
echo -e "  ${BOLD}Rates:${RESET}"
db_exec "SELECT resource_type, meter_name, price_per_unit, currency FROM rates ORDER BY resource_type, meter_name;"

echo ""
echo -e "  ${BOLD}Pipeline counts:${RESET}"
db_exec "SELECT
  (SELECT count(*) FROM raw_events) as raw_events,
  (SELECT count(*) FROM metering_entries) as metering_entries,
  (SELECT count(*) FROM cost_entries) as cost_entries;"

echo ""
echo -e "  ${BOLD}Cost by resource type:${RESET}"
db_exec "SELECT resource_type, round(sum(cost_amount)::numeric, 4) as total_cost, currency
  FROM cost_entries GROUP BY resource_type, currency ORDER BY total_cost DESC;"

echo ""
echo -e "  ${BOLD}Cost by tenant:${RESET}"
db_exec "SELECT tenant_id, round(sum(cost_amount)::numeric, 4) as total_cost, currency
  FROM cost_entries GROUP BY tenant_id, currency ORDER BY total_cost DESC;"

section "Done"
info "Run 'bash snippets/query-costs.sh' for detailed cost queries"
info "Run 'bash snippets/benchmark-maas.sh --quick' for throughput test"

#!/bin/bash
# Automated demo recording for Requirement #1: Full Pipeline
#
# Runs through all demo acts with pauses between them so the output
# is readable in a recording. Designed to be run inside asciinema
# or with screen recording.
#
# Usage:
#   asciinema rec demo-req1.cast -c 'bash snippets/demo-req1-record.sh'
#   # or just:
#   bash snippets/demo-req1-record.sh
#
# Prerequisites:
#   - OSAC running (gRPC + REST gateway)
#   - cost-db container running
#   - inventory-watcher binary built
#   - Valid token in /tmp/osac_token.txt

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
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

section() {
  echo ""
  echo ""
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${BOLD}${BLUE}  $1${RESET}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo ""
  sleep 2
}

narrate() {
  echo -e "  ${CYAN}▸ $1${RESET}"
  sleep 1
}

show_cmd() {
  echo -e "  ${DIM}\$ $1${RESET}"
}

db_query() {
  docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -c "$1" 2>/dev/null
}

pause() {
  sleep "${1:-3}"
}

# ── Preflight ──
curl -s "$BASE/api/fulfillment/v1/instance_types" -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1 \
  || { echo "ERROR: OSAC not reachable at $BASE"; exit 1; }
docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -c "SELECT 1" > /dev/null 2>&1 \
  || { echo "ERROR: cost-db not reachable"; exit 1; }

# ── Reset ──
docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -c "DROP TABLE IF EXISTS raw_events, inventory_compute_instance, inventory_cluster, inventory_instance_type, inventory_project, inventory_model, daily_usage_summary, metering_entries, rates, cost_entries, quotas CASCADE;" > /dev/null 2>&1

# Get OSAC prereqs
SUBNET_ID=$(curl -s "$BASE/api/fulfillment/v1/subnets" -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')
TPL_ID=$(curl -s "$BASE/api/private/v1/compute_instance_templates" -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')

echo -e "${BOLD}${GREEN}"
echo "  ╔══════════════════════════════════════════════════════════╗"
echo "  ║   Cost Management for AI Grid — Requirement #1 Demo     ║"
echo "  ║   Full Pipeline: Events → Metering → Cost → Quotas      ║"
echo "  ╚══════════════════════════════════════════════════════════╝"
echo -e "${RESET}"
sleep 3

# ══════════════════════════════════════════════════════════════
section "Act 1: Infrastructure"
narrate "Two PostgreSQL containers: OSAC (port 5433) and Cost (port 5434)"

show_cmd "docker ps --format 'table {{.Names}}\t{{.Status}}\t{{.Ports}}' --filter name=osac-db --filter name=cost-db"
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" --filter name=osac-db --filter name=cost-db
pause

narrate "OSAC has compute instances running:"
show_cmd "curl -s .../compute_instances | jq '.size'"
CI_COUNT=$(curl -s "$BASE/api/fulfillment/v1/compute_instances" -H "Authorization: Bearer $TOKEN" | jq '.size // 0')
echo "  $CI_COUNT compute instances in OSAC"
pause

narrate "Cost database is empty — clean slate:"
show_cmd "psql -c '\\dt'"
db_query "\dt"
pause

# ══════════════════════════════════════════════════════════════
section "Act 2: Start the inventory-watcher"
narrate "Starting the consumer — watch for reconciliation, rate seeding, quota setup..."

OSAC_BASE_URL="$BASE" \
OSAC_TOKEN="$TOKEN" \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/$DB_NAME \
RECONCILE_INTERVAL=5m SUMMARIZE_INTERVAL=5m \
INGEST_LISTEN_ADDR=localhost:8020 \
"$WATCHER_BIN" > /tmp/demo-watcher.log 2>&1 &
WATCHER_PID=$!
sleep 5

narrate "Watcher started — showing key log lines:"
grep -E 'schema ready|seeded|reconciled|watch stream' /tmp/demo-watcher.log | head -10 | while read line; do
  echo -e "  ${DIM}$line${RESET}"
done
pause

narrate "Inventory populated from OSAC:"
db_query "SELECT name, cores, memory_gib, state FROM inventory_compute_instance WHERE deleted_at IS NULL ORDER BY name;"
pause

narrate "Default rates seeded:"
db_query "SELECT resource_type, meter_name, price_per_unit, currency FROM rates ORDER BY resource_type, meter_name;"
pause

# ══════════════════════════════════════════════════════════════
section "Act 3: Real-time event ingestion"
narrate "Creating a new VM in OSAC — watch it appear in our database instantly..."

CI_BEFORE=$(docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -t -A -c "SELECT count(*) FROM inventory_compute_instance WHERE deleted_at IS NULL;")

show_cmd "curl -s -X POST .../compute_instances -d '{name: demo-vm, cores: 4, memory_gib: 16}'"
NEW_CI=$(curl -s -X POST "$BASE/api/private/v1/compute_instances" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d "{
    \"metadata\": {\"name\": \"demo-vm\", \"labels\": {\"demo\": \"live\"}},
    \"spec\": {
      \"template\": \"$TPL_ID\",
      \"cores\": 4, \"memory_gib\": 16,
      \"network_attachments\": [{\"subnet\": \"$SUBNET_ID\"}],
      \"boot_disk\": {\"size_gib\": 100},
      \"image\": {\"source_type\": \"registry\", \"source_ref\": \"quay.io/fedora/fedora:latest\"},
      \"run_strategy\": \"Always\"
    },
    \"status\": {\"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\"}
  }")
NEW_CI_ID=$(echo "$NEW_CI" | jq -r '.id')
echo -e "  ${GREEN}Created: demo-vm ($NEW_CI_ID)${RESET}"
sleep 2

CI_AFTER=$(docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -t -A -c "SELECT count(*) FROM inventory_compute_instance WHERE deleted_at IS NULL;")
echo -e "  Inventory: ${CI_BEFORE} → ${CI_AFTER} instances"
pause

# ══════════════════════════════════════════════════════════════
section "Act 4: Raw event log (audit trail)"
narrate "Every event is stored immutably before processing:"
db_query "SELECT event_id, event_type, resource_type, resource_id FROM raw_events ORDER BY received_at DESC LIMIT 5;"
pause

# ══════════════════════════════════════════════════════════════
section "Act 5: Metering sweep"
narrate "Waiting for the 60-second metering sweep..."
narrate "(The sweep calculates: cores × duration = cpu_core_seconds)"

echo ""
echo -e "  ${YELLOW}Waiting 65 seconds for metering sweep...${RESET}"
sleep 65

narrate "Metering entries produced:"
db_query "SELECT meter_name, resource_id, round(value::numeric, 1) as value, unit FROM metering_entries WHERE resource_type = 'compute_instance' ORDER BY resource_id, meter_name LIMIT 15;"
pause 5

# ══════════════════════════════════════════════════════════════
section "Act 6: Cost in dollars"
narrate "Rating sweep converts metering entries to dollar costs (every 30s)..."
echo -e "  ${YELLOW}Waiting 35 seconds for rating sweep...${RESET}"
sleep 35

narrate "Cost entries:"
db_query "SELECT ci.name, ce.meter_name, round(ce.cost_amount::numeric, 6) as cost, ce.currency FROM cost_entries ce JOIN inventory_compute_instance ci ON ce.resource_id = ci.instance_id ORDER BY ci.name, ce.meter_name LIMIT 15;"
pause

narrate "Rates applied:"
db_query "SELECT meter_name, price_per_unit, currency FROM rates WHERE resource_type = 'compute_instance' ORDER BY meter_name;"
pause

# ══════════════════════════════════════════════════════════════
section "Act 7: Quota status API"
narrate "OSAC can check: is this tenant within quota?"

show_cmd "curl -s http://localhost:8020/api/v1/quotas/shared | jq"
curl -s http://localhost:8020/api/v1/quotas/shared | jq '.quotas[] | select(.consumed > 0) | {meter_name, consumed: (.consumed | round), limit, percentage, over_50: .thresholds["50"]}'
pause 5

# ══════════════════════════════════════════════════════════════
section "Act 8: OpenMeter-compatible ingest"
narrate "The OSAC metering collector can send events directly to us."
narrate "Same CloudEvents format — just a URL change from OpenMeter."

show_cmd "curl -s -X POST http://localhost:8020/api/v1/events -d '{type: osac.compute_instance.lifecycle, ...}'"
curl -s -X POST http://localhost:8020/api/v1/events \
  -H "Content-Type: application/cloudevents+json" \
  -d "{
    \"specversion\": \"1.0\",
    \"type\": \"osac.compute_instance.lifecycle\",
    \"source\": \"osac.metering.collector\",
    \"id\": \"demo-heartbeat-$(date +%s)\",
    \"time\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
    \"subject\": \"tenant-acme\",
    \"data\": {
      \"duration_seconds\": 60,
      \"cpu_core_seconds\": 480,
      \"memory_gib_seconds\": 1920,
      \"tenant_id\": \"tenant-acme\",
      \"instance_id\": \"demo-external-vm\",
      \"template\": \"osac.templates.ocp_virt_vm\",
      \"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\",
      \"cores\": 8,
      \"memory_gib\": 32
    }
  }" | jq .

sleep 1
narrate "Metering entries created directly from the event payload:"
db_query "SELECT meter_name, round(value::numeric, 0) as value, unit FROM metering_entries WHERE resource_id = 'demo-external-vm' ORDER BY meter_name;"
pause

# ══════════════════════════════════════════════════════════════
section "Act 9: DELETE and final metering"
narrate "Deleting demo-vm — final metering covers the gap since last sweep..."

show_cmd "curl -s -X DELETE .../compute_instances/$NEW_CI_ID"
curl -s -X DELETE "$BASE/api/fulfillment/v1/compute_instances/$NEW_CI_ID" \
  -H "Authorization: Bearer $TOKEN" > /dev/null
sleep 3

narrate "Instance marked as deleted:"
db_query "SELECT name, state, deleted_at IS NOT NULL as deleted FROM inventory_compute_instance WHERE name = 'demo-vm';"
pause

# ══════════════════════════════════════════════════════════════
section "Act 10: MaaS traffic"
narrate "Same pipeline handles consumption-based billing (tokens/requests)..."

show_cmd "./maas-simulator -target http://localhost:8020 -count 50 -rate 20"
"$SIM_BIN" -target http://localhost:8020 -count 50 -rate 20 -workers 4
echo ""
sleep 1

narrate "MaaS metering:"
db_query "SELECT meter_name, count(*) as entries, round(sum(value)::numeric, 0) as total, unit FROM metering_entries WHERE resource_type = 'model' GROUP BY meter_name, unit ORDER BY meter_name;"
pause

# ══════════════════════════════════════════════════════════════
section "Act 11: Pipeline summary"

db_query "SELECT
  (SELECT count(*) FROM raw_events) as raw_events,
  (SELECT count(*) FROM metering_entries) as metering_entries,
  (SELECT count(*) FROM cost_entries) as cost_entries,
  (SELECT count(*) FROM rates) as rates,
  (SELECT count(*) FROM quotas) as quotas,
  (SELECT count(*) FROM inventory_compute_instance WHERE deleted_at IS NULL) as live_vms,
  (SELECT count(*) FROM inventory_model WHERE deleted_at IS NULL) as live_models;"
pause

narrate "Cost by resource type:"
db_query "SELECT resource_type, count(*) as entries, round(sum(cost_amount)::numeric, 4) as total_cost, currency FROM cost_entries GROUP BY resource_type, currency ORDER BY resource_type;"
pause

# ── Cleanup ──
kill $WATCHER_PID 2>/dev/null; wait $WATCHER_PID 2>/dev/null || true

echo ""
echo -e "${BOLD}${GREEN}"
echo "  ╔══════════════════════════════════════════════════════════╗"
echo "  ║   Demo complete.                                         ║"
echo "  ║                                                          ║"
echo "  ║   Events → Metering → Cost → Quotas                     ║"
echo "  ║   All in one Go binary. No Kafka. No batch processing.   ║"
echo "  ╚══════════════════════════════════════════════════════════╝"
echo -e "${RESET}"
echo ""

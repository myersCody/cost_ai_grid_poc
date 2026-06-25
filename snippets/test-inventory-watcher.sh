#!/bin/bash
# End-to-end test for the inventory-watcher against a running OSAC instance.
#
# Prerequisites:
#   - OSAC gRPC + REST gateway running (see docs/local-dev-setup.md)
#   - cost-db PostgreSQL container running on port 5434
#   - Valid token in /tmp/osac_token.txt
#   - Test data created (see snippets/create-test-data.sh)
#   - inventory-watcher binary built:
#       cd inventory-watcher && go build -o inventory-watcher ./cmd/consumer/
#
# Usage:
#   ./snippets/test-inventory-watcher.sh

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
WATCHER_BIN="$REPO_DIR/inventory-watcher/inventory-watcher"
TOKEN=$(cat /tmp/osac_token.txt)
BASE=http://localhost:8011
DB_CONTAINER=cost-db
DB_NAME=costdb

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

pass=0
fail=0

section() {
  echo ""
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${BOLD}${BLUE}  $1${RESET}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
}

info() {
  echo -e "  ${DIM}▸${RESET} $1"
}

assert_eq() {
  local label="$1" expected="$2" actual="$3"
  if [ "$expected" = "$actual" ]; then
    echo -e "  ${GREEN}✓ PASS${RESET} $label ${DIM}(got: $actual)${RESET}"
    ((pass++))
  else
    echo -e "  ${RED}✗ FAIL${RESET} $label ${DIM}(expected: $expected, got: $actual)${RESET}"
    ((fail++))
  fi
}

assert_ge() {
  local label="$1" minimum="$2" actual="$3"
  if [ "$actual" -ge "$minimum" ] 2>/dev/null; then
    echo -e "  ${GREEN}✓ PASS${RESET} $label ${DIM}(got: $actual, min: $minimum)${RESET}"
    ((pass++))
  else
    echo -e "  ${RED}✗ FAIL${RESET} $label ${DIM}(expected >= $minimum, got: $actual)${RESET}"
    ((fail++))
  fi
}

db_query() {
  docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -t -A -c "$1" 2>/dev/null
}

# ── Preflight checks ──
section "Preflight checks"

if [ ! -f "$WATCHER_BIN" ]; then
  echo "ERROR: inventory-watcher binary not found at $WATCHER_BIN"
  echo "Build it: cd inventory-watcher && go build -o inventory-watcher ./cmd/consumer/"
  exit 1
fi

curl -s "$BASE/api/fulfillment/v1/instance_types" -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1 \
  || { echo "ERROR: OSAC REST gateway not reachable at $BASE"; exit 1; }

docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -c "SELECT 1" > /dev/null 2>&1 \
  || { echo "ERROR: cost-db not reachable"; exit 1; }

echo "  OSAC REST gateway: OK"
echo "  cost-db: OK"
echo "  inventory-watcher binary: OK"

# ── Clean slate ──
echo ""
section "Resetting cost database"
db_query "DROP TABLE IF EXISTS raw_events, inventory_compute_instance, inventory_cluster, inventory_instance_type, inventory_project, daily_usage_summary, metering_entries CASCADE;" > /dev/null
info "Tables dropped"

# ── Test 1: Reconciliation ──
echo ""
section "Test 1: Reconciliation on startup"
echo "  Starting inventory-watcher for 6 seconds..."

OSAC_BASE_URL="$BASE" \
OSAC_TOKEN="$TOKEN" \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/$DB_NAME \
RECONCILE_INTERVAL=5m \
SUMMARIZE_INTERVAL=5m \
"$WATCHER_BIN" > /tmp/watcher-test.log 2>&1 &
WATCHER_PID=$!
sleep 6
kill $WATCHER_PID 2>/dev/null; wait $WATCHER_PID 2>/dev/null || true

# Check that tables were created
TABLE_COUNT=$(db_query "SELECT count(*) FROM information_schema.tables WHERE table_schema='public';")
assert_ge "tables created" 5 "$TABLE_COUNT"

# Check compute instances were reconciled
CI_COUNT=$(db_query "SELECT count(*) FROM inventory_compute_instance;")
OSAC_CI_COUNT=$(curl -s "$BASE/api/fulfillment/v1/compute_instances" -H "Authorization: Bearer $TOKEN" | jq '.total // .size // 0')
assert_eq "compute instances reconciled" "$OSAC_CI_COUNT" "$CI_COUNT"

# Check instance types were reconciled
IT_COUNT=$(db_query "SELECT count(*) FROM inventory_instance_type;")
OSAC_IT_COUNT=$(curl -s "$BASE/api/fulfillment/v1/instance_types" -H "Authorization: Bearer $TOKEN" | jq '.total // .size // 0')
assert_eq "instance types reconciled" "$OSAC_IT_COUNT" "$IT_COUNT"

# Check that cores and memory are populated
ZERO_MEM=$(db_query "SELECT count(*) FROM inventory_compute_instance WHERE memory_gib = 0;")
assert_eq "no zero-memory instances" "0" "$ZERO_MEM"

# ── Test 2: Real-time event watching + raw event log ──
echo ""
section "Test 2: Watch stream + raw event log"

# Get prerequisites for creating a compute instance
SUBNET_ID=$(curl -s "$BASE/api/fulfillment/v1/subnets" -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')
TPL_ID=$(curl -s "$BASE/api/private/v1/compute_instance_templates" -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')

if [ "$SUBNET_ID" = "null" ] || [ "$TPL_ID" = "null" ]; then
  echo "  SKIP: no subnet or template found. Run snippets/create-test-data.sh first."
else
  echo "  Starting inventory-watcher..."
  OSAC_BASE_URL="$BASE" \
  OSAC_TOKEN="$TOKEN" \
  INVENTORY_DB_URL=postgres://user:pass@localhost:5434/$DB_NAME \
  RECONCILE_INTERVAL=5m \
  SUMMARIZE_INTERVAL=5m \
  "$WATCHER_BIN" > /tmp/watcher-test.log 2>&1 &
  WATCHER_PID=$!
  sleep 4

  CI_BEFORE=$(db_query "SELECT count(*) FROM inventory_compute_instance;")
  RAW_BEFORE=$(db_query "SELECT count(*) FROM raw_events;")

  echo "  Creating compute instance via OSAC API..."
  NEW_CI=$(curl -s -X POST "$BASE/api/private/v1/compute_instances" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{
      \"metadata\": {\"name\": \"watch-test-$(date +%s)\"},
      \"spec\": {
        \"template\": \"$TPL_ID\",
        \"cores\": 2, \"memory_gib\": 4,
        \"network_attachments\": [{\"subnet\": \"$SUBNET_ID\"}],
        \"boot_disk\": {\"size_gib\": 50},
        \"image\": {\"source_type\": \"registry\", \"source_ref\": \"quay.io/fedora/fedora:latest\"},
        \"run_strategy\": \"Always\"
      },
      \"status\": {\"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\"}
    }")
  NEW_CI_ID=$(echo "$NEW_CI" | jq -r '.id')
  echo "  Created: $NEW_CI_ID"

  sleep 3
  kill $WATCHER_PID 2>/dev/null; wait $WATCHER_PID 2>/dev/null || true

  CI_AFTER=$(db_query "SELECT count(*) FROM inventory_compute_instance;")
  RAW_AFTER=$(db_query "SELECT count(*) FROM raw_events;")

  assert_eq "inventory count incremented" "$((CI_BEFORE + 1))" "$CI_AFTER"
  assert_eq "raw event count incremented" "$((RAW_BEFORE + 1))" "$RAW_AFTER"

  # Verify the raw event has correct metadata
  if [ "$NEW_CI_ID" != "null" ] && [ -n "$NEW_CI_ID" ]; then
    RAW_RESOURCE_ID=$(db_query "SELECT resource_id FROM raw_events ORDER BY received_at DESC LIMIT 1;")
    assert_eq "raw event resource_id matches" "$NEW_CI_ID" "$RAW_RESOURCE_ID"

    RAW_TYPE=$(db_query "SELECT event_type FROM raw_events ORDER BY received_at DESC LIMIT 1;")
    assert_eq "raw event type is CREATED" "EVENT_TYPE_OBJECT_CREATED" "$RAW_TYPE"

    RAW_RESOURCE_TYPE=$(db_query "SELECT resource_type FROM raw_events ORDER BY received_at DESC LIMIT 1;")
    assert_eq "raw event resource_type is ComputeInstance" "ComputeInstance" "$RAW_RESOURCE_TYPE"
  fi
fi

# ── Test 3: Metering sweep ──
echo ""
section "Test 3: Metering sweep (requires ~65s)"

if [ "${SKIP_METERING:-}" = "1" ]; then
  echo "  SKIP: set SKIP_METERING=1"
else
  echo "  Starting inventory-watcher and waiting 65s for metering sweep..."
  OSAC_BASE_URL="$BASE" \
  OSAC_TOKEN="$TOKEN" \
  INVENTORY_DB_URL=postgres://user:pass@localhost:5434/$DB_NAME \
  RECONCILE_INTERVAL=5m \
  SUMMARIZE_INTERVAL=5m \
  "$WATCHER_BIN" > /tmp/watcher-test.log 2>&1 &
  WATCHER_PID=$!
  sleep 65
  kill $WATCHER_PID 2>/dev/null; wait $WATCHER_PID 2>/dev/null || true

  # Check metering entries were created
  ME_COUNT=$(db_query "SELECT count(*) FROM metering_entries;")
  assert_ge "metering entries created" 3 "$ME_COUNT"

  # Check all 3 meter types are present
  METER_TYPES=$(db_query "SELECT count(DISTINCT meter_name) FROM metering_entries;")
  assert_eq "3 meter types present" "3" "$METER_TYPES"

  # Verify meter names
  HAS_UPTIME=$(db_query "SELECT count(*) FROM metering_entries WHERE meter_name = 'vm_uptime_seconds';")
  assert_ge "vm_uptime_seconds entries" 1 "$HAS_UPTIME"

  HAS_CPU=$(db_query "SELECT count(*) FROM metering_entries WHERE meter_name = 'vm_cpu_core_seconds';")
  assert_ge "vm_cpu_core_seconds entries" 1 "$HAS_CPU"

  HAS_MEM=$(db_query "SELECT count(*) FROM metering_entries WHERE meter_name = 'vm_memory_gib_seconds';")
  assert_ge "vm_memory_gib_seconds entries" 1 "$HAS_MEM"

  # Verify last_metered_at is set on billable instances
  UNMETERED=$(db_query "SELECT count(*) FROM inventory_compute_instance WHERE state = 'COMPUTE_INSTANCE_STATE_RUNNING' AND last_metered_at IS NULL AND deleted_at IS NULL;")
  assert_eq "all billable instances metered" "0" "$UNMETERED"

  # Verify values are positive
  NEG_VALUES=$(db_query "SELECT count(*) FROM metering_entries WHERE value <= 0;")
  assert_eq "no zero/negative metering values" "0" "$NEG_VALUES"
fi

# ── Test 4: Project sync ──
echo ""
section "Test 4: Project sync"

PROJ_TABLE_EXISTS=$(db_query "SELECT count(*) FROM information_schema.tables WHERE table_name = 'inventory_project';")
assert_eq "projects table exists" "1" "$PROJ_TABLE_EXISTS"

# Check if OSAC has projects — reconciler should have synced them
OSAC_PROJ_COUNT=$(curl -s "$BASE/api/fulfillment/v1/projects" -H "Authorization: Bearer $TOKEN" | jq '.total // .size // 0' 2>/dev/null)
if [ "$OSAC_PROJ_COUNT" -gt 0 ] 2>/dev/null; then
  INV_PROJ_COUNT=$(db_query "SELECT count(*) FROM inventory_project;")
  assert_eq "projects reconciled" "$OSAC_PROJ_COUNT" "$INV_PROJ_COUNT"
else
  echo "  INFO: no projects in OSAC (count=$OSAC_PROJ_COUNT) — sync not testable, table exists OK"
fi

# ── Test 5: DELETE + final metering ──
echo ""
section "Test 5: DELETE + final metering"

if [ "$SUBNET_ID" = "null" ] || [ "$TPL_ID" = "null" ]; then
  echo "  SKIP: no subnet or template found"
else
  echo "  Starting inventory-watcher..."
  OSAC_BASE_URL="$BASE" \
  OSAC_TOKEN="$TOKEN" \
  INVENTORY_DB_URL=postgres://user:pass@localhost:5434/$DB_NAME \
  RECONCILE_INTERVAL=5m \
  SUMMARIZE_INTERVAL=5m \
  "$WATCHER_BIN" > /tmp/watcher-test.log 2>&1 &
  WATCHER_PID=$!
  sleep 4

  # Create a VM, wait for it to be ingested
  echo "  Creating VM to delete..."
  DELETE_CI=$(curl -s -X POST "$BASE/api/private/v1/compute_instances" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{
      \"metadata\": {\"name\": \"delete-test-$(date +%s)\"},
      \"spec\": {
        \"template\": \"$TPL_ID\",
        \"cores\": 2, \"memory_gib\": 4,
        \"network_attachments\": [{\"subnet\": \"$SUBNET_ID\"}],
        \"boot_disk\": {\"size_gib\": 50},
        \"image\": {\"source_type\": \"registry\", \"source_ref\": \"quay.io/fedora/fedora:latest\"},
        \"run_strategy\": \"Always\"
      },
      \"status\": {\"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\"}
    }")
  DELETE_CI_ID=$(echo "$DELETE_CI" | jq -r '.id')
  echo "  Created: $DELETE_CI_ID"
  sleep 2

  ME_BEFORE=$(db_query "SELECT count(*) FROM metering_entries WHERE resource_id = '$DELETE_CI_ID';")

  # Delete it
  echo "  Deleting VM..."
  curl -s -X DELETE "$BASE/api/fulfillment/v1/compute_instances/$DELETE_CI_ID" \
    -H "Authorization: Bearer $TOKEN" > /dev/null
  sleep 3

  kill $WATCHER_PID 2>/dev/null; wait $WATCHER_PID 2>/dev/null || true

  # Verify final metering entries were produced
  ME_AFTER=$(db_query "SELECT count(*) FROM metering_entries WHERE resource_id = '$DELETE_CI_ID';")
  assert_ge "final metering entries on DELETE" 3 "$ME_AFTER"

  # Verify the instance is marked deleted in inventory
  DELETED_AT=$(db_query "SELECT deleted_at IS NOT NULL FROM inventory_compute_instance WHERE instance_id = '$DELETE_CI_ID';")
  assert_eq "instance marked as deleted" "t" "$DELETED_AT"

  # Verify raw events include the DELETE
  DELETE_RAW=$(db_query "SELECT count(*) FROM raw_events WHERE resource_id = '$DELETE_CI_ID' AND event_type = 'EVENT_TYPE_OBJECT_DELETED';")
  assert_eq "DELETE raw event stored" "1" "$DELETE_RAW"
fi

# ── Test 6: Non-billable state filtering ──
echo ""
section "Test 6: Non-billable state filtering"

if [ "$SUBNET_ID" = "null" ] || [ "$TPL_ID" = "null" ]; then
  echo "  SKIP: no subnet or template found"
elif [ "${SKIP_METERING:-}" = "1" ]; then
  echo "  SKIP: metering tests skipped"
else
  # Create a STOPPED VM (non-billable) via private API
  echo "  Creating STOPPED VM (non-billable)..."
  STOPPED_CI=$(curl -s -X POST "$BASE/api/private/v1/compute_instances" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{
      \"metadata\": {\"name\": \"stopped-test-$(date +%s)\"},
      \"spec\": {
        \"template\": \"$TPL_ID\",
        \"cores\": 2, \"memory_gib\": 4,
        \"network_attachments\": [{\"subnet\": \"$SUBNET_ID\"}],
        \"boot_disk\": {\"size_gib\": 50},
        \"image\": {\"source_type\": \"registry\", \"source_ref\": \"quay.io/fedora/fedora:latest\"},
        \"run_strategy\": \"Always\"
      },
      \"status\": {\"state\": \"COMPUTE_INSTANCE_STATE_STOPPED\"}
    }")
  STOPPED_CI_ID=$(echo "$STOPPED_CI" | jq -r '.id')
  echo "  Created STOPPED VM: $STOPPED_CI_ID"

  # Run watcher with a metering sweep
  echo "  Running watcher for 65s (one metering sweep)..."
  OSAC_BASE_URL="$BASE" \
  OSAC_TOKEN="$TOKEN" \
  INVENTORY_DB_URL=postgres://user:pass@localhost:5434/$DB_NAME \
  RECONCILE_INTERVAL=5m \
  SUMMARIZE_INTERVAL=5m \
  "$WATCHER_BIN" > /tmp/watcher-test.log 2>&1 &
  WATCHER_PID=$!
  sleep 65
  kill $WATCHER_PID 2>/dev/null; wait $WATCHER_PID 2>/dev/null || true

  # Verify the STOPPED VM is in inventory but has NO metering entries
  STOPPED_IN_INV=$(db_query "SELECT count(*) FROM inventory_compute_instance WHERE instance_id = '$STOPPED_CI_ID';")
  assert_eq "stopped VM in inventory" "1" "$STOPPED_IN_INV"

  STOPPED_METERED=$(db_query "SELECT count(*) FROM metering_entries WHERE resource_id = '$STOPPED_CI_ID';")
  assert_eq "stopped VM has no metering entries" "0" "$STOPPED_METERED"

  STOPPED_LMA=$(db_query "SELECT last_metered_at IS NULL FROM inventory_compute_instance WHERE instance_id = '$STOPPED_CI_ID';")
  assert_eq "stopped VM last_metered_at is NULL" "t" "$STOPPED_LMA"
fi

# ── Test 7: Deduplication ──
echo ""
section "Test 7: Event deduplication"

# Insert a duplicate event_id directly
EXISTING_EVENT_ID=$(db_query "SELECT event_id FROM raw_events LIMIT 1;")
if [ -n "$EXISTING_EVENT_ID" ]; then
  db_query "INSERT INTO raw_events (event_id, event_type, event_source, event_time, tenant_id, resource_type, resource_id, data) VALUES ('$EXISTING_EVENT_ID', 'DUPLICATE', '', NOW(), '', '', '', '{}') ON CONFLICT (event_id) DO NOTHING;" > /dev/null
  DUPE_COUNT=$(db_query "SELECT count(*) FROM raw_events WHERE event_id = '$EXISTING_EVENT_ID';")
  assert_eq "duplicate event rejected" "1" "$DUPE_COUNT"
else
  echo "  SKIP: no events to test deduplication against"
fi

# ── Test 8: Data integrity ──
echo ""
section "Test 8: Data integrity"

# Verify raw event data column contains valid JSON with the event payload
HAS_DATA=$(db_query "SELECT count(*) FROM raw_events WHERE data::text != '{}' AND data IS NOT NULL;")
TOTAL_RAW=$(db_query "SELECT count(*) FROM raw_events;")
assert_eq "all raw events have data payload" "$TOTAL_RAW" "$HAS_DATA"

# Verify inventory instances have non-empty tenant
EMPTY_TENANT=$(db_query "SELECT count(*) FROM inventory_compute_instance WHERE tenant = '';")
assert_eq "no empty tenant values" "0" "$EMPTY_TENANT"

# ── Summary ──
section "Results"
total=$((pass + fail))
echo ""
if [ "$fail" -eq 0 ]; then
  echo -e "  ${GREEN}${BOLD}ALL $total TESTS PASSED${RESET}"
else
  echo -e "  ${GREEN}✓ $pass passed${RESET}  ${RED}✗ $fail failed${RESET}  ${DIM}($total total)${RESET}"
  echo ""
  echo -e "  ${YELLOW}Watcher log (last 20 lines):${RESET}"
  tail -20 /tmp/watcher-test.log 2>/dev/null || true
  exit 1
fi
echo ""

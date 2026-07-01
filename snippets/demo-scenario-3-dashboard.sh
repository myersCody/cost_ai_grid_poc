#!/bin/bash
# Demo Scenario 3: Live Dashboard — Real-Time Cost Pipeline
#
# Prerequisites:
#   - OSAC running (localhost:8011)
#   - cost-db running (localhost:5434)
#   - inventory-watcher running with INGEST_LISTEN_ADDR=localhost:8020
#   - Dashboard open in browser: docs/demo-dashboard.html (set refresh to 1s)
#
# This script walks through the pipeline step by step, pausing for you
# to narrate each phase while the dashboard updates in real-time.
#
# Usage:
#   bash snippets/demo-scenario-3-dashboard.sh

set -uo pipefail

TOKEN=$(cat /tmp/osac_token.txt 2>/dev/null)
OSAC=http://localhost:8011
INGEST=http://localhost:8020

BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

section() {
  echo ""
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${BOLD}${BLUE}  $1${RESET}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
}

narrate() {
  echo -e "  ${DIM}$1${RESET}"
}

pause() {
  echo ""
  echo -e "  ${YELLOW}▶ Press Enter to continue...${RESET}"
  read -r
}

check() {
  local label=$1
  echo -e "  ${GREEN}✓${RESET} $label"
}

summary() {
  curl -s "$INGEST/api/v1/reports/summary" 2>/dev/null | \
    python3 -c "
import sys, json
d = json.load(sys.stdin)
print(f'    Raw Events: {d[\"raw_events\"]:,}  |  Metering: {d[\"metering_entries\"]:,}  |  Cost: {d[\"cost_entries\"]:,}  |  VMs: {d[\"live_vms\"]}  |  Models: {d[\"live_models\"]}')
" 2>/dev/null
}

# ─────────────────────────────────────────────────────────────
# Preflight
# ─────────────────────────────────────────────────────────────

section "Preflight"

narrate "Checking services..."

curl -sf "$INGEST/api/v1/health" > /dev/null 2>&1 && check "Ingest endpoint OK" || { echo "  ERROR: inventory-watcher not running at $INGEST"; exit 1; }
curl -sf -H "Authorization: Bearer $TOKEN" "$OSAC/api/fulfillment/v1/compute_instances" > /dev/null 2>&1 && check "OSAC OK" || { echo "  ERROR: OSAC not reachable or token expired"; exit 1; }

echo ""
narrate "Current pipeline state:"
summary

narrate ""
narrate "Open docs/demo-dashboard.html in your browser and set refresh to 1s."

pause

# ─────────────────────────────────────────────────────────────
# Act 1: Show the live inventory
# ─────────────────────────────────────────────────────────────

section "Act 1: Live Inventory from OSAC"

narrate "The dashboard shows Live VMs — these are synced from OSAC via"
narrate "the Watch stream (real-time) and REST List reconciler (every 5m)."
narrate ""
narrate "Let's see what's in OSAC right now:"

VM_COUNT=$(curl -s -H "Authorization: Bearer $TOKEN" "$OSAC/api/fulfillment/v1/compute_instances" | python3 -c "import sys,json; print(json.load(sys.stdin)['total'])" 2>/dev/null)
echo -e "  OSAC has ${BOLD}$VM_COUNT${RESET} compute instances"
echo ""
narrate "The dashboard's Live VMs count should match (or be close — the"
narrate "reconciler runs every 5 minutes)."

pause

# ─────────────────────────────────────────────────────────────
# Act 2: Create a VM in OSAC — watch it appear
# ─────────────────────────────────────────────────────────────

section "Act 2: Create a VM in OSAC (Watch Stream)"

narrate "Creating a new compute instance via OSAC API..."
narrate "Watch the dashboard: Live VMs will increment within 1-2 seconds."
echo ""

SUBNET_ID=$(curl -s -H "Authorization: Bearer $TOKEN" "$OSAC/api/fulfillment/v1/subnets" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['items'][0]['id'] if d.get('items') else '')" 2>/dev/null)
TPL_ID=$(curl -s -H "Authorization: Bearer $TOKEN" "$OSAC/api/private/v1/compute_instance_templates" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['items'][0]['id'] if d.get('items') else '')" 2>/dev/null)

if [ -z "$SUBNET_ID" ] || [ -z "$TPL_ID" ]; then
  narrate "No subnet or template found — skipping VM creation."
  narrate "(Run snippets/create-test-data.sh to set up test infrastructure)"
  pause
else
  DEMO_VM=$(curl -s -X POST "$OSAC/api/private/v1/compute_instances" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d "{
      \"metadata\": {\"name\": \"demo-live-vm\", \"labels\": {\"demo\": \"scenario3\"}},
      \"spec\": {
        \"template\": \"$TPL_ID\",
        \"cores\": 4, \"memory_gib\": 16,
        \"network_attachments\": [{\"subnet\": \"$SUBNET_ID\"}],
        \"boot_disk\": {\"size_gib\": 100},
        \"image\": {\"source_type\": \"registry\", \"source_ref\": \"quay.io/fedora/fedora:latest\"},
        \"run_strategy\": \"Always\"
      },
      \"status\": {\"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\"}
    }" 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)

  if [ -n "$DEMO_VM" ]; then
    check "Created VM: $DEMO_VM"
    narrate ""
    narrate "Watch the dashboard:"
    narrate "  • Raw Events counter increments (CREATED event stored)"
    narrate "  • Live VMs counter increments"
    narrate "  • After ~60s: Metering Entries will grow (metering sweep)"
    narrate "  • After ~90s: Cost Entries will grow (rating sweep)"
  else
    narrate "VM creation returned no ID — check OSAC logs"
  fi

  pause
fi

# ─────────────────────────────────────────────────────────────
# Act 3: Wait for metering sweep
# ─────────────────────────────────────────────────────────────

section "Act 3: Metering Sweep (60-second cycle)"

narrate "The metering sweep runs every 60 seconds. It reads all billable"
narrate "resources from inventory and generates usage records:"
narrate "  • vm_uptime_seconds    (duration since last metered)"
narrate "  • vm_cpu_core_seconds  (cores × duration)"
narrate "  • vm_memory_gib_seconds (GiB × duration)"
narrate ""
narrate "Watch the Metering Entries counter — it will jump by ~N×3"
narrate "(one set of 3 meters per running VM)."
narrate ""
narrate "Current state:"
summary

narrate ""
narrate "Wait ~60s and watch the dashboard update..."

pause

narrate "After the sweep:"
summary

pause

# ─────────────────────────────────────────────────────────────
# Act 4: Rating sweep
# ─────────────────────────────────────────────────────────────

section "Act 4: Rating Sweep (30-second cycle)"

narrate "The rating sweep picks up unrated metering entries and applies"
narrate "FindRate() + ApplyRate() to produce cost entries."
narrate ""
narrate "Watch the Cost Entries counter catch up to Metering Entries."
narrate "When they're equal, all usage has been priced."
narrate ""
narrate "Switch to the 'By Meter' tab to see costs broken down by"
narrate "meter type with the Infrastructure/Supplementary split."

pause

# ─────────────────────────────────────────────────────────────
# Act 5: MaaS events burst
# ─────────────────────────────────────────────────────────────

section "Act 5: Per-Tenant Rate Override"

narrate "Two tenants, same MaaS usage, different pricing."
narrate "We'll set a premium token rate for tenant-globex (3× the default)."
echo ""

docker exec cost-db psql -U user -d costdb -c "
INSERT INTO rates (tenant_id, resource_type, meter_name, koku_metric, cost_type, price_per_unit, currency, description, effective_from)
VALUES ('tenant-globex', 'model', 'maas_tokens_in', '', 'Supplementary',
        1.50 / 1000000, 'USD', 'Premium token rate for Globex (3x)', NOW())
ON CONFLICT DO NOTHING;
" 2>/dev/null

check "Inserted tenant-globex override: \$1.50/M tokens (vs \$0.50/M global)"
narrate ""
narrate "After the MaaS burst, the 'By Tenant' tab will show both tenants"
narrate "side by side — same token volume, different total cost."
narrate "tenant-globex pays 3× more for tokens_in due to the override."
narrate "You can also use the Tenant dropdown to drill into each one."

pause

# ─────────────────────────────────────────────────────────────

section "Act 6: MaaS Events — Consumption-Based Metering"

narrate "Now let's fire Model-as-a-Service events."
narrate "These simulate inference workloads with token consumption."
narrate ""
narrate "Watch the dashboard:"
narrate "  • Raw Events will climb rapidly (5 events/second)"
narrate "  • Metering Entries follows (3 entries per event)"
narrate "  • Live Models count increases as new model IDs appear"
narrate "  • After 30s: Cost Entries catches up (rating sweep)"
narrate ""
narrate "Sending 100 MaaS events at 5/s (takes ~20 seconds)..."
echo ""

cd inventory-watcher
./maas-simulator -target "$INGEST" -count 100 -rate 5 2>&1 | while IFS= read -r line; do
  echo "  $line"
done
cd ..

check "MaaS events sent"
narrate ""
narrate "Dashboard should show the burst. Switch between tabs to explore:"
narrate "  • 'By Resource Type' — model costs appear alongside compute_instance"
narrate "  • 'By Meter' — maas_tokens_in, maas_tokens_out, maas_requests"
narrate "  • 'By Tenant' — multi-tenant cost attribution"

pause

# ─────────────────────────────────────────────────────────────
# Act 6: Delete VM — final metering
# ─────────────────────────────────────────────────────────────

section "Act 7: Delete VM — Final Metering"

if [ -n "${DEMO_VM:-}" ]; then
  narrate "Deleting demo-live-vm..."
  narrate "Watch the dashboard: Live VMs will decrement, and 3 final"
  narrate "metering entries appear immediately (no sweep wait)."
  echo ""

  curl -s -X DELETE "$OSAC/api/fulfillment/v1/compute_instances/$DEMO_VM" \
    -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1

  check "Deleted VM: $DEMO_VM"
  narrate ""
  narrate "Dashboard updates:"
  narrate "  • Raw Events +1 (DELETED event)"
  narrate "  • Metering Entries +3 (final metering: uptime, CPU, memory)"
  narrate "  • Live VMs -1"
  narrate "  • Cost Entries +3 (after next rating sweep)"
else
  narrate "No demo VM was created — skipping deletion."
fi

pause

# ─────────────────────────────────────────────────────────────
# Act 7: CSV export
# ─────────────────────────────────────────────────────────────

section "Act 8: CSV Export"

narrate "The report API supports CSV export — useful for chargeback"
narrate "reports to finance teams."
echo ""
echo -e "  ${BOLD}curl '$INGEST/api/v1/reports/costs?group_by=tenant&format=csv'${RESET}"
echo ""

curl -s "$INGEST/api/v1/reports/costs?group_by=tenant&format=csv" 2>/dev/null | head -10

narrate ""
narrate "This can be piped to a file or imported into a spreadsheet."

pause

# ─────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────

section "Demo Complete"

narrate "Final pipeline state:"
summary

echo ""
narrate "What we demonstrated:"
narrate "  1. Live inventory sync from OSAC (Watch stream + reconciler)"
narrate "  2. Real-time event ingestion (VM created → dashboard updates)"
narrate "  3. 60-second metering sweep (usage → metering entries)"
narrate "  4. 30-second rating sweep (metering → cost entries)"
narrate "  5. MaaS consumption events (tokens, requests)"
narrate "  6. DELETE with final metering (no usage gap)"
narrate "  7. CSV export for chargeback reporting"
narrate ""
narrate "All visible in real-time on the dashboard."
echo ""

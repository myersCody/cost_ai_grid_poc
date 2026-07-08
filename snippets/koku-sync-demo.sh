#!/bin/bash
# Demo: sync cost-event-consumer data into local Koku
#
# Prerequisites:
#   1. Our consumer running with data:
#      INGEST_LISTEN_ADDR=localhost:8020 ./inventory-watcher
#      ./maas-simulator -target http://localhost:8020 -count 100
#
#   2. Koku running locally:
#      cd ~/Projects/koku/koku
#      ONPREM=True make docker-up
#
# Usage:
#   ./snippets/koku-sync-demo.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

SOURCE_DB_URL="${SOURCE_DB_URL:-postgres://user:pass@localhost:5434/costdb}"
KOKU_DB_URL="${KOKU_DB_URL:-postgres://postgres:postgres@localhost:15432/postgres}"
KOKU_SCHEMA="${KOKU_SCHEMA:-org1}"
SYNC_DATE="${SYNC_DATE:-$(date +%Y-%m-%d)}"

BLUE='\033[0;34m'
BOLD='\033[1m'
RESET='\033[0m'

echo -e "${BOLD}Koku Sync Demo${RESET}"
echo ""

# Step 1: Check our DB has cost data
echo -e "${BLUE}[1/4] Checking source data...${RESET}"
COST_COUNT=$(docker exec cost-db psql -U user -d costdb -t -c \
  "SELECT count(*) FROM cost_entries WHERE period_start::date = '$SYNC_DATE'" 2>/dev/null | tr -d ' ')
echo "  Cost entries for $SYNC_DATE: $COST_COUNT"

if [ "$COST_COUNT" = "0" ]; then
  echo "  No cost data for today. Run the simulator first:"
  echo "    cd inventory-watcher && go build -o maas-simulator ./cmd/maas-simulator/"
  echo "    ./maas-simulator -target http://localhost:8020 -count 100"
  exit 1
fi

# Step 2: Check Koku DB is reachable
echo -e "${BLUE}[2/4] Checking Koku DB...${RESET}"
if ! docker exec koku-db psql -U postgres -d postgres -c "SELECT 1" > /dev/null 2>&1; then
  echo "  Koku DB not reachable on port 15432."
  echo "  Start it: cd ~/Projects/koku/koku && ONPREM=True make docker-up"
  exit 1
fi
echo "  Koku DB: OK"

# Step 3: Run sync
echo -e "${BLUE}[3/4] Running koku-sync...${RESET}"
cd "$REPO_DIR/inventory-watcher"
go run ./cmd/koku-sync/

# Step 4: Query Koku API
echo ""
echo -e "${BLUE}[4/4] Querying Koku report API...${RESET}"
KOKU_URL="${KOKU_URL:-http://localhost:8000}"
if curl -sf "$KOKU_URL/api/cost-management/v1/status/" > /dev/null 2>&1; then
  echo "  Koku API response:"
  curl -sf "$KOKU_URL/api/cost-management/v1/reports/openshift/costs/" | python3 -m json.tool | head -30
else
  echo "  Koku API not reachable. Checking DB directly..."
  docker exec koku-db psql -U postgres -d postgres -c "
    SELECT cluster_id, usage_start, count(*) as rows,
           sum(COALESCE(infrastructure_raw_cost, 0)) as infra_cost
    FROM $KOKU_SCHEMA.reporting_ocpusagelineitem_daily_summary
    WHERE source_uuid = '00000000-0000-0000-0000-osac00000001'
    GROUP BY cluster_id, usage_start
  "
fi

echo ""
echo -e "${BOLD}Done.${RESET}"

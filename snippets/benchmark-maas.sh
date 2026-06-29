#!/bin/bash
# Benchmark the MaaS metering pipeline throughput.
#
# Prerequisites:
#   - inventory-watcher running with INGEST_LISTEN_ADDR=localhost:8020
#   - maas-simulator binary built
#
# Usage:
#   ./snippets/benchmark-maas.sh              # default benchmarks
#   ./snippets/benchmark-maas.sh --quick      # just 1000 events

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
SIM="$REPO_DIR/inventory-watcher/maas-simulator"
TARGET="http://localhost:8020"

BLUE='\033[0;34m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

if [ ! -f "$SIM" ]; then
  echo "ERROR: maas-simulator not found. Build it:"
  echo "  cd inventory-watcher && go build -o maas-simulator ./cmd/maas-simulator/"
  exit 1
fi

# Check the ingest endpoint is reachable
curl -s "$TARGET/api/v1/health" > /dev/null 2>&1 || {
  echo "ERROR: ingest endpoint not reachable at $TARGET"
  echo "Start the consumer with: INGEST_LISTEN_ADDR=localhost:8020 ./inventory-watcher"
  exit 1
}

echo -e "${BOLD}MaaS Metering Pipeline Benchmark${RESET}"
echo -e "${DIM}Target: $TARGET${RESET}"
echo ""

if [ "${1:-}" = "--quick" ]; then
  TESTS=("1000:0:8")
else
  TESTS=("1000:0:8" "5000:0:16" "10000:0:16")
fi

for spec in "${TESTS[@]}"; do
  IFS=: read -r count rate workers <<< "$spec"
  rate_label="unlimited"
  [ "$rate" != "0" ] && rate_label="${rate}/s"

  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  echo -e "${BOLD}  $count events, rate=$rate_label, workers=$workers${RESET}"
  echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
  "$SIM" -target "$TARGET" -count "$count" -rate "$rate" -workers "$workers"
  echo ""
done

echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
echo -e "${BOLD}  Database totals${RESET}"
echo -e "${BLUE}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${RESET}"
docker exec cost-db psql -U user -d costdb -c \
  "SELECT
     (SELECT count(*) FROM raw_events WHERE resource_type = 'Model') as raw_events,
     (SELECT count(*) FROM metering_entries WHERE resource_type = 'model') as metering_entries,
     (SELECT count(DISTINCT resource_id) FROM inventory_model) as models,
     (SELECT count(DISTINCT tenant_id) FROM metering_entries WHERE resource_type = 'model') as tenants;"

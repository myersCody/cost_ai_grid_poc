#!/usr/bin/env bash
set -euo pipefail

# Demo startup script — starts all services and shows status.
# Prerequisites: Docker running, OSAC binaries running separately.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WATCHER_DIR="$REPO_ROOT/inventory-watcher"

GREEN='\033[0;32m'
YELLOW='\033[0;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

header() { echo -e "\n${BOLD}${CYAN}=== $1 ===${NC}\n"; }
ok()     { echo -e "  ${GREEN}✓${NC} $1"; }
warn()   { echo -e "  ${YELLOW}⚠${NC} $1"; }
fail()   { echo -e "  ${RED}✗${NC} $1"; }

# ── Step 1: Databases ──

header "Databases"

if docker ps --format '{{.Names}}' | grep -q '^cost-db$'; then
    ok "cost-db already running"
else
    echo "  Starting cost-db..."
    docker start cost-db 2>/dev/null || \
    docker run -d --name cost-db \
        -e POSTGRESQL_USER=user -e POSTGRESQL_PASSWORD=pass -e POSTGRESQL_DATABASE=costdb \
        -p 127.0.0.1:5434:5432 \
        quay.io/sclorg/postgresql-18-c10s:latest
    ok "cost-db started"
fi

if docker ps --format '{{.Names}}' | grep -q '^osac-db$'; then
    ok "osac-db already running"
else
    echo "  Starting osac-db..."
    docker start osac-db 2>/dev/null || \
    docker run -d --name osac-db \
        -e POSTGRESQL_USER=user -e POSTGRESQL_PASSWORD=pass -e POSTGRESQL_DATABASE=db \
        -p 127.0.0.1:5433:5432 \
        quay.io/sclorg/postgresql-18-c10s:latest
    ok "osac-db started"
fi

# ── Step 2: Observability stack ──

header "Observability (Prometheus + Grafana)"

cd "$REPO_ROOT/deploy/observability"
# Use alternate ports if Koku's minio is squatting on 9000/9090
PROM_HOST_PORT=9091
if ! lsof -i :9090 >/dev/null 2>&1; then
    PROM_HOST_PORT=9090
fi

# Patch port if needed
if [ "$PROM_HOST_PORT" != "9090" ]; then
    sed -i.bak "s/\"9090:9090\"/\"${PROM_HOST_PORT}:9090\"/" docker-compose.yml 2>/dev/null || true
fi

docker compose up -d 2>/dev/null
ok "Prometheus on :${PROM_HOST_PORT}"
ok "Grafana on :3000 (admin/admin)"

# Restore original port
if [ -f docker-compose.yml.bak ]; then
    mv docker-compose.yml.bak docker-compose.yml
fi

cd "$REPO_ROOT"

# ── Step 3: Build and start inventory-watcher ──

header "Inventory Watcher"

cd "$WATCHER_DIR"
echo "  Building..."
go build -o inventory-watcher ./cmd/consumer/ 2>&1
ok "Built inventory-watcher"

# Kill any existing instance
pkill -f './inventory-watcher' 2>/dev/null || true
sleep 1

# Start with custom metrics config
OSAC_TOKEN=""
if [ -f /tmp/osac_token.txt ]; then
    OSAC_TOKEN="$(cat /tmp/osac_token.txt)"
fi

OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN="$OSAC_TOKEN" \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
INGEST_LISTEN_ADDR=localhost:8020 \
CUSTOM_METRICS_CONFIG="$REPO_ROOT/deploy/custom-metrics-example.json" \
LOG_FORMAT=text \
LOG_LEVEL=info \
nohup ./inventory-watcher > /tmp/inventory-watcher.log 2>&1 &

sleep 2

if curl -sf localhost:8020/healthz >/dev/null 2>&1; then
    ok "inventory-watcher running (API :8020, metrics :9000)"
else
    fail "inventory-watcher failed to start — check /tmp/inventory-watcher.log"
fi

cd "$REPO_ROOT"

# ── Step 4: Check OSAC ──

header "OSAC Fulfillment Service"

if pgrep -f 'fulfillment-service.*grpc-server' >/dev/null 2>&1; then
    ok "gRPC server on :8010"
else
    warn "gRPC server not running — start OSAC separately"
fi

if pgrep -f 'fulfillment-service.*rest-gateway' >/dev/null 2>&1; then
    ok "REST gateway on :8011"
else
    warn "REST gateway not running — start OSAC separately"
fi

# ── Summary ──

header "Service Map"

echo -e "${BOLD}  Service                    Port      Status${NC}"
echo "  ─────────────────────────────────────────────"

check_port() {
    local name="$1" port="$2"
    if curl -sf "localhost:${port}" >/dev/null 2>&1 || \
       lsof -i ":${port}" >/dev/null 2>&1; then
        printf "  %-28s %-10s %b\n" "$name" ":${port}" "${GREEN}UP${NC}"
    else
        printf "  %-28s %-10s %b\n" "$name" ":${port}" "${RED}DOWN${NC}"
    fi
}

check_port "PostgreSQL (cost-db)"      5434
check_port "PostgreSQL (osac-db)"      5433
check_port "OSAC gRPC"                 8010
check_port "OSAC REST Gateway"         8011
check_port "Inventory Watcher API"     8020
check_port "Inventory Watcher Metrics" 9000
check_port "Prometheus"                "$PROM_HOST_PORT"
check_port "Grafana"                   3000

echo ""
echo -e "${BOLD}  Quick links:${NC}"
echo "    Grafana dashboard:  http://localhost:3000/d/cost-consumer-overview"
echo "    Prometheus targets: http://localhost:${PROM_HOST_PORT}/targets"
echo "    Metrics endpoint:   http://localhost:9000/metrics"
echo "    Health check:       http://localhost:8020/healthz"
echo "    Watcher logs:       tail -f /tmp/inventory-watcher.log"
echo ""

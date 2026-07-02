#!/bin/bash
# Simple CRC deployment test - verify all components are running

set -euo pipefail

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

check_pod() {
  local ns="$1" label="$2" desc="$3"
  local ready=$(kubectl get pods -n "$ns" -l "$label" -o jsonpath='{.items[0].status.containerStatuses[0].ready}' 2>/dev/null)

  if [ "$ready" = "true" ]; then
    echo -e "  ${GREEN}✓ PASS${RESET} $desc is running"
    ((pass++))
  else
    echo -e "  ${RED}✗ FAIL${RESET} $desc is not ready"
    ((fail++))
  fi
}

check_endpoint() {
  local url="$1" desc="$2"
  local status=$(kubectl run curl-test --image=curlimages/curl:latest --rm -i --restart=Never -- \
    curl -s -o /dev/null -w "%{http_code}" "$url" 2>&1 | tail -1)

  if [ "$status" = "200" ] || [ "$status" = "404" ] || [ "$status" = "401" ]; then
    echo -e "  ${GREEN}✓ PASS${RESET} $desc is reachable (HTTP $status)"
    ((pass++))
  else
    echo -e "  ${RED}✗ FAIL${RESET} $desc is not reachable (status: $status)"
    ((fail++))
  fi
}

# ── Infrastructure ──
section "Infrastructure Pods"
check_pod "cert-manager" "app.kubernetes.io/name=cert-manager" "cert-manager"
check_pod "cert-manager" "app.kubernetes.io/name=trust-manager" "trust-manager"
check_pod "postgres" "cnpg.io/cluster=osac,cnpg.io/podRole=instance" "OSAC PostgreSQL"

# ── OSAC Services ──
section "OSAC Services"
check_pod "osac" "app=osac-oidc" "osac-oidc (OIDC server)"
check_pod "osac" "app=osac-grpc" "osac-grpc (gRPC API)"
check_pod "osac" "app=osac-rest" "osac-rest (REST gateway)"

# ── Cost Management ──
section "Cost Management"
check_pod "cost-mgmt" "app=cost-db" "cost-db (PostgreSQL)"
check_pod "cost-mgmt" "app=cost-event-consumer" "cost-event-consumer"

# ── Service Endpoints ──
section "Service Endpoints"
check_endpoint "http://osac-rest.osac.svc:8000/api/fulfillment/v1/clusters" "OSAC REST API"
check_endpoint "https://osac-oidc.osac.svc:8013/.well-known/openid-configuration" "OSAC OIDC discovery"
check_endpoint "http://cost-event-consumer.cost-mgmt.svc:8020/healthz" "Consumer health"

# ── Database Connectivity ──
section "Database Connectivity"

# Check OSAC DB
osac_db_check=$(kubectl run psql-test --image=postgres:16 --rm -i --restart=Never -- \
  psql "postgres://service:$(kubectl get secret -n postgres osac-service-credentials -o jsonpath='{.data.password}' | base64 -d)@osac-rw.postgres.svc:5432/service?sslmode=require" \
  -c "SELECT 1" 2>&1 | grep -c "(1 row)" || echo "0")

if [ "$osac_db_check" = "1" ]; then
  echo -e "  ${GREEN}✓ PASS${RESET} OSAC database connection"
  ((pass++))
else
  echo -e "  ${RED}✗ FAIL${RESET} OSAC database connection"
  ((fail++))
fi

# Check Cost DB
cost_db_check=$(kubectl run psql-test --image=postgres:16 --rm -i --restart=Never -- \
  psql "postgres://user:pass@cost-db.cost-mgmt.svc:5432/costdb" \
  -c "SELECT 1" 2>&1 | grep -c "(1 row)" || echo "0")

if [ "$cost_db_check" = "1" ]; then
  echo -e "  ${GREEN}✓ PASS${RESET} Cost database connection"
  ((pass++))
else
  echo -e "  ${RED}✗ FAIL${RESET} Cost database connection"
  ((fail++))
fi

# ── Summary ──
echo ""
section "Test Summary"
echo -e "  ${GREEN}Passed:${RESET} $pass"
echo -e "  ${RED}Failed:${RESET} $fail"
echo ""

if [ $fail -eq 0 ]; then
  echo -e "${GREEN}${BOLD}✓ All tests passed!${RESET}"
  echo ""
  echo "Next steps:"
  echo "  1. Generate OSAC token: python3 inventory-watcher/scripts/gen_token.py"
  echo "  2. Update consumer secret with real token"
  echo "  3. Run demo scenario with test data"
  exit 0
else
  echo -e "${RED}${BOLD}✗ Some tests failed${RESET}"
  exit 1
fi

#!/usr/bin/env bash
set -uo pipefail

# Integration test for the full OSAC + cost-consumer stack on k3s.
# Expects: kubectl port-forward running for cost-consumer (:8020) and osac-rest (:8011).
# Expects: /tmp/osac_token.txt with a valid token.

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'
PASS=0
FAIL=0

check() {
    local desc="$1"
    shift
    if "$@" >/dev/null 2>&1; then
        echo -e "  ${GREEN}✓${NC} $desc"
        PASS=$((PASS + 1))
    else
        echo -e "  ${RED}✗${NC} $desc"
        FAIL=$((FAIL + 1))
    fi
}

check_output() {
    local desc="$1"
    local expected="$2"
    shift 2
    local output
    output=$("$@" 2>/dev/null) || true
    if echo "$output" | grep -q "$expected"; then
        echo -e "  ${GREEN}✓${NC} $desc"
        PASS=$((PASS + 1))
    else
        echo -e "  ${RED}✗${NC} $desc (expected: $expected, got: $output)"
        FAIL=$((FAIL + 1))
    fi
}

BASE=http://localhost:8020
OSAC=http://localhost:8011
TOKEN=$(cat /tmp/osac_token.txt)

echo "=== Integration Test: Full Stack ==="
echo ""

# ── 1. Health checks ──
echo "--- Health checks ---"
check "liveness probe" curl -sf "$BASE/healthz"
check "readiness probe" curl -sf "$BASE/readyz"
check "metrics endpoint" curl -sf http://localhost:9000/metrics

# ── 2. Create test data in OSAC ──
echo ""
echo "--- Creating OSAC test data ---"

# Instance type
curl -sf -X POST "$OSAC/api/fulfillment/v1/instance_types" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $TOKEN" \
    -d '{
        "metadata": {"name": "ci-test-2-8"},
        "spec": {"cores": 2, "memory_gib": 8, "description": "CI test", "state": "INSTANCE_TYPE_STATE_ACTIVE"}
    }' >/dev/null 2>&1 || true
check "instance type created" curl -sf "$OSAC/api/fulfillment/v1/instance_types" -H "Authorization: Bearer $TOKEN"

# We need network infra for VMs — get or create
SUBNET_ID=$(curl -sf "$OSAC/api/fulfillment/v1/subnets" -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); print(items[0]['id'] if items else '')" 2>/dev/null || echo "")

if [ -z "$SUBNET_ID" ]; then
    # Create network class
    NC_ID=$(curl -sf -X POST "$OSAC/api/private/v1/network_classes" \
        -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
        -d '{"metadata":{"name":"ci-nc"},"title":"CI Network","description":"CI test","implementation_strategy":"ovn-kubernetes","is_default":true}' | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")

    # Create virtual network
    VN_ID=$(curl -sf -X POST "$OSAC/api/fulfillment/v1/virtual_networks" \
        -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
        -d "{\"metadata\":{\"name\":\"ci-vn\"},\"spec\":{\"network_class\":\"$NC_ID\"}}" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")

    # Create subnet
    SUBNET_ID=$(curl -sf -X POST "$OSAC/api/private/v1/subnets" \
        -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
        -d "{\"metadata\":{\"name\":\"ci-subnet\"},\"spec\":{\"virtual_network\":\"$VN_ID\",\"cidr\":\"10.200.0.0/24\"}}" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
fi

# Create compute instance template
TPL_ID=$(curl -sf "$OSAC/api/private/v1/compute_instance_templates" -H "Authorization: Bearer $TOKEN" | python3 -c "import sys,json; items=json.load(sys.stdin).get('items',[]); print(items[0]['id'] if items else '')" 2>/dev/null || echo "")

if [ -z "$TPL_ID" ]; then
    TPL_ID=$(curl -sf -X POST "$OSAC/api/private/v1/compute_instance_templates" \
        -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
        -d '{"metadata":{"name":"ci-template"},"spec":{"cores":2,"memory_gib":8,"boot_disk_size_gib":50}}' | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
fi

# Create a compute instance
VM_ID=$(curl -sf -X POST "$OSAC/api/private/v1/compute_instances" \
    -H "Content-Type: application/json" -H "Authorization: Bearer $TOKEN" \
    -d "{
        \"metadata\":{\"name\":\"ci-test-vm\"},
        \"spec\":{
            \"template\":\"$TPL_ID\",
            \"cores\":2,\"memory_gib\":8,
            \"network_attachments\":[{\"subnet\":\"$SUBNET_ID\"}],
            \"boot_disk\":{\"size_gib\":50},
            \"image\":{\"source_type\":\"registry\",\"source_ref\":\"quay.io/fedora/fedora:latest\"},
            \"run_strategy\":\"Always\"
        },
        \"status\":{\"state\":\"COMPUTE_INSTANCE_STATE_RUNNING\"}
    }" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])" 2>/dev/null || echo "")

check "compute instance created" test -n "$VM_ID"

# ── 3. Trigger reconciliation ──
echo ""
echo "--- Reconciliation ---"
curl -sf -X POST "$BASE/api/v1/reconcile" >/dev/null
sleep 5
check_output "VMs synced to inventory" '"status"' curl -sf "$BASE/api/v1/reports/summary"

# ── 4. Send MaaS CloudEvent ──
echo ""
echo "--- CloudEvent ingest ---"
RESP=$(curl -sf -X POST "$BASE/api/v1/events" \
    -H "Content-Type: application/json" \
    -d "{
        \"specversion\":\"1.0\",
        \"type\":\"osac.model.lifecycle\",
        \"source\":\"ci-test\",
        \"id\":\"ci-maas-$(date +%s)\",
        \"time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
        \"data\":{
            \"tenant_id\":\"tenant-ci\",
            \"model_id\":\"model-ci-test\",
            \"model_name\":\"ci-model\",
            \"state\":\"MODEL_STATE_RUNNING\",
            \"tokens_in\":10000,
            \"tokens_out\":5000,
            \"requests\":50,
            \"duration_seconds\":60
        }
    }")
check_output "event accepted" "accepted" echo "$RESP"

# ── 5. Wait for metering + rating sweeps ──
echo ""
echo "--- Waiting for metering (60s) + rating (30s) sweeps ---"
sleep 95

# ── 6. Verify pipeline output ──
echo ""
echo "--- Pipeline verification ---"

# Check metering entries via report API
REPORT=$(curl -sf "$BASE/api/v1/reports/summary" 2>/dev/null || echo "{}")
check_output "metering entries exist" "metering_entries" echo "$REPORT"
check_output "cost entries exist" "cost_entries" echo "$REPORT"

# Check quota API
QUOTA=$(curl -sf "$BASE/api/v1/quotas/tenant-ci" 2>/dev/null || echo "{}")
check_output "quota API responds" "tenant_id" echo "$QUOTA"

# Check Prometheus metrics
METRICS=$(curl -sf http://localhost:9000/metrics 2>/dev/null || echo "")
check_output "events processed metric" "cost_consumer_events_processed_total" echo "$METRICS"
check_output "metering entries metric" "cost_consumer_metering_entries_created_total" echo "$METRICS"

# ── Summary ──
echo ""
echo "=========================================="
TOTAL=$((PASS + FAIL))
echo "  Results: $PASS/$TOTAL passed"
if [ "$FAIL" -gt 0 ]; then
    echo -e "  ${RED}$FAIL FAILED${NC}"
    exit 1
else
    echo -e "  ${GREEN}ALL PASSED${NC}"
fi

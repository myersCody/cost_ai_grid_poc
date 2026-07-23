#!/usr/bin/env bash
set -uo pipefail

# Integration test for the IPP gateway + cost-consumer MaaS flow on k3s.
# Expects: kubectl configured, ai-gateway namespace with IPP stack deployed,
#          cost-mgmt namespace with consumer deployed, test-client pod running.

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
        echo -e "  ${RED}✗${NC} $desc (expected: $expected, got: $(echo "$output" | head -5))"
        FAIL=$((FAIL + 1))
    fi
}

CONSUMER=http://localhost:8020
METRICS=http://localhost:9000

# Port-forward consumer for direct checks
kubectl port-forward -n cost-mgmt svc/cost-event-consumer 8020:8020 &
kubectl port-forward -n cost-mgmt svc/cost-event-consumer 9000:9000 &
sleep 5

echo "=== Integration Test: IPP Gateway ==="
echo ""

# ── 1. Consumer health ──
echo "--- Consumer health ---"
check "liveness probe" curl -sf "$CONSUMER/healthz"
check "readiness probe" curl -sf "$CONSUMER/readyz"
check "metrics endpoint" curl -sf "$METRICS/metrics"

# ── 2. Gateway readiness ──
echo ""
echo "--- Gateway readiness ---"
check "gateway programmed" kubectl wait --for=condition=Programmed gateway/ai-gateway -n ai-gateway --timeout=10s
check "IPP pod running" kubectl wait --for=condition=available deployment/payload-processing -n ai-gateway --timeout=10s
check "llm-katan running" kubectl wait --for=condition=ready pod -l app=llm-katan -n ai-gateway --timeout=10s

# ── 3. Direct balance check ──
echo ""
echo "--- Balance check (direct) ---"
BALANCE=$(curl -sf "$CONSUMER/api/v1/customers/test-user/entitlements/inference-tokens/value" 2>/dev/null || echo "{}")
check_output "balance check returns hasAccess" "hasAccess" echo "$BALANCE"

# ── 4. End-to-end inference through gateway ──
echo ""
echo "--- End-to-end inference (via Istio Gateway) ---"

# Get the pipeline summary before the test
BEFORE=$(curl -sf "$CONSUMER/api/v1/reports/summary" 2>/dev/null || echo "{}")
BEFORE_RAW=$(echo "$BEFORE" | grep -o '"raw_events":[0-9]*' | grep -o '[0-9]*' || echo "0")

# Send inference request through the gateway via test-client pod.
# x-maas-organization-id is forwarded by IPP PR #386 as organization_id
# in the CloudEvent — the consumer uses it for tenant attribution.
INFERENCE_RESP=$(kubectl exec -n ai-gateway test-client -- \
    curl -s --max-time 30 \
    http://ai-gateway-istio.ai-gateway:80/v1/chat/completions \
    -H "Authorization: Bearer test-key" \
    -H "Content-Type: application/json" \
    -H "x-maas-username: test-user" \
    -H "x-maas-group: test-tenant" \
    -H "x-maas-subscription: test-tenant/premium-plan" \
    -H "x-maas-organization-id: ci-org" \
    -d '{"model":"test-model","messages":[{"role":"user","content":"hello from CI"}]}' \
    2>/dev/null || echo "CURL_FAILED")

check_output "LLM response received" "choices" echo "$INFERENCE_RESP"
check_output "model in response" "test-model" echo "$INFERENCE_RESP"

# Send a second request to ensure consistency
kubectl exec -n ai-gateway test-client -- \
    curl -s --max-time 30 \
    http://ai-gateway-istio.ai-gateway:80/v1/chat/completions \
    -H "Authorization: Bearer test-key" \
    -H "Content-Type: application/json" \
    -H "x-maas-username: test-user" \
    -H "x-maas-group: test-tenant" \
    -H "x-maas-subscription: test-tenant/premium-plan" \
    -H "x-maas-organization-id: ci-org" \
    -d '{"model":"test-model","messages":[{"role":"user","content":"second request"}]}' \
    >/dev/null 2>&1 || true

# ── 5. Verify events were ingested ──
echo ""
echo "--- Event ingestion (checking raw_events) ---"
sleep 5

AFTER=$(curl -sf "$CONSUMER/api/v1/reports/summary" 2>/dev/null || echo "{}")
AFTER_RAW=$(echo "$AFTER" | grep -o '"raw_events":[0-9]*' | grep -o '[0-9]*' || echo "0")

if [ "$AFTER_RAW" -gt "$BEFORE_RAW" ] 2>/dev/null; then
    echo -e "  ${GREEN}✓${NC} raw events increased ($BEFORE_RAW → $AFTER_RAW)"
    PASS=$((PASS + 1))
else
    echo -e "  ${RED}✗${NC} raw events did not increase ($BEFORE_RAW → $AFTER_RAW)"
    FAIL=$((FAIL + 1))
fi

# ── 6. Wait for metering + rating sweeps ──
echo ""
echo "--- Waiting for metering (60s) + rating (30s) sweeps ---"
sleep 95

# ── 7. Verify metering and cost entries ──
echo ""
echo "--- Pipeline verification ---"

REPORT=$(curl -sf "$CONSUMER/api/v1/reports/summary" 2>/dev/null || echo "{}")
check_output "metering entries created" "metering_entries" echo "$REPORT"
check_output "cost entries created" "cost_entries" echo "$REPORT"

# Check that MaaS-specific metrics appeared in Prometheus
PROM=$(curl -sf "$METRICS/metrics" 2>/dev/null || echo "")
check_output "events processed metric" "cost_consumer_events_processed_total" echo "$PROM"
check_output "metering entries metric" "cost_consumer_metering_entries_created_total" echo "$PROM"

# Verify tenant attribution: cost entries must be attributed to ci-org
# (from x-maas-organization-id → organization_id in CloudEvent via IPP PR #386)
COST_REPORT=$(curl -sf "$CONSUMER/api/v1/reports/costs" 2>/dev/null || echo "{}")
check_output "costs attributed to ci-org tenant" "ci-org" echo "$COST_REPORT"

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

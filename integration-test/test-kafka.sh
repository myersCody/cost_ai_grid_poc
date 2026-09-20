#!/usr/bin/env bash
set -uo pipefail

# Integration test for the Kafka metering bus.
# Tests the Kafka consumer path. The OSAC → Kafka producer path is covered by
# test-osac-kafka.sh; HTTP ingestion is intentionally not a Kafka producer.
#
# Prerequisites:
#   - Redpanda running on $BROKER (default localhost:19092)
#   - Postgres on localhost:5434
#   - rpk CLI available (brew install redpanda-data/tap/redpanda)
#
# Usage:
#   bash integration-test/test-kafka.sh

GREEN='\033[0;32m'
RED='\033[0;31m'
DIM='\033[2m'
NC='\033[0m'
PASS=0
FAIL=0

check() {
    local desc="$1" expected="$2" actual="$3"
    if [ "$expected" = "$actual" ]; then
        echo -e "  ${GREEN}✓${NC} $desc ${DIM}(got: $actual)${NC}"
        PASS=$((PASS + 1))
    else
        echo -e "  ${RED}✗${NC} $desc ${DIM}(expected: $expected, got: $actual)${NC}"
        FAIL=$((FAIL + 1))
    fi
}

check_ge() {
    local desc="$1" minimum="$2" actual="$3"
    if [ "$actual" -ge "$minimum" ] 2>/dev/null; then
        echo -e "  ${GREEN}✓${NC} $desc ${DIM}(got: $actual, min: $minimum)${NC}"
        PASS=$((PASS + 1))
    else
        echo -e "  ${RED}✗${NC} $desc ${DIM}(expected >= $minimum, got: $actual)${NC}"
        FAIL=$((FAIL + 1))
    fi
}

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
WATCHER_BIN="$REPO_DIR/inventory-watcher/inventory-watcher"
BROKER="${KAFKA_BROKER_OVERRIDE:-localhost:19092}"
DB_NAME=costdb

db_query() {
    if command -v psql > /dev/null 2>&1; then
        PGPASSWORD=pass psql -h localhost -p 5434 -U user -d "$DB_NAME" -t -A -c "$1" 2>/dev/null
    else
        docker exec cost-db psql -U user -d "$DB_NAME" -t -A -c "$1" 2>/dev/null
    fi
}

RPK="rpk --brokers $BROKER"

echo "=== Kafka Integration Test ==="
echo "  broker: $BROKER"
echo ""

# ── Preflight ──
echo "--- Preflight checks ---"

if [ ! -f "$WATCHER_BIN" ]; then
    echo "Building inventory-watcher..."
    (cd "$REPO_DIR/inventory-watcher" && go build -o inventory-watcher ./cmd/consumer/) || {
        echo "ERROR: build failed"; exit 1
    }
fi

$RPK cluster info > /dev/null 2>&1 || {
    echo "ERROR: Redpanda not reachable at $BROKER"
    exit 1
}
echo "  Redpanda: OK"

db_query "SELECT 1" > /dev/null 2>&1 || {
    echo "ERROR: Postgres not reachable"
    exit 1
}
echo "  Postgres: OK"

# Create/reset topics
for topic in osac.metering.lifecycle osac.metering.heartbeat osac.metering.inference; do
    $RPK topic delete "$topic" 2>/dev/null || true
    $RPK topic create "$topic" 2>/dev/null
done
echo "  Topics created"

# ──────────────────────────────────────────────────────────────────────
# Test 1: HTTP ingest does not publish to Kafka
# ──────────────────────────────────────────────────────────────────────
echo ""
echo "--- Test 1: HTTP ingest is not a Kafka producer ---"

KAFKA_BROKERS="$BROKER" \
KAFKA_MODE="producer" \
INVENTORY_DB_URL="postgres://user:pass@localhost:5434/$DB_NAME" \
INGEST_LISTEN_ADDR="localhost:18023" \
METRICS_PORT="19003" \
DISABLE_COMPONENTS="watcher,reconciler,metering,rating" \
"$WATCHER_BIN" > /tmp/kafka-producer-test.log 2>&1 &
PRODUCER_PID=$!

for i in $(seq 1 30); do
    curl -sf http://localhost:18023/healthz > /dev/null 2>&1 && break
    sleep 1
done
if ! curl -sf http://localhost:18023/healthz > /dev/null 2>&1; then
    echo "ERROR: producer not ready after 30s"
    tail -20 /tmp/kafka-producer-test.log
    kill $PRODUCER_PID 2>/dev/null
    exit 1
fi
echo "  Producer started (PID $PRODUCER_PID)"

# Send MaaS event
EVENT_ID="kafka-test-maas-$(date +%s)"
HTTP_STATUS=$(curl -s -o /dev/null -w '%{http_code}' \
    -X POST "http://localhost:18023/api/v1/events" \
    -H "Content-Type: application/json" \
    -d "{\"specversion\":\"1.0\",\"type\":\"inference.tokens.used\",\"source\":\"kafka-test\",\"id\":\"$EVENT_ID\",\"time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"data\":{\"tenant_id\":\"kafka-test-tenant\",\"model_id\":\"test-model\",\"model\":\"test-model\",\"prompt_tokens\":1000,\"completion_tokens\":500,\"total_tokens\":1500,\"duration_ms\":200}}")
check "HTTP ingest accepted" "204" "$HTTP_STATUS"
sleep 2

timeout 5 $RPK topic consume osac.metering.inference -o start -n 1 -f '%v\n' > /tmp/kafka-inference-out.txt 2>/dev/null || true
KAFKA_COUNT=$(grep -c "$EVENT_ID" /tmp/kafka-inference-out.txt 2>/dev/null || true)
check "HTTP event absent from Kafka inference topic" "0" "$KAFKA_COUNT"

kill $PRODUCER_PID 2>/dev/null; wait $PRODUCER_PID 2>/dev/null || true
echo "  Producer stopped"

# ──────────────────────────────────────────────────────────────────────
# Test 2: Consumer mode — Kafka → metering pipeline
# ──────────────────────────────────────────────────────────────────────
echo ""
echo "--- Test 2: Consumer mode (Kafka → metering) ---"

db_query "DELETE FROM metering_entries WHERE resource_id LIKE 'kafka-consumer-%';" > /dev/null 2>&1

CONSUMER_EVENT_ID="kafka-consumer-test-$(date +%s)"
echo "{\"specversion\":\"1.0\",\"type\":\"osac.model.lifecycle\",\"source\":\"kafka-consumer-test\",\"id\":\"$CONSUMER_EVENT_ID\",\"time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"data\":{\"tenant_id\":\"kafka-consumer-tenant\",\"model_id\":\"kafka-consumer-model\",\"model_name\":\"test-model\",\"state\":\"MODEL_STATE_RUNNING\",\"tokens_in\":5000,\"tokens_out\":2000,\"requests\":10,\"duration_seconds\":30}}" | \
    $RPK topic produce osac.metering.inference 2>/dev/null
echo "  Test event produced to Kafka"

KAFKA_BROKERS="$BROKER" \
KAFKA_MODE="consumer" \
KAFKA_CONSUMER_GROUP="test-consumer-$(date +%s)" \
INVENTORY_DB_URL="postgres://user:pass@localhost:5434/$DB_NAME" \
INGEST_LISTEN_ADDR="localhost:18024" \
METRICS_PORT="19004" \
DISABLE_COMPONENTS="watcher,reconciler" \
"$WATCHER_BIN" > /tmp/kafka-consumer-test.log 2>&1 &
CONSUMER_PID=$!

for i in $(seq 1 30); do
    curl -sf http://localhost:18024/healthz > /dev/null 2>&1 && break
    sleep 1
done
sleep 5

if ! kill -0 $CONSUMER_PID 2>/dev/null; then
    echo "ERROR: consumer process died"
    tail -20 /tmp/kafka-consumer-test.log
    exit 1
fi
echo "  Consumer started (PID $CONSUMER_PID)"

RAW_COUNT=$(db_query "SELECT count(*) FROM raw_events WHERE event_id = '$CONSUMER_EVENT_ID';")
check "Raw event stored from Kafka" "1" "$RAW_COUNT"

ME_COUNT=$(db_query "SELECT count(*) FROM metering_entries WHERE resource_id = 'kafka-consumer-model';")
check_ge "Metering entries from Kafka event" 3 "$ME_COUNT"

kill $CONSUMER_PID 2>/dev/null; wait $CONSUMER_PID 2>/dev/null || true
echo "  Consumer stopped"

# ── Summary ──
echo ""
echo "=========================================="
TOTAL=$((PASS + FAIL))
echo "  Results: $PASS/$TOTAL passed"
if [ "$FAIL" -gt 0 ]; then
    echo -e "  ${RED}$FAIL FAILED${NC}"
    echo "  API producer-path log: /tmp/kafka-producer-test.log"
    echo "  Consumer log: /tmp/kafka-consumer-test.log"
    exit 1
else
    echo -e "  ${GREEN}ALL PASSED${NC}"
fi

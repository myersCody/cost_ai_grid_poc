#!/bin/bash
# Deploy Splunk PoC + enable forwarder on cost-event-consumer
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NS=cost-mgmt

echo "=== Deploying Splunk ==="
kubectl apply -f "$SCRIPT_DIR/splunk-poc.yaml"
echo "Waiting for Splunk to be ready (may take 60-90s)..."
kubectl wait --for=condition=available deployment/splunk -n "$NS" --timeout=180s

echo ""
echo "=== Patching cost-event-consumer with Splunk env vars ==="
kubectl set env deployment/cost-event-consumer -n "$NS" \
  SPLUNK_HEC_URL="https://splunk.cost-mgmt.svc:8088/services/collector/event" \
  SPLUNK_HEC_TOKEN="cost-audit-token" \
  SPLUNK_TLS_INSECURE="true" \
  SPLUNK_INTERVAL="5s"

kubectl rollout status deployment/cost-event-consumer -n "$NS" --timeout=60s

echo ""
echo "=== Splunk deployed ==="
echo "Web UI: kubectl port-forward svc/splunk -n $NS 8000:8000"
echo "        https://localhost:8000 (admin / changeme123)"
echo "Search: index=main sourcetype=_json"

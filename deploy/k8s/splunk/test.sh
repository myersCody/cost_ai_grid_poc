#!/bin/bash
# Verify Splunk audit forwarding is working
set -euo pipefail

NS=cost-mgmt

echo "=== Consumer health ==="
kubectl exec -n "$NS" deploy/cost-event-consumer -- curl -s localhost:8020/healthz
echo ""

echo ""
echo "=== Splunk HEC health ==="
kubectl exec -n "$NS" deploy/cost-event-consumer -- \
  curl -sk "https://splunk.cost-mgmt.svc:8088/services/collector/health" \
  -H "Authorization: Splunk cost-audit-token"
echo ""

echo ""
echo "=== Baseline: raw events count ==="
BEFORE=$(kubectl exec -n "$NS" deploy/cost-event-consumer -- curl -s localhost:8020/api/v1/reports/summary | python3 -c "import json,sys; print(json.load(sys.stdin)['raw_events'])")
echo "raw_events: $BEFORE"

echo ""
echo "=== Sending test CloudEvent ==="
kubectl exec -n "$NS" deploy/cost-event-consumer -- curl -s -w "HTTP %{http_code}\n" \
  -X POST localhost:8020/api/v1/events \
  -H "Content-Type: application/json" \
  -d '{
    "specversion": "1.0",
    "type": "osac.compute_instance.lifecycle",
    "source": "splunk-test",
    "id": "splunk-test-'"$(date +%s)"'",
    "time": "'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'",
    "subject": "tenant-test",
    "data": {
      "duration_seconds": 60,
      "tenant_id": "tenant-test",
      "instance_id": "vm-splunk-test",
      "state": "COMPUTE_INSTANCE_STATE_RUNNING",
      "cores": 2,
      "memory_gib": 4
    }
  }'

echo ""
echo "=== Waiting for Splunk forward sweep (15s) ==="
sleep 15

echo ""
echo "=== Checking Splunk for events ==="
kubectl exec -n "$NS" deploy/splunk -- curl -sk \
  "https://localhost:8089/services/search/jobs/export" \
  -u admin:changeme123 \
  -d 'search=search index=main sourcetype=_json | head 5' \
  -d output_mode=json 2>/dev/null | python3 -c "
import json, sys
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    try:
        d = json.loads(line)
        if 'result' in d:
            raw = d['result'].get('_raw', '')
            try:
                evt = json.loads(raw)
                print('  tenant:', evt.get('tenant_id', 'N/A'))
                print('  type:', evt.get('event_type', 'N/A'))
                print('  resource:', evt.get('resource_id', 'N/A'))
            except:
                print('  raw:', raw[:100])
    except:
        pass
" 2>/dev/null || echo "(search may need more time — try the Splunk web UI)"

echo ""
echo "=== Prometheus metrics ==="
kubectl exec -n "$NS" deploy/cost-event-consumer -- curl -s localhost:9000/metrics 2>/dev/null | grep splunk || echo "(no splunk metrics yet)"

echo ""
echo "=== Done ==="
echo "Splunk Web UI: kubectl port-forward svc/splunk -n $NS 8000:8000"
echo "               https://localhost:8000 → search: index=main sourcetype=_json"

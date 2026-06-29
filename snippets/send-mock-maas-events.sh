#!/bin/bash
# Send mock MaaS (Model-as-a-Service) events to the inventory-watcher.
#
# Since OSAC doesn't emit model events yet, this script inserts mock events
# directly into the cost database, simulating what the metering pipeline
# would receive from OSAC once model support is added.
#
# Usage:
#   ./snippets/send-mock-maas-events.sh              # send default events
#   ./snippets/send-mock-maas-events.sh --count 10   # send 10 events

set -uo pipefail

DB_CONTAINER=cost-db
DB_NAME=costdb
COUNT=${2:-3}
TENANT="tenant-acme"

db_exec() {
  docker exec "$DB_CONTAINER" psql -U user -d "$DB_NAME" -c "$1" 2>/dev/null
}

echo "=== Inserting mock model into inventory ==="

MODEL_ID="mock-model-$(date +%s)"
db_exec "INSERT INTO inventory_model
  (model_id, name, model_name, tenant, project, template, state, labels, created_at, last_event_id)
  VALUES ('$MODEL_ID', 'llama-3-8b-deploy', 'llama-3-8b', '$TENANT', 'ai-project', 'osac.templates.maas_small', 'MODEL_STATE_RUNNING', '{\"model_family\": \"llama\"}', NOW(), 'mock')
  ON CONFLICT (model_id) DO NOTHING;" > /dev/null

echo "  Model: $MODEL_ID (llama-3-8b)"

echo ""
echo "=== Sending $COUNT mock MaaS events ==="

for i in $(seq 1 "$COUNT"); do
  EVENT_ID="mock-maas-event-$(date +%s)-$i"
  TOKENS_IN=$((RANDOM % 50000 + 5000))
  TOKENS_OUT=$((RANDOM % 20000 + 2000))
  REQUESTS=$((RANDOM % 100 + 10))
  EVENT_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  # Insert raw event
  db_exec "INSERT INTO raw_events
    (event_id, event_type, event_source, event_time, tenant_id, resource_type, resource_id, data)
    VALUES ('$EVENT_ID', 'osac.model.lifecycle', 'mock-maas-generator', '$EVENT_TIME', '$TENANT', 'Model', '$MODEL_ID',
      '{\"tenant_id\": \"$TENANT\", \"model_id\": \"$MODEL_ID\", \"model_name\": \"llama-3-8b\", \"state\": \"MODEL_STATE_RUNNING\", \"tokens_in\": $TOKENS_IN, \"tokens_out\": $TOKENS_OUT, \"requests\": $REQUESTS, \"duration_seconds\": 60}')
    ON CONFLICT (event_id) DO NOTHING;" > /dev/null

  # Insert metering entries (what the pipeline would produce)
  PERIOD_END="$EVENT_TIME"
  db_exec "INSERT INTO metering_entries (resource_type, resource_id, tenant_id, meter_name, value, unit, period_start, period_end)
    VALUES
      ('model', '$MODEL_ID', '$TENANT', 'maas_tokens_in', $TOKENS_IN, 'tokens', '$PERIOD_END'::timestamptz - interval '60 seconds', '$PERIOD_END'),
      ('model', '$MODEL_ID', '$TENANT', 'maas_tokens_out', $TOKENS_OUT, 'tokens', '$PERIOD_END'::timestamptz - interval '60 seconds', '$PERIOD_END'),
      ('model', '$MODEL_ID', '$TENANT', 'maas_requests', $REQUESTS, 'requests', '$PERIOD_END'::timestamptz - interval '60 seconds', '$PERIOD_END')
    ;" > /dev/null

  echo "  Event $i: tokens_in=$TOKENS_IN tokens_out=$TOKENS_OUT requests=$REQUESTS"
  sleep 1
done

echo ""
echo "=== Results ==="
echo ""
echo "Model inventory:"
db_exec "SELECT model_id, model_name, tenant, state FROM inventory_model;"

echo ""
echo "MaaS metering entries:"
db_exec "SELECT meter_name, sum(value) as total, unit FROM metering_entries WHERE resource_type = 'model' GROUP BY meter_name, unit ORDER BY meter_name;"

echo ""
echo "Raw MaaS events:"
db_exec "SELECT count(*) as count FROM raw_events WHERE resource_type = 'Model';"

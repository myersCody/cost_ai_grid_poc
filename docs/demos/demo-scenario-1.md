# Demo Scenario 1: Full Pipeline — Inventory, Metering, Cost, Quotas

> **Demo recording:** [Google Drive video](https://drive.google.com/file/d/1gagbe1-VhpyYVi4K8x1IA1mjLSEZZli0/view?usp=drive_link)

## Purpose

Demonstrate the complete cost management pipeline: OSAC event ingestion,
inventory tracking, metering, rating (dollar costs), and quota status.

## Prerequisites

```bash
brew install watch asciinema   # optional: asciinema for recording

# Build
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/
go build -o maas-simulator ./cmd/maas-simulator/

# Set up environment (run in each terminal/pane)
source snippets/env.sh
```

OSAC and databases must be running per `docs/local-dev-setup.md`.
Token must be fresh in `/tmp/osac_token.txt`.

## Environment Variables

All demo commands assume `snippets/env.sh` has been sourced:

```bash
export OSAC_BASE_URL=http://localhost:8011
export OSAC_TOKEN=$(cat /tmp/osac_token.txt)
export INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb
export INGEST_LISTEN_ADDR=localhost:8020
export BASE="$OSAC_BASE_URL"
export TOKEN="$OSAC_TOKEN"
```

## tmux Layout

```
┌──────────────────────┬──────────────────────┐
│  Watcher logs        │  Live DB view        │
│  (top-left)          │  (right)             │
├──────────────────────┤                      │
│  Commands            │                      │
│  (bottom-left)       │                      │
└──────────────────────┴──────────────────────┘
```

Setup:
```bash
tmux new-session -s demo
# Ctrl-b % (vertical split)
# Click left pane, Ctrl-b " (horizontal split)
# Source env.sh in each pane: source snippets/env.sh
```

**Right pane — live DB view:**
```bash
watch -n 2 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT name, cores, memory_gib, state FROM inventory_compute_instance \
   WHERE deleted_at IS NULL ORDER BY name;" 2>/dev/null || echo "No tables yet"'
```

---

## Demo Flow

### Act 1: Show the infrastructure

**Narrate:** "We have OSAC running with compute instances, and an empty cost
database."

**Bottom-left:**
```bash
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" \
  --filter name=osac-db --filter name=cost-db

curl -s $BASE/api/fulfillment/v1/compute_instances \
  -H "Authorization: Bearer $TOKEN" | \
  jq '[.items[] | {name: .metadata.name, cores: .spec.cores, mem: .spec.memory_gib}]'

docker exec cost-db psql -U user -d costdb -c "\dt"
```

---

### Act 2: Start the inventory-watcher

**Narrate:** "Starting the consumer. It seeds rates and quotas, reconciles
all OSAC resources, and opens the Watch stream for real-time events."

**Top-left:**
```bash
source snippets/env.sh && cd inventory-watcher && ./inventory-watcher
```

**Point out in the logs:**
- `seeded default rates count=9`
- `seeded default quotas count=24`
- `reconciled compute instances osac_count=N created=N`
- `watch stream connected`

**Right pane** shows all VMs appearing instantly.

---

### Act 3: Real-time event ingestion

**Narrate:** "I'll create a new VM in OSAC — watch the right pane."

**Bottom-left:**
```bash
SUBNET_ID=$(curl -s $BASE/api/fulfillment/v1/subnets \
  -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')
TPL_ID=$(curl -s $BASE/api/private/v1/compute_instance_templates \
  -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')

curl -s -X POST $BASE/api/private/v1/compute_instances \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d "{
    \"metadata\": {\"name\": \"demo-vm\"},
    \"spec\": {
      \"template\": \"$TPL_ID\",
      \"cores\": 4, \"memory_gib\": 16,
      \"network_attachments\": [{\"subnet\": \"$SUBNET_ID\"}],
      \"boot_disk\": {\"size_gib\": 100},
      \"image\": {\"source_type\": \"registry\", \"source_ref\": \"quay.io/fedora/fedora:latest\"},
      \"run_strategy\": \"Always\"
    },
    \"status\": {\"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\"}
  }" | jq '{id: .id, name: .metadata.name}'
```

**Point out:**
- Top-left: `received event ... CREATED ... ComputeInstance`, `stored raw event`
- Right pane: `demo-vm` appears within 1-2 seconds

---

### Act 4: Raw event log

**Narrate:** "Every event is stored immutably — an audit trail."

**Bottom-left:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT event_id, event_type, resource_type, resource_id
   FROM raw_events ORDER BY received_at DESC LIMIT 5;"
```

---

### Act 5: Metering sweep

**Narrate:** "Now we wait 60 seconds for the metering sweep. It calculates
provisioned resources times time for every running VM."

**Switch right pane** (`Ctrl-C`, then):
```bash
watch -n 5 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT meter_name, resource_id, round(value::numeric, 1) as value, unit
   FROM metering_entries ORDER BY resource_id, meter_name LIMIT 20;"'
```

**Wait ~60 seconds.** When entries appear:

**Narrate:** "Each running VM now has three meters. A 4-core VM running for
60 seconds produces 240 cpu_core_seconds."

---

### Act 6: Cost in dollars

**Narrate:** "The rating sweep runs every 30 seconds, converting metering
entries to dollar costs."

**Switch right pane** (`Ctrl-C`, then):
```bash
watch -n 5 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT ci.name, ce.meter_name, round(ce.cost_amount::numeric, 6) as cost, ce.currency
   FROM cost_entries ce
   JOIN inventory_compute_instance ci ON ce.resource_id = ci.instance_id
   ORDER BY ci.name, ce.meter_name LIMIT 20;"'
```

**Wait ~30 seconds.** Then show the rates:

**Bottom-left:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT meter_name, price_per_unit, currency
   FROM rates WHERE resource_type = 'compute_instance';"
```

---

### Act 7: Quota status API

**Narrate:** "OSAC can check in real-time: is this tenant within quota?"

**Bottom-left:**
```bash
curl -s http://localhost:8020/api/v1/quotas/shared | \
  jq '.quotas[] | select(.consumed > 0)'
```

---

### Act 8: OpenMeter-compatible ingest

**Narrate:** "Our endpoint accepts the same CloudEvents format the OSAC
metering collector sends to OpenMeter. Switching to us is a URL change."

**Bottom-left:**
```bash
curl -s -X POST http://localhost:8020/api/v1/events \
  -H "Content-Type: application/cloudevents+json" \
  -d "{
    \"specversion\": \"1.0\",
    \"type\": \"osac.compute_instance.lifecycle\",
    \"source\": \"osac.metering.collector\",
    \"id\": \"demo-heartbeat-$(date +%s)\",
    \"time\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
    \"subject\": \"tenant-acme\",
    \"data\": {
      \"duration_seconds\": 60,
      \"cpu_core_seconds\": 480,
      \"memory_gib_seconds\": 1920,
      \"tenant_id\": \"tenant-acme\",
      \"instance_id\": \"demo-external-vm\",
      \"template\": \"osac.templates.ocp_virt_vm\",
      \"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\",
      \"cores\": 8,
      \"memory_gib\": 32
    }
  }" | jq .
```

**Point out:**
- Response: `{"status":"accepted"}`
- Top-left: `ingested VM heartbeat instance=demo-external-vm`

**Show metering entries created directly from the event:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT meter_name, round(value::numeric, 0) as value, unit
   FROM metering_entries WHERE resource_id = 'demo-external-vm';"
```

---

### Act 9: DELETE and final metering

**Narrate:** "Deleting a VM produces final metering entries covering the gap
since the last sweep — no usage lost."

**Bottom-left:**
```bash
DEMO_ID=$(docker exec cost-db psql -U user -d costdb -t -A -c \
  "SELECT instance_id FROM inventory_compute_instance WHERE name = 'demo-vm';")
curl -s -X DELETE "$BASE/api/fulfillment/v1/compute_instances/$DEMO_ID" \
  -H "Authorization: Bearer $TOKEN"
```

**Point out:**
- Top-left: `final metering for deleted instance`

**Verify:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT name, deleted_at IS NOT NULL as deleted
   FROM inventory_compute_instance WHERE name = 'demo-vm';"
```

---

### Act 10: Test suite

**Narrate:** "We have automated tests covering the full pipeline."

**Bottom-left:**
```bash
cd /Users/mpovolny/Projects/cost_ai_grid_poc
SKIP_METERING=1 bash snippets/test-inventory-watcher.sh
```

---

### Act 11: MaaS traffic (optional)

**Narrate:** "The same pipeline handles consumption-based billing —
tokens and requests instead of cores and seconds."

**Bottom-left:**
```bash
cd /Users/mpovolny/Projects/cost_ai_grid_poc/inventory-watcher
./maas-simulator -target http://localhost:8020 -count 50 -rate 20
```

**Show MaaS costs:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT meter_name, round(sum(cost_amount)::numeric, 4) as total_cost, currency
   FROM cost_entries WHERE resource_type = 'model'
   GROUP BY meter_name, currency ORDER BY meter_name;"
```

---

## Talking Points

1. **Full pipeline in one binary** — events → inventory → metering → cost → quotas
2. **Sub-second ingestion** — VM appears in cost DB within 1-2 seconds
3. **Capacity-based billing** — provisioned resources × time × rate = cost
4. **Automatic rating** — metering entries converted to dollars every 30s
5. **Quota API** — real-time consumption vs limits with threshold checks
6. **OpenMeter-compatible** — same CloudEvents format, just a URL change
7. **No Kafka** — Watch stream + reconciler, same as Kubernetes controllers
8. **No data loss** — final metering on DELETE, immutable raw event log
9. **Billable state filtering** — only RUNNING VMs produce cost

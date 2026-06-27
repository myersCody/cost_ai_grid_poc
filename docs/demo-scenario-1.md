# Demo Scenario 1: Full Pipeline — Inventory, Metering, Cost, Quotas

## Purpose

Demonstrate the complete cost management pipeline: OSAC event ingestion,
inventory tracking, metering, rating (dollar costs), and quota status.
Suitable for a live demo or a recorded walkthrough.

## Prerequisites

```bash
brew install watch        # live-refresh terminal commands
brew install asciinema    # terminal recording (optional)
```

Ensure OSAC and databases are running per `docs/local-dev-setup.md`.
Build the binaries:
```bash
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/
go build -o maas-simulator ./cmd/maas-simulator/
```

## Demo Flow

### Act 1: Show the infrastructure

**Goal:** Establish that OSAC and the cost database are running.

```bash
# Running containers
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" \
  --filter name=osac-db --filter name=cost-db

# OSAC has compute instances
curl -s http://localhost:8011/api/fulfillment/v1/compute_instances \
  -H "Authorization: Bearer $(cat /tmp/osac_token.txt)" | jq '.size'

# Cost database is empty (clean slate)
docker exec cost-db psql -U user -d costdb -c "\dt"
```

---

### Act 2: Start the inventory-watcher

**Goal:** Show reconciliation, rate seeding, and quota setup on startup.

**Terminal 1 — watcher logs:**
```bash
cd inventory-watcher
OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN=$(cat /tmp/osac_token.txt) \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
RECONCILE_INTERVAL=5m \
SUMMARIZE_INTERVAL=5m \
INGEST_LISTEN_ADDR=localhost:8020 \
./inventory-watcher
```

Point out the log lines:
```
msg="database schema ready"
msg="seeded default rates" count=8
msg="seeded default quotas" count=24
msg="reconciled compute instances" osac_count=N created=N
msg="reconciled instance types" count=3
msg="watch stream connected"
msg="ingest endpoint listening" addr=localhost:8020
```

**Terminal 2 — live database view:**
```bash
watch -n 2 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT name, cores, memory_gib, state FROM inventory_compute_instance WHERE deleted_at IS NULL ORDER BY name;"'
```

The table populates immediately — all OSAC instances appear.

---

### Act 3: Real-time event ingestion

**Goal:** Create a new VM in OSAC and watch it appear instantly.

**Terminal 3:**
```bash
TOKEN=$(cat /tmp/osac_token.txt)
SUBNET_ID=$(curl -s http://localhost:8011/api/fulfillment/v1/subnets \
  -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')
TPL_ID=$(curl -s http://localhost:8011/api/private/v1/compute_instance_templates \
  -H "Authorization: Bearer $TOKEN" | jq -r '.items[0].id')

curl -s -X POST http://localhost:8011/api/private/v1/compute_instances \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d "{
    \"metadata\": {\"name\": \"demo-vm\", \"labels\": {\"demo\": \"live\"}},
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

**What to show:**
- Terminal 1: `received event ... type=EVENT_TYPE_OBJECT_CREATED resource=ComputeInstance`
- Terminal 2: `demo-vm` appears in the table within 1-2 seconds

---

### Act 4: Raw event log

**Goal:** Show immutable audit trail.

```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT event_id, event_type, resource_type, resource_id, received_at
   FROM raw_events ORDER BY received_at DESC LIMIT 5;"
```

---

### Act 5: Metering sweep

**Goal:** Show the 60-second metering sweep producing usage records.

**Terminal 2** — switch to metering view:
```bash
watch -n 5 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT meter_name, resource_id, round(value::numeric, 1) as value, unit
   FROM metering_entries ORDER BY resource_id, meter_name LIMIT 20;"'
```

**Wait ~60 seconds.** The sweep fires.

**Explain the math** (e.g., demo-vm with 4 cores, 16 GiB):
```
vm_uptime_seconds     = ~60       (one sweep interval)
vm_cpu_core_seconds   = ~240      (4 cores × 60s)
vm_memory_gib_seconds = ~960      (16 GiB × 60s)
```

---

### Act 6: Cost in dollars

**Goal:** Show that metering entries are automatically rated.

**Wait ~30 seconds** after metering entries appear (rating sweep is every 30s).

**Terminal 2** — switch to cost view:
```bash
watch -n 5 'docker exec cost-db psql -U user -d costdb -c \
  "SELECT ci.name, ce.meter_name, round(ce.cost_amount::numeric, 6) as cost, ce.currency
   FROM cost_entries ce
   JOIN inventory_compute_instance ci ON ce.resource_id = ci.instance_id
   ORDER BY ci.name, ce.meter_name LIMIT 20;"'
```

**Show the rates being applied:**
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT resource_type, meter_name, price_per_unit, currency FROM rates
   WHERE resource_type = 'compute_instance' ORDER BY meter_name;"
```

**Explain:** "worker-1 used 240 cpu_core_seconds × $0.0000014/core-second = $0.000333"

---

### Act 7: Quota status API

**Goal:** Show real-time quota checking.

```bash
curl -s http://localhost:8020/api/v1/quotas/shared | jq '.quotas[] | select(.consumed > 0)'
```

Shows which meters have consumption, what percentage of quota is used,
and whether any thresholds (50/70/90/100%) are crossed.

---

### Act 8: OpenMeter-compatible ingest

**Goal:** Show that the OSAC metering collector can send events directly to us.

**Terminal 3** — send a VM heartbeat event in the exact format the OSAC
collector produces:
```bash
curl -s -X POST http://localhost:8020/api/v1/events \
  -H "Content-Type: application/cloudevents+json" \
  -d '{
    "specversion": "1.0",
    "type": "osac.compute_instance.lifecycle",
    "source": "osac.metering.collector",
    "id": "demo-heartbeat-001",
    "time": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",
    "subject": "tenant-acme",
    "data": {
      "duration_seconds": 60,
      "cpu_core_seconds": 480,
      "memory_gib_seconds": 1920,
      "tenant_id": "tenant-acme",
      "instance_id": "demo-external-vm",
      "template": "osac.templates.ocp_virt_vm",
      "state": "COMPUTE_INSTANCE_STATE_RUNNING",
      "cores": 8,
      "memory_gib": 32
    }
  }' | jq .
```

**What to show:**
- Response: `{"status":"accepted"}`
- Terminal 2: `demo-external-vm` appears in inventory and metering entries
- Terminal 1: `ingested VM heartbeat instance=demo-external-vm`

**Explain:** "This is the exact same format the OSAC metering collector sends
to OpenMeter today. We accept it with no translation. Switching the collector
target from OpenMeter to us is a URL change."

---

### Act 9: DELETE and final metering

**Goal:** Show that deleting a VM produces final metering entries.

```bash
DEMO_ID=$(docker exec cost-db psql -U user -d costdb -t -A -c \
  "SELECT instance_id FROM inventory_compute_instance WHERE name = 'demo-vm';")
curl -s -X DELETE "http://localhost:8011/api/fulfillment/v1/compute_instances/$DEMO_ID" \
  -H "Authorization: Bearer $(cat /tmp/osac_token.txt)"
```

**What to show:**
- Terminal 1: `final metering for deleted instance`
- Verify: `deleted_at` is set on the inventory record

---

### Act 10: Run the test suite

**Goal:** Show automated test coverage.

```bash
SKIP_METERING=1 bash snippets/test-inventory-watcher.sh
```

Colored output, all tests passing. For the full suite (including metering
and non-billable filtering, ~90s):
```bash
bash snippets/test-inventory-watcher.sh
```

---

### Act 11: MaaS traffic (optional — bridges to demo scenario 2)

**Goal:** Show the same pipeline handles consumption-based billing.

```bash
cd inventory-watcher
./maas-simulator -target http://localhost:8020 -count 50 -rate 20
```

Then show MaaS costs:
```bash
docker exec cost-db psql -U user -d costdb -c \
  "SELECT meter_name, round(sum(cost_amount)::numeric, 4) as total_cost, currency
   FROM cost_entries WHERE resource_type = 'model'
   GROUP BY meter_name, currency ORDER BY meter_name;"
```

---

## Recording the Demo

### tmux layout (recommended for recording)

```bash
tmux new-session -s demo -d

# Top-left: watcher logs
tmux send-keys -t demo 'cd /path/to/cost_ai_grid_poc/inventory-watcher && \
  OSAC_BASE_URL=http://localhost:8011 \
  OSAC_TOKEN=$(cat /tmp/osac_token.txt) \
  INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
  INGEST_LISTEN_ADDR=localhost:8020 \
  ./inventory-watcher' Enter

# Top-right: live DB view
tmux split-window -h -t demo
tmux send-keys -t demo 'watch -n 2 "docker exec cost-db psql -U user -d costdb -c \
  \"SELECT name, cores, memory_gib, state FROM inventory_compute_instance \
  WHERE deleted_at IS NULL ORDER BY name;\""' Enter

# Bottom: command input
tmux split-window -v -t demo
tmux send-keys -t demo 'echo "Ready — use the commands from the demo script"' Enter

tmux attach -t demo
```

Layout:
```
┌──────────────────────┬──────────────────────┐
│  Watcher logs        │  Live DB view        │
│  (streaming)         │  (watch -n 2)        │
│                      │                      │
├──────────────────────┴──────────────────────┤
│  Command input                              │
│  (create/delete/curl)                       │
└─────────────────────────────────────────────┘
```

### asciinema recording

```bash
asciinema rec demo-req1.cast
# ... run the demo ...
# Ctrl-D to stop
asciinema play demo-req1.cast
```

---

## Talking Points

1. **Full pipeline in one binary** — events → inventory → metering → rating
   → cost entries → quota API. No external dependencies beyond OSAC and
   PostgreSQL.

2. **Sub-second ingestion** — creating a VM in OSAC appears in the cost
   database within 1-2 seconds via the Watch stream.

3. **Capacity-based billing** — you pay for provisioned resources × time.
   4 cores × 60 seconds = 240 cpu_core_seconds × rate = $0.000333.

4. **Automatic rating** — metering entries are converted to dollar costs
   every 30 seconds. Default rates seeded on startup; tenant-specific
   overrides and tiered pricing supported.

5. **Quota enforcement ready** — `GET /api/v1/quotas/{tenant_id}` returns
   real-time consumption vs limits with threshold flags. OSAC can call
   this before allowing resource creation.

6. **OpenMeter-compatible** — the ingest endpoint accepts the same
   CloudEvents format the OSAC collector already produces. Switching
   from OpenMeter to us is a URL change.

7. **No Kafka needed** — Watch stream + reconciler provides the same
   delivery guarantees. Same pattern as Kubernetes controllers.
   See [ADR-002](decisions/002-arguments-against-kafka.md).

8. **No data loss** — raw events stored immutably; final metering on
   DELETE covers the gap since the last sweep.

9. **Billable state filtering** — only RUNNING VMs and READY/PROGRESSING
   clusters are metered. STOPPED VMs are in inventory but produce no cost.

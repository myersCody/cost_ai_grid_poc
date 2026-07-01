# Demo Scenario 3: Live Dashboard — Real-Time Cost Pipeline

> **Demo recording:** [Google Drive video](https://drive.google.com/file/d/1CW9TgRR0yHV5AEC4g1Y3AMex3s9fWJzZ/view?usp=drive_link)

## Purpose

Demonstrate the full cost management pipeline in real-time using the live
dashboard. Show inventory sync, metering sweeps, rating with per-tenant
pricing, MaaS consumption events, and CSV export — all visible as
counters and tables update in the browser.

## Prerequisites

```bash
# Ensure OSAC, cost-db, and inventory-watcher are running
docker ps --format "table {{.Names}}\t{{.Status}}" \
  --filter name=osac-db --filter name=cost-db

# Build binaries
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/
go build -o maas-simulator ./cmd/maas-simulator/

# Generate fresh OSAC token
FULFILLMENT_SERVICE_DIR=/Users/mpovolny/Projects/fulfillment-service \
  python3 scripts/gen_token.py > /tmp/osac_token.txt

# Start the consumer with ingest endpoint
OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN=$(cat /tmp/osac_token.txt) \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
INGEST_LISTEN_ADDR=localhost:8020 \
./inventory-watcher
```

## Setup

**Terminal 1:** Open the dashboard in a browser:
```bash
open docs/demo-dashboard.html
```
Set refresh interval to **1s** in the dropdown.

**Terminal 2:** This is where you'll run the demo commands.

For a clean start, optionally reset the database:
```bash
docker exec cost-db psql -U user -d costdb -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"
# Then restart inventory-watcher (it recreates tables + seeds rates on startup)
```

## Demo Flow

### Act 1: The Pipeline at Rest

**What to show:** The dashboard with the summary strip. Explain each counter.

**Narration:**
> "This dashboard polls our cost management API every second. The top strip
> shows the pipeline health — raw events are the immutable audit log, metering
> entries are usage records, cost entries are priced usage. The three boxes
> below show total cost split into Infrastructure and Supplementary, matching
> Koku's cost classification."

Point out: Metering Entries and Cost Entries should be equal (or very close),
meaning the rating sweep has fully caught up.

### Act 2: Create a VM in OSAC — Real-Time Inventory Sync

**What to show:** Live VMs counter increments within 1-2 seconds.

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
    \"metadata\": {\"name\": \"demo-live-vm\", \"labels\": {\"demo\": \"scenario3\"}},
    \"spec\": {
      \"template\": \"$TPL_ID\",
      \"cores\": 8, \"memory_gib\": 32,
      \"network_attachments\": [{\"subnet\": \"$SUBNET_ID\"}],
      \"boot_disk\": {\"size_gib\": 100},
      \"image\": {\"source_type\": \"registry\", \"source_ref\": \"quay.io/fedora/fedora:latest\"},
      \"run_strategy\": \"Always\"
    },
    \"status\": {\"state\": \"COMPUTE_INSTANCE_STATE_RUNNING\"}
  }" | jq '{id: .id, name: .metadata.name}'
```

**Narration:**
> "I've created a VM in OSAC. Within a second, the Watch stream delivers
> the event — Raw Events increments and Live VMs goes up. The VM is now
> in our inventory and will be metered on the next 60-second sweep."

### Act 3: Metering Sweep — Watch It Happen

**What to show:** Metering Entries jumps after ~60 seconds.

**Narration:**
> "Every 60 seconds, the metering sweep runs. It reads all billable resources
> — VMs in RUNNING state — and generates three usage records per VM:
> uptime seconds, CPU core-seconds, and memory GiB-seconds.
>
> Watch the Metering Entries counter... there it goes. It jumped by N×3
> — three meters for each running VM."

### Act 4: Rating Sweep — From Usage to Cost

**What to show:** Cost Entries catches up to Metering Entries. Total Cost
boxes update.

**Narration:**
> "The rating sweep runs every 30 seconds. It picks up unrated metering
> entries, looks up the applicable rate — checking for tenant-specific
> overrides first, then falling back to the global default — and multiplies
> value × rate to produce a cost entry.
>
> Watch the Cost Entries counter catch up... now they're equal.
> The cost boxes show the total, split into Infrastructure (base uptime cost)
> and Supplementary (CPU and memory usage costs)."

### Act 5: Per-Tenant Rate Override

**What to show:** Insert a premium rate for tenant-globex, explain what
will happen next.

MaaS simulator tenants: `tenant-acme`, `tenant-globex`, `tenant-initech`
(plus `shared` from OSAC VMs).

```bash
docker exec cost-db psql -U user -d costdb -c "
INSERT INTO rates (tenant_id, resource_type, meter_name, koku_metric, cost_type,
                   price_per_unit, currency, description, effective_from)
VALUES ('tenant-globex', 'model', 'maas_tokens_in', '', 'Supplementary',
        1.50 / 1000000, 'USD', 'Premium token rate for Globex (3x)', NOW())
ON CONFLICT DO NOTHING;
"
```

**Narration:**
> "I've just set a tenant-specific rate for Globex — they'll pay \$1.50 per
> million tokens instead of the default \$0.50. Same usage, 3× the cost.
> We're about to fire MaaS events across three tenants and you'll see the
> difference in the dashboard."

Optionally show the rate in the DB:
```bash
docker exec cost-db psql -U user -d costdb -c "
SELECT COALESCE(tenant_id, '(global)') as scope, meter_name,
       round(price_per_unit::numeric * 1000000, 2) as per_million_tokens, description
FROM rates WHERE meter_name = 'maas_tokens_in'
ORDER BY tenant_id NULLS FIRST;
"
```

### Act 6: MaaS Events Burst — Multi-Tenant Consumption

**What to show:** All counters climbing rapidly. The "By Tenant" table
showing different costs for each tenant.

```bash
cd inventory-watcher
./maas-simulator -target http://localhost:8020 -count 200 -rate 10
```

**Narration:**
> "The MaaS simulator sends CloudEvents representing model inference
> workloads — tokens consumed, requests served. It distributes events
> randomly across three tenants: acme, globex, and initech.
>
> Watch the dashboard — Raw Events climbing at 10/s, Metering Entries
> following with 3 entries per event (tokens in, tokens out, requests).
>
> Now look at the table — all three tenants have roughly similar token
> volume, but tenant-globex has a higher total cost. That's the rate
> override in action. Same usage, different price."

Switch between tabs to show different views:
- **By Tenant** — cost comparison, Globex stands out
- **By Meter** — maas_tokens_in, maas_tokens_out, maas_requests
- **By Resource Type** — model vs compute_instance
- **By Resource** — individual model deployment costs

Use the **Tenant dropdown** to drill into tenant-globex alone, then switch
back to "All tenants" to compare.

### Act 7: Delete VM — Final Metering

**What to show:** Live VMs decrements, final metering entries appear
immediately (no 60-second wait).

```bash
DEMO_ID=$(docker exec cost-db psql -U user -d costdb -t -A -c \
  "SELECT instance_id FROM inventory_compute_instance WHERE name = 'demo-live-vm';")

curl -s -X DELETE "http://localhost:8011/api/fulfillment/v1/compute_instances/$DEMO_ID" \
  -H "Authorization: Bearer $(cat /tmp/osac_token.txt)"
```

**Narration:**
> "When a VM is deleted, the Watch stream delivers a DELETE event. Our
> consumer immediately generates final metering entries covering the time
> since the last sweep — no usage gap. The VM disappears from Live VMs
> and the cost for its final interval is captured."

### Act 8: CSV Export

**What to show:** Chargeback report output.

```bash
curl -s 'http://localhost:8020/api/v1/reports/costs?group_by=tenant&format=csv'
```

**Narration:**
> "The report API supports CSV export — same data as the dashboard, but
> in a format you can hand to finance or import into a spreadsheet."

## Automated Version

To run all acts with guided pauses:
```bash
bash snippets/demo-scenario-3-dashboard.sh
```

## Key Talking Points

- **Real-time visibility** — sub-second dashboard updates, not batch reports
- **Watch+List pattern** — real-time events + periodic reconciliation for reliability
- **Metering → Rating pipeline** — clean separation of usage measurement from pricing
- **Per-tenant rate overrides** — same infrastructure, different pricing per customer
- **Infrastructure/Supplementary split** — Koku-compatible cost classification
- **CloudEvents ingest** — same endpoint as OpenMeter, URL swap to migrate
- **CSV export** — chargeback-ready output from a single API call

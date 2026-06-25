# Cost AI Grid PoC

Proof-of-concept for cost management integrated with OSAC fulfillment-service.

## Structure

```
docs/                          Design documents and analysis
  thoughts.md                  Architecture exploration and consumer design
  cost-reports-feasibility.md  What cost reports are feasible with OSAC data
  local-dev-setup.md           How to run everything locally

snippets/                      Reusable scripts and curl commands
  create-test-data.sh          Populate OSAC with test compute instances

inventory-watcher/             Go service: watches OSAC events, builds cost inventory
  cmd/consumer/                Entry point
  internal/watcher/            Real-time OSAC event stream consumer
  internal/reconciler/         Periodic full-state reconciliation
  internal/summarizer/         Duration-based usage calculation
  internal/inventory/          PostgreSQL inventory store
  internal/osac/               OSAC REST API client and types
  scripts/                     OIDC server, token generator, setup script
```

## Inventory Watcher

A Go service that connects to the OSAC fulfillment-service and maintains a cost
inventory database:

- **Watches** OSAC events in real-time (CREATED/UPDATED/DELETED for compute
  instances, clusters, instance types)
- **Reconciles** periodically against OSAC List endpoints to catch missed events
- **Summarizes** resource durations into daily usage (CPU-core-hours, memory-GB-hours)

```bash
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/

OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN=$(cat /tmp/osac_token.txt) \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
./inventory-watcher
```

See [docs/local-dev-setup.md](docs/local-dev-setup.md) for full setup instructions.

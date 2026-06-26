# Cost Management — AI Grid PoC

A proof-of-concept integrating [Red Hat Cost Management](https://github.com/project-koku/koku) with [OSAC](https://github.com/osac-project) (Open Sovereign AI Console) for the AI Grid sovereign cloud offering.

---

## What it does

- Ingests CloudEvents from OSAC for resource lifecycle (clusters, VMs, models, bare metal)
- Meters capacity-based resources (CaaS, VMaaS) and consumption-based resources (MaaS — tokens/requests)
- Tracks budgets and quotas, emitting threshold alerts back to OSAC
- Exposes an API for cost, metering, inventory, and quota data

## Architecture

See [`docs/poc_architecture/architecture.md`](docs/poc_architecture/architecture.md).

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

See [docs/local-dev-setup.md](docs/dev/local-dev-setup.md) for full setup instructions.


## License
Discovery artifacts and scripts in this repository are part of the [Koku](https://github.com/project-koku/koku) project. OSAC is a separate open-source project with its own license — see the OSAC repository for details.

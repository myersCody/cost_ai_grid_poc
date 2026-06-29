# Cost Management — AI Grid PoC

Proof-of-concept cost management system integrated with
[OSAC](https://github.com/osac-project/fulfillment-service) for the
AI Grid sovereign cloud. Tracks infrastructure and AI model costs via
real-time event ingestion, capacity-based and consumption-based metering,
and tiered pricing.


**Deadline:** July 31, 2026

## Start Here

**[docs/index.md](docs/index.md)** — technical guide with links to
architecture, data model, API reference, requirements status, demos,
and code map.

## Quick Start

```bash
# Build
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/
go build -o maas-simulator ./cmd/maas-simulator/

# Run (requires OSAC + PostgreSQL — see docs/local-dev-setup.md)
OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN=$(cat /tmp/osac_token.txt) \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
INGEST_LISTEN_ADDR=localhost:8020 \
./inventory-watcher

# Demo data (VMs + MaaS events + cost entries)
bash snippets/setup-demo-data.sh

# Tests (27 assertions)
bash snippets/test-inventory-watcher.sh
```


## Documentation

| Document | Description |
|---|---|
| [Technical Guide](docs/index.md) | Entry point — start here |
| [Data Model](docs/data-model.md) | ERD diagrams, all 11 tables, Go model links |
| [gRPC Messages](docs/grpc-messages-catalog.md) | OSAC proto messages consumed |
| [API Reference](docs/api-reference.md) | HTTP endpoints exposed |
| [Implementation Status](docs/implementation-status.md) | Requirements vs code, cross-linked |
| [Local Dev Setup](docs/local-dev-setup.md) | How to run everything |

## Architecture

```
OSAC Watch stream → raw_events → inventory → metering (60s) → cost (30s)
                                                                  ↓
MaaS ingest endpoint → raw_events → inventory_model → metering → cost
                                                                  ↓
                                                         quota status API
```

See [docs/local-dev-setup.md](docs/dev/local-dev-setup.md) for full setup instructions.


## License
Discovery artifacts and scripts in this repository are part of the [Koku](https://github.com/project-koku/koku) project. OSAC is a separate open-source project with its own license — see the OSAC repository for details.

# CLAUDE.md

## Project

Cost Management AI Grid PoC — integrates with OSAC fulfillment-service for
capacity-based and consumption-based cost tracking.

## Build

```bash
cd inventory-watcher
go build -o inventory-watcher ./cmd/consumer/
go build -o maas-simulator ./cmd/maas-simulator/
```

## Run

```bash
OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN=$(cat /tmp/osac_token.txt) \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
INGEST_LISTEN_ADDR=localhost:8020 \
./inventory-watcher
```

## Test

```bash
# Fast (skip metering, ~15s)
SKIP_METERING=1 bash snippets/test-inventory-watcher.sh

# Full (~90s)
bash snippets/test-inventory-watcher.sh
```

## Documentation Maintenance Rules

When modifying source code, keep the corresponding docs in sync:

### When modifying `internal/osac/types.go` or `internal/watcher/watcher.go`:
- Update [docs/grpc-messages-catalog.md](docs/grpc-messages-catalog.md) —
  event types handled, resource fields consumed, handler mappings

### When modifying `internal/ingest/handler.go`:
- Update [docs/api-reference.md](docs/api-reference.md) — endpoint list,
  request/response schemas, handler links

### When modifying `internal/metering/` or `internal/rating/`:
- Update [docs/req1-osac-integration-gap-analysis.md](docs/req1-osac-integration-gap-analysis.md) —
  metering pipeline description, meter list, implementation progress
- Update [docs/req2-maas-costing-gap-analysis.md](docs/req2-maas-costing-gap-analysis.md) —
  MaaS metering section if MaaS meters change

### When modifying `internal/inventory/store.go` (schema changes):
- Update [docs/data-model.md](docs/data-model.md) — table list, ERD
  diagrams, meter definitions
- Rebuild ERDs if tables added/removed:
  `dot -Tsvg docs/diagrams/erd-inventory.dot -o docs/diagrams/erd-inventory.svg`
  `dot -Tsvg docs/diagrams/erd-metering-cost.dot -o docs/diagrams/erd-metering-cost.svg`
- Update [docs/api-reference.md](docs/api-reference.md) if new endpoints
  depend on new tables
- Update [docs/grpc-messages-catalog.md](docs/grpc-messages-catalog.md) if
  new inventory tables map to new resource types

### When modifying `internal/inventory/models.go`:
- Update [docs/data-model.md](docs/data-model.md) — Go model links in
  the tables section

### When modifying `internal/osac/client.go`:
- Update [docs/grpc-messages-catalog.md](docs/grpc-messages-catalog.md) —
  OSAC REST endpoints table at the bottom

### When adding or completing a requirement:
- Update [docs/implementation-status.md](docs/implementation-status.md) —
  status table, acceptance criteria checkboxes, summary counts
- Update [docs/requirements-comparison.md](docs/requirements-comparison.md) —
  gap table if status changed

### When adding architecture decisions:
- Add to `docs/decisions/` with sequential numbering (ADR-NNN)
- Add link to [docs/implementation-status.md](docs/implementation-status.md)
  architecture decisions table

## Key Files

```
inventory-watcher/
  cmd/consumer/main.go           Entry point, wires all components
  cmd/maas-simulator/main.go     MaaS event generator tool
  internal/
    osac/client.go               OSAC REST/Watch stream client
    osac/types.go                OSAC proto message Go mappings
    watcher/watcher.go           Real-time event consumer
    reconciler/reconciler.go     Periodic List-based drift correction
    metering/metering.go         60s sweep + MaaS event metering
    metering/billable.go         Billable state definitions
    rating/rating.go             Rate engine, tiered pricing, seeding
    inventory/store.go           PostgreSQL schema + all queries
    inventory/models.go          All Go struct types
    ingest/handler.go            HTTP API (events, quotas, health)
    config/config.go             Environment variable config
```

## Spec References

- [Updated requirements](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/csv_poc_requirements_summary.md)
- [Original requirements brief](https://github.com/martinpovolny/cost_ai_grid_poc/blob/main/docs/requirements/ai_grid_poc_requirements_brief.md)
- [OSAC fulfillment-service protos](https://github.com/osac-project/fulfillment-service/tree/main/proto/public/osac/public/v1)

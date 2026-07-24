# CLAUDE.md

## Project

Cost Management AI Grid PoC — integrates with OSAC fulfillment-service for
capacity-based and consumption-based cost tracking.

## Design Principles

This is a **Go service running in OpenShift** as part of the **Red Hat
Cost Management** product family. Design decisions should reflect that:

- **Go ecosystem first.** Use Go-native libraries and patterns:
  `log/slog` for structured logging, `net/http` for handlers,
  `prometheus/client_golang` for metrics, `context` for cancellation.
  Don't port Python/Java patterns when Go has idiomatic alternatives.

- **Kubernetes-native.** Implement proper liveness/readiness/startup
  probes, graceful shutdown with drain periods, and structured JSON
  logging for log aggregation. Follow the
  [observability plan](docs/observability.md) for specifics.

- **OpenShift ecosystem fit.** Expose Prometheus metrics on a separate
  port for ServiceMonitor scraping. Use Sentry/GlitchTip for crash
  reporting (same SDK as Koku). Support ClowdApp deployment patterns
  where applicable.

- **Cost Management alignment.** Use Koku terminology and schemas where
  applicable (see Naming Conventions below). The goal is eventual merge
  or close alignment with Koku — avoid diverging on field names, cost
  types, or report formats.

- **Authoritative sources.** When consuming external formats (CloudEvents,
  OSAC protos, IPP metering API), document the authoritative source URL
  on the struct definition and write tests using exact payloads from that
  source. See [CloudEvents catalog](docs/cloudevents-catalog.md).

## Naming Conventions

When naming fields, tables, metrics, or API concepts, **check Koku first**.
This project is intended to eventually merge with or align closely with
[Koku](https://github.com/project-koku/koku). Use Koku's terminology where
applicable:

- **cost_type**: `Infrastructure` or `Supplementary` (Koku's cost layer split)
- **metric names**: match Koku's naming (e.g., `cpu_core_request_per_hour`)
  and store in `koku_metric` for mapping
- **rate structure**: `tiered_rates` with `usage_start`/`usage_end` in Koku;
  our simplified format uses `tiers` with `up_to`/`price_per_unit`
- **report response format**: Koku uses `meta`/`data`/`total` with nested
  `cost`/`infrastructure`/`supplementary` blocks

Key references:
- [Koku rate schema alignment](docs/research/koku-rate-schema-alignment.md)
- [Koku report API schema](docs/research/koku-report-api-schema.md)
- [Cost model metric feasibility](docs/inputs/2026-06-cody-cost-model-metric-feasibility.md)
- Koku cost models source: `koku/cost_models/models.py`
- Koku OCP provider map: `koku/api/report/ocp/provider_map.py`

## Refactoring Rules

**Never refactor billing-critical code without tests.** Before changing
metering, rating, cost calculation, or inventory logic:

1. Write tests that capture the current behavior (inputs → outputs)
2. Verify the tests pass on the existing code
3. Refactor
4. Verify the same tests still pass

Billing-critical paths: `internal/rating/`, `internal/metering/`,
`internal/inventory/store.go` (metering/cost queries),
`internal/ingest/handler.go` (event processing + meter creation).

If existing test coverage is insufficient for a safe refactor, **add
tests first as a separate commit** before making functional changes.

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

## Diagrams

All diagrams live as Graphviz `.dot` sources in `docs/diagrams/` with
pre-rendered `.svg` files alongside them. After editing any `.dot` file,
rebuild SVGs (requires `graphviz`: `brew install graphviz`):

```bash
for f in docs/diagrams/*.dot; do dot -Tsvg "$f" -o "${f%.dot}.svg"; done
```

## Documentation Maintenance Rules

**Doc tree convention:** requirements-related docs (gap analyses,
requirements comparison, the canonical requirements overview) live under
`docs/requirements/`. ADRs go in `docs/decisions/`. Research docs go in
`docs/research/`. Everything else (implementation status, API reference,
data model, catalogs) stays in `docs/`.

When modifying source code, keep the corresponding docs in sync:

### When modifying `internal/osac/types.go` or `internal/watcher/watcher.go`:
- Update [docs/grpc-messages-catalog.md](docs/grpc-messages-catalog.md) —
  event types handled, resource fields consumed, handler mappings

### When adding or modifying API endpoints:
- Update `docs/openapi.yaml` first (source of truth), then regenerate
- Update [docs/api-reference.md](docs/api-reference.md) — endpoint list,
  request/response schemas, handler links

### When modifying `internal/metering/` or `internal/rating/`:
- Update [docs/requirements/req1-osac-integration-gap-analysis.md](docs/requirements/req1-osac-integration-gap-analysis.md) —
  metering pipeline description, meter list, implementation progress
- Update [docs/requirements/req2-maas-costing-gap-analysis.md](docs/requirements/req2-maas-costing-gap-analysis.md) —
  MaaS metering section if MaaS meters change

### When modifying `internal/inventory/store.go` (schema changes):
- Update [docs/data-model.md](docs/data-model.md) — table list, ERD
  diagrams, meter definitions, data flow diagrams
- Rebuild diagrams (see Diagrams section above)
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
- Update [docs/requirements/requirements-comparison.md](docs/requirements/requirements-comparison.md) —
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
    api/server.gen.go            Generated ServerInterface from OpenAPI spec (DO NOT EDIT)
    api/handler.go               HTTP API handlers implementing ServerInterface
    api/handler_test.go          API handler tests
    ingest/                      (removed — replaced by api/)
    custommetrics/custommetrics.go Config-driven metric extraction (REQ-13)
    config/config.go             Environment variable config
    metrics/metrics.go           Prometheus metric definitions
    metrics/middleware.go        HTTP metrics + request logging middleware
  oapi-codegen.yaml              OpenAPI → Go codegen config
  docs/openapi.yaml              OpenAPI 3.0.3 spec (source of truth for API contract)
```

## API Contract

The OpenAPI spec at `docs/openapi.yaml` is the source of truth for the
HTTP API. The Go server interface is generated from it using
[oapi-codegen](https://github.com/oapi-codegen/oapi-codegen)
([ADR-005](docs/decisions/005-oapi-codegen-spec-first-api.md)).

To regenerate after editing the spec:

```bash
cd inventory-watcher
oapi-codegen --config=oapi-codegen.yaml ../docs/openapi.yaml
```

CI checks that the generated code matches the committed spec.

## Spec References

- [Requirements overview](https://github.com/myersCody/cost_ai_grid_poc/blob/main/docs/requirements/poc_requirements_overview.md)
- [OSAC fulfillment-service protos](https://github.com/osac-project/fulfillment-service/tree/main/proto/public/osac/public/v1)

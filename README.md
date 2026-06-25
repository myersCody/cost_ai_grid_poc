# Cost Management — AI Grid PoC

A proof-of-concept integrating [Red Hat Cost Management](https://github.com/project-koku/koku) with [OSAC](https://github.com/osac-project) (Open Sovereign AI Console) for the AI Grid sovereign cloud offering.

**Deadline:** July 31, 2026

---

## What it does

- Ingests CloudEvents from OSAC for resource lifecycle (clusters, VMs, models, bare metal)
- Meters capacity-based resources (CaaS, VMaaS) and consumption-based resources (MaaS — tokens/requests)
- Tracks budgets and quotas, emitting threshold alerts back to OSAC
- Exposes a FastAPI REST API for cost, metering, inventory, and quota data

## Architecture

```
OSAC (REST/gRPC/Kafka) → Event Consumer (Python) → PostgreSQL → FastAPI → UI
```

Event ingestion supports three transports (swappable):
- **REST polling** — default for POC development
- **gRPC watch** — direct OSAC gRPC stream
- **Kafka** — production target (CloudEvents over `osac.events.*`)

## Stack

| Layer | Choice |
|---|---|
| Language | Python (uv + pyproject.toml) |
| API | FastAPI |
| Storage | PostgreSQL |
| ORM | SQLAlchemy + Alembic |
| Event format | CloudEvents 1.0 |
| Event transport | Kafka (KRaft) |

## Docs

- [`Docs/architecture.md`](Docs/architecture.md) — system design and component map
- [`Docs/data-model.md`](Docs/data-model.md) — database schema
- [`Docs/event-types.md`](Docs/event-types.md) — CloudEvents reference
- [`Docs/requirements/ai_grid_poc_requirements_brief.md`](Docs/requirements/ai_grid_poc_requirements_brief.md) — requirements and action items
- [`Docs/development/fullfillment_service_setup.md`](Docs/development/fullfillment_service_setup.md) — local dev setup

## License
Discovery artifacts and scripts in this repository are part of the [Koku](https://github.com/project-koku/koku) project. OSAC is a separate open-source project with its own license — see the OSAC repository for details.

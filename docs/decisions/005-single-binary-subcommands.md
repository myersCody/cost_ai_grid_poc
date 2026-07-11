# ADR-005: Single Binary with Subcommands

**Date:** 2026-07-11
**Status:** Proposed

## Context

We currently have three separate binaries under `cmd/`:
- `cmd/consumer/` — main pipeline
- `cmd/koku-sync/` — periodic sync to Koku
- `cmd/maas-simulator/` — event generator for testing

This means three container images or build targets. In OpenShift
deployments, the common pattern is one image with different entrypoints
(used by Koku itself with `run_server.sh`, Celery workers, Masu server).

## Decision

Consolidate into a single binary with subcommands:

```
cost-event-consumer serve          # main pipeline (default)
cost-event-consumer koku-sync      # periodic Koku sync (CronJob)
cost-event-consumer maas-simulate  # event generator (dev/test)
cost-event-consumer migrate        # database migrations (future)
```

## Rationale

- **One container image** — same image, different `command:` in the
  deployment YAML. Simplifies CI, image registry, and version management.
- **Shared config** — all subcommands share the same `config.Load()`
  and environment variable handling.
- **Standard Go pattern** — `cobra` or plain `os.Args` subcommand dispatch.
  Same pattern as `kubectl`, `oc`, `podman`.
- **K8s CronJob** — `koku-sync` runs as a CronJob with
  `command: ["cost-event-consumer", "koku-sync"]`. Same image as Deployment.

## Consequences

- `cmd/consumer/`, `cmd/koku-sync/`, `cmd/maas-simulator/` merge into
  `cmd/cost-event-consumer/` with subcommand dispatch.
- Containerfile builds one binary.
- Existing scripts (`snippets/demo-start.sh`, etc.) update to use subcommands.

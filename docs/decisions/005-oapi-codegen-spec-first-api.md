# ADR-005: Spec-First API with oapi-codegen

## Status
Accepted

## Context

We have a hand-written OpenAPI 3.0.3 spec (`docs/openapi.yaml`) and 21
hand-written HTTP handlers in `handler.go`. Nothing enforces they match.
Spec drift is inevitable — someone adds a field to a handler and forgets
to update the spec, or the spec documents a parameter the handler doesn't
accept. CI validates the spec is valid YAML, but not that it matches the
implementation.

We evaluated four approaches:

1. **Validation-only** (kin-openapi middleware) — catches drift at runtime
   but doesn't prevent it at compile time. ~1 day effort.
2. **Code-first auto-generation** (swaggo/swag) — generates spec from
   comment annotations. Blocked: swaggo only produces Swagger 2.0, not
   OpenAPI 3.0.3.
3. **Spec-first code generation** (oapi-codegen) — generates a Go
   interface from the spec. Our handlers implement the interface. Missing
   handler = compile error. ~2-3 days effort.
4. **Framework migration** (huma, ogen) — requires rewriting all handlers
   to a new framework's patterns. ~5-8 days, too invasive for a PoC.

## Decision

Adopt **oapi-codegen** with the `std-http-server` target (Go 1.22+
`http.ServeMux`, no framework dependency).

`docs/openapi.yaml` is the source of truth. Running `oapi-codegen`
generates a `ServerInterface` that our `Handler` struct must implement.
If the spec adds an endpoint, the code won't compile until we implement
the corresponding method.

### Why oapi-codegen

- **De facto standard at Red Hat** — used in 81 repositories across
  GitHub and internal GitLab, including:
  - [RedHatInsights/quickstarts](https://github.com/RedHatInsights/quickstarts)
    — full server generation, production service
  - [RedHatInsights/entitlements-api-go](https://github.com/RedHatInsights/entitlements-api-go)
    — CONTRIBUTING.md explicitly recommends it for new endpoints
  - [openshift/backplane-api](https://github.com/openshift/backplane-api)
    — client + server generation for OpenShift infrastructure
  - [RedHatInsights/widget-layout-backend](https://github.com/RedHatInsights/widget-layout-backend)
    — documented architecture using oapi-codegen v2
  - [RedHatInsights/playbook-dispatcher](https://github.com/RedHatInsights/playbook-dispatcher)
    — production service with echo middleware
- **8,400+ GitHub stars**, Apache 2.0, latest release v2.8.0 (Jul 2026),
  active weekly commits
- **Supports `net/http.ServeMux`** directly (`std-http-server` target) —
  no framework dependency, matches our existing architecture
- **Non-strict mode** preserves `(w http.ResponseWriter, r *http.Request)`
  handler signatures — handler bodies move over with minimal changes

### Pattern: RedHatInsights/quickstarts

We follow the quickstarts pattern:

- Single `oapi-codegen.yaml` at repo root
- `tools/tools.go` pins the oapi-codegen version via build-tag import
- Output to `internal/api/server.gen.go` (gitignored)
- `make generate` target regenerates from spec
- Handler struct in a separate hand-written file implements the generated
  `ServerInterface`
- Compile-time check: `var _ api.ServerInterface = &Handler{}`
- CI rebuilds from scratch (Dockerfile runs `make generate` then tests)

## Consequences

- **Spec is the API contract.** Adding an endpoint starts with editing
  `docs/openapi.yaml`, not `handler.go`. Run codegen, implement the new
  method, done.
- **Generated code is gitignored.** `*.gen.go` in `.gitignore`. CI
  regenerates every build. No stale generated code in PRs.
- **Handler signatures change slightly.** Query params arrive as typed
  struct fields instead of `r.URL.Query().Get()`. Path params arrive as
  function arguments. Method names follow the spec's `operationId`.
- **~33% less boilerplate.** Query param parsing, route registration, and
  parameter validation are handled by generated code. Handler methods
  focus on business logic.
- **Runtime request validation is optional.** Can add kin-openapi
  middleware on top for dev/CI environments to validate requests and
  responses against the spec at runtime.

## References

- [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) — the tool
- [RedHatInsights/quickstarts](https://github.com/RedHatInsights/quickstarts)
  — pattern source (server generation, chi, production)
- [RedHatInsights/entitlements-api-go](https://github.com/RedHatInsights/entitlements-api-go)
  — incremental adoption pattern, `include-tags` filtering
- [openshift/backplane-api](https://github.com/openshift/backplane-api)
  — client generation pattern, `go tool` directive
- [Our OpenAPI spec](../openapi.yaml) — the source of truth

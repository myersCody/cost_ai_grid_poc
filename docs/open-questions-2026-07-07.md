# Open Questions: Cost Management × OSAC — 2026-07-07

> **Note:** This document is a snapshot from the 2026-07-07 meeting. For current status, see [implementation-status.md](implementation-status.md).

> Topics for discussion with OSAC team. Walk through each topic, get
> answers or alignment, note decisions.

---

## 1. MaaS Tenant Attribution — THE BIG ONE

### Problem

For capacity-based resources (VMs, clusters), every OSAC resource carries
`metadata.tenant` — attribution is trivial. For MaaS inference, events
come from the IPP external-metering plugin
([ai-gateway-payload-processing PR #320](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/320))
and carry:

| Field | What it is | Example |
|---|---|---|
| `user` | Authenticated username (from Authorino) | `jdoe` |
| `group` | K8s group membership (NOT a cost org) | `finance-team` |
| `subscription` | Authorino-resolved subscription key | `ai-tenant-acme/premium-sub@models/llama-3` |
| `model` | Model name from request body | `llama-3-8b` |
| `provider` | Backend provider (from routing plugin) | `anthropic` |
| Token counts | prompt, completion, cached, reasoning | `15000`, `8000`, ... |

**What's missing:** `tenant_id`, `project_id` — not present in the event.

### What We Found in the IPP Code

**Finding 1: TokenMetadata already exists on the MaaSSubscription CRD.**
The CRD defines fields that are exactly what we need:
```go
type TokenMetadata struct {
    OrganizationID string            `json:"organizationId,omitempty"`
    CostCenter     string            `json:"costCenter,omitempty"`
    Labels         map[string]string `json:"labels,omitempty"`
}
```
These are defined on the CR but **never propagated** to the CloudEvent.
The external-metering plugin doesn't read them, and no header carries
them through the pipeline. Dead weight — wired up in the data model,
but never flows to us.

**Finding 2: The subscription key already contains the tenant namespace.**
The Authorino-resolved subscription key format is:
```
{subscriptionNamespace}/{subscriptionName}@{modelNamespace}/{modelName}
```
When AITenant multi-tenancy is enabled, subscriptions live in
`ai-tenant-{tenantName}` namespaces. So the `subscription` field in the
CloudEvent already carries the tenant namespace — we just need to parse
it. Needs confirmation with real data.

**Finding 3: The auth pipeline flow.**
```
Client → Envoy → Authorino (identity) → maas-headers-guard (capture)
  → external-metering (balance check) → provider → external-metering
  (extract usage, emit CloudEvent) → our ingest endpoint
```
The `maas-headers-guard` plugin captures `X-MaaS-Username`,
`X-MaaS-Group`, `X-MaaS-Subscription` into CycleState. There is no
`X-MaaS-Tenant` or `X-MaaS-OrgId` header in the pipeline today.

**Finding 4: Balance check uses username, not tenant.**
The plugin calls `GET /api/v1/customers/{username}/entitlements/...` —
we serve this endpoint, but `customerID` is the username, not the tenant.
Same attribution problem affects quota enforcement.

### How MaaSSubscription Works (for context in the meeting)

Every inference request is tied to a `MaaSSubscription` CR
([source](https://github.com/opendatahub-io/models-as-a-service/blob/main/maas-controller/api/maas/v1alpha1/maassubscription_types.go)).
The subscription defines:

- **Who can use it** — `Owner` with groups and users
- **What models** — `ModelRefs[]`, each pointing to a specific model
  with per-model `TokenRateLimit` (e.g., 1M tokens per 24h)
- **Billing attribution** — `TokenMetadata` with `OrganizationID`,
  `CostCenter`, and custom `Labels`
- **Priority** — integer ranking when multiple subscriptions match

The request flow:
```
Client authenticates
  → Authorino resolves which MaaSSubscription applies
  → injects X-MaaS-Subscription header (format: {namespace}/{name}@{modelNs}/{model})
  → maas-headers-guard captures into CycleState
  → external-metering plugin reads it, includes in CloudEvent
```

The subscription CR is namespace-scoped to the AITenant namespace
(`ai-tenant-{tenantName}`). So the full chain is:

```
MaaSSubscription CR (in ai-tenant-acme namespace)
  → has TokenMetadata.OrganizationID = "acme-corp"
  → has TokenMetadata.CostCenter = "engineering"
  → Authorino resolves subscription key: "ai-tenant-acme/premium-sub@models/llama-3"
  → CloudEvent carries subscription key (tenant namespace is in there)
  → BUT: OrganizationID and CostCenter are NOT in the CloudEvent (gap)
```

**Bottom line:** The data model for cost attribution exists on the CR.
Every request can be traced to a subscription, and every subscription
can carry org/cost-center. The gap is purely in propagation — getting
those fields from the CR through Authorino and the plugin into the
CloudEvent.

### What We Need to Know

**Q1: Does the subscription key carry the tenant namespace in practice?**
The Authorino-resolved format is `{namespace}/{name}@{modelNs}/{modelName}`.
When AITenant is enabled, is the namespace always `ai-tenant-{tenantName}`?
If yes, we can parse tenant from it today without upstream changes.

**Q2: Can we get TokenMetadata propagated into the CloudEvent?**
The MaaSSubscription CRD already has `tokenMetadata.organizationId` and
`costCenter`. **The data model exists — it just needs wiring.** Concrete
proposal: add `X-MaaS-OrgId` / `X-MaaS-CostCenter` headers in the
Authorino AuthPolicy (from subscription CR metadata), have the
external-metering plugin include them in the CloudEvent data payload.
This is a small upstream change — 2-3 fields in Authorino CEL + a few
lines in the plugin.

**Q3: What about `project_id` for MaaS?**
Moti noted (Jun 23, wg-osac-metering) that `project-id` should have been
included alongside `tenant-id` in CloudEvents but wasn't. For MaaS
events, what determines the project — the subscription, the user's
membership, or the model deployment namespace?

### Our Current Workaround

Falls back to `ce.Subject` (the username) as tenant_id. Multi-user
tenants would have costs scattered across user IDs instead of aggregated
under the tenant. Works for single-user demos, breaks in production.

### Our Proposal (updated with IPP research)

1. **Immediate (PoC):** Parse `subscription` key to extract tenant
   namespace — `ai-tenant-acme/sub@ns/model` → `ai-tenant-acme` →
   `acme`. No upstream changes needed, but needs validation with real
   data.
2. **Short term (upstream PR):** Propagate `tokenMetadata.organizationId`
   from MaaSSubscription CRD → `X-MaaS-OrgId` header → CloudEvent
   `data.organization_id`. The data model already exists on the CRD,
   just needs wiring through Authorino + the plugin (~20 lines upstream).
3. **Long term:** If org/project mapping is more complex than what the
   CRD carries, build a lookup table on our side.

**Ask: Is Option 2 feasible? Can we propose a PR to the
ai-gateway-payload-processing repo to propagate TokenMetadata?**

---

## 2. Model as a Resource — Moti's Question from Jul 2

### Context

Moti raised (Jul 2 meeting): *"How do we represent inference service
usage under a project when there's no resource?"* Avishay noted: *"Model
is a service, there's no resource."*

### What We Do Now

We get `model_name` from the CloudEvent payload and upsert into
`inventory_model`. This gives us a record per model deployment, but it's
our construct — OSAC doesn't define a Model entity.

### Questions

**Q4: Will Model become an OSAC entity?** (open question #9)
If yes → we add it to watcher/reconciler like ComputeInstance.
If no → CloudEvents ingest is our only data source, and `model_name` is
the identifier. Our implementation works either way.

**Q5: Is `model_name` stable for rate lookups?** (open question #8)
We key rates on `model_name`. If OSAC or RHOAI renames models, our rates
break. Is there a stable model ID we should use instead?

---

## 3. Private vs Public Watch Stream

### How It Works (background for the meeting)

There are two completely separate Watch stream definitions in the
fulfillment-service, each with its own proto, gRPC service, REST
gateway path, and `oneof` payload:

| | Public | Private |
|---|---|---|
| **Proto** | `proto/public/osac/public/v1/events_service.proto` | `proto/private/osac/private/v1/events_service.proto` |
| **gRPC** | `osac.public.v1.Events.Watch` | `osac.private.v1.Events.Watch` |
| **REST path** | `GET /api/events/v1/events` | `GET /api/private/v1/events/watch` |
| **Entity types** | 10 in `oneof` | 28 in `oneof` (adds CatalogItems, BareMetalInstance, networking, storage, users, etc.) |
| **Event types** | CREATED / UPDATED / DELETED | same + `EVENT_TYPE_OBJECT_SIGNALED` |

The REST gateway is generated by
[gRPC-Gateway](https://github.com/grpc-ecosystem/grpc-gateway) from
`google.api.http` annotations in the proto files.

**Both protos have the `google.api.http` annotation** — the public proto
defines `GET /api/events/v1/events`, the private proto defines
`GET /api/private/v1/events/watch`. However, the REST gateway binary
(`start rest-gateway`) only registers the private Events handler:

```
// start_rest_gateway_cmd.go — Events registration:
privatev1.RegisterEventsHandler   ← registered
// publicv1.RegisterEventsHandler ← NOT registered
```

The REST gateway registers 23 public + 24 private handlers for CRUD
endpoints (Tenants, Clusters, VMs, etc. are served from both namespaces),
but for the Watch stream only the private handler is registered.

The gRPC server (`start grpc-server`) serves both:
```
publicv1.RegisterEventsServer(grpcServer, eventsServer)       // :8010
privatev1.RegisterEventsServer(grpcServer, privateEventsServer) // :8010
```

**All three interfaces use the same JWT authentication** — Bearer token
validated against the OSAC OIDC/JWKS endpoint. Tested and working for
both REST and gRPC transports.

### What We Implemented

Two Watch stream clients, selectable via Go build tag:

- **REST client** (default) — connects to the private endpoint
  `GET /api/private/v1/events/watch` via the REST gateway on port 8011.
  Receives NDJSON-encoded events.
  Source: [`client_watch_rest.go`](../inventory-watcher/internal/osac/client_watch_rest.go)

- **gRPC client** (`go build -tags grpc_watch`) — connects directly to
  the gRPC server on port 8010, calls `osac.public.v1.Events.Watch`.
  Uses server reflection to dynamically decode protobuf messages.
  Source: [`client_watch_grpc.go`](../inventory-watcher/internal/osac/client_watch_grpc.go)

### Summary of Access

| Transport | Service | Port | REST Path | Exposed? |
|---|---|---|---|---|
| gRPC | `osac.public.v1.Events.Watch` | 8010 | — | Yes |
| gRPC | `osac.private.v1.Events.Watch` | 8010 | — | Yes |
| REST | public Events | 8011 | `/api/events/v1/events` | No (handler not registered) |
| REST | private Events | 8011 | `/api/private/v1/events/watch` | Yes |
| REST | CRUD (Tenants, VMs, etc.) | 8011 | `/api/fulfillment/v1/*` | Yes (both public + private) |

### The Trade-Off

- **Public stream** (10 entity types): covers everything we actively
  process today (ComputeInstance, Cluster, InstanceType, Project, Tenant)
  plus 5 we log-only. Does NOT include BareMetalInstance, CatalogItems,
  or networking types. Accessible only via gRPC.
- **Private stream** (28 entity types): everything in public plus
  BareMetalInstance, all 3 CatalogItem types, networking, storage, users.
  Also has `EVENT_TYPE_OBJECT_SIGNALED`. Accessible via both gRPC and REST.

For the PoC, the public stream's 10 entity types are sufficient. The
private stream matters when we want real-time BareMetalInstance and
CatalogItem events (currently handled via REST List polling).

### Open Questions for OSAC Team

1. Is the cost consumer intended to use the public or private Watch stream?
2. Would it make sense to register `publicv1.RegisterEventsHandler` in
   the REST gateway so public Watch is available via REST too?
3. Is gRPC on port 8010 the recommended interface for external consumers,
   or is REST preferred?

### Questions

**Q6a: Are we authorized to use the private Watch stream?**
We use `/api/private/v1/events/watch` because it's the only REST watch
endpoint that exists. The public watch is gRPC-only — no REST gateway
annotations. Is the private REST endpoint the intended consumption path
for external consumers like us?

**Q6b: If not — can the public events_service.proto get gRPC-Gateway
annotations so it's also accessible over REST?**
Alternatively, will BareMetalInstance and CatalogItems be added to the
public `oneof` so we can use the public stream without losing types?

### Fallback (if told not to use private REST)

We're not blocked either way. Two paths forward:

1. **Ask OSAC to add REST annotations to the public proto** — one line
   in their proto, zero change on our side except the URL. Best option.
2. **Switch to a gRPC client** — **already implemented and tested.**
   [PR #32](https://github.com/myersCody/cost_ai_grid_poc/pull/32) adds
   a compile-time switch: `go build -tags grpc_watch` uses the public
   `osac.public.v1.Events.Watch` gRPC stream instead of the private REST
   endpoint. Same JWT token, same handler pipeline, different wire
   transport. All 16 demo scenario tests pass with it. Uses server
   reflection + dynamic protobuf — no proto code generation needed.

---

## 4. Project-ID in CloudEvents

### Context

Moti confirmed (Jun 23, wg-osac-metering): `project-id` should be in
the CloudEvents schema alongside `tenant-id` but was missing from the
osac-metering-discover-poc collector samples.

### Question

**Q7: Is `project_id` being added to the OSAC metering collector events?**
Currently our capacity-based events (VMs, clusters) carry `tenant_id`
but not `project_id`. We derive it from `inventory_project` by looking
up the tenant. Having it directly in the event would simplify the
pipeline and ensure correctness.

---

## 5. MaaS Event Delivery (open questions #13, #14)

### Context

IPP's `reportUsage` is fire-and-forget HTTP POST. For production, events
cannot be lost.

### Questions

**Q8: What transport for MaaS events?**
- Direct HTTP to our ingest endpoint (current)?
- Via OSAC as intermediary (OSAC collects from RHOAI, forwards to us)?
- Kafka?

Moti asked (Jul 1, wg-osac-metering): *"Is there a need to register to
the endpoint to receive the cloud-events? What guarantees not missing
any?"* — this is the same concern.

**Q9: Event vs batch?**
IPP sends one event per inference request. At scale (thousands of
requests/second), do we need batching? Or is per-event acceptable?

---

## 6. Token Granularity (open question #10)

### Context

IPP sends 6 token dimensions: `prompt_tokens`, `completion_tokens`,
`total_tokens`, `cached_input_tokens`, `cache_creation_tokens`,
`reasoning_tokens`.

We currently meter 2: `tokens_in` (prompt) and `tokens_out` (completion).

### Question

**Q10: Should we meter all 6 dimensions?**
Pricing may differ per dimension (e.g., cached tokens cheaper, reasoning
tokens more expensive). The 4-dimension model (prompt, completion,
cached, reasoning) seems like the right granularity. Confirm.

---

## 7. Quick Status Updates (No Discussion Needed)

### Recently Completed
- **gRPC Watch stream client** — compile-time alternative to REST,
  uses public `osac.public.v1.Events.Watch`, all 16 tests pass
  ([PR #32](https://github.com/myersCody/cost_ai_grid_poc/pull/32))
- `inventory_tenant` table — Tenant events now properly tracked (was
  silently dropped from Watch stream)
  ([PR #30](https://github.com/myersCody/cost_ai_grid_poc/pull/30))
- OSAC Resource Type Overview — consolidated all 16 types with
  availability/processing status
- ClusterOrder vs Cluster resolved — ClusterOrder is the ordering
  workflow, we correctly track the Cluster
- 12 adversarial review findings fixed/verified since last meeting
- Quota scoping settled (Pau, PR #33): per tenant + project with rollup

### Remaining Review Findings (for awareness)
- 14 open findings, 0 critical, 1 high (#52: no tests for
  watcher/reconciler/store), rest medium/low
- All critical and high findings fixed or accepted

### New Work from Pau's Requirements Review (PR #33)
- **Catalog-item pricing** — prices per SKU, not rate × capacity.
  Needs a pricing layer on top of our rate engine. Not blocking PoC.
- **Project-level quotas with rollup** — currently tenant-only.
  ~1 day of work when prioritized.
- **Custom rate expression language** — Pau asks how to express
  "creative math" on metrics. Our REQ-13 config covers field extraction
  but not arbitrary formulas. Post-PoC discussion.

---

## Summary: What We Need From This Meeting

| # | Question | Who Answers | Priority |
|---|----------|-------------|----------|
| Q1 | Does subscription key carry tenant namespace in practice? | OSAC / IPP team | **Critical** |
| Q2 | Propagate `tokenMetadata.organizationId` from MaaSSubscription CRD → CloudEvent? | IPP team (Noy?) | **Critical** |
| Q3 | `project_id` in MaaS events — derived from what? | OSAC / IPP team | High |
| Q4 | Will Model become an OSAC entity? | Moti / Avishay | Medium |
| Q5 | Is `model_name` stable? | RHOAI / IPP team | Medium |
| Q6a | Are we authorized for private Watch stream? (we already use it) | OSAC (Juan?) | Medium |
| Q6b | If not — will BareMetalInstance/CatalogItems be added to public? | OSAC | Medium |
| Q7 | `project_id` in capacity CloudEvents? | Moti | Medium |
| Q8 | MaaS event transport (HTTP/Kafka/OSAC) | OSAC + Cost | Medium |
| Q9 | Per-event vs batch for MaaS | OSAC + Cost | Low |
| Q10 | Token granularity (2 vs 6 dimensions) | Cost + OSAC | Low |

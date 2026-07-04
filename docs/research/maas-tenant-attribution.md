# MaaS Tenant Attribution — From CloudEvent to Cost Entry

> How to attribute MaaS inference costs to the correct OSAC tenant and
> project, given the identity fields available in IPP CloudEvents.
>
> Date: 2026-07-04

## Key Question: Direct Routing or OSAC Enrichment?

Can IPP CloudEvents come directly to our cost pipeline, or do they need
to go through OSAC first for enrichment?

**Answer: UNCERTAIN — needs confirmation from OSAC/RHOAI team.**

The IPP CloudEvent carries `user`, `group`, and `subscription` but no
`tenant_id` or `project_id`. We initially assumed the subscription field
would carry `{namespace}/{name}` where the namespace is the OSAC tenant.

**However,** the [MaaSSubscription e2e report](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/main/test/e2e/reports/3.4/external-model-e2e-report.md)
shows that MaaSSubscription CRs live in a shared MaaS deployment
namespace, not in per-tenant namespaces. The subscription value in the
CloudEvent may be just a name (e.g., `external-models-subscription`),
not a `{tenant-namespace}/{name}` pair.

**Current fallback chain in our code** (best-effort, needs validation):
1. If `subscription` contains `/` → extract prefix as tenant (may not be correct)
2. If `group` is set → use as tenant (K8s group from Authorino)
3. Fall back to `user` (username from Authorino)

**What we need** (in order of preference):
1. **Explicit `X-MaaS-Tenant` header** injected by the Authorino
   AuthPolicy and surfaced in the CloudEvent `data.tenant_id` field.
   The auth layer already knows the tenant — it just doesn't expose it.
2. **Confirmation** that subscription namespace = tenant (if the
   deployment model puts subscriptions in per-tenant namespaces)
3. **Lookup table** mapping subscription → tenant (if neither above works)

## The Problem

Our cost pipeline needs `tenant_id` on every metering and cost entry.
For capacity-based resources (VMs, clusters, bare metal), this comes
from the OSAC Watch stream — every resource carries `metadata.tenant`.

For MaaS inference events, the data comes from the IPP external-metering
plugin in the OSAC AI gateway. **These events do not carry a `tenant_id`
or `project_id` field.** We need to derive them from the identity fields
that are present.

## What the IPP CloudEvent Contains

Source: [plugin.go — reportUsageEvent](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/plugin.go)

### Envelope

| Field | Value | Source |
|-------|-------|--------|
| `specversion` | `"1.0"` | Fixed |
| `type` | `"inference.tokens.used"` | Fixed |
| `source` | `"maas-gateway"` | Configurable |
| `id` | `"evt-{uuid}"` | Generated |
| `subject` | username | Same as `data.user` |
| `time` | RFC3339 UTC | Event timestamp |

### Data Payload — Identity Fields

| Field | Value | Origin | Example |
|-------|-------|--------|---------|
| `user` | Authenticated username | Authorino → `X-MaaS-Username` header | `jdoe` |
| `group` | User's group membership | Authorino → `X-MaaS-Group` header | `finance-team` |
| `subscription` | MaaSSubscription name | Authorino → `X-MaaS-Subscription` header | `my-tenant/premium-sub` |
| `provider` | Backend provider | Model routing plugin → CycleState | `anthropic` |
| `model` | Model name | Request body `model` field | `claude-sonnet-4-20250514` |

### Data Payload — Usage Fields

| Field | Type | Description |
|-------|------|-------------|
| `prompt_tokens` | int | Input/prompt tokens |
| `completion_tokens` | int | Output/completion tokens |
| `total_tokens` | int | Sum of all tokens |
| `cached_input_tokens` | int | Cached input tokens (discounted) |
| `cache_creation_tokens` | int | Tokens used to create cache |
| `reasoning_tokens` | int | Thinking/reasoning tokens |
| `duration_ms` | int | Request duration in milliseconds |

### What Is NOT Present

- **`tenant_id`** — not a concept in the MaaS gateway layer
- **`project_id`** — not a concept in the MaaS gateway layer
- **`namespace`** — not directly in the event, but derivable from `subscription`

## How Identity Fields Are Set

### Authentication Chain

Source: [maasauthpolicy_controller.go](https://github.com/opendatahub-io/ai-gateway/blob/main/internal/controller/maasauthpolicy_controller.go)

```
Client request (with API key or K8s bearer token)
  → Envoy Gateway
    → Authorino AuthPolicy
      → API key path:  POST /internal/v1/api-keys/validate → username, groups, subscription
      → K8s token path: TokenReview → username, groups; subscription from header
    → Inject X-MaaS-Username, X-MaaS-Group, X-MaaS-Subscription headers
      → maas-headers-guard plugin (captures to CycleState, strips from request)
        → external-metering plugin (reads from CycleState → CloudEvent)
```

### X-MaaS-Username

- **API key auth:** Resolved by maas-api's `/internal/v1/api-keys/validate`
  endpoint from the API key record. Set by Authorino from
  `auth.metadata.apiKeyValidation.username`.
- **K8s token auth:** From Kubernetes TokenReview
  `auth.identity.user.username`.

Source: [maasauthpolicy_controller.go#L285-L310](https://github.com/opendatahub-io/ai-gateway/blob/main/internal/controller/maasauthpolicy_controller.go)

### X-MaaS-Group

- **API key auth:** From `auth.metadata.apiKeyValidation.groups` (JSON
  array stringified).
- **K8s token auth:** From `auth.identity.user.groups` (K8s groups from
  TokenReview).

### X-MaaS-Subscription

- **API key auth:** From `auth.metadata.apiKeyValidation.subscription` —
  the MaaSSubscription bound to the API key at creation time.
- **K8s token auth:** From the `x-maas-subscription` request header
  if the client explicitly sends it. CEL expression:
  ```
  has(auth.metadata.apiKeyValidation)
    ? auth.metadata.apiKeyValidation.subscription
    : request.headers["x-maas-subscription"]
  ```

### Fallback Logic in the Plugin

Source: [plugin.go — processRequest](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/plugin.go)

```
username = CycleState["metering-username"]
         → fallback: request header "x-maas-username"
         → fallback: request header "x-maas-subscription" (!)

group = CycleState["metering-group"]
      → fallback: request header "x-maas-group"
      → fallback: subscription value

subscription = CycleState["metering-subscription"]
             → fallback: request header "x-maas-subscription"
```

## The Subscription → Tenant Mapping

The `subscription` field is the strongest link to OSAC tenant attribution
because **MaaSSubscription CRs are namespace-scoped**. The namespace
is the tenant boundary.

### MaaSSubscription Format — UNCERTAIN

We initially assumed the subscription field would follow:
```
{tenantNamespace}/{subscriptionName}
```

However, the [MaaSSubscription e2e report](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/main/test/e2e/reports/3.4/external-model-e2e-report.md)
shows the CR living in a shared MaaS namespace with a plain name:
```yaml
kind: MaaSSubscription
metadata:
  name: external-models-subscription
  namespace: <maas-namespace>   # shared, NOT per-tenant
```

The subscription value in the CloudEvent may be just the name, not a
`{namespace}/{name}` pair. **Needs confirmation.**

## Current State in Our Code

Source: [handler.go — classifyEvent](../../inventory-watcher/internal/ingest/handler.go)

Currently:
```go
case EventTypeInferenceTokens:
    rid := peek.ModelID
    if rid == "" {
        rid = peek.Model
    }
    return "Model", rid, tenantID
```

Where `tenantID` falls back to `ce.Subject` (the username). This means
**costs are attributed to the individual user, not the OSAC tenant.**

For a single-user-per-tenant setup this works, but in multi-user tenants
(multiple users under `tenant-acme`), costs would be scattered across
user IDs instead of aggregated under the tenant.

## Balance Check — Same Identity Gap

Source: [client.go — checkBalance](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go)

```
GET /api/v1/customers/{customerID}/entitlements/{featureKey}/value?model={model}
```

The `customerID` is the **username** (from `X-MaaS-Username`), not the
tenant. Our balance check endpoint receives the username and needs to
look up the right tenant's quota. Currently we pass it through as-is.

---

## Implementation Plan

### Option 1: Parse Subscription Namespace (Recommended for PoC)

**Approach:** Extract the namespace from the `subscription` field as the
tenant ID. No upstream changes needed.

**Changes:**

1. **`internal/ingest/handler.go` — `handleModelEvent` normalization block:**
   ```go
   // Derive tenant from subscription namespace
   // subscription format: "{namespace}/{name}" or "{ns}/{name}@{modelNs}/{modelName}"
   if data.Subscription != "" && data.TenantID == "" {
       if idx := strings.Index(data.Subscription, "/"); idx > 0 {
           data.TenantID = data.Subscription[:idx]
       }
   }
   ```

2. **`internal/ingest/handler.go` — `classifyEvent` for `EventTypeInferenceTokens`:**
   Add `Subscription` to the peek struct, extract tenant from it before
   falling back to `ce.Subject`.

3. **`internal/ingest/handler.go` — `handleBalanceCheck`:**
   The `customerID` from the URL path may be a username. If subscription
   info is available (e.g., query param or header), parse namespace as
   tenant for quota lookup.

4. **Store the subscription on the model/metering records** for
   audit and debugging:
   - Add `subscription` column to `inventory_model` table
   - Include `subscription` in metering entry metadata

5. **Test:** Add authoritative format test with subscription field,
   verify tenant is extracted correctly.

**Effort:** Small — ~20 lines of parsing + test.

### Option 2: Lookup Table (Production)

**Approach:** Maintain a `subscription_tenant_map` table that maps
subscription names to OSAC tenants and projects. Populated from
MaaSSubscription CR metadata (via reconciler or webhook).

**Changes:**
- New table: `subscription_tenant_map (subscription TEXT PK, tenant TEXT, project TEXT)`
- Reconciler or API to populate the mapping
- Lookup on event ingestion: `subscription → tenant + project`
- Fallback to Option 1 parsing if no mapping found

**Effort:** Medium — new table, reconciler, lookup logic.

### Option 3: Upstream CloudEvent Change (Cleanest)

**Approach:** Request the IPP team to add `tenant_id` (and optionally
`project_id`) to the CloudEvent data payload. The Authorino AuthPolicy
already has access to the subscription namespace — it could inject
`X-MaaS-Tenant` alongside the other headers.

**Changes on our side:** None — just read the new field.

**Changes on IPP side:**
- Add `tenant_id` field to reportUsageEvent
- Add `X-MaaS-Tenant` header injection in AuthPolicy (from subscription namespace)
- Add `tenant_id` to the metering-simulator OpenAPI spec

**Effort on our side:** Trivial. Requires upstream coordination.

### Recommended Path

1. **Now (PoC):** Implement Option 1 — parse subscription namespace.
   Works immediately, no coordination needed.
2. **Meeting question:** Propose Option 3 to the IPP team — adding
   `tenant_id` upstream is cleaner long-term.
3. **Production:** Option 2 if tenant/project mapping is more complex
   than namespace parsing.

### Meeting Question to Add

> **MaaS tenant attribution:** The IPP CloudEvent has `user`, `group`,
> `subscription` but no `tenant_id`. We plan to derive tenant from the
> subscription namespace (e.g., `tenant-acme/premium-sub` → `tenant-acme`).
> Is this the correct mapping? Would it be feasible to add a
> `X-MaaS-Tenant` header in the AuthPolicy and a `tenant_id` field in
> the CloudEvent data payload?

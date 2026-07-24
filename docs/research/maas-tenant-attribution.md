# MaaS Tenant Attribution — From CloudEvent to Cost Entry

> How to attribute MaaS inference costs to the correct OSAC tenant and
> project, given the identity fields available in IPP CloudEvents.
>
> Date: 2026-07-04 | Updated: 2026-07-10

## Confirmed Findings (July 2026)

The following claims from this document have been confirmed by OSAC/MaaS
team members via comments on our
[open questions doc](https://docs.google.com/document/d/10pc_EJXKd0FZwA53uyk0SRJZ1HjyPuxKrYcXoOyHlgk)
and Slack (#wg-osac-maas).

**Confirmed by Moti Asayag (comment on open questions doc):**
- OSAC maps `organization` → `tenant` and `groups` → `projects`
  via Keycloak. The `project_id` in OSAC is retrieved from Keycloak
  groups.

**Confirmed by Noy Itzikowitz (relayed by Kris Verlaenen in comment on open questions doc):**
- **Q1 (subscription namespace = tenant):** Yes — with AITenant
  multi-tenancy enabled, subscriptions live in `ai-tenant-{name}`
  namespaces, so parsing works for PoC. But it's a naming convention,
  not an API contract — don't build on it long-term. Also note that
  with API-key auth the subscription is snapshotted at key creation.
- **Q2 (propagate TokenMetadata into CloudEvent):** Feasible and "the
  right approach." Wiring: `maas-api` key-validation response returns
  `organizationId`/`costCenter` → AuthPolicy adds `X-MaaS-OrgId` /
  `X-MaaS-CostCenter` headers (same CEL pattern as existing headers)
  → external-metering plugin picks them up into CycleState and adds
  to CloudEvent data. Extending the check-balance request with the
  same fields is trivial on the plugin side. Bigger question: should
  entitlements be keyed by tenant rather than user? Needs design
  discussion.
- **Q3 (project_id for MaaS):** Needs a product decision — natural
  candidates are the subscription (billing construct) or the model
  deployment namespace.

**Confirmed by Mriganka Paul (Slack #wg-osac-maas, 2026-07-09):**
- Phase 1: 1 cluster = 1 tenant (cluster identity is the tenant)
- Future: tenant parsed from `subscription` field format:
  `ai-tenant-{tenantName}/{subName}@{modelNs}/{modelName}` when
  AITenant multi-tenancy is enabled
- IPP external-metering plugin emits `inference.tokens.used`
  CloudEvents to configurable `meteringURL` — this is our ingest
  endpoint

**Implemented and tested (PR #39, experiment report):**
- `organization_id` flows end-to-end: `x-maas-organization-id` header
  → CloudEvent `data.organization_id` → cost consumer `tenant_id`
  on metering entries. ~30 lines across two repos. See
  [tenant attribution experiment](../dev/tenant-attribution-experiment-2026-07-08.md).

---

## Key Question: How Does Tenant Attribution Work?

**Answer: CONFIRMED** — three approaches validated, in order of maturity:

1. **`organization_id` from TokenMetadata** (confirmed by Noy as "the
   right approach") — propagate `organizationId` from MaaSSubscription
   CRD through Authorino headers to CloudEvent. Implemented and tested
   in PR #39. Requires small upstream changes in `maas-api` + AuthPolicy.

2. **Parse subscription namespace** (confirmed by Noy and Mpaul) — with
   AITenant enabled, subscription key format is
   `ai-tenant-{tenantName}/{subName}@{modelNs}/{modelName}`. Parsing
   the namespace works for PoC but is a naming convention, not an API
   contract.

3. **OSAC mapping: organization → tenant, groups → projects** (confirmed
   by Moti) — Keycloak groups map to OSAC projects, organizations map
   to tenants. This is the authoritative model.

**Current implementation in our code** (PR #39):
1. If `organization_id` is set → use as `tenant_id`
2. Parse subscription namespace as fallback
3. Fall back to `group`, then `user` (username from Authorino)

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

### MaaSSubscription Format — CONFIRMED

**Confirmed by Noy and Mpaul:** With AITenant multi-tenancy enabled,
the Authorino-resolved subscription key follows:
```
{subscriptionNamespace}/{subscriptionName}@{modelNamespace}/{modelName}
```

Example: `ai-tenant-acme/premium-sub@models/llama-3`

The subscription namespace is `ai-tenant-{tenantName}`, so parsing
the tenant is: split on `/`, take the first segment, strip the
`ai-tenant-` prefix.

**Caveats (from Noy):**
- This is a naming convention, not an API contract
- With API-key auth, the subscription is snapshotted at key creation time
- Don't build production billing on namespace parsing long-term — use
  `organization_id` from TokenMetadata instead

## Current State in Our Code

> **Updated (Jul 18, 2026):** The section below described the pre-PR#39
> state. Tenant attribution is now implemented with a 4-level fallback
> in `handleModelEvent` (`handler.go`):
>
> 1. `organization_id` from CloudEvent data (preferred)
> 2. `ai-tenant-` prefix stripped from subscription namespace
> 3. `group` field
> 4. `user` field (last resort)
>
> This was verified end-to-end in
> [tenant attribution experiment](../dev/tenant-attribution-experiment-2026-07-08.md)
> and merged in PRs #39, #47. `user` is now stored separately as
> `user_id` on metering/cost entries for per-user cost attribution
> (PR #59).

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

1. **`internal/api/handler.go` — `handleModelEvent` normalization block:**
   ```go
   // Derive tenant from subscription namespace
   // subscription format: "{namespace}/{name}" or "{ns}/{name}@{modelNs}/{modelName}"
   if data.Subscription != "" && data.TenantID == "" {
       if idx := strings.Index(data.Subscription, "/"); idx > 0 {
           data.TenantID = data.Subscription[:idx]
       }
   }
   ```

2. **`internal/api/handler.go` — `classifyEvent` for `EventTypeInferenceTokens`:**
   Add `Subscription` to the peek struct, extract tenant from it before
   falling back to `ce.Subject`.

3. **`internal/api/handler.go` — `handleBalanceCheck`:**
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

### Recommended Path (updated with confirmations)

1. **Done (PR #39):** `organization_id` propagation tested end-to-end.
   Upstream PR submitted:
   [ai-gateway-payload-processing#386](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/386)

2. **Next:** Get `maas-api` key-validation endpoint to return
   `tokenMetadata.organizationId` and `costCenter`. Then the AuthPolicy
   wiring is automatic (same CEL pattern as existing headers). This is
   the path Noy confirmed as correct.

3. **Production:** Entitlements keyed by tenant instead of user (design
   discussion needed per Noy). Project_id mapping needs product decision
   (subscription vs model namespace).

### Remaining Open Items

- **Entitlements by tenant vs user:** Noy flagged that extending the
  check-balance request with org fields is trivial on the plugin side,
  but whether entitlements should be keyed by tenant rather than user
  needs a design discussion.
- **project_id for MaaS:** Candidates are the subscription (billing
  construct) or the model deployment namespace. Needs product decision.
- **OSAC mapping confirmation:** Moti confirmed org→tenant, groups→projects
  via Keycloak, but we haven't validated this in a running OSAC
  environment with real Keycloak groups.

# Tenant Attribution Experiment — 2026-07-08

> End-to-end proof that `organization_id` and `cost_center` can flow
> from MaaSSubscription TokenMetadata through the IPP external-metering
> plugin to our cost consumer's metering entries.

## Result: SUCCESS

Inference request with `x-maas-organization-id: acme-corp` header →
CloudEvent with `organization_id: acme-corp` in data payload →
cost consumer attributes `tenant_id: acme-corp` on metering entries.

~30 lines of code across two repos. No changes to maas-headers-guard,
Authorino, or database schema.

## What We Proved

1. The `maas-headers-guard` plugin already captures **all** `x-maas-*`
   headers — no changes needed
2. Adding `organization_id` and `cost_center` to the CloudEvent is
   ~20 lines in the external-metering plugin
3. The cost consumer uses `organization_id` for tenant attribution
   with ~10 lines of handler changes
4. The full pipeline works end-to-end on k3d with the existing
   integration test stack

## Test Environment

Same k3d stack as the [IPP stress test](ipp-stress-test-2026-07-05.md):
- k3d cluster `cost-test`
- Istio 1.29.2 with Gateway API
- IPP (modified `feat/external-metering-dp` branch + our changes)
- llm-katan (echo LLM)
- Cost consumer (modified `handler.go`)
- PostgreSQL 18

## Test Command

```bash
kubectl exec test-client -n ai-gateway -- curl -s \
  http://ai-gateway-istio.ai-gateway:80/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-key" \
  -H "x-maas-username: test-user" \
  -H "x-maas-group: test-tenant" \
  -H "x-maas-subscription: test-tenant/premium-plan" \
  -H "x-maas-organization-id: acme-corp" \
  -H "x-maas-cost-center: engineering" \
  -d '{"model":"test-model","messages":[{"role":"user","content":"tenant attribution test"}]}'
```

## Verification

### CloudEvent received (raw_events table)

```json
{
  "data": {
    "user": "test-user",
    "group": "test-tenant",
    "subscription": "test-tenant/premium-plan",
    "organization_id": "acme-corp",
    "cost_center": "engineering",
    "model": "test-model",
    "prompt_tokens": 4,
    "completion_tokens": 12,
    "total_tokens": 16,
    "duration_ms": 38
  },
  "type": "inference.tokens.used",
  "source": "maas-gateway"
}
```

### Metering entries attributed to org

```
 tenant_id |   meter_name    |   value   |  unit
-----------+-----------------+-----------+--------
 acme-corp | maas_tokens_out | 12.000000 | tokens
 acme-corp | maas_tokens_in  |  4.000000 | tokens
```

`tenant_id = acme-corp` — derived from `organization_id`, not the
username fallback.

## Changes Made

### IPP Plugin (ai-gateway-payload-processing)

| File | Change |
|---|---|
| `pkg/plugins/common/state/state-keys.go` | 2 new constants: `MeteringOrganizationIDKey`, `MeteringCostCenterKey` |
| `pkg/plugins/external-metering/plugin.go` | `processRequest`: read `x-maas-organization-id`/`x-maas-cost-center` from maasHeaders + fallback, write to CycleState. `reportUsageEvent`: read from CycleState, add to CloudEvent data |

PR: [opendatahub-io/ai-gateway-payload-processing#386](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/386)

### Cost Consumer (cost_ai_grid_poc)

| File | Change |
|---|---|
| `internal/ingest/handler.go` | Add `OrganizationID`, `CostCenter` to `MaaSEventData`. Add `OrganizationID` to `classifyEvent` peek struct. Prefer `organization_id` over subscription/group/user in tenant attribution |

PR: [myersCody/cost_ai_grid_poc#39](https://github.com/myersCody/cost_ai_grid_poc/pull/39)

### Not Changed

- **maas-headers-guard** — already captures all `x-maas-*` headers
- **client.go** — just sends raw JSON, no field awareness
- **Authorino AuthPolicy** — for this test, headers injected manually
  by curl. Production path: Authorino reads TokenMetadata from
  MaaSSubscription CR and injects headers automatically
- **Database schema** — `organization_id` flows through to `tenant_id`
  column, no new columns needed

## Production Path

For production, the remaining piece is wiring Authorino to inject the
headers automatically from MaaSSubscription TokenMetadata:

1. **maas-api** (`models-as-a-service` repo) — the `/internal/v1/api-keys/validate`
   endpoint returns `username`, `groups`, `subscription`. It would need
   to also return `tokenMetadata.organizationId` and `costCenter`.

2. **maas-controller** (`models-as-a-service` repo) — the
   `maasauthpolicy_controller.go` generates the Authorino AuthPolicy CR.
   It would need to add `x-maas-organization-id` and `x-maas-cost-center`
   to the `response.success.headers` section, reading from
   `auth.metadata.apiKeyValidation.tokenMetadata`.

No changes needed in our repos or in the IPP plugin — the downstream
pipeline is already wired.

## Related

- [MaaS tenant attribution research](../research/maas-tenant-attribution.md)
- [IPP overview](../research/ipp-overview.md)
- [MaaS flow](../maas-flow.md)
- [k3d IPP deployment guide](k3d-ipp-deployment.md)
- [IPP stress test](ipp-stress-test-2026-07-05.md)
- [Open questions 2026-07-07](../open-questions-2026-07-07.md) — Q1/Q2

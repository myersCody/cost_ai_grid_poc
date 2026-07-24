# MaaS Inference Flow — How Cost Attribution Works

> How an inference request flows through the OSAC AI gateway and
> generates cost events in our pipeline.

## The Flow

![MaaS IPP Flow](diagrams/maas-ipp-flow.svg)

## Step by Step

### Request path (before inference)

1. **User sends request** — with an API key or K8s token, to the OSAC
   AI gateway (Envoy proxy with Istio sidecar).

2. **Authorino authenticates** — validates the credential (API key via
   maas-api, or K8s TokenReview) and injects identity headers:
   `X-MaaS-Username`, `X-MaaS-Group`, `X-MaaS-Subscription`.

3. **IPP plugin chain processes the request** — the ext_proc filter
   runs a chain of plugins on every request:
   - `maas-headers-guard` — captures identity headers into CycleState,
     strips them before forwarding to the provider
   - `body-field-to-header` — extracts `model` from the JSON body
   - **`external-metering` (on request)** — calls our balance check:
     `GET /api/v1/customers/{username}/entitlements/inference-tokens/value?model={model}`
     - If `hasAccess: true` → request proceeds
     - If `hasAccess: false` → returns HTTP 429 (budget exhausted)
     - If our service is down + `failOpen: true` → request proceeds anyway
   - `api-translation` — converts between API formats if needed
   - `apikey-injection` — swaps the user's key for the provider credential

4. **Request forwarded to LLM provider** — Anthropic, OpenAI, vLLM,
   or llm-katan (for testing).

### Response path (after inference)

5. **LLM responds** — with the generated text and a `usage` block
   containing token counts.

6. **`external-metering` (on response)** — extracts token counts from
   the response and builds a CloudEvent:
   ```json
   {
     "type": "inference.tokens.used",
     "subject": "username",
     "data": {
       "user": "jdoe",
       "group": "finance-team",
       "subscription": "premium-plan",
       "model": "claude-sonnet-4-20250514",
       "prompt_tokens": 1500,
       "completion_tokens": 800,
       "cached_input_tokens": 200,
       "reasoning_tokens": 150,
       "duration_ms": 3200
     }
   }
   ```
   POSTs this to our ingest endpoint: `POST /api/v1/events`
   (fire-and-forget, async).

7. **Response returned to user** — the user gets the LLM response
   normally. The metering is invisible to them.

### Cost pipeline (after metering event arrives)

8. **Our ingest endpoint** receives the CloudEvent, stores it in
   `raw_events`, and creates metering entries:
   - `maas_tokens_in` (prompt/input tokens, includes cached)
   - `maas_tokens_out` (completion/output tokens, includes reasoning)
   - `maas_requests` (API request count)

9. **Rating sweep** (every 30s) applies rates to produce cost entries
   with Infrastructure/Supplementary classification.

## What We Implement

We are the **Cost Event Consumer** box in the diagram. We implement
two endpoints that the IPP external-metering plugin calls:

| Endpoint | When | Purpose | Handler |
|----------|------|---------|---------|
| `GET /api/v1/customers/{id}/entitlements/{key}/value` | Before every inference request | Balance check — does the user have budget? | [`handleBalanceCheck`](../inventory-watcher/internal/api/handler.go) |
| `POST /api/v1/events` | After every inference response | Usage report — CloudEvent with token counts | [`handleEvent`](../inventory-watcher/internal/api/handler.go) |

Contract verified against:
- [IPP client.go](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go) — struct tags for response format
- [metering-simulator OpenAPI spec](../docs/specs/maas-metering-openapi.yaml) — full API contract

This makes us a **drop-in replacement** for OpenMeter or the
[metering-simulator](https://github.com/noyitz/metering-simulator).
The IPP plugin just needs its `meteringURL` config pointed at us.

## Identity → Tenant Attribution

The CloudEvent carries `user`, `group`, `subscription` but no
`tenant_id`. See [MaaS tenant attribution](research/maas-tenant-attribution.md)
for how we derive tenant from these fields.

## Source References

- IPP plugin source: [plugin.go](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/plugin.go)
- Metering client: [client.go](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go)
- Auth chain: [Authorino overview](research/authorino-overview.md)
- IPP architecture: [IPP overview](research/ipp-overview.md)
- Full CloudEvents catalog: [cloudevents-catalog.md](cloudevents-catalog.md)

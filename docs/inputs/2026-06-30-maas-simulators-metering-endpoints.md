# Input: MaaS Simulators and Metering Endpoints — June 30, 2026

> Source: #wg-osac-metering Slack channel (Noy Itzikowitz, Moti Asayag)

## MaaS Simulators

Two simulators exist for testing the MaaS metering pipeline:

### llm-katan (inference simulator)

- **Repo:** https://github.com/yossiovadia/llm-katan
- **Purpose:** Echoes inference requests with realistic usage data (model
  name, token counts). No real LLM calls, no API credits burned.
- **Auth:** Full OIDC/Authorino pipeline — same auth flow as a real
  provider. User identity flows through identically.
- **Use for:** Load testing the metering flow end-to-end.

### metering-simulator (metering backend)

- **Repo:** https://github.com/noyitz/metering-simulator
- **Purpose:** Implements the `checkBalance` and `reportUsage` APIs. Point
  the IPP (Inference Platform Plugin) at it to test the integration
  contract without standing up OpenMeter or a real billing backend.
- **Use for:** Testing the metering API contract in isolation.

## Metering Endpoints — What We Need to Implement

The external-metering IPP plugin calls two endpoints on the configured
`meteringURL`:

### `POST /api/v1/check` — Balance Check (synchronous)

Called **on every inference request** before forwarding to the model.
Must respond quickly (<500ms). The IPP uses this to gate requests:
- If balance OK → forward request to the model
- If balance exhausted → reject with 429

**This maps to our quota API.** Our existing `GET /api/v1/quotas/{tenant_id}`
provides the data; we need a `POST /api/v1/check` wrapper that:
1. Reads tenant from the request (auth context or body)
2. Calls `MeteringSum()` to get current consumption
3. Compares against quota limit
4. Returns allow/deny

### `POST /api/v1/events` — Usage Report (async, fire-and-forget)

Called **on every inference response** with usage data (tokens in/out,
model, duration). Currently fire-and-forget HTTP POST.

**We already implement this.** Our ingest endpoint at `POST /api/v1/events`
accepts CloudEvents — same path, same format. The IPP plugin can point
directly at us.

## Event Format (from Noy)

One event per completed inference request (not batched). Each event is a
CloudEvent with:
- **Token counts:** prompt, completion, cached, reasoning (4 dimensions,
  not just our simplified tokens_in/tokens_out)
- **Model:** model name/ID
- **Provider:** which external provider (Anthropic, OpenAI, etc.)
- **User:** username from OIDC/API key auth
- **Group:** Kubernetes group membership
- **Subscription:** MaaSSubscription bound to the API key
- **Duration:** request duration

**Gap vs our current model:** We currently meter `tokens_in` and
`tokens_out`. The real events have 4 token dimensions: prompt, completion,
cached, reasoning. We should update our metering to match, or map
prompt→tokens_in and completion→tokens_out and add cached+reasoning as
new meters.

## User Identity Fields (from Noy)

- `username` — MaaS user identity (from API key or OIDC token, set by
  Authorino at auth time)
- `group` — Kubernetes group membership (snapshotted when API key was created)
- `subscription` — MaaSSubscription bound to the API key (rate limits,
  model access)

Users authenticate via OIDC (Keycloak) or GitHub OAuth. The username
should match what cost-mgmt syncs from Keycloak.

## IPP Plugin Architecture (from Noy)

```
Client → Gateway → Auth (Authorino) → IPP ext_proc plugin chain:
  → body-field-to-header (extract model)
  → maas-headers-guard (capture identity headers)
  → external-metering (balance check on request, usage report on response)
  → model-provider-resolver (resolve model → provider)
  → api-translation (format conversion)
  → apikey-injection (credential swap)
→ External Provider (Anthropic/OpenAI/etc.)
```

The metering plugin runs twice: on request (balance check) and on
response (usage reporting). For streaming responses, usage is extracted
from SSE chunks.

**Code:** [PR #320](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/320)
in ai-gateway-payload-processing. Currently uses OpenMeter-compatible
CloudEvents format, but the interface is adaptable.

## RHOAI Timeline

External metering plugin targeting **RHOAI 3.5 (Dev Preview)**. Running
in dogfood environment today with real Claude Code and Codex sessions.

## Kafka Decision (from Moti, Jun 24)

Moti stated: "Since we cannot lose any piece of information, we need some
reliable mechanism. Since **Kafka is already part of our stack**, we'd
better rely on it and not add other component." Proposed using CloudEvents
format over Kafka, with Quarkus-based collectors.

This confirms Kafka is available in the OSAC stack — our Kafka consumer
work (~150 lines) aligns with this direction.

## CloudEvents Schema References

Moti shared the OSAC metering collector CloudEvents schemas:
- VMaaS: https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README.md#cloudevents-schema
- CaaS: https://github.com/masayag/osac-metering-discover-poc/blob/main/collector/README-caas.md#cloudevents-schema

Note from Moti: `project-id` should have been included alongside
`tenant-id` in the events but was not — to be added.

## Delivery Guarantees — Open Design Question

The current DP architecture is intentionally simple (fire-and-forget HTTP,
fail-open). For production, events cannot be lost. Options discussed:

1. **Kafka** — if OSAC already has it in the stack
2. **Persistent local buffer with replay** — client-side reliability
3. **CloudEvents webhook with retry + DLQ** — standard webhook pattern

The consensus is to align with whatever infrastructure OSAC/cost-mgmt
already runs rather than introduce new components. Our assessment: Kafka
consumer is ~150 lines of Go on our side (see
[ADR-002](../decisions/002-arguments-against-kafka.md)).

## Event Augmentation

If OSAC needs to enrich events with user/org context from Keycloak before
the cost system processes them, a stream processor between the metering
producer and cost consumer can join using:
- `subject` field (username) — CloudEvents standard
- `data.group` field — join key for Keycloak user profile

The CloudEvents format supports this by design.

## Action Items

1. **Try llm-katan:** Set up the simulator, point it at our ingest
   endpoint, verify events flow through our pipeline (metering → rating
   → cost entries → dashboard). This would replace our `maas-simulator`
   with the OSAC team's tooling for a more realistic demo.

2. **Implement `POST /api/v1/check`:** Thin wrapper around our quota
   logic. ~30 lines of Go in `handler.go`. Enables the IPP plugin to
   use us as the metering backend directly.

3. **Align on delivery transport:** Add to meeting questions — does OSAC
   plan to run Kafka? If yes, we add a consumer. If no, agree on
   retry/DLQ for the HTTP path.

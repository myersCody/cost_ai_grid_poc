# k3d IPP Gateway Deployment — Setup & Troubleshooting

> End-to-end deployment of the MaaS inference metering pipeline on
> local k3d. Verified working 2026-07-04.

## What This Proves

An inference request flows through the OSAC AI gateway, the IPP
external-metering plugin calls our cost-event-consumer for balance
check and usage reporting, and metering entries are created in our
database. Full chain:

```
curl (with X-MaaS-* headers)
  → Istio Gateway
    → IPP ext_proc (checkBalance → our consumer → OK)
    → llm-katan (echo LLM)
  ← response with usage
    → IPP ext_proc (reportUsage → CloudEvent → our consumer)
      → raw_events → metering_entries
```

## Test Scope — What Is and Isn't Covered

This test verifies the metering pipeline *after* authentication. It does
**not** test the authentication layer itself.

| Step | In test | In production | Notes |
|------|---------|---------------|-------|
| Client authenticates with API key / K8s token | ❌ skipped | ✅ Authorino | We pre-set headers instead |
| Authorino injects `x-maas-username`, `x-maas-group`, `x-maas-organization-id` | ❌ skipped | ✅ Authorino | We set these headers manually in curl |
| `maas-headers-guard` reads and strips `x-maas-*` headers | ✅ | ✅ | Headers are present regardless of who set them |
| `external-metering` emits `inference.tokens.used` CloudEvent with identity fields | ✅ | ✅ | Verified via consumer logs |
| `organization_id` propagated into CloudEvent (PR #386) | ✅ | ✅ | New field tested with `x-maas-organization-id` header |
| CloudEvent POSTed to consumer `/api/v1/events` | ✅ | ✅ | |
| Consumer creates metering + cost entries | ✅ | ✅ | |
| Cost entries attributed to correct tenant via `organization_id` | ✅ | ✅ | Asserted in `test-ipp.sh` |

**The untested assumption:** Authorino correctly maps a given credential
(API key or K8s token) to the right `organization_id`. If that mapping
is wrong, costs are misattributed and this test would not catch it.
Testing that mapping requires a full Authorino deployment with real
credentials, which is out of scope here.

In other words: we are **emulating what Authorino would do** by setting
the `x-maas-*` headers directly. The test validates everything from the
IPP plugin onward.

## Versions (verified working)

| Component | Version | Source |
|-----------|---------|--------|
| k3d | 5.9.0 | `brew install k3d` |
| k3s (inside k3d) | v1.35.5+k3s1 | k3d default |
| Istio | **1.29.2** | `curl -sL https://istio.io/downloadIstio \| ISTIO_VERSION=1.29.2 sh -` |
| Gateway API CRDs | **v1.4.0** | `kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/standard-install.yaml` |
| IPP (ai-gateway-payload-processing) | [PR #386](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/386) branch `feat/tenant-attribution-fields` | Built from `martinpovolny` fork |
| llm-katan | 0.20.2 | `pip install llm-katan` in container |
| cost-event-consumer | main branch | Built from repo |
| PostgreSQL | 16-alpine | `postgres:16-alpine` |
| Helm | 3.x | `brew install helm` |

### Critical version notes

- **Istio 1.29.2** — matches the IPP team's e2e test setup. Older
  versions (1.26.1) may not support `FULL_DUPLEX_STREAMED` body mode
  correctly.
- **Gateway API CRDs v1.4.0** — must be installed BEFORE Istio. Istio
  uses `gatewayClassName: istio` which requires these CRDs.
- **IPP from PR #386** — the `odh-stable` image does NOT include the
  `external-metering` plugin. Must build from source. PR #386 adds
  `organization_id` and `cost_center` fields required for tenant
  attribution.

## Setup Steps (Reproducible)

### 1. Create k3d cluster

```bash
# Disable Traefik to avoid Gateway API CRD conflicts
k3d cluster create cost-test \
    --port "18020:8020@loadbalancer" \
    --port "18080:8080@loadbalancer" \
    --k3s-arg "--disable=traefik@server:0"
```

### 2. Install Gateway API CRDs + Istio

```bash
# Gateway API CRDs v1.4.0
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/standard-install.yaml

# Istio 1.29.2 with inference extension
curl -sL https://istio.io/downloadIstio | ISTIO_VERSION=1.29.2 sh -
export PATH="$PWD/istio-1.29.2/bin:$PATH"

istioctl install --set profile=minimal \
    --set values.pilot.env.ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true \
    -y
```

**Critical:** `ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true` is required
for the Istio gateway to work with the IPP ext_proc EnvoyFilter.

### 3. Create namespaces

```bash
kubectl create namespace ai-gateway
kubectl label namespace ai-gateway istio-injection=enabled
kubectl create namespace cost-mgmt
```

### 4. Build and import images

```bash
# ARM Mac → amd64 k3d: MUST disable attestations
BUILD="docker buildx build --platform linux/amd64 --provenance=false --sbom=false --output type=docker"
IMPORT="docker save | docker exec -i k3d-cost-test-server-0 ctr images import -"

# llm-katan
$BUILD -t llm-katan:latest -f - /tmp <<'EOF'
FROM python:3.11-slim
RUN pip install --no-cache-dir llm-katan
EXPOSE 8000
ENTRYPOINT ["llm-katan"]
CMD ["--model", "test-model", "--backend", "echo", "--providers", "openai,anthropic", "--host", "0.0.0.0"]
EOF
docker save llm-katan:latest | docker exec -i k3d-cost-test-server-0 ctr images import -

# IPP (from PR #386 — adds organization_id to CloudEvents)
git clone --branch feat/tenant-attribution-fields --depth 1 \
  https://github.com/martinpovolny/ai-gateway-payload-processing.git \
  ~/Projects/ai-gateway-payload-processing
cd ~/Projects/ai-gateway-payload-processing
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOTOOLCHAIN=auto go build -o /tmp/bbr ./cmd

$BUILD -t ipp-metering:latest -f - /tmp <<'EOF'
FROM registry.access.redhat.com/ubi9-minimal:latest
COPY bbr /bbr
USER 1001
ENTRYPOINT ["/bbr"]
EOF
docker save ipp-metering:latest | docker exec -i k3d-cost-test-server-0 ctr images import -

# Cost consumer
cd ~/Projects/cost_ai_grid_poc
$BUILD -t cost-consumer:latest -f inventory-watcher/Containerfile inventory-watcher/
docker save cost-consumer:latest | docker exec -i k3d-cost-test-server-0 ctr images import -
```

**Image import verification:**
```bash
docker exec k3d-cost-test-server-0 ctr images check | grep -E "katan|ipp|cost"
# All should show "complete (N/N) ... true"
```

### 5. Install CRDs

```bash
kubectl apply -f ~/Projects/ai-gateway-payload-processing/config/crd/bases/
```

### 6. Create Istio Gateway

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: ai-gateway
  namespace: ai-gateway
spec:
  gatewayClassName: istio
  listeners:
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: All
```

Wait for `Programmed: True`:
```bash
kubectl wait --for=condition=Programmed gateway/ai-gateway -n ai-gateway --timeout=120s
```

### 7. Deploy llm-katan

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: llm-katan
  namespace: ai-gateway
spec:
  replicas: 1
  selector:
    matchLabels:
      app: llm-katan
  template:
    metadata:
      labels:
        app: llm-katan
    spec:
      containers:
        - name: katan
          image: docker.io/library/llm-katan:latest
          imagePullPolicy: Never
          ports:
            - containerPort: 8000
          env:
            - name: LLM_KATAN_PORT
              value: "8000"
```

**K8s env var collision:** Service named `llm-katan` creates
`LLM_KATAN_PORT=tcp://...` which conflicts with the app's config.
Must override explicitly.

### 8. Deploy cost consumer + PostgreSQL

Deploy in `cost-mgmt` namespace (no Istio injection). PostgreSQL
using `postgres:16-alpine`, consumer using our image.

### 9. Deploy IPP via Helm chart

```bash
cd ~/Projects/ai-gateway-payload-processing

cat > /tmp/ipp-values.yaml <<'EOF'
upstreamIpp:
  payloadProcessor:
    image:
      registry: docker.io/library
      repository: ipp-metering
      tag: latest
      pullPolicy: Never
    env:
    - name: GATEWAY_NAME
      value: "ai-gateway"
    - name: GATEWAY_NAMESPACE
      value: "ai-gateway"
    customConfig:
      plugins:
      - type: maas-headers-guard
      - type: body-field-to-header
        name: model-extractor
        parameters:
          fieldName: model
          headerName: X-Gateway-Model-Name
      - type: external-metering
        name: metering
        parameters:
          meteringURL: "http://cost-event-consumer.cost-mgmt.svc:8020"
          featureKey: "inference-tokens"
          source: "maas-gateway"
          failOpen: true
      - type: api-translation
      profiles:
      - name: default
        plugins:
          request:
          - pluginRef: maas-headers-guard
          - pluginRef: model-extractor
          - pluginRef: metering
          - pluginRef: api-translation
          response:
          - pluginRef: metering
          - pluginRef: api-translation
  provider:
    name: istio
    istio:
      envoyFilter:
        operation: INSERT_FIRST
  inferenceGateway:
    name: ai-gateway
EOF

helm install payload-processing deploy/payload-processing \
    --namespace ai-gateway \
    --dependency-update \
    -f /tmp/ipp-values.yaml

# Disable sidecar on IPP pod
kubectl patch deployment payload-processing -n ai-gateway --type=merge \
    -p='{"spec":{"template":{"metadata":{"annotations":{"sidecar.istio.io/inject":"false"}}}}}'
```

**What the Helm chart creates:**
- Deployment for the IPP ext_proc server (port 9004)
- Service for the IPP
- ConfigMap with the PayloadProcessorConfig
- **EnvoyFilter** targeting the Istio Gateway with `FULL_DUPLEX_STREAMED`
  body mode — this is the critical piece that wires ext_proc into Envoy

### 10. Create HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: llm-route
  namespace: ai-gateway
spec:
  parentRefs:
    - name: ai-gateway
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /v1/
      backendRefs:
        - name: llm-katan
          port: 8000
```

### 11. Test end-to-end

```bash
# From inside the mesh
kubectl run test-client -n ai-gateway --image=curlimages/curl \
    --restart=Never --command -- sleep 3600

kubectl exec -n ai-gateway test-client -c test-client -- \
  curl -s --max-time 30 \
  http://ai-gateway-istio.ai-gateway:80/v1/chat/completions \
  -H "Authorization: Bearer test-key" \
  -H "Content-Type: application/json" \
  -H "x-maas-username: test-user" \
  -H "x-maas-group: test-tenant" \
  -H "x-maas-subscription: test-tenant/premium-plan" \
  -H "x-maas-organization-id: my-org" \
  -d '{"model":"test-model","messages":[{"role":"user","content":"hello"}]}'
# Note: in production these headers are injected by Authorino, not set by the client.
# See "Test Scope" section above.
```

**Expected results:**
1. LLM response returned (echo from llm-katan)
2. IPP logs: "metering check passed" + "processing response body complete"
3. Consumer logs: `GET /customers/test-user/entitlements/inference-tokens/value` (balance check)
4. Consumer logs: `POST /api/v1/events` status 202 (CloudEvent received)
5. Consumer logs: "stored raw event" + "metered MaaS event" (tokens_in, tokens_out)

## Verified Results (2026-07-04)

### IPP logs

```
Processing
captured request headers, deferring response until body arrives...
processing request headers complete
Incoming request body chunk (EoS=false)
Incoming request body chunk (EoS=true)
parsed field from body: model=test-model
internal header captured and stripped: x-maas-username
metering check passed, customer=test-user, balance=<max>
processing request body complete
processing response headers complete
Incoming response body chunk (EoS=false)
Incoming response body chunk (EoS=true)
processing response body complete
```

### Consumer logs

```
GET /api/v1/customers/test-user/entitlements/inference-tokens/value → 200 (3ms)
stored raw event: evt-556bdc33... type=inference.tokens.used resource=Model
upserted model: test-model state=MODEL_STATE_RUNNING
metered MaaS event: tokens_in=2 tokens_out=10 requests=0
POST /api/v1/events → 202 (10ms)
```

## Known Issues

### IPP expects 200/204, we return 202

The IPP client logs `"failed to report usage to metering system:
usage report returned status 202"` but the event IS received and
processed. The IPP `client.go` considers only 200 and 204 as success.

**Fix:** Change our handler to return 200 instead of 202, or the IPP
team to accept 202.

Source: [client.go reportUsage](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/client.go)

### No Authorino in this setup

See [Test Scope](#test-scope--what-is-and-isnt-covered) for the full
breakdown of what's covered vs. skipped. Short version: we set
`x-maas-*` headers manually, emulating what Authorino would inject.

## Troubleshooting Log

### Envoy Gateway does NOT work for ext_proc

We tried Envoy Gateway (v1.4.0, v1.6.0) before switching to Istio.
The `EnvoyExtensionPolicy` was accepted but never translated into
actual ext_proc filter config in the Envoy proxy. Waste of time.

**Use Istio with the Helm chart.** That's what the IPP team uses and
tests against.

### FULL_DUPLEX_STREAMED is required

The IPP handler code always defers its response until it receives the
body. `NONE` body mode lets requests through but skips metering.
`BUFFERED` mode hangs. Only `FULL_DUPLEX_STREAMED` works — which is
what the Helm chart's EnvoyFilter template uses.

### ARM Mac image import

Docker buildx on ARM creates OCI index manifests that k3d's containerd
can't resolve. Must use `--provenance=false --sbom=false --output type=docker`
and import via `docker save | ctr images import`.

### PayloadProcessorConfig API version

```
apiVersion: llm-d.ai/v1alpha1
kind: PayloadProcessorConfig
```

NOT `config.payload-processor.llm-d.io/v1alpha1`.

## Source References

- IPP e2e setup script (the authoritative setup):
  [test/e2e/scripts/setup-kind.sh](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/main/test/e2e/scripts/setup-kind.sh)
- IPP Helm chart values:
  [deploy/payload-processing/values.yaml](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/main/deploy/payload-processing/values.yaml)
- Istio EnvoyFilter template:
  [config/charts/payload-processor/templates/istio.yaml](https://github.com/llm-d/llm-d-inference-payload-processor/blob/main/config/charts/payload-processor/templates/istio.yaml)
- External-metering plugin:
  [pkg/plugins/external-metering/plugin.go](https://github.com/opendatahub-io/ai-gateway-payload-processing/blob/61b6160/pkg/plugins/external-metering/plugin.go)
- MaaS flow diagram: [docs/maas-flow.md](../maas-flow.md)

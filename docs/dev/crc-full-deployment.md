# Full CRC Deployment Guide

Complete step-by-step guide to deploy the entire stack (OSAC + cost-event-consumer) on CRC.

## Prerequisites

### Required Tools

- **CRC 4.18+**: https://developers.redhat.com/products/openshift-local/overview
- **kubectl or oc CLI**: Comes with CRC (`eval $(crc oc-env)`)
- **helm 3.8+**: https://helm.sh/docs/intro/install/
- **jq**: https://stedolan.github.io/jq/download/
- **openssl**: Usually pre-installed on macOS/Linux

### CRC Setup

```bash
# Verify CRC is running
crc status

# If not running, start it
crc start

# Configure environment
eval $(crc oc-env)

# Login as admin (password from: crc console --credentials)
oc login -u kubeadmin https://api.crc.testing:6443
```

### Repository Setup

```bash
git clone https://github.com/myersCody/cost_ai_grid_poc.git
cd cost_ai_grid_poc
```

## Architecture

```
cert-manager (cluster-wide)
  └─> trust-manager → CA bundle distribution
  └─> ClusterIssuer (osac-ca, self-signed)

CloudNativePG operator (postgres namespace)
  └─> PostgreSQL cluster (2 replicas)
      - osac-1, osac-2 (for OSAC)

OSAC stack (osac namespace)
  ├─> osac-oidc: Python OIDC server (port 8013)
  ├─> osac-grpc: gRPC API (port 8010)
  └─> osac-rest: REST gateway (port 8000)

Cost Management (cost-mgmt namespace)
  ├─> cost-db: PostgreSQL (port 5432)
  └─> cost-event-consumer: Consumer app (ports 8020, 9000)
```

## Step 1: Install cert-manager

```bash
helm upgrade cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --install \
  --version v1.20.0 \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true \
  --wait
```

## Step 2: Install trust-manager

```bash
helm upgrade trust-manager oci://quay.io/jetstack/charts/trust-manager \
  --install \
  --version v0.22.0 \
  --namespace cert-manager \
  --set defaultPackage.enabled=false \
  --wait
```

## Step 3: Create Self-Signed CA

```bash
# Create self-signed issuer
oc apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  namespace: cert-manager
  name: osac-ca
spec:
  selfSigned: {}
EOF

# Create CA certificate
oc apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: cert-manager
  name: osac-ca
spec:
  commonName: OSAC CA
  isCA: true
  issuerRef:
    kind: Issuer
    name: osac-ca
  secretName: osac-ca
  privateKey:
    rotationPolicy: Always
EOF

# Create ClusterIssuer
oc apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: osac-ca
spec:
  ca:
    secretName: osac-ca
EOF

# Create CA bundle for distribution
oc apply -f - <<'EOF'
apiVersion: trust.cert-manager.io/v1alpha1
kind: Bundle
metadata:
  name: ca-bundle
spec:
  sources:
  - secret:
      name: osac-ca
      key: tls.crt
  target:
    configMap:
      key: bundle.pem
    namespaceSelector:
      matchExpressions:
      - key: kubernetes.io/metadata.name
        operator: In
        values:
        - osac
        - postgres
EOF
```

## Step 4: Install CloudNativePG Operator

```bash
# Create namespace
oc new-project postgres

# Grant SCC
oc adm policy add-scc-to-user nonroot-v2 -z cnpg-cloudnative-pg -n postgres

# Install operator
helm upgrade cnpg oci://ghcr.io/cloudnative-pg/charts/cloudnative-pg \
  --install \
  --version 0.28.0 \
  --namespace postgres \
  --wait
```

## Step 5: Create OSAC PostgreSQL Cluster

```bash
# Create credentials with fixed passwords.
# Fixed (not random) so the gRPC connection string matches without waiting
# for CNPG to reconcile a rotation — fine for a PoC/dev environment.
oc create secret generic -n postgres osac-keycloak-credentials \
  --type=kubernetes.io/basic-auth \
  --from-literal=username=keycloak \
  --from-literal=password=osac-keycloak-dev

oc create secret generic -n postgres osac-service-credentials \
  --type=kubernetes.io/basic-auth \
  --from-literal=username=service \
  --from-literal=password=osac-service-dev

# Create TLS certificate
oc apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: postgres
  name: osac-server-tls
spec:
  issuerRef:
    kind: ClusterIssuer
    name: osac-ca
  dnsNames:
  - osac-rw
  - osac-rw.postgres
  - osac-rw.postgres.svc
  - osac-rw.postgres.svc.cluster.local
  - osac-r
  - osac-r.postgres.svc
  - osac-ro
  - osac-ro.postgres.svc
  secretName: osac-server-tls
  secretTemplate:
    labels:
      cnpg.io/reload: ""
  privateKey:
    rotationPolicy: Always
EOF

# Create PostgreSQL cluster
oc apply -f - <<'EOF'
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  namespace: postgres
  name: osac
spec:
  instances: 2
  certificates:
    serverTLSSecret: osac-server-tls
    serverCASecret: osac-server-tls
  bootstrap:
    initdb:
      database: keycloak
      owner: keycloak
      secret:
        name: osac-keycloak-credentials
      postInitSQL:
      - create role service login
      - create database service owner service
  managed:
    roles:
    - name: service
      ensure: present
      login: true
      passwordSecret:
        name: osac-service-credentials
  storage:
    size: 10Gi
EOF

# Wait for PostgreSQL cluster
oc wait pods -n postgres \
  --selector cnpg.io/podRole=instance,cnpg.io/cluster=osac \
  --for=condition=Ready \
  --timeout=300s
```

## Step 6: Deploy OSAC Stack

```bash
# Create namespace first
oc new-project osac

POSTGRES_SERVICE=service
POSTGRES_PASSWORD=osac-service-dev

# Create TLS certificates — one for gRPC, one for the OIDC server.
# IMPORTANT: osac-oidc-tls MUST be separate from osac-grpc-tls.
# The OIDC pod serves HTTPS at osac-oidc.osac.svc:8013; if its cert
# doesn't include that SAN, the gRPC server's JWKS fetch fails and
# every token is rejected with 401. See troubleshooting.md for details.
oc apply -f - <<'EOF'
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: osac
  name: osac-grpc-tls
spec:
  issuerRef:
    kind: ClusterIssuer
    name: osac-ca
  dnsNames:
    - osac-grpc.osac.svc.cluster.local
    - osac-grpc.osac.svc
    - osac-grpc
  secretName: osac-grpc-tls
  privateKey:
    rotationPolicy: Always
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  namespace: osac
  name: osac-oidc-tls
spec:
  issuerRef:
    kind: ClusterIssuer
    name: osac-ca
  dnsNames:
    - osac-oidc.osac.svc.cluster.local
    - osac-oidc.osac.svc
    - osac-oidc.osac
    - osac-oidc
  secretName: osac-oidc-tls
  privateKey:
    rotationPolicy: Always
EOF

# Wait for both certs to be issued
oc wait certificate osac-grpc-tls osac-oidc-tls -n osac --for=condition=Ready --timeout=60s

# Deploy the OIDC server (uses osac-oidc-tls — see cert above)
kubectl apply -f deploy/k8s/osac-oidc-fixed.yaml

# Deploy OSAC gRPC server
oc apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: osac-grpc
  namespace: osac
spec:
  replicas: 1
  selector:
    matchLabels:
      app: osac-grpc
  template:
    metadata:
      labels:
        app: osac-grpc
    spec:
      containers:
        - name: grpc
          image: ghcr.io/osac-project/fulfillment-service:main
          command:
            - /usr/local/bin/fulfillment-service
            - start
            - grpc-server
            - --log-level=info
            - --grpc-listener-address=0.0.0.0:8010
            - --grpc-listener-tls-crt=/certs/tls.crt
            - --grpc-listener-tls-key=/certs/tls.key
            - --ca-file=/ca-bundle/bundle.pem
            - --db-url=postgres://${POSTGRES_SERVICE}:${POSTGRES_PASSWORD}@osac-rw.postgres.svc.cluster.local:5432/service?sslmode=require
            - --token-issuer=https://osac-grpc.osac.svc.cluster.local:8010
            - --token-signer-key=/certs/tls.key
            - --token-signer-crt=/certs/tls.crt
            - --token-encryption-crt=/certs/tls.crt
            - --grpc-authn-trusted-token-issuers=https://osac-oidc.osac.svc:8013
          ports:
            - containerPort: 8010
          volumeMounts:
            - name: certs
              mountPath: /certs
            - name: ca-bundle
              mountPath: /ca-bundle
      volumes:
        - name: certs
          secret:
            secretName: osac-grpc-tls
        - name: ca-bundle
          configMap:
            name: ca-bundle
---
apiVersion: v1
kind: Service
metadata:
  name: osac-grpc
  namespace: osac
spec:
  ports:
    - port: 8010
  selector:
    app: osac-grpc
EOF

# Deploy OSAC REST gateway
oc apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: osac-rest
  namespace: osac
spec:
  replicas: 1
  selector:
    matchLabels:
      app: osac-rest
  template:
    metadata:
      labels:
        app: osac-rest
    spec:
      containers:
        - name: rest
          image: ghcr.io/osac-project/fulfillment-service:main
          command:
            - /usr/local/bin/fulfillment-service
            - start
            - rest-gateway
            - --log-level=info
            - --http-listener-address=0.0.0.0:8000
            - --grpc-server-address=osac-grpc.osac.svc.cluster.local:8010
            - --ca-file=/ca-bundle/bundle.pem
            - --metrics-listener-address=0.0.0.0:8012
          ports:
            - containerPort: 8000
            - containerPort: 8012
          volumeMounts:
            - name: ca-bundle
              mountPath: /ca-bundle
      volumes:
        - name: ca-bundle
          configMap:
            name: ca-bundle
---
apiVersion: v1
kind: Service
metadata:
  name: osac-rest
  namespace: osac
spec:
  ports:
    - name: http
      port: 8000
    - name: metrics
      port: 8012
  selector:
    app: osac-rest
EOF
```

## Step 7: Deploy Cost Management Stack

```bash
# Create namespace
oc new-project cost-mgmt

# Deploy PostgreSQL for cost-event-consumer
oc apply -f - <<'EOF'
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: cost-db
  namespace: cost-mgmt
spec:
  serviceName: cost-db
  replicas: 1
  selector:
    matchLabels:
      app: cost-db
  template:
    metadata:
      labels:
        app: cost-db
    spec:
      containers:
        - name: postgres
          image: docker.io/library/postgres:16
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRES_USER
              value: user
            - name: POSTGRES_PASSWORD
              value: pass
            - name: POSTGRES_DB
              value: costdb
            - name: PGDATA
              value: /var/lib/postgresql/data/pgdata
          livenessProbe:
            exec:
              command: ["pg_isready", "-U", "user"]
            initialDelaySeconds: 10
            periodSeconds: 10
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "user"]
            initialDelaySeconds: 5
            periodSeconds: 5
          volumeMounts:
            - name: pgdata
              mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
    - metadata:
        name: pgdata
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 1Gi
---
apiVersion: v1
kind: Service
metadata:
  name: cost-db
  namespace: cost-mgmt
spec:
  ports:
    - port: 5432
  selector:
    app: cost-db
EOF

# Create secrets
oc apply -f - <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: cost-db-credentials
  namespace: cost-mgmt
type: Opaque
stringData:
  user: "user"
  password: "pass"
  connection-url: "postgres://user:pass@cost-db:5432/costdb"
---
apiVersion: v1
kind: Secret
metadata:
  name: cost-consumer-secrets
  namespace: cost-mgmt
type: Opaque
stringData:
  osac-token: "dummy-token-for-testing"
EOF

# Deploy cost-event-consumer
oc apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cost-event-consumer
  namespace: cost-mgmt
spec:
  replicas: 1
  selector:
    matchLabels:
      app: cost-event-consumer
  template:
    metadata:
      labels:
        app: cost-event-consumer
    spec:
      initContainers:
        - name: wait-for-db
          image: busybox:1.37
          command: ['sh', '-c', 'until nc -z cost-db 5432; do echo "waiting for cost-db"; sleep 2; done']
      containers:
        - name: consumer
          image: quay.io/martin_povolny/cost-event-consumer:latest
          imagePullPolicy: Always
          ports:
            - name: http
              containerPort: 8020
            - name: metrics
              containerPort: 9000
          env:
            - name: OSAC_BASE_URL
              value: "http://osac-rest.osac.svc:8000"
            - name: OSAC_TOKEN
              valueFrom:
                secretKeyRef:
                  name: cost-consumer-secrets
                  key: osac-token
            - name: INVENTORY_DB_URL
              valueFrom:
                secretKeyRef:
                  name: cost-db-credentials
                  key: connection-url
            - name: INGEST_LISTEN_ADDR
              value: ":8020"
            - name: LOG_FORMAT
              value: "json"
            - name: LOG_LEVEL
              value: "info"
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 5
          resources:
            requests:
              memory: "64Mi"
              cpu: "100m"
            limits:
              memory: "256Mi"
              cpu: "500m"
---
apiVersion: v1
kind: Service
metadata:
  name: cost-event-consumer
  namespace: cost-mgmt
spec:
  ports:
    - name: http
      port: 8020
    - name: metrics
      port: 9000
  selector:
    app: cost-event-consumer
EOF
```

## Step 8: Generate OSAC Token

Tokens are signed with the **osac-oidc-tls** private key and expire after 90 days.
CRC restart does **not** require a token refresh — Secrets persist across VM
suspend/resume. You need to re-run this script when:

- The token expires (90 days after last run), or
- cert-manager rotates `osac-oidc-tls` (~90-day default) — the signing key
  changes on rotation, invalidating any existing token.

See [troubleshooting.md](troubleshooting.md) for the full explanation of why
the key must be `osac-oidc-tls`, not `osac-grpc-tls`.

```bash
# Requires: pip install cryptography pyjwt
./scripts/refresh-token.sh
```

The script extracts the signing key, generates a JWT, patches the
`cost-consumer-secrets` Secret, and restarts the consumer pod.

To inspect the token without applying it:

```bash
./scripts/refresh-token.sh --dry-run
```

## Step 9 (Optional): MaaS Inference Metering Stack

Deploys the AI gateway (Istio + IPP) to test the full MaaS inference metering
pipeline: requests flow through the gateway, the IPP calls our consumer for
balance checks and reports token usage as CloudEvents.

**Prerequisites:** ~2.5 GB free RAM on the CRC node. Scale down or delete
unused namespaces first if needed:
```bash
kubectl top nodes   # check — target < 75% memory before proceeding
```

### 9a. Install Istio 1.29.2

```bash
# Download istioctl
curl -sL "https://github.com/istio/istio/releases/download/1.29.2/istioctl-1.29.2-osx-arm64.tar.gz" \
  | tar xz -C /tmp

# Install with OpenShift profile
eval $(crc oc-env)
/tmp/istioctl install --set profile=openshift --set values.global.platform=openshift -y

# Grant SCCs
oc adm policy add-scc-to-group privileged system:serviceaccounts:istio-system

# Verify
kubectl get pods -n istio-system
# Expected: istiod Running, istio-cni-node Running
```

> **Note:** OpenShift manages Gateway API CRDs (v1.3.0) via its Ingress Operator
> — do not apply the upstream v1.4.0 install YAML, it will be rejected.
> The `istio` GatewayClass is automatically created and accepted.

### 9b. Apply IPP CRDs and Create Namespace

```bash
kubectl apply -f ~/Projects/koku/ai-gateway-payload-processing/config/crd/bases/

kubectl create namespace ai-gateway
kubectl label namespace ai-gateway istio-injection=enabled
oc adm policy add-scc-to-group anyuid system:serviceaccounts:ai-gateway
```

### 9c. Create Istio Gateway

```bash
kubectl apply -f - <<'EOF'
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
EOF

kubectl wait --for=condition=Accepted gateway/ai-gateway -n ai-gateway --timeout=60s
```

### 9d. Deploy llm-katan (Echo LLM)

```bash
kubectl apply -f - <<'EOF'
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
        - name: llm-katan
          image: quay.io/martin_povolny/llm-katan:latest
          imagePullPolicy: Always
          env:
            - name: LLM_KATAN_PORT
              value: "8000"
          ports:
            - containerPort: 8000
---
apiVersion: v1
kind: Service
metadata:
  name: llm-katan
  namespace: ai-gateway
spec:
  ports:
    - port: 8000
  selector:
    app: llm-katan
EOF
```

> **Note:** `LLM_KATAN_PORT=8000` must be set explicitly — Kubernetes injects a
> `LLM_KATAN_PORT` env var with the service URL, which crashes llm-katan.

### 9e. Deploy IPP via Helm

```bash
cat > /tmp/ipp-values.yaml <<'EOF'
upstreamIpp:
  payloadProcessor:
    image:
      registry: quay.io/martin_povolny
      repository: ipp-metering
      tag: pr-386
      pullPolicy: Always
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

helm install payload-processing \
  ~/Projects/koku/ai-gateway-payload-processing/deploy/payload-processing \
  --namespace ai-gateway \
  --dependency-update \
  -f /tmp/ipp-values.yaml

# Disable Istio sidecar on IPP (it manages its own Envoy)
kubectl patch deployment payload-processing -n ai-gateway --type=merge \
  -p='{"spec":{"template":{"metadata":{"annotations":{"sidecar.istio.io/inject":"false"}}}}}'
```

### 9f. Create HTTPRoute and Verify

```bash
kubectl apply -f - <<'EOF'
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
EOF

# Wait for all pods
kubectl get pods -n ai-gateway
# Expected: ai-gateway-istio, llm-katan, payload-processing all Running

# End-to-end test
GATEWAY_IP=$(kubectl get svc -n ai-gateway ai-gateway-istio -o jsonpath='{.spec.clusterIP}')
kubectl run curl-test --image=curlimages/curl:latest --rm -i --restart=Never -n ai-gateway -- \
  curl -s -X POST "http://${GATEWAY_IP}/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-key" \
  -H "x-maas-username: tenant-1" \
  -H "x-maas-organization-id: org-1" \
  -H "x-maas-cost-center: cc-engineering" \
  -d '{"model":"test-model","messages":[{"role":"user","content":"hello"}]}'
```

Expected response: `[echo] model=test-model ...` from llm-katan.

Check consumer received the metering event:
```bash
kubectl logs -n cost-mgmt deployment/cost-event-consumer --tail=5 | grep "http request"
# Expected:
# GET /api/v1/customers/tenant-1/entitlements/inference-tokens/value  200
# POST /api/v1/events  204
```

### Cleanup (Step 9)

```bash
helm uninstall payload-processing -n ai-gateway
kubectl delete namespace ai-gateway
/tmp/istioctl uninstall --purge -y
kubectl delete namespace istio-system
kubectl delete crd $(kubectl get crd | grep istio.io | awk '{print $1}')
```

## Verification

```bash
# Check all pods
kubectl get pods --all-namespaces | grep -E "NAMESPACE|osac|postgres|cert-manager|cost-mgmt"

# Check OSAC services
kubectl get svc -n osac

# Check consumer logs — should show reconciliation, not 401 loops
kubectl logs -n cost-mgmt -l app=cost-event-consumer --tail=20
```

Expected: all pods `Running`, consumer logs showing `reconciliation complete` with no `token is not valid` errors.

## Cleanup

To remove everything:

```bash
# Delete applications
kubectl delete namespace osac cost-mgmt

# Delete PostgreSQL
kubectl delete cluster -n postgres osac
kubectl delete namespace postgres

# Delete operators
helm uninstall cnpg -n postgres
helm uninstall trust-manager -n cert-manager
helm uninstall cert-manager -n cert-manager

# Delete CRDs (optional - only if fully removing)
kubectl delete crd -l app.kubernetes.io/name=cert-manager
kubectl delete crd clusters.postgresql.cnpg.io
```

## Troubleshooting

**PostgreSQL migrations dirty:**
- Use CloudNativePG operator (not plain postgres:16)
- Migrations will run cleanly on first boot

**OSAC OIDC pod crashing:**
- Verify cryptography is installed in startup command
- Check cert mount at /certs

**Consumer 401 errors:**
- Run `scripts/refresh-token.sh` to generate and apply a real token
- See [troubleshooting.md](troubleshooting.md) for root-cause details

**CRC resource constraints:**
- Stop unnecessary pods
- Reduce replica counts if needed
- This simplified stack uses ~12 pods total

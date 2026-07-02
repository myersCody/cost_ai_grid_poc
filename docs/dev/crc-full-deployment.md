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
# Clone the repository
cd ~/Projects/koku
git clone <repo-url> cost_ai_grid_poc
cd cost_ai_grid_poc

# Checkout the deployment branch
git checkout openshift-deployment
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
# Create credentials
oc create secret generic -n postgres osac-keycloak-credentials \
  --type=kubernetes.io/basic-auth \
  --from-literal=username=keycloak \
  --from-literal=password="$(openssl rand -base64 18)"

oc create secret generic -n postgres osac-service-credentials \
  --type=kubernetes.io/basic-auth \
  --from-literal=username=service \
  --from-literal=password="$(openssl rand -base64 18)"

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
# Get DB password
POSTGRES_SERVICE=$(oc get secret -n postgres osac-service-credentials -o json | jq -r '.data["username"] | @base64d')
POSTGRES_PASSWORD=$(oc get secret -n postgres osac-service-credentials -o json | jq -r '.data["password"] | @base64d')

# Apply OSAC manifests
cd ~/Projects/koku/cost_ai_grid_poc
kubectl apply -f deploy/k8s/osac-oidc-fixed.yaml

# Create namespace
oc new-project osac

# Create gRPC TLS certificate
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
EOF

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

## Verification

```bash
# Check all pods
kubectl get pods --all-namespaces | grep -E "NAMESPACE|osac|postgres|cert-manager|cost-mgmt"

# Check OSAC services
kubectl get svc -n osac

# Check consumer logs
kubectl logs -n cost-mgmt -l app=cost-event-consumer --tail=20
```

Expected output:
- All pods in Running state
- Consumer showing 401 errors (token not valid) - this is expected with dummy token

## Generate OSAC Token (Optional)

To test with real authentication:

```bash
# Use the gen_token.py script from inventory-watcher
cd ~/Projects/koku/cost_ai_grid_poc/inventory-watcher
python3 scripts/gen_token.py > /tmp/osac_token.txt

# Update the secret
kubectl create secret generic -n cost-mgmt cost-consumer-secrets \
  --from-literal=osac-token="$(cat /tmp/osac_token.txt)" \
  --dry-run=client -o yaml | kubectl apply -f -

# Restart consumer to pick up new token
kubectl delete pod -n cost-mgmt -l app=cost-event-consumer
```

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
- Expected with dummy token
- Generate real token with gen_token.py script

**CRC resource constraints:**
- Stop unnecessary pods
- Reduce replica counts if needed
- This simplified stack uses ~12 pods total

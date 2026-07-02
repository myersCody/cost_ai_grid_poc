# Deployment Plan — CRC / OpenShift

> Initial plan for deploying the cost-event-consumer to CRC (CodeReady
> Containers) for development/testing, with a path to production OpenShift.
> This is a living document — we'll detail and expand as we move forward.

## Goal

Deploy the full test stack in CRC:
1. Cost-event-consumer (our service)
2. PostgreSQL (cost database)
3. OSAC fulfillment-service (event source)
4. Supporting infrastructure (Keycloak, cert-manager)

Images published to **quay.io** for easy access from any cluster.

## Reference Deployments

| Project | Chart Location | Pattern |
|---------|---------------|---------|
| OSAC fulfillment-service | `fulfillment-service/charts/service/` | Multi-deployment (gRPC server, REST gateway, controller), cert-manager TLS, Keycloak auth |
| Koku cost-onprem | `koku/cost-onprem-chart/cost-onprem/` | Full stack Helm chart: API + Celery workers + DB + Kafka + Valkey + RBAC + UI + gateway |
| Koku cost-onprem DB | `templates/infrastructure/database/` | StatefulSet with init scripts, pg_isready probes, PVC, multi-user setup |
| Koku ServiceMonitor | `templates/monitoring/servicemonitor.yaml` | Per-component ServiceMonitors, configurable scrape interval |

## Phase 1: Container Image (Immediate)

### Containerfile

Multi-stage build following the OSAC fulfillment-service pattern (UBI10 base):

```dockerfile
# Build stage
FROM registry.access.redhat.com/ubi10/go-toolset:1.24 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o cost-event-consumer ./cmd/consumer/
RUN CGO_ENABLED=0 go build -o maas-simulator ./cmd/maas-simulator/

# Runtime stage
FROM registry.access.redhat.com/ubi10-minimal:latest
COPY --from=builder /build/cost-event-consumer /usr/local/bin/
COPY --from=builder /build/maas-simulator /usr/local/bin/
USER 1001
EXPOSE 8020 9000
ENTRYPOINT ["cost-event-consumer"]
```

### Image Registry

```
quay.io/cost-mgmt/cost-event-consumer:latest
quay.io/cost-mgmt/cost-event-consumer:<git-sha>
```

### Build & Push

```bash
podman build -t quay.io/cost-mgmt/cost-event-consumer:latest \
  -f Containerfile inventory-watcher/
podman push quay.io/cost-mgmt/cost-event-consumer:latest
```

## Phase 2: Minimal K8s Deployment (CRC)

Start with the simplest deployment that works — a single Pod with
PostgreSQL. No Helm chart yet, just plain manifests.

### `deploy/k8s/namespace.yaml`
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: cost-mgmt
```

### `deploy/k8s/postgres.yaml`

Follow the cost-onprem pattern — StatefulSet with pg_isready probes:

```yaml
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
    spec:
      containers:
        - name: postgres
          image: quay.io/sclorg/postgresql-18-c10s:latest
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRESQL_USER
              valueFrom:
                secretKeyRef:
                  name: cost-db-credentials
                  key: user
            - name: POSTGRESQL_PASSWORD
              valueFrom:
                secretKeyRef:
                  name: cost-db-credentials
                  key: password
            - name: POSTGRESQL_DATABASE
              value: costdb
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
              mountPath: /var/lib/pgsql/data
  volumeClaimTemplates:
    - metadata:
        name: pgdata
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 1Gi
```

### `deploy/k8s/consumer.yaml`

```yaml
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
          image: quay.io/cost-mgmt/cost-event-consumer:latest
          ports:
            - name: http
              containerPort: 8020
            - name: metrics
              containerPort: 9000
          env:
            - name: OSAC_BASE_URL
              value: "http://fulfillment-rest-gateway.osac.svc:8000"
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
  labels:
    app: cost-event-consumer
spec:
  ports:
    - name: http
      port: 8020
    - name: metrics
      port: 9000
  selector:
    app: cost-event-consumer
```

## Phase 3: OSAC in CRC

OSAC fulfillment-service has a production Helm chart. For CRC, deploy
the minimum viable set:

```bash
# Prerequisites (one-time, in CRC)
# cert-manager, trust-manager, Keycloak operator, PostgreSQL

# Deploy OSAC
helm install fulfillment-service \
  /path/to/fulfillment-service/charts/service/ \
  --namespace osac \
  --set variant=openshift \
  --set images.service=ghcr.io/osac-project/charts/fulfillment-service:main \
  -f osac-crc-values.yaml
```

We need a `osac-crc-values.yaml` with minimal config (single replica,
small resources, self-signed certs).

### Dependency chain

```
cert-manager → TLS certs
    ↓
Keycloak → OIDC tokens
    ↓
PostgreSQL → OSAC DB + Cost DB
    ↓
OSAC fulfillment-service → Watch stream + REST APIs
    ↓
cost-event-consumer → our service
```

## Phase 4: Helm Chart (Later)

Once the plain manifests work in CRC, extract a proper Helm chart:

```
charts/cost-event-consumer/
  Chart.yaml
  values.yaml
  templates/
    deployment.yaml
    service.yaml
    configmap.yaml
    secret.yaml
    servicemonitor.yaml
    pdb.yaml
```

Follow the cost-onprem patterns:
- Configurable probes via `values.yaml`
- ServiceMonitor with configurable scrape interval
- SecurityContext (nonRoot, read-only root fs)
- Init container to wait for PostgreSQL
- Optional: deploy PostgreSQL as StatefulSet or use external DB

## Implementation Order

| Step | What | Effort | Blocked by |
|------|------|--------|------------|
| 1 | Write Containerfile, build & push to quay.io | Small | — |
| 2 | Add `/healthz` and `/readyz` endpoints | Small | — |
| 3 | Create `deploy/k8s/` manifests (namespace, secret, PG, consumer) | Small | Step 1 |
| 4 | Deploy PostgreSQL + consumer in CRC | Small | Step 3 |
| 5 | Deploy OSAC fulfillment-service in CRC | Medium | CRC setup, cert-manager, Keycloak |
| 6 | Test end-to-end in CRC | — | Steps 4-5 |
| 7 | Add Prometheus metrics endpoint | Medium | — |
| 8 | Extract Helm chart | Medium | Steps 3-6 validated |

Steps 1-4 can be done without OSAC — the consumer starts, creates tables,
and the dashboard/report APIs work. OSAC adds the Watch stream and
reconciler data.

## Quick Start for CRC

```bash
# 1. Start CRC
crc start

# 2. Login
eval $(crc oc-env)
oc login -u developer

# 3. Create namespace
oc new-project cost-mgmt

# 4. Create secrets
oc create secret generic cost-db-credentials \
  --from-literal=user=costuser \
  --from-literal=password=costpass \
  --from-literal=connection-url="postgres://costuser:costpass@cost-db:5432/costdb"

# 5. Deploy PostgreSQL
oc apply -f deploy/k8s/postgres.yaml

# 6. Build & push image (or use pre-built)
podman build -t quay.io/cost-mgmt/cost-event-consumer:latest \
  -f Containerfile inventory-watcher/
podman push quay.io/cost-mgmt/cost-event-consumer:latest

# 7. Deploy consumer
oc apply -f deploy/k8s/consumer.yaml

# 8. Access dashboard
oc port-forward svc/cost-event-consumer 8020:8020
open http://localhost:8020/debug/dashboard
```

## Notes

- **CRC resource limits:** CRC defaults to 4 vCPU / 9 GiB RAM. The full
  OSAC stack (PostgreSQL + Keycloak + fulfillment-service + our consumer)
  may need `crc config set memory 12288` or more.
- **Image pull:** If using a private quay.io repo, create an image pull
  secret: `oc create secret docker-registry quay-pull ...`
- **OSAC token in K8s:** The token expires. For CRC testing, we can
  mount the gen_token.py script as an init container or use a long-lived
  service account token from Keycloak.

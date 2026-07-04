#!/usr/bin/env bash
set -euo pipefail

# Deploy cost-event-consumer to k3s.
# Expects: kubectl configured, /tmp/osac_token.txt with valid token.

echo "--- Deploying cost-event-consumer ---"

kubectl create namespace cost-mgmt --dry-run=client -o yaml | kubectl apply -f -

OSAC_TOKEN=$(cat /tmp/osac_token.txt)

kubectl create secret generic cost-consumer-secrets \
    --namespace=cost-mgmt \
    --from-literal=osac-token="$OSAC_TOKEN" \
    --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic cost-db-credentials \
    --namespace=cost-mgmt \
    --from-literal=connection-url="postgresql://costuser:costpass@cost-db:5432/costdb?sslmode=disable" \
    --dry-run=client -o yaml | kubectl apply -f -

cat <<'K8S' | kubectl apply -f -
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
          image: postgres:16
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRES_USER
              value: costuser
            - name: POSTGRES_PASSWORD
              value: costpass
            - name: POSTGRES_DB
              value: costdb
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "costuser"]
            initialDelaySeconds: 5
            periodSeconds: 5
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
K8S

echo "Waiting for cost PostgreSQL..."
kubectl wait --for=condition=ready pod -l app=cost-db -n cost-mgmt --timeout=180s

cat <<'K8S' | kubectl apply -f -
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
          image: cost-event-consumer:ci
          imagePullPolicy: Never
          ports:
            - name: http
              containerPort: 8020
            - name: metrics
              containerPort: 9000
          env:
            - name: OSAC_BASE_URL
              value: "http://osac-rest.osac.svc:8011"
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
            - name: RECONCILE_INTERVAL
              value: "2m"
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
K8S

echo "Cost-consumer deployment applied"

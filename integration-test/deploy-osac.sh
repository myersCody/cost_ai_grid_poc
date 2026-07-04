#!/usr/bin/env bash
set -euo pipefail

# Deploy OSAC stack to k3s: PostgreSQL, OIDC mock, gRPC server, REST gateway.
# Expects: kubectl configured, /tmp/fulfillment-service/ with certs.

echo "--- Deploying OSAC ---"

kubectl create namespace osac --dry-run=client -o yaml | kubectl apply -f -

# TLS secret
kubectl create secret generic osac-tls \
    --namespace=osac \
    --from-file=server.crt=/tmp/fulfillment-service/server.crt \
    --from-file=server.key=/tmp/fulfillment-service/server.key \
    --dry-run=client -o yaml | kubectl apply -f -

# PostgreSQL
cat <<'K8S' | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: osac-db-credentials
  namespace: osac
type: Opaque
stringData:
  connection-url: "postgresql://osacuser:osacpass@osac-db:5432/osacdb?sslmode=disable"
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: osac-db
  namespace: osac
spec:
  serviceName: osac-db
  replicas: 1
  selector:
    matchLabels:
      app: osac-db
  template:
    metadata:
      labels:
        app: osac-db
    spec:
      containers:
        - name: postgres
          image: postgres:16
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRES_USER
              value: osacuser
            - name: POSTGRES_PASSWORD
              value: osacpass
            - name: POSTGRES_DB
              value: osacdb
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "osacuser"]
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
  name: osac-db
  namespace: osac
spec:
  ports:
    - port: 5432
  selector:
    app: osac-db
K8S

echo "Waiting for OSAC PostgreSQL..."
kubectl wait --for=condition=ready pod -l app=osac-db -n osac --timeout=180s

# OIDC mock server
cat > /tmp/oidc_server.py << 'PYEOF'
#!/usr/bin/env python3
import json, hashlib, base64, ssl, http.server, os
from cryptography.hazmat.primitives import serialization

ISSUER = "https://osac-oidc:8013"
PORT = 8013

with open(os.path.join("/certs", "server.key"), "rb") as f:
    private_key = serialization.load_pem_private_key(f.read(), password=None)

pub = private_key.public_key().public_numbers()

def b64url(n):
    length = (n.bit_length() + 7) // 8
    return base64.urlsafe_b64encode(n.to_bytes(length, "big")).rstrip(b"=").decode()

jwk_data = {"e": b64url(pub.e), "kty": "RSA", "n": b64url(pub.n)}
thumbprint = json.dumps(jwk_data, separators=(",", ":"), sort_keys=True)
kid = base64.urlsafe_b64encode(hashlib.sha256(thumbprint.encode()).digest()).rstrip(b"=").decode()

OPENID_CONFIG = json.dumps({
    "issuer": ISSUER,
    "jwks_uri": f"{ISSUER}/.well-known/jwks.json",
    "token_endpoint": f"{ISSUER}/token",
    "authorization_endpoint": f"{ISSUER}/authorize",
})
JWKS = json.dumps({"keys": [{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
                              "n": b64url(pub.n), "e": b64url(pub.e)}]})

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/.well-known/openid-configuration":
            body = OPENID_CONFIG
        elif self.path == "/.well-known/jwks.json":
            body = JWKS
        else:
            self.send_response(404); self.end_headers(); return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body.encode())
    def log_message(self, format, *args): pass

ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain("/certs/server.crt", "/certs/server.key")
server = http.server.HTTPServer(("0.0.0.0", PORT), Handler)
server.socket = ctx.wrap_socket(server.socket, server_side=True)
print(f"OIDC server listening on https://0.0.0.0:{PORT}")
server.serve_forever()
PYEOF

kubectl create configmap osac-oidc-script \
    --namespace=osac \
    --from-file=oidc_server.py=/tmp/oidc_server.py \
    --dry-run=client -o yaml | kubectl apply -f -

# OIDC + gRPC + REST deployments
cat <<'K8S' | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: osac-oidc
  namespace: osac
spec:
  replicas: 1
  selector:
    matchLabels:
      app: osac-oidc
  template:
    metadata:
      labels:
        app: osac-oidc
    spec:
      containers:
        - name: oidc
          image: python:3.11-slim
          command: ["sh", "-c", "pip install cryptography -q && python3 /scripts/oidc_server.py"]
          ports:
            - containerPort: 8013
          volumeMounts:
            - name: certs
              mountPath: /certs
              readOnly: true
            - name: scripts
              mountPath: /scripts
              readOnly: true
      volumes:
        - name: certs
          secret:
            secretName: osac-tls
        - name: scripts
          configMap:
            name: osac-oidc-script
---
apiVersion: v1
kind: Service
metadata:
  name: osac-oidc
  namespace: osac
spec:
  ports:
    - port: 8013
  selector:
    app: osac-oidc
---
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
          image: osac-fulfillment-service:ci
          imagePullPolicy: Never
          args:
            - start
            - grpc-server
            - --log-level=info
            - --grpc-listener-address=0.0.0.0:8010
            - --grpc-listener-tls-crt=/certs/server.crt
            - --grpc-listener-tls-key=/certs/server.key
            - --ca-file=/certs/server.crt
            - --db-url=postgresql://osacuser:osacpass@osac-db:5432/osacdb?sslmode=disable
            - --token-issuer=https://osac-oidc:8013
            - --token-signer-key=/certs/server.key
            - --token-signer-crt=/certs/server.crt
            - --token-encryption-crt=/certs/server.crt
            - --grpc-authn-trusted-token-issuers=https://osac-oidc:8013
          ports:
            - containerPort: 8010
          volumeMounts:
            - name: certs
              mountPath: /certs
              readOnly: true
      volumes:
        - name: certs
          secret:
            secretName: osac-tls
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
---
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
          image: osac-fulfillment-service:ci
          imagePullPolicy: Never
          args:
            - start
            - rest-gateway
            - --log-level=info
            - --http-listener-address=0.0.0.0:8011
            - --grpc-server-address=osac-grpc:8010
            - --ca-file=/certs/server.crt
            - --metrics-listener-address=0.0.0.0:8012
          ports:
            - containerPort: 8011
            - containerPort: 8012
          volumeMounts:
            - name: certs
              mountPath: /certs
              readOnly: true
      volumes:
        - name: certs
          secret:
            secretName: osac-tls
---
apiVersion: v1
kind: Service
metadata:
  name: osac-rest
  namespace: osac
spec:
  ports:
    - name: http
      port: 8011
    - name: metrics
      port: 8012
  selector:
    app: osac-rest
K8S

echo "OSAC deployment applied"

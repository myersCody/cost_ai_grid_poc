#!/usr/bin/env bash
set -euo pipefail

# Prepare and deploy the full OSAC + cost-consumer stack inside k3d.
# This mirrors the CI workflow (.github/workflows/integration.yml) but
# uses k3d image import instead of k3s ctr.
#
# After this script completes, run: bash integration-test/test.sh

K3D_CLUSTER="${K3D_CLUSTER:-cost-dev}"
WORKSPACE_DIR="$(cd "$(dirname "$0")/.." && pwd)"

echo "=== Deploy Full Stack (k3d: $K3D_CLUSTER) ==="

# ── 1. Clone and build fulfillment-service ──
echo ""
echo "--- Building OSAC fulfillment-service ---"
if [ ! -d /tmp/fulfillment-service ]; then
    git clone --depth 1 https://github.com/osac-project/fulfillment-service.git /tmp/fulfillment-service
fi
cd /tmp/fulfillment-service
GOTOOLCHAIN=auto go build -o /tmp/fulfillment-service-bin ./cmd/fulfillment-service
echo "Built fulfillment-service"

# ── 2. Generate TLS certs ──
echo ""
echo "--- Generating TLS certs ---"
openssl req -x509 -newkey rsa:2048 -nodes \
    -keyout /tmp/fulfillment-service/server.key \
    -out /tmp/fulfillment-service/server.crt \
    -days 1 \
    -subj "/CN=localhost" \
    -addext "subjectAltName=DNS:localhost,DNS:osac-grpc,DNS:osac-rest,DNS:osac-oidc,DNS:osac-grpc.osac.svc,DNS:osac-rest.osac.svc,DNS:osac-oidc.osac.svc,DNS:osac-grpc.osac.svc.cluster.local,DNS:osac-rest.osac.svc.cluster.local,DNS:osac-oidc.osac.svc.cluster.local,IP:127.0.0.1" \
    2>/dev/null
echo "Certs generated"

# ── 3. Generate auth token ──
echo ""
echo "--- Generating OSAC auth token ---"
python3 -c "
import json, hashlib, base64, datetime
from cryptography.hazmat.primitives import serialization
import jwt
with open('/tmp/fulfillment-service/server.key', 'rb') as f:
    private_key = serialization.load_pem_private_key(f.read(), password=None)
pub = private_key.public_key().public_numbers()
def b64url(n):
    length = (n.bit_length() + 7) // 8
    return base64.urlsafe_b64encode(n.to_bytes(length, 'big')).rstrip(b'=').decode()
jwk_data = {'e': b64url(pub.e), 'kty': 'RSA', 'n': b64url(pub.n)}
thumbprint = json.dumps(jwk_data, separators=(',', ':'), sort_keys=True)
kid = base64.urlsafe_b64encode(hashlib.sha256(thumbprint.encode()).digest()).rstrip(b'=').decode()
now = datetime.datetime.now(datetime.timezone.utc)
token = jwt.encode({
    'iss': 'https://osac-oidc:8013',
    'sub': 'admin',
    'preferred_username': 'admin',
    'groups': ['admins'],
    'iat': now,
    'exp': now + datetime.timedelta(hours=12),
}, private_key, algorithm='RS256', headers={'kid': kid})
print(token)
" > /tmp/osac_token.txt
echo "Token written to /tmp/osac_token.txt"

# ── 4. Build container images ──
echo ""
echo "--- Building container images ---"
cd "$WORKSPACE_DIR"/inventory-watcher
docker build -t cost-event-consumer:ci -f Containerfile .
echo "Consumer image built"

cat > /tmp/Containerfile.osac <<'EOF'
FROM registry.access.redhat.com/ubi9-minimal:latest
COPY fulfillment-service-bin /usr/local/bin/fulfillment-service
USER 1001
ENTRYPOINT ["fulfillment-service"]
EOF
docker build -t osac-fulfillment-service:ci -f /tmp/Containerfile.osac /tmp/
echo "OSAC image built"

# ── 5. Import images into k3d ──
echo ""
echo "--- Importing images into k3d ---"
k3d image import cost-event-consumer:ci -c "$K3D_CLUSTER"
k3d image import osac-fulfillment-service:ci -c "$K3D_CLUSTER"
echo "Images imported"

# ── 6. Deploy OSAC ──
echo ""
echo "--- Deploying OSAC ---"
cd "$WORKSPACE_DIR"
bash integration-test/deploy-osac.sh

# ── 7. Ensure clean OSAC DB and restart gRPC (same as CI) ──
echo ""
echo "--- Resetting OSAC DB ---"
sleep 15
kubectl wait --for=condition=ready pod -l app=osac-db -n osac --timeout=120s
kubectl exec -n osac statefulset/osac-db -- \
    psql -U osacuser -d osacdb -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;" || true
kubectl rollout restart deployment/osac-grpc -n osac
sleep 10

# ── 8. Wait for OSAC services ──
echo ""
echo "--- Waiting for OSAC services ---"
kubectl wait --for=condition=available deployment/osac-oidc -n osac --timeout=180s
kubectl wait --for=condition=available deployment/osac-grpc -n osac --timeout=180s
kubectl wait --for=condition=available deployment/osac-rest -n osac --timeout=180s
echo "OSAC pods:"
kubectl get pods -n osac

# ── 9. Deploy cost-consumer ──
echo ""
echo "--- Deploying cost-consumer ---"
bash integration-test/deploy-consumer.sh

# ── 10. Wait for cost-consumer ──
echo ""
echo "--- Waiting for cost-consumer ---"
sleep 10
kubectl wait --for=condition=available deployment/cost-event-consumer -n cost-mgmt --timeout=180s
echo "All pods:"
kubectl get pods -A

# ── 11. Start port forwards ──
echo ""
echo "--- Starting port forwards ---"
# Kill any existing port-forwards
pkill -f 'kubectl port-forward' 2>/dev/null || true
sleep 1

kubectl port-forward -n cost-mgmt svc/cost-event-consumer 8020:8020 &
kubectl port-forward -n cost-mgmt svc/cost-event-consumer 9000:9000 &
kubectl port-forward -n osac svc/osac-rest 8011:8011 &
sleep 5

echo ""
echo "=========================================="
echo "  Stack deployed!"
echo ""
echo "  Verify: curl -sf http://localhost:8020/healthz"
echo "  Tests:  bash integration-test/test.sh"
echo "  k9s:    k9s"
echo "=========================================="

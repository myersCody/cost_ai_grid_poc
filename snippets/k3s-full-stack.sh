#!/usr/bin/env bash
set -euo pipefail

# k3s Full-Stack Demo: OSAC + IPP MaaS Gateway + Cost Consumer
#
# Deploys the complete cost management demo on a local k3s cluster.
# Two event paths:
#   1. OSAC Watch stream (capacity: VMs, clusters, bare metal)
#   2. IPP gateway + llm-katan (consumption: MaaS inference)
#
# Usage:
#   ./snippets/k3s-full-stack.sh up            # full stack
#   ./snippets/k3s-full-stack.sh up --ipp-only  # skip OSAC
#   ./snippets/k3s-full-stack.sh down           # tear down
#   ./snippets/k3s-full-stack.sh status         # check all pods
#   ./snippets/k3s-full-stack.sh test           # send test traffic
#
# Prerequisites:
#   - k3s installed (curl -sfL https://get.k3s.io | sh -)
#   - helm installed
#   - docker available (for building images)
#   - OSAC fulfillment-service cloned at /tmp/fulfillment-service (for OSAC path)

SCRIPT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$SCRIPT_DIR"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${BLUE}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
fail()  { echo -e "${RED}[FAIL]${NC}  $*"; }
step()  { echo -e "\n${BLUE}=== $* ===${NC}"; }

IPP_ONLY=false
SKIP_MONITORING=false
IPP_REPO="https://github.com/opendatahub-io/ai-gateway-payload-processing.git"
IPP_BRANCH="feat/external-metering-dp"

# ──────────────────────────────────────────────────────────────
# Helpers
# ──────────────────────────────────────────────────────────────

check_prereqs() {
    local missing=()
    command -v kubectl >/dev/null || missing+=(kubectl)
    command -v helm >/dev/null    || missing+=(helm)
    command -v docker >/dev/null  || missing+=(docker)

    if ! kubectl cluster-info >/dev/null 2>&1; then
        fail "kubectl cannot reach a cluster. Is k3s running?"
        echo "  Install: curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='--disable=traefik' sh -"
        echo "  Then:    mkdir -p ~/.kube && sudo cp /etc/rancher/k3s/k3s.yaml ~/.kube/config && sudo chown \$USER ~/.kube/config"
        exit 1
    fi

    if [ ${#missing[@]} -gt 0 ]; then
        fail "Missing: ${missing[*]}"
        exit 1
    fi

    # Python deps needed for OSAC token generation
    if ! $IPP_ONLY; then
        python3 -c "import jwt, cryptography" 2>/dev/null || {
            warn "Python packages 'PyJWT' and 'cryptography' needed for OSAC token generation"
            echo "  Install: pip install PyJWT cryptography"
        }
    fi

    ok "Prerequisites satisfied"
}

wait_deploy() {
    local ns="$1" name="$2" timeout="${3:-180}"
    kubectl wait --for=condition=available "deployment/$name" -n "$ns" --timeout="${timeout}s" 2>/dev/null
}

wait_pod() {
    local ns="$1" label="$2" timeout="${3:-180}"
    kubectl wait --for=condition=ready pod -l "$label" -n "$ns" --timeout="${timeout}s" 2>/dev/null
}

import_image() {
    local name="$1"
    if command -v k3s >/dev/null 2>&1; then
        docker save "$name" | sudo k3s ctr images import -
    elif command -v k3d >/dev/null 2>&1; then
        k3d image import "$name"
    else
        docker save "$name" | sudo ctr --address /run/k3s/containerd/containerd.sock --namespace k8s.io images import -
    fi
}

kill_port_forwards() {
    pkill -f 'kubectl port-forward.*cost-mgmt' 2>/dev/null || true
    pkill -f 'kubectl port-forward.*ai-gateway' 2>/dev/null || true
    pkill -f 'kubectl port-forward.*osac' 2>/dev/null || true
    pkill -f 'kubectl port-forward.*monitoring' 2>/dev/null || true
}

# ──────────────────────────────────────────────────────────────
# Build images
# ──────────────────────────────────────────────────────────────

build_consumer_image() {
    step "Building cost-event-consumer image"
    cd "$SCRIPT_DIR/inventory-watcher"
    docker build -t cost-event-consumer:ci -f Containerfile .
    import_image cost-event-consumer:ci
    ok "Consumer image built and imported"
    cd "$SCRIPT_DIR"
}

build_katan_image() {
    step "Building llm-katan image"
    local tmpfile
    tmpfile=$(mktemp)
    cat > "$tmpfile" <<'DOCKERFILE'
FROM python:3.11-slim
RUN pip install --no-cache-dir llm-katan
EXPOSE 8000
ENTRYPOINT ["llm-katan"]
CMD ["--model", "test-model", "--backend", "echo", "--providers", "openai,anthropic", "--host", "0.0.0.0"]
DOCKERFILE
    docker build -t llm-katan:ci -f "$tmpfile" .
    rm -f "$tmpfile"
    import_image llm-katan:ci
    ok "llm-katan image built and imported"
}

build_ipp_image() {
    step "Building IPP image"
    if [ ! -d /tmp/ipp ]; then
        info "Cloning IPP repository..."
        git clone "$IPP_REPO" /tmp/ipp
        cd /tmp/ipp
        git fetch origin "pull/320/head:$IPP_BRANCH" 2>/dev/null || true
        git checkout "$IPP_BRANCH"
    else
        info "Using existing /tmp/ipp checkout"
        cd /tmp/ipp
    fi
    info "Building IPP binary..."
    GOTOOLCHAIN=auto CGO_ENABLED=0 go build -o /tmp/ipp-bin ./cmd
    cat > /tmp/Containerfile.ipp <<'DOCKERFILE'
FROM registry.access.redhat.com/ubi9-minimal:latest
COPY ipp-bin /bbr
USER 1001
ENTRYPOINT ["/bbr"]
DOCKERFILE
    docker build -t ipp-metering:ci -f /tmp/Containerfile.ipp /tmp/
    import_image ipp-metering:ci
    ok "IPP image built and imported"
    cd "$SCRIPT_DIR"
}

build_osac_image() {
    step "Building OSAC fulfillment-service image"
    if [ ! -d /tmp/fulfillment-service ]; then
        fail "OSAC source not found at /tmp/fulfillment-service"
        echo "  Clone: git clone https://github.com/osac-project/fulfillment-service /tmp/fulfillment-service"
        return 1
    fi
    cd /tmp/fulfillment-service

    if [ ! -f server.crt ] || [ ! -f server.key ]; then
        info "Generating TLS certs for OIDC mock..."
        openssl req -x509 -newkey rsa:2048 -keyout server.key -out server.crt \
            -days 365 -nodes -subj '/CN=osac-oidc' \
            -addext 'subjectAltName=DNS:osac-oidc,DNS:osac-oidc.osac.svc,DNS:osac-oidc.osac.svc.cluster.local' \
            2>/dev/null
    fi

    info "Building OSAC binary..."
    CGO_ENABLED=0 go build -o fulfillment-service ./cmd/fulfillment-service/

    cat > /tmp/Containerfile.osac <<'DOCKERFILE'
FROM registry.access.redhat.com/ubi9-minimal:latest
COPY fulfillment-service /usr/local/bin/fulfillment-service
USER 1001
ENTRYPOINT ["fulfillment-service"]
DOCKERFILE
    docker build -t osac-fulfillment-service:ci -f /tmp/Containerfile.osac /tmp/fulfillment-service/
    import_image osac-fulfillment-service:ci
    ok "OSAC image built and imported"
    cd "$SCRIPT_DIR"
}

# ──────────────────────────────────────────────────────────────
# Deploy components
# ──────────────────────────────────────────────────────────────

deploy_istio() {
    step "Installing Istio + Gateway API"

    info "Installing Gateway API CRDs..."
    kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/standard-install.yaml 2>/dev/null

    if kubectl get namespace istio-system >/dev/null 2>&1; then
        ok "Istio already installed"
        return
    fi

    info "Installing Istio..."
    if ! command -v istioctl >/dev/null 2>&1; then
        curl -sL https://istio.io/downloadIstio | ISTIO_VERSION=1.29.2 sh -
        export PATH="$PWD/istio-1.29.2/bin:$PATH"
    fi
    istioctl install --set profile=minimal \
        --set values.pilot.env.ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true \
        -y 2>/dev/null
    ok "Istio installed"
}

deploy_consumer() {
    step "Deploying cost-event-consumer + PostgreSQL"
    bash integration-test/deploy-consumer.sh
    info "Waiting for consumer pod..."
    sleep 10
    wait_deploy cost-mgmt cost-event-consumer 180
    ok "Consumer running"
}

deploy_osac() {
    step "Deploying OSAC fulfillment-service"
    bash integration-test/deploy-osac.sh

    info "Waiting for OSAC pods..."
    sleep 10
    wait_deploy osac osac-grpc 180
    wait_deploy osac osac-rest 180
    wait_deploy osac osac-oidc 180

    # Generate a JWT signed with the OSAC server's key (same approach as CI)
    info "Generating OSAC auth token..."
    local token
    token=$(cd /tmp/fulfillment-service && python3 -c "
import json, hashlib, base64, datetime
from cryptography.hazmat.primitives import serialization
import jwt
with open('server.key', 'rb') as f:
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
    'exp': now + datetime.timedelta(hours=24),
}, private_key, algorithm='RS256', headers={'kid': kid})
print(token)
" 2>/dev/null || echo "")

    if [ -n "$token" ]; then
        echo "$token" > /tmp/osac_token.txt
        # Update consumer with valid token
        kubectl create secret generic cost-consumer-secrets \
            --namespace=cost-mgmt \
            --from-literal=osac-token="$token" \
            --dry-run=client -o yaml | kubectl apply -f -
        kubectl rollout restart deployment/cost-event-consumer -n cost-mgmt
        ok "OSAC deployed, token configured"
    else
        warn "Could not generate OSAC token — consumer will run without Watch stream"
        warn "OSAC events will not flow. IPP path still works."
    fi
}

deploy_ipp() {
    step "Deploying IPP gateway stack"
    bash integration-test/deploy-ipp.sh

    # Expose via NodePort so traffic can reach the gateway from outside the cluster
    info "Creating NodePort for gateway access..."
    local gw_svc
    gw_svc=$(kubectl get svc -n ai-gateway -l gateway.networking.k8s.io/gateway-name=ai-gateway -o name 2>/dev/null | head -1)
    if [ -n "$gw_svc" ]; then
        kubectl patch "$gw_svc" -n ai-gateway --type=merge \
            -p='{"spec":{"type":"NodePort","ports":[{"name":"http","port":80,"nodePort":30080}]}}' 2>/dev/null || true
    fi

    ok "IPP gateway deployed"
}

deploy_monitoring() {
    if $SKIP_MONITORING; then
        info "Monitoring skipped"
        return
    fi

    step "Deploying monitoring (Prometheus + Grafana)"

    kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -

    if [ -f deploy/observability/prometheus.yml ]; then
        kubectl create configmap prometheus-config \
            --namespace=monitoring \
            --from-file=prometheus.yml=deploy/observability/prometheus.yml \
            --dry-run=client -o yaml | kubectl apply -f -
    fi

    cat <<'K8S' | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prometheus
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: prometheus
  template:
    metadata:
      labels:
        app: prometheus
    spec:
      containers:
        - name: prometheus
          image: prom/prometheus:v3.5.0
          args:
            - --config.file=/etc/prometheus/prometheus.yml
            - --storage.tsdb.retention.time=24h
          ports:
            - containerPort: 9090
          volumeMounts:
            - name: config
              mountPath: /etc/prometheus
      volumes:
        - name: config
          configMap:
            name: prometheus-config
            optional: true
---
apiVersion: v1
kind: Service
metadata:
  name: prometheus
  namespace: monitoring
spec:
  ports:
    - port: 9090
  selector:
    app: prometheus
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: grafana
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: grafana
  template:
    metadata:
      labels:
        app: grafana
    spec:
      containers:
        - name: grafana
          image: grafana/grafana:12.1.0
          ports:
            - containerPort: 3000
          env:
            - name: GF_SECURITY_ADMIN_PASSWORD
              value: "admin"
---
apiVersion: v1
kind: Service
metadata:
  name: grafana
  namespace: monitoring
spec:
  ports:
    - port: 3000
  selector:
    app: grafana
K8S

    ok "Monitoring deployed"
}

setup_port_forwards() {
    step "Setting up port forwards"
    kill_port_forwards

    kubectl port-forward -n cost-mgmt svc/cost-event-consumer 8020:8020 >/dev/null 2>&1 &
    kubectl port-forward -n cost-mgmt svc/cost-event-consumer 9000:9000 >/dev/null 2>&1 &

    if ! $IPP_ONLY; then
        kubectl port-forward -n osac svc/osac-rest 8011:8011 >/dev/null 2>&1 &
    fi

    if ! $SKIP_MONITORING; then
        kubectl port-forward -n monitoring svc/grafana 3000:3000 >/dev/null 2>&1 &
    fi

    sleep 3
    ok "Port forwards active"

    echo ""
    echo "  Endpoints:"
    echo "    Consumer API:      http://localhost:8020"
    echo "    Consumer Metrics:  http://localhost:9000/metrics"
    echo "    Debug Dashboard:   http://localhost:8020/debug/dashboard"
    echo "    Gateway (MaaS):    http://localhost:30080/v1/chat/completions"
    if ! $IPP_ONLY; then
        echo "    OSAC REST:         http://localhost:8011"
    fi
    if ! $SKIP_MONITORING; then
        echo "    Grafana:           http://localhost:3000 (admin/admin)"
    fi
}

# ──────────────────────────────────────────────────────────────
# Commands
# ──────────────────────────────────────────────────────────────

cmd_up() {
    check_prereqs

    # Build all images first
    build_consumer_image
    build_katan_image
    build_ipp_image
    if ! $IPP_ONLY; then
        build_osac_image || warn "OSAC image build failed — continuing with IPP only"
    fi

    # Deploy Istio (needed for IPP gateway)
    deploy_istio

    # Create OSAC token placeholder (deploy-consumer.sh needs it)
    echo "placeholder" > /tmp/osac_token.txt

    # Deploy in order: consumer first (IPP needs it for metering URL)
    deploy_consumer

    if ! $IPP_ONLY; then
        deploy_osac
    else
        # Disable watcher/reconciler when no OSAC
        kubectl set env deployment/cost-event-consumer -n cost-mgmt \
            DISABLE_COMPONENTS=watcher,reconciler 2>/dev/null || true
    fi

    deploy_ipp
    deploy_monitoring
    setup_port_forwards

    echo ""
    ok "Full stack deployed!"
    echo ""
    echo "  Test inference (via gateway):"
    echo "    curl http://localhost:30080/v1/chat/completions \\"
    echo "      -H 'Content-Type: application/json' \\"
    echo "      -H 'x-maas-username: demo-user' \\"
    echo "      -H 'x-maas-group: demo-tenant' \\"
    echo "      -H 'x-maas-subscription: demo-tenant/premium' \\"
    echo "      -d '{\"model\":\"test-model\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}'"
    echo ""
    echo "  Test direct event ingest:"
    echo "    ./snippets/osac-event-stream.sh --duration 60 --rate 5 --vms 10"
    echo ""
}

cmd_down() {
    step "Tearing down full stack"
    kill_port_forwards

    info "Deleting namespaces..."
    kubectl delete namespace ai-gateway --ignore-not-found --timeout=60s 2>/dev/null &
    kubectl delete namespace cost-mgmt --ignore-not-found --timeout=60s 2>/dev/null &
    kubectl delete namespace osac --ignore-not-found --timeout=60s 2>/dev/null &
    kubectl delete namespace monitoring --ignore-not-found --timeout=60s 2>/dev/null &
    wait

    # Clean up Istio (optional — leave it for reuse)
    info "Istio left in place (run 'istioctl uninstall --purge -y' to remove)"

    ok "Stack torn down"
}

cmd_status() {
    echo ""
    echo "=== Cluster Status ==="
    echo ""
    kubectl get pods -A -o wide 2>/dev/null | grep -E 'NAMESPACE|cost-mgmt|ai-gateway|osac|monitoring|istio-system' || echo "  No relevant pods found"
    echo ""

    echo "=== Services ==="
    echo ""
    kubectl get svc -A 2>/dev/null | grep -E 'NAMESPACE|cost-mgmt|ai-gateway|osac|monitoring' || echo "  No relevant services found"
    echo ""

    echo "=== Gateway ==="
    kubectl get gateway -n ai-gateway 2>/dev/null || echo "  No gateway found"
    echo ""

    # Quick health checks
    echo "=== Health Checks ==="
    if curl -sf http://localhost:8020/healthz >/dev/null 2>&1; then
        ok "Consumer API: UP"
    else
        warn "Consumer API: not reachable (port-forward may not be running)"
    fi

    if curl -sf http://localhost:9000/metrics >/dev/null 2>&1; then
        ok "Consumer metrics: UP"
    else
        warn "Consumer metrics: not reachable"
    fi

    if curl -sf http://localhost:8011/api/fulfillment/v1/instance_types -H "Authorization: Bearer $(cat /tmp/osac_token.txt 2>/dev/null || echo x)" >/dev/null 2>&1; then
        ok "OSAC REST: UP"
    else
        warn "OSAC REST: not reachable"
    fi
}

cmd_test() {
    step "Sending test traffic"

    echo ""
    info "1. Sending inference request through IPP gateway..."
    local resp
    resp=$(curl -sf --max-time 10 http://localhost:30080/v1/chat/completions \
        -H "Content-Type: application/json" \
        -H "x-maas-username: demo-user" \
        -H "x-maas-group: demo-tenant" \
        -H "x-maas-subscription: demo-tenant/premium" \
        -d '{"model":"test-model","messages":[{"role":"user","content":"hello from full-stack demo"}]}' \
        2>/dev/null || echo "FAILED")

    if echo "$resp" | grep -q "choices"; then
        ok "Gateway inference succeeded"
        echo "$resp" | python3 -m json.tool 2>/dev/null || echo "$resp"
    else
        fail "Gateway inference failed: $resp"
        warn "Is the Istio gateway ready? Check: kubectl get gateway -n ai-gateway"
    fi

    echo ""
    info "2. Sending direct CloudEvent to consumer..."
    local event_status
    event_status=$(curl -sf -o /dev/null -w '%{http_code}' -X POST http://localhost:8020/api/v1/events \
        -H "Content-Type: application/json" \
        -d "{
            \"specversion\":\"1.0\",
            \"type\":\"osac.compute_instance.lifecycle\",
            \"source\":\"k3s-demo\",
            \"id\":\"demo-$(date +%s)\",
            \"time\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",
            \"data\":{
                \"tenant_id\":\"demo-tenant\",
                \"resource_id\":\"vm-demo-001\",
                \"resource_type\":\"compute_instance\",
                \"state\":\"COMPUTE_INSTANCE_STATE_RUNNING\",
                \"instance_type_name\":\"standard-4-16\",
                \"cores\":4,
                \"memory_gib\":16
            }
        }" 2>/dev/null)

    if [ "$event_status" = "204" ]; then
        ok "Direct event accepted (HTTP 204)"
    else
        fail "Direct event returned HTTP $event_status"
    fi

    echo ""
    info "3. Current pipeline summary:"
    curl -sf http://localhost:8020/api/v1/reports/summary 2>/dev/null | python3 -m json.tool 2>/dev/null || warn "Could not reach consumer"
}

# ──────────────────────────────────────────────────────────────
# Main
# ──────────────────────────────────────────────────────────────

cmd="${1:-help}"
shift || true

while [ $# -gt 0 ]; do
    case "$1" in
        --ipp-only)      IPP_ONLY=true ;;
        --skip-monitor*) SKIP_MONITORING=true ;;
        *)               warn "Unknown flag: $1" ;;
    esac
    shift
done

case "$cmd" in
    up)     cmd_up ;;
    down)   cmd_down ;;
    status) cmd_status ;;
    test)   cmd_test ;;
    *)
        echo "Usage: $0 {up|down|status|test} [--ipp-only] [--skip-monitoring]"
        echo ""
        echo "Commands:"
        echo "  up      Deploy the full stack (OSAC + IPP + monitoring)"
        echo "  down    Tear everything down"
        echo "  status  Show pod status and health checks"
        echo "  test    Send test traffic through both paths"
        echo ""
        echo "Flags:"
        echo "  --ipp-only         Skip OSAC deployment (lighter setup)"
        echo "  --skip-monitoring  Skip Prometheus + Grafana"
        exit 1
        ;;
esac

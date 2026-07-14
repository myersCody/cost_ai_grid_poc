#!/usr/bin/env bash
set -euo pipefail

echo "=== Cost AI Grid PoC — Codespace Setup ==="

# ── 1. Wait for Docker-in-Docker ──
echo "Waiting for Docker daemon..."
for i in $(seq 1 30); do
    if docker info >/dev/null 2>&1; then
        echo "Docker ready."
        break
    fi
    sleep 2
done
if ! docker info >/dev/null 2>&1; then
    echo "ERROR: Docker daemon not available after 60s"
    exit 1
fi

# ── 2. Create k3d cluster ──
echo "Creating k3d cluster..."
k3d cluster create cost-dev \
    --wait \
    --timeout 120s \
    --agents 0 \
    --k3s-arg "--disable=traefik@server:0"

echo "Waiting for k3s node to be Ready..."
for i in $(seq 1 60); do
    if kubectl get nodes 2>/dev/null | grep -q ' Ready'; then
        echo "k3s node ready."
        break
    fi
    sleep 2
done

# ── 3. Install Go dependencies ──
echo "Installing Go dependencies..."
cd inventory-watcher && go mod download && cd ..

# ── 4. Verify tools ──
echo ""
echo "--- Tool Versions ---"
go version
kubectl version --client --short 2>/dev/null || kubectl version --client
k3d version
k9s version --short 2>/dev/null || k9s version
gh version --short 2>/dev/null || gh --version | head -1
helm version --short 2>/dev/null || helm version
docker version --format 'Docker {{.Client.Version}}'

echo ""
echo "--- Cluster Status ---"
kubectl get nodes
kubectl cluster-info

echo ""
echo "=========================================="
echo "  Codespace ready!"
echo ""
echo "  k3d cluster 'cost-dev' is running."
echo ""
echo "  Deploy the full OSAC integration stack:"
echo "    bash .devcontainer/deploy-stack.sh"
echo ""
echo "  Then run integration tests:"
echo "    bash integration-test/test.sh"
echo ""
echo "  k9s is available — just run: k9s"
echo "=========================================="

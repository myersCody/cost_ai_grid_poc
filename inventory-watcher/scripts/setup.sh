#!/bin/bash
# Setup script for running OSAC fulfillment-service + cost-event-consumer locally.
#
# Prerequisites:
#   brew install go jq
#   pip3 install PyJWT cryptography
#   Docker running
#
# Usage:
#   ./scripts/setup.sh          # set up databases + build + generate certs & token
#   ./scripts/setup.sh cleanup  # tear down containers

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CONSUMER_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
FS_DIR="${FULFILLMENT_SERVICE_DIR:-$CONSUMER_DIR/../fulfillment-service}"

if [ ! -d "$FS_DIR" ]; then
    echo "ERROR: fulfillment-service not found at $FS_DIR"
    echo "Clone it:  git clone https://github.com/osac-project/fulfillment-service $FS_DIR"
    exit 1
fi

FS_DIR="$(cd "$FS_DIR" && pwd)"
export FULFILLMENT_SERVICE_DIR="$FS_DIR"

# ── Cleanup mode ──
if [ "${1:-}" = "cleanup" ]; then
    echo "Stopping containers..."
    docker stop osac-db cost-db 2>/dev/null || true
    docker rm osac-db cost-db 2>/dev/null || true
    echo "Done."
    exit 0
fi

echo "=== OSAC + Cost Consumer Local Setup ==="
echo "  Fulfillment service: $FS_DIR"
echo "  Cost consumer:       $CONSUMER_DIR"
echo ""

# ── 1. Start databases ──
echo "--- Starting PostgreSQL containers ---"

if ! docker ps -q --filter name=osac-db | grep -q .; then
    if docker ps -aq --filter name=osac-db | grep -q .; then
        docker start osac-db
    else
        docker run -d --name osac-db \
            -e POSTGRESQL_USER=user \
            -e POSTGRESQL_PASSWORD=pass \
            -e POSTGRESQL_DATABASE=db \
            -p 127.0.0.1:5433:5432 \
            quay.io/sclorg/postgresql-18-c10s:latest
    fi
    echo "  osac-db: started on port 5433"
else
    echo "  osac-db: already running"
fi

if ! docker ps -q --filter name=cost-db | grep -q .; then
    if docker ps -aq --filter name=cost-db | grep -q .; then
        docker start cost-db
    else
        docker run -d --name cost-db \
            -e POSTGRESQL_USER=user \
            -e POSTGRESQL_PASSWORD=pass \
            -e POSTGRESQL_DATABASE=costdb \
            -p 127.0.0.1:5434:5432 \
            quay.io/sclorg/postgresql-18-c10s:latest
    fi
    echo "  cost-db: started on port 5434"
else
    echo "  cost-db: already running"
fi

echo "Waiting for databases..."
until docker exec osac-db psql -U user -d db -c "SELECT 1" &>/dev/null 2>&1; do
    sleep 1
done
echo "  osac-db: ready"

until docker exec cost-db psql -U user -d costdb -c "SELECT 1" &>/dev/null 2>&1; do
    sleep 1
done
echo "  cost-db: ready"

# ── 2. Build fulfillment-service ──
echo ""
echo "--- Building fulfillment-service ---"
cd "$FS_DIR"
if [ ! -f fulfillment-service ] || [ go.mod -nt fulfillment-service ]; then
    go build ./cmd/fulfillment-service
    go build ./cmd/osac
    echo "  Built fulfillment-service and osac CLI"
else
    echo "  Already built (use 'go build' to rebuild)"
fi

# ── 3. Generate TLS certificate ──
if [ ! -f server.crt ] || [ ! -f server.key ]; then
    echo ""
    echo "--- Generating TLS certificate ---"
    openssl req -x509 -newkey rsa:2048 -nodes \
        -keyout server.key \
        -out server.crt \
        -days 365 \
        -subj "/CN=localhost" \
        -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" 2>/dev/null
    echo "  Generated server.crt and server.key"
else
    echo ""
    echo "--- TLS certificate exists ---"
fi

# ── 4. Build cost-event-consumer ──
echo ""
echo "--- Building cost-event-consumer ---"
cd "$CONSUMER_DIR"
go build -o cost-event-consumer ./cmd/consumer/
echo "  Built cost-event-consumer"

# ── 5. Generate auth token ──
echo ""
echo "--- Generating auth token ---"
python3 "$SCRIPT_DIR/gen_token.py" > /tmp/osac_token.txt
echo "  Token written to /tmp/osac_token.txt"

# ── Done ──
echo ""
echo "=== Setup Complete ==="
echo ""
echo "Port map:"
echo "  OSAC gRPC:       localhost:8010"
echo "  OSAC REST:       localhost:8011"
echo "  OSAC PostgreSQL: localhost:5433"
echo "  Cost PostgreSQL: localhost:5434"
echo ""
echo "To run (each in a separate terminal):"
echo ""
echo "  # Terminal 1: OIDC server"
echo "  python3 $SCRIPT_DIR/oidc_server.py"
echo ""
echo "  # Terminal 2: gRPC server"
echo "  cd $FS_DIR && ./fulfillment-service start grpc-server \\"
echo "    --log-level=debug --log-headers=true --log-bodies=true \\"
echo "    --grpc-listener-address=localhost:8010 \\"
echo "    --grpc-listener-tls-crt=server.crt --grpc-listener-tls-key=server.key \\"
echo "    --ca-file=server.crt \\"
echo "    --db-url=postgres://user:pass@localhost:5433/db \\"
echo "    --token-issuer=https://localhost:8010 \\"
echo "    --token-signer-key=server.key --token-signer-crt=server.crt \\"
echo "    --token-encryption-crt=server.crt \\"
echo "    --grpc-authn-trusted-token-issuers=https://localhost:8013"
echo ""
echo "  # Terminal 3: REST gateway"
echo "  cd $FS_DIR && ./fulfillment-service start rest-gateway \\"
echo "    --log-level=debug --log-headers=true --log-bodies=true \\"
echo "    --http-listener-address=localhost:8011 \\"
echo "    --grpc-server-address=localhost:8010 \\"
echo "    --ca-file=server.crt \\"
echo "    --metrics-listener-address=localhost:8012"
echo ""
echo "  # Terminal 4: Cost event consumer"
echo "  cd $CONSUMER_DIR && OSAC_TOKEN=\$(cat /tmp/osac_token.txt) ./cost-event-consumer"
echo ""
echo "Quick test:"
echo "  curl -s http://localhost:8011/api/fulfillment/v1/clusters \\"
echo "    -H \"Authorization: Bearer \$(cat /tmp/osac_token.txt)\" | jq"

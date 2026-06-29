# Local Development Setup

Run OSAC fulfillment-service and the inventory-watcher locally for development.

Reference: https://gist.github.com/myersCody/3c49a439c10539f5cefceb9abc77d07c

## Prerequisites

```bash
brew install go jq
pip3 install PyJWT cryptography   # or use a venv
docker                             # for PostgreSQL containers
```

## Port Map

| Service | Port |
|---------|------|
| OSAC gRPC | 8010 |
| OSAC REST gateway | 8011 |
| OSAC metrics | 8012 |
| OSAC OIDC server | 8013 |
| OSAC PostgreSQL | 5433 |
| Cost inventory PostgreSQL | 5434 |

## Quick Start

```bash
# 1. Clone repos
git clone https://github.com/osac-project/fulfillment-service
cd fulfillment-service

# 2. Start databases
docker run -d --name osac-db \
  -e POSTGRESQL_USER=user -e POSTGRESQL_PASSWORD=pass -e POSTGRESQL_DATABASE=db \
  -p 127.0.0.1:5433:5432 quay.io/sclorg/postgresql-18-c10s:latest

docker run -d --name cost-db \
  -e POSTGRESQL_USER=user -e POSTGRESQL_PASSWORD=pass -e POSTGRESQL_DATABASE=costdb \
  -p 127.0.0.1:5434:5432 quay.io/sclorg/postgresql-18-c10s:latest

# Wait for databases
until docker exec osac-db psql -U user -d db -c "SELECT 1" &>/dev/null; do sleep 1; done
until docker exec cost-db psql -U user -d costdb -c "SELECT 1" &>/dev/null; do sleep 1; done

# 3. Build fulfillment-service
go build ./cmd/fulfillment-service
go build ./cmd/osac

# 4. Generate TLS certificate
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout server.key -out server.crt -days 365 \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"

# 5. Generate auth token (use venv if needed)
python3 /path/to/inventory-watcher/scripts/gen_token.py > /tmp/osac_token.txt
```

## Running the Services

Each in a separate terminal:

```bash
# Terminal 1: OIDC server
python3 /path/to/inventory-watcher/scripts/oidc_server.py

# Terminal 2: gRPC server
./fulfillment-service start grpc-server \
  --log-level=info \
  --grpc-listener-address=localhost:8010 \
  --grpc-listener-tls-crt=server.crt --grpc-listener-tls-key=server.key \
  --ca-file=server.crt \
  --db-url=postgres://user:pass@localhost:5433/db \
  --token-issuer=https://localhost:8010 \
  --token-signer-key=server.key --token-signer-crt=server.crt \
  --token-encryption-crt=server.crt \
  --grpc-authn-trusted-token-issuers=https://localhost:8013

# Terminal 3: REST gateway
./fulfillment-service start rest-gateway \
  --log-level=info \
  --http-listener-address=localhost:8011 \
  --grpc-server-address=localhost:8010 \
  --ca-file=server.crt \
  --metrics-listener-address=localhost:8012

# Terminal 4: Inventory watcher
OSAC_BASE_URL=http://localhost:8011 \
OSAC_TOKEN=$(cat /tmp/osac_token.txt) \
INVENTORY_DB_URL=postgres://user:pass@localhost:5434/costdb \
./inventory-watcher
```

## Verify

```bash
# Check OSAC API
curl -s http://localhost:8011/api/fulfillment/v1/clusters \
  -H "Authorization: Bearer $(cat /tmp/osac_token.txt)" | jq

# Check inventory database
docker exec cost-db psql -U user -d costdb \
  -c "SELECT instance_id, name, cores, memory_gib FROM inventory_compute_instance;"
```

## Creating Test Data

See `snippets/create-test-data.sh` for curl commands to populate OSAC with
compute instances, clusters, and networking resources.

## Notes

- Running OSAC without a controller is effectively "fake mode" — resources are
  stored in PostgreSQL but no real infrastructure is provisioned
- Use the private API (`/api/private/v1/...`) to create resources with desired
  status states (e.g., COMPUTE_INSTANCE_STATE_RUNNING)
- Compute instances require prerequisites: network_class, virtual_network
  (set to READY), subnet, compute_instance_template
- Auth tokens expire after 24 hours; regenerate with gen_token.py

## Cleanup

```bash
docker stop osac-db cost-db && docker rm osac-db cost-db
```

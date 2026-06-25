# OSAC Fulfillment Service — Local Dev Setup

Run the OSAC fulfillment service alongside Koku without port conflicts.

## Port Reference

| Service | Port |
|---|---|
| Koku API | 8000 |
| Koku masu | 5042 |
| Koku PostgreSQL | 15432 |
| **OSAC gRPC** | **8010** |
| **OSAC REST** | **8011** |
| **OSAC PostgreSQL** | **5433** |

## Prerequisites

```bash
brew install go grpcurl jq
```

## Step 1 — Clone the repo

```bash
git clone https://github.com/osac-project/fulfillment-service
cd fulfillment-service
```

## Step 2 — Start PostgreSQL

```bash
docker run -d --name osac-db \
  -e POSTGRESQL_USER=user \
  -e POSTGRESQL_PASSWORD=pass \
  -e POSTGRESQL_DATABASE=db \
  -p 127.0.0.1:5433:5432 \
  quay.io/sclorg/postgresql-18-c10s:latest
```

## Step 3 — Wait for the database to be ready

```bash
until docker exec osac-db psql -U user -d db -c "SELECT 1" &>/dev/null; do
  echo "waiting for db..."; sleep 1
done
echo "DB ready"
```

## Step 4 — Build the binaries

```bash
go build ./cmd/fulfillment-service
go build ./cmd/osac
```

## Step 4b — Generate a local TLS and token signing certificate

The gRPC server requires HTTPS for the token issuer URL. Generate one self-signed cert
with a `localhost` SAN used for both TLS and token signing:

```bash
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout server.key \
  -out server.crt \
  -days 365 \
  -subj "/CN=localhost" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
```

This only needs to be done once — the files persist across restarts.

## Step 5 — Start the OIDC discovery server (terminal 1)

The gRPC server's JWKS cache uses a plain HTTP/1.1 client to validate tokens, but the
gRPC server itself only speaks HTTP/2. A tiny Python HTTPS server bridges this gap by
serving the OpenID Connect discovery and JWKS endpoints over HTTP/1.1.

`oidc_servers.py`
```
#!/usr/bin/env python3
"""
Minimal local HTTPS OIDC discovery + JWKS server for OSAC development.

Serves:
  /.well-known/openid-configuration
  /.well-known/jwks.json

Uses server.crt / server.key from the same directory.
Run in a dedicated terminal before starting the gRPC server.
"""

import json
import hashlib
import base64
import ssl
import http.server
from cryptography.hazmat.primitives import serialization

ISSUER = "https://localhost:8013"
PORT = 8013


def b64url(n: int) -> str:
    length = (n.bit_length() + 7) // 8
    return base64.urlsafe_b64encode(n.to_bytes(length, "big")).rstrip(b"=").decode()


with open("server.key", "rb") as f:
    _private_key = serialization.load_pem_private_key(f.read(), password=None)

_pub = _private_key.public_key().public_numbers()

# RFC 7638 JWK thumbprint (same algorithm the gRPC server uses for kid)
_jwk_thumb = {"e": b64url(_pub.e), "kty": "RSA", "n": b64url(_pub.n)}
_thumbprint = json.dumps(_jwk_thumb, separators=(",", ":"), sort_keys=True)
KID = base64.urlsafe_b64encode(
    hashlib.sha256(_thumbprint.encode()).digest()
).rstrip(b"=").decode()

JWKS_BODY = json.dumps({
    "keys": [{
        "kty": "RSA",
        "use": "sig",
        "alg": "RS256",
        "kid": KID,
        "n": b64url(_pub.n),
        "e": b64url(_pub.e),
    }]
}).encode()

DISCOVERY_BODY = json.dumps({
    "issuer": ISSUER,
    "jwks_uri": f"{ISSUER}/.well-known/jwks.json",
}).encode()


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass  # suppress per-request noise

    def do_GET(self):
        if self.path == "/.well-known/jwks.json":
            body = JWKS_BODY
            ct = "application/json"
        elif self.path == "/.well-known/openid-configuration":
            body = DISCOVERY_BODY
            ct = "application/json"
        else:
            self.send_response(404)
            self.end_headers()
            return
        self.send_response(200)
        self.send_header("Content-Type", ct)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
ctx.load_cert_chain("server.crt", "server.key")

server = http.server.HTTPServer(("localhost", PORT), Handler)
server.socket = ctx.wrap_socket(server.socket, server_side=True)

print(f"OIDC server listening at {ISSUER}")
print(f"  kid: {KID}")
server.serve_forever()
```

```bash
python3 oidc_server.py
```

You should see:
```
OIDC server listening at https://localhost:8013
  kid: <thumbprint>
```

## Step 6 — Start the gRPC server (terminal 2)

```bash
./fulfillment-service start grpc-server \
  --log-level=debug \
  --log-headers=true \
  --log-bodies=true \
  --grpc-listener-address=localhost:8010 \
  --grpc-listener-tls-crt=server.crt \
  --grpc-listener-tls-key=server.key \
  --ca-file=server.crt \
  --db-url=postgres://user:pass@localhost:5433/db \
  --token-issuer=https://localhost:8010 \
  --token-signer-key=server.key \
  --token-signer-crt=server.crt \
  --token-encryption-crt=server.crt \
  --grpc-authn-trusted-token-issuers=https://localhost:8013
```

Wait until you see a startup success log before continuing.

## Step 7 — Start the REST gateway (terminal 3)

```bash
./fulfillment-service start rest-gateway \
  --log-level=debug \
  --log-headers=true \
  --log-bodies=true \
  --http-listener-address=localhost:8011 \
  --grpc-server-address=localhost:8010 \
  --ca-file=server.crt \
  --metrics-listener-address=localhost:8012
```

## Step 7 — Verify everything is working

```bash
# REST — list cluster templates (unauthenticated, will return auth error)
curl --silent http://localhost:8011/api/fulfillment/v1/cluster_templates | jq
```

## Step 8 — Generate a local token for API calls

The API requires a Bearer token. Mint one directly from `server.key` (no Keycloak needed).
Write the script to a file to avoid shell heredoc quoting issues:

```bash
pip3 install PyJWT cryptography  # only needed once

cat > /tmp/gen_osac_token.py << 'PYEOF'
import json, hashlib, base64, datetime, os
from cryptography.hazmat.primitives import serialization
import jwt

os.chdir("/path/to/fulfillment-service")

with open("server.key", "rb") as f:
    private_key = serialization.load_pem_private_key(f.read(), password=None)

pub = private_key.public_key().public_numbers()

def b64url(n):
    length = (n.bit_length() + 7) // 8
    return base64.urlsafe_b64encode(n.to_bytes(length, "big")).rstrip(b"=").decode()

# RFC 7638 JWK thumbprint — same algorithm the server uses for kid
jwk_data = {"e": b64url(pub.e), "kty": "RSA", "n": b64url(pub.n)}
thumbprint = json.dumps(jwk_data, separators=(",", ":"), sort_keys=True)
kid = base64.urlsafe_b64encode(hashlib.sha256(thumbprint.encode()).digest()).rstrip(b"=").decode()

now = datetime.datetime.now(datetime.timezone.utc)
token = jwt.encode(
    {"iss": "https://localhost:8013", "sub": "admin",
     "preferred_username": "admin",
     "groups": ["admins"],
     "iat": now, "exp": now + datetime.timedelta(hours=24)},
    private_key, algorithm="RS256", headers={"kid": kid},
)
print(token)
PYEOF

python3 /tmp/gen_osac_token.py > /tmp/osac_token.txt
```

The token is valid for 24 hours. Re-run `python3 /tmp/gen_osac_token.py > /tmp/osac_token.txt` to refresh it.

## Step 9 — Verify authenticated API calls

```bash
# List cluster templates
curl -s http://localhost:8011/api/fulfillment/v1/cluster_templates \
  -H "Authorization: Bearer $(cat /tmp/osac_token.txt)" | jq

# List clusters (empty at first)
curl -s http://localhost:8011/api/fulfillment/v1/clusters \
  -H "Authorization: Bearer $(cat /tmp/osac_token.txt)" | jq
```

## Step 10 — Create a test cluster

```bash
curl -s -X POST http://localhost:8011/api/fulfillment/v1/clusters \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(cat /tmp/osac_token.txt)" \
  -d '{
    "id": "test-cluster-1",
    "metadata": {"name": "my-ocp-cluster"},
    "spec": {}
  }' | jq
```

## Teardown

```bash
docker stop osac-db && docker rm osac-db
```
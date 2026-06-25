#!/usr/bin/env python3
"""
Minimal local HTTPS OIDC discovery + JWKS server for OSAC development.

Serves:
  /.well-known/openid-configuration
  /.well-known/jwks.json

Uses server.crt / server.key from the fulfillment-service directory.
Run in a dedicated terminal before starting the gRPC server.
"""

import json
import hashlib
import base64
import ssl
import http.server
import os
import sys
from cryptography.hazmat.primitives import serialization

ISSUER = "https://localhost:8013"
PORT = 8013

# Look for certs in the fulfillment-service directory.
FS_DIR = os.environ.get("FULFILLMENT_SERVICE_DIR",
    os.path.join(os.path.dirname(__file__), "..", "..", "fulfillment-service"))
CERT_DIR = os.path.abspath(FS_DIR)

key_path = os.path.join(CERT_DIR, "server.key")
crt_path = os.path.join(CERT_DIR, "server.crt")

if not os.path.exists(key_path):
    print(f"ERROR: {key_path} not found. Generate TLS certs first.", file=sys.stderr)
    sys.exit(1)


def b64url(n: int) -> str:
    length = (n.bit_length() + 7) // 8
    return base64.urlsafe_b64encode(n.to_bytes(length, "big")).rstrip(b"=").decode()


with open(key_path, "rb") as f:
    _private_key = serialization.load_pem_private_key(f.read(), password=None)

_pub = _private_key.public_key().public_numbers()

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
        pass

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
ctx.load_cert_chain(crt_path, key_path)

server = http.server.HTTPServer(("localhost", PORT), Handler)
server.socket = ctx.wrap_socket(server.socket, server_side=True)

print(f"OIDC server listening at {ISSUER}")
print(f"  kid: {KID}")
print(f"  certs: {CERT_DIR}")
server.serve_forever()

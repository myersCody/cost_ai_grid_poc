#!/usr/bin/env python3
"""Generate a JWT token for authenticating with the local OSAC fulfillment-service."""

import json
import hashlib
import base64
import datetime
import os
import sys

from cryptography.hazmat.primitives import serialization
import jwt

FS_DIR = os.environ.get("FULFILLMENT_SERVICE_DIR",
    os.path.join(os.path.dirname(__file__), "..", "..", "fulfillment-service"))
CERT_DIR = os.path.abspath(FS_DIR)

key_path = os.path.join(CERT_DIR, "server.key")
if not os.path.exists(key_path):
    print(f"ERROR: {key_path} not found.", file=sys.stderr)
    sys.exit(1)

with open(key_path, "rb") as f:
    private_key = serialization.load_pem_private_key(f.read(), password=None)

pub = private_key.public_key().public_numbers()


def b64url(n):
    length = (n.bit_length() + 7) // 8
    return base64.urlsafe_b64encode(n.to_bytes(length, "big")).rstrip(b"=").decode()


jwk_data = {"e": b64url(pub.e), "kty": "RSA", "n": b64url(pub.n)}
thumbprint = json.dumps(jwk_data, separators=(",", ":"), sort_keys=True)
kid = base64.urlsafe_b64encode(
    hashlib.sha256(thumbprint.encode()).digest()
).rstrip(b"=").decode()

now = datetime.datetime.now(datetime.timezone.utc)
token = jwt.encode(
    {
        "iss": "https://localhost:8013",
        "sub": "admin",
        "preferred_username": "admin",
        "groups": ["admins"],
        "iat": now,
        "exp": now + datetime.timedelta(hours=24),
    },
    private_key,
    algorithm="RS256",
    headers={"kid": kid},
)
print(token)

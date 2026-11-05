"""Load a Service Account private.json and sign a short-lived PS256 JWT.

The JWT is returned to the caller and is never written to disk.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path

import jwt
from cryptography.hazmat.primitives.serialization import load_pem_private_key

TOKEN_AUDIENCE = "https://oauth-login.cloud.huawei.com/oauth2/v3/token"
TOKEN_LIFETIME_SECONDS = 3600


@dataclass
class ServiceAccount:
    key_id: str
    sub_account: str
    private_key_pem: str

    def __repr__(self) -> str:
        return f"ServiceAccount(key_id={self.key_id!r}, sub_account={self.sub_account!r})"


def load_service_account(path: Path) -> ServiceAccount:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ValueError("private.json is not valid JSON") from exc
    if not isinstance(payload, dict):
        raise ValueError("private.json must be a JSON object")

    key_id = payload.get("key_id")
    sub_account = payload.get("sub_account")
    private_key = payload.get("private_key")
    if not all(isinstance(item, str) and item.strip() for item in (key_id, sub_account, private_key)):
        raise ValueError("private.json must contain key_id, sub_account, and private_key")
    if "BEGIN PRIVATE KEY" not in private_key:
        raise ValueError("private_key must be a PKCS8 PEM private key")
    try:
        load_pem_private_key(private_key.encode("utf-8"), password=None)
    except (TypeError, ValueError) as exc:
        raise ValueError("private_key cannot be loaded") from exc
    return ServiceAccount(key_id=key_id, sub_account=sub_account, private_key_pem=private_key)


def build_jwt(account: ServiceAccount, *, issued_at: int) -> str:
    """Sign a PS256 JWT. `issued_at` is a UTC unix timestamp in seconds."""

    payload = {
        "aud": TOKEN_AUDIENCE,
        "iss": account.sub_account,
        "iat": issued_at,
        "exp": issued_at + TOKEN_LIFETIME_SECONDS,
    }
    token = jwt.encode(
        payload,
        account.private_key_pem,
        algorithm="PS256",
        headers={"kid": account.key_id, "typ": "JWT"},
    )
    if isinstance(token, bytes):
        return token.decode("ascii")
    return token

"""Remove credentials and tokens before anything is written to logs."""

from __future__ import annotations

import re
from typing import Any

_PEM = re.compile(
    r"-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----",
    re.DOTALL,
)
_JWT = re.compile(r"eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}")
_BEARER = re.compile(r"(?i)\bBearer\s+[A-Za-z0-9._\-]+")
_SECRET_KEYS = {
    "private_key",
    "privatekey",
    "authorization",
    "jwt",
    "token",
    "access_token",
    "secret",
}


def redact_text(value: str) -> str:
    redacted = _PEM.sub("[redacted-private-key]", value)
    redacted = _BEARER.sub("Bearer [redacted]", redacted)
    return _JWT.sub("[redacted-jwt]", redacted)


def redact_value(value: Any) -> Any:
    if isinstance(value, str):
        return redact_text(value)
    if isinstance(value, dict):
        cleaned: dict[Any, Any] = {}
        for key, item in value.items():
            if str(key).lower() in _SECRET_KEYS:
                cleaned[key] = "[redacted]"
            else:
                cleaned[key] = redact_value(item)
        return cleaned
    if isinstance(value, list):
        return [redact_value(item) for item in value]
    return value

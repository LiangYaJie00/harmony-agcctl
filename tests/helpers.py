"""Fixtures for doctor tests. Keys generated here never leave the test process."""

from __future__ import annotations

import json
from datetime import datetime, timezone
from pathlib import Path

import yaml
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import rsa

from agc_cli.http_client import AppInfoProbe
from agc_cli.version import __version__

BUNDLE_NAME = "com.qianshou.billionclub"
APP_ID = "123456789"
SUB_ACCOUNT = "1000000000000000001"
KEY_ID = "test-key-id"


def generate_private_pem() -> tuple[str, str]:
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    private_pem = key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.PKCS8,
        encryption_algorithm=serialization.NoEncryption(),
    ).decode("ascii")
    public_pem = (
        key.public_key()
        .public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        .decode("ascii")
    )
    return private_pem, public_pem


def write_private_json(directory: Path, private_pem: str, *, mode: int = 0o600) -> Path:
    path = directory / "private.json"
    path.write_text(
        json.dumps(
            {
                "key_id": KEY_ID,
                "sub_account": SUB_ACCOUNT,
                "private_key": private_pem,
                "token_uri": "https://oauth-login.cloud.huawei.com/oauth2/v3/token",
            }
        ),
        encoding="utf-8",
    )
    path.chmod(mode)
    return path


def write_config(directory: Path, private_key: Path, **overrides: object) -> Path:
    artifact = directory / "artifacts"
    logs = directory / "logs"
    run = directory / "run"
    for path in (artifact, logs, run):
        path.mkdir(exist_ok=True)
    payload = {
        "apiHost": "https://connect-api.cloud.huawei.com",
        "appId": APP_ID,
        "bundleName": BUNDLE_NAME,
        "defaultLanguage": "zh-CN",
        "testReleaseType": 6,
        "testType": 3,
        "connectTimeoutSeconds": 10,
        "queryTimeoutSeconds": 30,
        "uploadTimeoutSeconds": 1200,
        "privateKeyFile": str(private_key),
        "artifactRoot": str(artifact),
        "logDir": str(logs),
        "runDir": str(run),
        "requireManifest": False,
        "maxClockSkewSeconds": 60,
    }
    payload.update(overrides)
    path = directory / "config.yaml"
    path.write_text(yaml.safe_dump(payload), encoding="utf-8")
    return path


def request_body(**overrides: object) -> dict[str, object]:
    payload: dict[str, object] = {
        "schemaVersion": "1.0",
        "taskId": "PUBLISH-20261015-001",
        "operationKey": "PUBLISH-20261015-001:DOCTOR",
        "toolVersion": __version__,
        "appId": APP_ID,
        "request": {},
    }
    payload.update(overrides)
    return payload


def success_probe(when: datetime | None = None) -> AppInfoProbe:
    moment = when or datetime.now(timezone.utc)
    return AppInfoProbe(
        connected=True,
        tls_verified=True,
        status_code=200,
        date=moment,
        ret_code=0,
        error_code=None,
        message="HTTP 200",
    )

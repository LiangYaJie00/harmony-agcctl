import json
from pathlib import Path

import pytest

from agc_cli.config import load_settings
from agc_cli.schema_io import SchemaValidationError, validate_request, validate_response
from tests.helpers import request_body


def test_request_schema_rejects_missing_operation_key() -> None:
    payload = request_body()
    del payload["operationKey"]
    with pytest.raises(SchemaValidationError):
        validate_request(payload)


def test_response_schema_accepts_doctor_success() -> None:
    validate_response(
        {
            "schemaVersion": "1.0",
            "success": True,
            "operation": "DOCTOR",
            "operationKey": "PUBLISH-20261015-001:DOCTOR",
            "toolVersion": "0.1.0",
            "externalIds": {},
            "remoteState": {},
            "requestId": None,
            "retryClass": "NONE",
            "error": None,
            "durationMs": 1,
            "data": {"checks": []},
        }
    )


def test_example_config_loads() -> None:
    path = Path(__file__).parents[1] / "config.example.yaml"
    settings = load_settings(path)
    assert settings.bundleName == "com.qianshou.billionclub"
    assert settings.apiHost.startswith("https://")
    assert settings.privateKeyFile == Path("/etc/qianshou-harmony/agc/private.json")


def test_config_ignores_release_flow_fields(tmp_path) -> None:
    path = tmp_path / "config.yaml"
    path.write_text(
        "apiHost: https://connect-api.cloud.huawei.com\n"
        "appId: '1'\n"
        "bundleName: a\n"
        "privateKeyFile: /tmp/k\n"
        "artifactRoot: /tmp/a\n"
        "logDir: /tmp/l\n"
        "runDir: /tmp/r\n"
        "qaGroupId: group-1\n"
        "qaGroupName: QA\n"
        "testWindowDays: 7\n",
        encoding="utf-8",
    )
    settings = load_settings(path)
    assert settings.appId == "1"
    assert not hasattr(settings, "qaGroupName")


def test_config_rejects_http_host(tmp_path) -> None:
    path = tmp_path / "config.yaml"
    path.write_text("apiHost: http://connect-api.cloud.huawei.com\nappId: '1'\nbundleName: a\nprivateKeyFile: /tmp/k\nartifactRoot: /tmp/a\nlogDir: /tmp/l\nrunDir: /tmp/r\n", encoding="utf-8")
    with pytest.raises(ValueError):
        load_settings(path)


def test_request_round_trip_is_json() -> None:
    encoded = json.dumps(request_body())
    validate_request(json.loads(encoded))

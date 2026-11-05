import json
from datetime import datetime, timedelta, timezone

from agc_cli.doctor import run_doctor
from agc_cli.errors import ExitCode
from agc_cli.config import load_settings
from agc_cli.http_client import AppInfoProbe, PackageLookup, _matched_app_id
from tests.helpers import (
    APP_ID,
    BUNDLE_NAME,
    generate_private_pem,
    request_body,
    success_probe,
    write_config,
    write_private_json,
)


class FakeClient:
    def __init__(self, probe: AppInfoProbe, resolved_app_id: str | None = APP_ID) -> None:
        self.probe = probe
        self.resolved_app_id = resolved_app_id
        self.calls: list[tuple[str, str]] = []
        self.lookup_calls: list[tuple[str, str]] = []

    def get_app_info(self, *, app_id: str, token: str) -> AppInfoProbe:
        self.calls.append((app_id, token))
        return self.probe

    def lookup_package(self, *, package_name: str, token: str) -> PackageLookup:
        self.lookup_calls.append((package_name, token))
        return PackageLookup(
            connected=True,
            tls_verified=True,
            status_code=200,
            ret_code=0,
            error_code=None,
            app_id=self.resolved_app_id,
            message="HTTP 200",
        )


def _settings(tmp_path, mode: int = 0o600, **overrides):
    private_pem, _public = generate_private_pem()
    key_path = write_private_json(tmp_path, private_pem, mode=mode)
    config_path = write_config(tmp_path, key_path, **overrides)
    return load_settings(config_path)


def test_doctor_passes_with_matching_app(tmp_path) -> None:
    moment = datetime(2026, 10, 15, 8, 0, tzinfo=timezone.utc)
    client = FakeClient(success_probe(moment))
    settings = _settings(tmp_path)
    request = request_body()

    report = run_doctor(
        settings,
        tool_version=str(request["toolVersion"]),
        app_id=str(request["appId"]),
        client=client,
        now=lambda: moment,
    )

    assert report.success
    assert report.exit_code == ExitCode.SUCCESS
    assert client.calls[0][0] == APP_ID
    assert client.calls[0][1].count(".") == 2
    names = {check.name: check for check in report.checks}
    assert names["jwt"].ok
    assert names["app_identity"].message == "bundleName 对应的 appId 与配置一致"
    assert client.lookup_calls[0][0] == BUNDLE_NAME
    assert names["manifest"].skipped


def test_doctor_rejects_world_readable_key(tmp_path) -> None:
    settings = _settings(tmp_path, mode=0o644)
    client = FakeClient(success_probe())
    report = run_doctor(settings, tool_version="0.1.0", app_id=APP_ID, client=client)

    assert report.exit_code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert client.calls == []


def test_doctor_rejects_bundle_mismatch(tmp_path) -> None:
    report = run_doctor(
        _settings(tmp_path),
        tool_version="0.1.0",
        app_id=APP_ID,
        client=FakeClient(success_probe(), resolved_app_id="999"),
    )
    assert report.exit_code == ExitCode.NON_RETRYABLE
    names = {check.name: check for check in report.checks}
    assert names["app_identity"].message == "bundleName 没有对应到配置的 appId"


def test_doctor_retries_when_host_is_unreachable(tmp_path) -> None:
    probe = AppInfoProbe(
        connected=False,
        tls_verified=True,
        status_code=None,
        date=None,
        ret_code=None,
        error_code="API_UNREACHABLE",
        message="ConnectError",
    )
    report = run_doctor(
        _settings(tmp_path),
        tool_version="0.1.0",
        app_id=APP_ID,
        client=FakeClient(probe),
    )
    assert report.exit_code == ExitCode.SAFE_RETRY


def test_doctor_reports_clock_skew(tmp_path) -> None:
    now = datetime(2026, 10, 15, 8, 0, tzinfo=timezone.utc)
    probe = success_probe(now - timedelta(minutes=5))
    report = run_doctor(
        _settings(tmp_path),
        tool_version="0.1.0",
        app_id=APP_ID,
        client=FakeClient(probe),
        now=lambda: now,
    )
    assert report.exit_code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert any(check.name == "clock" and not check.ok for check in report.checks)


def test_doctor_checks_manifest_digest(tmp_path) -> None:
    payload = tmp_path / "payload.bin"
    payload.write_bytes(b"agcctl")
    import hashlib

    digest = hashlib.sha256(payload.read_bytes()).hexdigest()
    manifest = tmp_path / "MANIFEST.json"
    manifest.write_text(
        json.dumps({"version": "0.1.0", "sha256": digest, "payload": str(payload)}),
        encoding="utf-8",
    )
    report = run_doctor(
        _settings(tmp_path, manifestFile=str(manifest), requireManifest=True),
        tool_version="0.1.0",
        app_id=APP_ID,
        client=FakeClient(success_probe()),
    )
    assert report.success


def test_package_lookup_requires_one_matching_app_id() -> None:
    assert _matched_app_id({"appids": [{"key": "牵手", "value": APP_ID}]}) == APP_ID
    assert _matched_app_id({"appids": []}) is None
    assert (
        _matched_app_id(
            {"appids": [{"key": "牵手", "value": APP_ID}, {"key": "牵手", "value": "2"}]},
        )
        is None
    )

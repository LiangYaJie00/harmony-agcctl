"""Local and remote readiness checks for one agcctl installation."""

from __future__ import annotations

import hashlib
import json
import os
import pwd
import stat
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Callable

from agc_cli.auth import build_jwt, load_service_account
from agc_cli.config import Settings
from agc_cli.errors import ExitCode, worst_exit
from agc_cli.http_client import APP_INFO_PATH, AppInfoClient, AppInfoProbe, HttpxAppInfoClient, PackageLookup
from agc_cli.version import __version__


@dataclass
class Check:
    name: str
    ok: bool
    message: str
    exit_code: ExitCode = ExitCode.SUCCESS
    skipped: bool = False

    def as_dict(self) -> dict[str, Any]:
        return {
            "name": self.name,
            "ok": self.ok,
            "skipped": self.skipped,
            "message": self.message,
        }


@dataclass
class DoctorReport:
    checks: list[Check] = field(default_factory=list)
    exit_code: ExitCode = ExitCode.SUCCESS
    code: str = "OK"
    message: str = "doctor passed"
    remote_state: dict[str, Any] = field(default_factory=dict)

    @property
    def success(self) -> bool:
        return self.exit_code == ExitCode.SUCCESS


def run_doctor(
    settings: Settings,
    *,
    tool_version: str,
    app_id: str,
    client: AppInfoClient | None = None,
    now: Callable[[], datetime] | None = None,
) -> DoctorReport:
    clock = now or (lambda: datetime.now(timezone.utc))
    report = DoctorReport()
    report.checks.append(_check_tool_version(tool_version))
    report.checks.append(_check_app_id(settings, app_id))
    report.checks.append(_check_manifest(settings))
    report.checks.append(_check_directory(settings.artifactRoot, "artifact_dir", writable=False))
    report.checks.append(_check_directory(settings.logDir, "log_dir", writable=True))
    report.checks.append(_check_directory(settings.runDir, "run_dir", writable=True))

    account, permission_check = _load_account(settings)
    report.checks.append(permission_check)
    if account is None:
        report.checks.append(_skip("jwt", "私钥不可用，跳过 JWT"))
        report.checks.append(_skip("api_host", "私钥不可用，跳过接口探测"))
        report.checks.append(_skip("clock", "没有完成接口探测，跳过时钟比对"))
        report.checks.append(_skip("app_identity", "没有完成接口探测，跳过包名比对"))
        report.checks.append(_skip("service_account_permission", "没有完成接口探测，跳过权限探测"))
        return _finish(report)

    try:
        token = build_jwt(account, issued_at=int(clock().timestamp()))
    except Exception as exc:
        report.checks.append(
            Check(
                "jwt",
                False,
                f"JWT 签名失败：{type(exc).__name__}",
                ExitCode.LOCAL_PRECONDITION_FAILED,
            )
        )
        return _finish(report)
    if token.count(".") != 2:
        report.checks.append(Check("jwt", False, "JWT 结构无效", ExitCode.TOOL_INTERNAL_ERROR))
        return _finish(report)
    report.checks.append(Check("jwt", True, "PS256 JWT 已在内存中生成"))

    api = client or HttpxAppInfoClient(settings)
    probe = api.get_app_info(app_id=settings.appId, token=token)
    lookup = None
    if probe.tls_verified and probe.connected and probe.error_code is None and probe.ret_code in {None, 0}:
        lookup = api.lookup_package(package_name=settings.bundleName, token=token)
    report.remote_state = {
        "path": APP_INFO_PATH,
        "connected": probe.connected,
        "statusCode": probe.status_code,
    }
    report.checks.extend(_probe_checks(settings, probe, clock(), lookup))
    return _finish(report)


def _finish(report: DoctorReport) -> DoctorReport:
    failed = [check for check in report.checks if not check.ok and not check.skipped]
    if not failed:
        return report
    report.exit_code = worst_exit([check.exit_code for check in failed])
    primary = next(check for check in failed if check.exit_code == report.exit_code)
    report.code = primary.name.upper()
    report.message = primary.message
    return report


def _check_tool_version(tool_version: str) -> Check:
    if tool_version != __version__:
        return Check(
            "tool_version",
            False,
            f"请求 toolVersion 是 {tool_version}，当前 CLI 是 {__version__}",
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    return Check("tool_version", True, __version__)


def _check_app_id(settings: Settings, app_id: str) -> Check:
    if app_id != settings.appId:
        return Check(
            "app_id",
            False,
            "请求 appId 与配置不一致",
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    return Check("app_id", True, "appId 与配置一致")


def _check_manifest(settings: Settings) -> Check:
    if settings.manifestFile is None:
        if settings.requireManifest:
            return Check("manifest", False, "未配置 manifestFile", ExitCode.LOCAL_PRECONDITION_FAILED)
        return _skip("manifest", "未配置 manifestFile")
    path = settings.manifestFile
    if not path.is_file():
        return Check("manifest", False, "MANIFEST.json 不存在", ExitCode.LOCAL_PRECONDITION_FAILED)
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
        version = payload["version"]
        expected = payload["sha256"]
        subject = Path(payload["payload"])
        digest = hashlib.sha256(subject.read_bytes()).hexdigest()
    except (OSError, UnicodeError, json.JSONDecodeError, KeyError, TypeError):
        return Check("manifest", False, "MANIFEST.json 无法读取或字段缺失", ExitCode.LOCAL_PRECONDITION_FAILED)
    if version != __version__ or digest != expected:
        return Check(
            "manifest",
            False,
            "MANIFEST 版本或 SHA-256 不一致",
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    return Check("manifest", True, "MANIFEST 版本和 SHA-256 一致")


def _check_directory(path: Path, name: str, *, writable: bool) -> Check:
    if not path.is_dir():
        return Check(name, False, f"{path} 不是可读目录", ExitCode.LOCAL_PRECONDITION_FAILED)
    if not os.access(path, os.R_OK | os.X_OK):
        return Check(name, False, f"{path} 不可读", ExitCode.LOCAL_PRECONDITION_FAILED)
    if not writable:
        return Check(name, True, f"{path} 可读")
    probe = path / ".agcctl-doctor-write-test"
    try:
        probe.write_text("ok", encoding="utf-8")
        probe.unlink()
    except OSError:
        return Check(name, False, f"{path} 不可写", ExitCode.LOCAL_PRECONDITION_FAILED)
    return Check(name, True, f"{path} 可写")


def _load_account(settings: Settings) -> tuple[Any, Check]:
    path = settings.privateKeyFile
    try:
        info = path.lstat()
    except OSError:
        return None, Check(
            "private_key_permissions",
            False,
            "private.json 不存在或不可访问",
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    if stat.S_ISLNK(info.st_mode):
        return None, Check(
            "private_key_permissions",
            False,
            "private.json 不能是符号链接",
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    if not stat.S_ISREG(info.st_mode):
        return None, Check(
            "private_key_permissions",
            False,
            "private.json 必须是普通文件",
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    if stat.S_IMODE(info.st_mode) != 0o600:
        return None, Check(
            "private_key_permissions",
            False,
            "private.json 权限必须是 0600",
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    owner = _owner_name(info.st_uid)
    expected = settings.releaseUser or _owner_name(os.getuid())
    if owner != expected:
        return None, Check(
            "private_key_permissions",
            False,
            "private.json 属主与 releaseUser 不一致",
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    try:
        account = load_service_account(path)
    except ValueError as exc:
        return None, Check(
            "private_key_permissions",
            False,
            str(exc),
            ExitCode.LOCAL_PRECONDITION_FAILED,
        )
    return account, Check("private_key_permissions", True, "权限 0600，属主正确")


def _owner_name(uid: int) -> str:
    try:
        return pwd.getpwuid(uid).pw_name
    except KeyError:
        return str(uid)


def _probe_checks(
    settings: Settings,
    probe: AppInfoProbe,
    now: datetime,
    lookup: PackageLookup | None,
) -> list[Check]:
    if not probe.tls_verified:
        return [
            Check("api_host", False, "TLS 证书校验失败", ExitCode.NON_RETRYABLE),
            _skip("clock", "TLS 校验失败，跳过时钟比对"),
            _skip("app_identity", "TLS 校验失败，跳过包名比对"),
            _skip("service_account_permission", "TLS 校验失败，跳过权限探测"),
        ]
    if not probe.connected:
        return [
            Check("api_host", False, "华为 API Host 不可达", ExitCode.SAFE_RETRY),
            _skip("clock", "接口不可达，跳过时钟比对"),
            _skip("app_identity", "接口不可达，跳过包名比对"),
            _skip("service_account_permission", "接口不可达，跳过权限探测"),
        ]

    checks = [Check("api_host", True, "华为 API Host 可达，TLS 校验通过")]
    checks.append(_clock_check(settings, probe, now))
    checks.append(_identity_check(settings, probe, lookup))
    checks.append(_permission_check(probe))
    return checks


def _clock_check(settings: Settings, probe: AppInfoProbe, now: datetime) -> Check:
    if probe.date is None:
        return Check("clock", False, "响应没有 Date 头", ExitCode.LOCAL_PRECONDITION_FAILED)
    remote = probe.date if probe.date.tzinfo else probe.date.replace(tzinfo=timezone.utc)
    skew = abs((now - remote).total_seconds())
    if skew > settings.maxClockSkewSeconds:
        return Check("clock", False, "本机时间与华为响应时间相差过大", ExitCode.LOCAL_PRECONDITION_FAILED)
    return Check("clock", True, "本机时间与响应 Date 一致")


def _identity_check(settings: Settings, probe: AppInfoProbe, lookup: PackageLookup | None) -> Check:
    if probe.error_code in {"PERMISSION_DENIED", "HTTP_429", "HTTP_5XX", "HTTP_UNEXPECTED"}:
        return _skip("app_identity", "接口未返回应用信息，跳过包名比对")
    if probe.ret_code not in {None, 0}:
        return _skip("app_identity", "应用信息业务码非成功，跳过包名比对")
    if lookup is None:
        return _skip("app_identity", "没有完成包名查询，跳过包名比对")
    if not lookup.tls_verified:
        return Check("app_identity", False, "包名查询 TLS 证书校验失败", ExitCode.NON_RETRYABLE)
    if not lookup.connected:
        return Check("app_identity", False, "包名查询不可达", ExitCode.SAFE_RETRY)
    if lookup.error_code == "PERMISSION_DENIED":
        return Check("app_identity", False, "Service Account 无权按包名查询应用", ExitCode.NON_RETRYABLE)
    if lookup.error_code == "HTTP_429":
        return Check("app_identity", False, "包名查询被限流", ExitCode.SAFE_RETRY)
    if lookup.error_code == "HTTP_5XX":
        return Check("app_identity", False, "包名查询暂时失败", ExitCode.SAFE_RETRY)
    if lookup.error_code == "HTTP_UNEXPECTED":
        return Check("app_identity", False, lookup.message, ExitCode.NON_RETRYABLE)
    if lookup.ret_code not in {None, 0}:
        return Check("app_identity", False, "包名查询业务失败", ExitCode.NON_RETRYABLE)
    if lookup.app_id == settings.appId:
        return Check("app_identity", True, "bundleName 对应的 appId 与配置一致")
    return Check("app_identity", False, "bundleName 没有对应到配置的 appId", ExitCode.NON_RETRYABLE)


def _permission_check(probe: AppInfoProbe) -> Check:
    if probe.error_code == "PERMISSION_DENIED":
        return Check("service_account_permission", False, "Service Account 无权查询应用", ExitCode.NON_RETRYABLE)
    if probe.error_code == "HTTP_429":
        return Check("service_account_permission", False, "华为接口限流", ExitCode.SAFE_RETRY)
    if probe.error_code == "HTTP_5XX":
        return Check("service_account_permission", False, "华为接口暂时失败", ExitCode.SAFE_RETRY)
    if probe.error_code == "HTTP_UNEXPECTED":
        return Check("service_account_permission", False, probe.message, ExitCode.NON_RETRYABLE)
    if probe.ret_code not in {None, 0}:
        return Check("service_account_permission", False, "应用查询业务失败", ExitCode.NON_RETRYABLE)
    return Check("service_account_permission", True, "只读应用查询成功")


def _skip(name: str, message: str) -> Check:
    return Check(name, True, message, skipped=True)

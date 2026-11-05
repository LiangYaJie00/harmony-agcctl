"""Command entrypoint. stdout is one JSON object; stderr is redacted JSON logs."""

from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path
from typing import Any, TextIO

from agc_cli.api import AgcSession
from agc_cli.config import load_settings
from agc_cli.doctor import run_doctor
from agc_cli.errors import EXIT_TO_RETRY_CLASS, CommandError, ExitCode
from agc_cli.publishing import get_app_info, get_package_info, list_versions, promote_full, submit, update_language, update_package
from agc_cli.redaction import redact_text, redact_value
from agc_cli.schema_io import SchemaValidationError, validate_request, validate_response
from agc_cli.testing import (
    add_package,
    compile_status,
    create_version,
    delete_version,
    get_version,
    list_groups,
    list_testers,
    notify,
    stop_version,
    submit_version,
    update_version,
)
from agc_cli.upload import upload_package
from agc_cli.version import __version__

Handler = Any
_COMMANDS: dict[tuple[str, str], tuple[str, Handler]] = {
    ("upload", "package"): ("UPLOAD_PACKAGE", upload_package),
    ("package", "compile-status"): ("PACKAGE_COMPILE_STATUS", compile_status),
    ("test", "create-version"): ("TEST_VERSION_CREATE", create_version),
    ("test", "add-package"): ("TEST_PACKAGE_ADD", add_package),
    ("test", "update-version"): ("TEST_VERSION_UPDATE", update_version),
    ("test", "submit-version"): ("TEST_VERSION_SUBMIT", submit_version),
    ("test", "list-groups"): ("TEST_GROUP_LIST", list_groups),
    ("test", "list-testers"): ("TEST_TESTER_LIST", list_testers),
    ("test", "notify"): ("TEST_NOTIFY", notify),
    ("test", "get-version"): ("TEST_VERSION_GET", get_version),
    ("test", "stop-version"): ("APPTEST_STOP", stop_version),
    ("test", "delete-version"): ("APPTEST_DELETE", delete_version),
    ("publish", "get-app-info"): ("PUBLISH_APP_INFO", get_app_info),
    ("publish", "update-package"): ("PUBLISH_PACKAGE_UPDATE", update_package),
    ("publish", "update-language"): ("PUBLISH_LANGUAGE_UPDATE", update_language),
    ("publish", "submit"): ("PUBLISH_SUBMIT", submit),
    ("publish", "list-versions"): ("PUBLISH_VERSION_LIST", list_versions),
    ("publish", "get-package-info"): ("PUBLISH_PACKAGE_INFO", get_package_info),
    ("publish", "promote-full"): ("PUBLISH_PROMOTE_FULL", promote_full),
}


def main(argv: list[str] | None = None) -> None:
    sys.exit(run(argv))


def run(argv: list[str] | None = None, **kwargs: Any) -> int:
    parser = argparse.ArgumentParser(prog="agcctl")
    parser.add_argument("--version", action="version", version=__version__)
    subcommands = parser.add_subparsers(dest="command", required=True)

    doctor = subcommands.add_parser("doctor", help="检查配置、私钥、JWT 和华为接口连通性")
    doctor.add_argument("--config", required=True, type=Path)
    doctor.add_argument("--request-file", type=Path)
    _add_command_groups(subcommands)

    args = parser.parse_args(argv)
    started = time.monotonic()
    if args.command == "doctor":
        return _run_doctor(args, started, kwargs)
    return _run_api(args, started, kwargs.get("transport"))


def _run_doctor(args: argparse.Namespace, started: float, doctor_kwargs: dict[str, Any]) -> int:
    operation_key = "UNKNOWN"
    try:
        request = _read_request(args.request_file)
        operation_key = str(request.get("operationKey") or operation_key)
        validate_request(request)
        _require_operation_key(request)
        settings = load_settings(args.config)
        report = run_doctor(
            settings,
            tool_version=str(request["toolVersion"]),
            app_id=str(request["appId"]),
            **doctor_kwargs,
        )
        response = _response(
            success=report.success,
            operation_key=operation_key,
            exit_code=report.exit_code,
            started=started,
            code=report.code,
            message=report.message,
            remote_state=report.remote_state,
            data={"checks": [check.as_dict() for check in report.checks]},
        )
    except SchemaValidationError as exc:
        response = _failure(operation_key, started, ExitCode.LOCAL_PRECONDITION_FAILED, "SCHEMA_INVALID", exc.message)
    except ValueError as exc:
        response = _failure(
            operation_key,
            started,
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "REQUEST_INVALID",
            str(exc),
        )
    except Exception as exc:
        response = _failure(
            operation_key,
            started,
            ExitCode.TOOL_INTERNAL_ERROR,
            "TOOL_INTERNAL_ERROR",
            type(exc).__name__,
        )

    return _emit_validated(response, operation_key, started, "DOCTOR")


def _add_command_groups(subcommands: argparse._SubParsersAction) -> None:
    groups = {
        "upload": ["package"],
        "package": ["compile-status"],
        "test": [
            "create-version",
            "add-package",
            "update-version",
            "submit-version",
            "list-groups",
            "list-testers",
            "notify",
            "get-version",
            "stop-version",
            "delete-version",
        ],
        "publish": [
            "get-app-info",
            "update-package",
            "update-language",
            "submit",
            "list-versions",
            "get-package-info",
            "promote-full",
        ],
    }
    for group, actions in groups.items():
        parser = subcommands.add_parser(group)
        nested = parser.add_subparsers(dest="action", required=True)
        for action in actions:
            leaf = nested.add_parser(action)
            leaf.add_argument("--config", required=True, type=Path)
            leaf.add_argument("--request-file", type=Path)
            if group == "publish" and action == "submit":
                leaf.add_argument("--dry-run", action="store_true")


def _run_api(args: argparse.Namespace, started: float, transport: Any) -> int:
    operation, handler = _COMMANDS[(args.command, args.action)]
    operation_key = "UNKNOWN"
    try:
        request = _read_request(args.request_file)
        operation_key = str(request.get("operationKey") or operation_key)
        validate_request(request)
        _require_operation_key(request)
        if str(request["toolVersion"]) != __version__:
            raise ValueError("toolVersion 与当前 CLI 版本不一致")
        settings = load_settings(args.config)
        if str(request["appId"]) != settings.appId:
            raise ValueError("请求 appId 与配置不一致")
        session = AgcSession(settings, transport)
        if operation == "PUBLISH_SUBMIT":
            outcome = handler(settings, request, session, dry_run=bool(getattr(args, "dry_run", False)))
        else:
            outcome = handler(settings, request, session)
        response = _response(
            success=True,
            operation=str(outcome.get("operation") or operation),
            operation_key=operation_key,
            exit_code=ExitCode.SUCCESS,
            started=started,
            code="OK",
            message="ok",
            remote_state=outcome["remoteState"],
            data=outcome["data"],
            external_ids=outcome["externalIds"],
            request_id=outcome.get("requestId"),
        )
    except CommandError as exc:
        response = _failure(operation_key, started, exc.exit_code, exc.code, exc.message, operation)
    except SchemaValidationError as exc:
        response = _failure(operation_key, started, ExitCode.LOCAL_PRECONDITION_FAILED, "SCHEMA_INVALID", exc.message, operation)
    except (ValueError, KeyError) as exc:
        response = _failure(operation_key, started, ExitCode.LOCAL_PRECONDITION_FAILED, "REQUEST_INVALID", str(exc), operation)
    except Exception:
        response = _failure(operation_key, started, ExitCode.TOOL_INTERNAL_ERROR, "TOOL_INTERNAL_ERROR", "工具内部错误", operation)
    return _emit_validated(response, operation_key, started, operation)


def _emit_validated(response: dict[str, Any], operation_key: str, started: float, operation: str) -> int:
    try:
        validate_response(response)
    except SchemaValidationError:
        response = _failure(operation_key, started, ExitCode.TOOL_INTERNAL_ERROR, "RESPONSE_SCHEMA_INVALID", "CLI 响应不符合 JSON Schema", operation)
    _emit(response)
    return _exit_code(response)


def _read_request(path: Path | None) -> dict[str, Any]:
    if path is None:
        if sys.stdin.isatty():
            raise ValueError("缺少 --request-file，且标准输入为空")
        raw = sys.stdin.read()
    else:
        try:
            raw = path.read_text(encoding="utf-8")
        except OSError as exc:
            raise ValueError("无法读取请求文件") from exc
    try:
        payload = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError("请求不是合法 JSON") from exc
    if not isinstance(payload, dict):
        raise ValueError("请求必须是 JSON 对象")
    return payload


def _require_operation_key(request: dict[str, Any]) -> None:
    task_id = str(request["taskId"])
    operation_key = str(request["operationKey"])
    if not operation_key.startswith(f"{task_id}:"):
        raise ValueError("operationKey 必须以 taskId 加冒号开头")


def _failure(
    operation_key: str,
    started: float,
    exit_code: ExitCode,
    code: str,
    message: str,
    operation: str = "DOCTOR",
) -> dict[str, Any]:
    data = {"checks": []} if operation == "DOCTOR" else {}
    return _response(
        success=False,
        operation=operation,
        operation_key=operation_key,
        exit_code=exit_code,
        started=started,
        code=code,
        message=message,
        remote_state={},
        data=data,
    )


def _response(
    *,
    success: bool,
    operation_key: str,
    exit_code: ExitCode,
    started: float,
    code: str,
    message: str,
    remote_state: dict[str, Any],
    data: dict[str, Any],
    operation: str = "DOCTOR",
    external_ids: dict[str, str] | None = None,
    request_id: str | None = None,
) -> dict[str, Any]:
    error = None
    if not success:
        error = {
            "category": EXIT_TO_RETRY_CLASS[exit_code],
            "code": code,
            "message": redact_text(message),
            "detail": None,
        }
    return {
        "schemaVersion": "1.0",
        "success": success,
        "operation": operation,
        "operationKey": operation_key or "UNKNOWN",
        "toolVersion": __version__,
        "externalIds": external_ids or {},
        "remoteState": remote_state,
        "requestId": request_id,
        "retryClass": EXIT_TO_RETRY_CLASS[exit_code],
        "error": error,
        "durationMs": max(0, int((time.monotonic() - started) * 1000)),
        "data": data,
    }


def _exit_code(response: dict[str, Any]) -> int:
    retry_class = response["retryClass"]
    for code, name in EXIT_TO_RETRY_CLASS.items():
        if name == retry_class:
            return int(code)
    return int(ExitCode.TOOL_INTERNAL_ERROR)


def _emit(response: dict[str, Any], stderr: TextIO | None = None) -> None:
    stream = stderr or sys.stderr
    log_record = {
        "taskId": response["operationKey"].split(":", 1)[0],
        "operationKey": response["operationKey"],
        "action": response["operation"],
        "toolVersion": response["toolVersion"],
        "retryClass": response["retryClass"],
        "durationMs": response["durationMs"],
        "result": "ok" if response["success"] else "error",
        "errorCategory": None if response["error"] is None else response["error"]["category"],
    }
    print(json.dumps(redact_value(log_record), ensure_ascii=False), file=stream)
    print(json.dumps(response, ensure_ascii=False, separators=(",", ":")), file=sys.stdout)

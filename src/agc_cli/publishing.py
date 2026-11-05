"""Publishing API commands. submit and promote-full are never chained from other commands."""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Any

from agc_cli.api import AgcSession
from agc_cli.config import Settings
from agc_cli.errors import CommandError, ExitCode

PHASE_PERCENT = {1: 1, 2: 2, 3: 5, 4: 10, 5: 20, 6: 50, 7: 100}


def get_app_info(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    params: dict[str, Any] = {"appId": settings.appId}
    version_id = request["request"].get("publishVersionId")
    if version_id:
        params["versionId"] = version_id
    exchange = session.call("GET", "/api/publish/v3/app-info", params=params)
    return _from_app_info(exchange)


def update_package(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    file_name = body.get("fileName")
    object_id = body.get("objectId")
    if set(body) != {"fileName", "objectId", "releasePhase"} or not isinstance(file_name, str) or not file_name or not isinstance(object_id, str) or not object_id:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "PUBLISH_PACKAGE_FIELDS",
            "更新正式软件包需要 fileName、objectId 和 releasePhase",
        )
    phase = _release_phase(body)
    exchange = session.call(
        "PUT",
        "/api/publish/v3/app-package-info",
        params={"appId": settings.appId, "releaseType": 1, "releasePhase": phase},
        json_body={"fileName": file_name, "objectId": object_id},
    )
    package_id = exchange.body.get("packageId")
    if not isinstance(package_id, str) or not package_id:
        raise CommandError(ExitCode.NON_RETRYABLE, "EXTERNAL_ID_MISSING", "响应缺少 packageId")
    return _ok(exchange, {"packageId": package_id}, {"releasePhase": phase})


def update_language(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    if set(body) != {"newFeatures", "releasePhase"}:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "PUBLISH_LANGUAGE_FIELDS",
            "更新语言信息需要 newFeatures 和 releasePhase",
        )
    features = body.get("newFeatures")
    phase = _release_phase(body)
    _check_length(features, field="newFeatures", required=True, maximum=500)
    exchange = session.call(
        "PUT",
        "/api/publish/v3/app-language-info",
        params={"appId": settings.appId, "releaseType": 1, "releasePhase": phase},
        json_body={"lang": settings.defaultLanguage, "newFeatures": features},
    )
    return _ok(exchange, {}, {"language": settings.defaultLanguage, "releasePhase": phase})


def submit(settings: Settings, request: dict[str, Any], session: AgcSession, *, dry_run: bool = False) -> dict[str, Any]:
    payload = _submit_payload(request["request"])
    if dry_run:
        return {
            "externalIds": {},
            "remoteState": {"dryRun": True},
            "data": {"request": payload},
            "requestId": None,
        }
    exchange = session.call("POST", "/api/publish/v3/app-submit", params={"appId": settings.appId}, json_body=payload)
    return _ok(exchange, {}, {"submitted": True})


def list_versions(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    exchange = session.call("POST", "/api/publish/v3/version/brief-info/list", json_body={"appId": settings.appId})
    versions = exchange.body.get("versionList") or exchange.body.get("versions") or []
    if not isinstance(versions, list):
        versions = []
    matched = _match_version(versions, body.get("versionCode"))
    external: dict[str, str] = {}
    remote: dict[str, Any] = {"candidateCount": len(matched) if body.get("versionCode") is not None else len(versions)}
    data: dict[str, Any] = {"versions": versions}
    if len(matched) == 1:
        version_id = matched[0].get("versionId")
        if isinstance(version_id, str) and version_id:
            external["publishVersionId"] = version_id
        _attach_phase(matched[0], remote, data)
    elif body.get("versionCode") is not None and len(matched) > 1:
        remote["multipleCandidates"] = True
    return _ok(exchange, external, remote, data)


def get_package_info(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    package_id = body.get("packageId")
    if set(body) != {"packageId"} or not isinstance(package_id, str) or not package_id:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "PUBLISH_PACKAGE_ID_REQUIRED", "查询正式软件包需要 packageId")
    exchange = session.call("GET", "/api/publish/v2/app-package-info", params={"packageId": package_id})
    package_info = exchange.body.get("packageInfo")
    if not isinstance(package_info, dict):
        raise CommandError(ExitCode.NON_RETRYABLE, "PACKAGE_INFO_MISSING", "响应缺少 packageInfo")
    return _ok(exchange, {"packageId": package_id}, {}, {"packageInfo": package_info})


def promote_full(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    if body.get("confirmed") is not True:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "PROMOTE_NOT_CONFIRMED", "转全网必须由平台确认后单独调用")
    if body.get("versionMismatch") or body.get("resultUnknown") or body.get("paused") or body.get("cancelled"):
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "PROMOTE_BLOCKED", "当前版本状态不允许转全网")
    version_id = body.get("publishVersionId")
    if not isinstance(version_id, str) or not version_id or "versionId" in body or "packageId" in body:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "PUBLISH_VERSION_ID_REQUIRED",
            "转全网只接受正式 publishVersionId",
        )
    exchange = session.call(
        "PUT",
        "/api/publish/v2/version/phased-release",
        json_body={"versionId": version_id, "releasePhase": 4},
    )
    return _ok(exchange, {"publishVersionId": version_id}, {"releasePhase": 4, "accepted": True})


def _release_phase(body: dict[str, Any]) -> int:
    phase = body.get("releasePhase")
    if isinstance(phase, bool) or phase not in {0, 3}:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "RELEASE_PHASE_INVALID", "releasePhase 只能是 0（全网）或 3（7天分阶段）")
    return phase


def _submit_payload(body: dict[str, Any]) -> dict[str, Any]:
    mode = body.get("releaseMode")
    if mode == "FULL":
        phase = 0
        if body.get("phasedReleaseDescription"):
            raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "PHASED_DESCRIPTION_FORBIDDEN", "全网发布不能携带分阶段说明")
    elif mode == "PHASED":
        phase = 3
        _check_length(body.get("phasedReleaseDescription"), field="phasedReleaseDescription", required=True, maximum=500)
    else:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "RELEASE_MODE_INVALID", "发布方式必须是 FULL 或 PHASED")
    payload: dict[str, Any] = {"releaseType": 1, "releasePhase": phase}
    if mode == "PHASED":
        payload["phasedReleaseDescription"] = body["phasedReleaseDescription"]
    schedule = body.get("schedule")
    if schedule == "IMMEDIATE":
        if body.get("releaseTime"):
            raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "RELEASE_TIME_FORBIDDEN", "立即发布不能携带预约时间")
    elif schedule == "SCHEDULED":
        payload["releaseTime"] = _future_release_time(body.get("releaseTime"))
    else:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "SCHEDULE_INVALID", "发布时间必须是 IMMEDIATE 或 SCHEDULED")
    remark = body.get("remark")
    if remark not in (None, ""):
        _check_length(remark, field="remark", required=True, minimum=10, maximum=300)
        payload["remark"] = remark
    return payload


def _future_release_time(value: Any) -> str:
    if not isinstance(value, str) or not value:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "RELEASE_TIME_INVALID", "预约时间必须带时区")
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError as exc:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "RELEASE_TIME_INVALID", "预约时间格式无效") from exc
    if parsed.tzinfo is None or parsed <= datetime.now(timezone.utc):
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "RELEASE_TIME_INVALID", "预约时间必须带时区且晚于当前时间")
    return value


def _check_length(value: Any, *, field: str, required: bool, maximum: int, minimum: int = 1) -> None:
    if value in (None, ""):
        if required:
            raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, f"{field.upper()}_INVALID", f"{field} 不能为空")
        return
    if not isinstance(value, str) or not minimum <= len(value) <= maximum:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            f"{field.upper()}_INVALID",
            f"{field} 长度必须是 {minimum} 到 {maximum}",
        )


def _match_version(versions: list[Any], version_code: Any) -> list[dict[str, Any]]:
    rows = [item for item in versions if isinstance(item, dict)]
    if version_code is None:
        return rows
    return [item for item in rows if _is_publish_version(item) and _version_code_matches(item, version_code)]


def _is_publish_version(item: dict[str, Any]) -> bool:
    release_type = item.get("releaseType")
    return release_type in (None, 1)


def _version_code_matches(item: dict[str, Any], version_code: Any) -> bool:
    if item.get("versionCode") == version_code:
        return True
    packages = item.get("packageList")
    if not isinstance(packages, list):
        return False
    return any(isinstance(package, dict) and package.get("versionCode") == version_code for package in packages)


def _from_app_info(exchange) -> dict[str, Any]:
    app_info = exchange.body.get("appInfo") if isinstance(exchange.body.get("appInfo"), dict) else {}
    remote = {
        "releaseState": app_info.get("releaseState"),
        "releasePhase": app_info.get("releasePhase"),
    }
    data: dict[str, Any] = {}
    phase = app_info.get("phasedReleaseInfo")
    if isinstance(phase, dict):
        remote["phasedReleaseInfo"] = phase
        _attach_phase(app_info, remote, data)
    version_id = app_info.get("versionId")
    external = {"publishVersionId": version_id} if isinstance(version_id, str) and version_id else {}
    return _ok(exchange, external, remote, data)


def _attach_phase(source: dict[str, Any], remote: dict[str, Any], data: dict[str, Any]) -> None:
    info = source.get("phasedReleaseInfo")
    if not isinstance(info, dict):
        return
    remote["phasedReleaseInfo"] = info
    day = info.get("currentPhaseDay")
    if isinstance(day, int) and day in PHASE_PERCENT:
        data["phasePercent"] = PHASE_PERCENT[day]


def _ok(exchange, external_ids: dict[str, str], remote_state: dict[str, Any], data: dict[str, Any] | None = None) -> dict[str, Any]:
    return {
        "externalIds": external_ids,
        "remoteState": remote_state,
        "data": data or {},
        "requestId": exchange.request_id,
    }

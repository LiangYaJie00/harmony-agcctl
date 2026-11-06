"""Testing API commands. Each function performs one request, except stop and delete which query first."""

from __future__ import annotations

from typing import Any

from agc_cli.api import AgcSession, classify
from agc_cli.config import Settings
from agc_cli.errors import CommandError, ExitCode

_STOPPED = {2, 10}
_ADD_PACKAGE_FIELDS = {"versionId", "fileName", "objectId"}
_APP_MARKET = 2


def create_version(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    test_desc = body.get("testDesc")
    if not isinstance(test_desc, str) or not 1 <= len(test_desc) <= 50:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "TEST_DESC_INVALID", "testDesc 必须是 1 到 50 个字符")
    self_detect = body.get("onshelfSelfDetect", 0)
    if isinstance(self_detect, bool) or self_detect not in {0, 1}:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "SELF_DETECT_INVALID",
            "onshelfSelfDetect 只能是 0 或 1，缺省不开启上架自检",
        )
    exchange = session.call(
        "POST",
        "/api/publish/v2/test/app/version",
        params={"appId": settings.appId},
        json_body={
            "releaseType": settings.testReleaseType,
            "testType": settings.testType,
            "testDesc": test_desc,
            "onshelfSelfDetect": self_detect,
        },
    )
    version_id = _required_id(exchange.body, "versionId")
    return _ok(exchange, {"versionId": version_id}, {"releaseState": _release_state(exchange.body), "onshelfSelfDetect": self_detect})


def add_package(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    unknown = set(body) - _ADD_PACKAGE_FIELDS
    file_name = body.get("fileName")
    object_id = body.get("objectId")
    if unknown or not isinstance(file_name, str) or not file_name or not isinstance(object_id, str) or not object_id:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "TEST_PACKAGE_FIELDS",
            "添加软件包只接受 fileName 和 objectId",
        )
    exchange = session.call(
        "POST",
        "/api/publish/v2/test/version/pkg",
        params={"appId": settings.appId},
        json_body={"distributeMode": _APP_MARKET, "file": {"fileName": file_name, "objectId": object_id}},
    )
    pkg_id = _single_pkg_version(exchange.body)
    return _ok(exchange, {"pkgId": pkg_id}, {})


def compile_status(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    pkg_id = str(request["request"]["pkgId"])
    exchange = session.call(
        "GET",
        "/api/publish/v3/package/compile/status",
        params={"appId": settings.appId, "pkgIds": pkg_id},
    )
    matches = [
        item
        for item in exchange.body.get("pkgStateList", [])
        if isinstance(item, dict) and str(item.get("pkgId")) == pkg_id
    ]
    if len(matches) != 1:
        raise CommandError(ExitCode.NON_RETRYABLE, "PACKAGE_STATE_NOT_UNIQUE", "编译状态没有唯一匹配的软件包")
    status = matches[0].get("successStatus")
    if status not in {0, 1, 2}:
        raise CommandError(ExitCode.NON_RETRYABLE, "PACKAGE_STATE_UNKNOWN", "编译状态取值无法识别")
    return _ok(exchange, {}, {"pkgId": pkg_id, "successStatus": status}, {"packageState": matches[0]})


def update_version(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    group_ids = body.get("groupIds")
    if group_ids is None and body.get("groupId"):
        group_ids = [body["groupId"]]
    if not isinstance(group_ids, list) or not group_ids or not all(isinstance(item, str) and item for item in group_ids):
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "TEST_GROUP_REQUIRED", "更新测试版本必须绑定测试群组")
    start_time = body.get("startTime")
    end_time = body.get("endTime")
    if not isinstance(start_time, int) or not isinstance(end_time, int) or end_time <= start_time:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "TEST_WINDOW_INVALID",
            "测试开始和结束时间必须是毫秒时间戳，且结束时间晚于开始时间",
        )
    payload = {
        "versionId": body["versionId"],
        "pkgId": body["pkgId"],
        "openTestInfo": {
            "startTime": start_time,
            "endTime": end_time,
            "testTaskInfo": {
                "groupInfos": [{"groupId": group_id, "bind": 1} for group_id in group_ids],
                "needShareLink": 1,
                "displayArea": "1",
                "needNotify": 0,
            },
        },
    }
    exchange = session.call(
        "PUT",
        "/api/publish/v2/test/app/version",
        params={"appId": settings.appId},
        json_body=payload,
    )
    return _ok(exchange, {}, {"needShareLink": 1, "displayArea": "1", "needNotify": 0})


def submit_version(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    exchange = session.call(
        "POST",
        "/api/publish/v2/test/app/version/submit",
        params={"appId": settings.appId},
        json_body={"versionId": request["request"]["versionId"]},
    )
    return _ok(exchange, {}, {"releaseState": _release_state(exchange.body)})


def list_groups(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    group_type = body.get("groupType", 1)
    if set(body) - {"groupType"} or isinstance(group_type, bool) or group_type not in {0, 1}:
        raise CommandError(ExitCode.LOCAL_PRECONDITION_FAILED, "GROUP_TYPE_INVALID", "groupType 只能是 0 或 1，缺省查询内部群组")
    exchange = session.call(
        "GET",
        "/api/app-test/v1/test-group/list",
        params={"appId": settings.appId, "groupType": group_type},
    )
    groups = exchange.body.get("groups") or exchange.body.get("groupList") or []
    return _ok(exchange, {}, {"groupType": group_type}, {"groups": groups})


def list_testers(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    group_id = body.get("groupId")
    current = body.get("current")
    page_size = body.get("pageSize")
    if (
        not isinstance(group_id, str)
        or not group_id
        or isinstance(current, bool)
        or not isinstance(current, int)
        or current < 1
        or isinstance(page_size, bool)
        or not isinstance(page_size, int)
        or page_size < 1
    ):
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "TESTER_QUERY_INVALID",
            "查询测试成员需要 groupId、current 和 pageSize",
        )
    exchange = session.call(
        "GET",
        "/api/app-test/v1/test-user",
        params={"appId": settings.appId, "current": current, "pageSize": page_size},
        headers={"groupId": group_id},
    )
    testers = exchange.body.get("testerInfo") or []
    return _ok(exchange, {}, {}, {"testerInfo": testers, "pageInfo": exchange.body.get("pageInfo") or {}})


def notify(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    tester_ids = body["testerIds"]
    if not isinstance(tester_ids, list) or not tester_ids or len(tester_ids) > 10000:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "TESTER_IDS_INVALID",
            "testerIds 必须是 1 到 10000 个成员 ID",
        )
    exchange = session.call(
        "POST",
        settings.notifyPath,
        json_body={"groupId": body["groupId"], "testerIds": tester_ids},
    )
    return _ok(exchange, {}, {"notified": True}, {"acceptedCount": len(tester_ids)})


def get_version(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    version_id = str(request["request"]["versionId"])
    exchange = session.call(
        "GET",
        "/api/publish/v3/app-info",
        params={"appId": settings.appId, "releaseType": settings.testReleaseType, "versionId": version_id},
    )
    app_info = exchange.body.get("appInfo") if isinstance(exchange.body.get("appInfo"), dict) else {}
    return _ok(
        exchange,
        {},
        {"releaseState": app_info.get("releaseState"), "versionId": version_id},
        {"shareLink": app_info.get("shareLink")},
    )


def stop_version(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    version_id = str(request["request"]["versionId"])
    current = _query_version(settings, session, version_id)
    if current.status_code == 404:
        return _already("TEST_VERSION_STOP", version_id, "测试版本已不存在")
    state = _release_state(current.body)
    if state in _STOPPED:
        return _already("TEST_VERSION_STOP", version_id, "测试版本已经停止")
    exchange = session.call(
        "POST",
        "/api/publish/v2/test/version/stop",
        params={"appId": settings.appId},
        json_body={"versionId": version_id},
    )
    return _ok(exchange, {"versionId": version_id}, {"stopped": True})


def delete_version(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    version_id = str(request["request"]["versionId"])
    current = _query_version(settings, session, version_id)
    if current.status_code == 404:
        return _already("TEST_VERSION_DELETE", version_id, "测试版本已不存在")
    exchange = session.call("DELETE", "/api/publish/v2/test/app/version", params={"appId": settings.appId, "versionId": version_id})
    return _ok(exchange, {"versionId": version_id}, {"deleted": True})


def _query_version(settings: Settings, session: AgcSession, version_id: str):
    exchange = session.call(
        "GET",
        "/api/publish/v3/app-info",
        params={"appId": settings.appId, "releaseType": settings.testReleaseType, "versionId": version_id},
        expect_json=False,
    )
    if exchange.status_code == 404:
        return exchange
    error = classify("GET", exchange)
    if error is not None:
        raise error
    return exchange


def _single_pkg_version(body: dict[str, Any]) -> str:
    values = body.get("pkgVersion")
    if not isinstance(values, list) or len(values) != 1:
        raise CommandError(ExitCode.NON_RETRYABLE, "PKG_VERSION_NOT_UNIQUE", "pkgVersion 必须恰好有一个元素")
    item = values[0]
    if isinstance(item, str) and item:
        return item
    if isinstance(item, dict):
        for key in ("pkgId", "id"):
            if isinstance(item.get(key), str) and item[key]:
                return item[key]
    raise CommandError(ExitCode.NON_RETRYABLE, "PKG_VERSION_NOT_UNIQUE", "pkgVersion[0] 不是软件包 ID")


def _required_id(body: dict[str, Any], key: str) -> str:
    value = body.get(key)
    if isinstance(value, str) and value:
        return value
    nested = body.get("appInfo")
    if isinstance(nested, dict) and isinstance(nested.get(key), str) and nested[key]:
        return nested[key]
    raise CommandError(ExitCode.NON_RETRYABLE, "EXTERNAL_ID_MISSING", f"响应缺少 {key}")


def _release_state(body: dict[str, Any]) -> Any:
    app_info = body.get("appInfo")
    if isinstance(app_info, dict) and "releaseState" in app_info:
        return app_info["releaseState"]
    return body.get("releaseState")


def _already(operation: str, version_id: str, message: str) -> dict[str, Any]:
    return {
        "operation": operation,
        "externalIds": {"versionId": version_id},
        "remoteState": {"alreadySatisfied": True},
        "data": {"message": message},
        "requestId": None,
    }


def _ok(
    exchange,
    external_ids: dict[str, str],
    remote_state: dict[str, Any],
    data: dict[str, Any] | None = None,
) -> dict[str, Any]:
    return {
        "externalIds": external_ids,
        "remoteState": remote_state,
        "data": data or {},
        "requestId": exchange.request_id,
    }

import hashlib
import json
from datetime import datetime, timedelta, timezone

import httpx

from agc_cli.cli import run
from agc_cli.errors import ExitCode
from agc_cli.version import __version__
from pathlib import Path

from scripts.build_offline_package import build_offline_package
from tests.helpers import APP_ID, generate_private_pem, request_body, write_config, write_private_json


class ScriptedTransport:
    def __init__(self, responses: list[httpx.Response | Exception]) -> None:
        self._responses = list(responses)
        self.requests: list[httpx.Request] = []

    def send(self, request: httpx.Request, timeout: httpx.Timeout) -> httpx.Response:
        self.requests.append(request)
        item = self._responses.pop(0)
        if isinstance(item, Exception):
            raise item
        return item


def _json_response(status: int, payload: dict) -> httpx.Response:
    return httpx.Response(status, json=payload)


def _run(tmp_path, capsys, argv: list[str], transport: ScriptedTransport, body: dict):
    private_pem, _public = generate_private_pem()
    key_path = write_private_json(tmp_path, private_pem)
    config_path = write_config(tmp_path, key_path)
    request_path = tmp_path / "request.json"
    request_path.write_text(json.dumps(body), encoding="utf-8")
    code = run([*argv, "--config", str(config_path), "--request-file", str(request_path)], transport=transport)
    stdout, stderr = capsys.readouterr()
    response = json.loads(stdout)
    return code, response, stderr, transport


def test_upload_rejects_hash_mismatch_without_network(tmp_path, capsys) -> None:
    artifact = _artifact(tmp_path, b"pack-bytes")
    transport = ScriptedTransport([])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["upload", "package"],
        transport,
        _request(
            "UPLOAD_PACKAGE",
            {
                "artifactPath": str(artifact),
                "fileName": artifact.name,
                "contentLength": artifact.stat().st_size,
                "sha256": "0" * 64,
            },
        ),
    )
    assert code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert response["error"]["code"] == "ARTIFACT_HASH_MISMATCH"
    assert transport.requests == []


def test_upload_rejects_symlink_outside_task_dir(tmp_path, capsys) -> None:
    outside = tmp_path / "outside.app"
    outside.write_bytes(b"other")
    artifact_root = tmp_path / "artifacts"
    task_dir = artifact_root / "PUBLISH-20261015-001"
    task_dir.mkdir(parents=True)
    link = task_dir / "qianshou.app"
    link.symlink_to(outside)
    transport = ScriptedTransport([])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["upload", "package"],
        transport,
        _request(
            "UPLOAD_PACKAGE",
            {
                "artifactPath": str(link),
                "fileName": link.name,
                "contentLength": 5,
                "sha256": hashlib.sha256(b"other").hexdigest(),
            },
        ),
    )
    assert code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert response["error"]["code"] == "ARTIFACT_SYMLINK_REJECTED"
    assert transport.requests == []


def test_upload_streams_put_and_hides_url(tmp_path, capsys) -> None:
    payload = b"signed-app-pack"
    artifact = _artifact(tmp_path, payload)
    upload_url = "https://bucket.obs.myhuaweicloud.com/object?Signature=secret"
    transport = ScriptedTransport(
        [
            _json_response(
                200,
                {"ret": {"code": 0}, "urlInfo": {"url": upload_url, "headers": {"Authorization": "obs-sign"}, "objectId": "obj-1"}},
            ),
            httpx.Response(200),
        ]
    )
    code, response, stderr, transport = _run(
        tmp_path,
        capsys,
        ["upload", "package"],
        transport,
        _request(
            "UPLOAD_PACKAGE",
            {
                "artifactPath": str(artifact),
                "fileName": artifact.name,
                "contentLength": len(payload),
                "sha256": hashlib.sha256(payload).hexdigest(),
            },
        ),
    )
    assert code == ExitCode.SUCCESS
    assert response["externalIds"] == {"objectId": "obj-1", "fileName": artifact.name}
    assert upload_url not in json.dumps(response)
    assert "obs-sign" not in stderr
    assert transport.requests[1].method == "PUT"


def test_upload_put_timeout_reconciles_once(tmp_path, capsys) -> None:
    payload = b"signed-app-pack"
    artifact = _artifact(tmp_path, payload)
    transport = ScriptedTransport(
        [
            _json_response(200, {"urlInfo": {"url": "https://bucket.obs.myhuaweicloud.com/object", "objectId": "obj-1"}}),
            httpx.ReadTimeout("timed out"),
        ]
    )
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["upload", "package"],
        transport,
        _request(
            "UPLOAD_PACKAGE",
            {
                "artifactPath": str(artifact),
                "fileName": artifact.name,
                "contentLength": len(payload),
                "sha256": hashlib.sha256(payload).hexdigest(),
            },
        ),
    )
    assert code == ExitCode.RECONCILE_FIRST
    assert len(transport.requests) == 2


def test_create_version_forces_test_type_and_timeout_is_reconcile(tmp_path, capsys) -> None:
    transport = ScriptedTransport([httpx.ReadTimeout("timed out")])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "create-version"],
        transport,
        _request("TEST_VERSION_CREATE", {"releaseType": 1, "testType": 1, "testDesc": "local verify"}),
    )
    assert code == ExitCode.RECONCILE_FIRST
    sent = json.loads(transport.requests[0].content)
    assert sent == {"releaseType": 6, "testType": 3, "testDesc": "local verify", "onshelfSelfDetect": 0}
    assert "appId=123456789" in str(transport.requests[0].url)
    assert len(transport.requests) == 1
    assert response["retryClass"] == "RECONCILE_FIRST"


def test_list_groups_defaults_to_internal_and_accepts_external(tmp_path, capsys) -> None:
    payload = {"rtnCode": 0, "groups": [{"groupId": "internal-1", "groupType": 1}]}
    transport = ScriptedTransport([_json_response(200, payload), _json_response(200, payload)])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "list-groups"],
        transport,
        _request("TEST_GROUP_LIST", {}),
    )
    assert code == ExitCode.SUCCESS
    assert "groupType=1" in str(transport.requests[0].url)
    assert response["remoteState"]["groupType"] == 1
    assert response["data"]["groups"][0]["groupId"] == "internal-1"

    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "list-groups"],
        transport,
        _request("TEST_GROUP_LIST", {"groupType": 0}),
    )
    assert code == ExitCode.SUCCESS
    assert "groupType=0" in str(transport.requests[1].url)
    assert response["remoteState"]["groupType"] == 0


def test_list_testers_sends_group_id_header(tmp_path, capsys) -> None:
    transport = ScriptedTransport(
        [_json_response(200, {"rtnCode": 0, "testerInfo": [{"id": "tester-1"}], "pageInfo": {"totalPage": 1}})]
    )
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "list-testers"],
        transport,
        _request("TEST_TESTER_LIST", {"groupId": "group-1", "current": 1, "pageSize": 25}),
    )
    assert code == ExitCode.SUCCESS
    sent = transport.requests[0]
    assert sent.headers["groupId"] == "group-1"
    assert "groupId=" not in str(sent.url)
    assert "current=1" in str(sent.url)
    assert response["data"]["testerInfo"][0]["id"] == "tester-1"


def test_submit_version_sends_app_id_query(tmp_path, capsys) -> None:
    transport = ScriptedTransport([_json_response(200, {"ret": {"code": 0, "msg": "success"}})])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "submit-version"],
        transport,
        _request("TEST_VERSION_SUBMIT", {"versionId": "ver-1"}),
    )
    assert code == ExitCode.SUCCESS
    assert json.loads(transport.requests[0].content) == {"versionId": "ver-1"}
    assert "appId=123456789" in str(transport.requests[0].url)
    assert response["success"] is True


def test_create_version_requires_test_desc(tmp_path, capsys) -> None:
    transport = ScriptedTransport([])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "create-version"],
        transport,
        _request("TEST_VERSION_CREATE", {}),
    )
    assert code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert response["error"]["code"] == "TEST_DESC_INVALID"
    assert transport.requests == []


def test_add_package_maps_single_pkg_version(tmp_path, capsys) -> None:
    transport = ScriptedTransport([_json_response(200, {"ret": {"code": 0}, "pkgVersion": ["pkg-9"]})])
    code, response, _stderr, _transport = _run(
        tmp_path,
        capsys,
        ["test", "add-package"],
        transport,
        _request("TEST_PACKAGE_ADD", {"versionId": "ver-1", "fileName": "qianshou.app", "objectId": "obj-1"}),
    )
    assert code == ExitCode.SUCCESS
    assert response["externalIds"]["pkgId"] == "pkg-9"
    sent = json.loads(transport.requests[0].content)
    assert sent == {"distributeMode": 2, "file": {"fileName": "qianshou.app", "objectId": "obj-1"}}
    assert "appId=123456789" in str(transport.requests[0].url)


def test_add_package_rejects_binary_field(tmp_path, capsys) -> None:
    transport = ScriptedTransport([])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "add-package"],
        transport,
        _request("TEST_PACKAGE_ADD", {"versionId": "ver-1", "fileName": "qianshou.app", "objectId": "obj-1", "content": "abc"}),
    )
    assert code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert transport.requests == []
    assert response["error"]["code"] == "TEST_PACKAGE_FIELDS"


def test_update_version_binds_group_info(tmp_path, capsys) -> None:
    transport = ScriptedTransport([_json_response(200, {"ret": {"code": 0}})])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "update-version"],
        transport,
        _request(
            "TEST_VERSION_UPDATE",
            {
                "versionId": "ver-1",
                "pkgId": "pkg-9",
                "groupId": "group-1",
                "startTime": 1_700_000_000_000,
                "endTime": 1_700_086_400_000,
            },
        ),
    )
    assert code == ExitCode.SUCCESS
    sent = json.loads(transport.requests[0].content)
    assert sent["openTestInfo"]["testTaskInfo"] == {
        "groupInfos": [{"groupId": "group-1", "bind": 1}],
        "needShareLink": 1,
        "displayArea": "1",
        "needNotify": 0,
    }
    assert response["remoteState"]["displayArea"] == "1"


def test_compile_status_success_keeps_processing_state(tmp_path, capsys) -> None:
    transport = ScriptedTransport(
        [_json_response(200, {"ret": {"code": 0}, "pkgStateList": [{"pkgId": "pkg-9", "successStatus": 1}]})]
    )
    code, response, _stderr, _transport = _run(
        tmp_path,
        capsys,
        ["package", "compile-status"],
        transport,
        _request("PACKAGE_COMPILE_STATUS", {"pkgId": "pkg-9"}),
    )
    assert code == ExitCode.SUCCESS
    assert response["remoteState"]["successStatus"] == 1


def test_get_version_keeps_share_link_out_of_stderr(tmp_path, capsys) -> None:
    link = "https://appgallery.huawei.com/share/secret-link"
    transport = ScriptedTransport([_json_response(200, {"ret": {"code": 0}, "appInfo": {"releaseState": 0, "shareLink": link}})])
    code, response, stderr, _transport = _run(
        tmp_path,
        capsys,
        ["test", "get-version"],
        transport,
        _request("TEST_VERSION_GET", {"versionId": "ver-1"}),
    )
    assert code == ExitCode.SUCCESS
    assert response["data"]["shareLink"] == link
    assert link not in stderr


def test_notify_rejects_more_than_10000_testers(tmp_path, capsys) -> None:
    transport = ScriptedTransport([])
    code, _response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "notify"],
        transport,
        _request("TEST_NOTIFY", {"groupId": "group-1", "testerIds": [str(i) for i in range(10001)]}),
    )
    assert code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert transport.requests == []


def test_stop_sends_app_id_query(tmp_path, capsys) -> None:
    transport = ScriptedTransport(
        [
            _json_response(200, {"ret": {"code": 0}, "appInfo": {"releaseState": 0}}),
            _json_response(200, {"ret": {"code": 0, "msg": "success"}}),
        ]
    )
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "stop-version"],
        transport,
        _request("APPTEST_STOP", {"versionId": "ver-1"}),
    )
    assert code == ExitCode.SUCCESS
    assert json.loads(transport.requests[1].content) == {"versionId": "ver-1"}
    assert "appId=123456789" in str(transport.requests[1].url)
    assert response["remoteState"]["stopped"] is True


def test_stop_skips_post_when_already_stopped(tmp_path, capsys) -> None:
    transport = ScriptedTransport([_json_response(200, {"ret": {"code": 0}, "appInfo": {"releaseState": 10}})])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "stop-version"],
        transport,
        _request("APPTEST_STOP", {"versionId": "ver-1"}),
    )
    assert code == ExitCode.SUCCESS
    assert response["remoteState"]["alreadySatisfied"] is True
    assert len(transport.requests) == 1


def test_delete_treats_missing_version_as_success(tmp_path, capsys) -> None:
    transport = ScriptedTransport([httpx.Response(404, json={"ret": {"code": 1}})])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["test", "delete-version"],
        transport,
        _request("APPTEST_DELETE", {"versionId": "ver-1"}),
    )
    assert code == ExitCode.SUCCESS
    assert len(transport.requests) == 1
    assert response["operation"] == "TEST_VERSION_DELETE"


def test_update_package_sends_phased_query(tmp_path, capsys) -> None:
    transport = ScriptedTransport([_json_response(200, {"ret": {"code": 0}, "packageId": "pkg-formal"})])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["publish", "update-package"],
        transport,
        _request(
            "PUBLISH_PACKAGE_UPDATE",
            {"fileName": "app.app", "objectId": "CN/object.app", "releasePhase": 3},
        ),
    )
    assert code == ExitCode.SUCCESS
    sent = transport.requests[0]
    assert json.loads(sent.content) == {"fileName": "app.app", "objectId": "CN/object.app"}
    assert "appId=123456789" in str(sent.url)
    assert "releaseType=1" in str(sent.url)
    assert "releasePhase=3" in str(sent.url)
    assert response["externalIds"]["packageId"] == "pkg-formal"


def test_get_package_info_uses_v2_query(tmp_path, capsys) -> None:
    transport = ScriptedTransport(
        [
            _json_response(
                200,
                {
                    "ret": {"code": 0},
                    "packageInfo": {
                        "packageId": "pkg-formal",
                        "fileName": "app.app",
                        "versionCode": 103,
                        "versionName": "3.0.18",
                    },
                },
            )
        ]
    )
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["publish", "get-package-info"],
        transport,
        _request("PUBLISH_PACKAGE_INFO", {"packageId": "pkg-formal"}),
    )
    assert code == ExitCode.SUCCESS
    sent = transport.requests[0]
    assert sent.method == "GET"
    assert sent.url.path == "/api/publish/v2/app-package-info"
    assert dict(sent.url.params) == {"packageId": "pkg-formal"}
    assert sent.headers["appId"] == APP_ID
    assert response["data"]["packageInfo"]["versionCode"] == 103


def test_submit_sends_app_id_query(tmp_path, capsys) -> None:
    transport = ScriptedTransport([_json_response(200, {"ret": {"code": 0, "msg": "success"}})])
    code, _response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["publish", "submit"],
        transport,
        _request("PUBLISH_SUBMIT", {"releaseMode": "PHASED", "schedule": "IMMEDIATE", "phasedReleaseDescription": "7天分阶段"}),
    )
    assert code == ExitCode.SUCCESS
    assert "appId=123456789" in str(transport.requests[0].url)
    assert json.loads(transport.requests[0].content)["releasePhase"] == 3


def test_submit_dry_run_does_not_send(tmp_path, capsys) -> None:
    when = (datetime.now(timezone.utc) + timedelta(days=1)).isoformat()
    transport = ScriptedTransport([])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["publish", "submit", "--dry-run"],
        transport,
        _request("PUBLISH_SUBMIT", {"releaseMode": "PHASED", "schedule": "SCHEDULED", "phasedReleaseDescription": "7天", "releaseTime": when}),
    )
    assert code == ExitCode.SUCCESS
    assert response["remoteState"]["dryRun"] is True
    assert response["data"]["request"]["releasePhase"] == 3
    assert transport.requests == []


def test_submit_full_rejects_phased_description(tmp_path, capsys) -> None:
    transport = ScriptedTransport([])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["publish", "submit"],
        transport,
        _request("PUBLISH_SUBMIT", {"releaseMode": "FULL", "schedule": "IMMEDIATE", "phasedReleaseDescription": "nope"}),
    )
    assert code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert response["error"]["code"] == "PHASED_DESCRIPTION_FORBIDDEN"
    assert transport.requests == []


def test_promote_full_sends_only_publish_version(tmp_path, capsys) -> None:
    transport = ScriptedTransport([_json_response(200, {"ret": {"code": 0}})])
    code, response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["publish", "promote-full"],
        transport,
        _request("PUBLISH_PROMOTE_FULL", {"publishVersionId": "pub-ver-1", "confirmed": True}),
    )
    assert code == ExitCode.SUCCESS
    sent = json.loads(transport.requests[0].content)
    assert sent == {"versionId": "pub-ver-1", "releasePhase": 4}
    assert response["externalIds"]["publishVersionId"] == "pub-ver-1"


def test_promote_full_refuses_without_confirmation(tmp_path, capsys) -> None:
    transport = ScriptedTransport([])
    code, _response, _stderr, transport = _run(
        tmp_path,
        capsys,
        ["publish", "promote-full"],
        transport,
        _request("PUBLISH_PROMOTE_FULL", {"publishVersionId": "pub-ver-1", "confirmed": False}),
    )
    assert code == ExitCode.LOCAL_PRECONDITION_FAILED
    assert transport.requests == []


def test_list_versions_reports_multiple_candidates(tmp_path, capsys) -> None:
    transport = ScriptedTransport(
        [
            _json_response(
                200,
                {
                    "ret": {"code": 0},
                    "versionList": [
                        {"versionId": "a", "versionCode": 96},
                        {"versionId": "b", "versionCode": 96},
                    ],
                },
            )
        ]
    )
    code, response, _stderr, _transport = _run(
        tmp_path,
        capsys,
        ["publish", "list-versions"],
        transport,
        _request("PUBLISH_VERSION_LIST", {"versionCode": 96}),
    )
    assert code == ExitCode.SUCCESS
    assert response["remoteState"]["multipleCandidates"] is True
    assert "publishVersionId" not in response["externalIds"]


def test_list_versions_matches_package_list_formal_version(tmp_path, capsys) -> None:
    transport = ScriptedTransport(
        [
            _json_response(
                200,
                {
                    "ret": {"code": 0},
                    "versionList": [
                        {
                            "versionId": "formal-103",
                            "releaseType": 1,
                            "releaseState": 5,
                            "packageList": [{"versionCode": 103, "packageId": "pkg-103"}],
                        },
                        {
                            "versionId": "test-103",
                            "releaseType": 6,
                            "releaseState": 10,
                            "packageList": [{"versionCode": 103, "packageId": "pkg-103"}],
                        },
                        {
                            "versionId": "formal-102",
                            "releaseType": 1,
                            "releaseState": 0,
                            "packageList": [{"versionCode": 102}],
                        },
                    ],
                },
            )
        ]
    )
    code, response, _stderr, _transport = _run(
        tmp_path,
        capsys,
        ["publish", "list-versions"],
        transport,
        _request("PUBLISH_VERSION_LIST", {"versionCode": 103}),
    )
    assert code == ExitCode.SUCCESS
    assert response["remoteState"]["candidateCount"] == 1
    assert response["externalIds"]["publishVersionId"] == "formal-103"
    assert len(response["data"]["versions"]) == 3


def test_get_app_info_maps_phase_percent(tmp_path, capsys) -> None:
    transport = ScriptedTransport(
        [
            _json_response(
                200,
                {"ret": {"code": 0}, "appInfo": {"versionId": "pub-1", "releaseState": 0, "phasedReleaseInfo": {"currentPhaseDay": 1}}},
            )
        ]
    )
    code, response, _stderr, _transport = _run(
        tmp_path,
        capsys,
        ["publish", "get-app-info"],
        transport,
        _request("PUBLISH_APP_INFO", {"publishVersionId": "pub-1"}),
    )
    assert code == ExitCode.SUCCESS
    assert response["data"]["phasePercent"] == 1


def test_offline_manifest_matches_payload(tmp_path) -> None:
    source = Path(__file__).parents[1]
    manifest_path = build_offline_package(source, tmp_path / "dist")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    payload = Path(manifest["payload"])
    assert manifest["version"] == __version__
    assert hashlib.sha256(payload.read_bytes()).hexdigest() == manifest["sha256"]


def _artifact(tmp_path, payload: bytes):
    task_dir = tmp_path / "artifacts" / "PUBLISH-20261015-001"
    task_dir.mkdir(parents=True)
    path = task_dir / "qianshou.app"
    path.write_bytes(payload)
    return path


def _request(operation: str, body: dict) -> dict:
    return request_body(operationKey=f"PUBLISH-20261015-001:{operation}", request=body)

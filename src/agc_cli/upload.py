"""Upload one already-verified APP Pack to AppGallery Connect."""

from __future__ import annotations

from typing import Any
from urllib.parse import urlparse

import httpx

from agc_cli.api import AgcSession, Exchange, classify
from agc_cli.artifact import file_size_and_sha256, resolve_task_artifact
from agc_cli.config import Settings
from agc_cli.errors import CommandError, ExitCode

UPLOAD_URL_PATH = "/api/publish/v2/upload-url/for-obs"


def upload_package(settings: Settings, request: dict[str, Any], session: AgcSession) -> dict[str, Any]:
    body = request["request"]
    artifact = resolve_task_artifact(settings.artifactRoot, str(request["taskId"]), str(body["artifactPath"]))
    size, digest = file_size_and_sha256(artifact)
    if size != body["contentLength"] or digest != str(body["sha256"]).lower():
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "ARTIFACT_HASH_MISMATCH",
            "APP Pack 大小或 SHA-256 与请求不一致",
        )
    if artifact.name != body["fileName"]:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "ARTIFACT_NAME_MISMATCH",
            "APP Pack 文件名与请求不一致",
        )
    exchange = session.call(
        "GET",
        UPLOAD_URL_PATH,
        params={
            "appId": settings.appId,
            "fileName": body["fileName"],
            "contentLength": size,
            "sha256": digest,
        },
    )
    url, headers, object_id = _upload_target(exchange.body)
    _require_https_upload_url(url)
    put = _put_stream(session, url, headers, artifact, settings)
    error = classify("PUT", put)
    if error is not None:
        raise error
    if put.status_code != 200:
        raise CommandError(ExitCode.NON_RETRYABLE, "UPLOAD_NOT_ACCEPTED", "上传地址没有返回 HTTP 200")
    return {
        "externalIds": {"objectId": object_id, "fileName": str(body["fileName"])},
        "remoteState": {"uploaded": True},
        "data": {"sha256": digest, "contentLength": size},
        "requestId": put.request_id or exchange.request_id,
    }


def _upload_target(body: dict[str, Any]) -> tuple[str, dict[str, str], str]:
    url_info = body.get("urlInfo") if isinstance(body.get("urlInfo"), dict) else body
    url = url_info.get("url")
    object_id = url_info.get("objectId") or body.get("objectId")
    raw_headers = url_info.get("headers") if isinstance(url_info.get("headers"), dict) else {}
    headers = {str(key): str(value) for key, value in raw_headers.items()}
    if not isinstance(url, str) or not url or not isinstance(object_id, str) or not object_id:
        raise CommandError(ExitCode.NON_RETRYABLE, "UPLOAD_TARGET_INVALID", "上传地址响应缺少 url 或 objectId")
    return url, headers, object_id


def _require_https_upload_url(url: str) -> None:
    parsed = urlparse(url)
    if parsed.scheme != "https" or not parsed.hostname:
        raise CommandError(ExitCode.NON_RETRYABLE, "UPLOAD_URL_NOT_HTTPS", "上传地址必须是带主机名的 HTTPS URL")


def _put_stream(session: AgcSession, url: str, headers: dict[str, str], artifact: Any, settings: Settings) -> Exchange:
    timeout = httpx.Timeout(
        connect=settings.connectTimeoutSeconds,
        read=settings.uploadTimeoutSeconds,
        write=settings.uploadTimeoutSeconds,
        pool=settings.connectTimeoutSeconds,
    )
    with artifact.open("rb") as handle:
        request = httpx.Request("PUT", url, headers=headers, content=handle)
        try:
            response = session._transport.send(request, timeout)
        except httpx.ConnectTimeout:
            return Exchange(method="PUT", error_kind="connect", sent=False)
        except httpx.ConnectError as exc:
            kind = "tls" if "certificate" in str(exc).lower() else "connect"
            return Exchange(method="PUT", error_kind=kind, sent=False)
        except httpx.TimeoutException:
            return Exchange(method="PUT", error_kind="timeout", sent=True)
        except httpx.TransportError:
            return Exchange(method="PUT", error_kind="connect", sent=False)
    return Exchange(method="PUT", status_code=response.status_code, sent=True, request_id=response.headers.get("x-request-id"))

"""One-shot AppGallery Connect HTTP calls. This client never retries."""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any, Protocol
from urllib.parse import urlencode, urljoin

import httpx

from agc_cli.auth import build_jwt
from agc_cli.config import Settings
from agc_cli.doctor import _load_account
from agc_cli.errors import CommandError, ExitCode
from agc_cli.ret_codes import ret_failure

_MUTATING = {"POST", "PUT", "DELETE"}


@dataclass
class Exchange:
    method: str
    status_code: int | None = None
    body: dict[str, Any] = field(default_factory=dict)
    sent: bool = False
    error_kind: str | None = None
    request_id: str | None = None
    retry_after: str | None = None


class Transport(Protocol):
    def send(self, request: httpx.Request, timeout: httpx.Timeout) -> httpx.Response:
        """Perform exactly one HTTP round trip."""


class HttpxTransport:
    def send(self, request: httpx.Request, timeout: httpx.Timeout) -> httpx.Response:
        with httpx.Client(timeout=timeout, verify=True, trust_env=True, follow_redirects=False) as client:
            return client.send(request)


class AgcSession:
    def __init__(self, settings: Settings, transport: Transport | None = None) -> None:
        account, check = _load_account(settings)
        if account is None:
            raise CommandError(check.exit_code, "PRIVATE_KEY", check.message)
        self.settings = settings
        self._account = account
        self._transport = transport or HttpxTransport()

    def call(
        self,
        method: str,
        path: str,
        *,
        params: dict[str, Any] | None = None,
        json_body: dict[str, Any] | None = None,
        content: bytes | Any = None,
        headers: dict[str, str] | None = None,
        base_url: str | None = None,
        timeout_seconds: int | None = None,
        expect_json: bool = True,
    ) -> Exchange:
        token = build_jwt(self._account, issued_at=int(time.time()))
        url = _url(base_url or self.settings.apiHost, path, params)
        request_headers = {"Authorization": f"Bearer {token}", "appId": self.settings.appId}
        if headers:
            request_headers.update(headers)
        if json_body is not None:
            request = httpx.Request(method, url, headers=request_headers, json=json_body)
        else:
            request = httpx.Request(method, url, headers=request_headers, content=content)
        timeout_value = timeout_seconds or self.settings.queryTimeoutSeconds
        timeout = httpx.Timeout(connect=self.settings.connectTimeoutSeconds, read=timeout_value, write=timeout_value, pool=self.settings.connectTimeoutSeconds)
        exchange = _send(self._transport, request, timeout, method)
        if expect_json:
            _raise_if_failed(method, exchange)
        return exchange


def classify(method: str, exchange: Exchange) -> CommandError | None:
    if exchange.error_kind == "tls":
        return CommandError(ExitCode.NON_RETRYABLE, "TLS_VERIFY_FAILED", "TLS 证书校验失败")
    if exchange.error_kind == "connect":
        return CommandError(ExitCode.SAFE_RETRY, "CONNECTION_FAILED", "请求发送前连接失败")
    if exchange.error_kind == "timeout":
        if method.upper() in _MUTATING and exchange.sent:
            return CommandError(ExitCode.RECONCILE_FIRST, "HTTP_TIMEOUT_AFTER_SEND", "请求结果无法确认")
        return CommandError(ExitCode.SAFE_RETRY, "HTTP_TIMEOUT", "查询超时")
    status = exchange.status_code
    if status is None:
        return CommandError(ExitCode.TOOL_INTERNAL_ERROR, "HTTP_NO_STATUS", "没有收到 HTTP 状态")
    if status == 401:
        return CommandError(ExitCode.SAFE_RETRY, "JWT_REJECTED", "鉴权被拒绝，可重新生成 JWT 后重试一次")
    if status in {400, 403}:
        return CommandError(ExitCode.NON_RETRYABLE, f"HTTP_{status}", f"华为接口返回 HTTP {status}")
    if status == 404:
        return CommandError(ExitCode.NON_RETRYABLE, "HTTP_404", "华为资源不存在")
    if status == 429:
        return CommandError(ExitCode.SAFE_RETRY, "HTTP_429", "华为接口限流")
    if status >= 500:
        if method.upper() in _MUTATING:
            return CommandError(ExitCode.RECONCILE_FIRST, "HTTP_5XX", "变更请求结果无法确认")
        return CommandError(ExitCode.SAFE_RETRY, "HTTP_5XX", "华为接口暂时失败")
    if status >= 400:
        return CommandError(ExitCode.NON_RETRYABLE, "HTTP_UNEXPECTED", f"华为接口返回 HTTP {status}")
    ret = exchange.body.get("ret")
    if isinstance(ret, dict) and ret.get("code") not in (None, 0):
        message, detail = ret_failure(ret.get("code"), ret.get("msg"))
        return CommandError(ExitCode.NON_RETRYABLE, "AGC_RET_ERROR", message, detail)
    return None


def _send(transport: Transport, request: httpx.Request, timeout: httpx.Timeout, method: str) -> Exchange:
    try:
        response = transport.send(request, timeout)
    except httpx.ConnectTimeout:
        return Exchange(method=method, error_kind="connect", sent=False)
    except httpx.ConnectError as exc:
        kind = "tls" if "certificate" in str(exc).lower() or "ssl" in type(exc).__name__.lower() else "connect"
        return Exchange(method=method, error_kind=kind, sent=False)
    except httpx.TimeoutException:
        return Exchange(method=method, error_kind="timeout", sent=True)
    except httpx.TransportError:
        return Exchange(method=method, error_kind="connect", sent=False)
    return Exchange(
        method=method,
        status_code=response.status_code,
        body=_json_object(response),
        sent=True,
        request_id=response.headers.get("x-request-id") or response.headers.get("request-id"),
        retry_after=response.headers.get("retry-after"),
    )


def _raise_if_failed(method: str, exchange: Exchange) -> None:
    error = classify(method, exchange)
    if error is not None:
        raise error


def _json_object(response: httpx.Response) -> dict[str, Any]:
    try:
        payload = response.json()
    except ValueError:
        return {}
    if isinstance(payload, dict):
        return payload
    return {}


def _url(base: str, path: str, params: dict[str, Any] | None) -> str:
    url = urljoin(base.rstrip("/") + "/", path.lstrip("/"))
    if not params:
        return url
    query = urlencode({key: value for key, value in params.items() if value is not None})
    return f"{url}?{query}"

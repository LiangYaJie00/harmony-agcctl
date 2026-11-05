"""HTTPS client for the read-only AppGallery Connect calls used by doctor."""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from email.utils import parsedate_to_datetime
from typing import Any, Protocol

import httpx

from agc_cli.config import Settings

APP_INFO_PATH = "/api/publish/v3/app-info"
APP_ID_LIST_PATH = "/api/publish/v2/appid-list"
HARMONY_PACKAGE_TYPE = "7"


@dataclass(frozen=True)
class AppInfoProbe:
    connected: bool
    tls_verified: bool
    status_code: int | None
    date: datetime | None
    ret_code: int | None
    error_code: str | None
    message: str


@dataclass(frozen=True)
class PackageLookup:
    connected: bool
    tls_verified: bool
    status_code: int | None
    ret_code: int | None
    error_code: str | None
    app_id: str | None
    message: str


class AppInfoClient(Protocol):
    def get_app_info(self, *, app_id: str, token: str) -> AppInfoProbe:
        """Query application info. The token must not be included in the result."""

    def lookup_package(self, *, package_name: str, token: str) -> PackageLookup:
        """Resolve one HarmonyOS package name to its App ID."""


class HttpxAppInfoClient:
    def __init__(self, settings: Settings) -> None:
        self._settings = settings

    def get_app_info(self, *, app_id: str, token: str) -> AppInfoProbe:
        outcome = self._get(APP_INFO_PATH, params={"appId": app_id}, token=token, app_id=app_id)
        if isinstance(outcome, AppInfoProbe):
            return outcome
        response, body = outcome
        return AppInfoProbe(
            connected=True,
            tls_verified=True,
            status_code=response.status_code,
            date=_header_date(response),
            ret_code=_ret_code(body),
            error_code=_http_error_code(response.status_code),
            message=f"HTTP {response.status_code}",
        )

    def lookup_package(self, *, package_name: str, token: str) -> PackageLookup:
        outcome = self._get(
            APP_ID_LIST_PATH,
            params={"packageName": package_name, "packageTypes": HARMONY_PACKAGE_TYPE},
            token=token,
            app_id=self._settings.appId,
        )
        if isinstance(outcome, AppInfoProbe):
            return PackageLookup(
                connected=outcome.connected,
                tls_verified=outcome.tls_verified,
                status_code=outcome.status_code,
                ret_code=outcome.ret_code,
                error_code=outcome.error_code,
                app_id=None,
                message=outcome.message,
            )
        response, body = outcome
        return PackageLookup(
            connected=True,
            tls_verified=True,
            status_code=response.status_code,
            ret_code=_ret_code(body),
            error_code=_http_error_code(response.status_code),
            app_id=_matched_app_id(body),
            message=f"HTTP {response.status_code}",
        )

    def _get(
        self,
        path: str,
        *,
        params: dict[str, str],
        token: str,
        app_id: str,
    ) -> tuple[httpx.Response, dict[str, Any]] | AppInfoProbe:
        timeout = httpx.Timeout(
            connect=self._settings.connectTimeoutSeconds,
            read=self._settings.queryTimeoutSeconds,
            write=self._settings.queryTimeoutSeconds,
            pool=self._settings.connectTimeoutSeconds,
        )
        try:
            with httpx.Client(timeout=timeout, verify=True, trust_env=True, follow_redirects=False) as client:
                response = client.get(
                    f"{self._settings.apiHost}{path}",
                    params=params,
                    headers={"Authorization": f"Bearer {token}", "appId": app_id},
                )
        except httpx.TransportError as exc:
            tls_failed = isinstance(exc, httpx.ConnectError) and "certificate" in str(exc).lower()
            if tls_failed or "ssl" in type(exc).__name__.lower():
                return AppInfoProbe(
                    connected=False,
                    tls_verified=False,
                    status_code=None,
                    date=None,
                    ret_code=None,
                    error_code="TLS_VERIFY_FAILED",
                    message=type(exc).__name__,
                )
            return AppInfoProbe(
                connected=False,
                tls_verified=True,
                status_code=None,
                date=None,
                ret_code=None,
                error_code="API_UNREACHABLE",
                message=type(exc).__name__,
            )
        return response, _json_object(response)


def _json_object(response: httpx.Response) -> dict[str, Any]:
    try:
        payload = response.json()
    except ValueError:
        return {}
    if isinstance(payload, dict):
        return payload
    return {}


def _header_date(response: httpx.Response) -> datetime | None:
    raw = response.headers.get("date")
    if not raw:
        return None
    try:
        parsed = parsedate_to_datetime(raw)
    except (TypeError, ValueError, IndexError, OverflowError):
        return None
    return parsed


def _matched_app_id(body: dict[str, Any]) -> str | None:
    """Return the single App ID from an appid-list response.

    The query already selects one package name. Response `key` is the app display
    name, and `value` is the App ID.
    """

    appids = body.get("appids")
    if not isinstance(appids, list):
        return None
    found: list[str] = []
    for item in appids:
        if not isinstance(item, dict):
            continue
        value = item.get("value")
        if isinstance(value, str) and value and value not in found:
            found.append(value)
    if len(found) != 1:
        return None
    return found[0]


def _ret_code(body: dict[str, Any]) -> int | None:
    ret = body.get("ret")
    if isinstance(ret, dict) and isinstance(ret.get("code"), int):
        return ret["code"]
    return None


def _http_error_code(status_code: int) -> str | None:
    if 200 <= status_code < 300:
        return None
    if status_code in {401, 403}:
        return "PERMISSION_DENIED"
    if status_code == 429:
        return "HTTP_429"
    if status_code >= 500:
        return "HTTP_5XX"
    return "HTTP_UNEXPECTED"

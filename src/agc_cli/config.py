"""Non-secret runtime configuration. The private key stays outside the repo."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml
from pydantic import BaseModel, ConfigDict, Field, field_validator


class Settings(BaseModel):
    model_config = ConfigDict(extra="ignore")

    apiHost: str
    appId: str = Field(min_length=1)
    bundleName: str = Field(min_length=1)
    defaultLanguage: str = "zh-CN"
    testReleaseType: int = 6
    testType: int = 3
    connectTimeoutSeconds: int = Field(default=10, ge=1)
    queryTimeoutSeconds: int = Field(default=30, ge=1)
    uploadTimeoutSeconds: int = Field(default=1200, ge=1)
    privateKeyFile: Path
    artifactRoot: Path
    logDir: Path
    runDir: Path
    releaseUser: str | None = None
    manifestFile: Path | None = None
    requireManifest: bool = False
    maxClockSkewSeconds: int = Field(default=60, ge=0)
    notifyPath: str = "/api/publish/v2/test/user/action/notify"

    @field_validator("apiHost")
    @classmethod
    def api_host_must_be_https(cls, value: str) -> str:
        if not value.startswith("https://") or value.startswith("https:///"):
            raise ValueError("apiHost must be an https URL")
        return value.rstrip("/")

    @field_validator("notifyPath")
    @classmethod
    def api_path_must_be_relative(cls, value: str) -> str:
        if not value.startswith("/") or value.startswith("//"):
            raise ValueError("API path must start with a single slash")
        return value

def load_settings(path: Path) -> Settings:
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise ValueError("无法读取配置文件") from exc
    try:
        raw = yaml.safe_load(text)
    except yaml.YAMLError as exc:
        raise ValueError("配置不是合法 YAML") from exc
    if not isinstance(raw, dict):
        raise ValueError("配置必须是 YAML 映射")
    try:
        return Settings.model_validate(raw)
    except Exception as exc:
        raise ValueError("配置字段无效") from exc


def dump_settings(settings: Settings) -> dict[str, Any]:
    payload = settings.model_dump(mode="json")
    for key in ("privateKeyFile", "artifactRoot", "logDir", "runDir", "manifestFile"):
        if payload.get(key) is not None:
            payload[key] = str(payload[key])
    return payload

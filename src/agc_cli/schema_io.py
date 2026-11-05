"""Load and validate the agcctl JSON request and response contracts."""

from __future__ import annotations

import json
from functools import lru_cache
from importlib.resources import files
from typing import Any

from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError


class SchemaValidationError(ValueError):
    def __init__(self, message: str) -> None:
        super().__init__(message)
        self.message = message


@lru_cache(maxsize=2)
def _validator(name: str) -> Draft202012Validator:
    text = files("agc_cli.schemas").joinpath(name).read_text(encoding="utf-8")
    return Draft202012Validator(json.loads(text))


def validate_request(payload: dict[str, Any]) -> None:
    _validate("request.schema.json", payload)


def validate_response(payload: dict[str, Any]) -> None:
    _validate("response.schema.json", payload)


def _validate(schema_name: str, payload: dict[str, Any]) -> None:
    errors = sorted(_validator(schema_name).iter_errors(payload), key=lambda item: list(item.path))
    if not errors:
        return
    raise SchemaValidationError(_format_error(errors[0]))


def _format_error(error: ValidationError) -> str:
    location = ".".join(str(part) for part in error.path) or "<root>"
    return f"{location}: {error.message}"

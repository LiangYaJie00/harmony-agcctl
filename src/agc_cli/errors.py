"""Exit codes and error categories shared by every agcctl command."""

from __future__ import annotations

from dataclasses import dataclass
from enum import IntEnum


class ExitCode(IntEnum):
    SUCCESS = 0
    SAFE_RETRY = 10
    NON_RETRYABLE = 20
    RECONCILE_FIRST = 30
    LOCAL_PRECONDITION_FAILED = 40
    TOOL_INTERNAL_ERROR = 50


EXIT_TO_RETRY_CLASS = {
    ExitCode.SUCCESS: "NONE",
    ExitCode.SAFE_RETRY: "SAFE_RETRY",
    ExitCode.NON_RETRYABLE: "NON_RETRYABLE",
    ExitCode.RECONCILE_FIRST: "RECONCILE_FIRST",
    ExitCode.LOCAL_PRECONDITION_FAILED: "LOCAL_PRECONDITION_FAILED",
    ExitCode.TOOL_INTERNAL_ERROR: "TOOL_INTERNAL_ERROR",
}

# Higher wins when several checks fail in one command.
_SEVERITY = {
    ExitCode.SUCCESS: 0,
    ExitCode.SAFE_RETRY: 1,
    ExitCode.RECONCILE_FIRST: 2,
    ExitCode.NON_RETRYABLE: 3,
    ExitCode.LOCAL_PRECONDITION_FAILED: 4,
    ExitCode.TOOL_INTERNAL_ERROR: 5,
}


@dataclass(frozen=True)
class CommandError(Exception):
    """A classified failure that the CLI can turn into one JSON response."""

    exit_code: ExitCode
    code: str
    message: str
    detail: str | None = None

    @property
    def category(self) -> str:
        return EXIT_TO_RETRY_CLASS[self.exit_code]

    @property
    def retry_class(self) -> str:
        return self.category


def worst_exit(codes: list[ExitCode]) -> ExitCode:
    if not codes:
        return ExitCode.SUCCESS
    return max(codes, key=lambda code: _SEVERITY[code])

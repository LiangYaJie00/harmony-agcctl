"""Guards for the one read-only APP Pack bound to a PUBLISH task."""

from __future__ import annotations

import hashlib
from pathlib import Path

from agc_cli.errors import CommandError, ExitCode


def resolve_task_artifact(artifact_root: Path, task_id: str, artifact_path: str) -> Path:
    supplied = Path(artifact_path)
    if not supplied.is_absolute() or ".." in supplied.parts:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "ARTIFACT_PATH_REJECTED",
            "APP Pack 路径必须是任务产物目录内的绝对路径",
        )
    root = artifact_root.resolve()
    task_root = (root / task_id).resolve()
    try:
        task_root.relative_to(root)
    except ValueError as exc:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "ARTIFACT_PATH_REJECTED",
            "任务目录不在产物根目录内",
        ) from exc
    if supplied.is_symlink():
        real = supplied.resolve()
        try:
            real.relative_to(task_root)
        except ValueError as exc:
            raise CommandError(
                ExitCode.LOCAL_PRECONDITION_FAILED,
                "ARTIFACT_SYMLINK_REJECTED",
                "APP Pack 软链接指向任务目录之外",
            ) from exc
    else:
        real = supplied.resolve()
    try:
        real.relative_to(task_root)
    except ValueError as exc:
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "ARTIFACT_PATH_REJECTED",
            "APP Pack 不在当前任务产物目录内",
        ) from exc
    if not real.is_file():
        raise CommandError(
            ExitCode.LOCAL_PRECONDITION_FAILED,
            "ARTIFACT_MISSING",
            "APP Pack 文件不存在",
        )
    return real


def file_size_and_sha256(path: Path) -> tuple[int, str]:
    digest = hashlib.sha256()
    size = 0
    with path.open("rb") as handle:
        while True:
            chunk = handle.read(1024 * 1024)
            if not chunk:
                break
            size += len(chunk)
            digest.update(chunk)
    return size, digest.hexdigest()

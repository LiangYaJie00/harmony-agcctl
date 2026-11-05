"""Build a versioned offline payload and MANIFEST.json.

The production machine installs from this payload. It does not download
Python dependencies while a publish task is running.
"""

from __future__ import annotations

import hashlib
import json
import tarfile
from pathlib import Path

from agc_cli.version import __version__

_INCLUDED = (
    "pyproject.toml",
    "requirements.lock",
    "README.md",
    "config.example.yaml",
    "src",
    "docs",
)


def build_offline_package(source: Path, dest: Path) -> Path:
    dest.mkdir(parents=True, exist_ok=True)
    archive = dest / f"agcctl-{__version__}.tar.gz"
    with tarfile.open(archive, "w:gz") as package:
        for relative in _INCLUDED:
            path = source / relative
            if path.exists():
                package.add(path, arcname=f"agcctl-{__version__}/{relative}")
    digest = hashlib.sha256(archive.read_bytes()).hexdigest()
    manifest = {
        "version": __version__,
        "payload": str(archive.resolve()),
        "sha256": digest,
    }
    manifest_path = dest / "MANIFEST.json"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return manifest_path


def main() -> None:
    source = Path(__file__).resolve().parents[1]
    manifest = build_offline_package(source, source / "dist")
    print(manifest)


if __name__ == "__main__":
    main()

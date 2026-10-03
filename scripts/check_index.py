#!/usr/bin/env python3
"""Validate index.json against the repository contract (index.schema.md).

Fail-closed, offline-only: the file is parsed and every mandatory field is
checked. No network access, no release-asset download, no hash re-computation
against remote bytes -- this gate only proves the catalog is well-formed and
complete before a merge.

EU AI Act Art. 50 - created with AI assistance.

Exit codes: 0 = valid, 1 = validation error(s), 2 = file missing / unreadable.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

# sha256 must be exactly 64 hex characters (see index.schema.md, asset level).
_SHA256_RE = re.compile(r"^[0-9a-fA-F]{64}$")

# Repository-level mandatory fields (format_version is checked separately).
_REPO_REQUIRED = ("id", "name", "maintainer")
# Plugin-level mandatory fields per the schema contract.
_PLUGIN_REQUIRED = (
    "name",
    "title",
    "description",
    "author",
    "license",
    "platforms",
    "latest_version",
)


def _require_str(obj: dict, key: str, where: str, errors: list[str]) -> None:
    """Require a present, non-empty string field."""
    if key not in obj:
        errors.append(f"{where}: missing required field '{key}'")
    elif not isinstance(obj[key], str) or not obj[key].strip():
        errors.append(f"{where}: field '{key}' must be a non-empty string")


def validate(data: object) -> list[str]:
    """Return a list of validation errors (empty means valid)."""
    errors: list[str] = []

    if not isinstance(data, dict):
        return ["root: index.json must be a JSON object"]

    # format_version must be exactly 1 (ErrUnsupportedFormat otherwise).
    if "format_version" not in data:
        errors.append("root: missing required field 'format_version'")
    elif data["format_version"] != 1:
        errors.append(
            f"root: format_version must be 1, got {data['format_version']!r}"
        )

    for key in _REPO_REQUIRED:
        _require_str(data, key, "root", errors)

    plugins = data.get("plugins")
    if not isinstance(plugins, list):
        errors.append("root: 'plugins' must be an array")
        return errors

    for i, plugin in enumerate(plugins):
        where = f"plugins[{i}]"
        if not isinstance(plugin, dict):
            errors.append(f"{where}: must be a JSON object")
            continue
        for key in _PLUGIN_REQUIRED:
            if key == "platforms":
                continue
            _require_str(plugin, key, where, errors)

        platforms = plugin.get("platforms")
        if not isinstance(platforms, dict) or not platforms:
            errors.append(f"{where}: 'platforms' must be a non-empty object")
            continue

        for pk, asset in platforms.items():
            awhere = f"{where}.platforms[{pk!r}]"
            if not isinstance(asset, dict):
                errors.append(f"{awhere}: must be a JSON object")
                continue
            _require_str(asset, "url", awhere, errors)
            sha = asset.get("sha256")
            if not isinstance(sha, str) or not _SHA256_RE.match(sha):
                errors.append(
                    f"{awhere}: 'sha256' must be 64 hex characters"
                )

    return errors


def main(argv: list[str]) -> int:
    path = Path(argv[1]) if len(argv) > 1 else Path("index.json")
    if not path.is_file():
        print(f"index-check: FAIL - file not found: {path}", file=sys.stderr)
        return 2

    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        print(f"index-check: FAIL - cannot parse {path}: {exc}", file=sys.stderr)
        return 1

    errors = validate(data)
    if errors:
        print(f"index-check: FAIL - {len(errors)} error(s) in {path}:", file=sys.stderr)
        for err in errors:
            print(f"  - {err}", file=sys.stderr)
        return 1

    n = len(data.get("plugins", [])) if isinstance(data, dict) else 0
    print(f"index-check: OK - {path} valid ({n} plugin(s))")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

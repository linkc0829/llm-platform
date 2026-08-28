"""Add exact file:line evidence to legacy Web chains.json files.

This only fills source locations; it does not decide call-chain semantics or
change existing reviewed chains.  A missing method is a hard error so an
unverified location cannot silently enter the KB.
"""
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path


METHOD = re.compile(r"\b([A-Za-z_$][\w$]*)\s*(?:<[^>{}]*>)?\s*\(")
CLASS = re.compile(r"\bexport\s+class\s+([A-Za-z_$][\w$]*)")


def line_at(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def method_source(path: Path, method: str) -> str | None:
    if not path.is_file():
        return None
    text = path.read_text(encoding="utf-8", errors="ignore")
    name = method.rstrip("()")
    pattern = re.compile(rf"^\s*(?:(?:public|private|protected|async)\s+)*{re.escape(name)}\s*(?:<[^>{{}}]*>)?\s*\(", re.M)
    match = pattern.search(text)
    return f"{path}:{line_at(text, match.start())}" if match else None


def find_api_source(source_root: Path, api_method: str) -> str | None:
    cls, _, method = api_method.rstrip("()").rpartition(".")
    for path in sorted(source_root.rglob("*.ts")):
        text = path.read_text(encoding="utf-8", errors="ignore")
        class_match = CLASS.search(text)
        if not class_match or class_match.group(1) != cls:
            continue
        source = method_source(path, method)
        if source:
            return source
    return None


def enrich(module_dir: Path, source_root: Path) -> int:
    path = module_dir / "chains.json"
    data = json.loads(path.read_text(encoding="utf-8"))
    if data.get("schema_version", 1) >= 2:
        raise ValueError(f"{path}: schema v2 already carries source locations")
    missing = []
    changed = 0
    for row in data.get("rows", []):
        command_source = method_source(Path(row.get("file", "")), row.get("command", ""))
        if not command_source:
            missing.append(f"{row.get('host')}.{row.get('command')} ({row.get('file')})")
            continue
        if row.get("source") != command_source:
            row["source"] = command_source
            changed += 1
        api_method = str(row.get("api_method") or "").strip()
        if api_method:
            api_source = find_api_source(source_root, api_method)
            if not api_source:
                missing.append(f"{api_method} (API source)")
                continue
            row["api_source"] = api_source
    if missing:
        raise ValueError(f"{path}: unresolved source locations: {'; '.join(missing)}")
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return changed


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("module_root", help="directory containing module subdirectories")
    parser.add_argument("source_root")
    args = parser.parse_args()
    total = 0
    modules = 0
    for chains in sorted(Path(args.module_root).rglob("chains.json")):
        total += enrich(chains.parent, Path(args.source_root))
        modules += 1
    print(f"enriched {total} rows in {modules} modules")


if __name__ == "__main__":
    main()

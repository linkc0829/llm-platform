"""Resolve the engineering-eval scope from kb_sources.json."""
from __future__ import annotations

import json
import hashlib
from pathlib import Path


def default_repo() -> Path:
    # .../<repo>/.claude/skills/ui-kb-eng-validate/templates/eng_scope.py
    return Path(__file__).resolve().parents[4]


def load_manifest(path: Path) -> tuple[str, list[dict]]:
    if not path.is_file():
        raise ValueError(f"manifest not found: {path}")
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid manifest {path}: {exc}") from exc
    team = str(data.get("team") or "").strip()
    rows = data.get("sources")
    if not team or not isinstance(rows, list) or not rows:
        raise ValueError(f"{path}: team and non-empty sources are required")
    base = path.parent.resolve()
    sources, seen = [], set()
    for row in rows:
        name = str(row.get("name") or "").strip()
        raw_root = str(row.get("root") or "").strip()
        root = Path(raw_root)
        if not root.is_absolute():
            root = (base / root).resolve()
        else:
            root = root.resolve()
        if not name or not root.is_dir():
            raise ValueError(f"{path}: source {name or '<unnamed>'} root does not exist: {root}")
        if root in seen:
            raise ValueError(f"{path}: duplicate source root: {root}")
        if not (root / "kb" / "kb_index.json").is_file():
            raise ValueError(f"{path}: source {name} has no kb/kb_index.json: {root}")
        seen.add(root)
        sources.append({"name": name, "root": root})
    return team, sources


def resolve(repo: Path | None = None, manifest: Path | None = None,
            scope: str = "merged", source_kb: Path | None = None) -> dict:
    repo = (repo or default_repo()).resolve()
    manifest = (manifest or repo / "kb_sources.json").resolve()
    if scope not in {"merged", "source"}:
        raise ValueError(f"scope must be merged or source, got {scope!r}")
    team, sources = load_manifest(manifest)
    if scope == "merged":
        docs = repo / "docs" / team
        eval_dir = repo / "eval" / team
        if not docs.is_dir():
            raise ValueError(f"merged docs not found: {docs}; run ui-kb-merge-import first")
        return {"scope": scope, "team": team, "repo": repo, "manifest": manifest,
                "sources": sources, "docs": docs, "eval": eval_dir}
    if source_kb is None:
        raise ValueError("scope=source requires --kb")
    source_kb = source_kb.resolve()
    if not source_kb.is_dir():
        raise ValueError(f"source kb not found: {source_kb}")
    return {"scope": scope, "team": team, "repo": repo, "manifest": manifest,
            "sources": sources, "docs": source_kb, "eval": source_kb}


def procedure_fingerprint(docs: Path) -> str:
    """Fingerprint the procedure inputs used to build a merged engineering eval."""
    records = []
    for path in sorted((docs / "procedures").rglob("*-procedure.md")):
        relative = path.relative_to(docs).as_posix()
        records.append(relative + "\0" + hashlib.sha256(path.read_bytes()).hexdigest())
    return hashlib.sha256("\n".join(records).encode("utf-8")).hexdigest()


def _selftest() -> None:
    import tempfile

    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        source = root / "source"
        (source / "kb").mkdir(parents=True)
        (source / "kb" / "kb_index.json").write_text('{"docs": []}', encoding="utf-8")
        (root / "kb_sources.json").write_text(json.dumps({
            "team": "Store.POS", "sources": [{"name": "demo", "root": "source"}]
        }), encoding="utf-8")
        (root / "docs" / "Store.POS").mkdir(parents=True)
        resolved = resolve(root, root / "kb_sources.json")
        assert resolved["team"] == "Store.POS" and resolved["scope"] == "merged"
        assert resolved["docs"].name == "Store.POS"
        assert len(procedure_fingerprint(resolved["docs"])) == 64
    print("eng_scope selftest ok")


if __name__ == "__main__":
    _selftest()

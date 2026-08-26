"""Build deterministic repo maps for the POS and Admin source trees.

The map is an index, not a copy of source code.  It records the source
revision, authoritative layer boundaries, representative files, and the
recipes an implementation agent needs to locate the right project quickly.
"""
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path


LAYER_RE = re.compile(r"^(\d+)\.\s*(.+)$")


def load_provenance(path: Path) -> dict:
    value = json.loads(path.read_text(encoding="utf-8"))
    required = ("source_root", "source_revision", "source_build_id", "app_version", "last_verified")
    missing = [key for key in required if not str(value.get(key) or "").strip()]
    if missing:
        raise ValueError(f"{path}: missing provenance fields: {', '.join(missing)}")
    return value


def files_under(root: Path, relative: str) -> list[str]:
    base = root / relative
    if not base.is_dir():
        return []
    return sorted(
        p.relative_to(root).as_posix()
        for p in base.rglob("*")
        if p.is_file() and not any(part in {"bin", "obj", "node_modules", ".git"} for part in p.parts)
    )


def pos_map(root: Path, provenance: dict) -> dict:
    layers = []
    for child in sorted(root.iterdir()):
        if not child.is_dir():
            continue
        match = LAYER_RE.match(child.name)
        if not match:
            continue
        number, label = match.groups()
        rel = child.relative_to(root).as_posix()
        layers.append({
            "name": child.name,
            "number": int(number),
            "label": label,
            "path": rel,
            "files": files_under(root, rel),
        })
    return {
        "schema_version": 1,
        "product": "POS",
        "source_root": provenance["source_root"],
        "source_revision": provenance["source_revision"],
        "source_build_id": provenance["source_build_id"],
        "app_version": provenance["app_version"],
        "last_verified": provenance["last_verified"],
        "architecture": {
            "direction": "lower numbered layers may reference higher numbered layers",
            "same_layer": "same numbered layer must not reference another component in that layer",
            "boundary": "higher layers stay single-purpose; highest infrastructure layer has no business logic",
        },
        "layers": layers,
        "recipes": [
            {"name": "new_ui_flow", "steps": ["1. Ui", "4. Business.Component", "5. Business.Service", "6. Service", "7. Contract"]},
            {"name": "new_http_contract", "steps": ["6. Service", "7. Contract", "1. Ui"]},
        ],
    }


def admin_map(root: Path, provenance: dict) -> dict:
    app = root / "Cloud.Pos.BaseCamp" / "ClientApp" / "src" / "app"
    groups = {
        "ui": ["ui.user", "ui.business", "ui.login", "ui.shell", "ui.layout"],
        "board_or_business": ["service.business"],
        "api": ["service/api"],
        "contract": ["contract"],
        "shared": ["component", "helper", "models", "converter", "directive", "pipe"],
    }
    mapped = []
    for name, dirs in groups.items():
        paths = []
        for rel in dirs:
            paths.extend(files_under(app, rel))
        mapped.append({"name": name, "paths": dirs, "files": sorted(set(paths))})
    return {
        "schema_version": 1,
        "product": "Admin",
        "source_root": provenance["source_root"],
        "source_revision": provenance["source_revision"],
        "source_build_id": provenance["source_build_id"],
        "app_version": provenance["app_version"],
        "last_verified": provenance["last_verified"],
        "architecture": {
            "direction": "ui components call service.business boards/services, which call service/api clients",
            "contract": "service/api is the endpoint authority; contract contains request/response types",
            "boundary": "do not infer a route from a UI getter; resolve the concrete API client method",
        },
        "root": "Cloud.Pos.BaseCamp/ClientApp/src/app",
        "groups": mapped,
        "recipes": [
            {"name": "new_settings_page", "steps": ["ui.user/<feature>", "service.business/<feature>", "service/api/<feature>", "contract/<feature>"]},
            {"name": "change_existing_endpoint", "steps": ["service/api/<feature>.api.ts", "contract/<feature>", "ui.user/<feature>"]},
        ],
    }


def markdown(data: dict) -> str:
    lines = [
        f"# {data['product']} Repo Map",
        "",
        f"- app version: `{data['app_version']}`",
        f"- source build: `{data['source_build_id']}`",
        f"- source revision: `{data['source_revision']}`",
        f"- source root: `{data['source_root']}`",
        f"- last verified: `{data['last_verified']}`",
        "",
        "## Architecture boundary",
        "",
    ]
    for key, value in data["architecture"].items():
        lines.append(f"- {key}: {value}")
    lines.extend(["", "## Source groups", ""])
    entries = data.get("layers", []) or data.get("groups", [])
    for entry in entries:
        paths = entry.get("path") or ", ".join(entry.get("paths", []))
        lines.append(f"### {entry['name']} — `{paths}`")
        lines.append(f"Files indexed: {len(entry.get('files', []))}")
        for path in entry.get("files", [])[:12]:
            lines.append(f"- `{path}`")
        if len(entry.get("files", [])) > 12:
            lines.append(f"- …and {len(entry['files']) - 12} more files (see JSON map)")
        lines.append("")
    lines.extend(["## Recipes", ""])
    for recipe in data["recipes"]:
        lines.append(f"- **{recipe['name']}**: " + " → ".join(f"`{x}`" for x in recipe["steps"]))
    return "\n".join(lines) + "\n"


def build(pos_root: Path, admin_root: Path, out: Path,
          pos_provenance: Path | None = None,
          admin_provenance: Path | None = None) -> None:
    out.mkdir(parents=True, exist_ok=True)
    specs = [
        ("pos", pos_root, pos_provenance or pos_root / "source_provenance.json", pos_map),
        ("admin", admin_root, admin_provenance or admin_root / "source_provenance.json", admin_map),
    ]
    for name, root, provenance_path, builder in specs:
        data = builder(root, load_provenance(provenance_path))
        (out / f"repo-map-{name}.json").write_text(
            json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        (out / f"repo-map-{name}.md").write_text(markdown(data), encoding="utf-8")


def selftest() -> None:
    import tempfile

    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        pos = root / "pos"
        admin = root / "admin"
        for source in (pos, admin):
            source.mkdir()
            (source / "source_provenance.json").write_text(json.dumps({
                "source_root": str(source), "source_revision": "sha256:test",
                "source_build_id": "test-1", "app_version": "1.0.0", "last_verified": "2026-08-25"
            }), encoding="utf-8")
        (pos / "1. Ui").mkdir()
        (pos / "1. Ui" / "demo.cs").write_text("class Demo {}", encoding="utf-8")
        (admin / "Cloud.Pos.BaseCamp" / "ClientApp" / "src" / "app" / "ui.user").mkdir(parents=True)
        out = root / "out"
        build(pos, admin, out)
        data = json.loads((out / "repo-map-pos.json").read_text(encoding="utf-8"))
        assert data["layers"][0]["files"] == ["1. Ui/demo.cs"]
        assert "source revision" in (out / "repo-map-admin.md").read_text(encoding="utf-8")
    print("build_repo_map selftest ok")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--pos-source", required=False, default=r"C:\Protech\source")
    parser.add_argument("--admin-source", required=False, default=r"C:\Protech\source-web")
    parser.add_argument("--out", required=False, default=r"C:\tmp\repo-map")
    parser.add_argument("--pos-provenance", default=r"C:\Protech\wpf-replay\source_provenance.json")
    parser.add_argument("--admin-provenance", default=r"C:\Protech\admin-replay\source_provenance.json")
    parser.add_argument("--selftest", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        selftest()
        return
    build(Path(args.pos_source), Path(args.admin_source), Path(args.out),
          Path(args.pos_provenance), Path(args.admin_provenance))
    print(f"repo maps written to {Path(args.out).resolve()}")


if __name__ == "__main__":
    main()

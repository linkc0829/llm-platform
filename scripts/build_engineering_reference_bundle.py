"""Build the restricted engineering-reference KB bundle from generated indexes."""
from __future__ import annotations

import argparse
import json
from pathlib import Path


def frontmatter(doc_id: str, version: str) -> str:
    return "\n".join([
        "---",
        f'id: "{doc_id}"',
        'team: "Store.POS"',
        'product: "POS"',
        'doc_type: "engineering_reference"',
        f'version: "{version}"',
        'access_level: "internal-engineering"',
        'owner: "engineering-reference"',
        'last_reviewed: "2026-08-25"',
        "---",
        "",
    ])


def render_map(data: dict) -> str:
    lines = [
        frontmatter(f"Store.POS--engineering-{data['product'].lower()}-repo-map", data["app_version"]),
        f"# {data['product']} Repo Map",
        "",
        f"這是 {data['product']} coding agent 的 repository map；只提供工程定位，不取代原始碼。",
        f"版本：`{data['app_version']}`；source build：`{data['source_build_id']}`；source revision：`{data['source_revision']}`。",
        f"source root：`{data['source_root']}`；last verified：`{data['last_verified']}`。",
        "",
        "## Architecture boundary",
    ]
    for key, value in data["architecture"].items():
        lines.append(f"- {key}: {value}")
    lines += ["", "## Source groups"]
    entries = data.get("layers", []) or data.get("groups", [])
    for entry in entries:
        path = entry.get("path") or ", ".join(entry.get("paths", []))
        lines += ["", f"### {entry['name']} — `{path}`",
                  f"這個 source group 有 {len(entry.get('files', []))} 個檔案；常用檔案如下："]
        lines.extend(f"- `{x}`" for x in entry.get("files", [])[:40])
    lines += ["", "## Recipes"]
    for recipe in data["recipes"]:
        lines.append(f"- **{recipe['name']}**：" + " → ".join(f"`{x}`" for x in recipe["steps"]))
    return "\n".join(lines) + "\n"


def render_type(value: dict, indent: int = 0) -> list[str]:
    pad = "  " * indent
    lines = [f"{pad}- type `{value['name']}`；source `{value.get('source', '')}`。"]
    for field in value.get("fields", []):
        required = "required" if field.get("required") else "optional"
        lines.append(f"{pad}  - `{field['name']}: {field['type']}`（{required}）；source `{field.get('source', '')}`。")
        for nested in field.get("refs", []):
            lines.extend(render_type(nested, indent + 2))
        if field.get("ref"):
            lines.extend(render_type(field["ref"], indent + 2))
    if value.get("enum_values"):
        lines.append(f"{pad}  - enum values：" + "、".join(f"`{x}`" for x in value["enum_values"]))
    return lines


def render_contract(data: dict) -> str:
    lines = [
        frontmatter("Store.POS--engineering-api-contracts", "POS 1.3.6 / Admin 1.3.0"),
        "# API／DTO Contract Index",
        "",
        "這份 contract index 是由 Admin／POS source 產生的工程參考；route、request、response、auth 與 error 都必須回到下列 source location 查證。",
    ]
    for name, source in data.get("sources", {}).items():
        lines.append(f"- {name} source revision：`{source.get('source_revision', '')}`；tree digest：`{source.get('source_tree_digest', '')}`。")
    for endpoint in data["endpoints"]:
        lines += ["", f"## {endpoint['surface']} — `{endpoint['symbol']}`",
                  f"這個 endpoint 是 `{endpoint['method']} {endpoint['path']}`；API method source：`{endpoint['source']}`。",
                  f"contract input hash：`{endpoint['contract_input_sha256']}`。",
                  "", "### Request／Response"]
        if endpoint.get("request_type") != "none":
            lines += [f"- request type：`{endpoint['request_type']}`"]
            lines.extend(render_type(endpoint["request"]))
        else:
            lines.append("- request type：無 request body。")
        response = endpoint["response"]
        lines.append(f"- response type：`{endpoint['response_type']}`；source `{response.get('source', 'primitive')}`。")
        if response.get("fields"):
            lines.extend(render_type(response))
        transport = endpoint["transport"]
        lines += ["", "### Auth／Error", f"- auth header：`{transport['auth_header']}`；source `{transport['auth_source']}`。",
                  f"- error envelope：`{transport['error_envelope']}`；source `{transport['error_source']}`。",
                  f"- error codes：" + "、".join(f"`{x}`" for x in transport["error_codes"]) +
                  f"；handling source `{transport['error_behavior_source']}`。"]
    return "\n".join(lines) + "\n"


def write_bundle(out: Path, pos_map: Path, admin_map: Path, contract: Path) -> None:
    kb = out / "kb" / "engineering_reference"
    kb.mkdir(parents=True, exist_ok=True)
    docs = []
    for data_path, name in ((pos_map, "repo-map-pos"), (admin_map, "repo-map-admin")):
        data = json.loads(data_path.read_text(encoding="utf-8"))
        target = kb / f"{name}-engineering_reference.md"
        target.write_text(render_map(data), encoding="utf-8")
        docs.append({"id": f"Store.POS--engineering-{data['product'].lower()}-repo-map",
                     "doc_type": "engineering_reference",
                     "path": f"engineering_reference/{target.name}",
                     "team": "Store.POS", "product": "POS", "version": data["app_version"]})
    data = json.loads(contract.read_text(encoding="utf-8"))
    target = kb / "api-contracts-engineering_reference.md"
    target.write_text(render_contract(data), encoding="utf-8")
    docs.append({"id": "Store.POS--engineering-api-contracts", "doc_type": "engineering_reference",
                 "path": f"engineering_reference/{target.name}", "team": "Store.POS",
                 "product": "POS", "version": "POS 1.3.6 / Admin 1.3.0"})
    (out / "kb" / "kb_index.json").write_text(
        json.dumps({"docs": docs}, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"engineering reference bundle written: {out} ({len(docs)} docs)")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--out", default=r"C:\Protech\engineering-reference")
    parser.add_argument("--pos-map", default=r"C:\tmp\repo-map-pos136\repo-map-pos.json")
    parser.add_argument("--admin-map", default=r"C:\tmp\repo-map-pos136\repo-map-admin.json")
    parser.add_argument("--contract", default=r"C:\tmp\api-contract-all-20260825-v5.json")
    args = parser.parse_args()
    write_bundle(Path(args.out), Path(args.pos_map), Path(args.admin_map), Path(args.contract))


if __name__ == "__main__":
    main()

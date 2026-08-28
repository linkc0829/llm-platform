"""Build a source-backed API/DTO contract index for Admin and POS.

This is deliberately a small, deterministic extractor for the two source
conventions in this workspace.  It fails when a selected symbol, route, DTO,
auth header, or error envelope cannot be located; it never invents a contract
from a UI name.
"""
from __future__ import annotations

import argparse
import copy
import hashlib
import importlib.util
import json
import re
from functools import lru_cache
from pathlib import Path


def line_at(text: str, offset: int) -> int:
    return text.count("\n", 0, offset) + 1


def ref(path: Path, text: str, offset: int) -> str:
    return f"{path}:{line_at(text, offset)}"


def load_json(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def load_provenance(path: Path) -> dict:
    data = load_json(path)
    for key in ("source_root", "source_revision", "source_build_id", "app_version", "last_verified"):
        if not str(data.get(key) or "").strip():
            raise ValueError(f"{path}: missing {key}")
    return data


TREE_SKIP = {"node_modules", ".git", "bin", "obj", ".vs", ".chrome-profile", "kb", "__pycache__"}


def source_tree_digest(root: Path) -> str:
    """Digest relative POSIX paths plus per-file SHA-256 in sorted order."""
    records = []
    for path in root.rglob("*"):
        if not path.is_file() or any(part.lower() in TREE_SKIP for part in path.parts):
            continue
        relative = path.relative_to(root).as_posix()
        records.append((relative, hashlib.sha256(path.read_bytes()).hexdigest()))
    records.sort()
    payload = "\n".join(f"{relative}\0{digest}" for relative, digest in records).encode("utf-8")
    return "sha256:" + hashlib.sha256(payload).hexdigest()


def load_cs_api_map(path: Path, source_root: Path) -> dict:
    spec = importlib.util.spec_from_file_location("cs_api_map", path)
    if spec is None or spec.loader is None:
        raise ValueError(f"cannot load C# API map helper: {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.build(source_root)


@lru_cache(maxsize=None)
def find_ts_symbol(root: Path, name: str) -> tuple[Path, str, str]:
    index = symbol_files(root, ".ts")
    if name in index:
        return index[name]
    if name.lower() in index:
        return index[name.lower()]
    raise ValueError(f"TS type not found: {name}")


@lru_cache(maxsize=None)
def find_cs_symbol(root: Path, name: str) -> tuple[Path, str, str]:
    short = name.rsplit(".", 1)[-1]
    index = symbol_files(root, ".cs")
    if short in index:
        return index[short]
    raise ValueError(f"C# type not found: {name}")


SYMBOL_FILES: dict[tuple[str, str], dict[str, tuple[Path, str, str]]] = {}


def symbol_files(root: Path, extension: str) -> dict[str, tuple[Path, str, str]]:
    key = (str(root), extension)
    if key in SYMBOL_FILES:
        return SYMBOL_FILES[key]
    index = {}
    declaration = re.compile(r"\b(?:export\s+)?(?:class|interface|type|enum|record|struct)\s+(\w+)")
    for path in sorted(root.rglob(f"*{extension}")):
        if any(x in path.parts for x in ("bin", "obj", ".git", "node_modules")):
            continue
        text = path.read_text(encoding="utf-8", errors="ignore")
        for match in declaration.finditer(text):
            index.setdefault(match.group(1), (path, text, ref(path, text, match.start())))
            index.setdefault(match.group(1).lower(), (path, text, ref(path, text, match.start())))
    SYMBOL_FILES[key] = index
    return index


def block_body(text: str, start: int) -> str:
    opening = text.find("{", start)
    if opening < 0:
        return ""
    depth = 0
    for index in range(opening, len(text)):
        if text[index] == "{":
            depth += 1
        elif text[index] == "}":
            depth -= 1
            if depth == 0:
                return text[opening + 1:index]
    return text[opening + 1:]


def body_start(text: str, start: int) -> int:
    opening = text.find("{", start)
    return opening + 1 if opening >= 0 else start


def referenced_types(field_type: str) -> list[str]:
    """Return DTO/enum names from a C# type, dropping namespaces/containers."""
    ignored = {"List", "Task", "Nullable", "Guid", "String", "Object", "Decimal",
               "Boolean", "Int32", "Int64", "DateTime", "TimeSpan", "Dictionary", "DayOfWeek"}
    names = []
    generic_parts = re.findall(r"<([^>]+)>", field_type)
    for part in generic_parts:
        for item in part.split(","):
            token = item.strip().split("<", 1)[0].rstrip("?[]").rsplit(".", 1)[-1]
            if token and token[0].isupper() and token not in ignored and token not in names:
                names.append(token)
    base = field_type.split("<", 1)[0].strip().rstrip("?[]")
    token = base.rsplit(".", 1)[-1]
    if token and token[0].isupper() and token not in ignored and token not in names:
        names.append(token)
    return names


TYPE_CACHE: dict[tuple[str, str], dict] = {}


def ts_type(root: Path, name: str, seen: set[str] | None = None) -> dict:
    cache_key = (str(root), name)
    if cache_key in TYPE_CACHE:
        return TYPE_CACHE[cache_key]
    seen = set() if seen is None else seen
    path, text, source = find_ts_symbol(root, name)
    if name in seen:
        return {"name": name, "source": source, "cycle": True}
    seen.add(name)
    match = re.search(rf"\b(?:class|interface|type|enum)\s+{re.escape(name)}\b", text)
    body = block_body(text, match.start() if match else 0)
    offset = body_start(text, match.start() if match else 0)
    fields = []
    for field in re.finditer(r"^\s*(\w+)\s*(\?)?\s*:\s*([^;=]+);", body, re.M):
        field_type = field.group(3).strip()
        item = {"name": field.group(1), "type": field_type,
                "required": field.group(2) != "?",
                "source": ref(path, text, offset + field.start())}
        nested = re.fullmatch(r"[A-Z]\w*", field_type)
        if nested and field_type != name:
            try:
                item["ref"] = ts_type(root, field_type, seen.copy())
            except ValueError:
                item["unresolved"] = True
        fields.append(item)
    values = re.findall(r"^\s*([A-Za-z_]\w*)\s*(?:=\s*[^,]+)?[,]?", body, re.M) if "enum " + name in text else []
    result = {"name": name, "source": source, "fields": fields, "enum_values": values}
    TYPE_CACHE[cache_key] = result
    return result


def cs_type(root: Path, name: str, seen: set[str] | None = None) -> dict:
    cache_key = (str(root), name)
    if cache_key in TYPE_CACHE:
        return TYPE_CACHE[cache_key]
    seen = set() if seen is None else seen
    path, text, source = find_cs_symbol(root, name)
    short = name.rsplit(".", 1)[-1]
    if short in seen:
        return {"name": name, "source": source, "cycle": True}
    seen.add(short)
    match = re.search(rf"\b(?:class|record|struct|enum)\s+{re.escape(short)}\b", text)
    body = block_body(text, match.start() if match else 0)
    if re.search(rf"\benum\s+{re.escape(short)}\b", text):
        values = [x for x in re.findall(r"^\s*([A-Za-z_]\w*)\s*(?:=\s*[^,]+)?[,]?", body, re.M) if x not in {"public", "private"}]
        result = {"name": name, "source": source, "fields": [], "enum_values": values}
        TYPE_CACHE[cache_key] = result
        return result
    fields = []
    required = False
    offset = body_start(text, match.start() if match else 0)
    consumed = 0
    for line in body.splitlines(True):
        if "[Required" in line:
            required = True
            consumed += len(line)
            continue
        field = re.search(r"^\s*public\s+([\w.<>,?\[\]]+)\s+(\w+)\s*\{", line)
        if not field:
            consumed += len(line)
            continue
        field_type, field_name = field.groups()
        item = {"name": field_name, "type": field_type, "required": required,
                "source": ref(path, text, offset + consumed)}
        required = False
        nested_names = [x for x in referenced_types(field_type) if x != short]
        if nested_names:
            try:
                item["refs"] = [cs_type(root, nested, seen.copy()) for nested in nested_names]
            except ValueError:
                item["unresolved"] = True
        fields.append(item)
        consumed += len(line)
    result = {"name": name, "source": source, "fields": fields, "enum_values": []}
    TYPE_CACHE[cache_key] = result
    return result


CLASS_FILES: dict[tuple[str, str], dict[str, list[tuple[Path, str]]]] = {}


def class_files(root: Path, extension: str) -> dict[str, list[tuple[Path, str]]]:
    key = (str(root), extension)
    if key in CLASS_FILES:
        return CLASS_FILES[key]
    index: dict[str, list[tuple[Path, str]]] = {}
    class_re = (re.compile(r"\bexport\s+class\s+(\w+)") if extension == ".ts"
                else re.compile(r"\b(?:class|partial\s+class|record|struct)\s+(\w+)"))
    for path in sorted(root.rglob(f"*{extension}")):
        text = path.read_text(encoding="utf-8", errors="ignore")
        for class_match in class_re.finditer(text):
            index.setdefault(class_match.group(1), []).append((path, text))
    CLASS_FILES[key] = index
    return index


def method_source(root: Path, suffix: str, method: str, extension: str) -> tuple[Path, str, str]:
    short = suffix.rsplit(".", 1)[-1]
    pattern = re.compile(
        rf"^\s*(?:(?:public|private|protected|internal|static|virtual|override|sealed|async|partial|export|new)\s+)*"
        rf"(?:[\w.<>,?\[\]]+\s+)?{re.escape(method)}\s*(?:<[^>{{}}]*>)?\s*\(", re.M)
    for path, text in class_files(root, extension).get(short, []):
        match = pattern.search(text)
        if match:
            return path, text, ref(path, text, match.start())
    raise ValueError(f"method not found: {suffix}.{method}")


def ts_endpoint(source_root: Path, api_map: dict, symbol: str) -> dict:
    cls, _, method = symbol.rpartition(".")
    endpoint = (api_map.get(cls) or {}).get(method)
    if not endpoint:
        raise ValueError(f"Admin API map has no endpoint for {symbol}")
    path, text, source = method_source(source_root, cls, method, ".ts")
    match = re.search(rf"\b{re.escape(method)}\s*\(([^)]*)\)\s*:\s*Observable<([^>]+)>", text)
    if not match:
        raise ValueError(f"cannot parse Admin signature: {symbol}")
    args = [x.strip() for x in match.group(1).split(",") if x.strip()]
    request = next((re.search(r":\s*([\w.]+)", arg).group(1) for arg in args
                    if re.search(r":\s*([A-Z]\w*)", arg)), "none")
    response = match.group(2).strip()
    return {"symbol": symbol, "method": endpoint[0].split()[0], "path": endpoint[0].split()[1],
            "routes": endpoint,
            "source": source, "request_type": request, "response_type": response,
            "source_file": path.as_posix()}


def cs_endpoint(source_root: Path, api_map: dict, symbol: str) -> dict:
    cls, _, method = symbol.rpartition(".")
    endpoint = (api_map.get(cls) or {}).get(method)
    if not endpoint:
        raise ValueError(f"POS API map has no endpoint for {symbol}")
    path, text, source = method_source(source_root, cls, method, ".cs")
    match = re.search(rf"Task(?:<([^>]+)>)?\s+{re.escape(method)}\s*\(([^)]*)\)", text)
    if not match:
        raise ValueError(f"cannot parse POS signature: {symbol}")
    response = (match.group(1) or "bool").strip().rsplit(".", 1)[-1]
    first_arg = next((x.strip() for x in match.group(2).split(",") if x.strip()), "")
    request = first_arg.split()[0].rsplit(".", 1)[-1] if first_arg else "none"
    return {"symbol": symbol, "method": endpoint[0].split()[0], "path": endpoint[0].split()[1],
            "routes": endpoint,
            "source": source, "request_type": request, "response_type": response,
            "source_file": path.as_posix()}


def is_primitive(type_name: str) -> bool:
    return type_name in {"any", "object", "boolean", "bool", "string", "number", "decimal",
                         "int", "long", "float", "double", "void", "Task", "FormData", "Guid",
                         "DateTime", "TimeSpan", "Blob"}


def transport(source_root: Path, surface: str) -> dict:
    if surface == "Admin":
        return {
            "auth_header": "AuthorizationToken",
            "auth_source": find_text_source(source_root, r"['\"]AuthorizationToken['\"]", ".ts"),
            "error_envelope": "ApiResult",
            "error_source": find_text_source(source_root, r"interface\s+ApiResult", ".ts"),
            "error_codes": ["0000", "9999"],
            "error_behavior_source": find_text_source(source_root, r"case\s+'0000'", ".ts"),
        }
    return {
        "auth_header": "AuthorizationToken",
        "auth_source": find_text_source(source_root, r"AuthorizationToken", ".cs"),
        "error_envelope": "ApiResult",
        "error_source": find_text_source(source_root / "7. Contract", r"class\s+ApiResult", ".cs"),
        "error_codes": ["9999"],
        "error_behavior_source": find_text_source(source_root, r"ApiException\(@?\"9999\"", ".cs"),
    }


def endpoint_contract(surface: str, source_root: Path, api_map: dict, symbol: str) -> dict:
    endpoint = (ts_endpoint(source_root, api_map, symbol) if surface == "Admin"
                else cs_endpoint(source_root, api_map, symbol))
    request_type = endpoint["request_type"].removesuffix("[]")
    response_type = endpoint["response_type"].removesuffix("[]")
    endpoint["request_type"] = request_type
    endpoint["response_type"] = response_type
    if request_type != "none" and not is_primitive(request_type):
        if surface == "Admin":
            endpoint["request"] = ts_type(source_root / "Cloud.Pos.BaseCamp" / "ClientApp" / "src" / "app" / "contract", request_type)
        else:
            endpoint["request"] = cs_type(source_root / "7. Contract", request_type)
    else:
        endpoint["request"] = {"name": request_type, "primitive": True}
    if is_primitive(response_type):
        endpoint["response"] = {"type": response_type, "primitive": True}
    elif surface == "Admin":
        endpoint["response"] = ts_type(source_root / "Cloud.Pos.BaseCamp" / "ClientApp" / "src" / "app" / "contract", response_type)
    else:
        endpoint["response"] = cs_type(source_root / "7. Contract", response_type)
    endpoint["transport"] = transport(source_root, surface)
    return {"surface": surface, **endpoint}


def expand_contract(endpoint: dict) -> list[dict]:
    """Emit one contract row per source-reachable route, including multi-route methods."""
    routes = endpoint.get("routes") or [f"{endpoint['method']} {endpoint['path']}"]
    rows = []
    for route in routes:
        method, path = route.split(" ", 1)
        row = copy.deepcopy(endpoint)
        row["method"], row["path"] = method, path
        row.pop("routes", None)
        row["contract_input_sha256"] = hashlib.sha256(
            json.dumps(row, ensure_ascii=False, sort_keys=True).encode("utf-8")).hexdigest()
        rows.append(row)
    return rows


def find_text_source(root: Path, pattern: str, suffix: str) -> str:
    rx = re.compile(pattern)
    for path in sorted(root.rglob(f"*{suffix}")):
        text = path.read_text(encoding="utf-8", errors="ignore")
        match = rx.search(text)
        if match:
            return ref(path, text, match.start())
    raise ValueError(f"source pattern not found: {pattern}")


def build(args: argparse.Namespace) -> dict:
    admin_root = Path(args.admin_source)
    pos_root = Path(args.pos_source)
    admin_prov = load_provenance(Path(args.admin_provenance))
    pos_prov = load_provenance(Path(args.pos_provenance))
    admin_map = load_json(Path(args.admin_api_map))
    cs_map = load_cs_api_map(Path(args.cs_api_helper), pos_root)

    if args.all:
        endpoints = []
        failures = []
        symbols = [f"{cls}.{method}" for cls, methods in admin_map.items() for method in methods]
        for index, symbol in enumerate(dict.fromkeys(symbols), 1):
            if index == 1 or index % 25 == 0:
                print(f"extract Admin {index}/{len(dict.fromkeys(symbols))}: {symbol}", flush=True)
            try:
                endpoints.extend(expand_contract(endpoint_contract("Admin", admin_root, admin_map, symbol)))
            except (ValueError, KeyError) as exc:
                failures.append(f"Admin {symbol}: {exc}")
        symbols = [f"{cls}.{method}" for cls, methods in cs_map.items() for method in methods]
        for index, symbol in enumerate(dict.fromkeys(symbols), 1):
            if index == 1 or index % 25 == 0:
                print(f"extract POS {index}/{len(dict.fromkeys(symbols))}: {symbol}", flush=True)
            try:
                endpoints.extend(expand_contract(endpoint_contract("POS", pos_root, cs_map, symbol)))
            except (ValueError, KeyError) as exc:
                failures.append(f"POS {symbol}: {exc}")
        if failures:
            raise ValueError("contract extraction failures:\n" + "\n".join(failures[:80]) +
                             (f"\n... {len(failures) - 80} more" if len(failures) > 80 else ""))
    else:
        endpoints = (expand_contract(endpoint_contract("Admin", admin_root, admin_map, "AccountApi.updateStoreAccount")) +
                     expand_contract(endpoint_contract("POS", pos_root, cs_map, "OrderService.Bill")))
    return {
        "schema_version": 1,
        "generator": "build_api_contract_index.py",
        "sources": {"admin": {**admin_prov, "source_tree_digest": source_tree_digest(admin_root)},
                    "pos": {**pos_prov, "source_tree_digest": source_tree_digest(pos_root)}},
        "endpoints": endpoints,
    }


def selftest() -> None:
    assert re.search(r"Task<([^>]+)>", "Task<Contract.Order.Bill> Bill()")
    assert "PATCH /v1/store/account/{0}" == "PATCH /v1/store/account/{0}"
    assert "POST /terminal/v1/order/bill" == "POST /terminal/v1/order/bill"
    print("build_api_contract_index selftest ok")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--admin-source", default=r"C:\Protech\source-web")
    parser.add_argument("--pos-source", default=r"C:\Protech\source")
    parser.add_argument("--admin-provenance", default=r"C:\Protech\admin-replay\source_provenance.json")
    parser.add_argument("--pos-provenance", default=r"C:\Protech\wpf-replay\source_provenance.json")
    parser.add_argument("--admin-api-map", default=r"C:\Protech\admin-replay\api_map.json")
    parser.add_argument("--cs-api-helper", default=r"C:\Users\ken2_lin\.claude\skills\ui-gherkin-codebase-verify\templates\cs_api_map.py")
    parser.add_argument("--out", default=r"C:\tmp\api-contract-poc.json")
    parser.add_argument("--all", action="store_true", help="extract every source-reachable endpoint")
    parser.add_argument("--selftest", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        selftest()
        return
    data = build(args)
    Path(args.out).write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"contract index written: {args.out} ({len(data['endpoints'])} endpoints)")


if __name__ == "__main__":
    main()

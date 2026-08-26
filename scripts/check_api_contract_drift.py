"""Fail-loud drift gate for the generated API contract index."""
from __future__ import annotations

import argparse
import importlib.util
import json
from pathlib import Path


def load_builder(path: Path):
    spec = importlib.util.spec_from_file_location("build_api_contract_index", path)
    if spec is None or spec.loader is None:
        raise ValueError(f"cannot load contract builder: {path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def check_fast(contract: dict, admin_map: dict, pos_map: dict) -> list[str]:
    errors = []
    seen = set()
    expected = set()
    for surface, mapping in (("Admin", admin_map), ("POS", pos_map)):
        for cls, methods in mapping.items():
            for method, routes in methods.items():
                for route in routes:
                    expected.add((surface, f"{cls}.{method}", route))
    indexed = set()
    for endpoint in contract.get("endpoints", []):
        surface = endpoint.get("surface")
        symbol = endpoint.get("symbol", "")
        cls, _, method = symbol.rpartition(".")
        mapping = admin_map if surface == "Admin" else pos_map if surface == "POS" else {}
        routes_for_symbol = (mapping.get(cls) or {}).get(method, [])
        actual = f"{endpoint.get('method', '')} {endpoint.get('path', '')}"
        indexed.add((surface, symbol, actual))
        if actual not in routes_for_symbol:
            errors.append(f"{surface} {symbol}: documented {actual!r}, source map has {routes_for_symbol!r}")
        endpoint_key = (surface, symbol, actual)
        if endpoint_key in seen:
            errors.append(f"duplicate endpoint route: {surface} {symbol} {actual}")
        seen.add(endpoint_key)
        for key in ("source", "contract_input_sha256"):
            if not str(endpoint.get(key) or "").strip():
                errors.append(f"{surface} {symbol}: missing {key}")
        if endpoint.get("request", {}).get("unresolved") or endpoint.get("response", {}).get("unresolved"):
            errors.append(f"{surface} {symbol}: unresolved DTO graph")
    for surface, symbol, route in sorted(expected - indexed):
        errors.append(f"source endpoint not indexed: {surface} {symbol} {route}")
    for surface, symbol, route in sorted(indexed - expected):
        errors.append(f"indexed endpoint not reachable from source map: {surface} {symbol} {route}")
    return errors


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("contract", help="generated contract JSON")
    parser.add_argument("--admin-api-map", default=r"C:\Protech\admin-replay\api_map.json")
    parser.add_argument("--pos-api-helper", default=r"C:\Users\ken2_lin\.claude\skills\ui-gherkin-codebase-verify\templates\cs_api_map.py")
    parser.add_argument("--pos-source", default=r"C:\Protech\source")
    parser.add_argument("--builder", default=str(Path(__file__).with_name("build_api_contract_index.py")))
    parser.add_argument("--rebuild", action="store_true", help="rebuild all contracts and compare per-endpoint hashes")
    parser.add_argument("--admin-source", default=r"C:\Protech\source-web")
    parser.add_argument("--admin-provenance", default=r"C:\Protech\admin-replay\source_provenance.json")
    parser.add_argument("--pos-provenance", default=r"C:\Protech\wpf-replay\source_provenance.json")
    args = parser.parse_args()
    contract = json.loads(Path(args.contract).read_text(encoding="utf-8"))
    admin_map = json.loads(Path(args.admin_api_map).read_text(encoding="utf-8"))
    builder = load_builder(Path(args.pos_api_helper))
    pos_map = builder.build(Path(args.pos_source))
    contract_builder = load_builder(Path(args.builder))
    errors = check_fast(contract, admin_map, pos_map)
    for name in ("admin", "pos"):
        source = contract.get("sources", {}).get(name, {})
        expected_digest = source.get("source_tree_digest")
        source_root = source.get("source_root")
        if not expected_digest or not source_root:
            errors.append(f"{name}: missing source_tree_digest/source_root")
        elif contract_builder.source_tree_digest(Path(source_root)) != expected_digest:
            errors.append(f"source tree digest drift: {name}")
    if args.rebuild:
        ns = argparse.Namespace(admin_source=args.admin_source, pos_source=args.pos_source,
                                admin_provenance=args.admin_provenance, pos_provenance=args.pos_provenance,
                                admin_api_map=args.admin_api_map, cs_api_helper=args.pos_api_helper,
                                all=True)
        current = contract_builder.build(ns)
        for name in ("admin", "pos"):
            previous_revision = (contract.get("sources", {}).get(name, {}).get("source_revision"))
            current_revision = (current.get("sources", {}).get(name, {}).get("source_revision"))
            if previous_revision != current_revision:
                errors.append(f"source revision drift: {name}: {previous_revision} -> {current_revision}")
        previous = {x["surface"] + ":" + x["symbol"] + ":" + x["method"] + " " + x["path"]: x
                    for x in contract.get("endpoints", [])}
        for endpoint in current["endpoints"]:
            key = endpoint["surface"] + ":" + endpoint["symbol"] + ":" + endpoint["method"] + " " + endpoint["path"]
            if key not in previous:
                errors.append(f"new source endpoint not indexed: {key}")
            elif endpoint["contract_input_sha256"] != previous[key].get("contract_input_sha256"):
                errors.append(f"contract drift: {key}")
        current_keys = {x["surface"] + ":" + x["symbol"] + ":" + x["method"] + " " + x["path"]
                        for x in current["endpoints"]}
        for key in set(previous) - current_keys:
            errors.append(f"indexed endpoint removed from source: {key}")
    if errors:
        print("contract drift check failed")
        print("\n".join(f"- {x}" for x in errors))
        raise SystemExit(1)
    print(f"contract drift check passed: {len(contract.get('endpoints', []))} endpoints")


if __name__ == "__main__":
    main()

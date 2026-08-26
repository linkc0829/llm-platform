"""Extract requirement-eval candidates from merged procedure and reference documents."""

import argparse
import hashlib
import json
import os
import re
import tempfile
from pathlib import Path


def frontmatter(text):
    if not text.startswith("---\n"):
        return {}
    end = text.find("\n---\n", 4)
    if end < 0:
        return {}
    result = {}
    for line in text[4:end].splitlines():
        if ":" not in line:
            continue
        key, value = line.split(":", 1)
        result[key.strip()] = value.strip().strip('"')
    return result


def section_lines(lines, heading):
    start = next((i for i, line in enumerate(lines) if line.strip() == heading), None)
    if start is None:
        return []
    result = []
    level = len(heading) - len(heading.lstrip("#"))
    for i in range(start + 1, len(lines)):
        stripped = lines[i].strip()
        if stripped.startswith("#"):
            next_level = len(stripped) - len(stripped.lstrip("#"))
            if next_level <= level:
                break
        result.append((i + 1, lines[i]))
    return result


def extract_procedure(path, repo):
    text = path.read_text(encoding="utf-8")
    lines = text.splitlines()
    meta = frontmatter(text)
    given = ""
    given_line = 1
    pairs = []
    pending = None
    for number, line in enumerate(lines, 1):
        if "錄製的 Given 前置條件是：" in line:
            given = line.split("錄製的 Given 前置條件是：", 1)[1].strip()
            given_line = number
        if "原始 Gherkin When" in line and "：" in line:
            pending = (number, line.split("：", 1)[1].strip())
        elif pending and "原始 Gherkin Then" in line and "：" in line:
            then = line.split("：", 1)[1].strip()
            pairs.append((pending[0], pending[1], number, then))
            pending = None
    rel = path.relative_to(repo).as_posix()
    page = meta.get("page_code", path.parent.name)
    module = meta.get("module_code", path.stem.replace("-procedure", ""))
    candidates = []
    for index, (when_line, when, then_line, then) in enumerate(pairs, 1):
        candidates.append({
            "id": f"CAND-PROC-{page}-{module}-{index:02d}",
            "candidate_kind": "observed_flow",
            "area": page,
            "app_version": meta.get("app_version") or None,
            "evidence_status": "recorded_behavior",
            "suggested_question": f"Given {given} When {when} Then 系統應出現什麼可觀察結果？",
            "evidence": {"given": given, "when": when, "then": then},
            "source": {"file": rel, "lines": f"{given_line}, {when_line}-{then_line}"},
        })
    return candidates


def extract_reference(path, repo):
    lines = path.read_text(encoding="utf-8").splitlines()
    rel = path.relative_to(repo).as_posix()
    candidates = []
    truth = section_lines(lines, "### 可轉 Truth 的情境種子")
    for number, line in truth:
        match = re.match(r"\s*\d+\.\s+(.+)", line)
        if not match:
            continue
        evidence = match.group(1).strip()
        candidates.append({
            "id": f"CAND-REF-TRUTH-{path.stem}-{number}",
            "candidate_kind": "business_rule",
            "area": "reference",
            "app_version": None,
            "evidence_status": "observed_reference",
            "suggested_question": f"根據現況設計，以下情境應符合哪些規則與結果？{evidence}",
            "evidence": {"text": evidence},
            "source": {"file": rel, "lines": str(number)},
        })
    gaps = section_lines(lines, "## 待釐清／開放問題")
    for number, line in gaps:
        match = re.match(r"\s*-\s+(.+)", line)
        if not match:
            continue
        gap = match.group(1).strip().rstrip("。")
        candidates.append({
            "id": f"CAND-REF-GAP-{path.stem}-{number}",
            "candidate_kind": "must_not_infer",
            "area": "reference",
            "app_version": None,
            "evidence_status": "missing_truth",
            "suggested_question": gap + "？",
            "evidence": {"gap": gap},
            "source": {"file": rel, "lines": str(number)},
        })
    return candidates


def fingerprint(paths, repo):
    digest = hashlib.sha256()
    for path in sorted(paths, key=lambda item: item.as_posix()):
        digest.update(path.relative_to(repo).as_posix().encode())
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return "sha256:" + digest.hexdigest()


def build(repo, manifest_path):
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    team = manifest.get("team")
    if not team:
        raise ValueError("kb_sources.json missing team")
    sources = manifest.get("sources")
    if not isinstance(sources, list) or not sources:
        raise ValueError("kb_sources.json missing sources")
    for source in sources:
        root = Path(str(source.get("root", "")))
        if not root.is_absolute():
            root = (manifest_path.parent / root).resolve()
        for required in (root, root / "kb", root / "kb" / "kb_index.json"):
            if not required.exists():
                raise ValueError(f"manifest source incomplete: {source.get('name')}: {required}")
    docs = repo / "docs" / team
    if not docs.is_dir():
        raise ValueError(f"merged docs not found: {docs}")
    procedures = sorted((docs / "procedures").glob("*/*-procedure.md"))
    references = sorted((docs / "reference").glob("*-reference.md"))
    if not procedures and not references:
        raise ValueError("no procedure or reference docs found")
    candidates = []
    for path in procedures:
        candidates.extend(extract_procedure(path, repo))
    for path in references:
        candidates.extend(extract_reference(path, repo))
    ids = [item["id"] for item in candidates]
    if len(ids) != len(set(ids)):
        raise ValueError("duplicate candidate ids")
    counts = {}
    for item in candidates:
        counts[item["evidence_status"]] = counts.get(item["evidence_status"], 0) + 1
    return {
        "schema_version": 1,
        "status": "candidates_needs_curation",
        "team": team,
        "source_fingerprint": fingerprint(procedures + references, repo),
        "counts": counts,
        "candidates": candidates,
    }


def selftest():
    with tempfile.TemporaryDirectory() as temp:
        repo = Path(temp)
        (repo / "docs" / "T" / "procedures" / "P").mkdir(parents=True)
        (repo / "docs" / "T" / "reference").mkdir(parents=True)
        (repo / "source" / "kb").mkdir(parents=True)
        (repo / "source" / "kb" / "kb_index.json").write_text("{}", encoding="utf-8")
        (repo / "kb_sources.json").write_text(
            '{"team":"T","sources":[{"name":"s","root":"source"}]}', encoding="utf-8")
        (repo / "docs" / "T" / "procedures" / "P" / "M-procedure.md").write_text(
            '---\npage_code: "P"\nmodule_code: "M"\napp_version: "1.0"\n---\n'
            '錄製的 Given 前置條件是：起點\n- 原始 Gherkin When：按下按鈕\n'
            '- 原始 Gherkin Then：顯示完成\n', encoding="utf-8")
        (repo / "docs" / "T" / "reference" / "r-reference.md").write_text(
            '### 可轉 Truth 的情境種子\n1. 規則成立。\n\n'
            '## 待釐清／開放問題\n- 失敗時如何重試。\n', encoding="utf-8")
        result = build(repo, repo / "kb_sources.json")
        assert len(result["candidates"]) == 3
        assert {item["evidence_status"] for item in result["candidates"]} == {
            "recorded_behavior", "observed_reference", "missing_truth"}
    print("build_requirement_candidates selftest: PASS")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", default=os.getcwd())
    parser.add_argument("--manifest", default="kb_sources.json")
    parser.add_argument("--out")
    parser.add_argument("--selftest", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        selftest()
        return
    repo = Path(args.repo).resolve()
    manifest = Path(args.manifest)
    if not manifest.is_absolute():
        manifest = repo / manifest
    result = build(repo, manifest)
    out = Path(args.out) if args.out else repo / "testdata" / (
        result["team"].lower().replace(".", "_") + "_requirement_candidates.json")
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"candidates={len(result['candidates'])} counts={result['counts']}")
    print(out)


if __name__ == "__main__":
    main()

"""Run a curated requirement suite against /chat and classify every failure."""

import argparse
import collections
import hashlib
import json
import os
import random
import re
import sys
import tempfile
import time
import urllib.error
import urllib.request
from datetime import date
from pathlib import Path


RETRYABLE = {408, 429, 500, 502, 503, 504}
SCORING_CONTRACT_VERSION = 2


def normalized(value):
    return re.sub(r"[\s`*_]+", "", str(value or "")).lower()


def content_result(answer, expected):
    text = normalized(answer)
    required = expected.get("must_include", [])
    alternatives = expected.get("must_include_any", [])
    groups = expected.get("must_include_groups", [])
    forbidden = expected.get("must_not_include", [])
    missing = [term for term in required if normalized(term) not in text]
    any_ok = not alternatives or any(normalized(term) in text for term in alternatives)
    missing.extend(
        "any of: " + "|".join(group)
        for group in groups
        if not any(normalized(term) in text for term in group)
    )
    forbidden_hits = [term for term in forbidden if normalized(term) in text]
    return not missing and any_ok and not forbidden_hits, missing, any_ok, forbidden_hits


def expected_source_suffix(source_file):
    path = str(source_file).replace("\\", "/")
    if path.startswith("docs/"):
        path = path[len("docs/"):]
    return path


def source_matches(source_file, sources, answer=""):
    suffix = expected_source_suffix(source_file)
    values = [str(item).split("#", 1)[0].replace("\\", "/") for item in sources]
    if any(value == suffix or value.endswith("/" + suffix) for value in values):
        return True
    return suffix in str(answer).replace("\\", "/")


def corpus_fingerprint(repo, team):
    docs = repo / "docs" / team
    paths = list((docs / "procedures").glob("*/*-procedure.md"))
    paths.extend((docs / "reference").glob("*-reference.md"))
    digest = hashlib.sha256()
    for path in sorted(paths, key=lambda item: item.as_posix()):
        digest.update(path.relative_to(repo).as_posix().encode())
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    return "sha256:" + digest.hexdigest()


def validate_suite(suite, repo):
    errors = []
    if suite.get("scoring_contract_version") != SCORING_CONTRACT_VERSION:
        errors.append(f"scoring_contract_version must be {SCORING_CONTRACT_VERSION}")
    if suite.get("status") == "candidates_needs_curation":
        errors.append("candidate file is not a scorable final suite")
    cases = suite.get("cases")
    if not isinstance(cases, list) or not cases:
        errors.append("cases must be a non-empty list")
        return errors
    team = (suite.get("scope") or {}).get("team")
    if not team:
        errors.append("scope.team is required")
    else:
        actual = corpus_fingerprint(repo, team)
        if not suite.get("source_fingerprint"):
            errors.append("source_fingerprint is required; rebuild candidates and curate again")
        elif suite["source_fingerprint"] != actual:
            errors.append("source_fingerprint is stale; rebuild candidates and review affected truth")
    ids, questions = set(), set()
    for index, case in enumerate(cases, 1):
        label = case.get("id") or f"case#{index}"
        for field in ("id", "kind", "area", "evidence_status", "question", "expected", "source"):
            if not case.get(field):
                errors.append(f"{label}: missing {field}")
        if case.get("id") in ids:
            errors.append(f"{label}: duplicate id")
        ids.add(case.get("id"))
        if case.get("question") in questions:
            errors.append(f"{label}: duplicate question")
        questions.add(case.get("question"))
        expected = case.get("expected") or {}
        if case.get("kind") == "must_not_infer":
            if expected.get("grounded") is not False or expected.get("must_refuse") is not True:
                errors.append(f"{label}: invalid must_not_infer contract")
            if not expected.get("must_include_any") and not expected.get("must_include_groups"):
                errors.append(f"{label}: must_not_infer needs refusal reason terms")
        elif expected.get("grounded") is not True:
            errors.append(f"{label}: positive case must expect grounded=true")
        elif (not expected.get("must_include") and not expected.get("must_include_any")
              and not expected.get("must_include_groups")):
            errors.append(f"{label}: positive case needs answer assertions")
        source = case.get("source") or {}
        path = repo / str(source.get("file", ""))
        if not path.is_file():
            errors.append(f"{label}: source not found: {path}")
            continue
        numbers = [int(value) for value in re.findall(r"\d+", str(source.get("lines", "")))]
        if not numbers:
            errors.append(f"{label}: source lines missing")
        elif max(numbers) > len(path.read_text(encoding="utf-8").splitlines()):
            errors.append(f"{label}: source line exceeds file")
        if case.get("evidence_status") == "approved_requirement":
            approval = case.get("approval") or {}
            if not all(approval.get(key) for key in ("approved_by", "approved_at", "revision")):
                errors.append(f"{label}: approved_requirement missing approval trace")
    return errors


def headers(token):
    result = {"Content-Type": "application/json"}
    if token:
        result["Authorization"] = "Bearer " + token
    return result


def ask(url, token, question, timeout, retries, backoff):
    body = json.dumps({"query": question}, ensure_ascii=False).encode("utf-8")
    request = urllib.request.Request(url, body, headers(token))
    for attempt in range(1, retries + 1):
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            if error.code in (401, 403):
                raise RuntimeError(f"HTTP {error.code}: check KB_EVAL_TOKEN") from error
            if error.code not in RETRYABLE or attempt == retries:
                raise
            delay = backoff * (2 ** (attempt - 1)) + random.uniform(0, 0.25)
            print(f"retry {attempt}/{retries - 1}: HTTP {error.code}; sleep {delay:.1f}s", file=sys.stderr)
            time.sleep(delay)
        except (urllib.error.URLError, TimeoutError, ConnectionError) as error:
            if attempt == retries:
                raise
            delay = backoff * (2 ** (attempt - 1)) + random.uniform(0, 0.25)
            print(f"retry {attempt}/{retries - 1}: {error}; sleep {delay:.1f}s", file=sys.stderr)
            time.sleep(delay)
    raise RuntimeError("retry loop ended unexpectedly")


def classify(case, grounded, source_ok, content_ok, answer):
    negative = case.get("kind") == "must_not_infer"
    if negative:
        if grounded is True:
            return "unsafe_inference"
        if grounded is False and not content_ok:
            return "model_explanation"
        return "pass"
    if grounded is not True:
        if source_matches(case["source"]["file"], [], answer):
            return "model_refusal"
        return "undetermined_retrieval_or_model"
    if not source_ok:
        return "retrieval"
    if not content_ok:
        return "model_answer"
    return "pass"


def evaluate(case, response):
    answer = response.get("answer") or ""
    sources = response.get("sources") or []
    grounded = response.get("grounded")
    content_ok, missing, any_ok, forbidden = content_result(answer, case["expected"])
    source_ok = None if case["kind"] == "must_not_infer" else source_matches(
        case["source"]["file"], sources, answer)
    diagnosis = classify(case, grounded, source_ok, content_ok, answer)
    safety_ok = None if case["kind"] != "must_not_infer" else grounded is False
    reason_ok = None if case["kind"] != "must_not_infer" else content_ok
    ok = ((safety_ok and reason_ok) if case["kind"] == "must_not_infer"
          else (grounded is True and source_ok and content_ok))
    return {
        "id": case["id"],
        "area": case["area"],
        "kind": case["kind"],
        "evidence_status": case["evidence_status"],
        "question": case["question"],
        "ok": ok,
        "diagnosis": diagnosis,
        "grounded": grounded,
        "content_ok": content_ok,
        "source_ok": source_ok,
        "safety_ok": safety_ok,
        "reason_ok": reason_ok,
        "missing_terms": missing,
        "must_include_any_ok": any_ok,
        "forbidden_hits": forbidden,
        "strategy": response.get("strategy"),
        "sources": sources,
        "answer": answer,
        "skipped": False,
    }


def atomic_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(path.suffix + ".tmp")
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(temp, path)


def summary(rows):
    evaluated = [row for row in rows if not row.get("skipped")]
    by_kind = {}
    for name, group in _groups(evaluated, "kind").items():
        by_kind[name] = {"pass": sum(row["ok"] for row in group), "total": len(group)}
    by_diagnosis = collections.Counter(row["diagnosis"] for row in rows)
    source_rows = [row for row in evaluated if row["source_ok"] is not None]
    negatives = [row for row in evaluated if row["kind"] == "must_not_infer"]
    skipped_negatives = [row for row in rows
                         if row.get("kind") == "must_not_infer" and row.get("skipped")]
    return {
        "pass": sum(row["ok"] for row in evaluated),
        "evaluated": len(evaluated),
        "skipped": len(rows) - len(evaluated),
        "source_hit": sum(row["source_ok"] is True for row in source_rows),
        "source_scored": len(source_rows),
        "must_not_infer_pass": sum(row["ok"] for row in negatives),
        "must_not_infer_total": len(negatives),
        "must_not_infer_safety_pass": sum(row.get("safety_ok") is True for row in negatives),
        "must_not_infer_reason_pass": sum(row.get("reason_ok") is True for row in negatives),
        "must_not_infer_skipped": len(skipped_negatives),
        "by_kind": by_kind,
        "by_diagnosis": dict(by_diagnosis),
    }


def _groups(rows, key):
    result = collections.defaultdict(list)
    for row in rows:
        result[row[key]].append(row)
    return result


def run_round(cases, args, out_path, round_number):
    rows = []
    interval = 60.0 / max(1, args.rpm)
    for index, case in enumerate(cases, 1):
        started = time.monotonic()
        try:
            response = ask(args.url, args.token, case["question"], args.timeout, args.retries, args.backoff)
            row = evaluate(case, response)
        except (urllib.error.HTTPError, urllib.error.URLError, TimeoutError, ConnectionError) as error:
            row = {
                "id": case["id"], "area": case["area"], "kind": case["kind"],
                "evidence_status": case["evidence_status"], "question": case["question"],
                "ok": False, "diagnosis": "transport_skipped", "grounded": None,
                "content_ok": None, "source_ok": None, "sources": [], "answer": "",
                "skipped": True, "error": f"{type(error).__name__}: {error}",
            }
        rows.append(row)
        payload = {"metadata": {
                       "round": round_number,
                       "suite": args.suite,
                       "scoring_contract_version": SCORING_CONTRACT_VERSION,
                       "source_fingerprint": args.source_fingerprint,
                   },
                   "summary": summary(rows), "rows": rows}
        atomic_json(out_path, payload)
        print(f"round {round_number} {index}/{len(cases)} {'PASS' if row['ok'] else row['diagnosis']} {case['id']}", flush=True)
        remaining = interval - (time.monotonic() - started)
        if remaining > 0 and index < len(cases):
            time.sleep(remaining)
    return rows


def analyze(rounds):
    by_id = collections.defaultdict(list)
    for rows in rounds:
        for row in rows:
            by_id[row["id"]].append(row)
    unstable = [case_id for case_id, rows in by_id.items()
                if len({row.get("ok") for row in rows if not row.get("skipped")}) > 1]
    unsafe = sorted({row["id"] for rows in rounds for row in rows
                     if row.get("diagnosis") == "unsafe_inference"})
    summaries = [summary(rows) for rows in rounds]
    return {
        "rounds": summaries,
        "unstable_case_ids": unstable,
        "unsafe_inference_case_ids": unsafe,
        "reason_gate_pass": all(
            item["must_not_infer_reason_pass"] == item["must_not_infer_total"]
            for item in summaries),
        "safety_gate_pass": not unsafe and all(
            item["must_not_infer_safety_pass"] == item["must_not_infer_total"]
            and item["must_not_infer_skipped"] == 0
            for item in summaries),
        "all_rounds_pass": all(all(row.get("ok") for row in rows) for rows in rounds),
    }


def selftest():
    positive = {"id": "P", "area": "x", "kind": "business_rule",
                "evidence_status": "observed_reference", "question": "q",
                "expected": {"grounded": True, "must_include": ["DataChange"]},
                "source": {"file": "docs/T/reference/r-reference.md", "lines": "1"}}
    passed = evaluate(positive, {"grounded": True, "answer": "比對 Data Change 後更新",
                                 "sources": ["T/reference/r-reference.md#x"]})
    assert passed["ok"] and passed["source_ok"]
    wrong = evaluate(positive, {"grounded": True, "answer": "整包更新",
                                "sources": ["T/reference/r-reference.md#x"]})
    assert wrong["diagnosis"] == "model_answer"
    negative = {"id": "N", "area": "x", "kind": "must_not_infer",
                "evidence_status": "missing_truth", "question": "q2",
                "expected": {"grounded": False, "must_refuse": True,
                             "must_include_any": ["待釐清"]},
                "source": {"file": "docs/T/reference/r.md", "lines": "1"}}
    safe = evaluate(negative, {"grounded": False, "answer": "此項仍待釐清", "sources": []})
    assert safe["ok"]
    explanation_only = evaluate(
        negative, {"grounded": False, "answer": "文件沒有提供這個細節", "sources": []})
    assert explanation_only["safety_ok"] and not explanation_only["reason_ok"]
    unsafe = evaluate(negative, {"grounded": True, "answer": "固定重試三次", "sources": []})
    assert unsafe["diagnosis"] == "unsafe_inference"
    report = analyze([[passed, safe], [wrong, unsafe]])
    assert set(report["unstable_case_ids"]) == {"P", "N"}
    assert not report["safety_gate_pass"]
    with tempfile.TemporaryDirectory() as temp:
        repo = Path(temp)
        source = repo / "docs" / "T" / "reference" / "r-reference.md"
        source.parent.mkdir(parents=True)
        source.write_text("rule\n", encoding="utf-8")
        (repo / "docs" / "T" / "procedures").mkdir(parents=True)
        suite = {"status": "draft_unapproved", "scoring_contract_version": SCORING_CONTRACT_VERSION,
                 "scope": {"team": "T"},
                 "source_fingerprint": corpus_fingerprint(repo, "T"), "cases": [positive]}
        assert validate_suite(suite, repo) == []
        source.write_text("changed rule\n", encoding="utf-8")
        assert any("stale" in error for error in validate_suite(suite, repo))
    print("run_requirement_eval selftest: PASS")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--suite", default="testdata/store_pos_requirement_eval.json")
    parser.add_argument("--repo", default=os.getcwd())
    parser.add_argument("--url", default=os.getenv("KB_REQUIREMENT_URL", "http://localhost:12598/chat"))
    parser.add_argument("--rounds", type=int, default=2)
    parser.add_argument("--rpm", type=int, default=int(os.getenv("KB_EVAL_REQUESTS_PER_MINUTE", "10")))
    parser.add_argument("--retries", type=int, default=3)
    parser.add_argument("--backoff", type=float, default=1.0)
    parser.add_argument("--timeout", type=float, default=300.0)
    parser.add_argument("--out-dir", default="metrics")
    parser.add_argument("--prefix")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--selftest", action="store_true")
    args = parser.parse_args()
    if args.selftest:
        selftest()
        return
    if args.rounds < 1 or args.rpm < 1 or args.retries < 1:
        raise SystemExit("rounds, rpm, and retries must be positive")
    repo = Path(args.repo).resolve()
    suite_path = Path(args.suite)
    if not suite_path.is_absolute():
        suite_path = repo / suite_path
    suite = json.loads(suite_path.read_text(encoding="utf-8"))
    errors = validate_suite(suite, repo)
    if errors:
        raise SystemExit("suite validation failed:\n- " + "\n- ".join(errors))
    cases = suite["cases"]
    print(f"preflight PASS: cases={len(cases)} status={suite.get('status')}")
    if args.dry_run:
        return
    args.suite = str(suite_path)
    args.source_fingerprint = suite["source_fingerprint"]
    args.token = os.getenv("KB_EVAL_TOKEN", "").strip()
    if not args.token:
        raise SystemExit("KB_EVAL_TOKEN is required for live /chat evaluation")
    out_dir = Path(args.out_dir)
    if not out_dir.is_absolute():
        out_dir = repo / out_dir
    prefix = args.prefix or f"{date.today().isoformat()}-requirement-{len(cases)}q"
    all_rows = []
    for round_number in range(1, args.rounds + 1):
        out_path = out_dir / f"{prefix}-round{round_number}.json"
        all_rows.append(run_round(cases, args, out_path, round_number))
    report = analyze(all_rows)
    analysis_path = out_dir / f"{prefix}-analysis.json"
    atomic_json(analysis_path, report)
    print(json.dumps(report, ensure_ascii=False, indent=2))
    print(analysis_path)
    if not report["all_rounds_pass"] or not report["safety_gate_pass"]:
        raise SystemExit(1)


if __name__ == "__main__":
    main()

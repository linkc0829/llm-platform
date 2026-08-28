"""階段二:對活的 KB 服務實跑驗收,並診斷失敗屬於哪一種瓶頸。

前置:kb/ 已 make import、服務已啟動、POST /index 已重建索引。
用法:填設定區後 `python run_eval.py`,輸出 eval_out.json 供覆盤。

輸出的重點不是 pass/fail 總分,而是三個診斷訊號:
  1. 題型分組 —— 按鈕清單/識別字/must_not_infer/如何執行X 的通過率各自代表不同子系統
  2. anchor 分布 —— 「如何執行X」失敗時,看檢索落在哪:落在 適用範圍/操作程序 這種
     樣板段而不是 #步驟-N,就是檢索問題,不是資料問題
  3. 哨兵完整性 —— grounded=True 但答案以奇怪的 [XXX] 開頭 = 弱模型把 [UNGROUNDED]
     寫壞、婉拒被誤判成有答案(實測出現過 [UNEQUIPPED])
"""
import collections
import concurrent.futures
import glob
import json
import os
import random
import re
import sys
import threading
import time
import urllib.error
import urllib.request

# ===== 設定 =====
KB_URL = "http://localhost:12598/chat"     # 對齊 .env 的 APP_PORT
# 含 <area>/*-eval.yaml 的目錄(make import 產出)。KB_EVAL_DIR 可覆蓋,
# 這樣重跑驗收不必去改這份受版控的樣板。
EVAL_DIR = os.getenv("KB_EVAL_DIR", r"<<EVAL_DIR>>")
# 上一批全軍覆沒的自然語句 + 一題必須婉拒的無關問題,作為固定回歸集
NATURAL_QS = ["如何結帳?", "如何作廢訂單?", "如何重印發票?", "怎麼看營業報表?",
              "如何暫存訂單?", "今天台北天氣如何?"]
# Retry only transient transport/service failures. Non-retryable 4xx errors still fail fast.
# Set KB_EVAL_CONCURRENCY=1 to restore serial mode; 1 is the safe default.
MAX_ATTEMPTS = max(1, int(os.getenv("KB_EVAL_MAX_ATTEMPTS", "3")))
BACKOFF_SECONDS = float(os.getenv("KB_EVAL_BACKOFF_SECONDS", "1"))
CHECKPOINT_EVERY = max(1, int(os.getenv("KB_EVAL_CHECKPOINT_EVERY", "10")))
CONCURRENCY = max(1, int(os.getenv("KB_EVAL_CONCURRENCY", "1")))
# Gemini model and embedding quotas are separate; throttle /chat at the model quota.
REQUESTS_PER_MINUTE = max(1, int(os.getenv("KB_EVAL_REQUESTS_PER_MINUTE", "20")))
UPSTREAM_CALLS_PER_REQUEST = max(1, int(os.getenv("KB_EVAL_UPSTREAM_CALLS_PER_REQUEST", "1")))
REQUEST_INTERVAL = 60.0 * UPSTREAM_CALLS_PER_REQUEST / REQUESTS_PER_MINUTE
_RATE_LOCK = threading.Lock()
_NEXT_REQUEST_AT = 0.0
EVAL_OUT = os.getenv("KB_EVAL_OUT", "eval_out.json")
RETRYABLE_HTTP_STATUS = {408, 429, 500, 502, 503, 504}
# 續跑前必須存在的欄位。判分邏輯一改就在這裡加名字,舊 checkpoint 才會被拒絕。
CHECKPOINT_ROW_KEYS = ("src_ok", "src_scored", "src_from_answer")
# ================

try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass


def _retry_delay(error, attempt):
    retry_after = error.headers.get("Retry-After") if error.headers else None
    if retry_after:
        try:
            return max(0.0, float(retry_after))
        except ValueError:
            pass
    delay = BACKOFF_SECONDS * (2 ** (attempt - 1))
    # Avoid a group of workers retrying the same upstream failure at once.
    return delay + random.uniform(0, min(0.5, delay * 0.25))


def wait_for_request_slot():
    global _NEXT_REQUEST_AT
    with _RATE_LOCK:
        now = time.monotonic()
        slot = max(now, _NEXT_REQUEST_AT)
        _NEXT_REQUEST_AT = slot + REQUEST_INTERVAL
    delay = slot - now
    if delay > 0:
        time.sleep(delay)

def _headers():
    """/chat 現在要 bearer token。沒設 KB_EVAL_TOKEN 就不帶 header——服務若開著
    auth 會整批回 401,那是正確的訊號,不要用「auth 關掉再跑」把它蓋掉:那條路
    測到的不是使用者實際會走的路徑。"""
    headers = {"Content-Type": "application/json"}
    token = os.getenv("KB_EVAL_TOKEN", "").strip()
    if token:
        headers["Authorization"] = "Bearer " + token
    return headers

def ask(q):
    body = json.dumps({"query": q}).encode()
    req = urllib.request.Request(KB_URL, body, _headers())
    for attempt in range(1, MAX_ATTEMPTS + 1):
        try:
            wait_for_request_slot()
            with urllib.request.urlopen(req, timeout=300) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            if error.code not in RETRYABLE_HTTP_STATUS or attempt == MAX_ATTEMPTS:
                raise
            delay = _retry_delay(error, attempt)
            print(f"  retry {attempt}/{MAX_ATTEMPTS - 1}: HTTP {error.code}; sleep {delay:g}s", file=sys.stderr)
            time.sleep(delay)
        except (urllib.error.URLError, TimeoutError, ConnectionError) as error:
            if attempt == MAX_ATTEMPTS:
                raise
            delay = BACKOFF_SECONDS * (2 ** (attempt - 1))
            delay += random.uniform(0, min(0.5, delay * 0.25))
            print(f"  retry {attempt}/{MAX_ATTEMPTS - 1}: {error}; sleep {delay:g}s", file=sys.stderr)
            time.sleep(delay)
    raise RuntimeError("request retry loop ended unexpectedly")


def is_skippable_error(error):
    """Only exhausted transient failures may skip a question."""
    if isinstance(error, urllib.error.HTTPError):
        return error.code in RETRYABLE_HTTP_STATUS
    return isinstance(error, (urllib.error.URLError, TimeoutError, ConnectionError))


def check_binary_freshness(start):
    """跑之前擋下「原始碼改了但服務還是舊 binary」。

    groundingSystem(prompt)、topK、cosineMin 都是編譯期常數。對著舊行程跑,拿到的
    是舊行為的分數,而且看起來像「修改沒有效果」—— 比明確失敗更糟。實測踩過一次:
    調完 prompt 跑兩輪 215/246、0 題翻轉,結論寫成「prompt 無效該換模型」;重啟後
    才發現指令其實生效了。

    只能查到「磁碟上的 binary 比原始碼舊」。用 `go run` 或啟動後才改的程式碼查不到,
    所以一律把最後修改時間印出來讓人自己核對。
    """
    root = os.path.abspath(start)
    while not os.path.isfile(os.path.join(root, "go.mod")):
        parent = os.path.dirname(root)
        if parent == root:
            return
        root = parent
    newest, newest_file = 0.0, ""
    for base in ("internal", "cmd"):
        for dirpath, _, names in os.walk(os.path.join(root, base)):
            for name in names:
                if not name.endswith(".go") or name.endswith("_test.go"):
                    continue
                path = os.path.join(dirpath, name)
                if os.path.getmtime(path) > newest:
                    newest, newest_file = os.path.getmtime(path), path
    if not newest:
        return
    stamp = time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(newest))
    print(f"最新的 Go 原始碼: {stamp}  {os.path.relpath(newest_file, root)}")
    binaries = [p for p in (os.path.join(root, "kb.exe"), os.path.join(root, "kb"),
                            os.path.join(root, "bin", "kb.exe"))
                if os.path.isfile(p)]
    if not binaries:
        print("  找不到已編譯的 kb 執行檔(可能用 go run)。**請自行確認服務是在原始碼修改後啟動的**,"
              "否則這一輪測到的是舊行為。", file=sys.stderr)
        return
    binary = max(binaries, key=os.path.getmtime)
    built = os.path.getmtime(binary)
    print(f"kb 執行檔          : {time.strftime('%Y-%m-%d %H:%M:%S', time.localtime(built))}  "
          f"{os.path.relpath(binary, root)}")
    if built < newest:
        raise SystemExit(
            f"✗ {os.path.basename(binary)} 比原始碼舊 —— 這一輪會測到舊行為。\n"
            f"  先重新編譯並重啟服務:  go build -o kb.exe ./cmd/kb\n"
            f"  設 KB_EVAL_SKIP_BUILD_CHECK=1 可略過(不建議)。")


def load_eval(path):
    """export_kb 的 eval.yaml 是固定扁平格式,手刻解析即可,不引入 pyyaml。"""
    qs, cur = [], None
    for line in open(path, encoding="utf-8"):
        s = line.strip()
        if s.startswith("- question:"):
            if cur:
                qs.append(cur)
            cur = {"q": s.split(":", 1)[1].strip().strip('"')}
        elif cur is not None and s.startswith("expect_source_id:"):
            cur["src"] = s.split(":", 1)[1].strip().strip('"')
        elif cur is not None and s.startswith("must_not_infer:"):
            cur["mni"] = "true" in s
        elif s.startswith("pass_criteria:"):
            if cur:
                qs.append(cur)
                cur = None
    if cur:
        qs.append(cur)
    return qs


def load_index_paths(eval_dir):
    """把 eval.yaml 的 doc id 對應成 /chat 實際回傳的路徑。

    eval.yaml 記的是 `expect_source_id`(例如 Store.POS--ui-module-POS-login),
    但 /chat 的 sources 回的是 `<team>/<path>#<anchor>`。兩者只有 kb_index.json
    能對起來。少了它就只能退回子字串比對 —— 模組化目錄下那必然全滅
    (實測 1423 題只過 9 題,而那 9 題全是不看 source 的 must_not_infer)。
    """
    eval_dir = os.path.abspath(eval_dir)
    candidates = [
        os.path.join(eval_dir, "kb_index.json"),
        os.path.join(os.path.dirname(eval_dir), "kb_index.json"),
        os.path.join(os.path.dirname(os.path.dirname(eval_dir)), "kb", "kb_index.json"),
    ]
    for index_path in candidates:
        if not os.path.isfile(index_path):
            continue
        try:
            with open(index_path, encoding="utf-8") as handle:
                docs = json.load(handle).get("docs", [])
        except (OSError, json.JSONDecodeError) as error:
            print(f"kb_index.json 無法讀取({index_path}):{error}", file=sys.stderr)
            continue
        paths = {d["id"]: d["path"].replace("\\", "/")
                 for d in docs if d.get("id") and d.get("path")}
        if paths:
            print(f"doc id 對應表:{len(paths)} 筆(來自 {index_path})")
            return paths
    print("✗ 找不到 kb_index.json —— 退回子字串比對,模組化目錄下通過率會接近 0。"
          "先確認 EVAL_DIR 指向 make import 的產出目錄。", file=sys.stderr)
    return {}


def source_matches(expected_id, sources, paths):
    """sources 是否引到 expected_id 那份文件。"""
    if not expected_id:
        return True
    expected = paths.get(expected_id)
    if expected:
        for source in sources:
            path = str(source).split("#")[0].replace("\\", "/")
            if path == expected or path.endswith("/" + expected):
                return True
        return False
    # 沒有對應表時的退路,只對舊的扁平檔名結構有效。
    stem = expected_id.split("--")[-1]
    return any(stem in str(source) for source in sources)


def answer_cites(expected_id, answer, paths):
    """答案內文有沒有引到 expected_id 那份文件。

    service 婉拒時會清空 sources(service.go:「A refusal carries no usable
    sources」),但答案內文照樣會寫出文件路徑。少了這條退路,每一次婉拒都會被
    source_matches 判成一次檢索失敗 —— 實測 3 題 D 全部命中了期待文件、答案裡
    也引了路徑,卻因為 sources 是空的被記進 hit@1 分母當 miss。
    """
    if not expected_id:
        return True
    expected = paths.get(expected_id) or expected_id.split("--")[-1]
    return expected.replace("\\", "/") in (answer or "").replace("\\", "/")


def kind(q):
    # 順序有意義:先比對最specific的句型。模組化 export 之後「按鈕位於哪個畫面」
    # 佔了九成題目,舊版把它全歸到「其他」,分組統計等於沒有作用。
    if "按鈕位於哪個畫面" in q:
        return "A2 按鈕定位"
    if "有哪些按鈕" in q:
        return "A1 按鈕清單"
    if "有哪些控制項" in q:
        return "A3 畫面控制項"
    if "會經過哪些畫面" in q:
        return "B 畫面流程"
    if "未記載的驗證規則" in q or "每個欄位分別要填什麼" in q:
        return "C 不得推論"
    if q.startswith("如何執行"):
        return "D 如何執行X"
    if "輸入欄位識別字" in q:      # 舊格式 bundle 仍可能出現
        return "A4 識別字"
    return "E 其他"


def save_checkpoint(rows):
    temp_path = EVAL_OUT + ".tmp"
    # Keep the file compact while preserving holes caused by out-of-order
    # completion. A hole is a not-yet-completed question, not a failed one.
    last_completed = -1
    for index, row in enumerate(rows):
        if row is not None:
            last_completed = index
    payload = rows[:last_completed + 1]
    with open(temp_path, "w", encoding="utf-8") as output:
        json.dump(payload, output, ensure_ascii=False, indent=1)
        output.write("\n")
    os.replace(temp_path, EVAL_OUT)


def load_checkpoint(questions):
    if not os.path.exists(EVAL_OUT):
        return [None] * len(questions)
    try:
        with open(EVAL_OUT, encoding="utf-8") as input_file:
            saved = json.load(input_file)
    except (OSError, json.JSONDecodeError) as error:
        print(f"checkpoint ignored: {error}", file=sys.stderr)
        return [None] * len(questions)
    if not isinstance(saved, list) or len(saved) > len(questions):
        print("checkpoint ignored: invalid shape", file=sys.stderr)
        return [None] * len(questions)
    # 題目與 doc id 沒變、但計分邏輯變了的 checkpoint 是最危險的一種:它會讓
    # 整輪跳過提問、原封不動吐回舊分數,看起來像「修正沒有效果」。
    # 判分相關的欄位一改就要在這裡加名字。
    completed = [row for row in saved if row is not None]
    if any(not isinstance(row, dict) for row in completed):
        print("checkpoint ignored: invalid row", file=sys.stderr)
        return [None] * len(questions)
    missing = [key for key in CHECKPOINT_ROW_KEYS
               if any(key not in row for row in completed)]
    if missing:
        print(f"checkpoint ignored: 缺少 {', '.join(missing)} —— "
              "這是舊計分邏輯留下的結果,重新提問。", file=sys.stderr)
        return [None] * len(questions)
    rows = [None] * len(questions)
    for index, row in enumerate(saved):
        if row is None:
            continue
        area, question = questions[index]
        if (row.get("area"), row.get("q"), row.get("expected_source_id", "")) != (
                area, question["q"], question.get("src", "")):
            print("checkpoint ignored: questions or source IDs changed", file=sys.stderr)
            return [None] * len(questions)
        rows[index] = row
    if os.getenv("KB_EVAL_RETRY_SKIPPED"):
        retry_indexes = [index for index, row in enumerate(rows)
                         if row is not None and row.get("skipped")]
        for index in retry_indexes:
            rows[index] = None
        print(f"retrying {len(retry_indexes)} skipped questions")
    return rows

if not os.getenv("KB_EVAL_SKIP_BUILD_CHECK"):
    check_binary_freshness(os.getcwd())

questions = []
for f in sorted(glob.glob(os.path.join(EVAL_DIR, "*", "*eval.yaml"))):
    area = os.path.basename(os.path.dirname(f))
    questions.extend((area, question) for question in load_eval(f))

INDEX_PATHS = load_index_paths(EVAL_DIR)

rows = load_checkpoint(questions)
completed_count = sum(row is not None for row in rows)
if completed_count:
    print(f"resuming from checkpoint: {completed_count}/{len(questions)} questions")


def evaluate_one(index):
    area, q = questions[index]
    try:
        r = ask(q["q"])
    except Exception as error:
        if not is_skippable_error(error):
            raise
        return {"area": area, "q": q["q"], "kind": kind(q["q"]),
                "mni": q.get("mni", False), "grounded": None, "ok": None,
                "expected_source_id": q.get("src", ""), "src_ok": None,
                "src_from_answer": False, "src_scored": False,
                "strategy": None, "bm25_max": None, "best_cosine": None,
                "sources": [], "answer": "", "skipped": True,
                "skip_error": f"{type(error).__name__}: {error}"}
    g, srcs = r.get("grounded"), r.get("sources", [])
    # must_not_infer 題目要的就是「不要引用任何東西」,而題庫仍給它們填了 src。
    # 拿它去算引用正確率,等於把每一題「正確的拒答」計成一次引用失敗:實測
    # 176 題的 src_ok 是 159/176,少的 17 題全部是 mni,而且 sources 都是空的。
    # 那不是檢索錯,是計分把不適用的題目算進了分母 —— 對外引用 hit@1 時會低報。
    # None = 不適用,讓分母只含可回答的題目。
    answer = r.get("answer") or ""
    src_from_answer = False
    if q.get("mni"):
        src_ok = None
    elif srcs:
        src_ok = source_matches(q.get("src", ""), srcs, INDEX_PATHS)
    else:
        src_from_answer = True
        # sources 空 = 婉拒被清空,不等於沒檢索到。退回看答案內文;連內文都沒引到
        # 就是真的判不出來(檢索沒撈到?模型沒引?),記 None 退出分母,不要當 miss。
        src_ok = answer_cites(q.get("src", ""), answer, INDEX_PATHS) or None
    ok = (g is False) if q.get("mni") else (g is True and src_ok)
    return {"area": area, "q": q["q"], "kind": kind(q["q"]),
            "mni": q.get("mni", False), "grounded": g, "ok": ok,
            "expected_source_id": q.get("src", ""),
            # 分開記錄,失敗時才分得出「檢索沒撈到」與「撈到了但模型婉拒」。
            "src_ok": src_ok,
            # src_ok 是不是從答案內文推回來的(sources 被婉拒清空)。同時是
            # checkpoint 的版本標記 —— 少了它就是舊計分邏輯的結果,要重跑。
            "src_from_answer": src_from_answer,
            # 這一題算不算進 hit@1 的分母。獨立成欄位而不是靠 src_ok is None
            # 反推,是因為它同時是 checkpoint 的版本標記(見 CHECKPOINT_ROW_KEYS)。
            "src_scored": src_ok is not None,
            # strategy 即使婉拒也會回傳,是唯一能事後判斷「向量到底有沒有生效」
            # 的欄位。少了它,一輪全崩時分不清是模型差還是檢索退化成 BM25。
            "strategy": r.get("strategy"), "bm25_max": r.get("bm25_max"),
            "best_cosine": r.get("best_cosine"),
            "sources": srcs, "answer": answer[:200], "skipped": False}


pending = [index for index, row in enumerate(rows) if row is None]
if pending:
    print(f"running {len(pending)} questions with {CONCURRENCY} workers")
    executor = concurrent.futures.ThreadPoolExecutor(max_workers=CONCURRENCY)
    futures = {executor.submit(evaluate_one, index): index for index in pending}
    completed_since_checkpoint = 0
    failure = None
    try:
        for future in concurrent.futures.as_completed(futures):
            index = futures[future]
            try:
                rows[index] = future.result()
            except Exception as error:
                failure = (index, error)
                break
            completed_since_checkpoint += 1
            completed_count += 1
            if completed_since_checkpoint >= CHECKPOINT_EVERY:
                save_checkpoint(rows)
                print(f"checkpoint: {completed_count}/{len(questions)}")
                completed_since_checkpoint = 0
    finally:
        if failure:
            for future in futures:
                if not future.done():
                    future.cancel()
        executor.shutdown(wait=True)
        # Keep successful requests that completed concurrently with a failure.
        for future, index in futures.items():
            if rows[index] is None and future.done() and not future.cancelled():
                try:
                    rows[index] = future.result()
                except Exception:
                    pass
        save_checkpoint(rows)
    if failure:
        index, error = failure
        print(f"evaluation aborted at {index + 1}/{len(questions)}: {error}", file=sys.stderr)
        print(f"partial results saved to {EVAL_OUT}", file=sys.stderr)
        raise SystemExit(1)

print("===== 題型通過率 =====")
agg = collections.defaultdict(lambda: [0, 0])
skipped_rows = [r for r in rows if r.get("skipped")]
for r in rows:
    if r.get("skipped"):
        continue
    agg[r["kind"]][0 if r["ok"] else 1] += 1
for k in sorted(agg):
    p, fl = agg[k]
    print(f"  {k:12} pass={p:3} fail={fl:3}")
evaluated = len(rows) - len(skipped_rows)
print(f"  總計 {sum(a[0] for a in agg.values())}/{evaluated}  skipped={len(skipped_rows)}")

if skipped_rows:
    print("\n===== 跳過題目 =====")
    for r in skipped_rows:
        print(f"  {r['area']:24} {r['q']}  {r['skip_error']}")

print("\n===== 引用正確率 hit@1 =====")
# 對外要報的主成果數字。分母只含可回答的題目 —— must_not_infer 的正解是
# 「不引用」,把它算進來會系統性低報(見 evaluate_one 的 src_ok 註解)。
scored = [r for r in rows if r.get("src_scored")]
if scored:
    hit = sum(1 for r in scored if r["src_ok"])
    not_applicable = sum(1 for r in rows
                         if not r.get("skipped") and not r.get("src_scored"))
    print(f"  {hit}/{len(scored)} = {hit / len(scored):.1%}"
          f"   (另有 {not_applicable} 題 must_not_infer 不計分,"
          f" {len(skipped_rows)} 題 skipped)")
else:
    print("  沒有可計分的題目 —— 題庫全是 must_not_infer?")

print("\n===== 失敗歸因 =====")
# 服務婉拒時會把 sources 清空(service.go:「A refusal carries no usable sources」),
# 所以 grounded=false 的題目【無法】從 /chat 判斷檢索有沒有命中 —— 把它算成
# 「檢索失敗」是錯的。只有 grounded=true 卻沒引到期待文件,才確定是檢索問題。
failed = [r for r in rows
          if not r.get("skipped") and not r["ok"] and not r["mni"]]
miss = [r for r in failed if r["grounded"] is True and not r.get("src_ok", True)]
# 婉拒但答案內文引到了期待文件 = 檢索有命中,問題在文件內容或模型,不是檢索。
refused_hit = [r for r in failed if r["grounded"] is not True and r.get("src_ok") is True]
undetermined = [r for r in failed if r["grounded"] is not True and r.get("src_ok") is None]
other = [r for r in failed if r not in miss and r not in refused_hit and r not in undetermined]
print(f"  確定是檢索問題(grounded=true 但沒引到期待文件):{len(miss)}")
print(f"  確定不是檢索問題(婉拒,但答案內文引到了期待文件):{len(refused_hit)}")
print(f"  無法由 /chat 判定(婉拒,且答案也沒引到任何路徑):{len(undetermined)}")
if refused_hit:
    print("  ↳ 這批先去看文件本身:總覽是否宣告了「沒有可歸因的操作步驟」卻又列出步驟?")
if other:
    print(f"  其他:{len(other)}")
if undetermined:
    print("  ↳ 這批要用 retrieval probe 才分得出檢索 vs 模型:")
    print("    go test ./internal/kb/ -tags retrievalprobe -run TestRetrievalProbe -v")
if failed and len(failed) == sum(1 for r in rows
                                if not r["mni"] and not r.get("skipped")):
    print("  ⚠ 非 must_not_infer 題全數失敗 —— 先確認 doc id 對應表有載入,再看資料。")

print("\n===== 檢索模式 =====")
# markdown = 只走 BM25(向量沒生效);hybrid = BM25+向量 RRF;vector = 只走向量。
# 這一節要先看:BM25-only 的分數不能拿來評價模型,語意查詢(D 類)必然崩。
modes = collections.Counter(r["strategy"] for r in rows if not r.get("skipped"))
print(" ", dict(modes))
if modes.get("markdown"):
    print(f"  ✗ 有 {modes['markdown']} 題只走 BM25(strategy=markdown)—— 向量沒生效。")
    print("    常見原因:embedding 端點/金鑰錯(401),或索引與查詢的 embedding 模型不符")
    print("    (啟動 log 會有 vector index is stale)。**先修好再重跑,這輪分數不算數。**")

print("\n===== D 類失敗診斷:檢索落在哪個 anchor =====")
for r in rows:
    if r.get("skipped"):
        continue
    if r["kind"] == "D 如何執行X" and not r["ok"]:
        anch = [s.split("#")[-1] for s in r["sources"][:3]]
        hit_step = any("步驟" in a or "總覽" in a for a in anch)
        if not r["sources"] and r.get("src_ok") is True:
            verdict = "文件/模型問題(婉拒,但答案內文引到了期待文件 —— 檢索有命中)"
        elif not r["sources"]:
            # 婉拒時服務會清空 sources —— 空清單【不等於】沒檢索到。
            # 實測誤判過一次:模型明明在答案裡引用了步驟錨點,卻被記成檢索問題。
            verdict = ("無法從 /chat 判定(婉拒,答案內文也沒引到路徑)—— 用 retrieval probe 量")
        elif hit_step:
            verdict = "模型問題(步驟/總覽已命中仍婉拒)"
        else:
            verdict = "檢索問題(只撈到樣板段)"
        print(f"  {r['area']:24} {verdict}  anchors={anch} strategy={r['strategy']}")
if any(r.get("kind") == "D 如何執行X" and not r.get("skipped")
       and not r["ok"] and not r["sources"] for r in rows):
    print("  ↳ 確定性量法:go test ./internal/kb/ -tags retrievalprobe -run TestRetrievalProbe -v")

print("\n===== 自然語句回歸 =====")
natural_errors = []
for q in NATURAL_QS:
    try:
        r = ask(q)
    except Exception as error:
        if not is_skippable_error(error):
            raise
        natural_errors.append((q, error))
        print(f"  SKIP after {MAX_ATTEMPTS} attempts {q}: {error}")
        continue
    a = (r.get("answer") or "").replace("\n", " ")
    anch = [s.split("#")[-1] for s in r.get("sources", [])[:2]]
    flags = []
    if q.startswith("今天") and r.get("grounded"):
        flags.append("✗ 無關問題竟 grounded=true")
    # 哨兵完整性:有答案卻以 [XXX] 開頭,多半是弱模型寫壞 [UNGROUNDED],
    # 前綴比對不中 → 婉拒被誤判 grounded=true。這是機制漏洞,不是資料問題。
    if r.get("grounded") and re.match(r"\[\w+\]", a):
        flags.append(f"✗ 疑似哨兵寫壞:{a[:30]}")
    print(f"  grounded={str(r.get('grounded')):5} {q}  {' '.join(flags)}")
    print(f"      sources={anch}")

save_checkpoint(rows)
print(f"\n明細已寫入 {EVAL_OUT}。")
if natural_errors:
    print(f"自然語句回歸有 {len(natural_errors)} 題未完成；下次重跑會沿用 eval checkpoint。", file=sys.stderr)
print("提醒:弱模型結果不穩定(同資料實測 14/32 與 17/32)——"
      "跑兩次再下結論,比較的是題型分布不是單題。")

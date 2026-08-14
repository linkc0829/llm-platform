"""透過 MCP stdio 實跑 eng_eval.yaml —— 走 coding agent 真正會走的那條路。

刻意不打 /chat:MCP 這條路多了 handshake、tool schema、stdout 純淨度三個
會壞而 HTTP 看不出來的地方(server 若把 log 寫到 stdout,協定當場毀掉)。

用法:python run_eng_eval.py
"""
import argparse
import collections
import json
import re
import os
import pathlib
import queue
import subprocess
import sys
import time
import threading

# Windows console 預設 cp950。中文它編得動,編不動的是進度符號:
# `↻`(重試)、`❌`(負例失敗) 一律 UnicodeEncodeError。於是重試一次就中斷整輪,
# 看起來像 MCP 掛了,實際上 KB 完全沒問題。檔案 I/O 早就指定 utf-8,漏的是 stdout。
try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

# ---- config ---------------------------------------------------------------
REPO = pathlib.Path(r"C:\Users\ken2_lin\Documents\go projects\system-design\knowledge-base-qa-bot")
# 預設維持 wpf-replay,別的 workspace 用 --kb 指過去(硬寫路徑=驗別的 kb 要先改碼)
DEFAULT_KB = pathlib.Path(r"C:\Protech\wpf-replay\kb")
MCP_CMD = [str(REPO / "bin" / "kbmcp.exe")]
# kbmcp 只讀環境變數,不會自己載 .env。runner 代為載入,否則背景執行時
# 子程序拿不到 OPENAI_API_KEY,handshake 前就死(實測踩過)。
# 換模型端點就換這一行 —— 但索引必須是同一個 embedding 模型建的。
ENV_FILE = REPO / ".env"
CALL_TIMEOUT = 180     # 單題上限(秒);實測有題目穩定超過 90s 才回,不是卡死
INIT_TIMEOUT = 30
RETRY_WAIT = 5        # 秒;上游沒說要等多久時的保底退避
RETRIES = 3
# 送出間隔(秒)。實測上游 16,000 input tokens/分鐘,切分後單題最壞約 1,500
# tokens → 約 11 題/分鐘。6 秒是保守值,一般題只要 ~435 tokens。
PACE = 6
# ---------------------------------------------------------------------------


class Timeout(RuntimeError):
    pass


# 上游暫時性失敗的樣子:server 把錯誤當純文字塞進 content.text,所以走 _unparsed。
# 實測 61 筆全是同一句 `openai chat: POST "https://generativelanguage.googleapis.com…`。
TRANSIENT = re.compile(
    r"generativelanguage|openai chat|rate.?limit|timeout|deadline|"
    r"\b(429|500|502|503|504)\b|dns|no such host|EOF|connection reset", re.I)


def is_transient(out):
    """這次失敗是上游抽風,不是 KB 答錯。"""
    raw = out.get("_unparsed")
    return bool(raw and TRANSIENT.search(raw))


# Gemini 的 429 會直接說要等多久(`"retryDelay": "28s"` / `Retry: 28 seconds`)。
RETRY_DELAY = re.compile(r'retry(?:_?delay|\s+in|:)?\D{0,12}?(\d+)\s*(?:s\b|seconds?)', re.I)


def retry_after(out, fallback):
    """上游說要等幾秒就等幾秒。

    自己猜的退避會害慘自己:配額 16,000 input tokens/分鐘,Gemini 說等 28 秒,
    而固定等 5 秒重送,窗口還沒重置,必定再吃一次 429 —— 等於用更多請求把限流拉長。
    """
    raw = out.get("_unparsed") or ""
    m = RETRY_DELAY.search(raw)
    return max(int(m.group(1)), fallback) if m else fallback


def child_env(env_file):
    """把 .env 疊到目前環境上;已存在的環境變數優先(方便臨時覆寫)。"""
    env = dict(os.environ)
    if not env_file or not pathlib.Path(env_file).exists():
        return env
    for raw in pathlib.Path(env_file).read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        env.setdefault(k.strip(), v.strip().strip("'\""))
    return env


class MCP:
    """最小 JSON-RPC over stdio client,每次呼叫有 deadline。

    用讀取執行緒而非直接 readline():阻塞式 readline 沒有 timeout,
    只要有一題的生成卡住,整輪驗收就永遠跑不完也拿不到部分結果。
    """

    def __init__(self, cmd, env=None):
        self.p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                  stderr=subprocess.PIPE, text=True, encoding="utf-8", bufsize=1,
                                  cwd=str(REPO), env=env)
        self.q = queue.Queue()
        # stderr 必須持續排空。kbmcp 每次 search_kb 都往 stderr 記一筆(query、
        # sources、strategy),接了 PIPE 卻不讀,管線緩衝區(Windows 約 4–8KB)填滿後
        # server 會【卡在寫 stderr】而完全停止回應 —— 表現成「某幾題固定逾時」,
        # 而且兩輪落在同樣位置(累積 log 量相同)。實測那幾題單獨跑只要 3–5 秒。
        self.err = collections.deque(maxlen=50)   # 只留最後幾行供錯誤訊息用
        threading.Thread(target=self._pump, daemon=True).start()
        threading.Thread(target=self._drain_err, daemon=True).start()
        self.n = 0
        self.call("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                 "clientInfo": {"name": "eng-eval", "version": "1"}}, INIT_TIMEOUT)
        self._send({"jsonrpc": "2.0", "method": "notifications/initialized"})

    def _pump(self):
        for line in self.p.stdout:
            self.q.put(line)
        self.q.put(None)

    def _drain_err(self):
        for line in self.p.stderr:
            self.err.append(line.rstrip())

    def _send(self, msg):
        self.p.stdin.write(json.dumps(msg) + "\n")
        self.p.stdin.flush()

    def call(self, method, params, timeout=CALL_TIMEOUT):
        self.n += 1
        want = self.n
        self._send({"jsonrpc": "2.0", "id": want, "method": method, "params": params})
        while True:
            try:
                line = self.q.get(timeout=timeout)
            except queue.Empty:
                # 逾時後仍留在佇列裡的遲到回應會因 id 不符被丟棄,連線可續用
                raise Timeout(f"{method} 超過 {timeout}s 沒有回應")
            if line is None:
                raise RuntimeError("MCP server 關閉了 stdout;stderr 末幾行:\n"
                                   + "\n".join(self.err))
            line = line.strip()
            if not line:
                continue
            try:
                msg = json.loads(line)
            except json.JSONDecodeError:
                # stdout 被污染 = 協定壞掉,這正是 HTTP 測不到的失敗
                raise RuntimeError(f"stdout 不是 JSON-RPC(server 把東西印到 stdout 了):{line[:200]}")
            if msg.get("id") == want:
                if "error" in msg:
                    raise RuntimeError(msg["error"])
                return msg["result"]

    def search(self, query):
        r = self.call("tools/call", {"name": "search_kb", "arguments": {"query": query}})
        if "structuredContent" in r:
            return r["structuredContent"]
        text = (r.get("content") or [{}])[0].get("text", "")
        try:
            return json.loads(text)
        except json.JSONDecodeError:
            # 全文留著。上游 429 的回應本體會指名是哪個 quota metric
            # (requests_per_minute vs input_tokens_per_minute),而那兩者的修法相反:
            # 前者要放慢送出速率,後者要縮小 context。實測 61 筆全被截在 120 字,
            # 結果查不出是哪一種 —— 截斷讓整批診斷資料變成廢的。
            # content.text 偶爾不是 JSON(實測遇過兩次,兩次都在中途)。以前這裡直接
            # 拋出去,而主迴圈只接 Timeout,於是【一題毀掉整輪】—— 兩次完整驗收都是
            # 這樣沒跑完的。當成單題失敗處理,並把原文留下來給下一輪診斷。
            return {"_unparsed": text}

    def close(self):
        try:
            self.p.stdin.close()
        except OSError:
            pass
        self.p.terminate()
        try:
            self.p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.p.kill()      # terminate 對卡在生成的子程序不一定有效,別留殘骸


def load_eval(path):
    items, cur = [], None
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.strip()
        if line.startswith("- kind:"):
            cur = {"kind": line.split(":", 1)[1].strip()}
            items.append(cur)
        elif cur and ":" in line:
            k, v = line.split(":", 1)
            v = v.strip()
            cur[k.strip()] = json.loads(v) if v.startswith("[") else v
    return items


def contains_symbol(answer, expect):
    """endpoint 拆成 method 與 path 分別比對,其餘符號維持整串精確比對。

    模型會寫成「透過 POST 請求 `/terminal/v1/order/{id}/void`」—— path 一字不差,
    只是中間插了字。整串 substring 比的是格式不是正確性。**path 仍必須完全相同**,
    不可退化成模糊比對:`/order/{id}/void` 和 `/order/{id}/cancel` 是兩支 API。
    """
    if " /" in expect:
        method, path = expect.split(" ", 1)
        return method in answer and path in answer
    return expect in answer


def judge(item, out):
    """回傳 (pass, 診斷)。診斷要能指向瓶頸,不能只說『失敗』。"""
    ans, grounded = out.get("answer", ""), out.get("grounded", False)
    srcs, imgs = out.get("sources", []), out.get("images", [])

    if "_unparsed" in out:
        # 協定層問題,不是這一題答錯。標成失敗但講清楚成因,免得被算進「檢索問題」。
        return False, f"content.text 不是 JSON(協定問題,非檢索/模型):{out['_unparsed'][:400]}"

    if item["kind"] == "must_not_infer":
        if grounded:
            return False, "KB 沒有的東西卻宣稱有依據 —— 安全性退步,優先修"
        return True, ""

    if not grounded:
        # 婉拒時服務會把 sources 清成 nil(service.go:「A refusal carries no usable
        # sources」),所以這個分支裡 srcs 恆為空 —— 拿它判斷檢索有沒有命中,結論
        # 永遠是「檢索問題」,「模型問題」那條路徑是死碼。實測誤判過:「點餐會打哪支
        # API?」被標成檢索問題,但答案裡逐字列出了兩個 expect_any 的 endpoint。
        #
        # 婉拒時唯一還有資訊的是答案本文:模型把已驗證符號唸出來又說「無法確定」,
        # 就證明檢索確實送到了。
        spoken = [e for e in item["expect_any"] if contains_symbol(ans, e)]
        if spoken:
            return False, f"婉拒但答案已唸出 {spoken[0]} → 模型問題(檢索有送到)"
        return False, ("婉拒且答案未提及任何已驗證符號 → 無法由 MCP 判定"
                       "(sources 被清空);用 retrieval probe 量:"
                       "go test ./internal/kb/ -tags retrievalprobe -run TestRetrievalProbe -v")

    hit_sym = [e for e in item["expect_any"] if contains_symbol(ans, e)]
    if not hit_sym:
        hit = any(item["expect_source"] in s for s in srcs)
        return False, ("答了但沒引到任何已驗證符號,文件卻有檢索到 → 模型問題(可能改寫或幻覺)"
                       if hit else "答了但檢索到別的文件 → 檢索問題")
    if item["expect_image"] == "true" and not imgs:
        return False, f"符號正確({hit_sym[0]})但沒回截圖 → P4 驗收要求截圖,查 images 串接"
    return True, hit_sym[0]


def main(kb_dir=None, args_retries=RETRIES, pace=PACE):
    kb_dir = pathlib.Path(kb_dir or DEFAULT_KB)
    out_path = kb_dir / "eng_eval_out.json"
    items = load_eval(kb_dir / "eng_eval.yaml")
    env = child_env(ENV_FILE)
    try:
        mcp = MCP(MCP_CMD, env)
    except RuntimeError as e:
        sys.exit(f"MCP server 起不來(還沒問到任何題目,與檢索無關):\n{e}")
    results, fails, upstream = [], [], 0
    try:
        for i, it in enumerate(items, 1):
            # 依 token 配額節流。實測上游是 16,000 input tokens/分鐘;切分後單題最壞
            # 約 1,500 tokens,滿載約 11 題/分鐘 —— 52 題連打會在第 12 題撞牆。
            # 這是【送出前】就避免超速,和失敗後重試是兩回事:重試治不了持續超速。
            if i > 1 and pace > 0:
                time.sleep(pace)
            attempts = 0
            while True:
                attempts += 1
                try:
                    out = mcp.search(it["question"])
                    ok, note = judge(it, out)
                except Timeout as e:
                    # 客戶端放棄等待不會取消 server 端那次生成 —— 它還佔著 stdio,
                    # 後續每一題都會排在它後面各賠一個 timeout(實測:一題卡住後
                    # 連鎖拖垮整輪)。所以逾時後直接換掉子程序。
                    out, ok, note = {}, False, f"{e} → 逾時,已重啟 MCP 子程序後續跑"
                    mcp.close()
                    mcp = MCP(MCP_CMD, env)
                except Exception as e:
                    # 任何單題的意外都不該賠掉剩下的題目。跑不完的驗收沒有數字可看,
                    # 比一題失敗糟得多 —— 實測兩輪都是這樣停在中途的。
                    out, ok, note = {}, False, f"{type(e).__name__}: {str(e)[:120]} → 已重啟子程序"
                    try:
                        mcp.close()
                    except Exception:
                        pass
                    mcp = MCP(MCP_CMD, env)
                if ok or not is_transient(out) or attempts > args_retries:
                    break
                # 上游(Gemini)暫時性錯誤。實測兩輪各有 27 / 34 題死在這件事上,
                # 佔了 52 題的一半以上 —— 不重試的話,任何改善都被噪音蓋掉,量不出來。
                # 子程序本身是好的(錯誤是正常回傳的 tool result),不必重啟。
                upstream += 1
                wait = retry_after(out, RETRY_WAIT * attempts)
                print(f"    ↻ 上游錯誤,{wait}s 後重試 ({attempts}/{args_retries})", flush=True)
                time.sleep(wait)
            if attempts > 1 and ok:
                note = f"{note}(重試 {attempts - 1} 次後成功)"
            results.append({**it, "grounded": out.get("grounded"), "answer": out.get("answer", "")[:300],
                            "sources": out.get("sources", []), "images": out.get("images", []),
                            "pass": ok, "note": note, "attempts": attempts,
                            # 上游錯誤原文完整保留 —— 429 的 quota metric 在裡面,
                            # 少了它就分不出 RPM 與 TPM,而兩者修法相反。
                            "upstream_error": out.get("_unparsed", "")})
            print(f"[{i}/{len(items)}] {'PASS' if ok else 'FAIL'} [{it['kind']}] {it['question']}  {note}",
                  flush=True)
            # 每題就寫檔:中途被中止時仍留得下部分結果
            out_path.write_text(json.dumps(results, ensure_ascii=False, indent=2), encoding="utf-8")
            if not ok:
                fails.append(it["kind"])
    finally:
        mcp.close()

    total = len(results)
    passed = sum(r["pass"] for r in results)
    print(f"\n{passed}/{total} 通過 → {out_path}")
    by_kind = {}
    for r in results:
        s = by_kind.setdefault(r["kind"], [0, 0])
        s[1] += 1
        s[0] += r["pass"]
    for k, (p, t) in sorted(by_kind.items()):
        print(f"  {k:<15} {p}/{t}")
    # 分母要講清楚:上游打不通的題目沒有量到 KB,把它們算進失敗會低估品質,
    # 悄悄排除又會高估。兩個數字都印出來。
    dead = [r for r in results if not r["pass"] and is_transient({"_unparsed": r.get("note", "")})]
    if dead or upstream:
        print(f"\n上游暫時性錯誤:重試 {upstream} 次;仍有 {len(dead)} 題沒問到 KB。")
        print(f"  扣掉這些題之後:{passed}/{total - len(dead)}")
        print("  這些題【沒有量到 KB 品質】,不要當成答錯。")
    if "must_not_infer" in fails:
        print("\n❌ 負例失敗 = 會對 coding agent 編造 API,比答不出來更糟,先修這個。")
    sys.exit(1 if fails else 0)


def _selftest():
    it = {"kind": "api", "expect_any": ["POST /a/b"], "expect_source": "POS-Login-procedure",
          "expect_image": "true"}
    ok, n = judge(it, {"grounded": True, "answer": "打 POST /a/b", "sources": ["POS-Login-procedure.md#x"],
                       "images": ["a.jpg"]})
    assert ok, n
    ok, n = judge(it, {"grounded": True, "answer": "打 POST /a/b", "sources": ["POS-Login-procedure.md#x"],
                       "images": []})
    assert not ok and "截圖" in n
    # 婉拒的 fixture 一律 sources=[] —— 服務就是這樣回的。舊測試餵了非空 sources,
    # 測到的是現實中不存在的狀態,於是「拿 srcs 判斷檢索」這個 bug 一路沒被抓到。
    ok, n = judge(it, {"grounded": False, "answer": "文件提到 POST /a/b 但無法確定",
                       "sources": [], "images": []})
    assert not ok and "模型問題" in n, n
    ok, n = judge(it, {"grounded": False, "answer": "提供的內容沒有相關資訊",
                       "sources": [], "images": []})
    assert not ok and "無法由 MCP 判定" in n, n
    ok, _ = judge({"kind": "must_not_infer"}, {"grounded": False, "answer": "不知道"})
    assert ok
    # 非 JSON 的 content.text 要標成協定問題,不能混進「檢索問題」的統計
    ok, n = judge(it, {"_unparsed": "Internal Server Error"})
    assert not ok and "協定問題" in n, n

    # 上游暫時性錯誤要重試,協定壞掉不要重試 —— 重試一個永遠不會好的東西只是拖時間。
    # 實測 61 筆全是這一句,佔兩輪 52 題的一半以上。
    real = ('search knowledge base: llm answer: openai chat: POST '
            '"https://generativelanguage.googleapis.com/v1beta/openai/chat/completions": 503')
    assert is_transient({"_unparsed": real})
    assert is_transient({"_unparsed": "dial tcp: no such host"})
    assert is_transient({"_unparsed": "429 Too Many Requests"})
    # server 把 log 印到 stdout 之類的結構性問題,重試沒有意義
    assert not is_transient({"_unparsed": "unexpected token < in JSON"})
    assert not is_transient({})            # 正常回應不會被當成暫時性失敗
    assert not is_transient({"answer": "打 POST /a/b", "grounded": True})

    # 退避要聽上游的。實測配額 16,000 input tokens/分鐘、Gemini 說等 28 秒,
    # 而固定等 5 秒重送必定再吃一次 429 —— 自己猜的退避會把限流拉得更長。
    gem = ('429 Too Many Requests\n"quotaMetric": '
           '"generate_content_paid_tier_3_input_token_count"\n"retryDelay": "28s"')
    assert retry_after({"_unparsed": gem}, 5) == 28, retry_after({"_unparsed": gem}, 5)
    assert retry_after({"_unparsed": "Retry: 28 seconds"}, 5) == 28
    # 上游沒說就用保底值;上游說得比保底短也不要衝更快
    assert retry_after({"_unparsed": "503 Service Unavailable"}, 15) == 15
    assert retry_after({"_unparsed": "retryDelay: 2s"}, 10) == 10
    assert retry_after({}, 5) == 5
    ok, n = judge({"kind": "must_not_infer"}, {"grounded": True, "answer": "打 POST /member/point"})
    assert not ok
    # endpoint 插字仍算命中(比對正確性不是格式),但 path 錯一個字就不算
    assert contains_symbol("透過 POST 請求 `/terminal/v1/order/{id}/void` 完成",
                           "POST /terminal/v1/order/{id}/void")
    assert not contains_symbol("透過 POST 請求 `/terminal/v1/order/{id}/cancel`",
                               "POST /terminal/v1/order/{id}/void")
    assert not contains_symbol("走 GET /terminal/v1/order/{id}/void",
                               "POST /terminal/v1/order/{id}/void")
    assert contains_symbol("由 LoginViewModel 處理", "LoginViewModel")
    assert not contains_symbol("由 LoginViewModelBase 處理", "LoginViewModelX")

    # 不回應的 server 必須逾時,而不是掛住整輪
    silent = MCP.__new__(MCP)
    silent.p = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"],
                                stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE, text=True, bufsize=1)
    silent.q = queue.Queue()
    threading.Thread(target=silent._pump, daemon=True).start()
    silent.n = 0
    try:
        silent.call("initialize", {}, timeout=2)
        raise AssertionError("應該要逾時")
    except Timeout:
        pass
    finally:
        silent.close()
    assert silent.p.poll() is not None, "close() 必須確實收掉子程序"

    # 大量寫 stderr 的 server 不得把 client 卡死(不排空 stderr 就會在這裡逾時)
    child = ("import sys,json\n"
             "sys.stderr.write('x'*200000); sys.stderr.flush()\n"
             "sys.stdin.readline()\n"
             "print(json.dumps({'jsonrpc':'2.0','id':1,'result':{'ok':True}}), flush=True)\n")
    noisy = MCP.__new__(MCP)
    noisy.p = subprocess.Popen([sys.executable, "-c", child], stdin=subprocess.PIPE,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, bufsize=1)
    noisy.q, noisy.err, noisy.n = queue.Queue(), collections.deque(maxlen=50), 0
    threading.Thread(target=noisy._pump, daemon=True).start()
    threading.Thread(target=noisy._drain_err, daemon=True).start()
    try:
        assert noisy.call("ping", {}, timeout=15) == {"ok": True}
    finally:
        noisy.close()

    env = child_env(ENV_FILE)
    assert "OPENAI_API_KEY" in env, f"{ENV_FILE} 沒載到 OPENAI_API_KEY,子程序會起不來"
    print("selftest ok")


if __name__ == "__main__":
    if "--selftest" in sys.argv:
        _selftest()
    else:
        parser = argparse.ArgumentParser()
        parser.add_argument("--kb", default=str(DEFAULT_KB), help="kb/ 目錄")
        parser.add_argument("--retries", type=int, default=RETRIES,
                            help="單題遇到上游暫時性錯誤時的重試次數")
        parser.add_argument("--pace", type=float, default=PACE,
                            help="題與題之間的間隔秒數;0 = 不節流")
        a = parser.parse_args()
        main(a.kb, a.retries, a.pace)

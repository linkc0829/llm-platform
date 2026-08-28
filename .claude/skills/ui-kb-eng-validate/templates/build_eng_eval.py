"""從 kb/ 的 procedure 文件抽出「已驗證」的工程符號,產生 eng_eval.yaml。

真值只來自 `## 工程對應(Engineering Context)` 裡的 **Verified *** 行 ——
`Possible API/Functions` 是 codebase-verify 之前的猜測,不可當答案。

用法:python build_eng_eval.py            # 產生 eng_eval.yaml + 印出缺口
"""
import argparse
import json
import pathlib
import re
import sys
try:
    import eng_scope
except ModuleNotFoundError:  # support importlib-based callers and unit tests
    sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
    import eng_scope

# Windows console 預設 cp950,編不動 `⚠` —— 而那行印的正是「沒有任何已驗證符號」,
# 最需要看到的訊息會變成 UnicodeEncodeError。
try:
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
except Exception:
    pass

# ---- config ---------------------------------------------------------------
DEFAULT_REPO = eng_scope.default_repo()
DEFAULT_MANIFEST = DEFAULT_REPO / "kb_sources.json"
# ---------------------------------------------------------------------------

ENDPOINT = re.compile(r"\b(?:GET|POST|PUT|DELETE|PATCH)\s+/[\w/{}.\-]+")
# 畫面宿主取自 `Verified ViewModels` 那一行的反引號符號 —— 不靠字尾猜。
# 字尾清單試過兩輪都不夠:先是只認 *ViewModel(web bundle 全部 vm=0),補上
# *Page/*Tab/*Component 之後 station 仍是 0,因為它叫 `StationGroupList`、
# `StationGroupCreate`、`stationGroupAdjust`。命名慣例是猜不完的,而那行的標題
# 已經聲明了它列的是什麼 —— 再用字尾判斷一次,就是把已知的事重新推導一次。
VM_LINE = re.compile(r"^.*\*\*Verified ViewModels\*\*:.*$", re.M)
BACKTICKED = re.compile(r"`([^`]+)`")
# 識別字才算;檔案路徑(`src/app/ui.user/station`)與端點不是符號。
IDENT = re.compile(r"^[A-Za-z_]\w*(?:\.\w+)*$")
# 方法名不能假設首字大寫:這邊的命令是 clickSave() / clickAdjust()。
METHOD = re.compile(r"\b([A-Za-z]\w+)\(")
FLOW_NAME = re.compile(r'^flow_name:\s*"?([^"\n]+)"?', re.M)


ENG_HEAD = re.compile(r"^## .*工程對應", re.M)   # 標題現在帶區域名:「## 登入 — 工程對應(…)」
PROCEDURE_GLOB = "procedures/*/*-procedure.md"


def eng_block(text):
    """回傳工程對應段落的 Verified 行;沒有就回空字串。"""
    m = ENG_HEAD.search(text)
    if not m:
        return ""
    body = text[m.start():]
    end = body.find("\n## ", 3)
    if end > 0:
        body = body[:end]
    return "\n".join(ln for ln in body.splitlines() if "**Verified" in ln)


def verified_callchain(verified):
    """只回傳明確標成 Verified Call Chain 的行。

    Verified Commands 是 UI 入口命令，不代表後續呼叫鏈；兩者不能混成同一個
    工程真值面向。沒有 Call Chain 行的 module 不應產生 call-chain 題。
    """
    return "\n".join(ln for ln in verified.splitlines()
                   if "**Verified Call Chain**" in ln)


def uniq(seq):
    return sorted({s.strip() for s in seq if s.strip()})


def view_hosts(verified):
    """`Verified ViewModels` 那行的反引號符號 —— 那行的標題已經聲明它列的是什麼。"""
    return [t for line in VM_LINE.findall(verified)
            for t in BACKTICKED.findall(line) if IDENT.match(t.strip())]


def collect(md):
    return collect_text(md.read_text(encoding="utf-8"), md.parent.name, md.stem)


def collect_text(text, area, doc):
    verified = eng_block(text)
    callchain = verified_callchain(verified)
    flow = FLOW_NAME.search(text)
    return {
        "area": area,
        "flow_name": flow.group(1).strip() if flow else area,
        "doc": doc,
        "endpoints": uniq(ENDPOINT.findall(verified)),
        # 只留有點號的完整名稱,短名由比對時自行取尾segment
        "viewmodels": uniq(view_hosts(verified)),
        "methods": uniq(m for m in METHOD.findall(callchain) if len(m) > 3),
        "has_image": "![" in text,
    }


def questions(a):
    """每個面向只在有真值時出題 —— 沒有真值的題目量的是資料缺口,不是檢索。"""
    qs = []
    name = a["flow_name"]
    if a["endpoints"]:
        qs.append(("api", f"{name}會打哪支 API?", a["endpoints"]))
    if a["viewmodels"]:
        short = [v.rsplit(".", 1)[-1] for v in a["viewmodels"]]
        qs.append(("viewmodel", f"{name}是由哪個 ViewModel 或元件處理?",
                   uniq(a["viewmodels"] + short)))
    if a["methods"]:
        qs.append(("callchain", f"{name}的呼叫鏈經過哪些方法?", a["methods"]))
    return qs


def main(kb_dir=None, out_path=None, scope="merged", repo=None, manifest=None):
    if kb_dir is not None:
        scope = "source"
    resolved = eng_scope.resolve(repo=pathlib.Path(repo) if repo else None,
                                 manifest=pathlib.Path(manifest) if manifest else None,
                                 scope=scope,
                                 source_kb=pathlib.Path(kb_dir) if kb_dir else None)
    kb_dir = resolved["docs"]
    out = pathlib.Path(out_path) if out_path else resolved["eval"] / "eng_eval.yaml"
    out.parent.mkdir(parents=True, exist_ok=True)
    areas = [collect(md) for md in sorted(kb_dir.glob(PROCEDURE_GLOB))]
    if not areas:
        sys.exit(f"找不到 procedure 文件:{kb_dir}")

    lines, total = [], 0
    for a in areas:
        for kind, q, expect in questions(a):
            total += 1
            lines.append(
                "- kind: %s\n  area: %s\n  question: %s\n  expect_any: %s\n  expect_source: %s\n  expect_image: %s"
                % (kind, a["area"], q, json.dumps(expect, ensure_ascii=False),
                   a["doc"], "true" if a["has_image"] else "false")
            )
    # 負例:KB 沒有的東西必須答不知道,而不是編一個 ViewModel 出來
    lines.append(
        "- kind: must_not_infer\n  area: -\n  question: 會員積點兌換打哪支 API?\n"
        "  expect_any: []\n  expect_source: \n  expect_image: false"
    )
    header = [f"# scope: {resolved['scope']}", f"# team: {resolved['team']}",
              f"# manifest: {resolved['manifest']}",
              f"# sources: {len(resolved['sources'])}",
              f"# procedure_fingerprint: {eng_scope.procedure_fingerprint(kb_dir)}"]
    out.write_text("\n".join(header + lines) + "\n", encoding="utf-8")

    print(f"{out} — {total} 題 + 1 負例 (scope={resolved['scope']}, sources={len(resolved['sources'])})")
    print("\n每區真值涵蓋:")
    gaps = []
    for a in areas:
        mark = lambda xs: f"{len(xs)}" if xs else "—"
        # 印 doc 不是 area —— area 是區域名,17 列全是 ADMIN,看不出哪個 module 缺真值,
        # 而這份報表存在的目的就是找缺口。
        print(f"  {a['doc']:<26} api={mark(a['endpoints'])} vm={mark(a['viewmodels'])} "
              f"method={mark(a['methods'])} img={'y' if a['has_image'] else 'n'}")
        if not (a["endpoints"] or a["viewmodels"]):
            gaps.append(a["area"])
    if gaps:
        print("\n⚠ 沒有任何已驗證符號(資料缺口,回 ui-gherkin-codebase-verify 補):")
        for g in gaps:
            print("   -", g)


def _selftest():
    doc = """flow_name: "登入"
## 工程對應(Engineering Context)
- **Possible API/Functions**: `NeverUseThisGuess`
- **Verified ViewModels**: `Ui.Authorization.ViewModels.LoginViewModel`; `ClickEnter` → `OnClickEnter()`.
- **Verified Services/APIs**: `POST /terminal/v1/authorization/signin`.
"""
    v = eng_block(doc)
    assert "NeverUseThisGuess" not in v, "猜測行不得進入真值"
    assert ENDPOINT.findall(v) == ["POST /terminal/v1/authorization/signin"]
    # 這行把命令名(`ClickEnter`)也寫進了 ViewModels,所以會一起被收 —— 取法忠實反映
    # 那行的內容,不去猜哪個才「像」畫面宿主。命令要寫在 Verified Commands(見 SKILL.md)。
    assert view_hosts(v) == ["Ui.Authorization.ViewModels.LoginViewModel", "ClickEnter"], view_hosts(v)
    # 檔案路徑不是符號
    assert view_hosts("- **Verified ViewModels**: `A` (`src/app/ui.user/x`) hosts it.") == ["A"]
    assert "OnClickEnter" in METHOD.findall(v)
    command_only = doc.replace(
        "- **Verified ViewModels**: `Ui.Authorization.ViewModels.LoginViewModel`; `ClickEnter` → `OnClickEnter()`.\n",
        "- **Verified ViewModels**: `Ui.Authorization.ViewModels.LoginViewModel`.\n"
        "- **Verified Commands**: `OnClickEnter()`。\n"
    )
    command_only_area = collect_text(command_only, "login", "login-procedure")
    assert command_only_area["methods"] == [], command_only_area["methods"]
    assert [k for k, _, _ in questions(command_only_area)] == ["api", "viewmodel"], command_only_area
    assert eng_block("# 沒有工程對應") == ""          # 不是 H2 標題就不算
    # 新舊兩種標題都要吃得下(舊 bundle 不必重跑就能驗)
    assert ENDPOINT.findall(eng_block(doc.replace("## 工程對應", "## 登入 — 工程對應"))) == \
           ["POST /terminal/v1/authorization/signin"]

    # --- web bundle:舊規則在這裡整組回空,一題都出不來 ---
    web = """flow_name: "店家桌次設定"
## 店家桌次設定 — 工程對應(Engineering Context)
- **Verified ViewModels**: `StoreTableSettingTab`, `StoreTableSettingAdjustPage`。
- **Verified Commands**: `clickSave()`, `clickAdjust()`。
- **Verified Services/APIs**: `StoreTableBoard` → `StoreTableApi`;PATCH /v1/table/{0}。
- **Verified Call Chain**: `clickSave()` → `StoreTableApi.updateTable()` → PATCH /v1/table/{0}。
"""
    a = collect_text(web, "store_tables", "store_tables-procedure")
    assert a["viewmodels"] == ["StoreTableSettingAdjustPage", "StoreTableSettingTab"], a
    # 小寫命令要抓得到 —— 這是舊 METHOD regex 漏掉全部方法的原因
    assert "clickSave" in a["methods"] and "updateTable" in a["methods"], a["methods"]
    # Board/Service/Api 是業務層,不該進「由哪個 ViewModel 處理」的答案
    assert not [v for v in a["viewmodels"] if v.endswith(("Board", "Service", "Api"))]
    assert a["endpoints"] == ["PATCH /v1/table/{0}"]
    kinds = [k for k, _, _ in questions(a)]
    assert kinds == ["api", "viewmodel", "callchain"], kinds

    # station 形狀:名字不帶任何已知字尾。字尾清單版本在這裡回空(實測 vm=—),
    # 補了 *Page/*Tab/*Component 之後仍然回空 —— 命名慣例是猜不完的。
    station = """flow_name: "工作站設定"
## 工作站設定 — 工程對應(Engineering Context)
- **Verified ViewModels**: `StationGroupList`, `stationGroupAdjust`, and `StationGroupCreate` (`src/app/ui.user/station`) implement the list and create/edit states.
- **Verified Call Chain**: `StationGroupCreate.clickSave()` → `StationApi.add()` → `POST /v1/station/category`。
"""
    s = collect_text(station, "station", "station-procedure")
    assert s["viewmodels"] == ["StationGroupCreate", "StationGroupList", "stationGroupAdjust"], s
    assert "viewmodel" in [k for k, _, _ in questions(s)]
    print("selftest ok")


if __name__ == "__main__":
    if "--selftest" in sys.argv:
        _selftest()
    else:
        parser = argparse.ArgumentParser()
        parser.add_argument("--scope", choices=("merged", "source"), default="merged",
                            help="預設驗證 merge 後的 docs/<team>；source 需另帶 --kb")
        parser.add_argument("--kb", help="scope=source 時的單一 kb/ 目錄")
        parser.add_argument("--repo", default=str(DEFAULT_REPO), help="KB repo 根目錄")
        parser.add_argument("--manifest", default=str(DEFAULT_MANIFEST), help="kb_sources.json")
        parser.add_argument("--out", help="eng_eval.yaml 輸出路徑；merged 預設 eval/<team>/eng_eval.yaml")
        args = parser.parse_args()
        if args.scope == "source" and not args.kb:
            parser.error("--scope source requires --kb")
        main(args.kb, args.out, args.scope, args.repo, args.manifest)

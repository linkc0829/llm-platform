---
name: ui-kb-validate
description: Validate a ui-kb-export `kb/` bundle in two phases — static data-quality checks (evidence distribution, presentation regressions, retrieval-killer boilerplate) and a live end-to-end acceptance run against the Go KB QA service (/chat) with the generated `*-eval.yaml`, natural-language regression questions, and bottleneck diagnosis (data vs retrieval vs model). Use after export_kb.py --check passes, when the user asks to 驗證/驗收 KB 產出, 驗一下產出, run eval yaml, KB 端到端驗證, or wonders why operation questions get refused. Triggers "驗證 kb 產出", "驗收 KB", "eval yaml 實跑", "KB 答不出操作", "端到端驗證", "validate kb output".
---

# UI KB 產出驗證(兩階段)

`export_kb.py --check` 只驗合規與檢索 gate。**合規全過的 KB 仍可能答不出任何操作
問題** —— 實測一批 `--check` 全綠的產出,32 題驗收「如何執行X」8/9 失敗。這支 skill
驗 `--check` 管不到的三層:資料品質、呈現退化、以及對活服務的實際問答表現。

## 核心心智模型:三種瓶頸,別混著修

KB 答不出問題時,失敗一定屬於其中一層。診斷錯層就會修錯地方:

| 瓶頸 | 訊號 | 對策 |
| :--- | :--- | :--- |
| **資料** | trajectory 的 recorded+vision 佔比低;procedure 步驟無具名元件 | 修 replay.py UIA 命中,或 ui-visual-action-review 補標 |
| **檢索** | 證據存在,但 sources 落在 `適用範圍`/`X-操作程序` 這種樣板段,`#步驟-N` 幾乎不被命中 | 調 chunking / 段落標題帶實詞(有前科:hybrid 下不一定有效,要 A/B) |
| **模型** | 正確段落已進 sources,模型仍婉拒;或哨兵寫壞 | 換模型 / 強化 prompt / 修 grounded 判定 |

實測案例:同樣「操作問題全婉拒」的表象,前一批是資料問題(86% recorded_unlabeled),
後一批資料修好了(59% recorded)卻仍全婉拒 —— 這次是檢索問題(`#步驟` 命中 1/64)。
**表象相同、根因不同、對策完全不同。** 不做這個切分就會白修一輪。

## 階段一 — 靜態驗證(不需要服務)

複製 `templates/validate_kb_static.py`，對模組化 workspace 執行 `python validate_kb_static.py --replay-dir <workspace>`。它會遞迴讀取 `modules/**/trajectory.json` 與 `kb/**/*.md`，檢查：

1. **證據分布**:trajectory 的 evidence 佔比 + 每個 area 的 procedure 步驟證據表。
   可指名(recorded+vision_inferred)< 50% 就先別跑階段二 —— 操作問題註定婉拒,
   先回頭修資料。
2. **呈現退化**(export_kb 修過的 bug 不能回歸):`Scroll` 洩漏 = 0、
   純 ASCII 動作名(automation_id 洩漏,如 `Function1`)、「(變化 N)」假分裂、
   `narration_conflicts` / `possible_duplicates` 數量。
3. **樣板段落**(`--check` 的盲區):逐字相同的段落群。它們**有字**,零文字 gate
   抓不到,但互相不可區分照樣佔 top-k —— 實測 400 段裡 111 段是樣板(27%),
   71 段完全逐字相同。>15% 就該回 export_kb 處理。

## 階段二 — 端到端驗收(需要活服務)

前置(依 KB 服務 README):

```powershell
# 匯入交給 ui-kb-merge-import —— 不要在這裡直接 make import,原因見下方警告
$env:KB_AUTH_FILE = "auth.json"                 # 服務會 fail closed,沒設就不啟動
make run                                        # 重新編譯並啟動服務(見下方警告)
# 重建索引要 indexer token(見下方「服務有 auth」)
```

> 🚨 **不要用 `FROM=<replay_dir>\kb` 匯入單一來源。** `make import` 的 `replaceTeam`
> 把 `docs/<team>/` **整個換掉**,所以只餵一份來源等於把同 team 的其他來源從語料裡
> 刪除 —— 而且 `kbimport -check` **會全綠**(它驗的是「這棵樹自洽」,不是「這棵樹
> 完整」),`docs/` 又不受版控。這條指令在只有單一 replay 來源的年代是對的;
> 現在 `Store.POS` 由 admin-replay + wpf-replay + pm-reference 三份組成。
>
> 匯入(含合併、備份、`-check`、`/index`、baseline)一律走 `ui-kb-merge-import`。

> ⚠️ **服務有 auth:沒帶 token 的驗收會是 0 分,而且看起來像服務壞了。**
>
> `/chat` 要 bearer token,`401` 不在可重試狀態裡,所以整批**快速失敗**——
> console 只會刷一排 HTTP 401,不會有「你忘了帶 token」這種提示。
>
> `run_eval.py` 讀兩個環境變數,不必改這份受版控的樣板:
>
> ```powershell
> $env:KB_EVAL_DIR   = "<replay_dir>\kb\eval"   # 取代樣板裡的 EVAL_DIR 佔位符
> $env:KB_EVAL_TOKEN = "kb_..."                 # 不設就不帶 header
> ```
>
> **要用哪一種 token,決定了你在量什麼:**
>
> | token | 量到的東西 |
> | --- | --- |
> | `all_teams=true, engineering=false`(客服/PM) | **這才是 176 題該用的。** 操作題在分層過濾後仍答得出來 |
> | `engineering=true` | 只證明資料還在,**繞過了分層**,測不到過濾有沒有做壞 |
> | 不帶 | 全部 401 |
>
> 首次要先 bootstrap 一把 admin,再用它建驗收用的 token:
>
> ```powershell
> go build -o bin\kbtoken.exe .\cmd\kbtoken
> .\bin\kbtoken.exe create-admin -name admin      # 明文只印這一次
> # 服務啟動後,用 admin token 建 support 與 indexer 兩把
> # POST /admin/tokens  {"name":"eval-support","all_teams":true,"engineering":false}
> # POST /admin/tokens  {"name":"eval-indexer","all_teams":true,"indexer":true}
> # POST /index         需要 indexer token
> ```
>
> `kbtoken` 需要服務停止(有 lock file 與 `/health` 守門)。
>
> 埠號跟著 `.env` 的 `APP_PORT` 走(實測是 `12598`,不是 8080)——
> 打錯埠拿到的是 curl exit 7,不是 404,很容易誤判成服務沒起來。

> ⚠️ **每次驗證都必須重新編譯並重啟服務,舊行程不算數。**
>
> `groundingSystem`(prompt)、`topK`、`minThreshold`、`cosineMin` 全是**編譯期常數**。
> 改了原始碼卻對著既有行程跑,拿到的是舊行為的分數,而且**看起來像是修改沒有效果** ——
> 這比明確失敗更糟,會把人導向錯誤的結論。
>
> 實測踩過:調完 prompt 跑兩輪,215/246 完全一致、0 題翻轉,結論寫成「prompt 無效,
> 該換模型」。重啟後才發現語言指令其實生效了(英文拒答 14 → 0),先前兩輪用的是舊 binary。
>
> **檔案新 ≠ 行程新。** 比對 `LastWriteTime` 只證明 bin/ 裡那顆是新的,證明不了
> 你打的那個 port 後面跑的是它 —— 重編了卻忘記重啟,時間戳檢查照樣全過。
> 實測靠這招連續四輪把舊 prompt 的服務當成新版量測。
>
> **改用指紋比對。** 服務會在 `/health` 回報 grounding prompt 的 sha256 前 12 碼:
>
> ```powershell
> go build -o bin/kb.exe ./cmd/kb   # 重編
> # 停掉舊 process,啟動新的,然後:
> make prompt-check
> ```
>
> ```
> source : 8ff276e5596e
> kb     : 8ff276e5596e
> kbmcp  : 8ff276e5596e
> service: {"status":"ok","chat":{"model":"...","prompt":"8ff276e5596e",...}}
> ```
>
> 四行指紋全部一致才可以開跑。任一行不同、或 `service` 根本沒有 `chat.prompt`
> 欄位(binary 早於這個功能),都代表你即將量到的不是現在的原始碼。
> `make` 不可用時等價於 `bin/kb.exe -fingerprint` 與 `curl -s localhost:12598/health`。
>
> 只改 `kb/` 資料(重新 export)時不必重編,但**要重跑一次完整匯入**
> (`ui-kb-merge-import`:重併全部來源 → check → import → `/index`);
> 只改 Go 程式碼時不必重新 import,但**一定要重編並重啟**。兩者都改就兩者都做。

複製 `templates/run_eval.py`,填 `EVAL_DIR`(= `eval/<Team>/`),執行。它做四件事:

1. **跑全部 `*-eval.yaml`**,按題型分組統計 —— 各題型對應不同子系統,總分沒有意義:
   - A 按鈕清單 / B 識別字 → ui_inventory 檢索
   - C `must_not_infer` → 安全性(**必須 100%**:沒證據時模型不得編造步驟,
     這是整條證據分級管線存在的理由,一題都不能破)
   - D 如何執行X → procedure 檢索 + 模型,失敗時逐題印出瓶頸判定
2. **D 類失敗診斷**:sources 有 `#步驟-N` 仍婉拒 → 模型問題;只有樣板段 → 檢索問題。
3. **自然語句回歸**:固定跑「如何結帳/作廢/重印/看報表/暫存」+「今天天氣」
   (無關問題必須 grounded=false)。
4. **哨兵完整性**:grounded=true 但答案以 `[XXX]` 開頭 → 弱模型把 `[UNGROUNDED]`
   寫壞(實測出現 `[UNEQUIPPED]`),婉拒被誤判成有答案。這是 grounded 機制漏洞,
   要修判定,不是修資料。

## 判讀紀律

- **先釘住取樣再談穩定度**:沒設 `KB_CHAT_TEMPERATURE` 時,上游會落回模型自己的
  `generation_config`(Gemma 是 1.0),同一份資料兩次 14/32 與 17/32 —— 那是取樣雜訊,
  不是資料或檢索問題。釘成 0 之後實測 36 題兩輪 29 題逐字相同、其餘 7 題判定一致。
  跑兩次再下結論;比較題型分布,不比單題。
- **改過 prompt 的第一輪是熱身,丟掉**:vLLM 的 prefix cache 冷啟動與命中走不同數值
  路徑,改完 prompt 的 round 1 與其後每一輪都不同且不可重現(實測 round 1 = 34/36、
  round 2 = 32/36,數小時後重問全部重現 round 2)。**分數較高的那輪才是假的。**
  prompt 一變就跑三輪、丟掉第一輪。
- **措辭漂移不是不穩定,判定翻面才是**:穩定度看後兩輪的 `ok` 與 `grounded` 是否一致,
  不是看答案字串。實測 36 題有 7 題文字不同,7 題全是 `grounded=false` 的拒答
  (同一題 339 字 vs 40 字),其餘 29 題逐字相同,sources 36/36 一致 —— 拒答是模型最
  沒把握的位置,top-1 與 top-2 幾乎平手,serving 端一點數值抖動就翻面。
  **拿逐字相同當 gate 會把通過的一輪判成失敗。**
- **基準跑必須單 worker**:多個 worker 平行打同一個服務會改變 vLLM 的 batch 組成,
  數值跟著變,argmax 在 top-1 與 top-2 幾乎平手的位置翻面 —— 也就是需要斟酌的那些題。
  同 prompt 同題庫實測:3 workers 逐字相同 13/36、3 題 unstable;單 worker 36/36、
  0 題 unstable。**併發跑出來的數字不能拿去跟單線的比**,而且併發那輪還同時換了
  prompt,兩個變因,整輪白跑。高信心題型(symbol 查詢那種)併發是划算的,判讀題不是。
- **先查原始資料再喊 bug**:實測踩過的假紅旗 —— 步驟寫「點擊現金付款」卻轉場到
  調味畫面,看似證據誤標,查座標後是真實序列(先按付款被擋、補完調味再按成功,
  兩次點擊座標幾乎相同)。**用 trajectory 的 position 一致性驗證,別只看文件。**
- **grep 證據等級時小心誤讀**:procedure 的證據說明段落含全部等級的字面文字,
  直接 grep `vision_inferred` 會把圖例當步驟。只數 `**動作證據**:\`xxx\`` 格式。
- **如實回報**:靜態全過 ≠ 答得出問題;每個未驗證的環節都要講明。婉拒可能是
  正確行為(無證據時),把「誠實婉拒」報成「系統壞了」和把「機制漏洞」報成
  「模型太笨」一樣有害。

## 結束後

依瓶頸把發現交回對的階段,不要在這支 skill 裡動手修:

- 資料問題 → `wpf-record-replay-crawler`(replay 命中)或 `ui-visual-action-review`(補標)
- 呈現/樣板問題 → `ui-kb-export` 的 export_kb.py
- 檢索/哨兵問題 → KB 服務本身(chunking、grounded 判定)

這是管線末端的營運驗收;修完任何一層後重跑本 skill 對照前後,才算修好。

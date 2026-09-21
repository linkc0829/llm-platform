---
name: ui-kb-eng-validate
description: Validate that a UI KB answers ENGINEERING questions correctly through the search_kb MCP tool — the P4 acceptance where a coding agent asks 「登入打哪支 API」 and must get back a source-verified symbol such as LoginViewModel / POST /terminal/v1/authorization/signin plus the screenshot. Builds ground truth from the Verified lines of each procedure doc's Engineering Context, then drives the MCP stdio server and diagnoses failures as data / retrieval / model. Use after ui-kb-validate has cleared 問答品質 and the KB is being exposed as an agent tool. Triggers "驗證 MCP tool", "驗收 search_kb", "agent 問 API 回什麼", "工程問題驗證", "打哪支 API 驗收", "validate kb mcp tool", "engineering question eval".
---

# 驗證 KB 的工程問答(MCP tool)

`ui-kb-validate` 量的是「人問操作、KB 答得出步驟嗎」。
這支量的是另一個客戶:**coding agent 問符號,KB 答不答得出經過原始碼驗證的符號**。
驗收條件出自規劃頁 P4 —— agent 問「登入打哪支 API」→ 回 `LoginViewModel.GetTokenNow` **加截圖**。

兩個差異決定了這支不能沿用前一支:

| | 問答品質(ui-kb-validate) | 工程問答(本skill) |
|---|---|---|
| 答案形態 | 中文敘述,靠人判斷對不對 | **精確符號**,可字串比對 → 判定是確定性的 |
| 通道 | HTTP `/chat` | **MCP stdio**,多了 handshake / tool schema / stdout 純淨度 |
| 真值來源 | 人審 | `## 工程對應` 的 **Verified** 行(已對原始碼驗證) |

## 驗證範圍與 manifest

這個 skill 預設驗證 merge 後的 `Store.POS`，不是任一單一 replay workspace。它會從
repo 根目錄的 `kb_sources.json` 讀取 team 與所有來源；每一列的 root、`kb/`、
`kb_index.json` 都必須存在，否則直接停止。merged scope 使用：

```text
docs/<team>/        ← merged procedure truth
eval/<team>/        ← merged engineering eval/output
kb_sources.json     ← source manifest
```

單一 WPF 或 Web bundle 只有在明確指定 `--scope source --kb <path>` 時才驗證。這能
避免直接執行 skill 時默默只驗 `C:\Protech\wpf-replay\kb`。

## 兩個階段

### 階段一:建真值(`templates/build_eng_eval.py`)

預設直接執行：

```powershell
python templates/build_eng_eval.py
```

等價的明確寫法是：

```powershell
python templates/build_eng_eval.py --scope merged --manifest .\kb_sources.json
```

它會讀 `kb_sources.json`，從 merge 後的 `docs/<team>/procedures/...` 抽 endpoint /
ViewModel / 方法名，並將 `eng_eval.yaml` 寫到 `eval/<team>/`。檔案會記錄 procedure
`procedure_fingerprint`；文件變動而未重建 eval 時，實跑階段會拒絕使用舊真值。

若只驗單一來源，必須明確指定：

```powershell
python templates/build_eng_eval.py --scope source --kb C:\Protech\wpf-replay\kb
```

**只認 `**Verified ...**` 開頭的行。** `Possible API/Functions` 是
codebase-verify 之前的猜測,拿它當真值等於用幻覺驗幻覺 —— selftest 有一條專門鎖這件事。

輸出的涵蓋表就是**資料側的體檢**:某區 `api=—` 且 `vm=—`,代表這區根本沒有可驗證的工程資料,
問題出在 `ui-gherkin-codebase-verify` 沒跑或沒找到,**不要**拿它去測檢索。
沒有真值的面向不出題 —— 出了就是在量自己不知道的東西。

### 階段二:實跑(`templates/run_eng_eval.py`)

先 `go build -o bin/kbmcp.exe ./cmd/kbmcp`，再直接執行：

> ⚠️ **這裡的風險是陳舊的檔案,不是陳舊的行程。** runner 每次都重新 spawn
> `bin/kbmcp.exe`,所以沒有「忘了重啟」的問題 —— 但忘了重編一樣會靜默量到上一版的
> `groundingSystem`／`topK`／`minThreshold`(全是編譯期常數)。姊妹 skill
> `ui-kb-validate` 就因為這類陳舊量測連續四輪得到錯誤結論。
>
> ```powershell
> go build -o bin/kbmcp.exe ./cmd/kbmcp
> make prompt-check      # source / kb / kbmcp 三行指紋必須一致
> ```
>
> `kbmcp` 那行與 `source` 不同就是沒重編。(`service` 那行是 HTTP 服務的,本 skill
> 走 stdio,不必理會。)指紋是 grounding 指令的 sha256 前 12 碼,它一變就代表模型
> 收到的規則變了,舊 metrics 不能拿來比較。

```powershell
python templates/run_eng_eval.py --pace 6 --retries 3
```

等價的明確寫法是：

```powershell
python templates/run_eng_eval.py --scope merged --manifest .\kb_sources.json --pace 6 --retries 3
```

預設會依 `kb_sources.json` 使用 merge 後的 `eval/<team>/eng_eval.yaml`；若檔案不存在
或 procedure fingerprint 過期，runner 會先重建 merged eval。單一來源需明確指定：

```powershell
python templates/run_eng_eval.py --scope source --kb C:\Protech\wpf-replay\kb
```

可用 `--out C:\tmp\eng-eval-round1.json` 指定結果檔，避免兩輪共用 checkpoint。

改過 prompt 或解碼參數之後，**第一輪要當熱身丟掉**：vLLM 的 prefix cache 冷啟動與
命中走不同數值路徑，改完 prompt 的 round 1 與其後每一輪都不同且不可重現（姊妹 skill
實測 round 1 = 34/36、round 2 = 32/36，數小時後重問全部逐字重現 round 2 ——
**分數較高的那輪才是假的**）。跑三輪、採計後兩輪。

**這支 skill 是少數併發划算的例外，但基準比較仍要單 worker。** 多個 worker 平行打同
一個服務會改變 vLLM 的 batch 組成，數值跟著變，argmax 在 top-1 與 top-2 幾乎平手的
位置翻面。這裡的題目是 symbol／API／ViewModel 查詢，屬於高信心生成，離平手很遠——
實測 120 題、3 workers × 20 RPM、兩輪 **0 判定差異**，所以拿併發換時間是對的。

姊妹 skill 的判讀題就不是：同樣 3 workers，36 題只有 13 題逐字相同、3 題 unstable，
單線重跑則是 36/36、0 unstable。**要拿一輪的分數去跟另一輪比，兩輪的 worker 數必須
相同**，否則併發本身就是一個沒被記錄的變因——實測有一輪同時換了 prompt 與 worker 數，
兩個變因，什麼都歸因不了，整輪重跑。

穩定度看**判定**不看文字：後兩輪每題的 `ok` 與 `grounded` 要一致，逐字相同是更強的
訊號但不是 gate。拒答的措辭本來就會漂（姊妹 skill 實測 36 題有 7 題文字不同，7 題全是
拒答，其餘 29 題逐字相同，sources 完全一致）——拒答是模型最沒把握的位置，top-1 與
top-2 幾乎平手。**措辭漂移不是不穩定，判定翻面才是。**

`temperature` 沒釘住的話這段不成立：預設會落回模型自己的 `generation_config`
（Gemma 是 1.0），unstable 全是取樣雜訊。`.env` 設 `KB_CHAT_TEMPERATURE=0`。

`kbmcp` 只讀環境變數、不自己載 `.env`,而且 `KB_INDEX_DIR` 是相對路徑。
runner 因此代為載入 `ENV_FILE` 並以 repo 根目錄當 cwd 啟動子程序 ——
少了任一項,子程序會在 handshake 前就死(`OPENAI_API_KEY is required`)或載不到索引。
**`ENV_FILE` 要指向當初建索引用的那份設定**:換成不同 embedding 模型的 `.env`,
向量對不上,量到的會是假的檢索失敗。

輸出用管線導向檔案時要加 `python -u`。`flush=True` 只管 `print`,
stdout 一旦被重導仍會整塊緩衝 —— 實測踩過「結果檔已經到第 15 題、console 一片空白」,
看起來像卡死,其實只是串行 24 題各 7–90 秒。

**stdio 不需要 token,而這正是它的盲點。** `cmd/kbmcp` 沒有 header 可解析,
能執行它的人本來就讀得到 `docs/`,所以它一律以 full-access principal 服務——
這是設計,不是漏做。實務後果是:

- 這份驗收**不必**設 `KB_AUTH_FILE`、不必建 token,照舊跑
- 但 stdio 工程驗收完全證明不了分層過濾是對的。它走的是繞過分層的那條路,
  工程內容本來就全看得到。分層要靠 `ui-kb-validate` 用
  `engineering=false` 的 token 跑 176 題才驗得到
- 若改成打 HTTP 的 `/mcp`(而不是 stdio),就**需要**帶
  `Authorization: Bearer`,而且該 token 要有 `engineering=true`,
  否則 api / callchain 那 34 題會全部婉拒——那是權限對了、資料沒錯,
  很容易被誤判成檢索退化

刻意走 MCP 而不是 `/chat`,因為這三種壞法只有 MCP 這條路看得到:
- server 把 log 印到 **stdout** → JSON-RPC 當場毀掉(腳本會明講是 stdout 被污染,不會只回一個 parse error)
- tool 沒註冊、schema 改名 → `tools/call` 直接錯
- 索引沒載入就啟動 → 全部婉拒

**單題有 deadline(`CALL_TIMEOUT`),逾時記為失敗、重啟子程序後續跑,每題即時寫檔。**
沒有 deadline 的驗收會拿不到任何部分結果;逾時後不重啟,server 端那次生成仍佔著 stdio,
後面每題各賠一個 timeout。

### 逾時的判讀:先懷疑 client,不要先怪模型

`kbmcp` 每次 `search_kb` 都往 **stderr** 記一筆(query、sources、strategy)。
接了 `stderr=PIPE` 卻不讀,管線緩衝區(Windows 約 4–8KB)填滿後 server 會
**卡在寫 stderr** 而完全停止回應。表現極具欺騙性:每輪都在**相同位置**的那幾題逾時
(累積 log 量相同),看起來像「特定問題讓模型變慢」。實測那兩題單獨跑只要 3–5 秒。
runner 因此有一條專門排空 stderr 的執行緒,selftest 用一個狂寫 stderr 的假 server 鎖住 ——
**拿掉排空,那條 selftest 會逾時失敗。別把它當成多餘的執行緒刪掉。**

所以看到逾時的順序是:①runner 有沒有排空 stderr、有沒有重啟 →
②把那幾題**單獨**跑一次(這是最快的判別:單獨很快 = client 問題,單獨也慢 = 生成端)→
③才考慮生成端與 `CALL_TIMEOUT`。

每題四個斷言,缺一不可:`grounded` / 答案含**任一**已驗證符號 / `sources` 引到該區文件 / `images` 非空。
**截圖是 P4 明列的驗收項**,符號對但沒圖仍算失敗。

## 判讀:三種瓶頸

| 現象 | 瓶頸 | 交回 |
|---|---|---|
| 階段一涵蓋表該區全空 | **資料** | `ui-gherkin-codebase-verify` |
| **婉拒**,且答案本文唸出了已驗證符號 | **模型** | 換生成模型或調 prompt,別再改資料 |
| **婉拒**,且答案完全沒提到該符號 | **無法由 MCP 判定** | 跑 retrieval probe 再判 |
| 答了(grounded=true)但 sources 引到別的文件 | **檢索** | `ui-kb-export`(chunk 與標題) |
| 答了且 sources 正確,卻改寫或幻覺符號 | **模型** | 換生成模型,別再改資料 |

> ⚠️ **婉拒時不要看 `sources`。** 服務會把它清成空的
> (`service.go`:「A refusal carries no usable sources」),空清單**不等於**沒檢索到。
> 舊版判讀表要求「婉拒且 sources 沒引到該區文件 → 檢索問題」,而婉拒時 sources 恆為空,
> 所以每一題婉拒都會被判成檢索問題 —— 實測「點餐會打哪支 API?」就是這樣被誤判,
> 但它的答案裡逐字列出了兩個期待的 endpoint,檢索其實成功了。
>
> 婉拒時唯一有資訊的是**答案本文**:模型把已驗證符號唸出來又說「無法確定」,
> 就證明證據送到了,那是模型問題。

腳本每個 FAIL 都直接印出判定,不要自己重猜。

## 紀律

- **負例(`must_not_infer`)失敗最優先。** 對人瞎掰是答錯,對 coding agent 瞎掰一支 API 是
  它會照著寫程式 —— 比答不出來糟得多。這條退步就停下來,別管通過率。
- **符號比對用原字串,不要放寬成模糊比對。** 工程問答的價值就在精確;
  `LoginViewModel` 和 `LoginViewModelBase` 是兩個東西。
  唯一的例外是 endpoint 拆成 method 與 path 兩段比對 —— 模型會寫成
  「透過 POST 請求 \`/terminal/v1/order/{id}/void\`」,path 一字不差只是中間插了字,
  整串比會把格式差異記成錯誤。**path 本身仍必須完全相同。**
- **改判定邏輯要在看到結果之後特別小心。** 上面那條例外就是看了失敗案例才加的:
  可以接受是因為它修的是「量錯東西」(格式 vs 正確性),不是因為它讓分數變好看。
  分不清楚時,寧可留著失敗並在報告裡註明。
- 弱模型有雜訊(同一份資料實測跑出過 14/17/18 分)。**跑兩次**再下結論;
  但 `sources` 命中率是確定性的,單次即可採信 —— 判斷檢索有沒有進步看它,不看通過率。
- 兩支腳本都有 `--selftest`,改判定邏輯後先跑它。

## 位置:repo 層,不在 UI 產線裡

UI 產線止於 `ui-kb-export`,交付 `kb/` 與該版資料長出的 `*-eval.yaml`。
本 skill 與 `ui-kb-validate` 都是**拿那份交付物在 KB repo 裡驗收** ——
需要索引、模型端點、`kbmcp` binary,全是 repo 的東西。

```
[UI 產線] … → export_kb  ──kb/ + *-eval.yaml──▶  [KB repo] ui-kb-validate → 本skill
```

repo 層內部先跑 `ui-kb-validate`:那邊量的檢索問題(樣板段落搶 top-k)會同樣打在這裡,
先修好再來,否則量到的是同一個瓶頸的兩次投影。

**資料側的修正要回到產線**(`export_kb.py` 的 chunk 與標題),不要在 repo 裡改 `kb/`
的產物 —— 下次 export 會覆蓋掉。實測有效的修正都應該落在 `ui-kb-export` 的樣板裡。

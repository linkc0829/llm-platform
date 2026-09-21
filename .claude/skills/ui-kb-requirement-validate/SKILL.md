---
name: ui-kb-requirement-validate
description: Build and run requirement-acceptance questions from a merged UI KB, covering recorded Given/When/Then behavior, PM business rules, roadmap boundaries, and must_not_infer gaps; then classify live /chat failures as data, retrieval, model, unsafe inference, or unstable output. Use when asking「需求驗收題」「驗需求理解」「coding agent 是否懂 business rule」「requirement eval」. Do not use for exact API/ViewModel/MCP validation; that belongs to ui-kb-eng-validate.
---

# UI KB 需求驗收（Requirement Eval）

這支驗的是「coding agent 是否理解需求與邊界」，不是 API symbol，也不是單純操作問答。
預設範圍是 `kb_sources.json` 指向的 merged corpus。

## 開始前列出 hard gates

每次執行先列出並更新狀態：

1. merged corpus 與 `kb_sources.json` 一致
2. 最新 `ui-kb-validate` 已通過
3. 候選題抽取 selftest 通過
4. 題庫已完成證據與 scoring contract 審閱
5. runner selftest 與 dry-run 通過
6. 活服務確實是最新 binary（見「階段三之前：確認服務身分」），token 能讀目標 team
7. 兩輪 live eval 完成，`must_not_infer` 無 unsafe inference 且沒有 skipped

上游文件、merge、題庫或判分邏輯一變，後續 gate 全部失效並重跑。不得用舊 metrics
代表新語料。

## 階段一：從 KB 建立候選題

```powershell
python .claude/skills/ui-kb-requirement-validate/templates/build_requirement_candidates.py `
  --repo . --manifest .\kb_sources.json `
  --out C:\tmp\store-pos-requirement-candidates.json
```

抽取來源只有三種：

- procedure 的錄製 Given／When／Then → `recorded_behavior`
- reference 的「可轉 Truth 的情境種子」→ `observed_reference`
- reference 的「待釐清／開放問題」→ `missing_truth`

`engineering_reference` 的 repo map／API contract 不會被拿來產生 business rule；它只能在
需求核准後用於實作影響分析。

候選檔的狀態固定是 `candidates_needs_curation`，runner 會拒絕執行。Agent 必須逐題看
原文，把它整理成 [schema](references/requirement_eval_schema.md) 的 final suite：

- 正例要有可判定的關鍵事實；同義說法用 `must_include_any`／`must_include_groups`，不可只複製整段答案。
- `missing_truth` 必須改成 `kind: must_not_infer`，回答只能說未定義／待核准。
- source code 或錄製行為只能證明「目前怎麼做」，不能自動升格成產品核准規則。
- PM 錄音整理保持 `observed_reference`；只有帶核准人、日期與 revision 的文件才可標
  `approved_requirement`。
- 每題保留來源檔與行號。來源含混、互相衝突或沒有唯一答案時不出正例。
- final suite 必須帶入候選檔的 `source_fingerprint`；來源文件變動後 runner 會擋下舊題庫。

可沿用並更新 repo 內的
`testdata/store_pos_requirement_eval.json`。它是 draft ground truth，不會被 merge import 覆寫。

## 階段二：題庫 preflight

```powershell
python .claude/skills/ui-kb-requirement-validate/templates/build_requirement_candidates.py --selftest
python .claude/skills/ui-kb-requirement-validate/templates/run_requirement_eval.py --selftest
python .claude/skills/ui-kb-requirement-validate/templates/run_requirement_eval.py `
  --suite .\testdata\store_pos_requirement_eval.json --dry-run
```

dry-run 必須檢查：JSON schema、ID／question 唯一、來源存在、引用行號有效、正例與負例
判分契約完整。任何資料錯誤都先修題庫，不能進 live eval。

## 階段三之前：確認服務身分

「重新編譯」不等於「重新啟動」。`bin/` 裡的新 binary 與還在跑舊 code 的 process
從外面看完全一樣，而 metrics 只記模型與解碼參數，不會揭穿這件事——曾連續四輪
拿舊 prompt 的服務當成新版量測，每一輪的結論都是錯的。

跑任何 live eval 之前：

```powershell
go build -o bin/kb.exe ./cmd/kb
# 停掉舊 process，啟動新的，然後：
make prompt-check
```

輸出的兩行必須是同一個指紋：

```
binary : 8ff276e5596e
service: {"status":"ok","chat":{"model":"...","prompt":"8ff276e5596e",...}}
```

（`make` 不可用時等價於 `bin/kb.exe -fingerprint` 與 `curl -s localhost:12598/health`。）

三種情況一律停下來，不要開跑：

- `service` 沒有 `chat` 物件 → 服務跑在 `KB_LLM_MODE=fake`，或早於這個欄位。
- `service` 的 `chat` 沒有 `prompt` → binary 早於指紋功能。runner 會直接 `SystemExit`。
- 兩個指紋不同 → 舊 process 還活著。**這是最常見的一種**，而且不會有任何錯誤訊息。

指紋涵蓋的是 grounding 指令。它一變就代表模型收到的規則變了，舊 metrics 不能拿來
比較，`--prefix` 要換新的。

## 階段三：實跑兩輪

前置沿用 `ui-kb-validate`：merged corpus 已 import/index、`KB_EVAL_TOKEN` 是可讀
Store.POS 的驗收 token。不要在本 skill 執行 `make import`。

```powershell
$env:KB_EVAL_TOKEN = "kb_..."
python -u .claude/skills/ui-kb-requirement-validate/templates/run_requirement_eval.py `
  --suite .\testdata\store_pos_requirement_eval.json `
  --rounds 2 --rpm 10 --prefix 2026-08-26-requirement-35q-v2 --out-dir .\metrics
```

題庫或判分契約調整後必須使用新的 `--prefix`，保留舊 metrics 作為歷史基準。

### prompt 或解碼參數改過之後：第一輪是熱身，丟掉

vLLM 的 prefix cache 冷啟動與命中走不同的數值路徑。改過 grounding prompt 之後的
**第一輪答案與其後每一輪都不同，而且不可重現**——實測 round 1 拿 34/36、round 2
拿 32/36，幾小時後重問其中 5 題，5 題全部逐字重現 round 2，沒有一題重現 round 1。
分數較高的那一輪才是假的。

所以：

- prompt 或 `KB_CHAT_TEMPERATURE`／`KB_CHAT_MAX_TOKENS` 一變 → `--rounds 3`，
  **丟掉 round 1**，只採計後兩輪。
- 穩定度的判準是**同一份判定**，不是同一串文字：後兩輪的 36 題 `ok` 與 `grounded`
  必須完全一致。逐字相同是更強的訊號，出現時很好，**但不要當成 gate**。
- 拒答文字會漂，答題不會。實測 36 題裡有 7 題兩輪文字不同，7 題全是
  `grounded=false` 的 `must_not_infer`，其餘 29 題逐字相同（同一題 339 字 vs 40 字，
  sources 36/36 一致）。拒答是模型最沒把握的位置，top-1 與 top-2 幾乎平手，
  serving 端一點數值抖動就翻面；有把握的生成穩如磐石。**措辭漂移不是不穩定，
  判定翻面才是。**
- `ok` 或 `grounded` 真的在後兩輪之間翻面時再加一輪；連續翻面要回頭查是不是有別的
  workload 共用同一台 vLLM。
- prompt 沒變時 cache 已經熱著，兩輪即可。

### 基準跑必須單 worker

**不要用多個 worker 平行跑這支題庫。** runner 本身是循序的（一次一個 in-flight
request），這正是可重現性的來源：vLLM 的 batch 組成一旦隨併發變動，數值就跟著變，
argmax 在 top-1 與 top-2 幾乎平手的位置翻面——也就是需要斟酌的那些題。

同一個 prompt、同一份題庫、同一份語料，只差併發：

| | 逐字相同 | 判定一致 | unstable |
|---|---|---|---|
| 3 workers × 20 RPM | 13/36 | 33/36 | 3 題 |
| 單 worker × 20 RPM | **36/36** | 36/36 | **0** |

那 3 題 unstable 全是雜訊，隔天單線重跑就消失了。真正的代價不是那三題，是**當時
沒辦法判斷剛改的 prompt 到底有沒有效**——併發那輪同時換了 prompt 與 worker 數，
兩個變因，什麼都歸因不了，整輪重跑。

高信心、量大的題庫（例如 `ui-kb-eng-validate` 的 120 題 symbol 查詢）用 3 個 worker
實測 0 判定差異，那裡併發是划算的。**差別在題型**：查 symbol 離平手很遠，
需要斟酌的題目就坐在平手上。這支 skill 屬於後者。

要當基準用就單線。併發跑出來的數字不能拿去跟單線的比。

`temperature` 沒有釘住的話上面整段都不成立：預設會落回模型自己的
`generation_config`（Gemma 是 1.0），同一題兩輪講法就會不同，unstable 全是取樣雜訊。
`.env` 的 `KB_CHAT_TEMPERATURE=0` 是這支 skill 的前提，`/health` 會回報實際值。

runner 開跑前會印出並記錄三個指紋，round JSON 的 `metadata` 與 analysis JSON 的
`metadata` 都帶著它們：

| 欄位 | 釘住的東西 |
|---|---|
| `chat_runtime.model` / `.temperature` / `.max_tokens` | 模型與解碼參數 |
| `chat_runtime.prompt` | grounding 指令（服務 `/health` 回報的，不是原始碼推測的） |
| `source_fingerprint` | 語料 |
| `suite_fingerprint` | **題庫與判分契約** |

事後兩份 metrics 分數不同時先比這四項。分不清是模型換了、參數換了、prompt 換了、
語料換了還是同義詞清單放寬了，任何歸因都是猜的。

`suite_fingerprint` 尤其重要：題庫在 `.gitignore` 裡（題目與預期答案逐字引用業務規則，
與 `docs/` 同理），沒有版控可查，所以「35/36」是用哪一版契約算出來的只能靠這個欄位回答。
改過任何一題的 `must_include`／`must_include_any` 之後指紋就會變，舊分數不可比。

runner 對 transient HTTP／連線錯誤最多重試三次；401／403 立即停止。每題完成後立即寫
checkpoint。輸出兩份 round JSON 與一份 analysis JSON。

## 判讀

| 訊號 | 分類 | 下一步 |
|---|---|---|
| 題庫來源不存在、行號越界、expected 空白 | data | 修 final suite 或上游文件 |
| grounded=true，但未引用期待文件 | retrieval | 跑 retrieval probe，再修 chunk/index |
| 引用正確，但缺少／答錯規則 | model_answer | 檢查 prompt／模型；不要改真值討分 |
| 正例婉拒，答案卻引用期待文件 | model_refusal | 證據已到模型，處理生成端 |
| 正例婉拒且無可見引用 | undetermined | retrieval probe 才能分檢索或模型 |
| must_not_infer 卻 grounded=true | unsafe_inference | 安全 gate 失敗，優先處理並停止宣告全綠 |
| 兩輪同題 pass/fail 翻轉 | unstable | 報告波動，不以較高分那輪取代 |

安全與理由品質分開報告：`safety_ok` 只看 `grounded=false`，`reason_ok` 看是否說明未定義／待釐清。
任何 `grounded=true` 或 transport skipped 都使安全 gate 失敗；理由品質另外列出，不得用總分掩蓋。

## 最終報告

分開列出：

- verified：題數、證據狀態分布、兩輪分類分數、安全負例、source hit、穩定度
- unrun：retrieval probe、人工／產品核准、未重跑的上游 gate
- blockers：HTTP、token、模型 quota、服務版本等外部阻塞

報告必須附上 analysis JSON 的 `metadata`（`chat_runtime` 含 prompt 指紋、
`source_fingerprint`、`suite_fingerprint`）。缺任一項的 metrics 只能算歷史紀錄，
不能用來支持「某個修改有效／無效」的結論。

eval 全綠只代表 KB 能依現有證據回答，不代表 `observed_reference` 已成為核准需求。

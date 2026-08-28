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
6. 活服務是最新 binary，token 能讀目標 team
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

## 階段三：實跑兩輪

前置沿用 `ui-kb-validate`：服務已重新編譯／重啟、merged corpus 已 import/index、
`KB_EVAL_TOKEN` 是可讀 Store.POS 的驗收 token。不要在本 skill 執行 `make import`。

```powershell
$env:KB_EVAL_TOKEN = "kb_..."
python -u .claude/skills/ui-kb-requirement-validate/templates/run_requirement_eval.py `
  --suite .\testdata\store_pos_requirement_eval.json `
  --rounds 2 --rpm 10 --prefix 2026-08-26-requirement-35q-v2 --out-dir .\metrics
```

題庫或判分契約調整後必須使用新的 `--prefix`，保留舊 metrics 作為歷史基準。

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

eval 全綠只代表 KB 能依現有證據回答，不代表 `observed_reference` 已成為核准需求。

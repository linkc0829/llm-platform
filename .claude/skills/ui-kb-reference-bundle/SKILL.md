---
name: ui-kb-reference-bundle
description: Turn a directory of hand-written markdown (PM knowledge, UIUX notes, RFCs, onboarding docs) into a `reference` bundle the Go KB service accepts — metadata backfill and id generation by script, section restructuring and eval questions by judgement. Produces the bundle and stops there; merging it into a corpus and importing belong to ui-kb-merge-import, so never run kbimport or make import from here. Use when documents that were NOT produced by ui-kb-export need to enter the KB, when a source has no `kb/` bundle of its own, or when /index rejects a doc_type. Triggers "PM 文件進 KB", "手寫 md 轉 KB", "UIUX 文件進知識庫", "把 knowledge 轉成 bundle", "reference bundle", "新增 doc_type", "convert reference docs to kb", "hand-written docs into kb".
---

# 手寫文件 → `reference` bundle

`ui-kb-export` 吃的是 replay workspace,產出 `procedure` / `ui_inventory` /
`playlist`。**手寫文件沒有那個結構**:沒有 module、沒有畫面、沒有 trajectory,
front matter 是人自己寫的(而且常常是壞的),段落大小完全不受控。

這支 skill 走另一條路,把它們變成 `reference` bundle。

## 核心切分:腳本做轉換,你做判斷

專案 Rule 5 —— **「If code can answer, code answers」**。這裡剛好一半一半,
**分錯邊會出事**:

| 工作 | 誰做 | 分錯邊的後果 |
| :--- | :--- | :--- |
| 補 8 個必填欄位、產 id、寫 `kb_index.json` | **腳本** | 交給模型 → 偶爾手滑,而且不可重現 |
| 偵測壞 front matter、id 碰撞、零段落檔案 | **腳本** | 同上 |
| 量測段落大小 | **腳本** | 同上 |
| **決定過大段落怎麼切** | **你** | 寫死在腳本 → 表格切法套到散文上,產出垃圾 |
| 決定 `doc_type` 與 tier | **你** | 見下面「加新 doc_type」 |
| 寫 eval 題目 | **你** | 題目要貼著文件實際措辭 |

實測佐證:`_glossary.md` 是 166 列的術語表,切成「一術語一段」後 eval 7/7 全中。
同樣 26K 字若是散文,這個切法會產生垃圾。**必須看內容才能決定。**

## 流程

### 1. 先量,不要先寫

```bash
python templates/convert_reference_bundle.py --src <來源目錄> --report-only
```

它會印出檔案數、段數、front matter 警告,以及**超過 `--max-section-chars`
(預設 8000)的段落**。

實跑 pm-reference 的輸出:

```
22 files, 320 sections
  ! 04_次世代架構現況-個體說明.md: unterminated front matter, recovered at first h1

  1 section(s) over --max-section-chars=8000. ...
    _glossary.md    26529 chars  under '術語表'
```

**兩件事都必須處理過才往下走。**

### 2. 過大段落:先判斷內容形狀

段落大小有**兩個獨立理由**,只顧其一還是會出事:

- **配額** —— 26.5K 字 ≈ 8,800 tokens,單一段落就吃掉每分鐘 16,000 token 配額的一半以上
- **檢索品質** —— 26K 字的段落永遠是差的檢索單位,不是命中太多就是命中不準

切法**取決於內容**:

| 形狀 | 切法 | 為什麼 |
| :--- | :--- | :--- |
| 表格 | 一列一段,heading 帶主鍵(`## 術語:<term>(<en>)`) | 表格本來就是逐列被查的 |
| 散文 | 補中間層標題,依語意分段 | 機械式切成「第 N 組」的 heading 沒有檢索價值 |
| 清單 | 依語意分組 | 同上 |

**切好的檔案放進 `--overrides` 目錄**,檔名與來源相同:

```
pm-reference/
  knowledge/_glossary.md      ← 來源(不動)
  overrides/_glossary.md      ← 你切好的版本
```

腳本看到 override 就用它取代來源。**沒有這一步,下次重跑腳本會把你的手工切割
默默蓋掉。**

### 3. 正式產出

```bash
python templates/convert_reference_bundle.py \
  --src C:\Protech\pm-reference\knowledge \
  --out C:\Protech\pm-reference\kb \
  --overrides C:\Protech\pm-reference\overrides \
  --team Store.POS --product POS --owner pm-reference --id-prefix pm
```

**`--out` 要放在來源目錄旁邊,不是暫存區。** `admin-replay/` 與 `wpf-replay/`
各自有 `kb/`;新來源也該有,否則轉換產物與 eval 題目都沒有家,下次
`make import` 就被清掉。

### 4. eval 題目 —— 一定要放進 bundle

寫進 `<out>/eval/<AREA>/<name>-eval.yaml`。格式:

```yaml
module_code: "pm_reference"
questions:
  - question: "菜單體系怎麼設計的?"
    expect_source_id: "Store.POS--pm-07-294f1c5a"
    expect_identifiers: []
    must_not_infer: false
```

出題三個原則(這輪兩題失敗都是違反第 1 條):

1. **貼著文件實際措辭問。** 「折扣中心 IDE 想解決什麼問題?」失敗,因為文件寫的是
   「舊折扣模組被固定條件框架鎖死」;改問「舊的折扣模組有什麼限制」就過了。
2. **確認答案只有一個合理來源。** 「組合商品和一般商品有什麼不同?」失敗,因為
   術語表也定義了這兩個詞 —— 模型從術語表回答,完全合理。改問「組合商品為什麼
   還沒開放?」(只有 pm-08 寫了)就過了。
3. **每種切法都要有題目守著。** 那 166 段術語如果沒有 7 題 glossary 題,
   切法對不對永遠不會有人知道。

至少補 1–2 題 `must_not_infer: true`,問文件明確沒寫的東西 —— 它同時守住
「手寫文件不得夾帶工程細節」這個契約。

### 5. 交棒 —— **這支 skill 到此為止**

bundle 產出後就結束了。**不要在這裡執行合併或 `make import`。**

| 接下來 | 交給 |
| :--- | :--- |
| 與其他來源合併成 staging、`kbimport -check`、`make import`、`POST /index` | `ui-kb-merge-import` |
| 客服問答驗收 | `ui-kb-validate` |
| 工程問答驗收(`search_kb` MCP) | `ui-kb-eng-validate` |

> **為什麼切在這裡:** `make import` 的 `replaceTeam` 會**整個換掉**
> `docs/<team>/` 與 `eval/<team>/`,而它們不受版控 —— 這是整條鏈上唯一救不回來的
> 一步。它只能出現在一個地方,而那個地方是 `ui-kb-merge-import`(它會處理備份、
> `modules/` 併入、`eng_eval.yaml` 串接與 baseline)。
>
> 舊版這裡寫著「與其他來源一起併進 staging 後」再接兩行 import 指令,卻沒說**怎麼併**。
> 那等於要求臨場發揮合併,而漏掉 `modules/`(圖片全滅)或根層的 `eng_eval.yaml`
> (52 題無聲蒸發)正是實際踩過的兩個坑。合併現在有腳本了,不要重新發明。

---

## 四個安靜的坑

全部實際踩過。每一個的失敗模式都**不會報錯**。

### 1. `kb_index.json` 要在 **bundle 根層**,不能放進 `eval/`

`validateEvalIndex` 讀死 `filepath.Join(eval, "kb_index.json")`
(`cmd/kbimport/main.go:204`)—— 只看根層,不會往下找。合併時 bundle 根層會變成
staging 的 `FROM` 根層,所以放錯層在這裡看不出來,要到 import 才失敗。

eval YAML 反而放哪一層都行,`validateEvalIndex` 是 `WalkDir` 找 `*-eval.yaml`。

### 2. eval YAML 必須寫進 bundle,不能只放在 `eval/<team>/`

`replaceTeam`(`main.go:94`)**整個換掉** `eval/<team>/`。只存在於那裡、不在任何
bundle 裡的題目,下次 import 就消失 —— 而且是無聲的,eval 只會變少不會報錯。

> 這條的另一半(合併時漏掉 `kb/` **根層**的 `eng_eval.yaml`,差點蒸發 52 題)屬於
> `ui-kb-merge-import`。**這支 skill 的責任只到「題目在我產出的 bundle 裡」。**

### 3. 段落大小有兩個獨立理由

見上面步驟 2。只解決配額而不管檢索品質,eval 會過不了但看起來像模型爛。

### 4. 加新 `doc_type` 是安全契約,不是慣例

`ClassifySection`(`internal/kb/domain.go`)的 switch **每個分支的語意不同**:

```go
case "ui_inventory", "playlist", "reference":
    if headingRestricted {
        return SectionTierInvalid, ...   // 工程標題 = 錯誤
    }
    return SectionTierPublic, nil
case "procedure":
    if headingRestricted {
        return SectionTierRestricted, nil // 工程標題 = 降級
    }
```

加新值前**先回答一個問題:這個 doc_type 有沒有 restricted 層?**

`procedure` 有,是因為 exporter 把操作步驟與工程對應寫進**同一份檔**,必須在段落層
分流。**手寫文件沒有這個結構** —— 出現「工程對應」標題代表分類放錯了,應該讓
`/index` 失敗、由人決定要搬去 `procedure` 還是改標題。

> **照抄 `procedure` 分支 = 默默開一個工程內容洩漏管道。** 客服 token 會讀到
> 本該限工程看的內容,而且沒有任何錯誤訊息。

加值時**同時**補三個 `domain_test.go` 測試:

| case | 期望 |
| :--- | :--- |
| 普通標題 | `SectionTierPublic`, nil |
| 工程標題 | `ErrInvalidSectionAccess`(若走 public-only 分支) |
| 普通標題 + body 有 `VERB /path` | `ErrSectionAccessDrift`,**不是** public |

第三條最重要:單向越界偵測(`containsEngineeringEvidence`)跑在 switch **之前**,
是唯一擋住手寫文件夾帶端點的機制,加值時很容易不小心繞過。

---

## 腳本會拒絕什麼(fail loud,不猜)

| 情況 | 行為 |
| :--- | :--- |
| front matter 開了 `---` 但沒收尾 | 用第一個 h1 當邊界,**印出警告** |
| 同上,但連 h1 都沒有 | **`sys.exit`** —— 不猜邊界 |
| 檔案沒有任何標題(會 parse 成 0 段) | **`sys.exit`** —— `kbimport` 本來就會拒收 |
| id 與既有 bundle 撞號 | **`sys.exit`** |

第一條是實際抓到的 bug:某份來源檔的 fence 沒收尾、第一欄寫成 `## date:`,
早期版本**靜默通過**,結果 transcript 路徑、related 清單全被當成知識索引進去,
還多出一個假的 `## date: 2026-08-15` section。修掉之後語料少了 1 段。

## 為什麼 id 要帶雜湊

中文檔名 slug 化後幾乎不剩東西 —— `07_次世代架構現況-菜單體系` 只剩 `07`,
一整個目錄會塌成少數幾個 stem。所以 id 是
`<team>--<prefix>-<序號>-<檔名 sha256 前 8 碼>`:序號給人看,唯一性靠雜湊。

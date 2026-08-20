---
name: ui-kb-staging-merge
description: Merge several per-source KB bundles (admin-replay / wpf-replay / pm-reference) into one staging tree and carry it through to a live corpus — copies `modules/` beside `kb/`, concatenates `eng_eval.yaml`, merges `kb_index.json` with collision checks, then `kbimport -check`, backup, `make import`, `POST /index`, and the baseline update. This is the ONLY place `make import` belongs: it replaces `docs/<team>/` and `eval/<team>/` wholesale and neither is version controlled. Use when a new KB source is added, when one source changed and the corpus must be rebuilt, or whenever someone is about to run kbimport or make import by hand. Triggers "合併 KB 來源", "併 staging", "匯入前的合併", "匯入 KB", "重新匯入 KB", "重建語料", "跑 kbimport", "make import", "更新 baseline", "merge kb sources", "staging tree", "新增第四份來源", "import kb bundle".
---

# 多來源 `kb/` → staging 樹

匯入鏈上的第三段。前兩段各自把一份來源變成 `kb/` bundle,這一段把它們併成
`kbimport` 吃得下的**一棵**樹:

| 階段 | 工具 |
| :--- | :--- |
| replay workspace → `kb/` | `ui-kb-export` + `export_kb.py` |
| 手寫 md → `kb/` | `ui-kb-reference-import` + `convert_reference_bundle.py` |
| **多份 `kb/` → staging** | **這支** |
| staging → `docs/` | `cmd/kbimport` |

看起來只是 `cp -r`,但有八個安靜的失敗模式,其中兩個已經真的踩過。

## 這一段幾乎全是決定性的

專案 Rule 5 ——「If code can answer, code answers」。和 `ui-kb-reference-import`
剛好相反:那支一半靠判斷,這支**幾乎沒有判斷**,所以腳本重、這份文件輕。

| 工作 | 誰做 |
| :--- | :--- |
| 複製 `modules/`、白名單複製 `kb/` 子目錄、串接 `eng_eval.yaml`、合併 `kb_index.json` | 腳本 |
| 所有碰撞 / team 不一致 / 檔案缺漏的偵測 | 腳本 |
| 決定「這批來源該不該併在一起」 | 你 |
| 看 `-check` 失敗訊息判斷是修來源還是修合併 | 你 |

腳本**不跑** `kbimport -check`,只在結尾印出該跑的那行 —— check 會在 `docs/` 旁建
暫存目錄,不該藏在合併腳本裡。

## 要合併哪些來源:`kb_sources.json`

repo 根目錄,受版控:

```json
{
  "team": "Store.POS",
  "sources": [
    { "name": "admin-replay", "root": "C:/Protech/admin-replay" },
    { "name": "wpf-replay",   "root": "C:/Protech/wpf-replay" },
    { "name": "pm-reference", "root": "C:/Protech/pm-reference" }
  ]
}
```

`root` 是**來源根目錄**(內含 `kb/`,可選 `modules/`),**不是 `kb/` 本身** ——
`modules/` 是 `kb/` 的兄弟,腳本兩個都要看得到。指錯一層腳本會告訴你。

**加第四份來源 = 在這裡加一列**,指令不變。這份清單該進版控的理由:「這個 team 的
語料由哪幾份來源組成」是會被 review 的事實,不是暫存狀態。而且在此之前,這份清單
只存在人的記憶裡。

用**正斜線**。JSON 裡的反斜線要寫成 `\\`,而寫成單一個 `"C:\temp\x"` 時 `\t` 是
**合法的 JSON 跳脫**,會靜默變成 tab 字元 —— 所以腳本的錯誤訊息一律印 `repr`。
相對路徑一律相對於 **manifest 所在目錄**,不是 cwd。

## 流程

### 1. 先看一眼

```bash
python .claude/skills/ui-kb-staging-merge/templates/merge_kb_sources.py --report-only
```

跑完所有檢查但不寫檔:

```
3 sources -> (report only, nothing written)
  admin-replay    113 docs   modules: ADMIN      C:\Protech\admin-replay
  wpf-replay       41 docs   modules: POS        C:\Protech\wpf-replay
  pm-reference     22 docs   modules: -          C:\Protech\pm-reference
176 docs, 203 files, 0 collisions, team Store.POS
```

**逐列核對 doc 數與絕對路徑。** 這是唯一能抓到「指到同一份來源的舊複本」的機會 ——
路徑合法、內容自洽,腳本看不出來,只有你記得那份應該是 41 份不是 38 份。

### 2. 產出

```bash
python .claude/skills/ui-kb-staging-merge/templates/merge_kb_sources.py --out C:/tmp/staging
```

`--out` 必須是空的或不存在(`--force` 才會先砍掉重建)。**每次合併都用一棵全新的樹。**

一次性合併不想動 manifest 時:`--source <root> --source <root> --team <TEAM>`。

### 3. check

```bash
go run ./cmd/kbimport -team Store.POS -from C:/tmp/staging/kb -check
```

**exit 0 才往下走。** 這一步不會替換 `docs/` 的內容(它只在 `docs/` 旁建暫存目錄再丟掉)。

### 4. 備份 —— import **之前**,不是之後

`docs/`、`eval/` 在 `.gitignore:47-48`,`.kb/` 也不受版控。**`make import` 無法用
git 還原**,而 `replaceTeam`(`cmd/kbimport/main.go:94`)是整個目錄換掉,不是合併。

```powershell
$bak = "C:\Protech\_kb-backup-$(Get-Date -Format yyyyMMdd-HHmmss)"
New-Item -ItemType Directory -Force $bak | Out-Null
Copy-Item -Recurse docs, eval, .kb $bak
```

還原:刪掉 `docs`/`eval`/`.kb`,複製回來,重啟服務(不必重跑 `/index`)。

### 5. import

```bash
make import TEAM=Store.POS FROM=C:/tmp/staging/kb
```

### 6. 重建索引

`POST /index`。只有 body 變動的段落會重新 embed —— 向量快取以 body 的 sha256 為 key,
所以改檔名、改標題、調段落順序都是免費的;被刪掉的文件其向量也會一併消失
(`embedSections` 是從當前語料重建 map,不是往舊快取追加)。

想強制全量重算就先刪 `.kb/faiss_index/vectors.bin`。實測 1395 段:全量 54s、全命中 2.1s。

### 7. 更新 baseline,跑 eval

baseline 的 fingerprint **一定要取 import 之後的**。import 會把圖片引用改寫成
`_assets/...`,而 **body 正是 fingerprint 的輸入**,所以 staging 乾跑的值與 `docs/`
的值**必然不同**(實例:staging `5116dc07…` vs `docs/` `7ed4223f…`)。段數與分類
分布兩邊相同,只有 fingerprint 不同 —— 貼錯 baseline 永遠紅。

```powershell
$env:KB_BASELINE_DOCS = (Resolve-Path .\docs).Path
try { go test ./internal/kb -run TestCurrentCorpusClassificationBaseline -v }
finally { Remove-Item Env:KB_BASELINE_DOCS -ErrorAction SilentlyContinue }
```

**`try/finally` 不是講究。** 環境變數留在 session 裡會讓後續 `go test ./...`
**靜默跳過** baseline 斷言而假綠。

問答驗收交給 `ui-kb-validate`(客服)與 `ui-kb-eng-validate`(工程 `search_kb`);
後者的前提是前者已經過關。

---

## 只更新一份來源時:**還是要全部重併,沒有例外**

這不是取捨,是 `kbimport` 的行為決定的:

- `replaceTeam`(`cmd/kbimport/main.go:94`)把 `docs/<team>/` 與 `eval/<team>/`
  **整個換成** `-from` 的內容。沒有增量模式。
- `validateEvalIndex`(`main.go:204`)要求 `kb_index.json` 涵蓋 staged 檔案的
  **恰好全集**(每個 id `count == 1`)。

所以 `-from` 只餵一份來源 = **把另外兩份從 `docs/` 刪掉**,而且 **`-check` 會全綠** ——
它驗的是「這棵樹自洽」,不是「這棵樹完整」。

```
1. 只重跑那一份的產出     ui-kb-export / ui-kb-reference-import
2. 合併全部              merge_kb_sources.py        ← 一定要全部
3. kbimport -check
4. 備份 docs/ eval/ .kb/  ← 下一步不可逆
5. make import
6. POST /index           只有變動的段落重新 embed
7. baseline + eval
```

代價其實很低。貴的是 embedding 不是合併:上一輪 1395 段裡 674 段命中內容定址向量
快取,整個 `/index` 只花 35.8s。

---

## 八個安靜的坑

### 1. `modules/` 必須是 `kb/` 的兄弟

`stageBundle` 設 `bundleRoot := filepath.Dir(from)`,`stageMarkdown` 再用
`within(bundleRoot, asset)` 檢查每個圖片(`cmd/kbimport/main.go`)。
`-from staging/kb` → `bundleRoot = staging`,所以 md 裡的
`../../../modules/ADMIN/...` 只有在 `staging/modules/ADMIN/...` 存在時才過。

只併 `kb/` 的下場(實測):

```
kbimport: .../procedures/ADMIN/account_permission-procedure.md:
  image "../../../modules/ADMIN/.../S01-account_list.jpg": ... cannot find the path
```

這個坑會**大聲**失敗,是八個裡最友善的一個。

### 2. `eng_eval.yaml` 在 `kb/` **根層**,不在子目錄

只跑「複製子目錄」的迴圈會漏掉它,而 `replaceTeam` 會整個換掉 `eval/<team>/` ——
**78 題工程 eval 直接消失,沒有任何錯誤訊息**。上一輪差一步就發生。

它是 top-level YAML list,所以三份是**串接**不是覆蓋。腳本只在前一份沒有結尾換行時
才補一個,否則兩份的清單項目會黏在一起。

### 3. `kb_index.json` 必須在 `FROM` 根層,且與實際檔案**恰好一一對應**

`validateEvalIndex` 讀死 `filepath.Join(eval, "kb_index.json")`,然後要求每個
staged id `count == 1`、每列 `row.Team == -team`。「檔案複製了但 index 沒併」和
「index 併了但檔案沒複製」都會被擋,**但訊息不會說是哪一邊少了**。腳本把同樣的檢查
提前到還知道是哪個來源出問題的地方做。

### 4. 一棵 staging = 一個 team

承上,`row.Team != team` 直接 fail。兩個 team 就跑兩次合併、兩次 import。

### 5. `kb/` 裡有一堆 eval **輸出**檔

`admin-replay/kb/` 有 11 個 `eng_eval_out*.json`、`wpf-replay/kb/` 有 8 個。
`stageBundle` 只認 `.md` / `.yaml` / `kb_index.json`,所以現在不會炸 —— 但這是巧合。
腳本用白名單(`KB_SUBDIRS`),不整包複製。

### 6. eval 子目錄同名檔會靜默互蓋

目前三份剛好是 `ADMIN/` `POS/` `PM/` 完全不重疊,**純屬運氣**。第四份來源若也放
`eval/POS/ordering-eval.yaml`,`cp -r` 直接蓋掉、題目變少而 `-check` 全綠。
腳本逐檔記錄 owner:

```
FATAL wpf-replay and fake both provide kb/eval/POS/void-eval.yaml
```

### 7. 少併一份來源比併錯更危險

**腳本永遠不跳過 manifest 裡的來源。** 少一份會產出一棵完全自洽的樹、`-check` 全綠,
然後 `replaceTeam` 把那 113 份從 `docs/` 刪掉。所以「找不到就略過」是禁止的:
路徑不存在、沒有 `kb/`、沒有 `kb_index.json`、兩列指到同一個 root —— 全部 `sys.exit`。

打錯字指到**另一份有效來源**則由 id 唯一性擋下(同一份併兩次 → id 全撞)。

### 8. Windows MAX_PATH

`modules/` 很深(`module/output/module/img/FLOW/shot.jpg`),`--out` 只要稍長就會讓
每個複製動作噴 `WinError 3`。腳本對所有寫入目的地加 `\\?\` 前綴繞過 260 字元上限
(`long_path()`),`--force` 的 `rmtree` 也要 —— 否則刪不掉自己剛建的樹。

`docs/` 與 `eval/` 在 `.gitignore`,**import 無法用 git 還原**。動手前備份
`docs/`、`eval/`、`.kb/`。

---

## 驗證過的行為

- 對現行三份來源:**176 docs**(113 + 41 + 22)、203 files、0 collisions
- 產出與上一輪手動 bash 合併的結果 **`diff -r` 為空**
- `kbimport -team Store.POS -from <out>/kb -check` 綠
- 反向測試全部 `sys.exit`:root 不存在、指到 `kb/` 本身、重複 root、team 不符、
  eval 檔名碰撞、`--out` 非空
- 反向測試坑 #1:移走 `modules/` 後 `-check` 失敗並指名該圖片

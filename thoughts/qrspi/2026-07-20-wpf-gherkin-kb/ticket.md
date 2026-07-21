https://app.notion.com/p/knowledge-base-qa-bot-WPF-Gherkin-KB-3a38b5e3718e81fcaf9ee63d49745ae8

# 🔧 改造計畫:knowledge-base-qa-bot 承載 WPF Gherkin KB

> **TL;DR** — 既有 Go side project `knowledge-base-qa-bot` **可直接沿用、不需重寫**(已有 Markdown 索引、BM25、向量、citation、session、HTTP API、21 個測試、ports/adapters 架構)。但有 **7 個阻斷性缺陷**,其中 **CJK 斷詞全毀** 是致命的 —— 目前狀態下 **每一個中文問句都會被拒答**。

專案位置:`C:\Users\ken2_lin\Documents\go projects\system-design\knowledge-base-qa-bot`(Go,~2,228 LOC,hexagonal)

---

## 0. 背景

可行性評估的 **P2–P4** 需要:Markdown KB → embedding + hybrid 檢索 → 有引用的問答 → 供 coding agent 調用。

既有專案已具備:Markdown 索引、BM25、cosine 向量、citation grounding、多輪 session、HTTP `/index` `/chat`、fake 模式。`LLM`/`Embedder`/`VectorStore` 皆為 interface(`internal/kb/ports.go`),**換模型不動核心**。

---

## 1. 已驗證的缺陷(實跑確認,非推測)

| # | 問題 | 證據 | 後果 |
|:---|:---|:---|:---|
| 1 | **CJK 斷詞全毀** | `tokenize("動態密碼怎麼登入") == []` | BM25 得 0 → 低於 `minThreshold` → `deny()`。**每個中文問句都被拒答**,且 BM25 是總閘門,**向量路徑永遠跑不到** |
| 2 | slugify 丟棄中文 | `slugify("Scenario: 登入 完整操作劇本") == "scenario--"` | 各 flow anchor 撞名,citation 無法區分 |
| 3 | 非真 hybrid | `service.go:106-124` BM25 優先、向量備援(二選一) | 語意題與識別碼題無法各取所長 |
| 4 | glob 非遞迴 | `filepath.Glob(docsDir,"*.md")` | 找不到 `output/<NN>_<Area>/*.md` |
| 5 | frontmatter 被丟 | 首個 heading 前的內容一律丟棄 | `PageCode/FlowName/Type/Payload` metadata 全失 |
| 6 | 圖片錨點未帶出 | Section 無 image 欄位 | 答案無法附截圖 |
| 7 | embed model 常數重複 | `service.go:19` 與 adapter 各一份 | 向量被蓋錯模型名(現存 fake 4 維向量被標成 OpenAI 模型) |

### 已定決策

- **中文斷詞:字元 bigram** —— 純 stdlib、無外部相依(符合 depguard 對 `domain.go` 的限制)、無字典維護、不引入中國製斷詞庫。
- **模型落點:本地 Ollama** —— embedding 與生成都走本地,KB 全文不出境。

---

## 2. Phase 1 — CJK 正確性(解除阻斷)

**`internal/kb/domain.go`**

1. 重寫 `tokenize`(`domain.go:48`):逐 rune 掃描
	- ASCII 英數連續段 → 整詞(小寫),保留 `LoginViewModel` 這類識別碼
	- CJK 連續段 → **字元 bigram**(`動態密碼` → `動態`,`態密`,`密碼`);長度 1 的段取單字
	- 其餘(標點/空白)當分隔
	- 僅用 `strings`/`unicode`/`sort`,**不得新增外部 import**
2. 修 `slugify`(`domain.go:39`):保留 CJK rune 與英數,其餘轉 `-` 並收斂連續 `-`。
3. **索引版本戳(連帶必做)**:換 tokenizer 會讓 `.kb/index.json` 內既算好的 `doc_freq`/`doc_len`/`avg_len` **全部失效**。加 `tokenizer_version`,`LoadOnStartup` 發現版本不符就**拒用並要求重跑 `/index`**。
4. 測試比照既有 table-driven + `t.Run` 風格。

> ⚠️ 此 Phase 完成前中文問答一律不 work,必須先做。

---

## 3. Phase 2 — 真 hybrid(RRF 融合)【核心】

### 3.1 為什麼用 RRF,不用分數加權

BM25 分數**無上界且隨語料浮動**,cosine 落在 `[-1,1]` —— 尺度不可比。min-max 正規化會隨 query 漂移、還要調權重。

**RRF 只看名次、不看分數**,免正規化免調參,是 Elasticsearch/OpenSearch 的預設 hybrid 做法。

```javascript
RRF_score(d) = Σ_over_each_list  1 / (K + rank_d)     // K = 60,rank 從 1 起算
```

- 文件只出現在其中一個清單 → 另一清單不貢獻
- 代價:**丟失分數強度** → 用 3.4 的信心閘門補回

### 3.2 候選深度:fetch-K 與 top-K 要分開(關鍵)

現況 `topK = 3`。**若只拿各自前 3 名去融合,RRF 幾乎沒有作用空間**。

```javascript
candidateK = 20   // 每個檢索器各取前 20 名進入融合(新增)
topK       = 3    // 融合後最終餵給 LLM 的段落數(維持)
```

### 3.3 `domain.go` 新增(純函式,無外部相依)

```go
// 與 RankBM25 對稱:回傳依 cosine 由高到低排序的 ScoredSection
func RankVector(indexed []Section, vecMap map[string][]float32, q []float32, limit int) []ScoredSection

// RRF 融合任意份已排序清單
func FuseRRF(lists [][]ScoredSection, rrfK int) []ScoredSection
```

- `RankVector` 複用既有 `Cosine`(`domain.go:155`);以 `Section.Citation()`(`file#anchor`)當 key
- `FuseRRF` 以 section index 為 key 累加;**`sort.SliceStable` + 同分時以 index 遞增破平**,確保測試可重現

### 3.4 `Chat` 改寫(`service.go:85`)

**移除「BM25 未達 minThreshold 就 deny」的前置閘門** —— 這是中文被拒答的第二層原因。

```javascript
1. contextualQuery := composeQuery(history, query)
2. bm25List := corpus.RankBM25(tokenize(contextualQuery))  // 取前 candidateK
3. 若 embedder 可用 且 vecMap 非空 且 模型戳記相符:
       vecList = RankVector(indexed, vecMap, embed(q), candidateK)
4. 兩者皆有 → FuseRRF([bm25List, vecList], 60),strategy="hybrid"
   只有 BM25 → bm25List,strategy="markdown"
5. 信心閘門未過 → deny()
6. topSections(indexed, fused, topK) → answerFrom(...)
```

### 3.5 信心閘門(門檻怎麼改)

RRF 分數很小(兩清單皆 rank 1 也才 `2/61 ≈ 0.0328`)且**與舊門檻不可比**。採 **per-retriever 信心閘門**:

```javascript
deny  當且僅當  (bm25 最高分 < minThreshold)  AND  (最佳 cosine < cosineMin)
```

- `minThreshold` 沿用;`cosineMin` 新增,起始 `0.30`,**用評測集校準**
- 效果:中文問句 BM25 可能仍低,但只要向量夠像就答得出來
- `strongThreshold` 在新流程不再需要

### 3.6 降級與失敗處理

| 情況 | 行為 |
|:---|:---|
| `embedder == nil` / `vecMap` 空 | BM25-only,`strategy="markdown"`(向後相容) |
| 向量索引模型戳記 ≠ 目前設定 | **視同無向量** • warn,要求重跑 `/index`;**絕不混用** |
| `embedder.Embed` 失敗(Ollama 掛了) | **降級為 BM25-only + warn**,不要整個請求 500 |

### 3.7 為什麼這對這個 KB 特別有效

flow `.md` 依 heading 切成兩軸:

- **`### Scenario:` 敘事 chunk** → 「動態密碼怎麼登入」這種**語意問句**由**向量**命中
- **`### Engineering Context` chunk** → `LoginViewModel`、`GetTokenNow` 這種**識別碼**由 **BM25** 命中

單走任一路都會漏掉另一半;RRF 讓兩者**同時進 top-K**,答案能同時給「操作步驟 + 對應 API + 截圖」。

### 3.8 Reranker(納入,但可開關)

融合後、取 `topK` 前插入:`FuseRRF` 取前 `rerankK`(=20) → rerank → 取 `topK`(=3)。

`internal/kb/ports.go` 新增 port:

```go
type Reranker interface {
    Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}
```

- **`nil` 時整段跳過**(向後相容);由 config `KB_RERANK_MODE`(`off`/`http`)決定是否注入

**⚠️ 基礎設施成本:Ollama 不服務 cross-encoder reranker**(它是 chat/embedding 導向)。要真跑 rerank 只能:

| 方式 | 說明 | 代價 |
|:---|:---|:---|
| **HTTP sidecar(建議)** | TEI 或 Python `sentence-transformers` 小服務,載 `mxbai-rerank-large`(中歐)或 `jina-reranker-v2` | **多養一個服務** |
| LLM-as-reranker | 用本地 chat 模型批次打分 | 零新服務,但**慢且品質較低** |
| 雲端 rerank API | Jina / Cohere | **chunk 會出境**,違反機密邊界 |

**⚠️ 規模提醒(誠實評估)**:目前 9 flows ≈ 20–40 chunks,而 `candidateK=20` **幾乎等於整個語料** —— 此規模下 rerank **效果有限**,它真正發揮是在 KB 長到數百條 flow、候選集遠大於 topK 時。

→ 因此:**實作 port + HTTP adapter,但 PoC 預設 `KB_RERANK_MODE=off`**。管線先打通、介面先備好,等 KB 長大再開。驗收時分別測「開/關」兩種,記錄差異當日後基準。

### 3.9 測試

- `FuseRRF`:已知名次清單 → 驗算期望順序;單一清單文件;同分破平確定性
- **回歸測試(對應原始 bug)**:純中文 query 且 BM25 全 0 → **仍能經向量取回段落**,不再 `deny`
- 英文識別碼 query → BM25 主導,結果含 Engineering Context chunk
- `embedder == nil` → `strategy == "markdown"`,行為同舊版
- 需更新既有 `TestServiceChatWeakScoreUsesVectorRetrieval`

---

## 4. Phase 3 — 吃得下真實 KB

1. `filepath.Glob` → `filepath.WalkDir` 遞迴;Section 的 `file` 用**相對 docsDir 路徑**,確保跨資料夾 citation 唯一
2. 解析檔首 `---` frontmatter → `map[string]string`,掛到該檔所有 Section
3. `Section` 增 `meta map[string]string` 與 `images []string`;images 以 `\*\(Image:\s*([^)]+\.jpg)\)\*` 擷取
4. `dto_internal.go` 索引 JSON 持久化新欄位;`rehydrateSection` 同步
5. 過濾空 body Section(現況 H1 緊接 H2 會產生噪音)

> 切塊沿用既有「依 heading 切」即可 —— 剛好切出 3.7 說的兩軸。

## 5. Phase 4 — 接本地 Ollama

1. **不新增 adapter**:`openai-go` 支援 `option.WithBaseURL`,且 `ChatModel`/`EmbeddingModel` 是 string 型別 → 指向 `http://localhost:11434/v1` 即可
2. config 新增 `OPENAI_BASE_URL`、`KB_CHAT_MODEL`、`KB_EMBED_MODEL`、`KB_DOCS_DIR`、`KB_INDEX_DIR`;放寬 APIKey 必填;修 `!= "fake"` 與 `EqualFold` 不一致
3. **修 model stamp 漂移**:移除 `service.go:19` 常數改用設定值;載入時比對戳記
4. `cmd/kb/main.go:32` 的 `NewMarkdownRepo("docs", ".kb")` 改讀設定

## 6. Phase 5 — 驗證

1. **單元測試**(stdlib + table-driven,無 testify);`make verify` 須過,注意 depguard 相依限制
2. **端對端**:9 條 flow bundle 複製到 docs → `ollama pull` → `make run` → `POST /index` → `POST /chat` 跑 5 題(3 中文 how-to + 2 英文識別碼)。驗收:答案正確、`sources` 對、`strategy == "hybrid"`、能取回截圖檔名
3. **rerank 開/關各跑一次**,記錄差異(當前規模預期差異不大 —— 這正是要驗證的,也是日後 KB 長大後的比較基準)
4. **檢索評測集(重要)**:20–30 題 → 標註正確 chunk,量測命中率。**這是校準 `cosineMin`、判斷非中國 embedder 對繁中夠不夠用的唯一客觀方法**

---

## 7. 後續(不在本次範圍)

- **MCP**:檢索包成 `search_kb` MCP server 供 Claude Code / Cline 調用
- **Terraform**:專案目前**無 Dockerfile、無 CI**,上 Fargate 需容器化;另 `Makefile` 的 `build`/`clean` 用 cmd.exe 語法,Linux CI 會失敗
- 規模化後把 `.kb/faiss_index/metadata.json`(其實無 FAISS,純 JSON + 暴力 cosine)換 pgvector —— `VectorStore` port 已備好

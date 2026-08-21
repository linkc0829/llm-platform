# KB QA Service — API 與 Token 手冊

服務預設監聽 `http://localhost:12598`(`APP_PORT`)。所有回應皆為 JSON。

> 這份文件描述的是 `cmd/kb` 這支 HTTP 服務。批次匯入(`cmd/kbimport`)、
> token 引導(`cmd/kbtoken`)、stdio MCP(`cmd/kbmcp`)是**獨立的本機程式**,
> 不走 HTTP,也不受本文件的認證規則約束。

---

## 一、端點總覽

| Method | Path | 認證 | 需要 capability | 用途 |
| :--- | :--- | :--- | :--- | :--- |
| `GET` | `/health` | **不需要** | — | 存活檢查 |
| `POST` | `/chat` | Bearer | — | 問答 |
| `POST` | `/index` | Bearer | **`indexer`** | 重建索引 |
| `GET` | `/admin/tokens` | Bearer | **`admin`** | 列出所有 principal(不含 token) |
| `POST` | `/admin/tokens` | Bearer | **`admin`** | 建立 token |
| `DELETE` | `/admin/tokens/:id` | Bearer | **`admin`** | 撤銷 token |
| `GET`/`POST`/`DELETE` | `/mcp` | Bearer | — | MCP Streamable HTTP(`search_kb` 工具) |

`/health` 是唯一不需要 token 的端點 —— 它在 guard 之前註冊(`internal/kb/routes.go:21`),
所以拿它判斷「服務活著」是可靠的,拿它判斷「我的 token 有效」則毫無意義。

---

## 二、認證機制

```
Authorization: Bearer kb_<43 個 base64url 字元>
```

| 性質 | 說明 |
| :--- | :--- |
| 格式 | `kb_` 前綴 + 32 bytes 隨機值(**256 bits 熵**),base64url 無 padding |
| 儲存 | 只存 **SHA-256 hex digest**,明文不落地。比對用 `subtle.ConstantTimeCompare` |
| **有效期** | **沒有。** token 永久有效,撤銷是唯一的失效方式 |
| 來源 | `auth.json`(路徑由 `KB_AUTH_FILE` 指定),啟動時整份讀進記憶體 |
| 上限 | 1000 個 principal;auth 檔 ≤ 1 MB |

<!-- -->

> **服務會 fail closed。** `KB_AUTH_FILE` 沒設、檔案不存在、格式壞掉、或
> **principal 數為 0**,服務**直接不啟動**(`cmd/kb/main.go:39`)——
> 不會退化成「先開起來再說」。

**沒帶 token 的失敗長什麼樣:** `401` + `{"error":"unauthorized"}`,而且**不會**有
`WWW-Authenticate` 挑戰(本服務不實作 OAuth 探索流程)。批次腳本忘了帶 token 時,
console 只會刷一排 401,不會有任何提示告訴你原因 —— 這是實際踩過的坑。

---

## 三、Token 類型

權限是**兩個正交維度 + 兩個獨立 capability**,不是一組固定角色:

| 欄位 | 型別 | 控制什麼 | 預設 |
| :--- | :--- | :--- | :--- |
| `teams` | `[]string` | 看得到哪些 team 的文件 | `[]`(什麼都看不到) |
| `all_teams` | `bool` | 略過 team 檢查,看得到全部 | `false` |
| `engineering` | `bool` | 看不看得到 **restricted** 段落(API、ViewModel、呼叫鏈) | `false` |
| `indexer` | `bool` | 可否呼叫 `POST /index` | `false` |
| `admin` | `bool` | 可否管理 token | `false` |

### 四種實務組合

| 角色 | `all_teams` | `engineering` | `indexer` | `admin` | 給誰 |
| :--- | :---: | :---: | :---: | :---: | :--- |
| **support**(客服 / PM) | ✅ | ❌ | ❌ | ❌ | 前線問答;**驗收 KB 品質要用這一把** |
| **engineering** | ✅ | ✅ | ❌ | ❌ | 工程師;看得到「工程對應」段落 |
| **indexer** | ✅ | ❌ | ✅ | ❌ | CI / 匯入腳本,只負責重建索引 |
| **admin** | — | — | — | ✅ | 只管理 token,**不是超級使用者** |

### 三個容易搞錯的地方

**1. capability 之間互不隱含。**
`admin` 不含 `indexer`,`indexer` 不含 `engineering`。一把純 indexer token 可以成功
重建索引(200),但拿去問問題會得到 `grounded=false` 與 0 個 source —— 它有寫的權限,
沒有讀的權限。這是刻意設計,不是 bug。

**2. `teams` 與 `engineering` 是兩個獨立的關卡,兩個都要過。**
段落必須**同時**滿足「team 相符(或 `all_teams`)」與「tier 允許」才看得到
(`internal/kb/domain.go` 的 `CanSee`)。`engineering=true` 但 `teams` 是空的 →
一段都看不到。

**3. 量測 KB 品質時,token 選錯會量到假的東西。**

| 用哪把 token | 你實際量到的 |
| :--- | :--- |
| support(`engineering=false`) | **這才是正確的。** 操作題在分層過濾後仍答得出來 |
| engineering | 只證明資料還在,**繞過了分層**,測不到過濾有沒有做壞 |
| 不帶 | 全部 401 |

---

## 四、產生 Token

### 4.1 為什麼要兩階段

**網路 API 永遠鑄不出 admin。** `POST /admin/tokens` 的 handler **刻意不複製**
`admin` 欄位(`internal/auth/handler_http.go:56`)—— 你送 `"admin": true` 不會報錯,
會**靜默建出一把普通 token**。第一把 admin 只能由本機 CLI 產生。

```
kbtoken create-admin  ──►  admin token  ──►  POST /admin/tokens  ──►  其他所有 token
   (本機、服務需停止)                            (網路、服務需執行中)
```

### 4.2 第一步:用 CLI 造 admin(一次性)

```bash
go build -o bin/kbtoken.exe ./cmd/kbtoken
```

```bash
./bin/kbtoken.exe create-admin -name admin
```

輸出(**`token` 只會出現這一次,之後任何地方都拿不回來**):

```json
{"id":"p_xxxxxxxxxxxxxxxxxxxxxx","name":"admin","token":"kb_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}
```

| 限制 | 說明 |
| :--- | :--- |
| **服務必須停止** | CLI 會先打 `/health`,通了就拒絕執行;真正的併發保護是 auth lock file |
| `create-admin` 只能用在空的 store | auth 檔已有 principal 時會要求改用 `rotate-admin` |
| `-name` 格式 | `^[a-z0-9][a-z0-9._-]{1,63}$` —— **小寫**、2–64 字元 |

其他 admin 指令:

| 指令 | 用途 | 備註 |
| :--- | :--- | :--- |
| `kbtoken list` | 列出所有 principal 的中繼資料 | 不含 token 與 digest |
| `kbtoken rotate-admin -id <p_...> [-name <新名字>]` | 換一把新 admin | **舊的不會自動撤銷**,要另外 `revoke-admin` |
| `kbtoken revoke-admin -id <p_...> [-force]` | 撤銷 admin | 撤銷**最後一把** admin 必須加 `-force` |

### 4.3 第二步:用 admin token 建其他 token

服務啟動後:

```bash
curl -X POST http://localhost:12598/admin/tokens \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"eval-support","all_teams":true,"engineering":false}'
```

**Request**

| 欄位 | 型別 | 必填 | 說明 |
| :--- | :--- | :---: | :--- |
| `name` | string | ✅ | 小寫,`^[a-z0-9][a-z0-9._-]{1,63}$`,**不可與現有名稱重複** |
| `teams` | `[]string` | | 例如 `["Store.POS"]` |
| `all_teams` | bool | | 與 `teams` 擇一 |
| `engineering` | bool | | |
| `indexer` | bool | | |
| `admin` | bool | | **會被忽略**(見 4.1) |

**Response `201`**

```json
{"id":"p_xxxxxxxxxxxxxxxxxxxxxx","name":"eval-support","token":"kb_..."}
```

三把常用 token 的建法:

```bash
# 客服 / 驗收
-d '{"name":"eval-support","all_teams":true,"engineering":false}'

# 工程
-d '{"name":"eng","all_teams":true,"engineering":true}'

# 索引(CI)
-d '{"name":"eval-indexer","all_teams":true,"indexer":true}'
```

### 4.4 列出與撤銷

```bash
curl http://localhost:12598/admin/tokens -H "Authorization: Bearer $ADMIN_TOKEN"
```

回傳陣列,每筆含 `id` / `name` / `teams` / `all_teams` / `engineering` / `indexer` /
`admin` / `created_at`。**永遠不含 token 或 digest** —— 遺失的 token 只能撤銷重建。

```bash
curl -X DELETE http://localhost:12598/admin/tokens/p_xxxxxxxxxxxxxxxxxxxxxx \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

`204 No Content`。**用的是不可變的 `id`,不是 `name`**;已刪除的 id 永不重用。
透過網路刪 admin principal 會得到 `403`(admin 只能由 CLI 管理)。

---

## 五、使用 Token

### 5.1 `POST /chat`

```bash
curl -X POST http://localhost:12598/chat \
  -H "Authorization: Bearer $SUPPORT_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query":"如何作廢訂單?"}'
```

**Request**

| 欄位 | 型別 | 說明 |
| :--- | :--- | :--- |
| `query` | string | 必填 |
| `session_id` | string | 選填。**帶入上一次回傳的值即可接續多輪對話** |

**Response `200`**

```json
{
  "session_id": "...",
  "answer": "...",
  "grounded": true,
  "sources": ["procedures/POS/ordering-procedure.md#步驟-3"],
  "images": ["_assets/POS/.../S03-order_list.jpg"],
  "strategy": "hybrid",
  "bm25_max": 21.2,
  "best_cosine": 0.81
}
```

| 欄位 | 說明 |
| :--- | :--- |
| `grounded` | **要分支判斷的就是這個欄位,不要去 parse `answer` 的文字。** `false` = 檢索沒撐住或模型婉拒 |
| `sources` | 通過該 principal 權限過濾**之後**的引用 |
| `strategy` | `hybrid`(BM25 + 向量 RRF 融合)/ `markdown`(只有 BM25)/ `vector` |
| `bm25_max`、`best_cosine` | 檢索分數,診斷用 —— 婉拒時可分辨是「檢索沒東西」還是「有證據但模型不答」 |

多輪對話:回傳的 `session_id` 帶回下一次請求即可。**session 綁定 principal ID**,
換一把 token 用同一個 `session_id` 會拿到 `403`。

### 5.2 `POST /index`

```bash
curl -X POST http://localhost:12598/index \
  -H "Authorization: Bearer $INDEXER_TOKEN"
```

無 body。`200` 回 `{"files_indexed":176,"sections_indexed":1395}`。

- 逾時 **60 秒**。全量重建約 54s、快取全命中約 2.1s(1395 段)
- 只有 body 變動的段落會重新 embed(向量快取以 body 的 SHA-256 為 key)
- 想強制全量重算:先刪 `.kb/faiss_index/vectors.bin`
- 失敗一律回 `500 {"error":"internal error"}` —— **真正的原因只在 server log**,
  回應刻意不洩漏語料結構。存取稽核失敗時 log 會逐一點名 `檔案#anchor`

<!-- -->

> ⚠️ **Git Bash 的 `curl` 在長時間 `/index` 上可能回 `http=000`,而服務其實是 200。**
> 以 server log 為準,不要據此重跑。

### 5.3 `/mcp`(Streamable HTTP)

給 MCP client 用,同一把 bearer token,**權限過濾完全一致**。提供一個唯讀工具:

| 工具 | 輸入 | 輸出 |
| :--- | :--- | :--- |
| `search_kb` | `query`、`session_id`(選填) | `answer`、`grounded`、`session_id`、`sources`、`images`、`strategy` |

Stateless + JSON response 模式,`GET` / `POST` / `DELETE` 都掛在 `/mcp`。

---

## 六、狀態碼

| 狀態 | 何時 | Body |
| :--- | :--- | :--- |
| `200` | 成功 | 各端點 schema |
| `201` | token 建立成功 | `CreateTokenResponse` |
| `204` | token 刪除成功 | 空 |
| `400` | `query` 空、JSON 壞掉、principal 名稱不合法 | `{"error":"..."}` |
| `401` | 沒帶 token / 格式不是 `Bearer x` / token 無效 | `{"error":"unauthorized"}` |
| `403` | capability 不足;或 `session_id` 屬於別的 principal | `{"error":"forbidden"}` |
| `404` | principal 不存在 | `{"error":"principal not found"}` |
| `409` | 名稱重複、超過 1000 個 principal、快照過大 | `{"error":"token cannot be created"}` |
| `500` | 其他 | `{"error":"internal error"}` |

> **一個例外要記住:索引還沒建立時,`/chat` 回的是 `200`,不是錯誤。**
> Body 為 `{"answer":"The knowledge base has not been indexed yet. POST /index first.","sources":[]}`。
> 只看 HTTP 狀態碼的自動化流程會把它當成正常回答。

---

## 七、關閉認證的本機模式

`KB_AUTH_DISABLED=true` 時:

| 行為 | 說明 |
| :--- | :--- |
| 監聽位址 | **強制 `127.0.0.1`**,忽略 `APP_BIND_ADDRESS` |
| principal | 全部請求都是 `AllTeams + Engineering` 的全權身分 |
| `/admin/tokens` | **完全不註冊**(不是 403,是 404) |
| `/mcp` | 不驗證,同樣全權 |

**只用於本機開發。** 它會關掉分層過濾,所以在這個模式下跑出來的 eval 數字
**不能拿來證明權限篩選正確** —— 那正是它繞過的東西。

### stdio MCP(`cmd/kbmcp`)

沒有信任邊界,**一律全權存取**,不讀 token。tier 過濾只在 HTTP 與
Streamable HTTP `/mcp` 上生效。

---

## 八、相關環境變數

| 變數 | 預設 | 說明 |
| :--- | :--- | :--- |
| `APP_PORT` | `12598` | HTTP 埠。打錯埠拿到的是 curl exit 7,不是 404 |
| `APP_BIND_ADDRESS` | 空 | 認證關閉時被強制為 `127.0.0.1` |
| `KB_AUTH_FILE` | — | **必填**,auth 快照路徑。沒設服務不啟動 |
| `KB_AUTH_DISABLED` | `false` | 見第七節 |
| `KB_DOCS_DIR` | `docs` | 語料目錄 |
| `KB_INDEX_DIR` | `.kb` | 索引與向量快取目錄 |

---

## 九、營運注意事項

- **token 明文只出現一次**(建立當下)。遺失只能撤銷重建,`GET /admin/tokens` 幫不上忙
- **`auth.json` 等同於全部 token 的控制權**,不要進版控,權限收到 `0600`
  (Unix 下權限過寬服務會警告;Windows 不檢查)
- **改 auth 一定要停服務。** `kbtoken` 有 `/health` 探測與 lock file 兩道門
- **token 沒有到期日。** 定期輪替要自己排;`rotate-admin` **不會**自動撤銷舊的那把

---

## 十、拿到 token 之後:接上 opencode

opencode 透過 **MCP** 連 KB,走的是 5.3 節的 `/mcp` 端點,權限過濾與 `/chat` 完全一致。

### 10.1 先決定用哪一把 token

opencode 是工程用的 agent,所以要 `engineering=true`;但**不要**順手拿帶 `indexer` 的那把。

```bash
curl -X POST http://localhost:12598/admin/tokens \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"opencode","all_teams":true,"engineering":true}'
```

**一個 agent 一把 token。** 共用之後 log 裡的 `principal_id` 就分不出是誰問的,撤銷時
也只能全部一起撤。也不要給 `indexer` —— opencode 沒有理由能重建索引。

### 10.2 寫進設定檔

| 位置 | 適用 |
| :--- | :--- |
| `~/.config/opencode/opencode.jsonc` | **全域**,所有專案都吃得到(建議放這裡) |
| `<專案根>/opencode.json` 或 `opencode.jsonc` | 只在該專案生效 |

```json
{
  "$schema": "https://opencode.ai/config.json",
  "permission": "allow",
  "mcp": {
    "knowledge_base": {
      "type": "remote",
      "url": "http://127.0.0.1:12598/mcp",
      "enabled": true,
      "timeout": 60000,
      "headers": {
        "Authorization": "Bearer kb_你的token"
      }
    }
  }
}
```

`McpRemoteConfig` 的欄位只有 `type` / `url` / `enabled` / `headers` / `oauth` / `timeout`,
`headers` 是字串對字串。

> ⏱️ **`timeout` 一定要調大。** opencode 對 MCP 請求的預設是 **5000 ms**,而 `search_kb`
> 內含一次 LLM 呼叫,正常就要好幾秒 —— 用預設值會**穩定逾時**,看起來像 KB 壞了。

> 🚨 **漏掉 `headers` 是最容易犯、也最難看出來的錯。** 服務對沒帶 token 的 `/mcp` 一律
> 回 **401**,而 opencode 只會表現成「這個 MCP server 沒有可用的工具」——
> **不會告訴你是認證問題**。改完設定要重啟 opencode。

### 10.3 驗證

先確認 token 本身有效,再去怪 opencode:

```bash
curl -X POST http://localhost:12598/mcp \
  -H "Authorization: Bearer $OPENCODE_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

回應要看得到 `search_kb`。拿到 401 就是 token 的問題,與 opencode 無關。

接著在 opencode 裡問一個**只有 KB 有答案**的問題,確認它真的呼叫了 `search_kb` 而不是
憑記憶回答。服務端 log 會有一行 `search_kb`,含 `principal_id` / `grounded` / `sources`。

### 10.4 另一條路:stdio(`cmd/kbmcp`)

```json
{
  "mcp": {
    "knowledge_base": {
      "type": "local",
      "command": ["C:\\path\\to\\bin\\kbmcp.exe"],
      "enabled": true,
      "timeout": 60000
    }
  }
}
```

> 🔓 **stdio 沒有信任邊界,不讀 token,一律全權存取** —— 看得到所有 team 的所有段落,
> 包含 restricted。**分層過濾在這條路上完全不生效。** 要驗證分層是否正確,一定要走 remote。
>
> `kbmcp.exe` 直接讀 `.kb/` 的索引與向量檔,**格式改變後要重新編譯**。

### 10.5 安全

- `opencode.jsonc` 裡是**明文 token**。放進**專案內**就要確認它不會被 commit
- token 沒有到期日,這個檔案等同於一把長期有效的 KB 讀取權
- 要停用某個 agent:`DELETE /admin/tokens/:id`,**立即生效不必重啟服務**

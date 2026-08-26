# Requirement eval schema（題庫契約）

Final suite 使用 JSON。最小結構：

```json
{
  "schema_version": 1,
  "suite": "Store.POS requirement acceptance",
  "status": "draft_unapproved",
  "scoring_contract_version": 2,
  "source_fingerprint": "sha256:<from build_requirement_candidates.py>",
  "scope": {"team": "Store.POS", "pos_version": "1.3.6", "admin_version": "1.3.0"},
  "cases": [
    {
      "id": "REQ-BR-SYNC-001",
      "kind": "business_rule",
      "area": "sync",
      "evidence_status": "observed_reference",
      "question": "雲端只修改一個設定區塊並發布後，門店應如何同步？",
      "expected": {
        "grounded": true,
        "must_include": ["DataChange"],
        "must_include_groups": [["只拉取過期區塊", "過期區塊", "按需拉取"]]
      },
      "source": {
        "file": "docs/Store.POS/reference/example-reference.md",
        "lines": "42-47"
      }
    }
  ]
}
```

## Case contract

- `id`、`kind`、`area`、`evidence_status`、`question`、`expected`、`source` 必填。
- `source_fingerprint` 必須從同一輪 candidate build 帶入；procedure／reference 變動後 runner 會拒絕舊題庫。
- 可回答題：`expected.grounded=true`，且 `must_include` 或 `must_include_any` 至少一個非空。
- 拒答題：`kind=must_not_infer`、`evidence_status=missing_truth`、
  `expected.grounded=false`、`expected.must_refuse=true`，並提供 `must_include_any` 的拒答理由。
- `must_include` 全部要命中；`must_include_any` 至少命中一個；`must_include_groups` 的每組至少命中一個。
- `must_not_include` 只適合禁止正向宣告的詞，不要用在可能出現在否定句中的詞；例如不要用
  `整包重載` 來禁止「不應整包重載」，因為目前 runner 是字面比對。
- 比對忽略大小寫、空白、Markdown backtick；符號、數字與錯誤碼不要模糊比對。
- `source.file` 使用 repo-relative path；`source.lines` 可寫 `42-47, 75-86`。

## must_not_infer 的雙層判分

- `safety_ok`：服務回 `grounded=false`。這是安全 gate；grounded=true 一律算 unsafe inference。
- `reason_ok`：答案是否說明「未定義／待釐清」；用 `must_include_any` 或
  `must_include_groups` 驗證，屬回答品質，不與安全性混算。
- 安全 gate 仍要求每輪所有負例完成，transport skipped 不能算安全通過。

## Evidence status

| 值 | 意義 |
|---|---|
| `recorded_behavior` | 目前版本錄製到的可觀察行為，不等於產品核准 |
| `observed_reference` | PM／架構說明的現況整理，尚未附核准紀錄 |
| `approved_requirement` | 有核准人、核准日期與 revision 的正式真值 |
| `missing_truth` | 文件明確待釐清；只能做 `must_not_infer` |

若使用 `approved_requirement`，case 另加：

```json
"approval": {
  "approved_by": "product-owner@example.com",
  "approved_at": "2026-08-26",
  "revision": "REQ-123-v2"
}
```

# Acceptance Checkpoint

## Environment

- Bundle: 42 WPF replay Markdown files under `docs/`
- Chat model: `llama3.1:8b`
- Embedding model: `snowflake-arctic-embed2`
- Indexed at: 2026-07-20

## Query Results

| Query | Type | Correct | Sources | Strategy | Screenshot filename returned | bm25Max | bestCosine |
|---|---|---|---|---|---|---:|---:|
| 動態密碼登入頁有哪些輸入欄位和按鈕？ | Chinese how-to | no | `登入__00_動態密碼登入.md#登入-00-動態密碼登入`; `補印__47_補印訂單查詢.md#補印-47-補印訂單查詢`; `暫存訂單__41_暫存訂單列表.md#暫存訂單-41-暫存訂單列表` | hybrid | no | 38.847795 | 0.747335 |
| 如何設定印表機？ | Chinese how-to | no | `周邊管理__67_印表機設定.md#周邊管理-67-印表機設定`; `周邊管理__66_單據列印設定.md#周邊管理-66-單據列印設定`; `周邊管理__68_列印格式設定.md#周邊管理-68-列印格式設定` | hybrid | no | 14.265491 | 0.549170 |
| 如何列印營收報表？ | Chinese how-to | yes | `營業報表__62_報表列印條件.md#營業報表-62-報表列印條件`; `營業報表__63_日營收報表預覽.md#營業報表-63-日營收報表預覽`; `營業報表__64_商品銷售列印條件.md#營業報表-64-商品銷售列印條件` | hybrid | no | 17.852776 | 0.687991 |
| Password | English identifier | yes | `account_help.md#reset-password`; `登入__00_動態密碼登入.md#登入-00-動態密碼登入`; `account_help.md#change-email-address` | hybrid | yes | 5.809455 | 0.531758 |
| PART_TextBox | English identifier | no | `補印__47_補印訂單查詢.md#補印-47-補印訂單查詢`; `營業報表__65_商品銷售清單預覽.md#營業報表-65-商品銷售清單預覽`; `營業報表__62_報表列印條件.md#營業報表-62-報表列印條件` | hybrid | no | 2.455236 | 0.370364 |

## Verdict

`cosineMin = 0.30` is not defensible as a quality threshold: all five queries
exceeded it, including three incorrect or incomplete answers. It is only a
low-confidence refusal gate, not a relevance calibration.

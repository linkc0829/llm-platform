package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestBundle(t *testing.T, team string) string {
	t.Helper()
	dir := t.TempDir()

	procDir := filepath.Join(dir, "procedures", "POS_Ordering")
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatalf("mkdir procedures: %v", err)
	}

	// Module 1: Cart_Item_Modify_Combo (2 steps)
	m1Content := `---
id: "Store.POS--POS_Ordering-Cart_Item_Modify_Combo-procedure"
team: "Store.POS"
product: "POS"
doc_type: "procedure"
version: "2026-08-21T16:59:31+08:00"
access_level: "internal-engineering"
owner: "wpf-replay"
last_reviewed: "2026-08-26"
evidence_basis: "recorded_trajectory"
page_code: "POS_Ordering"
module_code: "Cart_Item_Modify_Combo"
flow_name: "點餐-套餐品項修改"
---

# 點餐-套餐品項修改 — 操作程序

## 點餐-套餐品項修改 — 完整步驟總覽
1. 點擊「增加套餐數量」
2. 進行一次未具名的點擊

### 點餐-套餐品項修改 — 步驟 1:點擊「增加套餐數量」
- 動作證據：` + "`vision_inferred`" + `
- 原始 Gherkin When（敘事註解）：點選加號符號，彈跳出套餐點餐視窗
- 原始 Gherkin Then（敘事註解）：系統狀態更新為套餐修改面板

### 點餐-套餐品項修改 — 步驟 2:進行一次未具名的點擊
- 動作證據：` + "`recorded_unlabeled`" + `
- 原始 Gherkin When（敘事註解）：點選完成套餐關閉彈跳視窗
- 原始 Gherkin Then（敘事註解）：系統狀態更新為套餐修改未完成
`
	if err := os.WriteFile(filepath.Join(procDir, "Cart_Item_Modify_Combo-procedure.md"), []byte(m1Content), 0o600); err != nil {
		t.Fatalf("write m1: %v", err)
	}

	// Module 2: Combo_Ordering (1 step)
	m2Content := `---
id: "Store.POS--POS_Ordering-Combo_Ordering-procedure"
team: "Store.POS"
product: "POS"
doc_type: "procedure"
version: "2026-08-21T14:55:09+08:00"
access_level: "internal-engineering"
owner: "wpf-replay"
last_reviewed: "2026-08-26"
evidence_basis: "recorded_trajectory"
page_code: "POS_Ordering"
module_code: "Combo_Ordering"
flow_name: "點餐-套餐商品點餐"
---

# 點餐-套餐商品點餐 — 操作程序

### 點餐-套餐商品點餐 — 步驟 1:點擊「選擇豪華雙人套餐」
- 動作證據：` + "`recorded`" + `
- 原始 Gherkin When（敘事註解）：點選套餐項目
- 原始 Gherkin Then（敘事註解）：進入套餐選擇面板
`
	if err := os.WriteFile(filepath.Join(procDir, "Combo_Ordering-procedure.md"), []byte(m2Content), 0o600); err != nil {
		t.Fatalf("write m2: %v", err)
	}

	initialManifest := manifestIndex{
		Docs: []manifestDoc{
			{
				ID:      "Store.POS--POS_Ordering-Cart_Item_Modify_Combo-procedure",
				DocType: "procedure",
				Path:    "procedures/POS_Ordering/Cart_Item_Modify_Combo-procedure.md",
				Team:    team,
				Product: "POS",
				Version: "2026-08-21T16:59:31+08:00",
			},
			{
				ID:      "Store.POS--POS_Ordering-Combo_Ordering-procedure",
				DocType: "procedure",
				Path:    "procedures/POS_Ordering/Combo_Ordering-procedure.md",
				Team:    team,
				Product: "POS",
				Version: "2026-08-21T14:55:09+08:00",
			},
		},
	}
	b, err := json.MarshalIndent(initialManifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kb_index.json"), b, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	return dir
}

func TestKBDistill_FullGenerationAndIdempotence(t *testing.T) {
	bundleDir := createTestBundle(t, "Store.POS")

	opts := options{
		team:   "Store.POS",
		bundle: bundleDir,
	}

	// 1. Run full generation
	if err := run(opts); err != nil {
		t.Fatalf("run(full) failed: %v", err)
	}

	// Verify distilled files exist
	distilledDir := filepath.Join(bundleDir, "distilled")
	f1 := filepath.Join(distilledDir, "POS_Ordering-Cart_Item_Modify_Combo-distilled.md")
	f2 := filepath.Join(distilledDir, "POS_Ordering-Combo_Ordering-distilled.md")

	b1, err := os.ReadFile(f1)
	if err != nil {
		t.Fatalf("read f1: %v", err)
	}
	content1 := string(b1)
	if !strings.Contains(content1, "doc_type: \"distilled\"") {
		t.Errorf("f1 missing doc_type: distilled")
	}
	if !strings.Contains(content1, "derived_from: \"Store.POS--POS_Ordering-Cart_Item_Modify_Combo-procedure\"") {
		t.Errorf("f1 missing derived_from")
	}
	if !strings.Contains(content1, "## 點餐-套餐品項修改 — 行為鏈") {
		t.Errorf("f1 missing H2 heading")
	}
	if !strings.Contains(content1, "1. 點擊「增加套餐數量」 → 點選加號符號，彈跳出套餐點餐視窗 → 系統狀態更新為套餐修改面板") {
		t.Errorf("f1 missing step 1 line")
	}
	if !strings.Contains(content1, "[L0: #點餐-套餐品項修改-步驟-1-點擊-增加套餐數量 | evidence: procedure_visual]") {
		t.Errorf("f1 missing step 1 L0 citation, got:\n%s", content1)
	}

	if _, err := os.ReadFile(f2); err != nil {
		t.Fatalf("read f2: %v", err)
	}

	// Verify manifest updated
	manifestBytes, err := os.ReadFile(filepath.Join(bundleDir, "kb_index.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest manifestIndex
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(manifest.Docs) != 4 {
		t.Fatalf("manifest docs count = %d, want 4 (2 procedures + 2 distilled)", len(manifest.Docs))
	}

	// 2. Run -check mode: must pass
	optsCheck := options{
		team:   "Store.POS",
		bundle: bundleDir,
		check:  true,
	}
	if err := run(optsCheck); err != nil {
		t.Fatalf("run(-check) failed: %v", err)
	}
}

func TestKBDistill_ModuleModeSelectiveUpdate(t *testing.T) {
	bundleDir := createTestBundle(t, "Store.POS")

	// First, run full to create both
	if err := run(options{team: "Store.POS", bundle: bundleDir}); err != nil {
		t.Fatalf("full run failed: %v", err)
	}

	// Now run module mode on Cart_Item_Modify_Combo only
	optsMod := options{
		team:    "Store.POS",
		bundle:  bundleDir,
		modules: stringSliceFlag{"POS_Ordering/Cart_Item_Modify_Combo"},
	}
	if err := run(optsMod); err != nil {
		t.Fatalf("module run failed: %v", err)
	}

	// Verify Combo_Ordering file still exists (not cleared)
	f2 := filepath.Join(bundleDir, "distilled", "POS_Ordering-Combo_Ordering-distilled.md")
	if _, err := os.Stat(f2); err != nil {
		t.Errorf("module mode unexpectedly deleted unselected module file: %v", err)
	}
}

func TestKBDistill_FailLoud_DuplicateStep(t *testing.T) {
	dir := t.TempDir()
	procDir := filepath.Join(dir, "procedures", "P")
	_ = os.MkdirAll(procDir, 0o755)

	content := `---
id: "Store.POS--P-M-procedure"
team: "Store.POS"
product: "POS"
doc_type: "procedure"
version: "1.0"
access_level: "internal-engineering"
owner: "tester"
last_reviewed: "2026-08-26"
page_code: "P"
module_code: "M"
---

### P-M — 步驟 1:Action A
- 動作證據：` + "`recorded`" + `

### P-M — 步驟 1:Action B
- 動作證據：` + "`recorded`" + `
`
	_ = os.WriteFile(filepath.Join(procDir, "M-procedure.md"), []byte(content), 0o600)
	manifest := manifestIndex{Docs: []manifestDoc{{ID: "Store.POS--P-M-procedure", Team: "Store.POS"}}}
	b, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dir, "kb_index.json"), b, 0o600)

	err := run(options{team: "Store.POS", bundle: dir})
	if err == nil || !strings.Contains(err.Error(), "duplicate step number 1") {
		t.Fatalf("expected duplicate step number error, got: %v", err)
	}
}

func TestKBDistill_FailLoud_GapInSteps(t *testing.T) {
	dir := t.TempDir()
	procDir := filepath.Join(dir, "procedures", "P")
	_ = os.MkdirAll(procDir, 0o755)

	content := `---
id: "Store.POS--P-M-procedure"
team: "Store.POS"
product: "POS"
doc_type: "procedure"
version: "1.0"
access_level: "internal-engineering"
owner: "tester"
last_reviewed: "2026-08-26"
page_code: "P"
module_code: "M"
---

### P-M — 步驟 1:Action A
- 動作證據：` + "`recorded`" + `

### P-M — 步驟 3:Action C
- 動作證據：` + "`recorded`" + `
`
	_ = os.WriteFile(filepath.Join(procDir, "M-procedure.md"), []byte(content), 0o600)
	manifest := manifestIndex{Docs: []manifestDoc{{ID: "Store.POS--P-M-procedure", Team: "Store.POS"}}}
	b, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dir, "kb_index.json"), b, 0o600)

	err := run(options{team: "Store.POS", bundle: dir})
	if err == nil || !strings.Contains(err.Error(), "non-consecutive step numbers") {
		t.Fatalf("expected non-consecutive error, got: %v", err)
	}
}

func TestKBDistill_FailLoud_MissingEvidence(t *testing.T) {
	dir := t.TempDir()
	procDir := filepath.Join(dir, "procedures", "P")
	_ = os.MkdirAll(procDir, 0o755)

	content := `---
id: "Store.POS--P-M-procedure"
team: "Store.POS"
product: "POS"
doc_type: "procedure"
version: "1.0"
access_level: "internal-engineering"
owner: "tester"
last_reviewed: "2026-08-26"
page_code: "P"
module_code: "M"
---

### P-M — 步驟 1:Action A
- 原始 Gherkin When（敘事註解）：When something
`
	_ = os.WriteFile(filepath.Join(procDir, "M-procedure.md"), []byte(content), 0o600)
	manifest := manifestIndex{Docs: []manifestDoc{{ID: "Store.POS--P-M-procedure", Team: "Store.POS"}}}
	b, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dir, "kb_index.json"), b, 0o600)

	err := run(options{team: "Store.POS", bundle: dir})
	if err == nil || !strings.Contains(err.Error(), "missing 動作證據") {
		t.Fatalf("expected missing 動作證據 error, got: %v", err)
	}
}

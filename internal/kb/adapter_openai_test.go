package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClientEmbedBatchesRequests(t *testing.T) {
	var batches [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("Embed() request path = %q, want %q", r.URL.Path, "/v1/embeddings")
		}
		var request struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Embed() decode request = %v, want nil", err)
			return
		}
		batches = append(batches, request.Input)
		data := make([]map[string]any, len(request.Input))
		for i := range request.Input {
			data[i] = map[string]any{"index": i, "embedding": []float64{float64(len(batches)), float64(i)}}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
			t.Errorf("Embed() encode response = %v, want nil", err)
		}
	}))
	t.Cleanup(server.Close)

	texts := make([]string, 65)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}
	client := NewOpenAIClient("test", server.URL+"/v1", "", "chat", "embed")
	vectors, err := client.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed() error = %v, want nil", err)
	}

	wantSizes := []int{32, 32, 1}
	if len(batches) != len(wantSizes) {
		t.Fatalf("Embed() batches = %d, want %d", len(batches), len(wantSizes))
	}
	for i, wantSize := range wantSizes {
		if len(batches[i]) != wantSize {
			t.Errorf("Embed() batch %d size = %d, want %d", i+1, len(batches[i]), wantSize)
		}
	}
	if len(vectors) != len(texts) {
		t.Fatalf("Embed() vectors = %d, want %d", len(vectors), len(texts))
	}
	for i, vector := range vectors {
		wantBatch := float32(i/embeddingBatchSize + 1)
		wantIndex := float32(i % embeddingBatchSize)
		if len(vector) != 2 || vector[0] != wantBatch || vector[1] != wantIndex {
			t.Errorf("Embed() vector %d = %v, want [%v %v]", i, vector, wantBatch, wantIndex)
		}
	}
}

func TestGroundedPromptIncludesEvidenceClasses(t *testing.T) {
	sections := []Section{
		mustSection(t, "login-ui_inventory.md", "畫面:登入", "控制項", nil),
		mustSection(t, "login-procedure.md", "步驟 1", "- **動作證據**:`recorded`", nil),
		mustSection(t, "login-procedure.md", "步驟 1a", "- **動作證據**:`vision_inferred`", nil),
		mustSection(t, "login-procedure.md", "步驟 2", "- **動作證據**:`inferred`", nil),
		mustSection(t, "guide.md", "說明", "一般說明", nil),
	}

	got := groundedPrompt("如何登入？", sections)
	for _, want := range []string{
		"[login-ui_inventory.md#畫面-登入] (evidence: ui_inventory)",
		"[login-procedure.md#步驟-1] (evidence: procedure)",
		"[login-procedure.md#步驟-1a] (evidence: procedure_visual)",
		"[login-procedure.md#步驟-2] (evidence: procedure_inferred)",
		"[guide.md#說明] (evidence: general)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("groundedPrompt() = %q, want substring %q", got, want)
		}
	}
}

func TestGroundingSystemWhitelistsProcedureEvidence(t *testing.T) {
	for _, want := range []string{
		"Only sections tagged evidence: procedure or procedure_visual may support an action order",
		"ui_inventory, procedure_unlabeled, procedure_inferred, or general cannot support steps or sequence",
		"A procedure_visual section is a coordinate-and-screenshot visual review",
		"never which control was clicked",
	} {
		if !strings.Contains(groundingSystem, want) {
			t.Errorf("groundingSystem = %q, want substring %q", groundingSystem, want)
		}
	}
}

func mustSection(t *testing.T, file, heading, body string, meta map[string]string) Section {
	t.Helper()
	section, err := NewSection(file, heading, body, meta, nil)
	if err != nil {
		t.Fatalf("NewSection(%q, %q) error = %v, want nil", file, heading, err)
	}
	return section
}

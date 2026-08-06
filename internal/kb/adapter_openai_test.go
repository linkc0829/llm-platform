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
	client := NewOpenAIClient("test", server.URL+"/v1", "", "", "", "chat", "embed")
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

func TestOpenAIClientUsesSeparateEmbeddingAPIKey(t *testing.T) {
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer chat-key" {
			t.Errorf("chat Authorization = %q, want Bearer chat-key", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "answer"}}}})
	}))
	t.Cleanup(chat.Close)
	embed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer embed-key" {
			t.Errorf("embed Authorization = %q, want Bearer embed-key", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1, 2}}}})
	}))
	t.Cleanup(embed.Close)

	client := NewOpenAIClient("chat-key", chat.URL+"/v1", embed.URL+"/v1", "embed-key", "", "chat", "embed")
	if _, err := client.Answer(context.Background(), "question", nil, nil); err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if _, err := client.Embed(context.Background(), []string{"text"}); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
}

func TestOpenAIClientEmbeddingAPIKeyFallsBackToChatAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer chat-key" {
			t.Errorf("embed Authorization = %q, want Bearer chat-key", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1}}}})
	}))
	t.Cleanup(server.Close)

	client := NewOpenAIClient("chat-key", "", server.URL+"/v1", "", "", "chat", "embed")
	if _, err := client.Embed(context.Background(), []string{"text"}); err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
}

func TestOpenAIClientSendsGeminiThinkingLevel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Answer() decode request = %v", err)
		}
		extraBody, ok := request["extra_body"].(map[string]any)
		if !ok {
			t.Fatalf("Answer() extra_body = %#v, want object", request["extra_body"])
		}
		google := extraBody["google"].(map[string]any)
		thinking := google["thinking_config"].(map[string]any)
		if thinking["thinking_level"] != "minimal" {
			t.Errorf("thinking level = %#v, want minimal", thinking["thinking_level"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "answer"}}}})
	}))
	t.Cleanup(server.Close)

	client := NewOpenAIClient("key", server.URL+"/v1", "", "", "minimal", "chat", "embed")
	if _, err := client.Answer(context.Background(), "question", nil, nil); err != nil {
		t.Fatalf("Answer() error = %v", err)
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
		// Without this the steps-only guardrail reads as a blanket ban and the model
		// refuses button-location questions even with the right section in the top 3.
		"ui_inventory sections are the authoritative record of which controls a screen contains",
		"the steps-only restriction above does not apply",
		"Reply in the same language as the question",
		// Without this the model recites the verified symbol and then refuses because the
		// context does not restate the question's wording.
		"refuse when the answer is absent, never when it is present but phrased differently",
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

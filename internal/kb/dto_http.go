package kb

type HealthResponse struct {
	Status string `json:"status"`
	// Chat names the decoding settings the service answers with. An eval run
	// records it so a later comparison can tell a corpus regression apart from
	// a model or parameter change. Absent when no real model is wired.
	Chat *ChatConfig `json:"chat,omitempty"`
}

type ChatConfig struct {
	Model string `json:"model"`
	// Prompt fingerprints the grounding instructions, so a run can prove which
	// prompt answered it. It changes whenever the prompt does.
	Prompt      string  `json:"prompt"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int64   `json:"max_tokens,omitempty"`
}

type IndexResponse struct {
	FilesIndexed    int `json:"files_indexed"`
	SectionsIndexed int `json:"sections_indexed"`
}

type ChatRequest struct {
	Query     string `json:"query"`
	SessionID string `json:"session_id"`
}

type ChatResponse struct {
	SessionID  string   `json:"session_id"`
	Answer     string   `json:"answer"`
	Grounded   bool     `json:"grounded"`
	Sources    []string `json:"sources"`
	Images     []string `json:"images"`
	Strategy   string   `json:"strategy"`
	BM25Max    float64  `json:"bm25_max"`
	BestCosine float64  `json:"best_cosine"`
}

func toChatResponse(a Answer, sessionID string, metrics RetrievalMetrics) ChatResponse {
	sources := make([]string, 0, len(a.Sources()))
	for _, citation := range a.Sources() {
		sources = append(sources, citation.String())
	}
	images := a.Images()
	if images == nil {
		images = []string{}
	}
	return ChatResponse{SessionID: sessionID, Answer: a.Text(), Grounded: a.Grounded(), Sources: sources, Images: images, Strategy: a.Strategy(), BM25Max: metrics.BM25Max, BestCosine: metrics.BestCosine}
}

package kb

import "testing"

func TestOpenAIClientEmbedIdentityUsesEffectiveBaseURL(t *testing.T) {
	fallback := NewOpenAIClient("key", "https://chat.example/v1", "", "", "", "chat", "embed")
	if got := fallback.EmbedIdentity(); got != "embed@https://chat.example/v1" {
		t.Errorf("EmbedIdentity() with fallback URL = %q, want embed@https://chat.example/v1", got)
	}

	explicit := NewOpenAIClient("key", "https://chat.example/v1", "https://embed.example/v1", "", "", "chat", "embed")
	if got := explicit.EmbedIdentity(); got != "embed@https://embed.example/v1" {
		t.Errorf("EmbedIdentity() with explicit URL = %q, want embed@https://embed.example/v1", got)
	}
}

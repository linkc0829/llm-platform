package kb

import "testing"

func TestOpenAIClientEmbedIdentityUsesBaseURL(t *testing.T) {
	client := NewOpenAIClient("key", "https://chat.example/v1", "", "chat", "embed", ChatOptions{})
	if got := client.EmbedIdentity(); got != "embed@https://chat.example/v1" {
		t.Errorf("EmbedIdentity() with base URL = %q, want embed@https://chat.example/v1", got)
	}
}

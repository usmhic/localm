package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderAliases(t *testing.T) {
	tests := map[string]string{
		"ollama":    "ollama",
		"LM-Studio": "lmstudio",
		"llama.cpp": "llamacpp",
		"LocalAI":   "localai",
		"openai":    "custom",
	}
	for input, expected := range tests {
		if actual := normalizeProviderName(input); actual != expected {
			t.Errorf("normalizeProviderName(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestCapabilityMetadataAcceptsListsAndFlags(t *testing.T) {
	capabilities := capabilityMetadata(
		json.RawMessage(`["streaming","function_calling"]`),
		json.RawMessage(`{"vision":true,"reasoning":false}`),
	)
	for _, capability := range []string{CapabilityStreaming, CapabilityTools, CapabilityVision} {
		if !capabilities[capability] {
			t.Errorf("expected %s capability", capability)
		}
	}
	if capabilities[CapabilityReasoning] {
		t.Fatal("false capability flag was treated as supported")
	}
}

func TestOllamaCompatibilityURL(t *testing.T) {
	t.Setenv("UPSTREAM_BASE_URL", "")
	t.Setenv("OLLAMA_BASE_URL", "http://ollama.internal:11434")
	actual, err := resolveUpstreamBaseURL("ollama")
	if err != nil {
		t.Fatalf("resolve Ollama URL: %v", err)
	}
	if actual != "http://ollama.internal:11434/v1" {
		t.Fatalf("unexpected Ollama URL: %s", actual)
	}
}

func TestInvalidUpstreamURL(t *testing.T) {
	if _, err := validateUpstreamBaseURL("file:///tmp/model.sock"); err == nil {
		t.Fatal("expected non-HTTP URL to fail validation")
	}
	credentialsURL := "https://" + "user" + ":" + "secret" + "@example.com/v1"
	if _, err := validateUpstreamBaseURL(credentialsURL); err == nil {
		t.Fatal("expected embedded credentials to fail validation")
	}
}

func TestProviderForwardsUpstreamKey(t *testing.T) {
	var authorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer upstream.Close()

	provider := newOpenAICompatibleProvider("custom", upstream.URL, "upstream-secret", upstream.Client())
	resp, err := provider.Forward(context.Background(), CapabilityChatCompletions, []byte(`{"model":"test","messages":[]}`))
	if err != nil {
		t.Fatalf("provider chat: %v", err)
	}
	defer resp.Body.Close()
	if authorization != "Bearer upstream-secret" {
		t.Fatalf("unexpected upstream authorization: %q", authorization)
	}
}

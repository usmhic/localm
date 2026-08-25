package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func endpointRequest(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer k1")
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func connectionForTest(name, baseURL string, priority int, capabilities CapabilitySet) ConnectionConfig {
	return ConnectionConfig{
		Name: name, Provider: "custom", BaseURL: strings.TrimRight(baseURL, "/") + "/v1",
		Priority: priority, Trust: "local", Models: map[string]struct{}{"qwen3:8b": {}},
		Capabilities: capabilities,
	}
}

func TestRoutesToHighestPriorityNamedConnection(t *testing.T) {
	var selected string
	low := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		selected = "low"
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer low.Close()
	high := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		selected = "high"
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer high.Close()

	cfg := testConfig(low.URL)
	cfg.Connections = []ConnectionConfig{
		connectionForTest("low", low.URL, 10, defaultCapabilities("custom")),
		connectionForTest("high", high.URL, 100, defaultCapabilities("custom")),
	}
	response := endpointRequest(t, newBridge(cfg, high.Client()).routes(), "/v1/chat/completions", `{"model":"qwen3:8b","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || selected != "high" {
		t.Fatalf("expected high-priority connection, status=%d selected=%q", response.Code, selected)
	}
}

func TestResponsesAndEmbeddingsPassThrough(t *testing.T) {
	paths := make([]string, 0, 2)
	payloads := make([]map[string]any, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream payload: %v", err)
		}
		if payload["extension"] != "preserved" {
			t.Errorf("extension field was not preserved: %#v", payload)
		}
		payloads = append(payloads, payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	handler := newBridge(cfg, upstream.Client()).routes()

	responses := endpointRequest(t, handler, "/v1/responses", `{"model":"qwen3:8b","input":"hello","extension":"preserved"}`)
	if responses.Code != http.StatusOK {
		t.Fatalf("responses status %d: %s", responses.Code, responses.Body.String())
	}
	embeddings := endpointRequest(t, handler, "/v1/embeddings", `{"model":"qwen3:8b","input":"hello","extension":"preserved"}`)
	if embeddings.Code != http.StatusOK {
		t.Fatalf("embeddings status %d: %s", embeddings.Code, embeddings.Body.String())
	}
	if len(paths) != 2 || paths[0] != "/v1/responses" || paths[1] != "/v1/embeddings" {
		t.Fatalf("unexpected upstream paths: %v", paths)
	}
	if payloads[0]["max_output_tokens"] != float64(cfg.DefaultMaxTokens) {
		t.Fatalf("Responses default token limit missing: %#v", payloads[0])
	}
	if _, exists := payloads[1]["max_tokens"]; exists {
		t.Fatalf("embedding request received a generation limit: %#v", payloads[1])
	}
}

func TestUnsupportedFeatureIsRejectedBeforeForwarding(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusOK) }))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	cfg.Connections = []ConnectionConfig{connectionForTest("chat-only", upstream.URL, 1, CapabilitySet{CapabilityChatCompletions: true})}
	response := endpointRequest(t, newBridge(cfg, upstream.Client()).routes(), "/v1/chat/completions", `{"model":"qwen3:8b","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function"}]}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"unsupported_feature"`) {
		t.Fatalf("unexpected response %d: %s", response.Code, response.Body.String())
	}
	if called {
		t.Fatal("unsupported request reached the upstream")
	}
}

func TestRequiredCapabilitiesAreDerivedFromCompatibleFields(t *testing.T) {
	fields := map[string]json.RawMessage{
		"stream":           json.RawMessage(`true`),
		"tools":            json.RawMessage(`[{"type":"function"}]`),
		"response_format":  json.RawMessage(`{"type":"json_schema"}`),
		"reasoning_effort": json.RawMessage(`"high"`),
		"input":            json.RawMessage(`[{"type":"input_image","image_url":"data:image/png;base64,AA=="}]`),
	}
	required, stream := requiredCapabilities(CapabilityChatCompletions, fields)
	if !stream {
		t.Fatal("streaming requirement was not detected")
	}
	for _, capability := range []string{CapabilityChatCompletions, CapabilityStreaming, CapabilityTools, CapabilityStructured, CapabilityReasoning, CapabilityVision} {
		if !required[capability] {
			t.Errorf("required capability %s was not detected", capability)
		}
	}
}

func TestCapabilitiesDiscoversModelsWithoutExposingURLs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected discovery path: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"qwen3:8b","capabilities":["streaming","tools"]}]}`)
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	cfg.DiscoveryTTL = time.Minute
	request := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer k1")
	response := httptest.NewRecorder()
	newBridge(cfg, upstream.Client()).routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("capabilities status %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"available":true`) || !strings.Contains(body, `"tools"`) {
		t.Fatalf("unexpected capabilities: %s", body)
	}
	if strings.Contains(body, upstream.URL) {
		t.Fatal("capability output exposed an internal URL")
	}
}

func TestRoutingUsesDiscoveredModelHealth(t *testing.T) {
	selected := ""
	high := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"object":"list","data":[]}`)
			return
		}
		selected = "high"
		_, _ = io.WriteString(w, `{}`)
	}))
	defer high.Close()
	low := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"qwen3:8b"}]}`)
			return
		}
		selected = "low"
		_, _ = io.WriteString(w, `{}`)
	}))
	defer low.Close()
	cfg := testConfig(low.URL)
	cfg.DiscoveryTTL = time.Minute
	cfg.Connections = []ConnectionConfig{
		connectionForTest("high", high.URL, 100, defaultCapabilities("custom")),
		connectionForTest("low", low.URL, 10, defaultCapabilities("custom")),
	}
	bridge := newBridge(cfg, low.Client())
	discovery := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	discovery.Header.Set("Authorization", "Bearer k1")
	bridge.routes().ServeHTTP(httptest.NewRecorder(), discovery)
	response := endpointRequest(t, bridge.routes(), "/v1/chat/completions", `{"model":"qwen3:8b","messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || selected != "low" {
		t.Fatalf("expected discovered healthy model route, status=%d selected=%q", response.Code, selected)
	}
}

func TestConnectionFileAndRemoteTrustPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "localm.yaml")
	allowed := map[string]struct{}{"qwen3:8b": {}}
	remoteConfig := "allow_remote: false\nconnections:\n  remote:\n    provider: custom\n    base_url: https://example.com/v1\n    trust: remote\n"
	if err := os.WriteFile(path, []byte(remoteConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConnectionsFile(path, allowed); err == nil || !strings.Contains(err.Error(), "allow_remote") {
		t.Fatalf("expected explicit remote-routing error, got %v", err)
	}

	localConfig := "connections:\n  local:\n    provider: ollama\n    base_url: http://127.0.0.1:11434/v1\n    priority: 100\n    models: [qwen3:8b]\n"
	if err := os.WriteFile(path, []byte(localConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	connections, err := loadConnectionsFile(path, allowed)
	if err != nil {
		t.Fatalf("load local connection: %v", err)
	}
	if len(connections) != 1 || connections[0].Name != "local" || connections[0].Priority != 100 {
		t.Fatalf("unexpected connections: %#v", connections)
	}
}

func TestTrackedConfigurationExamplesAreSafeAndValid(t *testing.T) {
	environmentTemplate, err := os.ReadFile(".env.example")
	if err != nil {
		t.Fatalf("read environment template: %v", err)
	}
	seen := make(map[string]bool)
	for lineNumber, line := range strings.Split(string(environmentTemplate), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) == "" {
			t.Fatalf("invalid .env.example line %d", lineNumber+1)
		}
		if value != "" {
			t.Fatalf(".env.example line %d contains a tracked value", lineNumber+1)
		}
		seen[name] = true
	}
	for _, required := range []string{"API_KEYS", "ALLOWED_MODELS", "LOCALM_CONFIG", "UPSTREAM_API_KEY"} {
		if !seen[required] {
			t.Errorf(".env.example is missing %s", required)
		}
	}

	connections, err := loadConnectionsFile("localm.example.yaml", map[string]struct{}{"qwen3:8b": {}})
	if err != nil {
		t.Fatalf("load tracked connection example: %v", err)
	}
	if len(connections) != 1 || connections[0].APIKey != "" || connections[0].Trust != "local" {
		t.Fatalf("tracked connection example is not minimal and secret-free: %#v", connections)
	}
}

func TestLegacyEnvironmentConfigurationRemainsCompatible(t *testing.T) {
	t.Setenv("LOCALM_CONFIG", "")
	t.Setenv("API_KEYS", "client-key")
	t.Setenv("ALLOWED_MODELS", "legacy-model")
	t.Setenv("LLM_PROVIDER", "ollama")
	t.Setenv("UPSTREAM_BASE_URL", "")
	t.Setenv("OLLAMA_BASE_URL", "http://127.0.0.1:11434")
	t.Setenv("MAX_CONCURRENT_LLM", "")
	t.Setenv("MAX_CONCURRENT_OLLAMA", "7")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("load legacy configuration: %v", err)
	}
	if len(cfg.Connections) != 1 || cfg.Connections[0].Name != defaultConnectionName {
		t.Fatalf("unexpected legacy connections: %#v", cfg.Connections)
	}
	if cfg.Connections[0].BaseURL != "http://127.0.0.1:11434/v1" || cfg.MaxConcurrent != 7 {
		t.Fatalf("legacy aliases were not preserved: %#v", cfg)
	}
	if !cfg.Connections[0].Capabilities[CapabilityTools] {
		t.Fatal("legacy pass-through capabilities were not preserved")
	}
}

func TestUpstreamErrorIsSanitized(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"failed at http://private.internal with secret"}}`)
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	response := endpointRequest(t, newBridge(cfg, upstream.Client()).routes(), "/v1/embeddings", `{"model":"qwen3:8b","input":"hello"}`)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "private.internal") || strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("upstream error was not sanitized: %s", response.Body.String())
	}
}

func TestRequestLogsAndPublicReadinessOmitSensitiveData(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"qwen3:8b"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	bridge := newBridge(cfg, upstream.Client())

	previousWriter := log.Writer()
	previousFlags := log.Flags()
	previousPrefix := log.Prefix()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})

	const privatePrompt = "private-prompt-must-not-appear"
	response := endpointRequest(t, bridge.routes(), "/v1/chat/completions", `{"model":"qwen3:8b","messages":[{"role":"user","content":"`+privatePrompt+`"}]}`)
	if response.Code != http.StatusOK {
		t.Fatalf("chat status %d: %s", response.Code, response.Body.String())
	}
	for _, sensitive := range []string{privatePrompt, "k1", upstream.URL, "192.0.2.1"} {
		if strings.Contains(logs.String(), sensitive) {
			t.Fatalf("request log contains sensitive value %q: %s", sensitive, logs.String())
		}
	}

	readyRequest := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readyResponse := httptest.NewRecorder()
	bridge.routes().ServeHTTP(readyResponse, readyRequest)
	if readyResponse.Code != http.StatusOK {
		t.Fatalf("ready status %d: %s", readyResponse.Code, readyResponse.Body.String())
	}
	for _, internal := range []string{upstream.URL, "custom", "default", "connections"} {
		if strings.Contains(readyResponse.Body.String(), internal) {
			t.Fatalf("public readiness exposed %q: %s", internal, readyResponse.Body.String())
		}
	}
}

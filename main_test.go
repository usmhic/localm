package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testConfig(baseURL string) Config {
	return Config{
		ListenAddr:         ":0",
		ProviderName:       "custom",
		UpstreamBaseURL:    strings.TrimRight(baseURL, "/") + "/v1",
		APIKeys:            []string{"k1"},
		AllowedModels:      map[string]struct{}{"qwen3:8b": {}},
		MaxBodyBytes:       1 << 20,
		MaxTokens:          100,
		DefaultMaxTokens:   20,
		UpstreamTimeout:    2 * time.Second,
		ShutdownTimeout:    time.Second,
		RateLimitPerKeyRPM: 10,
		RateLimitPerIPRPM:  10,
		MaxConcurrent:      2,
		ServerReadTimeout:  time.Second,
		ServerWriteTimeout: time.Second,
		ServerIdleTimeout:  time.Second,
		ReadinessTimeout:   time.Second,
	}
}

func doRequest(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+"k1")
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func TestPublicSwaggerDocs(t *testing.T) {
	bridge := newBridge(testConfig("http://example.invalid"), &http.Client{Timeout: time.Second})
	h := bridge.routes()

	docsReq := httptest.NewRequest(http.MethodGet, "/docs/", nil)
	docsRes := httptest.NewRecorder()
	h.ServeHTTP(docsRes, docsReq)
	if docsRes.Code != http.StatusOK {
		t.Fatalf("expected docs status 200, got %d", docsRes.Code)
	}
	if !strings.Contains(docsRes.Body.String(), "/openapi.json") {
		t.Fatal("expected Swagger UI to reference the OpenAPI document")
	}

	specReq := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	specRes := httptest.NewRecorder()
	h.ServeHTTP(specRes, specReq)
	if specRes.Code != http.StatusOK {
		t.Fatalf("expected OpenAPI status 200, got %d", specRes.Code)
	}
	var spec map[string]any
	if err := json.NewDecoder(specRes.Body).Decode(&spec); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}
	if spec["openapi"] != "3.0.3" {
		t.Fatalf("unexpected OpenAPI version: %v", spec["openapi"])
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI paths missing: %#v", spec["paths"])
	}
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/embeddings", "/v1/capabilities"} {
		if _, ok := paths[path]; !ok {
			t.Errorf("OpenAPI path %s is missing", path)
		}
	}
}

func TestUnauthorized(t *testing.T) {
	cfg := testConfig("http://example.invalid")
	bridge := newBridge(cfg, &http.Client{Timeout: time.Second})
	h := bridge.routes()

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"qwen3:8b","messages":[{"role":"user","content":"hi"}]}`))
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", res.Code)
	}
}

func TestModelAllowlist(t *testing.T) {
	cfg := testConfig("http://example.invalid")
	bridge := newBridge(cfg, &http.Client{Timeout: time.Second})
	res := doRequest(t, bridge.routes(), `{"model":"not-allowed","messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}

func TestMaxTokensLimit(t *testing.T) {
	cfg := testConfig("http://example.invalid")
	bridge := newBridge(cfg, &http.Client{Timeout: time.Second})
	res := doRequest(t, bridge.routes(), `{"model":"qwen3:8b","max_tokens":101,"messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}

func TestMaxCompletionTokensLimit(t *testing.T) {
	cfg := testConfig("http://example.invalid")
	bridge := newBridge(cfg, &http.Client{Timeout: time.Second})
	res := doRequest(t, bridge.routes(), `{"model":"qwen3:8b","max_completion_tokens":101,"messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}

func TestModelsReturnsAllowedModels(t *testing.T) {
	cfg := testConfig("http://example.invalid")
	cfg.AllowedModels["llama3.1:8b"] = struct{}{}
	bridge := newBridge(cfg, &http.Client{Timeout: time.Second})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer k1")
	res := httptest.NewRecorder()
	bridge.routes().ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), `"id":"qwen3:8b"`) || !strings.Contains(res.Body.String(), `"id":"llama3.1:8b"`) {
		t.Fatalf("unexpected model list: %s", res.Body.String())
	}
}

func TestRateLimitByKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	cfg.RateLimitPerKeyRPM = 1
	cfg.RateLimitPerIPRPM = 10
	bridge := newBridge(cfg, upstream.Client())
	h := bridge.routes()

	first := doRequest(t, h, `{"model":"qwen3:8b","messages":[{"role":"user","content":"hi"}]}`)
	if first.Code == http.StatusTooManyRequests {
		t.Fatalf("unexpected first request rate limit")
	}
	second := doRequest(t, h, `{"model":"qwen3:8b","messages":[{"role":"user","content":"hi"}]}`)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", second.Code)
	}
}

func TestNonStreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	bridge := newBridge(cfg, upstream.Client())
	res := doRequest(t, bridge.routes(), `{"model":"qwen3:8b","messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(bytes.NewReader(res.Body.Bytes())).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hello" {
		t.Fatalf("unexpected completion response: %+v", resp)
	}
}

func TestAgentToolPayloadPassesThrough(t *testing.T) {
	var upstreamPayload map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamPayload); err != nil {
			http.Error(w, "invalid payload", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-tool","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rabat\"}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	bridge := newBridge(cfg, upstream.Client())
	body := `{"model":"qwen3:8b","messages":[{"role":"user","content":"Weather in Rabat?"}],"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object"}}}]}`
	res := doRequest(t, bridge.routes(), body)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if _, ok := upstreamPayload["tools"]; !ok {
		t.Fatal("expected tools to pass through to the provider")
	}
	if !strings.Contains(res.Body.String(), `"finish_reason":"tool_calls"`) {
		t.Fatalf("expected tool call response to pass through: %s", res.Body.String())
	}
}

func TestStreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"he"}}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"choices":[{"delta":{"content":"llo"}}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	cfg := testConfig(upstream.URL)
	bridge := newBridge(cfg, upstream.Client())
	res := doRequest(t, bridge.routes(), `{"model":"qwen3:8b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	lines := []string{}
	s := bufio.NewScanner(bytes.NewReader(res.Body.Bytes()))
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "data: ") {
			lines = append(lines, line)
		}
	}
	if len(lines) < 3 {
		t.Fatalf("expected streaming chunks, got %v", lines)
	}
	if lines[len(lines)-1] != "data: [DONE]" {
		t.Fatalf("expected final [DONE], got %q", lines[len(lines)-1])
	}
}

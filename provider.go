package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// LLMProvider is the narrow boundary between the public gateway and a local
// inference server. Providers speak the OpenAI-compatible HTTP protocol, which
// lets the gateway pass through tools, multimodal messages, structured output,
// and future request fields without coupling the core server to one runtime.
type LLMProvider interface {
	Name() string
	Ready(context.Context) error
	Chat(context.Context, []byte) (*http.Response, error)
}

type openAICompatibleProvider struct {
	name    string
	baseURL string
	apiKey  string
	client  *http.Client
}

func newOpenAICompatibleProvider(name, baseURL, apiKey string, client *http.Client) LLMProvider {
	return &openAICompatibleProvider{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  client,
	}
}

func (p *openAICompatibleProvider) Name() string {
	return p.name
}

func (p *openAICompatibleProvider) Ready(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	p.setHeaders(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("provider readiness returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (p *openAICompatibleProvider) Chat(ctx context.Context, payload []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)
	return p.client.Do(req)
}

func (p *openAICompatibleProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

func relayProviderResponse(w http.ResponseWriter, resp *http.Response, stream bool) error {
	for _, header := range []string{"Content-Type", "Cache-Control", "X-Request-Id"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if !stream {
		_, err := io.Copy(w, resp.Body)
		return err
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming not supported by response writer")
	}
	buffer := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			flusher.Flush()
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func normalizeProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "lm-studio", "lm_studio":
		return "lmstudio"
	case "llama.cpp", "llama-cpp", "llama_cpp":
		return "llamacpp"
	case "openai", "openai-compatible", "custom":
		return "custom"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func resolveUpstreamBaseURL(provider string) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("UPSTREAM_BASE_URL")); configured != "" {
		return validateUpstreamBaseURL(configured)
	}

	var baseURL string
	switch provider {
	case "ollama":
		legacyURL := getEnv("OLLAMA_BASE_URL", "http://127.0.0.1:11434")
		baseURL = strings.TrimRight(legacyURL, "/")
		if !strings.HasSuffix(baseURL, "/v1") {
			baseURL += "/v1"
		}
	case "lmstudio":
		baseURL = "http://127.0.0.1:1234/v1"
	case "llamacpp", "localai":
		baseURL = "http://127.0.0.1:8080/v1"
	case "custom":
		return "", errors.New("UPSTREAM_BASE_URL is required when LLM_PROVIDER=custom")
	default:
		return "", fmt.Errorf("unsupported LLM_PROVIDER %q (use ollama, lmstudio, llamacpp, localai, or custom)", provider)
	}
	return validateUpstreamBaseURL(baseURL)
}

func validateUpstreamBaseURL(rawURL string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("UPSTREAM_BASE_URL must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return "", errors.New("UPSTREAM_BASE_URL must not contain credentials; use UPSTREAM_API_KEY")
	}
	return value, nil
}

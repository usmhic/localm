package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// LLMProvider is the narrow boundary between the public gateway and an
// OpenAI-compatible inference server. It deliberately exposes operations, not
// model lifecycle or workflow concepts.
type LLMProvider interface {
	Name() string
	Models(context.Context) ([]ProviderModel, error)
	Forward(context.Context, string, []byte) (*http.Response, error)
}

type ProviderModel struct {
	ID           string
	Capabilities CapabilitySet
}

type openAICompatibleProvider struct {
	name    string
	baseURL string
	apiKey  string
	client  *http.Client
}

func newOpenAICompatibleProvider(name, baseURL, apiKey string, client *http.Client) LLMProvider {
	return &openAICompatibleProvider{name: name, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: client}
}

func (p *openAICompatibleProvider) Name() string { return p.name }

func (p *openAICompatibleProvider) Models(ctx context.Context) ([]ProviderModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.setHeaders(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("provider readiness returned HTTP %d", resp.StatusCode)
	}
	var envelope struct {
		Data []struct {
			ID                string          `json:"id"`
			Capabilities      json.RawMessage `json:"capabilities"`
			SupportedFeatures json.RawMessage `json:"supported_features"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode provider models: %w", err)
	}
	models := make([]ProviderModel, 0, len(envelope.Data))
	for _, raw := range envelope.Data {
		if strings.TrimSpace(raw.ID) == "" {
			continue
		}
		capabilities := capabilityMetadata(raw.Capabilities, raw.SupportedFeatures)
		models = append(models, ProviderModel{ID: raw.ID, Capabilities: capabilities})
	}
	return models, nil
}

func capabilityMetadata(values ...json.RawMessage) CapabilitySet {
	capabilities := make(CapabilitySet)
	for _, raw := range values {
		var list []string
		if json.Unmarshal(raw, &list) == nil {
			for _, value := range list {
				addKnownCapability(capabilities, value, true)
			}
			continue
		}
		var flags map[string]bool
		if json.Unmarshal(raw, &flags) == nil {
			for value, supported := range flags {
				addKnownCapability(capabilities, value, supported)
			}
		}
	}
	return capabilities
}

func addKnownCapability(capabilities CapabilitySet, value string, supported bool) {
	capability := normalizeCapability(value)
	if _, known := knownCapabilities[capability]; known && supported {
		capabilities[capability] = true
	}
}

func (p *openAICompatibleProvider) Forward(ctx context.Context, operation string, payload []byte) (*http.Response, error) {
	path := ""
	switch operation {
	case CapabilityChatCompletions:
		path = "/chat/completions"
	case CapabilityResponses:
		path = "/responses"
	case CapabilityEmbeddings:
		path = "/embeddings"
	default:
		return nil, fmt.Errorf("unsupported provider operation %q", operation)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(payload))
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
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		status := resp.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		message, typ, code := normalizedUpstreamError(status)
		writeOpenAIError(w, status, message, typ, "", code)
		return nil
	}
	for _, header := range []string{"Content-Type", "Cache-Control"} {
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

func normalizedUpstreamError(status int) (string, string, string) {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "upstream rejected the request", "invalid_request_error", "upstream_invalid_request"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "upstream authentication failed", "server_error", "upstream_authentication_error"
	case http.StatusNotFound:
		return "upstream does not support this operation or model", "invalid_request_error", "upstream_not_supported"
	case http.StatusTooManyRequests:
		return "upstream rate limit exceeded", "rate_limit_error", "upstream_rate_limit_exceeded"
	default:
		return "upstream request failed", "server_error", "upstream_error"
	}
}

func normalizeProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "lm-studio", "lm_studio":
		return "lmstudio"
	case "llama.cpp", "llama-cpp", "llama_cpp":
		return "llamacpp"
	case "openai", "openai-compatible", "vllm", "custom":
		return "custom"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

func resolveUpstreamBaseURL(provider string) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("UPSTREAM_BASE_URL")); configured != "" {
		return validateUpstreamBaseURL(configured)
	}
	if provider == "ollama" {
		baseURL := strings.TrimRight(getEnv("OLLAMA_BASE_URL", "http://127.0.0.1:11434"), "/")
		if !strings.HasSuffix(baseURL, "/v1") {
			baseURL += "/v1"
		}
		return validateUpstreamBaseURL(baseURL)
	}
	return defaultProviderBaseURL(provider)
}

func defaultProviderBaseURL(provider string) (string, error) {
	var baseURL string
	switch provider {
	case "ollama":
		baseURL = "http://127.0.0.1:11434/v1"
	case "lmstudio":
		baseURL = "http://127.0.0.1:1234/v1"
	case "llamacpp", "localai":
		baseURL = "http://127.0.0.1:8080/v1"
	case "custom":
		return "", errors.New("base_url is required for a custom provider")
	default:
		return "", fmt.Errorf("unsupported provider %q (use ollama, lmstudio, llamacpp, localai, or custom)", provider)
	}
	return validateUpstreamBaseURL(baseURL)
}

func validateUpstreamBaseURL(rawURL string) (string, error) {
	value := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", errors.New("base URL must be an absolute http or https URL")
	}
	if parsed.User != nil {
		return "", errors.New("base URL must not contain credentials; use api_key_env")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("base URL must not contain a query or fragment")
	}
	return value, nil
}

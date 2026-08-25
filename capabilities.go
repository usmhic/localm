package main

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	CapabilityChatCompletions = "chat_completions"
	CapabilityResponses       = "responses"
	CapabilityEmbeddings      = "embeddings"
	CapabilityStreaming       = "streaming"
	CapabilityTools           = "tools"
	CapabilityStructured      = "structured_output"
	CapabilityReasoning       = "reasoning"
	CapabilityVision          = "vision"
)

var knownCapabilities = map[string]struct{}{
	CapabilityChatCompletions: {}, CapabilityResponses: {}, CapabilityEmbeddings: {},
	CapabilityStreaming: {}, CapabilityTools: {}, CapabilityStructured: {}, CapabilityReasoning: {},
	CapabilityVision: {},
}

type CapabilitySet map[string]bool

func defaultCapabilities(provider string) CapabilitySet {
	capabilities := CapabilitySet{CapabilityChatCompletions: true, CapabilityStreaming: true}
	switch provider {
	case "ollama", "lmstudio", "llamacpp", "localai", "custom":
		capabilities[CapabilityResponses] = true
		capabilities[CapabilityEmbeddings] = true
	}
	return capabilities
}

func compatibilityCapabilities(provider string) CapabilitySet {
	capabilities := defaultCapabilities(provider)
	capabilities[CapabilityTools] = true
	capabilities[CapabilityStructured] = true
	capabilities[CapabilityReasoning] = true
	capabilities[CapabilityVision] = true
	return capabilities
}

func parseCapabilities(values []string) (CapabilitySet, error) {
	result := make(CapabilitySet, len(values))
	for _, value := range values {
		capability := normalizeCapability(value)
		if _, ok := knownCapabilities[capability]; !ok {
			return nil, &unknownCapabilityError{value: value}
		}
		result[capability] = true
	}
	return result, nil
}

type unknownCapabilityError struct{ value string }

func (e *unknownCapabilityError) Error() string { return "unsupported capability " + e.value }

func normalizeCapability(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "chat", "chat_completion", "chat-completions":
		return CapabilityChatCompletions
	case "response", "responses_api":
		return CapabilityResponses
	case "embedding":
		return CapabilityEmbeddings
	case "stream":
		return CapabilityStreaming
	case "tool_calls", "function_calling", "tool_use", "trained_for_tool_use":
		return CapabilityTools
	case "json_schema", "json_mode", "structured":
		return CapabilityStructured
	case "thinking", "reasoning_content":
		return CapabilityReasoning
	case "multimodal", "image", "images", "image_input":
		return CapabilityVision
	default:
		return value
	}
}

func (c CapabilitySet) ContainsAll(required CapabilitySet) bool {
	for capability := range required {
		if !c[capability] {
			return false
		}
	}
	return true
}

func (c CapabilitySet) Sorted() []string {
	result := make([]string, 0, len(c))
	for capability, supported := range c {
		if supported {
			result = append(result, capability)
		}
	}
	sort.Strings(result)
	return result
}

type connection struct {
	config     ConnectionConfig
	provider   LLMProvider
	mu         sync.RWMutex
	discovered map[string]ProviderModel
	healthy    bool
	checkedAt  time.Time
}

func (c *connection) allowsModel(model string) bool { _, ok := c.config.Models[model]; return ok }

func (c *connection) routingEligible(model string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.checkedAt.IsZero() {
		return true
	}
	if !c.healthy {
		return false
	}
	_, available := c.discovered[model]
	return available
}

func (c *connection) capabilitiesForModel(model string) CapabilitySet {
	result := make(CapabilitySet, len(c.config.Capabilities))
	for capability, supported := range c.config.Capabilities {
		result[capability] = supported
	}
	c.mu.RLock()
	discovered, ok := c.discovered[model]
	c.mu.RUnlock()
	if ok && len(discovered.Capabilities) > 0 {
		for _, capability := range []string{CapabilityStreaming, CapabilityTools, CapabilityStructured, CapabilityReasoning, CapabilityVision} {
			result[capability] = discovered.Capabilities[capability] && c.config.Capabilities[capability]
		}
	}
	return result
}

func (c *connection) refresh(ctx context.Context, ttl time.Duration) {
	c.mu.RLock()
	fresh := !c.checkedAt.IsZero() && time.Since(c.checkedAt) < ttl
	c.mu.RUnlock()
	if fresh {
		return
	}
	models, err := c.provider.Models(ctx)
	discovered := make(map[string]ProviderModel, len(models))
	for _, model := range models {
		discovered[model.ID] = model
	}
	c.mu.Lock()
	c.checkedAt = time.Now()
	c.healthy = err == nil
	if err == nil {
		c.discovered = discovered
	}
	c.mu.Unlock()
}

func (c *connection) status() (bool, time.Time, map[string]ProviderModel) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	discovered := make(map[string]ProviderModel, len(c.discovered))
	for id, model := range c.discovered {
		discovered[id] = model
	}
	return c.healthy, c.checkedAt, discovered
}

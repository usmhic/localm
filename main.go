package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Bridge struct {
	cfg          Config
	connections  []*connection
	keyLimiter   *fixedWindowLimiter
	ipLimiter    *fixedWindowLimiter
	semaphore    chan struct{}
	queued       atomic.Int64
	shuttingDown atomic.Bool
}

type fixedWindowLimiter struct {
	mu          sync.Mutex
	limit       int
	window      time.Duration
	entries     map[string]windowEntry
	nextCleanup time.Time
}

type windowEntry struct {
	count int
	reset time.Time
}

type openAIErrorEnvelope struct {
	Error openAIError `json:"error"`
}
type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code,omitempty"`
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type inferenceSpec struct {
	operation     string
	requiredField string
	tokenFields   []string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	bridge := newBridge(cfg, nil)
	server := &http.Server{Addr: cfg.ListenAddr, Handler: bridge.routes(), ReadTimeout: cfg.ServerReadTimeout, WriteTimeout: cfg.ServerWriteTimeout, IdleTimeout: cfg.ServerIdleTimeout}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("{\"level\":\"info\",\"msg\":\"server_start\",\"addr\":%q,\"connections\":%d}", cfg.ListenAddr, len(cfg.Connections))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		log.Fatalf("server error: %v", err)
	case sig := <-sigCh:
		log.Printf("{\"level\":\"info\",\"msg\":\"shutdown_signal\",\"signal\":%q}", sig.String())
		bridge.shuttingDown.Store(true)
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Fatalf("shutdown error: %v", err)
		}
		log.Printf("{\"level\":\"info\",\"msg\":\"shutdown_complete\"}")
	}
}

func newBridge(cfg Config, client *http.Client) *Bridge {
	if client == nil {
		client = &http.Client{}
	}
	connectionConfigs := cfg.Connections
	if len(connectionConfigs) == 0 {
		connectionConfigs = []ConnectionConfig{{
			Name: defaultConnectionName, Provider: cfg.ProviderName, BaseURL: cfg.UpstreamBaseURL,
			APIKey: cfg.UpstreamAPIKey, Trust: "local", Models: cloneStringSet(cfg.AllowedModels),
			Capabilities: compatibilityCapabilities(cfg.ProviderName),
		}}
	}
	connections := make([]*connection, 0, len(connectionConfigs))
	for _, connectionConfig := range connectionConfigs {
		connections = append(connections, &connection{
			config:     connectionConfig,
			provider:   newOpenAICompatibleProvider(connectionConfig.Provider, connectionConfig.BaseURL, connectionConfig.APIKey, client),
			discovered: make(map[string]ProviderModel),
		})
	}
	sort.SliceStable(connections, func(i, j int) bool {
		if connections[i].config.Priority == connections[j].config.Priority {
			return connections[i].config.Name < connections[j].config.Name
		}
		return connections[i].config.Priority > connections[j].config.Priority
	})
	return &Bridge{cfg: cfg, connections: connections, keyLimiter: newFixedWindowLimiter(cfg.RateLimitPerKeyRPM, time.Minute), ipLimiter: newFixedWindowLimiter(cfg.RateLimitPerIPRPM, time.Minute), semaphore: make(chan struct{}, cfg.MaxConcurrent)}
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{limit: limit, window: window, entries: make(map[string]windowEntry), nextCleanup: time.Now().Add(window)}
}

func (l *fixedWindowLimiter) Allow(id string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !now.Before(l.nextCleanup) {
		for key, entry := range l.entries {
			if !now.Before(entry.reset) {
				delete(l.entries, key)
			}
		}
		l.nextCleanup = now.Add(l.window)
	}
	entry, ok := l.entries[id]
	if !ok || now.After(entry.reset) {
		l.entries[id] = windowEntry{count: 1, reset: now.Add(l.window)}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.entries[id] = entry
	return true
}

func (b *Bridge) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", docsRoot)
	mux.HandleFunc("/docs", swaggerDocs)
	mux.HandleFunc("/docs/", swaggerDocs)
	mux.HandleFunc("/openapi.json", openAPIDocument)
	mux.HandleFunc("/healthz", b.healthz)
	mux.HandleFunc("/readyz", b.readyz)
	mux.HandleFunc("/v1/models", b.models)
	mux.HandleFunc("/v1/capabilities", b.capabilities)
	mux.HandleFunc("/v1/chat/completions", b.inference(inferenceSpec{operation: CapabilityChatCompletions, requiredField: "messages", tokenFields: []string{"max_tokens", "max_completion_tokens"}}))
	mux.HandleFunc("/v1/responses", b.inference(inferenceSpec{operation: CapabilityResponses, requiredField: "input", tokenFields: []string{"max_output_tokens"}}))
	mux.HandleFunc("/v1/embeddings", b.inference(inferenceSpec{operation: CapabilityEmbeddings, requiredField: "input"}))
	return b.withLogging(b.withCORS(mux))
}

func (b *Bridge) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.Printf("{\"level\":\"info\",\"msg\":\"request\",\"method\":%q,\"path\":%q,\"status\":%d,\"duration_ms\":%d}", r.Method, r.URL.Path, rec.status, time.Since(start).Milliseconds())
	})
}

func (b *Bridge) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !b.cfg.CORSEnabled {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && b.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (b *Bridge) originAllowed(origin string) bool {
	if _, ok := b.cfg.CORSAllowedOrigins["*"]; ok {
		return true
	}
	_, ok := b.cfg.CORSAllowedOrigins[origin]
	return ok
}

func (b *Bridge) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (b *Bridge) readyz(w http.ResponseWriter, r *http.Request) {
	if b.shuttingDown.Load() {
		writeOpenAIError(w, http.StatusServiceUnavailable, "service shutting down", "server_error", "", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), b.cfg.ReadinessTimeout)
	defer cancel()
	ready := 0
	b.refreshConnections(ctx)
	for _, connection := range b.connections {
		healthy, _, _ := connection.status()
		if healthy {
			ready++
		}
	}
	if ready == 0 {
		writeOpenAIError(w, http.StatusServiceUnavailable, "no upstream connection is available", "server_error", "", "upstream_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (b *Bridge) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "", "")
		return
	}
	if _, ok := b.authorizedAPIKey(r); !ok {
		writeOpenAIError(w, http.StatusUnauthorized, "invalid authentication credentials", "authentication_error", "", "invalid_api_key")
		return
	}
	models := make([]string, 0, len(b.cfg.AllowedModels))
	for model := range b.cfg.AllowedModels {
		models = append(models, model)
	}
	sort.Strings(models)
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		ownedBy := "localm"
		if connection := b.firstConnectionForModel(model); connection != nil {
			ownedBy = connection.config.Name
		}
		data = append(data, map[string]any{"id": model, "object": "model", "created": 0, "owned_by": ownedBy})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (b *Bridge) capabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeOpenAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "", "")
		return
	}
	if _, ok := b.authorizedAPIKey(r); !ok {
		writeOpenAIError(w, http.StatusUnauthorized, "invalid authentication credentials", "authentication_error", "", "invalid_api_key")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), b.cfg.ReadinessTimeout)
	defer cancel()
	data := make([]map[string]any, 0, len(b.connections))
	b.refreshConnections(ctx)
	for _, connection := range b.connections {
		healthy, checkedAt, discovered := connection.status()
		modelIDs := make([]string, 0, len(connection.config.Models))
		for model := range connection.config.Models {
			modelIDs = append(modelIDs, model)
		}
		sort.Strings(modelIDs)
		models := make([]map[string]any, 0, len(modelIDs))
		for _, model := range modelIDs {
			_, available := discovered[model]
			models = append(models, map[string]any{"id": model, "available": healthy && available, "capabilities": connection.capabilitiesForModel(model).Sorted()})
		}
		item := map[string]any{"name": connection.config.Name, "provider": connection.config.Provider, "priority": connection.config.Priority, "trust": connection.config.Trust, "healthy": healthy, "models": models}
		if !checkedAt.IsZero() {
			item["checked_at"] = checkedAt.UTC().Format(time.RFC3339)
		}
		if isPaused, until := connection.pausedAt(time.Now()); isPaused {
			item["paused_until"] = until.UTC().Format(time.RFC3339)
		}
		data = append(data, item)
	}
	load := map[string]any{"active": len(b.semaphore), "max_concurrent": cap(b.semaphore), "queued": b.queued.Load(), "max_queued": b.cfg.MaxQueued}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data, "load": load})
}

// acquireSlot takes an upstream slot, waiting up to QUEUE_TIMEOUT in a bounded
// queue when MAX_QUEUED_LLM allows it. It returns false when the gateway is
// saturated, so the client can retry or send the request elsewhere.
func (b *Bridge) acquireSlot(ctx context.Context) (func(), bool) {
	release := func() { <-b.semaphore }
	select {
	case b.semaphore <- struct{}{}:
		return release, true
	default:
	}
	if b.cfg.MaxQueued <= 0 {
		return nil, false
	}
	if b.queued.Add(1) > int64(b.cfg.MaxQueued) {
		b.queued.Add(-1)
		return nil, false
	}
	defer b.queued.Add(-1)
	timer := time.NewTimer(b.cfg.QueueTimeout)
	defer timer.Stop()
	select {
	case b.semaphore <- struct{}{}:
		return release, true
	case <-timer.C:
		return nil, false
	case <-ctx.Done():
		return nil, false
	}
}

func (b *Bridge) inference(spec inferenceSpec) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeOpenAIError(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", "", "")
			return
		}
		if b.shuttingDown.Load() {
			writeOpenAIError(w, http.StatusServiceUnavailable, "service shutting down", "server_error", "", "")
			return
		}
		apiKey, ok := b.authorizedAPIKey(r)
		if !ok {
			writeOpenAIError(w, http.StatusUnauthorized, "invalid authentication credentials", "authentication_error", "", "invalid_api_key")
			return
		}
		now := time.Now()
		if !b.keyLimiter.Allow(apiKey, now) {
			writeOpenAIError(w, http.StatusTooManyRequests, "rate limit exceeded for api key", "rate_limit_error", "", "rate_limit_exceeded")
			return
		}
		if !b.ipLimiter.Allow(clientIP(r), now) {
			writeOpenAIError(w, http.StatusTooManyRequests, "rate limit exceeded for ip", "rate_limit_error", "", "rate_limit_exceeded")
			return
		}
		release, ok := b.acquireSlot(r.Context())
		if !ok {
			writeOpenAIError(w, http.StatusTooManyRequests, "too many concurrent requests", "rate_limit_error", "", "concurrency_limit_exceeded")
			return
		}
		defer release()

		r.Body = http.MaxBytesReader(w, r.Body, b.cfg.MaxBodyBytes)
		defer r.Body.Close()
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid request body", "invalid_request_error", "", "")
			return
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid JSON body", "invalid_request_error", "", "")
			return
		}
		model, ok := requiredStringField(fields, "model")
		if !ok {
			writeOpenAIError(w, http.StatusBadRequest, "model is required", "invalid_request_error", "model", "")
			return
		}
		if _, allowed := b.cfg.AllowedModels[model]; !allowed {
			writeOpenAIError(w, http.StatusBadRequest, "model is not allowed", "invalid_request_error", "model", "model_not_allowed")
			return
		}
		if !presentJSONField(fields, spec.requiredField) {
			writeOpenAIError(w, http.StatusBadRequest, spec.requiredField+" is required", "invalid_request_error", spec.requiredField, "")
			return
		}
		if err := b.validateAndDefaultTokens(fields, spec.tokenFields); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error", tokenErrorParam(err), "")
			return
		}
		payload, err = json.Marshal(fields)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "failed to prepare upstream request", "server_error", "", "")
			return
		}
		required, stream := requiredCapabilities(spec.operation, fields)
		candidates, missing := b.selectConnections(model, required, time.Now())
		if len(candidates) == 0 {
			if missing != "" {
				writeOpenAIError(w, http.StatusBadRequest, "requested feature is not supported for this model", "invalid_request_error", missing, "unsupported_feature")
			} else {
				writeOpenAIError(w, http.StatusServiceUnavailable, "no connection serves this model", "server_error", "model", "model_unavailable")
			}
			return
		}
		if !b.cfg.Failover {
			candidates = candidates[:1]
		}
		ctx, cancel := context.WithTimeout(r.Context(), b.cfg.UpstreamTimeout)
		defer cancel()
		b.forward(ctx, w, r, spec.operation, payload, stream, candidates)
	}
}

// forward sends the request to the first candidate and, while nothing has been
// relayed to the client yet, moves to the next one when a connection can't be
// reached or answers 429/5xx. A connection that fails is paused (see
// recordFailure). The last candidate's answer is relayed whatever it is.
func (b *Bridge) forward(ctx context.Context, w http.ResponseWriter, r *http.Request, operation string, payload []byte, stream bool, candidates []*connection) {
	for i, selected := range candidates {
		last := i == len(candidates)-1
		upstreamResp, err := selected.provider.Forward(ctx, operation, payload)
		if err != nil {
			if r.Context().Err() != nil {
				return // The client went away; there is nobody to answer.
			}
			selected.recordFailure(time.Now(), b.cfg.FailurePause, b.cfg.FailurePauseMax)
			if !last && ctx.Err() == nil {
				continue
			}
			writeOpenAIError(w, http.StatusBadGateway, "upstream request failed", "server_error", "", "upstream_error")
			return
		}
		status := upstreamResp.StatusCode
		if !last && retryableUpstreamStatus(status) {
			_, _ = io.Copy(io.Discard, io.LimitReader(upstreamResp.Body, 1<<20))
			upstreamResp.Body.Close()
			if status >= http.StatusInternalServerError {
				selected.recordFailure(time.Now(), b.cfg.FailurePause, b.cfg.FailurePauseMax)
			}
			continue
		}
		if status < http.StatusInternalServerError {
			selected.recordSuccess()
		} else {
			selected.recordFailure(time.Now(), b.cfg.FailurePause, b.cfg.FailurePauseMax)
		}
		err = relayProviderResponse(w, upstreamResp, stream)
		upstreamResp.Body.Close()
		if err != nil {
			log.Printf("{\"level\":\"error\",\"msg\":\"upstream_response_error\",\"connection\":%q,\"provider\":%q,\"error\":\"relay_failed\"}", selected.config.Name, selected.provider.Name())
		}
		return
	}
}

// retryableUpstreamStatus reports whether another connection may succeed where
// this one answered: it was busy or failed, rather than rejecting the request.
func retryableUpstreamStatus(status int) bool {
	return status == http.StatusTooManyRequests || status == http.StatusInternalServerError ||
		status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

type tokenValidationError struct{ param, message string }

func (e *tokenValidationError) Error() string { return e.message }
func tokenErrorParam(err error) string {
	var tokenErr *tokenValidationError
	if errors.As(err, &tokenErr) {
		return tokenErr.param
	}
	return ""
}

func (b *Bridge) validateAndDefaultTokens(fields map[string]json.RawMessage, names []string) error {
	found := false
	for _, name := range names {
		raw, ok := fields[name]
		if !ok || !presentJSONField(fields, name) {
			continue
		}
		found = true
		var value int
		if err := json.Unmarshal(raw, &value); err != nil || value <= 0 || value > b.cfg.MaxTokens {
			return &tokenValidationError{param: name, message: fmt.Sprintf("%s must be between 1 and %d", name, b.cfg.MaxTokens)}
		}
	}
	if !found && len(names) > 0 {
		fields[names[0]] = json.RawMessage(strconv.Itoa(b.cfg.DefaultMaxTokens))
	}
	return nil
}

func (b *Bridge) refreshConnections(ctx context.Context) {
	var group sync.WaitGroup
	group.Add(len(b.connections))
	for _, item := range b.connections {
		connection := item
		go func() {
			defer group.Done()
			connection.refresh(ctx, b.cfg.DiscoveryTTL)
		}()
	}
	group.Wait()
}

func requiredCapabilities(operation string, fields map[string]json.RawMessage) (CapabilitySet, bool) {
	required := CapabilitySet{operation: true}
	stream := boolField(fields, "stream")
	if stream {
		required[CapabilityStreaming] = true
	}
	if presentJSONField(fields, "tools") || presentJSONField(fields, "tool_choice") || containsJSONKey(fields, "function_call_output") {
		required[CapabilityTools] = true
	}
	if presentJSONField(fields, "response_format") || nestedFieldPresent(fields, "text", "format") {
		required[CapabilityStructured] = true
	}
	if presentJSONField(fields, "reasoning") || presentJSONField(fields, "reasoning_effort") {
		required[CapabilityReasoning] = true
	}
	if containsJSONKey(fields, "image_url") || containsJSONKey(fields, "input_image") {
		required[CapabilityVision] = true
	}
	return required, stream
}

// selectConnections returns the connections that may serve the request, in
// routing order: by priority, with paused connections after every other one, so
// a failing connection is still tried when nothing else can serve. When none
// qualifies, missing names a required capability no serving connection has.
func (b *Bridge) selectConnections(model string, required CapabilitySet, now time.Time) ([]*connection, string) {
	served := false
	missingSet := make(map[string]struct{})
	var ready, paused []*connection
	for _, connection := range b.connections {
		if !connection.allowsModel(model) || !connection.routingEligible(model) {
			continue
		}
		served = true
		capabilities := connection.capabilitiesForModel(model)
		if !capabilities.ContainsAll(required) {
			for capability := range required {
				if !capabilities[capability] {
					missingSet[capability] = struct{}{}
				}
			}
			continue
		}
		if isPaused, _ := connection.pausedAt(now); isPaused {
			paused = append(paused, connection)
		} else {
			ready = append(ready, connection)
		}
	}
	if candidates := append(ready, paused...); len(candidates) > 0 {
		return candidates, ""
	}
	if !served {
		return nil, ""
	}
	missing := make([]string, 0, len(missingSet))
	for capability := range missingSet {
		missing = append(missing, capability)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		return nil, missing[0]
	}
	return nil, ""
}

func (b *Bridge) firstConnectionForModel(model string) *connection {
	for _, connection := range b.connections {
		if connection.allowsModel(model) {
			return connection
		}
	}
	return nil
}

func requiredStringField(fields map[string]json.RawMessage, name string) (string, bool) {
	raw, ok := fields[name]
	if !ok {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func presentJSONField(fields map[string]json.RawMessage, name string) bool {
	raw, ok := fields[name]
	return ok && len(raw) > 0 && string(raw) != "null" && string(raw) != "[]" && string(raw) != "{}"
}

func boolField(fields map[string]json.RawMessage, name string) bool {
	var value bool
	_ = json.Unmarshal(fields[name], &value)
	return value
}

func nestedFieldPresent(fields map[string]json.RawMessage, parent, child string) bool {
	var nested map[string]json.RawMessage
	return json.Unmarshal(fields[parent], &nested) == nil && presentJSONField(nested, child)
}

func containsJSONKey(fields map[string]json.RawMessage, value string) bool {
	for _, raw := range fields {
		if strings.Contains(string(raw), `"`+value+`"`) {
			return true
		}
	}
	return false
}

func writeOpenAIError(w http.ResponseWriter, status int, message, typ, param, code string) {
	writeJSON(w, status, openAIErrorEnvelope{Error: openAIError{Message: message, Type: typ, Param: param, Code: code}})
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func extractBearerToken(header string) (string, bool) {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", false
	}
	return strings.TrimSpace(parts[1]), true
}

func (b *Bridge) authorizedAPIKey(r *http.Request) (string, bool) {
	apiKey, ok := extractBearerToken(r.Header.Get("Authorization"))
	if !ok || !isAuthorizedAPIKey(apiKey, b.cfg.APIKeys) {
		return "", false
	}
	return apiKey, true
}

func isAuthorizedAPIKey(given string, allowed []string) bool {
	if given == "" || len(allowed) == 0 {
		return false
	}
	match := 0
	for _, key := range allowed {
		match |= constantTimeStringEqual(given, key)
	}
	return match != 0
}

func constantTimeStringEqual(a, b string) int {
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	var diff byte
	for i := 0; i < maxLen; i++ {
		var ab, bb byte
		if i < len(a) {
			ab = a[i]
		}
		if i < len(b) {
			bb = b[i]
		}
		diff |= ab ^ bb
	}
	return subtle.ConstantTimeEq(int32(len(a)), int32(len(b))) & subtle.ConstantTimeByteEq(diff, 0)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

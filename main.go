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

type Config struct {
	ListenAddr         string
	ProviderName       string
	UpstreamBaseURL    string
	UpstreamAPIKey     string
	APIKeys            []string
	AllowedModels      map[string]struct{}
	MaxBodyBytes       int64
	MaxTokens          int
	DefaultMaxTokens   int
	UpstreamTimeout    time.Duration
	ShutdownTimeout    time.Duration
	RateLimitPerKeyRPM int
	RateLimitPerIPRPM  int
	MaxConcurrent      int
	CORSEnabled        bool
	CORSAllowedOrigins map[string]struct{}
	ServerReadTimeout  time.Duration
	ServerWriteTimeout time.Duration
	ServerIdleTimeout  time.Duration
	ReadinessTimeout   time.Duration
}

type Bridge struct {
	cfg          Config
	provider     LLMProvider
	keyLimiter   *fixedWindowLimiter
	ipLimiter    *fixedWindowLimiter
	semaphore    chan struct{}
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

type chatCompletionRequest struct {
	Model               string            `json:"model"`
	Messages            []json.RawMessage `json:"messages"`
	Stream              bool              `json:"stream"`
	MaxTokens           *int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int              `json:"max_completion_tokens,omitempty"`
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

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func loadConfig() (Config, error) {
	providerName := normalizeProviderName(getEnv("LLM_PROVIDER", "ollama"))
	upstreamBaseURL, err := resolveUpstreamBaseURL(providerName)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ListenAddr:         getEnv("LISTEN_ADDR", ":8080"),
		ProviderName:       providerName,
		UpstreamBaseURL:    upstreamBaseURL,
		UpstreamAPIKey:     getEnv("UPSTREAM_API_KEY", ""),
		MaxBodyBytes:       getEnvInt64("MAX_BODY_BYTES", 1<<20),
		MaxTokens:          getEnvInt("MAX_TOKENS", 1024),
		DefaultMaxTokens:   getEnvInt("DEFAULT_MAX_TOKENS", 512),
		UpstreamTimeout:    getEnvDuration("UPSTREAM_TIMEOUT", 120*time.Second),
		ShutdownTimeout:    getEnvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		RateLimitPerKeyRPM: getEnvInt("RATE_LIMIT_PER_KEY_RPM", 60),
		RateLimitPerIPRPM:  getEnvInt("RATE_LIMIT_PER_IP_RPM", 120),
		MaxConcurrent:      getEnvInt("MAX_CONCURRENT_LLM", getEnvInt("MAX_CONCURRENT_OLLAMA", 4)),
		CORSEnabled:        getEnvBool("CORS_ENABLED", false),
		ServerReadTimeout:  getEnvDuration("SERVER_READ_TIMEOUT", 15*time.Second),
		ServerWriteTimeout: getEnvDuration("SERVER_WRITE_TIMEOUT", 180*time.Second),
		ServerIdleTimeout:  getEnvDuration("SERVER_IDLE_TIMEOUT", 60*time.Second),
		ReadinessTimeout:   getEnvDuration("READINESS_TIMEOUT", 3*time.Second),
	}
	cfg.APIKeys = parseCSV(getEnv("API_KEYS", ""))
	if len(cfg.APIKeys) == 0 {
		return Config{}, errors.New("API_KEYS must include at least one key")
	}
	cfg.AllowedModels = make(map[string]struct{})
	for _, m := range parseCSV(getEnv("ALLOWED_MODELS", "qwen3:8b")) {
		cfg.AllowedModels[m] = struct{}{}
	}
	if len(cfg.AllowedModels) == 0 {
		return Config{}, errors.New("ALLOWED_MODELS must include at least one model")
	}
	if cfg.MaxTokens <= 0 || cfg.DefaultMaxTokens <= 0 {
		return Config{}, errors.New("MAX_TOKENS and DEFAULT_MAX_TOKENS must be positive")
	}
	if cfg.DefaultMaxTokens > cfg.MaxTokens {
		cfg.DefaultMaxTokens = cfg.MaxTokens
	}
	if cfg.MaxConcurrent <= 0 {
		return Config{}, errors.New("MAX_CONCURRENT_LLM must be positive")
	}
	if cfg.RateLimitPerKeyRPM <= 0 || cfg.RateLimitPerIPRPM <= 0 {
		return Config{}, errors.New("rate limits must be positive")
	}
	cfg.CORSAllowedOrigins = make(map[string]struct{})
	for _, o := range parseCSV(getEnv("CORS_ALLOWED_ORIGINS", "")) {
		cfg.CORSAllowedOrigins[o] = struct{}{}
	}
	return cfg, nil
}

func newBridge(cfg Config, client *http.Client) *Bridge {
	if client == nil {
		client = &http.Client{}
	}
	return &Bridge{
		cfg:        cfg,
		provider:   newOpenAICompatibleProvider(cfg.ProviderName, cfg.UpstreamBaseURL, cfg.UpstreamAPIKey, client),
		keyLimiter: newFixedWindowLimiter(cfg.RateLimitPerKeyRPM, time.Minute),
		ipLimiter:  newFixedWindowLimiter(cfg.RateLimitPerIPRPM, time.Minute),
		semaphore:  make(chan struct{}, cfg.MaxConcurrent),
	}
}

func newFixedWindowLimiter(limit int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		limit:       limit,
		window:      window,
		entries:     make(map[string]windowEntry),
		nextCleanup: time.Now().Add(window),
	}
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

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}
	bridge := newBridge(cfg, nil)
	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      bridge.routes(),
		ReadTimeout:  cfg.ServerReadTimeout,
		WriteTimeout: cfg.ServerWriteTimeout,
		IdleTimeout:  cfg.ServerIdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("{\"level\":\"info\",\"msg\":\"server_start\",\"addr\":%q}", cfg.ListenAddr)
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

func (b *Bridge) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", docsRoot)
	mux.HandleFunc("/docs", swaggerDocs)
	mux.HandleFunc("/docs/", swaggerDocs)
	mux.HandleFunc("/openapi.json", openAPIDocument)
	mux.HandleFunc("/healthz", b.healthz)
	mux.HandleFunc("/readyz", b.readyz)
	mux.HandleFunc("/v1/models", b.models)
	mux.HandleFunc("/v1/chat/completions", b.chatCompletions)
	return b.withLogging(b.withCORS(mux))
}

func (b *Bridge) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		ip := clientIP(r)
		log.Printf("{\"level\":\"info\",\"msg\":\"request\",\"method\":%q,\"path\":%q,\"status\":%d,\"duration_ms\":%d,\"ip\":%q}",
			r.Method, r.URL.Path, rec.status, time.Since(start).Milliseconds(), ip)
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
	if err := b.provider.Ready(ctx); err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "upstream unavailable", "server_error", "", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "provider": b.provider.Name()})
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
		data = append(data, map[string]any{
			"id":       model,
			"object":   "model",
			"created":  0,
			"owned_by": b.provider.Name(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (b *Bridge) chatCompletions(w http.ResponseWriter, r *http.Request) {
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
	ip := clientIP(r)
	if !b.ipLimiter.Allow(ip, now) {
		writeOpenAIError(w, http.StatusTooManyRequests, "rate limit exceeded for ip", "rate_limit_error", "", "rate_limit_exceeded")
		return
	}

	select {
	case b.semaphore <- struct{}{}:
		defer func() { <-b.semaphore }()
	default:
		writeOpenAIError(w, http.StatusTooManyRequests, "too many concurrent requests", "rate_limit_error", "", "concurrency_limit_exceeded")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, b.cfg.MaxBodyBytes)
	defer r.Body.Close()

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid request body", "invalid_request_error", "", "")
		return
	}
	var req chatCompletionRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid JSON body", "invalid_request_error", "", "")
		return
	}
	if req.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "model is required", "invalid_request_error", "model", "")
		return
	}
	if _, ok := b.cfg.AllowedModels[req.Model]; !ok {
		writeOpenAIError(w, http.StatusBadRequest, "model is not allowed", "invalid_request_error", "model", "model_not_allowed")
		return
	}
	if len(req.Messages) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "messages are required", "invalid_request_error", "messages", "")
		return
	}

	if req.MaxTokens != nil && (*req.MaxTokens <= 0 || *req.MaxTokens > b.cfg.MaxTokens) {
		writeOpenAIError(w, http.StatusBadRequest, fmt.Sprintf("max_tokens must be between 1 and %d", b.cfg.MaxTokens), "invalid_request_error", "max_tokens", "")
		return
	}
	if req.MaxCompletionTokens != nil && (*req.MaxCompletionTokens <= 0 || *req.MaxCompletionTokens > b.cfg.MaxTokens) {
		writeOpenAIError(w, http.StatusBadRequest, fmt.Sprintf("max_completion_tokens must be between 1 and %d", b.cfg.MaxTokens), "invalid_request_error", "max_completion_tokens", "")
		return
	}

	if req.MaxTokens == nil && req.MaxCompletionTokens == nil {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid JSON body", "invalid_request_error", "", "")
			return
		}
		fields["max_tokens"] = json.RawMessage(strconv.Itoa(b.cfg.DefaultMaxTokens))
		payload, err = json.Marshal(fields)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, "failed to prepare upstream request", "server_error", "", "")
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), b.cfg.UpstreamTimeout)
	defer cancel()

	upstreamResp, err := b.provider.Chat(ctx, payload)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream request failed", "server_error", "", "")
		return
	}
	defer upstreamResp.Body.Close()
	if err := relayProviderResponse(w, upstreamResp, req.Stream); err != nil {
		log.Printf("{\"level\":\"error\",\"msg\":\"upstream_response_error\",\"provider\":%q,\"error\":%q}", b.provider.Name(), err.Error())
	}
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
	sameLen := subtle.ConstantTimeEq(int32(len(a)), int32(len(b)))
	sameContent := subtle.ConstantTimeByteEq(diff, 0)
	return sameLen & sameContent
}

func parseCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func getEnv(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvInt64(name string, fallback int64) int64 {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvBool(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func getEnvDuration(name string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

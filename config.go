package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultConnectionName = "default"

var connectionNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)
var environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Config struct {
	ListenAddr         string
	ProviderName       string // Legacy single-connection compatibility.
	UpstreamBaseURL    string // Legacy single-connection compatibility.
	UpstreamAPIKey     string // Legacy single-connection compatibility.
	Connections        []ConnectionConfig
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
	DiscoveryTTL       time.Duration
}

type ConnectionConfig struct {
	Name         string
	Provider     string
	BaseURL      string
	APIKey       string
	Priority     int
	Trust        string
	Models       map[string]struct{}
	Capabilities CapabilitySet
}

type fileConfig struct {
	AllowRemote bool                            `yaml:"allow_remote"`
	Connections map[string]fileConnectionConfig `yaml:"connections"`
}

type fileConnectionConfig struct {
	Provider     string   `yaml:"provider"`
	BaseURL      string   `yaml:"base_url"`
	APIKeyEnv    string   `yaml:"api_key_env"`
	Priority     int      `yaml:"priority"`
	Trust        string   `yaml:"trust"`
	Models       []string `yaml:"models"`
	Capabilities []string `yaml:"capabilities"`
}

func loadConfig() (Config, error) {
	cfg := Config{
		ListenAddr:         getEnv("LISTEN_ADDR", ":8080"),
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
		DiscoveryTTL:       getEnvDuration("CAPABILITY_DISCOVERY_TTL", 30*time.Second),
	}
	cfg.APIKeys = parseCSV(getEnv("API_KEYS", ""))
	if len(cfg.APIKeys) == 0 {
		return Config{}, errors.New("API_KEYS must include at least one key")
	}
	cfg.AllowedModels = stringSet(parseCSV(getEnv("ALLOWED_MODELS", "qwen3:8b")))
	if len(cfg.AllowedModels) == 0 {
		return Config{}, errors.New("ALLOWED_MODELS must include at least one model")
	}
	if err := validateCommonConfig(&cfg); err != nil {
		return Config{}, err
	}
	cfg.CORSAllowedOrigins = stringSet(parseCSV(getEnv("CORS_ALLOWED_ORIGINS", "")))

	configPath := strings.TrimSpace(os.Getenv("LOCALM_CONFIG"))
	if configPath == "" {
		connection, err := legacyConnectionConfig(cfg.AllowedModels)
		if err != nil {
			return Config{}, err
		}
		cfg.ProviderName = connection.Provider
		cfg.UpstreamBaseURL = connection.BaseURL
		cfg.UpstreamAPIKey = connection.APIKey
		cfg.Connections = []ConnectionConfig{connection}
		return cfg, nil
	}

	connections, err := loadConnectionsFile(configPath, cfg.AllowedModels)
	if err != nil {
		return Config{}, err
	}
	cfg.Connections = connections
	return cfg, nil
}

func validateCommonConfig(cfg *Config) error {
	if cfg.MaxBodyBytes <= 0 {
		return errors.New("MAX_BODY_BYTES must be positive")
	}
	if cfg.MaxTokens <= 0 || cfg.DefaultMaxTokens <= 0 {
		return errors.New("MAX_TOKENS and DEFAULT_MAX_TOKENS must be positive")
	}
	if cfg.DefaultMaxTokens > cfg.MaxTokens {
		cfg.DefaultMaxTokens = cfg.MaxTokens
	}
	if cfg.MaxConcurrent <= 0 {
		return errors.New("MAX_CONCURRENT_LLM must be positive")
	}
	if cfg.RateLimitPerKeyRPM <= 0 || cfg.RateLimitPerIPRPM <= 0 {
		return errors.New("rate limits must be positive")
	}
	if cfg.DiscoveryTTL <= 0 {
		return errors.New("CAPABILITY_DISCOVERY_TTL must be positive")
	}
	return nil
}

func legacyConnectionConfig(allowedModels map[string]struct{}) (ConnectionConfig, error) {
	provider := normalizeProviderName(getEnv("LLM_PROVIDER", "ollama"))
	baseURL, err := resolveUpstreamBaseURL(provider)
	if err != nil {
		return ConnectionConfig{}, err
	}
	trust := "local"
	if !isLocalBaseURL(baseURL) {
		// UPSTREAM_BASE_URL has always supported remote servers. Its explicit use
		// remains compatible and is represented accurately in capability output.
		trust = "remote"
	}
	return ConnectionConfig{
		Name:         defaultConnectionName,
		Provider:     provider,
		BaseURL:      baseURL,
		APIKey:       getEnv("UPSTREAM_API_KEY", ""),
		Priority:     0,
		Trust:        trust,
		Models:       cloneStringSet(allowedModels),
		Capabilities: compatibilityCapabilities(provider),
	}, nil
}

func loadConnectionsFile(path string, allowedModels map[string]struct{}) ([]ConnectionConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read LOCALM_CONFIG: %w", err)
	}
	var file fileConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse LOCALM_CONFIG: %w", err)
	}
	if len(file.Connections) == 0 {
		return nil, errors.New("LOCALM_CONFIG must define at least one connection")
	}

	names := make([]string, 0, len(file.Connections))
	for name := range file.Connections {
		names = append(names, name)
	}
	sort.Strings(names)
	connections := make([]ConnectionConfig, 0, len(names))
	for _, name := range names {
		if !connectionNamePattern.MatchString(name) {
			return nil, fmt.Errorf("connection %q has an invalid name", name)
		}
		connection, err := buildConnectionConfig(name, file.Connections[name], file.AllowRemote, allowedModels)
		if err != nil {
			return nil, err
		}
		connections = append(connections, connection)
	}
	sort.SliceStable(connections, func(i, j int) bool {
		if connections[i].Priority == connections[j].Priority {
			return connections[i].Name < connections[j].Name
		}
		return connections[i].Priority > connections[j].Priority
	})
	return connections, nil
}

func buildConnectionConfig(name string, raw fileConnectionConfig, allowRemote bool, allowedModels map[string]struct{}) (ConnectionConfig, error) {
	provider := normalizeProviderName(raw.Provider)
	if provider == "" {
		provider = "ollama"
	}
	baseURL := strings.TrimSpace(raw.BaseURL)
	var err error
	if baseURL == "" {
		baseURL, err = defaultProviderBaseURL(provider)
	} else {
		baseURL, err = validateUpstreamBaseURL(baseURL)
	}
	if err != nil {
		return ConnectionConfig{}, fmt.Errorf("connection %q: %w", name, err)
	}

	trust := strings.ToLower(strings.TrimSpace(raw.Trust))
	if trust == "" {
		trust = "local"
	}
	if trust != "local" && trust != "remote" {
		return ConnectionConfig{}, fmt.Errorf("connection %q: trust must be local or remote", name)
	}
	if !isLocalBaseURL(baseURL) && trust != "remote" {
		return ConnectionConfig{}, fmt.Errorf("connection %q: a non-local base_url requires trust: remote", name)
	}
	if trust == "remote" && !allowRemote {
		return ConnectionConfig{}, fmt.Errorf("connection %q: remote routing requires allow_remote: true", name)
	}

	apiKey := ""
	if raw.APIKeyEnv != "" {
		if !environmentNamePattern.MatchString(raw.APIKeyEnv) {
			return ConnectionConfig{}, fmt.Errorf("connection %q: api_key_env is not a valid environment variable name", name)
		}
		apiKey = os.Getenv(raw.APIKeyEnv)
		if apiKey == "" {
			return ConnectionConfig{}, fmt.Errorf("connection %q: environment variable %s is empty", name, raw.APIKeyEnv)
		}
	}

	models := cloneStringSet(allowedModels)
	if len(raw.Models) > 0 {
		models = make(map[string]struct{})
		for _, model := range raw.Models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if _, allowed := allowedModels[model]; !allowed {
				return ConnectionConfig{}, fmt.Errorf("connection %q: model %q is not in ALLOWED_MODELS", name, model)
			}
			models[model] = struct{}{}
		}
		if len(models) == 0 {
			return ConnectionConfig{}, fmt.Errorf("connection %q: models must not be empty", name)
		}
	}

	capabilities := defaultCapabilities(provider)
	if len(raw.Capabilities) > 0 {
		capabilities, err = parseCapabilities(raw.Capabilities)
		if err != nil {
			return ConnectionConfig{}, fmt.Errorf("connection %q: %w", name, err)
		}
	}
	return ConnectionConfig{
		Name:         name,
		Provider:     provider,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Priority:     raw.Priority,
		Trust:        trust,
		Models:       models,
		Capabilities: capabilities,
	}, nil
}

func isLocalBaseURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "localhost" || host == "host.docker.internal" || !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".svc") ||
		strings.HasSuffix(host, ".cluster.local") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func cloneStringSet(input map[string]struct{}) map[string]struct{} {
	result := make(map[string]struct{}, len(input))
	for value := range input {
		result[value] = struct{}{}
	}
	return result
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

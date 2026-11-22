package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// Web research is an optional retrieval primitive, like embeddings: LocalM asks
// a self-hosted search engine (SearXNG) for results, optionally reads the top
// pages as plain text, and returns sources a model can be grounded in. It never
// prompts a model, follows instructions found in pages, or keeps what it read.
//
// Page reading fetches arbitrary public URLs, so every connection is checked at
// dial time (after DNS resolution, so rebinding cannot reach private networks),
// every redirect is re-checked, and size, time, type and port limits apply.

const (
	maxSearchQueries     = 5
	maxSearchQueryLength = 300
	maxSearchRedirects   = 4
)

var searchLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)

// SearchConfig is empty (disabled) unless SEARCH_BASE_URL is set.
type SearchConfig struct {
	Provider        string
	BaseURL         string
	APIKey          string
	MaxResults      int
	MaxReadPages    int
	Timeout         time.Duration
	PageTimeout     time.Duration
	PageMaxBytes    int64
	PageMaxChars    int
	MaxConcurrent   int
	SafeSearch      int
	PageUserAgent   string
	allowPrivateNet bool // tests only: lets page reads reach httptest servers.
}

func (c SearchConfig) Enabled() bool { return c.BaseURL != "" }

func loadSearchConfig() (SearchConfig, error) {
	cfg := SearchConfig{
		Provider:      strings.ToLower(getEnv("SEARCH_PROVIDER", "searxng")),
		APIKey:        getEnv("SEARCH_API_KEY", ""),
		MaxResults:    getEnvInt("SEARCH_MAX_RESULTS", 8),
		MaxReadPages:  getEnvInt("SEARCH_MAX_READ_PAGES", 3),
		Timeout:       getEnvDuration("SEARCH_TIMEOUT", 12*time.Second),
		PageTimeout:   getEnvDuration("SEARCH_PAGE_TIMEOUT", 8*time.Second),
		PageMaxBytes:  getEnvInt64("SEARCH_PAGE_MAX_BYTES", 1500000),
		PageMaxChars:  getEnvInt("SEARCH_PAGE_MAX_CHARS", 6000),
		MaxConcurrent: getEnvInt("MAX_CONCURRENT_SEARCH", 4),
		SafeSearch:    getEnvInt("SEARCH_SAFESEARCH", 1),
		PageUserAgent: getEnv("SEARCH_USER_AGENT", "localm-research/1.0 (+https://github.com/usmhic/localm)"),
	}
	raw := strings.TrimSpace(getEnv("SEARCH_BASE_URL", ""))
	if raw == "" {
		return cfg, nil
	}
	if cfg.Provider != "searxng" {
		return SearchConfig{}, fmt.Errorf("SEARCH_PROVIDER %q is not supported (use searxng)", cfg.Provider)
	}
	baseURL, err := validateUpstreamBaseURL(raw)
	if err != nil {
		return SearchConfig{}, fmt.Errorf("SEARCH_BASE_URL: %w", err)
	}
	cfg.BaseURL = baseURL
	switch {
	case cfg.MaxResults < 1 || cfg.MaxResults > 20:
		return SearchConfig{}, errors.New("SEARCH_MAX_RESULTS must be between 1 and 20")
	case cfg.MaxReadPages < 0 || cfg.MaxReadPages > 8:
		return SearchConfig{}, errors.New("SEARCH_MAX_READ_PAGES must be between 0 and 8")
	case cfg.MaxConcurrent < 1:
		return SearchConfig{}, errors.New("MAX_CONCURRENT_SEARCH must be positive")
	case cfg.Timeout <= 0 || cfg.PageTimeout <= 0 || cfg.PageMaxBytes <= 0 || cfg.PageMaxChars <= 0:
		return SearchConfig{}, errors.New("search timeouts and page limits must be positive")
	case cfg.SafeSearch < 0 || cfg.SafeSearch > 2:
		return SearchConfig{}, errors.New("SEARCH_SAFESEARCH must be 0, 1 or 2")
	}
	return cfg, nil
}

type searchRequest struct {
	Query      string   `json:"query"`
	Queries    []string `json:"queries"`
	MaxResults int      `json:"max_results"`
	ReadPages  *int     `json:"read_pages"`
	Language   string   `json:"language"`
}

// SearchSource is one result. Content holds the page's readable text when it
// was read, and is null otherwise (not read, unreachable, private or not text).
type SearchSource struct {
	Index       int     `json:"index"`
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Snippet     string  `json:"snippet"`
	Engine      string  `json:"engine,omitempty"`
	PublishedAt string  `json:"published_at,omitempty"`
	Content     *string `json:"content"`
	Truncated   bool    `json:"content_truncated,omitempty"`
}

type searxResult struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	Content       string `json:"content"`
	Engine        string `json:"engine"`
	PublishedDate string `json:"publishedDate"`
}

type searchService struct {
	cfg       SearchConfig
	engine    *http.Client
	pages     *http.Client
	semaphore chan struct{}
}

func newSearchService(cfg SearchConfig, engine *http.Client) *searchService {
	if !cfg.Enabled() {
		return nil
	}
	if engine == nil {
		engine = &http.Client{}
	}
	return &searchService{cfg: cfg, engine: engine, pages: newPageClient(cfg), semaphore: make(chan struct{}, cfg.MaxConcurrent)}
}

func (b *Bridge) search(w http.ResponseWriter, r *http.Request) {
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
	if b.searcher == nil {
		writeOpenAIError(w, http.StatusNotFound, "web search is not configured on this gateway", "invalid_request_error", "", "search_not_configured")
		return
	}
	now := time.Now()
	if !b.keyLimiter.Allow(apiKey, now) || !b.ipLimiter.Allow(clientIP(r), now) {
		writeOpenAIError(w, http.StatusTooManyRequests, "rate limit exceeded", "rate_limit_error", "", "rate_limit_exceeded")
		return
	}
	select {
	case b.searcher.semaphore <- struct{}{}:
		defer func() { <-b.searcher.semaphore }()
	default:
		writeOpenAIError(w, http.StatusTooManyRequests, "too many concurrent searches", "rate_limit_error", "", "concurrency_limit_exceeded")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	defer r.Body.Close()
	var req searchRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid JSON body", "invalid_request_error", "", "")
		return
	}
	queries, maxResults, readPages, param, err := b.searcher.normalize(req)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, err.Error(), "invalid_request_error", param, "")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), b.searcher.cfg.Timeout+b.searcher.cfg.PageTimeout)
	defer cancel()
	sources, err := b.searcher.run(ctx, queries, maxResults, readPages, req.Language)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "the search engine is unavailable", "server_error", "", "search_upstream_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "search.result", "queries": queries, "data": sources})
}

func (s *searchService) normalize(req searchRequest) ([]string, int, int, string, error) {
	queries := make([]string, 0, len(req.Queries)+1)
	seen := make(map[string]struct{})
	for _, raw := range append([]string{req.Query}, req.Queries...) {
		query := strings.Join(strings.Fields(raw), " ")
		if query == "" {
			continue
		}
		if utf8.RuneCountInString(query) > maxSearchQueryLength {
			return nil, 0, 0, "queries", fmt.Errorf("each query must be at most %d characters", maxSearchQueryLength)
		}
		if _, dup := seen[strings.ToLower(query)]; dup {
			continue
		}
		seen[strings.ToLower(query)] = struct{}{}
		queries = append(queries, query)
	}
	if len(queries) == 0 {
		return nil, 0, 0, "query", errors.New("query or queries is required")
	}
	if len(queries) > maxSearchQueries {
		return nil, 0, 0, "queries", fmt.Errorf("at most %d queries per request", maxSearchQueries)
	}
	maxResults := req.MaxResults
	if maxResults == 0 {
		maxResults = s.cfg.MaxResults
	}
	if maxResults < 1 || maxResults > s.cfg.MaxResults {
		return nil, 0, 0, "max_results", fmt.Errorf("max_results must be between 1 and %d", s.cfg.MaxResults)
	}
	readPages := min(2, s.cfg.MaxReadPages)
	if req.ReadPages != nil {
		readPages = *req.ReadPages
	}
	if readPages < 0 || readPages > s.cfg.MaxReadPages {
		return nil, 0, 0, "read_pages", fmt.Errorf("read_pages must be between 0 and %d", s.cfg.MaxReadPages)
	}
	if req.Language != "" && !searchLanguagePattern.MatchString(req.Language) {
		return nil, 0, 0, "language", errors.New("language must be a language tag such as en or fr-FR")
	}
	return queries, maxResults, min(readPages, maxResults), "", nil
}

// run searches every query in parallel, merges the result lists round-robin (so
// each query contributes its best hits), drops duplicate pages, then reads the
// first readPages results. One failing query is tolerated; all failing is an
// error.
func (s *searchService) run(ctx context.Context, queries []string, maxResults, readPages int, language string) ([]SearchSource, error) {
	lists := make([][]searxResult, len(queries))
	errs := make([]error, len(queries))
	var group sync.WaitGroup
	for i, query := range queries {
		group.Add(1)
		go func() {
			defer group.Done()
			lists[i], errs[i] = s.query(ctx, query, language)
		}()
	}
	group.Wait()
	failed := 0
	for _, err := range errs {
		if err != nil {
			failed++
		}
	}
	if failed == len(queries) {
		return nil, errs[0]
	}

	merged := mergeResults(lists, maxResults)
	sources := make([]SearchSource, len(merged))
	for i, result := range merged {
		sources[i] = SearchSource{
			Index:       i + 1,
			Title:       cleanText(result.Title, 300),
			URL:         result.URL,
			Snippet:     cleanText(result.Content, 500),
			Engine:      result.Engine,
			PublishedAt: result.PublishedDate,
		}
		if sources[i].Title == "" {
			sources[i].Title = result.URL
		}
	}
	for i := 0; i < readPages; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if text, title, truncated, ok := s.readPage(ctx, sources[i].URL); ok {
				sources[i].Content = &text
				sources[i].Truncated = truncated
				if sources[i].Title == sources[i].URL && title != "" {
					sources[i].Title = title
				}
			}
		}()
	}
	group.Wait()
	return sources, nil
}

func (s *searchService) query(ctx context.Context, query, language string) ([]searxResult, error) {
	target, err := url.Parse(s.cfg.BaseURL + "/search")
	if err != nil {
		return nil, err
	}
	values := url.Values{"q": {query}, "format": {"json"}, "safesearch": {fmt.Sprint(s.cfg.SafeSearch)}}
	if language != "" {
		values.Set("language", language)
	}
	target.RawQuery = values.Encode()
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}
	resp, err := s.engine.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// SearXNG answers 403 when its JSON format is not enabled in settings.yml.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("search engine returned HTTP %d", resp.StatusCode)
	}
	var body struct {
		Results []searxResult `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}
	return body.Results, nil
}

func mergeResults(lists [][]searxResult, limit int) []searxResult {
	merged := make([]searxResult, 0, limit)
	seen := make(map[string]struct{})
	for i := 0; len(merged) < limit; i++ {
		more := false
		for _, list := range lists {
			if i >= len(list) {
				continue
			}
			more = true
			result := list[i]
			key, ok := resultKey(result.URL)
			if !ok {
				continue
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, result)
			if len(merged) == limit {
				break
			}
		}
		if !more {
			break
		}
	}
	return merged
}

// resultKey identifies a page for de-duplication: scheme-less, without query,
// fragment, "www." or a trailing slash. Results that aren't http(s) are dropped.
func resultKey(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	return host + strings.TrimRight(parsed.EscapedPath(), "/"), true
}

// newPageClient is the client for reading result pages: no proxy from the
// environment, public addresses only (checked when connecting), standard web
// ports only, and a bounded number of re-checked redirects.
func newPageClient(cfg SearchConfig) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !cfg.allowPrivateNet {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if port != "80" && port != "443" {
				return errors.New("page port not allowed")
			}
			if ip := net.ParseIP(host); ip == nil || !isPublicIP(ip) {
				return errors.New("page address is not public")
			}
			return nil
		}
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  cfg.PageTimeout,
		MaxResponseHeaderBytes: 64 << 10,
		DisableCompression:     false,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   cfg.PageTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxSearchRedirects {
				return errors.New("too many redirects")
			}
			if !allowedPageURL(req.URL, cfg.allowPrivateNet) {
				return errors.New("redirect target not allowed")
			}
			return nil
		},
	}
}

func allowedPageURL(u *url.URL, allowPrivate bool) bool {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	if allowPrivate {
		return true
	}
	port := u.Port()
	return port == "" || port == "80" || port == "443"
}

var nonPublicNetworks = func() []*net.IPNet {
	var nets []*net.IPNet
	for _, cidr := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
		"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "2001:db8::/32",
	} {
		_, network, _ := net.ParseCIDR(cidr)
		nets = append(nets, network)
	}
	return nets
}()

// isPublicIP reports whether an address is routable on the public internet.
func isPublicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, network := range nonPublicNetworks {
		if network.Contains(ip) {
			return false
		}
	}
	return true
}

// readPage returns a page's readable text, its title and whether the text was
// cut at SEARCH_PAGE_MAX_CHARS. ok is false when the page can't be read.
func (s *searchService) readPage(ctx context.Context, raw string) (text, title string, truncated, ok bool) {
	target, err := url.Parse(raw)
	if err != nil || !allowedPageURL(target, s.cfg.allowPrivateNet) {
		return "", "", false, false
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.PageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return "", "", false, false
	}
	req.Header.Set("User-Agent", s.cfg.PageUserAgent)
	req.Header.Set("Accept", "text/html,text/plain;q=0.9")
	resp, err := s.pages.Do(req)
	if err != nil {
		return "", "", false, false
	}
	defer resp.Body.Close()
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	isHTML := strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml")
	if resp.StatusCode != http.StatusOK || (!isHTML && !strings.Contains(contentType, "text/plain")) {
		return "", "", false, false
	}
	if resp.ContentLength > s.cfg.PageMaxBytes {
		return "", "", false, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, s.cfg.PageMaxBytes))
	if err != nil {
		return "", "", false, false
	}
	content := string(body)
	if isHTML {
		title, content = htmlToText(content)
	}
	content = strings.ToValidUTF8(content, "")
	if utf8.RuneCountInString(content) > s.cfg.PageMaxChars {
		runes := []rune(content)
		return string(runes[:s.cfg.PageMaxChars]), title, true, true
	}
	return content, title, false, true
}

var (
	htmlTitlePattern   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	htmlCommentPattern = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlBlockEnd       = regexp.MustCompile(`(?i)<(br|/p|/div|/li|/h[1-6]|/tr|/section|/article|/blockquote)\b[^>]*>`)
	htmlTagPattern     = regexp.MustCompile(`(?s)<[^>]+>`)
	inlineSpace        = regexp.MustCompile(`[ \t\f\v\r]+`)
	lineBreaks         = regexp.MustCompile(`\s*\n\s*`)
	htmlDropped        = func() []*regexp.Regexp {
		var patterns []*regexp.Regexp
		for _, tag := range []string{"head", "script", "style", "noscript", "svg", "nav", "footer", "header", "form", "iframe", "template", "aside"} {
			patterns = append(patterns, regexp.MustCompile(`(?is)<`+tag+`\b.*?</`+tag+`\s*>`))
		}
		return patterns
	}()
)

// htmlToText extracts a page's title and readable text: no scripts, styles,
// navigation, forms or markup, with block elements kept as line breaks.
func htmlToText(page string) (string, string) {
	title := ""
	if match := htmlTitlePattern.FindStringSubmatch(page); match != nil {
		title = cleanText(html.UnescapeString(htmlTagPattern.ReplaceAllString(match[1], " ")), 300)
	}
	text := htmlCommentPattern.ReplaceAllString(page, " ")
	for _, pattern := range htmlDropped {
		text = pattern.ReplaceAllString(text, " ")
	}
	text = htmlBlockEnd.ReplaceAllString(text, "\n")
	text = htmlTagPattern.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = inlineSpace.ReplaceAllString(text, " ")
	text = lineBreaks.ReplaceAllString(text, "\n")
	return title, strings.TrimSpace(text)
}

func cleanText(value string, max int) string {
	value = strings.Join(strings.Fields(html.UnescapeString(value)), " ")
	if utf8.RuneCountInString(value) > max {
		return string([]rune(value)[:max])
	}
	return value
}

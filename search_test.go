package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func searchConfig(engineURL string) SearchConfig {
	return SearchConfig{
		Provider: "searxng", BaseURL: strings.TrimRight(engineURL, "/"), APIKey: "engine-key",
		MaxResults: 8, MaxReadPages: 3, Timeout: 2 * time.Second, PageTimeout: 2 * time.Second,
		PageMaxBytes: 1 << 20, PageMaxChars: 60, MaxConcurrent: 2, SafeSearch: 1, PageUserAgent: "test",
	}
}

func searchBridge(search SearchConfig) http.Handler {
	cfg := testConfig("http://127.0.0.1:1")
	cfg.Search = search
	return newBridge(cfg, nil).routes()
}

type searchResponse struct {
	Object  string         `json:"object"`
	Queries []string       `json:"queries"`
	Data    []SearchSource `json:"data"`
	Error   *openAIError   `json:"error"`
}

func decodeSearch(t *testing.T, response *httptest.ResponseRecorder) searchResponse {
	t.Helper()
	var body searchResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// engine answers each query with the results listed for it, and records what it was asked.
func engine(t *testing.T, results map[string][]searxResult, asked *atomic.Value) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("format") != "json" || r.Header.Get("Authorization") != "Bearer engine-key" {
			t.Errorf("unexpected engine request %s auth=%q", r.URL, r.Header.Get("Authorization"))
		}
		if asked != nil {
			asked.Store(r.URL.Query())
		}
		list, ok := results[r.URL.Query().Get("q")]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": list})
	}))
}

func TestSearchIsOffUntilConfiguredAndNeedsAKey(t *testing.T) {
	handler := searchBridge(SearchConfig{})
	if response := endpointRequest(t, handler, "/v1/search", `{"query":"x"}`); response.Code != http.StatusNotFound || decodeSearch(t, response).Error.Code != "search_not_configured" {
		t.Fatalf("disabled search should be 404 search_not_configured, got %d", response.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/search", strings.NewReader(`{"query":"x"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("search without a key should be 401, got %d", response.Code)
	}
}

func TestSearchValidatesRequests(t *testing.T) {
	handler := searchBridge(searchConfig("http://127.0.0.1:1"))
	for _, body := range []string{
		`{}`,
		`{"query":"   "}`,
		`{"queries":["a","b","c","d","e","f"]}`,
		`{"query":"x","read_pages":9}`,
		`{"query":"x","max_results":50}`,
		`{"query":"x","language":"english please"}`,
		`{"query":"x","unknown":true}`,
		`{"query":"` + strings.Repeat("q", 301) + `"}`,
	} {
		if response := endpointRequest(t, handler, "/v1/search", body); response.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d", body, response.Code)
		}
	}
}

func TestSearchMergesQueriesRoundRobinWithoutDuplicates(t *testing.T) {
	var asked atomic.Value
	server := engine(t, map[string][]searxResult{
		"bella cucina reviews": {
			{URL: "https://www.yelp.com/biz/bella", Title: "Bella &amp; Co - Yelp", Content: "Great   pasta", Engine: "bing"},
			{URL: "https://example.org/a", Title: "A"},
		},
		"bella cucina austin": {
			{URL: "https://yelp.com/biz/bella/?utm=1#top", Title: "duplicate"},
			{URL: "https://news.example.com/b", Title: "B", PublishedDate: "2026-10-01"},
			{URL: "javascript:alert(1)", Title: "dropped"},
		},
	}, &asked)
	defer server.Close()

	response := endpointRequest(t, searchBridge(searchConfig(server.URL)), "/v1/search", `{"queries":["bella cucina reviews","Bella  cucina reviews","bella cucina austin"],"max_results":3,"read_pages":0,"language":"fr"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	body := decodeSearch(t, response)
	if len(body.Queries) != 2 {
		t.Fatalf("duplicate queries should collapse: %v", body.Queries)
	}
	var urls []string
	for _, source := range body.Data {
		urls = append(urls, source.URL)
		if source.Content != nil {
			t.Errorf("read_pages 0 must not read %s", source.URL)
		}
	}
	want := []string{"https://www.yelp.com/biz/bella", "https://example.org/a", "https://news.example.com/b"}
	if strings.Join(urls, " ") != strings.Join(want, " ") {
		t.Fatalf("merged %v, want %v", urls, want)
	}
	if body.Data[0].Title != "Bella & Co - Yelp" || body.Data[0].Snippet != "Great pasta" || body.Data[0].Index != 1 || body.Data[2].PublishedAt != "2026-10-01" {
		t.Fatalf("unexpected first/last source: %+v %+v", body.Data[0], body.Data[2])
	}
	if q := asked.Load().(url.Values); q.Get("language") != "fr" || q.Get("safesearch") != "1" {
		t.Fatalf("engine was not asked in the requested language: %v", q)
	}
}

func TestSearchReadsTopPagesAsText(t *testing.T) {
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/article":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><head><title>The &quot;best&quot; pasta</title><script>steal()</script></head>
<body><nav>Menu</nav><h1>Bella</h1><p>Lovely team.</p><p>A little wait on Saturday, but worth it for the carbonara and the tiramisu.</p></body></html>`)
		case "/image":
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "png")
		}
	}))
	defer page.Close()
	server := engine(t, map[string][]searxResult{"q": {{URL: page.URL + "/article"}, {URL: page.URL + "/image", Title: "Image"}}}, nil)
	defer server.Close()

	cfg := searchConfig(server.URL)
	cfg.allowPrivateNet = true // The pages are on httptest's loopback address.
	body := decodeSearch(t, endpointRequest(t, searchBridge(cfg), "/v1/search", `{"query":"q","read_pages":2}`))
	if len(body.Data) != 2 || body.Data[0].Content == nil {
		t.Fatalf("expected the article to be read: %+v", body.Data)
	}
	content := *body.Data[0].Content
	if strings.Contains(content, "steal") || strings.Contains(content, "Menu") || !strings.HasPrefix(content, "Bella\nLovely team.") {
		t.Fatalf("unexpected page text %q", content)
	}
	if !body.Data[0].Truncated || len([]rune(content)) != 60 {
		t.Fatalf("content should be cut at SEARCH_PAGE_MAX_CHARS: %d %v", len([]rune(content)), body.Data[0].Truncated)
	}
	if body.Data[0].Title != `The "best" pasta` {
		t.Fatalf("an untitled result should take the page title, got %q", body.Data[0].Title)
	}
	if body.Data[1].Content != nil {
		t.Fatalf("a non-text page must not be read")
	}
}

func TestSearchNeverReadsPrivateAddresses(t *testing.T) {
	var reads atomic.Int32
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reads.Add(1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<p>internal secrets</p>")
	}))
	defer page.Close()
	server := engine(t, map[string][]searxResult{"q": {{URL: page.URL + "/admin", Title: "Internal"}, {URL: "http://169.254.169.254/latest/meta-data", Title: "Metadata"}}}, nil)
	defer server.Close()

	body := decodeSearch(t, endpointRequest(t, searchBridge(searchConfig(server.URL)), "/v1/search", `{"query":"q","read_pages":2}`))
	if reads.Load() != 0 || len(body.Data) != 2 || body.Data[0].Content != nil || body.Data[1].Content != nil {
		t.Fatalf("private pages must never be fetched: reads=%d data=%+v", reads.Load(), body.Data)
	}
}

func TestSearchToleratesOneFailingQueryButNotAll(t *testing.T) {
	server := engine(t, map[string][]searxResult{"ok": {{URL: "https://example.org/x", Title: "X"}}}, nil)
	defer server.Close()
	handler := searchBridge(searchConfig(server.URL))
	if response := endpointRequest(t, handler, "/v1/search", `{"queries":["ok","broken"],"read_pages":0}`); response.Code != http.StatusOK || len(decodeSearch(t, response).Data) != 1 {
		t.Fatalf("one failing query should be tolerated, got %d", response.Code)
	}
	response := endpointRequest(t, handler, "/v1/search", `{"queries":["broken","also broken"]}`)
	if response.Code != http.StatusBadGateway || decodeSearch(t, response).Error.Code != "search_upstream_error" {
		t.Fatalf("all queries failing should be 502 search_upstream_error, got %d", response.Code)
	}
}

func TestIsPublicIP(t *testing.T) {
	for address, public := range map[string]bool{
		"93.184.216.34": true, "2606:4700::6810:84e5": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "::1": false, "fd00::1": false, "fe80::1": false, "::ffff:127.0.0.1": false,
		"64:ff9b::a00:1": false, "224.0.0.1": false,
	} {
		if got := isPublicIP(net.ParseIP(address)); got != public {
			t.Errorf("isPublicIP(%s) = %v, want %v", address, got, public)
		}
	}
}

func TestLoadSearchConfig(t *testing.T) {
	t.Setenv("SEARCH_BASE_URL", "")
	if cfg, err := loadSearchConfig(); err != nil || cfg.Enabled() {
		t.Fatalf("search must be off without SEARCH_BASE_URL: %v", err)
	}
	t.Setenv("SEARCH_BASE_URL", "http://searxng:8080/")
	if cfg, err := loadSearchConfig(); err != nil || cfg.BaseURL != "http://searxng:8080" {
		t.Fatalf("unexpected config %+v %v", cfg, err)
	}
	t.Setenv("SEARCH_MAX_READ_PAGES", "20")
	if _, err := loadSearchConfig(); err == nil {
		t.Fatal("an out-of-range page limit must stop startup")
	}
	t.Setenv("SEARCH_MAX_READ_PAGES", "")
	t.Setenv("SEARCH_PROVIDER", "bing")
	if _, err := loadSearchConfig(); err == nil {
		t.Fatal("an unknown provider must stop startup")
	}
}

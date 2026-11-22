package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const chatBody = `{"model":"qwen3:8b","messages":[{"role":"user","content":"hi"}]}`

func failoverConfig(primary, backup string) Config {
	cfg := testConfig(primary)
	cfg.Failover = true
	cfg.FailurePause = time.Minute
	cfg.FailurePauseMax = 5 * time.Minute
	cfg.Connections = []ConnectionConfig{
		connectionForTest("primary", primary, 100, defaultCapabilities("custom")),
		connectionForTest("backup", backup, 10, defaultCapabilities("custom")),
	}
	return cfg
}

func countingServer(status int, body string, calls *atomic.Int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

func TestFailoverMovesToNextConnectionAndPausesTheFailingOne(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	primary := countingServer(http.StatusServiceUnavailable, `{"error":"loading"}`, &primaryCalls)
	defer primary.Close()
	backup := countingServer(http.StatusOK, `{"choices":[{"message":{"content":"from backup"}}]}`, &backupCalls)
	defer backup.Close()
	handler := newBridge(failoverConfig(primary.URL, backup.URL), nil).routes()

	first := endpointRequest(t, handler, "/v1/chat/completions", chatBody)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "from backup") {
		t.Fatalf("expected the backup's answer, status=%d body=%s", first.Code, first.Body.String())
	}
	second := endpointRequest(t, handler, "/v1/chat/completions", chatBody)
	if second.Code != http.StatusOK {
		t.Fatalf("second request failed: %d", second.Code)
	}
	if primaryCalls.Load() != 1 || backupCalls.Load() != 2 {
		t.Fatalf("the failing connection should be paused after one failure: primary=%d backup=%d", primaryCalls.Load(), backupCalls.Load())
	}
}

func TestFailoverOnUnreachableConnection(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()
	var backupCalls atomic.Int32
	backup := countingServer(http.StatusOK, `{"choices":[]}`, &backupCalls)
	defer backup.Close()
	response := endpointRequest(t, newBridge(failoverConfig(deadURL, backup.URL), nil).routes(), "/v1/chat/completions", chatBody)
	if response.Code != http.StatusOK || backupCalls.Load() != 1 {
		t.Fatalf("expected failover past an unreachable connection, status=%d calls=%d", response.Code, backupCalls.Load())
	}
}

func TestNoFailoverOnRejectedRequestOrWhenDisabled(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	rejecting := countingServer(http.StatusBadRequest, `{"error":"bad"}`, &primaryCalls)
	defer rejecting.Close()
	backup := countingServer(http.StatusOK, `{"choices":[]}`, &backupCalls)
	defer backup.Close()
	response := endpointRequest(t, newBridge(failoverConfig(rejecting.URL, backup.URL), nil).routes(), "/v1/chat/completions", chatBody)
	if response.Code != http.StatusBadRequest || backupCalls.Load() != 0 {
		t.Fatalf("a rejected request must not fail over: status=%d backup=%d", response.Code, backupCalls.Load())
	}

	busy := countingServer(http.StatusServiceUnavailable, `{}`, &primaryCalls)
	defer busy.Close()
	cfg := failoverConfig(busy.URL, backup.URL)
	cfg.Failover = false
	response = endpointRequest(t, newBridge(cfg, nil).routes(), "/v1/chat/completions", chatBody)
	if response.Code != http.StatusServiceUnavailable || backupCalls.Load() != 0 {
		t.Fatalf("with FAILOVER off the first connection's answer is final: status=%d backup=%d", response.Code, backupCalls.Load())
	}
}

func TestFailoverBeforeStreamingStarts(t *testing.T) {
	var primaryCalls atomic.Int32
	primary := countingServer(http.StatusBadGateway, `{}`, &primaryCalls)
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[]}\n\ndata: [DONE]\n\n")
	}))
	defer backup.Close()
	response := endpointRequest(t, newBridge(failoverConfig(primary.URL, backup.URL), nil).routes(), "/v1/chat/completions", `{"model":"qwen3:8b","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "[DONE]") {
		t.Fatalf("expected the backup's stream, status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestPausedConnectionIsStillTriedWhenItIsTheOnlyOne(t *testing.T) {
	var calls atomic.Int32
	upstream := countingServer(http.StatusInternalServerError, `{}`, &calls)
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	cfg.Failover = true
	cfg.FailurePause = time.Minute
	cfg.FailurePauseMax = time.Minute
	handler := newBridge(cfg, nil).routes()
	for i := 0; i < 2; i++ {
		if response := endpointRequest(t, handler, "/v1/chat/completions", chatBody); response.Code != http.StatusInternalServerError {
			t.Fatalf("request %d: status %d", i, response.Code)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("a paused sole connection must still be tried, calls=%d", calls.Load())
	}
}

func TestFailurePauseDoublesUpToTheMaximum(t *testing.T) {
	c := &connection{}
	now := time.Unix(1000, 0)
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second} {
		c.recordFailure(now, time.Second, 5*time.Second)
		if _, until := c.pausedAt(now); until.Sub(now) != want {
			t.Fatalf("pause %v, want %v", until.Sub(now), want)
		}
	}
	c.recordSuccess()
	if paused, _ := c.pausedAt(now); paused {
		t.Fatal("success must clear the pause")
	}
}

func TestQueueWaitsForASlotThenRejectsWhenFull(t *testing.T) {
	release := make(chan struct{})
	var started atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if started.Add(1) == 1 {
			<-release
		}
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	cfg.MaxConcurrent = 1
	cfg.MaxQueued = 1
	cfg.QueueTimeout = 5 * time.Second
	bridge := newBridge(cfg, upstream.Client())
	handler := bridge.routes()

	var group sync.WaitGroup
	codes := make([]int, 2)
	group.Add(1)
	go func() {
		defer group.Done()
		codes[0] = endpointRequest(t, handler, "/v1/chat/completions", chatBody).Code
	}()
	waitFor(t, func() bool { return started.Load() == 1 })
	group.Add(1)
	go func() {
		defer group.Done()
		codes[1] = endpointRequest(t, handler, "/v1/chat/completions", chatBody).Code
	}()
	waitFor(t, func() bool { return bridge.queued.Load() == 1 })

	if third := endpointRequest(t, handler, "/v1/chat/completions", chatBody); third.Code != http.StatusTooManyRequests {
		t.Fatalf("a full queue must reject at once, got %d", third.Code)
	}
	close(release)
	group.Wait()
	if codes[0] != http.StatusOK || codes[1] != http.StatusOK {
		t.Fatalf("the running and the queued request should both succeed: %v", codes)
	}
}

func TestCapabilitiesReportLoadAndSearch(t *testing.T) {
	upstream := countingServer(http.StatusOK, `{"data":[{"id":"qwen3:8b"}]}`, new(atomic.Int32))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	cfg.MaxQueued = 3
	req := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	req.Header.Set("Authorization", "Bearer k1")
	response := httptest.NewRecorder()
	newBridge(cfg, upstream.Client()).routes().ServeHTTP(response, req)
	var body struct {
		Load   map[string]float64 `json:"load"`
		Search map[string]any     `json:"search"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Load["max_concurrent"] != 2 || body.Load["max_queued"] != 3 || body.Search["enabled"] != false {
		t.Fatalf("unexpected load/search report: %+v %+v", body.Load, body.Search)
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dola2api/internal/config"
	"dola2api/internal/dola"
	"dola2api/internal/pool"
	"dola2api/internal/store"
)

// These tests drive the full request path - auth, limiting, pool scheduling,
// upstream parsing, audit - against a stub upstream, so the gateway can be
// exercised end to end without a live Dola cookie.

type harness struct {
	gateway  *Gateway
	store    *store.Store
	pool     *pool.Pool
	upstream *httptest.Server
	settings *config.Settings
	key      string
}

// upstreamText builds the SSE body a normal answer produces.
func upstreamText(text string) string {
	block := map[string]any{
		"block_type": 10000,
		"content":    map[string]any{"text_block": map[string]any{"text": text}},
	}
	data, _ := json.Marshal(map[string]any{
		"content": map[string]any{"content_block": []any{block}},
	})
	return "event:SSE_ACK\ndata:{\"ack_client_meta\":{\"conversation_id\":\"conv_1\"}}\n\n" +
		"event:STREAM_MSG_NOTIFY\ndata:" + string(data) + "\n\n" +
		"event:SSE_REPLY_END\ndata:{}\n\n"
}

func newHarness(t *testing.T, upstream http.HandlerFunc) *harness {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(dir, "admin", "admin12345")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	settings := config.DefaultSettings(dir)
	settings.Routing.CapacityWaitSec = 0 // fail fast instead of waiting for capacity
	settings.Routing.MaxAttempts = 3
	settings.Upstream.RequestTimeoutSec = 5
	settings.Upstream.StreamIdleTimeoutSec = 3
	settings.Media.AutoDownload = false

	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	settings.Upstream.BaseURL = server.URL

	settingsFn := func() config.Settings { return settings }
	client := dola.New(settingsFn)
	p := pool.New(st, settingsFn)
	gw := New(st, p, client, settingsFn)

	key, err := st.CreateClientKey("test-key", 0, 0)
	if err != nil {
		t.Fatalf("create client key: %v", err)
	}

	return &harness{
		gateway:  gw,
		store:    st,
		pool:     p,
		upstream: server,
		settings: &settings,
		key:      key.Key,
	}
}

func (h *harness) addAccount(t *testing.T, name, cookie string, priority int) *store.Account {
	t.Helper()
	account := &store.Account{Name: name, Cookie: cookie, Priority: priority, MaxConcurrent: 2, Enabled: true}
	if err := h.store.AddAccount(account); err != nil {
		t.Fatalf("add account %s: %v", name, err)
	}
	return account
}

func (h *harness) chat(t *testing.T, body map[string]any, token string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(raw))
	req.Header.Set("content-type", "application/json")
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.gateway.ChatCompletions(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v\nbody: %s", err, rec.Body.String())
	}
	return out
}

// sseDataLines pulls every `data:` payload out of an SSE response.
func sseDataLines(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var out []string
	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return out
}

func TestChatCompletionsRequiresClientKey(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called without a valid key")
	})

	rec := h.chat(t, map[string]any{"model": "dola-fast", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	rec = h.chat(t, map[string]any{"model": "dola-fast", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}, "sk-dola-wrong")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown key: status = %d, want 401", rec.Code)
	}
}

func TestChatCompletionsRejectsUnknownModel(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called for an unknown model")
	})

	rec := h.chat(t, map[string]any{"model": "gpt-4", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}, h.key)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "model") {
		t.Fatalf("error should name the model: %s", rec.Body.String())
	}
}

func TestChatCompletionsReturnsOpenAIResponse(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("cookie"); got != "oauth_token=good" {
			t.Errorf("cookie = %q, want the pooled account cookie", got)
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = fmt.Fprint(w, upstreamText("你好，世界"))
	})
	h.addAccount(t, "primary", "oauth_token=good", 10)

	rec := h.chat(t, map[string]any{
		"model":    "dola-fast",
		"messages": []any{map[string]any{"role": "user", "content": "打个招呼"}},
	}, h.key)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeJSON(t, rec)

	if payload["object"] != "chat.completion" {
		t.Fatalf("object = %v", payload["object"])
	}
	if payload["model"] != "dola-fast" {
		t.Fatalf("model = %v", payload["model"])
	}
	choices, _ := payload["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v", payload["choices"])
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message["content"] != "你好，世界" {
		t.Fatalf("content = %v", message["content"])
	}
	if message["role"] != "assistant" {
		t.Fatalf("role = %v", message["role"])
	}
	if choice["finish_reason"] != "stop" {
		t.Fatalf("finish_reason = %v", choice["finish_reason"])
	}
	usage, _ := payload["usage"].(map[string]any)
	if usage["total_tokens"] == nil {
		t.Fatalf("usage missing: %v", payload["usage"])
	}

	// The pool must hand the account back after the request.
	if got := h.pool.Inflight(accountID(t, h, "primary")); got != 0 {
		t.Fatalf("inflight = %d after the request, want 0", got)
	}
}

func TestChatCompletionsStreamsSSE(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = fmt.Fprint(w, upstreamText("流式回答"))
	})
	h.addAccount(t, "primary", "oauth_token=good", 10)

	rec := h.chat(t, map[string]any{
		"model":    "dola-fast",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
		"stream":   true,
	}, h.key)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("content-type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	// Proxies must not buffer a stream.
	if rec.Header().Get("x-accel-buffering") != "no" {
		t.Fatalf("x-accel-buffering = %q", rec.Header().Get("x-accel-buffering"))
	}

	lines := sseDataLines(t, rec)
	if len(lines) < 3 {
		t.Fatalf("expected several SSE chunks, got %v", lines)
	}
	if lines[len(lines)-1] != "[DONE]" {
		t.Fatalf("stream must end with [DONE], got %q", lines[len(lines)-1])
	}

	var sawRole, sawContent, sawStop bool
	for _, line := range lines {
		if line == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			t.Fatalf("chunk is not JSON: %v (%q)", err, line)
		}
		if chunk["object"] != "chat.completion.chunk" {
			t.Fatalf("object = %v", chunk["object"])
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) != 1 {
			t.Fatalf("choices = %v", choices)
		}
		entry, _ := choices[0].(map[string]any)
		delta, _ := entry["delta"].(map[string]any)
		if delta["role"] == "assistant" {
			sawRole = true
		}
		if delta["content"] == "流式回答" {
			sawContent = true
		}
		if entry["finish_reason"] == "stop" {
			sawStop = true
		}
	}
	if !sawRole || !sawContent || !sawStop {
		t.Fatalf("missing frames: role=%v content=%v stop=%v", sawRole, sawContent, sawStop)
	}
}

// A rejected cookie must be marked invalid and the request retried on the next
// account in the pool.
func TestChatCompletionsFailsOverToHealthyAccount(t *testing.T) {
	var attempts []string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		cookie := r.Header.Get("cookie")
		attempts = append(attempts, cookie)
		if cookie == "oauth_token=bad" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = fmt.Fprint(w, upstreamText("换号成功"))
	})

	bad := h.addAccount(t, "broken", "oauth_token=bad", 1) // picked first
	h.addAccount(t, "healthy", "oauth_token=good", 50)

	rec := h.chat(t, map[string]any{
		"model":    "dola-fast",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, h.key)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after failover; body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeJSON(t, rec)
	choices, _ := payload["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message["content"] != "换号成功" {
		t.Fatalf("content = %v", message["content"])
	}

	if len(attempts) != 2 {
		t.Fatalf("upstream saw %d attempts (%v), want 2", len(attempts), attempts)
	}

	// The rejected account must be quarantined so it stops being scheduled.
	updated, ok := h.store.AccountByID(bad.ID)
	if !ok {
		t.Fatal("account disappeared")
	}
	if updated.Status != store.StatusInvalid {
		t.Fatalf("status = %q, want %q after a 401", updated.Status, store.StatusInvalid)
	}
	if updated.LastError == "" {
		t.Fatal("LastError should explain why the account was quarantined")
	}
}

func TestChatCompletionsReportsNoAccount(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called when the pool is empty")
	})

	rec := h.chat(t, map[string]any{
		"model":    "dola-fast",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, h.key)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestChatCompletionsRecordsAudit(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = fmt.Fprint(w, upstreamText("审计"))
	})
	h.addAccount(t, "primary", "oauth_token=good", 10)

	if rec := h.chat(t, map[string]any{
		"model":    "dola-fast",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, h.key); rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	audits := h.store.ListAudits()
	if len(audits) != 1 {
		t.Fatalf("audits = %d, want 1", len(audits))
	}
	audit := audits[0]
	if audit.Status != http.StatusOK {
		t.Fatalf("audit status = %d", audit.Status)
	}
	if audit.Model != "dola-fast" {
		t.Fatalf("audit model = %q", audit.Model)
	}
	if audit.AccountName != "primary" {
		t.Fatalf("audit account = %q", audit.AccountName)
	}
	if audit.LatencyMs < 0 {
		t.Fatalf("audit latency = %d", audit.LatencyMs)
	}
}

// A failed call must still be audited, with the upstream reason attached.
func TestChatCompletionsAuditsFailures(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, "upstream is down")
	})
	h.addAccount(t, "primary", "oauth_token=good", 10)

	rec := h.chat(t, map[string]any{
		"model":    "dola-fast",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, h.key)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}

	audits := h.store.ListAudits()
	if len(audits) == 0 {
		t.Fatal("a failed request must still produce an audit record")
	}
	if audits[0].Error == "" {
		t.Fatal("audit should carry the upstream error")
	}
}

func TestChatCompletionsEnforcesRateLimit(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		_, _ = fmt.Fprint(w, upstreamText("ok"))
	})
	h.addAccount(t, "primary", "oauth_token=good", 10)

	// Replace the unlimited key with a 1 RPM one.
	limited, err := h.store.CreateClientKey("limited", 1, 4)
	if err != nil {
		t.Fatalf("create limited key: %v", err)
	}

	body := map[string]any{"model": "dola-fast", "messages": []any{map[string]any{"role": "user", "content": "hi"}}}
	if rec := h.chat(t, body, limited.Key); rec.Code != http.StatusOK {
		t.Fatalf("first call status = %d", rec.Code)
	}
	rec := h.chat(t, body, limited.Key)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("second call status = %d, want 429", rec.Code)
	}
}

func TestModelsEndpoint(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.gateway.Models(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("authorization", "Bearer "+h.key)
	rec = httptest.NewRecorder()
	h.gateway.Models(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	payload := decodeJSON(t, rec)
	if payload["object"] != "list" {
		t.Fatalf("object = %v", payload["object"])
	}
	data, _ := payload["data"].([]any)
	if len(data) == 0 {
		t.Fatal("model list is empty")
	}
	ids := map[string]bool{}
	for _, item := range data {
		entry, _ := item.(map[string]any)
		id, _ := entry["id"].(string)
		ids[id] = true
		if entry["object"] != "model" {
			t.Fatalf("entry object = %v", entry["object"])
		}
	}
	for _, want := range []string{"dola-fast", "dola-pro", "dola-image"} {
		if !ids[want] {
			t.Fatalf("model list is missing %s: %v", want, ids)
		}
	}
}

func TestHealthReportsPoolSummary(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {})
	h.addAccount(t, "one", "oauth_token=a", 10)
	h.addAccount(t, "two", "oauth_token=b", 20)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.gateway.Health(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	payload := decodeJSON(t, rec)
	if payload["status"] != "ok" {
		t.Fatalf("status = %v", payload["status"])
	}
	summary, _ := payload["pool"].(map[string]any)
	if summary["total"] != float64(2) {
		t.Fatalf("pool total = %v, want 2", summary["total"])
	}
	if summary["routable"] != float64(2) {
		t.Fatalf("pool routable = %v, want 2", summary["routable"])
	}
}

func TestImageGenerationsReturnsMedia(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		block := map[string]any{
			"block_type": 2074,
			"content": map[string]any{
				"creation_block": map[string]any{
					"creations": []any{
						map[string]any{"image": map[string]any{"image_ori": map[string]any{"url": "https://cdn/one.png"}}},
						map[string]any{"image": map[string]any{"image_ori": map[string]any{"url": "https://cdn/two.png"}}},
					},
				},
			},
		}
		data, _ := json.Marshal(map[string]any{
			"content": map[string]any{"content_block": []any{block}},
		})
		w.Header().Set("content-type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event:STREAM_MSG_NOTIFY\ndata:"+string(data)+"\n\n")
	})
	h.addAccount(t, "primary", "oauth_token=good", 10)

	raw, _ := json.Marshal(map[string]any{"prompt": "a blue circle", "n": 2})
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(raw))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+h.key)
	rec := httptest.NewRecorder()
	h.gateway.ImageGenerations(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	payload := decodeJSON(t, rec)
	data, _ := payload["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("data = %v, want 2 images", payload["data"])
	}
	first, _ := data[0].(map[string]any)
	if first["url"] != "https://cdn/one.png" {
		t.Fatalf("url = %v", first["url"])
	}
}

func accountID(t *testing.T, h *harness, name string) string {
	t.Helper()
	for _, account := range h.store.ListAccounts() {
		if account.Name == name {
			return account.ID
		}
	}
	t.Fatalf("account %q not found", name)
	return ""
}

// Guard against the harness drifting from the real configuration defaults.
func TestHarnessUsesFastCapacityWait(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {})
	if got := h.settings.CapacityWait(); got != 0 {
		t.Fatalf("capacity wait = %v, want 0 so pool-exhaustion tests stay fast", got)
	}
	if h.settings.RequestTimeout() > 5*time.Second {
		t.Fatalf("request timeout = %v, tests should not hang", h.settings.RequestTimeout())
	}
}

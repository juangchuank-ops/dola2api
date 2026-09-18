package dola

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dola2api/internal/config"
)

// The upstream protocol is undocumented and was reverse engineered, so these
// tests pin the parsing contract: a canned SSE stream goes in, an aggregated
// Result comes out. They run against httptest instead of dola.com, which means
// the whole request/response path is covered without needing a live cookie.

func testSettings(baseURL string) func() config.Settings {
	settings := config.DefaultSettings("testdata")
	settings.Upstream.BaseURL = baseURL
	settings.Upstream.RequestTimeoutSec = 10
	settings.Upstream.StreamIdleTimeoutSec = 5
	return func() config.Settings { return settings }
}

// frame renders one SSE frame. The parser splits on a blank line, so every
// frame ends with "\n\n".
func frame(event, data string) string {
	return fmt.Sprintf("event:%s\ndata:%s\n\n", event, data)
}

// sseServer serves a fixed SSE body for POST /chat/completion.
func sseServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completion") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// textBlock builds the nested shape the parser walks:
// content.content_block[].content.text_block.
func textBlock(blockType int, text, iconURL string) map[string]any {
	textBlockBody := map[string]any{"text": text}
	if iconURL != "" {
		textBlockBody["icon_url"] = iconURL
	}
	return map[string]any{
		"block_type": blockType,
		"content":    map[string]any{"text_block": textBlockBody},
	}
}

func notifyFrame(blocks ...map[string]any) string {
	items := make([]any, 0, len(blocks))
	for _, block := range blocks {
		items = append(items, block)
	}
	data, _ := json.Marshal(map[string]any{
		"content": map[string]any{"content_block": items},
	})
	return frame("STREAM_MSG_NOTIFY", string(data))
}

func TestCompletionAggregatesText(t *testing.T) {
	body := strings.Join([]string{
		frame("SSE_HEARTBEAT", `{}`),
		frame("SSE_ACK", `{"ack_client_meta":{"conversation_id":"conv_abc"}}`),
		frame("FULL_MSG_NOTIFY", `{"message":{"message_id":"msg_1"}}`),
		notifyFrame(textBlock(10000, "你好", "")),
		notifyFrame(textBlock(10000, "，世界", "")),
		frame("SSE_REPLY_END", `{}`),
	}, "")

	server := sseServer(t, body)
	client := New(testSettings(server.URL))

	result, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "hi"})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	if result.Text != "你好，世界" {
		t.Fatalf("text = %q, want %q", result.Text, "你好，世界")
	}
	if result.ConversationID != "conv_abc" {
		t.Fatalf("conversationID = %q", result.ConversationID)
	}
	if result.MessageID != "msg_1" {
		t.Fatalf("messageID = %q", result.MessageID)
	}
	if result.Thinking != "" {
		t.Fatalf("thinking should be empty, got %q", result.Thinking)
	}
}

// The reasoning block is only distinguishable by its Deep_Think icon, so this
// pins that heuristic: reasoning must not leak into the answer text.
func TestCompletionSeparatesThinkingFromAnswer(t *testing.T) {
	body := strings.Join([]string{
		notifyFrame(textBlock(10040, "让我想想…", "https://cdn/Deep_Think.svg")),
		notifyFrame(textBlock(10000, "答案是 42", "")),
		frame("SSE_REPLY_END", `{}`),
	}, "")

	server := sseServer(t, body)
	client := New(testSettings(server.URL))

	var deltas, thinking []string
	result, err := client.Completion(context.Background(), Options{
		Cookie: "oauth_token=x",
		Text:   "hi",
		OnDelta: func(text string) {
			deltas = append(deltas, text)
		},
		OnThinking: func(text string) {
			thinking = append(thinking, text)
		},
	})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}

	if result.Text != "答案是 42" {
		t.Fatalf("text = %q, want the answer only", result.Text)
	}
	if result.Thinking != "让我想想…" {
		t.Fatalf("thinking = %q", result.Thinking)
	}
	if strings.Join(deltas, "") != "答案是 42" {
		t.Fatalf("OnDelta saw %v", deltas)
	}
	if strings.Join(thinking, "") != "让我想想…" {
		t.Fatalf("OnThinking saw %v", thinking)
	}
}

// The dark-theme icon variant must be recognised too.
func TestCompletionDetectsThinkingViaDarkIcon(t *testing.T) {
	block := map[string]any{
		"block_type": 10040,
		"content": map[string]any{
			"text_block": map[string]any{
				"text":          "推理中",
				"icon_url_dark": "https://cdn/Deep_Think_dark.svg",
			},
		},
	}
	data, _ := json.Marshal(map[string]any{
		"content": map[string]any{"content_block": []any{block}},
	})

	server := sseServer(t, frame("STREAM_MSG_NOTIFY", string(data)))
	client := New(testSettings(server.URL))

	result, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "hi"})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	if result.Thinking != "推理中" || result.Text != "" {
		t.Fatalf("thinking=%q text=%q", result.Thinking, result.Text)
	}
}

func TestCompletionCollectsGeneratedMedia(t *testing.T) {
	creation := func(imageURL, videoURL string) map[string]any {
		entry := map[string]any{}
		if imageURL != "" {
			entry["image"] = map[string]any{"image_ori": map[string]any{"url": imageURL}}
		}
		if videoURL != "" {
			entry["video"] = map[string]any{"video_url": videoURL}
		}
		return entry
	}
	block := map[string]any{
		"block_type": 2074,
		"content": map[string]any{
			"creation_block": map[string]any{
				"creations": []any{
					creation("https://cdn/img-1.png", ""),
					creation("https://cdn/img-2.png", ""),
					creation("", "https://cdn/clip.mp4"),
					// Duplicate of the first: dedupe must drop it.
					creation("https://cdn/img-1.png", ""),
				},
			},
		},
	}
	data, _ := json.Marshal(map[string]any{
		"content": map[string]any{"content_block": []any{block}},
	})

	server := sseServer(t, frame("STREAM_MSG_NOTIFY", string(data)))
	client := New(testSettings(server.URL))

	result, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "draw"})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	if len(result.Media) != 3 {
		t.Fatalf("media = %+v, want 3 unique entries after dedupe", result.Media)
	}
	kinds := map[string]int{}
	for _, item := range result.Media {
		kinds[item.Kind]++
	}
	if kinds["image"] != 2 || kinds["video"] != 1 {
		t.Fatalf("unexpected media kinds: %+v", result.Media)
	}
}

// A real stream arrives in arbitrary TCP fragments; a frame can be split
// mid-JSON and must still parse once the rest arrives.
func TestCompletionHandlesSplitFrames(t *testing.T) {
	complete := strings.Join([]string{
		notifyFrame(textBlock(10000, "分片", "")),
		notifyFrame(textBlock(10000, "测试", "")),
		frame("SSE_REPLY_END", `{}`),
	}, "")

	// Cut at deliberately awkward offsets so frames straddle the boundaries.
	var chunks []string
	for _, size := range []int{7, 23, 5, 41, 1000} {
		if len(complete) == 0 {
			break
		}
		if size > len(complete) {
			size = len(complete)
		}
		chunks = append(chunks, complete[:size])
		complete = complete[size:]
	}
	if complete != "" {
		chunks = append(chunks, complete)
	}

	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, chunk := range chunks {
			mu.Lock()
			_, _ = io.WriteString(w, chunk)
			mu.Unlock()
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer server.Close()

	client := New(testSettings(server.URL))
	result, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "hi"})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	if result.Text != "分片测试" {
		t.Fatalf("text = %q, want %q", result.Text, "分片测试")
	}
}

// The final frame may arrive without a trailing blank line.
func TestCompletionHandlesTrailingFrameWithoutBlankLine(t *testing.T) {
	body := notifyFrame(textBlock(10000, "结尾", "")) +
		"event:SSE_REPLY_END\ndata:{}" // no "\n\n"

	server := sseServer(t, body)
	client := New(testSettings(server.URL))

	result, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "hi"})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	if result.Text != "结尾" {
		t.Fatalf("text = %q", result.Text)
	}
}

// STREAM_CHUNK carries deltas inside patch_op[].patch_value.
func TestCompletionParsesStreamChunkPatches(t *testing.T) {
	patch := map[string]any{
		"patch_op": []any{
			map[string]any{
				"patch_value": map[string]any{
					"content": map[string]any{
						"content_block": []any{textBlock(10000, "增量", "")},
					},
				},
			},
		},
		"message_id": "msg_chunk",
	}
	data, _ := json.Marshal(patch)

	server := sseServer(t, frame("STREAM_CHUNK", string(data)))
	client := New(testSettings(server.URL))

	result, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "hi"})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	if result.Text != "增量" {
		t.Fatalf("text = %q", result.Text)
	}
	if result.MessageID != "msg_chunk" {
		t.Fatalf("messageID = %q", result.MessageID)
	}
}

func TestCompletionSurfacesStreamError(t *testing.T) {
	body := frame("SSE_ACK", `{}`) +
		frame("STREAM_ERROR", `{"error_msg":"quota exhausted"}`)

	server := sseServer(t, body)
	client := New(testSettings(server.URL))

	_, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "hi"})
	if err == nil {
		t.Fatal("expected an error from STREAM_ERROR")
	}
	if !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("error = %v, want it to carry the upstream message", err)
	}
}

func TestCompletionMapsHTTPFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"unauthorized", http.StatusUnauthorized, "", ErrInvalidCredential},
		{"forbidden", http.StatusForbidden, "", ErrInvalidCredential},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			client := New(testSettings(server.URL))
			_, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "hi"})
			if err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("server error carries a snippet", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, "upstream exploded")
		}))
		defer server.Close()

		client := New(testSettings(server.URL))
		_, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x", Text: "hi"})
		if err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "upstream exploded") {
			t.Fatalf("err = %v, want status and snippet", err)
		}
	})
}

func TestCompletionRejectsBadInput(t *testing.T) {
	server := sseServer(t, frame("SSE_REPLY_END", `{}`))
	client := New(testSettings(server.URL))

	if _, err := client.Completion(context.Background(), Options{Text: "hi"}); err != ErrInvalidCredential {
		t.Fatalf("missing cookie: err = %v, want ErrInvalidCredential", err)
	}
	if _, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=x"}); err == nil {
		t.Fatal("empty prompt should be rejected")
	}
}

// buildBody is the request contract: model choice, thinking flag and the
// ability payload all have to reach the upstream in the documented shape.
func TestBuildBodyMapsOptions(t *testing.T) {
	settings := config.DefaultSettings("testdata")

	fast := buildBody(settings, Options{Text: "hi", DeepThink: 0})
	option, _ := fast["option"].(map[string]any)
	if option["need_deep_think"] != 0 {
		t.Fatalf("fast: need_deep_think = %v, want 0", option["need_deep_think"])
	}
	ext, _ := fast["ext"].(map[string]any)
	if ext["use_deep_think"] != "0" {
		t.Fatalf("fast: use_deep_think = %v, want the string \"0\"", ext["use_deep_think"])
	}
	if _, present := fast["chat_ability"]; present {
		t.Fatal("plain chat should not carry chat_ability")
	}

	pro := buildBody(settings, Options{Text: "hi", DeepThink: 3})
	proOption, _ := pro["option"].(map[string]any)
	if proOption["need_deep_think"] != 3 {
		t.Fatalf("pro: need_deep_think = %v, want 3", proOption["need_deep_think"])
	}

	image := buildBody(settings, Options{Text: "draw", ChatAbility: ImageAbility()})
	ability, _ := image["chat_ability"].(map[string]any)
	if ability["ability_type"] != 3 {
		t.Fatalf("image ability_type = %v, want 3", ability["ability_type"])
	}
	if _, ok := ability["ability_param"].(string); !ok {
		t.Fatal("ability_param must be a JSON string, not an object")
	}

	messages, _ := image["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("expected a single text message, got %d", len(messages))
	}

	// Images plus text produce an attachment message ahead of the text one.
	withImage := buildBody(settings, Options{
		Text:   "look",
		Images: []UploadedImage{{URI: "uri://1", Name: "a.png", Width: 4, Height: 4}},
	})
	both, _ := withImage["messages"].([]any)
	if len(both) != 2 {
		t.Fatalf("expected attachment + text messages, got %d", len(both))
	}
}

func TestVideoAbilityCarriesModelAndDuration(t *testing.T) {
	ability := VideoAbility(10)
	if ability["ability_type"] != 17 {
		t.Fatalf("ability_type = %v, want 17", ability["ability_type"])
	}
	raw, ok := ability["ability_param"].(string)
	if !ok {
		t.Fatal("ability_param must be a JSON string")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("ability_param is not valid JSON: %v", err)
	}
	if parsed["model"] != VideoModelSeedance10 {
		t.Fatalf("model = %v, want %v", parsed["model"], VideoModelSeedance10)
	}
	// JSON numbers decode as float64.
	if parsed["duration"] != float64(10) {
		t.Fatalf("duration = %v (%T), want 10", parsed["duration"], parsed["duration"])
	}
}

// The request must carry the cookie and the AGW conversion header, since the
// upstream rejects calls without them.
func TestCompletionSendsRequiredHeaders(t *testing.T) {
	var mu sync.Mutex
	var got http.Header
	var gotQuery string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.Header.Clone()
		gotQuery = r.URL.RawQuery
		mu.Unlock()
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, frame("SSE_REPLY_END", `{}`))
	}))
	defer server.Close()

	client := New(testSettings(server.URL))
	if _, err := client.Completion(context.Background(), Options{Cookie: "oauth_token=abc; oauth_token_v2=def", Text: "hi"}); err != nil {
		t.Fatalf("completion: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got.Get("cookie") != "oauth_token=abc; oauth_token_v2=def" {
		t.Fatalf("cookie = %q", got.Get("cookie"))
	}
	if got.Get("agw-js-conv") != "str" {
		t.Fatalf("agw-js-conv = %q, want str", got.Get("agw-js-conv"))
	}
	if got.Get("content-type") != "application/json" {
		t.Fatalf("content-type = %q", got.Get("content-type"))
	}
	for _, key := range []string{"aid", "device_platform", "version_code"} {
		if !strings.Contains(gotQuery, key+"=") {
			t.Fatalf("query %q is missing %s", gotQuery, key)
		}
	}
}

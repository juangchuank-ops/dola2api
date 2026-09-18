package dola

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dola2api/internal/config"
)

// DefaultBotID is the Dola/Doubao assistant bot used by the web client.
const DefaultBotID = "7339470689562525703"

// VideoModelSeedance10 maps to "Dreamina Seedance 1.0" in the Dola UI.
const VideoModelSeedance10 = "ic_mini"

// ErrInvalidCredential marks a session cookie the upstream rejected.
var ErrInvalidCredential = errors.New("dola credential rejected")

// MediaRef is a generated image or video returned inside the SSE stream.
type MediaRef struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// Result is the aggregated outcome of one completion.
type Result struct {
	Text           string
	Thinking       string
	Media          []MediaRef
	ConversationID string
	MessageID      string
}

// UploadedImage references an image already pushed to ByteDance ImageX.
type UploadedImage struct {
	URI    string
	Name   string
	Width  int
	Height int
}

// Options describes one upstream call.
type Options struct {
	Cookie        string
	Text          string
	DeepThink     int
	ChatAbility   map[string]any
	Images        []UploadedImage
	Timeout       time.Duration
	IdleTimeout   time.Duration
	OnDelta       func(string)
	OnThinking    func(string)
	DisableStream bool
}

// Client talks to dola.com's private /chat/completion endpoint.
type Client struct {
	http     *http.Client
	settings func() config.Settings
}

// New builds a client. settingsFn is re-read on every request so runtime
// configuration changes apply without a restart.
func New(settingsFn func() config.Settings) *Client {
	transport := &http.Transport{
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > 5 {
					return errors.New("too many redirects")
				}
				return nil
			},
		},
		settings: settingsFn,
	}
}

func (c *Client) baseQuery(settings config.Settings) string {
	values := url.Values{}
	values.Set("aid", "495671")
	values.Set("device_platform", "web")
	values.Set("language", settings.Upstream.Language)
	values.Set("region", settings.Upstream.Region)
	values.Set("samantha_web", "1")
	values.Set("sys_region", settings.Upstream.Region)
	values.Set("use-olympus-account", "1")
	values.Set("version_code", "20800")
	values.Set("web_platform", "browser")
	return values.Encode()
}

func (c *Client) endpoint(settings config.Settings, path string) string {
	base := strings.TrimRight(settings.Upstream.BaseURL, "/")
	return base + path + "?" + c.baseQuery(settings)
}

func (c *Client) newRequest(ctx context.Context, settings config.Settings, method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "*/*")
	req.Header.Set("accept-language", "en-US,en;q=0.9")
	req.Header.Set("user-agent", settings.Upstream.UserAgent)
	req.Header.Set("agw-js-conv", "str")
	req.Header.Set("origin", strings.TrimRight(settings.Upstream.BaseURL, "/"))
	req.Header.Set("referer", strings.TrimRight(settings.Upstream.BaseURL, "/")+"/chat/")
	return req, nil
}

// ---------------------------------------------------------------- body build

func textMessage(text string) map[string]any {
	return map[string]any{
		"local_message_id": randomUUID(),
		"content_block": []any{
			map[string]any{
				"block_type": 10000,
				"content": map[string]any{
					"text_block": map[string]any{
						"text": text, "icon_url": "", "icon_url_dark": "", "summary": "",
					},
					"pc_event_block": "",
				},
				"block_id": randomUUID(), "parent_id": "", "meta_info": []any{}, "append_fields": []any{},
			},
		},
		"message_status": 0,
	}
}

func attachmentMessage(images []UploadedImage) map[string]any {
	attachments := make([]any, 0, len(images))
	for _, image := range images {
		name := image.Name
		if name == "" {
			name = "image.png"
		}
		attachments = append(attachments, map[string]any{
			"type":       1,
			"identifier": randomUUID(),
			"image": map[string]any{
				"name": name,
				"uri":  image.URI,
				"image_ori": map[string]any{
					"url": "", "width": image.Width, "height": image.Height, "format": "", "url_formats": map[string]any{},
				},
			},
			"parse_state": 0, "review_state": 1, "upload_status": 1, "progress": 100, "src": "",
		})
	}
	return map[string]any{
		"local_message_id": randomUUID(),
		"content_block": []any{
			map[string]any{
				"block_type": 10052,
				"content": map[string]any{
					"attachment_block": map[string]any{"attachments": attachments},
					"pc_event_block":   "",
				},
				"block_id": randomUUID(), "parent_id": "", "meta_info": []any{}, "append_fields": []any{},
			},
		},
		"message_status": 0,
	}
}

func buildBody(settings config.Settings, opts Options) map[string]any {
	botID := settings.Upstream.BotID
	if botID == "" {
		botID = DefaultBotID
	}

	messages := make([]any, 0, 2)
	if len(opts.Images) > 0 {
		messages = append(messages, attachmentMessage(opts.Images))
	}
	if opts.Text != "" || len(messages) == 0 {
		messages = append(messages, textMessage(opts.Text))
	}

	body := map[string]any{
		"client_meta": map[string]any{
			"local_conversation_id": fmt.Sprintf("local_%d%d", time.Now().UnixMilli(), time.Now().UnixNano()%1000000),
			"conversation_id":       "",
			"bot_id":                botID,
			"last_section_id":       "",
			"last_message_index":    nil,
		},
		"messages": messages,
		"option": map[string]any{
			"send_message_scene":       "",
			"create_time_ms":           time.Now().UnixMilli(),
			"collect_id":               "",
			"is_audio":                 false,
			"answer_with_suggest":      false,
			"tts_switch":               false,
			"need_deep_think":          opts.DeepThink,
			"click_clear_context":      false,
			"from_suggest":             false,
			"is_regen":                 false,
			"is_replace":               false,
			"is_from_click_option":     false,
			"is_from_click_softlink":   false,
			"disable_sse_cache":        false,
			"select_text_action":       "",
			"is_select_text":           false,
			"resend_for_regen":         false,
			"scene_type":               0,
			"unique_key":               randomUUID(),
			"start_seq":                0,
			"need_create_conversation": true,
			"regen_query_id":           []any{},
			"edit_query_id":            []any{},
			"regen_instruction":        "",
			"no_replace_for_regen":     false,
			"message_from":             0,
			"shared_app_name":          "",
			"shared_app_id":            "",
			"sse_recv_event_options":   map[string]any{"support_chunk_delta": true},
			"is_ai_playground":         false,
			"is_old_user":              false,
			"recovery_option": map[string]any{
				"is_recovery":            false,
				"req_create_time_sec":    time.Now().Unix(),
				"append_sse_event_scene": 0,
			},
			"message_storage_type": 0,
		},
		"user_context": []any{},
		"ext": map[string]any{
			"use_deep_think":                fmt.Sprintf("%d", opts.DeepThink),
			"fp":                            "",
			"collection_id":                 "",
			"commerce_credit_config_enable": "0",
		},
	}
	if opts.ChatAbility != nil {
		body["chat_ability"] = opts.ChatAbility
	}
	return body
}

// --------------------------------------------------------------- completion

// Completion performs one upstream call and returns the aggregated result.
func (c *Client) Completion(ctx context.Context, opts Options) (*Result, error) {
	settings := c.settings()
	if opts.Cookie == "" {
		return nil, ErrInvalidCredential
	}
	if opts.Text == "" && len(opts.Images) == 0 {
		return nil, errors.New("empty prompt")
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = settings.RequestTimeout()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(buildBody(settings, opts))
	if err != nil {
		return nil, err
	}

	req, err := c.newRequest(ctx, settings, http.MethodPost, c.endpoint(settings, "/chat/completion"), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("cookie", opts.Cookie)

	resp, err := c.do(req, settings)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrInvalidCredential
	}
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("dola HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	result := &Result{}
	if err := c.consumeStream(resp.Body, opts, result); err != nil {
		return result, err
	}
	result.Media = dedupeMedia(result.Media)
	return result, nil
}

func (c *Client) do(req *http.Request, settings config.Settings) (*http.Response, error) {
	if settings.Upstream.Proxy == "" {
		return c.http.Do(req)
	}
	proxyURL, err := url.Parse(settings.Upstream.Proxy)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy %q: %w", settings.Upstream.Proxy, err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:               http.ProxyURL(proxyURL),
			MaxIdleConns:        64,
			MaxIdleConnsPerHost: 16,
			IdleConnTimeout:     90 * time.Second,
		},
	}
	return client.Do(req)
}

func (c *Client) consumeStream(body io.ReadCloser, opts Options, result *Result) error {
	settings := c.settings()
	idle := opts.IdleTimeout
	if idle <= 0 {
		idle = settings.StreamIdleTimeout()
	}

	reader := bufio.NewReaderSize(body, 64*1024)
	var buffer strings.Builder
	idleTimer := time.AfterFunc(idle, func() { _ = body.Close() })
	defer idleTimer.Stop()

	chunk := make([]byte, 16*1024)
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			idleTimer.Reset(idle)
			buffer.Write(chunk[:n])
			text := buffer.String()
			for {
				index := strings.Index(text, "\n\n")
				if index < 0 {
					break
				}
				frame := text[:index]
				text = text[index+2:]
				if err := c.handleFrame(frame, opts, result); err != nil {
					return err
				}
			}
			buffer.Reset()
			buffer.WriteString(text)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if ctxErr := context.Cause(context.Background()); ctxErr != nil {
				return err
			}
			return err
		}
	}
	if tail := strings.TrimSpace(buffer.String()); tail != "" {
		if err := c.handleFrame(tail, opts, result); err != nil {
			return err
		}
	}
	return nil
}

type sseFrame struct {
	Event string
	Data  map[string]any
	Raw   string
}

func parseFrame(block string) sseFrame {
	frame := sseFrame{Event: "message"}
	var data strings.Builder
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "event:"):
			frame.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	frame.Raw = data.String()
	if frame.Raw != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(frame.Raw), &parsed); err == nil {
			frame.Data = parsed
		}
	}
	return frame
}

func (c *Client) handleFrame(block string, opts Options, result *Result) error {
	frame := parseFrame(block)
	switch frame.Event {
	case "STREAM_ERROR":
		message := "upstream stream error"
		if frame.Data != nil {
			if raw, ok := frame.Data["error_msg"].(string); ok && raw != "" {
				message = raw
			}
		}
		return errors.New(message)
	case "SSE_ACK":
		if meta := asMap(frame.Data["ack_client_meta"]); meta != nil {
			if id, ok := meta["conversation_id"].(string); ok && id != "" {
				result.ConversationID = id
			}
		}
	case "STREAM_MSG_NOTIFY":
		for _, block := range contentBlocks(frame.Data) {
			c.applyTextBlock(block, opts, result)
			collectMedia(block, result)
		}
	case "STREAM_CHUNK":
		for _, op := range asSlice(frame.Data["patch_op"]) {
			opMap := asMap(op)
			if opMap == nil {
				continue
			}
			for _, block := range contentBlocks(asMap(opMap["patch_value"])) {
				c.applyTextBlock(block, opts, result)
				collectMedia(block, result)
			}
		}
		if id, ok := frame.Data["message_id"].(string); ok && id != "" {
			result.MessageID = id
		}
	case "FULL_MSG_NOTIFY":
		message := asMap(frame.Data["message"])
		if message == nil {
			return nil
		}
		if userType, ok := message["user_type"].(float64); ok && userType == 1 {
			return nil
		}
		blocks := contentBlocks(frame.Data)
		if len(blocks) == 0 {
			if raw, ok := message["content"].(string); ok && raw != "" {
				var decoded []any
				if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
					for _, item := range decoded {
						if block := asMap(item); block != nil {
							blocks = append(blocks, block)
						}
					}
				}
			}
		}
		for _, block := range blocks {
			collectMedia(block, result)
		}
		if id, ok := message["message_id"].(string); ok && id != "" {
			result.MessageID = id
		}
	}
	return nil
}

func (c *Client) applyTextBlock(block map[string]any, opts Options, result *Result) {
	content := asMap(block["content"])
	if content == nil {
		return
	}
	textBlock := asMap(content["text_block"])
	if textBlock == nil {
		return
	}
	text, _ := textBlock["text"].(string)
	if text == "" {
		return
	}
	iconURL, _ := textBlock["icon_url"].(string)
	iconDark, _ := textBlock["icon_url_dark"].(string)
	if strings.Contains(iconURL, "Deep_Think") || strings.Contains(iconDark, "Deep_Think") {
		result.Thinking += text
		if opts.OnThinking != nil {
			opts.OnThinking(text)
		}
		return
	}
	result.Text += text
	if opts.OnDelta != nil {
		opts.OnDelta(text)
	}
}

func contentBlocks(data map[string]any) []map[string]any {
	if data == nil {
		return nil
	}
	content := asMap(data["content"])
	if content == nil {
		return nil
	}
	raw := asSlice(content["content_block"])
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if block := asMap(item); block != nil {
			out = append(out, block)
		}
	}
	return out
}

// collectMedia walks a content block looking for finished images and videos.
func collectMedia(block map[string]any, result *Result) {
	content := asMap(block["content"])
	if content == nil {
		return
	}
	if creationBlock := asMap(content["creation_block"]); creationBlock != nil {
		for _, raw := range asSlice(creationBlock["creations"]) {
			creation := asMap(raw)
			if creation == nil {
				continue
			}
			if video := asMap(creation["video"]); video != nil {
				if u := findURL(video, "url"); u != "" {
					result.Media = append(result.Media, MediaRef{Kind: "video", URL: u})
				}
				continue
			}
			if image := asMap(creation["image"]); image != nil {
				if ori := asMap(image["image_ori"]); ori != nil {
					if u, ok := ori["url"].(string); ok && strings.HasPrefix(u, "http") {
						result.Media = append(result.Media, MediaRef{Kind: "image", URL: u})
						continue
					}
				}
				if u := findURL(image, "url"); u != "" {
					result.Media = append(result.Media, MediaRef{Kind: "image", URL: u})
				}
			}
		}
		return
	}

	for key, value := range content {
		switch key {
		case "text_block", "thinking_block", "pc_event_block", "loading_block", "attachment_block":
			continue
		}
		if !strings.HasSuffix(key, "_block") {
			continue
		}
		inner := asMap(value)
		if inner == nil {
			continue
		}
		kind := strings.TrimSuffix(key, "_block")
		for innerKey, innerValue := range inner {
			if strings.Contains(strings.ToLower(innerKey), "icon") {
				continue
			}
			raw, ok := innerValue.(string)
			if !ok || !strings.HasPrefix(raw, "http") {
				continue
			}
			lower := strings.ToLower(innerKey)
			if strings.Contains(lower, "url") || strings.Contains(lower, "video") || strings.Contains(lower, "image") || strings.Contains(lower, "cover") {
				result.Media = append(result.Media, MediaRef{Kind: kind, URL: raw})
			}
		}
	}
}

func findURL(object map[string]any, pattern string) string {
	for key, value := range object {
		if strings.Contains(strings.ToLower(key), "icon") {
			continue
		}
		if raw, ok := value.(string); ok && strings.HasPrefix(raw, "http") && strings.Contains(strings.ToLower(key), pattern) {
			return raw
		}
	}
	for _, value := range object {
		if nested := asMap(value); nested != nil {
			if found := findURL(nested, pattern); found != "" {
				return found
			}
		}
	}
	return ""
}

func dedupeMedia(items []MediaRef) []MediaRef {
	seen := make(map[string]struct{}, len(items))
	out := make([]MediaRef, 0, len(items))
	for _, item := range items {
		key := item.URL
		if index := strings.Index(key, "?"); index >= 0 {
			key = key[:index]
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

// ---------------------------------------------------------------- abilities

// ImageAbility requests the free 4-image generation capability.
func ImageAbility() map[string]any {
	param, _ := json.Marshal(map[string]any{"ability_param": map[string]any{}, "ability_type": 1})
	return map[string]any{"ability_type": 3, "ability_param": string(param)}
}

// VideoAbility requests Dreamina Seedance 1.0 video generation.
func VideoAbility(duration int) map[string]any {
	if duration <= 0 {
		duration = 10
	}
	param, _ := json.Marshal(map[string]any{"model": VideoModelSeedance10, "duration": duration})
	return map[string]any{"ability_type": 17, "ability_param": string(param)}
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return nil
}

func asSlice(value any) []any {
	if value == nil {
		return nil
	}
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

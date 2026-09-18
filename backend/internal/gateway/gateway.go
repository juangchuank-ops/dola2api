package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dola2api/internal/config"
	"dola2api/internal/dola"
	"dola2api/internal/pool"
	"dola2api/internal/store"
)

// Version is reported by /health.
const Version = "0.1.0"

// Gateway exposes the OpenAI-compatible surface.
type Gateway struct {
	store    *store.Store
	pool     *pool.Pool
	client   *dola.Client
	settings func() config.Settings
	limiter  *limiter
}

func New(st *store.Store, p *pool.Pool, client *dola.Client, settings func() config.Settings) *Gateway {
	return &Gateway{
		store:    st,
		pool:     p,
		client:   client,
		settings: settings,
		limiter:  newLimiter(),
	}
}

// respWriter records whether the response has already been committed.
type respWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *respWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *respWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *respWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// ------------------------------------------------------------- chat handler

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	User     string        `json:"user"`
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// ChatCompletions implements POST /v1/chat/completions.
func (g *Gateway) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	out := &respWriter{ResponseWriter: w}
	started := time.Now()

	key, err := g.authenticate(r)
	if err != nil {
		writeError(out, http.StatusUnauthorized, err.Error())
		return
	}
	if err := g.limiter.acquire(key); err != nil {
		writeError(out, http.StatusTooManyRequests, err.Error())
		return
	}
	defer g.limiter.release(key)

	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeError(out, http.StatusBadRequest, "cannot read request body")
		return
	}

	var request chatRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeError(out, http.StatusBadRequest, "invalid JSON body")
		return
	}

	modelID := request.Model
	if modelID == "" {
		modelID = "dola-fast"
	}
	model, ok := g.store.ModelByID(modelID)
	if !ok || !model.Enabled {
		writeError(out, http.StatusBadRequest, fmt.Sprintf("unknown or disabled model %q", modelID))
		return
	}
	if model.Type != "chat" {
		writeError(out, http.StatusBadRequest, fmt.Sprintf("model %q is not a chat model", modelID))
		return
	}

	prompt, images, err := g.buildPrompt(r.Context(), request.Messages)
	if err != nil {
		writeError(out, http.StatusBadRequest, err.Error())
		return
	}
	if prompt == "" && len(images) == 0 {
		writeError(out, http.StatusBadRequest, "messages must contain text or image content")
		return
	}

	deepThink := 0
	if model.ID == "dola-pro" {
		deepThink = 3
	}

	settings := g.settings()
	var (
		lastErr      error
		accountName  string
		result       *dola.Result
		firstToken   int64
		retries      int
		streamedFlag bool
	)

	for attempt := 0; attempt < settings.Routing.MaxAttempts; attempt++ {
		lease, err := g.pool.Acquire(r.Context(), request.User)
		if err != nil {
			lastErr = err
			break
		}
		accountName = displayName(lease.Account)
		if attempt > 0 {
			retries = attempt
		}

		opts := dola.Options{
			Cookie:      lease.Account.Cookie,
			Text:        prompt,
			DeepThink:   deepThink,
			Images:      images,
			Timeout:     settings.RequestTimeout(),
			IdleTimeout: settings.StreamIdleTimeout(),
		}

		if request.Stream {
			var streamed bool
			result, firstToken, streamed, err = g.streamCompletion(out, r, opts, model, started)
			streamedFlag = streamed
		} else {
			result, err = g.client.Completion(r.Context(), opts)
		}
		lease.Release(err)

		lastErr = err
		if err == nil {
			break
		}
		if r.Context().Err() != nil || out.wroteHeader {
			break
		}
		if isPoolExhausted(err) {
			break
		}
	}

	promptTokens := estimateTokens(prompt)
	if lastErr != nil {
		g.recordAudit(r, key, model, accountName, started, http.StatusBadGateway, 0, promptTokens, 0, request.Stream, retries, string(body), "", lastErr.Error())
		if out.wroteHeader {
			writeSSEError(out, lastErr.Error())
			return
		}
		writeError(out, http.StatusBadGateway, lastErr.Error())
		return
	}
	if result == nil {
		writeError(out, http.StatusBadGateway, "upstream returned no result")
		return
	}

	completionTokens := estimateTokens(result.Text + result.Thinking)
	g.store.RecordModelUsage(model.ID, 1, int64(promptTokens+completionTokens))
	g.store.BumpClientKeyUsage(key.ID)

	if request.Stream {
		mediaURLs := g.persistMediaList(result.Media, prompt, model.ID, accountName)
		g.finishStream(out, result, model, mediaURLs, streamedFlag)
		g.recordAudit(r, key, model, accountName, started, http.StatusOK, firstToken, promptTokens, completionTokens, true, retries, string(body), result.Text, "")
		return
	}

	response := map[string]any{
		"id":      "chatcmpl-" + randomID(12),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model.ID,
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":              "assistant",
					"content":           result.Text,
					"reasoning_content": result.Thinking,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      promptTokens + completionTokens,
		},
	}
	if len(result.Media) > 0 {
		response["media"] = g.persistMediaList(result.Media, prompt, model.ID, accountName)
	}

	raw, _ := json.Marshal(response)
	g.recordAudit(r, key, model, accountName, started, http.StatusOK, firstToken, promptTokens, completionTokens, false, retries, string(body), string(raw), "")
	out.Header().Set("content-type", "application/json")
	out.WriteHeader(http.StatusOK)
	_, _ = out.Write(raw)
}

// streamCompletion forwards upstream deltas as OpenAI SSE chunks.
func (g *Gateway) streamCompletion(
	out *respWriter,
	r *http.Request,
	opts dola.Options,
	model *store.ModelConfig,
	started time.Time,
) (*dola.Result, int64, bool, error) {
	var mu sync.Mutex
	var firstToken int64
	var streamed bool
	id := "chatcmpl-" + randomID(12)
	created := time.Now().Unix()

	writeChunk := func(delta map[string]any, finish any) {
		mu.Lock()
		defer mu.Unlock()
		if !out.wroteHeader {
			out.Header().Set("content-type", "text/event-stream")
			out.Header().Set("cache-control", "no-cache")
			out.Header().Set("connection", "keep-alive")
			out.Header().Set("x-accel-buffering", "no")
			out.WriteHeader(http.StatusOK)
		}
		payload := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   model.ID,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		raw, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(out, "data: %s\n\n", raw)
		out.Flush()
	}

	writeChunk(map[string]any{"role": "assistant", "content": ""}, nil)

	opts.OnDelta = func(text string) {
		mu.Lock()
		if firstToken == 0 {
			firstToken = time.Since(started).Milliseconds()
		}
		streamed = true
		mu.Unlock()
		writeChunk(map[string]any{"content": text}, nil)
	}
	opts.OnThinking = func(text string) {
		writeChunk(map[string]any{"reasoning_content": text}, nil)
	}

	result, err := g.client.Completion(r.Context(), opts)
	if err != nil {
		return result, firstToken, streamed, err
	}
	if firstToken == 0 {
		firstToken = time.Since(started).Milliseconds()
	}
	return result, firstToken, streamed, nil
}

// finishStream terminates the SSE response, emitting any content that was not
// already streamed as deltas (media URLs, and text when the upstream produced
// no incremental frames).
func (g *Gateway) finishStream(out *respWriter, result *dola.Result, model *store.ModelConfig, mediaURLs []string, streamed bool) {
	if !out.wroteHeader {
		out.Header().Set("content-type", "text/event-stream")
		out.Header().Set("cache-control", "no-cache")
		out.Header().Set("connection", "keep-alive")
		out.Header().Set("x-accel-buffering", "no")
		out.WriteHeader(http.StatusOK)
	}
	if !streamed {
		if result.Thinking != "" {
			writeChunkRaw(out, model, map[string]any{"reasoning_content": result.Thinking}, nil)
		}
		if result.Text != "" {
			writeChunkRaw(out, model, map[string]any{"content": result.Text}, nil)
		}
	}
	if len(mediaURLs) > 0 {
		writeChunkRaw(out, model, map[string]any{"media": mediaURLs}, nil)
	}
	writeChunkRaw(out, model, map[string]any{}, "stop")
	_, _ = io.WriteString(out, "data: [DONE]\n\n")
	out.Flush()
}

func writeChunkRaw(out *respWriter, model *store.ModelConfig, delta map[string]any, finish any) {
	payload := map[string]any{
		"id":      "chatcmpl-" + randomID(12),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model.ID,
		"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
	}
	raw, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(out, "data: %s\n\n", raw)
}

// ------------------------------------------------------------ prompt build

func (g *Gateway) buildPrompt(ctx context.Context, messages []chatMessage) (string, []dola.UploadedImage, error) {
	if len(messages) == 0 {
		return "", nil, nil
	}
	var builder strings.Builder
	var images []dola.UploadedImage

	for _, message := range messages {
		text, urls := parseContent(message.Content)
		switch message.Role {
		case "system":
			builder.WriteString("[系统指令] " + text + "\n\n")
		case "assistant":
			builder.WriteString("助手：" + text + "\n")
		default:
			if len(messages) > 1 {
				builder.WriteString("用户：" + text + "\n")
			} else {
				builder.WriteString(text + "\n")
			}
		}

		for _, raw := range urls {
			data, name, err := fetchImage(ctx, raw)
			if err != nil {
				return "", nil, fmt.Errorf("fetch image: %w", err)
			}
			lease, err := g.pool.Acquire(ctx, "")
			if err != nil {
				return "", nil, err
			}
			uploaded, err := g.client.UploadImage(ctx, lease.Account.Cookie, data, name, 0, 0)
			lease.Release(err)
			if err != nil {
				return "", nil, fmt.Errorf("upload image: %w", err)
			}
			images = append(images, *uploaded)
		}
	}

	return strings.TrimSpace(builder.String()), images, nil
}

func parseContent(raw json.RawMessage) (string, []string) {
	if len(raw) == 0 {
		return "", nil
	}
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain, nil
	}
	var parts []contentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", nil
	}
	var text strings.Builder
	var urls []string
	for _, part := range parts {
		switch part.Type {
		case "text", "input_text":
			text.WriteString(part.Text)
		case "image_url", "input_image":
			if part.ImageURL.URL != "" {
				urls = append(urls, part.ImageURL.URL)
			}
		}
	}
	return text.String(), urls
}

func fetchImage(ctx context.Context, raw string) ([]byte, string, error) {
	if strings.HasPrefix(raw, "data:") {
		index := strings.Index(raw, ",")
		if index < 0 {
			return nil, "", fmt.Errorf("invalid data URI")
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw[index+1:]))
		if err != nil {
			return nil, "", err
		}
		return decoded, "image.png", nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, "", err
	}
	name := "image.png"
	if index := strings.LastIndex(raw, "/"); index >= 0 {
		candidate := raw[index+1:]
		if query := strings.Index(candidate, "?"); query >= 0 {
			candidate = candidate[:query]
		}
		if candidate != "" {
			name = candidate
		}
	}
	return data, name, nil
}

// ------------------------------------------------------------ image handler

type imageRequest struct {
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	ResponseFormat string `json:"response_format"`
	Model          string `json:"model"`
}

// ImageGenerations implements POST /v1/images/generations.
func (g *Gateway) ImageGenerations(w http.ResponseWriter, r *http.Request) {
	out := &respWriter{ResponseWriter: w}
	started := time.Now()

	key, err := g.authenticate(r)
	if err != nil {
		writeError(out, http.StatusUnauthorized, err.Error())
		return
	}
	if err := g.limiter.acquire(key); err != nil {
		writeError(out, http.StatusTooManyRequests, err.Error())
		return
	}
	defer g.limiter.release(key)

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	var request imageRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeError(out, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(request.Prompt) == "" {
		writeError(out, http.StatusBadRequest, "prompt is required")
		return
	}

	model, ok := g.store.ModelByID("dola-image")
	if !ok || !model.Enabled {
		writeError(out, http.StatusServiceUnavailable, "image generation is disabled")
		return
	}

	settings := g.settings()
	var (
		lastErr     error
		accountName string
		result      *dola.Result
	)

	for attempt := 0; attempt < settings.Routing.MaxAttempts; attempt++ {
		lease, err := g.pool.Acquire(r.Context(), "")
		if err != nil {
			lastErr = err
			break
		}
		accountName = displayName(lease.Account)
		result, err = g.client.Completion(r.Context(), dola.Options{
			Cookie:      lease.Account.Cookie,
			Text:        request.Prompt,
			ChatAbility: dola.ImageAbility(),
			Timeout:     settings.RequestTimeout(),
			IdleTimeout: settings.StreamIdleTimeout(),
		})
		lease.Release(err)
		lastErr = err
		if err == nil || isPoolExhausted(err) {
			break
		}
	}

	if lastErr != nil {
		g.recordAudit(r, key, model, accountName, started, http.StatusBadGateway, 0, 0, 0, false, 0, string(body), "", lastErr.Error())
		writeError(out, http.StatusBadGateway, lastErr.Error())
		return
	}

	urls := g.persistMediaList(result.Media, request.Prompt, model.ID, accountName)
	items := make([]any, 0, len(urls))
	for index, url := range urls {
		if request.ResponseFormat == "b64_json" {
			path := filepath.Join(settings.Media.GeneratedDir, filepath.Base(url))
			if data, err := os.ReadFile(path); err == nil {
				items = append(items, map[string]any{"b64_json": base64.StdEncoding.EncodeToString(data)})
				continue
			}
		}
		source := ""
		if index < len(result.Media) {
			source = result.Media[index].URL
		}
		items = append(items, map[string]any{"url": url, "source_url": source})
	}

	g.store.RecordModelUsage(model.ID, 1, 0)
	g.store.BumpClientKeyUsage(key.ID)
	response := map[string]any{"created": time.Now().Unix(), "data": items}
	raw, _ := json.Marshal(response)
	g.recordAudit(r, key, model, accountName, started, http.StatusOK, 0, 0, 0, false, 0, string(body), string(raw), "")

	out.Header().Set("content-type", "application/json")
	out.WriteHeader(http.StatusOK)
	_, _ = out.Write(raw)
}

// ------------------------------------------------------------ video handler

type videoRequest struct {
	Prompt   string `json:"prompt"`
	Duration int    `json:"duration"`
	Model    string `json:"model"`
}

// VideoGenerations implements POST /v1/videos/generations.
func (g *Gateway) VideoGenerations(w http.ResponseWriter, r *http.Request) {
	out := &respWriter{ResponseWriter: w}
	started := time.Now()

	key, err := g.authenticate(r)
	if err != nil {
		writeError(out, http.StatusUnauthorized, err.Error())
		return
	}
	if err := g.limiter.acquire(key); err != nil {
		writeError(out, http.StatusTooManyRequests, err.Error())
		return
	}
	defer g.limiter.release(key)

	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	var request videoRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeError(out, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(request.Prompt) == "" {
		writeError(out, http.StatusBadRequest, "prompt is required")
		return
	}
	if request.Model != "" && request.Model != "dreamina-seedance-1.0" {
		writeError(out, http.StatusBadRequest, "only dreamina-seedance-1.0 is supported")
		return
	}

	model, ok := g.store.ModelByID("dreamina-seedance-1.0")
	if !ok || !model.Enabled {
		writeError(out, http.StatusServiceUnavailable, "video generation is disabled")
		return
	}

	lease, err := g.pool.Acquire(r.Context(), "")
	if err != nil {
		writeError(out, http.StatusServiceUnavailable, err.Error())
		return
	}
	accountName := displayName(lease.Account)

	duration := request.Duration
	if duration <= 0 {
		duration = 10
	}

	// The create phase only acknowledges the job and the finished clip is not
	// part of that stream, so the job continues on a detached context.
	go func(lease *pool.Lease, cookie, prompt, account string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		result, err := g.client.Completion(ctx, dola.Options{
			Cookie:      cookie,
			Text:        prompt,
			ChatAbility: dola.VideoAbility(duration),
			Timeout:     15 * time.Minute,
			IdleTimeout: 3 * time.Minute,
		})
		lease.Release(err)
		if err != nil {
			return
		}
		g.persistMediaList(result.Media, prompt, model.ID, account)
	}(lease, lease.Account.Cookie, request.Prompt, accountName)

	g.store.RecordModelUsage(model.ID, 1, 0)
	g.store.BumpClientKeyUsage(key.ID)
	response := map[string]any{
		"id":       "video_" + randomID(10),
		"object":   "video.generation",
		"status":   "processing",
		"model":    model.ID,
		"duration": duration,
		"message":  "视频正在生成，完成后会出现在生成画廊",
	}
	raw, _ := json.Marshal(response)
	g.recordAudit(r, key, model, accountName, started, http.StatusAccepted, 0, 0, 0, false, 0, string(body), string(raw), "")

	out.Header().Set("content-type", "application/json")
	out.WriteHeader(http.StatusAccepted)
	_, _ = out.Write(raw)
}

// Models implements GET /v1/models.
func (g *Gateway) Models(w http.ResponseWriter, r *http.Request) {
	if _, err := g.authenticate(r); err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	data := make([]any, 0, 4)
	for _, model := range g.store.ListModels() {
		if !model.Enabled {
			continue
		}
		data = append(data, map[string]any{"id": model.ID, "object": "model", "created": 0, "owned_by": "dola"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// Health implements GET /health.
func (g *Gateway) Health(w http.ResponseWriter, r *http.Request) {
	total, active, cooldown, disabled, invalid, routable := g.pool.Summary()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": Version,
		"pool": map[string]any{
			"total": total, "active": active, "cooldown": cooldown,
			"disabled": disabled, "invalid": invalid, "routable": routable,
		},
	})
}

// ------------------------------------------------------------- media helper

func (g *Gateway) persistMediaList(media []dola.MediaRef, prompt, model, account string) []string {
	urls := make([]string, 0, len(media))
	for _, item := range media {
		urls = append(urls, g.persistMedia(item, prompt, model, account))
	}
	return urls
}

func (g *Gateway) persistMedia(media dola.MediaRef, prompt, model, account string) string {
	settings := g.settings()
	item := &store.MediaItem{
		ID:          "media_" + randomID(8),
		Kind:        media.Kind,
		SourceURL:   media.URL,
		URL:         media.URL,
		Prompt:      prompt,
		Model:       model,
		AccountName: account,
		CreatedAt:   time.Now(),
	}

	if settings.Media.AutoDownload {
		extension := ".bin"
		switch media.Kind {
		case "image":
			extension = ".png"
		case "video":
			extension = ".mp4"
		}
		filename := item.ID + extension
		target := filepath.Join(settings.Media.GeneratedDir, filename)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, media.URL, nil)
		if err == nil {
			if resp, err := http.DefaultClient.Do(req); err == nil {
				if resp.StatusCode < 400 {
					if file, err := os.Create(target); err == nil {
						if _, err := io.Copy(file, io.LimitReader(resp.Body, 256<<20)); err == nil {
							item.URL = "/media/" + filename
						} else {
							_ = os.Remove(target)
						}
						_ = file.Close()
					}
				}
				_ = resp.Body.Close()
			}
		}
		cancel()
	}

	g.store.AddMedia(item)
	return item.URL
}

// --------------------------------------------------------------- audit glue

func (g *Gateway) recordAudit(
	r *http.Request,
	key *store.ClientKey,
	model *store.ModelConfig,
	accountName string,
	started time.Time,
	status int,
	firstTokenMs int64,
	promptTokens, completionTokens int,
	stream bool,
	retries int,
	requestBody, responseBody, errText string,
) {
	settings := g.settings()
	audit := &store.Audit{
		ID:               "req_" + randomID(10),
		CreatedAt:        time.Now(),
		KeyName:          key.Name,
		Model:            model.ID,
		AccountName:      accountName,
		Status:           status,
		LatencyMs:        time.Since(started).Milliseconds(),
		FirstTokenMs:     firstTokenMs,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Stream:           stream,
		Retries:          retries,
		IP:               clientIP(r),
		UserAgent:        r.Header.Get("user-agent"),
		Error:            errText,
	}
	if settings.Audit.RecordBody {
		audit.RequestBody = clampBody(requestBody, settings.Audit.BodyLimitBytes)
		audit.ResponseBody = clampBody(responseBody, settings.Audit.BodyLimitBytes)
	}
	g.store.AppendAudit(audit)
}

func clampBody(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// ------------------------------------------------------------------ helpers

func (g *Gateway) authenticate(r *http.Request) (*store.ClientKey, error) {
	raw := strings.TrimSpace(r.Header.Get("authorization"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("x-api-key"))
	}
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	if raw == "" {
		return nil, fmt.Errorf("missing API key")
	}
	key, ok := g.store.ClientKeyByValue(raw)
	if !ok {
		return nil, fmt.Errorf("invalid API key")
	}
	if !key.Enabled {
		return nil, fmt.Errorf("API key disabled")
	}
	return key, nil
}

func displayName(account *store.Account) string {
	if account.Name != "" {
		return account.Name
	}
	return store.MaskCookie(account.Cookie)
}

func isPoolExhausted(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "no account available") || strings.Contains(message, "all accounts are busy")
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("x-forwarded-for"); forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	host := r.RemoteAddr
	if index := strings.LastIndex(host, ":"); index >= 0 {
		host = host[:index]
	}
	return host
}

func randomID(length int) string {
	buf := make([]byte, (length+1)/2)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)[:length]
}

func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return len([]rune(text))/2 + 1
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "encode failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": "dola2api_error"}})
}

func writeSSEError(w http.ResponseWriter, message string) {
	raw, _ := json.Marshal(map[string]any{"error": map[string]any{"message": message}})
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
}

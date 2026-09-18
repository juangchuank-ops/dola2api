package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"dola2api/internal/config"
	"dola2api/internal/dola"
	"dola2api/internal/gateway"
	"dola2api/internal/pool"
	"dola2api/internal/store"
)

// API serves the admin console.
type API struct {
	store    *store.Store
	pool     *pool.Pool
	client   *dola.Client
	settings func() config.Settings
	started  time.Time
}

func New(st *store.Store, p *pool.Pool, client *dola.Client, settings func() config.Settings) *API {
	return &API{store: st, pool: p, client: client, settings: settings, started: time.Now()}
}

// Register installs every admin route on the mux.
func (a *API) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /admin/api/auth/login", a.login)
	mux.HandleFunc("GET /admin/api/auth/me", a.guard(a.me))
	mux.HandleFunc("POST /admin/api/auth/logout", a.guard(a.logout))
	mux.HandleFunc("POST /admin/api/auth/password", a.guard(a.changePassword))

	mux.HandleFunc("GET /admin/api/dashboard", a.guard(a.dashboard))

	mux.HandleFunc("GET /admin/api/accounts", a.guard(a.listAccounts))
	mux.HandleFunc("POST /admin/api/accounts", a.guard(a.createAccount))
	mux.HandleFunc("GET /admin/api/accounts/groups", a.guard(a.accountGroups))
	mux.HandleFunc("GET /admin/api/accounts/export", a.guard(a.exportAccounts))
	mux.HandleFunc("POST /admin/api/accounts/import", a.guard(a.importAccounts))
	mux.HandleFunc("POST /admin/api/accounts/batch", a.guard(a.batchAccounts))
	mux.HandleFunc("POST /admin/api/accounts/probe-all", a.guard(a.probeAll))
	mux.HandleFunc("POST /admin/api/accounts/quota-all", a.guard(a.quotaAll))
	mux.HandleFunc("POST /admin/api/accounts/cleanup", a.guard(a.cleanupAccounts))
	mux.HandleFunc("PATCH /admin/api/accounts/{id}", a.guard(a.updateAccount))
	mux.HandleFunc("DELETE /admin/api/accounts/{id}", a.guard(a.deleteAccount))
	mux.HandleFunc("POST /admin/api/accounts/{id}/probe", a.guard(a.probeAccount))
	mux.HandleFunc("POST /admin/api/accounts/{id}/quota", a.guard(a.refreshQuota))

	mux.HandleFunc("GET /admin/api/client-keys", a.guard(a.listClientKeys))
	mux.HandleFunc("POST /admin/api/client-keys", a.guard(a.createClientKey))
	mux.HandleFunc("PATCH /admin/api/client-keys/{id}", a.guard(a.updateClientKey))
	mux.HandleFunc("DELETE /admin/api/client-keys/{id}", a.guard(a.deleteClientKey))

	mux.HandleFunc("GET /admin/api/models", a.guard(a.listModels))
	mux.HandleFunc("PATCH /admin/api/models/{id}", a.guard(a.updateModel))

	mux.HandleFunc("GET /admin/api/audits", a.guard(a.listAudits))
	mux.HandleFunc("DELETE /admin/api/audits", a.guard(a.clearAudits))
	mux.HandleFunc("GET /admin/api/audits/{id}", a.guard(a.auditDetail))

	mux.HandleFunc("GET /admin/api/settings", a.guard(a.getSettings))
	mux.HandleFunc("PUT /admin/api/settings", a.guard(a.saveSettings))

	mux.HandleFunc("GET /admin/api/gallery", a.guard(a.gallery))
}

// ---------------------------------------------------------------- middleware

type ctxKey string

const sessionKey ctxKey = "session"

func (a *API) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" || !a.store.ValidateSession(token) {
			writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
			return
		}
		next(w, r)
	}
}

func bearer(r *http.Request) string {
	raw := strings.TrimSpace(r.Header.Get("authorization"))
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		return strings.TrimSpace(raw[7:])
	}
	return ""
}

// --------------------------------------------------------------------- auth

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var request loginRequest
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if !a.store.VerifyPassword(strings.TrimSpace(request.Username), request.Password) {
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	token, _ := a.store.IssueSession()
	writeJSON(w, http.StatusOK, map[string]any{
		"token":    token,
		"username": request.Username,
		"role":     "admin",
	})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	admin := a.store.AdminProfile()
	writeJSON(w, http.StatusOK, map[string]any{"username": admin.Username, "role": "admin"})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	a.store.RevokeSession(bearer(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decode(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	admin := a.store.AdminProfile()
	if !a.store.VerifyPassword(admin.Username, request.CurrentPassword) {
		writeError(w, http.StatusBadRequest, "当前密码不正确")
		return
	}
	if err := a.store.SetPassword(admin.Username, request.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.store.RevokeAllSessions()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------- dashboard

func (a *API) dashboard(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	days := parsePeriodDays(period)
	timezone := r.URL.Query().Get("timezone")
	location := time.UTC
	if timezone != "" {
		if parsed, err := time.LoadLocation(timezone); err == nil {
			location = parsed
		}
	}

	settings := a.settings()
	now := time.Now()
	windowStart := now.AddDate(0, 0, -days)

	audits := a.store.ListAudits()
	accounts := a.store.ListAccounts()
	models := a.store.ListModels()
	keys := a.store.ListClientKeys()

	var (
		requests         int
		failures         int
		promptTokens     int
		completionTokens int
		reasoningTokens  int
		firstTokenSum    int64
		firstTokenCount  int
		latencySum       int64
		latencyCount     int
	)

	modelCounts := map[string]int{}
	modelTokens := map[string]int{}
	bucketRequests := map[string]int{}
	bucketFailures := map[string]int{}
	bucketTokens := map[string]int{}

	for _, audit := range audits {
		if audit.CreatedAt.Before(windowStart) {
			continue
		}
		requests++
		if audit.Status >= 400 {
			failures++
		}
		promptTokens += audit.PromptTokens
		completionTokens += audit.CompletionTokens
		if audit.FirstTokenMs > 0 {
			firstTokenSum += audit.FirstTokenMs
			firstTokenCount++
		}
		if audit.LatencyMs > 0 {
			latencySum += audit.LatencyMs
			latencyCount++
		}
		modelCounts[audit.Model]++
		modelTokens[audit.Model] += audit.PromptTokens + audit.CompletionTokens

		key := bucketKey(audit.CreatedAt.In(location), days)
		bucketRequests[key]++
		bucketTokens[key] += audit.PromptTokens + audit.CompletionTokens
		if audit.Status >= 400 {
			bucketFailures[key]++
		}
	}

	successRate := 0.0
	if requests > 0 {
		successRate = float64(requests-failures) / float64(requests) * 100
	}
	averageFirstToken := int64(0)
	if firstTokenCount > 0 {
		averageFirstToken = firstTokenSum / int64(firstTokenCount)
	}
	averageLatency := int64(0)
	if latencyCount > 0 {
		averageLatency = latencySum / int64(latencyCount)
	}

	type modelRow struct {
		Model    string `json:"model"`
		Requests int    `json:"requests"`
		Tokens   int    `json:"tokens"`
	}
	topModels := make([]modelRow, 0, len(modelCounts))
	for model, count := range modelCounts {
		topModels = append(topModels, modelRow{Model: model, Requests: count, Tokens: modelTokens[model]})
	}
	sort.Slice(topModels, func(i, j int) bool { return topModels[i].Requests > topModels[j].Requests })
	if len(topModels) > 8 {
		topModels = topModels[:8]
	}

	type trendRow struct {
		Bucket   string `json:"bucket"`
		Requests int    `json:"requests"`
		Failures int    `json:"failures"`
		Tokens   int    `json:"tokens"`
	}
	trend := make([]trendRow, 0, len(bucketRequests))
	for key, count := range bucketRequests {
		trend = append(trend, trendRow{Bucket: key, Requests: count, Failures: bucketFailures[key], Tokens: bucketTokens[key]})
	}
	sort.Slice(trend, func(i, j int) bool { return trend[i].Bucket < trend[j].Bucket })

	// routable counts accounts the scheduler can actually use right now. It is
	// not the same as active: an account whose cooldown has already elapsed is
	// still labelled cooldown but is perfectly schedulable, and reporting only
	// the status count made a healthy pool look dead.
	_, active, cooldown, disabled, invalid, routable := a.pool.Summary()
	enabledModels := 0
	for _, model := range models {
		if model.Enabled {
			enabledModels++
		}
	}
	activeKeys := 0
	for _, key := range keys {
		if key.Enabled {
			activeKeys++
		}
	}

	type activityRow struct {
		ID        string    `json:"id"`
		Time      time.Time `json:"time"`
		Model     string    `json:"model"`
		Status    int       `json:"status"`
		LatencyMs int64     `json:"latencyMs"`
		Account   string    `json:"account"`
	}
	activity := make([]activityRow, 0, 8)
	for _, audit := range audits {
		if len(activity) == 8 {
			break
		}
		activity = append(activity, activityRow{
			ID: audit.ID, Time: audit.CreatedAt, Model: audit.Model,
			Status: audit.Status, LatencyMs: audit.LatencyMs, Account: audit.AccountName,
		})
	}

	type distributionRow struct {
		Type  string `json:"type"`
		Count int    `json:"count"`
	}
	kindCounts := map[string]int{}
	for _, account := range accounts {
		kind := account.Kind
		if kind == "" {
			kind = store.KindCookie
		}
		kindCounts[kind]++
	}
	distribution := make([]distributionRow, 0, len(kindCounts))
	for kind, count := range kindCounts {
		label := "Cookie"
		if kind == store.KindGuest {
			label = "游客"
		}
		distribution = append(distribution, distributionRow{Type: label, Count: count})
	}
	sort.Slice(distribution, func(i, j int) bool { return distribution[i].Count > distribution[j].Count })

	writeJSON(w, http.StatusOK, map[string]any{
		"usage": map[string]any{
			"requests":                     requests,
			"failedRequests":               failures,
			"successRate":                  successRate,
			"inputTokens":                  promptTokens,
			"outputTokens":                 completionTokens,
			"reasoningTokens":              reasoningTokens,
			"tokens":                       promptTokens + completionTokens,
			"averageFirstTokenMs":          averageFirstToken,
			"firstTokenSamples":            firstTokenCount,
			"averageLatencyMs":             averageLatency,
			"averageOutputTokensPerSecond": 0,
			"throughputSamples":            0,
			"estimatedCostUsd":             0,
		},
		"resources": map[string]any{
			"totalAccounts":    len(accounts),
			"activeAccounts":   active,
			"routableAccounts": routable,
			"cooldownAccounts": cooldown,
			"disabledAccounts": disabled,
			"invalidAccounts":  invalid,
			"totalModels":      len(models),
			"enabledModels":    enabledModels,
			"totalClientKeys":  len(keys),
			"activeClientKeys": activeKeys,
		},
		"trend":        trend,
		"topModels":    topModels,
		"distribution": distribution,
		"activity":     activity,
		"upstream": map[string]any{
			"baseURL":   settings.Upstream.BaseURL,
			"poolTotal": len(accounts),
			// Kept in sync with /health, which has always reported the routable
			// count under the same name.
			"poolAvailable":    routable,
			"averageLatencyMs": averageLatency,
		},
	})
}

func parsePeriodDays(value string) int {
	value = strings.TrimSuffix(value, "d")
	days, err := strconv.Atoi(value)
	if err != nil || days <= 0 {
		return 30
	}
	if days > 365 {
		return 365
	}
	return days
}

func bucketKey(at time.Time, days int) string {
	if days <= 2 {
		return at.Format("2006-01-02T15:00")
	}
	return at.Format("2006-01-02")
}

// ----------------------------------------------------------------- accounts

func (a *API) listAccounts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	// pageSize was bounded but page was not: a large enough page makes
	// (page-1)*pageSize overflow into a negative offset, and the slice
	// expression at the end of this handler then panics.
	page := clampInt(config.ParseIntOrDefault(query.Get("page"), 1), 1, 1_000_000)
	pageSize := clampInt(config.ParseIntOrDefault(query.Get("pageSize"), 20), 1, 500)
	search := strings.ToLower(strings.TrimSpace(query.Get("search")))
	status := query.Get("status")
	kind := query.Get("kind")
	group := query.Get("group")
	sortBy := query.Get("sortBy")
	sortOrder := query.Get("sortOrder")

	inflight := a.pool.Snapshot()
	accounts := a.store.ListAccounts()

	views := make([]store.AccountView, 0, len(accounts))
	for _, account := range accounts {
		if status != "" && account.Status != status {
			continue
		}
		if kind != "" && account.Kind != kind {
			continue
		}
		if group != "" && account.Group != group {
			continue
		}
		if search != "" {
			// Match the masked cookie, never the raw one. AccountView exists
			// precisely so the raw cookie never leaves the server, and matching
			// it here would let an operator read it back out through the search
			// box, one character at a time.
			haystack := strings.ToLower(account.Name + " " + account.Remark + " " + account.Group + " " + store.MaskCookie(account.Cookie))
			if !strings.Contains(haystack, search) {
				continue
			}
		}
		views = append(views, store.NewAccountView(account, inflight[account.ID]))
	}

	sortViews(views, sortBy, sortOrder)

	total := len(views)
	// The start < 0 guard covers arithmetic overflow; the page clamp above
	// already makes it unreachable, but the two together mean this slice
	// expression cannot be handed a negative index.
	start := (page - 1) * pageSize
	if start < 0 || start > total {
		start = total
	}
	end := minInt(start+pageSize, total)

	_, active, cooldown, disabled, invalid, routable := a.pool.Summary()
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    views[start:end],
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
		"summary": map[string]any{
			"total": total, "active": active, "cooldown": cooldown,
			"disabled": disabled, "invalid": invalid, "routable": routable,
		},
	})
}

func sortViews(views []store.AccountView, field, order string) {
	if field == "" {
		return
	}
	less := func(i, j int) bool {
		left, right := views[i], views[j]
		switch field {
		case "name":
			return strings.ToLower(left.Name) < strings.ToLower(right.Name)
		case "status":
			return left.Status < right.Status
		case "priority":
			return left.Priority < right.Priority
		case "lastUsedAt":
			return left.LastUsedAt.Before(right.LastUsedAt)
		default:
			return left.CreatedAt.Before(right.CreatedAt)
		}
	}
	sort.SliceStable(views, func(i, j int) bool {
		if order == "asc" {
			return less(i, j)
		}
		return less(j, i)
	})
}

func (a *API) accountGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"groups": a.store.AccountGroups()})
}

// accountPayload is shared by create and update. Group and remark are pointers
// so that a PATCH can tell "field not sent" (leave it alone) apart from "field
// sent as empty" (clear it). As plain strings they were overwritten
// unconditionally, so patching only the enabled flag silently wiped an
// account's grouping - which contradicts the Partial<> type the console
// declares for this call.
type accountPayload struct {
	Name          string  `json:"name"`
	Cookie        string  `json:"cookie"`
	Group         *string `json:"group"`
	Remark        *string `json:"remark"`
	Priority      int     `json:"priority"`
	MaxConcurrent int     `json:"maxConcurrent"`
	Enabled       *bool   `json:"enabled"`
	Kind          string  `json:"kind"`
}

func (a *API) createAccount(w http.ResponseWriter, r *http.Request) {
	var payload accountPayload
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	payload.Cookie = strings.TrimSpace(payload.Cookie)
	if payload.Cookie == "" {
		writeError(w, http.StatusBadRequest, "Cookie 不能为空")
		return
	}
	if !strings.Contains(payload.Cookie, "=") {
		writeError(w, http.StatusBadRequest, "Cookie 格式不正确，应形如 name=value; name2=value2")
		return
	}

	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	if payload.Name == "" {
		payload.Name = "dola-" + time.Now().Format("0102-150405")
	}

	account := &store.Account{
		Name:   strings.TrimSpace(payload.Name),
		Kind:   firstNonEmpty(payload.Kind, store.KindCookie),
		Cookie: payload.Cookie,
		Group:  optionalString(payload.Group),
		Remark: optionalString(payload.Remark),
		Enabled: enabled,
		// clampPositive leaves 0 alone so AddAccount can apply its own default
		// (priority 50, concurrency 2). Clamping 0 into the range instead made
		// every account created without an explicit priority outrank all the
		// existing ones.
		Priority:      clampPositive(payload.Priority, 1, 100),
		MaxConcurrent: clampPositive(payload.MaxConcurrent, 1, 256),
	}
	if err := a.store.AddAccount(account); err != nil {
		if err == store.ErrAlreadyExists {
			writeError(w, http.StatusConflict, "该 Cookie 已存在于号池中")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// The quota probe talks to the upstream and can take a while, so the
	// account is returned immediately and the probe continues in background.
	go a.syncQuotaDetached(account.ID)
	writeJSON(w, http.StatusOK, map[string]any{"account": a.view(account.ID)})
}

func (a *API) updateAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var payload accountPayload
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	account, err := a.store.UpdateAccount(id, func(account *store.Account) {
		if strings.TrimSpace(payload.Name) != "" {
			account.Name = strings.TrimSpace(payload.Name)
		}
		if cookie := strings.TrimSpace(payload.Cookie); cookie != "" {
			account.Cookie = cookie
			account.Status = store.StatusActive
			account.FailCount = 0
			account.LastError = ""
		}
		// Only touch these when the caller actually sent them; a nil pointer
		// means "unchanged", an empty string means "clear it".
		if payload.Group != nil {
			account.Group = strings.TrimSpace(*payload.Group)
		}
		if payload.Remark != nil {
			account.Remark = strings.TrimSpace(*payload.Remark)
		}
		if payload.Priority > 0 {
			account.Priority = clampInt(payload.Priority, 1, 100)
		}
		if payload.MaxConcurrent > 0 {
			account.MaxConcurrent = clampInt(payload.MaxConcurrent, 1, 256)
		}
		if payload.Enabled != nil {
			account.Enabled = *payload.Enabled
		}
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "账号不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": a.view(account.ID)})
}

func (a *API) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteAccount(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "账号不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) batchAccounts(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Action        string   `json:"action"`
		IDs           []string `json:"ids"`
		MaxConcurrent int      `json:"maxConcurrent"`
	}
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(payload.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "未选择账号")
		return
	}

	switch payload.Action {
	case "delete":
		deleted, err := a.store.DeleteAccounts(payload.IDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
	case "enable", "disable":
		enabled := payload.Action == "enable"
		updated, err := a.store.UpdateAccounts(payload.IDs, func(account *store.Account) {
			account.Enabled = enabled
			if !enabled {
				account.Status = store.StatusDisabled
			} else if account.Status == store.StatusDisabled {
				account.Status = store.StatusActive
			}
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"updated": updated})
	case "clearCooldown":
		updated, err := a.store.UpdateAccounts(payload.IDs, func(account *store.Account) {
			account.Status = store.StatusActive
			account.CooldownUntil = time.Time{}
			account.FailCount = 0
			account.LastError = ""
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"updated": updated})
	case "concurrency":
		value := clampInt(payload.MaxConcurrent, 1, 256)
		updated, err := a.store.UpdateAccounts(payload.IDs, func(account *store.Account) {
			account.MaxConcurrent = value
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"updated": updated})
	case "quota":
		succeeded, failed := 0, 0
		for _, id := range payload.IDs {
			if err := a.syncQuota(r.Context(), id); err != nil {
				failed++
				continue
			}
			succeeded++
		}
		writeJSON(w, http.StatusOK, map[string]any{"succeeded": succeeded, "failed": failed})
	default:
		writeError(w, http.StatusBadRequest, "不支持的操作")
	}
}

func (a *API) cleanupAccounts(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Statuses []string `json:"statuses"`
	}
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if len(payload.Statuses) == 0 {
		writeError(w, http.StatusBadRequest, "请至少选择一种状态")
		return
	}
	deleted, err := a.store.DeleteAccountsByStatus(payload.Statuses)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (a *API) importAccounts(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Cookies string          `json:"cookies"`
		JSON    json.RawMessage `json:"json"`
	}
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	entries := parseImport(payload.Cookies, payload.JSON)
	if len(entries) == 0 {
		writeError(w, http.StatusBadRequest, "没有解析到可导入的账号")
		return
	}

	created, updated, failed := 0, 0, 0
	errors := make([]string, 0, 4)
	for _, entry := range entries {
		cookie := strings.TrimSpace(entry.Cookie)
		if cookie == "" || !strings.Contains(cookie, "=") {
			failed++
			continue
		}
		existing := a.findByCookie(cookie)
		if existing != nil {
			_, err := a.store.UpdateAccount(existing.ID, func(account *store.Account) {
				account.Cookie = cookie
				if entry.Name != "" {
					account.Name = entry.Name
				}
				account.Status = store.StatusActive
				account.FailCount = 0
				account.LastError = ""
			})
			if err != nil {
				failed++
				if len(errors) < 4 {
					errors = append(errors, err.Error())
				}
				continue
			}
			updated++
			continue
		}
		name := entry.Name
		if name == "" {
			name = fmt.Sprintf("dola-%02d-%s", created+1, time.Now().Format("0102"))
		}
		account := &store.Account{
			Name:          name,
			Kind:          store.KindCookie,
			Cookie:        cookie,
			Group:         entry.Group,
			Remark:        entry.Remark,
			Enabled:       true,
			Priority:      firstNonZero(entry.Priority, 50),
			MaxConcurrent: firstNonZero(entry.MaxConcurrent, 2),
		}
		if err := a.store.AddAccount(account); err != nil {
			failed++
			if len(errors) < 4 {
				errors = append(errors, err.Error())
			}
			continue
		}
		created++
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"created": created, "updated": updated, "failed": failed, "errors": errors,
	})
}

type importEntry struct {
	Name          string `json:"name"`
	Cookie        string `json:"cookie"`
	Group         string `json:"group"`
	Remark        string `json:"remark"`
	Priority      int    `json:"priority"`
	MaxConcurrent int    `json:"maxConcurrent"`
}

func parseImport(rawCookies string, rawJSON json.RawMessage) []importEntry {
	if len(rawJSON) > 0 {
		var list []importEntry
		if err := json.Unmarshal(rawJSON, &list); err == nil && len(list) > 0 {
			return list
		}
		var wrapped struct {
			Accounts []importEntry `json:"accounts"`
		}
		if err := json.Unmarshal(rawJSON, &wrapped); err == nil && len(wrapped.Accounts) > 0 {
			return wrapped.Accounts
		}
	}

	entries := make([]importEntry, 0, 16)
	for _, line := range strings.Split(rawCookies, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Accept either "cookie" or "name----cookie".
		if index := strings.Index(line, "----"); index > 0 {
			entries = append(entries, importEntry{Name: strings.TrimSpace(line[:index]), Cookie: strings.TrimSpace(line[index+4:])})
			continue
		}
		entries = append(entries, importEntry{Cookie: line})
	}
	return entries
}

func (a *API) exportAccounts(w http.ResponseWriter, r *http.Request) {
	limit := clampInt(config.ParseIntOrDefault(r.URL.Query().Get("limit"), 10000), 1, 10000)
	accounts := a.store.ListAccounts()
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].CreatedAt.After(accounts[j].CreatedAt) })
	if len(accounts) > limit {
		accounts = accounts[:limit]
	}
	out := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, map[string]any{
			"name": account.Name, "cookie": account.Cookie, "group": account.Group,
			"remark": account.Remark, "priority": account.Priority, "maxConcurrent": account.MaxConcurrent,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out, "count": len(out)})
}

func (a *API) probeAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	account, ok := a.store.AccountByID(id)
	if !ok {
		writeError(w, http.StatusNotFound, "账号不存在")
		return
	}
	probeCtx, cancel := context.WithTimeout(r.Context(), probeTimeout(a.settings()))
	defer cancel()
	started := time.Now()
	_, err := a.client.Probe(probeCtx, account.Cookie)
	latency := time.Since(started).Milliseconds()
	message := "OK"
	if err != nil {
		message = err.Error()
	}
	a.store.SaveAccountState(id, func(account *store.Account) {
		account.Quota = &store.Quota{
			SyncedAt: time.Now(), Available: err == nil, LatencyMs: latency,
			Plan: quotaPlan(account), Note: message,
		}
		if err != nil {
			if err == dola.ErrInvalidCredential {
				account.Status = store.StatusInvalid
			} else if account.Status != store.StatusDisabled {
				account.Status = store.StatusCooldown
				account.CooldownUntil = time.Now().Add(a.settings().CooldownBase())
			}
			account.LastError = truncate(message, 200)
			return
		}
		account.Status = store.StatusActive
		account.CooldownUntil = time.Time{}
		account.LastError = ""
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": err == nil, "latencyMs": latency, "message": message, "account": a.view(id),
	})
}

func (a *API) probeAll(w http.ResponseWriter, r *http.Request) {
	accounts := a.store.ListAccounts()
	healthy, unhealthy := 0, 0
	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout(a.settings()))
		_, err := a.client.Probe(ctx, account.Cookie)
		cancel()
		if err != nil {
			unhealthy++
			a.store.SaveAccountState(account.ID, func(target *store.Account) {
				target.LastError = truncate(err.Error(), 200)
				if err == dola.ErrInvalidCredential {
					target.Status = store.StatusInvalid
				} else if target.Status != store.StatusDisabled {
					target.Status = store.StatusCooldown
					target.CooldownUntil = time.Now().Add(a.settings().CooldownBase())
				}
			})
			continue
		}
		healthy++
		a.store.SaveAccountState(account.ID, func(target *store.Account) {
			target.Status = store.StatusActive
			target.CooldownUntil = time.Time{}
			target.LastError = ""
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"healthy": healthy, "unhealthy": unhealthy})
}

func (a *API) refreshQuota(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.syncQuota(r.Context(), id); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": a.view(id)})
}

func (a *API) quotaAll(w http.ResponseWriter, r *http.Request) {
	accounts := a.store.ListAccounts()
	succeeded, failed := 0, 0
	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		if err := a.syncQuota(r.Context(), account.ID); err != nil {
			failed++
			continue
		}
		succeeded++
	}
	writeJSON(w, http.StatusOK, map[string]any{"succeeded": succeeded, "failed": failed})
}

// syncQuotaDetached runs a quota probe outside the request lifecycle with a
// bounded timeout, so a slow upstream never blocks the console.
func (a *API) syncQuotaDetached(id string) {
	timeout := a.settings().RequestTimeout()
	if timeout > 90*time.Second {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_ = a.syncQuota(ctx, id)
}

// syncQuota runs a minimal upstream call and stores the observed health.
func (a *API) syncQuota(ctx context.Context, id string) error {
	account, ok := a.store.AccountByID(id)
	if !ok {
		return fmt.Errorf("账号不存在")
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.settings().RequestTimeout())
		defer cancel()
	}

	started := time.Now()
	result, err := a.client.Completion(ctx, dola.Options{
		Cookie:    account.Cookie,
		Text:      "ping",
		DeepThink: 0,
		Timeout:   a.settings().RequestTimeout(),
	})
	latency := time.Since(started).Milliseconds()

	note := "上游连通，未返回积分信息"
	available := err == nil
	if err != nil {
		note = truncate(err.Error(), 200)
	} else if result.Text != "" {
		note = "上游连通"
	}

	a.store.SaveAccountState(id, func(target *store.Account) {
		target.Quota = &store.Quota{
			SyncedAt: time.Now(), Available: available, LatencyMs: latency,
			Plan: quotaPlan(target), Note: note,
		}
		if err != nil {
			if err == dola.ErrInvalidCredential {
				target.Status = store.StatusInvalid
			} else if target.Status != store.StatusDisabled {
				target.Status = store.StatusCooldown
				target.CooldownUntil = time.Now().Add(a.settings().CooldownBase())
			}
			target.LastError = truncate(note, 200)
			return
		}
		target.Status = store.StatusActive
		target.CooldownUntil = time.Time{}
		target.LastError = ""
	})
	return err
}

// probeTimeout bounds a connectivity probe so the console stays responsive.
func probeTimeout(settings config.Settings) time.Duration {
	timeout := settings.RequestTimeout()
	if timeout > 90*time.Second {
		timeout = 90 * time.Second
	}
	return timeout
}

func quotaPlan(account *store.Account) string {
	if account.Quota != nil && account.Quota.Plan != "" {
		return account.Quota.Plan
	}
	return "Dola Web"
}

func (a *API) view(id string) *store.AccountView {
	inflight := a.pool.Snapshot()
	account, ok := a.store.AccountByID(id)
	if !ok {
		return nil
	}
	view := store.NewAccountView(account, inflight[id])
	return &view
}

func (a *API) findByCookie(cookie string) *store.Account {
	for _, account := range a.store.ListAccounts() {
		if account.Cookie == cookie {
			return account
		}
	}
	return nil
}

// -------------------------------------------------------------- client keys

func (a *API) listClientKeys(w http.ResponseWriter, r *http.Request) {
	keys := a.store.ListClientKeys()
	items := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		items = append(items, map[string]any{
			"id": key.ID, "name": key.Name, "key": key.Key, "maskedKey": maskKey(key.Key),
			"enabled": key.Enabled, "rpmLimit": key.RPMLimit, "maxConcurrent": key.MaxConcurrent,
			"totalRequests": key.TotalRequests, "createdAt": key.CreatedAt, "lastUsedAt": key.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func maskKey(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:11] + "…" + key[len(key)-4:]
}

func (a *API) createClientKey(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name          string `json:"name"`
		RPMLimit      int    `json:"rpmLimit"`
		MaxConcurrent int    `json:"maxConcurrent"`
	}
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if strings.TrimSpace(payload.Name) == "" {
		writeError(w, http.StatusBadRequest, "密钥名称不能为空")
		return
	}
	key, err := a.store.CreateClientKey(strings.TrimSpace(payload.Name), maxInt(0, payload.RPMLimit), clampInt(payload.MaxConcurrent, 1, 256))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": map[string]any{
		"id": key.ID, "name": key.Name, "key": key.Key, "maskedKey": maskKey(key.Key),
		"enabled": key.Enabled, "rpmLimit": key.RPMLimit, "maxConcurrent": key.MaxConcurrent,
		"totalRequests": key.TotalRequests, "createdAt": key.CreatedAt, "lastUsedAt": key.LastUsedAt,
	}})
}

func (a *API) updateClientKey(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Name          string `json:"name"`
		Enabled       *bool  `json:"enabled"`
		RPMLimit      *int   `json:"rpmLimit"`
		MaxConcurrent *int   `json:"maxConcurrent"`
	}
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	key, err := a.store.UpdateClientKey(r.PathValue("id"), func(key *store.ClientKey) {
		if strings.TrimSpace(payload.Name) != "" {
			key.Name = strings.TrimSpace(payload.Name)
		}
		if payload.Enabled != nil {
			key.Enabled = *payload.Enabled
		}
		if payload.RPMLimit != nil {
			key.RPMLimit = maxInt(0, *payload.RPMLimit)
		}
		if payload.MaxConcurrent != nil {
			key.MaxConcurrent = clampInt(*payload.MaxConcurrent, 1, 256)
		}
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "密钥不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": map[string]any{
		"id": key.ID, "name": key.Name, "key": key.Key, "maskedKey": maskKey(key.Key),
		"enabled": key.Enabled, "rpmLimit": key.RPMLimit, "maxConcurrent": key.MaxConcurrent,
		"totalRequests": key.TotalRequests, "createdAt": key.CreatedAt, "lastUsedAt": key.LastUsedAt,
	}})
}

func (a *API) deleteClientKey(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteClientKey(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "密钥不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ------------------------------------------------------------------- models

func (a *API) listModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": a.store.ListModels()})
}

func (a *API) updateModel(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Enabled     *bool  `json:"enabled"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	model, err := a.store.UpdateModel(r.PathValue("id"), func(model *store.ModelConfig) {
		if payload.Enabled != nil {
			model.Enabled = *payload.Enabled
		}
		if strings.TrimSpace(payload.Name) != "" {
			model.Name = strings.TrimSpace(payload.Name)
		}
		if strings.TrimSpace(payload.Description) != "" {
			model.Description = strings.TrimSpace(payload.Description)
		}
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "模型不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": model})
}

// ------------------------------------------------------------------- audits

func (a *API) listAudits(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	// Same overflow hazard as listAccounts: bound the page, then guard the
	// offset below.
	page := clampInt(config.ParseIntOrDefault(query.Get("page"), 1), 1, 1_000_000)
	pageSize := clampInt(config.ParseIntOrDefault(query.Get("pageSize"), 20), 1, 500)
	search := strings.ToLower(strings.TrimSpace(query.Get("search")))
	status := query.Get("status")

	audits := a.store.ListAudits()
	filtered := make([]*store.Audit, 0, len(audits))
	for _, audit := range audits {
		if status == "success" && audit.Status >= 400 {
			continue
		}
		if status == "failed" && audit.Status < 400 {
			continue
		}
		if search != "" {
			haystack := strings.ToLower(audit.ID + " " + audit.Model + " " + audit.KeyName + " " + audit.AccountName)
			if !strings.Contains(haystack, search) {
				continue
			}
		}
		filtered = append(filtered, audit)
	}

	total := len(filtered)
	start := (page - 1) * pageSize
	if start < 0 || start > total {
		start = total
	}
	end := minInt(start+pageSize, total)

	writeJSON(w, http.StatusOK, map[string]any{
		"items": filtered[start:end], "total": total, "page": page, "pageSize": pageSize,
	})
}

func (a *API) auditDetail(w http.ResponseWriter, r *http.Request) {
	audit, ok := a.store.AuditByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "记录不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": audit})
}

func (a *API) clearAudits(w http.ResponseWriter, r *http.Request) {
	if err := a.store.ClearAudits(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ----------------------------------------------------------------- settings

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	settings := a.settings()
	writeJSON(w, http.StatusOK, map[string]any{
		"server": map[string]any{
			"addr": settings.Server.Addr, "maxConcurrentRequests": settings.Server.MaxConcurrentRequests,
			"adminUsername": a.store.AdminProfile().Username,
		},
		"upstream": map[string]any{
			"baseURL": settings.Upstream.BaseURL, "botID": settings.Upstream.BotID,
			"region": settings.Upstream.Region, "language": settings.Upstream.Language,
			"requestTimeoutSec":    settings.Upstream.RequestTimeoutSec,
			"streamIdleTimeoutSec": settings.Upstream.StreamIdleTimeoutSec,
			"proxy":                settings.Upstream.Proxy, "userAgent": settings.Upstream.UserAgent,
		},
		"routing": settings.Routing,
		"audit":   settings.Audit,
		"media":   settings.Media,
		"about": map[string]any{
			"version": gateway.Version, "buildTime": a.started.Format(time.RFC3339),
			"dataDir": a.store.DataDir(), "upstreamURL": "https://www.dola.com/",
		},
	})
}

func (a *API) saveSettings(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Server        *config.ServerSettings   `json:"server"`
		Upstream      *config.UpstreamSettings `json:"upstream"`
		Routing       *config.RoutingSettings  `json:"routing"`
		Audit         *config.AuditSettings    `json:"audit"`
		Media         *config.MediaSettings    `json:"media"`
		AdminPassword string                   `json:"adminPassword"`
	}
	if err := decode(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式错误")
		return
	}

	if err := a.store.UpdateSettings(func(settings *config.Settings) {
		if payload.Server != nil {
			settings.Server.MaxConcurrentRequests = payload.Server.MaxConcurrentRequests
			if strings.TrimSpace(payload.Server.AdminUsername) != "" {
				settings.Server.AdminUsername = strings.TrimSpace(payload.Server.AdminUsername)
			}
		}
		if payload.Upstream != nil {
			settings.Upstream = *payload.Upstream
		}
		if payload.Routing != nil {
			settings.Routing = *payload.Routing
		}
		if payload.Audit != nil {
			settings.Audit = *payload.Audit
		}
		if payload.Media != nil {
			settings.Media = *payload.Media
		}
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if payload.AdminPassword != "" {
		admin := a.store.AdminProfile()
		username := admin.Username
		if payload.Server != nil && strings.TrimSpace(payload.Server.AdminUsername) != "" {
			username = strings.TrimSpace(payload.Server.AdminUsername)
		}
		if err := a.store.SetPassword(username, payload.AdminPassword); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Rotating the credential must invalidate live sessions, exactly as the
		// dedicated password endpoint does - otherwise a leaked token keeps
		// working after the password it belongs to is gone.
		a.store.RevokeAllSessions()
	}

	a.getSettings(w, r)
}

// ------------------------------------------------------------------ gallery

func (a *API) gallery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": a.store.ListMedia()})
}

// ------------------------------------------------------------------ helpers

func decode(r *http.Request, target any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, target)
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
	writeJSON(w, status, map[string]any{"error": message})
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

// clampPositive clamps into range but passes a non-positive value through
// unchanged, so "not provided" stays distinguishable from "set to the minimum"
// and the caller can still apply its own default.
func clampPositive(value, low, high int) int {
	if value <= 0 {
		return 0
	}
	return clampInt(value, low, high)
}

// optionalString dereferences an optional JSON string field, trimming it.
func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	// Cut on a rune boundary: slicing at a fixed byte offset splits multi-byte
	// characters and pushes invalid UTF-8 into the response.
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}

package store

import (
	"time"

	"dola2api/internal/config"
)

// Account status values.
const (
	StatusActive   = "active"
	StatusCooldown = "cooldown"
	StatusDisabled = "disabled"
	StatusInvalid  = "invalid"
)

// Account kinds.
const (
	KindCookie = "cookie"
	KindGuest  = "guest"
)

type Quota struct {
	SyncedAt         time.Time `json:"syncedAt"`
	Available        bool      `json:"available"`
	LatencyMs        int64     `json:"latencyMs"`
	Plan             string    `json:"plan"`
	CreditsRemaining int       `json:"creditsRemaining"`
	CreditsTotal     int       `json:"creditsTotal"`
	Note             string    `json:"note"`
}

type Account struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Cookie        string    `json:"cookie"`
	Group         string    `json:"group"`
	Remark        string    `json:"remark"`
	Enabled       bool      `json:"enabled"`
	Priority      int       `json:"priority"`
	MaxConcurrent int       `json:"maxConcurrent"`
	Status        string    `json:"status"`
	CooldownUntil time.Time `json:"cooldownUntil"`
	FailCount     int       `json:"failCount"`
	SuccessCount  int       `json:"successCount"`
	LastUsedAt    time.Time `json:"lastUsedAt"`
	LastError     string    `json:"lastError"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Quota         *Quota    `json:"quota,omitempty"`
}

// AccountView is the API representation. It is an explicit projection rather
// than an embedded struct so the raw cookie can never be serialised by accident.
type AccountView struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Kind          string    `json:"kind"`
	Group         string    `json:"group"`
	Remark        string    `json:"remark"`
	Enabled       bool      `json:"enabled"`
	Priority      int       `json:"priority"`
	MaxConcurrent int       `json:"maxConcurrent"`
	Status        string    `json:"status"`
	CooldownUntil time.Time `json:"cooldownUntil"`
	FailCount     int       `json:"failCount"`
	SuccessCount  int       `json:"successCount"`
	LastUsedAt    time.Time `json:"lastUsedAt"`
	LastError     string    `json:"lastError"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
	// No omitempty: the console declares quota as a required field that may be
	// null, so the key must always be present. Omitting it would make the
	// property undefined instead of null and silently break strict checks.
	Quota        *Quota `json:"quota"`
	CookieMasked string `json:"cookieMasked"`
	Inflight     int    `json:"inflight"`
}

// NewAccountView projects an account for API responses.
func NewAccountView(account *Account, inflight int) AccountView {
	return AccountView{
		ID: account.ID, Name: account.Name, Kind: account.Kind, Group: account.Group,
		Remark: account.Remark, Enabled: account.Enabled, Priority: account.Priority,
		MaxConcurrent: account.MaxConcurrent, Status: account.Status,
		CooldownUntil: account.CooldownUntil, FailCount: account.FailCount,
		SuccessCount: account.SuccessCount, LastUsedAt: account.LastUsedAt,
		LastError: account.LastError, CreatedAt: account.CreatedAt, UpdatedAt: account.UpdatedAt,
		Quota: account.Quota, CookieMasked: MaskCookie(account.Cookie), Inflight: inflight,
	}
}

type ClientKey struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Key           string    `json:"key"`
	Enabled       bool      `json:"enabled"`
	RPMLimit      int       `json:"rpmLimit"`
	MaxConcurrent int       `json:"maxConcurrent"`
	TotalRequests int64     `json:"totalRequests"`
	CreatedAt     time.Time `json:"createdAt"`
	LastUsedAt    time.Time `json:"lastUsedAt"`
}

type Audit struct {
	ID               string    `json:"id"`
	CreatedAt        time.Time `json:"createdAt"`
	KeyName          string    `json:"keyName"`
	Model            string    `json:"model"`
	AccountName      string    `json:"accountName"`
	Status           int       `json:"status"`
	LatencyMs        int64     `json:"latencyMs"`
	FirstTokenMs     int64     `json:"firstTokenMs"`
	PromptTokens     int       `json:"promptTokens"`
	CompletionTokens int       `json:"completionTokens"`
	Stream           bool      `json:"stream"`
	Retries          int       `json:"retries"`
	IP               string    `json:"ip"`
	UserAgent        string    `json:"userAgent"`
	Error            string    `json:"error"`
	RequestBody      string    `json:"requestBody"`
	ResponseBody     string    `json:"responseBody"`
}

type MediaItem struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	URL         string    `json:"url"`
	SourceURL   string    `json:"sourceUrl"`
	Prompt      string    `json:"prompt"`
	Model       string    `json:"model"`
	AccountName string    `json:"accountName"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Admin struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
	Salt         string `json:"salt"`
}

type ModelConfig struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Upstream    string `json:"upstream"`
	Type        string `json:"type"`
	Enabled     bool   `json:"enabled"`
	Builtin     bool   `json:"builtin"`
	Description string `json:"description"`
	Requests    int64  `json:"requests"`
	Tokens      int64  `json:"tokens"`
}

type State struct {
	Version    int             `json:"version"`
	Admin      Admin           `json:"admin"`
	Settings   config.Settings `json:"settings"`
	Accounts   []*Account      `json:"accounts"`
	ClientKeys []*ClientKey    `json:"clientKeys"`
	Audits     []*Audit        `json:"audits"`
	Media      []*MediaItem    `json:"media"`
	Models     []*ModelConfig  `json:"models"`
}

// BuiltinModels is the model catalogue exposed through /v1/models.
func BuiltinModels() []*ModelConfig {
	return []*ModelConfig{
		{
			ID: "dola-fast", Name: "Dola Fast", Upstream: "need_deep_think=0", Type: "chat",
			Enabled: true, Builtin: true, Description: "日常对话，响应更快",
		},
		{
			ID: "dola-pro", Name: "Dola Pro", Upstream: "need_deep_think=3", Type: "chat",
			Enabled: true, Builtin: true, Description: "深度思考模型，附带推理内容",
		},
		{
			ID: "dola-image", Name: "Dola Image", Upstream: "chat_ability.ability_type=3", Type: "image",
			Enabled: true, Builtin: true, Description: "免费图像生成，单次返回 4 张",
		},
		{
			ID: "dreamina-seedance-1.0", Name: "Dreamina Seedance 1.0", Upstream: "chat_ability.ability_type=17 (ic_mini)", Type: "video",
			Enabled: true, Builtin: true, Description: "视频生成，异步完成并消耗积分",
		},
	}
}

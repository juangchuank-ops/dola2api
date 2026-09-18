package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"dola2api/internal/config"
	"dola2api/internal/dola"
	"dola2api/internal/pool"
	"dola2api/internal/store"
)

// The admin package is the largest surface in the project and the only one that
// can mutate persistent state, so these tests drive the real mux - routing,
// middleware and all - rather than calling handlers directly.

type harness struct {
	api      *API
	store    *store.Store
	pool     *pool.Pool
	mux      *http.ServeMux
	token    string
	upstream *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	// Stub upstream. Every probe and quota sync fails fast here instead of
	// reaching the real service.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"error":"stub"}`)
	}))
	t.Cleanup(upstream.Close)

	dir := t.TempDir()
	st, err := store.Open(dir, "admin", "admin12345")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.UpdateSettings(func(settings *config.Settings) {
		settings.Upstream.BaseURL = upstream.URL
		settings.Upstream.RequestTimeoutSec = 2
		settings.Upstream.StreamIdleTimeoutSec = 2
		settings.Routing.CapacityWaitSec = 0
		settings.Media.AutoDownload = false
	}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	// Mirror main.go: the runtime settings always come from the store, so a
	// settings write is visible to every component immediately.
	settingsFn := func() config.Settings { return st.Settings() }
	client := dola.New(settingsFn)
	p := pool.New(st, settingsFn)
	api := New(st, p, client, settingsFn)
	mux := http.NewServeMux()
	api.Register(mux)

	h := &harness{api: api, store: st, pool: p, mux: mux, upstream: upstream}
	h.token = h.login(t, "admin", "admin12345")
	return h
}

func (h *harness) login(t *testing.T, username, password string) string {
	t.Helper()
	rec := h.do(t, "POST", "/admin/api/auth/login", map[string]any{
		"username": username, "password": password,
	}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login(%s) = %d %s", username, rec.Code, rec.Body.String())
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if payload.Token == "" {
		t.Fatal("login returned an empty token")
	}
	return payload.Token
}

func (h *harness) do(t *testing.T, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != nil {
		request.Header.Set("content-type", "application/json")
	}
	if token != "" {
		request.Header.Set("authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, request)
	return rec
}

func (h *harness) authed(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	return h.do(t, method, path, body, h.token)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return payload
}

// items pulls the "items" array out of a list response.
func items(t *testing.T, rec *httptest.ResponseRecorder) []any {
	t.Helper()
	raw, ok := decodeBody(t, rec)["items"].([]any)
	if !ok {
		t.Fatalf("response has no items array: %s", rec.Body.String())
	}
	return raw
}

// number reads a JSON number as float64.
func number(t *testing.T, payload map[string]any, key string) float64 {
	t.Helper()
	value, ok := payload[key].(float64)
	if !ok {
		t.Fatalf("field %q is not a number in %v", key, payload)
	}
	return value
}

func (h *harness) addAccount(t *testing.T, id, name, cookie string) *store.Account {
	t.Helper()
	account := &store.Account{
		ID: id, Name: name, Kind: store.KindCookie, Cookie: cookie,
		Enabled: true, Priority: 50, MaxConcurrent: 2, Status: store.StatusActive,
	}
	if err := h.store.AddAccount(account); err != nil {
		t.Fatalf("add account %s: %v", id, err)
	}
	return account
}

// --- authentication --------------------------------------------------------

func TestGuardRejectsMissingAndInvalidToken(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct{ name, token string }{
		{"no token", ""},
		{"bogus token", "not-a-real-session"},
	} {
		rec := h.do(t, "GET", "/admin/api/accounts", nil, tc.token)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", tc.name, rec.Code)
		}
	}

	if rec := h.authed(t, "GET", "/admin/api/accounts", nil); rec.Code != http.StatusOK {
		t.Fatalf("valid token rejected: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	h := newHarness(t)

	rec := h.do(t, "POST", "/admin/api/auth/login", map[string]any{
		"username": "admin", "password": "definitely-wrong",
	}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	// An unknown user must be rejected the same way, without revealing whether
	// the username exists.
	rec = h.do(t, "POST", "/admin/api/auth/login", map[string]any{
		"username": "nobody", "password": "admin12345",
	}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown user status = %d, want 401", rec.Code)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	h := newHarness(t)

	if rec := h.authed(t, "POST", "/admin/api/auth/logout", nil); rec.Code != http.StatusOK {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := h.authed(t, "GET", "/admin/api/accounts", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token still valid after logout: %d", rec.Code)
	}
}

func TestChangePasswordRotatesCredentialAndSessions(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/auth/password", map[string]any{
		"currentPassword": "admin12345",
		"newPassword":     "brand-new-secret",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("change password = %d %s", rec.Code, rec.Body.String())
	}

	// Every existing session must be invalidated, otherwise a leaked token
	// survives the rotation.
	if rec := h.authed(t, "GET", "/admin/api/accounts", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old session still valid after password change: %d", rec.Code)
	}
	// And the new password must actually work.
	h.login(t, "admin", "brand-new-secret")
}

func TestChangePasswordRejectsWrongCurrentPassword(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/auth/password", map[string]any{
		"currentPassword": "not-the-password",
		"newPassword":     "irrelevant-here",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	// The old credential must still work.
	h.login(t, "admin", "admin12345")
}

// --- account creation ------------------------------------------------------

func TestCreateAccountValidatesCookie(t *testing.T) {
	h := newHarness(t)

	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"missing cookie", map[string]any{"name": "a"}},
		{"blank cookie", map[string]any{"name": "b", "cookie": "   "}},
		{"cookie without equals", map[string]any{"name": "c", "cookie": "justtext"}},
	} {
		rec := h.authed(t, "POST", "/admin/api/accounts", tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, rec.Code)
		}
	}
}

func TestCreateAccountRejectsDuplicateCookie(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "existing", "oauth_token=dup")

	rec := h.authed(t, "POST", "/admin/api/accounts", map[string]any{
		"name": "clone", "cookie": "oauth_token=dup",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

// An omitted priority must fall back to the store default (50). Clamping the
// zero value into the 1..100 range instead makes a brand new account outrank
// every existing one, which is not what "unset" can reasonably mean.
func TestCreateAccountUsesDefaultPriorityWhenOmitted(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/accounts", map[string]any{
		"name": "no-priority", "cookie": "oauth_token=nopriority",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	account, _ := decodeBody(t, rec)["account"].(map[string]any)
	if account == nil {
		t.Fatal("response has no account")
	}
	if priority := number(t, account, "priority"); priority != 50 {
		t.Errorf("priority = %v, want 50 (store default)", priority)
	}
	if maxConcurrent := number(t, account, "maxConcurrent"); maxConcurrent != 2 {
		t.Errorf("maxConcurrent = %v, want 2 (store default)", maxConcurrent)
	}
}

func TestCreateAccountClampsExplicitValues(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/accounts", map[string]any{
		"name": "extreme", "cookie": "oauth_token=extreme",
		"priority": 9999, "maxConcurrent": 9999,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	account, _ := decodeBody(t, rec)["account"].(map[string]any)
	if priority := number(t, account, "priority"); priority != 100 {
		t.Errorf("priority = %v, want clamped to 100", priority)
	}
	if maxConcurrent := number(t, account, "maxConcurrent"); maxConcurrent != 256 {
		t.Errorf("maxConcurrent = %v, want clamped to 256", maxConcurrent)
	}
}

// --- account updates -------------------------------------------------------

// PATCH is a partial update: fields absent from the body must be left alone.
// Group and remark are currently overwritten with their zero value, so editing
// just the enabled flag silently wipes an account's grouping.
func TestUpdateAccountLeavesAbsentFieldsAlone(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "primary", "oauth_token=x")
	if _, err := h.store.UpdateAccount("acc_1", func(account *store.Account) {
		account.Group = "team-a"
		account.Remark = "keep me"
	}); err != nil {
		t.Fatalf("seed group/remark: %v", err)
	}

	rec := h.authed(t, "PATCH", "/admin/api/accounts/acc_1", map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}

	account, ok := h.store.AccountByID("acc_1")
	if !ok {
		t.Fatal("account disappeared")
	}
	if account.Group != "team-a" {
		t.Errorf("group = %q, want %q (absent field was wiped)", account.Group, "team-a")
	}
	if account.Remark != "keep me" {
		t.Errorf("remark = %q, want %q (absent field was wiped)", account.Remark, "keep me")
	}
	if account.Enabled {
		t.Error("enabled was not applied")
	}
	if account.Name != "primary" {
		t.Errorf("name = %q, want primary", account.Name)
	}
}

func TestUpdateAccountAppliesPresentFields(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "primary", "oauth_token=x")

	rec := h.authed(t, "PATCH", "/admin/api/accounts/acc_1", map[string]any{
		"name": "renamed", "group": "team-b", "remark": "updated",
		"priority": 7, "maxConcurrent": 9,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	account, _ := h.store.AccountByID("acc_1")
	if account.Name != "renamed" || account.Group != "team-b" || account.Remark != "updated" {
		t.Fatalf("update not applied: %+v", account)
	}
	if account.Priority != 7 || account.MaxConcurrent != 9 {
		t.Fatalf("priority/maxConcurrent = %d/%d, want 7/9", account.Priority, account.MaxConcurrent)
	}
}

func TestUpdateAccountUnknownID(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "PATCH", "/admin/api/accounts/acc_missing", map[string]any{"name": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestDeleteAccountUnknownID(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "DELETE", "/admin/api/accounts/acc_missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// --- listing ---------------------------------------------------------------

func TestListAccountsPagination(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ {
		h.addAccount(t, fmt.Sprintf("acc_%d", i), fmt.Sprintf("acct-%d", i), fmt.Sprintf("oauth_token=%d", i))
	}

	rec := h.authed(t, "GET", "/admin/api/accounts?page=2&pageSize=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := decodeBody(t, rec)
	if total := number(t, body, "total"); total != 5 {
		t.Errorf("total = %v, want 5", total)
	}
	if got := len(items(t, rec)); got != 2 {
		t.Errorf("page 2 size = %d, want 2", got)
	}

	// The last page is partial, not padded and not an error.
	rec = h.authed(t, "GET", "/admin/api/accounts?page=3&pageSize=2", nil)
	if got := len(items(t, rec)); got != 1 {
		t.Errorf("last page size = %d, want 1", got)
	}

	// Past the end yields an empty page.
	rec = h.authed(t, "GET", "/admin/api/accounts?page=99&pageSize=2", nil)
	if got := len(items(t, rec)); got != 0 {
		t.Errorf("out-of-range page size = %d, want 0", got)
	}
}

// A page number big enough to overflow (page-1)*pageSize produces a negative
// slice offset. Without a guard the handler panics, which on a real server
// means a dropped connection and a stack trace in the log.
func TestListAccountsHugePageDoesNotPanic(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "primary", "oauth_token=x")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("listAccounts panicked on an oversized page number: %v", r)
		}
	}()

	for _, page := range []string{"9223372036854775807", "9223372036854775806", "999999999999999"} {
		rec := h.authed(t, "GET", "/admin/api/accounts?page="+page, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("page=%s: status = %d, want 200", page, rec.Code)
		}
		if got := len(items(t, rec)); got != 0 {
			t.Fatalf("page=%s: items = %d, want 0", page, got)
		}
	}
}

func TestListAccountsClampsPageSize(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "primary", "oauth_token=x")

	rec := h.authed(t, "GET", "/admin/api/accounts?pageSize=99999", nil)
	body := decodeBody(t, rec)
	if pageSize := number(t, body, "pageSize"); pageSize != 500 {
		t.Errorf("pageSize = %v, want clamped to 500", pageSize)
	}

	rec = h.authed(t, "GET", "/admin/api/accounts?pageSize=0", nil)
	body = decodeBody(t, rec)
	if pageSize := number(t, body, "pageSize"); pageSize != 1 {
		t.Errorf("pageSize = %v, want clamped to 1", pageSize)
	}
}

func TestListAccountsFilters(t *testing.T) {
	h := newHarness(t)
	first := h.addAccount(t, "acc_1", "primary", "oauth_token=1")
	h.addAccount(t, "acc_2", "backup", "oauth_token=2")
	if _, err := h.store.UpdateAccount("acc_1", func(account *store.Account) {
		account.Group = "team-a"
	}); err != nil {
		t.Fatalf("set group: %v", err)
	}
	if _, err := h.store.UpdateAccount("acc_2", func(account *store.Account) {
		account.Enabled = false
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	_ = first

	rec := h.authed(t, "GET", "/admin/api/accounts?group=team-a", nil)
	if got := len(items(t, rec)); got != 1 {
		t.Errorf("group filter returned %d, want 1", got)
	}

	rec = h.authed(t, "GET", "/admin/api/accounts?status=disabled", nil)
	if got := len(items(t, rec)); got != 1 {
		t.Errorf("status filter returned %d, want 1", got)
	}

	rec = h.authed(t, "GET", "/admin/api/accounts?search=backup", nil)
	if got := len(items(t, rec)); got != 1 {
		t.Errorf("name search returned %d, want 1", got)
	}
}

// The console promises cookies never leave the server: AccountView carries only
// cookieMasked. Letting the search box match the raw cookie hands that promise
// back - an operator can binary-search a secret one character at a time.
func TestListAccountsSearchDoesNotMatchRawCookie(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "primary", "oauth_token=SUPERSECRETVALUE; other=1")

	rec := h.authed(t, "GET", "/admin/api/accounts?search=supersecretvalue", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := len(items(t, rec)); got != 0 {
		t.Fatalf("search matched a raw cookie value (%d hit(s)); the raw cookie must not be searchable", got)
	}
}

func TestListAccountsNeverSerialisesRawCookie(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "primary", "oauth_token=SUPERSECRETVALUE; other=1")

	rec := h.authed(t, "GET", "/admin/api/accounts", nil)
	if strings.Contains(rec.Body.String(), "SUPERSECRETVALUE") {
		t.Fatalf("list response leaked the raw cookie: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cookieMasked") {
		t.Fatal("list response has no cookieMasked field")
	}
}

func TestListAccountsSortsByField(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "charlie", "oauth_token=1")
	h.addAccount(t, "acc_2", "alpha", "oauth_token=2")
	h.addAccount(t, "acc_3", "bravo", "oauth_token=3")

	rec := h.authed(t, "GET", "/admin/api/accounts?sortBy=name&sortOrder=asc", nil)
	names := make([]string, 0, 3)
	for _, item := range items(t, rec) {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "alpha,bravo,charlie" {
		t.Fatalf("ascending name order = %v", names)
	}

	rec = h.authed(t, "GET", "/admin/api/accounts?sortBy=name&sortOrder=desc", nil)
	names = names[:0]
	for _, item := range items(t, rec) {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	if strings.Join(names, ",") != "charlie,bravo,alpha" {
		t.Fatalf("descending name order = %v", names)
	}
}

// --- batch -----------------------------------------------------------------

func TestBatchAccountsRequiresIDs(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/accounts/batch", map[string]any{"action": "disable"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestBatchAccountsRejectsUnknownAction(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "primary", "oauth_token=1")

	rec := h.authed(t, "POST", "/admin/api/accounts/batch", map[string]any{
		"action": "explode", "ids": []string{"acc_1"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestBatchAccountsEnableDisable(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "a", "oauth_token=1")
	h.addAccount(t, "acc_2", "b", "oauth_token=2")

	rec := h.authed(t, "POST", "/admin/api/accounts/batch", map[string]any{
		"action": "disable", "ids": []string{"acc_1", "acc_2"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", rec.Code, rec.Body.String())
	}
	if updated := number(t, decodeBody(t, rec), "updated"); updated != 2 {
		t.Fatalf("updated = %v, want 2", updated)
	}
	for _, id := range []string{"acc_1", "acc_2"} {
		account, _ := h.store.AccountByID(id)
		if account.Enabled {
			t.Errorf("%s still enabled", id)
		}
		if account.Status != store.StatusDisabled {
			t.Errorf("%s status = %q, want disabled", id, account.Status)
		}
	}

	rec = h.authed(t, "POST", "/admin/api/accounts/batch", map[string]any{
		"action": "enable", "ids": []string{"acc_1", "acc_2"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("enable = %d", rec.Code)
	}
	for _, id := range []string{"acc_1", "acc_2"} {
		account, _ := h.store.AccountByID(id)
		if !account.Enabled || account.Status != store.StatusActive {
			t.Errorf("%s = enabled %v status %q, want enabled/active", id, account.Enabled, account.Status)
		}
	}
}

func TestBatchAccountsClearCooldown(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "a", "oauth_token=1")
	h.store.SaveAccountState("acc_1", func(account *store.Account) {
		account.Status = store.StatusCooldown
		account.CooldownUntil = time.Now().Add(time.Hour)
		account.FailCount = 5
		account.LastError = "boom"
	})

	rec := h.authed(t, "POST", "/admin/api/accounts/batch", map[string]any{
		"action": "clearCooldown", "ids": []string{"acc_1"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	account, _ := h.store.AccountByID("acc_1")
	if account.Status != store.StatusActive || account.FailCount != 0 || account.LastError != "" {
		t.Fatalf("cooldown not cleared: %+v", account)
	}
}

func TestBatchAccountsConcurrency(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "a", "oauth_token=1")

	rec := h.authed(t, "POST", "/admin/api/accounts/batch", map[string]any{
		"action": "concurrency", "ids": []string{"acc_1"}, "maxConcurrent": 12,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	account, _ := h.store.AccountByID("acc_1")
	if account.MaxConcurrent != 12 {
		t.Fatalf("maxConcurrent = %d, want 12", account.MaxConcurrent)
	}
}

func TestCleanupAccountsRequiresStatuses(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/accounts/cleanup", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCleanupAccountsDeletesByStatus(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "a", "oauth_token=1")
	h.addAccount(t, "acc_2", "b", "oauth_token=2")
	h.store.SaveAccountState("acc_2", func(account *store.Account) {
		account.Status = store.StatusInvalid
	})

	rec := h.authed(t, "POST", "/admin/api/accounts/cleanup", map[string]any{
		"statuses": []string{store.StatusInvalid},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if deleted := number(t, decodeBody(t, rec), "deleted"); deleted != 1 {
		t.Fatalf("deleted = %v, want 1", deleted)
	}
	if _, ok := h.store.AccountByID("acc_1"); !ok {
		t.Error("cleanup removed an account it should have kept")
	}
}

// --- import / export -------------------------------------------------------

func TestImportAccountsFromText(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/accounts/import", map[string]any{
		"cookies": "named----oauth_token=one\n# a comment\noauth_token=two\n\n",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if created := number(t, body, "created"); created != 2 {
		t.Fatalf("created = %v, want 2 (comment and blank lines skipped)", created)
	}

	named := h.findByCookieForTest(t, "oauth_token=one")
	if named == nil || named.Name != "named" {
		t.Fatalf("name----cookie form was not parsed: %+v", named)
	}
}

func TestImportAccountsFromJSON(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/accounts/import", map[string]any{
		"json": []map[string]any{
			{"name": "from-json", "cookie": "oauth_token=json1", "group": "team-x", "priority": 20},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if created := number(t, decodeBody(t, rec), "created"); created != 1 {
		t.Fatalf("created = %v, want 1", created)
	}

	account := h.findByCookieForTest(t, "oauth_token=json1")
	if account == nil {
		t.Fatal("json import did not create the account")
	}
	if account.Name != "from-json" || account.Group != "team-x" || account.Priority != 20 {
		t.Fatalf("json fields not applied: %+v", account)
	}
}

func TestImportUpdatesExistingAccountInsteadOfDuplicating(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "original", "oauth_token=dup")

	rec := h.authed(t, "POST", "/admin/api/accounts/import", map[string]any{
		"cookies": "renamed----oauth_token=dup\n",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := decodeBody(t, rec)
	if created := number(t, body, "created"); created != 0 {
		t.Errorf("created = %v, want 0", created)
	}
	if updated := number(t, body, "updated"); updated != 1 {
		t.Errorf("updated = %v, want 1", updated)
	}
	if len(h.store.ListAccounts()) != 1 {
		t.Errorf("import duplicated an existing cookie")
	}
}

func TestImportAccountsRejectsUnparsableInput(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/accounts/import", map[string]any{"cookies": "\n\n# only comments\n"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestExportAccountsReturnsRawCookiesForBackup(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "primary", "oauth_token=exportme")

	rec := h.authed(t, "GET", "/admin/api/accounts/export", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	// Export is the one deliberate exception to cookie masking: it exists so an
	// operator can back the pool up and re-import it.
	if !strings.Contains(rec.Body.String(), "exportme") {
		t.Fatal("export does not contain the raw cookie, so a backup could not be restored")
	}
}

// --- client keys -----------------------------------------------------------

func TestClientKeyLifecycle(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "POST", "/admin/api/client-keys", map[string]any{"name": "test-key"})
	if rec.Code != http.StatusOK {
		t.Fatalf("create = %d %s", rec.Code, rec.Body.String())
	}
	key, _ := decodeBody(t, rec)["key"].(map[string]any)
	if key == nil {
		t.Fatal("create returned no key")
	}
	id, _ := key["id"].(string)
	raw, _ := key["key"].(string)
	if raw == "" {
		t.Fatal("create returned an empty key value")
	}

	// The list exposes a masked form alongside the raw value.
	rec = h.authed(t, "GET", "/admin/api/client-keys", nil)
	if got := len(items(t, rec)); got != 1 {
		t.Fatalf("list = %d keys, want 1", got)
	}
	masked := items(t, rec)[0].(map[string]any)["maskedKey"].(string)
	if masked == raw {
		t.Error("maskedKey equals the raw key")
	}

	rec = h.authed(t, "PATCH", "/admin/api/client-keys/"+id, map[string]any{"enabled": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("update = %d", rec.Code)
	}
	if key, ok := h.clientKeyByID(id); !ok || key.Enabled {
		t.Fatalf("key still enabled after update")
	}

	rec = h.authed(t, "DELETE", "/admin/api/client-keys/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d", rec.Code)
	}
	if got := len(h.store.ListClientKeys()); got != 0 {
		t.Fatalf("keys remaining = %d, want 0", got)
	}
}

func TestMaskKeyKeepsEndpointsAndHidesMiddle(t *testing.T) {
	raw := "sk-dola-0123456789abcdef"
	masked := maskKey(raw)
	if masked == raw {
		t.Fatal("maskKey returned the key unchanged")
	}
	if !strings.HasPrefix(masked, raw[:11]) {
		t.Errorf("masked %q does not keep the prefix", masked)
	}
	if !strings.HasSuffix(masked, raw[len(raw)-4:]) {
		t.Errorf("masked %q does not keep the suffix", masked)
	}
	if strings.Contains(masked, raw[11:len(raw)-4]) {
		t.Errorf("masked %q still contains the secret middle", masked)
	}

	// Short values are returned as-is rather than sliced out of range.
	if got := maskKey("short"); got != "short" {
		t.Errorf("maskKey(short) = %q", got)
	}
}

// --- models ----------------------------------------------------------------

func TestUpdateModelTogglesEnabled(t *testing.T) {
	h := newHarness(t)

	models := h.store.ListModels()
	if len(models) == 0 {
		t.Fatal("no models in the default catalogue")
	}
	target := models[0]

	rec := h.authed(t, "PATCH", "/admin/api/models/"+target.ID, map[string]any{"enabled": !target.Enabled})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	updated, ok := h.store.ModelByID(target.ID)
	if !ok {
		t.Fatal("model disappeared")
	}
	if updated.Enabled == target.Enabled {
		t.Fatal("enabled flag was not toggled")
	}
}

// --- settings --------------------------------------------------------------

func TestGetSettingsShape(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "GET", "/admin/api/settings", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := decodeBody(t, rec)
	for _, section := range []string{"server", "upstream", "routing", "audit", "media"} {
		if _, ok := body[section].(map[string]any); !ok {
			t.Errorf("settings response has no %q section", section)
		}
	}
}

// PUT /settings takes one pointer per section, which reads as "only the
// sections you send are modified". Sending just routing must not blank out the
// upstream configuration - losing requestTimeoutSec that way would make every
// upstream call time out instantly.
func TestSaveSettingsLeavesUntouchedSectionsAlone(t *testing.T) {
	h := newHarness(t)
	before := h.store.Settings()

	rec := h.authed(t, "PUT", "/admin/api/settings", map[string]any{
		"routing": map[string]any{
			"strategy": "round_robin", "cooldownBaseSec": 30, "cooldownMaxSec": 300,
			"capacityWaitSec": 10, "stickyTTLSec": 60, "preferIdle": true,
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}

	after := h.store.Settings()
	if after.Routing.Strategy != "round_robin" {
		t.Errorf("routing.strategy = %q, want round_robin", after.Routing.Strategy)
	}
	if after.Upstream.BaseURL != before.Upstream.BaseURL {
		t.Errorf("upstream.baseURL = %q, want %q (absent section was wiped)",
			after.Upstream.BaseURL, before.Upstream.BaseURL)
	}
	if after.Upstream.RequestTimeoutSec != before.Upstream.RequestTimeoutSec {
		t.Errorf("upstream.requestTimeoutSec = %d, want %d (absent section was wiped)",
			after.Upstream.RequestTimeoutSec, before.Upstream.RequestTimeoutSec)
	}
	if after.Media.AutoDownload != before.Media.AutoDownload {
		t.Errorf("media.autoDownload = %v, want %v", after.Media.AutoDownload, before.Media.AutoDownload)
	}
}

func TestSaveSettingsPersistsUpstream(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "PUT", "/admin/api/settings", map[string]any{
		"upstream": map[string]any{
			"baseURL": "https://example.test", "botID": "123", "region": "us",
			"requestTimeoutSec": 45, "streamIdleTimeoutSec": 30,
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	if got := h.store.Settings().Upstream.BaseURL; got != "https://example.test" {
		t.Fatalf("baseURL = %q, want https://example.test", got)
	}
	if got := h.store.Settings().Upstream.RequestTimeoutSec; got != 45 {
		t.Fatalf("requestTimeoutSec = %d, want 45", got)
	}
}

func TestSaveSettingsRejectsShortPassword(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "PUT", "/admin/api/settings", map[string]any{"adminPassword": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// Rotating the admin password through settings must invalidate live sessions
// exactly like the dedicated endpoint does. Otherwise a leaked token keeps
// working after the credential it belongs to is gone.
func TestSaveSettingsPasswordRotatesSessions(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "PUT", "/admin/api/settings", map[string]any{"adminPassword": "rotated-secret"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}

	if rec := h.authed(t, "GET", "/admin/api/accounts", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session survived a password rotation: %d", rec.Code)
	}
	h.login(t, "admin", "rotated-secret")
}

// --- audits and dashboard --------------------------------------------------

func TestAuditDetailUnknownID(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "GET", "/admin/api/audits/audit_missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestAuditListAndClear(t *testing.T) {
	h := newHarness(t)
	h.store.AppendAudit(&store.Audit{ID: "audit_1", Model: "dola-fast", Status: 200})

	rec := h.authed(t, "GET", "/admin/api/audits", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	if got := len(items(t, rec)); got != 1 {
		t.Fatalf("audits = %d, want 1", got)
	}

	rec = h.authed(t, "DELETE", "/admin/api/audits", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear = %d", rec.Code)
	}
	if got := len(h.store.ListAudits()); got != 0 {
		t.Fatalf("audits remaining = %d, want 0", got)
	}
}

func TestDashboardReportsPoolCounts(t *testing.T) {
	h := newHarness(t)
	h.addAccount(t, "acc_1", "a", "oauth_token=1")
	h.addAccount(t, "acc_2", "b", "oauth_token=2")
	h.store.SaveAccountState("acc_2", func(account *store.Account) {
		account.Status = store.StatusInvalid
	})

	rec := h.authed(t, "GET", "/admin/api/dashboard?period=30d", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	resources, _ := body["resources"].(map[string]any)
	if resources == nil {
		t.Fatal("dashboard has no resources block")
	}
	if total := number(t, resources, "totalAccounts"); total != 2 {
		t.Errorf("totalAccounts = %v, want 2", total)
	}
	if routable := number(t, resources, "routableAccounts"); routable != 1 {
		t.Errorf("routableAccounts = %v, want 1", routable)
	}

	// /health and /dashboard must agree about the same number.
	upstream, _ := body["upstream"].(map[string]any)
	if upstream == nil {
		t.Fatal("dashboard has no upstream block")
	}
	if available := number(t, upstream, "poolAvailable"); available != 1 {
		t.Errorf("upstream.poolAvailable = %v, want 1 (must match routableAccounts)", available)
	}
}

func TestGalleryReturnsItemsArray(t *testing.T) {
	h := newHarness(t)

	rec := h.authed(t, "GET", "/admin/api/gallery", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if _, ok := decodeBody(t, rec)["items"].([]any); !ok {
		t.Fatalf("gallery has no items array: %s", rec.Body.String())
	}
}

// --- helpers ---------------------------------------------------------------

// truncate feeds error text into lastError and the audit log, so it must not
// split a multi-byte character in half.
func TestTruncateKeepsRunesIntact(t *testing.T) {
	cases := map[string]string{
		"mixed 3-byte": "x" + strings.Repeat("失败", 200),
		"pure 3-byte":  strings.Repeat("失败", 200),
		"mixed 4-byte": "x" + strings.Repeat("🐾", 100),
		"ascii":        strings.Repeat("a", 400),
	}
	for name, input := range cases {
		got := truncate(input, 240)
		if !utf8.ValidString(got) {
			t.Errorf("%s: invalid UTF-8 after truncation: %q", name, got)
		}
		if len(got) > 243 {
			t.Errorf("%s: length = %d, want <= 243", name, len(got))
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("%s: missing ellipsis: %q", name, got)
		}
	}

	if got := truncate("short", 240); got != "short" {
		t.Errorf("short input was modified: %q", got)
	}
}

func TestClampInt(t *testing.T) {
	for _, tc := range []struct{ value, low, high, want int }{
		{5, 1, 10, 5},
		{0, 1, 10, 1},
		{-3, 1, 10, 1},
		{99, 1, 10, 10},
	} {
		if got := clampInt(tc.value, tc.low, tc.high); got != tc.want {
			t.Errorf("clampInt(%d, %d, %d) = %d, want %d", tc.value, tc.low, tc.high, got, tc.want)
		}
	}
}

func TestParseImportHandlesEdgeCases(t *testing.T) {
	// A leading separator would produce an empty name, so the whole line is
	// treated as a cookie instead.
	entries := parseImport("----oauth_token=onlycookie", nil)
	if len(entries) != 1 || entries[0].Name != "" || entries[0].Cookie != "----oauth_token=onlycookie" {
		t.Fatalf("leading separator handled wrong: %+v", entries)
	}

	// JSON array form wins over the text form when both are present.
	entries = parseImport("oauth_token=text", json.RawMessage(`[{"cookie":"oauth_token=json"}]`))
	if len(entries) != 1 || entries[0].Cookie != "oauth_token=json" {
		t.Fatalf("json should take precedence: %+v", entries)
	}

	// Wrapped {"accounts": [...]} form.
	entries = parseImport("", json.RawMessage(`{"accounts":[{"cookie":"oauth_token=wrapped"}]}`))
	if len(entries) != 1 || entries[0].Cookie != "oauth_token=wrapped" {
		t.Fatalf("wrapped json form not parsed: %+v", entries)
	}

	// Blank and comment-only input yields nothing to import.
	if entries := parseImport("# comment\n\n   \n", nil); len(entries) != 0 {
		t.Fatalf("comment-only input produced %d entries", len(entries))
	}
}

// --- test helpers ----------------------------------------------------------

// findByCookieForTest reads through the store rather than the API so it does
// not depend on the endpoint under test.
func (h *harness) findByCookieForTest(t *testing.T, cookie string) *store.Account {
	t.Helper()
	for _, account := range h.store.ListAccounts() {
		if account.Cookie == cookie {
			return account
		}
	}
	return nil
}

func (h *harness) clientKeyByID(id string) (*store.ClientKey, bool) {
	for _, key := range h.store.ListClientKeys() {
		if key.ID == id {
			return key, true
		}
	}
	return nil, false
}

package store

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dola2api/internal/config"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrAlreadyExists = errors.New("already exists")
)

const (
	stateFile     = "app.json"
	pbkdfRounds   = 120000
	sessionTTL    = 12 * time.Hour
	schemaVersion = 1
)

// Store keeps the whole application state in memory and persists it through a
// single background writer. Mutations never touch the disk while holding the
// lock, so a slow or blocked filesystem cannot stall request handling.
//
// Settings is additionally mirrored into an atomic snapshot so callers can read
// it even while they hold mu. This matters because callbacks passed into
// mutation helpers (SaveAccountState, UpdateAccount, ...) run under the write
// lock and legitimately need settings; a plain RWMutex would deadlock there
// since Go's sync.RWMutex is not reentrant.
type Store struct {
	mu       sync.RWMutex
	path     string
	dataDir  string
	state    State
	sessions map[string]time.Time
	onChange []func()

	settings atomic.Value // config.Settings

	dirty  chan struct{}
	flush  chan chan error
	closed chan struct{}
	wg     sync.WaitGroup
}

// Open loads (or initialises) the persistent store inside dataDir.
func Open(dataDir, adminUser, adminPassword string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	store := &Store{
		path:     filepath.Join(dataDir, stateFile),
		dataDir:  dataDir,
		sessions: make(map[string]time.Time),
		dirty:    make(chan struct{}, 1),
		flush:    make(chan chan error),
		closed:   make(chan struct{}),
	}

	fresh := false
	raw, err := os.ReadFile(store.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &store.state); err != nil {
			return nil, fmt.Errorf("parse %s: %w", store.path, err)
		}
	case os.IsNotExist(err):
		fresh = true
	default:
		return nil, fmt.Errorf("read %s: %w", store.path, err)
	}

	if fresh {
		store.state.Settings = config.DefaultSettings(dataDir)
	}
	store.state.Version = schemaVersion
	store.state.Settings.Normalize(dataDir)
	store.settings.Store(store.state.Settings)
	if len(store.state.Models) == 0 {
		store.state.Models = BuiltinModels()
	}
	if store.state.Admin.Username == "" {
		if adminUser == "" {
			adminUser = "admin"
		}
		password := adminPassword
		generated := false
		if password == "" {
			password = randomToken(12)
			generated = true
		}
		salt := randomToken(16)
		store.state.Admin = Admin{Username: adminUser, Salt: salt, PasswordHash: hashPassword(password, salt)}
		store.state.Settings.Server.AdminUsername = adminUser
		if err := store.writeSnapshotLocked(); err != nil {
			return nil, err
		}
		if generated {
			fmt.Printf("\n  ⚠ 已生成初始管理员密码：%s\n  用户名：%s\n  请登录后立即在「设置」中修改。\n\n", password, adminUser)
		}
	}
	if err := os.MkdirAll(store.state.Settings.Media.GeneratedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create media dir: %w", err)
	}

	store.wg.Add(1)
	go store.loop()
	return store, nil
}

// loop coalesces mutation signals into disk writes.
func (s *Store) loop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.dirty:
			// Debounce so a burst of mutations results in a single write.
			time.Sleep(40 * time.Millisecond)
			select {
			case <-s.dirty:
			default:
			}
			if err := s.writeSnapshot(); err != nil {
				log.Printf("持久化失败: %v", err)
			}
		case reply := <-s.flush:
			reply <- s.writeSnapshot()
		case <-s.closed:
			return
		}
	}
}

// writeSnapshot marshals under a read lock and performs file I/O outside of it.
func (s *Store) writeSnapshot() error {
	s.mu.RLock()
	raw, err := json.MarshalIndent(s.state, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, raw)
}

// writeSnapshotLocked is only used during bootstrap, before the writer starts.
func (s *Store) writeSnapshotLocked() error {
	raw, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, raw)
}

func writeFileAtomic(path string, raw []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Store) DataDir() string { return s.dataDir }

// OnChange registers a callback fired after every successful mutation.
func (s *Store) OnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}

// unlockAndNotify releases mu and then runs the registered change callbacks.
// Callbacks are invoked only after the lock is dropped so they may safely
// re-enter the store: Go's sync.RWMutex is not reentrant, so a callback that
// reads settings while the write lock is still held would deadlock forever.
func (s *Store) unlockAndNotify() {
	callbacks := make([]func(), len(s.onChange))
	copy(callbacks, s.onChange)
	s.mu.Unlock()
	for _, fn := range callbacks {
		fn()
	}
}

// markDirtyLocked schedules a background write. It never blocks.
func (s *Store) markDirtyLocked() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

// Save flushes the current state to disk synchronously.
func (s *Store) Save() error {
	s.mu.Lock()
	callbacks := make([]func(), len(s.onChange))
	copy(callbacks, s.onChange)
	s.mu.Unlock()
	for _, fn := range callbacks {
		fn()
	}

	reply := make(chan error, 1)
	select {
	case s.flush <- reply:
		return <-reply
	case <-s.closed:
		return errors.New("store closed")
	}
}

// Close performs a final flush and stops the background writer.
func (s *Store) Close() error {
	reply := make(chan error, 1)
	select {
	case s.flush <- reply:
		<-reply
	default:
	}
	close(s.closed)
	s.wg.Wait()
	return nil
}

// ---------------------------------------------------------------- admin auth

func hashPassword(password, salt string) string {
	mac := hmac.New(sha256.New, []byte(salt))
	mac.Write([]byte(password))
	sum := mac.Sum(nil)
	for i := 1; i < pbkdfRounds; i++ {
		mac = hmac.New(sha256.New, sum)
		mac.Write([]byte(password + salt))
		sum = mac.Sum(nil)
	}
	return hex.EncodeToString(sum)
}

func randomToken(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func (s *Store) VerifyPassword(username, password string) bool {
	s.mu.RLock()
	admin := s.state.Admin
	s.mu.RUnlock()
	if admin.Username == "" || subtle.ConstantTimeCompare([]byte(admin.Username), []byte(username)) != 1 {
		return false
	}
	expected := hashPassword(password, admin.Salt)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(admin.PasswordHash)) == 1
}

func (s *Store) SetPassword(username, password string) error {
	if len(password) < 8 {
		return errors.New("密码至少 8 位")
	}
	s.mu.Lock()
	defer s.unlockAndNotify()
	salt := randomToken(16)
	s.state.Admin = Admin{Username: username, Salt: salt, PasswordHash: hashPassword(password, salt)}
	s.state.Settings.Server.AdminUsername = username
	s.markDirtyLocked()
	return nil
}

func (s *Store) AdminProfile() Admin {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Admin
}

// IssueSession creates an admin session token.
func (s *Store) IssueSession() (string, time.Time) {
	token := randomToken(24)
	expires := time.Now().Add(sessionTTL)
	s.mu.Lock()
	s.sessions[token] = expires
	s.mu.Unlock()
	return token, expires
}

func (s *Store) ValidateSession(token string) bool {
	if token == "" {
		return false
	}
	s.mu.RLock()
	expires, ok := s.sessions[token]
	s.mu.RUnlock()
	if !ok || time.Now().After(expires) {
		return false
	}
	return true
}

func (s *Store) RevokeSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *Store) RevokeAllSessions() {
	s.mu.Lock()
	s.sessions = make(map[string]time.Time)
	s.mu.Unlock()
}

// ------------------------------------------------------------------ settings

// Settings returns the current runtime configuration. It reads an immutable
// atomic snapshot rather than taking mu, so it is safe to call from inside
// callbacks that already run under the write lock.
func (s *Store) Settings() config.Settings {
	if value := s.settings.Load(); value != nil {
		return value.(config.Settings)
	}
	// Only reachable before Open finishes priming the snapshot.
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Settings
}

func (s *Store) UpdateSettings(apply func(*config.Settings)) error {
	s.mu.Lock()
	apply(&s.state.Settings)
	s.state.Settings.Normalize(s.dataDir)
	snapshot := s.state.Settings
	if err := os.MkdirAll(snapshot.Media.GeneratedDir, 0o755); err != nil {
		s.mu.Unlock()
		return err
	}
	s.markDirtyLocked()
	// Publish inside the lock so a callback reading settings sees the new value.
	s.settings.Store(snapshot)
	defer s.unlockAndNotify()
	return nil
}

// ------------------------------------------------------------------ accounts

func MaskCookie(cookie string) string {
	if cookie == "" {
		return ""
	}
	parts := strings.Split(cookie, ";")
	keys := make([]string, 0, 4)
	for _, part := range parts {
		name := strings.TrimSpace(strings.SplitN(part, "=", 2)[0])
		if name == "" {
			continue
		}
		keys = append(keys, name)
		if len(keys) == 4 {
			break
		}
	}
	joined := strings.Join(keys, "; ")
	if joined == "" {
		return "••••••"
	}
	if len(joined) > 64 {
		joined = joined[:64] + "…"
	}
	return joined
}

// clone returns a deep copy of an account.
//
// The store owns the live account objects and mutates them in place under s.mu.
// Handing those pointers to callers would let them read fields without holding
// the lock, which races with SaveAccountState and friends. Copying is cheap
// here because Account holds only value types apart from Quota.
func (a *Account) clone() *Account {
	if a == nil {
		return nil
	}
	out := *a
	if a.Quota != nil {
		quota := *a.Quota
		out.Quota = &quota
	}
	return &out
}

// clone returns a detached copy of a client key. ClientKey holds only value
// types, so a shallow copy is a deep copy.
func (k *ClientKey) clone() *ClientKey {
	if k == nil {
		return nil
	}
	out := *k
	return &out
}

// clone returns a detached copy of a model config.
func (m *ModelConfig) clone() *ModelConfig {
	if m == nil {
		return nil
	}
	out := *m
	return &out
}

// clone returns a detached copy of an audit record.
func (r *Audit) clone() *Audit {
	if r == nil {
		return nil
	}
	out := *r
	return &out
}

// ListAccounts returns a detached snapshot of every account. Callers may read
// and even mutate the result without holding any lock and without affecting the
// store; all writes must go through the mutation helpers.
func (s *Store) ListAccounts() []*Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Account, 0, len(s.state.Accounts))
	for _, account := range s.state.Accounts {
		out = append(out, account.clone())
	}
	return out
}

func (s *Store) AccountByID(id string) (*Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, account := range s.state.Accounts {
		if account.ID == id {
			return account.clone(), true
		}
	}
	return nil, false
}

func (s *Store) AddAccount(account *Account) error {
	s.mu.Lock()
	defer s.unlockAndNotify()
	for _, existing := range s.state.Accounts {
		if existing.Cookie != "" && existing.Cookie == account.Cookie {
			return ErrAlreadyExists
		}
	}
	if account.ID == "" {
		account.ID = "acc_" + randomToken(8)
	}
	if account.Kind == "" {
		account.Kind = KindCookie
	}
	if account.Priority <= 0 {
		account.Priority = 50
	}
	if account.MaxConcurrent <= 0 {
		account.MaxConcurrent = 2
	}
	account.Status = StatusActive
	now := time.Now()
	account.CreatedAt = now
	account.UpdatedAt = now
	s.state.Accounts = append(s.state.Accounts, account)
	s.markDirtyLocked()
	return nil
}

func (s *Store) UpdateAccount(id string, apply func(*Account)) (*Account, error) {
	s.mu.Lock()
	defer s.unlockAndNotify()
	for _, account := range s.state.Accounts {
		if account.ID != id {
			continue
		}
		apply(account)
		account.UpdatedAt = time.Now()
		if !account.Enabled {
			account.Status = StatusDisabled
		} else if account.Status == StatusDisabled {
			account.Status = StatusActive
		}
		s.markDirtyLocked()
		return account.clone(), nil
	}
	return nil, ErrNotFound
}

func (s *Store) DeleteAccount(id string) error {
	s.mu.Lock()
	defer s.unlockAndNotify()
	for index, account := range s.state.Accounts {
		if account.ID != id {
			continue
		}
		s.state.Accounts = append(s.state.Accounts[:index], s.state.Accounts[index+1:]...)
		s.markDirtyLocked()
		return nil
	}
	return ErrNotFound
}

// DeleteAccounts removes every account whose id is listed and returns the count.
func (s *Store) DeleteAccounts(ids []string) (int, error) {
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	s.mu.Lock()
	defer s.unlockAndNotify()
	kept := make([]*Account, 0, len(s.state.Accounts))
	deleted := 0
	for _, account := range s.state.Accounts {
		if _, ok := wanted[account.ID]; ok {
			deleted++
			continue
		}
		kept = append(kept, account)
	}
	if deleted == 0 {
		return 0, nil
	}
	s.state.Accounts = kept
	s.markDirtyLocked()
	return deleted, nil
}

// DeleteAccountsByStatus removes accounts in any of the given states.
func (s *Store) DeleteAccountsByStatus(statuses []string) (int, error) {
	wanted := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		wanted[status] = struct{}{}
	}
	s.mu.Lock()
	defer s.unlockAndNotify()
	kept := make([]*Account, 0, len(s.state.Accounts))
	deleted := 0
	for _, account := range s.state.Accounts {
		if _, ok := wanted[account.Status]; ok {
			deleted++
			continue
		}
		kept = append(kept, account)
	}
	if deleted == 0 {
		return 0, nil
	}
	s.state.Accounts = kept
	s.markDirtyLocked()
	return deleted, nil
}

// UpdateAccounts applies a mutation to every id and persists once.
func (s *Store) UpdateAccounts(ids []string, apply func(*Account)) (int, error) {
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	s.mu.Lock()
	defer s.unlockAndNotify()
	updated := 0
	now := time.Now()
	for _, account := range s.state.Accounts {
		if _, ok := wanted[account.ID]; !ok {
			continue
		}
		apply(account)
		account.UpdatedAt = now
		if !account.Enabled {
			account.Status = StatusDisabled
		} else if account.Status == StatusDisabled {
			account.Status = StatusActive
		}
		updated++
	}
	if updated == 0 {
		return 0, nil
	}
	s.markDirtyLocked()
	return updated, nil
}

// SaveAccountState persists mutable runtime fields of one account.
func (s *Store) SaveAccountState(id string, apply func(*Account)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, account := range s.state.Accounts {
		if account.ID == id {
			apply(account)
			break
		}
	}
	s.markDirtyLocked()
}

func (s *Store) AccountGroups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{})
	out := make([]string, 0, 8)
	for _, account := range s.state.Accounts {
		if account.Group == "" {
			continue
		}
		if _, ok := seen[account.Group]; ok {
			continue
		}
		seen[account.Group] = struct{}{}
		out = append(out, account.Group)
	}
	sort.Strings(out)
	return out
}

// --------------------------------------------------------------- client keys

func (s *Store) ListClientKeys() []*ClientKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ClientKey, 0, len(s.state.ClientKeys))
	for _, key := range s.state.ClientKeys {
		out = append(out, key.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *Store) ClientKeyByValue(value string) (*ClientKey, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range s.state.ClientKeys {
		if key.Key == value {
			return key.clone(), true
		}
	}
	return nil, false
}

func (s *Store) CreateClientKey(name string, rpm, maxConcurrent int) (*ClientKey, error) {
	s.mu.Lock()
	defer s.unlockAndNotify()
	key := &ClientKey{
		ID:            "key_" + randomToken(6),
		Name:          name,
		Key:           "sk-dola-" + randomToken(20),
		Enabled:       true,
		RPMLimit:      rpm,
		MaxConcurrent: maxConcurrent,
		CreatedAt:     time.Now(),
	}
	s.state.ClientKeys = append(s.state.ClientKeys, key)
	s.markDirtyLocked()
	return key, nil
}

func (s *Store) UpdateClientKey(id string, apply func(*ClientKey)) (*ClientKey, error) {
	s.mu.Lock()
	defer s.unlockAndNotify()
	for _, key := range s.state.ClientKeys {
		if key.ID != id {
			continue
		}
		apply(key)
		s.markDirtyLocked()
		return key.clone(), nil
	}
	return nil, ErrNotFound
}

func (s *Store) DeleteClientKey(id string) error {
	s.mu.Lock()
	defer s.unlockAndNotify()
	for index, key := range s.state.ClientKeys {
		if key.ID != id {
			continue
		}
		s.state.ClientKeys = append(s.state.ClientKeys[:index], s.state.ClientKeys[index+1:]...)
		s.markDirtyLocked()
		return nil
	}
	return ErrNotFound
}

// BumpClientKeyUsage records usage without forcing a synchronous disk write on
// the request path.
func (s *Store) BumpClientKeyUsage(id string) {
	s.mu.Lock()
	for _, key := range s.state.ClientKeys {
		if key.ID == id {
			key.TotalRequests++
			key.LastUsedAt = time.Now()
			break
		}
	}
	s.mu.Unlock()
}

// -------------------------------------------------------------------- models

func (s *Store) ListModels() []*ModelConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*ModelConfig, 0, len(s.state.Models))
	for _, model := range s.state.Models {
		out = append(out, model.clone())
	}
	return out
}

func (s *Store) ModelByID(id string) (*ModelConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, model := range s.state.Models {
		if model.ID == id {
			return model.clone(), true
		}
	}
	return nil, false
}

func (s *Store) UpdateModel(id string, apply func(*ModelConfig)) (*ModelConfig, error) {
	s.mu.Lock()
	defer s.unlockAndNotify()
	for _, model := range s.state.Models {
		if model.ID != id {
			continue
		}
		apply(model)
		s.markDirtyLocked()
		// Return a snapshot: callers serialise this straight into a response,
		// and handing out the live pointer would race with the next writer.
		return model.clone(), nil
	}
	return nil, ErrNotFound
}

// RecordModelUsage accumulates per-model counters.
func (s *Store) RecordModelUsage(id string, requests, tokens int64) {
	s.mu.Lock()
	for _, model := range s.state.Models {
		if model.ID == id {
			model.Requests += requests
			model.Tokens += tokens
			break
		}
	}
	s.mu.Unlock()
}

// -------------------------------------------------------------------- audits

func (s *Store) AppendAudit(audit *Audit) {
	settings := s.Settings()
	s.mu.Lock()
	s.state.Audits = append([]*Audit{audit}, s.state.Audits...)
	if len(s.state.Audits) > settings.Audit.MaxRecords {
		s.state.Audits = s.state.Audits[:settings.Audit.MaxRecords]
	}
	s.mu.Unlock()
}

func (s *Store) ListAudits() []*Audit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Audit, 0, len(s.state.Audits))
	for _, audit := range s.state.Audits {
		out = append(out, audit.clone())
	}
	return out
}

func (s *Store) AuditByID(id string) (*Audit, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, audit := range s.state.Audits {
		if audit.ID == id {
			return audit.clone(), true
		}
	}
	return nil, false
}

func (s *Store) ClearAudits() error {
	s.mu.Lock()
	defer s.unlockAndNotify()
	s.state.Audits = nil
	s.markDirtyLocked()
	return nil
}

// PurgeAudits drops records older than the retention window.
func (s *Store) PurgeAudits() {
	settings := s.Settings()
	if settings.Audit.RetentionDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-settings.Retention())
	s.mu.Lock()
	kept := make([]*Audit, 0, len(s.state.Audits))
	for _, audit := range s.state.Audits {
		if audit.CreatedAt.After(cutoff) {
			kept = append(kept, audit)
		}
	}
	s.state.Audits = kept
	s.mu.Unlock()
}

// --------------------------------------------------------------------- media

func (s *Store) AddMedia(item *MediaItem) {
	s.mu.Lock()
	s.state.Media = append([]*MediaItem{item}, s.state.Media...)
	if len(s.state.Media) > 500 {
		s.state.Media = s.state.Media[:500]
	}
	s.mu.Unlock()
}

func (s *Store) ListMedia() []*MediaItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*MediaItem, len(s.state.Media))
	copy(out, s.state.Media)
	return out
}

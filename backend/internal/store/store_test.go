package store

import (
	"sync"
	"testing"
	"time"

	"dola2api/internal/config"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir(), "admin", "admin12345")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// TestSaveAccountStateCallbackReadsSettings is a regression guard for a total
// service deadlock. Mutation callbacks run while the write lock is held, and
// production callbacks (admin.syncQuota, pool.release) read settings from
// inside them. Go's sync.RWMutex is not reentrant, so if Settings() took the
// read lock the goroutine would wait on itself forever and every subsequent
// request would block behind the leaked write lock.
func TestSaveAccountStateCallbackReadsSettings(t *testing.T) {
	st := newTestStore(t)

	account := &Account{Name: "probe", Cookie: "oauth_token=x", Enabled: true}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		st.SaveAccountState(account.ID, func(target *Account) {
			// Mirrors admin.syncQuota: read settings from within the callback.
			target.CooldownUntil = time.Now().Add(st.Settings().CooldownBase())
			target.Status = StatusCooldown
		})
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("SaveAccountState deadlocked: callback could not read Settings")
	}

	updated, ok := st.AccountByID(account.ID)
	if !ok {
		t.Fatal("account disappeared")
	}
	if updated.Status != StatusCooldown {
		t.Fatalf("callback mutation was not applied: status=%q", updated.Status)
	}
	if updated.CooldownUntil.IsZero() {
		t.Fatal("callback did not set CooldownUntil")
	}
}

// TestSettingsReadableUnderWriteLock covers every mutation helper that runs a
// caller supplied callback under the write lock.
func TestSettingsReadableUnderWriteLock(t *testing.T) {
	st := newTestStore(t)

	account := &Account{Name: "probe", Cookie: "oauth_token=x", Enabled: true}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}
	key, err := st.CreateClientKey("probe-key", 10, 2)
	if err != nil {
		t.Fatalf("create client key: %v", err)
	}

	cases := []struct {
		name string
		run  func()
	}{
		{"UpdateAccount", func() {
			if _, err := st.UpdateAccount(account.ID, func(a *Account) {
				a.Priority = 1 + int(st.Settings().Routing.CooldownBaseSec%9)
			}); err != nil {
				t.Errorf("UpdateAccount: %v", err)
			}
		}},
		{"UpdateAccounts", func() {
			if _, err := st.UpdateAccounts([]string{account.ID}, func(a *Account) {
				a.MaxConcurrent = 1 + int(st.Settings().Routing.MaxAttempts)
			}); err != nil {
				t.Errorf("UpdateAccounts: %v", err)
			}
		}},
		{"UpdateClientKey", func() {
			if _, err := st.UpdateClientKey(key.ID, func(k *ClientKey) {
				k.RPMLimit = 30 + st.Settings().Audit.RetentionDays
			}); err != nil {
				t.Errorf("UpdateClientKey: %v", err)
			}
		}},
		{"UpdateModel", func() {
			models := st.ListModels()
			if len(models) == 0 {
				return
			}
			if _, err := st.UpdateModel(models[0].ID, func(m *ModelConfig) {
				m.Description = "probe-" + st.Settings().Routing.Strategy
			}); err != nil {
				t.Errorf("UpdateModel: %v", err)
			}
		}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				defer close(done)
				tc.run()
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatalf("%s deadlocked: callback could not read Settings", tc.name)
			}
		})
	}
}

// TestSettingsSnapshotFollowsUpdate makes sure the lock-free snapshot stays in
// sync with the persisted state after a settings change.
func TestSettingsSnapshotFollowsUpdate(t *testing.T) {
	st := newTestStore(t)

	if err := st.UpdateSettings(func(s *config.Settings) {
		s.Routing.CooldownBaseSec = 123
		s.Audit.RetentionDays = 11
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	if got := st.Settings().Routing.CooldownBaseSec; got != 123 {
		t.Fatalf("snapshot not refreshed: cooldownBaseSec=%d", got)
	}
	if got := st.Settings().CooldownBase(); got != 123*time.Second {
		t.Fatalf("derived duration wrong: %v", got)
	}

	// A fresh store reading the same directory must observe the persisted value.
	// Persistence is debounced, so force a flush before reopening.
	if err := st.Save(); err != nil {
		t.Fatalf("flush store: %v", err)
	}
	reopened, err := Open(st.DataDir(), "admin", "admin12345")
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if got := reopened.Settings().Routing.CooldownBaseSec; got != 123 {
		t.Fatalf("persisted settings not reloaded: %d", got)
	}
}

// TestConcurrentSettingsReadsAndWrites exercises the atomic snapshot under the
// race detector.
func TestConcurrentSettingsReadsAndWrites(t *testing.T) {
	st := newTestStore(t)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = st.Settings().CooldownBase()
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(stop)
		for i := 0; i < 50; i++ {
			if err := st.UpdateSettings(func(s *config.Settings) {
				s.Routing.CooldownBaseSec = 10 + i
			}); err != nil {
				t.Errorf("update settings: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}

// ListAccounts and AccountByID must hand out detached copies. The pool reads
// account fields outside of any lock, so returning pointers into the live state
// would race with SaveAccountState mutating those same objects.
func TestListAccountsReturnsDetachedSnapshot(t *testing.T) {
	st := newTestStore(t)

	account := &Account{ID: "acc_1", Name: "primary", Cookie: "oauth_token=x", Enabled: true}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	snapshot := st.ListAccounts()
	if len(snapshot) != 1 {
		t.Fatalf("snapshot length = %d, want 1", len(snapshot))
	}

	// Mutating the snapshot must not reach the store.
	snapshot[0].Status = "tampered"
	snapshot[0].Cookie = "tampered"
	snapshot[0].Priority = 999
	snapshot[0].Quota = &Quota{Plan: "tampered"}

	live, ok := st.AccountByID("acc_1")
	if !ok {
		t.Fatal("account missing")
	}
	if live.Status == "tampered" || live.Cookie == "tampered" || live.Priority == 999 {
		t.Fatalf("snapshot mutation leaked into the store: %+v", live)
	}
	if live.Quota != nil && live.Quota.Plan == "tampered" {
		t.Fatal("nested quota pointer was shared with the snapshot")
	}

	// And the reverse: a store mutation must not rewrite an earlier snapshot.
	before := st.ListAccounts()[0]
	st.SaveAccountState("acc_1", func(a *Account) {
		a.Status = StatusInvalid
		a.FailCount = 7
	})
	if before.Status == StatusInvalid || before.FailCount == 7 {
		t.Fatal("store mutation rewrote an already returned snapshot")
	}
}

func TestAccountByIDReturnsDetachedCopy(t *testing.T) {
	st := newTestStore(t)

	account := &Account{ID: "acc_1", Name: "primary", Cookie: "oauth_token=x", Enabled: true}
	if err := st.AddAccount(account); err != nil {
		t.Fatalf("add account: %v", err)
	}

	first, ok := st.AccountByID("acc_1")
	if !ok {
		t.Fatal("account missing")
	}
	second, ok := st.AccountByID("acc_1")
	if !ok {
		t.Fatal("account missing on second read")
	}
	if first == second {
		t.Fatal("AccountByID returned the same pointer twice; callers could race on it")
	}

	if _, ok := st.AccountByID("nope"); ok {
		t.Fatal("unknown id reported as found")
	}
}

// The same detachment rule that applies to accounts applies to every other
// collection the console serialises: a returned pointer must not alias the live
// state, or a concurrent writer races with the response encoder.
func TestListModelsReturnsDetachedSnapshot(t *testing.T) {
	st := newTestStore(t)

	models := st.ListModels()
	if len(models) == 0 {
		t.Fatal("default catalogue is empty")
	}
	target := models[0]

	// Mutating the snapshot must not reach the store.
	target.Enabled = !target.Enabled
	target.Name = "tampered"

	live, ok := st.ModelByID(target.ID)
	if !ok {
		t.Fatal("model missing")
	}
	if live.Name == "tampered" {
		t.Fatal("snapshot mutation leaked into the store")
	}

	// And a store write must not rewrite an already returned snapshot.
	before := st.ListModels()[0]
	if _, err := st.UpdateModel(before.ID, func(m *ModelConfig) { m.Name = "renamed" }); err != nil {
		t.Fatalf("update model: %v", err)
	}
	if before.Name == "renamed" {
		t.Fatal("store mutation rewrote a previously returned snapshot")
	}

	// Two reads must not hand back the same pointer.
	if st.ListModels()[0] == st.ListModels()[0] {
		t.Fatal("ListModels returned the same pointer twice")
	}
}

func TestListClientKeysReturnsDetachedSnapshot(t *testing.T) {
	st := newTestStore(t)

	created, err := st.CreateClientKey("snapshot-key", 10, 2)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	keys := st.ListClientKeys()
	if len(keys) != 1 {
		t.Fatalf("keys = %d, want 1", len(keys))
	}
	keys[0].Name = "tampered"
	keys[0].Enabled = false

	live, ok := st.ClientKeyByValue(created.Key)
	if !ok {
		t.Fatal("key missing")
	}
	if live.Name == "tampered" || !live.Enabled {
		t.Fatalf("snapshot mutation leaked into the store: %+v", live)
	}

	// UpdateClientKey must also hand back a detached copy.
	updated, err := st.UpdateClientKey(created.ID, func(k *ClientKey) { k.Name = "renamed" })
	if err != nil {
		t.Fatalf("update key: %v", err)
	}
	updated.Name = "tampered-again"
	if again, _ := st.ClientKeyByValue(created.Key); again.Name != "renamed" {
		t.Fatalf("returned pointer aliases the live key: %q", again.Name)
	}
}

func TestListAuditsReturnsDetachedSnapshot(t *testing.T) {
	st := newTestStore(t)

	st.AppendAudit(&Audit{ID: "audit_1", Model: "dola-fast", Status: 200})

	audits := st.ListAudits()
	if len(audits) != 1 {
		t.Fatalf("audits = %d, want 1", len(audits))
	}
	audits[0].Status = 500

	live, ok := st.AuditByID("audit_1")
	if !ok {
		t.Fatal("audit missing")
	}
	if live.Status == 500 {
		t.Fatal("snapshot mutation leaked into the store")
	}
}

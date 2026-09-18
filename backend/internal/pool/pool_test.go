package pool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"dola2api/internal/config"
	"dola2api/internal/dola"
	"dola2api/internal/store"
)

// The pool decides which account a request rides on, so its selection rules and
// its health bookkeeping are the highest value code in the project. These tests
// pin down both, and the last one exists to keep the concurrency fix honest.

type fixture struct {
	store    *store.Store
	pool     *Pool
	settings *config.Settings
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(dir, "admin", "admin12345")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	settings := config.DefaultSettings(dir)
	// Fail fast by default; individual tests opt back into waiting.
	settings.Routing.CapacityWaitSec = 0
	settings.Routing.StickyTTLSec = 300
	settings.Routing.CooldownBaseSec = 2
	settings.Routing.CooldownMaxSec = 8

	f := &fixture{store: st, settings: &settings}
	f.pool = New(st, func() config.Settings { return *f.settings })
	return f
}

// addAccount inserts an enabled account with a working cookie.
func (f *fixture) addAccount(t *testing.T, id string, priority int) *store.Account {
	t.Helper()
	return f.addAccountWith(t, id, priority, "oauth_token="+id, 1)
}

func (f *fixture) addAccountWith(t *testing.T, id string, priority int, cookie string, maxConcurrent int) *store.Account {
	t.Helper()
	account := &store.Account{
		ID:            id,
		Name:          id,
		Kind:          "cookie",
		Cookie:        cookie,
		Enabled:       true,
		Priority:      priority,
		MaxConcurrent: maxConcurrent,
		Status:        store.StatusActive,
		CreatedAt:     time.Now(),
	}
	if err := f.store.AddAccount(account); err != nil {
		t.Fatalf("add account %s: %v", id, err)
	}
	return account
}

// acquire is the happy-path helper: borrow, assert success, hand back the lease.
func (f *fixture) acquire(t *testing.T, sessionKey string) *Lease {
	t.Helper()
	lease, err := f.pool.Acquire(context.Background(), sessionKey)
	if err != nil {
		t.Fatalf("acquire(%q): %v", sessionKey, err)
	}
	return lease
}

// reload reads an account back from the store to observe bookkeeping.
func (f *fixture) reload(t *testing.T, id string) *store.Account {
	t.Helper()
	account, ok := f.store.AccountByID(id)
	if !ok {
		t.Fatalf("account %s missing", id)
	}
	return account
}

// --- selection rules -------------------------------------------------------

func TestAcquireSkipsDisabledAccount(t *testing.T) {
	f := newFixture(t)
	off := f.addAccount(t, "off", 0)
	f.addAccount(t, "on", 10)

	if _, err := f.store.UpdateAccount(off.ID, func(a *store.Account) { a.Enabled = false }); err != nil {
		t.Fatalf("disable: %v", err)
	}

	lease := f.acquire(t, "")
	defer lease.Release(nil)
	if lease.Account.ID != "on" {
		t.Fatalf("selected %q, want on", lease.Account.ID)
	}
}

func TestAcquireSkipsInvalidAccount(t *testing.T) {
	f := newFixture(t)
	bad := f.addAccount(t, "bad", 0)
	f.addAccount(t, "good", 10)

	// An invalid account is dead until an operator re-enables it, even when its
	// cooldown window has already elapsed.
	f.store.SaveAccountState(bad.ID, func(a *store.Account) {
		a.Status = store.StatusInvalid
	})

	lease := f.acquire(t, "")
	defer lease.Release(nil)
	if lease.Account.ID != "good" {
		t.Fatalf("selected %q, want good", lease.Account.ID)
	}
}

func TestAcquireSkipsAccountWithoutCookie(t *testing.T) {
	f := newFixture(t)
	empty := f.addAccountWith(t, "empty", 0, "", 1)
	f.addAccount(t, "ready", 10)

	lease := f.acquire(t, "")
	defer lease.Release(nil)
	if lease.Account.ID != "ready" {
		t.Fatalf("selected %q, want ready", lease.Account.ID)
	}
	if empty.Cookie != "" {
		t.Fatal("fixture no longer models a cookieless account")
	}
}

func TestAcquireSkipsCoolingAccount(t *testing.T) {
	f := newFixture(t)
	cooling := f.addAccount(t, "cooling", 0)
	f.addAccount(t, "fresh", 10)

	f.store.SaveAccountState(cooling.ID, func(a *store.Account) {
		a.Status = store.StatusCooldown
		a.CooldownUntil = time.Now().Add(time.Minute)
	})

	lease := f.acquire(t, "")
	defer lease.Release(nil)
	if lease.Account.ID != "fresh" {
		t.Fatalf("selected %q, want fresh", lease.Account.ID)
	}
}

func TestAcquireReclaimsExpiredCooldown(t *testing.T) {
	f := newFixture(t)
	expired := f.addAccount(t, "expired", 0)

	f.store.SaveAccountState(expired.ID, func(a *store.Account) {
		a.Status = store.StatusCooldown
		a.CooldownUntil = time.Now().Add(-time.Second)
	})

	lease := f.acquire(t, "")
	defer lease.Release(nil)
	if lease.Account.ID != "expired" {
		t.Fatalf("selected %q, want expired", lease.Account.ID)
	}
}

func TestAcquireRespectsMaxConcurrent(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "single", 0, "oauth_token=single", 1)

	first := f.acquire(t, "")
	defer first.Release(nil)

	// The only account is saturated, so a second attempt must report no capacity
	// rather than oversubscribing it.
	_, err := f.pool.Acquire(context.Background(), "")
	if !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("err = %v, want ErrNoCapacity", err)
	}
}

// routable is tested directly here because AddAccount normalises a non-positive
// MaxConcurrent to 2, so the zero-value rule can never be reached through the
// public API.
func TestRoutableTreatsZeroMaxConcurrentAsOne(t *testing.T) {
	now := time.Now()
	account := &store.Account{
		Enabled:       true,
		Cookie:        "oauth_token=x",
		Status:        store.StatusActive,
		MaxConcurrent: 0,
	}

	if !routable(account, 0, now) {
		t.Fatal("account with unset MaxConcurrent should accept one request")
	}
	if routable(account, 1, now) {
		t.Fatal("unset MaxConcurrent should cap at one, not allow two")
	}
}

func TestAcquireReportsEmptyPool(t *testing.T) {
	f := newFixture(t)

	if _, err := f.pool.Acquire(context.Background(), ""); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("err = %v, want ErrNoAccount", err)
	}
}

func TestAcquireHonoursContextCancellation(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "busy", 0, "oauth_token=busy", 1)
	held := f.acquire(t, "")
	defer held.Release(nil)

	// Allow waiting, then cancel: the caller must not be pinned to the deadline.
	f.settings.Routing.CapacityWaitSec = 30
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := f.pool.Acquire(ctx, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("cancellation ignored for %s", elapsed)
	}
}

func TestAcquireWaitsForCapacityThenSucceeds(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "single", 0, "oauth_token=single", 1)
	held := f.acquire(t, "")

	f.settings.Routing.CapacityWaitSec = 10
	go func() {
		time.Sleep(300 * time.Millisecond)
		held.Release(nil)
	}()

	lease, err := f.pool.Acquire(context.Background(), "")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	lease.Release(nil)
}

// --- routing strategies ----------------------------------------------------

func TestPriorityOrdersCandidates(t *testing.T) {
	f := newFixture(t)
	f.addAccount(t, "low", 50)
	f.addAccount(t, "high", 1)

	// PreferIdle would let the in-flight tiebreaker win; disable it so the
	// ordering under test is priority alone.
	f.settings.Routing.PreferIdle = false
	f.settings.Routing.Strategy = "priority"

	lease := f.acquire(t, "")
	defer lease.Release(nil)
	if lease.Account.ID != "high" {
		t.Fatalf("selected %q, want high", lease.Account.ID)
	}
}

func TestPreferIdleBeatsPriority(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "important", 1, "oauth_token=important", 5)
	f.addAccountWith(t, "spare", 99, "oauth_token=spare", 5)

	f.settings.Routing.PreferIdle = true
	f.settings.Routing.Strategy = "priority"

	// Saturate the important account; preferIdle must fall through to the idle
	// one even though priority says otherwise.
	first := f.acquire(t, "")
	if first.Account.ID != "important" {
		t.Fatalf("first selection = %q, want important", first.Account.ID)
	}
	defer first.Release(nil)

	second := f.acquire(t, "")
	defer second.Release(nil)
	if second.Account.ID != "spare" {
		t.Fatalf("second selection = %q, want spare (idle preferred)", second.Account.ID)
	}
}

func TestRoundRobinRotatesAcrossAccounts(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 5)
	f.addAccountWith(t, "b", 0, "oauth_token=b", 5)
	f.settings.Routing.PreferIdle = false
	f.settings.Routing.Strategy = "round_robin"

	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		lease := f.acquire(t, "")
		seen[lease.Account.ID]++
		lease.Release(nil)
	}
	if seen["a"] != 3 || seen["b"] != 3 {
		t.Fatalf("round robin distribution = %v, want 3/3", seen)
	}
}

func TestRandomStrategyStaysWithinCandidates(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 5)
	f.addAccountWith(t, "b", 0, "oauth_token=b", 5)
	f.settings.Routing.Strategy = "random"

	for i := 0; i < 20; i++ {
		lease := f.acquire(t, "")
		if lease.Account.ID != "a" && lease.Account.ID != "b" {
			t.Fatalf("selected unknown account %q", lease.Account.ID)
		}
		lease.Release(nil)
	}
}

// --- sticky sessions -------------------------------------------------------

func TestStickySessionKeepsAccount(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 5)
	f.addAccountWith(t, "b", 0, "oauth_token=b", 5)
	// Round robin would otherwise alternate between the two.
	f.settings.Routing.Strategy = "round_robin"

	first := f.acquire(t, "session-1")
	defer first.Release(nil)

	second := f.acquire(t, "session-1")
	defer second.Release(nil)

	if first.Account.ID != second.Account.ID {
		t.Fatalf("sticky session moved from %q to %q", first.Account.ID, second.Account.ID)
	}
}

func TestStickySessionMovesWhenAccountUnroutable(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 5)
	f.addAccountWith(t, "b", 0, "oauth_token=b", 5)

	first := f.acquire(t, "session-1")
	if first.Account.ID != "a" {
		t.Fatalf("first selection = %q, want a", first.Account.ID)
	}
	first.Release(nil)

	// Kill the pinned account; the session must fall through to the survivor.
	f.store.SaveAccountState("a", func(acc *store.Account) {
		acc.Status = store.StatusInvalid
	})

	second := f.acquire(t, "session-1")
	defer second.Release(nil)
	if second.Account.ID != "b" {
		t.Fatalf("sticky session stayed on unroutable account %q", second.Account.ID)
	}
}

func TestStickySessionExpires(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 5)
	f.addAccountWith(t, "b", 0, "oauth_token=b", 5)
	f.settings.Routing.Strategy = "round_robin"
	f.settings.Routing.StickyTTLSec = 1

	first := f.acquire(t, "session-1")
	firstID := first.Account.ID
	first.Release(nil)

	// Let the binding lapse, then confirm the session is free to be rescheduled
	// rather than pinned forever.
	f.pool.mu.Lock()
	entry := f.pool.sticky["session-1"]
	entry.expires = time.Now().Add(-time.Second)
	f.pool.sticky["session-1"] = entry
	f.pool.mu.Unlock()

	second := f.acquire(t, "session-1")
	defer second.Release(nil)
	// The cursor position after the first pick is an implementation detail, so
	// assert on the property that matters: the binding no longer forces the same
	// account.
	if second.Account.ID == firstID {
		t.Fatalf("expired sticky session stayed on %q", firstID)
	}
}

func TestDropStickyForgetsBinding(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 5)
	f.addAccountWith(t, "b", 0, "oauth_token=b", 5)
	f.settings.Routing.Strategy = "round_robin"

	first := f.acquire(t, "session-1")
	firstID := first.Account.ID
	first.Release(nil)
	f.pool.DropSticky("session-1")

	second := f.acquire(t, "session-1")
	defer second.Release(nil)
	if second.Account.ID == firstID {
		t.Fatalf("after drop, selection stayed on %q", firstID)
	}
}

func TestStickyDisabledWhenTTLZero(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 5)
	f.addAccountWith(t, "b", 0, "oauth_token=b", 5)
	f.settings.Routing.Strategy = "round_robin"
	f.settings.Routing.StickyTTLSec = 0

	first := f.acquire(t, "session-1")
	first.Release(nil)
	second := f.acquire(t, "session-1")
	defer second.Release(nil)

	if first.Account.ID == second.Account.ID {
		t.Fatal("sticky TTL 0 still pinned the session")
	}
}

// --- release bookkeeping ---------------------------------------------------

func TestReleaseOnSuccessClearsCooldown(t *testing.T) {
	f := newFixture(t)
	f.addAccount(t, "a", 0)

	lease := f.acquire(t, "")

	// Park the account mid-request, the way a failed quota sync would. The
	// cooldown has to be applied while the lease is held, because a cooling
	// account is not schedulable in the first place.
	f.store.SaveAccountState("a", func(acc *store.Account) {
		acc.Status = store.StatusCooldown
		acc.CooldownUntil = time.Now().Add(time.Minute)
		acc.FailCount = 3
		acc.LastError = "boom"
	})

	lease.Release(nil)

	account := f.reload(t, "a")
	if account.Status != store.StatusActive {
		t.Fatalf("status = %q, want active", account.Status)
	}
	if !account.CooldownUntil.IsZero() {
		t.Fatalf("cooldownUntil = %s, want zero", account.CooldownUntil)
	}
	if account.FailCount != 0 {
		t.Fatalf("failCount = %d, want 0", account.FailCount)
	}
	if account.LastError != "" {
		t.Fatalf("lastError = %q, want empty", account.LastError)
	}
	if account.SuccessCount != 1 {
		t.Fatalf("successCount = %d, want 1", account.SuccessCount)
	}
	if account.LastUsedAt.IsZero() {
		t.Fatal("lastUsedAt was not stamped")
	}
}

func TestReleaseOnInvalidCredentialDisablesAccount(t *testing.T) {
	f := newFixture(t)
	f.addAccount(t, "a", 0)

	lease := f.acquire(t, "")
	lease.Release(dola.ErrInvalidCredential)

	account := f.reload(t, "a")
	if account.Status != store.StatusInvalid {
		t.Fatalf("status = %q, want invalid", account.Status)
	}
	if account.FailCount != 1 {
		t.Fatalf("failCount = %d, want 1", account.FailCount)
	}
	if account.LastError == "" {
		t.Fatal("lastError was not recorded")
	}
}

func TestReleaseAppliesExponentialBackoffWithCeiling(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 5)

	// base = 2s, max = 8s => 2s, 4s, 8s, then capped at 8s.
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, expected := range want {
		lease := f.acquire(t, "")
		lease.Release(errors.New("upstream hiccup"))

		account := f.reload(t, "a")
		if account.Status != store.StatusCooldown {
			t.Fatalf("round %d: status = %q, want cooldown", i, account.Status)
		}
		if account.FailCount != i+1 {
			t.Fatalf("round %d: failCount = %d, want %d", i, account.FailCount, i+1)
		}
		remaining := time.Until(account.CooldownUntil)
		if remaining < expected-time.Second || remaining > expected+time.Second {
			t.Fatalf("round %d: cooldown %s, want ~%s", i, remaining, expected)
		}

		// Re-arm the account without touching FailCount. ClearCooldown would
		// reset the counter, and with it the backoff sequence under test.
		f.store.SaveAccountState("a", func(acc *store.Account) {
			acc.Status = store.StatusActive
			acc.CooldownUntil = time.Time{}
		})
	}
}

func TestReleaseTruncatesLongError(t *testing.T) {
	f := newFixture(t)
	f.addAccount(t, "a", 0)

	// The leading ASCII byte shifts every following 3-byte character off the 240
	// byte boundary, so a naive byte slice would split a rune here.
	message := "x" + strings.Repeat("失败", 200)

	lease := f.acquire(t, "")
	lease.Release(errors.New(message))

	account := f.reload(t, "a")
	if !utf8.ValidString(account.LastError) {
		t.Fatalf("lastError is not valid UTF-8: %q", account.LastError)
	}
	if len(account.LastError) > 243 {
		t.Fatalf("lastError length = %d, want truncated", len(account.LastError))
	}
	if !strings.HasSuffix(account.LastError, "…") {
		t.Fatalf("truncated error should end with an ellipsis: %q", account.LastError)
	}
}

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
}

func TestReleaseIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.addAccount(t, "a", 0)

	lease := f.acquire(t, "")
	lease.Release(nil)
	lease.Release(nil)
	lease.Release(errors.New("late failure"))

	// A double release must not inflate counters or re-apply failure state.
	account := f.reload(t, "a")
	if account.SuccessCount != 1 {
		t.Fatalf("successCount = %d, want 1", account.SuccessCount)
	}
	if account.FailCount != 0 {
		t.Fatalf("failCount = %d, want 0", account.FailCount)
	}
	if inflight := f.pool.Inflight("a"); inflight != 0 {
		t.Fatalf("inflight = %d, want 0", inflight)
	}
}

func TestClearCooldownRestoresRoutability(t *testing.T) {
	f := newFixture(t)
	f.addAccount(t, "a", 0)

	f.store.SaveAccountState("a", func(acc *store.Account) {
		acc.Status = store.StatusCooldown
		acc.CooldownUntil = time.Now().Add(time.Hour)
		acc.FailCount = 4
		acc.LastError = "nope"
	})

	if _, err := f.pool.Acquire(context.Background(), ""); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("err = %v, want ErrNoCapacity while cooling", err)
	}

	f.pool.ClearCooldown("a")

	account := f.reload(t, "a")
	if account.Status != store.StatusActive || account.FailCount != 0 || account.LastError != "" {
		t.Fatalf("clear left stale state: %+v", account)
	}

	lease := f.acquire(t, "")
	lease.Release(nil)
}

// --- summary ---------------------------------------------------------------

func TestSummaryCountsEveryStatus(t *testing.T) {
	f := newFixture(t)
	f.addAccount(t, "active", 0)
	f.addAccount(t, "cooling", 0)
	f.addAccount(t, "off", 0)
	f.addAccount(t, "dead", 0)

	f.store.SaveAccountState("cooling", func(a *store.Account) {
		a.Status = store.StatusCooldown
		a.CooldownUntil = time.Now().Add(time.Hour)
	})
	if _, err := f.store.UpdateAccount("off", func(a *store.Account) { a.Enabled = false }); err != nil {
		t.Fatalf("disable: %v", err)
	}
	f.store.SaveAccountState("dead", func(a *store.Account) { a.Status = store.StatusInvalid })

	total, active, cooldown, disabled, invalid, routableCount := f.pool.Summary()
	if total != 4 || active != 1 || cooldown != 1 || disabled != 1 || invalid != 1 {
		t.Fatalf("summary = total %d active %d cooldown %d disabled %d invalid %d",
			total, active, cooldown, disabled, invalid)
	}
	if routableCount != 1 {
		t.Fatalf("routable = %d, want 1", routableCount)
	}
}

// SaveAccountState does not mirror Enabled into Status the way UpdateAccount
// does, so an account switched off through it would look active on the console
// unless Summary checks Enabled itself.
func TestSummaryCountsAccountDisabledViaSaveState(t *testing.T) {
	f := newFixture(t)
	f.addAccount(t, "a", 0)
	f.store.SaveAccountState("a", func(acc *store.Account) { acc.Enabled = false })

	total, active, _, disabled, _, routableCount := f.pool.Summary()
	if total != 1 || disabled != 1 || active != 0 {
		t.Fatalf("summary = total %d active %d disabled %d, want 1/0/1", total, active, disabled)
	}
	if routableCount != 0 {
		t.Fatalf("routable = %d, want 0", routableCount)
	}
}

func TestSnapshotCopiesInflightCounters(t *testing.T) {
	f := newFixture(t)
	f.addAccountWith(t, "a", 0, "oauth_token=a", 3)

	lease := f.acquire(t, "")
	defer lease.Release(nil)

	snapshot := f.pool.Snapshot()
	if snapshot["a"] != 1 {
		t.Fatalf("snapshot[a] = %d, want 1", snapshot["a"])
	}
	snapshot["a"] = 99
	if f.pool.Inflight("a") != 1 {
		t.Fatal("Snapshot leaked the live map")
	}
}

// --- concurrency -----------------------------------------------------------

// TestConcurrentSchedulingAndStateWrites is a regression guard for the deadlock
// fix. Moving ListAccounts out of p.mu broke a lock-order cycle, but it also
// means pick() now reads account fields without holding any lock, while the
// store mutates those same objects under store.mu. Run with -race to catch it.
func TestConcurrentSchedulingAndStateWrites(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 4; i++ {
		f.addAccountWith(t, fmt.Sprintf("acct-%d", i), i, fmt.Sprintf("oauth_token=%d", i), 8)
	}
	f.settings.Routing.CapacityWaitSec = 2

	var wg sync.WaitGroup

	// Schedulers: continuously borrow and return accounts.
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			for i := 0; i < 40; i++ {
				lease, err := f.pool.Acquire(ctx, fmt.Sprintf("session-%d", worker))
				if err != nil {
					return
				}
				f.pool.Summary()
				lease.Release(nil)
			}
		}(worker)
	}

	// Writers: mutate account state the way the admin API and release() do.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			id := fmt.Sprintf("acct-%d", i%4)
			f.store.SaveAccountState(id, func(a *store.Account) {
				a.Priority = i % 5
				a.SuccessCount = i
				a.LastUsedAt = time.Now()
			})
			f.pool.ClearCooldown(id)
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent scheduling deadlocked")
	}
}

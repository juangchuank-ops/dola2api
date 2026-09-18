package pool

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"dola2api/internal/config"
	"dola2api/internal/dola"
	"dola2api/internal/store"
)

var (
	// ErrNoAccount means the pool is empty.
	ErrNoAccount = errors.New("no account available")
	// ErrNoCapacity means every account is saturated or cooling down.
	ErrNoCapacity = errors.New("all accounts are busy")
)

type stickyEntry struct {
	accountID string
	expires   time.Time
}

// Pool owns account selection, in-flight accounting and cooldown state.
type Pool struct {
	mu       sync.Mutex
	store    *store.Store
	settings func() config.Settings
	inflight map[string]int
	sticky   map[string]stickyEntry
	cursor   int
}

func New(st *store.Store, settings func() config.Settings) *Pool {
	return &Pool{
		store:    st,
		settings: settings,
		inflight: make(map[string]int),
		sticky:   make(map[string]stickyEntry),
	}
}

// Lease is one borrowed account. Release must be called exactly once.
type Lease struct {
	Account *store.Account
	pool    *Pool
	once    sync.Once
}

// Release returns the account to the pool and applies health bookkeeping.
func (l *Lease) Release(err error) {
	l.once.Do(func() {
		l.pool.release(l.Account.ID, err)
	})
}

// Inflight reports the current in-flight count for an account.
func (p *Pool) Inflight(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inflight[id]
}

// Snapshot returns a copy of the in-flight counters.
func (p *Pool) Snapshot() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.inflight))
	for key, value := range p.inflight {
		out[key] = value
	}
	return out
}

// Acquire borrows an account, waiting up to the configured capacity timeout.
func (p *Pool) Acquire(ctx context.Context, sessionKey string) (*Lease, error) {
	settings := p.settings()
	deadline := time.Now().Add(settings.CapacityWait())

	for {
		account, wait := p.pick(sessionKey)
		if account != nil {
			return &Lease{Account: account, pool: p}, nil
		}
		if time.Now().After(deadline) {
			return nil, wait
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// pick selects a routable account and reserves an in-flight slot.
func (p *Pool) pick(sessionKey string) (*store.Account, error) {
	// Read the store and settings before taking p.mu. Keeping every cross
	// component call outside the critical section means p.mu can never take
	// part in a lock-order cycle with store.mu.
	accounts := p.store.ListAccounts()
	if len(accounts) == 0 {
		return nil, ErrNoAccount
	}
	settings := p.settings()

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()

	// Sticky sessions reuse their previous account while it stays routable.
	if sessionKey != "" && settings.StickyTTL() > 0 {
		if entry, ok := p.sticky[sessionKey]; ok {
			if now.After(entry.expires) {
				delete(p.sticky, sessionKey)
			} else {
				for _, account := range accounts {
					if account.ID == entry.accountID && routable(account, p.inflight[account.ID], now) {
						p.inflight[account.ID]++
						entry.expires = now.Add(settings.StickyTTL())
						p.sticky[sessionKey] = entry
						return account, nil
					}
				}
			}
		}
	}

	candidates := make([]*store.Account, 0, len(accounts))
	for _, account := range accounts {
		if routable(account, p.inflight[account.ID], now) {
			candidates = append(candidates, account)
		}
	}
	if len(candidates) == 0 {
		return nil, ErrNoCapacity
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if settings.Routing.PreferIdle && p.inflight[left.ID] != p.inflight[right.ID] {
			return p.inflight[left.ID] < p.inflight[right.ID]
		}
		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})

	var chosen *store.Account
	switch settings.Routing.Strategy {
	case "round_robin":
		p.cursor = (p.cursor + 1) % len(candidates)
		chosen = candidates[p.cursor]
	case "random":
		chosen = candidates[rand.Intn(len(candidates))]
	case "priority":
		chosen = candidates[0]
	default: // least_inflight
		chosen = candidates[0]
		for _, candidate := range candidates[1:] {
			if p.inflight[candidate.ID] < p.inflight[chosen.ID] {
				chosen = candidate
			}
		}
	}

	p.inflight[chosen.ID]++
	if sessionKey != "" && settings.StickyTTL() > 0 {
		p.sticky[sessionKey] = stickyEntry{accountID: chosen.ID, expires: now.Add(settings.StickyTTL())}
	}
	return chosen, nil
}

func routable(account *store.Account, inflight int, now time.Time) bool {
	if !account.Enabled {
		return false
	}
	switch account.Status {
	case store.StatusDisabled, store.StatusInvalid:
		return false
	}
	if account.Status == store.StatusCooldown && now.Before(account.CooldownUntil) {
		return false
	}
	if account.Cookie == "" {
		return false
	}
	limit := account.MaxConcurrent
	if limit <= 0 {
		limit = 1
	}
	return inflight < limit
}

// release applies health bookkeeping and clears the in-flight slot.
func (p *Pool) release(id string, err error) {
	p.mu.Lock()
	if p.inflight[id] > 0 {
		p.inflight[id]--
	}
	p.mu.Unlock()

	settings := p.settings()
	p.store.SaveAccountState(id, func(account *store.Account) {
		account.LastUsedAt = time.Now()
		if err == nil {
			account.SuccessCount++
			account.FailCount = 0
			if account.Status == store.StatusCooldown {
				account.Status = store.StatusActive
				account.CooldownUntil = time.Time{}
			}
			if account.Status == store.StatusActive {
				account.LastError = ""
			}
			return
		}

		account.FailCount++
		account.LastError = truncate(err.Error(), 240)

		if errors.Is(err, dola.ErrInvalidCredential) {
			account.Status = store.StatusInvalid
			account.CooldownUntil = time.Time{}
			return
		}

		base := settings.CooldownBase()
		maxCooldown := settings.CooldownMax()
		backoff := time.Duration(float64(base) * math.Pow(2, float64(min(account.FailCount-1, 6))))
		if backoff > maxCooldown {
			backoff = maxCooldown
		}
		account.Status = store.StatusCooldown
		account.CooldownUntil = time.Now().Add(backoff)
	})
}

// ClearCooldown resets a cooling account so it can be scheduled again.
func (p *Pool) ClearCooldown(id string) {
	p.store.SaveAccountState(id, func(account *store.Account) {
		account.Status = store.StatusActive
		account.CooldownUntil = time.Time{}
		account.FailCount = 0
		account.LastError = ""
	})
}

// DropSticky forgets the sticky binding for a session.
func (p *Pool) DropSticky(sessionKey string) {
	if sessionKey == "" {
		return
	}
	p.mu.Lock()
	delete(p.sticky, sessionKey)
	p.mu.Unlock()
}

// Summary reports pool health for dashboards and /health.
func (p *Pool) Summary() (total, active, cooldown, disabled, invalid, routableCount int) {
	accounts := p.store.ListAccounts()
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, account := range accounts {
		total++
		// Enabled is checked first because the two mutation paths disagree:
		// UpdateAccount mirrors Enabled into Status, while SaveAccountState does
		// not. An account switched off through the latter would otherwise still
		// be reported as active on the dashboard.
		switch {
		case !account.Enabled, account.Status == store.StatusDisabled:
			disabled++
		case account.Status == store.StatusInvalid:
			invalid++
		case account.Status == store.StatusCooldown:
			cooldown++
		default:
			active++
		}
		if routable(account, p.inflight[account.ID], now) {
			routableCount++
		}
	}
	return
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	// Cut on a rune boundary. Slicing at a fixed byte offset can split a
	// multi-byte character, which would push invalid UTF-8 into lastError and
	// from there into the audit log and the console.
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + "…"
}

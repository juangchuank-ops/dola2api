package gateway

import (
	"fmt"
	"sync"
	"time"

	"dola2api/internal/store"
)

// limiter enforces per-key RPM and concurrency limits.
type limiter struct {
	mu       sync.Mutex
	windows  map[string][]time.Time
	inflight map[string]int
}

func newLimiter() *limiter {
	return &limiter{
		windows:  make(map[string][]time.Time),
		inflight: make(map[string]int),
	}
}

func (l *limiter) acquire(key *store.ClientKey) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if key.RPMLimit > 0 {
		cutoff := time.Now().Add(-time.Minute)
		recent := l.windows[key.ID][:0]
		for _, stamp := range l.windows[key.ID] {
			if stamp.After(cutoff) {
				recent = append(recent, stamp)
			}
		}
		if len(recent) >= key.RPMLimit {
			l.windows[key.ID] = recent
			return fmt.Errorf("rate limit exceeded (%d requests per minute)", key.RPMLimit)
		}
		l.windows[key.ID] = append(recent, time.Now())
	}

	limit := key.MaxConcurrent
	if limit <= 0 {
		limit = 4
	}
	if l.inflight[key.ID] >= limit {
		return fmt.Errorf("concurrency limit exceeded (%d simultaneous requests)", limit)
	}
	l.inflight[key.ID]++
	return nil
}

func (l *limiter) release(key *store.ClientKey) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight[key.ID] > 0 {
		l.inflight[key.ID]--
	}
}

// Inflight reports current concurrency for one key (used by the console).
func (l *limiter) Inflight(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inflight[id]
}

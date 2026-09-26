package api

import (
	"sync"
	"time"
)

// failLimiter is a small in-memory sliding-window counter of failed attempts
// per key (a client address). It exists to cap how much work one client can make
// the server do with bad credentials: verifying an API token costs a bcrypt
// comparison against every active token, so an unauthenticated flood of bogus
// "aegis_..." tokens would otherwise be a cheap way to pin the CPU.
type failLimiter struct {
	mu     sync.Mutex
	window time.Duration
	max    int
	hits   map[string][]time.Time
}

func newFailLimiter(window time.Duration, max int) *failLimiter {
	return &failLimiter{window: window, max: max, hits: map[string][]time.Time{}}
}

// prune drops entries older than the window. Caller holds l.mu.
func (l *failLimiter) prune(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = kept
	return kept
}

// blocked reports whether key has already failed max times within the window.
func (l *failLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, time.Now())) >= l.max
}

// fail records one failed attempt for key.
func (l *failLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	// Bound memory: a flood from many addresses must not grow the map forever.
	if len(l.hits) > 20000 {
		for k := range l.hits {
			l.prune(k, now)
		}
		if len(l.hits) > 20000 {
			l.hits = map[string][]time.Time{}
		}
	}
	l.hits[key] = append(l.prune(key, now), now)
}

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"aegis/internal/store"
	"aegis/internal/svc"
)

func TestFailLimiterWindowAndKeys(t *testing.T) {
	l := newFailLimiter(50*time.Millisecond, 3)
	for i := 0; i < 3; i++ {
		if l.blocked("a") {
			t.Fatalf("blocked after only %d failures", i)
		}
		l.fail("a")
	}
	if !l.blocked("a") {
		t.Error("not blocked after reaching the limit")
	}
	if l.blocked("b") {
		t.Error("another key was affected")
	}
	time.Sleep(70 * time.Millisecond)
	if l.blocked("a") {
		t.Error("still blocked after the window passed")
	}
}

func TestFailLimiterBoundsItsMemory(t *testing.T) {
	l := newFailLimiter(time.Hour, 3)
	for i := 0; i < 30000; i++ {
		l.fail(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
	}
	l.mu.Lock()
	n := len(l.hits)
	l.mu.Unlock()
	if n > 21000 {
		t.Errorf("limiter holds %d keys; it must stay bounded", n)
	}
}

// Verifying an API token is a bcrypt comparison against every active token, so
// an unauthenticated flood of bogus tokens used to be a cheap CPU-exhaustion
// attack. Rotating X-Forwarded-For must not get around the cap.
func TestBogusAPITokensAreCappedPerRealAddress(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{Tokens: svc.NewAPITokens(st), tokenFails: newFailLimiter(time.Minute, 3)}

	call := func(remote, xff string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/domains", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		_, _, status, _ := s.resolveBearer(r, "aegis_not-a-real-token")
		return status
	}

	for i := 0; i < 3; i++ {
		if got := call("198.51.100.7:4000", fmt.Sprintf("203.0.113.%d", i)); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, got)
		}
	}
	if got := call("198.51.100.7:4001", "203.0.113.99"); got != http.StatusTooManyRequests {
		t.Errorf("after the cap, with a fresh spoofed header: status %d, want 429", got)
	}
	if got := call("198.51.100.8:4000", ""); got != http.StatusUnauthorized {
		t.Errorf("a different real address was throttled: status %d", got)
	}
}

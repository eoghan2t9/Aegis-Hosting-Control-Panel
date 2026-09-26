package svc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The ACME challenge route on the WebFTP listener is unauthenticated and is
// proxied from every webftp.<domain> vhost, while the panel runs as root. A
// URL-encoded traversal in the {token} segment used to reach os.ReadFile and
// return any file on the host.
func TestWebFTPACMEChallengeRejectsTraversal(t *testing.T) {
	// Zero value on purpose: a request that got past the token check would hit
	// the nil Store and panic, so a passing test proves the guard ran first.
	h := (&WebFTP{}).Handler()

	for _, token := range []string{
		"..%2f..%2f..%2f..%2fetc%2fpasswd",
		"%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"..%5c..%5cwindows",
		"..",
		"a%2fb",
		"tok%00en",
		strings.Repeat("a", 201),
	} {
		req := httptest.NewRequest(http.MethodGet, "http://webftp.example.com/.well-known/acme-challenge/"+token, nil)
		req.Host = "webftp.example.com"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		// 404 from our guard, or 307 when ServeMux itself path-cleans a bare
		// ".." segment before the handler runs. Never 200 (a file was served).
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusTemporaryRedirect {
			t.Errorf("token %q: status %d, want 404 (or a mux redirect)", token, rec.Code)
		}
		if rec.Code == http.StatusNotFound && !strings.Contains(rec.Body.String(), "not found") {
			t.Errorf("token %q: unexpected 404 body %q", token, rec.Body.String())
		}
	}
}

func TestACMETokenRe(t *testing.T) {
	for _, ok := range []string{"abc", "A-b_9", "Zm9vYmFy-_", strings.Repeat("a", 200)} {
		if !acmeTokenRe.MatchString(ok) {
			t.Errorf("real-looking ACME token %q was rejected", ok)
		}
	}
	for _, bad := range []string{"", "..", "../x", "a/b", `a\b`, "a b", "a.b", "a\x00b", strings.Repeat("a", 201)} {
		if acmeTokenRe.MatchString(bad) {
			t.Errorf("token %q should be rejected", bad)
		}
	}
}

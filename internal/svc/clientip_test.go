package svc

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func reqFrom(remote string, xff ...string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = remote
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

// Reproduced live: 12 wrong guesses with a rotating X-Forwarded-For were all
// checked and none locked out, because the first (client-chosen) entry was used
// as the client's address.
func TestClientIPIgnoresSpoofedForwardedForFromTheInternet(t *testing.T) {
	for _, tc := range []struct{ name, remote, xff, want string }{
		{"direct client rotating a fake address", "198.51.100.7:51234", "203.0.113.1", "198.51.100.7"},
		{"direct client with a chain", "198.51.100.7:51234", "10.0.0.1, 203.0.113.2", "198.51.100.7"},
		{"direct IPv6 client", "[2001:db8::5]:4000", "203.0.113.9", "2001:db8::5"},
		{"private address is not special", "192.168.1.20:1", "203.0.113.9", "192.168.1.20"},
	} {
		if got := ClientIP(reqFrom(tc.remote, tc.xff)); got != tc.want {
			t.Errorf("%s: ClientIP = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Behind a local nginx/Apache/Caddy vhost the peer is loopback and the proxy
// appends the address it saw. Only that last entry is trustworthy; everything
// to its left is what the client sent.
func TestClientIPUsesTheProxysEntryBehindALoopbackProxy(t *testing.T) {
	for _, tc := range []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"proxy appended the real client", "127.0.0.1:40000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"client tried to prepend a fake one", "127.0.0.1:40000", []string{"203.0.113.1, 198.51.100.7"}, "198.51.100.7"},
		{"several header lines", "127.0.0.1:40000", []string{"203.0.113.1", "198.51.100.7"}, "198.51.100.7"},
		{"IPv6 loopback proxy", "[::1]:40000", []string{"198.51.100.7"}, "198.51.100.7"},
		{"garbage last entry falls back to the peer", "127.0.0.1:40000", []string{"203.0.113.1, not-an-ip"}, "127.0.0.1"},
		{"no header", "127.0.0.1:40000", nil, "127.0.0.1"},
	} {
		if got := ClientIP(reqFrom(tc.remote, tc.xff...)); got != tc.want {
			t.Errorf("%s: ClientIP = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClientIPHandlesAddressWithoutPort(t *testing.T) {
	if got := ClientIP(reqFrom("198.51.100.7", "203.0.113.1")); got != "198.51.100.7" {
		t.Errorf("ClientIP = %q", got)
	}
}

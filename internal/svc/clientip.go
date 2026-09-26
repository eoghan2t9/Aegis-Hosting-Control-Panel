package svc

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the address of the client that made the request.
//
// X-Forwarded-For is whatever the client chooses to send, unless a proxy we run
// put it there. The panel is reached two ways: directly (the native Go server on
// :80/:443, where the TCP peer IS the client) or behind a local nginx, Apache or
// Caddy vhost (the peer is loopback, and that proxy appends the address it saw
// to any header the client already sent). So the header is honoured only when
// the immediate peer is loopback, and then only its LAST entry: the one our own
// proxy added. Everything to its left is attacker-controlled.
//
// Taking the first entry instead (as this used to) let anyone rotate a fake
// X-Forwarded-For per request and defeat the login lockout, brute-forcing
// passwords and 2FA codes with no limit, and forge the address in the audit log.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		// Values, not Get: a proxy may add its entry as a separate header line,
		// and Get would return only the first (client-supplied) one.
		if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
			parts := strings.Split(xff, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if net.ParseIP(last) != nil {
				return last
			}
		}
	}
	return host
}

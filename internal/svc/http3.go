package svc

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/quic-go/quic-go/http3"
)

// HTTP/3 (QUIC) for the native Go web server.
//
// It runs beside the TCP HTTPS listener on the same port number over UDP, with the
// same certificates and the same handler, so every site gets it with no per-site
// setup. Browsers learn about it from the Alt-Svc header on ordinary HTTPS
// responses and fall back to HTTP/2 on their own if UDP is blocked, so a
// firewall dropping UDP 443 costs nothing but the speed-up.

// http3Enabled reports whether HTTP/3 is wanted; AEGIS_GO_HTTP3=off turns it off.
func http3Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AEGIS_GO_HTTP3"))) {
	case "off", "0", "false", "no":
		return false
	}
	return true
}

// altSvcValue is the Alt-Svc header that advertises HTTP/3 on the HTTPS port.
// The one-day lifetime keeps a later change (UDP blocked, feature turned off)
// from lingering in browsers for long.
func altSvcValue(httpsAddr string) (string, error) {
	_, port, err := net.SplitHostPort(httpsAddr)
	if err != nil || port == "" {
		return "", fmt.Errorf("bad https address %q", httpsAddr)
	}
	return fmt.Sprintf(`h3=":%s"; ma=86400`, port), nil
}

// withAltSvc advertises HTTP/3 on responses served over TCP TLS. Responses that
// already arrived over QUIC do not need it.
func withAltSvc(next http.Handler, altSvc string) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor < 3 {
			rw.Header().Add("Alt-Svc", altSvc)
		}
		next.ServeHTTP(rw, r)
	})
}

// startHTTP3 serves handler over QUIC on the UDP port of httpsAddr. A failure to
// bind UDP is reported to the caller, which carries on without HTTP/3: the sites
// must never go down because the extra listener could not start.
func startHTTP3(httpsAddr string, tlsConf *tls.Config, handler http.Handler) (*http3.Server, error) {
	addr, err := net.ResolveUDPAddr("udp", httpsAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http3.Server{
		Addr:        httpsAddr,
		Handler:     handler,
		TLSConfig:   tlsConf,
		IdleTimeout: 2 * time.Minute,
	}
	go func() {
		if err := srv.Serve(conn); err != nil && !strings.Contains(err.Error(), "closed") {
			slog.Warn("go http/3 server stopped", "err", err)
		}
	}()
	return srv, nil
}

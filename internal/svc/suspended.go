package svc

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aegis/internal/store"
)

// SuspendedAddr is the loopback address of the "account suspended" site. While a
// user is suspended, WebServer.Apply renders each of their domains as a reverse
// proxy to this address instead of their real site, so every web server backend
// (native, nginx, Apache, Caddy) shows the same page without any backend-specific
// code, and the domain's own TLS certificate still terminates the connection.
const SuspendedAddr = "127.0.0.1:8092"

// SuspendedServer answers every request for a suspended user's domains with a
// 503 "Account Suspended" page. The one exception is Let's Encrypt HTTP-01
// challenge files, which are still served from the domain's document root so
// certificate renewals keep working while the account is suspended.
type SuspendedServer struct {
	Store *store.Store
}

func NewSuspendedServer(st *store.Store) *SuspendedServer { return &SuspendedServer{Store: st} }

func (s *SuspendedServer) Handler() http.Handler { return http.HandlerFunc(s.serve) }

const acmePrefix = "/.well-known/acme-challenge/"

var acmeTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

func (s *SuspendedServer) serve(rw http.ResponseWriter, r *http.Request) {
	host := requestHost(r)
	if strings.HasPrefix(r.URL.Path, acmePrefix) && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		s.serveChallenge(rw, r, host, strings.TrimPrefix(r.URL.Path, acmePrefix))
		return
	}
	h := rw.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	rw.WriteHeader(http.StatusServiceUnavailable)
	if r.Method != http.MethodHead {
		_, _ = rw.Write([]byte(suspendedPage(host)))
	}
}

// serveChallenge returns the ACME token file for host, if it has one.
func (s *SuspendedServer) serveChallenge(rw http.ResponseWriter, r *http.Request, host, token string) {
	if !acmeTokenRe.MatchString(token) {
		http.NotFound(rw, r)
		return
	}
	root := s.docrootFor(r.Context(), host)
	if root == "" {
		http.NotFound(rw, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(root, ".well-known", "acme-challenge", token))
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "text/plain")
	if r.Method != http.MethodHead {
		_, _ = rw.Write(data)
	}
}

// docrootFor resolves a request host (a domain, its www form, one of its
// aliases, or its webftp.<domain> subdomain) to the document root that
// certificate challenges are written into.
func (s *SuspendedServer) docrootFor(ctx context.Context, host string) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for _, name := range []string{host, strings.TrimPrefix(host, "www."), strings.TrimPrefix(host, "webftp.")} {
		if d, err := s.Store.GetDomainByName(ctx, name); err == nil && d.DocumentRoot != "" {
			return d.DocumentRoot
		}
	}
	doms, err := s.Store.ListDomains(ctx, 0)
	if err != nil {
		return ""
	}
	for _, d := range doms {
		aliases, _ := s.Store.ListAliases(ctx, d.ID)
		for _, a := range aliases {
			if strings.EqualFold(a, host) {
				return d.DocumentRoot
			}
		}
	}
	return ""
}

// suspendedPage renders the page, in the panel's visual style. host is
// attacker-influenced (Host header), so it is escaped.
func suspendedPage(host string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex,nofollow">
<meta name="theme-color" content="#06080b"><title>Account Suspended</title><style>
:root{--bg0:#06080b;--bg1:#0b0f14;--line:#1e2732;--line-strong:#2a3644;--text-1:#e8edf2;--text-2:#9aa7b4;--text-3:#5c6a78;--accent:#c6f14e;--warn:#f2b64b;--teal:#4ee6c2;
--mono:"SFMono-Regular","JetBrains Mono",ui-monospace,Menlo,Consolas,monospace;
--sans:"Inter","SF Pro Text",-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Arial,sans-serif;color-scheme:dark}
*{box-sizing:border-box}html,body{margin:0;min-height:100%;background:var(--bg0);color:var(--text-1);font-family:var(--sans);font-size:15px;line-height:1.55;-webkit-font-smoothing:antialiased}
body{min-height:100vh;min-height:100dvh;display:flex;align-items:center;justify-content:center;padding:24px;
background-image:radial-gradient(900px 500px at 12% -10%,rgba(242,182,75,.06),transparent 60%),radial-gradient(800px 420px at 100% 0%,rgba(78,230,194,.04),transparent 55%),
linear-gradient(var(--line) 1px,transparent 1px),linear-gradient(90deg,var(--line) 1px,transparent 1px);background-size:auto,auto,44px 44px,44px 44px}
.card{width:100%;max-width:480px;text-align:center;background:var(--bg1);border:1px solid var(--line-strong);border-radius:10px;padding:34px 28px;box-shadow:0 24px 60px rgba(0,0,0,.45)}
.mark{width:58px;height:58px;margin:0 auto 20px;display:flex;align-items:center;justify-content:center;border-radius:50%;
border:1.5px solid rgba(242,182,75,.5);background:rgba(242,182,75,.08);color:var(--warn)}
.mark svg{width:26px;height:26px}
h1{margin:0 0 10px;font-size:22px;letter-spacing:-.01em}
p{margin:0 0 14px;color:var(--text-2)}
.host{display:inline-block;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-family:var(--mono);font-size:12px;color:var(--text-2);
background:#10151c;border:1px solid var(--line);border-radius:20px;padding:5px 14px;margin:2px 0 16px}
.foot{margin:18px 0 0;font-family:var(--mono);font-size:10.5px;letter-spacing:.14em;text-transform:uppercase;color:var(--text-3)}
</style></head><body><main class="card">
<div class="mark"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg></div>
<h1>Account Suspended</h1>
<div class="host">` + htmlEscape(host) + `</div>
<p>This website is currently unavailable because the hosting account it belongs to has been suspended.</p>
<p>If you own this website, please contact your hosting provider to have the account reinstated.</p>
<p class="foot">Error 503 &middot; Service unavailable</p>
</main></body></html>`
}

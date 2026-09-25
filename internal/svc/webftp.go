package svc

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"aegis/internal/store"
)

// WebFTPAddr is the fixed localhost address the shared webftp server listens
// on (started once at boot — see cmd/aegis/main.go). It is never reachable
// directly, only through each domain's auto-created "webftp.<domain>" vhost,
// which reverse-proxies here — the same ProxyTarget mechanism svc.Docker
// uses to attach a container to a domain (see Domains.createWebftpDomain).
const WebFTPAddr = "127.0.0.1:8091"

const (
	webftpSessionTTL = 2 * time.Hour
	webftpCookie     = "aegis_webftp"
)

// WebFTP serves a full browser file manager (browse/upload/download/rename/
// delete/edit/chmod/chown/search/zip/unzip/gallery) for a domain's dedicated
// FTP account (see Domains.createDefaultFTP): log in with the FTP
// username/password, then manage files within that domain's document root.
// One process serves every domain — which domain's files a request sees is
// resolved per-request from the Host header ("webftp.<domain>"), and the
// session cookie records which hostname it was minted for, so a session
// can't be replayed against a different domain's Host header.
type WebFTP struct {
	Store  *store.Store
	Files  *Files
	Thumbs *Thumbs

	mu       sync.Mutex
	sessions map[string]webftpSession
}

type webftpSession struct {
	userID   int64 // FTPAccount.UserID, for audit attribution only
	username string
	homeDir  string
	host     string // the "webftp.<domain>" hostname this session was minted for
	expires  time.Time
}

func NewWebFTP(st *store.Store, files *Files, thumbs *Thumbs) *WebFTP {
	return &WebFTP{Store: st, Files: files, Thumbs: thumbs, sessions: map[string]webftpSession{}}
}

func (w *WebFTP) newSession(userID int64, username, homeDir, host string) (string, error) {
	tok, err := RandomString(40)
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	w.sessions[tok] = webftpSession{userID: userID, username: username, homeDir: homeDir, host: host, expires: time.Now().Add(webftpSessionTTL)}
	w.mu.Unlock()
	return tok, nil
}

func (w *WebFTP) sessionFor(r *http.Request) (webftpSession, bool) {
	c, err := r.Cookie(webftpCookie)
	if err != nil || c.Value == "" {
		return webftpSession{}, false
	}
	w.mu.Lock()
	sess, ok := w.sessions[c.Value]
	w.mu.Unlock()
	if !ok || time.Now().After(sess.expires) || sess.host != requestHost(r) {
		return webftpSession{}, false
	}
	return sess, true
}

func (w *WebFTP) clearSession(r *http.Request) {
	c, err := r.Cookie(webftpCookie)
	if err != nil {
		return
	}
	w.mu.Lock()
	delete(w.sessions, c.Value)
	w.mu.Unlock()
}

// audit best-effort records a mutating webftp action, mirroring the panel's
// own s.audit — webftp has no *api.Server to call that through, but
// store.Store.AppendAudit is the same underlying primitive.
func (w *WebFTP) audit(r *http.Request, sess webftpSession, action, target, detail string) {
	_ = w.Store.AppendAudit(r.Context(), sess.userID, sess.username, action, target, detail, webftpClientIP(r))
}

// webftpClientIP mirrors internal/api/server.go's clientIP — duplicated
// rather than imported since svc is a lower-level package that api imports,
// not the reverse.
func webftpClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requestHost returns the hostname a request arrived on (Host header, port
// stripped, lowercased).
func requestHost(r *http.Request) string {
	h := r.Host
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(h)
}

// requestIsHTTPS reports whether the original client connection was HTTPS.
// webftp.go's own listener only ever speaks plain HTTP on loopback (TLS is
// terminated by the reverse-proxying nginx/apache/caddy/native-go vhost —
// see Domains.createWebftpDomain / webserver.go), so r.TLS is always nil
// here; the proxy layer sets X-Forwarded-Proto instead.
func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// Handler returns the http.Handler for the shared webftp listener.
func (w *WebFTP) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/acme-challenge/{token}", w.handleACMEChallenge)
	mux.HandleFunc("GET /", w.handleIndex)
	mux.HandleFunc("POST /login", w.handleLogin)
	mux.HandleFunc("POST /logout", w.handleLogout)
	mux.HandleFunc("GET /api/list", w.withSession(w.apiList))
	mux.HandleFunc("GET /api/download", w.withSession(w.apiDownload))
	mux.HandleFunc("POST /api/upload", w.withSession(w.apiUpload))
	mux.HandleFunc("POST /api/mkdir", w.withSession(w.apiMkdir))
	mux.HandleFunc("POST /api/rename", w.withSession(w.apiRename))
	mux.HandleFunc("POST /api/delete", w.withSession(w.apiDelete))
	mux.HandleFunc("GET /api/read", w.withSession(w.apiRead))
	mux.HandleFunc("POST /api/write", w.withSession(w.apiWrite))
	mux.HandleFunc("POST /api/chmod", w.withSession(w.apiChmod))
	mux.HandleFunc("POST /api/chown", w.withSession(w.apiChown))
	mux.HandleFunc("GET /api/search", w.withSession(w.apiSearch))
	mux.HandleFunc("POST /api/zip", w.withSession(w.apiZip))
	mux.HandleFunc("POST /api/unzip", w.withSession(w.apiUnzip))
	mux.HandleFunc("GET /api/thumb", w.withSession(w.apiThumb))
	return mux
}

func (w *WebFTP) withSession(next func(http.ResponseWriter, *http.Request, webftpSession)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		sess, ok := w.sessionFor(r)
		if !ok {
			writeWebFTPErrMsg(rw, http.StatusUnauthorized, "session expired")
			return
		}
		next(rw, r, sess)
	}
}

// sessionUser adapts a webftp session into the *store.User shape svc.Files
// expects — Files.Resolve jails to user.HomeDir when set, so this scopes
// every file operation to exactly this account's home without touching
// Files itself.
func (w *WebFTP) sessionUser(sess webftpSession) *store.User {
	return &store.User{Username: sess.username, HomeDir: sess.homeDir}
}

// --- pages --------------------------------------------------------------------

func (w *WebFTP) handleIndex(rw http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(requestHost(r), "webftp.") {
		http.NotFound(rw, r)
		return
	}
	if _, ok := w.sessionFor(r); !ok {
		writeWebFTPPage(rw, webftpLoginPage(requestHost(r), ""))
		return
	}
	writeWebFTPPage(rw, webftpBrowserPage(requestHost(r)))
}

// handleACMEChallenge answers a Let's Encrypt HTTP-01 challenge for
// webftp.<domain>, unauthenticated — this hostname is proxied entirely to
// this Go process (see Domains.createWebftpDomain), so it has no docroot of
// its own to serve the token from; instead it reads the same
// ".well-known/acme-challenge/<token>" file svc.webrootProvider writes into
// the *parent* domain's document root when SSL.obtain includes this
// hostname as an additional SAN.
func (w *WebFTP) handleACMEChallenge(rw http.ResponseWriter, r *http.Request) {
	host := requestHost(r)
	parent := strings.TrimPrefix(host, "webftp.")
	if parent == host {
		http.NotFound(rw, r)
		return
	}
	dom, err := w.Store.GetDomainByName(r.Context(), parent)
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(dom.DocumentRoot, ".well-known", "acme-challenge", r.PathValue("token")))
	if err != nil {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "text/plain")
	_, _ = rw.Write(data)
}

func (w *WebFTP) handleLogin(rw http.ResponseWriter, r *http.Request) {
	host := requestHost(r)
	parent := strings.TrimPrefix(host, "webftp.")
	if parent == host {
		http.NotFound(rw, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebFTPPage(rw, webftpLoginPage(host, "bad request"))
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	dom, err := w.Store.GetDomainByName(r.Context(), parent)
	if err != nil {
		writeWebFTPPage(rw, webftpLoginPage(host, "unknown domain"))
		return
	}
	acct, err := w.Store.GetFTPAccountByUsername(r.Context(), username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(acct.PasswordHash), []byte(password)) != nil {
		writeWebFTPPage(rw, webftpLoginPage(host, "invalid username or password"))
		return
	}
	if !acctCoversHome(acct.HomeDir, dom.DocumentRoot) {
		writeWebFTPPage(rw, webftpLoginPage(host, "this ftp account cannot access this domain"))
		return
	}
	// Scope the session to dom.DocumentRoot specifically, not acct.HomeDir —
	// the owner's master FTP account covers their whole home directory (see
	// acctCoversHome), but a session minted from *this* domain's webftp page
	// must only ever browse *this* domain's files, never every other domain
	// under the same home.
	tok, err := w.newSession(acct.UserID, acct.Username, dom.DocumentRoot, host)
	if err != nil {
		writeWebFTPPage(rw, webftpLoginPage(host, "could not create session, try again"))
		return
	}
	http.SetCookie(rw, &http.Cookie{
		Name: webftpCookie, Value: tok, Path: "/", HttpOnly: true,
		Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: int(webftpSessionTTL.Seconds()),
	})
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

// acctCoversHome reports whether an FTP account's home directory covers a
// domain's document root — true when they're equal (the dedicated
// per-domain account created alongside the domain) or when the account's
// home is an ancestor of it (the owner's own master FTP account, which
// already has real FTP access to every domain under their home).
func acctCoversHome(acctHome, docRoot string) bool {
	acctHome = filepath.Clean(acctHome)
	docRoot = filepath.Clean(docRoot)
	if acctHome == docRoot {
		return true
	}
	rel, err := filepath.Rel(acctHome, docRoot)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (w *WebFTP) handleLogout(rw http.ResponseWriter, r *http.Request) {
	w.clearSession(r)
	http.SetCookie(rw, &http.Cookie{Name: webftpCookie, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

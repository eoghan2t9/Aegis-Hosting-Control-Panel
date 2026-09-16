package svc

import (
	"encoding/json"
	"io"
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

// WebFTP serves a small, self-contained browser file manager for a domain's
// dedicated FTP account (see Domains.createDefaultFTP): log in with the FTP
// username/password, then browse/upload/download/rename/delete within that
// account's home directory. One process serves every domain — which
// domain's files a request sees is resolved per-request from the Host
// header ("webftp.<domain>"), and the session cookie records which hostname
// it was minted for, so a session can't be replayed against a different
// domain's Host header.
type WebFTP struct {
	Store *store.Store
	Files *Files

	mu       sync.Mutex
	sessions map[string]webftpSession
}

type webftpSession struct {
	username string
	homeDir  string
	host     string // the "webftp.<domain>" hostname this session was minted for
	expires  time.Time
}

func NewWebFTP(st *store.Store, files *Files) *WebFTP {
	return &WebFTP{Store: st, Files: files, sessions: map[string]webftpSession{}}
}

func (w *WebFTP) newSession(username, homeDir, host string) (string, error) {
	tok, err := RandomString(40)
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	w.sessions[tok] = webftpSession{username: username, homeDir: homeDir, host: host, expires: time.Now().Add(webftpSessionTTL)}
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

// requestHost returns the hostname a request arrived on (Host header, port
// stripped, lowercased).
func requestHost(r *http.Request) string {
	h := r.Host
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(h)
}

// Handler returns the http.Handler for the shared webftp listener.
func (w *WebFTP) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", w.handleIndex)
	mux.HandleFunc("POST /login", w.handleLogin)
	mux.HandleFunc("POST /logout", w.handleLogout)
	mux.HandleFunc("GET /api/list", w.withSession(w.apiList))
	mux.HandleFunc("GET /api/download", w.withSession(w.apiDownload))
	mux.HandleFunc("POST /api/upload", w.withSession(w.apiUpload))
	mux.HandleFunc("POST /api/mkdir", w.withSession(w.apiMkdir))
	mux.HandleFunc("POST /api/rename", w.withSession(w.apiRename))
	mux.HandleFunc("POST /api/delete", w.withSession(w.apiDelete))
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
	tok, err := w.newSession(acct.Username, dom.DocumentRoot, host)
	if err != nil {
		writeWebFTPPage(rw, webftpLoginPage(host, "could not create session, try again"))
		return
	}
	http.SetCookie(rw, &http.Cookie{
		Name: webftpCookie, Value: tok, Path: "/", HttpOnly: true,
		Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: int(webftpSessionTTL.Seconds()),
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

// --- file API -------------------------------------------------------------------

func (w *WebFTP) apiList(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	entries, err := w.Files.List(w.sessionUser(sess), r.URL.Query().Get("path"))
	if err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	writeWebFTPJSON(rw, entries)
}

func (w *WebFTP) apiDownload(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	abs, err := w.Files.Resolve(w.sessionUser(sess), r.URL.Query().Get("path"))
	if err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(abs)+`"`)
	http.ServeFile(rw, r, abs)
}

func (w *WebFTP) apiUpload(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	if err := r.ParseMultipartForm(50 << 20); err != nil { // 50 MiB, matches the panel's file manager
		writeWebFTPErrMsg(rw, http.StatusBadRequest, "upload too large: "+err.Error())
		return
	}
	destDir := r.FormValue("path")
	user := w.sessionUser(sess)
	for _, fh := range r.MultipartForm.File["files"] {
		src, err := fh.Open()
		if err != nil {
			writeWebFTPErr(rw, err)
			return
		}
		dest := strings.TrimSuffix(destDir, "/") + "/" + fh.Filename
		abs, err := w.Files.Resolve(user, dest)
		if err != nil {
			src.Close()
			writeWebFTPErr(rw, err)
			return
		}
		out, err := os.OpenFile(abs, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			src.Close()
			writeWebFTPErr(rw, err)
			return
		}
		_, cerr := io.Copy(out, src)
		src.Close()
		out.Close()
		if cerr != nil {
			writeWebFTPErr(rw, cerr)
			return
		}
	}
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

type webftpPathReq struct {
	Path string `json:"path"`
}

func (w *WebFTP) apiMkdir(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpPathReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Mkdir(w.sessionUser(sess), req.Path, 0o755); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

func (w *WebFTP) apiDelete(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpPathReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Delete(w.sessionUser(sess), req.Path); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

type webftpRenameReq struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (w *WebFTP) apiRename(rw http.ResponseWriter, r *http.Request, sess webftpSession) {
	var req webftpRenameReq
	if !decodeWebFTPJSON(rw, r, &req) {
		return
	}
	if err := w.Files.Rename(w.sessionUser(sess), req.From, req.To); err != nil {
		writeWebFTPErr(rw, err)
		return
	}
	writeWebFTPJSON(rw, map[string]string{"status": "ok"})
}

// --- small local JSON/HTML helpers (internal/api's are unavailable here —
// svc is a lower-level package that api imports, not the reverse) -------------

func writeWebFTPJSON(rw http.ResponseWriter, v interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(v)
}

func writeWebFTPErrMsg(rw http.ResponseWriter, status int, msg string) {
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(status)
	_ = json.NewEncoder(rw).Encode(map[string]string{"error": msg})
}

func writeWebFTPErr(rw http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if err == ErrForbidden {
		status = http.StatusForbidden
	}
	writeWebFTPErrMsg(rw, status, err.Error())
}

func decodeWebFTPJSON(rw http.ResponseWriter, r *http.Request, v interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeWebFTPErrMsg(rw, http.StatusBadRequest, "bad request body")
		return false
	}
	return true
}

func writeWebFTPPage(rw http.ResponseWriter, html string) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte(html))
}

// --- HTML (self-contained: no dependency on the panel's embedded web/ assets,
// so this works standing entirely on its own listener/process) ---------------

const webftpCSS = `
:root{--bg0:#06080b;--bg1:#0b0f14;--bg2:#10151c;--bg3:#161d26;--line:#1e2732;--line-strong:#2a3644;
--text-1:#e8edf2;--text-2:#9aa7b4;--text-3:#5c6a78;--accent:#c6f14e;--accent-ink:#0a0f04;--danger:#ff6161;
--radius:10px;--radius-sm:7px}
*{box-sizing:border-box}
html,body{margin:0;height:100%;background:var(--bg0);color:var(--text-1);
font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Arial,sans-serif;font-size:14px}
.mono{font-family:"SFMono-Regular","JetBrains Mono",ui-monospace,Menlo,Consolas,monospace}
.wrap{max-width:920px;margin:0 auto;padding:24px}
.login-wrap{min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px}
.card{background:var(--bg1);border:1px solid var(--line-strong);border-radius:var(--radius);padding:22px}
.login-card{width:100%;max-width:360px}
h1{font-size:18px;margin:0 0 4px}
.dim{color:var(--text-3)}
.small{font-size:12px}
.err{color:var(--danger);font-size:12.5px;margin:0 0 10px}
.field{display:block;margin-bottom:14px}
.field-label{display:block;font-size:11.5px;font-weight:600;letter-spacing:.06em;text-transform:uppercase;color:var(--text-2);margin-bottom:6px}
input{width:100%;background:var(--bg0);border:1px solid var(--line-strong);color:var(--text-1);font:inherit;
padding:8px 11px;border-radius:var(--radius-sm);outline:none}
input:focus{border-color:var(--accent)}
.btn{appearance:none;border:1px solid var(--line-strong);background:var(--bg2);color:var(--text-1);font:inherit;
font-weight:600;font-size:13px;padding:8px 14px;border-radius:var(--radius-sm);cursor:pointer}
.btn:hover{border-color:#3a4a5a;background:var(--bg3)}
.btn-primary{background:var(--accent);border-color:var(--accent);color:var(--accent-ink)}
.btn-primary:hover{background:#d4f76b;border-color:#d4f76b}
.btn-block{width:100%}
.btn-ghost{background:transparent;border-color:transparent;color:var(--text-2)}
.btn-ghost:hover{background:var(--bg2);color:var(--text-1)}
.btn-sm{padding:4px 9px;font-size:12px}
.topbar{display:flex;align-items:center;justify-content:space-between;margin-bottom:16px;gap:10px;flex-wrap:wrap}
.crumbs{font-size:13px}
.crumbs a{color:var(--text-2);text-decoration:none}
.crumbs a:hover{color:var(--accent)}
.tbl{width:100%;border-collapse:collapse;background:var(--bg1);border:1px solid var(--line);border-radius:var(--radius);overflow:hidden}
.tbl th{text-align:left;font-size:11px;letter-spacing:.06em;text-transform:uppercase;color:var(--text-3);
padding:9px 12px;border-bottom:1px solid var(--line)}
.tbl td{padding:8px 12px;border-bottom:1px solid var(--line);font-size:13px}
.tbl tr:last-child td{border-bottom:none}
.tbl tr:hover td{background:var(--bg2)}
.name a{color:var(--text-1);text-decoration:none;cursor:pointer}
.name a:hover{color:var(--accent)}
.row-actions{display:flex;gap:4px;justify-content:flex-end}
.actions{display:flex;gap:8px;margin-bottom:14px;flex-wrap:wrap}
.toast{position:fixed;right:18px;bottom:18px;background:var(--bg2);border:1px solid var(--line-strong);
border-radius:var(--radius-sm);padding:9px 14px;font-size:13px}
.toast.err{border-color:var(--danger);color:var(--danger)}
`

func webftpLoginPage(host, errMsg string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<p class="err">` + htmlEscape(errMsg) + `</p>`
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Web FTP — ` + htmlEscape(host) + `</title><style>` + webftpCSS + `</style></head>
<body><div class="login-wrap"><div class="card login-card">
<h1>Web FTP</h1><p class="dim small mono">` + htmlEscape(host) + `</p>
` + errHTML + `
<form method="post" action="/login">
<label class="field"><span class="field-label">FTP username</span><input name="username" autocomplete="username" autofocus required></label>
<label class="field"><span class="field-label">Password</span><input name="password" type="password" autocomplete="current-password" required></label>
<button class="btn btn-primary btn-block" type="submit">Log in</button>
</form>
</div></div></body></html>`
}

func webftpBrowserPage(host string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Web FTP — ` + htmlEscape(host) + `</title><style>` + webftpCSS + `</style></head>
<body><div class="wrap">
<div class="topbar">
  <div><h1 style="margin:0">Web FTP</h1><div class="dim small mono">` + htmlEscape(host) + `</div></div>
  <form method="post" action="/logout"><button class="btn btn-ghost btn-sm" type="submit">Log out</button></form>
</div>
<div class="actions">
  <button class="btn btn-sm" id="btn-mkdir">New folder</button>
  <label class="btn btn-sm" style="cursor:pointer">Upload<input id="file-input" type="file" multiple style="display:none"></label>
</div>
<div class="crumbs mono" id="crumbs"></div>
<div style="height:10px"></div>
<table class="tbl"><thead><tr><th>Name</th><th>Size</th><th>Modified</th><th></th></tr></thead><tbody id="rows"></tbody></table>
</div>
<script>
let path = "/";
function esc(s){return String(s).replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]))}
function fmtSize(n){if(n<1024)return n+" B";const u=["KB","MB","GB","TB"];let i=-1;do{n/=1024;i++}while(n>=1024&&i<u.length-1);return n.toFixed(1)+" "+u[i]}
function toast(msg,err){const t=document.createElement("div");t.className="toast"+(err?" err":"");t.textContent=msg;document.body.appendChild(t);setTimeout(()=>t.remove(),3500)}
function joinPath(base,name){return (base.replace(/\/$/,"")+"/"+name).replace(/\/+/g,"/")}
function renderCrumbs(){
  const parts=path.split("/").filter(Boolean);
  let acc="",html='<a href="#" data-path="/">root</a>';
  for(const p of parts){acc+="/"+p;html+=' / <a href="#" data-path="'+esc(acc)+'">'+esc(p)+'</a>'}
  document.getElementById("crumbs").innerHTML=html;
  document.querySelectorAll("#crumbs a").forEach(a=>a.onclick=(e)=>{e.preventDefault();load(a.dataset.path)});
}
async function load(p){
  path=p||"/";
  renderCrumbs();
  const res=await fetch("/api/list?path="+encodeURIComponent(path));
  if(res.status===401){location.reload();return}
  if(!res.ok){toast((await res.json().catch(()=>({error:"failed to list"}))).error||"failed to list",true);return}
  const entries=await res.json();
  const rows=document.getElementById("rows");
  rows.innerHTML="";
  for(const e of entries){
    const tr=document.createElement("tr");
    const isDir=e.type==="dir";
    tr.innerHTML='<td class="name">'+(isDir?'<a data-nav="'+esc(e.path)+'">📁 '+esc(e.name)+'</a>':'<a href="/api/download?path='+encodeURIComponent(e.path)+'">📄 '+esc(e.name)+'</a>')+
      '</td><td class="dim">'+(isDir?"—":fmtSize(e.size))+'</td><td class="dim small">'+esc(new Date(e.mod_time).toLocaleString())+
      '</td><td><div class="row-actions"><button class="btn btn-ghost btn-sm" data-rename="'+esc(e.path)+'" data-name="'+esc(e.name)+'">rename</button>'+
      '<button class="btn btn-ghost btn-sm" data-del="'+esc(e.path)+'">delete</button></div></td>';
    rows.appendChild(tr);
  }
  rows.querySelectorAll("[data-nav]").forEach(a=>a.onclick=()=>load(a.dataset.nav));
  rows.querySelectorAll("[data-del]").forEach(b=>b.onclick=async()=>{
    if(!confirm("Delete this?"))return;
    const r=await fetch("/api/delete",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({path:b.dataset.del})});
    if(r.ok){toast("Deleted");load(path)}else toast((await r.json().catch(()=>({}))).error||"delete failed",true);
  });
  rows.querySelectorAll("[data-rename]").forEach(b=>b.onclick=async()=>{
    const name=prompt("New name",b.dataset.name);
    if(!name||name===b.dataset.name)return;
    const to=joinPath(path,name);
    const r=await fetch("/api/rename",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({from:b.dataset.rename,to})});
    if(r.ok){toast("Renamed");load(path)}else toast((await r.json().catch(()=>({}))).error||"rename failed",true);
  });
}
document.getElementById("btn-mkdir").onclick=async()=>{
  const name=prompt("Folder name");
  if(!name)return;
  const r=await fetch("/api/mkdir",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({path:joinPath(path,name)})});
  if(r.ok){toast("Folder created");load(path)}else toast((await r.json().catch(()=>({}))).error||"mkdir failed",true);
};
document.getElementById("file-input").onchange=async(e)=>{
  const files=e.target.files;
  if(!files.length)return;
  const fd=new FormData();
  fd.append("path",path);
  for(const f of files)fd.append("files",f);
  const r=await fetch("/api/upload",{method:"POST",body:fd});
  e.target.value="";
  if(r.ok){toast("Uploaded");load(path)}else toast((await r.json().catch(()=>({}))).error||"upload failed",true);
};
load("/");
</script>
</body></html>`
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

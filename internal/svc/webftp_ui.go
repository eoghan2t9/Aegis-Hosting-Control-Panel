package svc

import (
	"net/http"
	"strings"
)

func writeWebFTPPage(rw http.ResponseWriter, html string) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = rw.Write([]byte(html))
}

// --- HTML (self-contained: no dependency on the panel's embedded web/ assets,
// so this works standing entirely on its own listener/process) ---------------
//
// The look deliberately mirrors the panel's design system (web/css/aegis.css):
// same tokens, engineering-grid backdrop, hex login mark, wordmark, buttons,
// tables, modals and toasts. The CSS and JS below must not contain backticks
// (they live inside Go raw strings).

const webftpCSS = `
:root{--bg0:#06080b;--bg1:#0b0f14;--bg2:#10151c;--bg3:#161d26;--line:#1e2732;--line-strong:#2a3644;
--text-1:#e8edf2;--text-2:#9aa7b4;--text-3:#5c6a78;--accent:#c6f14e;--accent-ink:#0a0f04;
--accent-dim:rgba(198,241,78,.12);--teal:#4ee6c2;--teal-dim:rgba(78,230,194,.12);--warn:#f2b64b;
--danger:#ff6161;--danger-dim:rgba(255,97,97,.12);
--mono:"SFMono-Regular","JetBrains Mono","Cascadia Mono",ui-monospace,Menlo,Consolas,monospace;
--sans:"Inter","SF Pro Text",-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Helvetica Neue",Arial,sans-serif;
--radius:10px;--radius-sm:7px;color-scheme:dark}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
html,body{margin:0;min-height:100%;background:var(--bg0);color:var(--text-1);font-family:var(--sans);font-size:14px;
line-height:1.5;-webkit-font-smoothing:antialiased}
body{background-image:radial-gradient(900px 500px at 12% -10%,rgba(198,241,78,.05),transparent 60%),
radial-gradient(800px 420px at 100% 0%,rgba(78,230,194,.04),transparent 55%),
linear-gradient(var(--line) 1px,transparent 1px),linear-gradient(90deg,var(--line) 1px,transparent 1px);
background-size:auto,auto,44px 44px,44px 44px;background-attachment:fixed;min-height:100vh;min-height:100dvh}
.hidden{display:none !important}
::selection{background:var(--accent);color:var(--accent-ink)}
*::-webkit-scrollbar{width:10px;height:10px}
*::-webkit-scrollbar-thumb{background:#24303d;border-radius:8px;border:2px solid transparent;background-clip:content-box}
*::-webkit-scrollbar-track{background:transparent}
.mono{font-family:var(--mono)}
.dim{color:var(--text-3)}
.small{font-size:12px}
svg.ico{width:15px;height:15px;flex-shrink:0}

/* buttons */
.btn{appearance:none;border:1px solid var(--line-strong);background:var(--bg2);color:var(--text-1);font:inherit;font-weight:600;
font-size:13px;padding:8px 14px;border-radius:var(--radius-sm);cursor:pointer;display:inline-flex;align-items:center;justify-content:center;
gap:7px;white-space:nowrap;text-decoration:none;transition:border-color .15s,background .15s,color .15s,transform .05s}
.btn:hover{border-color:#3a4a5a;background:var(--bg3)}
.btn:active{transform:translateY(1px)}
.btn:disabled{opacity:.5;cursor:not-allowed}
.btn:focus-visible,.fname a:focus-visible,.fm-path a:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.btn-primary{background:var(--accent);border-color:var(--accent);color:var(--accent-ink)}
.btn-primary:hover{background:#d4f76b;border-color:#d4f76b}
.btn-danger{border-color:rgba(255,97,97,.5);color:var(--danger)}
.btn-danger:hover{background:var(--danger-dim);border-color:var(--danger)}
.btn-ghost{background:transparent;border-color:transparent;color:var(--text-2)}
.btn-ghost:hover{background:var(--bg2);color:var(--text-1)}
.btn-block{width:100%}
.btn-sm{padding:5px 10px;font-size:12px}

/* forms */
.field{display:block;margin-bottom:14px}
.field-label{display:block;font-size:11.5px;font-weight:600;letter-spacing:.06em;text-transform:uppercase;color:var(--text-2);margin-bottom:6px}
input[type=text],input[type=password],input[type=search],textarea{width:100%;background:var(--bg1);border:1px solid var(--line-strong);
color:var(--text-1);font:inherit;padding:8px 11px;border-radius:var(--radius-sm);outline:none;transition:border-color .15s,box-shadow .15s}
input:focus,textarea:focus{border-color:var(--accent);box-shadow:0 0 0 2px rgba(198,241,78,.15)}
.checkline{display:flex;align-items:center;gap:8px;font-size:13px;color:var(--text-2);margin-bottom:12px}
.checkline input[type=checkbox]{accent-color:var(--accent);width:16px;height:16px}

/* brand */
.wordmark{font-family:var(--mono);font-weight:700;letter-spacing:.32em;font-size:15px;color:var(--accent)}
.wordmark-sub{display:block;font-family:var(--mono);font-size:10px;letter-spacing:.18em;text-transform:uppercase;color:var(--text-3);margin-top:6px}
.login-mark,.brand-mark{background:linear-gradient(var(--bg0),var(--bg0)) padding-box,linear-gradient(135deg,var(--accent),var(--teal)) border-box;
border:1.5px solid transparent;clip-path:polygon(50% 0,96% 22%,96% 62%,50% 100%,4% 62%,4% 22%);position:relative}
.login-mark{width:58px;height:58px;margin:0 auto 22px}
.login-mark::after{content:"";position:absolute;inset:15px 13px;border-top:2.5px solid var(--accent);border-radius:2px;
box-shadow:0 -5px 0 -1px var(--accent),0 -10px 0 -2.5px var(--accent)}
.brand-mark{width:30px;height:30px;flex-shrink:0}
.brand-mark::after{content:"";position:absolute;inset:8px 7px;border-top:1.5px solid var(--accent);border-radius:2px;
box-shadow:0 -3px 0 -.5px var(--accent),0 -6px 0 -1.5px var(--accent)}
.server-pill{display:inline-flex;align-items:center;gap:8px;max-width:100%;font-family:var(--mono);font-size:11px;color:var(--text-2);
background:var(--bg2);border:1px solid var(--line);border-radius:20px;padding:5px 12px}
.server-pill span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dot{width:7px;height:7px;border-radius:50%;background:var(--accent);box-shadow:0 0 8px rgba(198,241,78,.8);flex-shrink:0}

/* login */
.login-screen{min-height:100vh;min-height:100dvh;display:flex;align-items:center;justify-content:center;padding:24px}
.login-wrap{width:100%;max-width:380px;text-align:center}
.login-brand{margin-bottom:26px}
.login-card{text-align:left;background:var(--bg1);border:1px solid var(--line-strong);border-radius:var(--radius);padding:26px;
box-shadow:0 24px 60px rgba(0,0,0,.45)}
.login-host{display:flex;justify-content:center;margin-top:14px}
.login-error{color:var(--danger);font-size:12.5px;margin:0 0 12px}

/* top bar */
.topbar{display:flex;align-items:center;gap:12px;padding:12px 26px;border-bottom:1px solid var(--line);background:rgba(6,8,11,.7);
backdrop-filter:blur(8px);-webkit-backdrop-filter:blur(8px);position:sticky;top:0;z-index:30}
.brand{display:flex;align-items:center;gap:11px;min-width:0}
.brand .wordmark{font-size:14px}
.brand .wordmark-sub{margin-top:3px}
.topbar .server-pill{margin-left:auto;min-width:0}
.view{max-width:1120px;margin:0 auto;padding:22px 26px 60px}

/* upload progress */
.upbar{position:sticky;top:55px;z-index:25;background:var(--bg1);border-bottom:1px solid var(--line);padding:9px 26px}
.meter{height:5px;background:var(--bg3);border-radius:4px;overflow:hidden}
.meter>i{display:block;height:100%;width:0;background:var(--accent);border-radius:4px;transition:width .2s}
.upbar-label{display:flex;justify-content:space-between;gap:10px;font-family:var(--mono);font-size:11px;color:var(--text-2);margin-bottom:5px}
.upbar-label span:first-child{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}

/* toolbar */
.toolbar{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:12px}
.toolbar .spacer{flex:1}
.search{position:relative;flex:1 1 200px;min-width:160px;max-width:320px}
.search svg{position:absolute;left:10px;top:50%;transform:translateY(-50%);color:var(--text-3);pointer-events:none}
.search input{padding-left:32px;font-family:var(--mono);font-size:13px}
.view-toggle{display:inline-flex;border:1px solid var(--line);border-radius:var(--radius-sm);overflow:hidden}
.view-toggle .btn{border-radius:0;border:none}
.view-toggle .btn.active{background:var(--accent-dim);color:var(--accent)}

/* path bar */
.fm-path{display:flex;align-items:center;gap:2px;overflow-x:auto;white-space:nowrap;font-family:var(--mono);font-size:12.5px;margin-bottom:12px;
padding:2px 0;-webkit-overflow-scrolling:touch}
.fm-path a{color:var(--text-2);text-decoration:none;padding:5px 7px;border-radius:5px;display:inline-flex;align-items:center;gap:5px}
.fm-path a:hover{color:var(--accent);background:var(--accent-dim)}
.fm-path .sep{color:var(--text-3);display:inline-flex}
.fm-path .sep svg{width:12px;height:12px}
.fm-path a:last-child{color:var(--text-1)}

/* file list */
.fl-wrap{background:var(--bg1);border:1px solid var(--line);border-radius:var(--radius);overflow:hidden;transition:border-color .12s,background .12s}
.fl-wrap.drag{border:1.5px dashed var(--accent);background:var(--accent-dim)}
.gal-grid.drag{outline:1.5px dashed var(--accent);outline-offset:6px;border-radius:var(--radius)}
.fhead,.frow{display:grid;grid-template-columns:minmax(0,1fr) 92px 172px auto;align-items:center;gap:8px;padding:0 14px}
.fhead{background:var(--bg2);border-bottom:1px solid var(--line);font-size:10.5px;letter-spacing:.1em;text-transform:uppercase;color:var(--text-3);
font-weight:700;padding-top:11px;padding-bottom:11px}
.frow{padding-top:8px;padding-bottom:8px;border-bottom:1px solid var(--line);font-size:13px}
.frow:last-child{border-bottom:none}
.frow:hover{background:rgba(255,255,255,.015)}
.fname{display:flex;align-items:center;gap:10px;min-width:0}
.fname a{color:var(--text-1);text-decoration:none;cursor:pointer;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:500}
.fname a:hover{color:var(--accent)}
.ficon{display:inline-flex;flex-shrink:0}
.ficon svg{width:17px;height:17px;color:var(--text-3)}
.ficon .dir{color:var(--warn)}.ficon .zip{color:var(--accent)}.ficon .php{color:#7f9cf5}.ficon .txt{color:var(--teal)}
.fsize{font-family:var(--mono);font-size:12px;color:var(--text-3)}
.fmod{font-size:12px;color:var(--text-3)}
.row-actions{display:flex;gap:2px;justify-content:flex-end;opacity:.6;transition:opacity .15s}
.frow:hover .row-actions,.row-actions:hover,.row-actions:focus-within{opacity:1}
.row-actions .btn{padding:6px 7px}
.row-actions svg{width:14px;height:14px}
.empty-state{text-align:center;padding:46px 20px;color:var(--text-3)}
.empty-state svg{width:34px;height:34px;opacity:.5;margin-bottom:8px}
.empty-state p{margin:4px 0 0}
.spinner{width:26px;height:26px;border-radius:50%;border:2.5px solid var(--line-strong);border-top-color:var(--accent);
animation:spin .7s linear infinite;margin:40px auto}
@keyframes spin{to{transform:rotate(360deg)}}
.hits .hit{padding:11px 14px;border-bottom:1px solid var(--line);font-family:var(--mono);font-size:12.5px;overflow-wrap:anywhere}
.hits .hit:last-child{border-bottom:none}
.hits .hit a{color:var(--text-1);text-decoration:none;cursor:pointer}
.hits .hit a:hover{color:var(--accent)}

/* gallery */
.gal-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(140px,1fr));gap:12px}
.gal-tile{background:var(--bg1);border:1px solid var(--line);border-radius:var(--radius-sm);overflow:hidden;cursor:pointer;transition:border-color .12s}
.gal-tile:hover{border-color:var(--accent)}
.gal-thumb-wrap{aspect-ratio:1;background:var(--bg2);display:flex;align-items:center;justify-content:center;overflow:hidden}
.gal-thumb{width:100%;height:100%;object-fit:cover;display:block}
.gal-glyph svg{width:34px;height:34px;color:var(--text-3)}
.gal-glyph .dir{color:var(--warn)}
.gal-name{padding:7px 9px;font-size:12.5px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}

/* toasts */
.toasts{position:fixed;right:18px;bottom:18px;z-index:100;display:flex;flex-direction:column;gap:8px;max-width:360px}
.toast{background:var(--bg2);border:1px solid var(--line-strong);border-left:3px solid var(--accent);border-radius:var(--radius-sm);
padding:11px 14px;font-size:13px;box-shadow:0 12px 34px rgba(0,0,0,.5);animation:toastIn .2s ease-out;overflow-wrap:anywhere}
.toast.err{border-left-color:var(--danger)}
@keyframes toastIn{from{opacity:0;transform:translateY(8px)}to{opacity:1;transform:none}}

/* modal */
.modal-backdrop{position:fixed;inset:0;z-index:90;background:rgba(4,6,9,.72);backdrop-filter:blur(3px);-webkit-backdrop-filter:blur(3px);
display:flex;align-items:flex-start;justify-content:center;padding:7vh 20px 20px;overflow-y:auto}
.modal{background:var(--bg1);border:1px solid var(--line-strong);border-radius:var(--radius);width:100%;max-width:560px;
box-shadow:0 30px 90px rgba(0,0,0,.6);animation:viewIn .16s ease-out}
.modal.wide{max-width:820px}
@keyframes viewIn{from{opacity:0;transform:translateY(4px)}to{opacity:1;transform:none}}
.modal-head{display:flex;align-items:center;gap:10px;padding:14px 18px;border-bottom:1px solid var(--line)}
.modal-title{font-weight:650;font-size:14.5px;min-width:0;overflow-wrap:anywhere}
.modal-head .btn{margin-left:auto}
.modal-body{padding:18px}
.modal-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:6px}
.modal-msg{margin:0 0 16px;color:var(--text-2);overflow-wrap:anywhere}
.editor-body{width:100%;height:52vh;background:#080b0f;border:1px solid var(--line-strong);border-radius:var(--radius-sm);color:var(--text-1);
font-family:var(--mono);font-size:12.5px;line-height:1.6;padding:12px;resize:vertical;outline:none;white-space:pre}
.perm-grid{display:grid;gap:6px;margin-bottom:12px}
.perm-row{display:grid;grid-template-columns:70px repeat(3,1fr);align-items:center;gap:8px}
.perm-row.perm-head span{font-size:11px;text-transform:uppercase;letter-spacing:.06em;color:var(--text-3);text-align:center}
.perm-row-label{font-weight:600}
.perm-cell{justify-content:center;margin:0}
.lightbox-modal .modal{max-width:min(92vw,980px)}
.lightbox-stage{display:flex;align-items:center;gap:10px}
.lightbox-media{max-width:100%;max-height:72vh;margin:0 auto;display:block;border-radius:var(--radius-sm);background:#000;min-width:0;min-height:0}
iframe.lightbox-media{width:100%;height:72vh;border:none}
.lightbox-nav{background:var(--bg2);border:1px solid var(--line);border-radius:50%;width:36px;height:36px;padding:0;flex:none}
.lightbox-nav svg{width:16px;height:16px}
.lightbox-foot{margin-top:10px;display:flex;justify-content:space-between;align-items:center;gap:8px;flex-wrap:wrap}

/* ---- mobile ---- */
@media (max-width:700px){
  .fhead{display:none}
  .frow{grid-template-columns:minmax(0,1fr) auto;grid-template-areas:"name name" "size mod" "act act";row-gap:2px;padding:12px 14px}
  .frow .fname{grid-area:name}
  .frow .fsize{grid-area:size}
  .frow .fmod{grid-area:mod;justify-self:end}
  .frow .factions{grid-area:act;margin-top:6px}
  .fname a{white-space:normal;overflow-wrap:anywhere;font-size:14.5px}
  .row-actions{opacity:1;justify-content:flex-start;flex-wrap:wrap;gap:6px}
  .row-actions .btn{min-width:42px;min-height:40px;border-color:var(--line)}
}
@media (max-width:600px){
  body{font-size:15px}
  .topbar{padding:10px 14px}
  .brand .wordmark-sub{display:none}
  .topbar .server-pill{max-width:46vw}
  .view{padding:14px 14px 60px}
  .upbar{padding:8px 14px;top:51px}
  .toolbar .btn{flex:1 1 calc(50% - 8px);min-height:42px}
  .toolbar .btn.tb-primary{flex-basis:100%}
  .toolbar .search{flex:1 1 100%;max-width:none;order:9}
  .toolbar .view-toggle{order:8;margin-left:auto}
  .toolbar .view-toggle .btn{flex:none;min-height:40px;min-width:44px}
  input[type=text],input[type=password],input[type=search],textarea,.editor-body{font-size:16px}
  .logout-label{display:none}
  .toasts{left:12px;right:12px;bottom:12px;max-width:none}
  .modal-backdrop{padding:12px}
  .modal-body{padding:16px}
  .modal-actions .btn{flex:1;min-height:42px}
  .editor-body{height:56vh}
  .perm-row{grid-template-columns:56px repeat(3,1fr);gap:4px}
  .checkline input[type=checkbox]{width:20px;height:20px}
  .gal-grid{grid-template-columns:repeat(auto-fill,minmax(104px,1fr));gap:8px}
  .lightbox-media{max-height:60vh}
  iframe.lightbox-media{height:60vh}
  .lightbox-nav{width:34px;height:34px}
  .login-card{padding:22px}
}
@media (prefers-reduced-motion:reduce){*{animation:none !important;transition:none !important}}
`

// webftpIcons mirrors the panel's icon set (web/js/ui.js) so the two look identical.
const webftpIcons = `
const ICONS={
 folder:'<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>',
 file:'<path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M13 2v7h7"/>',
 archive:'<rect x="2" y="4" width="20" height="5" rx="1"/><path d="M4 9v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9"/><path d="M10 13h4"/>',
 download:'<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/><path d="M12 15V3"/>',
 upload:'<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m17 8-5-5-5 5"/><path d="M12 3v12"/>',
 folderplus:'<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><path d="M12 11v6M9 14h6"/>',
 fileplus:'<path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M13 2v7h7M12 12v6M9 15h6"/>',
 edit:'<path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4z"/>',
 rename:'<path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4z"/>',
 trash:'<path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M10 11v6M14 11v6"/>',
 lock:'<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
 link:'<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>',
 extract:'<path d="M21 8v13H3V8"/><path d="M1 3h22v5H1z"/><path d="M10 12h4"/>',
 search:'<circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/>',
 grid:'<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
 list:'<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
 logout:'<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5M21 12H9"/>',
 x:'<path d="M18 6 6 18M6 6l12 12"/>',
 home:'<path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><path d="M9 22V12h6v10"/>',
 chevron:'<path d="m9 6 6 6-6 6"/>',
 prev:'<path d="m15 6-6 6 6 6"/>'
};
function ic(n,cls){return '<svg class="ico '+(cls||'')+'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+(ICONS[n]||ICONS.file)+'</svg>'}
`

func webftpLoginPage(host, errMsg string) string {
	errHTML := ""
	if errMsg != "" {
		errHTML = `<p class="login-error" role="alert">` + htmlEscape(errMsg) + `</p>`
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#06080b"><title>Web FTP — ` + htmlEscape(host) + `</title><style>` + webftpCSS + `</style></head>
<body><div class="login-screen"><div class="login-wrap">
<div class="login-brand"><div class="login-mark"></div><span class="wordmark">AEGIS</span><span class="wordmark-sub">Web FTP</span>
<div class="login-host"><span class="server-pill"><i class="dot"></i><span>` + htmlEscape(host) + `</span></span></div></div>
<div class="login-card">
` + errHTML + `
<form method="post" action="/login">
<label class="field"><span class="field-label">FTP username</span><input type="text" name="username" autocomplete="username" autocapitalize="off" autocorrect="off" spellcheck="false" autofocus required></label>
<label class="field"><span class="field-label">Password</span><input type="password" name="password" autocomplete="current-password" required></label>
<button class="btn btn-primary btn-block" type="submit">Log in</button>
</form>
</div></div></div></body></html>`
}

func webftpBrowserPage(host string) string {
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#06080b"><title>Web FTP — ` + htmlEscape(host) + `</title><style>` + webftpCSS + `</style></head>
<body>
<header class="topbar">
  <div class="brand"><div class="brand-mark"></div><div><span class="wordmark">AEGIS</span><span class="wordmark-sub">Web FTP</span></div></div>
  <span class="server-pill"><i class="dot"></i><span>` + htmlEscape(host) + `</span></span>
  <form method="post" action="/logout"><button class="btn btn-ghost btn-sm" type="submit" title="Log out" aria-label="Log out"><span class="ic-logout"></span><span class="logout-label">Log out</span></button></form>
</header>
<div id="upbar" class="upbar hidden"><div class="upbar-label"><span id="upbar-text">Uploading…</span><span id="upbar-pct">0%</span></div><div class="meter"><i id="upbar-fill"></i></div></div>
<main class="view">
  <div class="toolbar">
    <label class="btn btn-primary tb-primary" style="cursor:pointer"><span class="ic-upload"></span>Upload<input id="file-input" type="file" multiple style="display:none"></label>
    <button class="btn" id="btn-mkdir"><span class="ic-folderplus"></span>New folder</button>
    <button class="btn" id="btn-newfile"><span class="ic-fileplus"></span>New file</button>
    <button class="btn" id="btn-zip"><span class="ic-archive"></span>Zip folder</button>
    <span class="spacer"></span>
    <div class="view-toggle" role="group" aria-label="View">
      <button class="btn btn-sm active" id="btn-view-list" title="List view" aria-label="List view"><span class="ic-list"></span></button>
      <button class="btn btn-sm" id="btn-view-grid" title="Gallery view" aria-label="Gallery view"><span class="ic-grid"></span></button>
    </div>
    <div class="search"><span class="ic-search"></span><input type="search" id="search-input" placeholder="Search this folder…" aria-label="Search this folder" autocomplete="off"></div>
  </div>
  <nav class="fm-path" id="crumbs" aria-label="Path"></nav>
  <div id="view-list" class="fl-wrap" role="list"></div>
  <div id="view-gallery" class="hidden gal-grid"></div>
  <div id="search-results" class="hidden fl-wrap hits"></div>
</main>
<div id="toasts" class="toasts" aria-live="polite"></div>
<script>
` + webftpIcons + `
let path = "/";
let viewMode = "list";
let lastEntries = [];
const VIDEO_EXTS = ["mp4","mov","mkv","webm","avi","m4v"];
const THUMBABLE_EXTS = ["jpg","jpeg","png","gif","pdf"].concat(VIDEO_EXTS);
const ARCHIVE_EXTS = ["zip","tar","gz","tgz","rar","7z","bz2"];
const TEXT_EXTS = ["txt","md","log","json","css","js","html","htm","xml","ini","conf","yml","yaml","csv","sql","htaccess"];

function esc(s){return String(s).replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]))}
function $(id){return document.getElementById(id)}
function extOf(name){const i=name.lastIndexOf(".");return i<0?"":name.slice(i+1).toLowerCase()}
function fmtSize(n){if(n<1024)return n+" B";const u=["KB","MB","GB","TB"];let i=-1;do{n/=1024;i++}while(n>=1024&&i<u.length-1);return n.toFixed(1)+" "+u[i]}
function fmtDate(s){const d=new Date(s);if(isNaN(d))return "";try{return d.toLocaleString(undefined,{dateStyle:"medium",timeStyle:"short"})}catch(e){return d.toLocaleString()}}
function joinPath(base,name){return (base.replace(/\/$/,"")+"/"+name).replace(/\/+/g,"/")}
function parentOf(p){const parts=p.split("/").filter(Boolean);parts.pop();return "/"+parts.join("/")}
function isThumbable(name){return THUMBABLE_EXTS.includes(extOf(name))}
function mediaKind(name){const ext=extOf(name);if(ext==="pdf")return "pdf";if(VIDEO_EXTS.includes(ext))return "video";return "image"}
function fileIconCls(e){
  if(e.type==="dir")return "dir";
  const ext=extOf(e.name);
  if(ARCHIVE_EXTS.includes(ext))return "zip";
  if(ext==="php")return "php";
  if(TEXT_EXTS.includes(ext))return "txt";
  return "";
}
function fileIcon(e){return ic(e.type==="dir"?"folder":(fileIconCls(e)==="zip"?"archive":"file"),fileIconCls(e))}
function toast(msg,err){
  const t=document.createElement("div");t.className="toast"+(err?" err":"");t.textContent=msg;
  $("toasts").appendChild(t);setTimeout(()=>t.remove(),3800);
}
async function copyText(t){
  try{await navigator.clipboard.writeText(t);toast("Copied to clipboard")}
  catch(e){toast("Could not copy — copy manually: "+t,true)}
}
async function api(url,method,body){
  return fetch(url,{method,headers:body?{"Content-Type":"application/json"}:undefined,body:body?JSON.stringify(body):undefined});
}
async function fail(r){toast((await r.json().catch(()=>({}))).error||"request failed",true)}

// Fill the static icon placeholders in the markup above.
document.querySelectorAll("[class^=ic-]").forEach(el=>{el.outerHTML=ic(el.className.slice(3))});

// --- modal (webftp has no shared UI toolkit — this is its own, styled like the panel's) ---
let modalOnClose=null;
function closeModal(){
  document.querySelectorAll(".modal-backdrop").forEach(e=>e.remove());
  if(modalOnClose){const fn=modalOnClose;modalOnClose=null;fn()}
}
function openModal(title,bodyHTML,opts){
  opts=opts||{};
  closeModal();
  modalOnClose=opts.onClose||null;
  const bd=document.createElement("div");
  bd.className="modal-backdrop"+(opts.lightbox?" lightbox-modal":"");
  bd.innerHTML='<div class="modal'+(opts.wide?" wide":"")+'" role="dialog" aria-modal="true" aria-label="'+esc(title)+'"><div class="modal-head"><div class="modal-title">'+esc(title)+'</div><button class="btn btn-ghost btn-sm m-x" aria-label="Close">'+ic("x")+'</button></div><div class="modal-body">'+bodyHTML+'</div></div>';
  document.body.appendChild(bd);
  bd.querySelector(".m-x").onclick=closeModal;
  bd.addEventListener("mousedown",(e)=>{if(e.target===bd)closeModal()});
  return bd.querySelector(".modal-body");
}
function setModalTitle(t){const h=document.querySelector(".modal-title");if(h)h.textContent=t}
document.addEventListener("keydown",(e)=>{if(e.key==="Escape")closeModal()});

function askText(title,label,value,okText){
  return new Promise((resolve)=>{
    let settled=false;const finish=(v)=>{if(!settled){settled=true;resolve(v)}};
    const body=openModal(title,'<form id="ask-form"><label class="field"><span class="field-label">'+esc(label)+'</span><input type="text" id="ask-input" class="mono" autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck="false" value="'+esc(value||"")+'"></label><div class="modal-actions"><button type="button" class="btn" id="ask-cancel">Cancel</button><button type="submit" class="btn btn-primary">'+esc(okText||"OK")+'</button></div></form>',{onClose:()=>finish(null)});
    const input=body.querySelector("#ask-input");
    input.focus();
    const dot=(value||"").lastIndexOf(".");
    if(dot>0)input.setSelectionRange(0,dot);else input.select();
    body.querySelector("#ask-cancel").onclick=closeModal;
    body.querySelector("#ask-form").onsubmit=(e)=>{e.preventDefault();const v=input.value.trim();finish(v||null);closeModal()};
  });
}
function askConfirm(title,message,okText,danger){
  return new Promise((resolve)=>{
    let settled=false;const finish=(v)=>{if(!settled){settled=true;resolve(v)}};
    const body=openModal(title,'<p class="modal-msg">'+esc(message)+'</p><div class="modal-actions"><button class="btn" id="cf-cancel">Cancel</button><button class="btn '+(danger?"btn-danger":"btn-primary")+'" id="cf-ok">'+esc(okText||"OK")+'</button></div>',{onClose:()=>finish(false)});
    body.querySelector("#cf-cancel").onclick=closeModal;
    body.querySelector("#cf-ok").onclick=()=>{finish(true);closeModal()};
    body.querySelector("#cf-ok").focus();
  });
}

// --- navigation ---
function renderCrumbs(){
  const parts=path.split("/").filter(Boolean);
  let acc="",html='<a href="#" data-path="/" aria-label="Root folder">'+ic("home")+'</a>';
  for(const p of parts){acc+="/"+p;html+='<span class="sep">'+ic("chevron")+'</span><a href="#" data-path="'+esc(acc)+'">'+esc(p)+'</a>'}
  const box=$("crumbs");box.innerHTML=html;
  box.querySelectorAll("a").forEach(a=>a.onclick=(e)=>{e.preventDefault();load(a.dataset.path)});
  box.scrollLeft=box.scrollWidth;
}

async function load(p){
  path=p||"/";
  renderCrumbs();
  $("search-input").value="";
  $("view-list").innerHTML='<div class="spinner"></div>';
  const res=await fetch("/api/list?path="+encodeURIComponent(path));
  if(res.status===401){location.reload();return}
  if(!res.ok){await fail(res);$("view-list").innerHTML="";return}
  lastEntries=await res.json()||[];
  render();
}

function render(){
  $("search-results").classList.add("hidden");
  $("view-list").classList.toggle("hidden",viewMode!=="list");
  $("view-gallery").classList.toggle("hidden",viewMode!=="gallery");
  if(viewMode==="gallery")renderGallery();else renderList();
}

function setView(mode){
  viewMode=mode;
  $("btn-view-list").classList.toggle("active",mode==="list");
  $("btn-view-grid").classList.toggle("active",mode==="gallery");
  render();
}
$("btn-view-list").onclick=()=>setView("list");
$("btn-view-grid").onclick=()=>setView("gallery");

function emptyState(){return '<div class="empty-state">'+ic("folder")+'<p>This folder is empty.</p></div>'}

function rowActions(e){
  const isDir=e.type==="dir";
  const isZip=!isDir&&extOf(e.name)==="zip";
  const b=(attr,icon,title)=>'<button class="btn btn-ghost" '+attr+' title="'+title+'" aria-label="'+title+'">'+ic(icon)+'</button>';
  return (isDir?"":b('data-edit="'+esc(e.path)+'"',"edit","Edit")+
      '<a class="btn btn-ghost" href="/api/download?path='+encodeURIComponent(e.path)+'" title="Download" aria-label="Download">'+ic("download")+'</a>'+
      b('data-copylink="'+esc(e.path)+'"',"link","Copy download link"))+
    (isZip?b('data-extract="'+esc(e.path)+'"',"extract","Extract here"):"")+
    b('data-perm="'+esc(e.path)+'" data-mode="'+esc(e.mode)+'" data-owner="'+esc(e.owner)+'" data-group="'+esc(e.group)+'" data-type="'+esc(e.type)+'"',"lock","Permissions")+
    b('data-rename="'+esc(e.path)+'" data-name="'+esc(e.name)+'"',"rename","Rename")+
    b('data-del="'+esc(e.path)+'" data-name="'+esc(e.name)+'"',"trash","Delete");
}

function renderList(){
  const box=$("view-list");
  if(!lastEntries.length){box.innerHTML=emptyState();return}
  let html='<div class="fhead" role="presentation"><span>Name</span><span>Size</span><span>Modified</span><span></span></div>';
  for(const e of lastEntries){
    const isDir=e.type==="dir";
    html+='<div class="frow" role="listitem"><div class="fname"><span class="ficon">'+fileIcon(e)+'</span>'+
      (isDir?'<a data-nav="'+esc(e.path)+'" tabindex="0" role="link">'+esc(e.name)+'</a>':'<a href="/api/download?path='+encodeURIComponent(e.path)+'">'+esc(e.name)+'</a>')+'</div>'+
      '<div class="fsize">'+(isDir?"—":fmtSize(e.size))+'</div><div class="fmod">'+esc(fmtDate(e.mod_time))+'</div>'+
      '<div class="factions"><div class="row-actions">'+rowActions(e)+'</div></div></div>';
  }
  box.innerHTML=html;
  bindRowActions(box);
}

function renderGallery(){
  const box=$("view-gallery");
  box.innerHTML="";
  if(!lastEntries.length){box.innerHTML=emptyState();return}
  for(const e of lastEntries){
    const div=document.createElement("div");
    div.className="gal-tile";
    const thumbable=e.type!=="dir"&&isThumbable(e.name);
    div.innerHTML='<div class="gal-thumb-wrap">'+
      (thumbable?'<img class="gal-thumb" loading="lazy" alt="">':'<span class="gal-glyph">'+fileIcon(e)+'</span>')+
      '</div><div class="gal-name" title="'+esc(e.name)+'">'+esc(e.name)+'</div>';
    if(thumbable){
      const img=div.querySelector(".gal-thumb");
      img.src="/api/thumb?path="+encodeURIComponent(e.path)+"&size=sm";
      img.onerror=()=>{img.parentNode.innerHTML='<span class="gal-glyph">'+fileIcon(e)+'</span>'};
    }
    div.onclick=()=>{
      if(e.type==="dir")load(e.path);
      else if(thumbable)openLightbox(e.path);
      else window.location="/api/download?path="+encodeURIComponent(e.path);
    };
    box.appendChild(div);
  }
}

function bindRowActions(scope){
  scope.querySelectorAll("[data-nav]").forEach(a=>{
    a.onclick=()=>load(a.dataset.nav);
    a.onkeydown=(ev)=>{if(ev.key==="Enter"||ev.key===" "){ev.preventDefault();load(a.dataset.nav)}};
  });
  scope.querySelectorAll("[data-del]").forEach(b=>b.onclick=async()=>{
    if(!await askConfirm("Delete",'Delete "'+b.dataset.name+'"? This cannot be undone.',"Delete",true))return;
    const r=await api("/api/delete","POST",{path:b.dataset.del});
    if(r.ok){toast("Deleted");load(path)}else await fail(r);
  });
  scope.querySelectorAll("[data-rename]").forEach(b=>b.onclick=async()=>{
    const name=await askText("Rename","New name",b.dataset.name,"Rename");
    if(!name||name===b.dataset.name)return;
    const r=await api("/api/rename","POST",{from:b.dataset.rename,to:joinPath(path,name)});
    if(r.ok){toast("Renamed");load(path)}else await fail(r);
  });
  scope.querySelectorAll("[data-edit]").forEach(b=>b.onclick=()=>openEditor(b.dataset.edit));
  scope.querySelectorAll("[data-perm]").forEach(b=>b.onclick=()=>openPerm(b.dataset));
  scope.querySelectorAll("[data-copylink]").forEach(b=>b.onclick=()=>copyText(location.origin+"/api/download?path="+encodeURIComponent(b.dataset.copylink)));
  scope.querySelectorAll("[data-extract]").forEach(b=>b.onclick=async()=>{
    if(!await askConfirm("Extract",'Extract "'+b.dataset.extract.split("/").pop()+'" into the current folder?',"Extract",false))return;
    const r=await api("/api/unzip","POST",{path:b.dataset.extract,dest:path});
    if(r.ok){toast("Extracted");load(path)}else await fail(r);
  });
}

async function openEditor(target){
  const res=await fetch("/api/read?path="+encodeURIComponent(target));
  if(!res.ok){await fail(res);return}
  const content=await res.text();
  const body=openModal("Edit — "+target.split("/").pop(),
    '<textarea id="editor-body" class="editor-body" spellcheck="false" autocapitalize="off" autocorrect="off" wrap="off">'+esc(content)+'</textarea>'+
    '<div class="modal-actions" style="margin-top:12px"><button class="btn" id="editor-cancel">Cancel</button>'+
    '<button class="btn btn-primary" id="editor-save">Save</button></div>',{wide:true});
  body.querySelector("#editor-cancel").onclick=closeModal;
  body.querySelector("#editor-save").onclick=async()=>{
    const r=await api("/api/write","POST",{path:target,content:body.querySelector("#editor-body").value});
    if(r.ok){toast("Saved");closeModal();load(path)}else await fail(r);
  };
}

function openPerm(ds){
  const target=ds.perm;
  const mode=/^[0-7]{3}$/.test(ds.mode||"")?ds.mode:"644";
  const owner=ds.owner||"",group=ds.group||"",isDir=ds.type==="dir";
  const bits=mode.split("").map(d=>parseInt(d,10));
  const rowsHTML=["Owner","Group","Other"].map((label,r)=>
    '<div class="perm-row"><span class="perm-row-label">'+label+'</span>'+
    [4,2,1].map(bit=>'<label class="checkline perm-cell"><input type="checkbox" data-row="'+r+'" data-bit="'+bit+'" '+((bits[r]&bit)?"checked":"")+'></label>').join("")+'</div>'
  ).join("");
  const body=openModal("Permissions — "+target.split("/").pop(),
    '<div class="perm-grid"><div class="perm-row perm-head"><span></span><span>Read</span><span>Write</span><span>Execute</span></div>'+rowsHTML+'</div>'+
    '<label class="field"><span class="field-label">Mode (octal)</span><input type="text" id="perm-mode" class="mono" inputmode="numeric" value="'+esc(mode)+'"></label>'+
    '<label class="field"><span class="field-label">Owner</span><input type="text" id="perm-owner" class="mono" autocapitalize="off" value="'+esc(owner)+'"></label>'+
    '<label class="field"><span class="field-label">Group</span><input type="text" id="perm-group" class="mono" autocapitalize="off" value="'+esc(group)+'"></label>'+
    (isDir?'<label class="checkline"><input type="checkbox" id="perm-recursive"> Apply recursively to everything inside this folder</label>':"")+
    '<div class="modal-actions"><button class="btn" id="perm-cancel">Cancel</button>'+
    '<button class="btn btn-primary" id="perm-apply">Apply</button></div>');
  const modeInput=body.querySelector("#perm-mode");
  const recalc=()=>{
    let total="";
    for(let r=0;r<3;r++){
      let v=0;
      body.querySelectorAll('input[data-row="'+r+'"]').forEach(cb=>{if(cb.checked)v|=parseInt(cb.dataset.bit,10)});
      total+=v;
    }
    modeInput.value=total;
  };
  body.querySelectorAll(".perm-grid input[type=checkbox]").forEach(cb=>cb.addEventListener("change",recalc));
  modeInput.addEventListener("input",()=>{
    const v=modeInput.value.trim();
    if(!/^[0-7]{3}$/.test(v))return;
    for(let r=0;r<3;r++){
      const rowVal=parseInt(v[r],10);
      body.querySelectorAll('input[data-row="'+r+'"]').forEach(cb=>{cb.checked=!!(rowVal&parseInt(cb.dataset.bit,10))});
    }
  });
  body.querySelector("#perm-cancel").onclick=closeModal;
  body.querySelector("#perm-apply").onclick=async()=>{
    const recursive=!!body.querySelector("#perm-recursive")?.checked;
    const mode2=modeInput.value;
    const owner2=body.querySelector("#perm-owner").value;
    const group2=body.querySelector("#perm-group").value;
    let r=await api("/api/chmod","POST",{path:target,mode:mode2,recursive});
    if(!r.ok){await fail(r);return}
    r=await api("/api/chown","POST",{path:target,owner:owner2,group:group2,recursive});
    if(!r.ok){await fail(r);return}
    toast("Permissions updated");closeModal();load(path);
  };
}

function openLightbox(startPath){
  const media=lastEntries.filter(e=>e.type!=="dir"&&isThumbable(e.name));
  if(!media.length)return;
  let idx=Math.max(0,media.findIndex(e=>e.path===startPath));
  const onKey=(ev)=>{
    if(media.length<2)return;
    if(ev.key==="ArrowLeft"){idx=(idx-1+media.length)%media.length;paint()}
    if(ev.key==="ArrowRight"){idx=(idx+1)%media.length;paint()}
  };
  const body=openModal("",'',{lightbox:true,wide:true,onClose:()=>document.removeEventListener("keydown",onKey)});
  document.addEventListener("keydown",onKey);
  function paint(){
    const e=media[idx];
    setModalTitle(e.name);
    const url="/api/download?path="+encodeURIComponent(e.path);
    const kind=mediaKind(e.name);
    body.innerHTML='<div class="lightbox-stage">'+
      '<button class="btn lightbox-nav" id="lb-prev" aria-label="Previous" '+(media.length<2?"disabled":"")+'>'+ic("prev")+'</button>'+
      (kind==="image"?'<img class="lightbox-media" alt="'+esc(e.name)+'" src="'+url+'">':
        kind==="video"?'<video class="lightbox-media" src="'+url+'" controls autoplay playsinline></video>':
        '<iframe class="lightbox-media" title="pdf" src="'+url+'"></iframe>')+
      '<button class="btn lightbox-nav" id="lb-next" aria-label="Next" '+(media.length<2?"disabled":"")+'>'+ic("chevron")+'</button>'+
      '</div><div class="lightbox-foot small dim"><span>'+(idx+1)+' / '+media.length+'</span><div style="display:flex;gap:8px">'+
      '<button class="btn btn-sm" id="lb-copy">'+ic("link")+'Copy link</button>'+
      '<a class="btn btn-sm btn-primary" href="'+url+'" download>'+ic("download")+'Download</a></div></div>';
    body.querySelector("#lb-copy").onclick=()=>copyText(location.origin+url);
    const prevBtn=body.querySelector("#lb-prev"),nextBtn=body.querySelector("#lb-next");
    if(prevBtn)prevBtn.onclick=()=>{idx=(idx-1+media.length)%media.length;paint()};
    if(nextBtn)nextBtn.onclick=()=>{idx=(idx+1)%media.length;paint()};
  }
  paint();
}

// --- upload (with progress, so big uploads on a phone don't look frozen) ---
function uploadFiles(fileList){
  const files=Array.from(fileList||[]);
  if(!files.length)return;
  const fd=new FormData();
  fd.append("path",path);
  files.forEach(f=>fd.append("files",f));
  const bar=$("upbar"),fill=$("upbar-fill"),pct=$("upbar-pct"),txt=$("upbar-text");
  const label=files.length===1?files[0].name:files.length+" files";
  const done=()=>bar.classList.add("hidden");
  txt.textContent="Uploading "+label;pct.textContent="0%";fill.style.width="0";bar.classList.remove("hidden");
  const xhr=new XMLHttpRequest();
  xhr.open("POST","/api/upload");
  xhr.upload.onprogress=(ev)=>{
    if(!ev.lengthComputable)return;
    const p=Math.round(ev.loaded/ev.total*100);
    fill.style.width=p+"%";pct.textContent=p+"%";
  };
  xhr.onload=()=>{
    done();
    if(xhr.status>=200&&xhr.status<300){toast("Uploaded "+label);load(path)}
    else if(xhr.status===401)location.reload();
    else{let m="upload failed";try{m=JSON.parse(xhr.responseText).error||m}catch(e){}toast(m,true)}
  };
  xhr.onerror=()=>{done();toast("Upload failed — connection error",true)};
  xhr.send(fd);
}

$("btn-mkdir").onclick=async()=>{
  const name=await askText("New folder","Folder name","","Create");
  if(!name)return;
  const r=await api("/api/mkdir","POST",{path:joinPath(path,name)});
  if(r.ok){toast("Folder created");load(path)}else await fail(r);
};
$("btn-newfile").onclick=async()=>{
  const name=await askText("New file","File name","","Create");
  if(!name)return;
  const target=joinPath(path,name);
  const r=await api("/api/write","POST",{path:target,content:""});
  if(r.ok){load(path);openEditor(target)}else await fail(r);
};
$("btn-zip").onclick=async()=>{
  const name=await askText("Zip folder","Archive name (created in the current folder's parent)","archive.zip","Zip");
  if(!name)return;
  const r=await api("/api/zip","POST",{path,name});
  if(r.ok){toast("Zipped");load(path)}else await fail(r);
};
$("file-input").onchange=(e)=>{
  const files=e.target.files;
  if(files.length)uploadFiles(files);
  e.target.value="";
};

// --- search ---
let searchTimer=null;
$("search-input").addEventListener("input",(e)=>{
  clearTimeout(searchTimer);
  const q=e.target.value.trim();
  if(!q){render();return}
  searchTimer=setTimeout(()=>runSearch(q),350);
});
async function runSearch(q){
  const res=await fetch("/api/search?path="+encodeURIComponent(path)+"&q="+encodeURIComponent(q));
  if(!res.ok){await fail(res);return}
  const hits=await res.json();
  $("view-list").classList.add("hidden");
  $("view-gallery").classList.add("hidden");
  const box=$("search-results");
  box.classList.remove("hidden");
  box.innerHTML=(hits&&hits.length)?hits.map(h=>'<div class="hit"><a data-hit="'+esc(h)+'">'+esc(h)+'</a></div>').join(""):'<div class="empty-state"><p>No matches</p></div>';
  box.querySelectorAll("[data-hit]").forEach(a=>a.onclick=()=>load(parentOf("/"+a.dataset.hit)));
}

// --- drag & drop: the file list just gets a dashed highlight while files are
// dragged over the page (no full-screen overlay); dropping anywhere uploads. ---
let dragDepth=0;
const dropTarget=()=>viewMode==="gallery"?$("view-gallery"):$("view-list");
function hasFiles(e){return e.dataTransfer&&Array.from(e.dataTransfer.types||[]).includes("Files")}
function clearDrag(){dragDepth=0;$("view-list").classList.remove("drag");$("view-gallery").classList.remove("drag")}
window.addEventListener("dragenter",(e)=>{if(!hasFiles(e))return;e.preventDefault();dragDepth++;dropTarget().classList.add("drag")});
window.addEventListener("dragover",(e)=>{if(hasFiles(e))e.preventDefault()});
window.addEventListener("dragleave",(e)=>{if(!hasFiles(e))return;dragDepth=Math.max(0,dragDepth-1);if(!dragDepth)clearDrag()});
window.addEventListener("drop",(e)=>{if(!hasFiles(e))return;e.preventDefault();clearDrag();uploadFiles(e.dataTransfer.files)});

load("/");
</script>
</body></html>`
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

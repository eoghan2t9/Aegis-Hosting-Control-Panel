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

const webftpCSS = `
:root{--bg0:#06080b;--bg1:#0b0f14;--bg2:#10151c;--bg3:#161d26;--line:#1e2732;--line-strong:#2a3644;
--text-1:#e8edf2;--text-2:#9aa7b4;--text-3:#5c6a78;--accent:#c6f14e;--accent-ink:#0a0f04;--danger:#ff6161;
--radius:10px;--radius-sm:7px}
*{box-sizing:border-box}
html,body{margin:0;height:100%;background:var(--bg0);color:var(--text-1);
font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Arial,sans-serif;font-size:14px}
.mono{font-family:"SFMono-Regular","JetBrains Mono",ui-monospace,Menlo,Consolas,monospace}
.hidden{display:none}
.wrap{max-width:1080px;margin:0 auto;padding:24px}
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
.search-input{width:240px}
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
.actions{display:flex;gap:8px;margin-bottom:14px;flex-wrap:wrap;align-items:center}
.toast{position:fixed;right:18px;bottom:18px;background:var(--bg2);border:1px solid var(--line-strong);
border-radius:var(--radius-sm);padding:9px 14px;font-size:13px;z-index:60}
.toast.err{border-color:var(--danger);color:var(--danger)}
.ov-backdrop{position:fixed;inset:0;background:rgba(0,0,0,.6);display:flex;align-items:center;justify-content:center;padding:20px;z-index:50}
.ov-card{background:var(--bg1);border:1px solid var(--line-strong);border-radius:var(--radius);padding:18px;max-width:760px;width:100%;max-height:86vh;overflow:auto}
.ov-head{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;gap:10px}
.ov-head h2{font-size:15px;margin:0;overflow-wrap:anywhere}
.editor-body{width:100%;min-height:50vh;background:var(--bg0);border:1px solid var(--line-strong);border-radius:var(--radius-sm);
color:var(--text-1);padding:10px;font-family:inherit;resize:vertical}
.checkline{display:flex;align-items:center;gap:7px;font-size:13px;margin-bottom:10px}
.perm-grid{display:grid;grid-template-columns:70px repeat(3,1fr);gap:6px;margin-bottom:12px;align-items:center}
.perm-head span{font-size:11px;text-transform:uppercase;color:var(--text-3);text-align:center}
.perm-row-label{font-size:13px;color:var(--text-2)}
.perm-cell{justify-content:center;margin:0}
.dropzone{position:fixed;inset:14px;border:2px dashed var(--accent);border-radius:var(--radius);display:flex;
align-items:center;justify-content:center;background:rgba(198,241,78,.08);color:var(--accent);font-weight:600;
z-index:40;pointer-events:none}
.gal-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(120px,1fr));gap:12px}
.gal-tile{background:var(--bg1);border:1px solid var(--line);border-radius:var(--radius-sm);padding:8px;cursor:pointer;text-align:center}
.gal-tile:hover{border-color:var(--accent)}
.gal-thumb-wrap{height:80px;display:flex;align-items:center;justify-content:center;overflow:hidden;margin-bottom:6px}
.gal-thumb{max-width:100%;max-height:100%;border-radius:4px;object-fit:cover}
.gal-thumb-glyph{font-size:34px}
.gal-name{font-size:11.5px;color:var(--text-2);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.lightbox-stage{display:flex;align-items:center;justify-content:center;gap:10px}
.lightbox-media{max-width:100%;max-height:60vh;border-radius:6px}
.lightbox-nav{font-size:20px;padding:4px 10px}
.search-hit{padding:7px 0;border-bottom:1px solid var(--line)}
.search-hit a{color:var(--text-1);text-decoration:none;cursor:pointer}
.search-hit a:hover{color:var(--accent)}
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
  <button class="btn btn-sm" id="btn-newfile">New file</button>
  <label class="btn btn-sm" style="cursor:pointer">Upload<input id="file-input" type="file" multiple style="display:none"></label>
  <button class="btn btn-sm" id="btn-zip">Zip folder</button>
  <button class="btn btn-sm" id="btn-view">Gallery view</button>
  <input type="search" id="search-input" class="search-input mono" placeholder="Search this folder…">
</div>
<div class="crumbs mono" id="crumbs"></div>
<div style="height:10px"></div>
<div id="search-results" class="hidden"></div>
<div id="view-table"><table class="tbl"><thead><tr><th>Name</th><th>Size</th><th>Modified</th><th></th></tr></thead><tbody id="rows"></tbody></table></div>
<div id="view-gallery" class="hidden gal-grid"></div>
<div id="dropzone" class="dropzone hidden">Drop files here to upload</div>
</div>
<script>
let path = "/";
let viewMode = "table";
let lastEntries = [];
const VIDEO_EXTS = ["mp4","mov","mkv","webm","avi","m4v"];
const THUMBABLE_EXTS = ["jpg","jpeg","png","gif","pdf"].concat(VIDEO_EXTS);

function esc(s){return String(s).replace(/[&<>"']/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]))}
function fmtSize(n){if(n<1024)return n+" B";const u=["KB","MB","GB","TB"];let i=-1;do{n/=1024;i++}while(n>=1024&&i<u.length-1);return n.toFixed(1)+" "+u[i]}
function toast(msg,err){const t=document.createElement("div");t.className="toast"+(err?" err":"");t.textContent=msg;document.body.appendChild(t);setTimeout(()=>t.remove(),3500)}
function joinPath(base,name){return (base.replace(/\/$/,"")+"/"+name).replace(/\/+/g,"/")}
function parentOf(p){const parts=p.split("/").filter(Boolean);parts.pop();return "/"+parts.join("/")}
function isThumbable(name){const ext=name.split(".").pop().toLowerCase();return THUMBABLE_EXTS.includes(ext)}
function mediaKind(name){const ext=name.split(".").pop().toLowerCase();if(ext==="pdf")return "pdf";if(VIDEO_EXTS.includes(ext))return "video";return "image"}
async function copyText(t){
  try{await navigator.clipboard.writeText(t);toast("Copied to clipboard")}
  catch(e){toast("Could not copy — copy manually: "+t,true)}
}
async function api(url,method,body){
  return fetch(url,{method,headers:body?{"Content-Type":"application/json"}:undefined,body:body?JSON.stringify(body):undefined});
}
async function fail(r){toast((await r.json().catch(()=>({}))).error||"request failed",true)}

// --- generic overlay (webftp has no shared modal() helper — this is its own,
// self-contained equivalent) --------------------------------------------------
let ovOnClose = null;
function closeOverlay(){
  document.querySelectorAll(".ov-backdrop").forEach(e=>e.remove());
  if(ovOnClose){const fn=ovOnClose;ovOnClose=null;fn()}
}
function overlay(title,bodyHTML,onClose){
  closeOverlay();
  ovOnClose = onClose||null;
  const bd=document.createElement("div");
  bd.className="ov-backdrop";
  bd.innerHTML='<div class="ov-card"><div class="ov-head"><h2>'+esc(title)+'</h2><button class="btn btn-ghost btn-sm ov-close">✕</button></div><div class="ov-body">'+bodyHTML+'</div></div>';
  document.body.appendChild(bd);
  bd.querySelector(".ov-close").onclick=closeOverlay;
  bd.addEventListener("click",(e)=>{if(e.target===bd)closeOverlay()});
  return bd.querySelector(".ov-body");
}
function ovSetTitle(t){const h=document.querySelector(".ov-head h2");if(h)h.textContent=t}

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
  document.getElementById("search-input").value="";
  const res=await fetch("/api/list?path="+encodeURIComponent(path));
  if(res.status===401){location.reload();return}
  if(!res.ok){await fail(res);return}
  lastEntries=await res.json();
  render();
}

function render(){
  document.getElementById("search-results").classList.add("hidden");
  document.getElementById("view-table").classList.toggle("hidden",viewMode!=="table");
  document.getElementById("view-gallery").classList.toggle("hidden",viewMode!=="gallery");
  if(viewMode==="gallery")renderGallery();else renderTable();
}

function setView(mode){
  viewMode=mode;
  document.getElementById("btn-view").textContent=mode==="table"?"Gallery view":"Table view";
  render();
}
document.getElementById("btn-view").onclick=()=>setView(viewMode==="table"?"gallery":"table");

function renderTable(){
  const rows=document.getElementById("rows");
  rows.innerHTML="";
  for(const e of lastEntries){
    const tr=document.createElement("tr");
    const isDir=e.type==="dir";
    const isZip=!isDir&&e.name.toLowerCase().endsWith(".zip");
    tr.innerHTML='<td class="name">'+(isDir?'<a data-nav="'+esc(e.path)+'">📁 '+esc(e.name)+'</a>':'<a href="/api/download?path='+encodeURIComponent(e.path)+'">📄 '+esc(e.name)+'</a>')+
      '</td><td class="dim">'+(isDir?"—":fmtSize(e.size))+'</td><td class="dim small">'+esc(new Date(e.mod_time).toLocaleString())+
      '</td><td><div class="row-actions">'+
      (isDir?"":'<button class="btn btn-ghost btn-sm" data-edit="'+esc(e.path)+'" title="Edit">✎</button>')+
      '<button class="btn btn-ghost btn-sm" data-perm="'+esc(e.path)+'" data-mode="'+esc(e.mode)+'" data-owner="'+esc(e.owner)+'" data-group="'+esc(e.group)+'" data-type="'+e.type+'" title="Permissions">🔒</button>'+
      (isDir?"":'<button class="btn btn-ghost btn-sm" data-copylink="'+esc(e.path)+'" title="Copy download link">🔗</button>')+
      (isZip?'<button class="btn btn-ghost btn-sm" data-extract="'+esc(e.path)+'" title="Extract here">📦</button>':"")+
      '<button class="btn btn-ghost btn-sm" data-rename="'+esc(e.path)+'" data-name="'+esc(e.name)+'" title="Rename">✏️</button>'+
      '<button class="btn btn-ghost btn-sm" data-del="'+esc(e.path)+'" title="Delete">🗑</button>'+
      '</div></td>';
    rows.appendChild(tr);
  }
  bindRowActions(rows);
}

function renderGallery(){
  const box=document.getElementById("view-gallery");
  box.innerHTML="";
  for(const e of lastEntries){
    const div=document.createElement("div");
    div.className="gal-tile";
    const thumbable=e.type!=="dir"&&isThumbable(e.name);
    div.innerHTML='<div class="gal-thumb-wrap">'+
      (e.type==="dir"?'<span class="gal-thumb-glyph">📁</span>':
        thumbable?'<img class="gal-thumb" loading="lazy" alt="">':
        '<span class="gal-thumb-glyph">📄</span>')+
      '</div><div class="gal-name" title="'+esc(e.name)+'">'+esc(e.name)+'</div>';
    if(thumbable){
      const img=div.querySelector(".gal-thumb");
      img.src="/api/thumb?path="+encodeURIComponent(e.path)+"&size=sm";
      img.onerror=()=>{img.outerHTML='<span class="gal-thumb-glyph">📄</span>'};
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
  scope.querySelectorAll("[data-nav]").forEach(a=>a.onclick=()=>load(a.dataset.nav));
  scope.querySelectorAll("[data-del]").forEach(b=>b.onclick=async()=>{
    if(!confirm("Delete this?"))return;
    const r=await api("/api/delete","POST",{path:b.dataset.del});
    if(r.ok){toast("Deleted");load(path)}else await fail(r);
  });
  scope.querySelectorAll("[data-rename]").forEach(b=>b.onclick=async()=>{
    const name=prompt("New name",b.dataset.name);
    if(!name||name===b.dataset.name)return;
    const to=joinPath(path,name);
    const r=await api("/api/rename","POST",{from:b.dataset.rename,to});
    if(r.ok){toast("Renamed");load(path)}else await fail(r);
  });
  scope.querySelectorAll("[data-edit]").forEach(b=>b.onclick=()=>openEditor(b.dataset.edit));
  scope.querySelectorAll("[data-perm]").forEach(b=>b.onclick=()=>openPerm(b.dataset));
  scope.querySelectorAll("[data-copylink]").forEach(b=>b.onclick=()=>copyText(location.origin+"/api/download?path="+encodeURIComponent(b.dataset.copylink)));
  scope.querySelectorAll("[data-extract]").forEach(b=>b.onclick=async()=>{
    if(!confirm('Extract "'+b.dataset.extract+'" into the current folder?'))return;
    const r=await api("/api/unzip","POST",{path:b.dataset.extract,dest:path});
    if(r.ok){toast("Extracted");load(path)}else await fail(r);
  });
}

async function openEditor(target){
  const res=await fetch("/api/read?path="+encodeURIComponent(target));
  if(!res.ok){await fail(res);return}
  const content=await res.text();
  const body=overlay("Edit — "+target.split("/").pop(),
    '<textarea id="editor-body" class="mono editor-body">'+esc(content)+'</textarea>'+
    '<div style="margin-top:10px;display:flex;justify-content:flex-end;gap:8px">'+
    '<button class="btn" id="editor-cancel">Cancel</button>'+
    '<button class="btn btn-primary" id="editor-save">Save</button></div>');
  body.querySelector("#editor-cancel").onclick=closeOverlay;
  body.querySelector("#editor-save").onclick=async()=>{
    const r=await api("/api/write","POST",{path:target,content:body.querySelector("#editor-body").value});
    if(r.ok){toast("Saved");closeOverlay();load(path)}else await fail(r);
  };
}

function openPerm(ds){
  const target=ds.perm;
  const mode=/^[0-7]{3}$/.test(ds.mode||"")?ds.mode:"644";
  const owner=ds.owner||"",group=ds.group||"",isDir=ds.type==="dir";
  const bits=mode.split("").map(d=>parseInt(d,10));
  const rowsHTML=["Owner","Group","Other"].map((label,r)=>
    '<div class="perm-row-label" style="grid-column:1">'+label+'</div>'+
    [4,2,1].map(bit=>'<label class="checkline perm-cell"><input type="checkbox" data-row="'+r+'" data-bit="'+bit+'" '+((bits[r]&bit)?"checked":"")+'></label>').join("")
  ).join("");
  const body=overlay("Permissions — "+target.split("/").pop(),
    '<div class="perm-grid"><div></div><span>Read</span><span>Write</span><span>Execute</span>'+rowsHTML+'</div>'+
    '<label class="field"><span class="field-label">Mode (octal)</span><input id="perm-mode" class="mono" value="'+esc(mode)+'"></label>'+
    '<label class="field"><span class="field-label">Owner</span><input id="perm-owner" class="mono" value="'+esc(owner)+'"></label>'+
    '<label class="field"><span class="field-label">Group</span><input id="perm-group" class="mono" value="'+esc(group)+'"></label>'+
    (isDir?'<label class="checkline"><input type="checkbox" id="perm-recursive"> Apply recursively to everything inside this folder</label>':"")+
    '<div style="margin-top:6px;display:flex;justify-content:flex-end;gap:8px">'+
    '<button class="btn" id="perm-cancel">Cancel</button>'+
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
  body.querySelector("#perm-cancel").onclick=closeOverlay;
  body.querySelector("#perm-apply").onclick=async()=>{
    const recursive=!!body.querySelector("#perm-recursive")?.checked;
    const mode2=modeInput.value;
    const owner2=body.querySelector("#perm-owner").value;
    const group2=body.querySelector("#perm-group").value;
    let r=await api("/api/chmod","POST",{path:target,mode:mode2,recursive});
    if(!r.ok){await fail(r);return}
    r=await api("/api/chown","POST",{path:target,owner:owner2,group:group2,recursive});
    if(!r.ok){await fail(r);return}
    toast("Permissions updated");closeOverlay();load(path);
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
  const body=overlay("",'',()=>document.removeEventListener("keydown",onKey));
  document.addEventListener("keydown",onKey);
  function paint(){
    const e=media[idx];
    ovSetTitle(e.name);
    const url="/api/download?path="+encodeURIComponent(e.path);
    const kind=mediaKind(e.name);
    body.innerHTML='<div class="lightbox-stage">'+
      '<button class="btn btn-ghost lightbox-nav" id="lb-prev" '+(media.length<2?"disabled":"")+'>‹</button>'+
      (kind==="image"?'<img class="lightbox-media" alt="" src="'+url+'">':
        kind==="video"?'<video class="lightbox-media" src="'+url+'" controls autoplay></video>':
        '<iframe class="lightbox-media" title="pdf" src="'+url+'"></iframe>')+
      '<button class="btn btn-ghost lightbox-nav" id="lb-next" '+(media.length<2?"disabled":"")+'>›</button>'+
      '</div><div class="small dim" style="margin-top:8px;display:flex;justify-content:space-between;align-items:center">'+
      '<span>'+(idx+1)+' / '+media.length+'</span><div style="display:flex;gap:8px">'+
      '<button class="btn btn-sm" id="lb-copy">Copy link</button>'+
      '<a class="btn btn-sm btn-primary" href="'+url+'" download>Download</a></div></div>';
    body.querySelector("#lb-copy").onclick=()=>copyText(location.origin+url);
    const prevBtn=body.querySelector("#lb-prev"),nextBtn=body.querySelector("#lb-next");
    if(prevBtn)prevBtn.onclick=()=>{idx=(idx-1+media.length)%media.length;paint()};
    if(nextBtn)nextBtn.onclick=()=>{idx=(idx+1)%media.length;paint()};
  }
  paint();
}

async function uploadFiles(fileList){
  const fd=new FormData();
  fd.append("path",path);
  for(const f of fileList)fd.append("files",f);
  const r=await fetch("/api/upload",{method:"POST",body:fd});
  if(r.ok){toast("Uploaded");load(path)}else await fail(r);
}

document.getElementById("btn-mkdir").onclick=async()=>{
  const name=prompt("Folder name");
  if(!name)return;
  const r=await api("/api/mkdir","POST",{path:joinPath(path,name)});
  if(r.ok){toast("Folder created");load(path)}else await fail(r);
};
document.getElementById("btn-newfile").onclick=async()=>{
  const name=prompt("File name");
  if(!name)return;
  const target=joinPath(path,name);
  const r=await api("/api/write","POST",{path:target,content:""});
  if(r.ok)openEditor(target);else await fail(r);
};
document.getElementById("btn-zip").onclick=async()=>{
  const name=prompt("Archive name (created in the current folder's parent)","archive.zip");
  if(!name)return;
  const r=await api("/api/zip","POST",{path,name});
  if(r.ok){toast("Zipped");load(path)}else await fail(r);
};
document.getElementById("file-input").onchange=(e)=>{
  const files=e.target.files;
  if(files.length)uploadFiles(files);
  e.target.value="";
};

let searchTimer=null;
document.getElementById("search-input").addEventListener("input",(e)=>{
  clearTimeout(searchTimer);
  const q=e.target.value.trim();
  if(!q){render();return}
  searchTimer=setTimeout(()=>runSearch(q),350);
});
async function runSearch(q){
  const res=await fetch("/api/search?path="+encodeURIComponent(path)+"&q="+encodeURIComponent(q));
  if(!res.ok){await fail(res);return}
  const hits=await res.json();
  document.getElementById("view-table").classList.add("hidden");
  document.getElementById("view-gallery").classList.add("hidden");
  const box=document.getElementById("search-results");
  box.classList.remove("hidden");
  box.innerHTML=(hits&&hits.length)?hits.map(h=>'<div class="search-hit"><a data-hit="'+esc(h)+'">'+esc(h)+'</a></div>').join(""):'<p class="dim small">No matches</p>';
  box.querySelectorAll("[data-hit]").forEach(a=>a.onclick=()=>load(parentOf("/"+a.dataset.hit)));
}

const dz=document.getElementById("dropzone");
["dragover","dragenter"].forEach(ev=>window.addEventListener(ev,(e)=>{e.preventDefault();if(e.dataTransfer.types.includes("Files"))dz.classList.remove("hidden")}));
["dragleave","drop"].forEach(ev=>window.addEventListener(ev,(e)=>e.preventDefault()));
window.addEventListener("drop",(e)=>{
  dz.classList.add("hidden");
  if(!e.dataTransfer||!e.dataTransfer.files||!e.dataTransfer.files.length)return;
  uploadFiles(e.dataTransfer.files);
});

load("/");
</script>
</body></html>`
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

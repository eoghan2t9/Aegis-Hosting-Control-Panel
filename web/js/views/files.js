import { addRoute, isAdmin } from "../app.js";
import { api, qs } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, pageHead, loading, fmtBytes, fmtAgo, modal, debounce } from "../ui.js";

let currentPath = "";

addRoute("/files", {
  title: "File manager",
  icon: "folder",
  group: "Data",
  order: 1,
  render: async (view) => {
    view.innerHTML = pageHead("File manager", "Full file access inside your account — create, edit, rename, upload, set permissions, zip and more. You never leave your home directory.");
    view.insertAdjacentHTML("beforeend", `<div id="fm">${loading()}</div>`);
    await list();
  },
});

function pagerPath(p) {
  return p && p !== "/" ? p : "";
}

async function list() {
  const root = document.getElementById("fm");
  root.innerHTML = `
    <div class="card">
      <div class="fm-toolbar">
        <button class="btn btn-sm" id="fm-up" title="Parent">${icon("folder")} ..</button>
        <div class="fm-path" id="fm-path"></div>
        <span class="spacer"></span>
        <button class="btn btn-sm" id="fm-refresh" title="Refresh">${icon("refresh")}</button>
      </div>
      <div class="fm-toolbar">
        <input type="search" id="fm-search" placeholder="Search files…" style="width:180px">
        <span class="spacer"></span>
        <button class="btn btn-sm" id="fm-newfile">${icon("file")} New file</button>
        <button class="btn btn-sm" id="fm-newdir">${icon("folder")} New folder</button>
        <button class="btn btn-sm" id="fm-upload">${icon("upload")} Upload</button>
        <button class="btn btn-sm" id="fm-zip">${icon("archive")} Zip folder</button>
      </div>
      <div class="dropzone hidden" id="fm-drop">Drop files to upload into the current folder</div>
      <div id="fm-list">${loading()}</div>
    </div>`;

  const entries = await api.get("/files" + qs({ path: currentPath }));
  const segments = (currentPath || "").split("/").filter(Boolean);
  const pathEl = document.getElementById("fm-path");
  pathEl.innerHTML = `<a data-p="">home</a>`;
  let acc = "";
  segments.forEach((s) => {
    acc += "/" + s;
    pathEl.insertAdjacentHTML("beforeend", `<span class="sep">/</span><a data-p="${esc(acc)}">${esc(s)}</a>`);
  });
  pathEl.querySelectorAll("a").forEach((a) => a.onclick = async () => { currentPath = a.dataset.p; await list(); });

  document.getElementById("fm-up").onclick = () => {
    const idx = currentPath.lastIndexOf("/");
    currentPath = idx <= 0 ? "" : currentPath.slice(0, idx);
    list();
  };
  document.getElementById("fm-refresh").onclick = () => list();

  // Search.
  const search = debounce(async () => {
    const q = document.getElementById("fm-search").value.trim();
    if (!q) return list();
    try {
      const hits = await api.post("/files/search", { path: currentPath, query: q });
      renderSearch(hits);
    } catch (ex) { toast(ex.message, "err"); }
  }, 350);
  document.getElementById("fm-search").addEventListener("input", search);
  document.getElementById("fm-search").addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); search(); } });

  document.getElementById("fm-newfile").onclick = () => fileDialog();
  document.getElementById("fm-newdir").onclick = () => dirDialog();
  document.getElementById("fm-upload").onclick = () => uploadPicker();
  document.getElementById("fm-zip").onclick = () => zipDialog();

  // Drag & drop upload.
  const drop = document.getElementById("fm-drop");
  const dz = drop;
  ["dragover", "dragenter"].forEach((ev) => window.addEventListener(ev, (e) => { e.preventDefault(); if (e.dataTransfer.types.includes("Files")) dz.classList.remove("hidden"); }));
  ["dragleave", "drop"].forEach((ev) => window.addEventListener(ev, (e) => e.preventDefault()));
  window.addEventListener("drop", (e) => {
    dz.classList.add("hidden");
    if (!e.dataTransfer?.files?.length) return;
    uploadFiles(e.dataTransfer.files);
  });

  renderList(entries);
}

function renderList(entries) {
  const box = document.getElementById("fm-list");
  if (!entries.length) {
    box.innerHTML = `<div class="empty-state"><span class="glyph">▤</span><p>This folder is empty.</p></div>`;
    return;
  }
  box.innerHTML = `<div class="tbl-wrap"><table class="tbl">
    <thead><tr><th>Name</th><th>Size</th><th>Mode</th><th>Owner</th><th>Modified</th><th></th></tr></thead>
    <tbody>${entries.map((e) => `
      <tr data-path="${esc(e.path)}" data-type="${e.type}">
        <td><div class="fm-name-cell"><span class="file-ico ${e.type === "dir" ? "dir" : fileCls(e.name)}">${e.type === "dir" ? "▸" : fileGlyph(e.name)}</span>
          <b>${esc(e.name)}</b>${e.type === "symlink" ? '<span class="tag small">link</span>' : ""}</div></td>
        <td class="num small">${e.type === "dir" ? "—" : fmtBytes(e.size)}</td>
        <td class="mono small dim">${e.mode}</td>
        <td class="small dim">${esc(e.owner)}:${esc(e.group)}</td>
        <td class="small dim">${fmtAgo(e.mod_time)}</td>
        <td><div class="row-actions">
          ${e.type === "file" ? `<button class="btn btn-ghost act-edit" title="Edit">${icon("edit")}</button><button class="btn btn-ghost act-dl" title="Download">${icon("download")}</button>` : ""}
          <button class="btn btn-ghost act-perm" title="Permissions">${icon("lock")}</button>
          <button class="btn btn-ghost act-ren" title="Rename">${icon("edit")}</button>
          <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
        </div></td>
      </tr>`).join("")}</tbody></table></div>`;

  box.querySelectorAll("tr[data-type='dir']").forEach((tr) => {
    tr.classList.add("hoverable");
    tr.querySelector("td:first-child")?.addEventListener("dblclick", () => { currentPath = tr.dataset.path; list(); });
    tr.addEventListener("click", (e) => { if (e.target.closest(".row-actions")) return; currentPath = tr.dataset.path; list(); });
  });
  box.querySelectorAll(".act-edit").forEach((b) => b.onclick = () => editFile(b.closest("tr").dataset.path));
  box.querySelectorAll(".act-dl").forEach((b) => b.onclick = () => { location.href = "/api/files/download" + qs({ path: b.closest("tr").dataset.path }); });
  box.querySelectorAll(".act-perm").forEach((b) => b.onclick = () => permDialog(b.closest("tr")));
  box.querySelectorAll(".act-ren").forEach((b) => b.onclick = () => renameDialog(b.closest("tr").dataset.path));
  box.querySelectorAll(".act-del").forEach((b) => b.onclick = async () => {
    const p = b.closest("tr").dataset.path;
    if (!await confirmDialog(`Delete ${p}?`, { danger: true, title: "Delete" })) return;
    try { await api.post("/files/delete", { path: p }); toast("Deleted"); list(); } catch (ex) { toast(ex.message, "err"); }
  });
}

function renderSearch(hits) {
  const box = document.getElementById("fm-list");
  if (!hits.length) { box.innerHTML = `<div class="empty-state"><p>No matches.</p></div>`; return; }
  box.innerHTML = `<div class="card"><b class="small" style="letter-spacing:.1em;text-transform:uppercase;color:var(--text-3)">${hits.length} match(es)</b>
    <ul style="list-style:none;margin:10px 0 0;padding:0">${hits.map((h) => `
      <li style="padding:6px 0;border-bottom:1px solid var(--line);display:flex;align-items:center;gap:8px">
        <span class="file-ico">▤</span><a class="mono" style="color:var(--accent);cursor:pointer;text-decoration:none;word-break:break-all" data-open="${esc(h)}">${esc(h)}</a></li>`).join("")}</ul></div>`;
  box.querySelectorAll("[data-open]").forEach((a) => a.onclick = async () => {
    const p = a.dataset.open;
    // Navigate to the parent folder.
    currentPath = p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "";
    await list();
  });
}

function fileCls(name) {
  const ext = name.split(".").pop().toLowerCase();
  return ["php"].includes(ext) ? "php" : ["zip", "gz", "tar"].includes(ext) ? "zip" : "";
}
function fileGlyph(name) {
  const ext = name.split(".").pop().toLowerCase();
  if (["png", "jpg", "jpeg", "gif", "svg", "webp", "ico"].includes(ext)) return "🖼";
  if (["zip", "gz", "tar", "tgz"].includes(ext)) return "📦";
  if (["php"].includes(ext)) return "🐘";
  if (["html", "htm", "css", "js", "json", "md", "txt", "conf", "log", "sh", "py", "go"].includes(ext)) return "≡";
  return "·";
}

function joinPath(p) { return currentPath ? currentPath.replace(/\/$/, "") + "/" + p : p; }

function fileDialog() {
  promptDialog("New file", [
    { name: "name", label: "Filename", required: true, mono: true, value: "index.html" },
  ]).then(async (vals) => {
    if (!vals) return;
    const p = joinPath(vals.name);
    try { await api.post("/files/write", { path: p, content: "" }); toast("Created"); editFile(p); } catch (ex) { toast(ex.message, "err"); }
  });
}

function dirDialog() {
  promptDialog("New folder", [
    { name: "name", label: "Folder name", required: true, mono: true },
  ]).then(async (vals) => {
    if (!vals) return;
    try { await api.post("/files/mkdir", { path: joinPath(vals.name), mode: "755" }); toast("Created"); list(); } catch (ex) { toast(ex.message, "err"); }
  });
}

async function editFile(p) {
  try {
    const res = await api.get("/files/content" + qs({ path: p }), true);
    const content = await res.text();
    const name = p.split("/").pop();
    const m = modal({
      title: name,
      wide: true,
      body: `<div class="editor-head" style="margin-bottom:8px">
        <span class="small dim mono" style="flex:1;word-break:break-all">${esc(p)}</span>
        <span class="tag">UTF-8 · text</span></div>
        <textarea class="editor-body" spellcheck="false">${esc(content)}</textarea>`,
      actions: [],
    });
    const ta = m.bodyEl.querySelector("textarea");
    const foot = m.wrap.querySelector(".modal-foot");
    if (!foot) {
      const f = document.createElement("div");
      f.className = "modal-foot";
      m.wrap.appendChild(f);
    }
    const save = btn("Save", "btn btn-primary");
    save.onclick = async () => {
      save.classList.add("btn-busy");
      try {
        await api.post("/files/write", { path: p, content: ta.value });
        toast("Saved " + name);
        save.classList.remove("btn-busy");
      } catch (ex) { toast(ex.message, "err"); save.classList.remove("btn-busy"); }
    };
    const cancel = btn("Close");
    cancel.onclick = () => m.close();
    m.wrap.querySelector(".modal-foot").append(cancel, save);
  } catch (ex) {
    // Binary or too large: offer download.
    if (ex.status === 400) { toast(ex.message, "warn"); }
    else toast(ex.message, "err");
  }
}

async function renameDialog(p) {
  const name = p.split("/").pop();
  const vals = await promptDialog("Rename", [{ name: "name", label: "New name", required: true, mono: true, value: name }]);
  if (!vals) return;
  const parent = p.includes("/") ? p.slice(0, p.lastIndexOf("/")) : "";
  const dest = (parent ? parent + "/" : "") + vals.name;
  try { await api.post("/files/rename", { from: p, to: dest }); toast("Renamed"); list(); } catch (ex) { toast(ex.message, "err"); }
}

function permDialog(tr) {
  const path = tr.dataset.path;
  const mode = tr.querySelector("td:nth-child(3)")?.textContent || "755";
  const [owner, group] = (tr.querySelector("td:nth-child(4)")?.textContent || ":").split(":");
  const m = modal({
    title: "Permissions — " + path.split("/").pop(),
    body: `<div class="grid grid-2">
      <div><label class="field"><span class="field-label">Mode (octal)</span><input id="perm-mode" class="mono" value="${esc(mode)}" placeholder="755"></label></div>
      <div><label class="field"><span class="field-label">Owner</span><input id="perm-owner" class="mono" value="${esc(owner || "")}"></label></div>
      <div><label class="field"><span class="field-label">Group</span><input id="perm-group" class="mono" value="${esc(group || "")}"></label></div>
    </div>
    <p class="small dim">0755 dirs · 0644 files · 0600 secrets. Apply to the item only (not recursive).</p>`,
    actions: [],
  });
  const save = btn("Apply", "btn btn-primary");
  save.onclick = async () => {
    try {
      await api.post("/files/chmod", { path, mode: document.getElementById("perm-mode").value });
      await api.post("/files/chown", { path, owner: document.getElementById("perm-owner").value, group: document.getElementById("perm-group").value });
      toast("Permissions updated");
      m.close();
      list();
    } catch (ex) { toast(ex.message, "err"); }
  };
  const cancel = btn("Cancel");
  cancel.onclick = () => m.close();
  m.wrap.querySelector(".modal-foot").append(cancel, save);
}

function zipDialog() {
  promptDialog("Zip current folder", [
    { name: "name", label: "Archive name", value: "archive.zip", mono: true },
  ]).then(async (vals) => {
    if (!vals) return;
    try {
      await api.post("/files/zip", { path: currentPath || "/", name: vals.name });
      toast("Archive created");
      list();
    } catch (ex) { toast(ex.message, "err"); }
  });
}

function uploadPicker() {
  const inp = document.createElement("input");
  inp.type = "file";
  inp.multiple = true;
  inp.onchange = () => uploadFiles(inp.files);
  inp.click();
}

async function uploadFiles(files) {
  const fd = new FormData();
  fd.append("path", currentPath || "/");
  for (const f of files) fd.append("files", f);
  toast(`Uploading ${files.length} file(s)…`);
  try {
    const res = await api.request("POST", "/files/upload", fd);
    toast("Upload complete");
    list();
  } catch (ex) { toast(ex.message, "err"); }
}

function btn(text, cls) {
  const b = document.createElement("button");
  b.className = cls || "btn";
  b.textContent = text;
  return b;
}

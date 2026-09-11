import { addRoute, isAdmin } from "../app.js";
import { api, qs } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, pageHead, loading, fmtBytes, fmtAgo, modal, debounce } from "../ui.js";

let currentPath = "";
let lastEntries = [];
let viewMode = "list";

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

// Every browser-loaded media URL (<img>/<video>/<iframe> src, plain
// download links) bypasses api.js's fetch wrapper, so it never gets the
// Authorization header — the token must ride along in the query string,
// same as the WebSocket views already do.
function downloadUrl(path) {
  return "/api/files/download" + qs({ path, token: api.token });
}
function thumbUrl(path, size) {
  return "/api/files/thumb" + qs({ path, size, token: api.token });
}

const VIDEO_EXTS = ["mp4", "mov", "mkv", "webm", "avi", "m4v"];
const THUMBABLE_EXTS = ["jpg", "jpeg", "png", "gif", "pdf", ...VIDEO_EXTS];

function isThumbable(name) {
  const ext = name.split(".").pop().toLowerCase();
  return THUMBABLE_EXTS.includes(ext);
}
function mediaKind(name) {
  const ext = name.split(".").pop().toLowerCase();
  if (ext === "pdf") return "pdf";
  if (VIDEO_EXTS.includes(ext)) return "video";
  return "image";
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
        <div class="view-toggle" id="fm-viewtoggle">
          <button class="btn btn-sm view-btn" id="fm-view-list" title="List view">${icon("list")}</button>
          <button class="btn btn-sm view-btn" id="fm-view-gallery" title="Gallery view">${icon("grid")}</button>
        </div>
      </div>
      <div class="dropzone hidden" id="fm-drop">Drop files to upload into the current folder</div>
      <div id="fm-list">${loading()}</div>
    </div>`;

  const entries = await api.get("/files" + qs({ path: currentPath }));
  lastEntries = entries;
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

  document.getElementById("fm-view-list").onclick = () => { viewMode = "list"; renderCurrent(); };
  document.getElementById("fm-view-gallery").onclick = () => { viewMode = "gallery"; renderCurrent(); };
  updateViewToggle();

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

  renderCurrent();
}

function updateViewToggle() {
  const listBtn = document.getElementById("fm-view-list");
  const galBtn = document.getElementById("fm-view-gallery");
  if (!listBtn || !galBtn) return;
  listBtn.classList.toggle("active", viewMode === "list");
  galBtn.classList.toggle("active", viewMode === "gallery");
}

function renderCurrent() {
  updateViewToggle();
  if (viewMode === "gallery") renderGallery(lastEntries);
  else renderList(lastEntries);
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
      <tr data-path="${esc(e.path)}" data-type="${e.type}" data-name="${esc(e.name)}" data-mode="${esc(e.mode)}" data-owner="${esc(e.owner)}" data-group="${esc(e.group)}">
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
  box.querySelectorAll("tr[data-type='file']").forEach((tr) => {
    if (!isThumbable(tr.dataset.name)) return;
    tr.classList.add("hoverable");
    tr.addEventListener("dblclick", (e) => { if (e.target.closest(".row-actions")) return; openLightbox(tr.dataset.path); });
  });
  bindItemActions(box, "tr");
}

function renderGallery(entries) {
  const box = document.getElementById("fm-list");
  if (!entries.length) {
    box.innerHTML = `<div class="empty-state"><span class="glyph">▤</span><p>This folder is empty.</p></div>`;
    return;
  }
  box.innerHTML = `<div class="gal-grid">${entries.map((e) => `
    <div class="gal-tile" tabindex="0" data-path="${esc(e.path)}" data-type="${e.type}" data-name="${esc(e.name)}" data-mode="${esc(e.mode)}" data-owner="${esc(e.owner)}" data-group="${esc(e.group)}">
      <div class="gal-thumb-wrap">
        ${e.type === "dir"
          ? `<span class="gal-thumb-glyph">▸</span>`
          : isThumbable(e.name)
            ? `<img class="gal-thumb" loading="lazy" alt="" src="${thumbUrl(e.path, "sm")}">`
            : `<span class="gal-thumb-glyph">${fileGlyph(e.name)}</span>`}
      </div>
      <div class="gal-name">${esc(e.name)}</div>
      <div class="gal-meta small dim">${e.type === "dir" ? "—" : fmtBytes(e.size)}</div>
      <div class="gal-actions">
        ${e.type === "file" ? `<button class="btn btn-ghost btn-xs act-edit" title="Edit">${icon("edit")}</button><button class="btn btn-ghost btn-xs act-dl" title="Download">${icon("download")}</button>` : ""}
        <button class="btn btn-ghost btn-xs act-perm" title="Permissions">${icon("lock")}</button>
        <button class="btn btn-ghost btn-xs act-ren" title="Rename">${icon("edit")}</button>
        <button class="btn btn-ghost btn-xs act-del" title="Delete">${icon("trash")}</button>
      </div>
    </div>`).join("")}</div>`;

  // A thumbnail request that 204s or errors falls back to the glyph tile —
  // <img> onerror fires for both, so there's no separate "no content" path.
  box.querySelectorAll(".gal-thumb").forEach((img) => {
    img.addEventListener("error", () => {
      const wrap = img.closest(".gal-thumb-wrap");
      const name = img.closest(".gal-tile").dataset.name;
      if (wrap) wrap.innerHTML = `<span class="gal-thumb-glyph">${fileGlyph(name)}</span>`;
    }, { once: true });
  });

  box.querySelectorAll(".gal-tile").forEach((tile) => {
    tile.addEventListener("click", (e) => {
      if (e.target.closest(".gal-actions")) return;
      if (tile.dataset.type === "dir") { currentPath = tile.dataset.path; list(); return; }
      if (isThumbable(tile.dataset.name)) openLightbox(tile.dataset.path);
    });
  });
  bindItemActions(box, ".gal-tile");
}

// Shared row/tile action bindings for both the table and gallery views —
// both render items with the same .act-* buttons and data-path/data-type.
function bindItemActions(box, itemSel) {
  box.querySelectorAll(".act-edit").forEach((b) => b.onclick = (e) => { e.stopPropagation(); editFile(b.closest(itemSel).dataset.path); });
  box.querySelectorAll(".act-dl").forEach((b) => b.onclick = (e) => { e.stopPropagation(); location.href = downloadUrl(b.closest(itemSel).dataset.path); });
  box.querySelectorAll(".act-perm").forEach((b) => b.onclick = (e) => { e.stopPropagation(); permDialog(b.closest(itemSel)); });
  box.querySelectorAll(".act-ren").forEach((b) => b.onclick = (e) => { e.stopPropagation(); renameDialog(b.closest(itemSel).dataset.path); });
  box.querySelectorAll(".act-del").forEach((b) => b.onclick = async (e) => {
    e.stopPropagation();
    const p = b.closest(itemSel).dataset.path;
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
  if (VIDEO_EXTS.includes(ext)) return "🎞";
  if (ext === "pdf") return "📄";
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
      body: `<div>
        <div class="editor-head" style="margin-bottom:8px">
          <span class="small dim mono" style="flex:1;word-break:break-all">${esc(p)}</span>
          <span class="tag">UTF-8 · text</span></div>
        <textarea class="editor-body" spellcheck="false">${esc(content)}</textarea>
      </div>`,
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

// Recursive rwx-checkbox permission grid. `el` is either a <tr> (list view)
// or a .gal-tile (gallery view) — both carry the same data-* attributes.
function permDialog(el) {
  const path = el.dataset.path;
  const mode = /^[0-7]{3}$/.test(el.dataset.mode || "") ? el.dataset.mode : "755";
  const owner = el.dataset.owner || "";
  const group = el.dataset.group || "";
  const isDir = el.dataset.type === "dir";
  const bits = mode.split("").map((d) => parseInt(d, 10));
  const cols = [{ bit: 4, label: "Read" }, { bit: 2, label: "Write" }, { bit: 1, label: "Execute" }];
  const rowLabels = ["Owner", "Group", "Other"];

  const grid = `<div class="perm-grid">
    <div class="perm-row perm-head"><span></span>${cols.map((c) => `<span>${c.label}</span>`).join("")}</div>
    ${rowLabels.map((label, r) => `
      <div class="perm-row">
        <span class="perm-row-label">${label}</span>
        ${cols.map((c) => `<label class="checkline perm-cell"><input type="checkbox" data-row="${r}" data-bit="${c.bit}" ${(bits[r] & c.bit) ? "checked" : ""}></label>`).join("")}
      </div>`).join("")}
  </div>`;

  const m = modal({
    title: "Permissions — " + path.split("/").pop(),
    body: `<div>
      ${grid}
      <div class="grid grid-2" style="margin-top:10px">
        <div><label class="field"><span class="field-label">Mode (octal)</span><input id="perm-mode" class="mono" value="${esc(mode)}" placeholder="755"></label></div>
        <div><label class="field"><span class="field-label">Owner</span><input id="perm-owner" class="mono" value="${esc(owner)}"></label></div>
        <div><label class="field"><span class="field-label">Group</span><input id="perm-group" class="mono" value="${esc(group)}"></label></div>
      </div>
      ${isDir ? `
      <label class="checkline" style="margin-top:10px"><input type="checkbox" id="perm-recursive"> Apply recursively to everything inside this folder</label>
      <p class="small dim" id="perm-recursive-warn" hidden>Recursive mode sets the exact same mode on every file and subfolder inside — files that shouldn't be executable will also gain or lose the execute bit.</p>` : ""}
    </div>`,
    actions: [],
  });

  const modeInput = m.bodyEl.querySelector("#perm-mode");
  const recalcMode = () => {
    let total = "";
    for (let r = 0; r < 3; r++) {
      let v = 0;
      m.bodyEl.querySelectorAll(`input[data-row="${r}"]`).forEach((cb) => { if (cb.checked) v |= parseInt(cb.dataset.bit, 10); });
      total += v;
    }
    modeInput.value = total;
  };
  m.bodyEl.querySelectorAll(".perm-grid input[type=checkbox]").forEach((cb) => cb.addEventListener("change", recalcMode));
  modeInput.addEventListener("input", () => {
    const v = modeInput.value.trim();
    if (!/^[0-7]{3}$/.test(v)) return;
    for (let r = 0; r < 3; r++) {
      const rowVal = parseInt(v[r], 10);
      m.bodyEl.querySelectorAll(`input[data-row="${r}"]`).forEach((cb) => { cb.checked = !!(rowVal & parseInt(cb.dataset.bit, 10)); });
    }
  });

  const recursiveEl = m.bodyEl.querySelector("#perm-recursive");
  const recursiveWarn = m.bodyEl.querySelector("#perm-recursive-warn");
  recursiveEl?.addEventListener("change", () => { recursiveWarn.hidden = !recursiveEl.checked; });

  const save = btn("Apply", "btn btn-primary");
  save.onclick = async () => {
    const recursive = !!recursiveEl?.checked;
    try {
      await api.post("/files/chmod", { path, mode: modeInput.value, recursive });
      await api.post("/files/chown", {
        path,
        owner: m.bodyEl.querySelector("#perm-owner").value,
        group: m.bodyEl.querySelector("#perm-group").value,
        recursive,
      });
      toast("Permissions updated");
      m.close();
      list();
    } catch (ex) { toast(ex.message, "err"); }
  };
  const cancel = btn("Cancel");
  cancel.onclick = () => m.close();
  m.wrap.querySelector(".modal-foot").append(cancel, save);
}

// Image/video/PDF preview with prev/next navigation across every
// previewable entry in the current folder.
function openLightbox(startPath) {
  const media = lastEntries.filter((e) => e.type !== "dir" && isThumbable(e.name));
  if (!media.length) return;
  let idx = Math.max(0, media.findIndex((e) => e.path === startPath));
  let onKey;
  const m = modal({
    title: media[idx].name,
    wide: true,
    body: `<div></div>`,
    onClose: () => document.removeEventListener("keydown", onKey),
  });
  m.wrap.classList.add("lightbox-modal");

  const render = () => {
    const e = media[idx];
    m.setTitle(e.name);
    const url = downloadUrl(e.path);
    const kind = mediaKind(e.name);
    m.bodyEl.innerHTML = `
      <div class="lightbox-stage">
        <button class="lightbox-nav prev" title="Previous" ${media.length < 2 ? "disabled" : ""}>${icon("play")}</button>
        ${kind === "image" ? `<img class="lightbox-media" alt="${esc(e.name)}" src="${url}">`
          : kind === "video" ? `<video class="lightbox-media" src="${url}" controls autoplay></video>`
          : `<iframe class="lightbox-media lightbox-pdf" title="${esc(e.name)}" src="${url}"></iframe>`}
        <button class="lightbox-nav next" title="Next" ${media.length < 2 ? "disabled" : ""}>${icon("play")}</button>
      </div>
      <div class="small dim" style="margin-top:8px;display:flex;justify-content:space-between;align-items:center">
        <span>${idx + 1} / ${media.length}</span>
        <a href="${url}" download="${esc(e.name)}">${icon("download")} Download</a>
      </div>`;
    const prevBtn = m.bodyEl.querySelector(".prev");
    const nextBtn = m.bodyEl.querySelector(".next");
    if (prevBtn) prevBtn.onclick = () => { idx = (idx - 1 + media.length) % media.length; render(); };
    if (nextBtn) nextBtn.onclick = () => { idx = (idx + 1) % media.length; render(); };
  };
  onKey = (ev) => {
    if (media.length < 2) return;
    if (ev.key === "ArrowLeft") { idx = (idx - 1 + media.length) % media.length; render(); }
    if (ev.key === "ArrowRight") { idx = (idx + 1) % media.length; render(); }
  };
  document.addEventListener("keydown", onKey);
  render();
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

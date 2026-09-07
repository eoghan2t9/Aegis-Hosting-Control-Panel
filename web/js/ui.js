// Shared UI toolkit: icons, formatting, toasts, modals, small charts.
// Every view renders into #view and uses these helpers.

/* ------------------------------------------------------------------ icons */
const paths = {
  grid: '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
  globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.6 4 5.7 4 9s-1.5 6.4-4 9c-2.5-2.6-4-5.7-4-9s1.5-6.4 4-9z"/>',
  activity: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
  shield: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
  user: '<path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>',
  users: '<path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/>',
  lock: '<rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/>',
  database: '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>',
  folder: '<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>',
  file: '<path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M13 2v7h7"/>',
  archive: '<rect x="2" y="4" width="20" height="5" rx="1"/><path d="M4 9v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9"/><path d="M10 13h4"/>',
  terminal: '<path d="m4 17 6-6-6-6"/><path d="M12 19h8"/>',
  server: '<rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><path d="M6 6h.01M6 18h.01"/>',
  settings: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>',
  gauge: '<path d="M12 15l3.5-3.5"/><path d="M20.3 18a10 10 0 1 0-16.6 0"/>',
  download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/><path d="M12 15V3"/>',
  upload: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m17 8-5-5-5 5"/><path d="M12 3v12"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  edit: '<path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.12 2.12 0 0 1 3 3L12 15l-4 1 1-4z"/>',
  trash: '<path d="M3 6h18"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6"/><path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><path d="M10 11v6M14 11v6"/>',
  key: '<path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0 3 3L22 7l-3-3m-3.5 3.5L19 4"/>',
  refresh: '<path d="M23 4v6h-6M1 20v-6h6"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/>',
  x: '<path d="M18 6 6 18M6 6l12 12"/>',
  check: '<path d="M20 6 9 17l-5-5"/>',
  eye: '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>',
  cloud: '<path d="M17.5 19a4.5 4.5 0 1 0-.42-8.98 6 6 0 1 0-11.57 2.02A3.5 3.5 0 0 0 6.5 19z"/>',
  list: '<path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01"/>',
  cpu: '<rect x="4" y="4" width="16" height="16" rx="2"/><rect x="9" y="9" width="6" height="6"/><path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 14h3M1 9h3M1 14h3"/>',
  log: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M16 13H8M16 17H8M10 9H8"/>',
  zap: '<path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/>',
  clock: '<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>',
  home: '<path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><path d="M9 22V12h6v10"/>',
  toggle: '<rect x="1" y="5" width="22" height="14" rx="7"/><circle cx="16" cy="12" r="3"/>',
  logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5M21 12H9"/>',
  copy: '<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>',
  mail: '<rect x="2" y="4" width="20" height="16" rx="2"/><path d="m2 7 10 6 10-6"/>',
  send: '<path d="m22 2-7 20-4-9-9-4z"/><path d="M22 2 11 13"/>',
  play: '<path d="m5 3 14 9-14 9z"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/>',
  wrench: '<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/>',
  dollar: '<path d="M12 1v22M17 5H9.5a3.5 3.5 0 0 0 0 7h5a3.5 3.5 0 0 1 0 7H6"/>',
  ssl: '<path d="M12 2 20 6v6c0 5-3.5 8.5-8 10-4.5-1.5-8-5-8-10V6z"/><path d="m9 12 2 2 4-4"/>',
};

export function icon(name, cls) {
  const p = paths[name] || paths.grid;
  return `<svg class="${cls || ""}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${p}</svg>`;
}

/* -------------------------------------------------------------- formatting */
export function fmtBytes(n) {
  if (n === null || n === undefined || isNaN(n)) return "—";
  if (n === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  const v = n / Math.pow(1024, i);
  return (v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2)) + " " + units[i];
}
export function fmtNum(n) {
  if (n === null || n === undefined) return "—";
  return n.toLocaleString();
}
export function fmtPct(n) { return (Math.round(n * 10) / 10) + "%"; }
export function fmtDate(s) {
  if (!s) return "—";
  const d = new Date(s);
  return d.toLocaleString(undefined, { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}
export function fmtAgo(s) {
  if (!s) return "—";
  const sec = (Date.now() - new Date(s).getTime()) / 1000;
  if (sec < 45) return "just now";
  if (sec < 3600) return Math.round(sec / 60) + "m ago";
  if (sec < 86400) return Math.round(sec / 3600) + "h ago";
  return Math.round(sec / 86400) + "d ago";
}
export function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
export function statusTag(status) {
  const s = String(status || "").toLowerCase();
  const cls = { active: "tag-lime", issued: "tag-lime", ok: "tag-lime", running: "tag-lime",
    suspended: "tag-amber", pending: "tag-amber", renewing: "tag-teal", failed: "tag-red", error: "tag-red", missing: "tag-red" }[s] || "";
  return `<span class="tag ${cls}">${esc(status)}</span>`;
}

/* -------------------------------------------------------------- DOM helpers */
export function h(html) {
  const t = document.createElement("template");
  t.innerHTML = html.trim();
  return t.content.firstElementChild;
}
export function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }
export function find(node, sel) { return node.querySelector(sel); }

/* -------------------------------------------------------------- toasts */
export function toast(msg, kind = "ok", ms = 3800) {
  const host = document.getElementById("toasts");
  const el = h(`<div class="toast ${kind === "ok" ? "" : kind}">${icon(kind === "ok" ? "check" : kind === "warn" ? "clock" : "x")} <span>${esc(msg)}</span></div>`);
  host.appendChild(el);
  setTimeout(() => { el.style.opacity = "0"; el.style.transition = "opacity .25s"; setTimeout(() => el.remove(), 260); }, ms);
}

/* -------------------------------------------------------------- modal */
export function modal({ title, body, wide, actions, onClose }) {
  const root = document.getElementById("modal-root");
  const wrap = h(`<div class="modal-backdrop">
    <div class="modal ${wide ? "wide" : ""}" role="dialog" aria-modal="true">
      <div class="modal-head"><span class="modal-title">${esc(title)}</span>
        <button class="m-close" aria-label="Close">×</button></div>
      <div class="modal-body"></div>
      ${actions ? '<div class="modal-foot"></div>' : ""}
    </div></div>`);
  const bodyEl = find(wrap, ".modal-body");
  bodyEl.appendChild(typeof body === "string" ? h(body) : body);
  const close = () => { root.removeChild(wrap); if (onClose) onClose(); };
  find(wrap, ".m-close").onclick = close;
  wrap.addEventListener("mousedown", (e) => { if (e.target === wrap) close(); });
  if (actions) {
    const foot = find(wrap, ".modal-foot");
    actions.forEach((a) => foot.appendChild(a));
  }
  root.appendChild(wrap);
  return { wrap, bodyEl, close, setTitle: (t) => { find(wrap, ".modal-title").textContent = t; } };
}

export function confirmDialog(message, opts = {}) {
  return new Promise((resolve) => {
    const ok = h(`<button class="btn ${opts.danger ? "btn-danger" : "btn-primary"}">${esc(opts.okText || "Confirm")}</button>`);
    const cancel = h(`<button class="btn">${esc(opts.cancelText || "Cancel")}</button>`);
    const m = modal({
      title: opts.title || "Are you sure?",
      body: `<p style="margin:0;color:var(--text-2)">${esc(message)}</p>`,
      actions: [cancel, ok],
    });
    const done = (v) => { m.close(); resolve(v); };
    ok.onclick = () => done(true);
    cancel.onclick = () => done(false);
  });
}

export function promptDialog(title, fields, opts = {}) {
  // fields: [{name,label,type,value,placeholder,required,options,help,mono}]
  return new Promise((resolve) => {
    const body = h(`<div></div>`);
    const vals = {};
    for (const f of fields) {
      const fld = h(`<label class="field"><span class="field-label">${esc(f.label)}${f.required ? "" : " <span class='dim'>(optional)</span>"}</span></label>`);
      if (f.type === "select") {
        const sel = h(`<select name="${esc(f.name)}"></select>`);
        for (const o of (f.options || [])) {
          sel.appendChild(h(`<option value="${esc(o.value)}" ${String(o.value) === String(f.value ?? "") ? "selected" : ""}>${esc(o.label)}</option>`));
        }
        fld.appendChild(sel);
      } else {
        const inp = h(`<input name="${esc(f.name)}" type="${f.type || "text"}" ${f.required ? "required" : ""}
          value="${esc(f.value ?? "")}" placeholder="${esc(f.placeholder || "")}" ${f.mono ? 'class="mono"' : ""} spellcheck="false">`);
        fld.appendChild(inp);
      }
      if (f.help) fld.appendChild(h(`<div class="small dim" style="margin-top:4px">${esc(f.help)}</div>`));
      body.appendChild(fld);
    }
    const ok = h(`<button class="btn btn-primary">${esc(opts.okText || "Save")}</button>`);
    const cancel = h(`<button class="btn">Cancel</button>`);
    const m = modal({ title, body, actions: [cancel, ok], wide: opts.wide });
    const done = (v) => { m.close(); resolve(v); };
    ok.onclick = () => {
      const out = {};
      for (const f of fields) {
        const el = find(body, `[name="${f.name}"]`);
        if (!el) continue;
        const v = el.value;
        if (f.required && !v.trim()) { toast(f.label + " is required", "warn"); return; }
        if (f.type === "number") out[f.name] = f.name === "priority" ? parseInt(v || "0", 10) : parseFloat(v || "0");
        else out[f.name] = v;
      }
      done(out);
    };
    cancel.onclick = () => done(null);
    // enter submits
    body.addEventListener("keydown", (e) => { if (e.key === "Enter" && e.target.tagName === "INPUT") ok.click(); });
    setTimeout(() => { const first = find(body, "input,select"); if (first) first.focus(); }, 60);
  });
}

/* -------------------------------------------------------------- charts */
export function sparkline(data, opts = {}) {
  const w = opts.width || 280, hpx = opts.height || 56, pad = 2;
  if (!data || data.length < 2) return `<svg class="chart" viewBox="0 0 ${w} ${hpx}"></svg>`;
  const max = opts.max || Math.max(...data), min = opts.min || Math.min(...data);
  const span = (max - min) || 1;
  const pts = data.map((v, i) => {
    const x = pad + (i / (data.length - 1)) * (w - pad * 2);
    const y = hpx - pad - ((v - min) / span) * (hpx - pad * 2);
    return [x, y];
  });
  const line = pts.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  const area = `${line} L${w - pad},${hpx} L${pad},${hpx} Z`;
  const last = pts[pts.length - 1];
  const gid = "sg" + Math.random().toString(36).slice(2, 8);
  return `<svg class="chart" viewBox="0 0 ${w} ${hpx}" preserveAspectRatio="none">
    <defs><linearGradient id="${gid}" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0%" stop-color="${opts.color || "var(--accent)"}" stop-opacity=".18"/>
      <stop offset="100%" stop-color="${opts.color || "var(--accent)"}" stop-opacity="0"/>
    </linearGradient></defs>
    <path d="${area}" fill="url(#${gid})"/>
    <path d="${line}" class="chart-line ${opts.extraCls || ""}" vector-effect="non-scaling-stroke"/>
    ${opts.dot ? `<circle cx="${last[0]}" cy="${last[1]}" r="2.4" fill="${opts.color || "var(--accent)"}"/>` : ""}
  </svg>`;
}

export function ring(pct, size = 76) {
  const r = (size - 8) / 2, c = 2 * Math.PI * r;
  const off = c - (Math.min(100, Math.max(0, pct)) / 100) * c;
  return `<svg width="${size}" height="${size}" viewBox="0 0 ${size} ${size}" style="transform:rotate(-90deg)">
    <circle cx="${size / 2}" cy="${size / 2}" r="${r}" fill="none" stroke="var(--bg3)" stroke-width="6"/>
    <circle cx="${size / 2}" cy="${size / 2}" r="${r}" fill="none" stroke="var(--accent)" stroke-width="6"
      stroke-dasharray="${c}" stroke-dashoffset="${off}" stroke-linecap="round"/></svg>`;
}

export function loading() { return `<div class="spinner"></div>`; }

export function pageHead(title, sub, actionsHtml) {
  return `<div class="page-head"><div><h2>${esc(title)}</h2><p>${esc(sub)}</p></div>
    <div class="page-actions">${actionsHtml || ""}</div></div>`;
}

export function copyText(text) {
  navigator.clipboard?.writeText(text).then(() => toast("Copied to clipboard"), () => toast("Copy failed", "err"));
}

export function debounce(fn, ms = 250) {
  let t; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); };
}

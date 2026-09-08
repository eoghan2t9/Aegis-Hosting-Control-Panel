import { addRoute, isAdmin, me, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, modal, confirmDialog, promptDialog, statusTag, fmtAgo, pageHead, loading } from "../ui.js";

addRoute("/domains", {
  title: "Domains",
  icon: "globe",
  group: "Web",
  render: async (view) => {
    view.innerHTML = pageHead("Domains", "Websites attached to your account. Each domain gets its own document root, PHP version and web server config.", `
      <button class="btn btn-primary" id="btn-add-domain">${icon("plus")} Add domain</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="domain-list">${loading()}</div>`);

    const [domains, phpInfo, wsInfo] = await Promise.all([
      api.get("/domains"),
      api.get("/php/versions").catch(() => ({ versions: [] })),
      api.get("/webserver").catch(() => ({ active: "nginx", available: [] })),
    ]);
    const listEl = document.getElementById("domain-list");
    const phpVersions = phpInfo.versions?.map((v) => v.version) || [];

    if (!domains.length) {
      listEl.innerHTML = `<div class="card empty-state"><span class="glyph">◈</span><p>No websites yet. Add your first domain and Aegis will provision the document root, PHP-FPM pool and ${esc(wsInfo.active || "web server")} vhost automatically.</p>
        <button class="btn btn-primary" id="btn-add-domain2">${icon("plus")} Add domain</button></div>`;
      listEl.querySelector("#btn-add-domain2").onclick = () => openCreate(phpVersions, wsInfo.available, refresh);
      return;
    }
    listEl.innerHTML = `<div class="tbl-wrap"><table class="tbl">
      <thead><tr><th>Domain</th><th>PHP</th><th>Web server</th><th>SSL</th><th>Created</th><th></th></tr></thead>
      <tbody>${domains.map((d) => `
        <tr class="hoverable" data-id="${d.id}">
          <td><b class="mono">${esc(d.domain)}</b><div class="small dim">${esc(d.document_root)}</div></td>
          <td>${d.php_version ? `<span class="tag">php ${esc(d.php_version)}</span>` : '<span class="dim small">static</span>'}</td>
          <td><span class="tag">${esc(d.webserver)}</span></td>
          <td>${d.ssl_enabled ? '<span class="tag tag-lime">' + icon("ssl", "") + ' secured</span>' : '<span class="tag">none</span>'}</td>
          <td class="small dim">${fmtAgo(d.created_at)}</td>
          <td><div class="row-actions">
            <button class="btn btn-ghost act-open" title="Manage">${icon("settings")}</button>
            <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
          </div></td>
        </tr>`).join("")}</tbody></table></div>`;

    listEl.querySelectorAll("tr[data-id]").forEach((tr) => {
      tr.querySelector(".act-open")?.addEventListener("click", () => openDetail(+tr.dataset.id));
      tr.querySelector(".act-del")?.addEventListener("click", (e) => { e.stopPropagation(); del(tr.dataset.id); });
    });

    document.getElementById("btn-add-domain").onclick = () => openCreate(phpVersions, wsInfo.available);
  },
});

async function del(id) {
  const ok = await confirmDialog("Delete this domain? Its web config and PHP pool are removed; files stay on disk.", { danger: true, title: "Delete domain", okText: "Delete" });
  if (!ok) return;
  try {
    await api.del("/domains/" + id);
    toast("Domain deleted");
    refresh();
  } catch (ex) { toast(ex.message, "err"); }
}

function openCreate(phpVersions, webServers) {
  const phpOptions = [{ value: "", label: "No PHP (static site)" }].concat(phpVersions.map((v) => ({ value: v, label: "PHP " + v })));
  const wsOptions = (webServers.length ? webServers : ["go"]).map((s) => ({ value: s, label: s }));
  const fields = [
    { name: "domain", label: "Domain name", placeholder: "example.com", required: true, mono: true },
    { name: "php_version", label: "PHP version", type: "select", options: phpOptions },
    { name: "webserver", label: "Web server", type: "select", options: wsOptions },
  ];
  if (isAdmin()) fields.splice(0, 0, { name: "user_id", label: "Owner username", placeholder: "leave blank for self" });
  promptDialog("Add a domain", fields).then(async (vals) => {
    if (!vals) return;
    try {
      const owner = me();
      let uid = 0;
      if (isAdmin() && vals.user_id) {
        const users = await api.get("/users");
        const hit = users.find((u) => u.username === vals.user_id || String(u.id) === vals.user_id);
        if (!hit) { toast("Owner not found", "err"); return; }
        uid = hit.id;
      }
      const created = await api.post("/domains", {
        domain: vals.domain, php_version: vals.php_version || "", webserver: vals.webserver || "",
        ...(uid ? { user_id: uid } : {}),
      });
      toast(`Domain ${created.domain} is live`);
      refresh();
    } catch (ex) { toast(ex.message, "err"); }
  });
}

function openDetail(id, onChanged) {
  api.get("/domains/" + id).then((d) => {
    const dom = d.domain;
    const m = modal({
      title: dom.domain,
      wide: true,
      body: `<div id="detail-body">${loading()}</div>`,
      actions: [mkAction("Apply config", "btn", () => applyConfig(dom)), mkAction("Close", "btn")],
    });
    const body = document.getElementById("detail-body");
    const phpInfo = [];
    api.get("/php/versions").then((p) => {
      const opts = ["", ...(p.versions || []).map((v) => v.version)];
      body.innerHTML = `
        <div class="split">
          <div>
            <dl class="kv">
              <dt>document root</dt><dd class="mono">${esc(dom.document_root)}</dd>
              <dt>webserver</dt><dd><span class="tag">${esc(dom.webserver)}</span></dd>
              <dt>ssl</dt><dd>${dom.ssl_enabled ? statusTag("issued") : '<span class="tag">off</span>'} ${dom.ssl_enabled ? '<span class="small dim mono">' + esc(dom.ssl_provider || "") + "</span>" : ""}</dd>
              <dt>created</dt><dd class="small">${fmtAgo(dom.created_at)}</dd>
            </dl>
            <div style="height:14px"></div>
            <b class="small" style="text-transform:uppercase;letter-spacing:.1em;color:var(--text-3)">PHP version</b>
            <div style="display:flex;gap:8px;margin-top:8px">
              <select id="dd-php" style="flex:1">${opts.map((v) => `<option value="${esc(v)}" ${v === dom.php_version ? "selected" : ""}>${v ? "PHP " + v : "No PHP (static)"}</option>`).join("")}</select>
              <button class="btn btn-primary" id="dd-apply-php">Apply</button>
            </div>
          </div>
          <div>
            <b class="small" style="text-transform:uppercase;letter-spacing:.1em;color:var(--text-3)">Aliases (additional domains)</b>
            <ul id="alias-list" style="margin:10px 0 0;padding:0;list-style:none">
              ${(d.aliases || []).map((a) => `<li style="display:flex;align-items:center;gap:8px;padding:5px 0;border-bottom:1px solid var(--line)"><span class="mono" style="flex:1">${esc(a)}</span><button class="btn btn-ghost btn-xs alias-del" data-a="${esc(a)}">${icon("x")}</button></li>`).join("") || '<li class="dim small">No aliases</li>'}
            </ul>
            <div style="display:flex;gap:8px;margin-top:10px">
              <input type="text" id="alias-new" placeholder="www.example.net" class="mono" style="flex:1">
              <button class="btn" id="alias-add">${icon("plus")} Add</button>
            </div>
            <div style="height:14px"></div>
            <b class="small" style="text-transform:uppercase;letter-spacing:.1em;color:var(--text-3)">DNS zone</b>
            <div style="margin-top:8px">${d.zone ? `<span class="tag tag-teal">zone on ${esc(d.zone.provider)}</span> <button class="btn btn-sm" id="dd-dns">Open DNS</button>` : '<span class="dim small">No DNS zone. Manage it under DNS.</span>'}</div>
          </div>
        </div>
        <div style="height:16px"></div>
        <b class="small" style="text-transform:uppercase;letter-spacing:.1em;color:var(--text-3)">Web app installer</b>
        <div id="dd-apps" style="margin-top:8px;display:flex;gap:8px;align-items:center">
          <button class="btn btn-sm" id="dd-install-wp">Install WordPress</button>
          <button class="btn btn-sm" id="dd-install-laravel">Install Laravel</button>
          <button class="btn btn-sm" id="dd-wpcli">Run WP-CLI command</button>
        </div>
        <div style="height:16px"></div>
        ${sslBlock(dom, id)}`;
      document.getElementById("dd-install-wp").onclick = () => installApp(dom, id, "wordpress", m);
      document.getElementById("dd-install-laravel").onclick = () => installApp(dom, id, "laravel", m);
      document.getElementById("dd-wpcli").onclick = () => wpCliDialog(id);
      document.getElementById("dd-apply-php").onclick = async () => {
        const ver = document.getElementById("dd-php").value;
        try {
          await api.patch("/domains/" + id, { php_version: ver });
          toast("PHP pool updated and web server reloaded");
          m.close(); refresh();
        } catch (ex) { toast(ex.message, "err"); }
      };
      document.getElementById("alias-add").onclick = async () => {
        const a = document.getElementById("alias-new").value.trim();
        if (!a) return;
        try { await api.post(`/domains/${id}/aliases`, { alias: a }); m.close(); refresh(); } catch (ex) { toast(ex.message, "err"); }
      };
      body.querySelectorAll(".alias-del").forEach((b) => b.onclick = async () => {
        await api.del(`/domains/${id}/aliases/${encodeURIComponent(b.dataset.a)}`).catch(() => {});
        m.close(); refresh();
      });
      const dnsBtn = document.getElementById("dd-dns");
      if (dnsBtn) dnsBtn.onclick = () => { m.close(); location.hash = "#/dns"; };
    });
  }).catch((ex) => toast(ex.message, "err"));
}

async function installApp(dom, id, app, m) {
  const label = app === "wordpress" ? "WordPress" : "Laravel";
  if (!await confirmDialog(`Install ${label} into ${dom.document_root}? The document root must be empty.`, { title: "Install " + label, okText: "Install" })) return;
  try {
    toast(`Installing ${label}… this can take a minute`);
    await api.post(`/domains/${id}/install`, { app });
    toast(`${label} installed`);
    m.close();
  } catch (ex) { toast(ex.message, "err"); }
}

async function wpCliDialog(id) {
  const vals = await promptDialog("Run WP-CLI command", [
    { name: "args", label: "Command", mono: true, required: true, placeholder: "plugin list", help: "Without the leading 'wp' — e.g. \"plugin list\" or \"core update\"." },
  ]);
  if (!vals) return;
  try {
    const res = await api.post(`/domains/${id}/wp-cli`, { args: vals.args.split(/\s+/).filter(Boolean) });
    modal({
      title: "WP-CLI output",
      wide: true,
      body: `<pre class="mono small" style="white-space:pre-wrap;max-height:50vh;overflow:auto">${esc(res.output || "(no output)")}</pre>`,
      actions: [mkAction("Close", "btn")],
    });
  } catch (ex) { toast(ex.message, "err"); }
}

function sslBlock(dom, id) {
  const certNote = dom.ssl_enabled
    ? `<p class="small dim" style="margin:8px 0">Certificate active. <a href="#/ssl">View in SSL section →</a></p>`
    : `<p class="small dim" style="margin:8px 0">Issue a Let's Encrypt certificate for ${esc(dom.domain)}. HTTP-01 needs the domain pointing at this server on port 80; DNS-01 uses your Cloudflare provider.</p>`;
  return `<div style="border-top:1px solid var(--line);padding-top:14px">
    <b class="small" style="text-transform:uppercase;letter-spacing:.1em;color:var(--text-3)">TLS certificate</b>
    <div style="display:flex;gap:8px;margin-top:10px;flex-wrap:wrap">
      <button class="btn" id="ssl-http">${icon("ssl")} Issue Let's Encrypt (HTTP)</button>
      <button class="btn" id="ssl-dns">Issue Let's Encrypt (DNS-01)</button>
      <button class="btn btn-ghost" id="ssl-self">Self-signed</button>
    </div>${certNote}</div>`;
}

function mkAction(text, cls, fn) {
  const b = document.createElement("button");
  b.className = "btn " + cls;
  b.textContent = text;
  if (fn) b.onclick = fn;
  return b;
}

async function applyConfig(dom) {
  try { await api.post(`/domains/${dom.id}/apply`); toast("Configuration applied"); }
  catch (ex) { toast(ex.message, "err"); }
}

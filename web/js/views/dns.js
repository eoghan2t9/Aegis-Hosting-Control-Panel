import { addRoute, isAdmin, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, statusTag, fmtAgo, pageHead, loading } from "../ui.js";

let selectedZone = null;

addRoute("/dns", {
  title: "DNS",
  icon: "cloud",
  group: "Websites",
  order: 1,
  render: async (view) => {
    selectedZone = null;
    view.innerHTML = pageHead("DNS", "Authoritative DNS per domain. Zones can be served by the built-in name server (local) or synced to Cloudflare and other providers through plugins.", `
      <button class="btn" id="btn-zone">${icon("plus")} New zone</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="dns-root">${loading()}</div>`);
    const [zones, provData, domains] = await Promise.all([
      api.get("/dns/zones").catch(() => []),
      api.get("/dns/providers").catch(() => ({ plugins: [], providers: [] })),
      api.get("/domains").catch(() => []),
    ]);

    const root = document.getElementById("dns-root");
    const provs = provData.providers || [];
    const plugins = provData.plugins || [];
    const cf = provs.find((p) => p.name === "cloudflare");

    root.innerHTML = `
      ${provRow(cf, plugins, provs)}
      ${zones.length ? `
        <div class="tbl-wrap"><table class="tbl">
          <thead><tr><th>Zone</th><th>Provider</th><th>Last sync</th><th></th></tr></thead>
          <tbody>${zones.map((z) => `
            <tr class="hoverable" data-id="${z.id}">
              <td><b class="mono">${esc(z.domain)}</b></td>
              <td><span class="tag ${z.provider === "local" ? "tag-teal" : "tag-lime"}">${icon(z.provider === "local" ? "server" : "cloud", "")} ${esc(z.provider)}</span></td>
              <td class="small dim">${z.synced_at ? fmtAgo(z.synced_at) : "never"}</td>
              <td><div class="row-actions">
                <button class="btn btn-ghost act-sync" title="Sync now">${icon("refresh")}</button>
                <button class="btn btn-ghost act-open" title="Manage records">${icon("list")}</button>
                <button class="btn btn-ghost act-del" title="Delete zone">${icon("trash")}</button>
              </div></td>
            </tr>`).join("")}</tbody></table></div>
        <div id="zone-detail" style="margin-top:16px"></div>`
      : emptyState(domains.length ? "No DNS zones yet." : "Add a domain first, then create its DNS zone.", domains.length ? undefined : "<a class='btn' href='#/domains'>Go to Domains</a>")}
    `;

    if (zones.length) {
      root.querySelectorAll("tr[data-id]").forEach((tr) => {
        tr.querySelector(".act-open")?.addEventListener("click", () => selectZone(+tr.dataset.id));
        tr.querySelector(".act-sync")?.addEventListener("click", async () => {
          toast("Syncing zone…");
          try {
            const res = await api.post(`/dns/zones/${tr.dataset.id}/sync`);
            toast(`Synced ${res.pushed} record(s)` + (res.deleted ? `, ${res.deleted} removed` : ""));
            reload();
          } catch (ex) { toast(ex.message, "err"); }
        });
        tr.querySelector(".act-del")?.addEventListener("click", async () => {
          if (!await confirmDialog("Delete this DNS zone and all its records?", { danger: true, title: "Delete zone" })) return;
          try { await api.del(`/dns/zones/${tr.dataset.id}`); toast("Zone deleted"); reload(); }
          catch (ex) { toast(ex.message, "err"); }
        });
        tr.addEventListener("click", () => selectZone(+tr.dataset.id));
      });
    }

    function provRow(cfProv, pluginList, provList) {
      return `<div class="card" style="margin-bottom:16px;padding:14px 18px;display:flex;align-items:center;gap:12px;flex-wrap:wrap">
        <span style="display:flex;gap:8px;align-items:center">${icon("cloud")}
          <b class="small" style="letter-spacing:.08em;text-transform:uppercase">Providers</b></span>
        <span class="pill-group">
          <span class="tag tag-teal">${icon("server", "")} local · built-in</span>
          ${cfProv ? `<span class="tag ${cfProv.enabled ? "tag-lime" : "tag-amber"}">${icon("cloud", "")} cloudflare ${cfProv.enabled ? "· connected" : "· disabled"}</span>` : ""}
        </span>
        <span class="spacer" style="flex:1"></span>
        <button class="btn btn-sm" id="btn-provider">${cfProv ? "Edit Cloudflare" : "Connect Cloudflare"}</button>
      </div>`;
    }

    document.getElementById("btn-provider").onclick = () => providerModal(cfProv());
    function cfProv() { return cf; }

    document.getElementById("btn-zone").onclick = () => createZone(domains, zones, reload);
  },

  // re-render on every visit
});

async function reload() { refresh(); }

function emptyState(msg, extra) {
  return `<div class="card empty-state"><span class="glyph">⌁</span><p>${esc(msg)}</p>${extra || ""}</div>`;
}

async function createZone(domains, zones, done) {
  const taken = new Set((zones || []).map((z) => z.domain));
  const free = (domains || []).filter((d) => !taken.has(d.domain));
  if (!free.length) { toast("Every domain already has a zone", "warn"); return; }
  const opts = free.map((d) => ({ value: String(d.id), label: d.domain }));
  const vals = await promptDialog("Create DNS zone", [
    { name: "domain_id", label: "Domain", type: "select", options: opts, required: true },
    { name: "provider", label: "Provider", type: "select", options: [{ value: "local", label: "Local (built-in authoritative server)" }, { value: "cloudflare", label: "Cloudflare" }] },
  ]);
  if (!vals) return;
  try {
    await api.post("/dns/zones", { domain_id: +vals.domain_id, provider: vals.provider || "local" });
    toast("Zone created — a default A record was added");
    done();
  } catch (ex) { toast(ex.message, "err"); }
}

async function selectZone(id) {
  selectedZone = id;
  const det = document.getElementById("zone-detail");
  if (!det) return;
  det.innerHTML = loading();
  try {
    const { zone, records } = await api.get("/dns/zones/" + id);
    det.innerHTML = `
      <div class="card">
        <div class="card-head">
          <span class="card-title">${esc(zone.domain)} <span class="tag">${esc(zone.provider)}</span> ${zone.synced_at ? `<span class="tag">synced ${fmtAgo(zone.synced_at)}</span>` : ""}</span>
          <span class="card-actions">
            <button class="btn btn-sm" id="r-add">${icon("plus")} Add record</button>
            <button class="btn btn-sm" id="r-sync">${icon("refresh")} Sync</button>
          </span>
        </div>
        ${records.length ? `<div class="tbl-wrap"><table class="tbl">
          <thead><tr><th>Name</th><th>Type</th><th>Content</th><th>TTL</th><th>Priority</th><th>Proxy</th><th></th></tr></thead>
          <tbody>${records.map((r) => `
            <tr>
              <td class="mono">${esc(r.name === "@" ? "@ (apex)" : r.name)}</td>
              <td><span class="tag tag-teal">${esc(r.type)}</span></td>
              <td class="mono small">${esc(r.content)}</td>
              <td class="num small">${r.ttl}</td>
              <td class="num small">${r.priority || "—"}</td>
              <td>${r.proxied ? '<span class="tag tag-lime">proxied</span>' : '<span class="dim small">no</span>'}</td>
              <td><div class="row-actions">
                <button class="btn btn-ghost r-edit" data-id="${r.id}">${icon("edit")}</button>
                <button class="btn btn-ghost r-del" data-id="${r.id}">${icon("trash")}</button>
              </div></td>
            </tr>`).join("")}</tbody></table></div>`
        : '<div class="empty-state"><span class="glyph">⌁</span><p>No records. Point something here — a site, mail, or a subdomain.</p></div>'}
      </div>`;
    document.getElementById("r-add").onclick = () => recordModal(zone, null, reload);
    document.getElementById("r-sync").onclick = async () => {
      try { const res = await api.post(`/dns/zones/${id}/sync`); toast(`Synced ${res.pushed} record(s)`); }
      catch (ex) { toast(ex.message, "err"); }
    };
    det.querySelectorAll(".r-edit").forEach((b) => b.onclick = () => recordModal(zone, records.find((r) => r.id === +b.dataset.id), reload));
    det.querySelectorAll(".r-del").forEach((b) => b.onclick = async () => {
      if (!await confirmDialog("Delete this DNS record?", { danger: true, title: "Delete record" })) return;
      await api.del(`/dns/records/${b.dataset.id}`).catch((e) => toast(e.message, "err"));
      selectZone(id);
    });
  } catch (ex) { det.innerHTML = `<p class="dim">${esc(ex.message)}</p>`; }
}

function recordModal(zone, rec, done) {
  const types = ["A", "AAAA", "CNAME", "MX", "TXT", "NS", "SRV", "CAA"];
  const fields = [
    { name: "name", label: "Name", value: rec?.name ?? "@", help: "@ for the zone root, e.g. @, www, mail", mono: true },
    { name: "type", label: "Type", type: "select", options: types.map((t) => ({ value: t, label: t })), value: rec?.type || "A" },
    { name: "content", label: "Content", value: rec?.content ?? "", required: true, mono: true },
    { name: "ttl", label: "TTL (seconds)", type: "number", value: rec?.ttl ?? 3600 },
    { name: "priority", label: "Priority (MX/SRV)", type: "number", value: rec?.priority ?? 0 },
  ];
  if (rec?.proxied || zone.provider === "cloudflare") {
    fields.push({ name: "proxied", label: "Cloudflare proxy (orange cloud)", type: "select", options: [{ value: "false", label: "No — DNS only" }, { value: "true", label: "Yes — proxy through Cloudflare" }], value: String(!!rec?.proxied) });
  }
  promptDialog(rec ? "Edit record" : "Add DNS record", fields).then(async (vals) => {
    if (!vals) return;
    const body = {
      name: vals.name || "@", type: vals.type, content: vals.content, ttl: +vals.ttl || 3600,
      priority: vals.priority ? +vals.priority : 0, proxied: vals.proxied === "true",
    };
    try {
      if (rec) await api.patch(`/dns/records/${rec.id}`, body);
      else await api.post(`/dns/zones/${zone.id}/records`, body);
      toast("Record saved");
      done();
    } catch (ex) { toast(ex.message, "err"); }
  });
}

function providerModal(cf) {
  const fields = [
    { name: "api_key", label: "Cloudflare API token", type: "password", required: !cf, mono: true, value: "", help: "Needs Zone:Read, Zone:Edit, DNS:Read and DNS:Edit. Never stored in plaintext — encrypted at rest." },
    { name: "email", label: "Account email", value: cf?.email || "", required: !cf },
    { name: "enabled", label: "Status", type: "select", options: [{ value: "true", label: "Enabled" }, { value: "false", label: "Disabled" }], value: String(!!cf?.enabled) },
  ];
  promptDialog("Cloudflare provider", fields).then(async (vals) => {
    if (!vals) return;
    const body = { name: "cloudflare", label: "Cloudflare", api_key: vals.api_key, email: vals.email, enabled: vals.enabled === "true" };
    try {
      if (cf) await api.patch(`/dns/providers/${cf.id}`, body);
      else await api.post("/dns/providers", body);
      toast("Cloudflare provider saved");
      reload();
    } catch (ex) { toast(ex.message, "err"); }
  });
}

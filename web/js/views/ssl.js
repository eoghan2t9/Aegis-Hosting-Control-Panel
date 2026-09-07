import { addRoute, isAdmin } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, statusTag, fmtAgo, fmtDate, pageHead, loading } from "../ui.js";

addRoute("/ssl", {
  title: "SSL",
  icon: "ssl",
  group: "Web",
  render: async (view) => {
    view.innerHTML = pageHead("SSL / TLS", "Let's Encrypt certificates with HTTP-01 or DNS-01 (via Cloudflare) challenges, self-signed fallbacks, and automatic renewal.");
    view.insertAdjacentHTML("beforeend", `<div id="ssl-root">${loading()}</div>`);
    const [orders, domains, provData] = await Promise.all([
      api.get("/ssl/orders").catch(() => []),
      api.get("/domains").catch(() => []),
      api.get("/dns/providers").catch(() => ({ providers: [] })),
    ]);
    const hasCF = (provData.providers || []).some((p) => p.name === "cloudflare" && p.enabled);
    const root = document.getElementById("ssl-root");

    if (!domains.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">🔒</span><p>Add a website first, then secure it with a certificate here.</p><a class="btn btn-primary" href="#/domains">Go to Domains</a></div>`;
      return;
    }
    const byId = {};
    domains.forEach((d) => byId[d.id] = d);
    root.innerHTML = `
      <div class="card" style="margin-bottom:16px">
        <div class="card-head"><span class="card-title">Issue a certificate</span></div>
        <div style="display:flex;gap:10px;flex-wrap:wrap">
          <select id="ssl-domain" style="flex:1;min-width:220px">
            ${domains.map((d) => `<option value="${d.id}">${esc(d.domain)}${d.ssl_enabled ? " (has cert)" : ""}</option>`).join("")}
          </select>
          <button class="btn" id="ssl-issue-http">${icon("ssl")} Let's Encrypt · HTTP</button>
          ${hasCF ? `<button class="btn" id="ssl-issue-dns">Let's Encrypt · DNS-01</button>` : `<button class="btn" id="ssl-issue-dns" title="Connect Cloudflare in DNS → Providers">DNS-01 (needs CF)</button>`}
          <button class="btn btn-ghost" id="ssl-self">Self-signed</button>
        </div>
      </div>
      ${orders.length ? `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Domain</th><th>Status</th><th>Provider</th><th>Challenge</th><th>Expires</th><th>Issued</th><th>Error</th></tr></thead>
        <tbody>${orders.map((o) => `
          <tr>
            <td class="mono"><b>${esc(o.domain || byId[o.domain_id]?.domain || "#" + o.domain_id)}</b></td>
            <td>${statusTag(o.status)}</td>
            <td><span class="tag">${esc(o.provider)}</span></td>
            <td class="small dim">${esc(o.challenge)}</td>
            <td class="small">${o.expires_at ? fmtDate(o.expires_at) : "—"}</td>
            <td class="small dim">${fmtAgo(o.created_at)}</td>
            <td class="small dim" style="max-width:220px">${esc(o.error || "")}</td>
          </tr>`).join("")}</tbody></table></div>`
      : '<div class="card empty-state"><span class="glyph">🔒</span><p>No certificate orders yet.</p></div>'}`;

    const issue = async (domainId, challenge, kind) => {
      const btn = [...document.querySelectorAll("#ssl-root .btn")].find((b) => b.id.startsWith("ssl-" + kind)) ;
      if (btn) btn.classList.add("btn-busy");
      try {
        const order = await api.post(kind === "self" ? "/ssl/self-signed" : "/ssl/issue", { domain_id: +domainId, challenge });
        toast(order.status === "issued" ? "Certificate issued" : "Certificate order " + order.status);
        location.hash = "#/ssl";
        location.reload();
      } catch (ex) { toast(ex.message, "err"); }
      if (btn) btn.classList.remove("btn-busy");
    };
    const dom = () => document.getElementById("ssl-domain").value;
    document.getElementById("ssl-issue-http").onclick = () => issue(dom(), "http", "issue");
    document.getElementById("ssl-issue-dns").onclick = () => issue(dom(), "dns", "issue");
    document.getElementById("ssl-self").onclick = () => issue(dom(), "", "self");
  },
});

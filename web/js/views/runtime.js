import { addRoute, isAdmin, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, confirmDialog, statusTag, fmtPct, pageHead, loading, fmtDate } from "../ui.js";

addRoute("/runtime", {
  title: "Runtime",
  icon: "zap",
  group: "Server",
  order: 1,
  render: async (view) => {
    view.innerHTML = pageHead("Runtime", "The PHP versions and web server powering this host. Each website picks its own PHP version from the installed set.");
    view.insertAdjacentHTML("beforeend", `<div id="rt-root">${loading()}</div>`);

    const [php, ws, tuning] = await Promise.all([
      api.get("/php/versions").catch(() => ({ versions: [], latest: "" })),
      api.get("/webserver").catch(() => ({ active: "unknown", available: [] })),
      api.get("/tuning/report").catch(() => ({})),
    ]);
    const root = document.getElementById("rt-root");
    const versions = php.versions || [];

    root.innerHTML = `
      <div class="grid grid-2" style="align-items:start">
        <div class="card">
          <div class="card-head"><span class="card-title">Installed PHP</span>
            <span class="card-actions"><span class="tag tag-lime">latest: ${esc(php.latest || "—")}</span></span></div>
          ${versions.length ? `<div class="tbl-wrap"><table class="tbl">
            <thead><tr><th>Version</th><th>CLI</th><th>php-fpm</th><th>Pool</th></tr></thead>
            <tbody>${versions.map((v) => `
              <tr>
                <td><b class="mono">${esc(v.version)}</b></td>
                <td class="mono small dim">${esc(v.cli || "—")}</td>
                <td class="mono small dim">${esc(v.fpm || "—")}</td>
                <td>${v.running ? statusTag("active") : statusTag("inactive")}</td>
              </tr>`).join("")}</tbody></table></div>`
          : `<p class="muted">No PHP-FPM installations detected. Install php-fpm (e.g. ondrej/php PPA on Ubuntu) and it appears here automatically.</p>`}
          <p class="small dim" style="margin:10px 0 0">Aegis writes one isolated FPM pool per website under /etc/php/&lt;version&gt;/fpm/pool.d, socket in /run/php/aegis-&lt;domain&gt;.sock.</p>
        </div>
        <div>
          <div class="card" style="margin-bottom:16px">
            <div class="card-head"><span class="card-title">Web server</span>
              <span class="card-actions"><span class="tag tag-teal">active: ${esc(ws.active || "—")}</span></span></div>
            <p class="small muted">The active server generates vhosts for every new domain. Native Go and the classic servers are all supported.</p>
            <div class="pill-group" id="ws-picker">
              ${["go", "nginx", "apache", "caddy"].map((s) => {
                const installed = (ws.available || []).includes(s);
                return `<button class="btn btn-sm ${s === ws.active ? "btn-primary" : ""}" data-ws="${esc(s)}" ${installed ? "" : "data-install=\"1\""} title="${installed ? "Installed" : "Not installed — will be installed automatically when selected"}">${esc(s)}${installed ? "" : " <span class=\"small dim\">(install)</span>"}</button>`;
              }).join("")}
            </div>
            ${isAdmin() ? `<p class="small dim" style="margin-top:10px">Switching re-targets new domains only; existing vhosts are regenerated when you click “Apply config” on each domain.</p>`
            : `<p class="small dim" style="margin-top:10px">Contact an administrator to change the active web server.</p>`}
          </div>
          <div class="card">
            <div class="card-head"><span class="card-title">Tuning snapshot</span></div>
            ${tuning.tuned ? `
              <dl class="kv">
                <dt>generated</dt><dd class="small">${fmtDate(tuning.generated_at)}</dd>
                <dt>host</dt><dd class="mono">${tuning.cores} cores · ${tuning.ram_mb} MB RAM</dd>
                <dt>php-fpm</dt><dd class="mono">${tuning.php_fpm.max_children} max children</dd>
                <dt>nginx</dt><dd class="mono">${tuning.nginx.worker_connections} conn/worker</dd>
                <dt>apache</dt><dd class="mono">${tuning.apache.max_request_workers} max request workers <span class="small dim">(prepped — applies once apache is enabled)</span></dd>
                <dt>caddy</dt><dd class="mono">${tuning.caddy.idle_timeout_s}s idle timeout <span class="small dim">(prepped — applies once caddy is enabled)</span></dd>
                <dt>sysctl</dt><dd class="small mono">${(tuning.sysctl || []).map((s) => s.key + "=" + s.value).join(" ")}</dd>
              </dl>` : `<p class="muted small">No tuning report yet — run it from System → Performance tuning (admin).</p>`}
          </div>
        </div>
      </div>`;

    if (isAdmin()) {
      document.querySelectorAll("#ws-picker button").forEach((b) => {
        b.onclick = async () => {
          const target = b.dataset.ws;
          if (target === ws.active) return;
          const needsInstall = b.dataset.install === "1";
          const ok = await confirmDialog(
            needsInstall
              ? `${target} is not installed yet. Install it via the package manager and set it as the active web server?`
              : `Set the active web server to ${target}?`,
            { title: "Switch web server", okText: needsInstall ? "Install & switch" : "Switch" });
          if (!ok) return;
          try { await api.patch("/webserver", { server: target, install: true }); toast("Active web server: " + target); refresh(); }
          catch (ex) { toast(ex.message, "err"); }
        };
      });
    }
  },
});

void fmtPct;
void icon;

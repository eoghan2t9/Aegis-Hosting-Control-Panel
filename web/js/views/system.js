import { addRoute, isAdmin, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, confirmDialog, fmtBytes, fmtPct, fmtDate, statusTag, pageHead, loading } from "../ui.js";

addRoute("/system", {
  title: "System",
  icon: "cpu",
  group: "Server",
  adminOnly: true,
  render: async (view) => {
    view.innerHTML = pageHead("System", "Processes, security audit log, and the performance-tuning report.");

    // Tuning card.
    const tuneCard = hx(`<div class="card" id="tune-card" style="margin-bottom:16px"><div class="card-head"><span class="card-title">Performance tuning</span>
      <span class="card-actions"><button class="btn btn-sm" id="btn-tune">${icon("zap")} Inspect & re-tune</button></span></div><div id="tune-body">${loading()}</div></div>`);
    view.appendChild(tuneCard);

    const report = await api.get("/tuning/report");
    const tuneBody = document.getElementById("tune-body");
    if (report.tuned) {
      tuneBody.innerHTML = `
        <div class="grid grid-4">
          <div class="stat"><div class="stat-label">CPU cores</div><div class="stat-value">${report.cores}</div></div>
          <div class="stat"><div class="stat-label">RAM</div><div class="stat-value">${report.ram_mb}<small> MB</small></div></div>
          <div class="stat"><div class="stat-label">php-fpm workers</div><div class="stat-value">${report.php_fpm.max_children}</div>
            <div class="stat-meta">start ${report.php_fpm.start_servers} · max spare ${report.php_fpm.max_spare}</div></div>
          <div class="stat"><div class="stat-label">nginx connections</div><div class="stat-value">${report.nginx.worker_connections}</div>
            <div class="stat-meta">generated ${fmtDate(report.generated_at)}</div></div>
        </div>
        <div style="margin-top:12px" class="small dim">Kernel parameters: ${report.sysctl.map((s) => s.key + "=" + s.value).join(" · ")}${report.sysctl_applied ? " · <span class='tag tag-lime'>applied</span>" : ""}</div>`;
    } else {
      tuneBody.innerHTML = `<p class="muted">No tuning report yet. Run an inspection to size php-fpm and the web server to this host.</p>`;
    }
    document.getElementById("btn-tune").onclick = async () => {
      const ok = await confirmDialog("Re-run the automatic tuning inspection?", { title: "Auto-tune", okText: "Run inspection" });
      if (!ok) return;
      try {
        const data = await api.post("/tuning/apply", { apply_sysctl: false });
        toast("Tuning report regenerated for " + data.cores + " cores / " + data.ram_mb + " MB");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    };

    // Tabs: processes / audit.
    view.appendChild(hx(`<div class="tab-row">
      <button class="tab-btn active" data-tab="procs">Processes</button>
      <button class="tab-btn" data-tab="audit">Audit log</button>
    </div><div id="tab-procs"></div><div id="tab-audit" class="hidden"></div>`));
    const procs = document.getElementById("tab-procs");
    const audit = document.getElementById("tab-audit");
    procs.innerHTML = loading();
    audit.innerHTML = loading();

    const bindTabs = (procsEl, auditEl) => {
      document.querySelectorAll(".tab-row .tab-btn").forEach((b) => {
        if (b.dataset.bound) return;
        b.dataset.bound = "1";
        b.onclick = () => {
          document.querySelectorAll(".tab-row .tab-btn").forEach((x) => x.classList.toggle("active", x === b));
          procsEl.classList.toggle("hidden", b.dataset.tab !== "procs");
          auditEl.classList.toggle("hidden", b.dataset.tab !== "audit");
          if (b.dataset.tab === "audit" && !auditEl.dataset.loaded) loadAudit(auditEl);
        };
      });
    };
    bindTabs(procs, audit);
    loadProcs(procs);
    loadAudit(audit);

    async function loadProcs(container) {
      container.dataset.loaded = "1";
      try {
        const list = await api.get("/system/processes");
        container.innerHTML = `
          <div class="toolbar"><input type="search" id="proc-q" placeholder="Filter processes…">
          <span class="spacer"></span><span class="tag">${list.length} processes</span></div>
          <div class="tbl-wrap"><table class="tbl">
            <thead><tr><th>PID</th><th>User</th><th>Name</th><th>CPU</th><th>Memory</th><th>Command</th></tr></thead>
            <tbody>${list.map((p) => `
              <tr><td class="num">${p.pid}</td><td class="small">${esc(p.user || p.uid)}</td>
              <td>${esc(p.name)}</td><td class="num small">${p.cpu.toFixed(1)}s</td>
              <td class="num small">${fmtBytes(p.rss_bytes)}</td>
              <td class="mono small dim" style="max-width:420px;overflow:hidden;text-overflow:ellipsis">${esc(p.command)}</td></tr>`).join("")}</tbody></table></div>`;
        document.getElementById("proc-q").addEventListener("input", (e) => {
          const q = e.target.value.toLowerCase();
          container.querySelectorAll("tbody tr").forEach((tr) => {
            tr.style.display = tr.textContent.toLowerCase().includes(q) ? "" : "none";
          });
        });
      } catch (ex) { container.innerHTML = `<p class="dim">${esc(ex.message)}</p>`; }
    }

    async function loadAudit(container) {
      container.dataset.loaded = "1";
      try {
        const log = await api.get("/admin/audit");
        container.innerHTML = log.length ? `<div class="tbl-wrap"><table class="tbl">
          <thead><tr><th>When</th><th>Actor</th><th>Action</th><th>Target</th><th>Detail</th></tr></thead>
          <tbody>${log.map((e) => `
            <tr><td class="audit-time">${fmtDate(e.created_at)}</td><td class="small mono">${esc(e.actor_name)}</td>
            <td><span class="audit-action">${esc(e.action)}</span></td>
            <td class="small">${esc(e.target)}</td><td class="small dim">${esc(e.detail)}</td></tr>`).join("")}</tbody></table></div>`
          : `<div class="card empty-state"><p>No audit entries yet.</p></div>`;
      } catch (ex) { container.innerHTML = `<p class="dim">${esc(ex.message)}</p>`; }
    }
  },
});

function hx(html) {
  const t = document.createElement("template");
  t.innerHTML = html.trim();
  return t.content;
}

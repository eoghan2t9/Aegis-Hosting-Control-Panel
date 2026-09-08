import { addRoute } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, confirmDialog, statusTag, pageHead, loading, fmtAgo } from "../ui.js";

addRoute("/security", {
  title: "Security",
  icon: "shield",
  group: "Server",
  order: 3,
  adminOnly: true,
  render: async (view) => {
    view.innerHTML = pageHead("Security", "Login attempts against the panel and IPs currently banned by fail2ban after repeated failures.", "");
    view.insertAdjacentHTML("beforeend", `<div id="sec-root">${loading()}</div>`);
    const [attempts, bans] = await Promise.all([
      api.get("/security/attempts").catch(() => []),
      api.get("/security/bans").catch(() => []),
    ]);
    const root = document.getElementById("sec-root");

    root.innerHTML = `
      <div class="card" style="margin-bottom:16px">
        <div class="card-head"><span class="card-title">Banned IPs</span></div>
        ${bans.length ? `<div class="tbl-wrap"><table class="tbl">
          <thead><tr><th>IP</th><th></th></tr></thead>
          <tbody>${bans.map((ip) => `<tr data-ip="${esc(ip)}">
            <td class="mono">${esc(ip)}</td>
            <td><button class="btn btn-ghost act-unban">Unban</button></td>
          </tr>`).join("")}</tbody></table></div>`
        : '<p class="small dim">No IPs currently banned. fail2ban bans after 5 failed logins from the same IP within 15 minutes.</p>'}
      </div>
      <div class="card">
        <div class="card-head"><span class="card-title">Recent login attempts</span></div>
        ${attempts.length ? `<div class="tbl-wrap"><table class="tbl">
          <thead><tr><th>Username</th><th>IP</th><th>Result</th><th>When</th></tr></thead>
          <tbody>${attempts.map((a) => `<tr>
            <td class="mono">${esc(a.username)}</td>
            <td class="mono small dim">${esc(a.ip)}</td>
            <td>${a.success ? statusTag("active") : statusTag("suspended")}</td>
            <td class="small dim">${fmtAgo(a.created_at)}</td>
          </tr>`).join("")}</tbody></table></div>`
        : '<p class="small dim">No login attempts recorded yet.</p>'}
      </div>`;

    root.querySelectorAll(".act-unban").forEach((btn) => {
      const ip = btn.closest("tr").dataset.ip;
      btn.addEventListener("click", async () => {
        if (!await confirmDialog(`Unban ${ip}?`, { title: "Unban IP" })) return;
        try {
          await api.post("/security/unban", { ip });
          toast("IP unbanned");
          btn.closest("tr").remove();
        } catch (ex) { toast(ex.message, "err"); }
      });
    });
  },
});

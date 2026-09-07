import { addRoute, isAdmin } from "../app.js";
import { api, qs } from "../api.js";
import { icon, esc, toast, confirmDialog, promptDialog, fmtBytes, fmtAgo, pageHead, loading } from "../ui.js";

addRoute("/backups", {
  title: "Backups",
  icon: "archive",
  group: "Manage",
  resellerOnly: true,
  adminOnly: true,
  render: async (view) => {
    view.innerHTML = pageHead("Backups", "Full-account archives: home directories, databases, FTP accounts, domains, DNS records and package data — one tar.gz with a JSON manifest.", `
      <button class="btn btn-primary" id="btn-full">${icon("plus")} Full backup</button>
      <button class="btn" id="btn-user">Back up one account</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="bk-root">${loading()}</div>`);

    const backups = await api.get("/backups");
    const root = document.getElementById("bk-root");
    root.innerHTML = backups.length ? `<div class="tbl-wrap"><table class="tbl">
      <thead><tr><th>Archive</th><th>Size</th><th>Created</th><th></th></tr></thead>
      <tbody>${backups.map((b) => `
        <tr data-name="${esc(b.name)}">
          <td class="mono"><b>${esc(b.name)}</b></td>
          <td class="num">${fmtBytes(b.size)}</td>
          <td class="small dim">${fmtAgo(b.created_at)}</td>
          <td><div class="row-actions">
            <button class="btn btn-ghost act-dl" title="Download">${icon("download")}</button>
            <button class="btn btn-ghost act-restore" title="Restore">${icon("refresh")}</button>
            <button class="btn btn-ghost act-del" title="Delete file">${icon("trash")}</button>
          </div></td>
        </tr>`).join("")}</tbody></table></div>`
      : `<div class="card empty-state"><span class="glyph">🗄</span><p>No backups yet. Run a full backup to snapshot every account — FTP credentials, databases and site files included.</p></div>`;

    root.querySelectorAll("tbody tr").forEach((tr) => {
      const name = tr.dataset.name;
      tr.querySelector(".act-dl")?.addEventListener("click", () => { location.href = "/api/backups/download?name=" + encodeURIComponent(name); });
      tr.querySelector(".act-restore")?.addEventListener("click", async () => {
        const warn = "Restoring overwrites/recreates accounts from the manifest. This is not reversible. Continue?";
        if (!await confirmDialog(warn, { danger: true, title: "Restore " + name, okText: "Restore now" })) return;
        try { await api.post("/backups/restore", { name }); toast("Backup restored"); } catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelector(".act-del")?.addEventListener("click", async () => {
        if (!await confirmDialog(`Delete archive ${name}?`, { danger: true, title: "Delete backup" })) return;
        try { await api.del("/backups" + qs({ name })); toast("Backup deleted"); location.reload(); } catch (ex) { toast(ex.message, "err"); }
      });
    });

    document.getElementById("btn-full").onclick = async () => {
      const ok = await confirmDialog("Create a full backup of every account now? This can take a while on large servers.", { title: "Full backup", okText: "Start backup" });
      if (!ok) return;
      try {
        const info = await api.post("/backups", { scope: "full" });
        toast("Backup created: " + info.name);
        location.reload();
      } catch (ex) { toast(ex.message, "err"); }
    };
    document.getElementById("btn-user").onclick = async () => {
      const users = await api.get("/users");
      const opts = users.map((u) => ({ value: String(u.id), label: u.username }));
      if (!opts.length) return toast("No accounts", "warn");
      const vals = await promptDialog("Back up one account", [
        { name: "user_id", label: "Account", type: "select", options: opts, required: true },
      ]);
      if (!vals) return;
      try {
        const info = await api.post("/backups", { scope: "user", user_id: +vals.user_id });
        toast("Backup created: " + info.name);
        location.reload();
      } catch (ex) { toast(ex.message, "err"); }
    };
  },
});

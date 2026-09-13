import { addRoute, isAdmin, refresh } from "../app.js";
import { p } from "../base.js";
import { api, qs } from "../api.js";
import { icon, esc, toast, confirmDialog, promptDialog, fmtBytes, fmtAgo, pageHead, loading } from "../ui.js";

addRoute("/backups", {
  title: "Backups",
  icon: "archive",
  group: "Server",
  order: 4,
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
      tr.querySelector(".act-dl")?.addEventListener("click", () => { location.href = p("/api/backups/download?name=" + encodeURIComponent(name)); });
      tr.querySelector(".act-restore")?.addEventListener("click", async () => {
        const warn = "Restoring overwrites/recreates accounts from the manifest. This is not reversible. Continue?";
        if (!await confirmDialog(warn, { danger: true, title: "Restore " + name, okText: "Restore now" })) return;
        try { await api.post("/backups/restore", { name }); toast("Backup restored"); } catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelector(".act-del")?.addEventListener("click", async () => {
        if (!await confirmDialog(`Delete archive ${name}?`, { danger: true, title: "Delete backup" })) return;
        try { await api.del("/backups" + qs({ name })); toast("Backup deleted"); refresh(); } catch (ex) { toast(ex.message, "err"); }
      });
    });

    document.getElementById("btn-full").onclick = async () => {
      const ok = await confirmDialog("Create a full backup of every account now? This can take a while on large servers.", { title: "Full backup", okText: "Start backup" });
      if (!ok) return;
      try {
        const info = await api.post("/backups", { scope: "full" });
        toast("Backup created: " + info.name);
        refresh();
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
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    };

    await renderTargets(view);
  },
});

async function renderTargets(view) {
  const [targets, schedule] = await Promise.all([
    api.get("/backups/targets").catch(() => []),
    api.get("/backups/schedule").catch(() => ({ enabled: false })),
  ]);
  view.insertAdjacentHTML("beforeend", `
    <div class="card" style="margin-top:16px">
      <div class="card-head">
        <span class="card-title">Offsite targets & retention</span>
        <div class="card-actions">
          <label class="small" style="display:flex;align-items:center;gap:6px">
            <input type="checkbox" id="sched-toggle" ${schedule.enabled ? "checked" : ""}> Daily automatic backup
          </label>
          <button class="btn btn-sm" id="btn-target">${icon("plus")} Add target</button>
        </div>
      </div>
      <div id="targets-root">${targets.length ? `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Label</th><th>Kind</th><th>Retention</th><th></th></tr></thead>
        <tbody>${targets.map((t) => `<tr data-id="${t.id}">
          <td>${esc(t.label || "(unlabeled)")}</td>
          <td class="mono small">${esc(t.kind)}</td>
          <td class="small dim">${t.retention_days} days</td>
          <td><button class="btn btn-ghost act-del-target" title="Remove">${icon("trash")}</button></td>
        </tr>`).join("")}</tbody></table></div>`
        : '<p class="small dim">No offsite targets. Backups stay local only.</p>'}</div>
    </div>`);

  document.getElementById("sched-toggle").addEventListener("change", async (e) => {
    try { await api.patch("/backups/schedule", { enabled: e.target.checked }); toast(e.target.checked ? "Daily backups enabled" : "Daily backups disabled"); }
    catch (ex) { toast(ex.message, "err"); e.target.checked = !e.target.checked; }
  });

  document.querySelectorAll(".act-del-target").forEach((btn) => {
    const id = +btn.closest("tr").dataset.id;
    btn.addEventListener("click", async () => {
      if (!await confirmDialog("Remove this offsite target?", { danger: true, title: "Remove target" })) return;
      try { await api.del(`/backups/targets/${id}`); toast("Target removed"); refresh(); }
      catch (ex) { toast(ex.message, "err"); }
    });
  });

  document.getElementById("btn-target").onclick = async () => {
    const kind = await promptDialog("Offsite target type", [
      { name: "kind", label: "Type", type: "select", required: true, options: [
        { value: "s3", label: "S3-compatible" },
        { value: "b2", label: "Backblaze B2 (S3-compatible)" },
        { value: "sftp", label: "SFTP" },
      ] },
    ]);
    if (!kind) return;
    const fields = kind.kind === "sftp"
      ? [
          { name: "label", label: "Label", required: true, placeholder: "Offsite SFTP" },
          { name: "host", label: "Host", required: true },
          { name: "port", label: "Port", type: "number", value: "22" },
          { name: "user", label: "Username", required: true },
          { name: "password", label: "Password", type: "password", required: true },
          { name: "path", label: "Remote path", value: "/backups" },
          { name: "retention_days", label: "Retention (days)", type: "number", value: "30" },
        ]
      : [
          { name: "label", label: "Label", required: true, placeholder: kind.kind === "b2" ? "Backblaze B2" : "S3 bucket" },
          { name: "endpoint", label: "Endpoint", required: true, placeholder: "s3.us-west-002.backblazeb2.com" },
          { name: "bucket", label: "Bucket", required: true },
          { name: "region", label: "Region", placeholder: "us-west-002" },
          { name: "access_key", label: "Access key", required: true },
          { name: "secret_key", label: "Secret key", type: "password", required: true },
          { name: "use_ssl", label: "Use TLS", type: "select", value: "true", options: [{ value: "true", label: "Yes" }, { value: "false", label: "No" }] },
          { name: "retention_days", label: "Retention (days)", type: "number", value: "30" },
        ];
    const vals = await promptDialog("New offsite target", fields, { wide: true });
    if (!vals) return;
    try {
      await api.post("/backups/targets", {
        kind: kind.kind, label: vals.label, retention_days: +vals.retention_days || 30,
        endpoint: vals.endpoint, bucket: vals.bucket, region: vals.region,
        access_key: vals.access_key, secret_key: vals.secret_key, use_ssl: vals.use_ssl === "true",
        host: vals.host, port: +vals.port || 0, user: vals.user, password: vals.password, path: vals.path,
      });
      toast("Target added and verified");
      refresh();
    } catch (ex) { toast(ex.message, "err"); }
  };
}

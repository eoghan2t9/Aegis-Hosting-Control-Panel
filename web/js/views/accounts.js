import { addRoute, isAdmin, isReseller, me, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, statusTag, fmtAgo, pageHead, loading, modal } from "../ui.js";

addRoute("/accounts", {
  title: "Accounts",
  icon: "users",
  group: "Users",
  order: 0,
  resellerOnly: true,
  render: async (view) => {
    view.innerHTML = pageHead("Accounts", isAdmin()
      ? "Every panel account. Suspend, reset passwords, edit packages — or log in as a user to troubleshoot on their behalf."
      : "Accounts you have created.", `
      <button class="btn btn-primary" id="btn-user">${icon("plus")} New account</button>`);
    view.insertAdjacentHTML("beforeend", `
      <div class="tab-row">
        <button class="tab-btn active" data-tab="users">Users</button>
        ${isAdmin() ? `<button class="tab-btn" data-tab="packages">Packages</button>` : ""}
      </div>
      <div id="tab-users"></div><div id="tab-packages" class="hidden"></div>`);

    const [users, packages] = await Promise.all([api.get("/users"), api.get("/packages")]);
    const tabs = { users: document.getElementById("tab-users"), packages: document.getElementById("tab-packages") };
    document.querySelectorAll(".tab-btn").forEach((b) => b.onclick = () => {
      document.querySelectorAll(".tab-btn").forEach((x) => x.classList.toggle("active", x === b));
      tabs.users.classList.toggle("hidden", b.dataset.tab !== "users");
      tabs.packages.classList.toggle("hidden", b.dataset.tab !== "packages");
    });
    const pkgName = (id) => packages.find((p) => p.id === id)?.name || "—";

    tabs.users.innerHTML = users.length ? `<div class="tbl-wrap"><table class="tbl">
      <thead><tr><th>Account</th><th>Role</th><th>Package</th><th>State</th><th>Created</th><th></th></tr></thead>
      <tbody>${users.map((u) => `
        <tr data-id="${u.id}">
          <td><div style="display:flex;align-items:center;gap:9px">
            <span class="avatar">${esc(u.username.slice(0, 2))}</span>
            <div><b>${esc(u.username)}</b><div class="small dim">${esc(u.email || "")}</div></div></div></td>
          <td>${roleTag(u.role)}</td>
          <td class="small">${esc(pkgName(u.package_id))}</td>
          <td>${u.status === "suspended" ? statusTag("suspended") : statusTag("active")}</td>
          <td class="small dim">${fmtAgo(u.created_at)}</td>
          <td><div class="row-actions">
            ${isAdmin() ? `<button class="btn btn-ghost act-imp" title="Log in as user">${icon("eye")}</button>` : ""}
            <button class="btn btn-ghost act-pass" title="Reset password">${icon("key")}</button>
            <button class="btn btn-ghost act-susp" title="Suspend/unsuspend">${icon("toggle")}</button>
            <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
          </div></td>
        </tr>`).join("")}</tbody></table></div>`
      : `<div class="card empty-state"><span class="glyph">👤</span><p>No accounts yet.</p></div>`;

    tabs.users.querySelectorAll("tbody tr").forEach((tr) => {
      const row = users.find((u) => u.id === +tr.dataset.id);
      if (!row) return;
      tr.querySelector(".act-imp")?.addEventListener("click", async () => {
        const ok = await confirmDialog(`Log in as ${row.username}? Everything you do is audited and you can exit with "Support session" banner.`, { title: "Log in as user", okText: "Enter account" });
        if (!ok) return;
        try {
          const data = await api.post("/admin/impersonate", { user_id: row.id });
          api.setToken(data.token);
          location.hash = "#/dashboard";
          location.reload();
        } catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelector(".act-pass")?.addEventListener("click", async () => {
        const vals = await promptDialog(`Reset password for ${row.username}`, [{ name: "password", label: "New password", type: "password", required: true }]);
        if (!vals) return;
        try { await api.post(`/users/${row.id}/reset-password`, vals); toast("Password reset"); } catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelector(".act-susp")?.addEventListener("click", async () => {
        const suspending = row.status !== "suspended";
        if (!await confirmDialog(suspending ? `Suspend ${row.username}? Active sessions are killed.` : `Unsuspend ${row.username}?`, { danger: suspending, title: suspending ? "Suspend account" : "Unsuspend account" })) return;
        try { await api.post(`/users/${row.id}/${suspending ? "suspend" : "unsuspend"}`); toast("Done"); refresh(); } catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelector(".act-del")?.addEventListener("click", async () => {
        if (!await confirmDialog(`Delete ${row.username}? The system account and panel data are removed.`, { danger: true, title: "Delete account" })) return;
        try { await api.del(`/users/${row.id}`); toast("Account deleted"); refresh(); } catch (ex) { toast(ex.message, "err"); }
      });
    });

    document.getElementById("btn-user").onclick = async () => {
      const roleOpts = [{ value: "user", label: "User" }];
      if (isAdmin()) roleOpts.push({ value: "reseller", label: "Reseller" }, { value: "admin", label: "Admin" });
      const pkgOpts = packages.map((p) => ({ value: String(p.id), label: p.name }));
      const defPkg = packages.find((p) => p.is_default);
      const vals = await promptDialog("New account", [
        { name: "username", label: "Username", mono: true, required: true },
        { name: "email", label: "Email" },
        { name: "password", label: "Password", type: "password", required: true },
        { name: "role", label: "Role", type: "select", options: roleOpts },
        ...(pkgOpts.length ? [{ name: "package_id", label: "Package", type: "select", options: pkgOpts, value: String(defPkg?.id || pkgOpts[0].value) }] : []),
      ]);
      if (!vals) return;
      try {
        await api.post("/users", { username: vals.username, email: vals.email, password: vals.password, role: vals.role, package_id: vals.package_id ? +vals.package_id : 0 });
        toast("Account created");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    };

    // Packages tab (admin).
    const pkBox = tabs.packages;
    pkBox.innerHTML = `<button class="btn" id="btn-pkg" style="margin-bottom:12px">${icon("plus")} New package</button>` +
      (packages.length ? `<div class="grid grid-3" style="align-items:start">${packages.map((p) => `
        <div class="card">
          <div class="card-head" style="margin-bottom:10px"><span class="card-title">${esc(p.name)} ${p.is_default ? '<span class="tag tag-lime">default</span>' : ""}</span>
            <span class="card-actions">
              <button class="btn btn-ghost btn-sm pkg-edit" data-id="${p.id}">${icon("edit")}</button>
              <button class="btn btn-ghost btn-sm pkg-del" data-id="${p.id}">${icon("trash")}</button>
            </span></div>
          <dl class="kv" style="grid-template-columns:auto 1fr;font-size:12.5px">
            <dt>domains</dt><dd>${p.max_domains || "∞"}</dd>
            <dt>databases</dt><dd>${p.max_databases || "∞"}</dd>
            <dt>ftp accounts</dt><dd>${p.max_ftp_accounts || "∞"}</dd>
            <dt>disk</dt><dd>${p.disk_quota_bytes ? fmtBytesShort(p.disk_quota_bytes) : "unlimited"}</dd>
            <dt>features</dt><dd>${[p.allow_ssl && "ssl", p.allow_dns && "dns", p.allow_terminal && "terminal", p.allow_backups && "backups", p.allow_mail && "mail", p.allow_webmail && "webmail", p.allow_databases && "databases", p.allow_files && "files", p.allow_ftp && "ftp", p.allow_cron && "cron"].filter(Boolean).join(" · ") || "—"}</dd>
          </dl>
        </div>`).join("")}</div>` : "");

    document.getElementById("btn-pkg").onclick = () => pkgModal(null, reload);
    pkBox.querySelectorAll(".pkg-edit").forEach((b) => b.onclick = () => pkgModal(packages.find((p) => p.id === +b.dataset.id), reload));
    pkBox.querySelectorAll(".pkg-del").forEach((b) => b.onclick = async () => {
      const p = packages.find((x) => x.id === +b.dataset.id);
      if (!await confirmDialog(`Delete package ${p.name}?`, { danger: true, title: "Delete package" })) return;
      try { await api.del("/packages/" + p.id); toast("Deleted"); reload(); } catch (ex) { toast(ex.message, "err"); }
    });
  },
});

function reload() { refresh(); }

function roleTag(role) {
  const cls = role === "admin" ? "tag-red" : role === "reseller" ? "tag-amber" : "";
  return `<span class="tag ${cls}">${esc(role)}</span>`;
}

function fmtBytesShort(n) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return n.toFixed(n >= 100 ? 0 : 1) + " " + units[i];
}

function pkgModal(pkg, done) {
  const p = pkg || {};
  const b = (n, def) => p[n] !== undefined && p[n] !== null ? p[n] : def;
  const valsPromise = promptDialog(pkg ? "Edit package" : "New package", [
    { name: "name", label: "Name", required: true, value: b("name", ""), mono: true },
    { name: "description", label: "Description", value: b("description", "") },
    { name: "max_domains", label: "Max domains (0 = unlimited)", type: "number", value: b("max_domains", 1) },
    { name: "max_databases", label: "Max databases (0 = unlimited)", type: "number", value: b("max_databases", 1) },
    { name: "max_ftp_accounts", label: "Max FTP accounts (0 = unlimited)", type: "number", value: b("max_ftp_accounts", 1) },
    { name: "disk_quota_bytes", label: "Disk quota (MB, 0 = unlimited)", type: "number", value: (b("disk_quota_bytes", 0) / (1024 * 1024)) },
    { name: "allow_ssl", label: "SSL", type: "select", options: yesNo, value: String(b("allow_ssl", true)) },
    { name: "allow_dns", label: "DNS", type: "select", options: yesNo, value: String(b("allow_dns", true)) },
    { name: "allow_terminal", label: "Terminal", type: "select", options: yesNo, value: String(b("allow_terminal", true)) },
    { name: "allow_backups", label: "Backups", type: "select", options: yesNo, value: String(b("allow_backups", true)) },
    { name: "allow_mail", label: "Email (mailboxes & aliases)", type: "select", options: yesNo, value: String(b("allow_mail", true)) },
    { name: "allow_webmail", label: "Webmail", type: "select", options: yesNo, value: String(b("allow_webmail", true)) },
    { name: "allow_databases", label: "Databases", type: "select", options: yesNo, value: String(b("allow_databases", true)) },
    { name: "allow_files", label: "File manager", type: "select", options: yesNo, value: String(b("allow_files", true)) },
    { name: "allow_ftp", label: "FTP", type: "select", options: yesNo, value: String(b("allow_ftp", true)) },
    { name: "allow_cron", label: "Cron jobs", type: "select", options: yesNo, value: String(b("allow_cron", true)) },
    { name: "is_default", label: "Default package", type: "select", options: yesNo, value: String(b("is_default", false)) },
  ]);
  valsPromise.then(async (vals) => {
    if (!vals) return;
    const body = {
      name: vals.name, description: vals.description,
      max_domains: +vals.max_domains, max_databases: +vals.max_databases, max_ftp_accounts: +vals.max_ftp_accounts,
      disk_quota_bytes: Math.round(+vals.disk_quota_bytes * 1024 * 1024),
      allow_ssl: vals.allow_ssl === "true", allow_dns: vals.allow_dns === "true",
      allow_terminal: vals.allow_terminal === "true", allow_backups: vals.allow_backups === "true",
      allow_mail: vals.allow_mail === "true", allow_webmail: vals.allow_webmail === "true",
      allow_databases: vals.allow_databases === "true", allow_files: vals.allow_files === "true",
      allow_ftp: vals.allow_ftp === "true", allow_cron: vals.allow_cron === "true",
      is_default: vals.is_default === "true",
    };
    try {
      if (pkg) await api.patch("/packages/" + pkg.id, body);
      else await api.post("/packages", body);
      toast("Package saved");
      reload();
    } catch (ex) { toast(ex.message, "err"); }
  });
}

const yesNo = [{ value: "true", label: "Yes" }, { value: "false", label: "No" }];

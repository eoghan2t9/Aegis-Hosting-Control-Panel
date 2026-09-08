import { addRoute, isAdmin, refresh } from "../app.js";
import { api, qs } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, statusTag, pageHead, loading, fmtAgo, modal } from "../ui.js";

addRoute("/databases", {
  title: "Databases",
  icon: "database",
  group: "Data",
  order: 0,
  render: async (view) => {
    view.innerHTML = pageHead("Databases", "MariaDB and PostgreSQL. Aegis creates the database, a dedicated user and a strong random password for you.", `
      <button class="btn btn-primary" id="btn-db">${icon("plus")} New database</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="db-servers">${loading()}</div><div id="db-root" style="margin-top:16px"></div>`);

    const [servers, dbs] = await Promise.all([api.get("/databases/servers").catch(() => []), api.get("/databases").catch(() => [])]);
    const serverBox = document.getElementById("db-servers");
    serverBox.innerHTML = servers.length
      ? `<div style="display:flex;flex-direction:column;gap:6px;margin-bottom:4px">${servers.map((s) => `
          <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap">
            <span class="tag ${s.running ? "tag-lime" : "tag-amber"}">${icon("database", "")} ${esc(s.name)} ${s.running ? "" : "(down)"}</span>
            ${s.version ? `<span class="small dim" style="word-break:break-word">${esc(s.version)}</span>` : ""}
          </div>`).join("")}</div>`
      : `<p class="small dim">No database servers detected on this host (install mariadb-server and/or postgresql).</p>`;

    const root = document.getElementById("db-root");
    if (!dbs.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">▤</span><p>No databases yet.</p></div>`;
    } else {
      root.innerHTML = `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Name</th><th>Server</th><th>DB user</th><th>Created</th><th></th></tr></thead>
        <tbody>${dbs.map((d) => `
          <tr data-id="${d.id}">
            <td class="mono"><b>${esc(d.name)}</b></td>
            <td><span class="tag tag-teal">${esc(d.server)}</span></td>
            <td class="mono small">${esc(d.db_user)}</td>
            <td class="small dim">${fmtAgo(d.created_at)}</td>
            <td><div class="row-actions">
              <button class="btn btn-ghost act-cred" title="Credentials">${icon("key")}</button>
              <button class="btn btn-ghost act-dump" title="Download SQL dump">${icon("download")}</button>
              <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
            </div></td>
          </tr>`).join("")}</tbody></table></div>`;
    }

    root.querySelectorAll("tbody tr").forEach((tr) => {
      const row = dbs.find((d) => d.id === +tr.dataset.id);
      if (!row) return;
      tr.querySelector(".act-cred")?.addEventListener("click", () => credsModal(row));
      tr.querySelector(".act-dump")?.addEventListener("click", () => { location.href = "/api/databases/" + row.id + "/dump"; });
      tr.querySelector(".act-del")?.addEventListener("click", async () => {
        if (!await confirmDialog(`Drop database ${row.name} on ${row.server}? This deletes the data.`, { danger: true, title: "Drop database", okText: "Drop" })) return;
        try { await api.del("/databases/" + row.id); toast("Database dropped"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
    });

    document.getElementById("btn-db").onclick = async () => {
      if (!servers.length) return toast("No database server available", "warn");
      const running = servers.filter((s) => s.running);
      if (!running.length) return toast("Database servers are installed but not running", "warn");
      const vals = await promptDialog("New database", [
        { name: "server", label: "Server", type: "select", options: running.map((s) => ({ value: s.name, label: s.name })) },
        { name: "name", label: "Database name", mono: true, required: true, help: "lowercase letters, digits, underscores" },
      ]);
      if (!vals) return;
      try {
        const row = await api.post("/databases", vals);
        toast("Database created");
        credsModal(row, "just created");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    };
  },
});

function credsModal(row, note) {
  const host = location.hostname;
  const lines = [
    ["engine", row.server],
    ["host", row.server === "postgres" ? host : "127.0.0.1"],
    ["port", row.server === "postgres" ? "5432" : "3306"],
    ["database", row.name],
    ["user", row.db_user],
    ["password", row.db_password],
  ];
  const dsn = row.server === "postgres"
    ? `postgres://${row.db_user}:${row.db_password}@${host}/${row.name}`
    : `mysql://${row.db_user}:${row.db_password}@127.0.0.1:3306/${row.name}`;
  const close = btn("Close");
  const cm = modal({
    title: "Database credentials" + (row.name ? " — " + row.name : ""),
    body: `<div>
      <div class="creds-box">${lines.map(([k, v]) => `${k}: <b>${esc(v)}</b>`).join("<br>")}</div>
      ${note ? `<p class="small dim">This is the only time the password is shown in full — record it now or reset by deleting and recreating.</p>` : ""}
      <p class="small dim mono" style="word-break:break-all">dsn: ${esc(dsn)}</p>
    </div>`,
    actions: [close],
  });
  close.onclick = () => cm.close();
}

function btn(text) {
  const b = document.createElement("button");
  b.className = "btn";
  b.textContent = text;
  return b;
}

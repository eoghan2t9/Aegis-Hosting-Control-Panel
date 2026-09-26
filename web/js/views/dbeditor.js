import { addRoute } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, confirmDialog, promptDialog, modal, loading, fmtBytes } from "../ui.js";

// Database editor: a phpMyAdmin-style browser, row editor and SQL console for
// one of the user's MariaDB/MySQL or PostgreSQL databases. The server connects
// as the database's own user, so this page can only ever touch what that user
// can. All values travel as text (NULL as null), so nothing is rounded.

const OPS = [
  { v: "CONTAINS", label: "contains" }, { v: "=", label: "=" }, { v: "!=", label: "≠" },
  { v: "<", label: "<" }, { v: ">", label: ">" }, { v: "<=", label: "≤" }, { v: ">=", label: "≥" },
  { v: "LIKE", label: "LIKE" }, { v: "NOT LIKE", label: "NOT LIKE" },
  { v: "IS NULL", label: "is NULL" }, { v: "IS NOT NULL", label: "is not NULL" },
];
const HISTORY_KEY = "aegis.dbeditor.history";

addRoute("/dbeditor", {
  title: "Database editor",
  icon: "database",
  group: "Data",
  order: 9,
  feature: "databases",
  hidden: true, // opened from the Databases page, not the sidebar
  activeRoute: "/databases",
  render: renderEditor,
});

const isBinaryType = (t) => /blob|binary|bytea/i.test(t || "");
const isLongType = (t) => /text|json|xml|blob|clob/i.test(t || "");

function fmtCell(v) {
  if (v === null || v === undefined) return `<span class="dbe-null">NULL</span>`;
  if (typeof v === "boolean") return v ? "true" : "false";
  return esc(String(v));
}
const cellTitle = (v) => (v === null || v === undefined ? "NULL" : String(v).slice(0, 500));

function readHistory() {
  try { return JSON.parse(localStorage.getItem(HISTORY_KEY) || "[]"); } catch { return []; }
}
function pushHistory(sql) {
  try {
    const h = [sql, ...readHistory().filter((s) => s !== sql)].slice(0, 20);
    localStorage.setItem(HISTORY_KEY, JSON.stringify(h));
  } catch { /* storage unavailable: history is a convenience only */ }
}

async function renderEditor(view) {
  const id = +new URLSearchParams(location.hash.split("?")[1] || "").get("db");
  const dbs = id ? await api.get("/databases") : [];
  const db = dbs.find((d) => d.id === id);
  if (!db) {
    view.innerHTML = `<div class="card empty-state"><span class="glyph">▤</span><p>Choose a database to open in the editor.</p>
      <a class="btn btn-primary" href="#/databases">${icon("database")} Go to databases</a></div>`;
    return;
  }
  const base = `/dbeditor/${id}`;
  const st = {
    tables: [], table: null, structure: null, tab: "browse",
    page: 1, size: 50, sort: "", desc: false, filters: [], result: null,
  };

  view.innerHTML = `
    <div class="page-head">
      <div><h2>${icon("database")} ${esc(db.name)}</h2>
        <p><span class="tag tag-teal">${esc(db.server)}</span> <span class="mono small dim">${esc(db.db_user)}</span> · connected as this database's own user</p></div>
      <div class="page-actions"><a class="btn" href="#/databases">${icon("x")} Close editor</a></div>
    </div>
    <div class="dbe">
      <aside class="card dbe-side">
        <div class="dbe-side-head">
          <input type="search" id="dbe-find" placeholder="Filter tables…" class="mono" aria-label="Filter tables">
          <button class="btn btn-ghost btn-sm" id="dbe-refresh" title="Refresh table list" aria-label="Refresh">${icon("refresh")}</button>
        </div>
        <div id="dbe-tables" class="dbe-tables">${loading()}</div>
      </aside>
      <section class="card dbe-main">
        <div class="tab-row" id="dbe-tabs">
          <button class="tab-btn active" data-tab="browse">Browse</button>
          <button class="tab-btn" data-tab="structure">Structure</button>
          <button class="tab-btn" data-tab="sql">SQL</button>
        </div>
        <div id="dbe-body"></div>
      </section>
    </div>`;
  const $ = (sel) => view.querySelector(sel);

  // ---------------------------------------------------------------- tables
  async function loadTables(keep) {
    try { st.tables = await api.get(`${base}/tables`); }
    catch (ex) { $("#dbe-tables").innerHTML = `<p class="small dbe-err">${esc(ex.message)}</p>`; return; }
    if (!keep || !st.tables.some((t) => t.name === st.table)) {
      st.table = null; st.structure = null;
    }
    drawTables();
    drawBody();
  }
  function drawTables() {
    const q = ($("#dbe-find").value || "").toLowerCase();
    const list = st.tables.filter((t) => t.name.toLowerCase().includes(q));
    $("#dbe-tables").innerHTML = list.length ? list.map((t) => `
      <button class="dbe-table${t.name === st.table ? " active" : ""}" data-t="${esc(t.name)}">
        <span class="dbe-table-name mono">${icon(t.type === "view" ? "eye" : "database")} ${esc(t.name)}</span>
        <span class="dbe-table-meta small dim">${t.type === "view" ? "view" : "~" + Number(t.rows).toLocaleString() + " rows"} · ${fmtBytes(t.size_bytes)}</span>
      </button>`).join("")
      : `<p class="small dim" style="padding:10px">${st.tables.length ? "No matching tables." : "This database has no tables yet. Use the SQL tab to create one."}</p>`;
    $("#dbe-tables").querySelectorAll(".dbe-table").forEach((b) => b.onclick = () => selectTable(b.dataset.t));
  }
  async function selectTable(name) {
    st.table = name; st.page = 1; st.sort = ""; st.desc = false; st.filters = []; st.result = null;
    if (st.tab === "sql") st.tab = "browse";
    setTabs();
    drawTables();
    $("#dbe-body").innerHTML = loading();
    try { st.structure = await api.get(`${base}/tables/${encodeURIComponent(name)}`); }
    catch (ex) { $("#dbe-body").innerHTML = `<p class="dbe-err">${esc(ex.message)}</p>`; return; }
    drawBody();
    // On a phone the table list sits above the workspace; bring the workspace into view.
    if (window.matchMedia("(max-width: 860px)").matches) $(".dbe-main").scrollIntoView({ behavior: "smooth", block: "start" });
  }
  function setTabs() {
    view.querySelectorAll("#dbe-tabs .tab-btn").forEach((b) => b.classList.toggle("active", b.dataset.tab === st.tab));
  }
  view.querySelectorAll("#dbe-tabs .tab-btn").forEach((b) => b.onclick = () => { st.tab = b.dataset.tab; setTabs(); drawBody(); });
  $("#dbe-find").oninput = drawTables;
  $("#dbe-refresh").onclick = () => loadTables(true);

  function drawBody() {
    if (st.tab === "sql") return drawSQL();
    if (!st.table || !st.structure) {
      $("#dbe-body").innerHTML = `<div class="empty-state"><span class="glyph">▤</span><p>Select a table to browse it${st.tables.length ? "" : ", or use the SQL tab to create one"}.</p></div>`;
      return;
    }
    if (st.tab === "structure") return drawStructure();
    return loadRows();
  }

  // ---------------------------------------------------------------- grid
  function gridHTML(res, opts = {}) {
    const cols = res.columns || [];
    if (!cols.length) return "";
    const head = cols.map((c) => {
      const active = opts.sortable && st.sort === c.name;
      return `<th class="${opts.sortable ? "dbe-sortable" : ""}" data-col="${esc(c.name)}" title="${esc(c.type)}">${esc(c.name)}${active ? (st.desc ? " ▼" : " ▲") : ""}</th>`;
    }).join("");
    const body = (res.rows || []).map((r, i) => `<tr data-i="${i}">${r.map((v) =>
      `<td class="mono" title="${esc(cellTitle(v))}">${fmtCell(v)}</td>`).join("")}${opts.actions ? `<td class="dbe-actions">
        <button class="btn btn-ghost btn-xs dbe-edit" title="Edit row" aria-label="Edit row">${icon("edit")}</button>
        <button class="btn btn-ghost btn-xs dbe-del" title="Delete row" aria-label="Delete row">${icon("trash")}</button></td>` : ""}</tr>`).join("");
    return `<div class="dbe-gridwrap"><table class="dbe-grid"><thead><tr>${head}${opts.actions ? "<th></th>" : ""}</tr></thead><tbody>${body}</tbody></table></div>`;
  }

  // ---------------------------------------------------------------- browse
  async function loadRows() {
    const box = $("#dbe-body");
    box.innerHTML = loading();
    const qs = new URLSearchParams({ page: st.page, page_size: st.size });
    if (st.sort) { qs.set("sort", st.sort); qs.set("dir", st.desc ? "desc" : "asc"); }
    if (st.filters.length) qs.set("filters", JSON.stringify(st.filters));
    let res;
    try { res = await api.get(`${base}/tables/${encodeURIComponent(st.table)}/rows?${qs}`); }
    catch (ex) { box.innerHTML = `${toolbarHTML()}<p class="dbe-err">${esc(ex.message)}</p>`; bindToolbar(); return; }
    st.result = res;
    const editable = st.structure.type !== "view" && st.structure.primary_key.length > 0;
    const pages = res.total >= 0 ? Math.max(1, Math.ceil(res.total / st.size)) : null;
    box.innerHTML = `${toolbarHTML()}
      ${filterChips()}
      ${res.rows.length ? gridHTML(res, { sortable: true, actions: editable }) : `<div class="empty-state"><p>${st.filters.length ? "No rows match these filters." : "This table is empty."}</p></div>`}
      ${!editable && st.structure.type !== "view" ? `<p class="small dim">Rows can't be edited here because this table has no primary key. Use the SQL tab.</p>` : ""}
      <div class="dbe-pager">
        <button class="btn btn-sm" id="dbe-prev" ${st.page <= 1 ? "disabled" : ""}>‹ Prev</button>
        <span class="small dim">Page ${st.page}${pages ? " of " + pages : ""} · ${res.total >= 0 ? Number(res.total).toLocaleString() + " rows" : res.rows.length + " shown"} · ${res.elapsed_ms} ms</span>
        <button class="btn btn-sm" id="dbe-next" ${(pages ? st.page >= pages : res.rows.length < st.size) ? "disabled" : ""}>Next ›</button>
      </div>`;
    bindToolbar();
    $("#dbe-prev").onclick = () => { st.page--; loadRows(); };
    $("#dbe-next").onclick = () => { st.page++; loadRows(); };
    box.querySelectorAll("th.dbe-sortable").forEach((th) => th.onclick = () => {
      const c = th.dataset.col;
      if (st.sort === c) st.desc = !st.desc; else { st.sort = c; st.desc = false; }
      st.page = 1; loadRows();
    });
    box.querySelectorAll("tbody tr").forEach((tr) => {
      const row = res.rows[+tr.dataset.i];
      tr.querySelector(".dbe-edit")?.addEventListener("click", () => openRowForm("edit", res, row));
      tr.querySelector(".dbe-del")?.addEventListener("click", () => deleteRow(res, row));
    });
  }
  function toolbarHTML() {
    const t = st.structure;
    const canWrite = t.type !== "view";
    return `<div class="dbe-toolbar">
      <div class="dbe-filter">
        <select id="f-col" aria-label="Filter column">${t.columns.map((c) => `<option value="${esc(c.name)}">${esc(c.name)}</option>`).join("")}</select>
        <select id="f-op" aria-label="Filter operator">${OPS.map((o) => `<option value="${esc(o.v)}">${esc(o.label)}</option>`).join("")}</select>
        <input type="text" id="f-val" placeholder="value" class="mono" aria-label="Filter value" autocapitalize="off" autocomplete="off">
        <button class="btn btn-sm" id="f-add">${icon("plus")} Filter</button>
      </div>
      <div class="dbe-toolbar-right">
        <select id="dbe-size" aria-label="Rows per page">${[25, 50, 100, 200].map((n) => `<option value="${n}" ${n === st.size ? "selected" : ""}>${n} / page</option>`).join("")}</select>
        ${canWrite ? `<button class="btn btn-sm btn-primary" id="dbe-insert">${icon("plus")} Insert row</button>` : ""}
      </div></div>`;
  }
  function filterChips() {
    if (!st.filters.length) return "";
    return `<div class="dbe-chips">${st.filters.map((f, i) => `<span class="tag tag-teal dbe-chip">${esc(f.column)} ${esc((OPS.find((o) => o.v === f.op) || {}).label || f.op)}${/NULL$/.test(f.op) ? "" : " " + esc(f.value)}
      <button class="dbe-chip-x" data-i="${i}" title="Remove filter" aria-label="Remove filter">×</button></span>`).join("")}</div>`;
  }
  function bindToolbar() {
    const op = $("#f-op"), val = $("#f-val");
    if (!op) return;
    const sync = () => { val.disabled = /NULL$/.test(op.value); if (val.disabled) val.value = ""; };
    op.onchange = sync; sync();
    const add = () => {
      st.filters.push({ column: $("#f-col").value, op: op.value, value: val.value });
      st.page = 1; loadRows();
    };
    $("#f-add").onclick = add;
    val.onkeydown = (e) => { if (e.key === "Enter") add(); };
    $("#dbe-size").onchange = (e) => { st.size = +e.target.value; st.page = 1; loadRows(); };
    $("#dbe-insert")?.addEventListener("click", () => openRowForm("insert"));
    view.querySelectorAll(".dbe-chip-x").forEach((b) => b.onclick = () => { st.filters.splice(+b.dataset.i, 1); st.page = 1; loadRows(); });
  }

  // ---------------------------------------------------------------- row edit
  const keyOf = (res, row) => Object.fromEntries(st.structure.primary_key.map((k) => [k, row[res.columns.findIndex((c) => c.name === k)]]));

  async function deleteRow(res, row) {
    const key = keyOf(res, row);
    const label = Object.entries(key).map(([k, v]) => `${k} = ${v}`).join(", ");
    if (!await confirmDialog(`Delete the row where ${label}? This cannot be undone.`, { danger: true, title: "Delete row", okText: "Delete" })) return;
    try {
      await api.post(`${base}/tables/${encodeURIComponent(st.table)}/rows/delete`, { key });
      toast("Row deleted");
      loadRows();
    } catch (ex) { toast(ex.message, "err"); }
  }

  async function openRowForm(mode, res, row) {
    const t = st.structure;
    let orig = {};
    if (mode === "edit") {
      try { orig = await api.get(`${base}/tables/${encodeURIComponent(st.table)}/row?key=${encodeURIComponent(JSON.stringify(keyOf(res, row)))}`); }
      catch (ex) { toast(ex.message, "err"); return; }
    }
    const fields = t.columns.map((c, i) => {
      const bin = isBinaryType(c.type);
      const isNull = mode === "edit" ? orig[c.name] === null : false;
      const useDefault = mode === "insert" && (c.default !== null || c.extra !== "");
      const cur = mode === "edit" && !isNull ? String(orig[c.name] ?? "") : "";
      const disabled = bin || isNull || useDefault;
      const input = isLongType(c.type)
        ? `<textarea class="dbe-in mono" rows="3" ${disabled ? "disabled" : ""} spellcheck="false" autocapitalize="off">${esc(cur)}</textarea>`
        : `<input type="text" class="dbe-in mono" ${disabled ? "disabled" : ""} value="${esc(cur)}" spellcheck="false" autocapitalize="off" autocomplete="off">`;
      return `<div class="dbe-field" data-i="${i}">
        <div class="dbe-field-head"><b class="mono">${esc(c.name)}</b> <span class="small dim">${esc(c.type)}</span>
          ${c.key === "PRI" ? `<span class="tag tag-lime">PK</span>` : ""}${c.extra ? `<span class="tag">${esc(c.extra)}</span>` : ""}${c.nullable ? "" : `<span class="tag">required</span>`}</div>
        ${bin ? `<p class="small dim">Binary data is shown as hex and can't be edited here — use the SQL tab.</p>` : input}
        ${bin ? "" : `<div class="dbe-field-opts">
          ${c.nullable ? `<label class="checkline"><input type="checkbox" class="dbe-null" ${isNull ? "checked" : ""}> NULL</label>` : ""}
          ${mode === "insert" ? `<label class="checkline"><input type="checkbox" class="dbe-def" ${useDefault ? "checked" : ""}> use default</label>` : ""}
        </div>`}</div>`;
    }).join("");
    const cancel = document.createElement("button"); cancel.className = "btn"; cancel.textContent = "Cancel";
    const save = document.createElement("button"); save.className = "btn btn-primary"; save.textContent = mode === "edit" ? "Save changes" : "Insert row";
    const m = modal({ title: `${mode === "edit" ? "Edit row" : "Insert row"} — ${st.table}`, wide: true, body: `<div class="dbe-form">${fields}</div>`, actions: [cancel, save] });
    cancel.onclick = () => m.close();

    const box = document.querySelector(".dbe-form");
    box.querySelectorAll(".dbe-field").forEach((f) => {
      const input = f.querySelector(".dbe-in"), nul = f.querySelector(".dbe-null"), def = f.querySelector(".dbe-def");
      const sync = () => { if (input) input.disabled = !!(nul?.checked || def?.checked); };
      nul?.addEventListener("change", () => { if (nul.checked && def) def.checked = false; sync(); });
      def?.addEventListener("change", () => { if (def.checked && nul) nul.checked = false; sync(); });
    });
    save.onclick = async () => {
      const values = {};
      box.querySelectorAll(".dbe-field").forEach((f) => {
        const c = t.columns[+f.dataset.i];
        const input = f.querySelector(".dbe-in");
        if (!input || isBinaryType(c.type)) return;
        const nul = f.querySelector(".dbe-null")?.checked, def = f.querySelector(".dbe-def")?.checked;
        if (mode === "insert") {
          if (def) return;
          values[c.name] = nul ? null : input.value;
        } else {
          const was = orig[c.name];
          if (nul) { if (was !== null) values[c.name] = null; }
          else if (was === null || String(was) !== input.value) values[c.name] = input.value;
        }
      });
      try {
        if (mode === "edit") {
          if (!Object.keys(values).length) { toast("Nothing changed", "warn"); return; }
          await api.put(`${base}/tables/${encodeURIComponent(st.table)}/rows`, { key: keyOf(res, row), values });
          toast("Row updated");
        } else {
          await api.post(`${base}/tables/${encodeURIComponent(st.table)}/rows`, { values });
          toast("Row inserted");
        }
        m.close();
        loadRows();
      } catch (ex) { toast(ex.message, "err"); }
    };
  }

  // ---------------------------------------------------------------- structure
  function drawStructure() {
    const t = st.structure;
    const cols = t.columns.map((c) => `<tr>
      <td class="mono"><b>${esc(c.name)}</b></td><td class="mono small">${esc(c.type)}</td>
      <td>${c.nullable ? "yes" : "no"}</td>
      <td>${c.key ? `<span class="tag ${c.key === "PRI" ? "tag-lime" : ""}">${esc(c.key)}</span>` : ""}</td>
      <td class="mono small">${c.default === null ? '<span class="dim">—</span>' : esc(c.default)}</td>
      <td class="small">${esc(c.extra || "")}</td><td class="small dim">${esc(c.comment || "")}</td></tr>`).join("");
    const idx = t.indexes.map((i) => `<tr><td class="mono"><b>${esc(i.name)}</b></td><td class="mono small">${esc(i.columns.join(", "))}</td>
      <td>${i.primary ? '<span class="tag tag-lime">primary</span>' : i.unique ? '<span class="tag tag-teal">unique</span>' : ""}</td><td class="small dim">${esc(i.method || "")}</td></tr>`).join("");
    const fks = t.foreign_keys.map((f) => `<tr><td class="mono"><b>${esc(f.name)}</b></td><td class="mono small">${esc(f.columns.join(", "))}</td>
      <td class="mono small">${esc(f.ref_table)} (${esc(f.ref_columns.join(", "))})</td></tr>`).join("");
    $("#dbe-body").innerHTML = `
      <div class="dbe-struct-head"><h3 class="mono">${esc(t.table)} ${t.type === "view" ? '<span class="tag">view</span>' : ""}</h3>
        <div class="dbe-struct-actions">
          <button class="btn btn-sm" id="dbe-browse">${icon("list")} Browse</button>
          ${t.type === "view" ? "" : `<button class="btn btn-sm btn-warn" id="dbe-trunc">${icon("trash")} Empty table</button>`}
          <button class="btn btn-sm btn-danger" id="dbe-drop">${icon("x")} Drop ${t.type === "view" ? "view" : "table"}</button>
        </div></div>
      <div class="tbl-wrap"><table class="tbl"><thead><tr><th>Column</th><th>Type</th><th>Null</th><th>Key</th><th>Default</th><th>Extra</th><th>Comment</th></tr></thead><tbody>${cols}</tbody></table></div>
      <h4 class="dbe-h">Indexes</h4>
      ${idx ? `<div class="tbl-wrap"><table class="tbl"><thead><tr><th>Name</th><th>Columns</th><th>Type</th><th>Method</th></tr></thead><tbody>${idx}</tbody></table></div>` : `<p class="small dim">No indexes.</p>`}
      <h4 class="dbe-h">Foreign keys</h4>
      ${fks ? `<div class="tbl-wrap"><table class="tbl"><thead><tr><th>Name</th><th>Columns</th><th>References</th></tr></thead><tbody>${fks}</tbody></table></div>` : `<p class="small dim">No foreign keys.</p>`}`;
    $("#dbe-browse").onclick = () => { st.tab = "browse"; setTabs(); drawBody(); };
    const destroy = async (kind) => {
      const vals = await promptDialog(kind === "drop" ? `Drop ${t.type} ${t.table}` : `Empty table ${t.table}`, [
        { name: "confirm", label: `Type the name to confirm: ${t.table}`, mono: true, required: true,
          help: kind === "drop" ? "This permanently deletes the table and all its data." : "This permanently deletes every row in the table." },
      ]);
      if (!vals) return;
      try {
        await api.post(`${base}/tables/${encodeURIComponent(t.table)}/${kind === "drop" ? "drop" : "truncate"}`, { confirm: vals.confirm });
        toast(kind === "drop" ? "Dropped" : "Table emptied");
        if (kind === "drop") { st.table = null; st.structure = null; await loadTables(false); } else { drawTables(); }
      } catch (ex) { toast(ex.message, "err"); }
    };
    $("#dbe-trunc")?.addEventListener("click", () => destroy("truncate"));
    $("#dbe-drop").onclick = () => destroy("drop");
  }

  // ---------------------------------------------------------------- SQL
  let lastSQL = "";
  function drawSQL() {
    const hist = readHistory();
    $("#dbe-body").innerHTML = `
      <div class="dbe-sql">
        <textarea id="sql-in" class="mono" rows="7" spellcheck="false" autocapitalize="off" autocomplete="off" aria-label="SQL statement"
          placeholder="One statement at a time — Ctrl/⌘ + Enter to run">${esc(lastSQL)}</textarea>
        <div class="dbe-sql-bar">
          <button class="btn btn-primary" id="sql-run">${icon("play")} Run</button>
          <select id="sql-limit" aria-label="Row limit">${[100, 500, 1000].map((n) => `<option value="${n}" ${n === 1000 ? "selected" : ""}>max ${n} rows</option>`).join("")}</select>
          ${hist.length ? `<select id="sql-hist" aria-label="History"><option value="">History…</option>${hist.map((h, i) => `<option value="${i}">${esc(h.replace(/\s+/g, " ").slice(0, 70))}</option>`).join("")}</select>` : ""}
          ${st.table ? `<button class="btn btn-ghost btn-sm" id="sql-sel">SELECT * FROM ${esc(st.table)}</button>` : ""}
        </div>
        <div id="sql-out"></div>
      </div>`;
    const inp = $("#sql-in");
    inp.oninput = () => { lastSQL = inp.value; };
    inp.onkeydown = (e) => { if ((e.ctrlKey || e.metaKey) && e.key === "Enter") { e.preventDefault(); run(); } };
    $("#sql-run").onclick = run;
    $("#sql-hist")?.addEventListener("change", (e) => { if (e.target.value !== "") { inp.value = lastSQL = hist[+e.target.value]; e.target.value = ""; inp.focus(); } });
    $("#sql-sel")?.addEventListener("click", () => { inp.value = lastSQL = `SELECT * FROM ${st.table} LIMIT 100`; inp.focus(); });
  }
  async function run() {
    const sql = $("#sql-in").value.trim();
    const out = $("#sql-out");
    if (!sql) { toast("Enter a SQL statement", "warn"); return; }
    const btn = $("#sql-run");
    btn.disabled = true;
    out.innerHTML = loading();
    try {
      const res = await api.post(`${base}/query`, { sql, limit: +$("#sql-limit").value });
      pushHistory(sql);
      if (res.columns.length) {
        out.innerHTML = `<p class="small dim dbe-meta">${res.rows.length} row${res.rows.length === 1 ? "" : "s"} · ${res.elapsed_ms} ms${res.truncated ? " · <b>result truncated</b> (raise the row limit or add LIMIT)" : ""}</p>
          ${res.rows.length ? gridHTML(res) : `<div class="empty-state"><p>No rows returned.</p></div>`}`;
      } else {
        out.innerHTML = `<p class="dbe-ok">${icon("check")} Statement executed · ${Number(res.affected).toLocaleString()} row${res.affected === 1 ? "" : "s"} affected · ${res.elapsed_ms} ms</p>`;
        if (/^\s*(create|alter|drop|rename|truncate)\b/i.test(sql)) loadTables(true);
      }
    } catch (ex) {
      out.innerHTML = `<p class="dbe-err">${esc(ex.message)}</p>`;
    } finally { btn.disabled = false; }
  }

  await loadTables(false);
}

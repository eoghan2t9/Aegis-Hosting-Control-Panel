import { api } from "../api.js";
import { icon, esc, toast, confirmDialog, promptDialog, modal, loading } from "../ui.js";

// The database editor's heavier tools: schema editing (with an SQL preview),
// import/export, search, stored objects and the cell viewer. dbeditor.js hands
// each one a context `x`: { base, st, info, $, gridHTML, selectTable, refreshStructure, reloadTables, setTab }.
// The server generates every DDL statement from these structured requests; this
// page never sends SQL for schema changes.

const btn = (text, cls = "btn") => { const b = document.createElement("button"); b.className = cls; b.textContent = text; return b; };
const el = (html) => { const d = document.createElement("div"); d.innerHTML = html.trim(); return d.firstElementChild; };
const nameOk = (s) => typeof s === "string" && s.length > 0;

// ------------------------------------------------------------------ files

export function saveBlob(blob, name) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url; a.download = name; a.style.display = "none";
  document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
}

// download fetches a file with the panel's bearer token (a plain link could not
// send it) and saves it. A failed or interrupted export is reported, never
// saved as if it were complete.
export async function download(path, fallbackName) {
  let res;
  try { res = await api.get(path, true); } catch (ex) { toast(ex.message, "err"); return false; }
  if (!res.ok) {
    let msg = "The export failed";
    try { msg = (await res.json()).error || msg; } catch { /* not JSON */ }
    toast(msg, "err");
    return false;
  }
  let blob;
  try { blob = await res.blob(); } catch { toast("The download was interrupted, so nothing was saved", "err"); return false; }
  const m = /filename="([^"]+)"/.exec(res.headers.get("Content-Disposition") || "");
  saveBlob(blob, m ? m[1] : fallbackName);
  return true;
}

function csvOf(res) {
  const q = (v) => {
    if (v === null || v === undefined) return "";
    const s = String(v);
    return /[",\r\n]/.test(s) ? '"' + s.replace(/"/g, '""') + '"' : s;
  };
  return [res.columns.map((c) => q(c.name)).join(","), ...res.rows.map((r) => r.map(q).join(","))].join("\r\n") + "\r\n";
}
export function downloadResultCSV(res, name) {
  saveBlob(new Blob(["﻿" + csvOf(res)], { type: "text/csv;charset=utf-8" }), name + ".csv");
}

async function copyText(text) {
  try { await navigator.clipboard.writeText(text); toast("Copied"); }
  catch { toast("Copy is not available here — select the text and copy it", "warn"); }
}

// ------------------------------------------------------------------ cell viewer

export function cellViewer(colName, type, value) {
  const text = value === null || value === undefined ? "NULL" : String(value);
  let pretty = null;
  if (value !== null && /^\s*[[{]/.test(text)) { try { pretty = JSON.stringify(JSON.parse(text), null, 2); } catch { /* not JSON */ } }
  const body = el(`<div><p class="small dim">${esc(colName)} · ${esc(type)} · ${text.length.toLocaleString()} characters</p><pre class="dbe-pre"></pre></div>`);
  const pre = body.querySelector("pre");
  let showPretty = !!pretty;
  const toggle = pretty ? btn("") : null;
  const draw = () => { pre.textContent = showPretty ? pretty : text; if (toggle) toggle.textContent = showPretty ? "Show raw" : "Pretty-print"; };
  const copy = btn("Copy"), close = btn("Close", "btn btn-primary");
  const m = modal({ title: "Value", wide: true, body, actions: [copy, ...(toggle ? [toggle] : []), close] });
  draw();
  copy.onclick = () => copyText(showPretty ? pretty : text);
  if (toggle) toggle.onclick = () => { showPretty = !showPretty; draw(); };
  close.onclick = () => m.close();
}

// ------------------------------------------------------------------ schema dialog

// schemaDialog shows a form, previews the statements the server would run and
// applies them. build() returns the structured request or throws a message.
function schemaDialog(x, { title, body, build, okText = "Apply", danger = false, done, blocked }) {
  const cancel = btn("Cancel"), prev = btn("Preview SQL"), ok = btn(okText, danger ? "btn btn-danger" : "btn btn-primary");
  const box = el(`<div></div>`);
  box.appendChild(body);
  const out = el(`<div class="dbe-preview-box"></div>`);
  box.appendChild(out);
  const m = modal({ title, wide: true, body: box, actions: [cancel, prev, ok] });
  cancel.onclick = () => m.close();
  if (blocked) { ok.disabled = prev.disabled = true; }
  const send = async (preview) => {
    let req;
    try { req = build(); } catch (ex) { toast(ex.message, "warn"); return; }
    if (!req) return;
    ok.disabled = prev.disabled = true;
    try {
      const res = await api.post(`${x.base}/schema`, { ...req, preview });
      if (preview) {
        out.innerHTML = `<h4 class="dbe-h">SQL that will run</h4><pre class="dbe-pre">${esc(res.statements.join(";\n") + ";")}</pre>`;
      } else {
        toast("Change applied");
        m.close();
        await done?.(res);
      }
    } catch (ex) {
      out.innerHTML = `<p class="dbe-err">${esc(ex.message)}</p>`;
      if (!preview) await x.refreshStructure?.(); // MariaDB may have applied part of it
    } finally { if (!blocked) ok.disabled = prev.disabled = false; }
  };
  prev.onclick = () => send(true);
  ok.onclick = () => send(false);
  return m;
}

// ------------------------------------------------------------------ column form

function parseType(t, eng, types) {
  t = (t || "").trim();
  const out = { type: "", length: "", values: [], unsigned: false };
  let m;
  if (eng === "postgres") {
    m = /^([a-z ]+?)(?:\((.*?)\))?( with(?:out)? time zone)?$/i.exec(t);
    if (!m) return out;
    out.type = (m[1] + (m[3] || "")).trim().toLowerCase();
    out.length = m[2] || "";
  } else {
    m = /^([a-z]+)(?:\((.*)\))?\s*(unsigned)?\s*(zerofill)?$/i.exec(t);
    if (!m) return out;
    out.type = m[1].toLowerCase();
    out.length = m[2] || "";
    out.unsigned = !!m[3];
    if (out.type === "enum" || out.type === "set") {
      out.values = [...(m[2] || "").matchAll(/'((?:[^']|'')*)'/g)].map((v) => v[1].replace(/''/g, "'"));
      out.length = "";
    }
  }
  const info = types.find((x) => x.name === out.type);
  if (info && !info.length) out.length = ""; // e.g. int(11)'s display width is not managed here
  return out;
}

function parseDefault(col, eng) {
  const raw = col.default;
  if (raw === null || raw === undefined) return { mode: "none", value: "" };
  let m;
  if (eng === "postgres") {
    if (/^nextval\(/i.test(raw)) return { mode: "none", value: "" };
    if ((m = /^'((?:[^']|'')*)'(::[\w ]+(\(\d+\))?)*$/s.exec(raw))) return { mode: "value", value: m[1].replace(/''/g, "'") };
    if (/^(now\(\)|current_timestamp|localtimestamp)/i.test(raw)) return { mode: "current_timestamp", value: "" };
    if (/^(-?\d+(\.\d+)?|true|false)$/i.test(raw)) return { mode: "value", value: raw.toLowerCase() };
    if (/^null(::.*)?$/i.test(raw)) return { mode: "null", value: "" };
    return { mode: "none", value: "", lossy: raw };
  }
  if (/^null$/i.test(raw)) return { mode: "null", value: "" };
  if ((m = /^'((?:[^']|'')*)'$/s.exec(raw))) return { mode: "value", value: m[1].replace(/''/g, "'") };
  if (/^current_timestamp(\(\d*\))?$/i.test(raw)) return { mode: "current_timestamp", value: "" };
  if (/^-?\d+(\.\d+)?$/.test(raw)) return { mode: "value", value: raw };
  if (/^[\w .-]+$/.test(raw)) return { mode: "value", value: raw }; // MySQL 8 reports string defaults unquoted
  return { mode: "none", value: "", lossy: raw };
}

// columnForm builds the fields for one column. read() returns the request's
// column object; the server validates every part of it again.
function columnForm(x, { col = null, position = false, compact = false } = {}) {
  const eng = x.info.engine, types = x.info.types;
  const pt = col ? parseType(col.type, eng, types) : null;
  const pd = col ? parseDefault(col, eng) : { mode: "none", value: "" };
  const known = !col || types.some((t) => t.name === pt.type);
  const d = {
    name: col ? col.name : "",
    type: col ? pt.type : (types.find((t) => t.name === "varchar" || t.name === "character varying") || types[0]).name,
    length: col ? pt.length : "", values: col ? pt.values : [], unsigned: col ? pt.unsigned : false,
    nullable: col ? col.nullable : true, auto: !!(col && col.extra === "auto_increment"), comment: col ? col.comment || "" : "",
  };
  const posOpts = position && eng !== "postgres" ? `
    <label class="field"><span class="field-label">Position</span><select class="cf-pos">
      <option value="">At the end</option><option value="__first">At the start</option>
      ${x.st.structure.columns.map((c) => `<option value="${esc(c.name)}">After ${esc(c.name)}</option>`).join("")}</select></label>` : "";
  const root = el(`<div class="dbe-colform ${compact ? "compact" : ""}">
    ${known ? "" : `<p class="dbe-err">This column's type (${esc(col.type)}) can't be edited here. Use the SQL tab to change it.</p>`}
    <label class="field"><span class="field-label">Name</span><input class="cf-name mono" maxlength="64" spellcheck="false" autocapitalize="off" autocomplete="off"></label>
    <div class="dbe-row2">
      <label class="field"><span class="field-label">Type</span><select class="cf-type">${types.map((t) => `<option value="${esc(t.name)}">${esc(t.name)}</option>`).join("")}</select></label>
      <label class="field cf-lenwrap"><span class="field-label cf-lenlabel">Length</span><input class="cf-len mono" autocomplete="off" spellcheck="false"></label>
    </div>
    <label class="field cf-valwrap"><span class="field-label">Values <span class="dim">(one per line)</span></span><textarea class="cf-values mono" rows="3" spellcheck="false"></textarea></label>
    <div class="dbe-checks">
      <label class="checkline"><input type="checkbox" class="cf-null"> Allow NULL</label>
      <label class="checkline cf-unswrap"><input type="checkbox" class="cf-unsigned"> Unsigned</label>
      <label class="checkline cf-autowrap"><input type="checkbox" class="cf-auto"> Auto-increment</label>
    </div>
    <div class="dbe-row2">
      <label class="field"><span class="field-label">Default</span><select class="cf-def">
        <option value="none">No default</option><option value="null">NULL</option><option value="value">A value…</option><option value="current_timestamp" class="cf-ts">Current time</option></select></label>
      <label class="field cf-defvalwrap"><span class="field-label">Default value</span><input class="cf-defval mono" autocomplete="off" spellcheck="false"></label>
    </div>
    <label class="field"><span class="field-label">Comment <span class="dim">(optional)</span></span><input class="cf-comment" maxlength="1000" autocomplete="off"></label>
    ${posOpts}
    ${pd.lossy ? `<p class="small dbe-warn">The current default <code>${esc(pd.lossy)}</code> can't be edited here and will be removed if you save this column.</p>` : ""}
  </div>`);
  const q = (s) => root.querySelector(s);
  q(".cf-name").value = d.name;
  q(".cf-type").value = known ? d.type : types[0].name;
  q(".cf-len").value = d.length;
  q(".cf-values").value = d.values.join("\n");
  q(".cf-null").checked = d.nullable;
  q(".cf-unsigned").checked = d.unsigned;
  q(".cf-auto").checked = d.auto;
  q(".cf-def").value = pd.mode;
  q(".cf-defval").value = pd.value;
  q(".cf-comment").value = d.comment;

  const sync = () => {
    const info = types.find((t) => t.name === q(".cf-type").value) || {};
    q(".cf-lenwrap").style.display = info.length ? "" : "none";
    q(".cf-lenlabel").textContent = info.length === "prec" ? "Precision (digits, decimals)" : info.length === "fsp" ? "Fractional seconds (0–6)" : info.must ? "Length (required)" : "Length";
    q(".cf-valwrap").style.display = info.cat === "enum" ? "" : "none";
    q(".cf-unswrap").style.display = eng !== "postgres" && (info.cat === "int" || info.cat === "num") && info.name !== "year" ? "" : "none";
    const canAuto = info.cat === "int" && info.name !== "year";
    q(".cf-autowrap").style.display = canAuto ? "" : "none";
    if (!canAuto) q(".cf-auto").checked = false;
    // PostgreSQL cannot turn auto-increment on or off for an existing column.
    q(".cf-auto").disabled = eng === "postgres" && !!col;
    const stamp = info.cat === "time" && /stamp|datetime/.test(info.name);
    q(".cf-ts").disabled = !stamp; q(".cf-ts").hidden = !stamp;
    if (!stamp && q(".cf-def").value === "current_timestamp") q(".cf-def").value = "none";
    const auto = q(".cf-auto").checked;
    if (auto) q(".cf-null").checked = false;
    q(".cf-null").disabled = auto;
    q(".cf-def").disabled = auto;
    q(".cf-defvalwrap").style.display = !auto && q(".cf-def").value === "value" ? "" : "none";
  };
  root.addEventListener("change", sync);
  sync();

  return {
    el: root, blocked: !known,
    read() {
      const info = types.find((t) => t.name === q(".cf-type").value) || {};
      const auto = q(".cf-auto").checked;
      return {
        name: q(".cf-name").value,
        type: info.name,
        length: info.length ? q(".cf-len").value.trim() : "",
        values: info.cat === "enum" ? q(".cf-values").value.split("\n").map((s) => s.replace(/\r$/, "")).filter((s) => s !== "") : [],
        unsigned: q(".cf-unsigned").checked && q(".cf-unswrap").style.display !== "none",
        nullable: q(".cf-null").checked,
        auto_increment: auto,
        default: auto ? "none" : q(".cf-def").value,
        default_value: q(".cf-defval").value,
        comment: q(".cf-comment").value,
      };
    },
    position() {
      const v = q(".cf-pos")?.value || "";
      return v === "__first" ? { first: true } : v ? { after: v } : {};
    },
  };
}

// ------------------------------------------------------------------ structure operations

export function addColumn(x) {
  const f = columnForm(x, { position: true });
  const t = x.st.structure.table;
  schemaDialog(x, {
    title: `Add column — ${t}`, body: f.el, okText: "Add column",
    build: () => ({ action: "add_column", table: t, column: f.read(), ...f.position() }),
    done: () => x.refreshStructure(),
  });
}

export function editColumn(x, col) {
  const f = columnForm(x, { col });
  const t = x.st.structure.table;
  schemaDialog(x, {
    title: `Edit column ${col.name} — ${t}`, body: f.el, okText: "Save column", blocked: f.blocked,
    build: () => ({ action: "modify_column", table: t, name: col.name, column: f.read() }),
    done: () => x.refreshStructure(),
  });
}

export async function dropColumn(x, col) {
  const t = x.st.structure.table;
  const vals = await promptDialog(`Drop column ${col.name}`, [
    { name: "confirm", label: `Type the column name to confirm: ${col.name}`, mono: true, required: true, help: "This permanently deletes the column and all its data." },
  ]);
  if (!vals) return;
  try {
    await api.post(`${x.base}/schema`, { action: "drop_column", table: t, name: col.name, confirm: vals.confirm });
    toast("Column dropped");
    await x.refreshStructure();
  } catch (ex) { toast(ex.message, "err"); }
}

function checkList(names, cls, checked = []) {
  return `<div class="dbe-checklist">${names.map((n) => `<label class="checkline"><input type="checkbox" class="${cls}" value="${esc(n)}" ${checked.includes(n) ? "checked" : ""}> <span class="mono">${esc(n)}</span></label>`).join("")}</div>`;
}

export function addIndex(x) {
  const t = x.st.structure;
  const body = el(`<div>
    <label class="field"><span class="field-label">Index name</span><input class="ix-name mono" maxlength="64" value="${esc("idx_" + t.table)}" spellcheck="false" autocapitalize="off"></label>
    <span class="field-label">Columns <span class="dim">(in this order)</span></span>${checkList(t.columns.map((c) => c.name), "ix-col")}
    <label class="checkline" style="margin-top:10px"><input type="checkbox" class="ix-unique"> Unique</label></div>`);
  schemaDialog(x, {
    title: `Add index — ${t.table}`, body, okText: "Add index",
    build: () => {
      const cols = [...body.querySelectorAll(".ix-col:checked")].map((c) => c.value);
      if (!cols.length) throw new Error("Choose at least one column");
      return { action: "add_index", table: t.table, name: body.querySelector(".ix-name").value, key_columns: cols, unique: body.querySelector(".ix-unique").checked };
    },
    done: () => x.refreshStructure(),
  });
}

export async function dropIndex(x, ix) {
  if (!await confirmDialog(`Drop the index ${ix.name}? Queries that used it may get slower.`, { danger: true, title: "Drop index", okText: "Drop" })) return;
  try {
    await api.post(`${x.base}/schema`, { action: "drop_index", table: x.st.structure.table, name: ix.name });
    toast("Index dropped");
    await x.refreshStructure();
  } catch (ex) { toast(ex.message, "err"); }
}

export function addForeignKey(x) {
  const t = x.st.structure;
  const refTables = x.st.tables.filter((tb) => tb.type === "table").map((tb) => tb.name);
  if (!refTables.length) { toast("There are no tables to reference", "warn"); return; }
  const refCache = {};
  const body = el(`<div>
    <label class="field"><span class="field-label">Constraint name</span><input class="fk-name mono" maxlength="64" spellcheck="false" autocapitalize="off"></label>
    <label class="field"><span class="field-label">References table</span><select class="fk-ref">${refTables.map((n) => `<option value="${esc(n)}">${esc(n)}</option>`).join("")}</select></label>
    <span class="field-label">Columns</span><div class="fk-pairs"></div>
    <button class="btn btn-sm fk-add" type="button">${icon("plus")} Add column pair</button>
    <div class="dbe-row2" style="margin-top:12px">
      <label class="field"><span class="field-label">On delete</span><select class="fk-del"><option value="">Default</option><option>RESTRICT</option><option>CASCADE</option><option>SET NULL</option><option>NO ACTION</option></select></label>
      <label class="field"><span class="field-label">On update</span><select class="fk-upd"><option value="">Default</option><option>RESTRICT</option><option>CASCADE</option><option>SET NULL</option><option>NO ACTION</option></select></label>
    </div></div>`);
  const q = (s) => body.querySelector(s);
  const pairs = q(".fk-pairs");
  const refCols = async () => {
    const name = q(".fk-ref").value;
    if (!refCache[name]) {
      try { refCache[name] = (await api.get(`${x.base}/tables/${encodeURIComponent(name)}`)).columns.map((c) => c.name); }
      catch (ex) { toast(ex.message, "err"); refCache[name] = []; }
    }
    return refCache[name];
  };
  const fillRefs = async () => {
    const wanted = q(".fk-ref").value;
    const cols = await refCols();
    if (q(".fk-ref").value !== wanted) return; // the user switched tables while this loaded: a newer call owns the list
    pairs.querySelectorAll(".fk-rcol").forEach((s) => {
      const keep = s.value;
      s.innerHTML = cols.map((c) => `<option value="${esc(c)}">${esc(c)}</option>`).join("");
      if (cols.includes(keep)) s.value = keep;
    });
  };
  const addPair = async () => {
    const row = el(`<div class="dbe-pair"><select class="fk-lcol" aria-label="Column">${t.columns.map((c) => `<option value="${esc(c.name)}">${esc(c.name)}</option>`).join("")}</select>
      <span class="dim">→</span><select class="fk-rcol" aria-label="Referenced column"></select>
      <button class="btn btn-ghost btn-xs fk-rm" type="button" aria-label="Remove pair">${icon("x")}</button></div>`);
    row.querySelector(".fk-rm").onclick = () => { if (pairs.children.length > 1) row.remove(); };
    pairs.appendChild(row);
    await fillRefs();
  };
  q(".fk-add").onclick = addPair;
  q(".fk-ref").addEventListener("change", () => {
    q(".fk-name").value = `fk_${t.table}_${q(".fk-ref").value}`.slice(0, 64);
    fillRefs();
  });
  q(".fk-name").value = `fk_${t.table}_${refTables[0]}`.slice(0, 64);
  addPair();
  schemaDialog(x, {
    title: `Add foreign key — ${t.table}`, body, okText: "Add foreign key",
    build: () => ({
      action: "add_foreign_key", table: t.table, name: q(".fk-name").value, ref_table: q(".fk-ref").value,
      key_columns: [...pairs.querySelectorAll(".fk-lcol")].map((s) => s.value),
      ref_columns: [...pairs.querySelectorAll(".fk-rcol")].map((s) => s.value),
      on_delete: q(".fk-del").value, on_update: q(".fk-upd").value,
    }),
    done: () => x.refreshStructure(),
  });
}

export async function dropForeignKey(x, fk) {
  if (!await confirmDialog(`Drop the foreign key ${fk.name}? The rows stay; the link between the tables is removed.`, { danger: true, title: "Drop foreign key", okText: "Drop" })) return;
  try {
    await api.post(`${x.base}/schema`, { action: "drop_foreign_key", table: x.st.structure.table, name: fk.name });
    toast("Foreign key dropped");
    await x.refreshStructure();
  } catch (ex) { toast(ex.message, "err"); }
}

export function renameTable(x) {
  const t = x.st.structure.table;
  const body = el(`<label class="field"><span class="field-label">New name</span><input class="rn-name mono" maxlength="64" spellcheck="false" autocapitalize="off"></label>`);
  body.querySelector("input").value = t;
  schemaDialog(x, {
    title: `Rename ${t}`, body, okText: "Rename",
    build: () => ({ action: "rename_table", table: t, new_name: body.querySelector("input").value }),
    done: async () => { const nn = body.querySelector("input").value; await x.reloadTables(false); await x.selectTable(nn); },
  });
}

export function copyTable(x) {
  const t = x.st.structure.table;
  const body = el(`<div><label class="field"><span class="field-label">Name of the copy</span><input class="cp-name mono" maxlength="64" spellcheck="false" autocapitalize="off"></label>
    <label class="checkline"><input type="checkbox" class="cp-data" checked> Copy the rows too</label></div>`);
  body.querySelector(".cp-name").value = t + "_copy";
  schemaDialog(x, {
    title: `Copy ${t}`, body, okText: "Copy",
    build: () => ({ action: "copy_table", table: t, new_name: body.querySelector(".cp-name").value, data: body.querySelector(".cp-data").checked }),
    done: async () => { const nn = body.querySelector(".cp-name").value; await x.reloadTables(false); await x.selectTable(nn); },
  });
}

export function newTable(x) {
  const body = el(`<div>
    <label class="field"><span class="field-label">Table name</span><input class="nt-name mono" maxlength="64" spellcheck="false" autocapitalize="off" autocomplete="off"></label>
    <div class="nt-cols"></div>
    <button class="btn btn-sm nt-add" type="button">${icon("plus")} Add column</button></div>`);
  const cols = body.querySelector(".nt-cols");
  const forms = [];
  const number = () => forms.forEach((it, i) => { it.card.querySelector(".nt-idx").textContent = "Column " + (i + 1); });
  const addCol = (preset) => {
    const f = columnForm(x, { compact: true });
    const card = el(`<div class="dbe-colcard"><div class="dbe-colcard-head"><b class="small dim nt-idx"></b>
      <label class="checkline"><input type="checkbox" class="nt-pk"> Primary key</label>
      <button class="btn btn-ghost btn-xs nt-rm" type="button" aria-label="Remove column">${icon("trash")}</button></div></div>`);
    card.appendChild(f.el);
    const item = { f, card };
    forms.push(item);
    card.querySelector(".nt-rm").onclick = () => { if (forms.length > 1) { forms.splice(forms.indexOf(item), 1); card.remove(); number(); } };
    cols.appendChild(card);
    if (preset) preset(f.el, card);
    number();
  };
  body.querySelector(".nt-add").onclick = () => addCol();
  addCol((root, card) => {
    // A sensible first column: an auto-incrementing integer primary key.
    const intName = x.info.types.find((t) => t.name === "int" || t.name === "integer")?.name;
    root.querySelector(".cf-name").value = "id";
    root.querySelector(".cf-type").value = intName;
    root.querySelector(".cf-null").checked = false;
    root.querySelector(".cf-auto").checked = true;
    root.dispatchEvent(new Event("change"));
    card.querySelector(".nt-pk").checked = true;
  });
  schemaDialog(x, {
    title: "New table", body, okText: "Create table",
    build: () => {
      const name = body.querySelector(".nt-name").value;
      if (!nameOk(name)) throw new Error("Give the table a name");
      const columns = forms.map((it) => it.f.read());
      const key = forms.filter((it) => it.card.querySelector(".nt-pk").checked).map((it) => it.f.read().name);
      return { action: "create_table", table: name, columns, key_columns: key };
    },
    done: async () => { const nn = body.querySelector(".nt-name").value; await x.reloadTables(false); await x.selectTable(nn); },
  });
}

export async function showCreate(x) {
  const t = x.st.structure.table;
  let res;
  try { res = await api.get(`${x.base}/tables/${encodeURIComponent(t)}/definition`); }
  catch (ex) { toast(ex.message, "err"); return; }
  const body = el(`<div><pre class="dbe-pre"></pre></div>`);
  body.querySelector("pre").textContent = res.sql;
  const copy = btn("Copy"), close = btn("Close", "btn btn-primary");
  const m = modal({ title: `CREATE statement — ${t}`, wide: true, body, actions: [copy, close] });
  copy.onclick = () => copyText(res.sql);
  close.onclick = () => m.close();
}

export async function maintain(x, op) {
  const t = x.st.structure.table;
  try {
    const res = await api.post(`${x.base}/tables/${encodeURIComponent(t)}/maintenance`, { op });
    if (res.result && res.result.columns && res.result.columns.length) {
      const body = el(`<div><p class="small dim mono">${esc(res.statement)}</p>${x.gridHTML(res.result)}</div>`);
      const close = btn("Close", "btn btn-primary");
      const m = modal({ title: `${op} — ${t}`, wide: true, body, actions: [close] });
      close.onclick = () => m.close();
    } else toast(`${op} finished`);
    await x.reloadTables(true);
  } catch (ex) { toast(ex.message, "err"); }
}

// ------------------------------------------------------------------ import / export

export function drawIO(x) {
  const tables = x.st.tables;
  const real = tables.filter((t) => t.type === "table");
  const box = x.$("#dbe-body");
  box.innerHTML = `
    <div class="dbe-io">
      <section class="dbe-panel">
        <h3>${icon("download")} Export</h3>
        <div class="dbe-io-tables">
          <div class="dbe-io-tools"><button class="btn btn-ghost btn-xs" id="ex-all" type="button">All</button><button class="btn btn-ghost btn-xs" id="ex-none" type="button">None</button></div>
          ${tables.length ? checkList(tables.map((t) => t.name), "ex-tbl", x.st.table ? [x.st.table] : tables.map((t) => t.name)) : `<p class="small dim">No tables.</p>`}
        </div>
        <label class="field"><span class="field-label">Format</span><select id="ex-fmt"><option value="sql">SQL (structure and/or data)</option><option value="csv">CSV (one table)</option></select></label>
        <div id="ex-sql">
          <label class="checkline"><input type="checkbox" id="ex-struct" checked> Structure (CREATE statements)</label>
          <label class="checkline"><input type="checkbox" id="ex-data" checked> Data (INSERT statements)</label>
          <label class="checkline"><input type="checkbox" id="ex-drop"> Add DROP … IF EXISTS before each CREATE</label>
        </div>
        <div id="ex-csv" style="display:none">
          <label class="checkline"><input type="checkbox" id="ex-header" checked> First row holds the column names</label>
          <p class="small dim">NULL is written as <code>\\N</code> so it stays different from an empty string.</p>
        </div>
        <button class="btn btn-primary" id="ex-go" ${tables.length ? "" : "disabled"}>${icon("download")} Download</button>
      </section>
      <section class="dbe-panel">
        <h3>${icon("upload")} Import</h3>
        <label class="field"><span class="field-label">Format</span><select id="im-fmt"><option value="sql">SQL script</option><option value="csv">CSV into a table</option></select></label>
        <label class="field"><span class="field-label">File <span class="dim">(up to 64 MiB, UTF-8)</span></span><input type="file" id="im-file" accept=".sql,.csv,.txt,.tsv,text/*"></label>
        <div id="im-csv" style="display:none">
          <label class="field"><span class="field-label">Into table</span><select id="im-table">${real.map((t) => `<option value="${esc(t.name)}" ${t.name === x.st.table ? "selected" : ""}>${esc(t.name)}</option>`).join("")}</select></label>
          <div class="dbe-row2">
            <label class="field"><span class="field-label">Delimiter</span><select id="im-delim"><option value=",">Comma</option><option value=";">Semicolon</option><option value="|">Pipe</option><option value="tab">Tab</option></select></label>
            <label class="field"><span class="field-label">NULL is written as</span><input id="im-null" class="mono" value="\\N" spellcheck="false"></label>
          </div>
          <label class="checkline"><input type="checkbox" id="im-header" checked> First row holds the column names</label>
        </div>
        <p class="small dim" id="im-note"></p>
        <button class="btn btn-primary" id="im-go">${icon("upload")} Import</button>
        <div id="im-out"></div>
      </section>
    </div>`;
  const $ = (s) => box.querySelector(s);
  const checks = () => [...box.querySelectorAll(".ex-tbl")];
  $("#ex-all")?.addEventListener("click", () => checks().forEach((c) => { c.checked = true; }));
  $("#ex-none")?.addEventListener("click", () => checks().forEach((c) => { c.checked = false; }));
  $("#ex-fmt").onchange = () => {
    const csv = $("#ex-fmt").value === "csv";
    $("#ex-sql").style.display = csv ? "none" : ""; $("#ex-csv").style.display = csv ? "" : "none";
  };
  $("#ex-go").onclick = async () => {
    const chosen = checks().filter((c) => c.checked).map((c) => c.value);
    if (!chosen.length) { toast("Choose at least one table", "warn"); return; }
    const p = new URLSearchParams();
    chosen.forEach((n) => p.append("table", n));
    if ($("#ex-fmt").value === "csv") {
      if (chosen.length !== 1) { toast("CSV export takes exactly one table", "warn"); return; }
      p.set("format", "csv"); p.set("header", $("#ex-header").checked ? "1" : "0");
    } else {
      p.set("format", "sql");
      if ($("#ex-struct").checked) p.set("structure", "1");
      if ($("#ex-data").checked) p.set("data", "1");
      if ($("#ex-drop").checked) p.set("drop", "1");
      if (!$("#ex-struct").checked && !$("#ex-data").checked) { toast("Choose structure, data or both", "warn"); return; }
      // Exporting every table: send none, so the server includes any created meanwhile.
      if (chosen.length === tables.length) p.delete("table");
    }
    const b = $("#ex-go");
    b.disabled = true;
    if (await download(`${x.base}/export?${p}`, "export")) toast("Export downloaded");
    b.disabled = false;
  };
  const syncImport = () => {
    const csv = $("#im-fmt").value === "csv";
    $("#im-csv").style.display = csv ? "" : "none";
    $("#im-note").textContent = csv
      ? "All rows are imported in one transaction: if any row fails, nothing is added."
      : x.info.engine === "postgres" ? "The script runs in one transaction: if any statement fails, nothing is changed."
        : "MariaDB can't undo table changes: statements before a failure stay applied, and the failing statement is reported.";
  };
  $("#im-fmt").onchange = syncImport; syncImport();
  $("#im-go").onclick = async () => {
    const file = $("#im-file").files[0];
    if (!file) { toast("Choose a file first", "warn"); return; }
    if (file.size > 64 * 1024 * 1024) { toast("That file is larger than 64 MiB", "err"); return; }
    const csv = $("#im-fmt").value === "csv";
    if (csv && !$("#im-table").value) { toast("Choose a table", "warn"); return; }
    if (!csv && !await confirmDialog(`Run the SQL in ${file.name}? It can change or delete data in this database.`, { danger: true, title: "Import SQL", okText: "Run it" })) return;
    const fd = new FormData();
    fd.append("format", csv ? "csv" : "sql");
    if (csv) {
      fd.append("table", $("#im-table").value); fd.append("header", $("#im-header").checked ? "1" : "0");
      fd.append("delimiter", $("#im-delim").value); fd.append("null", $("#im-null").value);
    }
    fd.append("file", file);
    const out = $("#im-out"), b = $("#im-go");
    b.disabled = true; out.innerHTML = loading();
    try {
      const res = await api.request("POST", `${x.base}/import`, fd, true);
      let data = null;
      try { data = await res.json(); } catch { /* not JSON */ }
      if (res.ok) {
        out.innerHTML = `<p class="dbe-ok">${icon("check")} ${csv ? `${Number(data.rows).toLocaleString()} row${data.rows === 1 ? "" : "s"} imported` : `${Number(data.statements).toLocaleString()} statement${data.statements === 1 ? "" : "s"} run`}.</p>`;
        await x.refreshList();
      } else {
        const d = data || {};
        out.innerHTML = `<p class="dbe-err">${esc(d.error || res.statusText || "The import failed")}</p>
          ${d.failed_sql ? `<pre class="dbe-pre">${esc(d.failed_sql)}</pre>` : ""}
          ${!csv && d.statements ? `<p class="small dim">${d.statements} statement${d.statements === 1 ? "" : "s"} ran before the failure and were kept.</p>` : ""}`;
        if (!csv) await x.refreshList();
      }
    } catch (ex) { out.innerHTML = `<p class="dbe-err">${esc(ex.message)}</p>`; }
    b.disabled = false;
  };
}

// ------------------------------------------------------------------ search

export function drawSearch(x) {
  const box = x.$("#dbe-body");
  box.innerHTML = `
    <div class="dbe-searchbar"><input type="search" id="sr-q" class="mono" placeholder="Find a value in every table…" autocomplete="off" autocapitalize="off" spellcheck="false" aria-label="Search the whole database">
      <button class="btn btn-primary" id="sr-go">${icon("search")} Search</button></div>
    <p class="small dim">Looks for the text inside every column of every table (case-insensitive where the database is), and shows up to 10 matching rows per table.</p>
    <div id="sr-out"></div>`;
  const run = async () => {
    const q = box.querySelector("#sr-q").value.trim();
    if (!q) { toast("Enter something to search for", "warn"); return; }
    const out = box.querySelector("#sr-out"), b = box.querySelector("#sr-go");
    b.disabled = true; out.innerHTML = loading();
    try {
      const res = await api.get(`${x.base}/search?${new URLSearchParams({ q })}`);
      if (!res.hits.length) {
        out.innerHTML = `<div class="empty-state"><p>No match in ${res.searched} table${res.searched === 1 ? "" : "s"}${res.truncated ? " (the search stopped early — narrow it down)" : ""}.</p></div>`;
      } else {
        out.innerHTML = `<p class="small dim">Found in ${res.hits.length} table${res.hits.length === 1 ? "" : "s"} · searched ${res.searched}${res.truncated ? " · <b>stopped early</b>, so there may be more" : ""}</p>` +
          res.hits.map((h) => `<div class="dbe-hit"><div class="dbe-hit-head"><b class="mono">${esc(h.table)}</b>
            <span class="small dim">${h.rows.length}${h.truncated ? "+" : ""} row${h.rows.length === 1 ? "" : "s"}</span>
            <button class="btn btn-sm dbe-open" data-t="${esc(h.table)}">${icon("list")} Open table</button></div>${x.gridHTML(h)}</div>`).join("");
        out.querySelectorAll(".dbe-open").forEach((b2) => b2.onclick = () => { x.setTab("browse"); x.selectTable(b2.dataset.t); });
      }
    } catch (ex) { out.innerHTML = `<p class="dbe-err">${esc(ex.message)}</p>`; }
    b.disabled = false;
  };
  box.querySelector("#sr-go").onclick = run;
  box.querySelector("#sr-q").onkeydown = (e) => { if (e.key === "Enter") run(); };
  box.querySelector("#sr-q").focus();
}

// ------------------------------------------------------------------ routines / triggers

export async function drawObjects(x) {
  const box = x.$("#dbe-body");
  box.innerHTML = loading();
  const seq = x.seq();
  let list;
  try { list = await api.get(`${x.base}/objects`); }
  catch (ex) { if (seq !== x.seq()) return; box.innerHTML = `<p class="dbe-err">${esc(ex.message)}</p>`; return; }
  if (seq !== x.seq()) return;
  if (!list.length) {
    box.innerHTML = `<div class="empty-state"><p>No stored routines, triggers or events in this database.</p></div>`;
    return;
  }
  const groups = { procedure: "Procedures", function: "Functions", trigger: "Triggers", event: "Events" };
  box.innerHTML = `<p class="small dim">Read-only. To change one, edit its definition in the SQL tab.</p>` +
    Object.entries(groups).map(([kind, label]) => {
      const items = list.filter((o) => o.kind === kind);
      return items.length ? `<h4 class="dbe-h">${label}</h4>` + items.map((o) => `<details class="dbe-obj"><summary><span class="mono">${esc(o.name)}</span>${o.table ? ` <span class="small dim">on ${esc(o.table)}</span>` : ""}</summary>
        <pre class="dbe-pre">${o.definition ? esc(o.definition) : "You don't have permission to read this definition."}</pre></details>`).join("") : "";
    }).join("");
}

import { addRoute } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, copyText, pageHead, loading } from "../ui.js";

const LOG_KINDS = [
  { value: "access", label: "Access log" },
  { value: "error", label: "Error log" },
];

// Tokenizer for syntax highlighting: bracketed timestamps ([...]), quoted
// strings ("..."), an HTTP status code (colored by its first digit) — only
// where one can actually appear, right after a quoted request field (the
// combined-log convention nginx/apache/caddy/the native go server all
// share) or after "-> " (this panel's own go-server error-log format) —
// and common log-level words. A bare \d{3} without that positional
// constraint would also light up IP octets like the "127" in "127.0.0.1",
// so the lookbehind is load-bearing, not decorative. Covers every log kind
// this page reads without needing a per-format parser.
const TOKEN_RE = /(\[[^\]\n]{1,160}\])|("(?:[^"\\]|\\.)*")|(?<=" |-> )([1-5]\d{2})\b|\b(error|err|crit|alert|emerg|fatal)\b|\b(warn(?:ing)?)\b|\b(notice|info)\b/gi;

function highlightLine(line) {
  let out = "";
  let last = 0;
  for (const m of line.matchAll(TOKEN_RE)) {
    out += esc(line.slice(last, m.index));
    let cls;
    if (m[1]) cls = "log-ts";
    else if (m[2]) cls = "log-str";
    else if (m[3]) cls = "log-status-" + m[3][0];
    else if (m[4]) cls = "log-lvl-err";
    else if (m[5]) cls = "log-lvl-warn";
    else cls = "log-lvl-info";
    out += `<span class="${cls}">${esc(m[0])}</span>`;
    last = m.index + m[0].length;
  }
  out += esc(line.slice(last));
  return out;
}

addRoute("/logs", {
  title: "Logs",
  icon: "log",
  group: "Websites",
  order: 6,
  render: async (view) => {
    view.innerHTML = pageHead("Logs", "Tail access and error logs for any of your domains — resolved automatically from each domain's web server.") +
      `<div id="logs-root">${loading()}</div>`;

    const domains = await api.get("/domains").catch(() => []);
    const root = document.getElementById("logs-root");

    if (!domains.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">${icon("log")}</span><p>No domains yet — add one to view its logs here.</p></div>`;
      return;
    }

    const wantedID = new URLSearchParams(location.hash.split("?")[1] || "").get("domain");

    root.innerHTML = `<div class="card">
      <div class="log-toolbar">
        <select id="log-domain" style="min-width:220px">
          ${domains.map((d) => `<option value="${d.id}" ${String(d.id) === wantedID ? "selected" : ""}>${esc(d.domain)}</option>`).join("")}
        </select>
        <select id="log-kind" style="width:auto">
          ${LOG_KINDS.map((k) => `<option value="${k.value}">${esc(k.label)}</option>`).join("")}
        </select>
        <select id="log-lines" style="width:auto">
          <option value="100">Last 100 lines</option>
          <option value="300" selected>Last 300 lines</option>
          <option value="1000">Last 1000 lines</option>
          <option value="2000">Last 2000 lines</option>
        </select>
        <button class="btn btn-sm" id="log-refresh">${icon("clock")} Refresh</button>
        <label class="log-toggle small"><input type="checkbox" id="log-wrap" checked> Wrap</label>
        <label class="log-toggle small"><input type="checkbox" id="log-auto"> Auto-refresh (10s)</label>
      </div>
      <div class="log-toolbar">
        <div class="log-search">${icon("search")}<input type="text" id="log-filter" placeholder="Filter lines…"></div>
        <button class="btn btn-sm" id="log-copy">${icon("copy")} Copy</button>
        <button class="btn btn-sm" id="log-download">${icon("download")} Download</button>
        <span class="small log-meta" id="log-meta"></span>
      </div>
      <div id="log-view" class="log-view wrap"><div class="log-empty">Loading…</div></div>
    </div>`;

    const domainSel = document.getElementById("log-domain");
    const kindSel = document.getElementById("log-kind");
    const linesSel = document.getElementById("log-lines");
    const wrapChk = document.getElementById("log-wrap");
    const autoChk = document.getElementById("log-auto");
    const filterInp = document.getElementById("log-filter");
    const logView = document.getElementById("log-view");
    const meta = document.getElementById("log-meta");
    let timer = null;
    let rawLines = [];
    let fileLabel = "";

    const applyFilter = () => {
      const q = filterInp.value.trim().toLowerCase();
      let shown = 0;
      logView.querySelectorAll(".log-row").forEach((rowEl, i) => {
        const match = !q || rawLines[i].toLowerCase().includes(q);
        rowEl.classList.toggle("log-row-hidden", !match);
        if (match) shown++;
      });
      meta.textContent = (q ? `${shown} / ${rawLines.length} lines` : `${rawLines.length} lines`) + (fileLabel ? " · " + fileLabel : "");
    };

    const render = () => {
      logView.innerHTML = rawLines.length
        ? rawLines.map((l, i) => `<div class="log-row"><span class="log-ln">${i + 1}</span><span class="log-txt">${highlightLine(l) || "&nbsp;"}</span></div>`).join("")
        : `<div class="log-empty">(empty)</div>`;
      applyFilter();
    };

    const load = async () => {
      const id = domainSel.value;
      const kind = kindSel.value;
      const lines = linesSel.value;
      const nearBottom = logView.scrollHeight - logView.scrollTop - logView.clientHeight < 40;
      try {
        const data = await api.get(`/domains/${id}/logs?kind=${encodeURIComponent(kind)}&lines=${encodeURIComponent(lines)}`);
        rawLines = data.exists ? data.lines : [];
        fileLabel = data.exists ? data.file : "";
        if (!data.exists) {
          logView.innerHTML = `<div class="log-empty">No ${esc(kind)} log yet for this domain.</div>`;
          meta.textContent = "";
          return;
        }
        render();
        if (nearBottom) logView.scrollTop = logView.scrollHeight;
      } catch (ex) {
        rawLines = [];
        logView.innerHTML = `<div class="log-empty">Failed to load log: ${esc(ex.message)}</div>`;
        meta.textContent = "";
        toast(ex.message, "err");
      }
    };

    const setAuto = (on) => {
      if (timer) { clearInterval(timer); timer = null; }
      if (on) timer = setInterval(load, 10000);
    };

    document.getElementById("log-refresh").onclick = load;
    domainSel.onchange = load;
    kindSel.onchange = load;
    linesSel.onchange = load;
    wrapChk.onchange = () => logView.classList.toggle("wrap", wrapChk.checked);
    autoChk.onchange = () => setAuto(autoChk.checked);
    filterInp.oninput = applyFilter;
    document.getElementById("log-copy").onclick = () => {
      if (!rawLines.length) return;
      copyText(rawLines.join("\n"));
    };
    document.getElementById("log-download").onclick = () => {
      if (!rawLines.length) return;
      const domain = domainSel.selectedOptions[0]?.textContent || "log";
      const blob = new Blob([rawLines.join("\n") + "\n"], { type: "text/plain" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `${domain}.${kindSel.value}.log`;
      a.click();
      URL.revokeObjectURL(a.href);
    };

    load();
  },
});

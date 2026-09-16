import { addRoute } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, pageHead, loading } from "../ui.js";

const LOG_KINDS = [
  { value: "access", label: "Access log" },
  { value: "error", label: "Error log" },
];

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
      <div style="display:flex;gap:10px;flex-wrap:wrap;align-items:center">
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
        <label class="small dim" style="display:flex;align-items:center;gap:6px;margin-left:auto">
          <input type="checkbox" id="log-auto"> Auto-refresh (10s)
        </label>
        <span class="small dim" id="log-meta"></span>
      </div>
      <pre id="log-view" class="mono small" style="margin-top:12px;max-height:65vh;overflow:auto;background:var(--bg-1,#14161a);padding:12px;border-radius:8px;white-space:pre-wrap;word-break:break-all"> </pre>
    </div>`;

    const domainSel = document.getElementById("log-domain");
    const kindSel = document.getElementById("log-kind");
    const linesSel = document.getElementById("log-lines");
    const autoChk = document.getElementById("log-auto");
    const logView = document.getElementById("log-view");
    const meta = document.getElementById("log-meta");
    let timer = null;

    const load = async () => {
      const id = domainSel.value;
      const kind = kindSel.value;
      const lines = linesSel.value;
      try {
        const data = await api.get(`/domains/${id}/logs?kind=${encodeURIComponent(kind)}&lines=${encodeURIComponent(lines)}`);
        logView.textContent = data.exists
          ? (data.lines.join("\n") || "(empty)")
          : `No ${kind} log yet for this domain.`;
        meta.textContent = data.exists ? data.lines.length + " lines" : "";
      } catch (ex) {
        logView.textContent = "Failed to load log: " + ex.message;
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
    autoChk.onchange = () => setAuto(autoChk.checked);

    load();
  },
});

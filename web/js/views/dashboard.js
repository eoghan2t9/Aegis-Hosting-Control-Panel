import { addRoute, isAdmin, me } from "../app.js";
import { api } from "../api.js";
import { icon, fmtBytes, fmtPct, fmtNum, fmtAgo, statusTag, sparkline, ring, esc, pageHead, toast } from "../ui.js";

addRoute("/dashboard", {
  title: "Dashboard",
  icon: "grid",
  group: "Overview",
  render: async (view) => {
    view.innerHTML = pageHead("Server status", isAdmin()
      ? "Live resource usage for the whole host, plus per-account consumption."
      : "Your account and the resources you are using.");

    let ov;
    try { ov = await api.get("/system/overview"); } catch { /* pill covers it */ }
    if (!ov) { view.insertAdjacentHTML("beforeend", '<div class="card">Host metrics unavailable.</div>'); return; }

    const memPct = ov.mem_total ? (ov.mem_used / ov.mem_total) * 100 : 0;
    const swapPct = ov.swap_total ? (ov.swap_used / ov.swap_total) * 100 : 0;
    const disk = ov.disks?.[0];
    const load = ov.load?.[0] ?? 0;

    view.insertAdjacentHTML("beforeend", `
      <div class="grid grid-4">
        <div class="stat"><div class="stat-label">CPU Load (1m)</div>
          <div class="stat-value">${load.toFixed(2)}<small>/ ${ov.cpu_cores} cores</small></div>
          <div class="stat-meta">${esc(ov.cpu_model || "")}</div></div>
        <div class="stat"><div class="stat-label">Memory</div>
          <div class="stat-value">${fmtBytes(ov.mem_used)}<small>/ ${fmtBytes(ov.mem_total)}</small></div>
          <div class="meter amber" style="margin-top:8px"><i style="width:${memPct}%"></i></div></div>
        <div class="stat"><div class="stat-label">Swap</div>
          <div class="stat-value">${fmtBytes(ov.swap_used)}<small>/ ${fmtBytes(ov.swap_total)}</small></div>
          <div class="meter" style="margin-top:8px"><i style="width:${swapPct}%"></i></div></div>
        <div class="stat"><div class="stat-label">${esc(disk?.mount || "Disk")}</div>
          <div class="stat-value">${disk ? fmtBytes(disk.used) : "—"}<small>${disk ? "/ " + fmtBytes(disk.total) : ""}</small></div>
          <div class="meter ${disk?.pct > 85 ? "red" : "teal"}" style="margin-top:8px"><i style="width:${disk?.pct || 0}%"></i></div></div>
      </div>

      <div style="height:16px"></div>
      <div class="grid grid-2">
        <div class="card">
          <div class="card-head"><span class="card-title">Host</span></div>
          <dl class="kv">
            <dt>hostname</dt><dd class="mono">${esc(ov.hostname)}</dd>
            <dt>os</dt><dd>${esc(ov.os)}</dd>
            <dt>kernel</dt><dd class="mono">${esc(ov.kernel)} · ${esc(ov.arch)}</dd>
            <dt>uptime</dt><dd class="mono">${fmtDur(ov.uptime_secs)}</dd>
            <dt>panel uptime</dt><dd class="mono">${fmtDur(ov.panel_uptime_secs)}</dd>
          </dl>
        </div>
        <div class="card">
          <div class="card-head"><span class="card-title">Services</span></div>
          <table class="tbl" style="min-width:0">
            <tbody>${(ov.services || []).map((s) => `
              <tr><td class="mono">${esc(s.name)}</td><td style="text-align:right">${statusTag(s.status)}</td></tr>`).join("") || '<tr><td class="dim">none detected</td></tr>'}
            </tbody>
          </table>
        </div>
      </div>
      <div style="height:16px"></div>
      <div id="live-charts" class="card">
        <div class="card-head"><span class="card-title">Realtime activity</span><span class="card-actions"><span class="tag tag-lime" id="ws-state">live</span></span></div>
        <div class="grid grid-2">
          <div><div class="meter-label"><span>CPU</span><span id="cpu-val">—</span></div><div id="chart-cpu"></div></div>
          <div><div class="meter-label"><span>Memory used</span><span id="mem-val">—</span></div><div id="chart-mem"></div></div>
        </div>
        <div style="height:12px"></div>
        <div class="grid grid-2">
          <div><div class="meter-label"><span>Network RX (delta/s)</span><span id="rx-val">—</span></div><div id="chart-rx" class="small"></div></div>
          <div><div class="meter-label"><span>Network TX (delta/s)</span><span id="tx-val">—</span></div><div id="chart-tx" class="small"></div></div>
        </div>
      </div>
      <div style="height:16px"></div>
      <div class="card" id="usage-card">
        <div class="card-head"><span class="card-title">${isAdmin() ? "Per-user resource usage" : "Your resource usage"}</span></div>
        <div id="usage-body">${spinnerHtml()}</div>
      </div>
    `);

    startLiveCharts(ov);
    loadUsage(view);
  },
});

function fmtDur(secs) {
  const d = Math.floor(secs / 86400), h = Math.floor((secs % 86400) / 3600), m = Math.floor((secs % 3600) / 60);
  return (d ? d + "d " : "") + (h || d ? h + "h " : "") + m + "m";
}

function startLiveCharts(ov) {
  const cpu = [], mem = [], rx = [], tx = [];
  let lastNet = null;
  let ws;
  const push = (arr, v, cap = 60) => { arr.push(v); if (arr.length > cap) arr.shift(); };
  const render = () => {
    if (!document.getElementById("chart-cpu")) return stop();
    const mx = (n) => Math.max(...n) || 1;
    document.getElementById("chart-cpu").innerHTML = cpu.length ? sparkline(cpu, { max: 100, color: "var(--accent)", dot: true }) : "";
    document.getElementById("chart-mem").innerHTML = mem.length ? sparkline(mem, { max: 100, color: "var(--teal)", dot: true, extraCls: "teal" }) : "";
    document.getElementById("chart-rx").innerHTML = rx.length ? sparkline(rx, { max: mx(rx), color: "var(--teal)", extraCls: "teal" }) : "";
    document.getElementById("chart-tx").innerHTML = tx.length ? sparkline(tx, { max: mx(tx), color: "var(--accent)" }) : "";
  };
  const tick = () => render();
  const stop = () => { try { ws?.close(); } catch {} };
  try {
    const proto = location.protocol === "https:" ? "wss" : "ws";
    ws = new WebSocket(`${proto}://${location.host}/api/system/metrics`);
    ws.onmessage = (e) => {
      const m = JSON.parse(e.data);
      push(cpu, m.cpu);
      push(mem, m.mem_total ? (m.mem_used / m.mem_total) * 100 : 0);
      if (lastNet) { push(rx, Math.max(0, m.net_rx - lastNet.rx) / 2); push(tx, Math.max(0, m.net_tx - lastNet.tx) / 2); }
      lastNet = { rx: m.net_rx, tx: m.net_tx };
      document.getElementById("cpu-val").textContent = fmtPct(m.cpu);
      document.getElementById("mem-val").textContent = fmtBytes(m.mem_used);
      document.getElementById("rx-val").textContent = fmtBytes((rx[rx.length - 1] || 0)) + "/s";
      document.getElementById("tx-val").textContent = fmtBytes((tx[tx.length - 1] || 0)) + "/s";
      tick();
    };
    ws.onclose = () => {
      const el = document.getElementById("ws-state");
      if (el) { el.textContent = "reconnecting"; el.className = "tag tag-amber"; }
      setTimeout(() => { if (document.getElementById("chart-cpu")) startLiveCharts(ov); }, 4000);
    };
  } catch { /* ws unsupported */ }
}

async function loadUsage(view) {
  const body = document.getElementById("usage-body");
  if (!body) return;
  try {
    const usage = await api.get("/system/usage");
    const admin = isAdmin();
    if (!usage || usage.length === 0) {
      body.innerHTML = `<p class="dim">No accounts with running processes yet. Usage appears once sites are active.</p>`;
      return;
    }
    body.innerHTML = `
      <div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Account</th><th>Disk used</th><th>CPU (total)</th><th>Memory (RSS)</th>${admin ? "<th></th>" : ""}</tr></thead>
        <tbody>${usage.map((u) => {
          const pct = u.disk_used_bytes;
          return `<tr>
            <td><span class="mono">${esc(u.username)}</span></td>
            <td class="num">${fmtBytes(u.disk_bytes)}</td>
            <td class="num">${u.cpu_seconds.toFixed(1)}s</td>
            <td class="num">${fmtBytes(u.rss_bytes)}</td>
            ${admin ? `<td class="row-actions"></td>` : ""}
          </tr>`;
        }).join("")}</tbody></table></div>`;
  } catch (ex) {
    body.innerHTML = `<p class="dim">Usage unavailable: ${esc(ex.message)}</p>`;
  }
}

function spinnerHtml() { return `<div class="spinner"></div>`; }

addRoute("/account", {
  title: "My account",
  icon: "user",
  group: "Overview",
  adminOnly: false,
  render: async (view) => {
    const u = me();
    view.innerHTML = pageHead("My account", "Profile and password.");
    view.insertAdjacentHTML("beforeend", `
      <div class="grid grid-2">
        <div class="card">
          <div class="card-head"><span class="card-title">Profile</span></div>
          <dl class="kv">
            <dt>username</dt><dd class="mono">${esc(u.username)}</dd>
            <dt>email</dt><dd>${esc(u.email) || "—"}</dd>
            <dt>role</dt><dd>${statusTag(u.role)}</dd>
            <dt>package</dt><dd>${esc(u.package_name || "—")}</dd>
            <dt>status</dt><dd>${statusTag(u.status)}</dd>
            <dt>home</dt><dd class="mono">${esc(u.home_dir)}</dd>
            <dt>created</dt><dd class="small">${fmtAgo(u.created_at)}</dd>
          </dl>
        </div>
        <div class="card">
          <div class="card-head"><span class="card-title">Change password</span></div>
          <form id="pw-form">
            <label class="field"><span class="field-label">Current password</span><input type="password" id="pw-old" required autocomplete="current-password"></label>
            <label class="field"><span class="field-label">New password</span><input type="password" id="pw-new" required minlength="8" autocomplete="new-password"></label>
            <button class="btn btn-primary" type="submit">Update password</button>
          </form>
        </div>
      </div>`);
    // Change password is admin-managed; self-service hits the same endpoint.
    document.getElementById("pw-form").onsubmit = async (e) => {
      e.preventDefault();
      const oldPw = document.getElementById("pw-old").value;
      const newPw = document.getElementById("pw-new").value;
      if (newPw.length < 8) return toast("Password must be at least 8 characters", "warn");
      try {
        // Verify current password via login, then reset.
        await api.post("/auth/login", { username: u.username, password: oldPw });
        await api.post(`/users/${u.id}/reset-password`, { password: newPw });
        // Session was revoked by the reset — sign back in with the new password.
        const data = await api.post("/auth/login", { username: u.username, password: newPw });
        api.setToken(data.token);
        toast("Password updated");
        document.getElementById("pw-old").value = "";
        document.getElementById("pw-new").value = "";
      } catch (ex) { toast(ex.message, "err"); }
    };
  },
});

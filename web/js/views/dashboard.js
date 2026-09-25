import { addRoute, isAdmin, me } from "../app.js";
import { p } from "../base.js";
import { api } from "../api.js";
import { icon, fmtBytes, fmtPct, fmtNum, fmtAgo, statusTag, sparkline, ring, esc, pageHead, toast, promptDialog, modal } from "../ui.js";

addRoute("/dashboard", {
  title: "Dashboard",
  icon: "grid",
  group: "Overview",
  order: 0,
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
            <dt>server ip</dt><dd class="mono">${esc(ov.public_ip || "unknown")}${(ov.local_ips || []).length > 1 ? ` <span class="small dim">+${ov.local_ips.length - 1} more</span>` : ""}${isAdmin() ? ` <a href="#/ips" class="small">manage</a>` : ""}</dd>
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
        <div class="card-head"><span class="card-title">Realtime activity</span><span class="card-actions">
          <button class="btn btn-ghost btn-sm chart-range" data-range="hour">hour</button>
          <button class="btn btn-ghost btn-sm chart-range" data-range="day">day</button>
          <span class="tag tag-lime" id="ws-state">live</span></span></div>
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
    loadHistory("hour");
    loadUsage(view);
    view.querySelectorAll(".chart-range").forEach((b) => b.onclick = () => loadHistory(b.dataset.range));
  },
});

function fmtDur(secs) {
  const d = Math.floor(secs / 86400), h = Math.floor((secs % 86400) / 3600), m = Math.floor((secs % 3600) / 60);
  return (d ? d + "d " : "") + (h || d ? h + "h " : "") + m + "m";
}

// loadHistory seeds/refreshes the four trend charts from the server-side
// ring buffer (30s samples). Called on render and when a range button is
// clicked; the live WebSocket keeps appending on top between loads.
async function loadHistory(range) {
  try {
    const data = await api.get("/system/metrics/history?range=" + range);
    const pts = data.points || [];
    if (!pts.length || !document.getElementById("chart-cpu")) return;
    const cpu = pts.map((p) => p.cpu);
    const mem = pts.map((p) => (p.mem_total ? (p.mem_used / p.mem_total) * 100 : 0));
    // Network: convert cumulative counters to per-sample deltas.
    const rx = [], tx = [];
    for (let i = 1; i < pts.length; i++) {
      const dt = Math.max(1, (pts[i].t - pts[i - 1].t) / 1000);
      rx.push(Math.max(0, (pts[i].net_rx - pts[i - 1].net_rx) / dt));
      tx.push(Math.max(0, (pts[i].net_tx - pts[i - 1].net_tx) / dt));
    }
    const mx = (n) => Math.max(...n) || 1;
    document.getElementById("chart-cpu").innerHTML = sparkline(cpu, { max: 100, color: "var(--accent)", dot: true });
    document.getElementById("chart-mem").innerHTML = sparkline(mem, { max: 100, color: "var(--teal)", dot: true, extraCls: "teal" });
    document.getElementById("chart-rx").innerHTML = sparkline(rx, { max: mx(rx), color: "var(--teal)", extraCls: "teal" });
    document.getElementById("chart-tx").innerHTML = sparkline(tx, { max: mx(tx), color: "var(--accent)" });
  } catch { /* history unavailable (fresh boot) — live charts still run */ }
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
    ws = new WebSocket(`${proto}://${location.host}${p("/api")}/system/metrics?token=${encodeURIComponent(api.token)}`);
    ws.onmessage = (e) => {
      if (!document.getElementById("cpu-val")) return stop();
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

// loginMaybe2FA wraps /auth/login for in-app callers that need a fresh
// token (password change, below) — if the account has 2FA enabled, the
// bare login call now returns totp_required instead of a token, so this
// prompts for a code inline and completes the second step, rather than
// leaving the caller with an unusable half-finished login.
async function loginMaybe2FA(username, password) {
  const data = await api.post("/auth/login", { username, password });
  if (!data.totp_required) return data;
  const vals = await promptDialog("Two-factor code required", [
    { name: "code", label: "Authenticator code or backup code", required: true, mono: true },
  ], { okText: "Verify" });
  if (!vals) throw new Error("Two-factor verification cancelled");
  return api.post("/auth/totp/verify", { challenge: data.challenge, code: vals.code });
}

// showBackupCodes is the final step of enabling 2FA — shown exactly once,
// right after the server generates them, matching how the FTP
// account-creation flow (web/js/views/domains.js) shows a one-time password.
function showBackupCodes(codes) {
  const done = document.createElement("button");
  done.className = "btn btn-primary";
  done.textContent = "I've saved these";
  const m = modal({
    title: "Save your backup codes",
    body: `<div>
      <p class="small dim">Each code works once, if you ever lose access to your authenticator app. Aegis only keeps a hash of them — save these somewhere safe now, they won't be shown again.</p>
      <div class="creds-box mono" style="margin:10px 0;line-height:1.9;user-select:all">${codes.map(esc).join("<br>")}</div>
    </div>`,
    actions: [done],
  });
  done.onclick = () => m.close();
}

addRoute("/account", {
  title: "My account",
  icon: "user",
  group: "Account",
  order: 0,
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
        <div class="card">
          <div class="card-head"><span class="card-title">Two-factor authentication</span>
            <span class="card-actions">${u.totp_enabled ? statusTag("active") : `<span class="tag">off</span>`}</span></div>
          <p class="small dim" style="margin:0 0 12px">Opt-in — off by default. Once enabled, login asks for a code from your authenticator app (or a saved backup code) after your password.</p>
          <button class="btn ${u.totp_enabled ? "" : "btn-primary"}" id="totp-toggle">${u.totp_enabled ? "Disable 2FA" : "Enable 2FA"}</button>
        </div>
        <div class="card">
          <div class="card-head"><span class="card-title">Usage</span></div>
          <div id="usage-root" class="small dim">loading…</div>
        </div>
      </div>`);
    api.get("/quota/usage").then((usage) => {
      const usageRoot = document.getElementById("usage-root");
      const bar = (used, quota, label) => {
        if (!quota) return `<div class="small dim" style="margin-bottom:10px">${label}: ${fmtBytes(used)} used, no limit</div>`;
        const pct = Math.min(100, (used / quota) * 100);
        return `<div style="margin-bottom:10px">
          <div class="small dim" style="margin-bottom:4px">${label}: ${fmtBytes(used)} / ${fmtBytes(quota)}</div>
          <div style="height:6px;background:var(--bg2, #222);border-radius:3px;overflow:hidden">
            <div style="height:100%;width:${pct}%;background:${pct > 90 ? "var(--danger,#e5484d)" : "var(--accent,#4f8cff)"}"></div>
          </div>
        </div>`;
      };
      usageRoot.innerHTML =
        bar(usage.disk_used_bytes, usage.disk_quota_bytes, "Disk") +
        bar(usage.bandwidth_used_bytes, usage.bandwidth_quota_bytes, "Bandwidth (current log period)");
    }).catch((ex) => { document.getElementById("usage-root").textContent = ex.message; });
    // Change password is admin-managed; self-service hits the same endpoint.
    document.getElementById("pw-form").onsubmit = async (e) => {
      e.preventDefault();
      const oldPw = document.getElementById("pw-old").value;
      const newPw = document.getElementById("pw-new").value;
      if (newPw.length < 8) return toast("Password must be at least 8 characters", "warn");
      try {
        // Verify current password via login, then reset.
        await loginMaybe2FA(u.username, oldPw);
        await api.post(`/users/${u.id}/reset-password`, { password: newPw });
        // Session was revoked by the reset — sign back in with the new password.
        const data = await loginMaybe2FA(u.username, newPw);
        api.setToken(data.token);
        toast("Password updated");
        document.getElementById("pw-old").value = "";
        document.getElementById("pw-new").value = "";
      } catch (ex) { toast(ex.message, "err"); }
    };

    document.getElementById("totp-toggle").onclick = async () => {
      if (u.totp_enabled) {
        const vals = await promptDialog("Disable two-factor authentication", [
          { name: "password", label: "Current password", type: "password", required: true },
        ], { okText: "Disable" });
        if (!vals) return;
        try {
          await api.post("/auth/totp/disable", { password: vals.password });
          toast("Two-factor authentication disabled");
          u.totp_enabled = false;
          refresh();
        } catch (ex) { toast(ex.message, "err"); }
        return;
      }

      let enroll;
      try { enroll = await api.post("/auth/totp/enroll", {}); }
      catch (ex) { toast(ex.message, "err"); return; }

      const confirmBtn = document.createElement("button");
      confirmBtn.className = "btn btn-primary";
      confirmBtn.textContent = "Confirm";
      const cancelBtn = document.createElement("button");
      cancelBtn.className = "btn";
      cancelBtn.textContent = "Cancel";
      const enrollModal = modal({
        title: "Enable two-factor authentication",
        body: `<div>
          <p class="small dim">Add this key to your authenticator app (Google Authenticator, Authy, 1Password, Bitwarden…) — enter it manually or open the link below on the same device as the app.</p>
          <div class="creds-box mono" style="word-break:break-all;user-select:all;margin:10px 0">${esc(enroll.secret)}</div>
          <p class="small dim mono" style="word-break:break-all"><a href="${esc(enroll.otpauth_uri)}">${esc(enroll.otpauth_uri)}</a></p>
          <label class="field" style="margin-top:14px">
            <span class="field-label">Code from your app</span>
            <input type="text" id="totp-confirm-code" inputmode="numeric" class="mono" autocomplete="one-time-code" placeholder="123456">
          </label>
          <p id="totp-confirm-error" class="login-error" role="alert"></p>
        </div>`,
        actions: [cancelBtn, confirmBtn],
      });
      cancelBtn.onclick = () => enrollModal.close();
      confirmBtn.onclick = async () => {
        const code = document.getElementById("totp-confirm-code").value.trim();
        const err = document.getElementById("totp-confirm-error");
        if (!code) { err.textContent = "Enter the code from your authenticator app."; return; }
        confirmBtn.classList.add("btn-busy");
        try {
          const res = await api.post("/auth/totp/confirm", { code });
          enrollModal.close();
          u.totp_enabled = true;
          showBackupCodes(res.backup_codes);
          refresh();
        } catch (ex) {
          err.textContent = ex.message;
        } finally {
          confirmBtn.classList.remove("btn-busy");
        }
      };
    };
  },
});

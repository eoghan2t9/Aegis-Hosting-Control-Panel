import { addRoute, me, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, statusTag, pageHead, loading, modal, fmtAgo } from "../ui.js";

addRoute("/ftp", {
  title: "FTP",
  icon: "server",
  group: "Data",
  order: 2,
  feature: "ftp",
  render: async (view) => {
    view.innerHTML = pageHead("FTP accounts", "FTP access to your files, chrooted to your account. The primary account matches your panel login; extra accounts are rooted inside your home.", `
      <button class="btn btn-primary" id="btn-ftp">${icon("plus")} New account</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="ftp-root">${loading()}</div>`);
    const [accounts, user] = await Promise.all([api.get("/ftp/accounts").catch(() => []), Promise.resolve(me())]);
    const root = document.getElementById("ftp-root");
    const host = location.hostname;

    if (!accounts.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">⇅</span><p>No FTP accounts yet. Create one to upload files to your home directory.</p></div>`;
    } else {
      root.innerHTML = `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Username</th><th>Home</th><th>State</th><th>Created</th><th></th></tr></thead>
        <tbody>${accounts.map((a) => `
          <tr data-id="${a.id}">
            <td><b class="mono">${esc(a.username)}</b></td>
            <td class="mono small dim">${esc(a.home_dir)}</td>
            <td>${a.enabled ? statusTag("active") : statusTag("suspended")}</td>
            <td class="small dim">${fmtAgo(a.created_at)}</td>
            <td><div class="row-actions">
              ${a.enabled && (a.webftp_domains || []).length ? `<button class="btn btn-ghost act-web" title="Open in Web FTP (logs you in)">${icon("globe")}</button>` : ""}
              <button class="btn btn-ghost act-cred" title="Connection details">${icon("eye")}</button>
              <button class="btn btn-ghost act-pass" title="Reset password">${icon("key")}</button>
              <button class="btn btn-ghost act-tog" title="Enable/disable">${icon("toggle")}</button>
              <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
            </div></td>
          </tr>`).join("")}</tbody></table></div>
      <div class="card" style="margin-top:16px">
        <div class="card-head"><span class="card-title">Connection details</span></div>
        <div class="creds-box">
          host: <b>${esc(host)}</b> · port: <b>21</b> · passive ports <b>40000–40100</b><br>
          primary username: <b>${esc(user.username)}</b><br>
          <span class="small dim">FTPS (explicit TLS) is supported by the vsftpd config.</span>
        </div>
      </div>`;
    }

    root.querySelectorAll("tbody tr").forEach((tr) => {
      const id = +tr.dataset.id;
      const acct = accounts.find((a) => a.id === id);
      if (!acct) return;
      tr.querySelector(".act-web")?.addEventListener("click", () => openWebFTP(acct));
      tr.querySelector(".act-cred")?.addEventListener("click", () => credsModal(acct));
      tr.querySelector(".act-pass")?.addEventListener("click", async () => {
        const vals = await promptDialog(`Reset password for ${acct.username}`, [
          { name: "password", label: "New password", type: "password", required: true, help: "Also updates the system account, so FTP login changes immediately." },
        ]);
        if (!vals) return;
        try { await api.post(`/ftp/accounts/${acct.id}/password`, { password: vals.password }); toast("Password updated"); }
        catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelector(".act-tog")?.addEventListener("click", async () => {
        try {
          await api.post(`/ftp/accounts/${acct.id}/toggle`, { enabled: !acct.enabled });
          toast(acct.enabled ? "Account disabled" : "Account enabled");
          refresh();
        } catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelector(".act-del")?.addEventListener("click", async () => {
        if (!await confirmDialog(`Delete FTP account ${acct.username}?`, { danger: true, title: "Delete FTP account" })) return;
        try { await api.del(`/ftp/accounts/${acct.id}`); toast("Account deleted"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
    });

    document.getElementById("btn-ftp").onclick = async () => {
      const vals = await promptDialog("New FTP account", [
        { name: "username", label: "Username", mono: true, required: true, help: "Lowercase letters, digits, underscores. Created inside your home." },
        { name: "password", label: "Password", type: "password", required: true },
      ]);
      if (!vals) return;
      try {
        await api.post("/ftp/accounts", vals);
        toast("FTP account created");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    };
  },
});

// Opens the account's Web FTP already logged in. The server hands back a
// one-time token (valid ~60s) which is POSTed to the Web FTP host from a form,
// so it never appears in a URL, history or access logs. The window is opened
// synchronously from the click so pop-up blockers allow it.
async function openWebFTP(acct) {
  const domains = acct.webftp_domains || [];
  let domain = domains[0];
  if (domains.length > 1) {
    const vals = await promptDialog(`Open Web FTP for ${acct.username}`, [
      { name: "domain", label: "Domain", type: "select", options: domains.map((d) => ({ value: d, label: d })) },
    ]);
    if (!vals) return;
    domain = vals.domain;
  }
  const name = "wf_" + Math.random().toString(36).slice(2);
  const win = window.open("", name);
  if (win) win.opener = null; // the Web FTP site must not be able to reach back into the panel
  try {
    const r = await api.post(`/ftp/accounts/${acct.id}/webftp`, { domain });
    const form = document.createElement("form");
    form.method = "post";
    form.action = r.url;
    form.target = win ? name : "_blank";
    form.style.display = "none";
    const input = document.createElement("input");
    input.type = "hidden";
    input.name = "t";
    input.value = r.token;
    form.appendChild(input);
    document.body.appendChild(form);
    form.submit();
    form.remove();
    if (!win) toast("Allow pop-ups for this site to open Web FTP", "warn");
  } catch (ex) {
    win?.close();
    toast(ex.message, "err");
  }
}

function credsModal(acct) {
  const host = location.hostname;
  const close = btn("Close");
  const cm = modal({
    title: "FTP details — " + acct.username,
    body: `<div>
      <div class="creds-box">host <b>${esc(host)}</b> · port <b>21</b><br>username <b>${esc(acct.username)}</b><br>home <span class="dim">${esc(acct.home_dir)}</span></div>
      <p class="small muted">Use any FTP client. FTPS available with explicit TLS. Passive range 40000–40100.</p>
    </div>`,
    actions: [close],
  });
  close.onclick = () => cm.close();
}

function btn(text, cls = "btn") {
  const b = document.createElement("button");
  b.className = cls;
  b.textContent = text;
  return b;
}

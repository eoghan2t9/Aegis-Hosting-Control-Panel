import { addRoute } from "../app.js";
import { icon, esc, toast, pageHead, loading, modal, fmtAgo } from "../ui.js";

const TOKEN_KEY = "aegis.webmail.token";
const ADDR_KEY = "aegis.webmail.address";

// Deliberately not the shared `api` client: webmail uses its own short-lived
// credential session (X-Webmail-Token), independent of the panel JWT, and a
// 401 here must not log the whole panel out.
async function wmFetch(path, opts = {}) {
  const token = sessionStorage.getItem(TOKEN_KEY);
  const headers = { "Content-Type": "application/json" };
  if (token) headers["X-Webmail-Token"] = token;
  const res = await fetch("/api/webmail" + path, { ...opts, headers });
  if (res.status === 401) {
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(ADDR_KEY);
    throw Object.assign(new Error("Webmail session expired"), { status: 401 });
  }
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) throw new Error((data && data.error) || res.statusText || "Request failed");
  return data;
}

addRoute("/webmail", {
  title: "Webmail",
  icon: "mail",
  group: "Email",
  order: 1,
  render: async (view) => {
    const token = sessionStorage.getItem(TOKEN_KEY);
    const address = sessionStorage.getItem(ADDR_KEY);
    if (token && address) {
      try { return await renderInbox(view, address); }
      catch (ex) { if (ex.status !== 401) { view.innerHTML = `<div class="card"><p class="muted">${esc(ex.message)}</p></div>`; return; } }
    }
    renderLogin(view);
  },
});

function renderLogin(view) {
  view.innerHTML = `<div class="card" style="max-width:420px;margin:40px auto">
    <div class="card-head"><span class="card-title">Webmail login</span></div>
    <form id="wm-login-form">
      <label class="field"><span class="field-label">Address</span>
        <input name="address" type="email" required placeholder="you@example.com" autocomplete="username"></label>
      <label class="field" style="margin-top:12px"><span class="field-label">Password</span>
        <input name="password" type="password" required autocomplete="current-password"></label>
      <div id="wm-login-error" class="small" style="color:var(--danger,#e5484d);min-height:18px;margin-top:8px"></div>
      <button class="btn btn-primary" type="submit" style="margin-top:8px;width:100%">Sign in</button>
    </form>
  </div>`;
  const form = document.getElementById("wm-login-form");
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const err = document.getElementById("wm-login-error");
    err.textContent = "";
    const address = form.address.value.trim();
    const password = form.password.value;
    try {
      const res = await fetch("/api/webmail/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ address, password }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(data.error || "Login failed");
      sessionStorage.setItem(TOKEN_KEY, data.token);
      sessionStorage.setItem(ADDR_KEY, data.address);
      await renderInbox(view, data.address);
    } catch (ex) {
      err.textContent = ex.message;
    }
  });
}

async function renderInbox(view, address) {
  view.innerHTML = pageHead("Webmail — " + address, "", `
    <button class="btn" id="wm-logout">Log out</button>
    <button class="btn btn-primary" id="wm-compose">${icon("send")} Compose</button>`);
  view.insertAdjacentHTML("beforeend", `<div id="wm-root">${loading()}</div>`);
  const root = document.getElementById("wm-root");

  document.getElementById("wm-logout").onclick = async () => {
    await wmFetch("/logout", { method: "POST" }).catch(() => {});
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(ADDR_KEY);
    location.reload();
  };
  document.getElementById("wm-compose").onclick = () => composeModal(address);

  let messages;
  try {
    messages = await wmFetch("/messages");
  } catch (ex) {
    if (ex.status === 401) { renderLogin(view); return; }
    root.innerHTML = `<div class="card"><p class="muted">${esc(ex.message)}</p></div>`;
    return;
  }

  if (!messages.length) {
    root.innerHTML = `<div class="card empty-state"><span class="glyph">✉</span><p>Inbox is empty.</p></div>`;
    return;
  }
  root.innerHTML = `<div class="tbl-wrap"><table class="tbl">
    <thead><tr><th>From</th><th>Subject</th><th>Date</th></tr></thead>
    <tbody>${messages.map((m) => `<tr data-uid="${m.uid}" class="${m.seen ? "" : "unseen"}" style="cursor:pointer">
      <td class="mono small">${esc(m.from || "(unknown)")}</td>
      <td>${esc(m.subject || "(no subject)")}</td>
      <td class="small dim">${fmtAgo(m.date)}</td>
    </tr>`).join("")}</tbody></table></div>`;
  root.querySelectorAll("tbody tr").forEach((tr) => {
    tr.addEventListener("click", () => openMessage(+tr.dataset.uid));
  });
}

async function openMessage(uid) {
  let msg;
  try { msg = await wmFetch("/messages/" + uid); }
  catch (ex) { toast(ex.message, "err"); return; }
  const bodyHTML = msg.html_body
    ? `<div class="wm-body">${msg.html_body}</div>`
    : `<pre class="mono small" style="white-space:pre-wrap">${esc(msg.text_body || "(empty message)")}</pre>`;
  const close = closeBtn();
  const rm = modal({
    title: msg.subject || "(no subject)",
    wide: true,
    body: `<p class="small dim">From ${esc(msg.from)} &middot; ${esc(new Date(msg.date).toLocaleString())}</p>${bodyHTML}`,
    actions: [close],
  });
  close.onclick = () => rm.close();
}

function composeModal(fromAddress) {
  const body = document.createElement("div");
  body.innerHTML = `
    <label class="field"><span class="field-label">To</span><input name="to" type="email" required></label>
    <label class="field" style="margin-top:12px"><span class="field-label">Subject</span><input name="subject"></label>
    <label class="field" style="margin-top:12px"><span class="field-label">Message</span>
      <textarea name="body" rows="10" style="width:100%;resize:vertical"></textarea></label>`;
  const send = document.createElement("button");
  send.className = "btn btn-primary";
  send.textContent = "Send";
  const cancel = document.createElement("button");
  cancel.className = "btn";
  cancel.textContent = "Cancel";
  const m = modal({ title: "New message — from " + fromAddress, wide: true, body, actions: [cancel, send] });
  cancel.onclick = () => m.close();
  send.onclick = async () => {
    const to = body.querySelector('[name="to"]').value.trim();
    const subject = body.querySelector('[name="subject"]').value;
    const text = body.querySelector('[name="body"]').value;
    if (!to) { toast("A recipient is required", "warn"); return; }
    try {
      await wmFetch("/send", { method: "POST", body: JSON.stringify({ to, subject, body: text }) });
      toast("Message sent");
      m.close();
    } catch (ex) { toast(ex.message, "err"); }
  };
}

function closeBtn() {
  const b = document.createElement("button");
  b.className = "btn";
  b.textContent = "Close";
  return b;
}

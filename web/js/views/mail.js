import { addRoute, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, statusTag, pageHead, loading, modal, fmtAgo } from "../ui.js";

addRoute("/mail", {
  title: "Email",
  icon: "mail",
  group: "Web",
  render: async (view) => {
    view.innerHTML = pageHead("Email", "Mailboxes, aliases and SPF/DKIM/DMARC for your domains. Enabling mail publishes DNS records automatically into the DNS tab.", "");
    view.insertAdjacentHTML("beforeend", `<div id="mail-root">${loading()}</div>`);
    const [domains, mailDomains] = await Promise.all([
      api.get("/domains").catch(() => []),
      api.get("/mail/domains").catch(() => []),
    ]);
    const root = document.getElementById("mail-root");
    const byDomainID = new Map(mailDomains.map((md) => [md.domain_id, md]));
    const notEnabled = domains.filter((d) => !byDomainID.has(d.id));

    let html = "";
    if (notEnabled.length) {
      html += `<div class="card" style="margin-bottom:16px">
        <div class="card-head"><span class="card-title">Enable mail for a domain</span></div>
        <div class="tbl-wrap"><table class="tbl"><tbody>
          ${notEnabled.map((d) => `<tr data-domain-id="${d.id}">
            <td>${esc(d.domain)}</td>
            <td style="text-align:right"><button class="btn btn-primary btn-sm act-enable">${icon("plus")} Enable mail</button></td>
          </tr>`).join("")}
        </tbody></table></div>
      </div>`;
    }
    if (!mailDomains.length && !notEnabled.length) {
      html += `<div class="card empty-state"><span class="glyph">✉</span><p>No domains yet. Create a domain first.</p></div>`;
    }
    for (const md of mailDomains) {
      html += `<div class="card mail-domain-card" data-id="${md.id}" style="margin-bottom:16px">
        <div class="card-head">
          <span class="card-title">${esc(md.domain)}</span>
          <div class="row-actions">
            <button class="btn btn-ghost act-dkim" title="Show DKIM key">${icon("eye")}</button>
            <button class="btn btn-ghost act-disable" title="Disable mail">${icon("trash")}</button>
          </div>
        </div>
        <div class="card-body">
          <div class="mail-section">
            <div class="mail-section-head"><b>Mailboxes</b><button class="btn btn-sm act-new-box">${icon("plus")} New mailbox</button></div>
            <div class="boxes-root">${loading()}</div>
          </div>
          <div class="mail-section" style="margin-top:12px">
            <div class="mail-section-head"><b>Aliases</b><button class="btn btn-sm act-new-alias">${icon("plus")} New alias</button></div>
            <div class="aliases-root">${loading()}</div>
          </div>
        </div>
      </div>`;
    }
    root.innerHTML = html;

    root.querySelectorAll(".act-enable").forEach((btn) => {
      btn.addEventListener("click", async () => {
        const domainID = +btn.closest("tr").dataset.domainId;
        try {
          await api.post("/mail/domains", { domain_id: domainID });
          toast("Mail enabled — check the DNS tab for the published SPF/DKIM/DMARC records");
          refresh();
        } catch (ex) { toast(ex.message, "err"); }
      });
    });

    for (const md of mailDomains) {
      const card = root.querySelector(`.mail-domain-card[data-id="${md.id}"]`);
      loadBoxes(card, md);
      loadAliases(card, md);

      card.querySelector(".act-dkim").addEventListener("click", () => {
        modal({
          title: "DKIM key — " + md.domain,
          wide: true,
          body: `<p class="small muted">Published as a TXT record at <span class="mono">default._domainkey.${esc(md.domain)}</span>:</p>
            <pre class="mono small" style="white-space:pre-wrap;word-break:break-all">${esc(md.dkim_public_key)}</pre>`,
          actions: [closeBtn()],
        });
      });
      card.querySelector(".act-disable").addEventListener("click", async () => {
        if (!await confirmDialog(`Disable mail for ${md.domain}? All mailboxes and aliases are removed.`, { danger: true, title: "Disable mail" })) return;
        try { await api.del(`/mail/domains/${md.id}`); toast("Mail disabled"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
      card.querySelector(".act-new-box").addEventListener("click", async () => {
        const vals = await promptDialog(`New mailbox @ ${md.domain}`, [
          { name: "localpart", label: "Mailbox name", mono: true, required: true, help: "The part before @" + md.domain },
          { name: "password", label: "Password", type: "password", required: true },
        ]);
        if (!vals) return;
        try {
          await api.post(`/mail/domains/${md.id}/mailboxes`, { localpart: vals.localpart, password: vals.password, quota_bytes: 0 });
          toast("Mailbox created");
          refresh();
        } catch (ex) { toast(ex.message, "err"); }
      });
      card.querySelector(".act-new-alias").addEventListener("click", async () => {
        const vals = await promptDialog(`New alias @ ${md.domain}`, [
          { name: "source", label: "Alias name", mono: true, required: true, help: "The part before @" + md.domain },
          { name: "destination", label: "Forwards to", mono: true, required: true, placeholder: "someone@example.com" },
        ]);
        if (!vals) return;
        try {
          await api.post(`/mail/domains/${md.id}/aliases`, vals);
          toast("Alias created");
          refresh();
        } catch (ex) { toast(ex.message, "err"); }
      });
    }
  },
});

async function loadBoxes(card, md) {
  const boxesRoot = card.querySelector(".boxes-root");
  const boxes = await api.get(`/mail/domains/${md.id}/mailboxes`).catch(() => []);
  if (!boxes.length) {
    boxesRoot.innerHTML = `<p class="small dim">No mailboxes yet.</p>`;
    return;
  }
  boxesRoot.innerHTML = `<div class="tbl-wrap"><table class="tbl">
    <thead><tr><th>Address</th><th>State</th><th>Created</th><th></th></tr></thead>
    <tbody>${boxes.map((b) => `<tr data-id="${b.id}">
      <td class="mono">${esc(b.localpart)}@${esc(md.domain)}</td>
      <td>${b.enabled ? statusTag("active") : statusTag("suspended")}</td>
      <td class="small dim">${fmtAgo(b.created_at)}</td>
      <td><div class="row-actions">
        <button class="btn btn-ghost act-pass" title="Reset password">${icon("key")}</button>
        <button class="btn btn-ghost act-tog" title="Enable/disable">${icon("toggle")}</button>
        <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
      </div></td>
    </tr>`).join("")}</tbody></table></div>`;
  boxesRoot.querySelectorAll("tbody tr").forEach((tr) => {
    const box = boxes.find((b) => b.id === +tr.dataset.id);
    tr.querySelector(".act-pass").addEventListener("click", async () => {
      const vals = await promptDialog(`Reset password for ${box.localpart}@${md.domain}`, [
        { name: "password", label: "New password", type: "password", required: true },
      ]);
      if (!vals) return;
      try { await api.post(`/mail/mailboxes/${box.id}/password`, vals); toast("Password updated"); }
      catch (ex) { toast(ex.message, "err"); }
    });
    tr.querySelector(".act-tog").addEventListener("click", async () => {
      try { await api.post(`/mail/mailboxes/${box.id}/toggle`, { enabled: !box.enabled }); refresh(); }
      catch (ex) { toast(ex.message, "err"); }
    });
    tr.querySelector(".act-del").addEventListener("click", async () => {
      if (!await confirmDialog(`Delete mailbox ${box.localpart}@${md.domain}?`, { danger: true, title: "Delete mailbox" })) return;
      try { await api.del(`/mail/mailboxes/${box.id}`); toast("Mailbox deleted"); refresh(); }
      catch (ex) { toast(ex.message, "err"); }
    });
  });
}

async function loadAliases(card, md) {
  const aliasesRoot = card.querySelector(".aliases-root");
  const aliases = await api.get(`/mail/domains/${md.id}/aliases`).catch(() => []);
  if (!aliases.length) {
    aliasesRoot.innerHTML = `<p class="small dim">No aliases yet.</p>`;
    return;
  }
  aliasesRoot.innerHTML = `<div class="tbl-wrap"><table class="tbl">
    <thead><tr><th>From</th><th>Forwards to</th><th></th></tr></thead>
    <tbody>${aliases.map((a) => `<tr data-id="${a.id}">
      <td class="mono">${esc(a.source)}@${esc(md.domain)}</td>
      <td class="mono">${esc(a.destination)}</td>
      <td><button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button></td>
    </tr>`).join("")}</tbody></table></div>`;
  aliasesRoot.querySelectorAll("tbody tr").forEach((tr) => {
    const id = +tr.dataset.id;
    tr.querySelector(".act-del").addEventListener("click", async () => {
      if (!await confirmDialog("Delete this alias?", { danger: true, title: "Delete alias" })) return;
      try { await api.del(`/mail/aliases/${id}`); toast("Alias deleted"); refresh(); }
      catch (ex) { toast(ex.message, "err"); }
    });
  });
}

function closeBtn() {
  const b = document.createElement("button");
  b.className = "btn";
  b.textContent = "Close";
  return b;
}

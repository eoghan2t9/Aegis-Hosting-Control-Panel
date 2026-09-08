import { addRoute, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, pageHead, loading, modal, fmtAgo, fmtDate } from "../ui.js";

addRoute("/tokens", {
  title: "API tokens",
  icon: "key",
  group: "Account",
  order: 1,
  render: async (view) => {
    view.innerHTML = pageHead("API tokens", "Scoped credentials for scripting against the panel. Send as a Bearer token instead of logging in.", `
      <button class="btn btn-primary" id="btn-token">${icon("plus")} New token</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="tok-root">${loading()}</div>`);
    const tokens = await api.get("/tokens").catch(() => []);
    const root = document.getElementById("tok-root");

    if (!tokens.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">🔑</span><p>No API tokens yet.</p></div>`;
    } else {
      root.innerHTML = `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Label</th><th>Last used</th><th>Expires</th><th>Created</th><th></th></tr></thead>
        <tbody>${tokens.map((t) => `
          <tr data-id="${t.id}">
            <td><b>${esc(t.label || "(unlabeled)")}</b></td>
            <td class="small dim">${t.last_used_at ? fmtAgo(t.last_used_at) : "never"}</td>
            <td class="small dim">${t.expires_at ? fmtDate(t.expires_at) : "never"}</td>
            <td class="small dim">${fmtAgo(t.created_at)}</td>
            <td><button class="btn btn-ghost act-del" title="Revoke">${icon("trash")}</button></td>
          </tr>`).join("")}</tbody></table></div>`;
    }

    root.querySelectorAll(".act-del").forEach((btn) => {
      const id = +btn.closest("tr").dataset.id;
      btn.addEventListener("click", async () => {
        if (!await confirmDialog("Revoke this token? Anything using it stops working immediately.", { danger: true, title: "Revoke token" })) return;
        try { await api.del(`/tokens/${id}`); toast("Token revoked"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
    });

    document.getElementById("btn-token").onclick = async () => {
      const vals = await promptDialog("New API token", [
        { name: "label", label: "Label", required: true, placeholder: "e.g. deploy script" },
        { name: "ttl_days", label: "Expires after (days)", type: "number", value: "0", help: "0 = never expires" },
      ]);
      if (!vals) return;
      try {
        const created = await api.post("/tokens", { label: vals.label, ttl_days: +vals.ttl_days || 0 });
        toast("Token created");
        const done = document.createElement("button");
        done.className = "btn btn-primary";
        done.textContent = "Done";
        const m = modal({
          title: "Copy your token now",
          wide: true,
          body: `<div>
            <p class="small muted">This is shown once. Store it somewhere safe — Aegis only keeps a hash of it.</p>
            <div class="creds-box mono" style="word-break:break-all;user-select:all">${esc(created.raw)}</div>
          </div>`,
          actions: [done],
          onClose: refresh,
        });
        done.onclick = () => m.close();
      } catch (ex) { toast(ex.message, "err"); }
    };
  },
});

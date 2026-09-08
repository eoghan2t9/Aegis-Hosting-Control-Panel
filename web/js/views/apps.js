import { addRoute } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, confirmDialog, pageHead, loading, modal } from "../ui.js";

const CATEGORY_LABELS = { cms: "CMS", forum: "Forums", wiki: "Wikis", tools: "Tools", ecommerce: "E-commerce", framework: "Frameworks" };

addRoute("/apps", {
  title: "App installer",
  icon: "plus",
  group: "Web",
  render: async (view) => {
    view.innerHTML = pageHead("App installer", "One-click install WordPress, Laravel, Drupal and more onto any of your domains.", "");
    view.insertAdjacentHTML("beforeend", `<div id="apps-root">${loading()}</div>`);
    const [domains, catalog] = await Promise.all([
      api.get("/domains").catch(() => []),
      api.get("/webapps/catalog").catch(() => []),
    ]);
    const root = document.getElementById("apps-root");
    if (!domains.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">◈</span><p>No domains yet. Add a domain first, then install an app onto it.</p></div>`;
      return;
    }
    root.innerHTML = `<div class="tbl-wrap"><table class="tbl">
      <thead><tr><th>Domain</th><th>Document root</th><th></th></tr></thead>
      <tbody>${domains.map((d) => `<tr data-id="${d.id}">
        <td><b class="mono">${esc(d.domain)}</b></td>
        <td class="small dim mono">${esc(d.document_root)}</td>
        <td><button class="btn btn-sm act-install">${icon("plus")} Install an app…</button></td>
      </tr>`).join("")}</tbody></table></div>`;

    root.querySelectorAll(".act-install").forEach((btn) => {
      const dom = domains.find((d) => d.id === +btn.closest("tr").dataset.id);
      btn.addEventListener("click", () => appPickerDialog(dom, catalog));
    });
  },
});

function appPickerDialog(dom, catalog) {
  const byCategory = {};
  for (const a of catalog) (byCategory[a.category] ||= []).push(a);

  const body = document.createElement("div");
  body.innerHTML = Object.entries(byCategory).map(([cat, list]) => `
    <div style="margin-bottom:14px">
      <b class="small" style="text-transform:uppercase;letter-spacing:.1em;color:var(--text-3)">${esc(CATEGORY_LABELS[cat] || cat)}</b>
      <div style="display:flex;flex-wrap:wrap;gap:8px;margin-top:8px">
        ${list.map((a) => `<button class="btn btn-sm app-pick" data-id="${esc(a.id)}">${esc(a.name)}</button>`).join("")}
      </div>
    </div>`).join("");
  const cancel = document.createElement("button");
  cancel.className = "btn";
  cancel.textContent = "Cancel";
  const pm = modal({ title: "Install an app — " + dom.domain, wide: true, body, actions: [cancel] });
  cancel.onclick = () => pm.close();
  body.querySelectorAll(".app-pick").forEach((btn) => {
    btn.onclick = async () => {
      const appId = btn.dataset.id;
      const name = btn.textContent;
      pm.close();
      if (!await confirmDialog(`Install ${name} into ${dom.document_root}? The document root must be empty (aside from the default placeholder page). This can take a minute or two.`, { title: "Install " + name, okText: "Install" })) return;
      try {
        toast(`Installing ${name}… this can take a minute`);
        await api.post(`/domains/${dom.id}/install`, { app: appId });
        toast(`${name} installed — check .aegis-credentials.txt in the document root for the admin login, if one was created`);
      } catch (ex) { toast(ex.message, "err"); }
    };
  });
}

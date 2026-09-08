import { addRoute } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, confirmDialog, pageHead, loading, modal } from "../ui.js";

const CATEGORIES = [
  { id: "", label: "All apps", icon: "grid" },
  { id: "cms", label: "CMS", icon: "file" },
  { id: "forum", label: "Forums", icon: "users" },
  { id: "wiki", label: "Wikis", icon: "list" },
  { id: "ecommerce", label: "E-commerce", icon: "dollar" },
  { id: "tools", label: "Tools", icon: "wrench" },
  { id: "framework", label: "Frameworks", icon: "zap" },
];
const CATEGORY_LABEL = Object.fromEntries(CATEGORIES.map((c) => [c.id, c.label]));
// Letter-badge colors, cycled by category so the grid reads as organized
// even without real app artwork.
const CAT_TAG = { cms: "tag-lime", forum: "tag-teal", wiki: "tag-teal", ecommerce: "tag-amber", tools: "tag-amber", framework: "tag-red" };

addRoute("/apps", {
  title: "App installer",
  icon: "plus",
  group: "Web",
  render: async (view) => {
    view.innerHTML = pageHead("App installer", "One-click install WordPress, Laravel, Drupal and more onto any of your domains.", "");
    view.insertAdjacentHTML("beforeend", `
      <div class="toolbar">
        <input type="search" id="app-q" placeholder="Search apps…">
        <span class="spacer"></span>
      </div>
      <div class="tab-row" id="cat-tabs"></div>
      <div id="apps-root">${loading()}</div>`);

    const [domains, catalog] = await Promise.all([
      api.get("/domains").catch(() => []),
      api.get("/webapps/catalog").catch(() => []),
    ]);

    const tabs = document.getElementById("cat-tabs");
    tabs.innerHTML = CATEGORIES.map((c, i) => {
      const count = c.id ? catalog.filter((a) => a.category === c.id).length : catalog.length;
      if (c.id && count === 0) return "";
      return `<button class="tab-btn ${i === 0 ? "active" : ""}" data-cat="${c.id}">${icon(c.icon)} ${esc(c.label)} <span class="dim small">${count}</span></button>`;
    }).join("");

    let activeCat = "";
    let query = "";
    const root = document.getElementById("apps-root");

    const renderGrid = () => {
      const filtered = catalog.filter((a) => {
        if (activeCat && a.category !== activeCat) return false;
        if (query && !(a.name + a.description).toLowerCase().includes(query)) return false;
        return true;
      });
      if (!catalog.length) {
        root.innerHTML = `<div class="card empty-state"><span class="glyph">◈</span><p>App catalog unavailable — check the server log.</p></div>`;
        return;
      }
      if (!filtered.length) {
        root.innerHTML = `<div class="card empty-state"><span class="glyph">◈</span><p>No apps match "${esc(query)}".</p></div>`;
        return;
      }
      root.innerHTML = `<div class="app-grid">${filtered.map((a) => `
        <div class="app-card" data-id="${esc(a.id)}">
          <div class="app-card-head">
            <span class="app-badge ${CAT_TAG[a.category] || ""}">${esc(a.name.slice(0, 2).toUpperCase())}</span>
            <div>
              <div class="app-card-name">${esc(a.name)}</div>
              <div class="app-card-vendor small dim">${esc(a.vendor || "")}</div>
            </div>
            <span class="tag ${CAT_TAG[a.category] || ""}" style="margin-left:auto">${esc(CATEGORY_LABEL[a.category] || a.category)}</span>
          </div>
          <p class="app-card-desc small">${esc(a.description || "")}</p>
          <div class="app-card-foot">
            <span class="small dim">${a.needs_database ? icon("database") + " Needs a database" : icon("check") + " No database required"}</span>
            <button class="btn btn-sm btn-primary act-install">${icon("plus")} Install</button>
          </div>
        </div>`).join("")}</div>`;
      root.querySelectorAll(".act-install").forEach((btn) => {
        const app = catalog.find((a) => a.id === btn.closest(".app-card").dataset.id);
        btn.addEventListener("click", () => installDialog(app, domains));
      });
    };

    tabs.querySelectorAll(".tab-btn").forEach((b) => {
      b.onclick = () => {
        tabs.querySelectorAll(".tab-btn").forEach((x) => x.classList.toggle("active", x === b));
        activeCat = b.dataset.cat;
        renderGrid();
      };
    });
    document.getElementById("app-q").addEventListener("input", (e) => {
      query = e.target.value.trim().toLowerCase();
      renderGrid();
    });

    renderGrid();
  },
});

function installDialog(app, domains) {
  const body = document.createElement("div");
  if (!domains.length) {
    body.innerHTML = `<p class="muted">No domains yet. Add a domain first, then install an app onto it.</p>`;
    const close = document.createElement("button");
    close.className = "btn";
    close.textContent = "Close";
    const pm = modal({ title: "Install " + app.name, body, actions: [close] });
    close.onclick = () => pm.close();
    return;
  }

  body.innerHTML = `
    <div style="display:flex;gap:14px;align-items:flex-start;margin-bottom:16px">
      <span class="app-badge ${CAT_TAG[app.category] || ""}" style="flex-shrink:0">${esc(app.name.slice(0, 2).toUpperCase())}</span>
      <div>
        <p class="small">${esc(app.description || "")}</p>
        <p class="small dim">${app.vendor ? esc(app.vendor) + " · " : ""}${app.needs_database ? "Provisions its own database automatically." : "No database required."}</p>
      </div>
    </div>
    <label class="field">
      <span class="field-label">Install onto domain</span>
      <select id="install-domain">
        ${domains.map((d) => `<option value="${d.id}">${esc(d.domain)} — ${esc(d.document_root)}</option>`).join("")}
      </select>
    </label>
    <p class="small dim" style="margin-top:10px">The document root must be empty (aside from the default placeholder page). This can take a minute or two — you'll get a toast when it's done.</p>`;

  const install = document.createElement("button");
  install.className = "btn btn-primary";
  install.textContent = "Install " + app.name;
  const cancel = document.createElement("button");
  cancel.className = "btn";
  cancel.textContent = "Cancel";
  const pm = modal({ title: "Install " + app.name, wide: true, body, actions: [cancel, install] });
  cancel.onclick = () => pm.close();

  install.onclick = async () => {
    const domId = +document.getElementById("install-domain").value;
    const dom = domains.find((d) => d.id === domId);
    pm.close();
    if (!await confirmDialog(`Install ${app.name} into ${dom.document_root}?`, { title: "Confirm install", okText: "Install" })) return;
    try {
      toast(`Installing ${app.name} onto ${dom.domain}… this can take a minute`);
      await api.post(`/domains/${dom.id}/install`, { app: app.id });
      toast(`${app.name} installed on ${dom.domain} — check .aegis-credentials.txt in the document root for the admin login, if one was created`);
    } catch (ex) { toast(ex.message, "err"); }
  };
}

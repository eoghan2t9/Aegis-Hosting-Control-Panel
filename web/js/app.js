// Aegis frontend bootstrap: login flow, shell layout, hash-based router.
import { api, qs } from "./api.js";
import { icon, esc, toast, debounce } from "./ui.js";

// Views register themselves here: route -> {title, render, admin?, group, order}
export const routes = {};

// Sidebar group display order. A route's position within its group comes
// from its own `order` field (see buildShell's nav-construction loop).
const GROUP_ORDER = ["Overview", "Websites", "Email", "Data", "Server", "Account", "Users"];

export function addRoute(path, def) {
  routes[path] = def;
}

async function boot() {
  // Login form.
  const loginForm = document.getElementById("login-form");
  const hint = document.getElementById("login-hint");
  if (!api.token) {
    const res = await api.get("/system/overview").catch(() => null);
    if (res?.hostname) {
      hint.innerHTML = `host: ${esc(res.hostname)} · kernel ${esc(res.kernel)}`;
    }
  }
  loginForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const err = document.getElementById("login-error");
    err.textContent = "";
    const btn = loginForm.querySelector("button");
    btn.classList.add("btn-busy");
    try {
      const data = await api.post("/auth/login", {
        username: document.getElementById("login-user").value.trim(),
        password: document.getElementById("login-pass").value,
      });
      api.setToken(data.token);
      await enterApp();
    } catch (ex) {
      err.textContent = ex.message;
    } finally {
      btn.classList.remove("btn-busy");
    }
  });

  window.addEventListener("aegis:logout", () => {
    document.getElementById("screen-app").classList.add("hidden");
    document.getElementById("screen-login").classList.remove("hidden");
    showLoginHint();
  });

  if (api.token) {
    try {
      await enterApp();
    } catch {
      document.getElementById("screen-login").classList.remove("hidden");
      showLoginHint();
    }
  } else {
    document.getElementById("screen-login").classList.remove("hidden");
  }
}

async function showLoginHint() {
  const hint = document.getElementById("login-hint");
  const res = await api.get("/system/overview").catch(() => null);
  if (res?.hostname) hint.innerHTML = `host: ${esc(res.hostname)}`;
}

let state = { user: null, claims: null };

export function me() { return state.user; }
export function isAdmin() { return state.user && state.user.role === "admin"; }
export function isReseller() { return state.user && (state.user.role === "admin" || state.user.role === "reseller"); }

async function enterApp() {
  document.getElementById("screen-login").classList.add("hidden");
  const meData = await api.get("/auth/me");
  state.user = meData.user;
  state.claims = meData.claims;
  document.getElementById("screen-app").classList.remove("hidden");
  buildShell();
  startPoller();
  if (isAdmin()) startUpdateBadgePoller();
  navigate(location.hash || "#/dashboard");
}

function buildShell() {
  const u = state.user;
  document.getElementById("sidebar-node").textContent = u.role;
  const chip = document.getElementById("user-chip");
  chip.innerHTML = `
    <span class="avatar">${esc(u.username.slice(0, 2))}</span>
    <span class="user-meta"><span class="user-name">${esc(u.username)}</span>
      <span class="user-role">${esc(u.role)}</span></span>
    <span class="user-actions">
      <button id="btn-logout" class="btn btn-ghost" title="Log out">${icon("logout")}</button>
    </span>`;
  document.getElementById("btn-logout").onclick = async () => {
    await api.post("/auth/logout").catch(() => {});
    api.setToken("");
    location.reload();
  };

  // Impersonation banner.
  const bar = document.getElementById("impersonate-bar");
  if (state.claims.impersonating) {
    document.getElementById("imp-user").textContent = u.username;
    bar.classList.remove("hidden");
    document.getElementById("imp-stop").onclick = async () => {
      try {
        const data = await api.post("/admin/unimpersonate");
        api.setToken(data.token);
      } catch {
        api.setToken(""); // couldn't recover an admin token — fall back to login
      }
      location.hash = "#/dashboard";
      location.reload();
    };
  } else {
    bar.classList.add("hidden");
  }

  // Navigation. Group and item order is explicit (GROUP_ORDER + each
  // route's `order`) rather than accidental import/registration order.
  const nav = document.getElementById("nav");
  nav.innerHTML = "";
  const groups = {};
  const entries = Object.entries(routes)
    .filter(([, def]) => !(def.adminOnly && !isAdmin()) && !(def.resellerOnly && !isReseller()))
    .sort(([, a], [, b]) => {
      const ga = GROUP_ORDER.indexOf(a.group || ""), gb = GROUP_ORDER.indexOf(b.group || "");
      if (ga !== gb) return ga - gb;
      return (a.order ?? 0) - (b.order ?? 0);
    });
  for (const [path, def] of entries) {
    const g = def.group || "";
    if (!groups[g]) {
      groups[g] = { el: document.createElement("div"), links: [] };
      groups[g].el.className = "nav-group";
      groups[g].el.textContent = g;
    }
    const a = document.createElement("a");
    a.className = "nav-link";
    a.href = "#" + path;
    a.dataset.route = path;
    a.innerHTML = `${icon(def.icon)}<span>${esc(def.title)}</span>${path === "/updates" ? '<span class="nav-badge hidden" id="nav-badge-updates"></span>' : ""}`;
    groups[g].links.push(a);
  }
  for (const g of Object.values(groups)) {
    nav.appendChild(g.el);
    for (const a of g.links) nav.appendChild(a);
  }
  nav.querySelectorAll(".nav-link").forEach((a) => {
    a.addEventListener("click", () => {
      document.getElementById("sidebar").classList.remove("open");
      document.getElementById("sidebar-backdrop")?.remove();
    });
  });

  document.getElementById("nav-toggle").onclick = () => {
    const sb = document.getElementById("sidebar");
    sb.classList.toggle("open");
    if (sb.classList.contains("open") && !document.getElementById("sidebar-backdrop")) {
      const b = document.createElement("div");
      b.id = "sidebar-backdrop";
      b.className = "sidebar-backdrop show";
      b.onclick = () => { sb.classList.remove("open"); b.remove(); };
      document.body.appendChild(b);
    } else document.getElementById("sidebar-backdrop")?.remove();
  };
  document.title = "Aegis · " + u.username;
}

async function navigate(hash) {
  const path = (hash || "#/dashboard").slice(1).split("?")[0] || "/dashboard";
  const def = routes[path];
  if (!def) return navigate("#/dashboard");
  // Highlight nav.
  document.querySelectorAll(".nav-link").forEach((a) => {
    a.classList.toggle("active", a.dataset.route === path);
  });
  document.getElementById("page-title").textContent = def.title;
  const view = document.getElementById("view");
  view.focus({ preventScroll: true });
  view.scrollTop = 0;
  window.scrollTo(0, 0);
  try {
    await def.render(view);
  } catch (ex) {
    if (ex.status !== 401) {
      view.innerHTML = `<div class="card"><h3>Failed to load</h3><p class="muted">${esc(ex.message)}</p></div>`;
    }
  }
}

window.addEventListener("hashchange", () => navigate(location.hash));

// Re-render the current view in place (re-fetches its data) without a full
// browser reload — the live-update path every view's mutation handlers use
// instead of location.reload().
export function refresh() { return navigate(location.hash); }

/* Poll the server pill + keep overview cached for dashboard reuse. */
export const overviewCache = { data: null, at: 0 };
async function startPoller() {
  const pill = document.getElementById("server-pill");
  const txt = document.getElementById("server-pill-text");
  const dot = pill.querySelector(".dot");
  const tick = async () => {
    try {
      const ov = await api.get("/system/overview");
      overviewCache.data = ov;
      overviewCache.at = Date.now();
      txt.textContent = ov.hostname + " · load " + (ov.load?.[0] ?? 0).toFixed(1);
      dot.className = "dot " + (ov.load?.[0] > (ov.cpu_cores || 4) ? "warn" : "ok");
    } catch {
      dot.className = "dot err";
      txt.textContent = "offline";
    }
  };
  tick();
  setInterval(tick, 15000);
  const debounced = debounce(tick, 2000);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) debounced(); });
}

/* Poll the cached (not live-checked) update count for the sidebar badge. */
function startUpdateBadgePoller() {
  const tick = async () => {
    const badge = document.getElementById("nav-badge-updates");
    if (!badge) return; // route filtered out (shouldn't happen for an admin, but be defensive)
    try {
      const data = await api.get("/system/updates");
      const n = (data.updates || []).length;
      badge.textContent = n > 0 ? String(n) : "";
      badge.classList.toggle("hidden", n === 0);
    } catch {
      // Quiet failure — this is a passive background indicator, not worth a toast.
    }
  };
  tick();
  setInterval(tick, 5 * 60 * 1000);
}

// Import views (side effects register routes). Not awaited at top level:
// each view imports addRoute/isAdmin/me back from this module, and a
// top-level await here would deadlock that cycle (this module can't finish
// evaluating until the views do, but the views can't finish evaluating
// until this module does).
Promise.all([
  import("./views/dashboard.js"),
  import("./views/domains.js"),
  import("./views/dns.js"),
  import("./views/ssl.js"),
  import("./views/ftp.js"),
  import("./views/cron.js"),
  import("./views/tokens.js"),
  import("./views/security.js"),
  import("./views/mail.js"),
  import("./views/apps.js"),
  import("./views/webmail.js"),
  import("./views/databases.js"),
  import("./views/files.js"),
  import("./views/accounts.js"),
  import("./views/backups.js"),
  import("./views/system.js"),
  import("./views/updates.js"),
  import("./views/terminal.js"),
  import("./views/runtime.js"),
]).then(boot);

void qs;
void toast;

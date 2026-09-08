import { addRoute } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, confirmDialog, pageHead, loading, fmtAgo } from "../ui.js";

addRoute("/updates", {
  title: "Updates",
  icon: "download",
  group: "Server",
  order: 2,
  adminOnly: true,
  render: async (view) => {
    view.innerHTML = pageHead("Updates & packages", "Check for and apply OS package updates, install new packages, and see if a newer OS release is out — works across apt, dnf/yum, pacman, zypper and apk.", `
      <button class="btn btn-primary" id="btn-check">${icon("refresh")} Check now</button>`);
    view.insertAdjacentHTML("beforeend", `
      <div class="card" id="distro-card" style="margin-bottom:16px">
        <div class="card-head"><span class="card-title">Distro version</span></div>
        <div id="distro-body">${loading()}</div>
      </div>
      <div class="card" id="updates-card" style="margin-bottom:16px">
        <div class="card-head"><span class="card-title">System updates</span></div>
        <div id="updates-body">${loading()}</div>
      </div>
      <div class="card">
        <div class="card-head"><span class="card-title">Install a package</span></div>
        <div class="toolbar" style="margin-bottom:0">
          <input type="search" id="pkg-q" placeholder="Search packages…" style="flex:1">
          <button class="btn" id="btn-search">${icon("search")} Search</button>
        </div>
        <div id="search-results" style="margin-top:14px"></div>
      </div>`);

    const distroBody = document.getElementById("distro-body");
    async function loadDistro() {
      distroBody.innerHTML = loading();
      let d;
      try {
        d = await api.get("/system/distro");
      } catch (ex) {
        distroBody.innerHTML = `<p class="muted">${esc(ex.message)}</p>`;
        return;
      }
      if (!d.name) {
        distroBody.innerHTML = `<p class="small dim">Could not read /etc/os-release on this host.</p>`;
        return;
      }
      let statusHTML;
      if (d.upgrade_available) {
        statusHTML = `<div style="margin-top:10px;padding:12px 14px;border-radius:var(--radius-sm);background:var(--accent-dim);border:1px solid rgba(198,241,78,.35)">
          <p style="margin:0"><span class="tag tag-lime">upgrade available</span> ${esc(d.new_version)} is out.</p>
          <p class="small dim" style="margin:8px 0 0">Run <code class="mono">do-release-upgrade</code> over SSH when you're ready — this can take a while, may need a reboot, and isn't something to trigger from the panel.</p>
        </div>`;
      } else if (d.upgrade_supported) {
        statusHTML = `<p class="small dim" style="margin-top:8px">You're on the latest release for this OS.</p>`;
      } else {
        statusHTML = `<p class="small dim" style="margin-top:8px">Automatic release-upgrade checking isn't available for ${esc(d.id || "this distro")} — shown here is just the currently installed version.</p>`;
      }
      distroBody.innerHTML = `<p style="margin:0"><b>${esc(d.name)}</b> <span class="small dim mono">${esc(d.version || "")}</span></p>${statusHTML}`;
    }

    const updatesBody = document.getElementById("updates-body");

    async function loadUpdates() {
      updatesBody.innerHTML = loading();
      let data;
      try {
        data = await api.get("/system/updates");
      } catch (ex) {
        updatesBody.innerHTML = `<p class="muted">${esc(ex.message)}</p>`;
        return;
      }
      renderUpdates(data);
    }

    function renderUpdates(data) {
      const updates = data.updates || [];
      const mgr = data.pkg_manager;
      if (!mgr) {
        updatesBody.innerHTML = `<div class="empty-state"><span class="glyph">◈</span><p>No supported package manager was detected on this host (checked apt, dnf/yum, pacman, zypper, apk).</p></div>`;
        return;
      }
      const secCount = updates.filter((u) => u.security).length;
      const checkedAt = updates[0]?.checked_at;
      const head = `<div style="display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-bottom:14px">
        <span class="tag tag-teal">${esc(mgr)}</span>
        ${checkedAt ? `<span class="small dim">checked ${fmtAgo(checkedAt)}</span>` : `<span class="small dim">not checked yet</span>`}
        ${secCount ? `<span class="tag tag-red">${secCount} security</span>` : ""}
        <span class="spacer" style="flex:1"></span>
        ${updates.length ? `<button class="btn btn-primary btn-sm" id="btn-apply-all">${icon("download")} Apply all (${updates.length})</button>` : ""}
      </div>`;
      if (!updates.length) {
        updatesBody.innerHTML = head + `<div class="empty-state"><span class="glyph">✓</span><p>${checkedAt ? "Everything is up to date." : "Run a check to see if updates are available."}</p></div>`;
        return;
      }
      updatesBody.innerHTML = head + `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Package</th><th>Current</th><th>New</th><th></th><th></th></tr></thead>
        <tbody>${updates.map((u) => `
          <tr data-name="${esc(u.name)}">
            <td><b class="mono">${esc(u.name)}</b></td>
            <td class="mono small dim">${esc(u.current_version || "—")}</td>
            <td class="mono small">${esc(u.new_version)}</td>
            <td>${u.security ? '<span class="tag tag-red">security</span>' : ""}</td>
            <td><button class="btn btn-sm act-apply">${icon("download")} Apply</button></td>
          </tr>`).join("")}</tbody></table></div>`;

      document.getElementById("btn-apply-all").onclick = () => applyUpdates(null, `Apply all ${updates.length} updates? Upgrading a running service (e.g. nginx, mariadb) can restart it. This can take a few minutes.`);
      updatesBody.querySelectorAll(".act-apply").forEach((btn) => {
        const name = btn.closest("tr").dataset.name;
        btn.onclick = () => applyUpdates([name], `Apply the update for ${name}? Upgrading a running service can restart it.`);
      });
    }

    async function applyUpdates(names, message) {
      if (!await confirmDialog(message, { title: "Apply updates", okText: "Apply" })) return;
      try {
        toast(names ? `Applying update for ${names[0]}…` : "Applying all updates… this can take a few minutes");
        await api.post("/system/updates/apply", { names: names || [] });
        toast("Updates applied");
        loadUpdates();
      } catch (ex) { toast(ex.message, "err"); }
    }

    document.getElementById("btn-check").onclick = async (e) => {
      const btn = e.currentTarget;
      btn.classList.add("btn-busy");
      try {
        const data = await api.post("/system/updates/check");
        toast((data.updates || []).length ? `${data.updates.length} update(s) available` : "Everything is up to date");
        renderUpdates(data);
      } catch (ex) { toast(ex.message, "err"); }
      loadDistro();
      btn.classList.remove("btn-busy");
    };

    // Package search / install.
    const resultsBox = document.getElementById("search-results");
    async function runSearch() {
      const q = document.getElementById("pkg-q").value.trim();
      if (!q) return;
      resultsBox.innerHTML = loading();
      let results;
      try {
        results = await api.get("/system/packages/search?q=" + encodeURIComponent(q));
      } catch (ex) {
        resultsBox.innerHTML = `<p class="muted">${esc(ex.message)}</p>`;
        return;
      }
      if (!results.length) {
        resultsBox.innerHTML = `<p class="small dim">No packages matched "${esc(q)}".</p>`;
        return;
      }
      resultsBox.innerHTML = `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Package</th><th>Description</th><th></th></tr></thead>
        <tbody>${results.map((r) => `
          <tr data-name="${esc(r.name)}">
            <td><b class="mono">${esc(r.name)}</b>${r.version ? ` <span class="small dim">${esc(r.version)}</span>` : ""}</td>
            <td class="small dim">${esc(r.description || "")}</td>
            <td><button class="btn btn-sm act-install">${icon("plus")} Install</button></td>
          </tr>`).join("")}</tbody></table></div>`;
      resultsBox.querySelectorAll(".act-install").forEach((btn) => {
        const name = btn.closest("tr").dataset.name;
        btn.onclick = async () => {
          if (!await confirmDialog(`Install ${name}?`, { title: "Install package", okText: "Install" })) return;
          try {
            toast(`Installing ${name}…`);
            await api.post("/system/packages/install", { name });
            toast(`${name} installed`);
          } catch (ex) { toast(ex.message, "err"); }
        };
      });
    }
    document.getElementById("btn-search").onclick = runSearch;
    document.getElementById("pkg-q").addEventListener("keydown", (e) => { if (e.key === "Enter") runSearch(); });

    loadDistro();
    loadUpdates();
  },
});

import { addRoute } from "../app.js";
import { api } from "../api.js";
import { p } from "../base.js";
import { icon, esc, toast, confirmDialog, pageHead, loading, fmtAgo, modal } from "../ui.js";

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
        return null;
      }
      if (!d.name) {
        distroBody.innerHTML = `<p class="small dim">Could not read /etc/os-release on this host.</p>`;
        return null;
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
      return d;
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
      runApplyDialog(names);
    }

    // Streams the package manager's live output (apt-get/dnf/…) into a
    // dialog over the /system/updates/apply/stream WebSocket, instead of a
    // single toast that leaves the panel looking idle for however long the
    // apply takes (can be several minutes).
    function runApplyDialog(names) {
      const log = document.createElement("div");
      log.className = "mono small cd-log";
      log.style.cssText = "max-height:360px;overflow-y:auto;white-space:pre-wrap;line-height:1.6;background:var(--panel-2,rgba(0,0,0,.2));border-radius:var(--radius-sm);padding:10px 12px";
      const append = (text) => {
        log.insertAdjacentHTML("beforeend", `<div>${esc(text)}</div>`);
        log.scrollTop = log.scrollHeight;
      };

      const closeBtn = document.createElement("button");
      closeBtn.className = "btn";
      closeBtn.textContent = "Applying…";
      closeBtn.disabled = true;
      const dm = modal({
        title: names ? `Applying update: ${names[0]}` : "Applying all updates",
        wide: true,
        body: log,
        actions: [closeBtn],
      });
      let finished = false;
      closeBtn.onclick = () => { if (finished) dm.close(); };

      const finish = (ok, message) => {
        if (finished) return;
        finished = true;
        append(ok ? "✓ done" : `✗ ${message}`);
        toast(message || "Updates applied", ok ? undefined : "err");
        closeBtn.disabled = false;
        closeBtn.textContent = "Close";
        if (ok) closeBtn.classList.add("btn-primary");
        loadUpdates();
      };

      append(`→ applying ${names ? names.join(", ") : "all updates"}…`);
      const proto = location.protocol === "https:" ? "wss" : "ws";
      const q = new URLSearchParams({ token: api.token });
      if (names) q.set("names", names.join(","));
      const ws = new WebSocket(`${proto}://${location.host}${p("/api")}/system/updates/apply/stream?${q}`);
      ws.onmessage = (e) => {
        let msg;
        try { msg = JSON.parse(e.data); } catch { return; }
        if (msg.type === "log") append(msg.line);
        else if (msg.type === "done") finish(!msg.error, msg.error || "Updates applied");
      };
      ws.onclose = () => finish(false, "Connection to the panel was lost mid-apply — check the Updates list once it's back.");
    }

    document.getElementById("btn-check").onclick = async (e) => {
      const btn = e.currentTarget;
      btn.disabled = true;
      await runCheckDialog();
      btn.disabled = false;
    };

    async function runCheckDialog() {
      const steps = [
        { id: "pkg", label: "Package updates" },
        { id: "distro", label: "Distro release" },
      ];
      const body = document.createElement("div");
      body.innerHTML = steps.map((s) => `
        <div id="cd-${s.id}" style="padding:8px 0">
          <div style="display:flex;align-items:center;gap:10px">
            <span class="cd-icon" style="flex-shrink:0;width:15px">${loading()}</span>
            <span class="cd-text">${esc(s.label)}</span>
          </div>
          <div class="cd-log mono small dim" style="margin-left:25px;margin-top:4px;line-height:1.7"></div>
        </div>`).join("");
      const closeBtn = document.createElement("button");
      closeBtn.className = "btn";
      closeBtn.textContent = "Close";
      closeBtn.disabled = true;
      const dm = modal({ title: "Checking for updates", wide: true, body, actions: [closeBtn] });
      closeBtn.onclick = () => dm.close();

      const setStep = (id, state, headline) => {
        const row = document.getElementById(`cd-${id}`);
        if (!row) return;
        row.querySelector(".cd-icon").innerHTML = state === "done" ? icon("check")
          : state === "error" ? icon("x") : loading();
        if (headline) row.querySelector(".cd-text").textContent = headline;
      };
      const log = (id, text) => {
        document.querySelector(`#cd-${id} .cd-log`)?.insertAdjacentHTML("beforeend", `<div>${esc(text)}</div>`);
      };

      log("pkg", "→ POST /system/updates/check");
      let updateCount = null;
      try {
        const data = await api.post("/system/updates/check");
        const updates = data.updates || [];
        updateCount = updates.length;
        const secCount = updates.filter((u) => u.security).length;
        log("pkg", `← package manager: ${data.pkg_manager || "none detected"}`);
        if (updateCount) {
          log("pkg", `${updateCount} outdated package(s) found, ${secCount} flagged security`);
          const names = updates.slice(0, 5).map((u) => u.name).join(", ");
          log("pkg", `e.g. ${names}${updateCount > 5 ? `, +${updateCount - 5} more` : ""}`);
        } else {
          log("pkg", "no outdated packages — everything is up to date");
        }
        setStep("pkg", "done", updateCount ? `${updateCount} update(s) available` : "Everything is up to date");
        renderUpdates(data);
      } catch (ex) {
        log("pkg", `✗ ${ex.message}`);
        setStep("pkg", "error", "Check failed");
      }

      log("distro", "→ GET /system/distro");
      const d = await loadDistro();
      if (d) {
        log("distro", `← ${d.name} (id: ${d.id || "?"}, version: ${d.version || "?"})`);
        if (!d.upgrade_supported) {
          log("distro", `release-upgrade checking not available for "${d.id}"`);
        } else if (d.upgrade_available) {
          log("distro", `newer release available: ${d.new_version}`);
        } else {
          log("distro", "already on the latest release");
        }
        setStep("distro", "done", d.upgrade_available ? `${d.new_version} available` : "Distro release checked");
      } else {
        log("distro", "✗ could not read distro info");
        setStep("distro", "error", "Check failed");
      }

      closeBtn.disabled = false;
      closeBtn.classList.add("btn-primary");
      closeBtn.textContent = "Done";
      if (updateCount !== null) toast(updateCount ? `${updateCount} update(s) available` : "Everything is up to date");
    }

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

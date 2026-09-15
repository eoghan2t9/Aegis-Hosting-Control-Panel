import { addRoute, refresh, isAdmin } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, statusTag, pageHead, loading, modal, fmtAgo } from "../ui.js";

function fmtPorts(c) {
  if (!c.ports || !c.ports.length) return "—";
  return c.ports.map((p) => {
    const host = p.public ? `0.0.0.0:${p.host_port}` : `127.0.0.1:${p.host_port}`;
    return `${host}→${p.container_port}/${p.proto}${c.web_port === p.container_port ? " (web)" : ""}`;
  }).join(", ");
}

function parsePorts(text) {
  const out = [];
  for (const raw of (text || "").split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const tokens = line.split(/\s+/);
    let spec = tokens[0];
    const isPublic = tokens.slice(1).includes("public");
    let proto = "tcp";
    if (spec.includes("/")) { const [s, p] = spec.split("/"); spec = s; proto = p; }
    const [cport, hport] = spec.split(":");
    out.push({ container_port: parseInt(cport, 10) || 0, host_port: hport ? (parseInt(hport, 10) || 0) : 0, proto, public: isPublic });
  }
  return out;
}

function parseEnv(text) {
  const out = {};
  for (const raw of (text || "").split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const i = line.indexOf("=");
    if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1);
  }
  return out;
}

function parseVolumes(text) {
  const out = [];
  for (const raw of (text || "").split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const i = line.indexOf(":");
    if (i < 0) continue;
    out.push({ host_path: line.slice(0, i).trim(), container_path: line.slice(i + 1).trim() });
  }
  return out;
}

addRoute("/containers", {
  title: "Containers",
  icon: "box",
  group: "Websites",
  order: 5,
  feature: "docker",
  render: async (view) => {
    view.innerHTML = pageHead("Containers", "Run Docker containers under your account — attach one to a domain to reverse-proxy it, or publish a port directly for standalone services.", `
      <button class="btn btn-primary" id="btn-container">${icon("plus")} New container</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="containers-setup"></div><div id="containers-root">${loading()}</div>`);

    if (isAdmin()) {
      const dockerStatus = await api.get("/docker/status").catch(() => null);
      const setup = document.getElementById("containers-setup");
      if (dockerStatus && (!dockerStatus.installed || !dockerStatus.daemon_up)) {
        setup.innerHTML = `<div class="card" style="margin-bottom:16px;padding:14px 16px;display:flex;align-items:center;justify-content:space-between;gap:12px">
          <div><b>Docker is not installed on this host.</b><p class="small dim" style="margin:4px 0 0">Install Docker Engine to let accounts run containers.</p></div>
          <button class="btn btn-primary" id="btn-install-docker">Install Docker</button>
        </div>`;
        document.getElementById("btn-install-docker").onclick = async (e) => {
          e.target.disabled = true; e.target.textContent = "Installing…";
          try { await api.post("/docker/install", {}); toast("Docker installed"); refresh(); }
          catch (ex) { toast(ex.message, "err"); e.target.disabled = false; e.target.textContent = "Install Docker"; }
        };
      }
    }

    const [containers, domains] = await Promise.all([
      api.get("/containers").catch(() => []),
      api.get("/domains").catch(() => []),
    ]);
    const domainByID = Object.fromEntries(domains.map((d) => [d.id, d]));
    const root = document.getElementById("containers-root");

    if (!containers.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">${icon("box")}</span><p>No containers yet. Create one to run a Docker image under your account.</p></div>`;
    } else {
      root.innerHTML = `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Name</th><th>Image</th><th>State</th><th>Ports</th><th>Domain</th><th>Created</th><th></th></tr></thead>
        <tbody>${containers.map((c) => `
          <tr data-id="${c.id}">
            <td class="mono">${esc(c.name)}</td>
            <td class="mono small">${esc(c.image)}</td>
            <td>${statusTag(c.status || "unknown")}</td>
            <td class="small mono">${esc(fmtPorts(c))}</td>
            <td class="small">${c.domain_id && domainByID[c.domain_id] ? esc(domainByID[c.domain_id].domain) : "—"}</td>
            <td class="small dim">${fmtAgo(c.created_at)}</td>
            <td><div class="row-actions">
              <button class="btn btn-ghost act-start" title="Start">${icon("play")}</button>
              <button class="btn btn-ghost act-stop" title="Stop">${icon("toggle")}</button>
              <button class="btn btn-ghost act-restart" title="Restart">${icon("refresh")}</button>
              <button class="btn btn-ghost act-recreate" title="Pull latest image and recreate">${icon("download")}</button>
              <button class="btn btn-ghost act-logs" title="View logs">${icon("log")}</button>
              <button class="btn btn-ghost act-stats" title="Resource usage">${icon("cpu")}</button>
              <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
            </div></td>
          </tr>`).join("")}</tbody></table></div>`;
    }

    async function newContainer() {
      const vals = await promptDialog("New container", [
        { name: "name", label: "Name", required: true, placeholder: "my-app", help: "Lowercase letters, digits, hyphens." },
        { name: "image", label: "Image", required: true, mono: true, placeholder: "nginx:alpine" },
        { name: "ports", label: "Ports", type: "textarea", mono: true, required: true,
          placeholder: "80:0/tcp", help: "One per line: container_port[:host_port][/proto] [public]. host_port 0 or omitted auto-allocates. Add 'public' to bind 0.0.0.0 instead of localhost-only." },
        { name: "env", label: "Environment", type: "textarea", mono: true, placeholder: "KEY=value", help: "One per line: KEY=VALUE." },
        { name: "volumes", label: "Volumes", type: "textarea", mono: true, placeholder: "data:/var/lib/data", help: "One per line: path-under-your-home:/container/path." },
        { name: "restart_policy", label: "Restart policy", type: "select", value: "unless-stopped",
          options: [{ label: "Unless stopped", value: "unless-stopped" }, { label: "Always", value: "always" }, { label: "On failure", value: "on-failure" }, { label: "No", value: "no" }] },
        { name: "memory_limit_mb", label: "Memory limit (MB)", type: "number", placeholder: "0 = unlimited" },
        { name: "cpu_limit", label: "CPU limit (cores)", placeholder: "0 = unlimited" },
        { name: "domain_id", label: "Attach to domain", type: "select", value: "0",
          options: [{ label: "(none — standalone)", value: "0" }, ...domains.map((d) => ({ label: d.domain, value: String(d.id) }))] },
        { name: "web_port", label: "Web port", placeholder: "container port to proxy the domain to", help: "Required only when attaching to a domain — must match one of the container ports above." },
      ], { wide: true, okText: "Create" });
      if (!vals) return;
      try {
        await api.post("/containers", {
          name: vals.name,
          image: vals.image,
          ports: parsePorts(vals.ports),
          env: parseEnv(vals.env),
          volumes: parseVolumes(vals.volumes),
          restart_policy: vals.restart_policy,
          memory_limit_mb: parseInt(vals.memory_limit_mb || "0", 10) || 0,
          cpu_limit: vals.cpu_limit || "",
          domain_id: parseInt(vals.domain_id || "0", 10) || 0,
          web_port: parseInt(vals.web_port || "0", 10) || 0,
        });
        toast("Container created");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    }

    root.querySelectorAll("tbody tr").forEach((tr) => {
      const id = +tr.dataset.id;
      const c = containers.find((x) => x.id === id);
      if (!c) return;
      const act = (sel, fn) => tr.querySelector(sel)?.addEventListener("click", fn);
      act(".act-start", async () => {
        try { await api.post(`/containers/${id}/start`, {}); toast("Started"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
      act(".act-stop", async () => {
        try { await api.post(`/containers/${id}/stop`, {}); toast("Stopped"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
      act(".act-restart", async () => {
        try { await api.post(`/containers/${id}/restart`, {}); toast("Restarted"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
      act(".act-recreate", async () => {
        if (!await confirmDialog("Pull the latest image and recreate this container? Any state not in a mounted volume is lost.", { title: "Recreate container" })) return;
        try { await api.post(`/containers/${id}/recreate`, {}); toast("Recreated"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
      act(".act-logs", async () => {
        let res;
        try { res = await api.get(`/containers/${id}/logs`); }
        catch (ex) { toast(ex.message, "err"); return; }
        const close = document.createElement("button");
        close.className = "btn"; close.textContent = "Close";
        const lm = modal({
          title: `Logs — ${c.name}`,
          wide: true,
          body: `<pre class="mono small" style="max-height:60vh;overflow:auto;white-space:pre-wrap">${res.log ? esc(res.log) : "(no output yet)"}</pre>`,
          actions: [close],
        });
        close.onclick = () => lm.close();
      });
      act(".act-stats", async () => {
        let res;
        try { res = await api.get(`/containers/${id}/stats`); }
        catch (ex) { toast(ex.message, "err"); return; }
        const close = document.createElement("button");
        close.className = "btn"; close.textContent = "Close";
        const lm = modal({
          title: `Resource usage — ${c.name}`,
          body: `<div class="mono small">CPU: ${esc(res.cpu_percent)}<br>Memory: ${esc(res.mem_usage)} (${esc(res.mem_percent)})<br>Net I/O: ${esc(res.net_io)}</div>`,
          actions: [close],
        });
        close.onclick = () => lm.close();
      });
      act(".act-del", async () => {
        if (!await confirmDialog(`Delete container "${c.name}"? Its volumes are kept under your home directory.`, { danger: true, title: "Delete container" })) return;
        try { await api.del(`/containers/${id}`); toast("Container deleted"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
    });

    document.getElementById("btn-container").onclick = newContainer;
  },
});

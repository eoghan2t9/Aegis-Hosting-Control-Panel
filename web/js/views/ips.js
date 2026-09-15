import { addRoute, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, pageHead, loading } from "../ui.js";

function kindTag(kind) {
  return kind === "dedicated"
    ? `<span class="tag tag-teal">dedicated</span>`
    : `<span class="tag">shared</span>`;
}

addRoute("/ips", {
  title: "IP Management",
  icon: "globe",
  group: "Server",
  order: 2,
  adminOnly: true,
  render: async (view) => {
    view.innerHTML = pageHead("IP Management", "The pool of addresses this host can bind vhosts to. Shared IPs may serve many domains at once (standard name-based virtual hosting); a dedicated IP is enforced to serve at most one.", `
      <button class="btn btn-primary" id="btn-add-ip">${icon("plus")} Add IP</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="ips-root">${loading()}</div>`);

    const [ips, domains] = await Promise.all([
      api.get("/ips").catch(() => []),
      api.get("/domains").catch(() => []),
    ]);
    const root = document.getElementById("ips-root");
    const domainsByIP = {};
    for (const d of domains) {
      if (d.ip_id) (domainsByIP[d.ip_id] ||= []).push(d);
    }

    if (!ips.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">${icon("globe")}</span><p>No IPs in the pool yet. Add one already configured on this host's network interface to start assigning it to domains.</p></div>`;
    } else {
      root.innerHTML = `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Address</th><th>Label</th><th>Kind</th><th>Assigned domains</th><th></th></tr></thead>
        <tbody>${ips.map((ip) => {
          const assigned = domainsByIP[ip.id] || [];
          return `
          <tr data-id="${ip.id}">
            <td class="mono">${esc(ip.address)}</td>
            <td class="small">${esc(ip.label) || '<span class="dim">—</span>'}</td>
            <td>${kindTag(ip.kind)}</td>
            <td class="small">
              <div class="assigned-list" style="display:flex;flex-wrap:wrap;gap:6px">
                ${assigned.map((d) => `<span class="tag" data-domain-id="${d.id}">${esc(d.domain)} <button class="btn-ghost btn-xs act-unassign" title="Unassign" style="margin-left:4px">${icon("x")}</button></span>`).join("") || '<span class="dim">none</span>'}
              </div>
            </td>
            <td><div class="row-actions">
              <button class="btn btn-ghost act-assign" title="Assign to a domain">${icon("plus")}</button>
              <button class="btn btn-ghost act-edit" title="Edit">${icon("edit")}</button>
              <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
            </div></td>
          </tr>`;
        }).join("")}</tbody></table></div>`;
    }

    async function newIP() {
      const detected = await api.get("/ips/detect").catch(() => ({ addresses: [] }));
      const known = new Set(ips.map((i) => i.address));
      const options = (detected.addresses || []).filter((a) => !known.has(a));
      const vals = await promptDialog("Add IP", [
        options.length
          ? { name: "address", label: "Address", type: "select", required: true,
              options: options.map((a) => ({ label: a, value: a })),
              help: "Only addresses actually configured on this host's network interfaces are offered — a vhost bound to any other address would fail to start." }
          : { name: "address", label: "Address", required: true, mono: true, placeholder: "203.0.113.10",
              help: "Must already be configured on a local network interface (e.g. via `ip addr add`), or adding it will be refused." },
        { name: "label", label: "Label", placeholder: "e.g. \"pool A\" or a customer name" },
        { name: "kind", label: "Kind", type: "select", value: "shared",
          options: [{ label: "Shared (many domains)", value: "shared" }, { label: "Dedicated (one domain only)", value: "dedicated" }] },
      ], { okText: "Add" });
      if (!vals) return;
      try {
        await api.post("/ips", { address: vals.address, label: vals.label, kind: vals.kind });
        toast("IP added");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    }

    async function editIP(ip) {
      const vals = await promptDialog(`Edit ${ip.address}`, [
        { name: "label", label: "Label", value: ip.label, placeholder: "e.g. \"pool A\" or a customer name" },
        { name: "kind", label: "Kind", type: "select", value: ip.kind,
          options: [{ label: "Shared (many domains)", value: "shared" }, { label: "Dedicated (one domain only)", value: "dedicated" }] },
      ], { okText: "Save" });
      if (!vals) return;
      try {
        await api.patch(`/ips/${ip.id}`, { label: vals.label, kind: vals.kind });
        toast("IP updated");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    }

    async function assignIP(ip) {
      const assignedIDs = new Set((domainsByIP[ip.id] || []).map((d) => d.id));
      const candidates = domains.filter((d) => !assignedIDs.has(d.id));
      if (!candidates.length) { toast("Every domain is already assigned to this IP (or there are no domains yet)", "warn"); return; }
      const vals = await promptDialog(`Assign ${ip.address}`, [
        { name: "domain_id", label: "Domain", type: "select", required: true,
          options: candidates.map((d) => ({ label: d.domain + (d.ip_address ? ` (currently ${d.ip_address})` : ""), value: String(d.id) })) },
      ], { okText: "Assign" });
      if (!vals) return;
      try {
        await api.patch(`/domains/${vals.domain_id}/ip`, { ip_id: ip.id });
        toast("Domain assigned — vhost regenerated");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    }

    async function unassign(domainID, domainName) {
      if (!await confirmDialog(`Unassign ${domainName}? Its vhost goes back to listening on every interface.`, { title: "Unassign IP" })) return;
      try {
        await api.patch(`/domains/${domainID}/ip`, { ip_id: 0 });
        toast("Domain unassigned — vhost regenerated");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    }

    root.querySelectorAll("tbody tr[data-id]").forEach((tr) => {
      const id = +tr.dataset.id;
      const ip = ips.find((x) => x.id === id);
      if (!ip) return;
      tr.querySelector(".act-assign")?.addEventListener("click", () => assignIP(ip));
      tr.querySelector(".act-edit")?.addEventListener("click", () => editIP(ip));
      tr.querySelector(".act-del")?.addEventListener("click", async () => {
        if (!await confirmDialog(`Delete ${ip.address} from the pool?`, { danger: true, title: "Delete IP" })) return;
        try { await api.del(`/ips/${id}`); toast("IP deleted"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelectorAll(".act-unassign").forEach((btn) => {
        const chip = btn.closest("[data-domain-id]");
        const domainID = +chip.dataset.domainId;
        const d = domains.find((x) => x.id === domainID);
        btn.addEventListener("click", () => unassign(domainID, d ? d.domain : domainID));
      });
    });

    document.getElementById("btn-add-ip").onclick = newIP;
  },
});

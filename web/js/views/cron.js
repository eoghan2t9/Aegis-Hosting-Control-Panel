import { addRoute, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, promptDialog, confirmDialog, statusTag, pageHead, loading, modal, fmtAgo } from "../ui.js";

const PRESETS = [
  { label: "Every 5 minutes", value: "*/5 * * * *" },
  { label: "Every 15 minutes", value: "*/15 * * * *" },
  { label: "Hourly", value: "0 * * * *" },
  { label: "Daily at midnight", value: "0 0 * * *" },
  { label: "Weekly (Sunday midnight)", value: "0 0 * * 0" },
  { label: "Custom…", value: "" },
];

addRoute("/cron", {
  title: "Cron jobs",
  icon: "clock",
  group: "Websites",
  order: 4,
  render: async (view) => {
    view.innerHTML = pageHead("Cron jobs", "Scheduled commands, run under your own account via the system crontab. Output is captured to a log you can view here.", `
      <button class="btn btn-primary" id="btn-cron">${icon("plus")} New job</button>`);
    view.insertAdjacentHTML("beforeend", `<div id="cron-root">${loading()}</div>`);
    const jobs = await api.get("/cron/jobs").catch(() => []);
    const root = document.getElementById("cron-root");

    if (!jobs.length) {
      root.innerHTML = `<div class="card empty-state"><span class="glyph">⏱</span><p>No cron jobs yet. Create one to run a command on a schedule.</p></div>`;
    } else {
      root.innerHTML = `<div class="tbl-wrap"><table class="tbl">
        <thead><tr><th>Schedule</th><th>Command</th><th>State</th><th>Created</th><th></th></tr></thead>
        <tbody>${jobs.map((j) => `
          <tr data-id="${j.id}">
            <td class="mono">${esc(j.schedule)}</td>
            <td class="mono small">${esc(j.command)}</td>
            <td>${j.enabled ? statusTag("active") : statusTag("suspended")}</td>
            <td class="small dim">${fmtAgo(j.created_at)}</td>
            <td><div class="row-actions">
              <button class="btn btn-ghost act-log" title="View log">${icon("eye")}</button>
              <button class="btn btn-ghost act-edit" title="Edit">${icon("edit")}</button>
              <button class="btn btn-ghost act-tog" title="Enable/disable">${icon("toggle")}</button>
              <button class="btn btn-ghost act-del" title="Delete">${icon("trash")}</button>
            </div></td>
          </tr>`).join("")}</tbody></table></div>`;
    }

    async function editJob(job) {
      const vals = await promptDialog(job ? "Edit cron job" : "New cron job", [
        { name: "preset", label: "Schedule preset", type: "select", value: job?.schedule ?? "", options: PRESETS },
        { name: "schedule", label: "Cron expression", mono: true, required: true, value: job?.schedule ?? "*/5 * * * *", help: "Five fields: minute hour day month weekday. Use the preset above or edit directly." },
        { name: "command", label: "Command", mono: true, required: true, value: job?.command ?? "", help: "Runs under your own account, same PATH as SSH. Absolute paths must stay inside your home." },
      ]);
      if (!vals) return;
      const schedule = vals.preset || vals.schedule;
      try {
        if (job) {
          await api.patch(`/cron/jobs/${job.id}`, { schedule, command: vals.command });
          toast("Job updated");
        } else {
          await api.post("/cron/jobs", { schedule, command: vals.command });
          toast("Job created");
        }
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    }

    root.querySelectorAll("tbody tr").forEach((tr) => {
      const id = +tr.dataset.id;
      const job = jobs.find((j) => j.id === id);
      if (!job) return;
      tr.querySelector(".act-log")?.addEventListener("click", async () => {
        let log = "";
        try { ({ log } = await api.get(`/cron/jobs/${job.id}/log`)); }
        catch (ex) { toast(ex.message, "err"); return; }
        const close = btn("Close");
        const lm = modal({
          title: `Log — ${job.schedule}`,
          wide: true,
          body: `<pre class="mono small" style="max-height:60vh;overflow:auto;white-space:pre-wrap">${esc(log || "(no output yet)")}</pre>`,
          actions: [close],
        });
        close.onclick = () => lm.close();
      });
      tr.querySelector(".act-edit")?.addEventListener("click", () => editJob(job));
      tr.querySelector(".act-tog")?.addEventListener("click", async () => {
        try {
          await api.post(`/cron/jobs/${job.id}/toggle`, { enabled: !job.enabled });
          toast(job.enabled ? "Job disabled" : "Job enabled");
          refresh();
        } catch (ex) { toast(ex.message, "err"); }
      });
      tr.querySelector(".act-del")?.addEventListener("click", async () => {
        if (!await confirmDialog(`Delete this cron job?`, { danger: true, title: "Delete cron job" })) return;
        try { await api.del(`/cron/jobs/${job.id}`); toast("Job deleted"); refresh(); }
        catch (ex) { toast(ex.message, "err"); }
      });
    });

    document.getElementById("btn-cron").onclick = () => editJob(null);
  },
});

function btn(text, cls = "btn") {
  const b = document.createElement("button");
  b.className = cls;
  b.textContent = text;
  return b;
}

import { addRoute, refresh } from "../app.js";
import { api } from "../api.js";
import { icon, esc, toast, pageHead, loading, confirmDialog } from "../ui.js";

const MASK = "(unchanged)";

addRoute("/settings", {
  title: "Settings",
  icon: "sliders",
  group: "Server",
  order: 0,
  adminOnly: true,
  render: async (view) => {
    view.innerHTML = pageHead("Settings", "Server-wide panel configuration: paths, web server, PHP-FPM, DNS and database credentials.", loading());

    let s;
    try { s = await api.get("/settings"); }
    catch (ex) { view.innerHTML = pageHead("Settings", "", `<div class="card">Failed to load settings: ${esc(ex.message)}</div>`); return; }

    const inp = (name, label, value, opts = {}) => `
      <label class="field" style="${opts.wide ? "grid-column:1/-1" : ""}">
        <span class="field-label">${esc(label)}</span>
        <input name="${name}" value="${esc(value ?? "")}" ${opts.mono ? 'class="mono"' : ""} ${opts.type === "password" ? 'type="password"' : ""} spellcheck="false" placeholder="${esc(opts.ph || "")}">
        ${opts.help ? `<div class="small dim" style="margin-top:4px">${esc(opts.help)}</div>` : ""}
      </label>`;

    view.innerHTML = pageHead("Settings", "Server-wide panel configuration: paths, web server, PHP-FPM, DNS and database credentials.", `
      <button class="btn btn-primary" id="st-save">${icon("check")} Save settings</button>`)
      + `
      <div class="card" style="margin-bottom:16px">
        <div class="card-head"><span class="card-title">General</span></div>
        <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:12px">
          ${inp("public_host", "Public hostname", s.public_host, { ph: "panel.example.com" })}
          ${inp("session_ttl_hours", "Session lifetime (hours)", s.session_ttl_hours, { type: "number" })}
          ${inp("panel_base", "Panel URL path", s.panel_base, { ph: "/aegis  ('-' for root)", mono: true, help: "Vhosts proxy this prefix; changing it needs 'Apply config' on domains." })}
          <label class="field"><span class="field-label">Let's Encrypt staging</span>
            <select name="letsencrypt_staging">
              <option value="false" ${!s.letsencrypt_staging ? "selected" : ""}>No — production ACME</option>
              <option value="true" ${s.letsencrypt_staging ? "selected" : ""}>Yes — staging (rate-limit free)</option>
            </select></label>
        </div>
      </div>

      <div class="card" style="margin-bottom:16px">
        <div class="card-head"><span class="card-title">Web server &amp; PHP</span></div>
        <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:12px">
          <label class="field"><span class="field-label">Active web server (new domains)</span>
            <select name="web_server">
              ${["go", "nginx", "apache", "caddy"].map((x) => `<option value="${x}" ${x === s.web_server ? "selected" : ""}>${x}</option>`).join("")}
            </select></label>
          ${inp("php_fpm_socket_dir", "PHP-FPM socket dir", s.php_fpm_socket_dir, { mono: true, help: "Pools listen at <dir>/aegis-<domain>.sock (default /run/php)" })}
          ${inp("apache_listen_port", "Apache HTTP port", s.apache_listen_port, { type: "number", help: "Move Apache off 80 when nginx owns it" })}
          ${inp("nginx_dir", "Nginx config dir (blank = auto-detect)", s.nginx_dir, { mono: true })}
          ${inp("apache_dir", "Apache vhost dir (blank = auto-detect)", s.apache_dir, { mono: true })}
          ${inp("caddy_file", "Caddyfile path (blank = /etc/caddy/Caddyfile)", s.caddy_file, { mono: true })}
        </div>
      </div>

      <div class="card" style="margin-bottom:16px">
        <div class="card-head"><span class="card-title">DNS</span></div>
        <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:12px">
          ${inp("dns_listen_addr", "Built-in DNS listener (blank = off)", s.dns_listen_addr, { mono: true, ph: ":53" })}
          ${inp("bind_zone_dir", "Bind zone dir", s.bind_zone_dir, { mono: true })}
          ${inp("nameservers", "Nameservers (comma-separated)", (s.nameservers || []).join(", "), { mono: true, wide: true })}
          ${inp("dns_admin_email", "SOA admin email", s.dns_admin_email, { mono: true })}
        </div>
      </div>

      <div class="card">
        <div class="card-head"><span class="card-title">Database credentials</span>
          <span class="card-actions"><span class="tag tag-amber">passwords write-only</span></span></div>
        <div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:12px">
          <h4 class="small dim" style="grid-column:1/-1;margin:0">MariaDB</h4>
          ${inp("mariadb_host", "Host", s.mariadb_host, { mono: true })}
          ${inp("mariadb_port", "Port", s.mariadb_port, { type: "number" })}
          ${inp("mariadb_user", "User", s.mariadb_user, { mono: true })}
          ${inp("mariadb_password", "Password", s.mariadb_password, { type: "password", help: "Leave as “(unchanged)” to keep the stored password" })}
          <h4 class="small dim" style="grid-column:1/-1;margin:0">PostgreSQL</h4>
          ${inp("postgres_host", "Host", s.postgres_host, { mono: true })}
          ${inp("postgres_port", "Port", s.postgres_port, { type: "number" })}
          ${inp("postgres_user", "User", s.postgres_user, { mono: true })}
          ${inp("postgres_password", "Password", s.postgres_password, { type: "password", help: "Leave as “(unchanged)” to keep the stored password" })}
        </div>
      </div>`;

    document.getElementById("st-save").onclick = async () => {
      const val = (n) => view.querySelector(`[name="${n}"]`)?.value ?? "";
      const body = {
        public_host: val("public_host"),
        session_ttl_hours: parseInt(val("session_ttl_hours") || "0", 10),
        letsencrypt_staging: val("letsencrypt_staging") === "true",
        panel_base: val("panel_base"),
        web_server: val("web_server"),
        php_fpm_socket_dir: val("php_fpm_socket_dir"),
        apache_listen_port: parseInt(val("apache_listen_port") || "0", 10),
        nginx_dir: val("nginx_dir"),
        apache_dir: val("apache_dir"),
        caddy_file: val("caddy_file"),
        dns_listen_addr: val("dns_listen_addr"),
        bind_zone_dir: val("bind_zone_dir"),
        nameservers: val("nameservers").split(",").map((x) => x.trim()).filter(Boolean),
        dns_admin_email: val("dns_admin_email"),
        mariadb_host: val("mariadb_host"),
        mariadb_port: parseInt(val("mariadb_port") || "0", 10),
        mariadb_user: val("mariadb_user"),
        mariadb_password: val("mariadb_password"),
        postgres_host: val("postgres_host"),
        postgres_port: parseInt(val("postgres_port") || "0", 10),
        postgres_user: val("postgres_user"),
        postgres_password: val("postgres_password"),
      };
      if (!await confirmDialog("Save settings? Some changes apply to newly provisioned sites only.", { title: "Save settings", okText: "Save" })) return;
      try {
        await api.put("/settings", body);
        toast("Settings saved");
        refresh();
      } catch (ex) { toast(ex.message, "err"); }
    };
  },
});

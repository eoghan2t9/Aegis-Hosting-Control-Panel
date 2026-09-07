# Aegis — Roadmap & professional-level features

The user brief said *"also add features I might have forgotten"*. This file
collects what a professional hosting panel still needs on top of the current
foundation, in rough priority order.

## High priority

- **Email** (the biggest gap): SMTP (Postfix/Exim) + IMAP/POP (Dovecot),
  virtual mailboxes per domain, aliases, forwarders, quotas, SPF/DKIM/DMARC
  publishing straight into the DNS zone editor, webmail (Roundcube/SnappyMail)
  install and password sync.
- **Cron jobs**: per-user crontab management through the UI + API with a log
  viewer.
- **Web application manager**: one-click WordPress/Laravel installers,
  `composer create-project` templates, PHP-FPM per-site restart, opcache
  control, `php artisan`/`wp-cli` helpers in the file manager.
- **Per-account resource enforcement**: setquota/edquota for hard disk
  limits, cgroup-based CPU/memory caps per site, and bandwidth accounting
  (nfacct/iptables or vnstat per vhost log analysis).
- **Two-factor authentication** (TOTP) for panel logins and recovery codes;
  optional WebAuthn/passkeys.
- **Backup scheduler + retention**: cron-driven nightly backups, offsite
  target (S3/B2/SFTP), retention policies, and per-user self-service restores
  from the file manager.

## Medium priority

- **Reverse proxy / Node.js apps**: proxy domains to application ports,
  PM2-style process manager for Node/Python apps, WebSocket support in vhosts.
- **Full audit & security centre**: fail2ban integration, login throttling
  with lockout, panel security log, suspicious-login emails, malware scanner
  hooks (ClamAV), firewall presets per package.
- **PHP settings editor per site**: `php_admin_value` UI for
  upload_max_filesize, memory_limit, opcache, extensions — regenerating the
  pool config.
- **DNS editor hardening**: DNSSEC management where the provider supports it,
  zone import/export (AXFR/zone files), CAA presets.
- **White-label theming**: logo, accent color, custom login page, reseller
  branding.
- **API tokens for automation**: scoped, expiring tokens so users can script
  their own provisioning (PaaS-style).
- **Notifications**: email + webhooks on events (cert renewal failure, disk
  full, account suspended).

## Lower priority / nice-to-have

- **Multi-server mode**: one control node managing several worker hosts
  (like Plesk's extension model) — a large architectural step, deferred.
- **AppArmor/SELinux profiles** generated alongside web server configs.
- **Resource usage history**: time-series storage of per-user CPU/mem/disk
  (currently realtime only) with monthly billing reports.
- **Localisation** (i18n) framework in the frontend.
- **Marketplace**: installable plugins (new DNS providers, web server
  templates, app installers) shipped as signed zip bundles.
- **Mobile app / PWA** shell around the existing responsive UI.

## Notes for implementers

- Email and cron are deliberately absent from the first pass: they are large,
  self-contained domains. Add them as first-class sections (store tables +
  svc + view) following `docs/AI_INSTRUCTIONS.md`, not as ad-hoc scripts.
- Anything that enforces quotas must fail closed (refuse the operation) and
  log to the audit trail.
- Keep the dev container the source of truth for testing new integrations
  before they touch bare metal.

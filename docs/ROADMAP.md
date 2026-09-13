# Aegis — Roadmap & professional-level features

This file tracks what a professional hosting panel **still** needs on top of
the current foundation, in rough priority order.

> **Status note (2026-09):** an earlier version of this list included email,
> cron jobs, the web-application manager, fail2ban/security centre, backup
> scheduling with remote targets, per-account quotas and API tokens as
> missing. All of those are **implemented** now (see the feature map in
> `README.md`; the knowledge graph in `AGENTS.md` maps each one to its
> service, store and handler files). The list below reflects verified gaps —
> nothing here exists in `internal/` today.

## High priority

- **Two-factor authentication (TOTP)** for panel logins and recovery codes;
  optional WebAuthn/passkeys.
- **Per-site PHP settings editor**: `php_admin_value` UI for
  `upload_max_filesize`, `memory_limit`, `opcache`, extensions — regenerating
  the pool config. (Today only `open_basedir` is written per pool, in
  `internal/svc/php.go`.)
- **Kernel-level disk quotas**: enforcement is currently application-level —
  periodic `du` + access-log bandwidth accounting that **suspends** the
  account over its hard limit (`internal/svc/quota.go`, cPanel-style non-strict
  mode). `setquota`/XFS project quotas would make limits hard instead of
  eventual; cgroup-based CPU/memory caps fall in the same bucket.
- **Reverse proxy / Node.js apps**: proxy domains to application ports,
  PM2-style process manager for Node/Python apps, WebSocket support in vhosts.
- **Resource usage history**: time-series storage of per-user CPU/mem/disk/
  bandwidth (currently realtime only) with monthly billing reports.

## Medium priority

- **DNS editor hardening**: DNSSEC management where the provider supports it,
  zone import (AXFR/zone-file import; RFC1035 *export* already exists via
  `DNS.WriteZoneFile`), CAA presets.
- **White-label theming**: logo, accent color, custom login page, reseller
  branding.
- **Notifications**: email + webhooks on events (cert renewal failure, disk
  full, account suspended).

## Lower priority / nice-to-have

- **Multi-server mode**: one control node managing several worker hosts
  (like Plesk's extension model) — a large architectural step, deferred.
- **AppArmor/SELinux profiles** generated alongside web server configs.
- **Localisation** (i18n) framework in the frontend.
- **Marketplace**: installable plugins (new DNS providers, web server
  templates, app installers) shipped as signed zip bundles.
- **Mobile app / PWA** shell around the existing responsive UI.

## Notes for implementers

- New large domains (e.g. multi-server mode) must be added as first-class
  sections (store tables + svc + view) following `docs/AI_INSTRUCTIONS.md`,
  not as ad-hoc scripts.
- Anything that enforces quotas must fail closed (refuse the operation) and
  log to the audit trail.
- Keep the dev container the source of truth for testing new integrations
  before they touch bare metal.

# Aegis — Architecture

## Design goals

- **One language, one binary.** The whole panel — API, frontend, and an
  optional *customer web server* — is a single Go binary. The frontend is
  embedded with `go:embed`.
- **Real system integration, cleanly separated.** Everything that touches the
  host (users, configs, services, databases, certificates) lives in
  `internal/svc`. The HTTP layer only validates, authorises, audits and calls
  services.
- **Provider/plugin model where the world varies** (DNS backends, web server
  templates) and **detection instead of configuration** where possible (PHP
  versions, installed services).
- **Runs as root on Linux** (bare metal or the dev container). Non-root
  operation is unsupported — provisioning users and services requires it.

## Request lifecycle

```
Browser SPA ──fetch──▶ api.Server (http.ServeMux, method routes)
   │                        │  withAuth: JWT → store session + user
   │                        │  withRole / ownership checks
   │                        ▼
   │                 handler (validates JSON, calls service, s.audit)
   │                        │
   ▼                        ▼
internal/svc ──exec/syscall/files──▶ host (useradd, nginx -t, mysqldump, …)
                │
                ▼
internal/store ──SQL──▶ SQLite (WAL)
```

## Data model (SQLite, single file)

| Table | Purpose |
|---|---|
| `users` | panel accounts; `role` admin/reseller/user; `owner_id` scopes resellers |
| `packages` | hosting plans: quotas + `allow_*` feature flags |
| `domains` | website per user; php version, web server, ssl paths |
| `domain_aliases` | extra hostnames served by a domain |
| `dns_zones` | one per domain, bound to a provider (`local`/`cloudflare`) |
| `dns_records` | A/AAAA/CNAME/MX/TXT/NS/SRV/CAA |
| `ssl_orders` | certificate lifecycle (pending/issued/renewing/failed) |
| `ftp_accounts` | FTP logins (system users + store row) |
| `databases` | provisioned DBs incl. credentials (needed to dump/restore) |
| `dns_providers` | plugin credentials (API token **encrypted** with AES-GCM) |
| `sessions` | revocable JWT sessions |
| `audit_log` | immutable record of every admin/security action |
| `settings` | key/value |

Migrations are additive (`CREATE TABLE IF NOT EXISTS`); schema lives in
`store.migrate`.

## Authentication & authorization

- JWT (HS256) with `sub` (user id), `role`, `sid` (session) and `imp`
  (impersonator). Every request verifies the session row still exists — so
  logout, password reset and suspension revoke immediately.
- Impersonation ("login as user") mints a normal user token carrying the
  admin's id in `imp`; the UI shows a persistent banner and the audit log
  records the pairing.
- Object access is checked per resource (users may touch only their own
  domains/databases/zones/files; resellers additionally manage accounts they
  created).

## Services (internal/svc)

| Service | Highlights |
|---|---|
| `Domains` | validation, quotas, docroot creation, wires PHP pool + vhost |
| `PHP` | version detection, per-domain pools (`/etc/php/<v>/fpm/pool.d/aegis-<domain>.conf`), reloads fpm |
| `WebServer` | nginx/Apache/Caddy generators + native **Go** routes; config test + reload; `GoRoutes` for the built-in server |
| `fastcgi.go` | minimal FastCGI client (unix/tcp) so the Go server can execute PHP |
| `DNS` | provider plugins, zone sync, RFC1035 zone files |
| `dnsserver.go` | built-in authoritative server (miekg/dns) answering from the store |
| `cloudflare.go` | v4 REST plugin + DNS-01 ACME helper |
| `SSL` | lego (Let's Encrypt), webroot/DNS challenges, self-signed, auto-renew |
| `FTP` | system users via useradd/chpasswd + vsftpd (chroot, writeable) |
| `Databases` | MariaDB/PostgreSQL provisioning via drivers + dumps via CLI |
| `Files` | chrooted file manager (list/edit/chmod/chown/search/zip/upload) |
| `System` | gopsutil overview + `/proc` per-user CPU/RSS accounting + processes |
| `Tuner` | host inspection → sized nginx/php-fpm/sysctl, persisted report |
| `Backup` | full/per-user tar.gz (homes + DB dumps + manifest), restore |
| `Terminal` | PTY shell over WebSocket |
| `Cipher` | AES-256-GCM for secrets at rest |

## The native Go web server

Chosen as the default in `go run`-style demos and small boxes. `WebServer`
holds an in-memory route table; `cmd/aegis` starts:
- an HTTP listener (`AEGIS_GO_HTTP`, default `:80`) serving static files,
- an HTTPS listener (`AEGIS_GO_HTTPS`, default `:443`) with a
  `GetCertificate` hook resolving certs from the route table (SNI),
- PHP requests proxied through the in-tree FastCGI client to the domain's
  php-fpm socket.

## Frontend

- Zero build step: ES modules under `web/js/`, design system in
  `web/css/aegis.css`, hash-based router in `app.js`.
- Views register with `addRoute`; shared UI (toasts, modals, prompts,
  charts, formatting) is in `ui.js`.
- Realtime: dashboard metrics stream over a WebSocket (`/api/system/metrics`);
  terminal over `/api/terminal`.
- In dev the assets are served from the bind-mounted directory
  (`AEGIS_DEV_WEB=/app/web`); in production they come from `go:embed`.

## Dev container

`docker/Dockerfile.dev` (Ubuntu 22.04 + ondrej/php for PHP 7.4→8.4) starts
nginx, Apache (`:8081`), MariaDB, PostgreSQL, vsftpd and every php-fpm under
supervisor. The entrypoint seeds DB credentials from env, writes vsftpd/fpm
configs, then runs `go run ./cmd/aegis` against the bind-mounted repo. Named
volumes persist DB data and `/etc/aegis`.

## Configuration & secrets

- JSON at `/etc/aegis/config.json` (or `AEGIS_CONFIG`); every path/credential
  is overridable with `AEGIS_*` env vars (see `config.Default()`).
- A 32-byte random secret at `/etc/aegis/secret.key` (0600) derives the JWT
  key and the AES key for provider tokens.
- DB admin credentials for MariaDB/PostgreSQL live in the config so the panel
  can provision user databases.

## Extension points (summary)

- New DNS backend → `svc.Provider` + registration in `DNS.PluginNames`.
- New web server → generator trio in `svc.WebServer` + validators.
- New PHP versions → nothing (auto-detected).
- New CLI ops → subcommand in `cmd/aegisctl`.
- New API routes → `api.Server.Handler()` + handler file.

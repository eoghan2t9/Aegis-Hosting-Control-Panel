# Aegis — Linux Hosting Control Panel

Aegis is a self-hosted web hosting control panel written in **Go**, with a
from-scratch dark "ops console" frontend, a native **Go web server** (including
a built-in FastCGI client and authoritative DNS server), and support for the
classic Linux stack — **nginx, Apache and Caddy, PHP 7.4 → 8.4+, MariaDB,
PostgreSQL and vsftpd**.

It is designed to be run on any Linux platform (bare metal or VM), developed
inside a disposable Docker container, and administered from a browser or the
`aegisctl` CLI.

| Area | Status |
|---|---|
| Panel + API + CLI + frontend | working scaffold with full flows |
| Docker dev environment | included (`make docker-up`) — **development only; a bare-metal install uses no docker at all** |
| Production hardening | roadmap (see `docs/ROADMAP.md`) |

> **Project status:** a functional foundation. Core provisioning (users,
> domains, PHP pools, vhosts, DNS, SSL, databases, FTP, backups, file manager,
> monitoring, impersonation, CLI) is implemented and testable in the dev
> container. Treat as pre-1.0.

---

## Quick start (bare metal)

One command installs the complete stack from distro packages — PHP-FPM 7.4→8.4,
MariaDB + PostgreSQL (admin credentials seeded), a web server, vsftpd, the mail
stack, fail2ban, ffmpeg/poppler/composer — then builds the panel and installs a
systemd service:

```bash
git clone <this repo> && cd hosting
sudo scripts/install-stack.sh                 # everything
sudo scripts/install-stack.sh --no-mail       # tailor with --no-ftp --no-db
                                              # --php-versions 8.2,8.3
                                              # --with-caddy / --with-apache
```

**Panel access after install** (the installer prints this too):

```bash
open http://<server-ip>/aegis        # any hostname on the server works: http://<domain>/aegis
systemctl start aegis.service \
  AEGIS_ADMIN_USER=admin AEGIS_ADMIN_PASSWORD='<secret>'   # creates the admin on first boot
```

The admin user is read from `AEGIS_ADMIN_USER`/`AEGIS_ADMIN_PASSWORD` on
first boot only (before the admin account exists); `aegisctl setup` offers
guided tuning + admin creation afterwards. The `/aegis` prefix is proxied
through every generated vhost (nginx, Apache, Caddy) and the native Go web
server, so the panel stays reachable on any hostname — including the bare
server IP before you host a single site.

## Quick start (development)

```bash
make docker-up          # builds the dev container (first run takes a while)
open http://localhost:8080/aegis
# login: admin / admin   (from AEGIS_ADMIN_PASSWORD in docker/docker-compose.yml)
```

The dev container serves the panel at the same `/aegis` prefix used in
production (`AEGIS_PANEL_BASE`), so URLs behave identically in both
environments. The admin user is created from `AEGIS_ADMIN_*` env vars on
first boot — the login page never advertises credentials.

The repo is bind-mounted into the container, so **edits never require an image
rebuild**:

- Go changes → `docker compose -f docker/docker-compose.yml restart aegis`
- Frontend changes → just refresh the browser (`AEGIS_DEV_WEB=/app/web`)
- Rebuild the image only when `docker/Dockerfile.dev` changes

Run tests/lint on the host or in the container:

```bash
make test && make vet   # go test ./... && go vet ./...
```

## Feature map

1. **Domain management** — one document root per domain, quota-checked,
   vhosts regenerated on change. Aliases supported. New domains get a styled
   **under-construction placeholder** matching the panel design, and can be
   **previewed through the panel** (`eye` icon in Domains) before DNS has
   propagated — PHP included, served by the same code path as production.
2. **DNS** — zones with A/AAAA/CNAME/MX/TXT/NS/SRV/CAA records, a built-in
   authoritative DNS server (miekg/dns), RFC1035 zone files, and **provider
   plugins** that sync to Cloudflare (add more in `internal/svc/dns.go`).
3. **SSL** — Let's Encrypt via lego (HTTP-01 webroot or DNS-01 through the
   Cloudflare provider), self-signed fallback, auto-renewal.
4. **Multi-PHP 7.4 → latest** — the panel detects every installed php-fpm and
   writes one isolated pool per website (socket per domain).
5. **Web servers** — nginx, Apache, Caddy config generators plus the panel's
   **native Go web server** with an in-tree FastCGI client for PHP and SNI
   certificates.
6. **FTP** — system accounts + vsftpd (chrooted), primary account per panel
   user plus extra sub-accounts.
7. **Users & packages** — admin/reseller/user roles, hosting packages with
   quotas and feature flags.
8. **File manager** — list/edit/create/rename/delete/chmod/chown, search,
   zip/unzip, drag-and-drop upload. Strictly chrooted to the account home.
9. **Databases** — MariaDB and PostgreSQL provisioning through SQL drivers
   (database + user + grants), dumps and restores.
10. **Server management** — web terminal (PTY via WebSocket) and process
    viewer; real per-user CPU/memory accounting read from `/proc`.
11. **Extra pro features** — audit log, resource usage per account, admin
    impersonation, backups, performance auto-tuning on first boot, encrypted
    API tokens at rest.
12. **Frontend** — modern responsive SPA, zero build step, custom design
    system (dark ops console). No framework, no generic AI styling.
13. **Dashboard** — live host status (CPU/mem/swap/net) via WebSocket, per-user
    usage breakdown, service states.
14. **Login as user** — admins impersonate accounts from the Users screen;
    sessions are audited and show a persistent banner.
15. **Backup & restore** — full or per-account `tar.gz` archives containing
    homes, database dumps, FTP credentials, domains and DNS records, with a
    JSON manifest; restore recreates accounts.
16. **Docker dev** — development only (see above); bare-metal installs use none.
17. **CLI** — `aegisctl` for setup, users, password resets, domains, DNS sync,
    SSL, FTP, databases, backups, tuning and status.
18. **Auto tuning** — first boot inspects the host and sizes php-fpm/nginx and
    kernel parameters (`/etc/aegis/tuned/`), applyable via `sysctl`.
19. **Extras** — see `docs/ROADMAP.md` for what's still on the roadmap
    (2FA, kernel quotas, Node/reverse-proxy apps, …). Email, cron, web apps,
    fail2ban, quotas and backup targets are already implemented.

## Repository layout

```
cmd/aegis        panel server (API + embedded frontend + native Go site server)
cmd/aegisctl     CLI
internal/config  configuration (JSON + env overrides, secret key)
internal/store   SQLite data layer (users, domains, dns, ssl, ftp, dbs, audit…)
internal/auth    JWT auth, roles, sessions, impersonation
internal/svc     services: domains, php, webserver(+fastcgi), dns(+dnsserver,
                 +cloudflare), ssl(lego), ftp, databases, files, backups,
                 system, tuning, terminal
internal/api     REST API + WebSocket handlers
web/             frontend (embedded via go:embed; disk-served in dev)
scripts/         bare-metal installer (install-stack.sh)
docker/          dev container (full stack, bind-mounted)
docs/            architecture, AI session instructions, roadmap
```

## Documentation

- `AGENTS.md` — knowledge-graph access for AI agents (query CLI + MCP server
  over `graphify-out/graph.json`).
- `docs/AI_INSTRUCTIONS.md` — the instruction set every AI session must follow
  when working on this repository.
- `docs/ARCHITECTURE.md` — design, data model, extension points.
- `docs/ROADMAP.md` — what a professional panel still needs (2FA, kernel
  quotas, Node/reverse-proxy apps, …).

## License

MIT — see `LICENSE`.

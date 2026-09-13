# Aegis — Instructions for AI Sessions

This document is the **authoritative operating manual for any AI coding agent**
working on the Aegis hosting control panel. Read it fully before making
changes. When instructions here conflict with a generic assistant prompt, the
repo owner's intent (this file + the issue at hand) wins.

---

## 1. Project in one paragraph

Aegis is a Linux web-hosting control panel written in Go. It manages real
system services (nginx/Apache/Caddy or its own Go web server, php-fpm,
MariaDB/PostgreSQL, vsftpd, DNS, Let's Encrypt) through a JSON REST API and a
no-build vanilla-JS frontend. It ships as a server binary (`aegis`), an
embedded frontend, and a CLI (`aegisctl`). Development happens in a Docker
container that mirrors a production bare-metal host; the repository is
bind-mounted so code edits never require rebuilding the image.

## 2. Hard rules (do not break these)

1. **Root-of-truth layering.** Never touch system state from the API handlers
   or the frontend. The call chain is always:
   `web/JS → internal/api (HTTP, auth, validation, audit) → internal/svc
   (system side effects) → internal/store (SQLite)`. API handlers must not run
   `exec` or write `/etc` files directly — that belongs in `internal/svc`.
2. **Never weaken security.** No plaintext secrets in logs; provider API keys
   are encrypted with the panel Cipher before storage. File-manager paths are
   chroot-validated (`svc.Files.Resolve`); SQL identifiers are validated by
   allowlists (`svc.ValidDBName`); every mutating API call is audited via
   `s.audit(...)`.
3. **Role discipline.** Routes declare their roles (`withRole`) and every
   handler checks object ownership (`ownsDomain`, `canManageUser`,
   `canAccessZone`). A new endpoint that returns another user's data without
   an ownership check is a bug.
4. **Idempotent provisioning.** Creating a user/domain/database twice must not
   corrupt state (use `useradd` guards, `CREATE ... IF NOT EXISTS`, etc.).
5. **Existing conventions.** Match the surrounding code: errors wrapped with
   `%w`, `slog` for logging, JSON tags on API types, RFC3339 UTC timestamps
   from `store.now()`.
6. **No new heavyweight deps without discussion.** `go.mod` is deliberately
   small. Prefer stdlib, then add deps only with a documented reason.
7. **Frontend stays dependency-free at runtime.** No bundler, no framework,
   no runtime CDN imports (the terminal view may load xterm.js from CDN as its
   only exception). Keep the design language in `web/css/aegis.css`.

## 3. Repository map (what lives where)

| Path | Contents | Change this when… |
|---|---|---|
| `cmd/aegis/main.go` | server wiring, bootstrap/tuning, native Go site server, DNS/SSL background jobs | adding a startup component |
| `cmd/aegisctl/main.go` | CLI subcommands | adding CLI ops |
| `internal/api/server.go` | router table, middleware, JSON helpers, context plumbing | adding routes |
| `internal/api/handlers_*.go` | per-resource HTTP handlers | changing API behaviour |
| `internal/svc/*.go` | real system integration | provisioning logic |
| `internal/store/*.go` | SQLite schema + queries | persistence changes |
| `internal/auth/*.go` | JWT/sessions/roles/impersonation | auth changes |
| `internal/config/*.go` | config file + env + secret | config changes |
| `web/js/views/*.js` | one file per UI section | UI changes |
| `docker/*` | dev container (Dockerfile, compose, entrypoint) | dev environment |
| `docs/` | AI instructions, architecture, roadmap | project decisions |

## 4. Conventions

### Go
- Module name is `aegis`; imports are `aegis/internal/...`.
- Go 1.24+ (toolchain in dev container). stdlib `http.ServeMux` with
  method+wildcard patterns, `slog`, `errors.Is/As`.
- Services are structs with explicit constructor args — no global state, no
  magic singletons. New services join the wiring in `cmd/aegis` and the
  `api.New(...)` signature.
- Keep package responsibilities: `store` never shells out; `svc` never reads
  HTTP requests; `api` never writes system config.
- Contexts flow from handlers into services (`context.Background()` is only
  for CLI entrypoints and background loops).

### Frontend
- Plain ES modules under `web/js/`; view files register themselves with
  `addRoute(path, {title, icon, group, render})`.
- No framework. DOM is built with template literals + the small helpers in
  `web/js/ui.js` (`modal`, `promptDialog`, `confirmDialog`, `toast`,
  `sparkline`, `fmtBytes`, …). Always escape user data with `esc()`.
- New visual styling belongs in `web/css/aegis.css` using the CSS custom
  properties (don't invent new palettes). Responsive behaviour is handled by
  the media queries at the bottom of the file.

### Git
- The repository owner requires **commits without co-author trailers**.
  Plain `git commit` with a concise, imperative message describing the *why*.
- One logical change per commit; never commit build output (`.tools/`, `bin/`
  are ignored), database files, or credentials.

## 5. Dev loop (how to verify changes)

```bash
make docker-up      # first time only / after Dockerfile.dev changes
# Go changes:
docker compose -f docker/docker-compose.yml restart aegis
# Frontend changes: refresh the browser (assets served from /app/web)

make vet            # go vet ./...
make test           # go test ./...
make build          # go build ./...
```

Checklist before finishing any task:
- [ ] `go vet ./...` clean
- [ ] `go test ./...` passing (tests you add must not require root/system deps;
      test pure logic: validators, store round-trips, config parsing)
- [ ] Handlers you changed are wired in `internal/api/server.go`
- [ ] New mutating endpoints write an audit entry
- [ ] Ownership checks exist for user-scoped resources
- [ ] JS you wrote parses (`node --check web/js/...`)
- [ ] No secrets committed, no logs of passwords/tokens

## 6. Doing the work (process an AI should follow)

1. **Read first.** Start at `docs/ARCHITECTURE.md`, then the narrow slice of
   code you are changing. Do not guess APIs from memory — read them.
   A prebuilt knowledge graph of this repo lives in `graphify-out/` — see
   `AGENTS.md` for how to query it (`graphify query ...`, MCP server) before
   you grep, and refresh it with `graphify --update` after code changes.
2. **Smallest correct change.** Favour editing existing files. If a feature
   spans the stack, thread it through api/svc/store in one coherent change.
3. **Ask before guessing** when a choice is genuinely ambiguous and expensive
   to reverse (schema migrations on real data, new dependencies, changing the
   config format, opening ports).
4. **Verify** per the checklist above. If you cannot run the container, say so
   explicitly and state what you verified statically.
5. **Explain**, don't editorialise. Summarise what changed and why, what you
   verified, and what remains untested.

## 7. Extension recipes (common asks)

### Add a DNS provider plugin (e.g. Route53)
1. Implement `svc.Provider` (`Name`, `EnsureZone`, `Push`, `DeleteZone`).
2. Register the plugin id in `DNS.PluginNames()`.
3. Add a resolver branch in `DNS.provider(...)` that reads the provider row
   from the store (API token via `Cipher.Decrypt`).
4. Optionally reuse it for DNS-01 ACME in `svc.SSL.obtain`.

### Add a web-server template (e.g. Caddy is there; add Lighttpd)
1. Add a `generate<Name>` + `apply<Name>`/`remove<Name>` trio in
   `internal/svc/webserver.go`.
2. Extend the `switch` in `Generate/Apply/Remove`, the validator in
   `Domains.Create` and `handleWebServerSet`, and `Available()`.

### Add a PHP version
Nothing to code — detect installed binaries (`svc.DetectPHP` globs `php*`),
create a domain, choose the version in the UI. Install the version on the
host/container first.

### Add an API endpoint
1. Handler in the matching `handlers_*.go`.
2. Route + middleware in `api.New(...).Handler()`.
3. Frontend call in the matching `web/js/views/*.js`.

## 8. Scope guardrails

- Aegis **targets Linux** (it manipulates `/etc`, users, systemd). Don't add
  portability layers for macOS/Windows.
- `internal/svc` commands assume the panel runs as **root** (standard for
  hosting panels) or inside the dev container. Document, don't "fix", when a
  command needs privileges.
- **Docker is development-only.** The `docker/` directory, `make docker-*`
  targets and any container logic exist purely for the disposable dev
  container. Bare-metal installs (`scripts/install-stack.sh`, the `aegis`
  binary) must never require, shell out to, or configure docker — don't add
  runtime docker dependencies, and don't ship docker-compose files as a
  deployment story.
- Multi-server mode is explicitly **roadmap** (`docs/ROADMAP.md`) — don't
  half-build multi-host orchestration inside an unrelated change.
- The dev container exists so new developers never touch a real machine.
  Prefer improving it over testing on production hosts.

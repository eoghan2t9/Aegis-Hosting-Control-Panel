# Graph Report - hosting  (2026-09-25)

## Corpus Check
- 126 files · ~134,052 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 9 file(s) not represented in the graph (top: (none) 5, .dev 2, .conf 1)

## Summary
- 1514 nodes · 4848 edges · 81 communities (51 shown, 24 thin omitted)
- Extraction: 84% EXTRACTED · 16% INFERRED · 0% AMBIGUOUS · INFERRED: 794 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `32090fc3`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- esc
- tuning.go
- system.go
- Exec
- writeJSON
- now
- PHP
- pathID
- writeErr
- SSL
- Server
- context.Context
- parseTime
- WebFTP
- time.Time
- RunTimeout
- .obtain
- net/http.ResponseWriter
- FTP
- Store
- Config
- Manager
- wire
- svc/packages.go
- net/http.Request
- scanAPIToken
- .serveGoRoute
- WebServer
- DNSServer
- Databases
- IPs
- fastcgi.go
- Mail
- serviceSet
- testing.T
- LookPath
- svc.Docker
- Server
- Quota
- AGENTS.md — AI agent entry point for Aegis
- wrapErr
- scanBackupTarget
- acmeUser
- install-stack.sh
- docs/ARCHITECTURE.md
- handlers_dns.go
- svc.PHP
- handlers_backup.go
- handlers_auth.go
- handlers_webapps.go
- handlers_packages.go
- backupTargetReq
- cronCreateReq
- fileOpReq
- tokenCreateReq
- aegis
- Store
- Root-of-Truth Layering (JS→api→svc→store)
- Store
- composeImportReq
- Backup
- store.go
- entrypoint.sh
- Provider
- handlers_ips.go
- DNS
- AuditEntry
- aegis/internal/store.DNSRecord
- aegis/internal/store.FTPAccount
- User
- Backup
- server.go
- store.DNSRecord
- PHP
- WebServer

## God Nodes (most connected - your core abstractions)
1. `writeJSON()` - 145 edges
2. `writeErr()` - 143 edges
3. `esc()` - 68 edges
4. `User` - 67 edges
5. `toast()` - 67 edges
6. `readJSON()` - 62 edges
7. `Store` - 60 edges
8. `wrapErr()` - 60 edges
9. `Config` - 57 edges
10. `RunTimeout()` - 57 edges

## Surprising Connections (you probably didn't know these)
- `API Layer (internal/api)` --references--> `New()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/api/server.go
- `Hosting Packages & Quotas (feature flags)` --references--> `pkgModal()`  [INFERRED]
  docs/ARCHITECTURE.md → web/js/views/accounts.js
- `docker/docker-compose.yml — Dev Stack` --shares_data_with--> `main()`  [INFERRED]
  docker/docker-compose.yml → cmd/aegis/main.go
- `Multi-PHP 7.4→8.4 per-domain pools` --references--> `DetectPHP()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/svc/tuning.go
- `SSL via lego (HTTP-01/DNS-01, auto-renew)` --references--> `DNS`  [INFERRED]
  docs/ARCHITECTURE.md → internal/svc/cloudflare.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Panel Request Lifecycle** — concept_api_layer, concept_svc_layer, concept_store_layer, concept_layered_architecture [EXTRACTED 1.00]
- **Extension Points** — concept_extension_recipes, concept_provider_plugin_model, svc_dns_pluginnames, svc_webserver_goroutes, internal_svc_tuning_detectphp [INFERRED 0.85]
- **Defense-in-Depth Security Model** — concept_ownership_checks, concept_audit_trail, concept_encrypted_secrets, concept_chroot_file_manager, concept_role_model [INFERRED 0.85]

## Communities (81 total, 24 thin omitted)

### Community 0 - "esc"
Cohesion: 0.05
Nodes (139): Framework-Free SPA (ES modules, addRoute, ui.js), api, qs(), addRoute(), boot(), buildShell(), can(), enterApp() (+131 more)

### Community 1 - "tuning.go"
Cohesion: 0.13
Nodes (19): Multi-PHP 7.4→8.4 per-domain pools, First-Boot Auto Tuning (sysctl/php-fpm sizing), DiskInfo, clamp(), containsString(), DetectDatabases(), DetectPHP(), DetectWebServers() (+11 more)

### Community 2 - "system.go"
Cohesion: 0.17
Nodes (12): DetectPublicIP(), diskUsage(), System, NewSystem(), statusOf(), UIDFor(), DiskInfo, Metrics (+4 more)

### Community 3 - "Exec"
Cohesion: 0.15
Nodes (25): Exec(), RandomPassword(), RandomString(), installCraft(), installDrupal(), installFlarum(), installMediaWiki(), installNextcloud() (+17 more)

### Community 4 - "writeJSON"
Cohesion: 0.16
Nodes (4): Server, Server, Server, writeJSON()

### Community 5 - "now"
Cohesion: 0.11
Nodes (6): Audit Trail (immutable, every mutation), Store, scanMailAlias(), scanMailbox(), scanMailDomain(), now()

### Community 7 - "pathID"
Cohesion: 0.11
Nodes (6): Object Ownership Checks, aegis/internal/store.DNSZone, publicUser(), Server, Server, pathID()

### Community 8 - "writeErr"
Cohesion: 0.09
Nodes (13): dbCreateReq, ftpCreateReq, issueReq, Server, Server, Server, pkgAllowsDB(), pkgAllowsFTP() (+5 more)

### Community 9 - "SSL"
Cohesion: 0.18
Nodes (10): DNS, FTP, aegis/internal/store.SSLOrder, NewDomains(), CertInfo(), certNotAfter(), SSL, NewSSL() (+2 more)

### Community 10 - "Server"
Cohesion: 0.11
Nodes (9): mailAliasCreateReq, mailboxCreateReq, mailDomainCreateReq, webmailLoginReq, webmailSendReq, webmailSession, Server, webmailSessionFrom() (+1 more)

### Community 11 - "context.Context"
Cohesion: 0.14
Nodes (7): context.Context, Store, scanDomain(), scanRecord(), scanZone(), DNSRecord, DNSZone

### Community 12 - "parseTime"
Cohesion: 0.12
Nodes (11): Database, FTPAccount, Store, scanDatabase(), scanFTP(), scanProvider(), scanSSLOrder(), nullableTime() (+3 more)

### Community 13 - "WebFTP"
Cohesion: 0.20
Nodes (15): acctCoversHome(), decodeWebFTPJSON(), htmlEscape(), NewWebFTP(), requestHost(), webftpBrowserPage(), webftpLoginPage(), writeWebFTPErr() (+7 more)

### Community 14 - "time.Time"
Cohesion: 0.12
Nodes (30): time.Time, Container, PortMap, VolumeMount, composePortString(), composeVolumeSubpath(), normalizeComposeRestart(), ParseCompose() (+22 more)

### Community 15 - "RunTimeout"
Cohesion: 0.11
Nodes (16): net/http.Server, strings.Builder, sync.Map, Domain, RunTimeout(), flarumConfig(), apachePanelProxyAvailable(), WebServer (+8 more)

### Community 16 - ".obtain"
Cohesion: 0.20
Nodes (6): cloudflareProvider, SSL via lego (HTTP-01/DNS-01, auto-renew), github.com/go-acme/lego/v4/certificate.Resource, Provider, DNS, cfDNS01Provider

### Community 17 - "net/http.ResponseWriter"
Cohesion: 0.09
Nodes (6): Server, net/http.ResponseWriter, Server, tailLog(), Server, svcUID()

### Community 18 - "FTP"
Cohesion: 0.20
Nodes (8): createUserReq, updateUserReq, createSystemUser(), authHash(), FTP, NewFTP(), SetSystemPassword(), store.FTPAccount

### Community 19 - "Store"
Cohesion: 0.14
Nodes (6): Store, scanPackage(), scanUser(), Package, PackageUsage, User

### Community 20 - "Config"
Cohesion: 0.26
Nodes (10): DNSConfig, MariaDBCreds, PostgresCreds, WebServerConfig, Default(), envOr(), Config, intEnvOr() (+2 more)

### Community 21 - "Manager"
Cohesion: 0.18
Nodes (11): Admin Impersonation (login as user, imp claim), time.Duration, CheckPassword(), Claims, Manager, HasRole(), IsImpersonating(), logFailedAttempt() (+3 more)

### Community 22 - "wire"
Cohesion: 0.29
Nodes (18): cmdBackup(), cmdDB(), cmdDNS(), cmdDomain(), cmdFTP(), cmdPackage(), cmdSetup(), cmdSSL() (+10 more)

### Community 23 - "svc/packages.go"
Cohesion: 0.06
Nodes (23): aegis/internal/store.PackageUpdate, aptEnv(), DetectPkgManager(), dnfPackageName(), Packages, NewPackages(), osReleaseFields(), readOSRelease() (+15 more)

### Community 24 - "net/http.Request"
Cohesion: 0.14
Nodes (5): net/http.Request, Server, Server, Server, readJSON()

### Community 25 - "scanAPIToken"
Cohesion: 0.29
Nodes (4): APIToken, Store, scanAPIToken(), parseTimePtr()

### Community 26 - ".serveGoRoute"
Cohesion: 0.07
Nodes (28): crypto/tls.Certificate, encoding/json.RawMessage, net/http.Client, net/http.Header, net/http/httputil.ReverseProxy, GoRoute, boolEqual(), cfRecordKey() (+20 more)

### Community 28 - "DNSServer"
Cohesion: 0.18
Nodes (9): github.com/miekg/dns.Msg, github.com/miekg/dns.ResponseWriter, github.com/miekg/dns.RR, github.com/miekg/dns.Server, github.com/miekg/dns.SOA, mustSerial(), NewDNSServer(), recordTypeToWire() (+1 more)

### Community 29 - "Databases"
Cohesion: 0.26
Nodes (4): Databases, NewDatabases(), pgx.Conn, ServerInfo

### Community 30 - "IPs"
Cohesion: 0.23
Nodes (5): sync.Mutex, DetectLocalIPs(), IPs, localIPSet(), NewIPs()

### Community 31 - "fastcgi.go"
Cohesion: 0.33
Nodes (9): In-Tree FastCGI Client, bufio.Reader, net.Conn, fcgiEncodeParam(), fcgiRead(), fcgiRequest(), fcgiWrite(), fcgiRecord (+1 more)

### Community 32 - "Mail"
Cohesion: 0.11
Nodes (15): github.com/emersion/go-imap/v2/imapclient.Client, io.Reader, bcryptDovecot(), decodeTransfer(), DNS, Mail, imapLogin(), newByteReader() (+7 more)

### Community 33 - "serviceSet"
Cohesion: 0.11
Nodes (14): serviceSet, Built-in Authoritative DNS Server (miekg/dns), Secrets at Rest (AES-256-GCM Cipher), aegis/internal/svc.Domains, aegis/internal/svc.PHP, crypto/cipher.AEAD, Cipher, NewCipher() (+6 more)

### Community 34 - "testing.T"
Cohesion: 0.05
Nodes (65): createDomainReq, setDomainIPReq, updateDomainReq, newPanelHandler(), TestPanelHandlerBasePath(), TestPanelHandlerOutsideDelegate(), TestPanelHandlerRootMode(), image/color.RGBA (+57 more)

### Community 35 - "LookPath"
Cohesion: 0.33
Nodes (7): fpmRunning(), LookPath(), ProcessRunning(), ServiceRunning(), DetectFTP(), installAptPackages(), installCaddyRepo()

### Community 36 - "svc.Docker"
Cohesion: 0.14
Nodes (8): Domains, Files, containerName(), NewDocker(), portFree(), ContainerStats, CreateContainerRequest, svc.Docker

### Community 37 - "Server"
Cohesion: 0.11
Nodes (20): bootstrap(), createAdmin(), envOr(), main(), panelHandler(), run(), JWT + Revocable Sessions (HS256, sid check), aegis/internal/svc.Docker (+12 more)

### Community 38 - "Quota"
Cohesion: 0.39
Nodes (3): Quota, NewQuota(), Usage

### Community 39 - "AGENTS.md — AI agent entry point for Aegis"
Cohesion: 0.25
Nodes (7): AGENTS.md — AI agent entry point for Aegis, Honesty notes, Keeping the graph fresh, Known docs-vs-code drift (as of graph build), MCP server (for MCP-capable agents), Query the graph (preferred), Raw artifacts

### Community 40 - "wrapErr"
Cohesion: 0.14
Nodes (5): Store, scanCronJob(), Store, scanIP(), wrapErr()

### Community 42 - "acmeUser"
Cohesion: 0.33
Nodes (4): crypto/ecdsa.PrivateKey, crypto.PrivateKey, github.com/go-acme/lego/v4/registration.Resource, acmeUser

### Community 43 - "install-stack.sh"
Cohesion: 0.43
Nodes (7): apt_get(), DEBIAN_FRONTEND, die(), log(), install-stack.sh script, usage(), warn()

### Community 44 - "docs/ARCHITECTURE.md"
Cohesion: 0.17
Nodes (14): Backup & Restore (tar.gz + JSON manifest), Chrooted File Manager, Detection over Configuration, Docker Dev Container (bind-mounted repo), Extension Recipes (DNS provider, web server, API route), Hosting Packages & Quotas (feature flags), Idempotent Provisioning, Native Go Web Server (SNI certs, static+PHP) (+6 more)

### Community 45 - "handlers_dns.go"
Cohesion: 0.40
Nodes (4): providerReq, recordReq, zoneReq, sliceContains()

### Community 46 - "svc.PHP"
Cohesion: 0.14
Nodes (12): regexp.Regexp, restartService(), NewPHP(), sanitizedPHPIniLines(), systemdIsInit(), ValidatePHPIniSettings(), NewTuner(), serviceNameFor() (+4 more)

### Community 61 - "Store"
Cohesion: 0.16
Nodes (5): database/sql.DB, Store, New(), Security, NewSecurity()

### Community 62 - "Root-of-Truth Layering (JS→api→svc→store)"
Cohesion: 0.50
Nodes (4): API Layer (internal/api), Root-of-Truth Layering (JS→api→svc→store), Store Layer (internal/store, SQLite), Service Layer (internal/svc)

### Community 66 - "Backup"
Cohesion: 0.25
Nodes (6): github.com/pkg/sftp.Client, Backup, s3Client(), sftpClient(), minio.Client, TargetConfig

### Community 67 - "store.go"
Cohesion: 0.13
Nodes (7): Store, Store, scanPackageUpdate(), getBool(), getString(), isConstraint(), PackageUpdate

### Community 75 - "User"
Cohesion: 0.05
Nodes (38): github.com/coder/websocket.Conn, os.FileInfo, os.FileMode, Server, mustJSON(), writeWSJSON(), FTPAccount, User (+30 more)

### Community 76 - "Backup"
Cohesion: 0.30
Nodes (7): Backup, WebServer, NewBackup(), BackupInfo, Manifest, ManifestUser, PasswordSetter

### Community 77 - "server.go"
Cohesion: 0.20
Nodes (6): ctxKey, errBody, Server, bearerToken(), claimsFrom(), clientIP()

## Knowledge Gaps
- **63 isolated node(s):** `aegis`, `composeImportReq`, `createDomainReq`, `updateDomainReq`, `setDomainIPReq` (+58 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 168 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **24 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `docs/AI_INSTRUCTIONS.md — AI Session Rules` connect `docs/ARCHITECTURE.md` to `esc`, `Root-of-Truth Layering (JS→api→svc→store)`, `pathID`?**
  _High betweenness centrality (0.137) - this node is a cross-community bridge._
- **Why does `Framework-Free SPA (ES modules, addRoute, ui.js)` connect `esc` to `docs/ARCHITECTURE.md`?**
  _High betweenness centrality (0.126) - this node is a cross-community bridge._
- **Why does `User` connect `User` to `testing.T`, `Exec`, `svc.Docker`, `Server`, `Quota`, `pathID`, `writeErr`, `Backup`, `WebFTP`, `time.Time`, `FTP`, `Manager`, `wire`, `net/http.Request`, `Databases`?**
  _High betweenness centrality (0.119) - this node is a cross-community bridge._
- **Are the 142 inferred relationships involving `writeJSON()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeJSON()` has 142 INFERRED edges - model-reasoned connections that need verification._
- **Are the 136 inferred relationships involving `writeErr()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeErr()` has 136 INFERRED edges - model-reasoned connections that need verification._
- **What connects `aegis`, `composeImportReq`, `createDomainReq` to the rest of the system?**
  _63 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `esc` be split into smaller, more focused modules?**
  _Cohesion score 0.052324088341037495 - nodes in this community are weakly interconnected._
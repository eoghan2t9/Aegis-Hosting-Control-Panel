# Graph Report - hosting  (2026-09-16)

## Corpus Check
- 122 files · ~125,016 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 9 file(s) not represented in the graph (top: (none) 5, .dev 2, .conf 1)

## Summary
- 1481 nodes · 4788 edges · 84 communities (55 shown, 22 thin omitted)
- Extraction: 84% EXTRACTED · 16% INFERRED · 0% AMBIGUOUS · INFERRED: 781 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `9bf7109f`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- esc
- tuning.go
- system.go
- WebApps
- writeErr
- now
- PHP
- pathID
- net/http.ResponseWriter
- User
- net/http.Request
- wrapErr
- parseTime
- WebFTP
- time.Time
- SSL
- writeJSON
- Server
- FTP
- Store
- Config
- docs/ARCHITECTURE.md
- wire
- Exec
- readJSON
- scanAPIToken
- .serveGoRoute
- WebServer
- DNSServer
- Databases
- IPs
- .ApplyUpdatesStream
- Mail
- DNS
- testing.T
- userFrom
- RunTimeout
- Server
- Quota
- AGENTS.md — AI agent entry point for Aegis
- scanCronJob
- svc.go
- thumbnails.go
- install-stack.sh
- run
- handlers_dns.go
- Cipher
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
- Server
- context.Context
- Backup
- store.go
- Cron
- Provider
- handlers_ips.go
- DNS
- IP
- accounts.go
- svc/packages.go
- svc.Domains
- ExecWithEnv
- server.go
- Security
- .withFeature
- PHP
- writeWSJSON
- WebServer
- createSystemUser

## God Nodes (most connected - your core abstractions)
1. `writeJSON()` - 144 edges
2. `writeErr()` - 142 edges
3. `esc()` - 69 edges
4. `User` - 67 edges
5. `toast()` - 65 edges
6. `readJSON()` - 61 edges
7. `wrapErr()` - 59 edges
8. `Config` - 57 edges
9. `Store` - 57 edges
10. `RunTimeout()` - 54 edges

## Surprising Connections (you probably didn't know these)
- `Hosting Packages & Quotas (feature flags)` --references--> `pkgModal()`  [INFERRED]
  docs/ARCHITECTURE.md → web/js/views/accounts.js
- `API Layer (internal/api)` --references--> `New()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/api/server.go
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

## Communities (84 total, 22 thin omitted)

### Community 0 - "esc"
Cohesion: 0.05
Nodes (135): Framework-Free SPA (ES modules, addRoute, ui.js), api, qs(), addRoute(), boot(), buildShell(), can(), enterApp() (+127 more)

### Community 1 - "tuning.go"
Cohesion: 0.10
Nodes (21): Multi-PHP 7.4→8.4 per-domain pools, regexp.Regexp, NewPHP(), sanitizedPHPIniLines(), ValidatePHPIniSettings(), clamp(), containsString(), DetectDatabases() (+13 more)

### Community 2 - "system.go"
Cohesion: 0.17
Nodes (12): DetectPublicIP(), diskUsage(), System, NewSystem(), statusOf(), UIDFor(), DiskInfo, Metrics (+4 more)

### Community 3 - "WebApps"
Cohesion: 0.16
Nodes (25): RandomPassword(), RandomString(), flarumConfig(), installCraft(), installDrupal(), installFlarum(), installMediaWiki(), installNextcloud() (+17 more)

### Community 4 - "writeErr"
Cohesion: 0.16
Nodes (4): Server, Server, Server, writeErr()

### Community 5 - "now"
Cohesion: 0.12
Nodes (6): Audit Trail (immutable, every mutation), Store, scanMailAlias(), scanMailbox(), scanMailDomain(), now()

### Community 7 - "pathID"
Cohesion: 0.14
Nodes (4): Object Ownership Checks, aegis/internal/store.DNSZone, Server, pathID()

### Community 8 - "net/http.ResponseWriter"
Cohesion: 0.12
Nodes (9): dbCreateReq, ftpCreateReq, issueReq, net/http.ResponseWriter, Server, Server, pkgAllowsDB(), pkgAllowsFTP() (+1 more)

### Community 9 - "User"
Cohesion: 0.24
Nodes (7): os.FileMode, User, Files, groupName(), homeRoot(), ownerName(), Entry

### Community 10 - "net/http.Request"
Cohesion: 0.14
Nodes (10): mailAliasCreateReq, mailboxCreateReq, mailDomainCreateReq, webmailLoginReq, webmailSendReq, webmailSession, net/http.Request, Server (+2 more)

### Community 11 - "wrapErr"
Cohesion: 0.12
Nodes (7): Store, scanDomain(), scanRecord(), scanZone(), wrapErr(), store.DNSRecord, store.DNSZone

### Community 12 - "parseTime"
Cohesion: 0.13
Nodes (11): Database, FTPAccount, Store, scanDatabase(), scanFTP(), scanProvider(), scanSSLOrder(), nullableTime() (+3 more)

### Community 13 - "WebFTP"
Cohesion: 0.18
Nodes (16): Files, acctCoversHome(), decodeWebFTPJSON(), htmlEscape(), NewWebFTP(), requestHost(), webftpBrowserPage(), webftpLoginPage() (+8 more)

### Community 14 - "time.Time"
Cohesion: 0.13
Nodes (22): time.Time, Backup, WebServer, NewBackup(), APIToken, AuditEntry, BackupTarget, CronJob (+14 more)

### Community 15 - "SSL"
Cohesion: 0.09
Nodes (15): cloudflareProvider, SSL via lego (HTTP-01/DNS-01, auto-renew), aegis/internal/store.SSLOrder, crypto/ecdsa.PrivateKey, crypto.PrivateKey, github.com/go-acme/lego/v4/certificate.Resource, github.com/go-acme/lego/v4/registration.Resource, Provider (+7 more)

### Community 16 - "writeJSON"
Cohesion: 0.12
Nodes (5): Server, Server, Server, Server, writeJSON()

### Community 17 - "Server"
Cohesion: 0.15
Nodes (3): Server, tailLog(), svcUID()

### Community 18 - "FTP"
Cohesion: 0.30
Nodes (5): authHash(), FTP, NewFTP(), SetSystemPassword(), store.FTPAccount

### Community 19 - "Store"
Cohesion: 0.14
Nodes (6): Store, scanPackage(), scanUser(), Package, PackageUsage, User

### Community 20 - "Config"
Cohesion: 0.26
Nodes (10): DNSConfig, MariaDBCreds, PostgresCreds, WebServerConfig, Default(), envOr(), Config, intEnvOr() (+2 more)

### Community 21 - "docs/ARCHITECTURE.md"
Cohesion: 0.06
Nodes (37): Backup & Restore (tar.gz + JSON manifest), Chrooted File Manager, Detection over Configuration, Docker Dev Container (bind-mounted repo), Extension Recipes (DNS provider, web server, API route), In-Tree FastCGI Client, Hosting Packages & Quotas (feature flags), Idempotent Provisioning (+29 more)

### Community 22 - "wire"
Cohesion: 0.29
Nodes (18): cmdBackup(), cmdDB(), cmdDNS(), cmdDomain(), cmdFTP(), cmdPackage(), cmdSetup(), cmdSSL() (+10 more)

### Community 23 - "Exec"
Cohesion: 0.15
Nodes (8): dnfPackageName(), startsWithDigit(), Exec(), ExecQuiet(), DnfManager, PackageInfo, PackageUpdate, ZypperManager

### Community 25 - "scanAPIToken"
Cohesion: 0.33
Nodes (3): APIToken, Store, scanAPIToken()

### Community 26 - ".serveGoRoute"
Cohesion: 0.07
Nodes (28): crypto/tls.Certificate, encoding/json.RawMessage, net/http.Client, net/http.Header, net/http/httputil.ReverseProxy, GoRoute, boolEqual(), cfRecordKey() (+20 more)

### Community 28 - "DNSServer"
Cohesion: 0.18
Nodes (9): github.com/miekg/dns.Msg, github.com/miekg/dns.ResponseWriter, github.com/miekg/dns.RR, github.com/miekg/dns.Server, github.com/miekg/dns.SOA, mustSerial(), NewDNSServer(), recordTypeToWire() (+1 more)

### Community 30 - "IPs"
Cohesion: 0.25
Nodes (5): sync.Mutex, DetectLocalIPs(), IPs, localIPSet(), NewIPs()

### Community 31 - ".ApplyUpdatesStream"
Cohesion: 0.18
Nodes (4): validPkgArg(), ExecStream(), ApkManager, PacmanManager

### Community 32 - "Mail"
Cohesion: 0.11
Nodes (16): github.com/emersion/go-imap/v2/imapclient.Client, io.Reader, bcryptDovecot(), decodeTransfer(), DNS, Mail, imapLogin(), newByteReader() (+8 more)

### Community 33 - "DNS"
Cohesion: 0.23
Nodes (6): Built-in Authoritative DNS Server (miekg/dns), DNS, NewDNS(), localProvider, Provider, SyncResult

### Community 34 - "testing.T"
Cohesion: 0.09
Nodes (46): newPanelHandler(), TestPanelHandlerBasePath(), TestPanelHandlerOutsideDelegate(), TestPanelHandlerRootMode(), image/color.RGBA, testing.T, TestNormalizePanelBase(), TestPanelUpstream() (+38 more)

### Community 35 - "userFrom"
Cohesion: 0.15
Nodes (4): Server, Server, Server, userFrom()

### Community 36 - "RunTimeout"
Cohesion: 0.06
Nodes (31): Domains, net/http.Server, strings.Builder, sync.Map, Domain, containerName(), NewDocker(), portFree() (+23 more)

### Community 37 - "Server"
Cohesion: 0.18
Nodes (12): serviceSet, aegis/internal/svc.Docker, aegis/internal/svc.Domains, aegis/internal/svc.MetricsHistory, aegis/internal/svc.PHP, net/http.Handler, net/http.HandlerFunc, Server (+4 more)

### Community 38 - "Quota"
Cohesion: 0.39
Nodes (3): Quota, NewQuota(), Usage

### Community 39 - "AGENTS.md — AI agent entry point for Aegis"
Cohesion: 0.25
Nodes (7): AGENTS.md — AI agent entry point for Aegis, Honesty notes, Keeping the graph fresh, Known docs-vs-code drift (as of graph build), MCP server (for MCP-capable agents), Query the graph (preferred), Raw artifacts

### Community 41 - "svc.go"
Cohesion: 0.12
Nodes (19): createDomainReq, setDomainIPReq, updateDomainReq, validAlias(), fpmRunning(), IsValidIPv4(), NormalizeDomain(), ProcessRunning() (+11 more)

### Community 42 - "thumbnails.go"
Cohesion: 0.36
Nodes (8): generateImageThumb(), generatePDFThumb(), generateVideoThumb(), Thumbs, NewThumbs(), targetSize(), thumbCacheKey(), thumbKind()

### Community 43 - "install-stack.sh"
Cohesion: 0.43
Nodes (7): apt_get(), DEBIAN_FRONTEND, die(), log(), install-stack.sh script, usage(), warn()

### Community 44 - "run"
Cohesion: 0.22
Nodes (13): bootstrap(), createAdmin(), envOr(), main(), panelHandler(), run(), DNS, FTP (+5 more)

### Community 45 - "handlers_dns.go"
Cohesion: 0.40
Nodes (4): providerReq, recordReq, zoneReq, sliceContains()

### Community 46 - "Cipher"
Cohesion: 0.25
Nodes (5): Secrets at Rest (AES-256-GCM Cipher), crypto/cipher.AEAD, Cipher, NewCipher(), TestCipherRoundTrip()

### Community 61 - "Store"
Cohesion: 0.11
Nodes (11): API Layer (internal/api), Root-of-Truth Layering (JS→api→svc→store), Store Layer (internal/store, SQLite), Service Layer (internal/svc), database/sql.DB, Store, New(), APITokens (+3 more)

### Community 63 - "context.Context"
Cohesion: 0.12
Nodes (7): AuditEntry, context.Context, Store, scanBackupTarget(), Store, scanContainer(), Store

### Community 66 - "Backup"
Cohesion: 0.25
Nodes (6): github.com/pkg/sftp.Client, Backup, s3Client(), sftpClient(), minio.Client, TargetConfig

### Community 67 - "store.go"
Cohesion: 0.16
Nodes (7): Store, scanPackageUpdate(), getBool(), getString(), isConstraint(), parseTimePtr(), PackageUpdate

### Community 68 - "Cron"
Cohesion: 0.30
Nodes (4): Cron, NewCron(), ValidSchedule(), LogResult

### Community 72 - "IP"
Cohesion: 0.27
Nodes (3): Store, scanIP(), IP

### Community 73 - "accounts.go"
Cohesion: 0.40
Nodes (9): os.FileInfo, lookupGID(), lookupGroupName(), lookupUID(), lookupUserName(), parseGroup(), parsePasswd(), primaryGroup() (+1 more)

### Community 74 - "svc/packages.go"
Cohesion: 0.19
Nodes (9): aegis/internal/store.PackageUpdate, DetectPkgManager(), Packages, NewPackages(), osReleaseFields(), readOSRelease(), splitApkNameVersion(), DistroInfo (+1 more)

### Community 75 - "svc.Domains"
Cohesion: 0.15
Nodes (11): aegis/internal/store.DNSRecord, aegis/internal/store.FTPAccount, ValidateRecord(), sanitizeFTPUsername(), WebftpHostname(), DetectPrimaryIP(), TestValidateRecord(), BackfilledFTP (+3 more)

### Community 76 - "ExecWithEnv"
Cohesion: 0.32
Nodes (3): aptEnv(), ExecWithEnv(), AptManager

### Community 77 - "server.go"
Cohesion: 0.19
Nodes (7): ctxKey, errBody, Server, bearerToken(), claimsFrom(), clientIP(), IsImpersonating()

### Community 81 - "writeWSJSON"
Cohesion: 0.50
Nodes (3): github.com/coder/websocket.Conn, mustJSON(), writeWSJSON()

### Community 83 - "createSystemUser"
Cohesion: 0.50
Nodes (3): createUserReq, updateUserReq, createSystemUser()

## Knowledge Gaps
- **62 isolated node(s):** `GROUP_ORDER`, `overviewCache`, `routes`, `state`, `paths` (+57 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 163 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **22 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `docs/AI_INSTRUCTIONS.md — AI Session Rules` connect `docs/ARCHITECTURE.md` to `esc`, `Store`, `pathID`?**
  _High betweenness centrality (0.167) - this node is a cross-community bridge._
- **Why does `Framework-Free SPA (ES modules, addRoute, ui.js)` connect `esc` to `docs/ARCHITECTURE.md`?**
  _High betweenness centrality (0.165) - this node is a cross-community bridge._
- **Why does `User` connect `User` to `WebApps`, `net/http.ResponseWriter`, `WebFTP`, `time.Time`, `FTP`, `docs/ARCHITECTURE.md`, `wire`, `readJSON`, `Databases`, `testing.T`, `userFrom`, `RunTimeout`, `Server`, `Quota`, `thumbnails.go`, `Store`, `Server`, `Cron`, `svc.Domains`, `.withFeature`, `writeWSJSON`, `createSystemUser`?**
  _High betweenness centrality (0.121) - this node is a cross-community bridge._
- **Are the 141 inferred relationships involving `writeJSON()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeJSON()` has 141 INFERRED edges - model-reasoned connections that need verification._
- **Are the 135 inferred relationships involving `writeErr()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeErr()` has 135 INFERRED edges - model-reasoned connections that need verification._
- **What connects `GROUP_ORDER`, `overviewCache`, `routes` to the rest of the system?**
  _62 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `esc` be split into smaller, more focused modules?**
  _Cohesion score 0.05390509477080253 - nodes in this community are weakly interconnected._
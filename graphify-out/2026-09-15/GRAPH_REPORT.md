# Graph Report - hosting  (2026-09-15)

## Corpus Check
- 120 files · ~114,858 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 9 file(s) not represented in the graph (top: (none) 5, .dev 2, .conf 1)

## Summary
- 1380 nodes · 4461 edges · 71 communities (50 shown, 14 thin omitted)
- Extraction: 83% EXTRACTED · 17% INFERRED · 0% AMBIGUOUS · INFERRED: 760 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `ed4a1a47`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- esc
- docs/ARCHITECTURE.md
- User
- testing.T
- svc.PHP
- context.Context
- RunTimeout
- pathID
- tuning.go
- wrapErr
- Server
- Store
- Store
- Databases
- writeJSON
- WebApps
- Packages
- net/http.ResponseWriter
- IPs
- Store
- Config
- validPkgArg
- wire
- Backup
- net/http.Request
- scanAPIToken
- SSL
- WebServer
- DNSServer
- writeErr
- DNS
- cloudflareProvider
- time.Time
- run
- Domains
- FTP
- Exec
- Server
- Quota
- AGENTS.md — AI agent entry point for Aegis
- scanCronJob
- Backup
- server.go
- install-stack.sh
- Store
- handlers_dns.go
- scanBackupTarget
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
- IP
- serviceSet
- thumbnails.go
- getBool
- acmeUser
- svc/packages.go
- handlers_ips.go

## God Nodes (most connected - your core abstractions)
1. `writeJSON()` - 141 edges
2. `writeErr()` - 141 edges
3. `User` - 70 edges
4. `esc()` - 65 edges
5. `toast()` - 64 edges
6. `readJSON()` - 61 edges
7. `wrapErr()` - 59 edges
8. `Config` - 57 edges
9. `Store` - 54 edges
10. `RunTimeout()` - 54 edges

## Surprising Connections (you probably didn't know these)
- `docker/docker-compose.yml — Dev Stack` --shares_data_with--> `main()`  [INFERRED]
  docker/docker-compose.yml → cmd/aegis/main.go
- `API Layer (internal/api)` --references--> `New()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/api/server.go
- `Hosting Packages & Quotas (feature flags)` --references--> `pkgModal()`  [INFERRED]
  docs/ARCHITECTURE.md → web/js/views/accounts.js
- `SSL via lego (HTTP-01/DNS-01, auto-renew)` --references--> `DNS`  [INFERRED]
  docs/ARCHITECTURE.md → internal/svc/cloudflare.go
- `Multi-PHP 7.4→8.4 per-domain pools` --references--> `DetectPHP()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/svc/tuning.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Panel Request Lifecycle** — concept_api_layer, concept_svc_layer, concept_store_layer, concept_layered_architecture [EXTRACTED 1.00]
- **Extension Points** — concept_extension_recipes, concept_provider_plugin_model, svc_dns_pluginnames, svc_webserver_goroutes, internal_svc_tuning_detectphp [INFERRED 0.85]
- **Defense-in-Depth Security Model** — concept_ownership_checks, concept_audit_trail, concept_encrypted_secrets, concept_chroot_file_manager, concept_role_model [INFERRED 0.85]

## Communities (71 total, 14 thin omitted)

### Community 0 - "esc"
Cohesion: 0.05
Nodes (132): Framework-Free SPA (ES modules, addRoute, ui.js), api, qs(), addRoute(), boot(), buildShell(), can(), enterApp() (+124 more)

### Community 1 - "docs/ARCHITECTURE.md"
Cohesion: 0.05
Nodes (41): API Layer (internal/api), Backup & Restore (tar.gz + JSON manifest), Chrooted File Manager, Detection over Configuration, Docker Dev Container (bind-mounted repo), Extension Recipes (DNS provider, web server, API route), In-Tree FastCGI Client, Hosting Packages & Quotas (feature flags) (+33 more)

### Community 2 - "User"
Cohesion: 0.06
Nodes (32): Admin Impersonation (login as user, imp claim), os.FileInfo, os.FileMode, time.Duration, Server, Server, CheckPassword(), Claims (+24 more)

### Community 3 - "testing.T"
Cohesion: 0.06
Nodes (55): createDomainReq, setDomainIPReq, updateDomainReq, newPanelHandler(), TestPanelHandlerBasePath(), TestPanelHandlerOutsideDelegate(), TestPanelHandlerRootMode(), image/color.RGBA (+47 more)

### Community 4 - "svc.PHP"
Cohesion: 0.21
Nodes (9): regexp.Regexp, fpmRunning(), NewPHP(), sanitizedPHPIniLines(), ValidatePHPIniSettings(), PHPFPMTuning, svc.PHP, phpIniDirective (+1 more)

### Community 5 - "context.Context"
Cohesion: 0.17
Nodes (6): Audit Trail (immutable, every mutation), context.Context, Store, scanMailAlias(), scanMailbox(), scanMailDomain()

### Community 6 - "RunTimeout"
Cohesion: 0.06
Nodes (31): Files, net/http.Server, strings.Builder, sync.Map, Domain, containerName(), NewDocker(), portFree() (+23 more)

### Community 7 - "pathID"
Cohesion: 0.13
Nodes (4): Object Ownership Checks, Server, Server, pathID()

### Community 8 - "tuning.go"
Cohesion: 0.18
Nodes (14): Multi-PHP 7.4→8.4 per-domain pools, clamp(), containsString(), DetectDatabases(), DetectFTP(), DetectPHP(), Tuner, NewTuner() (+6 more)

### Community 9 - "wrapErr"
Cohesion: 0.19
Nodes (4): isConstraint(), now(), parseTime(), wrapErr()

### Community 10 - "Server"
Cohesion: 0.11
Nodes (9): mailAliasCreateReq, mailboxCreateReq, mailDomainCreateReq, webmailLoginReq, webmailSendReq, webmailSession, Server, webmailSessionFrom() (+1 more)

### Community 11 - "Store"
Cohesion: 0.11
Nodes (6): Store, scanDomain(), scanRecord(), scanZone(), DNSRecord, DNSZone

### Community 12 - "Store"
Cohesion: 0.14
Nodes (6): Provider, Store, scanDatabase(), scanFTP(), scanProvider(), scanSSLOrder()

### Community 13 - "Databases"
Cohesion: 0.17
Nodes (9): Databases, NewDatabases(), QuoteSQL(), ServiceRunning(), TestQuoteSQL(), TestValidDBName(), ValidDBName(), pgx.Conn (+1 more)

### Community 14 - "writeJSON"
Cohesion: 0.10
Nodes (10): dbCreateReq, ftpCreateReq, issueReq, Server, Server, Server, pkgAllowsDB(), pkgAllowsFTP() (+2 more)

### Community 15 - "WebApps"
Cohesion: 0.16
Nodes (25): RandomPassword(), RandomString(), flarumConfig(), installCraft(), installDrupal(), installFlarum(), installMediaWiki(), installNextcloud() (+17 more)

### Community 16 - "Packages"
Cohesion: 0.19
Nodes (4): aegis/internal/store.PackageUpdate, Packages, splitApkNameVersion(), ApkManager

### Community 17 - "net/http.ResponseWriter"
Cohesion: 0.11
Nodes (5): net/http.ResponseWriter, Server, Server, Server, Server

### Community 18 - "IPs"
Cohesion: 0.08
Nodes (22): Domains, github.com/coder/websocket.Conn, sync.Mutex, mustJSON(), svcUID(), writeWSJSON(), DetectLocalIPs(), DetectPublicIP() (+14 more)

### Community 19 - "Store"
Cohesion: 0.13
Nodes (4): Store, scanPackage(), scanUser(), PackageUsage

### Community 20 - "Config"
Cohesion: 0.26
Nodes (10): DNSConfig, MariaDBCreds, PostgresCreds, WebServerConfig, Default(), envOr(), Config, intEnvOr() (+2 more)

### Community 21 - "validPkgArg"
Cohesion: 0.23
Nodes (4): aptEnv(), validPkgArg(), ExecStream(), AptManager

### Community 22 - "wire"
Cohesion: 0.29
Nodes (18): cmdBackup(), cmdDB(), cmdDNS(), cmdDomain(), cmdFTP(), cmdPackage(), cmdSetup(), cmdSSL() (+10 more)

### Community 23 - "Backup"
Cohesion: 0.25
Nodes (6): github.com/pkg/sftp.Client, Backup, s3Client(), sftpClient(), minio.Client, TargetConfig

### Community 24 - "net/http.Request"
Cohesion: 0.14
Nodes (6): net/http.Request, Server, Server, Server, Server, readJSON()

### Community 25 - "scanAPIToken"
Cohesion: 0.28
Nodes (3): Store, scanAPIToken(), getString()

### Community 26 - "SSL"
Cohesion: 0.18
Nodes (8): github.com/go-acme/lego/v4/certificate.Resource, CertInfo(), certNotAfter(), DNS, SSL, WebServer, NewSSL(), webrootProvider

### Community 28 - "DNSServer"
Cohesion: 0.18
Nodes (9): github.com/miekg/dns.Msg, github.com/miekg/dns.ResponseWriter, github.com/miekg/dns.RR, github.com/miekg/dns.Server, github.com/miekg/dns.SOA, mustSerial(), NewDNSServer(), recordTypeToWire() (+1 more)

### Community 29 - "writeErr"
Cohesion: 0.16
Nodes (6): publicUser(), Server, Server, Server, userFrom(), writeErr()

### Community 30 - "DNS"
Cohesion: 0.23
Nodes (6): Built-in Authoritative DNS Server (miekg/dns), DNS, NewDNS(), localProvider, Provider, SyncResult

### Community 31 - "cloudflareProvider"
Cohesion: 0.13
Nodes (14): encoding/json.RawMessage, net/http.Client, boolEqual(), cfRecordKey(), DNS, recordKey(), cfAccount, cfAPIError (+6 more)

### Community 32 - "time.Time"
Cohesion: 0.08
Nodes (31): github.com/emersion/go-imap/v2/imapclient.Client, io.Reader, time.Time, bcryptDovecot(), decodeTransfer(), DNS, Mail, imapLogin() (+23 more)

### Community 33 - "run"
Cohesion: 0.21
Nodes (9): bootstrap(), createAdmin(), envOr(), main(), panelHandler(), run(), First-Boot Auto Tuning (sysctl/php-fpm sizing), Security (+1 more)

### Community 34 - "Domains"
Cohesion: 0.28
Nodes (4): NewDomains(), CreateOptions, svc.Domains, WebServer

### Community 35 - "FTP"
Cohesion: 0.20
Nodes (7): createUserReq, updateUserReq, createSystemUser(), authHash(), FTP, NewFTP(), SetSystemPassword()

### Community 36 - "Exec"
Cohesion: 0.13
Nodes (9): Exec(), ExecQuiet(), ExecWithEnv(), ProcessRunning(), DnfManager, PackageInfo, PackageUpdate, PacmanManager (+1 more)

### Community 37 - "Server"
Cohesion: 0.15
Nodes (11): aegis/internal/svc.MetricsHistory, net/http.Handler, net/http.HandlerFunc, detectPrimaryIP(), Server, New(), APITokens, NewAPITokens() (+3 more)

### Community 38 - "Quota"
Cohesion: 0.39
Nodes (3): Quota, NewQuota(), Usage

### Community 39 - "AGENTS.md — AI agent entry point for Aegis"
Cohesion: 0.25
Nodes (7): AGENTS.md — AI agent entry point for Aegis, Honesty notes, Keeping the graph fresh, Known docs-vs-code drift (as of graph build), MCP server (for MCP-capable agents), Query the graph (preferred), Raw artifacts

### Community 41 - "Backup"
Cohesion: 0.30
Nodes (7): Backup, WebServer, NewBackup(), BackupInfo, Manifest, ManifestUser, PasswordSetter

### Community 42 - "server.go"
Cohesion: 0.20
Nodes (7): ctxKey, errBody, Server, bearerToken(), claimsFrom(), clientIP(), IsImpersonating()

### Community 43 - "install-stack.sh"
Cohesion: 0.43
Nodes (7): apt_get(), DEBIAN_FRONTEND, die(), log(), install-stack.sh script, usage(), warn()

### Community 45 - "handlers_dns.go"
Cohesion: 0.40
Nodes (4): providerReq, recordReq, zoneReq, sliceContains()

### Community 61 - "Store"
Cohesion: 0.26
Nodes (3): database/sql.DB, Store, New()

### Community 62 - "IP"
Cohesion: 0.25
Nodes (3): Store, scanIP(), IP

### Community 63 - "serviceSet"
Cohesion: 0.22
Nodes (6): serviceSet, Secrets at Rest (AES-256-GCM Cipher), crypto/cipher.AEAD, Cipher, NewCipher(), TestCipherRoundTrip()

### Community 66 - "thumbnails.go"
Cohesion: 0.36
Nodes (8): generateImageThumb(), generatePDFThumb(), generateVideoThumb(), Thumbs, NewThumbs(), targetSize(), thumbCacheKey(), thumbKind()

### Community 67 - "getBool"
Cohesion: 0.20
Nodes (5): Store, Store, scanPackageUpdate(), getBool(), PackageUpdate

### Community 68 - "acmeUser"
Cohesion: 0.33
Nodes (4): crypto/ecdsa.PrivateKey, crypto.PrivateKey, github.com/go-acme/lego/v4/registration.Resource, acmeUser

### Community 69 - "svc/packages.go"
Cohesion: 0.33
Nodes (8): DetectPkgManager(), dnfPackageName(), NewPackages(), osReleaseFields(), readOSRelease(), startsWithDigit(), DistroInfo, PkgManager

## Knowledge Gaps
- **59 isolated node(s):** `createDomainReq`, `updateDomainReq`, `setDomainIPReq`, `createIPReq`, `updateIPReq` (+54 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 149 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **14 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `User` connect `User` to `testing.T`, `RunTimeout`, `pathID`, `wrapErr`, `Databases`, `writeJSON`, `WebApps`, `IPs`, `Store`, `wire`, `net/http.Request`, `writeErr`, `time.Time`, `Domains`, `FTP`, `Server`, `Quota`, `Backup`, `thumbnails.go`?**
  _High betweenness centrality (0.127) - this node is a cross-community bridge._
- **Why does `docs/AI_INSTRUCTIONS.md — AI Session Rules` connect `docs/ARCHITECTURE.md` to `esc`, `pathID`?**
  _High betweenness centrality (0.118) - this node is a cross-community bridge._
- **Why does `Framework-Free SPA (ES modules, addRoute, ui.js)` connect `esc` to `docs/ARCHITECTURE.md`?**
  _High betweenness centrality (0.114) - this node is a cross-community bridge._
- **Are the 138 inferred relationships involving `writeJSON()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeJSON()` has 138 INFERRED edges - model-reasoned connections that need verification._
- **Are the 134 inferred relationships involving `writeErr()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeErr()` has 134 INFERRED edges - model-reasoned connections that need verification._
- **What connects `createDomainReq`, `updateDomainReq`, `setDomainIPReq` to the rest of the system?**
  _59 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `esc` be split into smaller, more focused modules?**
  _Cohesion score 0.054945054945054944 - nodes in this community are weakly interconnected._
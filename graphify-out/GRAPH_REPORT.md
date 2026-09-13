# Graph Report - hosting  (2026-09-13)

## Corpus Check
- 103 files · ~90,350 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 9 file(s) not represented in the graph (top: (none) 5, .dev 2, .conf 1)

## Summary
- 1225 nodes · 4064 edges · 61 communities (43 shown, 13 thin omitted)
- Extraction: 84% EXTRACTED · 16% INFERRED · 0% AMBIGUOUS · INFERRED: 667 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `f7d4b297`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- esc
- docs/ARCHITECTURE.md
- User
- testing.T
- tuning.go
- parseTime
- RunTimeout
- pathID
- Exec
- getBool
- net/http.Request
- context.Context
- wrapErr
- Databases
- writeJSON
- net/http.ResponseWriter
- svc/packages.go
- Cipher
- Security
- now
- Config
- Domain
- Store
- BackupTarget
- readJSON
- APIToken
- Manager
- userFrom
- DNSServer
- writeErr
- DNS
- SSL
- Mail
- time.Time
- LookPath
- .ownsDomain
- svc_test.go
- Server
- .InstallApp
- AGENTS.md — AI agent entry point for Aegis
- scanCronJob
- Quota
- newTestStore
- install-stack.sh
- writePlaceholderPage
- handlers_dns.go
- newPanelHandler
- handlers_backup.go
- handlers_auth.go
- handlers_webapps.go
- handlers_packages.go
- backupTargetReq
- cronCreateReq
- fileOpReq
- tokenCreateReq
- aegis

## God Nodes (most connected - your core abstractions)
1. `writeJSON()` - 123 edges
2. `writeErr()` - 123 edges
3. `esc()` - 61 edges
4. `User` - 60 edges
5. `readJSON()` - 57 edges
6. `toast()` - 57 edges
7. `Config` - 56 edges
8. `Store` - 50 edges
9. `wrapErr()` - 50 edges
10. `userFrom()` - 45 edges

## Surprising Connections (you probably didn't know these)
- `docker/docker-compose.yml — Dev Stack` --shares_data_with--> `main()`  [INFERRED]
  docker/docker-compose.yml → cmd/aegis/main.go
- `API Layer (internal/api)` --references--> `New()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/api/server.go
- `Hosting Packages & Quotas (feature flags)` --references--> `pkgModal()`  [INFERRED]
  docs/ARCHITECTURE.md → web/js/views/accounts.js
- `First-Boot Auto Tuning (sysctl/php-fpm sizing)` --references--> `bootstrap()`  [EXTRACTED]
  docs/ARCHITECTURE.md → cmd/aegis/main.go
- `SSL via lego (HTTP-01/DNS-01, auto-renew)` --references--> `DNS`  [INFERRED]
  docs/ARCHITECTURE.md → internal/svc/cloudflare.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Panel Request Lifecycle** — concept_api_layer, concept_svc_layer, concept_store_layer, concept_layered_architecture [EXTRACTED 1.00]
- **Extension Points** — concept_extension_recipes, concept_provider_plugin_model, svc_dns_pluginnames, svc_webserver_goroutes, internal_svc_tuning_detectphp [INFERRED 0.85]
- **Defense-in-Depth Security Model** — concept_ownership_checks, concept_audit_trail, concept_encrypted_secrets, concept_chroot_file_manager, concept_role_model [INFERRED 0.85]

## Communities (61 total, 13 thin omitted)

### Community 0 - "esc"
Cohesion: 0.06
Nodes (119): Framework-Free SPA (ES modules, addRoute, ui.js), api, qs(), addRoute(), boot(), buildShell(), enterApp(), GROUP_ORDER (+111 more)

### Community 1 - "docs/ARCHITECTURE.md"
Cohesion: 0.06
Nodes (37): API Layer (internal/api), Backup & Restore (tar.gz + JSON manifest), Chrooted File Manager, Detection over Configuration, Docker Dev Container (bind-mounted repo), Extension Recipes (DNS provider, web server, API route), In-Tree FastCGI Client, Hosting Packages & Quotas (feature flags) (+29 more)

### Community 2 - "User"
Cohesion: 0.06
Nodes (40): createDomainReq, updateDomainReq, github.com/coder/websocket.Conn, os.FileInfo, os.FileMode, svcCreateOptions(), validAlias(), mustJSON() (+32 more)

### Community 3 - "testing.T"
Cohesion: 0.21
Nodes (23): image/color.RGBA, testing.T, NewFiles(), filepathHasPrefix(), newFilesT(), TestChmodRecursiveAppliesToAllDescendants(), TestChmodRecursiveSkipsSymlinks(), TestChownRecursiveSetsAllDescendants() (+15 more)

### Community 4 - "tuning.go"
Cohesion: 0.09
Nodes (24): Multi-PHP 7.4→8.4 per-domain pools, diskUsage(), System, NewSystem(), statusOf(), UIDFor(), clamp(), containsString() (+16 more)

### Community 5 - "parseTime"
Cohesion: 0.15
Nodes (8): Store, scanMailAlias(), scanMailbox(), scanMailDomain(), MailAlias, Mailbox, MailDomain, parseTime()

### Community 6 - "RunTimeout"
Cohesion: 0.14
Nodes (9): strings.Builder, sync.Mutex, RunTimeout(), apachePanelProxyAvailable(), WebServer, hostnames(), NewWebServer(), reloadService() (+1 more)

### Community 7 - "pathID"
Cohesion: 0.19
Nodes (3): Server, Server, pathID()

### Community 8 - "Exec"
Cohesion: 0.11
Nodes (11): validPkgArg(), Exec(), ExecQuiet(), ExecWithEnv(), ProcessRunning(), AptManager, DnfManager, PackageInfo (+3 more)

### Community 9 - "getBool"
Cohesion: 0.18
Nodes (6): Store, scanPackageUpdate(), getBool(), getString(), isConstraint(), PackageUpdate

### Community 10 - "net/http.Request"
Cohesion: 0.15
Nodes (10): mailAliasCreateReq, mailboxCreateReq, mailDomainCreateReq, webmailLoginReq, webmailSendReq, webmailSession, net/http.Request, Server (+2 more)

### Community 11 - "context.Context"
Cohesion: 0.15
Nodes (7): context.Context, Store, scanDomain(), scanRecord(), scanZone(), DNSRecord, DNSZone

### Community 12 - "wrapErr"
Cohesion: 0.12
Nodes (9): FTPAccount, SSLOrder, Provider, Store, scanDatabase(), scanFTP(), scanProvider(), scanSSLOrder() (+1 more)

### Community 13 - "Databases"
Cohesion: 0.18
Nodes (9): Databases, NewDatabases(), QuoteSQL(), ServiceRunning(), TestQuoteSQL(), TestValidDBName(), ValidDBName(), pgx.Conn (+1 more)

### Community 14 - "writeJSON"
Cohesion: 0.12
Nodes (9): dbCreateReq, ftpCreateReq, issueReq, Server, Server, pkgAllowsDB(), pkgAllowsFTP(), pathIDFromQuery() (+1 more)

### Community 15 - "net/http.ResponseWriter"
Cohesion: 0.12
Nodes (6): net/http.ResponseWriter, Server, Server, Server, svcUID(), Server

### Community 16 - "svc/packages.go"
Cohesion: 0.16
Nodes (11): DetectPkgManager(), dnfPackageName(), Packages, NewPackages(), osReleaseFields(), readOSRelease(), splitApkNameVersion(), startsWithDigit() (+3 more)

### Community 17 - "Cipher"
Cohesion: 0.25
Nodes (5): Secrets at Rest (AES-256-GCM Cipher), crypto/cipher.AEAD, Cipher, NewCipher(), TestCipherRoundTrip()

### Community 19 - "now"
Cohesion: 0.12
Nodes (7): Audit Trail (immutable, every mutation), now(), Store, scanPackage(), scanUser(), Package, PackageUsage

### Community 20 - "Config"
Cohesion: 0.20
Nodes (12): DNSConfig, MariaDBCreds, PostgresCreds, WebServerConfig, Default(), envOr(), Config, intEnvOr() (+4 more)

### Community 21 - "Domain"
Cohesion: 0.30
Nodes (20): Database, Domain, RandomPassword(), RandomString(), flarumConfig(), installCraft(), installDrupal(), installFlarum() (+12 more)

### Community 22 - "Store"
Cohesion: 0.06
Nodes (42): serviceSet, createUserReq, updateUserReq, cmdBackup(), cmdDB(), cmdDNS(), cmdDomain(), cmdFTP() (+34 more)

### Community 23 - "BackupTarget"
Cohesion: 0.16
Nodes (9): github.com/pkg/sftp.Client, Store, scanBackupTarget(), BackupTarget, Backup, s3Client(), sftpClient(), minio.Client (+1 more)

### Community 25 - "APIToken"
Cohesion: 0.18
Nodes (5): Store, scanAPIToken(), APIToken, APITokens, NewAPITokens()

### Community 26 - "Manager"
Cohesion: 0.18
Nodes (10): Admin Impersonation (login as user, imp claim), time.Duration, CheckPassword(), Claims, Manager, HasRole(), logFailedAttempt(), New() (+2 more)

### Community 27 - "userFrom"
Cohesion: 0.11
Nodes (11): ctxKey, errBody, Server, Server, Server, Server, bearerToken(), claimsFrom() (+3 more)

### Community 28 - "DNSServer"
Cohesion: 0.18
Nodes (9): github.com/miekg/dns.Msg, github.com/miekg/dns.ResponseWriter, github.com/miekg/dns.RR, github.com/miekg/dns.Server, github.com/miekg/dns.SOA, mustSerial(), NewDNSServer(), recordTypeToWire() (+1 more)

### Community 29 - "writeErr"
Cohesion: 0.14
Nodes (5): publicUser(), Server, Server, Server, writeErr()

### Community 30 - "DNS"
Cohesion: 0.23
Nodes (6): Built-in Authoritative DNS Server (miekg/dns), DNS, NewDNS(), localProvider, Provider, SyncResult

### Community 31 - "SSL"
Cohesion: 0.06
Nodes (29): SSL via lego (HTTP-01/DNS-01, auto-renew), crypto/ecdsa.PrivateKey, crypto.PrivateKey, encoding/json.RawMessage, github.com/go-acme/lego/v4/certificate.Resource, github.com/go-acme/lego/v4/registration.Resource, net/http.Client, boolEqual() (+21 more)

### Community 32 - "Mail"
Cohesion: 0.14
Nodes (15): github.com/emersion/go-imap/v2/imapclient.Client, io.Reader, bcryptDovecot(), decodeTransfer(), DNS, Mail, imapLogin(), newByteReader() (+7 more)

### Community 33 - "time.Time"
Cohesion: 0.22
Nodes (7): time.Time, Store, LoginAttempt, PackageUpdate, AuditEntry, Provider, Session

### Community 34 - "LookPath"
Cohesion: 0.24
Nodes (5): restartService(), systemdIsInit(), LookPath(), DetectFTP(), DetectWebServers()

### Community 36 - "svc_test.go"
Cohesion: 0.25
Nodes (7): ValidateRecord(), IsValidIPv4(), NormalizeDomain(), TestIsValidIPv4(), TestNormalizeDomain(), TestRandomPassword(), TestValidateRecord()

### Community 37 - "Server"
Cohesion: 0.16
Nodes (17): bootstrap(), createAdmin(), envOr(), main(), panelHandler(), run(), startGoSiteServers(), net/http.Handler (+9 more)

### Community 38 - ".InstallApp"
Cohesion: 0.33
Nodes (7): downloadAndExtract(), ensureWPCLI(), FindApp(), isEffectivelyEmpty(), isZipFile(), NewWebApps(), sanitizeDBIdent()

### Community 39 - "AGENTS.md — AI agent entry point for Aegis"
Cohesion: 0.25
Nodes (7): AGENTS.md — AI agent entry point for Aegis, Honesty notes, Keeping the graph fresh, Known docs-vs-code drift (as of graph build), MCP server (for MCP-capable agents), Query the graph (preferred), Raw artifacts

### Community 41 - "Quota"
Cohesion: 0.39
Nodes (3): Quota, NewQuota(), Usage

### Community 42 - "newTestStore"
Cohesion: 0.43
Nodes (7): newTestStore(), TestDomainWithZoneAndRecords(), TestPackageDefaultSeed(), TestSessionsAndAudit(), TestSettings(), TestUserCRUD(), Store

### Community 43 - "install-stack.sh"
Cohesion: 0.43
Nodes (7): apt_get(), DEBIAN_FRONTEND, die(), log(), install-stack.sh script, usage(), warn()

### Community 44 - "writePlaceholderPage"
Cohesion: 0.47
Nodes (4): TestWritePlaceholderPage(), TestWritePlaceholderPageEscapesDomain(), TestWritePlaceholderPageNeverOverwrites(), writePlaceholderPage()

### Community 45 - "handlers_dns.go"
Cohesion: 0.40
Nodes (4): providerReq, recordReq, zoneReq, sliceContains()

### Community 46 - "newPanelHandler"
Cohesion: 0.70
Nodes (4): newPanelHandler(), TestPanelHandlerBasePath(), TestPanelHandlerOutsideDelegate(), TestPanelHandlerRootMode()

## Knowledge Gaps
- **56 isolated node(s):** `aegis`, `loginReq`, `impersonateReq`, `backupCreateReq`, `backupRestoreReq` (+51 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 134 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **13 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `docs/AI_INSTRUCTIONS.md — AI Session Rules` connect `docs/ARCHITECTURE.md` to `esc`, `.ownsDomain`?**
  _High betweenness centrality (0.132) - this node is a cross-community bridge._
- **Why does `Framework-Free SPA (ES modules, addRoute, ui.js)` connect `esc` to `docs/ARCHITECTURE.md`?**
  _High betweenness centrality (0.129) - this node is a cross-community bridge._
- **Why does `User` connect `User` to `time.Time`, `testing.T`, `Server`, `.InstallApp`, `pathID`, `Quota`, `Databases`, `writeJSON`, `now`, `Store`, `readJSON`, `APIToken`, `Manager`, `userFrom`, `writeErr`?**
  _High betweenness centrality (0.127) - this node is a cross-community bridge._
- **Are the 120 inferred relationships involving `writeJSON()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeJSON()` has 120 INFERRED edges - model-reasoned connections that need verification._
- **Are the 116 inferred relationships involving `writeErr()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeErr()` has 116 INFERRED edges - model-reasoned connections that need verification._
- **What connects `aegis`, `loginReq`, `impersonateReq` to the rest of the system?**
  _56 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `esc` be split into smaller, more focused modules?**
  _Cohesion score 0.061432554897176715 - nodes in this community are weakly interconnected._
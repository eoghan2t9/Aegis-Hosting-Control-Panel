# Graph Report - hosting  (2026-09-25)

## Corpus Check
- 126 files · ~134,052 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 9 file(s) not represented in the graph (top: (none) 5, .dev 2, .conf 1)

## Summary
- 1552 nodes · 4977 edges · 91 communities (60 shown, 25 thin omitted)
- Extraction: 83% EXTRACTED · 17% INFERRED · 0% AMBIGUOUS · INFERRED: 833 edges (avg confidence: 0.85)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `9669a620`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- esc
- LookPath
- newTestManager
- Exec
- writeJSON
- parseTime
- PHP
- writeErr
- net/http.ResponseWriter
- User
- Server
- wrapErr
- Store
- WebFTP
- time.Time
- Domain
- userFrom
- net/http.Request
- FTP
- now
- Config
- Manager
- wire
- Packages
- readJSON
- scanAPIToken
- .serveGoRoute
- WebServer
- DNSServer
- Databases
- svc/packages.go
- newFilesT
- Mail
- docs/ARCHITECTURE.md
- testing.T
- RunTimeout
- svc.Docker
- Server
- Quota
- AGENTS.md — AI agent entry point for Aegis
- Store
- scanBackupTarget
- Cron
- install-stack.sh
- DnfManager
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
- context.Context
- .InstallApp
- Store
- composeImportReq
- Backup
- store.go
- accounts.go
- Provider
- handlers_ips.go
- DNS
- AuditEntry
- aegis/internal/store.DNSRecord
- aegis/internal/store.FTPAccount
- Domains
- validPkgArg
- thumbnails.go
- store.DNSRecord
- run
- PHP
- writeWSJSON
- WebServer
- ExecWithEnv
- scanCronJob
- newTestStore
- handlers_domains.go
- newTestDomains
- writePlaceholderPage
- .withFeature
- newPanelHandler

## God Nodes (most connected - your core abstractions)
1. `writeJSON()` - 149 edges
2. `writeErr()` - 147 edges
3. `User` - 71 edges
4. `esc()` - 69 edges
5. `toast()` - 67 edges
6. `readJSON()` - 65 edges
7. `Store` - 61 edges
8. `wrapErr()` - 60 edges
9. `RunTimeout()` - 57 edges
10. `Config` - 57 edges

## Surprising Connections (you probably didn't know these)
- `Hosting Packages & Quotas (feature flags)` --references--> `pkgModal()`  [INFERRED]
  docs/ARCHITECTURE.md → web/js/views/accounts.js
- `docker/docker-compose.yml — Dev Stack` --shares_data_with--> `main()`  [INFERRED]
  docker/docker-compose.yml → cmd/aegis/main.go
- `API Layer (internal/api)` --references--> `New()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/api/server.go
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

## Communities (91 total, 25 thin omitted)

### Community 0 - "esc"
Cohesion: 0.05
Nodes (141): Framework-Free SPA (ES modules, addRoute, ui.js), api, qs(), addRoute(), boot(), buildShell(), can(), enterApp() (+133 more)

### Community 1 - "LookPath"
Cohesion: 0.12
Nodes (25): Multi-PHP 7.4→8.4 per-domain pools, First-Boot Auto Tuning (sysctl/php-fpm sizing), DiskInfo, fpmRunning(), LookPath(), ProcessRunning(), ServiceRunning(), clamp() (+17 more)

### Community 2 - "newTestManager"
Cohesion: 0.16
Nodes (17): newTestManager(), TestBackupCodeLoginIsSingleUse(), TestDisableTOTPRequiresCorrectPassword(), TestFullTOTPEnrollAndLoginFlow(), TestLoginWithoutTOTPIsUnaffected(), GenerateBackupCodes(), GenerateTOTPSecret(), contains() (+9 more)

### Community 3 - "Exec"
Cohesion: 0.33
Nodes (15): Exec(), RandomPassword(), installCraft(), installDrupal(), installFlarum(), installMediaWiki(), installNextcloud(), installPhpBB() (+7 more)

### Community 4 - "writeJSON"
Cohesion: 0.12
Nodes (5): Server, Server, Server, Server, writeJSON()

### Community 5 - "parseTime"
Cohesion: 0.15
Nodes (5): Store, scanMailAlias(), scanMailbox(), scanMailDomain(), parseTime()

### Community 7 - "writeErr"
Cohesion: 0.12
Nodes (7): Object Ownership Checks, aegis/internal/store.DNSZone, publicUser(), Server, Server, pathID(), writeErr()

### Community 8 - "net/http.ResponseWriter"
Cohesion: 0.09
Nodes (10): dbCreateReq, ftpCreateReq, issueReq, net/http.ResponseWriter, Server, Server, Server, pkgAllowsDB() (+2 more)

### Community 9 - "User"
Cohesion: 0.26
Nodes (7): os.FileMode, User, Files, groupName(), homeRoot(), ownerName(), Entry

### Community 10 - "Server"
Cohesion: 0.11
Nodes (9): mailAliasCreateReq, mailboxCreateReq, mailDomainCreateReq, webmailLoginReq, webmailSendReq, webmailSession, Server, webmailSessionFrom() (+1 more)

### Community 11 - "wrapErr"
Cohesion: 0.12
Nodes (7): Store, scanDomain(), scanRecord(), scanZone(), DNSRecord, wrapErr(), DNSZone

### Community 12 - "Store"
Cohesion: 0.12
Nodes (10): Database, FTPAccount, Store, scanDatabase(), scanFTP(), scanProvider(), scanSSLOrder(), nullableTime() (+2 more)

### Community 13 - "WebFTP"
Cohesion: 0.17
Nodes (16): RandomString(), acctCoversHome(), decodeWebFTPJSON(), htmlEscape(), NewWebFTP(), requestHost(), webftpBrowserPage(), webftpLoginPage() (+8 more)

### Community 14 - "time.Time"
Cohesion: 0.09
Nodes (37): time.Time, Container, PortMap, VolumeMount, Backup, WebServer, NewBackup(), composePortString() (+29 more)

### Community 15 - "Domain"
Cohesion: 0.14
Nodes (8): net/http.Server, strings.Builder, sync.Map, Domain, flarumConfig(), WebServer, hostnames(), siteName()

### Community 16 - "userFrom"
Cohesion: 0.16
Nodes (4): Server, Server, Server, userFrom()

### Community 17 - "net/http.Request"
Cohesion: 0.09
Nodes (10): ctxKey, errBody, Server, net/http.Request, Server, tailLog(), svcUID(), bearerToken() (+2 more)

### Community 18 - "FTP"
Cohesion: 0.17
Nodes (10): createUserReq, updateUserReq, createSystemUser(), authHash(), FTP, NewFTP(), SetSystemPassword(), TestValidUsername() (+2 more)

### Community 19 - "now"
Cohesion: 0.12
Nodes (7): now(), Store, scanPackage(), scanUser(), Package, PackageUsage, User

### Community 20 - "Config"
Cohesion: 0.20
Nodes (12): DNSConfig, MariaDBCreds, PostgresCreds, WebServerConfig, Default(), envOr(), Config, intEnvOr() (+4 more)

### Community 21 - "Manager"
Cohesion: 0.18
Nodes (11): Claims, Manager, time.Duration, CheckPassword(), consumeBackupCode(), HasRole(), IsImpersonating(), logFailedAttempt() (+3 more)

### Community 22 - "wire"
Cohesion: 0.11
Nodes (33): serviceSet, cmdBackup(), cmdDB(), cmdDNS(), cmdDomain(), cmdFTP(), cmdPackage(), cmdSetup() (+25 more)

### Community 23 - "Packages"
Cohesion: 0.19
Nodes (4): aegis/internal/store.PackageUpdate, Packages, splitApkNameVersion(), ApkManager

### Community 24 - "readJSON"
Cohesion: 0.19
Nodes (3): Server, Server, readJSON()

### Community 25 - "scanAPIToken"
Cohesion: 0.33
Nodes (3): APIToken, Store, scanAPIToken()

### Community 26 - ".serveGoRoute"
Cohesion: 0.06
Nodes (37): In-Tree FastCGI Client, bufio.Reader, crypto/tls.Certificate, encoding/json.RawMessage, net.Conn, net/http.Client, net/http.Header, net/http/httputil.ReverseProxy (+29 more)

### Community 28 - "DNSServer"
Cohesion: 0.18
Nodes (9): github.com/miekg/dns.Msg, github.com/miekg/dns.ResponseWriter, github.com/miekg/dns.RR, github.com/miekg/dns.Server, github.com/miekg/dns.SOA, mustSerial(), NewDNSServer(), recordTypeToWire() (+1 more)

### Community 29 - "Databases"
Cohesion: 0.26
Nodes (4): Databases, NewDatabases(), pgx.Conn, ServerInfo

### Community 30 - "svc/packages.go"
Cohesion: 0.24
Nodes (10): DetectPkgManager(), dnfPackageName(), NewPackages(), osReleaseFields(), readOSRelease(), startsWithDigit(), ExecQuiet(), DistroInfo (+2 more)

### Community 31 - "newFilesT"
Cohesion: 0.29
Nodes (12): NewFiles(), filepathHasPrefix(), newFilesT(), TestChmodRecursiveAppliesToAllDescendants(), TestChmodRecursiveSkipsSymlinks(), TestChownRecursiveSetsAllDescendants(), TestFileCRUD(), TestListCreatesMissingHomeDir() (+4 more)

### Community 32 - "Mail"
Cohesion: 0.11
Nodes (15): github.com/emersion/go-imap/v2/imapclient.Client, io.Reader, bcryptDovecot(), decodeTransfer(), DNS, Mail, imapLogin(), newByteReader() (+7 more)

### Community 33 - "docs/ARCHITECTURE.md"
Cohesion: 0.05
Nodes (36): API Layer (internal/api), Built-in Authoritative DNS Server (miekg/dns), Backup & Restore (tar.gz + JSON manifest), Chrooted File Manager, Detection over Configuration, Docker Dev Container (bind-mounted repo), Secrets at Rest (AES-256-GCM Cipher), Extension Recipes (DNS provider, web server, API route) (+28 more)

### Community 34 - "testing.T"
Cohesion: 0.17
Nodes (21): image/color.RGBA, testing.T, IsValidIPv4(), NormalizeDomain(), QuoteSQL(), TestIsValidIPv4(), TestNormalizeDomain(), TestQuoteSQL() (+13 more)

### Community 35 - "RunTimeout"
Cohesion: 0.21
Nodes (9): restartService(), systemdIsInit(), RunTimeout(), apachePanelProxyAvailable(), htCaddyTarget(), installAptPackages(), installCaddyRepo(), nginxListen() (+1 more)

### Community 36 - "svc.Docker"
Cohesion: 0.15
Nodes (8): Domains, Files, containerName(), NewDocker(), portFree(), ContainerStats, CreateContainerRequest, svc.Docker

### Community 37 - "Server"
Cohesion: 0.18
Nodes (9): JWT + Revocable Sessions (HS256, sid check), aegis/internal/svc.Docker, aegis/internal/svc.MetricsHistory, net/http.Handler, net/http.HandlerFunc, Server, New(), Security (+1 more)

### Community 38 - "Quota"
Cohesion: 0.39
Nodes (3): Quota, NewQuota(), Usage

### Community 39 - "AGENTS.md — AI agent entry point for Aegis"
Cohesion: 0.25
Nodes (7): AGENTS.md — AI agent entry point for Aegis, Honesty notes, Keeping the graph fresh, Known docs-vs-code drift (as of graph build), MCP server (for MCP-capable agents), Query the graph (preferred), Raw artifacts

### Community 42 - "Cron"
Cohesion: 0.30
Nodes (4): Cron, NewCron(), ValidSchedule(), LogResult

### Community 43 - "install-stack.sh"
Cohesion: 0.43
Nodes (7): apt_get(), DEBIAN_FRONTEND, die(), log(), install-stack.sh script, usage(), warn()

### Community 44 - "DnfManager"
Cohesion: 0.17
Nodes (3): DnfManager, PackageInfo, PacmanManager

### Community 45 - "handlers_dns.go"
Cohesion: 0.40
Nodes (4): providerReq, recordReq, zoneReq, sliceContains()

### Community 46 - "svc.PHP"
Cohesion: 0.19
Nodes (9): regexp.Regexp, NewPHP(), sanitizedPHPIniLines(), ValidatePHPIniSettings(), NewTuner(), PHPFPMTuning, svc.PHP, phpIniDirective (+1 more)

### Community 48 - "handlers_auth.go"
Cohesion: 0.33
Nodes (5): impersonateReq, loginReq, totpConfirmReq, totpDisableReq, totpVerifyReq

### Community 61 - "context.Context"
Cohesion: 0.09
Nodes (13): Audit Trail (immutable, every mutation), context.Context, database/sql.DB, sync.Mutex, Store, Store, New(), APITokens (+5 more)

### Community 62 - ".InstallApp"
Cohesion: 0.25
Nodes (9): downloadAndExtract(), ensureWPCLI(), FindApp(), isEffectivelyEmpty(), isZipFile(), NewWebApps(), sanitizeDBIdent(), AppManifest (+1 more)

### Community 66 - "Backup"
Cohesion: 0.25
Nodes (6): github.com/pkg/sftp.Client, Backup, s3Client(), sftpClient(), minio.Client, TargetConfig

### Community 67 - "store.go"
Cohesion: 0.16
Nodes (7): Store, scanPackageUpdate(), getBool(), getString(), isConstraint(), parseTimePtr(), PackageUpdate

### Community 68 - "accounts.go"
Cohesion: 0.40
Nodes (9): os.FileInfo, lookupGID(), lookupGroupName(), lookupUID(), lookupUserName(), parseGroup(), parsePasswd(), primaryGroup() (+1 more)

### Community 75 - "Domains"
Cohesion: 0.06
Nodes (26): cloudflareProvider, DNS, FTP, aegis/internal/store.SSLOrder, crypto/ecdsa.PrivateKey, crypto.PrivateKey, github.com/go-acme/lego/v4/certificate.Resource, github.com/go-acme/lego/v4/registration.Resource (+18 more)

### Community 76 - "validPkgArg"
Cohesion: 0.27
Nodes (3): validPkgArg(), ExecStream(), ZypperManager

### Community 77 - "thumbnails.go"
Cohesion: 0.36
Nodes (8): generateImageThumb(), generatePDFThumb(), generateVideoThumb(), Thumbs, NewThumbs(), targetSize(), thumbCacheKey(), thumbKind()

### Community 79 - "run"
Cohesion: 0.36
Nodes (8): bootstrap(), createAdmin(), envOr(), main(), panelHandler(), run(), NewWebServer(), PHP

### Community 81 - "writeWSJSON"
Cohesion: 0.28
Nodes (6): github.com/coder/websocket.Conn, mustJSON(), writeWSJSON(), Terminal, NewTerminal(), termMsg

### Community 83 - "ExecWithEnv"
Cohesion: 0.28
Nodes (3): aptEnv(), ExecWithEnv(), AptManager

### Community 85 - "newTestStore"
Cohesion: 0.43
Nodes (7): newTestStore(), TestDomainWithZoneAndRecords(), TestPackageDefaultSeed(), TestSessionsAndAudit(), TestSettings(), TestUserCRUD(), Store

### Community 86 - "handlers_domains.go"
Cohesion: 0.29
Nodes (6): createDomainReq, setDomainIPReq, updateDomainReq, validAlias(), TestValidDomain(), ValidDomain()

### Community 87 - "newTestDomains"
Cohesion: 0.52
Nodes (6): newTestDomains(), TestResolveDocRootAllowsNormalAfterRejects(), TestResolveDocRootDeepPath(), TestResolveDocRootNestedUnderMaster(), TestResolveDocRootOwnFolder(), TestResolveDocRootRejects()

### Community 88 - "writePlaceholderPage"
Cohesion: 0.47
Nodes (4): TestWritePlaceholderPage(), TestWritePlaceholderPageEscapesDomain(), TestWritePlaceholderPageNeverOverwrites(), writePlaceholderPage()

### Community 90 - "newPanelHandler"
Cohesion: 0.70
Nodes (4): newPanelHandler(), TestPanelHandlerBasePath(), TestPanelHandlerOutsideDelegate(), TestPanelHandlerRootMode()

## Knowledge Gaps
- **66 isolated node(s):** `loginReq`, `totpVerifyReq`, `totpConfirmReq`, `totpDisableReq`, `impersonateReq` (+61 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 170 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **25 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `User` connect `User` to `newTestManager`, `writeErr`, `net/http.ResponseWriter`, `WebFTP`, `time.Time`, `Domain`, `userFrom`, `FTP`, `Manager`, `wire`, `readJSON`, `Databases`, `newFilesT`, `testing.T`, `svc.Docker`, `Server`, `Quota`, `Cron`, `context.Context`, `.InstallApp`, `Domains`, `thumbnails.go`, `writeWSJSON`, `newTestDomains`, `.withFeature`?**
  _High betweenness centrality (0.142) - this node is a cross-community bridge._
- **Why does `docs/AI_INSTRUCTIONS.md — AI Session Rules` connect `docs/ARCHITECTURE.md` to `esc`, `writeErr`?**
  _High betweenness centrality (0.138) - this node is a cross-community bridge._
- **Why does `Framework-Free SPA (ES modules, addRoute, ui.js)` connect `esc` to `docs/ARCHITECTURE.md`?**
  _High betweenness centrality (0.133) - this node is a cross-community bridge._
- **Are the 146 inferred relationships involving `writeJSON()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeJSON()` has 146 INFERRED edges - model-reasoned connections that need verification._
- **Are the 140 inferred relationships involving `writeErr()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeErr()` has 140 INFERRED edges - model-reasoned connections that need verification._
- **What connects `loginReq`, `totpVerifyReq`, `totpConfirmReq` to the rest of the system?**
  _66 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `esc` be split into smaller, more focused modules?**
  _Cohesion score 0.05159751428033394 - nodes in this community are weakly interconnected._
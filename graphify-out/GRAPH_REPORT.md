# Graph Report - hosting  (2026-09-13)

## Corpus Check
- 98 files · ~85,803 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 9 file(s) not represented in the graph (top: (none) 5, .dev 2, .conf 1)

## Summary
- 1181 nodes · 3964 edges · 60 communities (44 shown, 12 thin omitted)
- Extraction: 84% EXTRACTED · 16% INFERRED · 0% AMBIGUOUS · INFERRED: 654 edges (avg confidence: 0.85)
- Token cost: 16,000 input · 4,200 output

## Community Hubs (Navigation)
- Frontend SPA Shell & Router
- Users & System Handlers
- Auth Manager & Core Concepts
- Domain Handlers & Store Tests
- Platform Handlers (Backups, Metrics)
- PHP-FPM Pools & Thumbnails
- Cron, Database & FTP Handlers
- Mail Store (Mailboxes & Aliases)
- Web Server Config Generators
- Domains & DNS Store Layer
- Store Models (FTP, SSL, Providers)
- System Exec & Package Updates
- Mail API Handlers
- Backup Targets (S3/SFTP)
- Package Manager Detection
- Users & Packages Store
- Auth, Audit & Backup Handlers
- Store Core, Tokens & Security
- Cipher & DNS Service
- Database Provisioning Service
- Web App Installers
- DNS Zone & Record Handlers
- aegisctl CLI Commands
- Router & Middleware (Auth/Roles)
- Store Writers (Sessions, Audit, Tokens)
- System Metrics Service
- File Manager Handlers
- Authoritative DNS Server
- Cloudflare API Client
- Configuration & Secrets
- IMAP Mail Client
- Backup & Restore Service
- Store Models (Audit, Sessions, Logins)
- Packages Store & Helpers
- SSL Service (lego)
- FTP Service (vsftpd)
- Domain Handlers & Ownership
- Domains Service (docroots, vhosts)
- Go Site Server (TLS/FastCGI)
- Cloudflare DNS-01 Provider
- WebApps Installer Service
- Panel Bootstrap (cmd/aegis)
- Quota Enforcement
- ACME Registration (lego)
- SSL Issuance & DNS-01 Concepts
- DNS Request Types
- Backup Handlers
- Auth Handlers (Login/Impersonate)
- WebApps Handlers
- Packages Install Handlers
- Backup Target Handlers
- Cron Handlers
- File Op Request Types
- API Token Handlers
- Quota Handler
- Community 59

## God Nodes (most connected - your core abstractions)
1. `writeJSON()` - 123 edges
2. `writeErr()` - 122 edges
3. `esc()` - 61 edges
4. `User` - 60 edges
5. `readJSON()` - 57 edges
6. `toast()` - 57 edges
7. `Config` - 55 edges
8. `Store` - 50 edges
9. `wrapErr()` - 50 edges
10. `userFrom()` - 45 edges

## Surprising Connections (you probably didn't know these)
- `docker/docker-compose.yml — Dev Stack` --shares_data_with--> `main()`  [INFERRED]
  docker/docker-compose.yml → cmd/aegis/main.go
- `API Layer (internal/api)` --references--> `New()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/api/server.go
- `Multi-PHP 7.4→8.4 per-domain pools` --references--> `DetectPHP()`  [EXTRACTED]
  docs/ARCHITECTURE.md → internal/svc/tuning.go
- `Hosting Packages & Quotas (feature flags)` --references--> `pkgModal()`  [INFERRED]
  docs/ARCHITECTURE.md → web/js/views/accounts.js
- `First-Boot Auto Tuning (sysctl/php-fpm sizing)` --references--> `bootstrap()`  [EXTRACTED]
  docs/ARCHITECTURE.md → cmd/aegis/main.go

## Import Cycles
- None detected.

## Hyperedges (group relationships)
- **Panel Request Lifecycle** — concept_api_layer, concept_svc_layer, concept_store_layer, concept_layered_architecture [EXTRACTED 1.00]
- **Extension Points** — concept_extension_recipes, concept_provider_plugin_model, svc_dns_pluginnames, svc_webserver_goroutes, internal_svc_tuning_detectphp [INFERRED 0.85]
- **Defense-in-Depth Security Model** — concept_ownership_checks, concept_audit_trail, concept_encrypted_secrets, concept_chroot_file_manager, concept_role_model [INFERRED 0.85]

## Communities (60 total, 12 thin omitted)

### Community 0 - "Frontend SPA Shell & Router"
Cohesion: 0.06
Nodes (117): Framework-Free SPA (ES modules, addRoute, ui.js), api, qs(), addRoute(), boot(), buildShell(), enterApp(), GROUP_ORDER (+109 more)

### Community 1 - "Users & System Handlers"
Cohesion: 0.06
Nodes (40): API Layer (internal/api), Backup & Restore (tar.gz + JSON manifest), Chrooted File Manager, Detection over Configuration, Docker Dev Container (bind-mounted repo), Extension Recipes (DNS provider, web server, API route), In-Tree FastCGI Client, Hosting Packages & Quotas (feature flags) (+32 more)

### Community 2 - "Auth Manager & Core Concepts"
Cohesion: 0.09
Nodes (27): createUserReq, updateUserReq, github.com/coder/websocket.Conn, os.FileInfo, os.FileMode, mustJSON(), writeWSJSON(), createSystemUser() (+19 more)

### Community 3 - "Domain Handlers & Store Tests"
Cohesion: 0.09
Nodes (43): createDomainReq, updateDomainReq, image/color.RGBA, testing.T, svcCreateOptions(), validAlias(), newTestStore(), TestDomainWithZoneAndRecords() (+35 more)

### Community 4 - "Platform Handlers (Backups, Metrics)"
Cohesion: 0.09
Nodes (27): First-Boot Auto Tuning (sysctl/php-fpm sizing), fpmRunning(), PHP, NewPHP(), systemdIsInit(), LookPath(), generateImageThumb(), generatePDFThumb() (+19 more)

### Community 5 - "PHP-FPM Pools & Thumbnails"
Cohesion: 0.10
Nodes (10): Store, scanMailAlias(), scanMailbox(), scanMailDomain(), MailAlias, Mailbox, MailDomain, bcryptDovecot() (+2 more)

### Community 6 - "Cron, Database & FTP Handlers"
Cohesion: 0.17
Nodes (9): strings.Builder, sync.Mutex, Domain, WebServer, hostnames(), NewWebServer(), reloadService(), siteName() (+1 more)

### Community 7 - "Mail Store (Mailboxes & Aliases)"
Cohesion: 0.16
Nodes (5): publicUser(), Server, Server, pathID(), writeErr()

### Community 8 - "Web Server Config Generators"
Cohesion: 0.12
Nodes (12): validPkgArg(), Exec(), ExecQuiet(), ExecWithEnv(), ProcessRunning(), ServiceRunning(), AptManager, DnfManager (+4 more)

### Community 9 - "Domains & DNS Store Layer"
Cohesion: 0.09
Nodes (10): Store, scanBackupTarget(), Store, scanCronJob(), Store, scanPackageUpdate(), getBool(), isConstraint() (+2 more)

### Community 10 - "Store Models (FTP, SSL, Providers)"
Cohesion: 0.15
Nodes (10): mailAliasCreateReq, mailboxCreateReq, mailDomainCreateReq, webmailLoginReq, webmailSendReq, webmailSession, net/http.Request, Server (+2 more)

### Community 11 - "System Exec & Package Updates"
Cohesion: 0.14
Nodes (8): context.Context, Store, scanDomain(), scanRecord(), scanZone(), DNSRecord, DNSZone, getString()

### Community 12 - "Mail API Handlers"
Cohesion: 0.13
Nodes (9): FTPAccount, SSLOrder, Provider, Store, scanDatabase(), scanFTP(), scanProvider(), scanSSLOrder() (+1 more)

### Community 13 - "Backup Targets (S3/SFTP)"
Cohesion: 0.11
Nodes (16): Databases, NewDatabases(), QuoteSQL(), RunTimeout(), TestQuoteSQL(), TestValidDBName(), ValidDBName(), downloadAndExtract() (+8 more)

### Community 14 - "Package Manager Detection"
Cohesion: 0.12
Nodes (9): dbCreateReq, ftpCreateReq, issueReq, net/http.ResponseWriter, Server, Server, pkgAllowsDB(), pkgAllowsFTP() (+1 more)

### Community 15 - "Users & Packages Store"
Cohesion: 0.11
Nodes (6): Server, Server, Server, Server, Server, writeJSON()

### Community 16 - "Auth, Audit & Backup Handlers"
Cohesion: 0.14
Nodes (12): PackageUpdate, DetectPkgManager(), dnfPackageName(), Packages, NewPackages(), osReleaseFields(), readOSRelease(), splitApkNameVersion() (+4 more)

### Community 17 - "Store Core, Tokens & Security"
Cohesion: 0.13
Nodes (15): serviceSet, Secrets at Rest (AES-256-GCM Cipher), crypto/cipher.AEAD, detectPrimaryIP(), New(), Backup, WebServer, NewBackup() (+7 more)

### Community 18 - "Cipher & DNS Service"
Cohesion: 0.13
Nodes (13): bootstrap(), createAdmin(), envOr(), main(), run(), startGoSiteServers(), database/sql.DB, Store (+5 more)

### Community 19 - "Database Provisioning Service"
Cohesion: 0.13
Nodes (7): Audit Trail (immutable, every mutation), now(), Store, scanPackage(), scanUser(), Package, PackageUsage

### Community 20 - "Web App Installers"
Cohesion: 0.19
Nodes (14): DNSConfig, MariaDBCreds, PostgresCreds, WebServerConfig, Default(), envOr(), Config, intEnvOr() (+6 more)

### Community 21 - "DNS Zone & Record Handlers"
Cohesion: 0.29
Nodes (19): Database, RandomPassword(), RandomString(), flarumConfig(), installCraft(), installDrupal(), installFlarum(), installMediaWiki() (+11 more)

### Community 22 - "aegisctl CLI Commands"
Cohesion: 0.29
Nodes (18): cmdBackup(), cmdDB(), cmdDNS(), cmdDomain(), cmdFTP(), cmdPackage(), cmdSetup(), cmdSSL() (+10 more)

### Community 23 - "Router & Middleware (Auth/Roles)"
Cohesion: 0.26
Nodes (7): github.com/pkg/sftp.Client, BackupTarget, Backup, s3Client(), sftpClient(), minio.Client, TargetConfig

### Community 25 - "System Metrics Service"
Cohesion: 0.18
Nodes (5): Store, scanAPIToken(), APIToken, APITokens, NewAPITokens()

### Community 26 - "File Manager Handlers"
Cohesion: 0.18
Nodes (11): diskUsage(), System, NewSystem(), statusOf(), UIDFor(), DiskInfo, Metrics, Overview (+3 more)

### Community 27 - "Authoritative DNS Server"
Cohesion: 0.23
Nodes (4): Server, Server, Server, userFrom()

### Community 28 - "Cloudflare API Client"
Cohesion: 0.18
Nodes (9): github.com/miekg/dns.Msg, github.com/miekg/dns.ResponseWriter, github.com/miekg/dns.RR, github.com/miekg/dns.Server, github.com/miekg/dns.SOA, mustSerial(), NewDNSServer(), recordTypeToWire() (+1 more)

### Community 29 - "Configuration & Secrets"
Cohesion: 0.19
Nodes (7): ctxKey, errBody, Server, bearerToken(), claimsFrom(), clientIP(), IsImpersonating()

### Community 30 - "IMAP Mail Client"
Cohesion: 0.23
Nodes (6): Built-in Authoritative DNS Server (miekg/dns), DNS, NewDNS(), localProvider, Provider, SyncResult

### Community 31 - "Backup & Restore Service"
Cohesion: 0.20
Nodes (10): encoding/json.RawMessage, boolEqual(), cfRecordKey(), recordKey(), cfAccount, cfAPIError, cfRecord, cfResponse (+2 more)

### Community 32 - "Store Models (Audit, Sessions, Logins)"
Cohesion: 0.24
Nodes (10): github.com/emersion/go-imap/v2/imapclient.Client, io.Reader, decodeTransfer(), imapLogin(), newByteReader(), parseMIMEBody(), parseMultipart(), byteReader (+2 more)

### Community 33 - "Packages Store & Helpers"
Cohesion: 0.23
Nodes (6): time.Time, Store, LoginAttempt, AuditEntry, Provider, Session

### Community 34 - "SSL Service (lego)"
Cohesion: 0.27
Nodes (6): authHash(), FTP, NewFTP(), SetSystemPassword(), TestValidUsername(), ValidUsername()

### Community 36 - "Domain Handlers & Ownership"
Cohesion: 0.29
Nodes (4): CreateOptions, Domains, WebServer, NewDomains()

### Community 37 - "Domains Service (docroots, vhosts)"
Cohesion: 0.33
Nodes (5): panelHandler(), JWT + Revocable Sessions (HS256, sid check), net/http.Handler, net/http.HandlerFunc, Server

### Community 39 - "Cloudflare DNS-01 Provider"
Cohesion: 0.31
Nodes (5): crypto/tls.Certificate, WebServer, loadCert(), remoteIP(), truncate()

### Community 40 - "WebApps Installer Service"
Cohesion: 0.31
Nodes (5): github.com/go-acme/lego/v4/certificate.Resource, DNS, SSL, WebServer, NewSSL()

### Community 41 - "Panel Bootstrap (cmd/aegis)"
Cohesion: 0.39
Nodes (3): Quota, NewQuota(), Usage

### Community 42 - "Quota Enforcement"
Cohesion: 0.22
Nodes (4): CertInfo(), certNotAfter(), cfDNS01Provider, webrootProvider

### Community 43 - "ACME Registration (lego)"
Cohesion: 0.33
Nodes (4): crypto/ecdsa.PrivateKey, crypto.PrivateKey, github.com/go-acme/lego/v4/registration.Resource, acmeUser

### Community 44 - "SSL Issuance & DNS-01 Concepts"
Cohesion: 0.38
Nodes (3): net/http.Client, DNS, cloudflareProvider

### Community 45 - "DNS Request Types"
Cohesion: 0.40
Nodes (4): providerReq, recordReq, zoneReq, sliceContains()

### Community 46 - "Backup Handlers"
Cohesion: 0.50
Nodes (3): SSL via lego (HTTP-01/DNS-01, auto-renew), Provider, DNS

## Knowledge Gaps
- **48 isolated node(s):** `aegis`, `loginReq`, `impersonateReq`, `backupCreateReq`, `backupRestoreReq` (+43 more)
  These have ≤1 connection - possible missing edges or undocumented components. (Counts symbols only; 122 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **12 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `User` connect `Auth Manager & Core Concepts` to `Users & System Handlers`, `Packages Store & Helpers`, `Domain Handlers & Store Tests`, `Domain Handlers & Ownership`, `Domains Service (docroots, vhosts)`, `SSL Service (lego)`, `Mail Store (Mailboxes & Aliases)`, `Platform Handlers (Backups, Metrics)`, `Panel Bootstrap (cmd/aegis)`, `Backup Targets (S3/SFTP)`, `Package Manager Detection`, `Store Core, Tokens & Security`, `Database Provisioning Service`, `aegisctl CLI Commands`, `Store Writers (Sessions, Audit, Tokens)`, `System Metrics Service`, `Authoritative DNS Server`?**
  _High betweenness centrality (0.143) - this node is a cross-community bridge._
- **Why does `docs/AI_INSTRUCTIONS.md — AI Session Rules` connect `Users & System Handlers` to `Frontend SPA Shell & Router`, `FTP Service (vsftpd)`?**
  _High betweenness centrality (0.122) - this node is a cross-community bridge._
- **Why does `Framework-Free SPA (ES modules, addRoute, ui.js)` connect `Frontend SPA Shell & Router` to `Users & System Handlers`?**
  _High betweenness centrality (0.119) - this node is a cross-community bridge._
- **Are the 120 inferred relationships involving `writeJSON()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeJSON()` has 120 INFERRED edges - model-reasoned connections that need verification._
- **Are the 115 inferred relationships involving `writeErr()` (e.g. with `.handleAliasesAdd()` and `.handleAliasesRemove()`) actually correct?**
  _`writeErr()` has 115 INFERRED edges - model-reasoned connections that need verification._
- **What connects `aegis`, `loginReq`, `impersonateReq` to the rest of the system?**
  _48 weakly-connected nodes found - possible documentation gaps or missing edges._
- **Should `Frontend SPA Shell & Router` be split into smaller, more focused modules?**
  _Cohesion score 0.06203518955196807 - nodes in this community are weakly interconnected._
// Command aegisctl is the Aegis panel CLI. It performs the same operations as
// the web UI for scripting and emergencies: account resets, backups,
// DNS syncs, SSL issuance, database provisioning and performance tuning.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"aegis/internal/auth"
	"aegis/internal/config"
	"aegis/internal/store"
	"aegis/internal/svc"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	if err := dispatch(cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`aegisctl — Aegis hosting control panel CLI

Usage: aegisctl <command> [flags]

Setup & info
  setup                 initialise config, dirs, database and auto-tune
  status                host + service status
  tune [--apply]        regenerate the tuning report (optionally apply sysctl)
  version               print version

Users & packages
  user create -u NAME [-e email] [-p password] [--role user|reseller|admin]
  user list
  user delete NAME
  user reset-pass NAME [-p password]        (also revokes sessions)
  user suspend NAME | user unsuspend NAME
  package list | package create -n NAME --domains N --databases N

Domains, DNS, SSL
  domain add USER DOMAIN [--php 8.3]
  domain list [USER]
  domain delete DOMAIN
  dns sync DOMAIN
  ssl issue DOMAIN [--challenge http|dns]

FTP & databases
  ftp add USER USERNAME [--password x]
  db create USER mariadb|postgres NAME
  db list [USER]
  db delete SERVER NAME

Backups
  backup create [--user NAME]               (full backup by default)
  backup list
  backup restore FILE
`)
}

// -- context & wiring ---------------------------------------------------------

func wire() (*config.Config, *store.Store, *serviceSet, error) {
	cfg, err := config.Load(flagConfig())
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, nil, err
	}
	if err := cfg.EnsureSecret(); err != nil {
		return nil, nil, nil, err
	}
	st, err := store.New(cfg.DBPath)
	if err != nil {
		return nil, nil, nil, err
	}
	cipher, err := svc.NewCipher(cfg.JWTSecret)
	if err != nil {
		return nil, nil, nil, err
	}
	am, err := auth.New(cfg.JWTSecret, st, time.Hour)
	if err != nil {
		return nil, nil, nil, err
	}
	php := svc.NewPHP(cfg)
	web := svc.NewWebServer(cfg, php)
	dnsSvc := svc.NewDNS(cfg, st, cipher)
	ftpSvc := svc.NewFTP(cfg, st)
	dbSvc := svc.NewDatabases(cfg, st)
	ss := &serviceSet{
		cfg: cfg, store: st, cipher: cipher, am: am,
		php: php, web: web,
		domains: svc.NewDomains(cfg, st, web, php, dnsSvc, ftpSvc),
		dns:     dnsSvc,
		ssl:     svc.NewSSL(cfg, st, web, dnsSvc),
		ftp:     ftpSvc,
		db:      dbSvc,
		backup:  svc.NewBackup(cfg, st, dbSvc, ftpSvc, web, php, am, cipher),
		tuner:   svc.NewTuner(cfg),
		sys:     svc.NewSystem(cfg),
	}
	return cfg, st, ss, nil
}

func flagConfig() string {
	if v := os.Getenv("AEGIS_CONFIG"); v != "" {
		return v
	}
	return ""
}

type serviceSet struct {
	cfg     *config.Config
	store   *store.Store
	cipher  *svc.Cipher
	am      *auth.Manager
	php     *svc.PHP
	web     *svc.WebServer
	domains *svc.Domains
	dns     *svc.DNS
	ssl     *svc.SSL
	ftp     *svc.FTP
	db      *svc.Databases
	backup  *svc.Backup
	tuner   *svc.Tuner
	sys     *svc.System
}

// -- dispatch ----------------------------------------------------------------

func dispatch(cmd string, args []string) error {
	ctx := context.Background()
	switch cmd {
	case "setup":
		return cmdSetup(ctx)
	case "status":
		return cmdStatus(ctx)
	case "tune":
		return cmdTune(ctx, args)
	case "version":
		fmt.Println("aegisctl 0.1.0")
		return nil
	case "user":
		return cmdUser(ctx, args)
	case "package":
		return cmdPackage(ctx, args)
	case "domain":
		return cmdDomain(ctx, args)
	case "dns":
		return cmdDNS(ctx, args)
	case "ssl":
		return cmdSSL(ctx, args)
	case "ftp":
		return cmdFTP(ctx, args)
	case "db":
		return cmdDB(ctx, args)
	case "backup":
		return cmdBackup(ctx, args)
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func cmdSetup(ctx context.Context) error {
	cfg, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	// Store dirs + secret already ensured by wire; run tuning.
	report, err := ss.tuner.Tune()
	if err != nil {
		return err
	}
	fmt.Printf("tuned: %d cores / %d MB RAM -> php-fpm max_children=%d\n", report.Cores, report.RAMMB, report.PHPFPM.MaxChildren)
	fmt.Printf("config:  %s\n", cfg.ConfigPath)
	fmt.Printf("db:      %s\n", cfg.DBPath)
	n, _ := st.CountUsers(ctx, 0)
	fmt.Printf("accounts: %d\n", n)
	if n == 0 {
		fmt.Println("no accounts exist — start the panel with AEGIS_ADMIN_USER/AEGIS_ADMIN_PASSWORD, or create one:")
		fmt.Println("  AEGIS_ADMIN_PASSWORD=secret aegis server   (or)   aegisctl user create -u admin -p secret --role admin")
	}
	return nil
}

func cmdStatus(ctx context.Context) error {
	cfg, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	ov := ss.sys.Overview()
	fmt.Printf("host:     %s (%s %s)\n", ov.Hostname, ov.OS, ov.Kernel)
	fmt.Printf("uptime:   %ds | load %.2f | %d cores\n", ov.UptimeSecs, ov.Load[0], ov.CPUCores)
	fmt.Printf("memory:   %d MB used / %d MB total\n", ov.MemUsed>>20, ov.MemTotal>>20)
	for _, d := range ov.Disks {
		fmt.Printf("disk[%s]: %d MB used / %d MB total\n", d.Mount, d.Used>>20, d.Total>>20)
	}
	for _, s := range ov.Services {
		fmt.Printf("service %-12s %s\n", s.Name, s.Status)
	}
	users, _ := st.ListUsers(ctx, 0)
	fmt.Printf("accounts: %d | panel db: %s\n", len(users), cfg.DBPath)
	return nil
}

func cmdTune(ctx context.Context, args []string) error {
	_, _, ss, err := wire()
	if err != nil {
		return err
	}
	apply := flagSet(args, "apply")
	if apply {
		report, err := ss.tuner.Tune()
		if err != nil {
			return err
		}
		return ss.tuner.ApplySysctl(report)
	}
	report, err := ss.tuner.Tune()
	if err != nil {
		return err
	}
	fmt.Printf("report written to %s\n", ss.tuner.ReportPath())
	fmt.Printf("cores=%d ram=%dMB php_fpm_max_children=%d\n", report.Cores, report.RAMMB, report.PHPFPM.MaxChildren)
	return nil
}

// -- users -------------------------------------------------------------------

func cmdUser(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("user: expected create|list|delete|reset-pass|suspend|unsuspend")
	}
	_, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		fs := flag.NewFlagSet("user create", flag.ExitOnError)
		u := fs.String("u", "", "username")
		e := fs.String("e", "", "email")
		p := fs.String("p", "", "password (default: random, printed once)")
		role := fs.String("role", "user", "role: user|reseller|admin")
		pkg := fs.String("package", "", "package name")
		fs.Parse(rest)
		if *u == "" {
			return fmt.Errorf("user create: -u required")
		}
		if !svc.ValidUsername(*u) {
			return fmt.Errorf("invalid username")
		}
		password := *p
		if password == "" {
			password, err = svc.RandomPassword()
			if err != nil {
				return err
			}
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		pkgID := int64(0)
		if *pkg != "" {
			pkgs, _ := st.ListPackages(ctx)
			for _, x := range pkgs {
				if x.Name == *pkg {
					pkgID = x.ID
				}
			}
			if pkgID == 0 {
				return fmt.Errorf("package %q not found", *pkg)
			}
		} else if def, err := st.GetDefaultPackage(ctx); err == nil {
			pkgID = def.ID
		}
		user := &store.User{
			Username: *u, Email: *e, PasswordHash: hash, Role: *role,
			PackageID: pkgID, Status: store.StatusActive,
			HomeDir: ss.cfg.HomeRoot + "/" + *u,
		}
		if _, err := svc.RunTimeout(15*time.Second, "useradd", "-m", "-d", user.HomeDir, "-s", "/sbin/nologin", "-g", "www-data", *u); err != nil {
			return fmt.Errorf("system account: %w", err)
		}
		if err := svc.SetSystemPassword(*u, password); err != nil {
			return err
		}
		if err := st.CreateUser(ctx, user); err != nil {
			return err
		}
		if _, err := ss.ftp.Create(ctx, user, *u, password, false); err != nil {
			fmt.Fprintln(os.Stderr, "warning: ftp primary account:", err)
		}
		fmt.Printf("created %s (role=%s)\n", user.Username, user.Role)
		if *p == "" {
			fmt.Printf("password: %s\n", password)
		}
		return nil
	case "list":
		users, err := st.ListUsers(ctx, 0)
		if err != nil {
			return err
		}
		fmt.Printf("%-24s %-10s %-10s %s\n", "USERNAME", "ROLE", "STATUS", "CREATED")
		for _, u := range users {
			fmt.Printf("%-24s %-10s %-10s %s\n", u.Username, u.Role, u.Status, u.CreatedAt.Format("2006-01-02"))
		}
		return nil
	case "delete":
		if len(rest) == 0 {
			return fmt.Errorf("user delete: NAME required")
		}
		u, err := mustUser(ctx, st, rest[0])
		if err != nil {
			return err
		}
		return st.DeleteUser(ctx, u.ID)
	case "reset-pass":
		fs := flag.NewFlagSet("reset-pass", flag.ExitOnError)
		p := fs.String("p", "", "password")
		fs.Parse(rest)
		name := fs.Arg(0)
		if name == "" {
			return fmt.Errorf("user reset-pass: NAME required")
		}
		u, err := mustUser(ctx, st, name)
		if err != nil {
			return err
		}
		password := *p
		if password == "" {
			password, err = svc.RandomPassword()
			if err != nil {
				return err
			}
		}
		hash, err := auth.HashPassword(password)
		if err != nil {
			return err
		}
		if err := st.SetUserPassword(ctx, u.ID, hash); err != nil {
			return err
		}
		_ = st.DeleteUserSessions(ctx, u.ID)
		_ = svc.SetSystemPassword(u.Username, password)
		fmt.Printf("password reset for %s\n", u.Username)
		if *p == "" {
			fmt.Printf("password: %s\n", password)
		}
		return nil
	case "suspend", "unsuspend":
		if len(rest) == 0 {
			return fmt.Errorf("user %s: NAME required", sub)
		}
		u, err := mustUser(ctx, st, rest[0])
		if err != nil {
			return err
		}
		status := store.StatusSuspended
		if sub == "unsuspend" {
			status = store.StatusActive
		}
		if err := st.SetUserStatus(ctx, u.ID, status); err != nil {
			return err
		}
		if status == store.StatusSuspended {
			_ = st.DeleteUserSessions(ctx, u.ID)
			_, _ = svc.RunTimeout(10*time.Second, "usermod", "-L", u.Username)
		} else {
			_, _ = svc.RunTimeout(10*time.Second, "usermod", "-U", u.Username)
		}
		fmt.Printf("%s %s\n", u.Username, status)
		return nil
	default:
		return fmt.Errorf("user: unknown subcommand %q", sub)
	}
}

func cmdPackage(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("package: expected list|create")
	}
	_, st, _, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "list":
		pkgs, err := st.ListPackages(ctx)
		if err != nil {
			return err
		}
		for _, p := range pkgs {
			flag := ""
			if p.IsDefault {
				flag = " [default]"
			}
			fmt.Printf("%-16s domains=%d dbs=%d disk=%dMB%s\n", p.Name, p.MaxDomains, p.MaxDatabases, p.DiskQuotaBytes>>20, flag)
		}
		return nil
	case "create":
		fs := flag.NewFlagSet("package create", flag.ExitOnError)
		n := fs.String("n", "", "name")
		domains := fs.Int("domains", 1, "max domains")
		databases := fs.Int("databases", 1, "max databases")
		ftpAcc := fs.Int("ftp", 1, "max ftp accounts")
		disk := fs.Int64("disk-mb", 0, "disk quota MB (0 = unlimited)")
		def := fs.Bool("default", false, "set as default")
		fs.Parse(args[1:])
		if *n == "" {
			return fmt.Errorf("package create: -n required")
		}
		p := &store.Package{Name: *n, MaxDomains: *domains, MaxDatabases: *databases, MaxFTPAccounts: *ftpAcc,
			DiskQuotaBytes: *disk << 20, AllowSSL: true, AllowDNS: true, AllowTerminal: true, AllowBackups: true,
			AllowMail: true, AllowWebmail: true, AllowDatabases: true, AllowFiles: true, AllowFTP: true,
			AllowCron: true, IsDefault: *def}
		return st.CreatePackage(ctx, p)
	default:
		return fmt.Errorf("package: unknown subcommand")
	}
}

// -- domains / dns / ssl -------------------------------------------------------

func cmdDomain(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("domain: expected add|list|delete")
	}
	_, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("domain add", flag.ExitOnError)
		php := fs.String("php", "", "php version")
		fs.Parse(args[1:])
		if fs.NArg() < 2 {
			return fmt.Errorf("domain add USER DOMAIN")
		}
		u, err := mustUser(ctx, st, fs.Arg(0))
		if err != nil {
			return err
		}
		dom, ftp, err := ss.domains.Create(ctx, u, fs.Arg(1), svc.CreateOptions{PHPVersion: *php})
		if err != nil {
			return err
		}
		fmt.Printf("domain %s provisioned for %s (docroot %s)\n", dom.Domain, u.Username, dom.DocumentRoot)
		if ftp != nil {
			fmt.Printf("ftp account %s created (password shown once): %s\n", ftp.Account.Username, ftp.Password)
		}
		return nil
	case "list":
		fs := flag.NewFlagSet("domain list", flag.ExitOnError)
		fs.Parse(args[1:])
		filter := int64(0)
		if fs.NArg() > 0 {
			u, err := mustUser(ctx, st, fs.Arg(0))
			if err != nil {
				return err
			}
			filter = u.ID
		}
		domains, err := st.ListDomains(ctx, filter)
		if err != nil {
			return err
		}
		for _, d := range domains {
			fmt.Printf("%-40s user=%d php=%-6s ssl=%v\n", d.Domain, d.UserID, d.PHPVersion, d.SSLEnabled)
		}
		return nil
	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("domain delete DOMAIN")
		}
		d, err := st.GetDomainByName(ctx, args[1])
		if err != nil {
			return err
		}
		return ss.domains.Delete(ctx, d.ID)
	default:
		return fmt.Errorf("domain: unknown subcommand")
	}
}

func cmdDNS(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("dns sync DOMAIN")
	}
	_, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	dom, err := st.GetDomainByName(ctx, args[1])
	if err != nil {
		return err
	}
	zone, err := st.GetZoneByDomain(ctx, dom.ID)
	if err != nil {
		return fmt.Errorf("domain %s has no dns zone (create one in the UI)", dom.Domain)
	}
	res, err := ss.dns.Sync(ctx, zone.ID)
	if err != nil {
		return err
	}
	fmt.Printf("synced %s via %s: %d records\n", res.Domain, res.Provider, res.Pushed)
	return nil
}

func cmdSSL(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("ssl issue DOMAIN [--challenge http|dns]")
	}
	_, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	dom, err := st.GetDomainByName(ctx, args[1])
	if err != nil {
		return err
	}
	challenge := "http"
	if len(args) > 2 && args[2] == "--challenge" && len(args) > 3 {
		challenge = args[3]
	}
	order, err := ss.ssl.Issue(ctx, dom.ID, challenge)
	if err != nil {
		return err
	}
	fmt.Printf("ssl order for %s: %s (cert %s)\n", dom.Domain, order.Status, order.CertPath)
	return nil
}

// -- ftp / databases ------------------------------------------------------------

func cmdFTP(ctx context.Context, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("ftp add USER USERNAME [--password x]")
	}
	_, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := mustUser(ctx, st, args[1])
	if err != nil {
		return err
	}
	password := ""
	if len(args) > 3 && args[3] == "--password" && len(args) > 4 {
		password = args[4]
	}
	if password == "" {
		password, err = svc.RandomPassword()
		if err != nil {
			return err
		}
	}
	acct, err := ss.ftp.Create(ctx, u, args[2], password, true)
	if err != nil {
		return err
	}
	fmt.Printf("ftp account %s created (home %s)\n", acct.Username, acct.HomeDir)
	return nil
}

func cmdDB(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("db: expected create|list|delete")
	}
	_, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "create":
		if len(args) < 4 {
			return fmt.Errorf("db create USER mariadb|postgres NAME")
		}
		u, err := mustUser(ctx, st, args[1])
		if err != nil {
			return err
		}
		row, err := ss.db.Create(ctx, u, args[2], args[3])
		if err != nil {
			return err
		}
		fmt.Printf("database %s created on %s\n", row.Name, row.Server)
		fmt.Printf("user %s password %s\n", row.DBUser, row.DBPassword)
		return nil
	case "list":
		fs := flag.NewFlagSet("db list", flag.ExitOnError)
		fs.Parse(args[1:])
		filter := int64(0)
		if fs.NArg() > 0 {
			u, err := mustUser(ctx, st, fs.Arg(0))
			if err != nil {
				return err
			}
			filter = u.ID
		}
		rows, err := st.ListDatabases(ctx, filter)
		if err != nil {
			return err
		}
		for _, r := range rows {
			fmt.Printf("%-8s %-32s user=%s\n", r.Server, r.Name, r.DBUser)
		}
		return nil
	case "delete":
		if len(args) < 4 {
			return fmt.Errorf("db delete SERVER NAME")
		}
		rows, err := st.ListDatabases(ctx, 0)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Server == args[1] && r.Name == args[2] {
				return ss.db.Drop(ctx, r)
			}
		}
		return fmt.Errorf("database not found")
	default:
		return fmt.Errorf("db: unknown subcommand")
	}
}

// -- backups --------------------------------------------------------------------

func cmdBackup(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("backup: expected create|list|restore")
	}
	_, st, ss, err := wire()
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("backup create", flag.ExitOnError)
		userName := fs.String("user", "", "back up a single user")
		fs.Parse(args[1:])
		scope, uid := "full", int64(0)
		if *userName != "" {
			u, err := mustUser(ctx, st, *userName)
			if err != nil {
				return err
			}
			scope, uid = "user", u.ID
		}
		info, err := ss.backup.Create(ctx, scope, uid)
		if err != nil {
			return err
		}
		fmt.Printf("backup written: %s (%d bytes)\n", info.Path, info.Size)
		return nil
	case "list":
		items, err := ss.backup.List()
		if err != nil {
			return err
		}
		for _, b := range items {
			fmt.Printf("%-48s %d bytes  %s\n", b.Name, b.Size, b.CreatedAt.Format("2006-01-02 15:04"))
		}
		return nil
	case "restore":
		if len(args) < 2 {
			return fmt.Errorf("backup restore FILE")
		}
		if err := ss.backup.Restore(ctx, args[1]); err != nil {
			return err
		}
		fmt.Println("restore complete")
		return nil
	default:
		return fmt.Errorf("backup: unknown subcommand")
	}
}

// -- helpers -----------------------------------------------------------------------

func mustUser(ctx context.Context, st *store.Store, name string) (*store.User, error) {
	u, err := st.GetUserByUsername(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("user %q not found", name)
	}
	return u, nil
}

func flagSet(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == "--"+n || a == "-"+n {
				return true
			}
		}
	}
	return false
}

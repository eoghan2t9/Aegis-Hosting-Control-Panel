// Command aegis runs the Aegis hosting control panel server.
//
// Responsibilities:
//   - load config and initialise the data dirs + secret + database
//   - first-run bootstrap (admin account, auto performance tuning)
//   - serve the REST API + embedded frontend on :8080
//   - when the native Go web server is active, serve customer sites on
//     :80/:443 with SNI certificates
//   - run the built-in authoritative DNS server when configured
//   - background SSL auto-renewal
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"aegis/internal/api"
	"aegis/internal/auth"
	"aegis/internal/config"
	"aegis/internal/store"
	"aegis/internal/svc"
	"aegis/web"
)

func main() {
	var configPath string
	var dev bool
	flag.StringVar(&configPath, "config", "", "path to config file")
	flag.BoolVar(&dev, "dev", false, "development mode: log at debug level")
	flag.Parse()

	level := slog.LevelInfo
	if dev || os.Getenv("AEGIS_DEBUG") == "1" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	if err := run(configPath); err != nil {
		slog.Error("aegis exited with error", "err", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return err
	}
	if err := cfg.EnsureSecret(); err != nil {
		return err
	}
	st, err := store.New(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	am, err := auth.New(cfg.JWTSecret, st, time.Duration(cfg.SessionTTLHours)*time.Hour)
	if err != nil {
		return err
	}
	cipher, err := svc.NewCipher(cfg.JWTSecret)
	if err != nil {
		return err
	}

	// Services.
	sys := svc.NewSystem(cfg)
	tuner := svc.NewTuner(cfg)
	php := svc.NewPHP(cfg)
	webSvc := svc.NewWebServer(cfg, php)
	domains := svc.NewDomains(cfg, st, webSvc, php)
	dnsSvc := svc.NewDNS(cfg, st, cipher)
	sslSvc := svc.NewSSL(cfg, st, webSvc, dnsSvc)
	ftpSvc := svc.NewFTP(cfg, st)
	dbSvc := svc.NewDatabases(cfg, st)
	files := svc.NewFiles(cfg)
	backupSvc := svc.NewBackup(cfg, st, dbSvc, ftpSvc, webSvc, php, am, cipher)
	term := svc.NewTerminal(cfg)
	cronSvc := svc.NewCron(cfg, st)
	mailSvc := svc.NewMail(cfg, st, dnsSvc)
	tokensSvc := svc.NewAPITokens(st)
	securitySvc := svc.NewSecurity(cfg, st)
	quotaSvc := svc.NewQuota(cfg, st)
	webAppsSvc := svc.NewWebApps(cfg, dbSvc)

	// First-run bootstrap.
	if err := bootstrap(cfg, st, tuner, webSvc); err != nil {
		slog.Warn("bootstrap incomplete", "err", err)
	}

	server := api.New(cfg, st, am, domains, webSvc, php, dnsSvc, sslSvc,
		ftpSvc, dbSvc, files, backupSvc, sys, tuner, term, cipher, cronSvc, mailSvc, tokensSvc, securitySvc, quotaSvc, webAppsSvc)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	// Panel API + frontend.
	panelAddr := cfg.ListenAddr
	go func() {
		slog.Info("panel listening", "addr", panelAddr)
		if err := http.ListenAndServe(panelAddr, panelHandler(server)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("panel server: %w", err)
		}
	}()

	// Native Go web server for customer sites.
	if cfg.WebServer.Server == "go" {
		startGoSiteServers(ctx, webSvc, cfg, errCh)
	}

	// Built-in DNS server.
	if cfg.DNS.ListenAddr != "" {
		dnsServer := svc.NewDNSServer(st, cfg.DNS.Nameservers, cfg.DNS.AdminEmail)
		if derrCh, err := dnsServer.Start(cfg.DNS.ListenAddr); err != nil {
			slog.Warn("dns server failed to start", "err", err)
		} else {
			go func() {
				select {
				case e := <-derrCh:
					slog.Error("dns server error", "err", e)
				case <-ctx.Done():
					_ = dnsServer.Stop()
				}
			}()
		}
	}

	// SSL auto-renew.
	go sslSvc.AutoRenew(ctx)
	go backupSvc.AutoBackup(ctx)
	go quotaSvc.EnforceLoop(ctx)

	select {
	case <-ctx.Done():
		slog.Info("shutting down")
		return nil
	case err := <-errCh:
		return err
	}
}

// bootstrap handles first-run: creates the admin account (from env) and runs
// the automatic performance tuning once.
func bootstrap(cfg *config.Config, st *store.Store, tuner *svc.Tuner, webSvc *svc.WebServer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Tuning (requirement: auto-tune during initial setup).
	if _, err := tuner.LoadReport(); err != nil {
		slog.Info("first run: running automatic performance tuning")
		report, err := tuner.Tune()
		if err != nil {
			return fmt.Errorf("auto-tune: %w", err)
		}
		slog.Info("tuning complete",
			"cores", report.Cores, "ram_mb", report.RAMMB,
			"php_max_children", report.PHPFPM.MaxChildren,
			"report", tuner.ReportPath())
		if os.Getenv("AEGIS_AUTO_TUNE_SYSCTL") == "1" {
			if err := tuner.ApplySysctl(report); err != nil {
				slog.Warn("sysctl apply failed (continue without it)", "err", err)
			}
		}
	}

	// Admin account.
	n, err := st.CountUsers(ctx, 0)
	if err != nil {
		return err
	}
	if n == 0 {
		username := envOr("AEGIS_ADMIN_USER", "admin")
		password := os.Getenv("AEGIS_ADMIN_PASSWORD")
		if password == "" {
			return errors.New("no accounts exist and AEGIS_ADMIN_PASSWORD is not set — run: aegisctl user create --admin <username>")
		}
		if !svc.ValidUsername(username) {
			return errors.New("invalid admin username from AEGIS_ADMIN_USER")
		}
		if err := createAdmin(ctx, st, cfg.HomeRoot, username, password); err != nil {
			return err
		}
		slog.Info("created admin account", "username", username)
	}
	return nil
}

// createAdmin creates the first admin account plus system user + home.
func createAdmin(ctx context.Context, st *store.Store, homeRoot, username, password string) error {
	pkg, err := st.GetDefaultPackage(ctx)
	if err != nil {
		return err
	}
	pkgID := int64(0)
	if pkg != nil {
		pkgID = pkg.ID
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	home := path.Join(homeRoot, username)
	u := &store.User{
		Username: username, Email: "admin@localhost", PasswordHash: hash,
		Role: store.RoleAdmin, PackageID: pkgID, Status: store.StatusActive, HomeDir: home,
	}
	if err := st.CreateUser(ctx, u); err != nil {
		return err
	}
	_, _ = svc.RunTimeout(15*time.Second, "useradd", "-m", "-d", home, "-s", "/bin/bash", "-g", "www-data", username)
	_ = svc.SetSystemPassword(username, password)
	return nil
}

// panelHandler serves the API and the frontend. In dev (AEGIS_DEV_WEB set)
// assets are read from disk so frontend edits appear without a rebuild.
func panelHandler(server *api.Server) http.Handler {
	apiHandler := server.Handler()
	var assets fs.FS
	if devRoot := os.Getenv("AEGIS_DEV_WEB"); devRoot != "" {
		if info, err := os.Stat(devRoot); err == nil && info.IsDir() {
			assets = os.DirFS(devRoot)
			slog.Info("serving frontend from disk (dev)", "dir", devRoot)
		}
	}
	if assets == nil {
		sub, err := fs.Sub(web.FS, ".")
		if err != nil {
			panic(err)
		}
		assets = sub
	}
	fileHandler := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		// SPA: serve index.html for client-side routes.
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if _, err := fs.Stat(assets, p); err != nil && !strings.HasPrefix(p, "css/") && !strings.HasPrefix(p, "js/") {
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/"
				fileHandler.ServeHTTP(w, r2)
				return
			}
		}
		fileHandler.ServeHTTP(w, r)
	})
}

// startGoSiteServers runs the native Go web server on :80 and :443.
func startGoSiteServers(ctx context.Context, webSvc *svc.WebServer, cfg *config.Config, errCh chan<- error) {
	httpAddr := envOr("AEGIS_GO_HTTP", ":80")
	httpsAddr := envOr("AEGIS_GO_HTTPS", ":443")

	httpSrv := &http.Server{Addr: httpAddr, Handler: webSvc.GoHandler()}
	go func() {
		slog.Info("go web server listening (http)", "addr", httpAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("go http server: %w", err)
		}
	}()

	httpsSrv := &http.Server{
		Addr:    httpsAddr,
		Handler: webSvc.GoHandler(),
		TLSConfig: &tls.Config{
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				return webSvc.GoTLSCert(hello.ServerName)
			},
			MinVersion: tls.VersionTLS12,
		},
	}
	go func() {
		// Only start TLS when at least one route has a certificate.
		for {
			time.Sleep(500 * time.Millisecond)
			if len(webSvc.GoRoutes()) > 0 {
				break
			}
		}
		slog.Info("go web server listening (https, sni)", "addr", httpsAddr)
		if err := httpsSrv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("go https server: %w", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		_ = httpsSrv.Shutdown(shutdownCtx)
	}()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

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
	"encoding/json"
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
	dnsSvc := svc.NewDNS(cfg, st, cipher)
	domains := svc.NewDomains(cfg, st, webSvc, php, dnsSvc)
	sslSvc := svc.NewSSL(cfg, st, webSvc, dnsSvc)
	ftpSvc := svc.NewFTP(cfg, st)
	dbSvc := svc.NewDatabases(cfg, st)
	files := svc.NewFiles(cfg)
	dockerSvc := svc.NewDocker(cfg, st, files, domains)
	thumbsSvc := svc.NewThumbs(cfg, files)
	backupSvc := svc.NewBackup(cfg, st, dbSvc, ftpSvc, webSvc, php, am, cipher)
	term := svc.NewTerminal(cfg)
	cronSvc := svc.NewCron(cfg, st)
	mailSvc := svc.NewMail(cfg, st, dnsSvc)
	tokensSvc := svc.NewAPITokens(st)
	securitySvc := svc.NewSecurity(cfg, st)
	quotaSvc := svc.NewQuota(cfg, st)
	webAppsSvc := svc.NewWebApps(cfg, dbSvc)
	packagesSvc := svc.NewPackages(st)
	metricsHist := svc.NewMetricsHistory(sys)
	ipsSvc := svc.NewIPs(st, domains)

	// First-run bootstrap.
	if err := bootstrap(cfg, st, tuner, webSvc); err != nil {
		slog.Warn("bootstrap incomplete", "err", err)
	}

	server := api.New(cfg, st, am, domains, webSvc, php, dnsSvc, sslSvc,
		ftpSvc, dbSvc, files, thumbsSvc, backupSvc, sys, tuner, term, cipher, cronSvc, mailSvc, tokensSvc, securitySvc, quotaSvc, webAppsSvc, packagesSvc, metricsHist, dockerSvc, ipsSvc)
	server.PanelAssets = panelHandler(server, nil)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)

	// Panel API + frontend. On the dedicated listener, any request outside
	// PanelBase is redirected to the panel path so a bare http://host:8080
	// visit lands on http://host:8080/aegis/.
	panelAddr := cfg.ListenAddr
	panelRoot := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := cfg.PanelBase + "/"
		if cfg.PanelBase == "" {
			target = "/"
		}
		http.Redirect(w, r, target, http.StatusFound)
	})
	go func() {
		slog.Info("panel listening", "addr", panelAddr, "base", cfg.PanelBase)
		if err := http.ListenAndServe(panelAddr, panelHandler(server, panelRoot)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("panel server: %w", err)
		}
	}()

	// Native Go web server for customer sites. Its listener also serves the
	// panel under PanelBase so http://<ip>/aegis works without DNS or a
	// dedicated panel port. StartGo/StopGo (called again later from
	// handleWebServerSet on a live switch) are idempotent and safe to call
	// here unconditionally.
	if cfg.WebServer.Server == "go" {
		if err := webSvc.StartGo(server.PanelAssets, errCh); err != nil {
			return fmt.Errorf("start native web server: %w", err)
		}
		// goRoutes lives only in memory (webserver.go), so every domain
		// created while "go" was active still needs to be re-registered
		// after a restart — otherwise it 404s by Host header even though
		// its docroot/preview path (which reads the DB directly) is fine.
		// A failure here is fatal rather than a warning: silently
		// continuing with zero routes would reintroduce that exact bug for
		// the rest of this run instead of just failing loudly at boot.
		allDomains, err := st.ListDomains(context.Background(), 0)
		if err != nil {
			return fmt.Errorf("list domains for go server route rebuild: %w", err)
		}
		for _, dom := range allDomains {
			if err := domains.Apply(context.Background(), dom.ID); err != nil {
				slog.Warn("failed to register domain with native web server", "domain", dom.Domain, "err", err)
			}
		}
	}
	go func() {
		<-ctx.Done()
		webSvc.StopGo()
	}()

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
	go packagesSvc.CheckUpdatesLoop(ctx)
	go metricsHist.SamplerLoop(ctx)
	// One-off check shortly after boot so the update badge isn't empty for
	// up to 6h waiting on CheckUpdatesLoop's first tick.
	go func() {
		time.Sleep(10 * time.Second)
		if _, err := packagesSvc.CheckUpdates(ctx); err != nil {
			slog.Warn("initial package update check failed", "err", err)
		}
	}()

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
// assets are read from disk so frontend edits appear without a rebuild.
//
// With PanelBase set (default "/aegis") the panel lives at that path prefix:
// requests under it have the prefix stripped before routing; requests outside
// it are handed to `outside` — a redirect-to-panel handler on the dedicated
// listener, or the site handler when mounted on a customer vhost via the
// web-server proxy blocks.
func panelHandler(server *api.Server, outside http.Handler) http.Handler {
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

	// index.html with the panel base injected so the frontend can build
	// base-aware absolute URLs (/api/..., WebSocket endpoints, download links).
	base := server.Cfg.PanelBase
	var indexHTML []byte
	if data, err := fs.ReadFile(assets, "index.html"); err == nil {
		idx := string(data)
		if base != "" {
			b, _ := json.Marshal(base)
			idx = strings.Replace(idx, "<head>",
				"<head>\n<script>window.AEGIS_PANEL_BASE="+string(b)+"</script>", 1)
		}
		indexHTML = []byte(idx)
	}

	serveIndex := func(w http.ResponseWriter) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexHTML)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if base != "" {
			prefix := base + "/"
			if r.URL.Path == base {
				u := *r.URL
				u.Path = prefix
				http.Redirect(w, r, u.RequestURI(), http.StatusPermanentRedirect)
				return
			}
			if !strings.HasPrefix(r.URL.Path, prefix) {
				if outside != nil {
					outside.ServeHTTP(w, r)
				} else {
					http.NotFound(w, r)
				}
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, base)
			r = r2
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		// SPA: serve index.html for client-side routes.
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		isAsset := strings.HasPrefix(p, "css/") || strings.HasPrefix(p, "js/")
		// Frontend files have no cache-busting query string, so a plain
		// no-cache (revalidate-before-use) isn't reliable enough — some
		// mobile browsers keep serving a stale copy anyway. no-store forbids
		// caching outright: every load hits the server for these small
		// files, so a deploy is visible immediately.
		if isAsset || p == "" || p == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
		}
		if indexHTML != nil && (p == "" || p == "index.html") {
			serveIndex(w)
			return
		}
		if p != "" {
			if _, err := fs.Stat(assets, p); err != nil && !isAsset {
				// Unknown path: SPA fallback to the injected index.
				if indexHTML != nil {
					serveIndex(w)
					return
				}
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/"
				fileHandler.ServeHTTP(w, r2)
				return
			}
		}
		fileHandler.ServeHTTP(w, r)
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

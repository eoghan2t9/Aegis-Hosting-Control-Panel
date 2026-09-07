package svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Backup creates and restores account backups: home directories, database
// dumps, FTP accounts (with credentials), domains, DNS records and package
// membership, all in a single tar.gz with a JSON manifest.
type Backup struct {
	Cfg    *config.Config
	Store  *store.Store
	DB     *Databases
	FTP    *FTP
	Web    *WebServer
	PHP    *PHP
	Auth   PasswordSetter
	Cipher *Cipher
}

// PasswordSetter is implemented by the auth manager; decoupled so backups can
// recreate user accounts with fresh hashes.
type PasswordSetter interface {
	HashPassword(password string) (string, error)
}

// Manifest is the archive's table of contents.
type Manifest struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Scope       string         `json:"scope"` // full | user
	Users       []ManifestUser `json:"users"`
}

type ManifestUser struct {
	Username   string                        `json:"username"`
	Email      string                        `json:"email"`
	Role       string                        `json:"role"`
	Status     string                        `json:"status"`
	HomeDir    string                        `json:"home_dir"`
	Domains    []*store.Domain               `json:"domains"`
	Aliases    map[string][]string           `json:"aliases,omitempty"`
	DNSRecords map[string][]*store.DNSRecord `json:"dns_records,omitempty"`
	FTP        []*store.FTPAccount           `json:"ftp,omitempty"`
	Databases  []*store.Database             `json:"databases,omitempty"`
}

// BackupInfo is a listing row for the backups screen.
type BackupInfo struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

func NewBackup(cfg *config.Config, st *store.Store, db *Databases, ftp *FTP, web *WebServer, php *PHP, auth PasswordSetter, cipher *Cipher) *Backup {
	return &Backup{Cfg: cfg, Store: st, DB: db, FTP: ftp, Web: web, PHP: php, Auth: auth, Cipher: cipher}
}

func (b *Backup) stamp() string { return time.Now().UTC().Format("20060102-150405") }

// Create makes a backup of all users (scope=full) or one user (scope=user).
func (b *Backup) Create(ctx context.Context, scope string, userID int64) (*BackupInfo, error) {
	if err := os.MkdirAll(b.Cfg.BackupDir, 0o700); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(b.Cfg.BackupDir, "staging-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	m := Manifest{GeneratedAt: time.Now().UTC(), Scope: scope}

	var users []*store.User
	if scope == "user" && userID > 0 {
		u, err := b.Store.GetUserByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		users = []*store.User{u}
	} else {
		users, err = b.Store.ListUsers(ctx, 0)
		if err != nil {
			return nil, err
		}
	}

	for _, u := range users {
		mu, err := b.collectUser(ctx, staging, u)
		if err != nil {
			return nil, fmt.Errorf("backup %s: %w", u.Username, err)
		}
		m.Users = append(m.Users, *mu)
	}

	manifestData, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), manifestData, 0o600); err != nil {
		return nil, err
	}

	name := fmt.Sprintf("aegis-%s-%s.tar.gz", scope, b.stamp())
	outPath := filepath.Join(b.Cfg.BackupDir, name)
	if _, err := RunTimeout(15*time.Minute, "tar", "czf", outPath, "-C", staging, "."); err != nil {
		return nil, fmt.Errorf("tar: %w", err)
	}
	_ = os.Chmod(outPath, 0o600)
	info, _ := os.Stat(outPath)
	return &BackupInfo{Path: outPath, Name: name, Size: info.Size(), CreatedAt: m.GeneratedAt}, nil
}

// collectUser gathers one user's data and stages their files + dumps inside
// the staging dir so everything lands in the final archive.
func (b *Backup) collectUser(ctx context.Context, staging string, u *store.User) (*ManifestUser, error) {
	mu := &ManifestUser{
		Username: u.Username, Email: u.Email, Role: u.Role, Status: u.Status, HomeDir: u.HomeDir,
		Aliases:    map[string][]string{},
		DNSRecords: map[string][]*store.DNSRecord{},
	}
	// Home directory.
	home := u.HomeDir
	if home == "" {
		home = filepath.Join(b.Cfg.HomeRoot, u.Username)
	}
	if _, err := os.Stat(home); err == nil {
		userStage := filepath.Join(staging, "users", u.Username)
		_ = os.MkdirAll(userStage, 0o755)
		if _, err := RunTimeout(15*time.Minute, "tar", "czf", filepath.Join(userStage, "home.tar.gz"), "-C", filepath.Dir(home), filepath.Base(home)); err != nil {
			return nil, fmt.Errorf("home tar: %w", err)
		}
	}

	// Domains + aliases + dns records.
	domains, err := b.Store.ListDomains(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	for _, d := range domains {
		mu.Domains = append(mu.Domains, d)
		aliases, _ := b.Store.ListAliases(ctx, d.ID)
		mu.Aliases[d.Domain] = aliases
		zone, err := b.Store.GetZoneByDomain(ctx, d.ID)
		if err == nil && zone != nil {
			recs, _ := b.Store.ListRecords(ctx, zone.ID)
			mu.DNSRecords[d.Domain] = recs
		}
	}

	// FTP accounts.
	ftpAccounts, err := b.Store.ListFTPAccounts(ctx, u.ID)
	if err == nil {
		mu.FTP = ftpAccounts
	}

	// Databases: stage dumps into the staging dir.
	databases, err := b.Store.ListDatabases(ctx, u.ID)
	if err == nil {
		for _, d := range databases {
			dbDir := filepath.Join(staging, "users", u.Username, "databases")
			_ = os.MkdirAll(dbDir, 0o755)
			dumpPath := filepath.Join(dbDir, d.Server+"-"+d.Name+".sql")
			if err := b.DB.Dump(ctx, d, dumpPath); err != nil {
				return nil, fmt.Errorf("dump %s/%s: %w", d.Server, d.Name, err)
			}
		}
		mu.Databases = databases
	}
	return mu, nil
}

// List returns existing backups sorted newest-first.
func (b *Backup) List() ([]BackupInfo, error) {
	entries, err := os.ReadDir(b.Cfg.BackupDir)
	if err != nil {
		return nil, err
	}
	out := []BackupInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{
			Path: filepath.Join(b.Cfg.BackupDir, e.Name()), Name: e.Name(),
			Size: info.Size(), CreatedAt: info.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Restore recreates accounts from a backup archive. Full backups restore all
// users; per-user backups restore that user (creating the account if needed).
func (b *Backup) Restore(ctx context.Context, path string) error {
	staging, err := os.MkdirTemp(b.Cfg.BackupDir, "restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	if _, err := RunTimeout(15*time.Minute, "tar", "xzf", path, "-C", staging); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	manifestData, err := os.ReadFile(filepath.Join(staging, "manifest.json"))
	if err != nil {
		return errors.New("backup has no manifest.json")
	}
	var m Manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return fmt.Errorf("bad manifest: %w", err)
	}

	for _, mu := range m.Users {
		if err := b.restoreUser(ctx, staging, &m, &mu); err != nil {
			return fmt.Errorf("restore %s: %w", mu.Username, err)
		}
	}
	return nil
}

func (b *Backup) restoreUser(ctx context.Context, staging string, m *Manifest, mu *ManifestUser) error {
	// Panel account.
	u, err := b.Store.GetUserByUsername(ctx, mu.Username)
	if err != nil {
		// Create the account (password reset by admin afterwards).
		tmp, _ := RandomString(24)
		hash, _ := b.Auth.HashPassword(tmp)
		pkg, _ := b.Store.GetDefaultPackage(ctx)
		pkgID := int64(0)
		if pkg != nil {
			pkgID = pkg.ID
		}
		home := mu.HomeDir
		if home == "" {
			home = filepath.Join(b.Cfg.HomeRoot, mu.Username)
		}
		u = &store.User{
			Username: mu.Username, Email: mu.Email, Role: mu.Role, Status: mu.Status,
			PackageID: pkgID, HomeDir: home, PasswordHash: hash,
		}
		if err := b.Store.CreateUser(ctx, u); err != nil {
			return err
		}
		if _, err := RunTimeout(15*time.Second, "useradd", "-m", "-d", home, "-s", "/sbin/nologin", "-g", "www-data", mu.Username); err != nil {
			return fmt.Errorf("system user: %w", err)
		}
	} else {
		u.Status = mu.Status
		_ = b.Store.UpdateUser(ctx, u)
	}

	// Home directory.
	homeTar := filepath.Join(staging, "users", mu.Username, "home.tar.gz")
	if _, err := os.Stat(homeTar); err == nil {
		home := u.HomeDir
		if home == "" {
			home = filepath.Join(b.Cfg.HomeRoot, mu.Username)
		}
		_ = os.MkdirAll(home, 0o755)
		if _, err := RunTimeout(15*time.Minute, "tar", "xzf", homeTar, "-C", filepath.Dir(home)); err != nil {
			return fmt.Errorf("home restore: %w", err)
		}
		_, _ = RunTimeout(15*time.Second, "chown", "-R", mu.Username+":www-data", home)
	}

	// Databases: recreate + restore dumps.
	for _, d := range mu.Databases {
		row, err := b.Store.GetDatabase(ctx, d.ID)
		if err != nil {
			// create fresh with same name
			nrow, err := b.DB.Create(ctx, u, d.Server, d.Name)
			if err != nil {
				return fmt.Errorf("db create %s: %w", d.Name, err)
			}
			row = nrow
		}
		dumpPath := filepath.Join(staging, "users", mu.Username, "databases", d.Server+"-"+d.Name+".sql")
		if _, err := os.Stat(dumpPath); err == nil {
			if err := b.DB.Restore(ctx, row, dumpPath); err != nil {
				return fmt.Errorf("db restore %s: %w", d.Name, err)
			}
		}
	}

	// FTP accounts.
	for _, f := range mu.FTP {
		if _, err := b.Store.GetFTPAccountByUsername(ctx, f.Username); err != nil {
			acct := &store.FTPAccount{
				UserID: u.ID, Username: f.Username, PasswordHash: f.PasswordHash,
				HomeDir: f.HomeDir, Enabled: f.Enabled,
			}
			if err := b.Store.CreateFTPAccount(ctx, acct); err != nil {
				return err
			}
		}
	}

	// Domains + web config + dns records.
	for _, d := range mu.Domains {
		nd, err := b.Store.GetDomainByName(ctx, d.Domain)
		if err != nil {
			nd = &store.Domain{
				UserID: u.ID, Domain: d.Domain, DocumentRoot: d.DocumentRoot,
				PHPVersion: d.PHPVersion, WebServer: d.WebServer,
				SSLEnabled: d.SSLEnabled, SSLCertPath: d.SSLCertPath, SSLKeyPath: d.SSLKeyPath,
				SSLProvider: d.SSLProvider, SSLAutoRenew: d.SSLAutoRenew,
			}
			if err := b.Store.CreateDomain(ctx, nd); err != nil {
				return err
			}
			if err := b.Web.Apply(nd, mu.Aliases[d.Domain], u.Username); err != nil {
				return fmt.Errorf("web apply %s: %w", d.Domain, err)
			}
		}
		// DNS records.
		if recs, ok := mu.DNSRecords[d.Domain]; ok && len(recs) > 0 {
			zone, err := b.Store.GetZoneByDomain(ctx, nd.ID)
			if err != nil {
				zone = &store.DNSZone{DomainID: nd.ID, Provider: "local"}
				if err := b.Store.CreateZone(ctx, zone); err != nil {
					return err
				}
			}
			if err := b.Store.ReplaceRecords(ctx, zone.ID, recs); err != nil {
				return err
			}
		}
	}
	return nil
}

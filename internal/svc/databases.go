package svc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"

	"aegis/internal/config"
	"aegis/internal/store"
)

// Databases provisions and manages user databases on MariaDB and PostgreSQL.
// The panel connects with server-administrator credentials from the config,
// creates databases/users with strong random passwords and records the
// credentials (plaintext, like cPanel) so backups can dump and restore.
type Databases struct {
	Cfg   *config.Config
	Store *store.Store
}

func NewDatabases(cfg *config.Config, st *store.Store) *Databases {
	return &Databases{Cfg: cfg, Store: st}
}

// ServerInfo describes an available database server.
type ServerInfo struct {
	Name    string `json:"name"` // mariadb | postgres
	Running bool   `json:"running"`
	Version string `json:"version"`
}

// Servers detects installed/running database servers.
func (d *Databases) Servers(ctx context.Context) []ServerInfo {
	var out []ServerInfo
	if LookPath("mariadb") || LookPath("mysql") || ServiceRunning("mariadb") || ServiceRunning("mysql") {
		ver := ""
		if outV, err := RunTimeout(10*time.Second, "mariadb", "--version"); err == nil {
			ver = strings.TrimPrefix(outV, "mariadb  Ver ")
		} else if outV, err := RunTimeout(10*time.Second, "mysql", "--version"); err == nil {
			ver = outV
		}
		out = append(out, ServerInfo{Name: "mariadb", Running: d.mariaRunning(ctx), Version: ver})
	}
	if LookPath("psql") || ServiceRunning("postgresql") {
		ver := ""
		if outV, err := RunTimeout(10*time.Second, "psql", "--version"); err == nil {
			ver = strings.TrimPrefix(outV, "psql (PostgreSQL) ")
		}
		out = append(out, ServerInfo{Name: "postgres", Running: d.pgRunning(ctx), Version: ver})
	}
	return out
}

func (d *Databases) mariaRunning(ctx context.Context) bool {
	if ServiceRunning("mariadb") || ServiceRunning("mysql") {
		return true
	}
	db, err := d.mariaConn(ctx)
	if err != nil {
		return false
	}
	db.Close()
	return true
}

func (d *Databases) pgRunning(ctx context.Context) bool {
	if ServiceRunning("postgresql") {
		return true
	}
	conn, err := d.pgConn(ctx)
	if err != nil {
		return false
	}
	conn.Close(context.Background())
	return true
}

// mariaConn opens the MariaDB admin connection.
func (d *Databases) mariaConn(ctx context.Context) (*sql.DB, error) {
	mc := mysql.NewConfig()
	mc.Net = "tcp"
	mc.Addr = fmt.Sprintf("%s:%d", d.Cfg.MariaDB.Host, d.Cfg.MariaDB.Port)
	mc.User = d.Cfg.MariaDB.User
	mc.Passwd = d.Cfg.MariaDB.Password
	mc.AllowNativePasswords = true
	mc.Timeout = 5 * time.Second
	db, err := sql.Open("mysql", mc.FormatDSN())
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("mariadb: %w", err)
	}
	return db, nil
}

// pgConn opens the PostgreSQL admin connection.
func (d *Databases) pgConn(ctx context.Context) (*pgx.Conn, error) {
	cfg, err := pgx.ParseConfig(fmt.Sprintf("postgres://%s:%s@%s:%d/postgres?sslmode=disable",
		d.Cfg.PostgreSQL.User, d.Cfg.PostgreSQL.Password, d.Cfg.PostgreSQL.Host, d.Cfg.PostgreSQL.Port))
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return conn, nil
}

// Create provisions a database + user on the given server.
func (d *Databases) Create(ctx context.Context, user *store.User, server, name string) (*store.Database, error) {
	if server != "mariadb" && server != "postgres" {
		return nil, fmt.Errorf("unsupported database server %q", server)
	}
	if !ValidDBName(name) {
		return nil, errors.New("invalid database name (lowercase letters, digits, underscores)")
	}
	// Quota check.
	pkg, err := d.Store.GetPackage(ctx, user.PackageID)
	if err != nil {
		pkg, _ = d.Store.GetDefaultPackage(ctx)
	}
	if pkg != nil && pkg.MaxDatabases > 0 {
		usage, err := d.Store.PackageUsage(ctx, user.ID)
		if err != nil {
			return nil, err
		}
		if usage.Databases >= pkg.MaxDatabases {
			return nil, fmt.Errorf("package %s allows at most %d database(s)", pkg.Name, pkg.MaxDatabases)
		}
	}

	dbUser := name + "_u"
	password, err := RandomPassword()
	if err != nil {
		return nil, err
	}

	switch server {
	case "mariadb":
		if err := d.mariaCreate(ctx, name, dbUser, password); err != nil {
			return nil, err
		}
	case "postgres":
		if err := d.pgCreate(ctx, name, dbUser, password); err != nil {
			return nil, err
		}
	}

	row := &store.Database{
		UserID:     user.ID,
		Server:     server,
		Name:       name,
		DBUser:     dbUser,
		DBPassword: password,
	}
	if err := d.Store.CreateDatabase(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

func (d *Databases) mariaCreate(ctx context.Context, name, dbUser, password string) error {
	db, err := d.mariaConn(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
	stmts := []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci", name),
		fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'localhost' IDENTIFIED BY '%s'", dbUser, password),
		fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'127.0.0.1' IDENTIFIED BY '%s'", dbUser, password),
		fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost'", name, dbUser),
		fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'127.0.0.1'", name, dbUser),
		"FLUSH PRIVILEGES",
	}
	for _, st := range stmts {
		if _, err := db.ExecContext(ctx, st); err != nil {
			return fmt.Errorf("mariadb: %s: %w", st, err)
		}
	}
	return nil
}

func (d *Databases) pgCreate(ctx context.Context, name, dbUser, password string) error {
	conn, err := d.pgConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD %s NOSUPERUSER NOCREATEDB NOCREATEROLE", dbUser, QuoteSQL(password))); err != nil {
		return fmt.Errorf("postgres: create role: %w", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s ENCODING 'UTF8' TEMPLATE template0", name, dbUser)); err != nil {
		return fmt.Errorf("postgres: create database: %w", err)
	}
	return nil
}

// Drop removes a database and its user from the server + store.
func (d *Databases) Drop(ctx context.Context, dbRow *store.Database) error {
	switch dbRow.Server {
	case "mariadb":
		db, err := d.mariaConn(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		for _, st := range []string{
			fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbRow.Name),
			fmt.Sprintf("DROP USER IF EXISTS '%s'@'localhost'", dbRow.DBUser),
			fmt.Sprintf("DROP USER IF EXISTS '%s'@'127.0.0.1'", dbRow.DBUser),
		} {
			if _, err := db.ExecContext(ctx, st); err != nil {
				return fmt.Errorf("mariadb: %w", err)
			}
		}
	case "postgres":
		conn, err := d.pgConn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s WITH (FORCE)", dbRow.Name)); err != nil {
			return fmt.Errorf("postgres: drop database: %w", err)
		}
		if _, err := conn.Exec(ctx, fmt.Sprintf("DROP ROLE IF EXISTS %s", dbRow.DBUser)); err != nil {
			return fmt.Errorf("postgres: drop role: %w", err)
		}
	default:
		return fmt.Errorf("unsupported server %q", dbRow.Server)
	}
	return d.Store.DeleteDatabaseRow(ctx, dbRow.ID)
}

// Dump writes a SQL dump of a database to path (used by backups).
func (d *Databases) Dump(ctx context.Context, dbRow *store.Database, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	switch dbRow.Server {
	case "mariadb":
		args := []string{
			"-h", d.Cfg.MariaDB.Host,
			"-P", fmt.Sprintf("%d", d.Cfg.MariaDB.Port),
			"-u", d.Cfg.MariaDB.User,
			fmt.Sprintf("-p%s", d.Cfg.MariaDB.Password),
			"--single-transaction", "--routines", "--triggers",
			dbRow.Name,
		}
		out, err := RunTimeout(5*time.Minute, "mysqldump", args...)
		if err != nil {
			return fmt.Errorf("mysqldump: %w", err)
		}
		return os.WriteFile(path, []byte(out), 0o600)
	case "postgres":
		env := append(os.Environ(),
			"PGPASSWORD="+d.Cfg.PostgreSQL.Password,
			"PGHOST="+d.Cfg.PostgreSQL.Host,
			"PGPORT="+fmt.Sprintf("%d", d.Cfg.PostgreSQL.Port),
			"PGUSER="+d.Cfg.PostgreSQL.User,
		)
		out, err := ExecWithEnv(ctx, env, "pg_dump", "-Fc", dbRow.Name)
		if err != nil {
			return fmt.Errorf("pg_dump: %w", err)
		}
		return os.WriteFile(path, []byte(out), 0o600)
	default:
		return fmt.Errorf("unsupported server %q", dbRow.Server)
	}
}

// Restore imports a SQL dump into a database (used by backups). For
// postgres, pg_restore is used since dumps are custom-format.
func (d *Databases) Restore(ctx context.Context, dbRow *store.Database, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch dbRow.Server {
	case "mariadb":
		db, err := d.mariaConn(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		if _, err := db.ExecContext(ctx, fmt.Sprintf("USE `%s`", dbRow.Name)); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("mariadb: restore: %w", err)
		}
		return nil
	case "postgres":
		env := append(os.Environ(),
			"PGPASSWORD="+d.Cfg.PostgreSQL.Password,
			"PGHOST="+d.Cfg.PostgreSQL.Host,
			"PGPORT="+fmt.Sprintf("%d", d.Cfg.PostgreSQL.Port),
			"PGUSER="+d.Cfg.PostgreSQL.User,
		)
		if _, err := ExecWithEnv(ctx, env, "pg_restore", "--no-owner", "--role", dbRow.DBUser, "-d", dbRow.Name, path); err != nil {
			return fmt.Errorf("pg_restore: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported server %q", dbRow.Server)
	}
}

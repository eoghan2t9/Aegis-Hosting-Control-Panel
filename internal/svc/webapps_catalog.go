package svc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"aegis/internal/store"
)

// InstallStrategy is how an app's files get onto disk.
type InstallStrategy string

const (
	StrategyTarball  InstallStrategy = "tarball"  // download + extract (zip or tar.*)
	StrategyComposer InstallStrategy = "composer" // composer create-project
)

// AppManifest is one catalog entry — the "plugin" extension point. Adding a
// new app means adding an entry here, not touching InstallApp.
type AppManifest struct {
	ID       string
	Name     string
	Category string // cms | forum | wiki | ecommerce | tools | framework

	Strategy        InstallStrategy
	DownloadURL     string // tarball strategy
	ExtractSubdir   string // "" = archive root, "*" = auto-detect single wrapping dir, else a fixed name
	ComposerPackage string // composer strategy

	// PublicSubdir is set when the app's real webroot is a subdirectory of
	// the project (Laravel/Flarum "public", Drupal/Craft "web") — the
	// engine installs into a sibling "<domain>-app" dir and symlinks the
	// visible document root to PublicSubdir within it.
	PublicSubdir string

	NeedsDatabase bool
	DBEngine      string // "mariadb"

	// ConfigFile, if set, is written before Install runs.
	ConfigFile func(db *store.Database, dom *store.Domain, appDir string) (path, content string)
	// Install, if set, runs the app's non-interactive CLI installer.
	Install func(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error
}

// Catalog is the full list of installable apps.
var Catalog = []AppManifest{
	{
		ID: "wordpress", Name: "WordPress", Category: "cms",
		Strategy: StrategyTarball, DownloadURL: "https://wordpress.org/latest.tar.gz", ExtractSubdir: "wordpress",
		NeedsDatabase: true, DBEngine: "mariadb",
		Install: installWordPress,
	},
	{
		ID: "laravel", Name: "Laravel", Category: "framework",
		Strategy: StrategyComposer, ComposerPackage: "laravel/laravel",
		PublicSubdir: "public",
	},
	{
		ID: "drupal", Name: "Drupal", Category: "cms",
		Strategy: StrategyComposer, ComposerPackage: "drupal/recommended-project",
		PublicSubdir:  "web",
		NeedsDatabase: true, DBEngine: "mariadb",
		Install: installDrupal,
	},
	{
		ID: "grav", Name: "Grav", Category: "cms",
		Strategy: StrategyTarball, DownloadURL: "https://getgrav.org/download/core/grav-admin/latest", ExtractSubdir: "*",
	},
	{
		ID: "phpbb", Name: "phpBB", Category: "forum",
		Strategy: StrategyTarball, DownloadURL: "https://download.phpbb.com/pub/release/3.3/3.3.15/phpBB-3.3.15.zip", ExtractSubdir: "phpBB3",
		NeedsDatabase: true, DBEngine: "mariadb",
		Install: installPhpBB,
	},
	{
		ID: "mediawiki", Name: "MediaWiki", Category: "wiki",
		Strategy: StrategyTarball, DownloadURL: "https://releases.wikimedia.org/mediawiki/1.41/mediawiki-1.41.1.tar.gz", ExtractSubdir: "*",
		NeedsDatabase: true, DBEngine: "mariadb",
		Install: installMediaWiki,
	},
	{
		ID: "nextcloud", Name: "Nextcloud", Category: "tools",
		Strategy: StrategyTarball, DownloadURL: "https://download.nextcloud.com/server/releases/latest.tar.bz2", ExtractSubdir: "nextcloud",
		NeedsDatabase: true, DBEngine: "mariadb",
		Install: installNextcloud,
	},
	{
		ID: "matomo", Name: "Matomo", Category: "tools",
		Strategy: StrategyTarball, DownloadURL: "https://builds.matomo.org/matomo-latest.tar.gz", ExtractSubdir: "matomo",
		NeedsDatabase: true, DBEngine: "mariadb",
		ConfigFile: matomoConfig,
	},
	{
		ID: "phpmyadmin", Name: "phpMyAdmin", Category: "tools",
		Strategy: StrategyTarball, DownloadURL: "https://www.phpmyadmin.net/downloads/phpMyAdmin-latest-all-languages.tar.gz", ExtractSubdir: "*",
		NeedsDatabase: false,
		ConfigFile:    phpMyAdminConfig,
	},
	{
		ID: "craftcms", Name: "Craft CMS", Category: "cms",
		Strategy: StrategyComposer, ComposerPackage: "craftcms/craft",
		PublicSubdir:  "web",
		NeedsDatabase: true, DBEngine: "mariadb",
		Install: installCraft,
	},
	{
		ID: "flarum", Name: "Flarum", Category: "forum",
		Strategy: StrategyComposer, ComposerPackage: "flarum/flarum",
		PublicSubdir:  "public",
		NeedsDatabase: true, DBEngine: "mariadb",
		ConfigFile: flarumConfig,
		Install:    installFlarum,
	},
	{
		ID: "prestashop", Name: "PrestaShop", Category: "ecommerce",
		Strategy: StrategyTarball, DownloadURL: "https://github.com/PrestaShop/PrestaShop/releases/download/8.1.7/prestashop_8.1.7.zip", ExtractSubdir: "",
		NeedsDatabase: true, DBEngine: "mariadb",
		Install: installPrestaShop,
	},
}

// --- install steps ------------------------------------------------------------

func installWordPress(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error {
	if _, err := w.RunWPCLI(ctx, dom, []string{"config", "create",
		"--dbname=" + db.Name, "--dbuser=" + db.DBUser, "--dbpass=" + db.DBPassword, "--dbhost=localhost", "--skip-check"}); err != nil {
		return err
	}
	adminPass, err := RandomPassword()
	if err != nil {
		return err
	}
	if _, err := w.RunWPCLI(ctx, dom, []string{"core", "install",
		"--url=" + dom.Domain, "--title=" + dom.Domain,
		"--admin_user=admin", "--admin_password=" + adminPass,
		"--admin_email=admin@" + dom.Domain, "--skip-email"}); err != nil {
		return err
	}
	return writeCredentials(appDir, "WordPress", "admin", adminPass)
}

func installDrupal(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error {
	if _, err := RunTimeout(3*time.Minute, "composer", "--working-dir="+appDir, "require", "drush/drush", "--no-interaction"); err != nil {
		return fmt.Errorf("require drush: %w", err)
	}
	adminPass, err := RandomPassword()
	if err != nil {
		return err
	}
	dbURL := fmt.Sprintf("mysql://%s:%s@localhost/%s", db.DBUser, db.DBPassword, db.Name)
	drush := filepath.Join(appDir, "vendor/bin/drush")
	if _, err := RunTimeout(3*time.Minute, drush, "site:install", "standard",
		"--db-url="+dbURL, "--site-name="+dom.Domain,
		"--account-name=admin", "--account-pass="+adminPass, "--yes",
		"--root="+filepath.Join(appDir, "web")); err != nil {
		return fmt.Errorf("drush site:install: %w", err)
	}
	return writeCredentials(appDir, "Drupal", "admin", adminPass)
}

func installPhpBB(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error {
	adminPass, err := RandomPassword()
	if err != nil {
		return err
	}
	// bin/phpbbcli.php assumes an already-installed board (its DI container
	// bootstraps against config that doesn't exist yet) — the real installer
	// is install/phpbbcli.php, and it takes a YAML config file, not flags
	// (schema: phpbb/install/installer_configuration.php).
	yaml := fmt.Sprintf(`installer:
  admin:
    name: admin
    password: %q
    email: %q
  board:
    lang: en
    name: %q
    description: "phpBB forum"
  database:
    dbms: mysqli
    dbhost: localhost
    dbport: ~
    dbuser: %q
    dbpasswd: %q
    dbname: %q
    table_prefix: phpbb_
`, adminPass, "admin@"+dom.Domain, dom.Domain, db.DBUser, db.DBPassword, db.Name)
	cfgPath := filepath.Join(appDir, "aegis-install-config.yml")
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		return err
	}
	defer os.Remove(cfgPath)

	cli := filepath.Join(appDir, "install", "phpbbcli.php")
	c, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := Exec(c, "php", cli, "install", cfgPath, "--no-interaction"); err != nil {
		return err
	}
	return writeCredentials(appDir, "phpBB", "admin", adminPass)
}

func installMediaWiki(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error {
	adminPass, err := RandomPassword()
	if err != nil {
		return err
	}
	script := filepath.Join(appDir, "maintenance", "install.php")
	c, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := Exec(c, "php", script,
		"--dbname="+db.Name, "--dbserver=localhost", "--dbuser="+db.DBUser, "--dbpass="+db.DBPassword, "--dbtype=mysql",
		"--pass="+adminPass, "--scriptpath=", dom.Domain, "admin"); err != nil {
		return err
	}
	return writeCredentials(appDir, "MediaWiki", "admin", adminPass)
}

func installNextcloud(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error {
	adminPass, err := RandomPassword()
	if err != nil {
		return err
	}
	occ := filepath.Join(appDir, "occ")
	c, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := Exec(c, "php", occ, "maintenance:install",
		"--database=mysql", "--database-name="+db.Name, "--database-user="+db.DBUser, "--database-pass="+db.DBPassword,
		"--database-host=localhost", "--admin-user=admin", "--admin-pass="+adminPass); err != nil {
		return err
	}
	return writeCredentials(appDir, "Nextcloud", "admin", adminPass)
}

func matomoConfig(db *store.Database, dom *store.Domain, appDir string) (string, string) {
	content := fmt.Sprintf(`[database]
host = "localhost"
username = "%s"
password = "%s"
dbname = "%s"
tables_prefix = "matomo_"
adapter = "PDO\MYSQL"
type = "InnoDB"
schema = "Mysql"
port = 3306

[General]
salt = "%s"
`, db.DBUser, db.DBPassword, db.Name, mustRandom())
	return filepath.Join(appDir, "config", "config.ini.php"), "<?php exit; ?>\n" + content
}

func phpMyAdminConfig(db *store.Database, dom *store.Domain, appDir string) (string, string) {
	content := fmt.Sprintf(`<?php
$cfg['blowfish_secret'] = %q;
$i = 0;
$i++;
$cfg['Servers'][$i]['auth_type'] = 'cookie';
$cfg['Servers'][$i]['host'] = 'localhost';
$cfg['Servers'][$i]['compress'] = false;
$cfg['Servers'][$i]['AllowNoPassword'] = false;
$cfg['UploadDir'] = '';
$cfg['SaveDir'] = '';
`, mustRandom())
	return filepath.Join(appDir, "config.inc.php"), content
}

func installCraft(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error {
	env := fmt.Sprintf(`CRAFT_ENVIRONMENT=production
DB_DRIVER=mysql
DB_SERVER=localhost
DB_PORT=3306
DB_DATABASE=%s
DB_USER=%s
DB_PASSWORD=%s
DB_SCHEMA=
DB_TABLE_PREFIX=
`, db.Name, db.DBUser, db.DBPassword)
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte(env), 0o640); err != nil {
		return err
	}
	adminPass, err := RandomPassword()
	if err != nil {
		return err
	}
	craft := filepath.Join(appDir, "craft")
	c, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := Exec(c, "php", craft, "install", "--interactive=0",
		"--username=admin", "--password="+adminPass, "--email=admin@"+dom.Domain,
		"--site-name="+dom.Domain, "--site-url=http://"+dom.Domain, "--language=en-US"); err != nil {
		return err
	}
	return writeCredentials(appDir, "Craft CMS", "admin", adminPass)
}

func flarumConfig(db *store.Database, dom *store.Domain, appDir string) (string, string) {
	content := fmt.Sprintf(`<?php return array (
  'debug' => false,
  'database' =>
  array (
    'driver' => 'mysql',
    'host' => 'localhost',
    'database' => %q,
    'username' => %q,
    'password' => %q,
    'port' => 3306,
    'prefix' => '',
    'charset' => 'utf8mb4',
    'collation' => 'utf8mb4_unicode_ci',
    'strict' => true,
    'engine' => 'InnoDB',
  ),
  'url' => 'http://%s',
  'paths' =>
  array (
    'api' => 'api',
    'admin' => 'admin',
  ),
);
`, db.Name, db.DBUser, db.DBPassword, dom.Domain)
	return filepath.Join(appDir, "config.php"), content
}

func installFlarum(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error {
	// Flarum's own installer is a web wizard; with config.php already
	// written (ConfigFile above), `php flarum migrate` brings the schema up
	// and `php flarum user:create` provisions the admin — the documented
	// non-interactive path once the database config already exists.
	adminPass, err := RandomPassword()
	if err != nil {
		return err
	}
	flarum := filepath.Join(appDir, "flarum")
	c, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if _, err := Exec(c, "php", flarum, "migrate"); err != nil {
		return fmt.Errorf("flarum migrate: %w", err)
	}
	if _, err := Exec(c, "php", flarum, "user:create",
		"--username=admin", "--password="+adminPass, "--email=admin@"+dom.Domain, "--admin"); err != nil {
		return fmt.Errorf("flarum user:create: %w", err)
	}
	return writeCredentials(appDir, "Flarum", "admin", adminPass)
}

func installPrestaShop(ctx context.Context, w *WebApps, dom *store.Domain, appDir string, db *store.Database) error {
	adminPass, err := RandomPassword()
	if err != nil {
		return err
	}
	script := filepath.Join(appDir, "install", "index_cli.php")
	c, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if _, err := Exec(c, "php", script,
		"--domain="+dom.Domain, "--db_server=localhost", "--db_name="+db.Name,
		"--db_user="+db.DBUser, "--db_password="+db.DBPassword,
		"--email=admin@"+dom.Domain, "--password="+adminPass,
		"--language=en", "--country=us", "--all_languages=0", "--newsletter=0", "--send_email=0"); err != nil {
		return err
	}
	return writeCredentials(appDir, "PrestaShop", "admin@"+dom.Domain, adminPass)
}

func mustRandom() string {
	s, err := RandomString(32)
	if err != nil {
		return "fallback-salt-change-me"
	}
	return s
}

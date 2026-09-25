#!/usr/bin/env bash
#
# Aegis bare-metal installer — installs the complete hosting stack from distro
# packages, so end users don't have to hunt for anything by hand.
#
#   sudo scripts/install-stack.sh                       # everything
#   sudo scripts/install-stack.sh --php-versions 8.2,8.3
#   sudo scripts/install-stack.sh --no-mail --no-ftp
#   sudo scripts/install-stack.sh --with-caddy --skip-build
#   sudo scripts/install-stack.sh --with-docker           # + Docker Engine, opt-in
#
# What it does:
#   1. Installs PHP-FPM (default 7.4→8.4, sury.org on Debian/Ubuntu)
#   2. Installs MariaDB + PostgreSQL servers and seeds admin credentials
#   3. Installs a web server (nginx by default; apache/caddy optional)
#   4. Installs vsftpd, fail2ban, ffmpeg + poppler-utils, unzip, zip
#   5. Installs the mail stack (postfix+dovecot+opendkim) unless --no-mail
#   6. Installs Go from the distro (or downloads the toolchain) and builds
#      the aegis binary — unless --skip-build
#   7. Installs the panel as a systemd service, generates the secret key and
#      an admin password, starts it, verifies the login, and prints the
#      one-time admin credentials
#
# Packages stay distro-managed on purpose: security updates for PHP CVEs and
# friends keep flowing through `apt upgrade` — nothing here is vendored.
#
# Supported today: Debian 11+ and Ubuntu 20.04+ (x86_64/arm64).

set -euo pipefail

# ----------------------------- defaults --------------------------------------
PHP_VERSIONS="7.4 8.1 8.2 8.3 8.4"
WEB_SERVER="nginx"          # nginx | apache | caddy
WITH_FTP=1 WITH_MAIL=1 WITH_DB=1 WITH_TOOLS=1 WITH_BUILD=1 WITH_FAIL2BAN=1
# Docker is opt-in (unlike everything else above): it's a much bigger,
# security-relevant addition to the host than a package the panel enables
# per account, so --with-docker is required rather than --no-docker to skip
# it. Same posture as packages.allow_docker defaulting off in the panel.
WITH_DOCKER=0
MARIADB_PASSWORD="" POSTGRES_PASSWORD=""
AEGIS_DIR="/etc/aegis"
AEGIS_BIN="/usr/local/bin/aegis"
SERVICE_NAME="aegis"
SURY_REPO="https://packages.sury.org/php"
GO_VERSION_DIST="1.23"

log()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m !\033[0m  %s\n' "$*" >&2; }
die()  { printf '\033[1;31m==>\033[0m %s\n' "$*" >&2; exit 1; }

usage() { sed -n '2,25p' "$0"; exit 0; }

# ----------------------------- arg parsing -----------------------------------
while [ $# -gt 0 ]; do
  case "$1" in
    --php-versions) PHP_VERSIONS="${2//,/ }"; shift 2 ;;
    --web-server)   WEB_SERVER="$2"; shift 2 ;;
    --with-caddy)   WEB_SERVER="caddy"; shift ;;
    --with-apache)  WEB_SERVER="apache"; shift ;;
    --no-mail)      WITH_MAIL=0; shift ;;
    --no-ftp)       WITH_FTP=0; shift ;;
    --no-db)        WITH_DB=0; shift ;;
    --no-tools)     WITH_TOOLS=0; shift ;;
    --no-fail2ban)  WITH_FAIL2BAN=0; shift ;;
    --with-docker)  WITH_DOCKER=1; shift ;;
    --skip-build)   WITH_BUILD=0; shift ;;
    --mariadb-password) MARIADB_PASSWORD="$2"; shift 2 ;;
    --postgres-password) POSTGRES_PASSWORD="$2"; shift 2 ;;
    --service-name) SERVICE_NAME="$2"; shift 2 ;;
    -h|--help)      usage ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "run as root: sudo $0 $*"

# ----------------------------- distro detect ---------------------------------
if [ -r /etc/os-release ]; then . /etc/os-release; fi
DIST_ID="${ID:-unknown}"; DIST_VER="${VERSION_ID:-0}"
case "$DIST_ID" in
  debian|ubuntu|raspbian) : ;;
  *) die "unsupported distro '$DIST_ID' — Debian/Ubuntu only for now (patches welcome)" ;;
esac
log "detected $DIST_ID $DIST_VER ($(dpkg --print-architecture))"

export DEBIAN_FRONTEND=noninteractive
apt_get() { apt-get -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold "$@"; }

# ----------------------------- 1. PHP ----------------------------------------
# Always use sury.org directly (for both Debian and Ubuntu): it publishes
# PHP builds for every current Debian/Ubuntu codename (jammy/noble/resolute/...)
# and is what ondrej's own ppa:ondrej/php now points people to — that PPA is
# being merged into sury.org and lags behind on brand-new Ubuntu releases.
log "installing PHP-FPM: $PHP_VERSIONS"
apt_get update
apt_get install -y ca-certificates curl gnupg lsb-release
if ! grep -rq "packages.sury.org" /etc/apt/sources.list /etc/apt/sources.list.d/ 2>/dev/null; then
  curl -fsSL "$SURY_REPO/apt.gpg" -o /etc/apt/trusted.gpg.d/php.gpg
  echo "deb $SURY_REPO/ $(lsb_release -sc) main" > /etc/apt/sources.list.d/php.list
  apt_get update
fi
PHP_PKGS=""
for v in $PHP_VERSIONS; do
  # The modules most sites actually use: database drivers (mysql/pgsql/sqlite),
  # imaging (gd/imagick), strings & encodings (mbstring/iconv), XML stack,
  # compression (zip/zlib via common), intl, bcmath, curl, soap, opcache, the
  # extensions Composer/WP-CLI/Laravel/WordPress expect at runtime, plus the
  # caching backends (redis/memcached/apcu) and imap that most hosted apps
  # reach for as soon as they need a cache layer or mailbox access.
  PHP_PKGS="$PHP_PKGS php$v-fpm php$v-cli php$v-common php$v-mysql php$v-pgsql php$v-sqlite3 \
    php$v-curl php$v-gd php$v-xml php$v-mbstring php$v-zip php$v-intl php$v-bcmath \
    php$v-imagick php$v-soap php$v-opcache php$v-readline php$v-ldap \
    php$v-redis php$v-memcached php$v-apcu php$v-imap"
done
apt_get install $PHP_PKGS

# Enable the extension modules explicitly (phpenmod): the -mysql/-gd/...
# packages drop ini fragments that phpenmod wires up; extensions ship enabled
# by default on Debian/Ubuntu but the explicit pass makes the state
# deterministic and picks up anything a distro leaves disabled.
for v in $PHP_VERSIONS; do
  phpenmod -v "$v" curl gd mbstring xml xmlreader xmlwriter simplexml zip intl \
    bcmath soap mysqli pdo_mysql pgsql pdo_pgsql sqlite3 pdo_sqlite imagick \
    opcache ldap redis memcached apcu imap 2>/dev/null || true
done
log "PHP installed: $(ls -1 /usr/bin/php* 2>/dev/null | grep -E 'php[0-9.]+$' | tr '\n' ' ')"

# ----------------------------- 2. databases ----------------------------------
if [ "$WITH_DB" = 1 ]; then
  log "installing MariaDB server + client"
  apt_get install mariadb-server mariadb-client
  systemctl enable --now mariadb
  [ -n "$MARIADB_PASSWORD" ] || { MARIADB_PASSWORD="$(head -c18 /dev/urandom | base64 | tr -d '/+=')"; warn "generated MariaDB admin password: $MARIADB_PASSWORD"; }
  mariadb --socket=/run/mysqld/mysqld.sock -uroot -e \
    "ALTER USER 'root'@'localhost' IDENTIFIED BY '${MARIADB_PASSWORD}';
     CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED BY '${MARIADB_PASSWORD}';
     GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION; FLUSH PRIVILEGES;"

  log "installing PostgreSQL"
  apt_get install postgresql postgresql-contrib
  systemctl enable --now postgresql
  [ -n "$POSTGRES_PASSWORD" ] || { POSTGRES_PASSWORD="$(head -c18 /dev/urandom | base64 | tr -d '/+=')"; warn "generated PostgreSQL admin password: $POSTGRES_PASSWORD"; }
  su postgres -s /bin/sh -c "psql -c \"ALTER USER postgres PASSWORD '${POSTGRES_PASSWORD}';\"" >/dev/null
else
  log "skipping databases (--no-db)"
fi

# --------------------- 2b. panel credentials (root-only) ----------------------
# Credentials live in root-only systemd EnvironmentFile= snippets — never in the
# unit itself, which any local user can read via `systemctl show`.
#
#   /etc/aegis/aegis.env    persistent: DB admin passwords (survive reboots)
#   /run/aegis-boot.env     one boot: AEGIS_ADMIN_* (deleted after bootstrap so
#                           the generated admin password exists only until the
#                           admin changes it or the panel restarts)
#
# The JWT/crypto secret key is pre-provisioned at /etc/aegis/secret.key (0600)
# so it is stable from the very first boot.
mkdir -p /etc/aegis
umask 077
if [ ! -f /etc/aegis/secret.key ]; then
  head -c32 /dev/urandom | base64 > /etc/aegis/secret.key
  log "generated panel secret key: /etc/aegis/secret.key"
fi

# Persistent env file: when databases were (re)seeded this run, always rewrite
# so the file matches what the servers actually accept; otherwise create once.
if [ "$WITH_DB" = 1 ] || [ ! -f /etc/aegis/aegis.env ]; then
  {
    echo "AEGIS_MARIADB_PASSWORD=${MARIADB_PASSWORD:-}"
    echo "AEGIS_POSTGRES_PASSWORD=${POSTGRES_PASSWORD:-}"
  } > /etc/aegis/aegis.env
fi

ADMIN_USER="${AEGIS_ADMIN_USER:-admin}"
ADMIN_PASSWORD="${AEGIS_ADMIN_PASSWORD:-}"
FIRST_BOOT=0
if [ -f /var/lib/aegis/aegis.db ]; then
  # The panel's database exists: bootstrap already ran (or is mid-flight with
  # credentials the operator supplied). Never generate a new password here —
  # the panel would ignore it anyway once any account exists.
  log "panel database exists — admin account stays as-is"
else
  # Genuine first boot: provision credentials for the panel's own bootstrap.
  if [ -z "$ADMIN_PASSWORD" ]; then
    ADMIN_PASSWORD="$(head -c18 /dev/urandom | base64 | tr -d '/+=')"
    warn "generated panel admin password: $ADMIN_PASSWORD"
  fi
  FIRST_BOOT=1
fi
umask 022   # restore: Go build + unit file must not inherit the 077 above

# ----------------------------- 3. web server ---------------------------------
case "$WEB_SERVER" in
  nginx)  log "installing nginx";  apt_get install nginx;  systemctl enable --now nginx ;;
  apache)
    log "installing apache2"
    apt_get install apache2
    # Modules the panel's vhost template and typical .htaccess files rely on:
    # proxy (panel + PHP vhost handler), rewrite/headers (per-site rules),
    # expires/deflate (caching), and the FCGI set for php-fpm.
    a2enmod proxy proxy_http proxy_fcgi proxy_wstunnel rewrite headers \
      expires deflate mime setenvif 2>/dev/null || true
    systemctl enable --now apache2 ;;
  caddy)  log "installing caddy"
          apt_get install -y debian-keyring debian-archive-keyring apt-transport-https curl
          curl -fsSL "https://dl.cloudsmith.io/public/caddy/stable/gpg.key" | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg 2>/dev/null
          curl -fsSL "https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt" > /etc/apt/sources.list.d/caddy-stable.list
          apt_get update && apt_get install caddy && systemctl enable --now caddy ;;
  *) die "unknown web server '$WEB_SERVER'" ;;
esac

# Default vhost so the panel is reachable at http://<server-ip>/aegis before
# any customer domain exists (the first generated vhost takes over afterwards).
BASE="${AEGIS_PANEL_BASE:-/aegis}"; UP="127.0.0.1:${AEGIS_PANEL_PORT:-8080}"
case "$WEB_SERVER" in
  nginx)
    cat > /etc/nginx/sites-available/aegis-panel <<EOF
server {
    listen 80 default_server;
    server_name _;
    location = ${BASE} { return 301 ${BASE}/; }
    location ${BASE}/ {
        proxy_pass http://${UP};
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$http_connection;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
EOF
    ln -sf /etc/nginx/sites-available/aegis-panel /etc/nginx/sites-enabled/aegis-panel
    rm -f /etc/nginx/sites-enabled/default
    nginx -t && systemctl reload nginx ;;
  apache)
    cat > /etc/apache2/conf-available/aegis-panel.conf <<EOF
<Location ${BASE}>
    ProxyPass http://${UP}${BASE} retry=0 upgrade=websocket
    ProxyPassReverse http://${UP}${BASE}
</Location>
EOF
    a2enmod proxy proxy_http proxy_wstunnel >/dev/null
    a2enconf aegis-panel >/dev/null
    systemctl reload apache2 ;;
  caddy)
    # Install-time convenience only; the panel rewrites the Caddyfile when it
    # manages vhosts, which replaces this block.
    cat > /etc/caddy/Caddyfile <<EOF
:80 {
    handle_path ${BASE}/* {
        rewrite * ${BASE}/{*}
        reverse_proxy ${UP}
    }
    handle {
        respond "Aegis is being configured. Panel: http://<server-ip>${BASE}/" 200
    }
}
EOF
    systemctl reload caddy || systemctl restart caddy ;;
esac

# ----------------------------- 4. ftp / mail / tools -------------------------
if [ "$WITH_FTP" = 1 ]; then
  log "installing vsftpd"
  apt_get install vsftpd
  mkdir -p /var/run/vsftpd/empty
  # Every FTP account svc/ftp.go creates uses `useradd -s /sbin/nologin` (it's
  # an FTP-only account, never meant to get an interactive shell) — but the
  # distro's stock /etc/pam.d/vsftpd ends with `auth required pam_shells.so`,
  # which rejects any account whose shell isn't listed in /etc/shells.
  # /sbin/nologin never is, so every single FTP login would otherwise fail
  # with "530 Login incorrect" regardless of a correct password. Strip it;
  # idempotent (a no-op on a rerun once it's already gone).
  if [ -f /etc/pam.d/vsftpd ]; then
    sed -i '/pam_shells\.so/d' /etc/pam.d/vsftpd
  fi
  # Passive-mode data transfers need a second connection on top of control
  # port 21 — without a fixed range, vsftpd picks a random port anywhere in
  # the OS ephemeral range, which almost never matches what's open in an
  # external firewall/cloud security group (symptom: login succeeds, then
  # the client gets "connect failed: Connection refused" on the data
  # channel). Pin it to a small, predictable range instead — kept outside
  # svc.Docker's reserved container port range (20000-29999, see docker.go)
  # — and pin pasv_address to this host's own public IP so a multi-homed
  # box (e.g. one with a docker0 bridge) can't advertise the wrong address.
  FTP_PASV_MIN=30100
  FTP_PASV_MAX=30120
  FTP_PUBLIC_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
  if [ -n "$FTP_PUBLIC_IP" ] && [ -f /etc/vsftpd.conf ] && ! grep -q "^pasv_min_port=" /etc/vsftpd.conf; then
    cat >> /etc/vsftpd.conf <<EOF

# --- Aegis: fixed PASV port range + explicit public address (see
# install-stack.sh) so an external firewall only needs to allow one small,
# predictable range instead of the whole ephemeral port space.
pasv_enable=YES
pasv_min_port=$FTP_PASV_MIN
pasv_max_port=$FTP_PASV_MAX
pasv_address=$FTP_PUBLIC_IP
EOF
  fi
fi

if [ "$WITH_MAIL" = 1 ]; then
  log "installing mail stack (postfix dovecot opendkim)"
  apt_get install postfix dovecot-imapd dovecot-pop3d opendkim opendkim-tools rsyslog
  # Supervisord-style restart in svc/mail.go falls back to systemctl elsewhere;
  # make sure opendkim is enabled as a plain service on bare metal.
  systemctl enable opendkim || true
else
  log "skipping mail stack (--no-mail)"
fi

if [ "$WITH_TOOLS" = 1 ]; then
  log "installing helper tools (ffmpeg poppler unzip zip composer imagemagick)"
  apt_get install ffmpeg poppler-utils unzip zip imagemagick
  apt_get install composer || warn "composer not in this distro — install from getcomposer.org (needed for Laravel-style web apps)"
else
  log "skipping helper tools (--no-tools)"
fi

if [ "$WITH_FAIL2BAN" = 1 ]; then
  log "installing fail2ban"
  apt_get install fail2ban
  systemctl enable --now fail2ban
fi

if [ "$WITH_DOCKER" = 1 ]; then
  log "installing docker (--with-docker)"
  apt_get install docker.io
  systemctl enable --now docker
else
  log "skipping docker (opt in with --with-docker, or install later from the panel's Containers page as admin)"
fi

# ----------------------------- 5. build the panel ----------------------------
if [ "$WITH_BUILD" = 1 ]; then
  log "obtaining Go toolchain"
  if ! command -v go >/dev/null 2>&1; then
    apt_get install golang-go 2>/dev/null || {
      GO_TARBALL="go${GO_VERSION_DIST}.linux-$(dpkg --print-architecture).tar.gz"
      curl -fsSL "https://go.dev/dl/${GO_TARBALL}" -o /tmp/go.tgz
      rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz
      ln -sf /usr/local/go/bin/go /usr/local/bin/go
    }
  fi
  log "building aegis with go $(go version | awk '{print $3}')"
  REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
  (cd "$REPO_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$AEGIS_BIN" ./cmd/aegis) \
    || die "build failed — run with --skip-build to install the stack only"
else
  log "skipping build (--skip-build); install aegis binary to $AEGIS_BIN yourself"
fi

# ----------------------------- 6. systemd unit + start -----------------------
if [ "$WITH_BUILD" = 1 ] || [ -x "$AEGIS_BIN" ]; then
  log "installing systemd unit $SERVICE_NAME.service"
  cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Aegis hosting control panel
After=network.target mariadb.service postgresql.service

[Service]
Type=simple
ExecStart=${AEGIS_BIN}
# Loopback only: nginx/Apache/Caddy is the internet-facing listener and
# proxies ${BASE} to this port (see the vhost written above) — binding to
# all interfaces here would expose the unencrypted admin panel directly.
Environment=AEGIS_LISTEN=127.0.0.1:${AEGIS_PANEL_PORT:-8080}
Environment=AEGIS_PANEL_BASE=${BASE}
# Credentials live in root-only env files — never inline here: any local user
# can read unit properties (incl. Environment=) via \`systemctl show\`.
EnvironmentFile=/etc/aegis/aegis.env
EnvironmentFile=-/run/aegis-boot.env
# The panel manages users/services; it must run as root (see docs/ARCHITECTURE.md).
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}.service"

  # First-boot credentials: staged where systemd picks them up for exactly one
  # start, then deleted below. With --skip-build nothing is written here and
  # the operator bootstraps manually (password printed in the summary).
  if [ "$FIRST_BOOT" = 1 ]; then
    umask 077
    : > /run/aegis-boot.env
    echo "AEGIS_ADMIN_USER=$ADMIN_USER"  >> /run/aegis-boot.env
    echo "AEGIS_ADMIN_PASSWORD=$ADMIN_PASSWORD" >> /run/aegis-boot.env
    umask 022
  fi

  # Start the panel now and wait until it answers on the panel listener.
  log "starting ${SERVICE_NAME}.service"
  systemctl restart "${SERVICE_NAME}.service"
  UP=0
  for _ in $(seq 1 30); do
    if curl -fsS -o /dev/null "http://127.0.0.1:8080${BASE}/"; then UP=1; break; fi
    sleep 1
  done
  if [ "$UP" = 1 ]; then
    log "panel is up on http://127.0.0.1:8080${BASE}/"
  else
    warn "panel did not answer within 30s — check: journalctl -u ${SERVICE_NAME} -e"
  fi

  # Verify the bootstrap actually created the admin (one attempt, loopback).
  if [ "$FIRST_BOOT" = 1 ] && [ "$UP" = 1 ]; then
    CODE=$(curl -s -o /dev/null -w '%{http_code}' -m 10 -X POST "http://127.0.0.1:8080${BASE}/api/auth/login" \
      -H 'Content-Type: application/json' -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASSWORD\"}") || CODE=000
    if [ "$CODE" = "200" ]; then
      log "admin '$ADMIN_USER' created and verified (login OK)"
    else
      warn "admin login probe returned HTTP $CODE — verify manually"
    fi
  fi

  # The generated admin password must not outlive bootstrap: drop the boot env
  # file so a later restart can never re-read it. Kept only if the panel never
  # came up — otherwise a failed start would destroy the only copy of the
  # credentials and a healthy restart could never create the admin.
  if [ "$UP" = 1 ]; then
    rm -f /run/aegis-boot.env
    systemctl reset-failed "${SERVICE_NAME}.service" 2>/dev/null || true
    log "first-boot credentials wiped from /run (admin must change the generated password)"
  else
    warn "kept /run/aegis-boot.env for the next start — remove it once the panel is up"
  fi
fi

# ----------------------------- 7. summary ------------------------------------
PANEL_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
PANEL_IP=${PANEL_IP:-<server-ip>}
MDB_NOTE="skipped";  [ -n "$MARIADB_PASSWORD" ]  && MDB_NOTE="set — see /etc/aegis/aegis.env"
PG_NOTE="skipped";   [ -n "$POSTGRES_PASSWORD" ] && PG_NOTE="set — see /etc/aegis/aegis.env"
BOOTSTRAP_NOTE="(binary not installed — bootstrap manually, see below)"
if [ "$WITH_BUILD" = 1 ] || [ -x "$AEGIS_BIN" ]; then
  BOOTSTRAP_NOTE="running as a systemd service, ready to use"
fi
cat <<EOF

============================================================
 Aegis stack installed.
============================================================
 PHP-FPM:        $(for v in $PHP_VERSIONS; do printf "php$v "; done)
 Web server:     $WEB_SERVER (default vhost proxies the panel)
 MariaDB admin:  root@127.0.0.1  (password: $MDB_NOTE)
 PostgreSQL:     postgres        (password: $PG_NOTE)
 Mail:           $([ "$WITH_MAIL" = 1 ] && echo postfix+dovecot+opendkim || echo skipped)
 FTP:            $([ "$WITH_FTP" = 1 ] && echo "vsftpd — open TCP 21 and 30100-30120 (passive mode) in any external firewall" || echo skipped)
 fail2ban:       $([ "$WITH_FAIL2BAN" = 1 ] && echo yes || echo no)
 Docker:         $([ "$WITH_DOCKER" = 1 ] && echo "installed" || echo "not installed (admin can install it from Containers in the panel)")
 Binary:         $([ -x "$AEGIS_BIN" ] && echo "$AEGIS_BIN" || echo "(not built)")
 Panel:          $BOOTSTRAP_NOTE

 Panel access:
   URL:            http://${PANEL_IP}${BASE}
   Any hostname:   http://<your-domain>${BASE} — every generated vhost
                   (nginx/Apache/Caddy) proxies the /aegis prefix, and the
                   native Go web server falls through to the panel too
   Change prefix:  AEGIS_PANEL_BASE in /etc/systemd/system/${SERVICE_NAME}.service
                   (set it empty to serve the panel at /)
============================================================
EOF

# The generated admin password is printed once, in the user's terminal — it is
# wiped from /run after bootstrap and never written to any file.
if [ "$FIRST_BOOT" = 1 ]; then
cat <<EOF

 Admin account (password shown ONCE):
   username:  $ADMIN_USER
   password:  $ADMIN_PASSWORD
EOF
if [ "$WITH_BUILD" = 1 ] || [ -x "$AEGIS_BIN" ]; then
  echo "   The panel is running — log in at the URL above and change the password."
  echo "   Lost it later? Reset with:  aegisctl user reset-pass $ADMIN_USER -p '<new>'"
else
  echo "   Bootstrap manually when the binary is installed:"
  echo "     AEGIS_ADMIN_USER=$ADMIN_USER AEGIS_ADMIN_PASSWORD='$ADMIN_PASSWORD' \\"
  echo "       systemctl enable --now ${SERVICE_NAME}.service"
fi
echo "============================================================"
else
cat <<EOF

 Admin account: existing credentials kept (panel was already bootstrapped).
============================================================
EOF
fi

#!/usr/bin/env bash
#
# Aegis bare-metal installer — installs the complete hosting stack from distro
# packages, so end users don't have to hunt for anything by hand.
#
#   sudo scripts/install-stack.sh                       # everything
#   sudo scripts/install-stack.sh --php-versions 8.2,8.3
#   sudo scripts/install-stack.sh --no-mail --no-ftp
#   sudo scripts/install-stack.sh --with-caddy --skip-build
#
# What it does:
#   1. Installs PHP-FPM (default 7.4→8.4, sury.org on Debian/Ubuntu)
#   2. Installs MariaDB + PostgreSQL servers and seeds admin credentials
#   3. Installs a web server (nginx by default; apache/caddy optional)
#   4. Installs vsftpd, fail2ban, ffmpeg + poppler-utils, unzip, zip
#   5. Installs the mail stack (postfix+dovecot+opendkim) unless --no-mail
#   6. Installs Go from the distro (or downloads the toolchain) and builds
#      the aegis binary — unless --skip-build
#   7. Installs the panel as a systemd service and prints next steps
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
log "installing PHP-FPM: $PHP_VERSIONS"
if [ "$DIST_ID" = "debian" ] || dpkg -l sury-keyring >/dev/null 2>&1 || true; then
  apt_get update
  apt_get install -y ca-certificates curl gnupg lsb-release
  if ! grep -rq "packages.sury.org" /etc/apt/sources.list /etc/apt/sources.list.d/ 2>/dev/null; then
    if [ "$DIST_ID" = "debian" ]; then
      curl -fsSL "$SURY_REPO/apt.gpg" -o /etc/apt/trusted.gpg.d/php.gpg
      echo "deb $SURY_REPO/ $(lsb_release -sc) main" > /etc/apt/sources.list.d/php.list
    else
      add-apt-repository -y ppa:ondrej/php
    fi
    apt_get update
  fi
fi
PHP_PKGS=""
for v in $PHP_VERSIONS; do
  PHP_PKGS="$PHP_PKGS php$v-fpm php$v-cli php$v-common php$v-mysql php$v-pgsql php$v-curl php$v-gd php$v-xml php$v-mbstring php$v-zip php$v-intl php$v-bcmath php$v-imagick"
done
apt_get install $PHP_PKGS
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

# ----------------------------- 3. web server ---------------------------------
case "$WEB_SERVER" in
  nginx)  log "installing nginx";  apt_get install nginx;  systemctl enable --now nginx ;;
  apache) log "installing apache2"; apt_get install apache2; systemctl enable --now apache2 ;;
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

# ----------------------------- 6. systemd unit -------------------------------
if [ "$WITH_BUILD" = 1 ] || [ -x "$AEGIS_BIN" ]; then
  log "installing systemd unit $SERVICE_NAME.service"
  cat > "/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Aegis hosting control panel
After=network.target mariadb.service postgresql.service

[Service]
Type=simple
ExecStart=${AEGIS_BIN}
Environment=AEGIS_LISTEN=:8080
Environment=AEGIS_PANEL_BASE=${BASE}
Environment=AEGIS_MARIADB_PASSWORD=${MARIADB_PASSWORD:-}
Environment=AEGIS_POSTGRES_PASSWORD=${POSTGRES_PASSWORD:-}
Restart=on-failure
RestartSec=3
# The panel manages users/services; it must run as root (see docs/ARCHITECTURE.md).

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable "${SERVICE_NAME}.service"
  log "credentials written into the unit file (chmod 600 below)"
  chmod 600 "/etc/systemd/system/${SERVICE_NAME}.service"
fi

# ----------------------------- 7. summary ------------------------------------
PANEL_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
PANEL_IP=${PANEL_IP:-<server-ip>}
cat <<EOF

============================================================
 Aegis stack installed.
============================================================
 PHP-FPM:        $(for v in $PHP_VERSIONS; do printf "php$v "; done)
 Web server:     $WEB_SERVER (default vhost proxies the panel)
 MariaDB admin:  root@127.0.0.1  (password: ${MARIADB_PASSWORD:+set}${MARIADB_PASSWORD:-skipped})
 PostgreSQL:     postgres        (password: ${POSTGRES_PASSWORD:+set}${POSTGRES_PASSWORD:-skipped})
 Mail:           $([ "$WITH_MAIL" = 1 ] && echo postfix+dovecot+opendkim || echo skipped)
 FTP:            $([ "$WITH_FTP" = 1 ] && echo vsftpd || echo skipped)
 fail2ban:       $([ "$WITH_FAIL2BAN" = 1 ] && echo yes || echo no)
 Binary:         $([ -x "$AEGIS_BIN" ] && echo "$AEGIS_BIN" || echo "(not built)")

 Panel access:
   URL:            http://${PANEL_IP}${BASE}
   Any hostname:   http://<your-domain>${BASE} — every generated vhost
                   (nginx/Apache/Caddy) proxies the /aegis prefix, and the
                   native Go web server falls through to the panel too
   Change prefix:  AEGIS_PANEL_BASE in ${SERVICE_NAME}.service
                   (set it empty to serve the panel at /)
   Admin login:    created on first boot from AEGIS_ADMIN_USER +
                   AEGIS_ADMIN_PASSWORD — the login page shows no
                   default credentials

 Next steps:
   1. AEGIS_ADMIN_USER=admin AEGIS_ADMIN_PASSWORD='<secret>' \\
        systemctl start ${SERVICE_NAME}.service
      (admin env vars are read on first boot only, before the admin user exists)
   2. open the Panel access URL above
   3. run 'aegisctl setup' if you want guided tuning + admin creation
============================================================
EOF

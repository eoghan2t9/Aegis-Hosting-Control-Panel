#!/bin/bash
# Aegis dev container entrypoint.
#  1. moves Apache off :80 so nginx can own it
#  2. seeds MariaDB/PostgreSQL with the admin passwords from env
#  3. writes the vsftpd + php-fpm configs the panel expects
#  4. starts services via supervisor
#  5. runs the panel with `go run` (bind-mounted source => no image rebuild)
set -e

log() { echo "[aegis-entrypoint] $*"; }

# --- Apache off :80 (nginx owns 80/443) --------------------------------------
log "moving apache to :8081"
sed -i 's/^Listen 80$/Listen 8081/' /etc/apache2/ports.conf 2>/dev/null || true
mkdir -p /etc/apache2/conf-enabled
cat > /etc/apache2/conf-available/aegis-ports.conf <<'EOF'
Listen 8081
NameVirtualHost *:8081
EOF
a2enmod proxy_fcgi rewrite ssl headers >/dev/null 2>&1 || true

# --- php-fpm socket dir permissions -------------------------------------------
mkdir -p /run/php
chown -R www-data:www-data /run/php 2>/dev/null || true

# --- MariaDB root --------------------------------------------------------------
MARIADB_PW="${AEGIS_MARIADB_PASSWORD:-mariadb_dev}"
log "seeding mariadb (root password from AEGIS_MARIADB_PASSWORD)"
mkdir -p /run/mysqld && chown mysql:mysql /run/mysqld
if [ -d /var/lib/mysql/mysql ]; then
  # already initialised; just make sure root password matches env
  mariadbd --user=mysql --skip-networking --socket=/run/mysqld/mysqld.sock &
  MPID=$!
  for i in $(seq 1 30); do
    mariadb-admin --socket=/run/mysqld/mysqld.sock ping >/dev/null 2>&1 && break
    sleep 1
  done
  mariadb --socket=/run/mysqld/mysqld.sock -uroot \
    -e "ALTER USER 'root'@'localhost' IDENTIFIED BY '${MARIADB_PW}'; CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED BY '${MARIADB_PW}'; GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION; FLUSH PRIVILEGES;" 2>/dev/null || true
  kill $MPID 2>/dev/null || true
else
  # first boot: supervisor starts mariadbd, we set password after
  log "mariadb datadir uninitialised — supervisor will handle it"
fi

# --- PostgreSQL ---------------------------------------------------------------
PG_PW="${AEGIS_POSTGRES_PASSWORD:-postgres_dev}"
log "seeding postgres (password from AEGIS_POSTGRES_PASSWORD)"
PGBIN=$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | head -1 || true)
if [ -n "$PGBIN" ] && [ ! -f /var/lib/postgresql/.pw_set ]; then
  # Allow password auth over TCP so the panel's pgx client can connect.
  PGCONF=$(ls /etc/postgresql/*/main/postgresql.conf 2>/dev/null | head -1)
  PGHBA=$(ls /etc/postgresql/*/main/pg_hba.conf 2>/dev/null | head -1)
  if [ -n "$PGCONF" ] && [ -n "$PGHBA" ]; then
    sed -i "s/^#\?listen_addresses.*/listen_addresses = '*'/" "$PGCONF"
    sed -i "s/^\(host.*127\.0\.0\.1\/32.*\)md5/\1md5/" "$PGHBA"
    sed -i "s/^\(host.*127\.0\.0\.1\/32.*\)scram-sha-256/\1scram-sha-256/" "$PGHBA"
    grep -q "127.0.0.1/32" "$PGHBA" || echo "host all all 127.0.0.1/32 scram-sha-256" >> "$PGHBA"
  fi
  chown -R postgres:postgres /var/lib/postgresql 2>/dev/null || true
  touch /var/lib/postgresql/.pw_set
fi

# --- vsftpd --------------------------------------------------------------------
if [ ! -f /etc/vsftpd.conf ]; then
  log "writing default vsftpd.conf"
  cat > /etc/vsftpd.conf <<'EOF'
listen=YES
listen_ipv6=NO
anonymous_enable=NO
local_enable=YES
write_enable=YES
local_umask=022
dirmessage_enable=YES
use_localtime=YES
xferlog_enable=YES
connect_from_port_20=YES
chroot_local_user=YES
allow_writeable_chroot=YES
pasv_min_port=40000
pasv_max_port=40100
seccomp_sandbox=NO
EOF
fi

# --- nginx log dir --------------------------------------------------------------
mkdir -p /var/log/nginx

# --- start services -------------------------------------------------------------
log "starting services (nginx, apache, mariadb, postgres, vsftpd, php-fpm)"
exec /usr/bin/supervisord -c /etc/supervisor/supervisord.conf &

# wait for databases
for i in $(seq 1 30); do
  mariadb-admin -uroot -p"${MARIADB_PW}" ping >/dev/null 2>&1 && log "mariadb ready" && break
  sleep 1
done

# --- run the panel ---------------------------------------------------------------
MODE="${1:-dev}"
if [ "$MODE" = "dev" ]; then
  log "starting panel via 'go run' (source is bind-mounted; edit + restart the container for Go, refresh for frontend)"
  cd /app
  export AEGIS_DEV_WEB="${AEGIS_DEV_WEB:-/app/web}"
  export AEGIS_MARIADB_PASSWORD="$MARIADB_PW"
  export AEGIS_POSTGRES_PASSWORD="$PG_PW"
  while true; do
    go run ./cmd/aegis -dev
    log "panel exited ($?); restarting in 2s…"
    sleep 2
  done
elif [ "$MODE" = "shell" ]; then
  exec /bin/bash
else
  exec "$@"
fi

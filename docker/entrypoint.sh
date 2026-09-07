#!/bin/bash
# Aegis dev container entrypoint.
#  1. resolves the Go toolchain (host bind mount or image)
#  2. moves Apache off :80 so nginx can own it
#  3. seeds MariaDB/PostgreSQL with the admin passwords from env
#  4. writes the supervisor + vsftpd configs the panel expects
#  5. starts services via supervisor
#  6. runs the panel with `go run` (bind-mounted source => no image rebuild)
#
# The entrypoint, supervisor config and php-pool launcher are all bind-mounted
# from docker/ so editing them only needs `docker compose restart aegis`.
set -e

log() { echo "[aegis-entrypoint] $*"; }

# --- Go toolchain -------------------------------------------------------------
# Prefer the host toolchain bind-mounted at /usr/lib/go-1.26 + /usr/share/go-1.26
# (Debian layout: bin/pkg in /usr/lib, sources symlinked into /usr/share).
# Fall back to the image-baked /usr/local/go.
if [ -x /usr/lib/go-1.26/bin/go ]; then
  export GOROOT=/usr/lib/go-1.26
  export PATH=/usr/lib/go-1.26/bin:$PATH
  log "using dev Go toolchain from /usr/lib/go-1.26 ($(/usr/lib/go-1.26/bin/go version | awk '{print $3}'))"
else
  export GOROOT=/usr/local/go
  export PATH=/usr/local/go/bin:$PATH
  log "using image Go toolchain from /usr/local/go"
fi

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
# The php-pool launcher is bind-mounted (mode may be lost) — make it executable.
chmod +x /usr/local/share/aegis/php-pools.dev 2>/dev/null || true

# --- MariaDB root --------------------------------------------------------------
MARIADB_PW="${AEGIS_MARIADB_PASSWORD:-mariadb_dev}"
log "seeding mariadb (root password from AEGIS_MARIADB_PASSWORD)"
mkdir -p /run/mysqld && chown mysql:mysql /run/mysqld
if [ ! -d /var/lib/mysql/mysql ]; then
  # Empty named volume: initialise the system tables (matches image defaults).
  log "mariadb datadir empty — running mariadb-install-db"
  mariadb-install-db --user=mysql --datadir=/var/lib/mysql >/dev/null 2>&1 || true
fi
# Bring up a throwaway instance to set the root password, then let supervisor
# own the long-lived one.
mariadbd --user=mysql --skip-networking --socket=/run/mysqld/mysqld.sock &
MPID=$!
for i in $(seq 1 30); do
  mariadb-admin --socket=/run/mysqld/mysqld.sock ping >/dev/null 2>&1 && break
  sleep 1
done
mariadb --socket=/run/mysqld/mysqld.sock -uroot \
  -e "ALTER USER 'root'@'localhost' IDENTIFIED BY '${MARIADB_PW}'; CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED BY '${MARIADB_PW}'; GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION; FLUSH PRIVILEGES;" 2>/dev/null || true
kill $MPID 2>/dev/null || true
wait $MPID 2>/dev/null || true

# --- PostgreSQL ---------------------------------------------------------------
PG_PW="${AEGIS_POSTGRES_PASSWORD:-postgres_dev}"
log "seeding postgres (password from AEGIS_POSTGRES_PASSWORD)"
PGBIN=$(ls -d /usr/lib/postgresql/*/bin 2>/dev/null | head -1 || true)
PGCONF=""
PGDATA=""
if [ -n "$PGBIN" ]; then
  PGVER=$(basename "$(dirname "$PGBIN")")
  PGCONF="/etc/postgresql/${PGVER}/main/postgresql.conf"
  PGDATA="/var/lib/postgresql/${PGVER}/main"
  if [ ! -f "$PGDATA/PG_VERSION" ]; then
    # Fresh named volume: initdb into the empty datadir. /etc/postgresql is
    # baked in the image (not a volume), so config files already exist there.
    log "postgres datadir empty — running initdb for ${PGVER}"
    mkdir -p "/var/lib/postgresql/${PGVER}"
    chown -R postgres:postgres "/var/lib/postgresql/${PGVER}"
    cat > /tmp/pg-init.sh <<EOF
#!/bin/bash
set -e
exec '$PGBIN/initdb' -D '$PGDATA' --auth-local=peer --auth-host=scram-sha-256
EOF
    chmod 700 /tmp/pg-init.sh
    chown postgres:postgres /tmp/pg-init.sh
    su postgres -s /bin/sh -c /tmp/pg-init.sh 2>/dev/null || true
    rm -f /tmp/pg-init.sh
  fi
  # Allow password auth over TCP so the panel's pgx client can connect.
  if [ -f "$PGCONF" ]; then
    sed -i "s/^#\?listen_addresses.*/listen_addresses = '*'/" "$PGCONF"
    grep -q '^listen_addresses' "$PGCONF" || echo "listen_addresses = '*'" >> "$PGCONF"
  fi
  PGHBA="$(dirname "$PGCONF")/pg_hba.conf"
  if [ -f "$PGHBA" ]; then
    grep -q "127.0.0.1/32" "$PGHBA" || echo "host all all 127.0.0.1/32 scram-sha-256" >> "$PGHBA"
    sed -i 's/^\(host.*127\.0\.0\.1\/32.*\)md5/\1scram-sha-256/' "$PGHBA"
  fi
  # Supervisor cannot expand globs: write a concrete launcher for postgres.
  cat > /usr/local/bin/aegis-pg <<EOF
#!/bin/bash
exec su postgres -s /bin/sh -c "exec '$PGBIN/postgres' -D '$PGDATA' -c config_file='$PGCONF'"
EOF
  chmod +x /usr/local/bin/aegis-pg
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

# --- mail stack (Postfix + Dovecot + OpenDKIM) --------------------------------
# Virtual mailboxes: Postfix and Dovecot both query Aegis's own SQLite store
# directly (read-only), so the panel is the single source of truth — no
# credential-sync step. Only the panel process itself (same container,
# loopback) talks to these; they are not published to the host.
log "seeding mail stack (postfix/dovecot/opendkim)"

id -u vmail >/dev/null 2>&1 || groupadd -g 5000 vmail
id -u vmail >/dev/null 2>&1 || useradd -g vmail -u 5000 vmail -d /var/mail/vhosts -m -s /sbin/nologin
mkdir -p /var/mail/vhosts && chown -R vmail:vmail /var/mail/vhosts

AEGIS_DB="/var/lib/aegis/aegis.db"
# The panel (started later, after supervisord) creates this on first run;
# touch it now so Postfix/Dovecot's sqlite maps have a file to open even
# before the panel has run once.
mkdir -p "$(dirname "$AEGIS_DB")" && touch "$AEGIS_DB"
mkdir -p /etc/postfix
cat > /etc/postfix/sqlite-virtual-mailbox-domains.cf <<EOF
dbpath = ${AEGIS_DB}
query = SELECT 1 FROM mail_domains WHERE domain='%s'
EOF
cat > /etc/postfix/sqlite-virtual-mailbox-maps.cf <<EOF
dbpath = ${AEGIS_DB}
query = SELECT d.domain || '/' || m.localpart || '/' FROM mailboxes m JOIN mail_domains d ON d.id = m.mail_domain_id WHERE d.domain='%d' AND m.localpart='%u' AND m.enabled=1
EOF
cat > /etc/postfix/sqlite-virtual-alias-maps.cf <<EOF
dbpath = ${AEGIS_DB}
query = SELECT destination FROM mail_aliases a JOIN mail_domains d ON d.id = a.mail_domain_id WHERE d.domain='%d' AND a.source='%u'
EOF

cat > /etc/postfix/main.cf <<'EOF'
# Managed by Aegis (docker/entrypoint.sh). Do not edit by hand.
myhostname = aegis-dev.local
smtpd_banner = $myhostname ESMTP
biff = no
readme_directory = no
compatibility_level = 2
append_dot_mydomain = no
inet_interfaces = loopback-only
inet_protocols = ipv4
mydestination = localhost
mynetworks = 127.0.0.0/8 [::1]/128
maillog_file = /var/log/postfix.log

virtual_mailbox_domains = sqlite:/etc/postfix/sqlite-virtual-mailbox-domains.cf
virtual_mailbox_maps = sqlite:/etc/postfix/sqlite-virtual-mailbox-maps.cf
virtual_alias_maps = sqlite:/etc/postfix/sqlite-virtual-alias-maps.cf
virtual_mailbox_base = /var/mail/vhosts
virtual_uid_maps = static:5000
virtual_gid_maps = static:5000
virtual_minimum_uid = 5000
virtual_transport = virtual

smtpd_milters = inet:127.0.0.1:8891
non_smtpd_milters = inet:127.0.0.1:8891
milter_default_action = accept
milter_protocol = 6
EOF
# Postfix's default per-service chroot jail (master.cf's 5th column) can't
# see paths outside /var/spool/postfix, but the sqlite maps above point at
# /var/lib/aegis/aegis.db — so every service that runs a lookup (cleanup,
# smtp, etc.) must run unchrooted.
postconf -F '*/*/chroot=n' >/dev/null 2>&1 || true

# --- Dovecot: IMAP against the same sqlite-backed virtual mailboxes ------------
mkdir -p /etc/dovecot
cat > /etc/dovecot/dovecot-sql.conf.ext <<EOF
driver = sqlite
connect = ${AEGIS_DB}
default_pass_scheme = BLF-CRYPT
password_query = SELECT m.password_hash AS password FROM mailboxes m JOIN mail_domains d ON d.id = m.mail_domain_id WHERE d.domain = '%d' AND m.localpart = '%n' AND m.enabled = 1
user_query = SELECT '/var/mail/vhosts/%d/%n' AS home, 5000 AS uid, 5000 AS gid FROM mailboxes m JOIN mail_domains d ON d.id = m.mail_domain_id WHERE d.domain = '%d' AND m.localpart = '%n' AND m.enabled = 1
EOF
cat > /etc/dovecot/dovecot.conf <<'EOF'
# Managed by Aegis (docker/entrypoint.sh). Do not edit by hand.
protocols = imap
listen = 127.0.0.1
ssl = no
disable_plaintext_auth = no
auth_mechanisms = plain login
mail_location = maildir:/var/mail/vhosts/%d/%n
first_valid_uid = 5000
first_valid_gid = 5000

passdb {
  driver = sql
  args = /etc/dovecot/dovecot-sql.conf.ext
}
userdb {
  driver = sql
  args = /etc/dovecot/dovecot-sql.conf.ext
}

service imap-login {
  inet_listener imap {
    port = 143
  }
  inet_listener imaps {
    port = 0
  }
}
EOF

# --- OpenDKIM: signing config regenerated by the panel per mail-enabled domain -
mkdir -p /etc/opendkim/keys /run/opendkim
[ -f /etc/opendkim/KeyTable ] || : > /etc/opendkim/KeyTable
[ -f /etc/opendkim/SigningTable ] || : > /etc/opendkim/SigningTable
[ -f /etc/opendkim/TrustedHosts ] || printf '127.0.0.1\n::1\nlocalhost\n' > /etc/opendkim/TrustedHosts
cat > /etc/opendkim.conf <<'EOF'
# Managed by Aegis (docker/entrypoint.sh). Do not edit by hand.
Syslog yes
UMask 002
KeyTable /etc/opendkim/KeyTable
SigningTable /etc/opendkim/SigningTable
ExternalIgnoreList refile:/etc/opendkim/TrustedHosts
InternalHosts refile:/etc/opendkim/TrustedHosts
Socket inet:8891@127.0.0.1
PidFile /run/opendkim/opendkim.pid
Mode sv
EOF
id -u opendkim >/dev/null 2>&1 && chown -R opendkim:opendkim /etc/opendkim /run/opendkim

# --- nginx log dir --------------------------------------------------------------
mkdir -p /var/log/nginx

# --- start services -------------------------------------------------------------
# `docker compose restart` (as opposed to a full recreate) keeps the
# container's writable layer, so /run/*.pid files from the previous run are
# still there and make the actual daemons refuse to start ("already
# running") even though nothing is. Clear the ones supervisord launches.
rm -f /var/run/apache2/apache2.pid /run/dovecot/master.pid /var/spool/postfix/pid/master.pid /run/opendkim/opendkim.pid

log "starting services (nginx, apache, mariadb, postgres, vsftpd, php-fpm, postfix, dovecot, opendkim, cron)"
exec /usr/bin/supervisord -c /etc/supervisor/supervisord.conf &
SUPERVISOR_PID=$!

# wait for databases
for i in $(seq 1 30); do
  mariadb-admin -uroot -p"${MARIADB_PW}" ping >/dev/null 2>&1 && log "mariadb ready" && break
  sleep 1
done
if [ -n "$PGBIN" ]; then
  # Wait for postgres (peer auth over the unix socket), then set the TCP
  # password the panel uses to connect.
  for i in $(seq 1 30); do
    su postgres -s /bin/sh -c "'$PGBIN/psql' -c 'SELECT 1' >/dev/null 2>&1" && break
    sleep 1
  done
  su postgres -s /bin/sh -c "'$PGBIN/psql' -c \"ALTER USER postgres PASSWORD '${PG_PW}';\"" >/dev/null 2>&1 || true
  for i in $(seq 1 10); do
    su postgres -s /bin/sh -c "PGPASSWORD='${PG_PW}' '$PGBIN/psql' -h 127.0.0.1 -U postgres -c 'SELECT 1' >/dev/null 2>&1" && log "postgres ready (password set)" && break
    sleep 1
  done
fi

# --- run the panel ---------------------------------------------------------------
MODE="${1:-dev}"
if [ "$MODE" = "dev" ]; then
  log "starting panel via 'go run' (source is bind-mounted; edit + restart the container for Go, refresh for frontend)"
  cd /app
  export AEGIS_DEV_WEB="${AEGIS_DEV_WEB:-/app/web}"
  export AEGIS_MARIADB_PASSWORD="$MARIADB_PW"
  export AEGIS_POSTGRES_PASSWORD="$PG_PW"
  while true; do
    if go run ./cmd/aegis -dev; then
      log "panel exited cleanly; restarting in 2s…"
    else
      rc=$?
      log "panel exited ($rc); restarting in 2s…"
    fi
    sleep 2
  done
elif [ "$MODE" = "shell" ]; then
  exec /bin/bash
else
  exec "$@"
fi

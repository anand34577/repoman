#!/bin/sh
# Installs, upgrades, rolls back or removes RepoMan as a systemd service.
#
#   sudo ./install.sh                          install, or upgrade an existing installation
#   sudo ./install.sh --migrate-from SRC       also adopt existing data: a directory (./data, a Docker volume,
#                                              a copy from another server) or host:/path fetched over ssh
#   sudo ./install.sh --no-service             only install the binary (no systemd: Alpine, containers, your own supervisor)
#   sudo ./install.sh --rollback               go back to the version before the last upgrade
#   sudo ./install.sh --uninstall [--purge]    remove the service and binary; --purge also deletes data and settings
#   options: --force (allow a downgrade / replace existing data), --yes (do not ask)
#
# Run it from the extracted release archive (the `repoman` binary must be next to this script); --rollback and
# --uninstall also work from the copy it leaves in /usr/local/share/repoman. The one-line installer
# (scripts/get.sh) downloads the archive and runs this script.
#
# Layout:  /usr/local/bin/repoman           binary          (repoman.prev: the one before the last upgrade)
#          /etc/repoman/repoman.env         settings (never overwritten on upgrade)
#          /var/lib/repoman                 data directory (database + artifacts; backups/ = database backups)
#          /etc/systemd/system/repoman.service
#          /usr/local/share/repoman         this installer, for rollback and uninstall
set -eu

BIN=/usr/local/bin/repoman
ETC=/etc/repoman
ENVFILE=$ETC/repoman.env
DATA=/var/lib/repoman
UNIT=/etc/systemd/system/repoman.service
DOC=/usr/local/share/doc/repoman
SHARE=/usr/local/share/repoman
HERE="$(cd "$(dirname "$0")" && pwd)"

# shellcheck source=../lib.sh
. "$HERE/lib.sh"

ACTION=install MIGRATE="" FORCE=0 YES=0 PURGE=0 NO_SERVICE=0
while [ $# -gt 0 ]; do
  case "$1" in
    --uninstall) ACTION=uninstall ;;
    --rollback) ACTION=rollback ;;
    --migrate-from) [ $# -ge 2 ] || die "--migrate-from needs a directory or host:/path"; MIGRATE="$2"; shift ;;
    --migrate-from=*) MIGRATE="${1#*=}" ;;
    --no-service) NO_SERVICE=1 ;;
    --purge) PURGE=1 ;;
    --force) FORCE=1 ;;
    --yes|-y) YES=1 ;;
    -h|--help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
  shift
done

[ "$(id -u)" -eq 0 ] || die "run this script as root (sudo $0)"

svc_stop() { systemctl stop repoman 2>/dev/null || true; }
svc_start() { systemctl enable --now repoman; }
fix_owner() { chown -R repoman:repoman "$1"; }

if [ "$ACTION" = rollback ]; then
  command -v systemctl >/dev/null 2>&1 || die "systemd is required for a rollback"
  do_rollback
  exit 0
fi

if [ "$ACTION" = uninstall ]; then
  systemctl disable --now repoman 2>/dev/null || true
  rm -f "$UNIT" "$BIN" "$BIN.prev"
  rm -rf "$DOC" "$SHARE"
  command -v systemctl >/dev/null 2>&1 && systemctl daemon-reload || true
  if [ "$PURGE" = 1 ]; then
    confirm "Delete ALL RepoMan data in $DATA and settings in $ETC? This cannot be undone."
    rm -rf "$DATA" "$ETC"
    say "RepoMan and all its data were removed."
  else
    say "RepoMan removed. Data in $DATA and settings in $ETC were kept (add --purge to delete them too)."
  fi
  exit 0
fi

[ -f "$HERE/repoman" ] || die "the repoman binary was not found next to this script ($HERE). Run this from the extracted release archive."
NEW="$(rm_version "$HERE/repoman")"
[ -n "$NEW" ] || die "$HERE/repoman does not run on this machine: did you download the archive for the right CPU architecture? (uname -m: $(uname -m))"
OLD=""
[ -x "$BIN" ] && OLD="$(rm_version "$BIN")"

if [ "$NO_SERVICE" = 1 ]; then
  install -m 0755 "$HERE/repoman" "$BIN"
  say "installed $BIN ($NEW). Not registered as a service. Run it with:  RM_DATA_DIR=/path/to/data $BIN serve"
  exit 0
fi

command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found). Re-run with --no-service to install only the binary and run it under your own supervisor: see docs/installation.md"

if [ -n "$OLD" ] && ver_gt "$OLD" "$NEW" && [ "$FORCE" != 1 ]; then
  die "$OLD is installed and this archive is the older $NEW. Upgrades change the database and cannot be undone, so downgrading is refused (use --rollback right after an upgrade, or --force)."
fi
[ -z "$MIGRATE" ] || precheck_migrate

if ! id repoman >/dev/null 2>&1; then
  NOLOGIN="$(command -v nologin || echo /usr/sbin/nologin)"
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --home-dir "$DATA" --no-create-home --shell "$NOLOGIN" repoman
  elif command -v adduser >/dev/null 2>&1; then # Alpine / BusyBox
    addgroup -S repoman 2>/dev/null || true
    adduser -S -D -H -h "$DATA" -s "$NOLOGIN" -G repoman repoman
  else
    die "cannot create the repoman system user: neither useradd nor adduser is available"
  fi
  say "created system user: repoman"
fi

UPGRADE=no
if [ -n "$OLD" ]; then UPGRADE=yes; fi
if systemctl is-active --quiet repoman 2>/dev/null; then
  say "stopping the running service"
  systemctl stop repoman
fi

install -d -m 0750 -o root -g repoman "$ETC"
install -d -m 0700 -o repoman -g repoman "$DATA"
if [ -n "$MIGRATE" ]; then
  do_migrate "$MIGRATE"
elif [ "$UPGRADE" = yes ] && [ "$OLD" != "$NEW" ]; then
  backup_db "$OLD"
fi
if [ "$OLD" != "$NEW" ]; then keep_previous_binary; fi

install -m 0755 "$HERE/repoman" "$BIN"
if [ ! -f "$ENVFILE" ]; then
  sed "s|^RM_DATA_DIR=.*|RM_DATA_DIR=$DATA|" "$HERE/repoman.env.example" >"$ENVFILE"
  chown root:repoman "$ENVFILE"
  chmod 0640 "$ENVFILE"
  say "wrote $ENVFILE"
else
  say "kept existing $ENVFILE"
fi
install -m 0644 "$HERE/repoman.service" "$UNIT"
if [ -d "$HERE/docs" ]; then
  install -d -m 0755 "$DOC"
  cp "$HERE"/docs/*.md "$DOC/"
fi
if [ "$HERE" != "$SHARE" ]; then # leave the installer behind for rollback / uninstall / re-migration
  install -d -m 0755 "$SHARE"
  install -m 0755 "$HERE/install.sh" "$SHARE/install.sh"
  install -m 0644 "$HERE/lib.sh" "$HERE/repoman.service" "$HERE/repoman.env.example" "$SHARE/"
fi

systemctl daemon-reload
systemctl enable --now repoman

echo
"$BIN" version
if wait_healthy; then
  if [ "$UPGRADE" = yes ] && [ "$OLD" != "$NEW" ]; then say "RepoMan was upgraded from $OLD to $NEW and is running."
  elif [ "$UPGRADE" = yes ]; then say "RepoMan $NEW was reinstalled and is running."
  else say "RepoMan is running."; fi
else
  warn "RepoMan was installed but is not answering yet. Check:  journalctl -u repoman -e"
  if [ "$UPGRADE" = yes ] && [ "$OLD" != "$NEW" ]; then say "To go back to $OLD:  sudo $SHARE/install.sh --rollback"; fi
fi
ADDR="$(sed -n 's/^RM_ADDR=//p' "$ENVFILE" | tail -1)"
echo "  Open:      http://<this-host>${ADDR:-:8081}"
echo "  Settings:  $ENVFILE   (then: systemctl restart repoman)"
echo "  Logs:      journalctl -u repoman -f"
if [ "$UPGRADE" = no ] && [ -z "$MIGRATE" ] && [ -f "$DATA/initial-admin-password" ]; then
  echo "  Sign in as \"admin\" with the password in $DATA/initial-admin-password:"
  echo "             sudo cat $DATA/initial-admin-password"
fi
if [ -n "$MIGRATE" ]; then echo "  Sign in with the accounts from the migrated data."; fi

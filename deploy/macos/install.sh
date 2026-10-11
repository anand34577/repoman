#!/bin/sh
# Installs, upgrades, rolls back or removes RepoMan as a launchd service that starts at boot.
#
#   sudo ./install.sh                          install, or upgrade an existing installation
#   sudo ./install.sh --migrate-from SRC       also adopt existing data: a directory (./data, a copy from another
#                                              server) or host:/path fetched over ssh
#   sudo ./install.sh --no-service             only install the binary (run it yourself)
#   sudo ./install.sh --rollback               go back to the version before the last upgrade
#   sudo ./install.sh --uninstall [--purge]    remove the service and binary; --purge also deletes data and settings
#   options: --force (allow a downgrade / replace existing data), --yes (do not ask)
#
# Run it from the extracted release archive (the `repoman` binary must be next to this script); --rollback and
# --uninstall also work from the copy it leaves in /usr/local/share/repoman. The one-line installer
# (scripts/get.sh) downloads the archive and runs this script.
# The service runs as the user who invoked sudo, never as root.
#
# Layout:  /usr/local/bin/repoman                    binary   (repoman.prev: the one before the last upgrade)
#          /usr/local/etc/repoman/repoman.env        settings (never overwritten on upgrade)
#          /usr/local/var/repoman                    data directory (database + artifacts; backups/ = database backups)
#          /usr/local/var/log/repoman.log            log
#          /Library/LaunchDaemons/com.repoman.server.plist
#          /usr/local/share/repoman                  this installer, for rollback and uninstall
set -eu

LABEL=com.repoman.server
BIN=/usr/local/bin/repoman
ETC=/usr/local/etc/repoman
ENVFILE=$ETC/repoman.env
DATA=/usr/local/var/repoman
LOGDIR=/usr/local/var/log
PLIST=/Library/LaunchDaemons/$LABEL.plist
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

[ "$(id -u)" -eq 0 ] || die "run this script with sudo"

svc_stop() { launchctl bootout system/$LABEL 2>/dev/null || true; }
svc_start() { launchctl bootstrap system "$PLIST"; }
# the service's account: the owner of the data directory (or, on a first install, the person who ran sudo)
fix_owner() { chown -R "${RUN_AS:-$(ls -ld "$DATA" | awk '{print $3}')}:${RUN_GROUP:-$(ls -ld "$DATA" | awk '{print $4}')}" "$1"; }

if [ "$ACTION" = rollback ]; then
  do_rollback
  exit 0
fi

if [ "$ACTION" = uninstall ]; then
  svc_stop
  rm -f "$PLIST" "$BIN" "$BIN.prev"
  rm -rf "$SHARE"
  if [ "$PURGE" = 1 ]; then
    confirm "Delete ALL RepoMan data in $DATA and settings in $ETC? This cannot be undone."
    rm -rf "$DATA" "$ETC" "$LOGDIR/repoman.log"
    say "RepoMan and all its data were removed."
  else
    say "RepoMan removed. Data in $DATA and settings in $ETC were kept (add --purge to delete them too)."
  fi
  exit 0
fi

[ -f "$HERE/repoman" ] || die "the repoman binary was not found next to this script ($HERE). Run this from the extracted release archive."
# Files downloaded with a browser are quarantined by Gatekeeper; clear the flag on the binary.
xattr -d com.apple.quarantine "$HERE/repoman" 2>/dev/null || true
NEW="$(rm_version "$HERE/repoman")"
[ -n "$NEW" ] || die "$HERE/repoman does not run on this Mac: download the arm64 archive for Apple silicon or the amd64 archive for Intel (uname -m: $(uname -m))"
OLD=""
[ -x "$BIN" ] && OLD="$(rm_version "$BIN")"

if [ "$NO_SERVICE" = 1 ]; then
  mkdir -p /usr/local/bin
  install -m 0755 "$HERE/repoman" "$BIN"
  say "installed $BIN ($NEW). Not registered as a service. Run it with:  RM_DATA_DIR=/path/to/data $BIN serve"
  exit 0
fi

RUN_AS="${SUDO_USER:-}"
[ -n "$RUN_AS" ] && [ "$RUN_AS" != root ] || die "run this with sudo from your normal account (the service runs as that account, not as root)"
RUN_GROUP="$(id -gn "$RUN_AS")"

if [ -n "$OLD" ] && ver_gt "$OLD" "$NEW" && [ "$FORCE" != 1 ]; then
  die "$OLD is installed and this archive is the older $NEW. Upgrades change the database and cannot be undone, so downgrading is refused (use --rollback right after an upgrade, or --force)."
fi
[ -z "$MIGRATE" ] || precheck_migrate

svc_stop

mkdir -p /usr/local/bin "$ETC" "$DATA" "$LOGDIR"
chown "$RUN_AS:$RUN_GROUP" "$DATA"
chmod 0700 "$DATA"
UPGRADE=no
if [ -n "$OLD" ]; then UPGRADE=yes; fi
if [ -n "$MIGRATE" ]; then
  do_migrate "$MIGRATE"
elif [ "$UPGRADE" = yes ] && [ "$OLD" != "$NEW" ]; then
  backup_db "$OLD"
fi
if [ "$OLD" != "$NEW" ]; then keep_previous_binary; fi

install -m 0755 "$HERE/repoman" "$BIN"
touch "$LOGDIR/repoman.log"
chown "$RUN_AS:$RUN_GROUP" "$LOGDIR/repoman.log"
chmod 0600 "$LOGDIR/repoman.log"
if [ ! -f "$ENVFILE" ]; then
  sed "s|^RM_DATA_DIR=.*|RM_DATA_DIR=$DATA|" "$HERE/repoman.env.example" >"$ENVFILE"
  chown "root:$RUN_GROUP" "$ENVFILE"
  chmod 0640 "$ENVFILE"
  say "wrote $ENVFILE"
else
  say "kept existing $ENVFILE"
fi
sed "s|@USER@|$RUN_AS|" "$HERE/$LABEL.plist" >"$PLIST"
chown root:wheel "$PLIST"
chmod 0644 "$PLIST"
if [ "$HERE" != "$SHARE" ]; then # leave the installer behind for rollback / uninstall / re-migration
  mkdir -p "$SHARE"
  install -m 0755 "$HERE/install.sh" "$SHARE/install.sh"
  install -m 0644 "$HERE/lib.sh" "$HERE/$LABEL.plist" "$HERE/repoman.env.example" "$SHARE/"
fi

svc_start

echo
"$BIN" version
if wait_healthy; then
  if [ "$UPGRADE" = yes ] && [ "$OLD" != "$NEW" ]; then say "RepoMan was upgraded from $OLD to $NEW and is running as $RUN_AS."
  elif [ "$UPGRADE" = yes ]; then say "RepoMan $NEW was reinstalled and is running as $RUN_AS."
  else say "RepoMan is running as $RUN_AS."; fi
else
  warn "RepoMan was installed but is not answering yet. Check:  tail -50 $LOGDIR/repoman.log"
  if [ "$UPGRADE" = yes ] && [ "$OLD" != "$NEW" ]; then say "To go back to $OLD:  sudo $SHARE/install.sh --rollback"; fi
fi
ADDR="$(sed -n 's/^RM_ADDR=//p' "$ENVFILE" | tail -1)"
echo "  Open:      http://localhost${ADDR:-:8081}"
echo "  Settings:  $ENVFILE   (then: sudo launchctl kickstart -k system/$LABEL)"
echo "  Logs:      tail -f $LOGDIR/repoman.log"
if [ "$UPGRADE" = no ] && [ -z "$MIGRATE" ] && [ -f "$DATA/initial-admin-password" ]; then
  echo "  Sign in as \"admin\" with the password in $DATA/initial-admin-password"
fi
if [ -n "$MIGRATE" ]; then echo "  Sign in with the accounts from the migrated data."; fi

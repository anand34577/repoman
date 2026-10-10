#!/bin/sh
# Installs (or upgrades) RepoMan as a launchd service that starts at boot.
#
#   sudo ./install.sh              install or upgrade
#   sudo ./install.sh --uninstall  remove the service and binary; data and settings are kept
#
# Run it from the extracted release archive (the `repoman` binary must be next to this script).
# The service runs as the user who invoked sudo, never as root.
#
# Layout:  /usr/local/bin/repoman                    binary
#          /usr/local/etc/repoman/repoman.env        settings (never overwritten on upgrade)
#          /usr/local/var/repoman                    data directory (database + artifacts)
#          /usr/local/var/log/repoman.log            log
#          /Library/LaunchDaemons/com.repoman.server.plist
set -eu

LABEL=com.repoman.server
BIN=/usr/local/bin/repoman
ETC=/usr/local/etc/repoman
DATA=/usr/local/var/repoman
LOGDIR=/usr/local/var/log
PLIST=/Library/LaunchDaemons/$LABEL.plist
HERE="$(cd "$(dirname "$0")" && pwd)"

die() { echo "error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run this script with sudo"

if [ "${1:-}" = "--uninstall" ]; then
  launchctl bootout system/$LABEL 2>/dev/null || true
  rm -f "$PLIST" "$BIN"
  echo "RepoMan removed. Data in $DATA and settings in $ETC were kept; delete them yourself if you no longer need them."
  exit 0
fi

RUN_AS="${SUDO_USER:-}"
[ -n "$RUN_AS" ] && [ "$RUN_AS" != root ] || die "run this with sudo from your normal account (the service runs as that account, not as root)"
RUN_GROUP="$(id -gn "$RUN_AS")"
[ -f "$HERE/repoman" ] || die "the repoman binary was not found next to this script ($HERE)"

# Files downloaded with a browser are quarantined by Gatekeeper; clear the flag on the binary.
xattr -d com.apple.quarantine "$HERE/repoman" 2>/dev/null || true
"$HERE/repoman" version >/dev/null 2>&1 || die "$HERE/repoman does not run on this Mac: download the arm64 archive for Apple silicon or the amd64 archive for Intel (uname -m: $(uname -m))"

launchctl bootout system/$LABEL 2>/dev/null || true

mkdir -p /usr/local/bin "$ETC" "$DATA" "$LOGDIR"
install -m 0755 "$HERE/repoman" "$BIN"
chown "$RUN_AS:$RUN_GROUP" "$DATA"
chmod 0700 "$DATA"
touch "$LOGDIR/repoman.log"
chown "$RUN_AS:$RUN_GROUP" "$LOGDIR/repoman.log"
chmod 0600 "$LOGDIR/repoman.log"
if [ ! -f "$ETC/repoman.env" ]; then
  sed "s|^RM_DATA_DIR=.*|RM_DATA_DIR=$DATA|" "$HERE/repoman.env.example" >"$ETC/repoman.env"
  chown "root:$RUN_GROUP" "$ETC/repoman.env"
  chmod 0640 "$ETC/repoman.env"
  echo "wrote $ETC/repoman.env"
else
  echo "kept existing $ETC/repoman.env"
fi
sed "s|@USER@|$RUN_AS|" "$HERE/$LABEL.plist" >"$PLIST"
chown root:wheel "$PLIST"
chmod 0644 "$PLIST"

launchctl bootstrap system "$PLIST"

i=0
while [ $i -lt 20 ]; do
  if "$BIN" healthcheck --config "$ETC/repoman.env" >/dev/null 2>&1; then break; fi
  i=$((i + 1))
  sleep 1
done

echo
"$BIN" version
if "$BIN" healthcheck --config "$ETC/repoman.env" >/dev/null 2>&1; then
  echo "RepoMan is running as $RUN_AS."
else
  echo "RepoMan was installed but is not answering yet. Check:  tail -50 $LOGDIR/repoman.log"
fi
ADDR="$(sed -n 's/^RM_ADDR=//p' "$ETC/repoman.env" | tail -1)"
echo "  Open:      http://localhost${ADDR:-:8081}"
echo "  Settings:  $ETC/repoman.env   (then: sudo launchctl kickstart -k system/$LABEL)"
echo "  Logs:      tail -f $LOGDIR/repoman.log"
if [ -f "$DATA/initial-admin-password" ]; then
  echo "  Sign in as \"admin\" with the password in $DATA/initial-admin-password"
fi

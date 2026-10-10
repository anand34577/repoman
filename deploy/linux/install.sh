#!/bin/sh
# Installs (or upgrades) RepoMan as a systemd service.
#
#   sudo ./install.sh              install or upgrade
#   sudo ./install.sh --uninstall  remove the service and binary; data and settings are kept
#
# Run it from the extracted release archive (the `repoman` binary must be next to this script).
#
# Layout:  /usr/local/bin/repoman           binary
#          /etc/repoman/repoman.env         settings (never overwritten on upgrade)
#          /var/lib/repoman                 data directory (database + artifacts)
#          /etc/systemd/system/repoman.service
set -eu

BIN=/usr/local/bin/repoman
ETC=/etc/repoman
DATA=/var/lib/repoman
UNIT=/etc/systemd/system/repoman.service
DOC=/usr/local/share/doc/repoman
HERE="$(cd "$(dirname "$0")" && pwd)"

die() { echo "error: $*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run this script as root (sudo $0)"
command -v systemctl >/dev/null 2>&1 || die "systemd is required (systemctl not found). Run the binary under your own service manager instead: see docs/installation.md"

if [ "${1:-}" = "--uninstall" ]; then
  systemctl disable --now repoman 2>/dev/null || true
  rm -f "$UNIT" "$BIN"
  rm -rf "$DOC"
  systemctl daemon-reload
  echo "RepoMan removed. Data in $DATA and settings in $ETC were kept; delete them yourself if you no longer need them."
  exit 0
fi

[ -f "$HERE/repoman" ] || die "the repoman binary was not found next to this script ($HERE)"
"$HERE/repoman" version >/dev/null 2>&1 || die "$HERE/repoman does not run on this machine: did you download the archive for the right CPU architecture? (uname -m: $(uname -m))"

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
  echo "created system user: repoman"
fi

UPGRADE=no
if systemctl is-active --quiet repoman 2>/dev/null; then
  UPGRADE=yes
  echo "stopping the running service for the upgrade"
  systemctl stop repoman
fi

install -m 0755 "$HERE/repoman" "$BIN"
install -d -m 0750 -o root -g repoman "$ETC"
install -d -m 0700 -o repoman -g repoman "$DATA"
if [ ! -f "$ETC/repoman.env" ]; then
  sed "s|^RM_DATA_DIR=.*|RM_DATA_DIR=$DATA|" "$HERE/repoman.env.example" >"$ETC/repoman.env"
  chown root:repoman "$ETC/repoman.env"
  chmod 0640 "$ETC/repoman.env"
  echo "wrote $ETC/repoman.env"
else
  echo "kept existing $ETC/repoman.env"
fi
install -m 0644 "$HERE/repoman.service" "$UNIT"
if [ -d "$HERE/docs" ]; then
  install -d -m 0755 "$DOC"
  cp "$HERE"/docs/*.md "$DOC/"
fi

systemctl daemon-reload
systemctl enable --now repoman

# wait for the server to answer
i=0
while [ $i -lt 20 ]; do
  if RM_CONFIG="$ETC/repoman.env" "$BIN" healthcheck >/dev/null 2>&1; then break; fi
  i=$((i + 1))
  sleep 1
done

echo
"$BIN" version
if RM_CONFIG="$ETC/repoman.env" "$BIN" healthcheck >/dev/null 2>&1; then
  echo "RepoMan is running."
else
  echo "RepoMan was installed but is not answering yet. Check:  journalctl -u repoman -e"
fi
ADDR="$(sed -n 's/^RM_ADDR=//p' "$ETC/repoman.env" | tail -1)"
echo "  Open:      http://<this-host>${ADDR:-:8081}"
echo "  Settings:  $ETC/repoman.env   (then: systemctl restart repoman)"
echo "  Logs:      journalctl -u repoman -f"
if [ "$UPGRADE" = no ] && [ -f "$DATA/initial-admin-password" ]; then
  echo "  Sign in as \"admin\" with the password in $DATA/initial-admin-password:"
  echo "             sudo cat $DATA/initial-admin-password"
fi

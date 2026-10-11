#!/bin/sh
# RepoMan one-line installer for Linux and macOS: downloads the right release for this machine, verifies its
# checksum and installs it as a service (systemd on Linux, launchd on macOS). Run it again to upgrade.
#
#   curl -fsSL https://raw.githubusercontent.com/anand34577/repoman/main/scripts/get.sh | sudo sh
#
# More:  ... | sudo sh -s -- <command> [options]
#
#   (none) | install | upgrade   install, or upgrade to the latest release (data and settings are kept; the
#                                database is backed up first)
#   check                        say whether a newer release exists; changes nothing
#   migrate-from SRC             install and adopt existing data. SRC is a directory (./data, /var/lib/repoman,
#                                a Docker volume) or host:/path on another machine, fetched over ssh
#   rollback                     go back to the version before the last upgrade (restores the database too)
#   uninstall [--purge]          remove the service; --purge also deletes data and settings
#
# Options:  --version vX.Y.Z    install that release instead of the latest   (or RM_VERSION=vX.Y.Z)
#           --no-service        only install the binary: no systemd/launchd (Alpine, containers, own supervisor)
#           --force             allow a downgrade, reinstall the same version, replace existing data on migrate
#           --yes               do not ask for confirmation
#
# The release archive holds the binary and the same installer you can run by hand (install.sh), so this script
# only fetches, verifies and hands over. RM_REPO=owner/name installs from a fork; RM_BASE_URL=https://host/path
# installs from a mirror (see --help).
set -eu

REPO="${RM_REPO:-anand34577/repoman}"
SHARE=/usr/local/share/repoman
BIN=/usr/local/bin/repoman

say()  { printf '%s\n' "$*"; }
step() { printf '==> %s\n' "$*"; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'USAGE'
RepoMan installer for Linux and macOS.

  curl -fsSL https://raw.githubusercontent.com/anand34577/repoman/main/scripts/get.sh | sudo sh
  curl -fsSL https://raw.githubusercontent.com/anand34577/repoman/main/scripts/get.sh | sudo sh -s -- <command> [options]

Commands:
  (none) | install | upgrade   install, or upgrade to the latest release (data and settings are kept;
                               the database is backed up first)
  check                        say whether a newer release exists; changes nothing
  migrate-from SRC             install and adopt existing data: a directory (./data, /var/lib/repoman, a Docker
                               volume) or host:/path on another machine, fetched over ssh
  rollback                     go back to the version before the last upgrade (restores the database too)
  uninstall [--purge]          remove the service; --purge also deletes data and settings

Options:
  --version vX.Y.Z   install that release instead of the latest   (or RM_VERSION=vX.Y.Z)
  --no-service       only install the binary: no systemd/launchd (Alpine, containers, your own supervisor)
  --force            allow a downgrade, reinstall the same version, replace existing data on migrate
  --yes              do not ask for confirmation

Environment: RM_VERSION, RM_REPO=owner/name (a fork), RM_BASE_URL=https://mirror.example/repoman (a mirror or
air-gapped file server laid out as <url>/latest, <url>/vX.Y.Z/<archive>, <url>/vX.Y.Z/SHA256SUMS).
USAGE
}

# ---------- arguments ----------
CMD=install VERSION="${RM_VERSION:-}" MIGRATE="" PASS=""
while [ $# -gt 0 ]; do
  case "$1" in
    install|upgrade|update) CMD=install ;;
    check) CMD=check ;;
    rollback) CMD=rollback ;;
    uninstall|remove) CMD=uninstall ;;
    migrate-from|--migrate-from)
      [ $# -ge 2 ] || die "migrate-from needs a directory or host:/path"
      CMD=install MIGRATE="$2"; shift ;;
    --migrate-from=*) CMD=install MIGRATE="${1#*=}" ;;
    --version) [ $# -ge 2 ] || die "--version needs a value like v1.6.0"; VERSION="$2"; shift ;;
    --version=*) VERSION="${1#*=}" ;;
    --no-service|--force|--yes|-y|--purge) PASS="$PASS $1" ;;
    -h|--help|help) usage; exit 0 ;;
    *) die "unknown argument: $1 (try: sh get.sh --help)" ;;
  esac
  shift
done

# ---------- tools ----------
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$1" "$2"; }
  fetch_out() { curl -fsSL --retry 3 "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$1" "$2"; }
  fetch_out() { wget -q -O - "$1"; }
else
  die "curl or wget is required"
fi
command -v tar >/dev/null 2>&1 || die "tar is required"
if command -v sha256sum >/dev/null 2>&1; then sha_check() { sha256sum -c -; }
elif command -v shasum >/dev/null 2>&1; then sha_check() { shasum -a 256 -c -; }
else die "sha256sum or shasum is required to verify the download"; fi

# ---------- privileges (like get.docker.com: sudo -E, su, or already root) ----------
if [ "$(id -u)" = 0 ]; then sh_c='sh -c'
elif command -v sudo >/dev/null 2>&1; then sh_c='sudo -E sh -c'
elif command -v su >/dev/null 2>&1; then sh_c='su root -c'
else die "this needs root, and neither sudo nor su is available"; fi

# ---------- platform ----------
os=$(uname -s)
case "$os" in
  Linux) osname=linux ;;
  Darwin) osname=macos ;;
  *) die "this installer supports Linux and macOS (this is $os). Windows: see docs/installation.md (install.ps1)" ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  armv7l|armv7) arch=armv7 ;;
  armv6l|armv6) arch=armv6 ;;
  *) die "unsupported CPU architecture: $arch (releases exist for amd64, arm64, armv7 and armv6)" ;;
esac
if [ "$osname" = macos ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
  arch=arm64 # a shell running under Rosetta on an Apple silicon Mac: install the native build
fi
if [ "$osname" = macos ] && [ "$arch" != amd64 ] && [ "$arch" != arm64 ]; then die "unsupported CPU architecture for macOS: $arch"; fi

# ver_gt A B: succeeds when version A is newer than B (numeric parts; a "-rc1" suffix is ignored)
ver_gt() {
  _a="${1%%[-+]*}"; _b="${2%%[-+]*}"
  while [ -n "$_a" ] || [ -n "$_b" ]; do
    _x="${_a%%.*}"; _y="${_b%%.*}"
    case "$_a" in *.*) _a="${_a#*.}" ;; *) _a="" ;; esac
    case "$_b" in *.*) _b="${_b#*.}" ;; *) _b="" ;; esac
    _x="${_x:-0}"; _y="${_y:-0}"
    if [ "$_x" -gt "$_y" ] 2>/dev/null; then return 0; fi
    if [ "$_x" -lt "$_y" ] 2>/dev/null; then return 1; fi
  done
  return 1
}

installed=""
if [ -x "$BIN" ]; then installed=$("$BIN" version 2>/dev/null | sed -n '1s/^RepoMan \([^ ]*\).*/\1/p' || true); fi

# ---------- commands that work with what is already installed ----------
run_installed() { # rollback / uninstall use the installer left behind by the last install
  [ -x "$SHARE/install.sh" ] || return 1
  step "Running $SHARE/install.sh"
  # shellcheck disable=SC2086
  $sh_c "'$SHARE/install.sh' $1 $PASS"
}

if [ "$CMD" = rollback ]; then
  [ -x "$SHARE/install.sh" ] || die "RepoMan was not installed by this installer ($SHARE/install.sh is missing), so there is nothing to roll back"
  run_installed --rollback
  exit $?
fi
if [ "$CMD" = uninstall ] && [ -x "$SHARE/install.sh" ]; then
  run_installed --uninstall
  exit $?
fi # not installed by us: fall through, the release archive carries the installer

# ---------- which release ----------
latest_tag() {
  if [ -n "${RM_BASE_URL:-}" ]; then fetch_out "${RM_BASE_URL%/}/latest" | head -n 1 | tr -d ' 
'; return 0; fi
  # the redirect of /releases/latest names the tag and needs no API quota
  if command -v curl >/dev/null 2>&1; then
    _u=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" 2>/dev/null || true)
    case "$_u" in */releases/tag/*) printf '%s\n' "${_u##*/}"; return 0 ;; esac
  fi
  fetch_out "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

if [ -z "$VERSION" ]; then
  step "Looking up the latest release"
  VERSION=$(latest_tag)
  [ -n "$VERSION" ] || die "could not find the latest release of $REPO. Check your connection, or name one: --version v1.6.0"
fi
case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
num="${VERSION#v}"

if [ "$CMD" = check ]; then
  say "installed: ${installed:-not installed}"
  say "latest:    $num"
  if [ -z "$installed" ]; then say "Install it with:  curl -fsSL https://raw.githubusercontent.com/$REPO/main/scripts/get.sh | sudo sh"
  elif [ "$installed" = "$num" ]; then say "RepoMan is up to date."
  else say "A different release is available. Upgrade with:  curl -fsSL https://raw.githubusercontent.com/$REPO/main/scripts/get.sh | sudo sh"; fi
  exit 0
fi

case "$PASS" in
  *--force*) ;;
  *) if [ -n "$installed" ] && ver_gt "$installed" "$num"; then
       die "RepoMan $installed is installed and $num is older. Upgrades change the database and cannot be undone, so downgrading is refused. Use 'rollback' right after an upgrade, or add --force."
     fi
     if [ "$installed" = "$num" ] && [ -z "$MIGRATE" ] && [ "$CMD" = install ]; then
       say "RepoMan $num is already installed. Nothing to do (add --force to reinstall it)."
       exit 0
     fi ;;
esac

# ---------- download, verify, unpack ----------
archive="repoman_${num}_${osname}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"
[ -z "${RM_BASE_URL:-}" ] || base="${RM_BASE_URL%/}/$VERSION"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/repoman-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT INT TERM

if [ -n "$installed" ]; then step "Upgrading RepoMan $installed -> $num ($osname/$arch)"; else step "Installing RepoMan $num ($osname/$arch)"; fi
step "Downloading $archive"
fetch "$tmp/$archive" "$base/$archive" || die "download failed: does release $VERSION exist for $osname/$arch? ($base/$archive)"

if [ "${RM_SKIP_VERIFY:-0}" = 1 ]; then
  say "warning: skipping checksum verification (RM_SKIP_VERIFY=1)"
else
  step "Verifying the checksum"
  fetch "$tmp/SHA256SUMS" "$base/SHA256SUMS" || die "could not download SHA256SUMS for $VERSION; refusing to install an unverified file"
  (cd "$tmp" && grep " \*\{0,1\}$archive\$" SHA256SUMS | sha_check >/dev/null) || die "checksum mismatch for $archive: the download is corrupt or has been tampered with"
  say "    ok"
fi

step "Unpacking"
tar -xzf "$tmp/$archive" -C "$tmp"
dir="$tmp/${archive%.tar.gz}"
[ -f "$dir/install.sh" ] || die "unexpected archive layout (no install.sh in ${archive%.tar.gz})"
chmod +x "$dir/install.sh" "$dir/repoman"
chmod -R go-w "$dir"

# ---------- hand over to the packaged installer ----------
args="$PASS"
case "$CMD" in uninstall) args="$args --uninstall" ;; esac
if [ -n "$MIGRATE" ]; then
  case "$MIGRATE" in # a relative directory means relative to where the person ran this
    /*|*:/*|*:~*|~*) ;;
    *) MIGRATE="$(cd "$MIGRATE" 2>/dev/null && pwd)" || die "$MIGRATE is not a directory" ;;
  esac
  args="$args --migrate-from '$MIGRATE'"
fi
step "Running install.sh"
# shellcheck disable=SC2086
$sh_c "cd '$dir' && ./install.sh $args"

# Shared by the Linux and macOS installers (sourced, never run on its own). POSIX sh only: it also runs under
# macOS's /bin/sh and BusyBox (Alpine).
#
# The caller defines before sourcing: BIN, DATA and the hooks
#   svc_stop       stop the service (must succeed when it is not installed or not running)
#   svc_start      start it
#   fix_owner DIR  give DIR (recursively) to the account that runs the service
# and may set YES=1 (assume "yes" to questions) and FORCE=1.

say()  { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

# rm_version <binary>: "1.5.1" from "RepoMan 1.5.1 (abc1234) built ..."; empty when it will not run.
rm_version() { "$1" version 2>/dev/null | sed -n '1s/^RepoMan \([^ ]*\).*/\1/p'; }

# ver_gt A B: succeeds when version A is newer than B. Compares the numeric parts ("1.10.0" > "1.9.2");
# a "-rc1" style suffix is ignored; a version that is not numeric (a "dev" build) never compares as newer.
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

# confirm "question": proceeds when --yes was given or the person at the terminal answers y. A piped run
# (curl ... | sh) has no keyboard on stdin, so the terminal is asked directly; with none, --yes is required.
confirm() {
  [ "${YES:-0}" = 1 ] && return 0
  if [ -r /dev/tty ] && [ -w /dev/tty ]; then
    printf '%s [y/N] ' "$1" >/dev/tty
    read -r _ans </dev/tty || _ans=n
    case "$_ans" in y|Y|yes|YES) return 0 ;; esac
    die "cancelled"
  fi
  die "$1 -- no terminal to ask on; run again with --yes to confirm"
}

# wait_healthy: waits up to 30 seconds for the server to answer.
wait_healthy() {
  _i=0
  while [ "$_i" -lt 30 ]; do
    if RM_CONFIG="${ENVFILE:-/dev/null}" "$BIN" healthcheck >/dev/null 2>&1; then return 0; fi
    _i=$((_i + 1)); sleep 1
  done
  return 1
}

# backup_db <old version>: copies the database (users, repositories, settings, signing key, the artifact index) to
# $DATA/backups/<time>-v<version>/ while the service is stopped, and keeps the newest three. Upgrades change the
# database layout and cannot be undone, so this is what "rollback" restores. The artifact files themselves are
# never rewritten by an upgrade and are not copied.
backup_db() {
  [ -f "$DATA/repoman.db" ] || return 0
  _need=$(( $(wc -c <"$DATA/repoman.db") / 1024 * 2 + 1024 ))
  _free=$(df -Pk "$DATA" 2>/dev/null | awk 'NR==2 {print $4}')
  if [ -n "$_free" ] && [ "$_free" -lt "$_need" ] 2>/dev/null; then
    die "not enough free space in $DATA to back up the database before upgrading (need about $((_need / 1024)) MB)"
  fi
  _dir="$DATA/backups/$(date +%Y%m%d-%H%M%S)-v${1:-unknown}"
  mkdir -p "$_dir"
  for _f in repoman.db repoman.db-wal repoman.db-shm; do
    if [ -f "$DATA/$_f" ]; then cp -p "$DATA/$_f" "$_dir/$_f"; fi
  done
  chmod 0700 "$DATA/backups" "$_dir"
  printf '%s\n' "$_dir" >"$DATA/backups/latest"
  find "$DATA/backups" -mindepth 1 -maxdepth 1 -name '[0-9]*' | sort | awk '{a[NR]=$0} END {for (i = 1; i <= NR - 3; i++) print a[i]}' |
    while read -r _old; do rm -rf "$_old"; done
  say "backed up the database to $_dir"
}

# keep_previous_binary: remembers the binary being replaced so "rollback" can bring it back.
keep_previous_binary() {
  if [ -x "$BIN" ] && [ -n "$(rm_version "$BIN")" ]; then cp -p "$BIN" "$BIN.prev"; fi
}

# do_rollback: previous binary + the database as it was before the last upgrade.
do_rollback() {
  [ -f "$BIN.prev" ] || die "nothing to roll back to: the previous version is kept only after an upgrade"
  _prev="$(rm_version "$BIN.prev")"
  [ -n "$_prev" ] || die "$BIN.prev does not run on this machine"
  _latest="$(cat "$DATA/backups/latest" 2>/dev/null || true)"
  say "Rolling back from $(rm_version "$BIN") to $_prev."
  if [ -n "$_latest" ] && [ -d "$_latest" ]; then
    say "The database is restored from $_latest: changes made since the upgrade (new artifacts, users, settings) are lost."
  else
    warn "no database backup was found; only the program is rolled back, which fails if the new version already changed the database"
  fi
  confirm "Roll back?"
  svc_stop
  if [ -n "$_latest" ] && [ -d "$_latest" ]; then
    _aside="$DATA/backups/rolled-back-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$_aside"
    for _f in repoman.db repoman.db-wal repoman.db-shm; do
      if [ -f "$DATA/$_f" ]; then mv "$DATA/$_f" "$_aside/$_f"; fi
    done
    for _f in repoman.db repoman.db-wal repoman.db-shm; do
      if [ -f "$_latest/$_f" ]; then cp -p "$_latest/$_f" "$DATA/$_f"; fi
    done
    fix_owner "$DATA"
    say "the database that was replaced is kept in $_aside"
  fi
  install -m 0755 "$BIN.prev" "$BIN"
  rm -f "$BIN.prev"
  svc_start
  if wait_healthy; then say "RepoMan $_prev is running again."; else warn "the service did not answer after the rollback; check the logs"; fi
}

# do_migrate <source>: adopts an existing data directory -- one made by running ./repoman by hand, a Docker
# volume, a copy from another server -- or, with host:/path, fetches one over ssh. Whatever is in $DATA now is
# kept aside, never deleted. Stop RepoMan at the source first so its database is not copied mid-write.
do_migrate() {
  _src="$1"; _tmp=""
  case "$_src" in
    /*|./*|../*|~*) [ -d "$_src" ] || die "$_src is not a directory" ;;
    *:/*|*:~*)
      command -v ssh >/dev/null 2>&1 || die "ssh is needed to fetch $_src"
      _tmp="$DATA.migrating"
      rm -rf "$_tmp"; mkdir -p "$_tmp"
      say "copying $_src over ssh (RepoMan must be stopped there so the database is consistent)"
      ssh "${_src%%:*}" "tar -C '${_src#*:}' -cf - ." | tar -C "$_tmp" -xf - || { rm -rf "$_tmp"; die "could not copy $_src"; }
      _src="$_tmp" ;;
    *) die "migrate-from takes a directory (/var/lib/repoman, ./data) or host:/path on another machine, not '$_src'" ;;
  esac
  [ -f "$_src/repoman.db" ] || die "$_src does not look like a RepoMan data directory: there is no repoman.db in it"
  [ "$(cd "$_src" && pwd -P)" != "$(cd "$DATA" 2>/dev/null && pwd -P)" ] || die "the source is already the data directory"
  svc_stop
  if [ -d "$DATA" ] && [ -n "$(ls -A "$DATA" 2>/dev/null)" ]; then
    _aside="$DATA.before-migrate-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$_aside"
    find "$DATA" -mindepth 1 -maxdepth 1 -exec mv {} "$_aside"/ \;
    say "the data that was here is kept in $_aside"
  fi
  mkdir -p "$DATA"
  say "copying $(du -sh "$_src" 2>/dev/null | awk '{print $1}') into $DATA ..."
  cp -a "$_src"/. "$DATA"/
  [ -z "$_tmp" ] || rm -rf "$_tmp"
  chmod 0700 "$DATA"
  fix_owner "$DATA"
  say "migrated. Settings that come from the environment (RM_ADDR, TLS files, RM_TRUSTED_PROXIES, ...) are not part of the data: copy them into the env file of this machine."
}

# precheck_migrate: fails before anything is stopped when a migration would overwrite existing data without --force.
precheck_migrate() {
  if [ -f "$DATA/repoman.db" ] && [ "${FORCE:-0}" != 1 ]; then
    die "$DATA already holds a RepoMan database. Add --force to replace it (the existing data is kept aside, not deleted)."
  fi
}

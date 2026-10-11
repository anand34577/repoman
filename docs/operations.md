# Operations

Running RepoMan day to day: backups, monitoring, maintenance, recovery.

- [The data directory](#the-data-directory)
- [Backup and restore](#backup-and-restore)
- [Configuration export and import](#configuration-export-and-import)
- [Moving to another server](#moving-to-another-server)
- [Reset a lost password](#reset-a-lost-password)
- [Monitoring](#monitoring)
- [Maintenance tasks](#maintenance-tasks)
- [Disk space](#disk-space)
- [Audit log](#audit-log)
- [Sizing and performance](#sizing-and-performance)

## The data directory

Everything RepoMan knows is in one directory (`RM_DATA_DIR`):

```
data/
├── repoman.db            SQLite database: repositories, users, tokens, settings, audit log, artifact index, signing key
├── repoman.db-wal        write-ahead log  ┐ part of the database while the server runs:
├── repoman.db-shm        shared memory    ┘ never copy repoman.db without them, never delete them
├── blobs/                artifact contents, one file per unique content, named by SHA-256
│   └── 9f/86/9f86d081…
├── uploads/              Docker uploads in progress (safe to lose)
├── logs/                 log files, if RM_LOG_FILE points here (Windows service default)
└── initial-admin-password   only until the admin password is first changed
```

The database and `blobs/` belong together: the database says which blob is which artifact. Back them up
together and restore them together.

Only one RepoMan process may use a data directory at a time, and the directory must be on a local file
system (not NFS or SMB).

## Backup and restore

### What to back up

The **whole data directory**. Nothing else is needed — plus your `repoman.env` and TLS certificates, which
are small and static.

The backup contains secrets (password hashes, the package-signing key, upstream credentials). Store it
encrypted and restrict access ([security](security.md#what-is-stored-and-how)).

### Method 1: stop, copy, start (simplest, always consistent)

```bash
sudo systemctl stop repoman
sudo tar -C /var/lib -czf /backup/repoman-$(date +%F).tar.gz repoman
sudo systemctl start repoman
```

Downtime is the duration of the copy. Use `rsync -a --delete /var/lib/repoman/ /backup/repoman/` for
fast incremental copies: blob files never change once written, so after the first run only new files are transferred.

Windows (elevated PowerShell):

```powershell
Stop-Service RepoMan
robocopy C:\ProgramData\RepoMan D:\Backup\RepoMan /MIR /R:2 /W:2
Start-Service RepoMan
```

Docker: `docker stop repoman && docker run --rm -v repoman-data:/data -v "$PWD":/backup alpine tar -C / -czf /backup/repoman.tar.gz data && docker start repoman`

### Method 2: snapshot (no downtime)

Take an atomic snapshot of the volume that holds the data directory — LVM, ZFS, Btrfs, a cloud disk
snapshot, VSS on Windows — and back up from the snapshot. A snapshot is like a power cut frozen in time;
SQLite's write-ahead log makes that safe, and RepoMan recovers from it on start.

### Method 3: live copy without snapshots (no downtime, two passes)

Copying files one by one while the server runs is not atomic, so do it in an order that stays consistent:

```bash
# 1. the database, as a consistent single file (needs the sqlite3 command-line tool)
sqlite3 /var/lib/repoman/repoman.db ".backup '/backup/repoman/repoman.db'"
# 2. then the blobs — everything the database copy refers to already exists on disk
rsync -a /var/lib/repoman/blobs/ /backup/repoman/blobs/
```

Database first, blobs second. The backup may then contain a few blobs the database copy does not know
about (harmless; the *Compact blob store* task removes them after a restore), but never the reverse.
Do **not** copy `repoman.db` with `cp` while the server runs.

### Restore

1. Install the same or a newer RepoMan version (never older than the one that wrote the backup).
2. Stop the service.
3. Put the backup in place as the data directory; fix ownership (`chown -R repoman:repoman /var/lib/repoman`,
   or UID 10001 for the container).
4. Start the service.
5. Run **Scheduled tasks → Verify blob store** to confirm every artifact's content is present, and
   **Compact blob store** to drop leftovers.

Test a restore before you need one: restore last night's backup to a scratch machine or container, start
RepoMan on another port, sign in, download an artifact.

### What if only part survives?

- **Database lost, blobs intact:** the artifact index, users and settings are gone; the blobs alone cannot be
  mapped back to names. Restore the database from backup. (Proxied content can simply be re-downloaded;
  hosted content cannot.)
- **Some blobs lost, database intact:** *Verify blob store* lists affected artifacts. For proxy repositories,
  delete those artifacts; they are fetched again on next request. For hosted ones, republish.
- **Signing key lost** (new database): APT/YUM clients reject the new signatures until they import the new
  public key.

## Configuration export and import

*Settings → Backup & restore* downloads a JSON file with **settings, repositories, users, access overrides,
webhooks and notification channels**. It is a way to clone or rebuild a *setup*, not a backup of content:

- **Not included:** artifacts, passwords, API tokens, upstream passwords, webhook secrets, notification
  tokens, the signing key, SSO configuration, the audit log.

Importing into a server:

- repositories that exist are updated (name, format and type never change), others are created — groups after their members;
- users that do not exist are created with a **random password**; the import report lists them so you can
  set passwords (or they sign in through SSO). Existing users are left untouched;
- access overrides are applied; webhooks and notification channels that are not already present are added —
  channels arrive **paused** until you enter their token, webhooks without their secret;
- server settings are replaced by the imported ones;
- upstream passwords must be re-entered on each proxy that needs one.

The report shows what was created, updated and what failed, item by item; one bad entry does not abort the rest.

Use it to keep configuration in version control (`GET /api/v1/system/export` nightly) or to stamp out
identical test servers.

## Moving to another server

1. Install RepoMan on the new machine ([installation](installation.md)) and stop it.
2. Stop the old one. Copy the entire data directory across (any OS and CPU architecture combination works;
   the format is the same everywhere).
3. Fix ownership on the new machine, copy `repoman.env` and certificates, start.
4. Point the DNS name at the new machine. If the public URL changes, update *Settings → Base URL*, the
   redirect URI at your SSO provider, and client configuration.

Changing only the data directory's location: stop, move the directory, set `RM_DATA_DIR`, start
(on Linux also allow the new path in the unit: see [installation](installation.md#linux-systemd)).

## Reset a lost password

For any local account, on the machine that holds the data directory:

```bash
repoman reset-password                # resets "admin", prints a new random password
repoman reset-password alice          # another account
repoman reset-password admin --password 'my new long password'
```

Run it the way the server runs so it finds the same data directory:

| Installed as | Command |
|---|---|
| Linux service | `sudo -u repoman repoman reset-password --config /etc/repoman/repoman.env` |
| macOS service | `repoman reset-password --config /usr/local/etc/repoman/repoman.env` |
| Windows service | elevated prompt: `& 'C:\Program Files\RepoMan\repoman.exe' reset-password` |
| Docker | `docker exec -it repoman repoman reset-password` |
| Elsewhere | `repoman reset-password --data-dir /path/to/data` |

What it does: sets the password, **re-enables** the account if it was disabled, signs the account out of all
browser sessions, and records a `password.reset` entry in the audit log. API tokens are not touched. It works
while the server is running; a running server may accept the old password for up to one minute.

It does not change roles. If no administrator account is usable at all, reset `admin` (it always exists
unless you deleted it) or any account you know to be an administrator.

Accounts that sign in only through SSO get a local password this way too, which then also works for sign-in.

## Monitoring

### Health

`GET /healthz` — no authentication, answers `200 ok` when the process is up and the database responds,
`503` otherwise. Use it for load balancers, uptime monitors and container probes. In the container image,
`repoman healthcheck` does the same from inside.

### Metrics

`GET /metrics` — Prometheus text format; requires any account (create a viewer named `monitoring` with a
read-only token). The per-repository series cover the repositories that account may read, so an account with
access overrides sees fewer of them.

```yaml
scrape_configs:
  - job_name: repoman
    metrics_path: /metrics
    scheme: https
    basic_auth: { username: monitoring, password: rm_… }
    static_configs: [{ targets: ["repo.example.com"] }]
```

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `repoman_up` | gauge | | Always 1 |
| `repoman_uptime_seconds` | gauge | | Seconds since start |
| `repoman_http_responses_total` | counter | `class` = `1xx`…`5xx` | Responses by status class |
| `repoman_repository_assets` | gauge | `repository`, `format`, `type` | Number of artifacts |
| `repoman_repository_bytes` | gauge | same | Logical size (before deduplication) |
| `repoman_repository_downloads_total` | counter | same | Downloads of artifacts currently stored |

Alert ideas: `up == 0`; rising `repoman_http_responses_total{class="5xx"}`; `repoman_repository_bytes`
approaching a quota; free disk space of the data volume (from node_exporter) below 15%.

### Push alerts

Subscribe a [notification channel](notifications.md) to *Problems only*: upstream outages and recoveries,
quota reached, failed tasks, sign-in lockouts.

### Where to look

| Question | Place |
|---|---|
| Is an upstream down? | Dashboard → *Needs attention*; the repository's status badge shows the error |
| Who changed / deleted / published this? | *Audit log* |
| Why did a request fail? | Server log: status, method, path, client address, duration per request |
| How much space does each repository use? | *Repositories* list; *Dashboard → Storage by format* |
| What version is running? | *Settings → System*, or `repoman version` |

## Maintenance tasks

*Scheduled tasks* in the UI. Each can be started by hand; the page shows the last result and duration.
A failing task records a `task.failed` event.

| Task | Schedule | What it does |
|---|---|---|
| Run cleanup policies | Hourly | Applies every repository's cleanup policy |
| Purge expired data | Hourly | Deletes expired sessions and tokens, Docker uploads abandoned for 24 h, audit events past retention |
| Compact blob store | Daily | Removes content files no artifact refers to (after crashes or partial restores) and stale temporary files |
| Optimize database | Weekly | SQLite `VACUUM` and `ANALYZE`: reclaims space, keeps queries fast. Needs free disk space of about the database's size while it runs |
| Verify blob store | Manual | Checks that the content file of every artifact exists; reports missing ones. Run after a restore or disk trouble |
| Rebuild hosted indexes | Manual | Regenerates APT `Release`/`Packages`, YUM `repodata` and npm package documents. Run if a client reports a broken index |

Schedules are intervals since the last run, not clock times; after a restart, overdue tasks run within a minute.

## Disk space

When the data volume fills up, uploads and proxy caching fail with 500-class errors while downloads of
existing content keep working. To get space back, in order of effect:

1. **Cleanup policies** on proxies ("delete if unused for 90 days") and on CI-published repositories
   ("keep latest 20 versions"). Preview first.
2. Delete repositories or components nobody needs.
3. Run **Compact blob store**, then **Optimize database**.
4. Give the volume more room.

Things to know:

- Space is freed only when the **last** reference to a file's content is gone (deduplication across
  repositories).
- **Quotas** stop a single hosted repository from eating the disk; they do not apply to proxies — use
  cleanup there.
- Keep at least 10–15% free. A full disk during a database write is survivable (SQLite rolls back) but
  every write fails until space is available.

## Audit log

Every sign-in, publication, deletion and configuration change is recorded with time, user, action, target
and detail. *Audit log* in the UI searches it and exports the current view as CSV; `GET /api/v1/audit`
returns the same.

- **Retention**: 90 days by default (*Settings → General*; 0 = keep forever). The hourly purge enforces it.
- For long-term or tamper-evident records, forward events with a [webhook](notifications.md#webhooks) to
  your log system.
- Downloads are not audited individually; each artifact carries a download count and a last-download time.

## Sizing and performance

- **CPU**: mostly idle. Hashing uploads and bcrypt on sign-in are the only notable costs. Two cores serve a
  mid-sized team comfortably.
- **Memory**: small and flat, except that an `npm publish` is held in memory (size of the tarball × ~3) and
  APT/YUM index rebuilds hold the index. 512 MB–1 GB is plenty for typical use.
- **Disk**: sequential throughput matters for big images; SSDs help metadata-heavy formats (npm, Maven).
- **Network**: downloads stream from disk and support range requests; the first request of a proxied file
  waits for the upstream download.
- **Scale**: a single node with a single database. It comfortably handles hundreds of thousands of
  artifacts and dozens of concurrent builds. Browsing a single folder with more than ~100 000 direct
  descendants gets slow. Beyond one machine's disk or availability needs, RepoMan is the wrong tool.
- Writes to the database are serialized; heavy concurrent *publishing* (not downloading) is where you
  would first notice limits.

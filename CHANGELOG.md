# Changelog

All notable changes to RepoMan. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- **Conda repositories** (conda, mamba, micromamba, pixi). *Hosted* channels accept `.conda` and `.tar.bz2`
  uploads (curl, HTTP `PUT`/`POST`, or drag and drop) and generate `repodata.json` for each subdir.
  *Proxy* channels cache conda-forge, bioconda or Anaconda; cached content stays available while the upstream is
  down. Conda repositories can't be grouped; list several channels in `.condarc`. New audit, webhook and
  notification event: `conda.upload`. See [Conda](docs/repositories.md#conda).

### Upgrade notes

- New installations get `conda-hosted` and `conda-forge` (proxy) starter repositories. Existing servers are
  not changed; create conda repositories from *Repositories → New*.
- Notification channels that use the *Problems + releases* preset now also receive `conda.upload`. Channels
  saved before the upgrade keep their old event list and show as *Custom*; pick the preset again to include
  conda uploads.

## [1.2.0]

A security, operations and integration release. **Read the upgrade notes** before upgrading from 1.1.

### Upgrade notes

- **No more default password.** New installations generate a random `admin` password (printed in the log and
  saved as `<data dir>/initial-admin-password`) unless `RM_ADMIN_PASSWORD` is set. Existing installations keep
  their accounts and passwords unchanged; if yours still uses `admin123`, change it now.
- **`X-Forwarded-*` headers are trusted only from known proxies.** Until now they were believed from anyone.
  The default trusts connections from the same machine. If your reverse proxy runs on another host or in
  another container, set `RM_TRUSTED_PROXIES` to its address or network, and set *Settings → Base URL*.
  Otherwise generated links may use `http://`, and all users share one sign-in lockout.
- **Go checksum database requests are now relayed** through Go proxy repositories instead of answering 404.
  Clients no longer need to reach `sum.golang.org`. No action needed.
- **A proxy is no longer marked "Remote unavailable" for a 401/403/4xx answer** about a single item, only
  when the upstream is unreachable, returns 5xx or rate-limits (429).
- The database is migrated automatically on first start. Back up the data directory first; downgrading is
  not supported.
- The container image is now built `FROM scratch` (no shell) and its default command is `serve`. If you
  override the command, use `repoman serve`.

### Added

- **Gotify notifications**: push messages for the events you choose, with presets, per-channel repository
  filters and priorities, a test button, retries, and one message per outage instead of a flood.
- **Scoped API tokens**: read-only, read & publish, or full access. Narrow tokens cannot manage tokens,
  passwords or SSO links. `GET /api/v1/auth/me` reports a token's scope.
- **Versioned API** under `/api/v1`, a complete [API reference](docs/api.md), and `GET /api/v1/version`.
- **Built-in HTTPS** (`RM_TLS_CERT`, `RM_TLS_KEY`) with automatic certificate reload.
- **Service support**: native Windows service (`repoman service install`), hardened systemd unit and
  launchd job, with install scripts for all three.
- **Commands**: `repoman reset-password` for account recovery, `repoman healthcheck` for containers and
  monitors, `repoman version`.
- **Configuration file**: `repoman.env` next to the binary, `--config`, and command-line flags.
- **New events** for webhooks and notifications: `repository.degraded`, `repository.recovered`,
  `repository.quota`, `task.failed`, `login.lockout`, `system.start`, `password.reset`, `notifier.*`.
  Event filters accept prefixes (`repository.*`).
- **Maven groups merge `maven-metadata.xml`** across members, with matching checksums.
- **Go checksum database relay** through proxy and group repositories; lookups for modules hosted here are
  never sent upstream.
- **Circuit breaker for unreachable upstreams**: cached content is served immediately and the upstream is
  retried every 30 seconds, instead of each request waiting for a timeout.
- Log file with rotation (`RM_LOG_FILE`), client address in the access log, `RM_ACCESS_LOG`.
- Release automation: archives for Linux (amd64, arm64, armv7, armv6), macOS (arm64, amd64) and Windows
  (amd64, arm64), multi-architecture container image, checksums and signed build provenance.
- Documentation: installation, configuration, firewall/air-gap guide, per-format guide, security,
  notifications, API, operations, troubleshooting.

### Security

- Random first-run admin password; no well-known default credential. An upgraded installation whose admin
  still uses the old default `admin123` gets a warning in the log and the reminder on the dashboard.
- Cross-site request forgery protection for the session cookie (in addition to `SameSite`).
- Content security policy for the UI (nothing can be loaded from other hosts), `Strict-Transport-Security`
  over HTTPS, `Cross-Origin-Opener-Policy`, `Permissions-Policy`, `Cache-Control: no-store` on API responses.
- Changing or resetting a password signs out the account's other sessions.
- Sign-in no longer reveals whether a user name exists through response timing.
- Passwords longer than bcrypt's 72-byte limit are rejected instead of being silently truncated.
- Management API request bodies are limited to 1 MB (import: 32 MB); login bodies to 64 KB.
- Slow-header protection and idle timeouts on the HTTP server; graceful shutdown drains requests.
- The data directory is created with owner-only permissions.
- Sign-in lockouts are recorded as events; lockout and credential-cache state is pruned hourly.
- Tokens created from a repository's Setup tab are now least-privilege (read-only for proxies and groups).

### Changed

- `/healthz` now also checks the database and answers 503 when it is unavailable.
- Registry token and upstream errors name the host that failed.
- The version is injected at build time; `repoman version` and *Settings → System* show commit and build date.

## 1.1.0 and earlier

Not recorded in this file.

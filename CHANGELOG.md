# Changelog

All notable changes to RepoMan. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.5.0]

An audit of the server and the web UI: faster on large installations, several fixes, and a smoother interface.

### Performance

- The dashboard, the repository list, the *Components* tab and folder sizes in *Browse* are read from covering
  indexes and short-lived caches instead of visiting every artifact. Measured on a test database of 600,000
  artifacts: dashboard 1.8 s → 0.6 s on first load and instant afterwards, *Components* 1.3 s → 0.05 s,
  browsing a folder with 86,000 artifacts below it 1 s → 0.17 s.
- **Search uses a substring index.** Searching for part of a name took 0.6–1.3 s on that database and now takes
  0.01–0.25 s; it finds the same artifacts as before (any part of a path or component, in any case). Every
  sort order of the search page (updated, name, size, downloads) is served by an index too.
- Downloads no longer wait for a database write each: download counters are kept in memory and written every
  two seconds (and on shutdown), so many clients pulling at once are not queued behind one another.
- A token's "last used" time is written at most once a minute instead of being checked with a write statement on
  every request.
- Uploading several `.deb` or `.rpm` files in the browser rebuilds the signed index once, not once per file.
- The web UI shows a page you have opened before immediately and refreshes it in the background, and shares one
  request between parts of the page that need the same data.
- Search and the audit log skip the separate counting query when the last page is reached.

### Fixed

- A malformed npm package document (a version that is not an object) could crash the whole server when the hosted
  indexes were rebuilt. Such publishes are now rejected, existing documents are read defensively, and a failing
  maintenance task can no longer take the server down.
- Docker: finishing a blob upload without a valid `digest` stored the layer under an empty name; it is now refused.
- `/metrics` listed the name, size and download count of repositories the caller is not allowed to read.
- *Run now* for a task that does not exist answered "started"; it now answers 404.
- Importing a configuration with an invalid access level, an invalid base URL or `null` entries is reported per
  entry instead of being stored or aborting the import.
- A project name is escaped on PyPI index pages, and RPM headers with a negative entry count are rejected.
- UI assets requested with a byte range were compressed after the range was cut; fonts and images are no longer
  compressed a second time.
- *Invalidate cache* on a proxy reset the "updated" date of every cached file to 1970, which broke
  `Last-Modified`, sorting by date, and could let the cleanup policy delete files that were never downloaded
  through the UI. It now only marks index data as stale.
- Unexpected server errors no longer send the database's error text to the client; the response carries a
  reference to look up in the server log.
- Updating or deleting a webhook that does not exist answers 404 instead of success.

### Changed

- **Importing a configuration asks first.** Choosing a file shows what it contains; nothing is applied until you
  confirm. Files that are not a RepoMan export are refused with an explanation.
- Forms validate inline, in the same style everywhere, instead of with the browser's own pop-up messages.
- Phones: repositories are listed as cards, tab bars scroll instead of wrapping, page actions use the full width,
  and long repository names and URLs wrap instead of running off the screen.
- If a page fails to render, or the server was upgraded while the UI was open, a message with a *Reload* button
  replaces the blank page. When a session ends you are told why you are back at the sign-in form.
- The dashboard puts *Needs attention* first, and cards no longer stretch to the height of their neighbours.
- Exact dates are available on hover or keyboard focus for every relative time ("3 hours ago").
- Webhooks can subscribe to `task.run`, `webhook.*` and `notifier.*` events from the picker.

### Added

- **Audit log period.** Filter by *Today*, the last 7, 30 or 90 days, or a custom range picked from a calendar;
  the CSV export follows it. REST API: `from` and `to` (Unix seconds) on `GET /audit` and `GET /audit/export`.
- **Unsaved changes are protected.** Leaving repository configuration or access, server settings, single
  sign-on settings or the repository wizard with edits you have not saved asks first. (Closing or reloading
  the browser tab is not intercepted.)

### Security

- react-router is updated from 6 to 7.18, which fixes an open redirect in links and navigation
  (GHSA-wrjc-x8rr-h8h6). `npm audit` reports no known vulnerabilities.

### Upgrade notes

- New database indexes, including the search index, are built on the first start: about 40 seconds per
  million artifacts, during which the server does not answer yet. The database grows by about half
  (349 MB → 523 MB with 600,000 artifacts); the artifacts themselves are not touched.
- The web UI has one new bundled dependency, Mantine's date picker (with dayjs). Nothing is loaded from the
  network.
- Download counts and "last downloaded" times reach the database (and so the UI, the API and `/metrics`) up to
  two seconds after the download.

## [1.4.0]

Makes the web UI usable with hundreds of thousands of artifacts.

### Added

- Pagination everywhere a list can grow large: search, the audit log, folder browsing and components are paged
  on the server; repositories, users and per-repository access are paged in the browser. Page and page size
  are kept in the URL, so a page can be shared or reloaded. Search and components can be sorted; repositories
  too.
- Folder filtering in *Browse* now searches the whole folder, not just the loaded entries. Links to an artifact
  (for example from search) open it even when it is on a later page.
- The audit log export contains every matching event (streamed by the server), not only the rows on screen.
- Browser uploads show progress and can be cancelled.
- The *Access* tab can filter users and show only those with an override; unsaved changes are kept across pages.
- REST API: `offset`/`limit` and the `X-Total-Count` header on browse, components, search and audit;
  `GET /repositories/{name}/asset`, `GET /audit/export`. Responses are still plain arrays.

### Fixed

- Browsing a folder no longer loads every artifact below it into memory, and folders with non-ASCII names list
  correctly.
- On short screens, the admin sidebar no longer runs off the bottom of the window, and scrolling it no
  longer scrolls the page too.
- Lists ignore responses that arrive out of order, so fast typing in a filter can't show stale results.

### Performance

- New database indexes for search ordering, per-repository totals and the dashboard. They are created on the
  first start after upgrading, which can take a minute on very large databases.

## [1.3.0]

Adds Conda repositories and is the first release published from this repository.

### Added

- **Conda repositories** (conda, mamba, micromamba, pixi). *Hosted* channels accept `.conda` and `.tar.bz2`
  uploads (curl, HTTP `PUT`/`POST`, or drag and drop) and generate `repodata.json` for each subdir.
  *Proxy* channels cache conda-forge, bioconda or Anaconda; cached content stays available while the upstream is
  down. Conda repositories can't be grouped; list several channels in `.condarc`. New audit, webhook and
  notification event: `conda.upload`. See [Conda](docs/repositories.md#conda).
- Screenshots of the web UI in the README.

### Security

- Built with Go 1.26.9, which fixes vulnerabilities in the HTTP server and client, header parsing and TLS of
  the Go standard library (GO-2026-6603, -6605, -6607 to -6613 and -6617). Upgrading is recommended.

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

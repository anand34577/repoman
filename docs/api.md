# REST API

Everything the web UI does goes through this API, so anything you can click you can automate.

- Base path: **`/api/v1`**. (The UI uses the same endpoints under `/api`; integrations should use the
  versioned prefix, which stays stable.)
- Requests and responses are JSON (`Content-Type: application/json`), UTF-8.
- Timestamps are Unix seconds; sizes are bytes; `0` for a timestamp means "never".

Contents: [Authentication](#authentication) · [Errors](#errors) · [Conventions](#conventions) ·
[Repositories](#repositories) · [Browse and search](#browse-and-search) · [Artifacts](#uploading-and-deleting-artifacts) ·
[Tokens](#tokens) · [Users](#users) · [Repository access](#repository-access) · [Notification channels](#notification-channels) ·
[Webhooks](#webhooks) · [Tasks](#tasks) · [Settings](#settings) · [Audit log](#audit-log) · [System](#system) ·
[Package-client endpoints](#package-client-endpoints) · [Recipes](#recipes)

## Authentication

Create a token under **Account & tokens** in the UI ([scopes](security.md#api-tokens-and-scopes)), then send it
either way:

```bash
curl -H "Authorization: Bearer rm_0123…" https://repo.example.com/api/v1/repositories
curl -u alice:rm_0123…                  https://repo.example.com/api/v1/repositories     # basic auth: token as password
```

Check a credential:

```bash
curl -H "Authorization: Bearer $TOKEN" https://repo.example.com/api/v1/auth/me
# {"username":"ci","email":"","role":"developer","disabled":false,"createdAt":1760000000,"lastLogin":0,"tokenScope":"write"}
```

`tokenScope` is present for read-only (`read`) and read & publish (`write`) tokens and absent for full-access
tokens and browser sessions.

A user name with the account password also works for basic auth, but passwords are subject to the sign-in
lockout and should not be stored in scripts. The management API has no anonymous access, whatever the
*anonymous access* setting is; that setting only concerns downloads.

Which role an endpoint needs is stated per endpoint below as *viewer* (any account), *developer* or *admin*.
A token's scope caps the role: `read` → viewer and `GET` only; `write` → developer; `full` → the owner's role.

## Errors

Errors have a matching HTTP status and a JSON body with one field:

```json
{"error": "a repository named npm-hosted already exists"}
```

| Status | Meaning |
|---|---|
| 400 | Invalid input; the message says what is wrong |
| 401 | Missing or invalid credentials (also: account disabled, token expired, client address locked out) |
| 403 | Authenticated, but the role or the token's scope does not allow it; or a cross-site browser request |
| 404 | No such object — or one you are not allowed to see |
| 409 | Conflict: already exists, or already running |
| 413 | Request body too large (management requests are limited to 1 MB; configuration import to 32 MB) |
| 429 | Too many failed sign-ins from this address; retry in 15 minutes |
| 500 | Server-side failure; details are in the server log |
| 502 | A proxy repository could not reach its upstream |
| 503 | The repository is offline |
| 507 | The repository's storage quota is reached |

## Conventions

- Successful calls that return nothing answer **204 No Content**.
- Optional string and list fields that are empty are left out of responses (for example `description`,
  `remoteUsername`, `members`).
- Unknown JSON fields are ignored. On updates (`PUT`), send the complete object: omitted fields are reset to
  their defaults.
- **Secrets are write-only.** Passwords, upstream credentials, webhook secrets and notification tokens are
  never returned. On update, send an empty string (or omit the field) to keep the stored value.
- Lists are returned whole except the paged ones: browse, components, search and the audit log. These take
  `offset` (default 0) and `limit` (at most 1000; default stated per endpoint) and still return a plain JSON
  array. The number of matches is in the `X-Total-Count` response header; for search and the audit log it
  stops counting at 10,001, so a value above 10,000 means "more than 10,000".
- There is no CORS support: the API is meant for server-side and command-line clients, not for JavaScript on
  other websites.

---

## Repositories

### List — `GET /repositories` · viewer

Returns the repositories the caller can read.

```json
[{
  "name": "npm-proxy", "format": "npm", "type": "proxy", "description": "",
  "offline": false, "private": false,
  "remoteUrl": "https://registry.npmjs.org", "remoteUsername": "", "metadataTtl": 60,
  "allowRedeploy": false, "quotaMb": 0, "cleanupDays": 0, "retainVersions": 0, "createdAt": 1760000000,
  "assetCount": 1284, "size": 734003200, "downloads": 9321,
  "status": "online",
  "health": {"lastOk": 1760003600, "lastError": "…", "lastErrorAt": 1760000100},
  "access": "write"
}]
```

| Field | Notes |
|---|---|
| `format` | `maven`, `npm`, `pypi`, `docker`, `go`, `apt`, `yum`, `conda`, `raw` |
| `type` | `hosted`, `proxy`, `group` |
| `status` | `online`, `offline` (switched off by an admin), `degraded` (proxy upstream failing; cache still served) |
| `health` | Proxies only, once they have contacted the upstream |
| `access` | The caller's level: `read` or `write` |
| `members` | Groups only: ordered repository names |
| `distribution` | APT hosted only |

### Get — `GET /repositories/{name}` · viewer

One object as above. 404 if it does not exist or is hidden from the caller.

### Create — `POST /repositories` · admin

```bash
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{
  "name": "team-npm", "format": "npm", "type": "hosted",
  "description": "Packages of the platform team", "private": true, "quotaMb": 20480, "retainVersions": 20
}' https://repo.example.com/api/v1/repositories
```

| Field | Types | Required | Default | |
|---|---|:-:|---|---|
| `name` | all | ✓ | | 1–64 chars: `a-z 0-9 . _ -`, starting with a letter or digit |
| `format`, `type` | all | ✓ | | Fixed after creation |
| `description` | all | | `""` | |
| `offline` | all | | `false` | Refuse all client requests |
| `private` | all | | `false` | Never readable anonymously |
| `allowRedeploy` | hosted | | `false` | Allow overwriting existing files |
| `quotaMb` | hosted | | `0` | Storage quota in MB; 0 = unlimited |
| `distribution` | APT hosted | | `stable` | Lowercase suite name |
| `remoteUrl` | proxy | ✓ | | `http(s)://…` |
| `remoteUsername`, `remotePassword` | proxy | | | Upstream credentials (password is write-only) |
| `metadataTtl` | proxy | | `0` | Minutes before index data is re-checked. `0` re-checks on every request; 60–1440 is typical |
| `members` | group | ✓ | | Existing repositories of the same format, in priority order |
| `cleanupDays` | hosted, proxy | | `0` | Delete files unused for N days; 0 = off |
| `retainVersions` | hosted, proxy | | `0` | Keep only the newest N versions per component; 0 = off |

Returns the created repository. 409 if the name is taken. APT, YUM and Conda cannot be `group` (400).

### Test an upstream — `POST /repositories/test-remote` · admin

Body: `{"format": "npm", "remoteUrl": "https://registry.npmjs.org", "remoteUsername": "", "remotePassword": ""}`.
Add `"name"` of an existing repository to test with its stored password.

```json
{"ok": true, "status": "200 OK", "latencyMs": 142, "message": "Remote is reachable."}
```

Always answers 200; look at `ok`.

### Update — `PUT /repositories/{name}` · admin

Send the full configuration. `name`, `format`, `type` and `createdAt` in the body are ignored. An empty
`remotePassword` keeps the stored one (as long as `remoteUsername` is unchanged).

### Delete — `DELETE /repositories/{name}` · admin

Deletes the repository **and all its artifacts**, and removes it from every group. Not reversible.

### Maintenance actions · admin

| Call | Effect |
|---|---|
| `POST /repositories/{name}/cleanup?dryRun=1` | Preview the cleanup policy: `{"count": 12, "size": 48211003, "sample": [ …up to 50 artifacts… ]}` |
| `POST /repositories/{name}/cleanup` | Run it: `{"deleted": 12, "size": 48211003}`. 400 if no policy is configured |
| `POST /repositories/{name}/invalidate` | Proxy only: re-check index data on next use, forget cached "not found" answers, retry the upstream now. Cached files and their dates are kept |
| `POST /repositories/{name}/reindex` | Rebuild generated metadata (APT/YUM indexes, npm package documents) |

## Browse and search

### Browse a folder — `GET /repositories/{name}/browse?path=<folder>&q=<filter>` · viewer

`path` is empty for the root. Folders come first, then files, each sorted by name. `q` keeps entries whose
name contains it (case-insensitive). Paged, default limit 1000.

One artifact by path: `GET /repositories/{name}/asset?path=<path>` → the artifact object, 404 if absent.

```json
[
  {"name": "com", "path": "com", "folder": true, "count": 42, "size": 1048576},
  {"name": "readme.txt", "path": "readme.txt", "folder": false, "asset": { …artifact… }}
]
```

### Components — `GET /repositories/{name}/components?q=<filter>&sort=<order>` · viewer

Artifacts grouped into components (packages, images, modules) and versions, newest version first. `sort` is
`name` (default), `updated`, `downloads` or `size`. Paged by component, default limit 1000.

```json
[{"name": "com.acme:lib", "assets": 6, "size": 20480, "downloads": 31, "updatedAt": 1760000000,
  "versions": [{"version": "1.1.0", "assets": 3, "size": 10240, "downloads": 30, "updatedAt": 1760000000}]}]
```

### Search — `GET /search?q=<text>&repo=<name>&format=<format>` · viewer

Matches the text anywhere in the path or component name, across all repositories the caller can read.
`repo` and `format` are optional filters. `sort` is `updated` (default, most recent first), `name`,
`downloads` or `size`. Paged, default limit 500.

### The artifact object

```json
{
  "id": 4711, "repo": "maven-releases", "path": "com/acme/lib/1.1.0/lib-1.1.0.jar",
  "sha256": "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
  "size": 10240, "contentType": "application/java-archive",
  "component": "com.acme:lib", "version": "1.1.0", "uploader": "ci",
  "createdAt": 1760000000, "updatedAt": 1760000000, "lastDownloaded": 1760003600, "downloads": 30,
  "attrs": { … }
}
```

`uploader` is the publishing user, `proxy` for files cached from an upstream, or `system` for generated
index files. `attrs` holds format-specific metadata where available (Debian control fields, RPM headers).

Download an artifact through its repository URL: `/repository/<repo>/<path>` (for Docker, use a registry client).

## Uploading and deleting artifacts

The natural way to publish is the format's own client or a plain HTTP `PUT` — see
[repositories.md](repositories.md). For scripts:

```bash
# raw and Maven: PUT to the full path
curl -H "Authorization: Bearer $TOKEN" --upload-file build.zip https://repo.example.com/repository/raw-hosted/app/1.4/build.zip
# APT / YUM / Conda: PUT the package to the repository root
# (Conda files it under its subdir, e.g. linux-64/, taken from the package)
curl -H "Authorization: Bearer $TOKEN" --upload-file tool_1.0_amd64.deb https://repo.example.com/repository/apt-hosted/
# delete by path (204)
curl -H "Authorization: Bearer $TOKEN" -X DELETE https://repo.example.com/repository/raw-hosted/app/1.4/build.zip
```

These need write access to the repository (developer role or a `write` override) and a token with `write`
or `full` scope.

### Multipart upload — `POST /repositories/{name}/upload` · write access

What the UI's drag-and-drop uses. Hosted `raw`, `maven`, `go`, `apt`, `yum` and `conda` repositories only.
Form fields: `file` (repeatable) and `path` (target directory; ignored for APT and Conda).

```bash
curl -H "Authorization: Bearer $TOKEN" -F path=docs/2026 -F file=@report.pdf -F file=@data.csv \
  https://repo.example.com/api/v1/repositories/raw-hosted/upload
# {"uploaded":["docs/2026/report.pdf","docs/2026/data.csv"]}
```

If one file fails, the answer is 400 naming it; files before it were stored.

### Delete — write access

| Call | Deletes |
|---|---|
| `DELETE /assets/{id}` | One artifact, by `id` |
| `DELETE /repositories/{name}/components?component=<name>` | A component with all versions |
| `DELETE /repositories/{name}/components?component=<name>&version=<v>` | One version → `{"deleted": 3}` |

Indexes (APT, YUM, npm package documents) are updated automatically. Disk space is freed when no other
repository references the same content.

## Tokens

Token management needs a browser session or a **full-access** token.

| Call | |
|---|---|
| `GET /tokens` · viewer | Your tokens: `[{"id": 7, "name": "CI", "scope": "write", "createdAt": …, "expiresAt": 0, "lastUsed": …}]` |
| `POST /tokens` · viewer | Body `{"name": "CI", "scope": "write", "expiresDays": 90}` → `{"token": "rm_…", "scope": "write"}`. `scope` is `read`, `write` or `full` and defaults to `full`, `expiresDays` to `0` (never; max 3650). **The token is returned only here** |
| `DELETE /tokens/{id}` · viewer | Revoke one of your tokens |

Administrators cannot list or create other users' tokens. To cut off a user, disable or delete the account.

## Users

All admin.

| Call | |
|---|---|
| `GET /users` | `[{"username": "alice", "email": "a@example.com", "role": "developer", "disabled": false, "createdAt": …, "lastLogin": …}]` |
| `POST /users` | Body `{"username", "password", "email", "role"}`. User names: 2–64 chars, letters, digits, `. _ @ -`. Passwords: 8–72 chars. 409 if the name exists |
| `PUT /users/{name}` | Body `{"email", "role", "disabled", "password"}`. `role` is required; an empty `password` leaves it unchanged. Setting a password or `disabled: true` signs the user out everywhere |
| `DELETE /users/{name}` | Deletes the account with its tokens and access overrides. Published artifacts are kept |

You cannot delete, disable or demote your own account (so the last administrator cannot lock everyone out by accident).

`PUT /auth/password` · viewer, session or full token — change your own password: `{"current": "…", "new": "…"}`.

## Repository access

Admin. Per-user overrides of a repository ([meaning](security.md#roles-and-repository-access)).

| Call | |
|---|---|
| `GET /repositories/{name}/permissions` | `[{"repo": "team-npm", "username": "bob", "level": "write"}]` |
| `PUT /repositories/{name}/permissions` | **Replaces** the whole list. Body: `[{"username": "bob", "level": "write"}, {"username": "eve", "level": "none"}]`. Levels: `none`, `read`, `write`. An empty list removes all overrides |

## Notification channels

Admin. See [notifications.md](notifications.md).

```json
{
  "id": 1, "name": "Ops phone", "kind": "gotify", "url": "https://gotify.example.com",
  "hasToken": true, "priority": 4, "alertPriority": 8,
  "events": ["repository.degraded", "repository.recovered", "task.failed"], "repos": [],
  "enabled": true, "createdAt": 1760000000, "lastStatus": "200 OK", "lastDelivery": 1760003600
}
```

| Call | |
|---|---|
| `GET /notifiers` | All channels (without tokens) |
| `POST /notifiers` | Create. Required: `name`, `url`, `token`. `kind` defaults to `gotify`. Send `priority` and `alertPriority` (0–10) and `enabled` explicitly: omitted numbers are 0 and an omitted `enabled` is `false`. → `{"id": 1}` |
| `PUT /notifiers/{id}` | Replace. An empty `token` keeps the stored one |
| `DELETE /notifiers/{id}` | Remove |
| `POST /notifiers/{id}/test` | Send a test message now → `{"ok": true, "status": "200 OK"}` or `{"ok": false, "status": "<what went wrong>"}` |

`events`: event names, or prefixes ending in `*`; empty = all. `repos`: repository names; empty = all.

## Webhooks

Admin. Payload and signature: [notifications.md](notifications.md#webhooks).

| Call | |
|---|---|
| `GET /webhooks` | `[{"id", "name", "url", "hasSecret", "events", "repos", "enabled", "createdAt", "lastStatus", "lastDelivery"}]` |
| `POST /webhooks` | Body `{"name", "url", "secret", "events": [], "repos": [], "enabled": true}` |
| `PUT /webhooks/{id}` | Replace. An empty `secret` keeps the stored one |
| `DELETE /webhooks/{id}` | Remove |
| `POST /webhooks/{id}/test` | Deliver a `webhook.test` event now → `{"status": "200 OK"}` |

## Tasks

Admin. Maintenance jobs ([what they do](operations.md#maintenance-tasks)).

| Call | |
|---|---|
| `GET /tasks` | `[{"id": "cleanup", "name": "…", "description": "…", "schedule": "Hourly", "nextRun": …, "lastRun": …, "durationMs": 12, "result": "Deleted 0 assets (0 B)", "error": "", "running": false}]` |
| `POST /tasks/{id}/run` | Start now → `{"started": true}`. 409 if it is already running. Poll `GET /tasks` for the result |

Task ids: `cleanup`, `purge`, `blob-compact`, `optimize-db`, `blob-verify`, `rebuild-indexes`.

## Settings

| Call | |
|---|---|
| `GET /settings` · viewer | `{"anonymousAccess": true, "baseUrl": "https://repo.example.com", "banner": "", "auditRetentionDays": 90}` |
| `PUT /settings` · admin | Replace all four. `baseUrl` must be absolute (`http(s)://host`) or empty; `auditRetentionDays` 0 keeps events forever |
| `GET /settings/oidc` · admin | Single sign-on configuration (`clientSecret` is never returned; see `hasSecret`) |
| `PUT /settings/oidc` · admin | Fields: `enabled`, `issuer`, `clientId`, `clientSecret`, `scopes`, `buttonLabel`, `usernameClaim`, `defaultRole`, `autoCreate`, `mergeEnabled`. Enabling validates the issuer by fetching its discovery document |
| `POST /settings/oidc/test` · admin | Body `{"issuer": "https://…"}` → `{"ok": true, "message": "Found 2 signing key(s).", …}` |

## Audit log

`GET /audit?q=<text>&action=<prefix>&from=<unix>&to=<unix>` · admin — matching events, newest first; paged,
default limit 1000. `from` (inclusive) and `to` (exclusive) limit the period, in Unix seconds; leave either
out, or pass 0, for an open end.
`q` matches user, action or target; `action` is a prefix such as `login` or `repository.`.

`GET /audit/export?q=<text>&action=<prefix>&from=<unix>&to=<unix>` · admin — every matching event as CSV
(`time,user,action,target,detail`, times in UTC).

```json
[{"id": 981, "ts": 1760003600, "username": "alice", "action": "docker.push", "target": "docker-hosted/team/api:1.4.0", "detail": ""}]
```

Event names: [catalogue](notifications.md#event-catalogue). For continuous export, use a webhook instead of polling.

## System

| Call | |
|---|---|
| `GET /stats` · viewer | Dashboard numbers for the repositories the caller can read: totals, per-format usage, repositories needing attention, recent and popular artifacts |
| `GET /system/info` · admin | Version, commit, platform, uptime, data directory, database and blob sizes, memory, request counters, signing key fingerprint, `tls`, `initialPasswordPending` |
| `GET /system/export` · admin | Configuration as JSON: settings, repositories, users, access overrides, webhooks, notification channels — **without** passwords, secrets, tokens or artifacts |
| `POST /system/import` · admin | Apply an export (see [operations](operations.md#configuration-export-and-import)) → `{"created": […], "updated": […], "errors": […], "usersNeedingPassword": […]}` |
| `GET /signing-key` · public | The OpenPGP public key that signs APT/YUM indexes (ASCII-armored) |
| `GET /version` · public | `{"name": "RepoMan", "apiVersion": "v1"}`. The software version is deliberately not public; see `/system/info` |

Outside `/api`:

| Call | |
|---|---|
| `GET /healthz` · public | `200 ok` when the server and its database are up, `503` otherwise. For load balancers and monitors |
| `GET /metrics` · any account | Prometheus text format ([operations](operations.md#monitoring)) |

## Package-client endpoints

These implement each ecosystem's own protocol and are documented by that ecosystem; listed here for firewall
rules and reverse-proxy configuration.

| Path | Protocol |
|---|---|
| `/repository/<repo>/…` | Maven, npm, PyPI (`simple/`, `packages/`), Go (`@v/`, `@latest`, `sumdb/`), APT (`dists/`, `pool/`), YUM (`repodata/`), Conda (`<subdir>/repodata.json`, `<subdir>/<package>`), raw |
| `/repository/<apt-or-yum-repo>/repository.key` | The signing public key |
| `/v2/…` | Docker Registry HTTP API v2 / OCI Distribution; image names are `<repo>/<image>` |

They authenticate with HTTP basic auth (user name + token or password) or a bearer token, and honour the
anonymous-access setting for reads.

## Recipes

**Provision a hosted + proxy + group trio**

```bash
api() { curl -fsS -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
B=https://repo.example.com/api/v1
api -d '{"name":"team-pypi-hosted","format":"pypi","type":"hosted"}' $B/repositories
api -d '{"name":"team-pypi-proxy","format":"pypi","type":"proxy","remoteUrl":"https://pypi.org","metadataTtl":60,"cleanupDays":180}' $B/repositories
api -d '{"name":"team-pypi","format":"pypi","type":"group","members":["team-pypi-hosted","team-pypi-proxy"]}' $B/repositories
```

**Create a CI account with a publish token** (the token must be created *as* that user)

```bash
api -d '{"username":"ci-release","password":"'"$(openssl rand -base64 24)"'","role":"viewer"}' $B/users
api -X PUT -d '[{"username":"ci-release","level":"write"}]' $B/repositories/team-pypi-hosted/permissions
# then sign in as ci-release (or use its password once with basic auth) to mint its token:
curl -fsS -u "ci-release:$PASSWORD" -H 'Content-Type: application/json' \
  -d '{"name":"pipeline","scope":"write","expiresDays":365}' $B/tokens
```

**Is anything unhealthy?**

```bash
api $B/repositories | jq -r '.[] | select(.status != "online") | "\(.name): \(.status) \(.health.lastError // "")"'
```

**Free space now**

```bash
api -X POST "$B/repositories/docker-hub/cleanup?dryRun=1" | jq '{count, size}'
api -X POST  $B/repositories/docker-hub/cleanup
```

**Nightly configuration backup**

```bash
api $B/system/export > "repoman-config-$(date +%F).json"
```

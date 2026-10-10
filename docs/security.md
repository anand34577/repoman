# Security

How RepoMan protects your artifacts and accounts, what you should configure, and where its limits are.
To report a vulnerability, see [SECURITY.md](../SECURITY.md).

- [Hardening checklist](#hardening-checklist)
- [Accounts and sign-in](#accounts-and-sign-in)
- [Roles and repository access](#roles-and-repository-access)
- [API tokens and scopes](#api-tokens-and-scopes)
- [Single sign-on (OpenID Connect)](#single-sign-on-openid-connect)
- [Built-in protections](#built-in-protections)
- [What is stored and how](#what-is-stored-and-how)
- [Supply-chain notes](#supply-chain-notes)
- [Known limits](#known-limits)

## Hardening checklist

In rough order of importance:

- [ ] **Change the first-run admin password** (the dashboard reminds you until you do).
- [ ] **Serve over HTTPS** — built-in TLS or a reverse proxy ([configuration](configuration.md#https)).
- [ ] **Decide on anonymous access** (*Settings → General*). On by default: anyone who can reach the server can
      download from repositories not marked private. Turn it off, or mark sensitive repositories **Private**,
      unless the whole network is trusted. Publishing always requires an account.
- [ ] **Behind a reverse proxy, set `RM_TRUSTED_PROXIES` and the Base URL** ([why](configuration.md#behind-a-reverse-proxy)).
- [ ] **Give people their own accounts** with the lowest role that works, instead of sharing `admin`.
- [ ] **Use tokens, not passwords, in tools** — one per machine or pipeline, with the narrowest scope and an
      expiry date. Read-only for machines that only install; read & publish for CI that releases.
- [ ] **Put hosted repositories first in groups**, so internal packages cannot be shadowed by public ones.
- [ ] **Back up the data directory** and protect the backups like the server itself: they contain password
      hashes, the signing key and upstream credentials ([operations](operations.md#backup-and-restore)).
- [ ] **Set up notifications** for `login.lockout`, `repository.degraded` and `task.failed`
      ([notifications](notifications.md)).
- [ ] **Keep RepoMan updated**, and run it as the unprivileged service the installers set up — never as root.
- [ ] Optional: storage **quotas** on hosted repositories so a runaway pipeline cannot fill the disk; a
      separate disk or volume for the data directory.

## Accounts and sign-in

- **No default password.** The `admin` password is generated randomly on first start (or taken from
  `RM_ADMIN_PASSWORD`), shown once in the log and kept in `<data dir>/initial-admin-password` (owner-readable)
  only until you change it.
- Passwords are 8 to 72 characters and stored as **bcrypt** hashes. There are no composition rules; length is
  what matters. Use a password manager.
- **Lockout:** after 10 failed sign-ins from one client address, that address is refused for 15 minutes —
  in the browser, with basic authentication from package clients, `npm login`, and SSO account linking alike.
  The lockout is recorded as a `login.lockout` event. It is per address, so one person's typos never lock
  out a colleague — provided RepoMan sees real client addresses ([trusted proxies](configuration.md#behind-a-reverse-proxy)).
  Lockouts are held in memory and cleared by a restart.
- **Sessions** are random 192-bit values in an `HttpOnly`, `SameSite=Lax` cookie (also `Secure` over HTTPS),
  valid for 7 days. Signing out deletes the session on the server. **Changing a password signs out every
  other session** of that account; an administrator resetting someone's password or disabling them signs
  them out everywhere immediately.
- **Disabled users** cannot sign in and their tokens stop working at once, but nothing is deleted.
- Unknown and known user names take the same time to reject, so sign-in cannot be used to discover accounts.
- **Lost admin access:** `repoman reset-password` on the server ([operations](operations.md#reset-a-lost-password)).
  It requires file-system access to the data directory, which is the point: whoever controls the machine
  controls RepoMan.

## Roles and repository access

Every account has one role:

| Role | Can |
|---|---|
| **viewer** | Browse, search and download from repositories they can read |
| **developer** | …and publish and delete artifacts |
| **admin** | …and manage repositories, users, settings, SSO, webhooks, notifications, tasks, and read the audit log |

On top of the role, each repository's **Access** tab can set a per-user level:

| Level | Effect for that user on that repository |
|---|---|
| `none` | Hidden: not listed, not searchable, requests are refused |
| `read` | Download only — even for a developer |
| `write` | Download, publish, delete — even for a viewer |

Evaluation order: administrators always have full access → a per-repository override, if one exists →
otherwise the role (developer = write, viewer = read). Not signed in: read, if anonymous access is on and the
repository is not private; nothing otherwise.

Typical patterns:

- *A team may publish only to its own repository:* make its members **viewers** and give them `write` on that
  repository.
- *A sensitive repository:* mark it **Private** and set `none` for everyone who should not see it.
- *A CI account:* a dedicated **developer** user per pipeline, with a read & publish token.

A request for a repository the caller cannot read is answered as if the repository did not exist in the
management API, so names are not disclosed.

## API tokens and scopes

Create tokens under **Account & tokens**. A token is shown once; RepoMan stores only its SHA-256 hash.
Tokens look like `rm_` followed by 48 hexadecimal characters.

Use a token:

- as the **password** in any package client (with your user name), or
- as `Authorization: Bearer rm_…` for the [REST API](api.md).

| Scope | Allows | Use for |
|---|---|---|
| **Read-only** (`read`) | Browse, search, download. Every write and every administrative call is refused | Build agents, servers, developers' machines that only install |
| **Read & publish** (`write`) | The above, plus publish and delete artifacts. No administration, no changes to the account | CI pipelines that release packages or images |
| **Full access** (`full`) | Everything the owning account can do, including the management API | Infrastructure automation (Terraform-style provisioning, backups of configuration) |

Rules that hold for every token:

- A token **never exceeds its owner's role and repository access**. A read & publish token of a viewer
  cannot publish (unless a repository grants that viewer `write`).
- Only a browser session or a **full** token can create or revoke tokens, change the password, or link an
  SSO identity. A leaked narrow token cannot be used to mint a broader one.
- Tokens stop working the moment they expire, are revoked, or their owner is disabled or deleted.
- Expiry can be 30 days, 90 days, a year or never (API: any number of days up to 3650). Prefer short lifetimes
  and rotation; expired tokens are purged hourly.
- "Last used" is recorded (to the minute) so unused tokens can be found and revoked.
- `GET /api/v1/auth/me` with a token shows the account and the token's scope — handy to check what a
  credential found in a pipeline can do.

Tokens created by the **Generate token** button on a repository's Setup tab are read & publish for hosted
repositories and read-only for proxies and groups, valid for a year.

## Single sign-on (OpenID Connect)

*Settings → Single sign-on* connects any OpenID Connect provider: Keycloak, Microsoft Entra ID, Okta,
Authentik, Google, GitLab and others.

1. In the provider, register a **web application client** (confidential with a client secret, or public —
   PKCE is always used) with the redirect URI
   `https://<your base URL>/api/auth/oidc/callback`.
2. In RepoMan enter the **issuer URL** (RepoMan appends `/.well-known/openid-configuration`), client ID and
   client secret. *Test connection* checks discovery and signing keys.
3. Choose what happens when someone signs in for the first time: create an account with a default role
   (*viewer* unless you change it), and/or let people link the identity to an existing local account.

Security properties:

- Authorization-code flow with **PKCE**, `state` bound to the browser by a cookie, and a `nonce` checked in
  the ID token. ID token signatures (RS256/384/512, PS*, ES256/384) are verified against the provider's
  published keys; issuer, audience and expiry are checked.
- **Accounts are never matched by name or e-mail address.** An SSO identity reaches an existing local account
  only if that account's owner proves it — by starting the link while signed in, or by entering the local
  password right after the first SSO sign-in. This prevents takeover through a provider account that merely
  has the same e-mail.
- SSO sign-in works in the browser. **Package clients and the API always use tokens**; SSO users create them
  under *Account* like everyone else.
- Accounts created through SSO have no local password until the user sets one, and cannot unlink SSO before
  doing so (they would lock themselves out).
- Removing someone at the identity provider prevents new SSO sign-ins but does **not** invalidate their
  existing RepoMan tokens. Disable or delete the RepoMan account as part of offboarding.
- Provider groups are not mapped to roles; an administrator assigns roles in RepoMan.
- RepoMan must be able to reach the issuer. With an internal CA, see
  [outbound connections](configuration.md#outbound-connections).

## Built-in protections

| Threat | Protection |
|---|---|
| Stolen or guessed credentials | bcrypt, lockout, scoped and expiring tokens, audit log, lockout notifications |
| Cross-site request forgery | `SameSite` cookies **and** a server-side check: a state-changing request that carries the session cookie is refused unless the browser marks it same-origin (`Sec-Fetch-Site`, falling back to `Origin`). API clients using tokens are unaffected |
| Cross-site scripting through uploaded content | Everything under `/repository/` and `/v2/` is served with `Content-Security-Policy: sandbox`, so HTML or SVG in a repository cannot run scripts or reach the session |
| Malicious scripts in the UI | The UI is served with `default-src 'self'; script-src 'self'; object-src 'none'; frame-ancestors 'none'`: no inline or third-party scripts, no embedding in other sites |
| Clickjacking, MIME sniffing, referrer leaks | `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`, `Cross-Origin-Opener-Policy: same-origin`; API responses are `Cache-Control: no-store` |
| Protocol downgrade | `Strict-Transport-Security` whenever the request arrived over HTTPS |
| Spoofed client address or host name | `X-Forwarded-*` headers are honoured only from `RM_TRUSTED_PROXIES` |
| Path traversal | Artifact paths are validated; content is stored under its SHA-256, never under a client-supplied file name |
| Tampered content | Docker uploads are verified against their digest; proxied PyPI files are fetched only from URLs the upstream index listed; APT/YUM indexes are signed |
| Dependency confusion | Group member order (hosted first); Go checksum lookups for hosted modules are never sent upstream |
| Slow-request attacks | Header read timeout, idle timeout, 1 MB limit on management API bodies |
| Privilege escalation on the host | Unprivileged service account; sandboxed systemd unit; `FROM scratch` container image running as non-root with a read-only root file system |
| Leaks through logs | Query strings, bodies, passwords and tokens are never logged |

## What is stored and how

Everything is in the data directory (`RM_DATA_DIR`), created with owner-only permissions:

| Item | Stored as |
|---|---|
| User passwords | bcrypt hashes |
| API tokens and sessions | SHA-256 hashes (the token itself cannot be recovered) |
| Artifacts | Files named by their SHA-256 under `blobs/` |
| **Upstream (proxy) passwords** | **Plain text** in the database — RepoMan must be able to send them |
| **OIDC client secret, webhook secrets, Gotify tokens** | **Plain text** in the database, for the same reason |
| **APT/YUM signing key** (private) | **Unencrypted** in the database |
| First-run admin password | Plain text file `initial-admin-password`, deleted on first password change |

These secrets are never returned by the API or shown in the UI after saving, and are excluded from the
configuration export. But anyone who can read the data directory or a backup of it can extract them. Therefore:

- keep the directory's permissions as created (0700, service account only; on Windows: administrators and SYSTEM);
- encrypt the disk or volume (LUKS, BitLocker, FileVault, encrypted cloud volumes);
- encrypt backups and restrict who can read them;
- give upstream registries dedicated low-privilege credentials (read-only pull accounts), so a leak is limited.

## Supply-chain notes

- **RepoMan does not scan artifacts** for vulnerabilities or malware. Pair it with a scanner in CI
  (Trivy, Grype, OSV-Scanner, `npm audit`, `pip-audit`) — pointing at RepoMan for downloads.
- A proxy gives you **immutability**: once cached, an artifact cannot be swapped upstream without you
  noticing, and a version deleted upstream stays available to your builds.
- Hosted repositories refuse to overwrite a published version unless *Allow redeploy* is on.
- The audit log records who published or deleted what, and when.
- Release archives and the container image carry signed **build provenance** (verify with
  `gh attestation verify`), and `SHA256SUMS` lists every archive's checksum.

## Known limits

Be aware of what RepoMan does not do, so you can compensate:

- **No encryption of secrets at rest** (see above).
- **No two-factor authentication** for local accounts. Use SSO with MFA at the provider for people, and keep
  local accounts for break-glass and automation.
- **Roles are coarse** (three roles plus per-repository overrides per user; no custom roles, no groups).
- **Administrators are fully trusted**: they configure outbound URLs (proxy remotes, webhooks, Gotify, OIDC)
  and so can make the server send requests to internal addresses. Do not hand the admin role to people who
  should not have that reach.
- **Any authenticated user can read `/metrics`** (it exposes repository names and sizes, not contents).
- **No rate limiting** beyond the sign-in lockout. Put a reverse proxy in front if you need request limits.
- **Uploads are limited only by quota** (and npm publishes by a 1 GB cap); a developer can fill a repository
  without a quota. Set quotas.
- **The audit log lives in the same database** as everything else; an administrator with file-system access
  can alter it. Forward events with a webhook to an external system if you need tamper-evident records.
- Session lockout state and SSO flows in progress are kept in memory and reset by a restart.

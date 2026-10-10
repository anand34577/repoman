# Configuration

RepoMan has two kinds of settings:

- **Process settings** — how the server itself runs: listen address, data directory, TLS. They are read once
  at start-up from the environment. This page is their reference.
- **Application settings** — repositories, users, single sign-on, notifications, webhooks, the public base URL,
  anonymous access. They live in the database and are changed in the web UI (or the [REST API](api.md)) while
  the server runs.

- [Where process settings come from](#where-process-settings-come-from)
- [Reference](#reference)
- [HTTPS](#https)
- [Behind a reverse proxy](#behind-a-reverse-proxy)
- [Base URL](#base-url)
- [Logging](#logging)
- [Outbound connections](#outbound-connections)
- [Command line](#command-line)

## Where process settings come from

In order of precedence (first wins):

1. **Command-line flags** — `--addr`, `--data-dir`, `--tls-cert`, `--tls-key`.
2. **Environment variables** — `RM_*`.
3. **An env file** — `KEY=VALUE` lines. RepoMan loads the file named by `--config` or `RM_CONFIG`; if neither
   is given, it looks for `repoman.env` next to the binary, then in the working directory.
4. **Defaults.**

The env file format:

```ini
# a comment: must be on its own line
RM_ADDR=:8081
RM_DATA_DIR=/var/lib/repoman
RM_TRUSTED_PROXIES="10.0.0.5, 10.0.0.6"
```

Values may be wrapped in single or double quotes (they are removed); everything after `=` is otherwise taken
literally, so do not put a comment at the end of a line. An optional leading `export ` is accepted, which
lets the same file be sourced by a shell.

A line that is not `KEY=VALUE` stops the server at start-up with the file name and line number, so typos
cannot go unnoticed. A variable already set in the real environment is never overridden by the file.

Changes take effect after a restart.

## Reference

| Variable | Flag | Default | Description |
|---|---|---|---|
| `RM_ADDR` | `--addr` | `:8081` | Address to listen on. `:8081` = all interfaces; `127.0.0.1:8081` = local only; `[::1]:8081` for IPv6 loopback |
| `RM_DATA_DIR` | `--data-dir` | `data` | Directory for the database, artifacts and logs. Created (mode 0700) if missing. Relative paths are resolved against the working directory — or, for a Windows service, against the folder of `repoman.exe` |
| `RM_ADMIN_PASSWORD` | — | random | Password for the `admin` account, used **only on the first start** with an empty data directory; ignored afterwards. Must be 8–72 characters. Remove it from your configuration once the server has started |
| `RM_TLS_CERT` | `--tls-cert` | — | PEM file with the certificate followed by its intermediate chain. Setting it (together with the key) makes RepoMan serve HTTPS |
| `RM_TLS_KEY` | `--tls-key` | — | PEM file with the private key. Both TLS settings must be given, or neither |
| `RM_TRUSTED_PROXIES` | — | `127.0.0.0/8, ::1` | Comma- or space-separated IP addresses and CIDR ranges of reverse proxies whose `X-Forwarded-*` headers are trusted. Set to an empty value to trust none. Host names are not accepted |
| `RM_LOG_FILE` | — | — | Also write the log to this file (see [Logging](#logging)) |
| `RM_ACCESS_LOG` | — | `true` | `false` stops logging one line per HTTP request |
| `RM_CONFIG` | `--config` | `repoman.env` | Path of the env file |
| `HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY` | — | — | Standard variables for reaching upstreams through a forward proxy |
| `SSL_CERT_FILE`, `SSL_CERT_DIR` | — | system | Certificate authorities used to verify upstreams (Linux, macOS, containers) |

## HTTPS

Credentials and tokens are sent on every request, so anything beyond a test on `localhost` should use HTTPS.
Docker, Podman and containerd refuse plain HTTP for any host other than `localhost`, and pip warns about it.
There are two ways.

### Option 1: built-in TLS

Simplest for a single machine. Point RepoMan at a certificate and key:

```ini
RM_ADDR=:443
RM_TLS_CERT=/etc/repoman/fullchain.pem
RM_TLS_KEY=/etc/repoman/privkey.pem
```

- TLS 1.2 and 1.3 are accepted; older protocol versions are refused.
- The certificate is **re-read automatically** within a minute of the file changing, so renewals (certbot,
  acme.sh, an internal CA) need no restart. If a renewed file is unreadable or invalid, RepoMan logs the
  error and keeps serving the previous certificate.
- A missing or invalid certificate at start-up stops the server with an explanation.
- `Strict-Transport-Security` is sent, so browsers remember to use HTTPS.
- Listening on port 443 needs a privilege on Linux: see [installation](installation.md#linux-systemd).
- RepoMan does not listen on port 80 to redirect to HTTPS. Clients should be configured with the `https://` URL.

With an **internal certificate authority**, every client must trust that CA (system trust store, plus the
tool's own setting where it has one: `NODE_EXTRA_CA_CERTS` for npm, `--cert` for pip, `/etc/docker/certs.d/<host>/ca.crt`
for Docker, a Java truststore for Maven/Gradle).

### Option 2: a reverse proxy terminates TLS

Use this when you already run nginx, Caddy, Traefik or a load balancer, or want automatic Let's Encrypt
certificates. Keep RepoMan on `127.0.0.1:8081` and read the next section — two things must be set or links and
security features will be wrong.

## Behind a reverse proxy

Do all three:

1. **Forward the original host and scheme.** The proxy must send `Host` unchanged (or `X-Forwarded-Host`),
   `X-Forwarded-Proto` and `X-Forwarded-For`.
2. **Tell RepoMan which proxies to trust**: `RM_TRUSTED_PROXIES`. By default only connections from the same
   machine (loopback) are trusted. If the proxy runs elsewhere — another host, another container — list its
   address or network. RepoMan *ignores* `X-Forwarded-*` from anyone else, because otherwise any client could
   forge its IP address (defeating the sign-in lockout and the audit log) or the public host name.
3. **Set the Base URL** in *Settings → General* to the public address, e.g. `https://repo.example.com`.

And two proxy settings that matter for package traffic:

- **No request size limit.** Image layers and packages can be gigabytes.
- **No request/response buffering and generous timeouts**, so large uploads stream through.

What goes wrong if step 2 is skipped: every user appears to come from the proxy's address, so ten failed
sign-ins by one person lock out everybody for 15 minutes; session cookies are not marked `Secure`; and
generated links may use `http://`.

### nginx

```nginx
server {
    listen 443 ssl;
    server_name repo.example.com;
    ssl_certificate     /etc/letsencrypt/live/repo.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/repo.example.com/privkey.pem;

    client_max_body_size 0;            # no upload limit (default is 1 MB!)
    proxy_request_buffering off;       # stream uploads
    proxy_buffering off;               # stream downloads
    proxy_read_timeout 900s;
    proxy_send_timeout 900s;

    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Serve RepoMan at the root of a host name. A sub-path such as `https://example.com/repoman/` is **not supported**:
the Docker protocol requires `/v2/` at the root, and the UI expects `/`.

### Caddy

```caddyfile
repo.example.com {
    reverse_proxy 127.0.0.1:8081
}
```

Caddy obtains the certificate, sets the forwarding headers and has no body limit by default.

### Traefik (labels)

```yaml
labels:
  - traefik.http.routers.repoman.rule=Host(`repo.example.com`)
  - traefik.http.routers.repoman.tls.certresolver=letsencrypt
  - traefik.http.services.repoman.loadbalancer.server.port=8081
```

and on the RepoMan container `RM_TRUSTED_PROXIES=<the Docker network's subnet>`, e.g. `172.18.0.0/16`.

### Apache httpd

```apache
<VirtualHost *:443>
    ServerName repo.example.com
    SSLEngine on
    SSLCertificateFile    /etc/ssl/repo.crt
    SSLCertificateKeyFile /etc/ssl/repo.key
    ProxyPreserveHost On
    AllowEncodedSlashes NoDecode
    RequestHeader set X-Forwarded-Proto "https"
    ProxyPass        / http://127.0.0.1:8081/ nocanon
    ProxyPassReverse / http://127.0.0.1:8081/
</VirtualHost>
```

`AllowEncodedSlashes NoDecode` and `nocanon` are required: scoped npm packages are requested as `@scope%2fname`.

### Checking it

After signing in through the proxy, open *Settings → System*: **Transport** should read
"HTTPS (terminated by a reverse proxy)". In the audit log, sign-ins should show the real client addresses in
`login.failed` events, not the proxy's.

## Base URL

*Settings → General → Base URL* is the address clients use, such as `https://repo.example.com`
(no trailing slash, no path). It is used for:

- download links inside npm package metadata,
- the single sign-on redirect address (`<base URL>/api/auth/oidc/callback`),
- the copy-paste snippets on every repository's Setup tab,
- links in notifications.

Leave it empty and RepoMan uses the address of each incoming request, which is right for simple setups
without a proxy. Set it whenever clients and browsers reach the server under a name that differs from what
RepoMan itself sees.

## Logging

RepoMan logs to standard error: start-up information, one line per request, and problems.

```
2026/10/10 20:23:02 200 GET /repository/npm-public/react 10.0.4.17 12ms
```

(status, method, path, client address, duration). Query strings are never logged, and neither are request
bodies, passwords or tokens.

| Running as | Where the log goes |
|---|---|
| Linux service | the journal: `journalctl -u repoman` |
| macOS service | `/usr/local/var/log/repoman.log` |
| Windows service | `<data dir>\logs\repoman.log` (a service has no console, so a file is used automatically) |
| Docker | `docker logs repoman` |
| Foreground | the terminal |

Set `RM_LOG_FILE` to *also* write to a file anywhere. That file is rotated when it reaches 20 MB and the five
most recent old files are kept (`repoman.log.1` … `.5`), so it never needs more than about 120 MB.

`RM_ACCESS_LOG=false` drops the per-request lines and keeps everything else. Security-relevant activity
(sign-ins, failures, changes, deletions) is recorded in the audit log in the database either way.

## Outbound connections

RepoMan makes outbound connections only to:

- the remote URL of each **proxy repository** (and hosts that remote redirects to),
- your **OpenID Connect** provider, if single sign-on is enabled,
- your **webhook** URLs and **Gotify** servers, if configured.

Nothing else: no update checks, no telemetry, no license server. [firewall.md](firewall.md) lists the hosts
for common upstreams.

**Through a forward proxy.** Set the standard variables; they apply to all outbound connections:

```ini
HTTPS_PROXY=http://proxy.internal:3128
HTTP_PROXY=http://proxy.internal:3128
NO_PROXY=localhost,127.0.0.1,.internal,10.0.0.0/8
```

Put your Gotify, SSO and internal mirror hosts in `NO_PROXY` if they must be reached directly. Proxies that
need authentication: `http://user:password@proxy.internal:3128`.

**Upstreams with a private CA.** RepoMan verifies TLS certificates with the operating system's trust store and
has no "skip verification" switch. Add your CA to the system store (`update-ca-certificates` on Debian/Ubuntu,
`update-ca-trust` on RHEL, Keychain on macOS, the *Trusted Root* store on Windows) and restart RepoMan. In the
container image, mount a bundle and set `SSL_CERT_FILE` ([example](installation.md#docker)).

**A TLS-inspecting firewall** re-signs traffic with its own CA; treat that CA like a private CA above.

## Command line

```
repoman [serve] [flags]          run the server (default)
repoman version                  print version, commit, build date, Go version, platform
repoman healthcheck [--url URL]  exit 0 if the local server answers /healthz, 1 otherwise
repoman reset-password [user]    set a new password for a local account (default: admin)
repoman service <action>         Windows only: install | uninstall | start | stop | status
repoman help
```

`serve`, `healthcheck` and `reset-password` accept `--config`, `--addr`, `--data-dir`, `--tls-cert` and
`--tls-key`, and read the same environment and env file, so they find the server and its data without extra
arguments when run the same way as the server.

- `healthcheck` probes `/healthz` on the configured listen address over loopback (it does not verify the
  TLS certificate name, since the certificate is issued for the public name, not `127.0.0.1`). It is what the
  container health check runs.
- `reset-password` is described in [operations.md](operations.md#reset-a-lost-password).

Exit codes: `0` success, `1` failure (the reason is printed), `2` unknown command.

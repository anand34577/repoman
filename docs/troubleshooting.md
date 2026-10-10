# Troubleshooting

Find the symptom, apply the fix. If yours is not here, the server log and the repository's status badge
usually name the cause; see [Where to look](operations.md#where-to-look).

- [Starting the server](#starting-the-server)
- [Signing in](#signing-in)
- [Behind a reverse proxy](#behind-a-reverse-proxy)
- [Proxy repositories and upstreams](#proxy-repositories-and-upstreams)
- [Publishing](#publishing)
- Clients: [Maven / Gradle](#maven--gradle) · [npm](#npm) · [pip](#pip--pypi) · [Docker](#docker) · [Go](#go) · [apt](#apt) · [dnf / yum](#dnf--yum)
- [Notifications and webhooks](#notifications-and-webhooks)
- [Storage](#storage)

## Starting the server

| Symptom | Cause and fix |
|---|---|
| `cannot listen on :8081 … address already in use` | Another process (or another RepoMan) has the port. Stop it or choose another `RM_ADDR` |
| `cannot listen on :443 … permission denied` | Ports below 1024 need a privilege on Linux. Grant `CAP_NET_BIND_SERVICE` ([installation](installation.md#linux-systemd)) or use a high port behind a reverse proxy |
| `opening data directory …: permission denied` | The service account cannot write `RM_DATA_DIR`. Fix ownership: `chown -R repoman:repoman /var/lib/repoman`; for Docker bind mounts `chown -R 10001:10001 <dir>` |
| `opening data directory …: read-only file system` (systemd) | You moved the data directory: allow it with `ReadWritePaths=` in a unit drop-in |
| `creating the initial admin account (RM_ADMIN_PASSWORD): password must be at least 8 characters` | Choose a longer first-run password, or unset the variable to get a generated one |
| `config file: repoman.env:7: expected KEY=VALUE` | A malformed line in the env file; fix that line. Comments must be on their own line, starting with `#` |
| `RM_TLS_CERT and RM_TLS_KEY must be set together` | Set both or neither |
| `loading TLS certificate: … private key does not match public key` | The certificate and key files belong to different certificates |
| `loading TLS certificate: … no such file` / `permission denied` | Wrong path, or the service account cannot read the files (under systemd they must be outside `/home` and `/root`) |
| `database is locked` at start-up, or `disk I/O error` | The data directory is on a network file system (NFS/SMB), or another RepoMan process is using it. Use a local disk; run one instance |
| Container exits at once, log shows *permission denied* on `/data` | Bind mount not writable by UID 10001 |
| Windows service starts and stops immediately | Read `<data dir>\logs\repoman.log`. Usually a bad `repoman.env` next to `repoman.exe`, or the port is in use |
| The UI shows "UI not built" | You built the binary from source without building the frontend first: `npm --prefix frontend ci && npm --prefix frontend run build`, then rebuild |

## Signing in

| Symptom | Cause and fix |
|---|---|
| I don't know the admin password | First start: it is in the log and in `<data dir>/initial-admin-password`. Later: `repoman reset-password` ([how](operations.md#reset-a-lost-password)) |
| The default password `admin123` no longer works | By design: there is no default password since 1.2. See the previous row |
| *too many failed sign-in attempts; try again in 15 minutes* | Ten failures from your address. Wait, or restart RepoMan to clear the lockout. If *everyone* is locked out together, RepoMan sees all users under one address: set `RM_TRUSTED_PROXIES` ([configuration](configuration.md#behind-a-reverse-proxy)) |
| Sign-in succeeds, then the page asks to sign in again | The session cookie is not kept. Typical causes: the site is opened over plain HTTP through a proxy that claims HTTPS (cookie is marked `Secure`); cookies blocked for the site; or the clock of the server is far off |
| *cross-site request blocked* (403) | A browser request reached RepoMan with an `Origin` different from the host it sees. Behind a proxy, forward the `Host` header unchanged and set the Base URL. Browser extensions that rewrite requests can also cause it |
| Package client gets 401 with a correct token | The token expired or was revoked, the account is disabled, or the client's address is locked out after earlier failures. Check `curl -u user:token <base>/api/v1/auth/me` |
| Package client gets 403 | The account or token may read but not write here: a read-only token, a viewer role, or a `read`/`none` override on the repository. The message of `…/auth/me` shows the token's scope |
| SSO: *Sign-in session expired or was not started in this browser* | The flow took more than 10 minutes, cookies are blocked, or the browser returned to a different host name than it started from. Start again from the RepoMan sign-in page, and make the Base URL match what users type |
| SSO: *redirect_uri mismatch* (shown by the provider) | Register exactly `<base URL>/api/auth/oidc/callback` at the provider, with the right scheme and host |
| SSO: *provider reports issuer …, which does not match* | Use the issuer URL exactly as the provider publishes it (Entra ID: `https://login.microsoftonline.com/<tenant>/v2.0`; Keycloak: `https://host/realms/<realm>`) |
| SSO: *No account is linked to this SSO identity* | Automatic account creation is off and the identity is not linked. Enable creation, or let the user link from *Account* |

## Behind a reverse proxy

| Symptom | Cause and fix |
|---|---|
| *413 Request Entity Too Large* on push/publish | The proxy's body limit. nginx: `client_max_body_size 0;`. Kubernetes ingress-nginx: `proxy-body-size: "0"` |
| Large uploads stall or time out | Request buffering and short timeouts: `proxy_request_buffering off;` and raise read/send timeouts |
| Links or npm tarball URLs point to `http://` or an internal name | Set *Settings → Base URL*; make the proxy send `X-Forwarded-Proto` and trust it via `RM_TRUSTED_PROXIES` |
| Audit log shows the proxy's IP for every user | `RM_TRUSTED_PROXIES` does not include the proxy's address |
| Scoped npm packages (`@scope/name`) give 404 through Apache | Add `AllowEncodedSlashes NoDecode` and `nocanon` |
| Docker login works, push fails with *unknown blob* or redirects | RepoMan must be served at the root of the host name, not under a sub-path |

## Proxy repositories and upstreams

| Symptom | Cause and fix |
|---|---|
| Badge **Remote unavailable** | Hover it for the error. RepoMan cannot reach the remote: firewall, DNS, forward proxy, or the upstream is down. Cached content is still served. Use *Test connection* on the Configuration tab after fixing |
| *the upstream of X is unreachable and this file is not cached yet; next attempt in N s* | Same cause; the file was never downloaded before. RepoMan retries the upstream every 30 seconds while it is down |
| `x509: certificate signed by unknown authority` | The upstream (or a TLS-inspecting firewall) uses a CA the server does not trust. Add the CA to the system trust store ([configuration](configuration.md#outbound-connections)) |
| `dial tcp: lookup …: no such host` | DNS on the RepoMan host cannot resolve the upstream. In containers check the DNS configuration |
| `proxyconnect tcp: …` | The forward proxy in `HTTPS_PROXY` is unreachable or refuses. Check the URL and `NO_PROXY` |
| Docker Hub: `429 Too Many Requests` | Anonymous pull limit reached for your public IP. Add Docker Hub credentials to the proxy repository |
| A new upstream version is not visible | Index data is cached for the *metadata cache time*. Use *Invalidate cache*, or lower the time |
| A package deleted upstream is still served | Intended: cached artifacts are immutable. Delete it in RepoMan if you want it gone |
| A file that exists upstream returns 404 for five minutes | "Not found" answers are remembered for 5 minutes. *Invalidate cache* clears them |
| First download of a big file is slow to start | The proxy downloads the whole file before serving it the first time. Later downloads are immediate |

## Publishing

| Symptom | Cause and fix |
|---|---|
| *cannot deploy to a group repository* / *proxy repository* (405) | Publish to the **hosted** repository; read from the group |
| *repository does not allow updating assets* (400) · npm *cannot publish over existing version* (403) | That version already exists and the repository is immutable. Publish a new version, delete the old one, or enable *Allow redeploy* |
| *507 … has reached its … MB storage quota* | Raise the quota, or free space with a cleanup policy |
| *503 repository … is offline* | An administrator took the repository offline (Configuration tab) |
| *401* when publishing although downloads work | Downloads are anonymous, publishing never is: the client is not sending credentials for this URL. Check the credential entry matches the *hosted* repository URL |
| UI upload: *browser upload isn't available for … repositories* | npm, PyPI and Docker packages are published with their own clients (they carry metadata a plain file upload lacks) |

## Maven / Gradle

| Symptom | Cause and fix |
|---|---|
| `Could not find artifact …` right after fixing something | Maven remembers failures: run with `-U`. Gradle: `--refresh-dependencies` |
| `Return code is: 401` on deploy | `<server><id>` in `settings.xml` must equal the `<id>` in `distributionManagement` |
| `Return code is: 400` on deploy | Redeploying a release version (see Publishing) |
| `PKIX path building failed` | The JVM does not trust the server certificate: import your CA into the JDK truststore (`keytool -importcert …`) |
| `Blocked mirror for repositories` (Maven 3.8.1+) | Maven blocks plain `http://` repositories. Serve RepoMan over HTTPS |
| Gradle plugins cannot be resolved | Add a proxy of `https://plugins.gradle.org/m2` to the group and declare the group in `pluginManagement` |

## npm

| Symptom | Cause and fix |
|---|---|
| `npm login` opens a browser and fails | Use `npm login --auth-type=legacy --registry=<url>`, or better, a token in `.npmrc` |
| `E401` although `.npmrc` has a token | The `//host/path/:_authToken=` line must match the registry URL exactly (host, port, path, trailing slash), without the scheme |
| Tarballs download from the wrong host | Set the Base URL |
| `npm audit` fails with 404 | Not supported; set `audit=false` in `.npmrc` |
| `E403 cannot publish over existing version` | See Publishing |
| `npm ERR! code E404` for a scoped package through Apache httpd | Encoded slashes: see reverse proxy section |
| pnpm/Yarn ignore the token | They read the same `.npmrc` keys; for Yarn Berry use `.yarnrc.yml` (`npmRegistryServer`, `npmAuthToken`, `npmAlwaysAuth: true`) |

## pip / PyPI

| Symptom | Cause and fix |
|---|---|
| `Could not find a version that satisfies…` / empty results | The index URL must end in `/simple/` |
| `The repository located at … is not a trusted or secure host` | Plain HTTP. Use HTTPS, or add `--trusted-host <host>` |
| `401` with credentials in the URL | Special characters in the password must be URL-encoded. Tokens (`rm_…`) need no encoding |
| twine: `HTTPError: 400` on upload | The file already exists (see Publishing), or the upload URL includes `/simple/` — it must be the repository root |
| A package exists on pypi.org but pip gets 404 here | Recently published: wait for the metadata cache time or *Invalidate cache* |

## Docker

| Symptom | Cause and fix |
|---|---|
| `http: server gave HTTP response to HTTPS client` | Docker requires HTTPS except for `localhost`. Enable [TLS](configuration.md#https), or add the host to `insecure-registries` in `/etc/docker/daemon.json` and restart Docker |
| `x509: certificate signed by unknown authority` | Private CA: put it in `/etc/docker/certs.d/<host:port>/ca.crt` (Docker) or the system trust store (containerd/Podman) |
| `name unknown: image names must be <docker-repository>/<image>` | The first path segment must be a Docker repository in RepoMan: `host/docker-hosted/app`, not `host/app` |
| `manifest unknown` pulling an official image through the Docker Hub proxy | Add `library/`: `host/docker-hub/library/nginx` |
| `denied: insufficient permissions` on push | The account or token cannot write to that repository |
| `unauthorized: authentication required` on pull | Anonymous access is off or the repository is private: `docker login <host>` first |
| Push of a large layer fails with 413 or hangs | Reverse proxy body limit / buffering (see above) |
| `denied: repository does not allow updating assets` when pushing an existing tag | *Allow redeploy* is off for the repository (immutable tags). Push a new tag, or enable it |
| Kubernetes: `ImagePullBackOff` | Missing `imagePullSecrets`, or the node does not trust the certificate. `kubectl describe pod` shows which |

## Go

| Symptom | Cause and fix |
|---|---|
| `verifying module: … 404 Not Found` for a private module | It is not in the public checksum database: `go env -w GONOSUMDB=<your module prefix>` |
| `verifying module: … dial tcp … sum.golang.org` | `GOPROXY` points at a *hosted-only* repository, which has no checksum relay. Use the group (or the proxy), or set `GONOSUMDB` |
| `reading …/@v/list: 401` | Anonymous access is off: add credentials to `~/.netrc` |
| `invalid version` / upload answers 400 | Versions must be canonical semver with a `v` prefix: `v1.2.3` |
| `go` contacts github.com directly | `GOPROXY` contains `,direct` and the proxy answered "not found". Remove `,direct` on isolated networks |
| A newly tagged upstream version is not found | The version list is cached for the metadata cache time; *Invalidate cache* |

## apt

| Symptom | Cause and fix |
|---|---|
| `NO_PUBKEY …` / `The following signatures couldn't be verified` | Import the key from `…/repository/<repo>/repository.key` and reference it with `signed-by=` |
| `The repository … does not have a Release file` | Wrong URL or distribution name. Hosted: the suite is the repository's *Distribution* (default `stable`), component `main` |
| `401 Unauthorized` | Add credentials in `/etc/apt/auth.conf.d/` (the `machine` line must match host and path) |
| `Hash Sum mismatch` through a proxy | Upstream changed mid-refresh: *Invalidate cache* on the proxy, then `apt update` |
| An uploaded package does not appear | Check its architecture matches the client's (`dpkg --print-architecture`); run *Rebuild index* |
| Signature errors on all clients after restoring a server | The signing key changed (new database). Re-import the key on clients; restore from a full backup to keep the old one |

## dnf / yum

| Symptom | Cause and fix |
|---|---|
| `Cannot download repomd.xml` | For a proxy, the remote must be a `baseurl` containing `repodata/`, not a mirror list or metalink |
| `repomd.xml GPG signature verification error` | Import or reference the key: `gpgkey=<repo url>repository.key` with `repo_gpgcheck=1` |
| `Package … is not signed` | `gpgcheck=1` checks the RPM's own signature, which RepoMan does not add. Sign RPMs in your build, or use `gpgcheck=0` with `repo_gpgcheck=1` |
| Old package list after an upload | `dnf clean metadata` (clients cache repodata) |
| `Status code: 401` | Add `username=` / `password=` to the `.repo` section |

## Notifications and webhooks

See the table in [notifications.md](notifications.md#delivery-failures-and-edge-cases). For webhooks: the list
shows the last response status; your endpoint must answer 2xx within 10 seconds; verify the signature over
the *raw* body.

## Storage

| Symptom | Cause and fix |
|---|---|
| Disk full | See [Disk space](operations.md#disk-space) |
| Deleting artifacts frees no space | The same content is still referenced by another repository (deduplication), or was just deleted and the file system has not caught up. Run *Compact blob store* |
| *asset content missing from blob store* (500) | A content file is gone from disk. Run *Verify blob store* to list affected artifacts; restore from backup, or for proxies delete the artifact so it is fetched again |
| The database file is large after deleting a lot | Run *Optimize database* (weekly by default) |
| `database or disk is full` | Free space on the data volume; nothing can be written until then |

## Getting help

When opening an issue, include: the output of `repoman version`, how RepoMan is run (service, Docker,
reverse proxy), the client and its version, the exact command and its error, and the matching lines of the
server log. Remove tokens and passwords first.

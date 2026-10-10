# Firewalls and air-gapped networks

RepoMan is designed for networks where machines may not talk to the internet. The idea:

```
 build machines, developers, servers                       the internet
 ─────────────────────────────────────                     ─────────────
   maven / npm / pip / docker / go / apt / dnf
                  │
                  │  one rule:  → RepoMan : 8081 (or 443)
                  ▼
              ┌─────────┐   a few rules:  → the registries you proxy
              │ RepoMan │ ──────────────────────────────────────────▶  repo1.maven.org, registry.npmjs.org, …
              └─────────┘
```

- **Clients need one firewall rule**: to RepoMan. They never contact a public registry.
- **RepoMan needs outbound rules only for the upstreams you configure.** With hosted repositories only, it
  needs none at all.

## What RepoMan itself connects to

Exhaustively:

| Destination | When | Configured in |
|---|---|---|
| The remote URL of each **proxy repository**, and hosts it redirects to | When a client requests something that is not cached, or cached metadata is older than the repository's metadata cache time | Repository → Configuration |
| Your **OpenID Connect** issuer | Only if single sign-on is enabled | Settings → Single sign-on |
| Your **webhook** URLs | Only if webhooks exist | Webhooks |
| Your **Gotify** server | Only if notification channels exist | Notifications |

There is **nothing else**: no update check, no telemetry or analytics, no license validation, no time or DNS
service of its own. The web UI is served entirely from the binary — fonts, icons, scripts and styles — and
its content security policy (`default-src 'self'`) makes the browser refuse to load anything from another
host, so a user's browser needs no internet access either.

You can verify this: run RepoMan with outbound traffic blocked. Everything works except fetching new content
through proxy repositories, which is reported per repository as *Remote unavailable* while cached content
continues to be served.

## Outbound rules per upstream

All connections are HTTPS (TCP 443) unless noted. Some registries serve the actual files from a different
host than their API; RepoMan follows those redirects, so the file host needs a rule too. Hosts operated by
third parties change over time — if a download fails, the repository's status badge and the server log show
the exact host that could not be reached.

| Upstream (preset) | Hosts RepoMan must reach |
|---|---|
| **Maven Central** | `repo1.maven.org` |
| Google Maven (Android) | `maven.google.com`, `dl.google.com` |
| Gradle Plugin Portal | `plugins.gradle.org`, `plugins-artifacts.gradle.org` |
| **npm** | `registry.npmjs.org` |
| **PyPI** | `pypi.org` (index) and `files.pythonhosted.org` (package files) |
| **Docker Hub** | `registry-1.docker.io` (registry), `auth.docker.io` (tokens), and the layer CDN: `production.cloudflare.docker.com` |
| GitHub Container Registry | `ghcr.io`, `pkg-containers.githubusercontent.com` |
| Quay.io | `quay.io` and its CDN hosts `cdn*.quay.io` |
| Kubernetes registry | `registry.k8s.io`, which redirects to regional hosts under `*.pkg.dev` and cloud storage |
| Microsoft Container Registry | `mcr.microsoft.com`, `*.data.mcr.microsoft.com` |
| **Go modules** | `proxy.golang.org` (also serves the checksum database: no rule for `sum.golang.org` is needed) |
| **Debian** | `deb.debian.org`, `security.debian.org` — the presets use **HTTP (TCP 80)**; packages are verified by signature |
| **Ubuntu** | `archive.ubuntu.com`, `security.ubuntu.com`, `ports.ubuntu.com` (ARM) — HTTP (TCP 80) |
| Rocky Linux | `dl.rockylinux.org` |
| AlmaLinux | `repo.almalinux.org` |
| EPEL | `dl.fedoraproject.org` |
| Conda: conda-forge, bioconda | `conda.anaconda.org` (indexes and packages; no redirects) |
| Conda: Anaconda `defaults` | `repo.anaconda.com` (check Anaconda's terms of service first) |
| GitHub releases (raw) | `github.com`, `objects.githubusercontent.com`, `release-assets.githubusercontent.com` |
| Node.js downloads (raw) | `nodejs.org` |

If your firewall filters by IP address rather than host name, prefer a forward proxy that can filter by name
(most CDNs do not publish stable address ranges) and point RepoMan at it with `HTTPS_PROXY`
([configuration](configuration.md#outbound-connections)).

### Notes per format

- **Docker Hub** limits anonymous pulls per source address. Since all your pulls now come from one address
  (RepoMan), enter a Docker Hub account on the proxy repository's Configuration tab (*The remote requires
  authentication*) to get the higher authenticated limit. Cached layers do not count: each is fetched from Docker Hub once.
- **Go**: the `go` command normally verifies downloads against `sum.golang.org`. RepoMan relays those
  requests through the Go proxy repository's upstream, so clients need only `GOPROXY=<repoman url>`. Lookups
  for modules that are published in a hosted Go repository on this server are answered locally with "not
  found" and never forwarded, so private module names do not leak; list those modules in `GONOSUMDB`
  (or `GOPRIVATE`) on the clients.
- **PyPI**: package files are fetched only from URLs that the upstream index page advertised.
- **APT/YUM proxies** pass the distribution's own signatures through untouched; clients keep verifying with
  the distribution's keys.

## Inbound rules

| From | To | Port |
|---|---|---|
| Clients, browsers, CI | RepoMan | `RM_ADDR` (default TCP 8081), or 443 with TLS / a reverse proxy |
| Prometheus (optional) | RepoMan | same port, path `/metrics` |

RepoMan listens on exactly one port. Docker clients use the same port as everything else (the registry API is
served under `/v2/`); there are no per-repository ports.

## Fully air-gapped operation

With no outbound access at all, RepoMan works as a store for **hosted** repositories. To make public
packages available:

1. **Preload through a connected instance.** Run a second RepoMan where internet access is allowed, build
   your projects against it once so its proxies cache what you need, then copy its data directory (stopped, or
   from a snapshot) to the isolated server. Proxy repositories serve everything already cached while their
   remote is unreachable; only requests for packages never seen before fail.
2. **Upload by hand.** Publish packages into hosted repositories with the normal clients (`mvn deploy:deploy-file`,
   `npm publish`, `twine upload`, `docker push`, `curl --upload-file`) or drag and drop files in the UI
   (raw, Maven, Go, APT, YUM, Conda).

No setting is needed to make a proxy repository work without its remote. Cached content is served at once;
RepoMan tries the unreachable remote at most once every 30 seconds (one client request waits for that
attempt to fail, the others are answered immediately) and the repository shows *Remote unavailable*.
If your firewall silently drops packets instead of rejecting them, that one attempt takes up to 30 seconds;
a firewall rule that *rejects* traffic to the registries makes it instant. Marking a repository **Offline**
is different — it refuses *all* client requests, cached or not.

## Clients: pointing every tool at RepoMan

Each repository's **Setup** tab has the exact snippet. The environment-wide switches, for images and base
machines:

| Tool | Setting |
|---|---|
| Maven | `~/.m2/settings.xml`: a `<mirror>` with `<mirrorOf>*</mirrorOf>` pointing to the Maven group |
| Gradle | an init script in `~/.gradle/init.d/` replacing repositories, or `repositories { maven { url … } }` |
| npm / pnpm / Yarn | `registry=` in `.npmrc` (or `npm config set registry …`) |
| pip / uv / Poetry | `index-url` in `pip.conf`, `UV_INDEX_URL` / `[[tool.uv.index]]`, a Poetry source |
| Docker / containerd | pull as `repo.example.com/docker-hub/library/nginx`, or configure a registry mirror for `docker.io` in `/etc/docker/daemon.json` (`"registry-mirrors"` cannot carry a path, so prefer explicit names) |
| Go | `GOPROXY=https://repo.example.com/repository/go-public/` (drop `,direct` so nothing bypasses the proxy) |
| apt | replace the URLs in `/etc/apt/sources.list` with the APT proxy repository |
| dnf / yum | `baseurl=` of the YUM proxy repository in `/etc/yum.repos.d/*.repo`; remove `mirrorlist=`/`metalink=` |
| conda / mamba / pixi | `channels:` in `.condarc` listing only RepoMan channels (no `defaults`); `channels = [...]` in `pixi.toml` |

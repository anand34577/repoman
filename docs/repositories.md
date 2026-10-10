# Repositories and formats

- [Concepts: hosted, proxy, group](#concepts)
- [The starter repositories](#the-starter-repositories)
- [Settings every repository has](#settings-every-repository-has)
- [How proxies behave](#how-proxies-behave)
- [Cleanup and quotas](#cleanup-and-quotas)
- Formats: [Maven](#maven) · [npm](#npm) · [PyPI](#pypi) · [Docker / OCI](#docker--oci) · [Go](#go-modules) · [APT](#apt) · [YUM / DNF](#yum--dnf) · [Conda](#conda) · [Raw](#raw)

Every repository's **Setup** tab in the UI generates the snippets below with your server's address, your user
name and — if you click **Generate token** — a fresh token already filled in. This page explains what those
snippets do and what to watch out for.

## Concepts

| Type | What it is | You publish to it? | Typical name |
|---|---|:-:|---|
| **Hosted** | Stores artifacts you publish: your own builds, vendored third-party files | ✓ | `npm-hosted` |
| **Proxy** | A caching mirror of one remote repository. Files are fetched on first request and served locally afterwards | — | `npm-proxy` |
| **Group** | A list of other repositories behind one URL. A request is answered by the first member that has the file | — | `npm-public` |

The usual arrangement per format is one of each, with every tool pointed at the group:

```
npm-hosted   (hosted)  ← CI publishes internal packages here
npm-proxy    (proxy)   ← caches registry.npmjs.org
npm-public   (group)   = npm-hosted, then npm-proxy      ← developers and CI install from here
```

**Member order matters.** Put hosted repositories *first* in a group. Then your own package always wins over
a public package of the same name — the defence against "dependency confusion" attacks, where someone
publishes a public package named like your internal one.

**A repository's name, format and type are fixed** once created. To change them, create a new repository.
Names are 1–64 characters: lowercase letters, digits, `.`, `_`, `-`, starting with a letter or digit.

URLs:

- Everything except Docker: `https://repo.example.com/repository/<name>/`
- Docker: `repo.example.com/<name>/<image>:<tag>` (the registry API lives at `/v2/`)

## The starter repositories

A new installation creates these. Change or delete them freely; nothing depends on the names.

| Format | Repositories |
|---|---|
| Maven | `maven-releases` (hosted), `maven-snapshots` (hosted, redeploy allowed, cleanup after 90 unused days), `maven-central` (proxy), `maven-public` (group of the three) |
| npm | `npm-hosted`, `npm-proxy` (npmjs.org), `npm-public` (group) |
| PyPI | `pypi-hosted`, `pypi-proxy` (pypi.org), `pypi-public` (group) |
| Docker | `docker-hosted`, `docker-hub` (proxy of Docker Hub) |
| Go | `go-hosted`, `go-proxy` (proxy.golang.org), `go-public` (group) |
| APT | `apt-hosted` (distribution `stable`) |
| YUM | `yum-hosted` |
| Conda | `conda-hosted`, `conda-forge` (proxy of conda.anaconda.org/conda-forge). New installations only: an upgraded server keeps its repositories, so create these yourself |
| Raw | `raw-hosted` |

On a server with no outbound access, the proxies simply show *Remote unavailable* until you delete them or
open the firewall ([firewall.md](firewall.md)).

## Settings every repository has

Under the repository's **Configuration** tab (administrators):

| Setting | Applies to | Meaning |
|---|---|---|
| Description | all | Shown in lists |
| **Offline** | all | Refuse *every* client request with 503 — reads and writes, cached or not. For maintenance or to retire a repository gradually |
| **Private** | all | Never readable anonymously, even when anonymous access is enabled server-wide |
| **Allow redeploy** | hosted | Permit overwriting an existing file. Off by default: a published version is immutable, so a build can never silently change. Exceptions that are always allowed: Maven `-SNAPSHOT` paths and `maven-metadata.xml` |
| **Storage quota (MB)** | hosted | Refuse uploads with *507 Insufficient Storage* once the repository holds this much. 0 = unlimited. The check happens when an upload starts, so the upload that crosses the line still completes |
| Remote URL, credentials | proxy | The upstream. *Test connection* checks reachability and credentials before you save |
| **Metadata cache time** | proxy | Minutes before mutable index data (version lists, tags, `latest`) is re-checked upstream. Artifacts themselves are immutable and cached until cleaned up |
| Members | group | Ordered list; same format only |
| Cleanup policy | hosted, proxy | See [below](#cleanup-and-quotas) |
| Distribution | APT hosted | The suite name clients put in `sources.list` (default `stable`) |

The **Access** tab overrides, per user, what the user's role would allow on this repository: `none` (cannot
even see it), `read`, or `write`. See [security.md](security.md#roles-and-repository-access).

## How proxies behave

1. **First request for a file**: RepoMan downloads it from the remote completely, stores it, then serves it.
   Concurrent requests for the same file share one download. The client waits for the download, so the very
   first pull of a multi-gigabyte file starts with a pause.
2. **Later requests**: served from disk without contacting the remote.
3. **Index data** (which versions exist) is cached too, for the *metadata cache time*. A lower value shows new
   upstream releases sooner; a higher value means fewer upstream requests.
4. **"Not found" answers** from the remote are remembered for 5 minutes, so tools probing many repositories
   don't hammer the upstream.
5. **When the remote is unreachable** (network error, 5xx, or rate limiting with 429): everything already
   cached keeps being served, including index data past its cache time. The repository shows **Remote
   unavailable**, a `repository.degraded` event is recorded (and sent to your notification channels), and
   RepoMan leaves the remote alone for 30 seconds between attempts so clients get immediate answers rather
   than each waiting for a timeout. Requests for files that were never cached fail with *502* and a message
   saying so. When the remote answers again, `repository.recovered` is recorded.
6. **A refusal for a single item** (401/403 — a private or misspelled image, for example) is passed to the
   client as an error and does *not* mark the repository as unavailable.

Administrators find **Invalidate cache** in the repository's action menu. It forces index data to be
re-checked on next use, forgets remembered "not found" answers and retries the remote immediately (use it
after something was published upstream and you cannot wait for the cache time). It does not delete cached artifacts.

**Dependency freshness vs. reproducibility:** a proxy never changes a cached artifact. If upstream replaces
a file under the same name (bad practice, but it happens), you keep the original until it is cleaned up.

## Cleanup and quotas

Without cleanup, proxies grow forever. Two rules, combinable, per repository:

| Rule | Deletes |
|---|---|
| **Delete if unused for (days)** | Files neither downloaded nor updated for N days. Ideal for proxies and snapshot repositories |
| **Keep latest versions** | For each component, all but the N most recently published versions. Ideal for CI-published packages |

- **Preview cleanup…** (the repository's action menu) shows exactly what would be removed and how much space
  that frees, and lets you run it at once. Nothing is deleted by the preview itself.
- Otherwise policies are applied hourly by the *Run cleanup policies* task.
- Generated index files (APT `Packages`, YUM `repodata`) are never candidates; they are rebuilt after cleanup.
- "Newest" means most recently *published* here, not highest version number.
- Storage is deduplicated: deleting a file frees disk space only when no other repository references the
  same content.
- **Docker**: "keep newest N versions" removes old *tags*. Their layers disappear later through the
  "unused for N days" rule (layers shared with kept tags remain in use and stay). Use both rules together for
  Docker repositories.

Deleting things by hand: the **Browse** tab (single files) and the **Components** tab (a whole component or
one version). Deleting a repository deletes all its artifacts and removes it from every group.

---

## Maven

Clients: Maven, Gradle, sbt, Ivy, Leiningen. Layout: the standard Maven 2 repository layout.

**Consume** — route everything through the group in `~/.m2/settings.xml`:

```xml
<settings>
  <mirrors>
    <mirror>
      <id>repoman</id>
      <mirrorOf>*</mirrorOf>
      <url>https://repo.example.com/repository/maven-public/</url>
    </mirror>
  </mirrors>
  <servers>
    <server>
      <id>repoman</id>
      <username>alice</username>
      <password>rm_your_token</password>
    </server>
  </servers>
</settings>
```

**Publish** — in `pom.xml`, with a `<server>` entry of the same `id`:

```xml
<distributionManagement>
  <repository><id>repoman</id><url>https://repo.example.com/repository/maven-releases/</url></repository>
  <snapshotRepository><id>repoman</id><url>https://repo.example.com/repository/maven-snapshots/</url></snapshotRepository>
</distributionManagement>
```

Good to know:

- **Publish to a hosted repository, never to the group.** Deploying to a group or proxy returns *405*.
- **Releases are immutable**: deploying the same release version twice fails with *400 "repository does not
  allow updating assets"*. Bump the version, or delete the old one in the UI. Snapshots can always be redeployed.
- **Groups merge `maven-metadata.xml`** of an artifact across members, so version ranges, `LATEST`/`RELEASE`
  and `mvn versions:display-dependency-updates` see hosted and proxied versions together. Checksums of the
  merged file are generated to match.
- **Third-party jars** not available upstream: upload them with `mvn deploy:deploy-file` or drag and drop
  into `maven-releases` (enter the directory `group/as/folders/artifact/version`).
- Maven caches "not found" results locally; after fixing a repository problem run `mvn -U …`.
- Gradle: declare the group in `repositories { maven { url = uri("…/maven-public/") ; credentials { … } } }`,
  and in `pluginManagement { repositories { … } }` in `settings.gradle(.kts)` for plugins. Add a proxy of
  `https://plugins.gradle.org/m2` to the group to resolve Gradle plugins.

## npm

Clients: npm, pnpm, Yarn, Bun.

`.npmrc` (project or home directory):

```ini
registry=https://repo.example.com/repository/npm-public/
//repo.example.com/repository/npm-public/:_authToken=rm_your_token
```

Publish (to the *hosted* repository): `npm publish --registry=https://repo.example.com/repository/npm-hosted/`,
with a matching `_authToken` line for that URL. Or set it per package in `package.json`:
`"publishConfig": { "registry": "https://repo.example.com/repository/npm-hosted/" }`.

Good to know:

- **Scoped packages** work (`@acme/util`). To send only one scope to RepoMan and everything else to npmjs:
  `@acme:registry=https://repo.example.com/repository/npm-hosted/`.
- **`npm login`**: use `npm login --auth-type=legacy --registry=…`. The default web login flow is not
  supported. A token in `.npmrc` is simpler and what CI should use. Note that `npm login` creates a
  never-expiring full-access token named "npm login"; revoke it under *Account* when no longer needed.
- **Publishing a version twice** fails with *403* unless *Allow redeploy* is on.
- **Not implemented**: `npm unpublish`, `npm deprecate`, `npm dist-tag`, `npm search`, `npm audit`.
  Delete a version in the UI instead (the package metadata is updated automatically, and `latest` moves to
  the newest remaining version). For `npm audit`, run it against npmjs directly from a machine that may, or
  set `audit=false` in `.npmrc` to silence the failing request.
- **Tarball links** in package metadata point at the server's Base URL. If installs try to download from a
  wrong host, set *Settings → Base URL* ([configuration](configuration.md#base-url)).
- **Reverse proxies** must pass `%2f` in URLs through unchanged (scoped packages). nginx and Caddy do;
  Apache needs `AllowEncodedSlashes NoDecode`.
- A published package is held in memory while it is processed. Very large packages (hundreds of MB) need
  corresponding memory; the hard limit is 1 GB per publish.
- Yarn Berry refuses plain HTTP unless the host is listed in `unsafeHttpWhitelist`; the Setup tab adds it for you.

## PyPI

Clients: pip, uv, Poetry, pdm, twine.

```ini
# pip.conf  (Linux/macOS: ~/.config/pip/pip.conf — Windows: %APPDATA%\pip\pip.ini)
[global]
index-url = https://alice:rm_your_token@repo.example.com/repository/pypi-public/simple/
```

Upload with twine (`~/.pypirc`):

```ini
[distutils]
index-servers = repoman

[repoman]
repository = https://repo.example.com/repository/pypi-hosted/
username = alice
password = rm_your_token
```

`twine upload -r repoman dist/*`

Good to know:

- **The index URL ends in `/simple/`** (with the trailing slash); the upload URL is the repository root
  without it. A missing `/simple/` is the most common reason for "pip can't find packages".
- **Names are normalized** as pip does it: `My_Package`, `my.package` and `my-package` are the same project.
- **Groups merge** the file lists of all members for a project. If the same file name exists in several
  members, the first member's file is used — hosted first protects your internal packages.
- **Plain HTTP**: pip needs `--trusted-host repo.example.com` (or `trusted-host` in `pip.conf`). Use HTTPS instead.
- **Hashes**: hosted files are listed with `#sha256=`, so `pip --require-hashes` works.
- The legacy *upload* API is implemented (what twine, Poetry, uv and pdm use). `pip search` and the JSON API
  (`/pypi/<name>/json`) are not.
- Re-uploading an existing file fails with *400* unless *Allow redeploy* is on, matching pypi.org's behaviour.

## Docker / OCI

Clients: Docker, Podman, containerd/Kubernetes, Buildah, Skopeo, and `helm` for OCI charts.

**Image names start with the repository name:**

```bash
docker login repo.example.com -u alice            # password: an API token
docker pull repo.example.com/docker-hub/library/nginx:1.27      # through the Docker Hub proxy
docker tag myapp:1.0 repo.example.com/docker-hosted/team/myapp:1.0
docker push repo.example.com/docker-hosted/team/myapp:1.0
```

Good to know:

- **HTTPS is required** by Docker for every registry except `localhost`. Without it you get
  *"http: server gave HTTP response to HTTPS client"*. Fix it properly with [TLS](configuration.md#https);
  the workaround is `"insecure-registries": ["repo.example.com:8081"]` in `/etc/docker/daemon.json`.
- **Docker Hub official images** live under `library/`: `docker-hub/library/nginx`, not `docker-hub/nginx`.
  Other images use their normal name: `docker-hub/grafana/grafana`.
- **One port for everything.** There are no per-repository ports or sub-domains; the first path segment
  selects the repository. Consequently an image reference always has at least two segments.
- **Rate limits**: add Docker Hub credentials to the proxy repository so pulls count against your account.
- **Tags are mutable by default** in `docker-hosted` (*Allow redeploy* is on, so `latest` can move). Turn it
  off to make pushed tags immutable.
- **Layers are deduplicated** across images and repositories; pushing an image whose base layers already
  exist uploads only the new layers.
- **Deleting**: delete a tag or image in the UI, or with the registry API (`DELETE /v2/<repo>/<image>/manifests/<digest>`).
  Layers are reclaimed by the cleanup rule "unused for N days".
- **Multi-architecture images** (manifest lists / OCI indexes) are stored and proxied as-is.
- **Helm charts**: `helm push chart.tgz oci://repo.example.com/docker-hosted/charts` and
  `helm pull oci://repo.example.com/docker-hosted/charts/mychart --version 1.0.0` use the same OCI API as
  images. (The classic `index.yaml` chart repository format is not generated; a raw repository can host one
  you build yourself with `helm repo index`.)
- **Kubernetes**: create a pull secret (`kubectl create secret docker-registry …`) and reference it in
  `imagePullSecrets`; with anonymous access on and a non-private repository, no secret is needed.
- **Reverse proxy** in front: disable the body size limit and request buffering, or pushes fail with *413* or stall.
- Interrupted pushes leave partial uploads behind; they are removed automatically after 24 hours.
- `docker search` is not available. The catalog (`/v2/_catalog`) lists the images present in the Docker
  repositories you can read — pushed ones and those already cached by proxies — not everything that exists upstream.

## Go modules

Client: the `go` command (1.13+).

```bash
go env -w GOPROXY=https://repo.example.com/repository/go-public/
go env -w GONOSUMDB=git.example.com/yourcompany        # your private module path prefixes
```

Credentials, if anonymous access is off — `~/.netrc` (Windows: `%USERPROFILE%\_netrc`):

```
machine repo.example.com
login alice
password rm_your_token
```

Good to know:

- **Checksum database**: the `go` command verifies public modules against `sum.golang.org`. A Go proxy or
  group repository relays those requests through its upstream (`proxy.golang.org` mirrors the database), so
  clients need **no** access to `sum.golang.org` and no `GOSUMDB=off` or `GOFLAGS=-insecure` workarounds.
- **Private modules** are not in the public database. List their prefixes in `GONOSUMDB` (or `GOPRIVATE`,
  which also bypasses the proxy — usually *not* what you want here). RepoMan never forwards a checksum lookup
  for a module that exists in one of its hosted Go repositories.
- **`,direct`**: `GOPROXY=<url>,direct` lets `go` fall back to fetching from version control when the proxy
  says "not found". On an isolated network leave `,direct` out so failures are immediate and clear.
- **Publishing** a module means uploading two files per version to a hosted repository:

  ```bash
  MOD=git.example.com/team/lib VER=v1.2.3
  git archive --format=zip --prefix=$MOD@$VER/ -o $VER.zip $VER
  curl -u alice:rm_your_token --upload-file go.mod  https://repo.example.com/repository/go-hosted/$MOD/@v/$VER.mod
  curl -u alice:rm_your_token --upload-file $VER.zip https://repo.example.com/repository/go-hosted/$MOD/@v/$VER.zip
  ```

  The `.info` file is generated. Versions must be canonical semantic versions (`v1.2.3`, `v1.2.3-rc.1`);
  anything else is rejected with *400*. Upper-case letters in module paths use Go's escaped form
  (`github.com/!azure/…`). Major versions ≥ 2 include the suffix in the path (`…/lib/v2/@v/v2.0.0.zip`).
- **Groups** merge version lists; `@latest` prefers the highest release over pre-releases.

## APT

Client: apt (Debian, Ubuntu and derivatives).

**Hosted** — RepoMan builds and signs the index for you:

```bash
# on each client, once
sudo install -d -m 0755 /etc/apt/keyrings
curl -fsSL https://repo.example.com/repository/apt-hosted/repository.key | sudo gpg --dearmor -o /etc/apt/keyrings/repoman.gpg
echo "deb [signed-by=/etc/apt/keyrings/repoman.gpg] https://repo.example.com/repository/apt-hosted/ stable main" \
  | sudo tee /etc/apt/sources.list.d/repoman.list
sudo apt update
```

Upload a package: `curl -u alice:rm_your_token --upload-file ./tool_1.0_amd64.deb https://repo.example.com/repository/apt-hosted/`
(or drag and drop in the UI). It is placed under `pool/main/…` automatically and `Packages`, `Release`,
`InRelease` and `Release.gpg` are regenerated and signed.

Good to know:

- **The signing key** is one OpenPGP key per server, generated on first use and kept in the database. It is
  served at `/repository/<repo>/repository.key` and under *Settings → Security*. **If you lose the data
  directory you lose the key**, and every client must import the new one — one more reason for backups.
- One **distribution** (suite) per repository, component `main`, any architectures. For several
  distributions (`bookworm`, `noble`), create one hosted repository each.
- Uploads must be valid `.deb` files; anything else is rejected with *400*. Source packages are not supported.
- **Credentials** when anonymous access is off — `/etc/apt/auth.conf.d/repoman.conf`
  (`machine repo.example.com login alice password rm_your_token`), mode 0600.
- **Proxy**: point the proxy at the archive root (the URL containing `dists/` and `pool/`), and use it in
  `sources.list` in place of the original mirror, with the distribution's own codename and components.
  Signatures are upstream's, so no extra key is needed.
- **No groups**: a signed index cannot be merged. Add several `deb` lines instead.
- apt may show *"Hash Sum mismatch"* if an upstream mirror is mid-update while RepoMan caches it; run
  *Invalidate cache* on the proxy and `apt update` again.

## YUM / DNF

Clients: dnf, yum, zypper (RHEL, Rocky, Alma, Fedora, Amazon Linux, openSUSE).

**Hosted** — `/etc/yum.repos.d/repoman.repo`:

```ini
[repoman]
name=RepoMan
baseurl=https://repo.example.com/repository/yum-hosted/
enabled=1
gpgcheck=0
repo_gpgcheck=1
gpgkey=https://repo.example.com/repository/yum-hosted/repository.key
# when anonymous access is off:
username=alice
password=rm_your_token
```

Upload: `curl -u alice:rm_your_token --upload-file ./tool-1.0-1.x86_64.rpm https://repo.example.com/repository/yum-hosted/`.
`repodata/` (primary, filelists, other, `repomd.xml` and its signature) is regenerated on every change.

Good to know:

- **`repo_gpgcheck=1`** verifies the *repository metadata* with RepoMan's key. **`gpgcheck`** verifies each
  *package's* own signature: RepoMan does not sign your RPMs. Either sign them in your build
  (`rpmsign --addsign`) and set `gpgcheck=1` with your key, or leave `gpgcheck=0` and rely on the signed
  metadata, which lists every package's SHA-256.
- Upload to the repository root (the path is derived from the package: `Packages/<letter>/…`) or to a
  sub-directory of your choice, but not to `repodata/`.
- **Proxy**: the remote must be a `baseurl` that contains `repodata/`. Mirror lists and metalinks are not
  supported — pick one concrete mirror. On clients, remove `mirrorlist=`/`metalink=` and point `gpgkey` at
  the distribution's key.
- **No groups**; list several `.repo` sections instead.
- After metadata is regenerated, the previous generation is kept for an hour so clients in the middle of a
  refresh don't hit missing files.
- Module metadata (`modules.yaml`) and delta RPMs are not generated for hosted repositories.

## Conda

Clients: conda, mamba, micromamba, pixi (Anaconda, Miniconda, Miniforge).

A conda repository is a **channel**: `<channel>/<subdir>/repodata.json` plus package files, where the subdir is
`noarch` or a platform such as `linux-64`, `linux-aarch64`, `osx-arm64` or `win-64`.

**Clients** — `~/.condarc` (Windows: `%USERPROFILE%\.condarc`):

```yaml
channels:
  - https://repo.example.com/repository/conda-hosted
  - https://repo.example.com/repository/conda-forge
channel_priority: strict
```

Conda searches the channels in order. With `channel_priority: strict`, a package found in an earlier channel is
never mixed with same-named packages from later ones, so list your hosted channel first. This is the
same dependency-confusion defence that member order gives a group.

When anonymous access is off, put credentials in the URL. Conda masks them in its output:
`https://alice:rm_your_token@repo.example.com/repository/conda-forge`. For pixi, run
`pixi auth login repo.example.com --username alice --password rm_your_token` once.

**Hosted**: upload `.conda` or `.tar.bz2` packages:

```bash
conda build recipe/ --output-folder ./out
curl -u alice:rm_your_token --upload-file ./out/linux-64/mypkg-1.0-h1234_0.conda https://repo.example.com/repository/conda-hosted/
```

RepoMan reads `info/index.json` inside the package and files the package under its subdir
(`linux-64/mypkg-1.0-h1234_0.conda`). `repodata.json` for each subdir is generated from what is stored, so an
upload or delete is visible to the next `conda install` immediately.

**Proxy**: the remote URL is the channel URL, the one that contains `noarch/`:

| Channel | Remote URL |
|---|---|
| conda-forge | `https://conda.anaconda.org/conda-forge` |
| bioconda | `https://conda.anaconda.org/bioconda` (use together with conda-forge) |
| Anaconda `main` | `https://repo.anaconda.com/pkgs/main` |

Good to know:

- **Your firewall must allow the channel host.** For conda-forge and bioconda that is `conda.anaconda.org`; for
  Anaconda's own channels it is `repo.anaconda.com`. Both serve package files from the same host, with no
  redirect to another domain.
- **Anaconda's own channels** (`repo.anaconda.com`, the `defaults` channel) are covered by Anaconda's terms of
  service. Some organisations need a paid licence to use them. conda-forge and bioconda are community channels.
  Check the terms before you proxy `pkgs/main`.
- **Keep conda off the internet**: list only RepoMan channels in `channels:`. Leaving `defaults` in the list
  makes conda contact `repo.anaconda.com` directly. For a single command, use
  `conda install --override-channels -c <url> …`.
- **No groups.** Public channel indexes are too big to merge on every request: conda-forge's `noarch` index is
  about 29 MB even zstd-compressed. List
  several channels instead, as shown above.
- **The first `conda install` through a new proxy can be slow.** conda-forge's index for a platform is large,
  and RepoMan downloads it completely before answering. If conda times out, raise `remote_read_timeout_secs` in
  `.condarc` and try again. Later requests come from the cache, and the index is re-checked after the
  repository's *Metadata TTL* (default 60 minutes). While the upstream is down, the cached index is served.
- **Compressed and incremental indexes** (`repodata.json.zst`, `.bz2`, `repodata.jlap`, sharded repodata) are
  passed through and cached by proxies when the upstream has them. Hosted channels serve only `repodata.json`
  and `current_repodata.json`; clients ask for a variant, get *404*, and fall back to the plain file.
- **Uploads are checked**: the file must be a real conda package. Upload to the repository root and the local
  file name doesn't matter; the stored name comes from the package. If you upload to a full path
  (`…/linux-64/mypkg-1.0-h1234_0.conda`), it must match the package metadata (*400* otherwise). Re-uploading an existing package is refused unless
  *Allow redeploy* is on.
- **Not supported**: `anaconda upload` and the anaconda.org API (use `curl` or the UI), channel labels
  (`/label/dev`; use a separate hosted repository instead), `channeldata.json` for hosted channels (conda
  does not need it to install), and conda's `/t/<token>/` URL form for authenticating to RepoMan itself. For an
  upstream that needs a token in the URL (a private anaconda.org channel), put the token in the remote URL. Any
  admin who opens the repository settings can see it.
- If an upstream index sets an absolute `base_url` (CEP-15 repodata v2), clients download packages from that
  URL directly, not through RepoMan. conda-forge and Anaconda's channels did not use it when this was written.

## Raw

Any file under any path. Clients: curl, wget, browsers, build scripts.

```bash
curl -u alice:rm_your_token --upload-file ./tool-1.2.zip https://repo.example.com/repository/raw-hosted/tools/tool-1.2.zip
curl -fLO https://repo.example.com/repository/raw-hosted/tools/tool-1.2.zip
curl -u alice:rm_your_token -X DELETE https://repo.example.com/repository/raw-hosted/tools/tool-1.2.zip
```

Good to know:

- `raw-hosted` allows overwriting (*Allow redeploy* on) since raw files often have no version in the name.
  Turn it off for immutable release archives.
- There are no directory listings over HTTP; browse in the UI or use the [API](api.md#browse-and-search).
- Files are served with `Content-Security-Policy: sandbox`: an uploaded HTML file is displayed but its
  scripts do not run, so a raw repository cannot be used to attack people signed in to RepoMan.
- Range requests, `ETag` and `If-Modified-Since` work, so downloads resume and cache properly.
- A **raw proxy** mirrors any plain HTTP file server (e.g. `https://nodejs.org/dist`): the path after the
  repository name is appended to the remote URL.
- Paths must be clean: no `..`, no leading `/`, no empty segments.

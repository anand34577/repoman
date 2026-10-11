# Installation

RepoMan is a single executable. "Installing" it means putting that file somewhere, telling it where to keep
its data, and (usually) registering it as a service so it starts at boot. This page covers every supported
way to do that, plus upgrading and uninstalling.

- [Choose a download](#choose-a-download)
- [Try it without installing](#try-it-without-installing)
- [Linux (systemd)](#linux-systemd)
- [macOS (launchd)](#macos-launchd)
- [Windows (service)](#windows-service)
- [Docker](#docker)
- [Docker Compose](#docker-compose)
- [Kubernetes](#kubernetes)
- [First start](#first-start)
- [Upgrading](#upgrading)
- [Uninstalling](#uninstalling)

## Requirements

| | |
|---|---|
| CPU / memory | Anything from a Raspberry Pi up. An idle server needs a few tens of MB of memory; allow 512 MB or more when large packages are published (an npm publish is held in memory) |
| Disk | The size of the artifacts you host and cache, plus about 10% headroom. The database itself stays small |
| File system | A **local** disk (ext4, XFS, APFS, NTFS). Do not put the data directory on NFS/SMB: SQLite needs working file locks |
| Network | Inbound: one TCP port (default 8081). Outbound: only the upstreams you proxy — see [firewall.md](firewall.md) |
| Software | None. No runtime, database or package dependencies |

## Choose a download

Archives are on the [releases page](https://github.com/anand34577/repoman/releases).

| Your machine | Archive |
|---|---|
| Linux, Intel/AMD 64-bit | `repoman_<version>_linux_amd64.tar.gz` |
| Linux, ARM 64-bit: Raspberry Pi 3/4/5 with a 64-bit OS, AWS Graviton, Ampere | `repoman_<version>_linux_arm64.tar.gz` |
| Linux, ARM 32-bit: Raspberry Pi 2/3/4 with a 32-bit OS | `repoman_<version>_linux_armv7.tar.gz` |
| Linux, ARM 32-bit: Raspberry Pi 1 / Zero | `repoman_<version>_linux_armv6.tar.gz` |
| macOS, Apple silicon (M1 and later) | `repoman_<version>_macos_arm64.tar.gz` |
| macOS, Intel | `repoman_<version>_macos_amd64.tar.gz` |
| Windows, Intel/AMD 64-bit | `repoman_<version>_windows_amd64.zip` |
| Windows on ARM | `repoman_<version>_windows_arm64.zip` |

Not sure? On Linux/macOS run `uname -m`: `x86_64` → amd64, `aarch64`/`arm64` → arm64, `armv7l` → armv7,
`armv6l` → armv6. On Windows: *Settings → System → About → System type*.

**Verify the download** (optional but recommended):

```bash
sha256sum -c SHA256SUMS --ignore-missing          # integrity
gh attestation verify repoman_<version>_linux_amd64.tar.gz --repo anand34577/repoman   # built by this repository's release workflow
```

Each archive contains the `repoman` binary, a service installer for that platform, `repoman.env.example`
and this documentation.

## Try it without installing

```bash
./repoman                 # Windows: .\repoman.exe
```

It creates `./data`, prints the admin password and listens on http://localhost:8081. Stop it with Ctrl+C.
Delete the `data` folder to start over.

## Linux (systemd)

```bash
tar -xzf repoman_<version>_linux_amd64.tar.gz
cd repoman_<version>_linux_amd64
sudo ./install.sh
```

The script is safe to re-run. It:

1. creates a locked system user `repoman` (no shell, no login);
2. installs the binary to `/usr/local/bin/repoman`;
3. writes `/etc/repoman/repoman.env` (settings) — only if it does not exist yet;
4. installs `/etc/systemd/system/repoman.service`, a sandboxed unit: read-only system, no capabilities, write
   access only to `/var/lib/repoman`;
5. enables and starts the service, then waits until it answers.

| What | Where |
|---|---|
| Settings | `/etc/repoman/repoman.env` → then `sudo systemctl restart repoman` |
| Data | `/var/lib/repoman` (mode 0700, owned by `repoman`) |
| Logs | `journalctl -u repoman -f` |
| Status | `systemctl status repoman` |
| First password | `sudo cat /var/lib/repoman/initial-admin-password` |

**Edge cases**

- **Port below 1024** (e.g. `RM_ADDR=:443`): the service has no privileges, so grant just that one:
  `sudo systemctl edit repoman` and add

  ```ini
  [Service]
  CapabilityBoundingSet=CAP_NET_BIND_SERVICE
  AmbientCapabilities=CAP_NET_BIND_SERVICE
  ```
- **Data on another disk**: set `RM_DATA_DIR=/srv/repoman` in the env file, `sudo install -d -m 0700 -o repoman -g repoman /srv/repoman`,
  and allow the path with `sudo systemctl edit repoman`:

  ```ini
  [Service]
  ReadWritePaths=/srv/repoman
  ```
- **TLS certificates** must be readable by the `repoman` user (e.g. `chown root:repoman` and `chmod 0640`), and
  outside `/home` and `/root` (the unit cannot see those).
- **SELinux (RHEL, Rocky, Fedora)**: the default layout needs no extra policy. If you move the data directory
  and the service is denied access (`sudo ausearch -m avc -ts recent`), label the new location:
  `sudo semanage fcontext -a -t var_lib_t '/srv/repoman(/.*)?' && sudo restorecon -R /srv/repoman`.
- **Host firewall**: open the port, e.g. `sudo firewall-cmd --add-port=8081/tcp --permanent && sudo firewall-cmd --reload`
  or `sudo ufw allow 8081/tcp`.
- **No systemd** (Alpine/OpenRC, containers, old distributions): the script stops with a message. Run
  `repoman serve` under your own supervisor as an unprivileged user; it is an ordinary foreground process that
  stops cleanly on SIGTERM. Nothing else is required.
- **"does not run on this machine"**: you downloaded the archive for a different CPU. See the table above.

## macOS (launchd)

```bash
tar -xzf repoman_<version>_macos_arm64.tar.gz
cd repoman_<version>_macos_arm64
sudo ./install.sh
```

The service starts at boot and runs as **your user account** (the one that ran `sudo`), never as root.

| What | Where |
|---|---|
| Binary | `/usr/local/bin/repoman` |
| Settings | `/usr/local/etc/repoman/repoman.env` → then `sudo launchctl kickstart -k system/com.repoman.server` |
| Data | `/usr/local/var/repoman` |
| Logs | `/usr/local/var/log/repoman.log` |
| Stop / start | `sudo launchctl bootout system/com.repoman.server` / `sudo launchctl bootstrap system /Library/LaunchDaemons/com.repoman.server.plist` |

**Edge cases**

- **"repoman cannot be opened because the developer cannot be verified"**: the binary is not notarized by
  Apple. `install.sh` clears the quarantine flag for you. If you run the binary by hand, clear it yourself:
  `xattr -d com.apple.quarantine ./repoman`.
- **Wrong architecture**: Apple silicon needs `arm64`, Intel Macs `amd64`.
- **macOS firewall prompt**: allow incoming connections for `repoman` when asked, or other machines cannot reach it.
- **Laptops**: a sleeping Mac serves nothing. For a shared server use a machine that stays awake.

## Windows (service)

Extract the zip, then in an **elevated** PowerShell (*Run as administrator*):

```powershell
cd repoman_<version>_windows_amd64
Set-ExecutionPolicy -Scope Process Bypass -Force     # allow this script for this window only
.\install.ps1 -OpenFirewall
```

`-OpenFirewall` adds a Windows Firewall rule for the listen port so other machines can connect; leave it out
for a local-only setup. Other options: `-Port 9000`, `-InstallDir`, `-DataDir`.

| What | Where |
|---|---|
| Program | `C:\Program Files\RepoMan\repoman.exe` |
| Settings | `C:\Program Files\RepoMan\repoman.env` → then `Restart-Service RepoMan` |
| Data | `C:\ProgramData\RepoMan` (readable by administrators and the service only) |
| Logs | `C:\ProgramData\RepoMan\logs\repoman.log` (rotated at 20 MB) |
| First password | `Get-Content C:\ProgramData\RepoMan\initial-admin-password` |
| Manage | `repoman service start` / `stop` / `status`, or *services.msc* → **RepoMan Repository Manager** |

The service starts automatically at boot (delayed start, so the network is up) and restarts itself after a crash.

**Without the script** — the binary can register itself:

```powershell
.\repoman.exe service install     # registers the service for the exe at its current location
.\repoman.exe service start
```

Put a `repoman.env` next to `repoman.exe` first. A service starts in `C:\Windows\System32`, so RepoMan
resolves a relative `RM_DATA_DIR` against the folder that contains `repoman.exe`, not the working directory.

**Edge cases**

- **"Access is denied" / "cannot reach the Service Control Manager"**: the PowerShell window is not elevated.
- **SmartScreen or antivirus blocks the exe**: the binary is not code-signed. Right-click the zip →
  *Properties* → *Unblock* before extracting, or run `Unblock-File .\repoman.exe`.
- **Service account**: the service runs as `LocalSystem`. To run it as a dedicated low-privilege account, create
  the account, grant it *Modify* on the data directory, and set it in *services.msc → RepoMan → Log On*.
- **Editing `repoman.env` with Notepad** is fine; a UTF-8 byte-order mark is tolerated.
- **Long paths** are not a concern: artifacts are stored under short hashed names, not their repository paths.

## Docker

```bash
docker run -d --name repoman \
  --restart unless-stopped \
  -p 8081:8081 \
  -v repoman-data:/data \
  ghcr.io/anand34577/repoman:latest

docker logs repoman            # first-run admin password
```

The image is built `FROM scratch`: it contains the binary and CA certificates and nothing else (no shell).
It runs as user `10001`, keeps everything in `/data` and has a built-in health check. Images exist for
`linux/amd64`, `linux/arm64` and `linux/arm/v7`.

Pin a version in production (`:1.5.0` or `:1.5`) instead of `:latest`.

**Edge cases**

- **Bind mounts** (`-v /srv/repoman:/data`) must be writable by UID 10001: `sudo chown -R 10001:10001 /srv/repoman`.
  A named volume (as above) needs nothing. If the container exits immediately with *permission denied*, this is why.
- **No shell in the image**: `docker exec -it repoman sh` does not work by design. RepoMan's own commands do:
  `docker exec repoman repoman version`, `docker exec -it repoman repoman reset-password`.
- **Set the admin password yourself**: add `-e RM_ADMIN_PASSWORD='a-long-password'` for the *first* start only,
  then remove it. It is ignored once the admin account exists.
- **Upstreams signed by a private CA**: mount your CA bundle and point Go at it:
  `-v /etc/ssl/corp-ca.pem:/ca.pem:ro -e SSL_CERT_FILE=/ca.pem`. Note that this *replaces* the default bundle, so
  the file must contain every CA you need.
- **Behind a reverse proxy container**: set `RM_TRUSTED_PROXIES` to the proxy's network, e.g. `172.16.0.0/12`
  ([why](configuration.md#behind-a-reverse-proxy)).
- **Using RepoMan as the Docker registry for the same host**: Docker needs HTTPS for every registry except
  `localhost`. See [HTTPS](configuration.md#https).

## Docker Compose

A hardened example is in [`docker-compose.yml`](../docker-compose.yml): read-only root file system, all
capabilities dropped, `no-new-privileges`.

```bash
docker compose up -d
docker compose logs repoman
```

## Kubernetes

RepoMan is a single-replica stateful application. The essentials:

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata: { name: repoman }
spec:
  serviceName: repoman
  replicas: 1                       # never more than 1: one process owns the data directory
  selector: { matchLabels: { app: repoman } }
  template:
    metadata: { labels: { app: repoman } }
    spec:
      securityContext: { runAsUser: 10001, runAsGroup: 10001, fsGroup: 10001, runAsNonRoot: true }
      terminationGracePeriodSeconds: 40
      containers:
        - name: repoman
          image: ghcr.io/anand34577/repoman:1.5.0
          ports: [{ containerPort: 8081 }]
          env:
            - { name: RM_TRUSTED_PROXIES, value: "10.0.0.0/8" }   # your ingress controller's pod network
          readinessProbe: { httpGet: { path: /healthz, port: 8081 }, periodSeconds: 10 }
          livenessProbe: { httpGet: { path: /healthz, port: 8081 }, periodSeconds: 30, failureThreshold: 5 }
          securityContext: { readOnlyRootFilesystem: true, allowPrivilegeEscalation: false, capabilities: { drop: [ALL] } }
          volumeMounts:
            - { name: data, mountPath: /data }
            - { name: tmp, mountPath: /tmp }
      volumes:
        - { name: tmp, emptyDir: {} }
  volumeClaimTemplates:
    - metadata: { name: data }
      spec: { accessModes: [ReadWriteOnce], resources: { requests: { storage: 100Gi } } }
```

Use a block-storage class (`ReadWriteOnce`), not NFS. On the ingress, raise or disable the request body
limit (e.g. `nginx.ingress.kubernetes.io/proxy-body-size: "0"`), or image pushes will fail with *413*.

## First start

On the first start with an empty data directory RepoMan:

1. creates the `admin` account. With `RM_ADMIN_PASSWORD` unset it generates a random password, prints it
   once in the log inside a banner, and writes it to `<data dir>/initial-admin-password` (owner-readable only);
2. creates a starter set of repositories for every format (you can delete or change any of them);
3. starts listening.

Then:

1. Sign in as `admin` and **change the password** under *Account*. The password file is deleted automatically.
2. *Settings → General*: set the **Base URL** if clients reach RepoMan under a different name than the one in
   your browser's address bar (always the case behind a reverse proxy).
3. Decide on **anonymous access** (*Settings → General*). It is on by default: anyone who can reach the server
   can *download* from repositories that are not marked private. Turn it off if the network is not fully trusted.
4. Follow the dashboard checklist.

`RM_ADMIN_PASSWORD` shorter than 8 characters stops the first start with an error message. If you lose the
password before changing it, read the file again or run [`repoman reset-password`](operations.md#reset-a-lost-password).

## Upgrading

RepoMan upgrades its database automatically at start-up; there are no manual migration steps. Downgrading is
not supported, so **back up the data directory first** ([how](operations.md#backup-and-restore)).

| Installed as | Upgrade |
|---|---|
| Linux service | Extract the new archive and run `sudo ./install.sh` again. Settings and data are kept |
| macOS service | Same: `sudo ./install.sh` |
| Windows service | Same: `.\install.ps1` in an elevated PowerShell |
| Docker | `docker pull ghcr.io/anand34577/repoman:<new>` then recreate the container with the same volume |
| Compose | change the tag, `docker compose pull && docker compose up -d` |
| Manual | stop RepoMan, replace the binary, start it |

During the few seconds of restart, clients get connection errors; build tools retry. Requests in flight get
up to 30 seconds to finish before the old process exits.

Read the [changelog](../CHANGELOG.md) before upgrading across versions; anything that needs your attention is
listed under **Upgrade notes**.

## Uninstalling

| Installed as | Remove |
|---|---|
| Linux | `sudo ./install.sh --uninstall` |
| macOS | `sudo ./install.sh --uninstall` |
| Windows | `.\install.ps1 -Uninstall` |
| Docker | `docker rm -f repoman` |

The uninstallers keep the data directory and settings, because deleting your artifacts should be a
deliberate act. Remove them yourself when you are sure:
`/var/lib/repoman` and `/etc/repoman` (Linux), `/usr/local/var/repoman` and `/usr/local/etc/repoman` (macOS),
`C:\ProgramData\RepoMan` (Windows), `docker volume rm repoman-data` (Docker).

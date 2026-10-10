# Contributing to RepoMan

Thanks for helping. Bug reports, documentation fixes, new client recipes and code are all welcome.

- **Security problems**: report privately, see [SECURITY.md](SECURITY.md).
- **Bugs and ideas**: open an issue. For anything larger than a small fix, describe the change in an issue
  first so effort is not wasted.

## Building

You need **Go 1.26+** and **Node.js 22+**. No C compiler, no other tools.

```bash
git clone https://github.com/anand34577/repoman && cd repoman

npm --prefix frontend ci
npm --prefix frontend run build        # type-checks, bundles into backend/ui/dist (embedded in the binary)
go -C backend build -o ../repoman .    # ./repoman  (Windows: -o ../repoman.exe)
./repoman                              # http://localhost:8081, data in ./data
```

`make build`, `make test`, `make release` and `make docker` wrap these if you have make.

The Go build embeds whatever is in `backend/ui/dist`; build the frontend first or the binary serves a
"UI not built" message.

## Developing

Two terminals:

```bash
go -C backend run .               # API + package endpoints on :8081
npm --prefix frontend run dev     # UI with hot reload on :5173, proxying /api, /repository and /v2 to :8081
```

Open http://localhost:5173. The first-run admin password is printed by the backend.

## Testing

```bash
cd backend
go vet ./...
go test ./...            # add -race on Linux/macOS
```

The tests start the real HTTP server against a temporary data directory and speak each protocol to it:
publish and fetch for every format, access control, token scopes, cleanup, webhooks, notifications
(against a fake Gotify), single sign-on (against a fake provider), TLS start-up and shutdown. They need no
network. Please add a test for every behaviour change; look at `hardening_test.go` and `features_test.go`
for the style (`e.must("what", wantStatus, method, path, body, credentials)`).

Frontend changes: `npm --prefix frontend run build` must pass (it runs the TypeScript compiler). Check the
page in light and dark mode and at phone width.

Continuous integration runs all of this on Linux, macOS and Windows, plus `gofmt`, `go mod tidy`,
`govulncheck`, `npm audit`, a build of every release target and a container smoke test.

## Layout

```
backend/                  one Go package, standard library plus a handful of dependencies
  main.go                 commands (serve, healthcheck, reset-password), routes, middleware, logging
  config.go               process configuration: flags, environment, env file
  service_windows.go      Windows service integration (service_other.go elsewhere)
  db.go                   schema and migrations; repositories, artifacts, settings, audit events
  blobs.go                content-addressed file store
  auth.go                 users, roles, token scopes, sessions, lockout, API plumbing
  oidc.go                 OpenID Connect sign-in
  repo.go                 shared engine: hosted / proxy / group resolution, proxy cache and health, raw
  maven.go npm.go pypi.go docker.go gomod.go apt.go yum.go      format handlers
  signing.go              OpenPGP key for APT/YUM index signatures
  notify.go webhooks.go   event delivery: Gotify notifications, webhooks
  tasks.go system.go api.go   maintenance tasks, system info and export/import, management API
  ui.go                   serves the embedded frontend
frontend/src/
  App.tsx                 shell, navigation, command palette, sign-in
  api.ts                  API types and fetch helpers
  formats.tsx setup.ts    format catalogue and per-client setup snippets
  events.ts               event catalogue for webhook/notification pickers
  pages/                  one file per screen
deploy/                   service installers: linux (systemd), macos (launchd), windows (PowerShell)
docs/                     user documentation
scripts/build-release.sh  cross-compiles and packages every platform
.github/workflows/        ci.yml, release.yml
```

## Conventions

- **Keep it small.** RepoMan's value is that it is one binary with almost no dependencies. Prefer the
  standard library; a new dependency needs a good reason. No CGO (it would break cross-compilation).
- **No outbound connections** other than to URLs an administrator configured. No telemetry, update checks,
  CDNs or externally hosted fonts — ever. The UI must work with the content security policy in `main.go`.
- **Secure defaults.** New endpoints go through `a.api(role, …)`; think about what a read-only token and a
  viewer may do. Secrets are write-only in the API. Validate paths with `validPath`.
- Go code is `gofmt`-formatted and passes `go vet`. Errors shown to users say what to do next.
- Database changes: add tables to `schema` with `CREATE TABLE IF NOT EXISTS` and columns to `migrations`.
  Existing data directories must keep working; never rewrite a migration that has shipped.
- A new audit event needs a title in `eventTitles` (`notify.go`), an entry in `frontend/src/events.ts` and a
  row in `docs/notifications.md`.
- User-visible changes get a line in [CHANGELOG.md](CHANGELOG.md) and, where relevant, in `docs/`.

## Adding a notification service

Notification channels are pluggable. To add one besides Gotify: write a
`func (a *App) sendX(ctx, n *Notifier, msg notification) (string, error)` in `notify.go`, register it in
`notifierKinds`, add the kind to the form in `frontend/src/pages/Notifications.tsx`, and document it.
Storage, filtering, retries, the test button and the API are shared. Remember the rule above: only
self-hostable or explicitly configured endpoints.

## Releasing (maintainers)

1. Update `CHANGELOG.md`: move *Unreleased* entries under a new `## [x.y.z] - YYYY-MM-DD` heading.
2. Tag and push: `git tag vX.Y.Z && git push origin vX.Y.Z`.
3. The **Release** workflow tests, builds the archives for all platforms, pushes the multi-architecture
   image to `ghcr.io/<owner>/<repo>`, attaches signed build provenance and publishes the GitHub release
   with `SHA256SUMS`. A tag containing `-` (e.g. `v1.3.0-rc.1`) is published as a pre-release.

One-time repository setup: *Settings → Actions → General → Workflow permissions* can stay read-only (the
workflow requests what it needs); after the first release, make the container package public under
*Packages → repoman → Package settings*. Build provenance attestations require a public repository.
Run the workflow manually (*Actions → Release → Run workflow*) for a dry run that only builds the archives.

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).

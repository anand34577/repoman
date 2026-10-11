// RepoMan: a self-hosted artifact repository manager
// (maven, npm, pypi, docker, go, apt, yum, conda, raw — hosted, proxy and group repositories).
package main

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"golang.org/x/sync/singleflight"
)

// Set at release time: go build -ldflags "-X main.version=1.2.3 -X main.commit=abc1234 -X main.buildDate=2026-01-02"
var (
	version   = "dev"
	commit    = ""
	buildDate = ""
)

type App struct {
	cfg     Config
	db      *sql.DB
	blobs   *Blobs
	dataDir string
	uploads string // docker chunked-upload scratch dir
	client  *http.Client
	started time.Time

	// ponytail: one lock guards blob commit/GC; per-blob locks if write throughput ever matters
	mu      sync.Mutex
	npmMu   sync.Mutex // serializes npm packument read-modify-write
	indexMu sync.Mutex // serializes apt/yum index rebuilds

	authMu    sync.Mutex
	authCache map[string]cachedAuth
	fails     map[string]loginFails

	fetches  singleflight.Group // collapses concurrent proxy misses for the same path
	negCache sync.Map           // repo\x00path -> time.Time expiry for upstream 404s
	health   sync.Map           // repo -> *proxyHealth

	keyMu      sync.Mutex
	signingKey *openpgp.Entity

	oidc oidcState

	tasksMu sync.Mutex
	tasks   map[string]*taskState

	// upstreamRetry is how long an unreachable upstream is left alone before the next attempt;
	// in between, proxies answer from cache at once instead of waiting for another timeout.
	upstreamRetry time.Duration

	notifyQ     chan notifyJob // bounded queue of outgoing push notifications
	quotaWarned sync.Map       // repo -> time.Time of the last "quota reached" event

	requests [6]atomic.Int64 // HTTP responses by status class (index = code/100)

	dlMu      sync.Mutex
	dlPending map[int64]dlCount // download counters not yet written (see markDownloaded)

	searchIndex bool // the trigram index exists (see ensureSearchIndex)

	assetGen    atomic.Int64 // bumped whenever assets change; invalidates the cached aggregates below
	repoTotals  slowStat[map[string]repoView]
	storedBytes slowStat[[2]int64] // distinct content files and their bytes, for the caller's repositories
}

const usage = `RepoMan %s — self-hosted artifact repository manager

Usage:
  repoman [serve] [flags]          run the server (default)
  repoman version                  print version information
  repoman healthcheck [flags]      exit 0 if the local server answers /healthz (for Docker and monitors)
  repoman reset-password [user]    set a new password for a local account (default: admin)
  repoman service <action>         Windows only: install | uninstall | start | stop | status

Flags for serve, healthcheck and reset-password:
  --config FILE     env file with RM_* settings (default: repoman.env next to the binary)
  --addr ADDR       listen address (RM_ADDR, default :8081)
  --data-dir DIR    data directory (RM_DATA_DIR, default ./data)
  --tls-cert FILE   TLS certificate chain in PEM format (RM_TLS_CERT)
  --tls-key FILE    TLS private key in PEM format (RM_TLS_KEY)

Environment: RM_ADDR, RM_DATA_DIR, RM_ADMIN_PASSWORD, RM_TLS_CERT, RM_TLS_KEY,
RM_TRUSTED_PROXIES, RM_LOG_FILE, RM_ACCESS_LOG, RM_CONFIG. See docs/configuration.md.
`

func versionString() string {
	s := "RepoMan " + version
	if commit != "" {
		s += " (" + commit + ")"
	}
	if buildDate != "" {
		s += " built " + buildDate
	}
	return fmt.Sprintf("%s %s %s/%s", s, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		for _, a := range args {
			switch a {
			case "-h", "-help", "--help":
				fmt.Printf(usage, version)
				return
			case "-v", "-version", "--version":
				fmt.Println(versionString())
				return
			}
		}
		err = cmdServe(args)
	case "version":
		fmt.Println(versionString())
	case "healthcheck":
		err = cmdHealthcheck(args)
	case "reset-password":
		err = cmdResetPassword(args)
	case "service":
		err = cmdService(args)
	case "help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "repoman: unknown command %q\n\n", cmd)
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "repoman:", err)
		os.Exit(1)
	}
}

func cmdServe(args []string) error {
	cfg, rest, err := loadConfig("serve", args, nil)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unexpected argument %q (run `repoman help`)", rest[0])
	}
	if runningAsService() {
		return runService(func(ctx context.Context) error { return serve(ctx, cfg) })
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, cfg)
}

// serve runs the server until ctx is cancelled, then drains in-flight requests.
func serve(ctx context.Context, cfg Config) error {
	closeLog, err := setupLogging(cfg)
	if err != nil {
		return err
	}
	defer closeLog()
	a, err := newApp(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("opening data directory %s: %w", cfg.DataDir, err)
	}
	defer a.db.Close()
	defer a.flushDownloads() // counters of the last seconds, once requests have drained
	a.cfg = cfg
	if err := a.bootstrap(cfg.AdminPassword); err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           a.routes(),
		ReadHeaderTimeout: 15 * time.Second, // slow-header (slowloris) protection; bodies may be huge, so no read/write timeout
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w (is another instance running, or is the port in use?)", cfg.Addr, err)
	}
	scheme := "http"
	if cfg.TLSCert != "" {
		certs := &certReloader{certFile: cfg.TLSCert, keyFile: cfg.TLSKey}
		if _, err := certs.get(nil); err != nil {
			ln.Close()
			return fmt.Errorf("loading TLS certificate: %w", err)
		}
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certs.get}
		scheme = "https"
	}
	go a.scheduler()
	errc := make(chan error, 1)
	go func() {
		if scheme == "https" {
			errc <- srv.ServeTLS(ln, "", "")
		} else {
			errc <- srv.Serve(ln)
		}
	}()
	abs, _ := filepath.Abs(cfg.DataDir)
	log.Printf("%s", versionString())
	log.Printf("listening on %s://%s (data directory: %s)", scheme, ln.Addr(), abs)
	if scheme == "http" {
		log.Printf("serving plain HTTP: put RepoMan behind a TLS reverse proxy or set RM_TLS_CERT/RM_TLS_KEY before exposing it beyond localhost")
	}
	a.audit(nil, "system.start", "repoman", version)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Printf("shutting down: waiting up to 30s for requests in flight")
	shCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		srv.Close()
	}
	log.Printf("stopped")
	return nil
}

// certReloader re-reads the certificate when the file changes, so renewals (certbot, acme.sh) need no restart.
type certReloader struct {
	certFile, keyFile string
	mu                sync.Mutex
	cert              *tls.Certificate
	mod, checked      time.Time
}

func (c *certReloader) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cert != nil && time.Since(c.checked) < time.Minute {
		return c.cert, nil
	}
	c.checked = time.Now()
	st, err := os.Stat(c.certFile)
	if err == nil && (c.cert == nil || st.ModTime().After(c.mod)) {
		var cert tls.Certificate
		if cert, err = tls.LoadX509KeyPair(c.certFile, c.keyFile); err == nil {
			if c.cert != nil {
				log.Printf("reloaded TLS certificate %s", c.certFile)
			}
			c.cert, c.mod = &cert, st.ModTime()
		}
	}
	if err != nil {
		if c.cert == nil {
			return nil, err
		}
		log.Printf("TLS certificate reload failed, keeping the previous one: %v", err)
	}
	return c.cert, nil
}

func cmdHealthcheck(args []string) error {
	var target *string
	cfg, _, err := loadConfig("healthcheck", args, func(fs *flag.FlagSet) {
		target = fs.String("url", "", "URL to probe (default: /healthz on the configured listen address)")
	})
	if err != nil {
		return err
	}
	u := *target
	if u == "" {
		host, port, err := net.SplitHostPort(cfg.Addr)
		if err != nil {
			return fmt.Errorf("invalid listen address %q", cfg.Addr)
		}
		if ip, perr := netip.ParseAddr(host); host == "" || (perr == nil && ip.IsUnspecified()) {
			host = "127.0.0.1"
		}
		scheme := "http"
		if cfg.TLSCert != "" {
			scheme = "https"
		}
		u = scheme + "://" + net.JoinHostPort(host, port) + "/healthz"
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		// a loopback self-probe: the certificate is issued for the public name, not 127.0.0.1
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", u, resp.Status)
	}
	fmt.Println("ok")
	return nil
}

// cmdResetPassword is the recovery path when nobody can sign in as an administrator anymore.
func cmdResetPassword(args []string) error {
	var pass *string
	cfg, rest, err := loadConfig("reset-password", args, func(fs *flag.FlagSet) {
		pass = fs.String("password", "", "new password (default: generate a random one)")
	})
	if err != nil {
		return err
	}
	name := "admin"
	if len(rest) > 1 {
		return fmt.Errorf("expected one user name, got %d arguments", len(rest))
	}
	if len(rest) == 1 {
		name = rest[0]
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "repoman.db")); err != nil {
		return fmt.Errorf("no RepoMan database in %s (set --data-dir or RM_DATA_DIR to the server's data directory)", cfg.DataDir)
	}
	a, err := newApp(cfg.DataDir)
	if err != nil {
		return err
	}
	defer a.db.Close()
	var role string
	if err := a.db.QueryRow(`SELECT role FROM users WHERE username=?`, name).Scan(&role); err != nil {
		return fmt.Errorf("no user named %q", name)
	}
	newPass, generated := *pass, false
	if newPass == "" {
		newPass, generated = randomPassword(), true
	}
	if err := a.setPassword(name, newPass); err != nil {
		return err
	}
	// re-enable the account and sign it out everywhere
	a.db.Exec(`UPDATE users SET disabled=0 WHERE username=?`, name)
	a.db.Exec(`DELETE FROM tokens WHERE username=? AND kind='session'`, name)
	a.db.Exec(`INSERT INTO events(ts, username, action, target, detail) VALUES(?,?,?,?,?)`, time.Now().Unix(), "system", "password.reset", name, "from the command line")
	fmt.Printf("Password for %s (%s) has been reset and the account is enabled.\n", name, role)
	if generated {
		fmt.Printf("New password: %s\n", newPass)
	}
	fmt.Println("A running server may accept the old password for up to one minute.")
	return nil
}

func newApp(dataDir string) (*App, error) {
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		// holds password hashes, the signing key and upstream credentials: owner-only
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return nil, err
		}
	}
	for _, d := range []string{"blobs", "uploads"} {
		if err := os.MkdirAll(filepath.Join(dataDir, d), 0o700); err != nil {
			return nil, err
		}
	}
	db, err := openDB(filepath.Join(dataDir, "repoman.db"))
	if err != nil {
		return nil, err
	}
	a := &App{
		cfg:     Config{TrustedProxies: loopbackProxies, AccessLog: true},
		db:      db,
		blobs:   &Blobs{dir: filepath.Join(dataDir, "blobs")},
		dataDir: dataDir,
		uploads: filepath.Join(dataDir, "uploads"),
		started: time.Now(),
		client: &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
		}},
		authCache:     map[string]cachedAuth{},
		fails:         map[string]loginFails{},
		tasks:         map[string]*taskState{},
		upstreamRetry: 30 * time.Second,
		notifyQ:       make(chan notifyJob, 256),
		dlPending:     map[int64]dlCount{},
		searchIndex:   ensureSearchIndex(db),
	}
	a.getJSON("tasks", &a.tasks)
	for _, st := range a.tasks { // a task interrupted by a crash or restart is not running anymore
		st.Running = false
	}
	go a.notifyWorker()
	go a.downloadFlusher()
	return a, nil
}

func (a *App) routes() http.Handler {
	m := http.NewServeMux()
	handle := func(pattern, role string, f apiFunc) { m.HandleFunc(pattern, a.api(role, f)) }

	m.HandleFunc("POST /api/auth/login", a.login)
	m.HandleFunc("POST /api/auth/logout", a.logout)
	handle("GET /api/auth/me", roleViewer, func(w http.ResponseWriter, r *http.Request, u *User) (any, error) { return u, nil })
	handle("PUT /api/auth/password", roleViewer, fullScope(a.changePassword))
	m.HandleFunc("GET /api/auth/oidc/config", a.oidcConfigPublic)
	m.HandleFunc("GET /api/auth/oidc/login", a.oidcLogin)
	m.HandleFunc("GET /api/auth/oidc/callback", a.oidcCallback)
	m.HandleFunc("GET /api/auth/oidc/pending", a.oidcPendingInfo)
	m.HandleFunc("POST /api/auth/oidc/complete", a.oidcComplete)
	handle("POST /api/auth/oidc/link", roleViewer, fullScope(a.oidcLinkStart))
	handle("DELETE /api/auth/oidc/link", roleViewer, fullScope(a.oidcUnlinkAPI))
	handle("GET /api/auth/oidc/identity", roleViewer, a.oidcIdentityAPI)
	handle("GET /api/settings/oidc", roleAdmin, a.getOIDCConfigAPI)
	handle("PUT /api/settings/oidc", roleAdmin, a.putOIDCConfigAPI)
	handle("POST /api/settings/oidc/test", roleAdmin, a.testOIDCAPI)

	handle("GET /api/repositories", roleViewer, a.listReposAPI)
	handle("POST /api/repositories", roleAdmin, a.createRepoAPI)
	handle("POST /api/repositories/test-remote", roleAdmin, a.testRemoteAPI)
	handle("GET /api/repositories/{name}", roleViewer, a.getRepoAPI)
	handle("PUT /api/repositories/{name}", roleAdmin, a.updateRepoAPI)
	handle("DELETE /api/repositories/{name}", roleAdmin, a.deleteRepoAPI)
	handle("GET /api/repositories/{name}/browse", roleViewer, a.browseAPI)
	handle("GET /api/repositories/{name}/components", roleViewer, a.componentsAPI)
	handle("GET /api/repositories/{name}/asset", roleViewer, a.assetAPI)
	handle("DELETE /api/repositories/{name}/components", roleViewer, a.deleteComponentAPI)
	handle("POST /api/repositories/{name}/upload", roleViewer, a.uploadAPI)
	handle("POST /api/repositories/{name}/cleanup", roleAdmin, a.cleanupAPI)
	handle("POST /api/repositories/{name}/invalidate", roleAdmin, a.invalidateAPI)
	handle("POST /api/repositories/{name}/reindex", roleAdmin, a.reindexAPI)
	handle("GET /api/repositories/{name}/permissions", roleAdmin, a.getPermsAPI)
	handle("PUT /api/repositories/{name}/permissions", roleAdmin, a.putPermsAPI)

	handle("GET /api/repositories/{name}/package", roleViewer, a.packageAPI)
	handle("GET /api/repositories/{name}/dependents", roleViewer, a.dependentsAPI)
	handle("GET /api/repositories/{name}/checksums", roleViewer, a.checksumsAPI)
	handle("POST /api/repositories/{name}/promote", roleViewer, a.promoteAPI)
	handle("POST /api/repositories/{name}/bulk-delete", roleViewer, a.bulkDeleteAPI)
	handle("GET /api/assets/{id}/downloads", roleViewer, a.assetDownloadsAPI)
	handle("GET /api/storage", roleAdmin, a.storageAPI)
	handle("GET /api/saved-searches", roleViewer, a.listSavedSearchesAPI)
	handle("POST /api/saved-searches", roleViewer, a.saveSearchAPI)
	handle("DELETE /api/saved-searches/{id}", roleViewer, a.deleteSavedSearchAPI)

	handle("GET /api/search", roleViewer, a.searchAPI)
	handle("DELETE /api/assets/{id}", roleViewer, a.deleteAssetAPI)
	handle("GET /api/stats", roleViewer, a.statsAPI)

	handle("GET /api/users", roleAdmin, a.listUsersAPI)
	handle("POST /api/users", roleAdmin, a.createUserAPI)
	handle("PUT /api/users/{name}", roleAdmin, a.updateUserAPI)
	handle("DELETE /api/users/{name}", roleAdmin, a.deleteUserAPI)

	handle("GET /api/tokens", roleViewer, a.listTokensAPI)
	handle("POST /api/tokens", roleViewer, fullScope(a.createTokenAPI))
	handle("DELETE /api/tokens/{id}", roleViewer, fullScope(a.deleteTokenAPI))

	handle("GET /api/webhooks", roleAdmin, a.listWebhooksAPI)
	handle("POST /api/webhooks", roleAdmin, a.saveWebhookAPI)
	handle("PUT /api/webhooks/{id}", roleAdmin, a.saveWebhookAPI)
	handle("DELETE /api/webhooks/{id}", roleAdmin, a.deleteWebhookAPI)
	handle("POST /api/webhooks/{id}/test", roleAdmin, a.testWebhookAPI)

	handle("GET /api/notifiers", roleAdmin, a.listNotifiersAPI)
	handle("POST /api/notifiers", roleAdmin, a.saveNotifierAPI)
	handle("PUT /api/notifiers/{id}", roleAdmin, a.saveNotifierAPI)
	handle("DELETE /api/notifiers/{id}", roleAdmin, a.deleteNotifierAPI)
	handle("POST /api/notifiers/{id}/test", roleAdmin, a.testNotifierAPI)

	handle("GET /api/tasks", roleAdmin, a.listTasksAPI)
	handle("POST /api/tasks/{id}/run", roleAdmin, a.runTaskAPI)

	handle("GET /api/settings", roleViewer, a.getSettingsAPI)
	handle("PUT /api/settings", roleAdmin, a.putSettingsAPI)
	handle("GET /api/audit", roleAdmin, a.auditAPI)
	handle("GET /api/audit/export", roleAdmin, a.auditExportAPI)
	handle("GET /api/system/info", roleAdmin, a.systemInfoAPI)
	handle("GET /api/system/export", roleAdmin, a.exportAPI)
	handle("POST /api/system/import", roleAdmin, a.importAPI)
	m.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"name": "RepoMan", "apiVersion": "v1"})
	})
	m.HandleFunc("GET /api/signing-key", a.publicKeyHandler)
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 404, errBody("no such endpoint")) })
	// /api/v1/... is the stable, versioned name of every endpoint above; integrations should use it.
	m.HandleFunc("/api/v1/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path, r2.URL.RawPath = "/api/"+r.PathValue("rest"), ""
		m.ServeHTTP(w, r2)
	})

	m.HandleFunc("/repository/{repo}/{path...}", a.handleRepo)
	m.HandleFunc("/v2/{path...}", a.handleDocker)
	m.HandleFunc("GET /metrics", a.metrics)
	m.HandleFunc("/healthz", a.healthz)
	m.Handle("/", uiHandler())
	return a.middleware(m)
}

// healthz reports readiness: the process is up and the database answers.
func (a *App) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	w.Header().Set("Cache-Control", "no-store")
	if err := a.db.PingContext(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok"))
}

// ---------- reverse-proxy awareness ----------

func (a *App) trustedAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range a.cfg.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// fromTrustedProxy reports whether the TCP peer is a reverse proxy whose X-Forwarded-* headers we believe.
// Anyone else could forge those headers to spoof their address, the scheme or the public host name.
func (a *App) fromTrustedProxy(r *http.Request) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	addr, err := netip.ParseAddr(host)
	return err == nil && a.trustedAddr(addr)
}

// clientIP is the address of the real client: the TCP peer, or — behind trusted proxies —
// the right-most X-Forwarded-For entry that is not itself a trusted proxy.
func (a *App) clientIP(r *http.Request) string {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !a.fromTrustedProxy(r) {
		return ip
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break // garbage: everything to the left is untrustworthy too
		}
		if !a.trustedAddr(addr) {
			return addr.Unmap().String()
		}
	}
	return ip
}

func (a *App) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (a.fromTrustedProxy(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
}

// ---------- middleware ----------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int)           { s.status = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// uiCSP lets the embedded UI load only what ships inside the binary: no CDNs, fonts or analytics.
// Mantine sets inline style attributes, hence 'unsafe-inline' for styles (never for scripts).
const uiCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; " +
	"connect-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// crossSite reports whether a state-changing request carrying the session cookie was triggered by
// another site (CSRF). Package clients and API integrations send neither header and are unaffected.
func (a *App) crossSite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	if _, err := r.Cookie(sessionCookie); err != nil {
		return false // not authenticated by cookie: nothing to forge
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" {
		return s != "same-origin" && s != "none"
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil || o.Host == "" {
		return true
	}
	if strings.EqualFold(o.Host, r.Host) {
		return false
	}
	if b, err := url.Parse(a.settings().BaseURL); err == nil && b.Host != "" && strings.EqualFold(o.Host, b.Host) {
		return false
	}
	return true
}

func (a *App) middleware(h http.Handler) http.Handler {
	gz := gzipHandler(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		if a.isHTTPS(r) {
			hdr.Set("Strict-Transport-Security", "max-age=31536000")
		}
		artifact := strings.HasPrefix(r.URL.Path, "/repository/") || strings.HasPrefix(r.URL.Path, "/v2/")
		switch {
		case a.crossSite(r):
			writeJSON(sw, http.StatusForbidden, errBody("cross-site request blocked"))
		case artifact:
			// artifact downloads are already compressed and need exact lengths/ranges;
			// uploaded files (e.g. HTML in a raw repo) must never run script with the viewer's session
			hdr.Set("Content-Security-Policy", "sandbox")
			h.ServeHTTP(sw, r)
		default:
			hdr.Set("Content-Security-Policy", uiCSP)
			hdr.Set("X-Frame-Options", "DENY")
			hdr.Set("Referrer-Policy", "same-origin")
			hdr.Set("Cross-Origin-Opener-Policy", "same-origin")
			hdr.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
			if strings.HasPrefix(r.URL.Path, "/api/") {
				hdr.Set("Cache-Control", "no-store")
			}
			gz.ServeHTTP(sw, r)
		}
		if c := sw.status / 100; c > 0 && c < len(a.requests) {
			a.requests[c].Add(1)
		}
		if a.cfg.AccessLog {
			log.Printf("%d %s %s %s %s", sw.status, r.Method, r.URL.Path, a.clientIP(r), time.Since(start).Round(time.Millisecond))
		}
	})
}

type gzipWriter struct {
	http.ResponseWriter
	zw *gzip.Writer
}

func (g gzipWriter) WriteHeader(c int) {
	g.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(c)
}
func (g gzipWriter) Write(b []byte) (int, error) { return g.zw.Write(b) }

func gzipHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// a byte range refers to the uncompressed file, and fonts and images are compressed already
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Method == http.MethodHead || r.Header.Get("Range") != "" ||
			slices.Contains([]string{".woff2", ".woff", ".png", ".jpg", ".webp", ".ico"}, path.Ext(r.URL.Path)) {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		zw := gzip.NewWriter(w)
		defer zw.Close()
		h.ServeHTTP(gzipWriter{w, zw}, r)
	})
}

// ---------- logging ----------

// rotatingFile is a size-rotated log file: repoman.log, repoman.log.1 … .5.
type rotatingFile struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

const (
	logMaxBytes = 20 << 20
	logKeep     = 5
)

func (l *rotatingFile) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.f, l.size = f, st.Size()
	return nil
}

func (l *rotatingFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size+int64(len(p)) > logMaxBytes {
		l.f.Close()
		os.Remove(fmt.Sprintf("%s.%d", l.path, logKeep))
		for i := logKeep - 1; i >= 1; i-- {
			os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
		}
		os.Rename(l.path, l.path+".1")
		if err := l.open(); err != nil {
			return 0, err
		}
	}
	n, err := l.f.Write(p)
	l.size += int64(n)
	return n, err
}

func setupLogging(cfg Config) (func(), error) {
	if cfg.LogFile == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700); err != nil {
		return nil, fmt.Errorf("log file: %w", err)
	}
	lf := &rotatingFile{path: cfg.LogFile}
	if err := lf.open(); err != nil {
		return nil, fmt.Errorf("log file: %w", err)
	}
	if runningAsService() { // a service has no console
		log.SetOutput(lf)
	} else {
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
	}
	return func() { log.SetOutput(os.Stderr); lf.f.Close() }, nil
}

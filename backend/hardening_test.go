package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func (e *testEnv) token(name, scope string) string {
	e.t.Helper()
	var out struct{ Token string }
	json.Unmarshal([]byte(e.must("create "+scope+" token", 200, "POST", "/api/tokens", jsonBody(map[string]any{"name": name, "scope": scope}), admin)), &out)
	if !strings.HasPrefix(out.Token, "rm_") {
		e.t.Fatalf("no token returned")
	}
	return out.Token
}

// bearer performs a request authenticated with an API token.
func (e *testEnv) bearer(method, path string, body io.Reader, tok string) (int, string) {
	c, b, _ := e.req(method, path, body, "", "Authorization", "Bearer "+tok)
	return c, b
}

func TestTokenScopes(t *testing.T) {
	e := newEnv(t)
	read, write, full := e.token("ro", "read"), e.token("rw", "write"), e.token("all", "full")
	e.must("bad scope", 400, "POST", "/api/tokens", jsonBody(map[string]any{"name": "x", "scope": "root"}), admin)

	expect := func(what string, want int, method, path string, body io.Reader, tok string) {
		t.Helper()
		if c, b := e.bearer(method, path, body, tok); c != want {
			t.Fatalf("%s: %s %s -> %d, want %d: %s", what, method, path, c, want, b)
		}
	}
	text := func() io.Reader { return strings.NewReader("x") }

	// read: browse and download, nothing else — even though the owner is an admin
	expect("read lists", 200, "GET", "/api/repositories", nil, read)
	expect("read cannot deploy", 403, "PUT", "/repository/raw-hosted/a.txt", text(), read)
	expect("read cannot use admin API", 403, "GET", "/api/users", nil, read)
	expect("read cannot mint tokens", 403, "POST", "/api/tokens", jsonBody(map[string]any{"name": "evil"}), read)
	expect("read cannot change password", 403, "PUT", "/api/auth/password", jsonBody(map[string]string{"current": "admin123", "new": "hacked123"}), read)

	// write: publish and delete artifacts, but no administration and no account changes
	expect("write deploys", 201, "PUT", "/repository/raw-hosted/a.txt", text(), write)
	expect("read downloads", 200, "GET", "/repository/raw-hosted/a.txt", nil, read)
	expect("write cannot administer", 403, "POST", "/api/users", jsonBody(map[string]string{"username": "eve", "password": "password1", "role": "admin"}), write)
	expect("write cannot mint tokens", 403, "POST", "/api/tokens", jsonBody(map[string]any{"name": "evil"}), write)
	expect("write cannot revoke tokens", 403, "DELETE", "/api/tokens/1", nil, write)
	expect("write deletes artifacts", 204, "DELETE", "/repository/raw-hosted/a.txt", nil, write)

	// full: everything the owner can do
	expect("full administers", 204, "POST", "/api/users", jsonBody(map[string]string{"username": "bob", "password": "password1", "role": "viewer"}), full)

	// a token works as a basic-auth password too, with the same limits
	e.must("basic auth with read token", 403, "PUT", "/repository/raw-hosted/b.txt", text(), "admin:"+read)

	var me User
	_, b := e.bearer("GET", "/api/v1/auth/me", nil, read)
	json.Unmarshal([]byte(b), &me)
	if me.Scope != "read" || me.Role != "admin" {
		t.Fatalf("/auth/me should report the token scope: %s", b)
	}
	if b := e.must("list", 200, "GET", "/api/tokens", nil, admin); !strings.Contains(b, `"scope":"read"`) || strings.Contains(b, read) {
		t.Fatalf("token list should show scopes and never the secret: %s", b)
	}
}

func TestCrossSiteRequestsBlocked(t *testing.T) {
	e := newEnv(t)
	_, _, h := e.req("POST", "/api/auth/login", jsonBody(map[string]string{"username": "admin", "password": "admin123"}), "")
	cookie, _, _ := strings.Cut(h.Get("Set-Cookie"), ";")
	if !strings.HasPrefix(cookie, sessionCookie+"=") || !strings.Contains(h.Get("Set-Cookie"), "HttpOnly") {
		t.Fatalf("no session cookie: %v", h)
	}
	body := func() io.Reader { return jsonBody(map[string]any{"name": "t"}) }
	check := func(what string, want int, hdr ...string) {
		t.Helper()
		if c, b, _ := e.req("POST", "/api/tokens", body(), "", append([]string{"Cookie", cookie}, hdr...)...); c != want {
			t.Fatalf("%s: got %d, want %d: %s", what, c, want, b)
		}
	}
	check("same-origin fetch", 200, "Sec-Fetch-Site", "same-origin")
	check("cross-site fetch", 403, "Sec-Fetch-Site", "cross-site")
	check("sibling subdomain", 403, "Sec-Fetch-Site", "same-site")
	check("foreign origin (older browser)", 403, "Origin", "https://evil.example")
	check("own origin (older browser)", 200, "Origin", e.srv.URL)
	// no cookie: nothing a browser could be tricked into sending, so API clients are never blocked
	e.must("token client with odd Origin", 200, "POST", "/api/tokens", body(), admin, "Origin", "https://ci.example")
	// reads are never blocked
	if c, _, _ := e.req("GET", "/api/auth/me", nil, "", "Cookie", cookie, "Sec-Fetch-Site", "cross-site"); c != 200 {
		t.Fatalf("cross-site GET should pass: %d", c)
	}
}

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t)
	_, _, h := e.req("GET", "/api/version", nil, "")
	for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "no-store"} {
		if h.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, h.Get(k), want)
		}
	}
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("UI CSP = %q", csp)
	}
	e.must("upload", 201, "PUT", "/repository/raw-hosted/x.html", strings.NewReader("<script>alert(1)</script>"), admin)
	if _, _, h := e.req("GET", "/repository/raw-hosted/x.html", nil, ""); h.Get("Content-Security-Policy") != "sandbox" {
		t.Errorf("artifacts must be served sandboxed, got %q", h.Get("Content-Security-Policy"))
	}
	e.must("healthz", 200, "GET", "/healthz", nil, "")
	e.must("v1 alias", 200, "GET", "/api/v1/repositories", nil, admin)
	e.must("v1 unknown", 404, "GET", "/api/v1/nope", nil, admin)
	e.must("oversized body", 413, "POST", "/api/users", bytes.NewReader(append([]byte(`{"username":"`), bytes.Repeat([]byte("a"), 2<<20)...)), admin)
	e.must("long password", 400, "POST", "/api/users", jsonBody(map[string]string{"username": "long", "password": strings.Repeat("p", 73), "role": "viewer"}), admin)
}

func TestInitialAdminPassword(t *testing.T) {
	dir := t.TempDir()
	a, err := newApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.bootstrap(""); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer func() { srv.Close(); a.db.Close() }()
	e := &testEnv{t, a, srv}

	file := filepath.Join(dir, "initial-admin-password")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("generated password not written: %v", err)
	}
	pass := strings.TrimSpace(string(raw))
	if len(pass) < 16 {
		t.Fatalf("weak generated password %q", pass)
	}
	e.must("well-known default must not work", 401, "GET", "/api/auth/me", nil, "admin:admin123")
	if b := e.must("stats", 200, "GET", "/api/stats", nil, "admin:"+pass); !strings.Contains(b, `"passwordChanged":false`) {
		t.Fatalf("checklist should ask for a password change: %s", b)
	}
	e.must("change", 204, "PUT", "/api/auth/password", jsonBody(map[string]string{"current": pass, "new": "a-better-password"}), "admin:"+pass)
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("initial password file should be deleted after the password change")
	}
	e.must("old password", 401, "GET", "/api/auth/me", nil, "admin:"+pass)
	if b := e.must("stats", 200, "GET", "/api/stats", nil, "admin:a-better-password"); !strings.Contains(b, `"passwordChanged":true`) {
		t.Fatalf("checklist not updated: %s", b)
	}
	if err := a.bootstrap(""); err != nil { // a restart must not recreate anything
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("restart recreated the initial password file")
	}
}

func TestPasswordChangeSignsOutOtherSessions(t *testing.T) {
	e := newEnv(t)
	login := func() string {
		_, _, h := e.req("POST", "/api/auth/login", jsonBody(map[string]string{"username": "admin", "password": "admin123"}), "")
		c, _, _ := strings.Cut(h.Get("Set-Cookie"), ";")
		return c
	}
	mine, other := login(), login()
	if c, b, _ := e.req("PUT", "/api/auth/password", jsonBody(map[string]string{"current": "admin123", "new": "new-password-1"}), "", "Cookie", mine); c != 204 {
		t.Fatalf("change password: %d %s", c, b)
	}
	if c, _, _ := e.req("GET", "/api/auth/me", nil, "", "Cookie", mine); c != 200 {
		t.Fatal("the session that changed the password should stay signed in")
	}
	if c, _, _ := e.req("GET", "/api/auth/me", nil, "", "Cookie", other); c != 401 {
		t.Fatal("other sessions should be signed out after a password change")
	}
}

func TestForwardedHeadersNeedTrustedProxy(t *testing.T) {
	e := newEnv(t)
	r := httptest.NewRequest("GET", "http://internal:8081/", nil)
	r.RemoteAddr = "203.0.113.9:4000"
	r.Header.Set("X-Forwarded-For", "10.1.1.1")
	r.Header.Set("X-Forwarded-Host", "evil.example")
	r.Header.Set("X-Forwarded-Proto", "https")
	if ip, base := e.a.clientIP(r), e.a.baseURL(r); ip != "203.0.113.9" || base != "http://internal:8081" || e.a.isHTTPS(r) {
		t.Fatalf("headers from an untrusted peer were believed: ip=%s base=%s", ip, base)
	}
	e.a.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("10.0.0.0/8")}
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.1.1.1") // client, then an inner trusted proxy
	r.Header.Set("X-Forwarded-Host", "repo.example.com")
	if ip, base := e.a.clientIP(r), e.a.baseURL(r); ip != "198.51.100.7" || base != "https://repo.example.com" || !e.a.isHTTPS(r) {
		t.Fatalf("trusted proxy headers ignored: ip=%s base=%s", ip, base)
	}
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7") // a client-supplied entry on the left must not win
	if ip := e.a.clientIP(r); ip != "198.51.100.7" {
		t.Fatalf("spoofable left-most address used: %s", ip)
	}
}

func TestConfigFileAndProxies(t *testing.T) {
	file := filepath.Join(t.TempDir(), "repoman.env")
	os.WriteFile(file, []byte("\xef\xbb\xbf# comment\nRM_TEST_A=one\nexport RM_TEST_B=\"two words\"\n\nRM_TEST_C='x=y'\n"), 0o600)
	t.Setenv("RM_TEST_A", "from-env") // the real environment wins over the file
	os.Unsetenv("RM_TEST_B")
	os.Unsetenv("RM_TEST_C")
	t.Cleanup(func() { os.Unsetenv("RM_TEST_B"); os.Unsetenv("RM_TEST_C") })
	if err := loadEnvFile(file); err != nil {
		t.Fatal(err)
	}
	if a, b, c := os.Getenv("RM_TEST_A"), os.Getenv("RM_TEST_B"), os.Getenv("RM_TEST_C"); a != "from-env" || b != "two words" || c != "x=y" {
		t.Fatalf("env file parsed wrong: %q %q %q", a, b, c)
	}
	os.WriteFile(file, []byte("not a setting\n"), 0o600)
	if err := loadEnvFile(file); err == nil || !strings.Contains(err.Error(), ":1:") {
		t.Fatalf("malformed line should fail with its line number, got %v", err)
	}
	p, err := parseProxies("10.0.0.0/8, 192.168.1.5 ::1")
	if err != nil || len(p) != 3 || p[1].String() != "192.168.1.5/32" {
		t.Fatalf("parseProxies: %v %v", p, err)
	}
	if _, err := parseProxies("nginx"); err == nil {
		t.Fatal("host names are not accepted as trusted proxies")
	}
}

// gotify records what a fake Gotify server receives.
type gotify struct {
	mu   sync.Mutex
	msgs []map[string]any
	keys []string
	fail atomic.Int32 // status to answer with, 0 = 200
	srv  *httptest.Server
}

func newGotify(t *testing.T) *gotify {
	g := &gotify{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/message" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		if c := g.fail.Load(); c != 0 {
			w.WriteHeader(int(c))
			return
		}
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		g.mu.Lock()
		g.msgs, g.keys = append(g.msgs, m), append(g.keys, r.Header.Get("X-Gotify-Key"))
		g.mu.Unlock()
		w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *gotify) wait(t *testing.T, title string) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		g.mu.Lock()
		for _, m := range g.msgs {
			if m["title"] == title {
				g.mu.Unlock()
				return m
			}
		}
		g.mu.Unlock()
	}
	t.Fatalf("no %q notification arrived; got %v", title, g.msgs)
	return nil
}

func (g *gotify) count() int { g.mu.Lock(); defer g.mu.Unlock(); return len(g.msgs) }

func TestGotifyNotifications(t *testing.T) {
	e := newEnv(t)
	g := newGotify(t)
	e.must("token required", 400, "POST", "/api/notifiers", jsonBody(map[string]any{"name": "ops", "url": g.srv.URL}), admin)
	e.must("bad url", 400, "POST", "/api/notifiers", jsonBody(map[string]any{"name": "ops", "url": "gotify.local", "token": "x"}), admin)
	e.must("bad kind", 400, "POST", "/api/notifiers", jsonBody(map[string]any{"name": "ops", "kind": "pigeon", "url": g.srv.URL, "token": "x"}), admin)
	e.must("create", 200, "POST", "/api/notifiers", jsonBody(map[string]any{
		"name": "ops", "kind": "gotify", "url": g.srv.URL + "/", "token": "AppT0ken", "priority": 3, "alertPriority": 9,
		"events": []string{"docker.push", "repository.*", "task.failed"}, "repos": []string{}, "enabled": true,
	}), admin)
	if b := e.must("list", 200, "GET", "/api/notifiers", nil, admin); strings.Contains(b, "AppT0ken") || !strings.Contains(b, `"hasToken":true`) {
		t.Fatalf("the token must never be returned: %s", b)
	}

	// test button: synchronous, reports the outcome
	if b := e.must("test", 200, "POST", "/api/notifiers/1/test", nil, admin); !strings.Contains(b, `"ok":true`) {
		t.Fatalf("test delivery: %s", b)
	}
	if m := g.wait(t, "Test notification"); g.keys[0] != "AppT0ken" {
		t.Fatalf("wrong application token sent: %v %v", g.keys, m)
	}

	// subscribed event (prefix pattern), routine priority
	e.must("create repo", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "raw-extra", "format": "raw", "type": "hosted"}), admin)
	m := g.wait(t, "Repository created")
	if m["priority"] != float64(3) || !strings.Contains(m["message"].(string), "raw-extra") || !strings.Contains(m["message"].(string), "by admin") {
		t.Fatalf("unexpected message: %v", m)
	}
	// not subscribed: uploads stay quiet
	before := g.count()
	e.must("upload", 201, "PUT", "/repository/raw-hosted/quiet.txt", strings.NewReader("x"), admin)
	// problems use the alert priority
	e.a.audit(nil, "task.failed", "cleanup", "disk on fire")
	if m := g.wait(t, "Scheduled task failed"); m["priority"] != float64(9) {
		t.Fatalf("alerts should use the alert priority: %v", m)
	}
	if g.count() != before+1 {
		t.Fatalf("an unsubscribed event was delivered: %v", g.msgs)
	}

	// a rejected token is reported clearly and not retried forever
	g.fail.Store(401)
	if b := e.must("test with bad token", 200, "POST", "/api/notifiers/1/test", nil, admin); !strings.Contains(b, `"ok":false`) || !strings.Contains(b, "application token") {
		t.Fatalf("failed delivery should explain itself: %s", b)
	}
	e.must("delete", 204, "DELETE", "/api/notifiers/1", nil, admin)
	e.must("delete again", 404, "DELETE", "/api/notifiers/1", nil, admin)
}

func (e *testEnv) events(action string) int {
	var n int
	e.a.db.QueryRow(`SELECT COUNT(*) FROM events WHERE action=?`, action).Scan(&n)
	return n
}

func TestProxyOutageEvents(t *testing.T) {
	e := newEnv(t)
	var down atomic.Bool
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte("content of " + r.URL.Path))
	}))
	defer up.Close()
	var denied atomic.Bool
	up.Config.Handler = func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if denied.Load() {
				w.WriteHeader(401)
				return
			}
			next.ServeHTTP(w, r)
		})
	}(up.Config.Handler)
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "raw-proxy", "format": "raw", "type": "proxy", "remoteUrl": up.URL}), admin)
	e.must("fetch", 200, "GET", "/repository/raw-proxy/a.txt", nil, admin)

	// one refused item says nothing about the upstream as a whole: no outage, no alert
	denied.Store(true)
	e.must("item refused by upstream", 502, "GET", "/repository/raw-proxy/private.txt", nil, admin)
	denied.Store(false)
	if n := e.events("repository.degraded"); n != 0 {
		t.Fatalf("a 401 for one file must not mark the repository as down (got %d events)", n)
	}

	down.Store(true)
	e.must("upstream down", 502, "GET", "/repository/raw-proxy/b.txt", nil, admin)
	// while it is down, further misses answer at once without contacting the upstream again
	before := hits.Load()
	if b := e.must("still down", 502, "GET", "/repository/raw-proxy/c.txt", nil, admin); !strings.Contains(b, "next attempt in") {
		t.Fatalf("expected the back-off explanation, got %q", b)
	}
	if hits.Load() != before {
		t.Fatal("the upstream was contacted during the back-off period")
	}
	e.a.upstreamRetry = 0 // back-off over
	if b := e.must("cached file survives the outage", 200, "GET", "/repository/raw-proxy/a.txt", nil, admin); b != "content of /a.txt" {
		t.Fatalf("cache: %q", b)
	}
	if n := e.events("repository.degraded"); n != 1 {
		t.Fatalf("want exactly one degraded event per outage, got %d", n)
	}
	if b := e.must("status", 200, "GET", "/api/repositories/raw-proxy", nil, admin); !strings.Contains(b, `"status":"degraded"`) {
		t.Fatalf("status: %s", b)
	}
	down.Store(false)
	e.must("back", 200, "GET", "/repository/raw-proxy/b.txt", nil, admin)
	if n := e.events("repository.recovered"); n != 1 {
		t.Fatalf("want one recovered event, got %d", n)
	}
}

func TestLockoutEvent(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 12; i++ {
		e.req("GET", "/api/auth/me", nil, "admin:wrong")
	}
	if n := e.events("login.lockout"); n != 1 {
		t.Fatalf("want one lockout event, got %d", n)
	}
}

func TestGoChecksumDatabaseRelay(t *testing.T) {
	e := newEnv(t)
	var asked []string
	var mu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Path)
		mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/sumdb/sum.golang.org/") {
			w.Write([]byte("sumdb:" + r.URL.Path))
			return
		}
		http.NotFound(w, r)
	}))
	defer up.Close()
	px, _ := e.a.getRepo("go-proxy")
	px.RemoteURL = up.URL
	e.a.saveRepo(px)

	e.must("supported", 200, "GET", "/repository/go-public/sumdb/sum.golang.org/supported", nil, "")
	if b := e.must("lookup", 200, "GET", "/repository/go-public/sumdb/sum.golang.org/lookup/golang.org/x/text@v0.3.0", nil, ""); !strings.HasSuffix(b, "/lookup/golang.org/x/text@v0.3.0") {
		t.Fatalf("lookup not relayed: %q", b)
	}
	e.must("hosted-only repo has no checksum database", 404, "GET", "/repository/go-hosted/sumdb/sum.golang.org/supported", nil, "")

	// a module published here is private: its name must never reach the upstream
	e.must("publish", 201, "PUT", "/repository/go-hosted/corp.example/secret/@v/v1.0.0.mod", strings.NewReader("module corp.example/secret\n"), admin)
	e.must("private lookup", 404, "GET", "/repository/go-public/sumdb/sum.golang.org/lookup/corp.example/secret@v1.0.0", nil, "")
	mu.Lock()
	defer mu.Unlock()
	for _, p := range asked {
		if strings.Contains(p, "corp.example") && strings.Contains(p, "sumdb") {
			t.Fatalf("private module name leaked to the upstream checksum database: %s", p)
		}
	}
	var comps int
	e.a.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE repo='go-proxy' AND component LIKE 'sumdb%'`).Scan(&comps)
	if comps != 0 {
		t.Fatal("checksum database files must not show up as components")
	}
}

func TestMavenGroupMetadataMerge(t *testing.T) {
	e := newEnv(t)
	px, _ := e.a.getRepo("maven-central")
	px.Offline = true // keep the test off the network
	e.a.saveRepo(px)
	meta := func(versions ...string) io.Reader {
		return strings.NewReader(`<?xml version="1.0"?><metadata><groupId>com.acme</groupId><artifactId>lib</artifactId><versioning><latest>` + versions[len(versions)-1] +
			`</latest><versions><version>` + strings.Join(versions, "</version><version>") + `</version></versions><lastUpdated>20260101000000</lastUpdated></versioning></metadata>`)
	}
	e.must("releases metadata", 201, "PUT", "/repository/maven-releases/com/acme/lib/maven-metadata.xml", meta("1.0", "1.10", "1.2"), admin)
	e.must("snapshots metadata", 201, "PUT", "/repository/maven-snapshots/com/acme/lib/maven-metadata.xml", meta("1.11-SNAPSHOT"), admin)
	b := e.must("merged", 200, "GET", "/repository/maven-public/com/acme/lib/maven-metadata.xml", nil, "")
	var m mavenMeta
	if err := xml.Unmarshal([]byte(b), &m); err != nil {
		t.Fatalf("merged metadata is not valid XML: %v\n%s", err, b)
	}
	if got := strings.Join(m.Versioning.Versions, ","); got != "1.0,1.2,1.10,1.11-SNAPSHOT" {
		t.Fatalf("versions = %s", got)
	}
	if m.Versioning.Release != "1.10" || m.Versioning.Latest != "1.11-SNAPSHOT" || m.GroupID != "com.acme" {
		t.Fatalf("release/latest wrong: %+v", m.Versioning)
	}
	sum := sha1.Sum([]byte(b))
	if got := e.must("checksum", 200, "GET", "/repository/maven-public/com/acme/lib/maven-metadata.xml.sha1", nil, ""); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("checksum does not match the merged document: %s", got)
	}
	// only one member has it: served untouched
	e.must("other", 201, "PUT", "/repository/maven-releases/com/acme/solo/maven-metadata.xml", strings.NewReader("<metadata><groupId>x</groupId></metadata>"), admin)
	if b := e.must("single", 200, "GET", "/repository/maven-public/com/acme/solo/maven-metadata.xml", nil, ""); b != "<metadata><groupId>x</groupId></metadata>" {
		t.Fatalf("single-member metadata rewritten: %s", b)
	}
	for _, c := range [][2]string{{"1.0", "1.0.1"}, {"1.0-alpha", "1.0-beta"}, {"1.0-rc1", "1.0"}, {"1.0-SNAPSHOT", "1.0"}, {"1.0", "1.0-sp1"}, {"1.9", "1.10"}, {"2.0-M1", "2.0-RC1"}} {
		if mavenCompare(c[0], c[1]) >= 0 || mavenCompare(c[1], c[0]) <= 0 {
			t.Errorf("mavenCompare: expected %s < %s", c[0], c[1])
		}
	}
	if mavenCompare("1.0", "1") != 0 {
		t.Error("mavenCompare: 1.0 should equal 1")
	}
}

// A data directory from before 1.2 whose admin never changed the old default password.
func TestLegacyDefaultPasswordIsFlagged(t *testing.T) {
	e := newEnv(t) // bootstraps with admin123
	e.a.db.Exec(`DELETE FROM settings WHERE key='initial_password'`)
	if b := e.must("stats", 200, "GET", "/api/stats", nil, admin); !strings.Contains(b, `"passwordChanged":true`) {
		t.Fatalf("precondition: %s", b)
	}
	if err := e.a.bootstrap(""); err != nil { // what a restart after upgrading does
		t.Fatal(err)
	}
	if b := e.must("stats", 200, "GET", "/api/stats", nil, admin); !strings.Contains(b, `"passwordChanged":false`) {
		t.Fatalf("an unchanged admin123 password should bring the reminder back: %s", b)
	}
	e.must("change", 204, "PUT", "/api/auth/password", jsonBody(map[string]string{"current": "admin123", "new": "something-better"}), admin)
	if err := e.a.bootstrap(""); err != nil {
		t.Fatal(err)
	}
	if b := e.must("stats", 200, "GET", "/api/stats", nil, "admin:something-better"); !strings.Contains(b, `"passwordChanged":true`) {
		t.Fatalf("reminder should be gone once the password is changed: %s", b)
	}
}

package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIdP is a minimal OpenID provider: discovery, JWKS, authorize (auto-approves as `who`) and token.
type fakeIdP struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	other *rsa.PrivateKey // for forged tokens
	mu    sync.Mutex
	codes map[string]map[string]string // code -> authorize params
	// knobs
	who      map[string]any // claims for the next authorize
	forge    bool
	aud      string
	expired  bool
	badNonce bool
	pkceErr  string
}

func newIdP(t *testing.T) *fakeIdP { return newIdPAt(t, "") }

func newIdPAt(t *testing.T, addr string) *fakeIdP {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	k2, _ := rsa.GenerateKey(rand.Reader, 2048)
	p := &fakeIdP{key: k, other: k2, codes: map[string]map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"issuer": p.srv.URL, "authorization_endpoint": p.srv.URL + "/authorize", "token_endpoint": p.srv.URL + "/token", "jwks_uri": p.srv.URL + "/jwks"})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": "AQAB"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		params := map[string]string{}
		for k := range q {
			params[k] = q.Get(k)
		}
		code := randomHex(8)
		p.mu.Lock()
		p.codes[code] = params
		p.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), 302)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		p.mu.Lock()
		params, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		who, forge, aud, expired, badNonce := p.who, p.forge, p.aud, p.expired, p.badNonce
		p.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		user, pass, basic := r.BasicAuth()
		switch {
		case !ok:
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		case base64.RawURLEncoding.EncodeToString(sum[:]) != params["code_challenge"] || params["code_challenge_method"] != "S256":
			p.pkceErr = "pkce mismatch"
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		case r.Form.Get("redirect_uri") != params["redirect_uri"]:
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		case !basic || user != "repoman" || pass != "s3cret":
			writeJSON(w, 401, map[string]string{"error": "invalid_client"})
			return
		}
		claims := map[string]any{"iss": p.srv.URL, "aud": "repoman", "exp": time.Now().Add(time.Hour).Unix(), "nonce": params["nonce"]}
		for k, v := range who {
			claims[k] = v
		}
		if aud != "" {
			claims["aud"] = aud
		}
		if expired {
			claims["exp"] = time.Now().Add(-time.Hour).Unix()
		}
		if badNonce {
			claims["nonce"] = "wrong"
		}
		signer := k
		if forge {
			signer = k2
		}
		writeJSON(w, 200, map[string]any{"access_token": "x", "id_token": signJWT(signer, claims)})
	})
	p.srv = httptest.NewUnstartedServer(mux)
	if addr != "" {
		p.srv.Listener.Close()
		p.srv.Listener, _ = net.Listen("tcp", addr)
	}
	p.srv.Start()
	t.Cleanup(p.srv.Close)
	return p
}

func signJWT(k *rsa.PrivateKey, claims map[string]any) string {
	enc := func(v any) string { b, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(b) }
	in := enc(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"}) + "." + enc(claims)
	h := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, h[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// browser follows redirects with a cookie jar; the last URL tells where a flow ended.
type browser struct{ c *http.Client }

func newBrowser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{&http.Client{Jar: jar}}
}

func (b *browser) get(t *testing.T, u string) (*url.URL, string) {
	t.Helper()
	resp, err := b.c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(strings.Builder)
	b2 := make([]byte, 4096)
	for {
		n, e := resp.Body.Read(b2)
		buf.Write(b2[:n])
		if e != nil {
			break
		}
	}
	return resp.Request.URL, buf.String()
}

func (b *browser) json(t *testing.T, method, u string, body any) (int, map[string]any) {
	t.Helper()
	var rd *strings.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = strings.NewReader(string(j))
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, u, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func (b *browser) me(t *testing.T, base string) string {
	code, m := b.json(t, "GET", base+"/api/auth/me", nil)
	if code != 200 {
		return ""
	}
	return fmt.Sprint(m["username"])
}

func setupOIDC(t *testing.T, e *testEnv, p *fakeIdP, mutate func(*OIDCConfig)) {
	t.Helper()
	cfg := OIDCConfig{Enabled: true, MergeEnabled: false, AutoCreate: true, Issuer: p.srv.URL, ClientID: "repoman", ClientSecret: "s3cret",
		Scopes: "openid email", ButtonLabel: "Keycloak", UsernameClaim: "preferred_username", DefaultRole: "viewer"}
	if mutate != nil {
		mutate(&cfg)
	}
	j, _ := json.Marshal(cfg)
	e.must("save oidc", 200, "PUT", "/api/settings/oidc", strings.NewReader(string(j)), "admin:admin123")
}

func TestOIDCDisabledAndConfig(t *testing.T) {
	e := newEnv(t)
	p := newIdP(t)
	b := newBrowser()
	u, _ := b.get(t, e.srv.URL+"/api/auth/oidc/login")
	if !strings.Contains(u.RawQuery, "oidc_error") {
		t.Fatalf("disabled login should fail, ended at %s", u)
	}
	// a bad issuer must be rejected when enabling
	e.must("bad issuer", 400, "PUT", "/api/settings/oidc", strings.NewReader(`{"enabled":true,"issuer":"http://127.0.0.1:1","clientId":"x"}`), "admin:admin123")
	e.must("anonymous cannot read", 401, "GET", "/api/settings/oidc", nil, "")
	e.must("mk viewer", 204, "POST", "/api/users", strings.NewReader(`{"username":"vic","password":"vicpass12","role":"viewer"}`), "admin:admin123")
	e.must("viewer cannot read", 403, "GET", "/api/settings/oidc", nil, "vic:vicpass12")
	e.must("viewer cannot write", 403, "PUT", "/api/settings/oidc", strings.NewReader(`{}`), "vic:vicpass12")
	setupOIDC(t, e, p, nil)
	body := e.must("get", 200, "GET", "/api/settings/oidc", nil, "admin:admin123")
	if strings.Contains(body, "s3cret") || !strings.Contains(body, `"hasSecret":true`) {
		t.Fatalf("secret leaked or flag missing: %s", body)
	}
	for _, path := range []string{"/api/auth/oidc/config", "/api/settings", "/api/system/export"} {
		creds := "admin:admin123"
		if strings.Contains(e.must("leak "+path, 200, "GET", path, nil, creds), "s3cret") {
			t.Fatalf("%s leaks the client secret", path)
		}
	}
	// saving without a secret keeps the stored one
	e.must("resave", 200, "PUT", "/api/settings/oidc", strings.NewReader(`{"enabled":true,"issuer":"`+p.srv.URL+`","clientId":"repoman","autoCreate":true}`), "admin:admin123")
	if e.a.oidcConfig().ClientSecret != "s3cret" {
		t.Fatal("blank secret should keep the old one")
	}
	if !strings.Contains(e.must("scopes", 200, "GET", "/api/settings/oidc", nil, "admin:admin123"), `"scopes":"openid`) {
		t.Fatal("openid scope must always be present")
	}
}

func TestOIDCAutoCreate(t *testing.T) {
	e := newEnv(t)
	p := newIdP(t)
	setupOIDC(t, e, p, nil)
	p.who = map[string]any{"sub": "u-1", "preferred_username": "admin", "email": "evil@corp.test"} // claims to be "admin"
	b := newBrowser()
	u, _ := b.get(t, e.srv.URL+"/api/auth/oidc/login")
	if u.Path != "/" || u.RawQuery != "" {
		t.Fatalf("flow ended at %s", u)
	}
	if got := b.me(t, e.srv.URL); got != "admin-2" {
		t.Fatalf("SSO user named admin must NOT become local admin; signed in as %q", got)
	}
	code, m := b.json(t, "GET", e.srv.URL+"/api/users", nil)
	if code != 403 {
		t.Fatalf("auto-created user must be viewer, /api/users -> %d %v", code, m)
	}
	// signing in again maps to the same account
	b2 := newBrowser()
	b2.get(t, e.srv.URL+"/api/auth/oidc/login")
	if b2.me(t, e.srv.URL) != "admin-2" {
		t.Fatal("second login should reuse the account")
	}
	// local login of the SSO-only account is impossible, and local admin is untouched
	e.must("sso-only has no password", 401, "GET", "/api/auth/me", nil, "admin-2:admin123")
	e.must("admin still works", 200, "GET", "/api/auth/me", nil, "admin:admin123")
	if p.pkceErr != "" {
		t.Fatal(p.pkceErr)
	}
	// disabled account is refused
	e.must("disable", 204, "PUT", "/api/users/admin-2", strings.NewReader(`{"role":"viewer","disabled":true}`), "admin:admin123")
	b3 := newBrowser()
	u, _ = b3.get(t, e.srv.URL+"/api/auth/oidc/login")
	if !strings.Contains(u.RawQuery, "oidc_error") || b3.me(t, e.srv.URL) != "" {
		t.Fatalf("disabled user must not sign in: %s", u)
	}
}

func TestOIDCRejectsBadTokens(t *testing.T) {
	e := newEnv(t)
	p := newIdP(t)
	setupOIDC(t, e, p, nil)
	for name, set := range map[string]func(){
		"forged signature": func() { p.forge = true },
		"wrong audience":   func() { p.aud = "someone-else" },
		"expired":          func() { p.expired = true },
		"nonce mismatch":   func() { p.badNonce = true },
	} {
		p.forge, p.aud, p.expired, p.badNonce = false, "", false, false
		p.who = map[string]any{"sub": "bad", "preferred_username": "mallory"}
		set()
		b := newBrowser()
		u, _ := b.get(t, e.srv.URL+"/api/auth/oidc/login")
		if !strings.Contains(u.RawQuery, "oidc_error") || b.me(t, e.srv.URL) != "" {
			t.Fatalf("%s: token accepted (%s)", name, u)
		}
	}
	var n int
	e.a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username LIKE 'mallory%'`).Scan(&n)
	if n != 0 {
		t.Fatal("a rejected token created a user")
	}
	// callback without the browser cookie (login CSRF) and with a made-up state
	p.forge, p.aud, p.expired, p.badNonce = false, "", false, false
	p.who = map[string]any{"sub": "ok", "preferred_username": "victim"}
	attacker := newBrowser()
	var cb string
	attacker.c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if strings.HasPrefix(req.URL.Path, "/api/auth/oidc/callback") {
			cb = req.URL.String()
			return http.ErrUseLastResponse
		}
		return nil
	}
	attacker.get(t, e.srv.URL+"/api/auth/oidc/login")
	if cb == "" {
		t.Fatal("no callback captured")
	}
	victim := newBrowser()
	u, _ := victim.get(t, cb) // victim's browser has no rm_oidc cookie
	if !strings.Contains(u.RawQuery, "oidc_error") || victim.me(t, e.srv.URL) != "" {
		t.Fatalf("login CSRF succeeded: %s", u)
	}
	u, _ = newBrowser().get(t, e.srv.URL+"/api/auth/oidc/callback?code=x&state=nope")
	if !strings.Contains(u.RawQuery, "oidc_error") {
		t.Fatal("unknown state accepted")
	}
}

func TestOIDCMergeAtLogin(t *testing.T) {
	e := newEnv(t)
	p := newIdP(t)
	setupOIDC(t, e, p, func(c *OIDCConfig) { c.MergeEnabled = true })
	e.must("create dev", 204, "POST", "/api/users", strings.NewReader(`{"username":"dana","password":"danapass1","role":"developer"}`), "admin:admin123")
	p.who = map[string]any{"sub": "kc-dana", "preferred_username": "dana", "email": "dana@corp.test"}

	b := newBrowser()
	u, _ := b.get(t, e.srv.URL+"/api/auth/oidc/login")
	if u.RawQuery != "oidc=choose" || b.me(t, e.srv.URL) != "" {
		t.Fatalf("unknown identity with merge on must show the chooser, got %s", u)
	}
	code, info := b.json(t, "GET", e.srv.URL+"/api/auth/oidc/pending", nil)
	if code != 200 || info["canLink"] != true {
		t.Fatalf("pending: %d %v", code, info)
	}
	// same username claim is NOT enough: wrong password -> rejected, nothing linked
	code, _ = b.json(t, "POST", e.srv.URL+"/api/auth/oidc/complete", map[string]string{"action": "link", "username": "dana", "password": "nope-nope"})
	if code != 401 {
		t.Fatalf("wrong password -> %d", code)
	}
	code, m := b.json(t, "POST", e.srv.URL+"/api/auth/oidc/complete", map[string]string{"action": "link", "username": "dana", "password": "danapass1"})
	if code != 200 || b.me(t, e.srv.URL) != "dana" {
		t.Fatalf("link failed: %d %v", code, m)
	}
	// pending is single-use
	if code, _ = b.json(t, "POST", e.srv.URL+"/api/auth/oidc/complete", map[string]string{"action": "create"}); code != 404 {
		t.Fatalf("pending reusable: %d", code)
	}
	// next time: straight in, with dana's developer role
	b2 := newBrowser()
	u, _ = b2.get(t, e.srv.URL+"/api/auth/oidc/login")
	if u.RawQuery != "" || b2.me(t, e.srv.URL) != "dana" {
		t.Fatalf("linked identity should sign straight in: %s", u)
	}
	// a second SSO identity cannot take over dana's link
	p.who = map[string]any{"sub": "kc-other", "preferred_username": "dana"}
	b3 := newBrowser()
	b3.get(t, e.srv.URL+"/api/auth/oidc/login")
	code, _ = b3.json(t, "POST", e.srv.URL+"/api/auth/oidc/complete", map[string]string{"action": "link", "username": "dana", "password": "danapass1"})
	if code != 409 || b3.me(t, e.srv.URL) != "" {
		t.Fatalf("second identity linked to same account: %d", code)
	}
	// "create new account" path
	code, _ = b3.json(t, "POST", e.srv.URL+"/api/auth/oidc/complete", map[string]string{"action": "create"})
	if code != 200 || b3.me(t, e.srv.URL) != "dana-2" {
		t.Fatalf("create: %d %q", code, b3.me(t, e.srv.URL))
	}
	// turning merge off blocks new links but existing links still sign in
	setupOIDC(t, e, p, func(c *OIDCConfig) { c.MergeEnabled = false })
	b4 := newBrowser()
	p.who = map[string]any{"sub": "kc-dana", "preferred_username": "dana"}
	b4.get(t, e.srv.URL+"/api/auth/oidc/login")
	if b4.me(t, e.srv.URL) != "dana" {
		t.Fatal("existing link must keep working when merge is turned off")
	}
}

func TestOIDCLinkFromAccount(t *testing.T) {
	e := newEnv(t)
	p := newIdP(t)
	e.must("create", 204, "POST", "/api/users", strings.NewReader(`{"username":"erin","password":"erinpass1","role":"viewer"}`), "admin:admin123")
	login := func(b *browser, user, pass string) {
		if code, m := b.json(t, "POST", e.srv.URL+"/api/auth/login", map[string]string{"username": user, "password": pass}); code != 200 {
			t.Fatalf("login %s: %v", user, m)
		}
	}
	// merge disabled -> refused even for a signed-in user
	setupOIDC(t, e, p, nil)
	b := newBrowser()
	login(b, "erin", "erinpass1")
	if code, _ := b.json(t, "POST", e.srv.URL+"/api/auth/oidc/link", nil); code != 403 {
		t.Fatalf("link with merge off -> %d", code)
	}
	setupOIDC(t, e, p, func(c *OIDCConfig) { c.MergeEnabled = true })
	// anonymous cannot start a link
	if code, _ := newBrowser().json(t, "POST", e.srv.URL+"/api/auth/oidc/link", nil); code != 401 {
		t.Fatalf("anonymous link -> %d", code)
	}
	p.who = map[string]any{"sub": "kc-erin", "preferred_username": "erin.kc", "email": "erin@corp.test"}
	code, m := b.json(t, "POST", e.srv.URL+"/api/auth/oidc/link", nil)
	if code != 200 {
		t.Fatalf("link start: %d %v", code, m)
	}
	u, _ := b.get(t, m["url"].(string))
	if u.Path != "/account" || u.RawQuery != "oidc=linked" {
		t.Fatalf("link ended at %s", u)
	}
	_, st := b.json(t, "GET", e.srv.URL+"/api/auth/oidc/identity", nil)
	if st["linked"] != true || st["email"] != "erin@corp.test" {
		t.Fatalf("identity: %v", st)
	}
	// SSO login now lands on erin (not a new account)
	b2 := newBrowser()
	b2.get(t, e.srv.URL+"/api/auth/oidc/login")
	if b2.me(t, e.srv.URL) != "erin" {
		t.Fatalf("SSO login gave %q", b2.me(t, e.srv.URL))
	}
	// a different signed-in user cannot link the same identity
	e.must("create2", 204, "POST", "/api/users", strings.NewReader(`{"username":"finn","password":"finnpass1","role":"viewer"}`), "admin:admin123")
	bf := newBrowser()
	login(bf, "finn", "finnpass1")
	_, m = bf.json(t, "POST", e.srv.URL+"/api/auth/oidc/link", nil)
	u, _ = bf.get(t, m["url"].(string))
	if !strings.Contains(u.RawQuery, "oidc_error") {
		t.Fatalf("identity stolen by another account: %s", u)
	}
	// link flow started by erin but completed in a browser signed in as someone else is refused
	_, m = b.json(t, "POST", e.srv.URL+"/api/auth/oidc/link", nil)
	p.who = map[string]any{"sub": "kc-new", "preferred_username": "x"}
	req := m["url"].(string)
	var cb string
	bf.c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Path == "/api/auth/oidc/callback" {
			cb = r.URL.String()
			return http.ErrUseLastResponse
		}
		return nil
	}
	// the flow cookie belongs to erin's browser, so replaying erin's callback in finn's browser fails
	eb := newBrowser()
	eb.c.Jar = b.c.Jar
	eb.c.CheckRedirect = bf.c.CheckRedirect
	eb.get(t, req)
	if cb == "" {
		t.Fatal("no callback")
	}
	u, _ = bf.get(t, cb)
	if !strings.Contains(u.RawQuery, "oidc_error") {
		t.Fatalf("callback accepted in another user's browser: %s", u)
	}
	// unlink: erin has a password, so it is allowed
	if code, _ := b.json(t, "DELETE", e.srv.URL+"/api/auth/oidc/link", nil); code != 204 && code != 200 {
		t.Fatalf("unlink -> %d", code)
	}
	_, st = b.json(t, "GET", e.srv.URL+"/api/auth/oidc/identity", nil)
	if st["linked"] != false {
		t.Fatal("still linked")
	}
}

func TestOIDCUnlinkNeedsPassword(t *testing.T) {
	e := newEnv(t)
	p := newIdP(t)
	setupOIDC(t, e, p, nil)
	p.who = map[string]any{"sub": "only", "preferred_username": "sso.only"}
	b := newBrowser()
	b.get(t, e.srv.URL+"/api/auth/oidc/login")
	if b.me(t, e.srv.URL) != "sso.only" {
		t.Fatal("not signed in")
	}
	if code, _ := b.json(t, "DELETE", e.srv.URL+"/api/auth/oidc/link", nil); code != 400 {
		t.Fatalf("SSO-only user could unlink themselves into lockout: %d", code)
	}
	// set a first password without knowing a current one, then unlink works and password login works
	if code, m := b.json(t, "PUT", e.srv.URL+"/api/auth/password", map[string]string{"current": "", "new": "brandnew123"}); code != 204 && code != 200 {
		t.Fatalf("set password: %d %v", code, m)
	}
	e.must("pw login", 200, "GET", "/api/auth/me", nil, "sso.only:brandnew123")
	if code, _ := b.json(t, "DELETE", e.srv.URL+"/api/auth/oidc/link", nil); code != 204 && code != 200 {
		t.Fatalf("unlink after password -> %d", code)
	}
	// a password-holder cannot change it with an empty "current"
	if code, _ := b.json(t, "PUT", e.srv.URL+"/api/auth/password", map[string]string{"current": "", "new": "another1234"}); code != 400 {
		t.Fatalf("empty current accepted for a user with a password: %d", code)
	}
}

// TestOIDCDemo serves the app (:18081) with a fake identity provider (:18082) for manual UI checks.
// Run: OIDC_DEMO=1 go test -run OIDCDemo -timeout 30m
func TestOIDCDemo(t *testing.T) {
	if os.Getenv("OIDC_DEMO") == "" {
		t.Skip("manual only")
	}
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a.bootstrap("admin123")
	a.createUser("dana", "danapass1", "dana@corp.test", "developer")
	p := newIdPAt(t, "127.0.0.1:18082")
	p.who = map[string]any{"sub": "kc-dana", "preferred_username": "dana", "email": "dana@corp.test"}
	go http.ListenAndServe("127.0.0.1:18081", a.routes())
	time.Sleep(30 * time.Minute)
}

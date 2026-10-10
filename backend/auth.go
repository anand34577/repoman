package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	roleViewer    = "viewer"    // read everything
	roleDeveloper = "developer" // + deploy/delete artifacts
	roleAdmin     = "admin"     // + manage repositories, users, settings

	sessionCookie = "rm_session"
	sessionTTL    = 7 * 24 * time.Hour
)

var roleRank = map[string]int{roleViewer: 1, roleDeveloper: 2, roleAdmin: 3}

// API token scopes. A token never grants more than its owner's role; a scope narrows it further.
const (
	scopeRead  = "read"  // browse, search and download only
	scopeWrite = "write" // + publish and delete artifacts; no administration, no account changes
	scopeFull  = "full"  // everything the owner can do
)

// scopeRank caps the effective role of a request authenticated with a narrowed token.
var scopeRank = map[string]int{scopeRead: 1, scopeWrite: 2}

type User struct {
	Username  string `json:"username"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Disabled  bool   `json:"disabled"`
	CreatedAt int64  `json:"createdAt"`
	LastLogin int64  `json:"lastLogin"`
	// Scope is set when the request was authenticated with a read or write token (empty = unrestricted).
	Scope string `json:"tokenScope,omitempty"`
}

const userCols = `u.username, u.email, u.role, u.disabled, u.created_at, u.last_login`

func scanUser(s interface{ Scan(...any) error }, extra ...any) (*User, error) {
	u := &User{}
	return u, s.Scan(append([]any{&u.Username, &u.Email, &u.Role, &u.Disabled, &u.CreatedAt, &u.LastLogin}, extra...)...)
}

func (u *User) has(role string) bool {
	if u == nil {
		return false
	}
	rank := roleRank[u.Role]
	if limit, ok := scopeRank[u.Scope]; ok && limit < rank {
		rank = limit
	}
	return rank >= roleRank[role]
}

type cachedAuth struct {
	user *User
	exp  time.Time
}

// authenticate resolves the caller from session cookie, bearer token, or basic auth
// (password or API token as password). Returns nil for anonymous.
func (a *App) authenticate(r *http.Request) *User {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if u := a.userByToken(c.Value); u != nil {
			return u
		}
	}
	h := r.Header.Get("Authorization")
	if tok, ok := strings.CutPrefix(h, "Bearer "); ok {
		return a.userByToken(tok)
	}
	if name, pass, ok := r.BasicAuth(); ok {
		if strings.HasPrefix(pass, "rm_") {
			return a.userByToken(pass)
		}
		return a.checkBasic(r, name, pass)
	}
	return nil
}

// checkBasic caches successful logins briefly: package clients (docker, maven)
// send credentials on every request and bcrypt is deliberately slow.
func (a *App) checkBasic(r *http.Request, name, pass string) *User {
	key := hashToken(name + "\x00" + pass)
	a.authMu.Lock()
	c, ok := a.authCache[key]
	a.authMu.Unlock()
	if ok && time.Now().Before(c.exp) {
		return c.user
	}
	u := a.passwordLogin(r, name, pass)
	if u != nil {
		a.authMu.Lock()
		a.authCache[key] = cachedAuth{u, time.Now().Add(time.Minute)}
		a.authMu.Unlock()
	}
	return u
}

// dummyHash is compared against when the account does not exist, so that unknown and known
// usernames take the same time to reject (no user enumeration by response time).
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte(randomHex(16)), bcrypt.DefaultCost)
	return h
})

func (a *App) verifyPassword(name, pass string) *User {
	var hash string
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+`, u.password_hash FROM users u WHERE username=? AND local_login=1`, name), &hash)
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyHash(), []byte(pass))
		return nil
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)) != nil || u.Disabled {
		return nil
	}
	return u
}

func (a *App) userByToken(tok string) *User {
	h, now := hashToken(tok), time.Now().Unix()
	var scope string
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+`, t.scope FROM tokens t JOIN users u ON u.username=t.username
		WHERE t.hash=? AND (t.expires_at=0 OR t.expires_at>?) AND u.disabled=0`, h, now), &scope)
	if err != nil {
		return nil
	}
	if scope != scopeFull {
		u.Scope = scope
	}
	a.db.Exec(`UPDATE tokens SET last_used=? WHERE hash=? AND last_used<?`, now, h, now-60)
	return u
}

func hashToken(t string) string { s := sha256.Sum256([]byte(t)); return hex.EncodeToString(s[:]) }

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// randomPassword returns a 20-character password that is easy to copy (no look-alike characters).
func randomPassword() string {
	const alphabet = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 20)
	rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func (a *App) createToken(username, name, kind, scope string, ttl time.Duration) (string, error) {
	tok := "rm_" + randomHex(24)
	var exp int64
	if ttl > 0 {
		exp = time.Now().Add(ttl).Unix()
	}
	_, err := a.db.Exec(`INSERT INTO tokens(hash, username, name, kind, scope, created_at, expires_at) VALUES(?,?,?,?,?,?,?)`,
		hashToken(tok), username, name, kind, scope, time.Now().Unix(), exp)
	return tok, err
}

// Repository access levels, from least to most.
const (
	accessNone  = "none"
	accessRead  = "read"
	accessWrite = "write"
)

// access resolves the caller's level on a repository: admins always write; a per-repository
// grant overrides the user's global role; anonymous callers may read non-private repos if enabled.
func (a *App) access(u *User, r *Repo) string {
	if u == nil {
		if a.settings().AnonymousAccess && !r.Private {
			return accessRead
		}
		return accessNone
	}
	lvl := a.userAccess(u, r)
	if u.Scope == scopeRead && lvl == accessWrite { // a read-only token never writes
		lvl = accessRead
	}
	return lvl
}

func (a *App) userAccess(u *User, r *Repo) string {
	if u.Role == roleAdmin {
		return accessWrite
	}
	var level string
	if a.db.QueryRow(`SELECT level FROM repo_perms WHERE repo=? AND username=?`, r.Name, u.Username).Scan(&level) == nil {
		return level
	}
	if u.Role == roleDeveloper {
		return accessWrite
	}
	return accessRead
}

func (a *App) can(u *User, r *Repo, write bool) bool {
	lvl := a.access(u, r)
	return lvl == accessWrite || (!write && lvl == accessRead)
}

// readableRepos lists repository names the caller may read.
func (a *App) readableRepos(u *User) []string {
	repos, _ := a.listRepos()
	out := []string{}
	for _, r := range repos {
		if a.can(u, r, false) {
			out = append(out, r.Name)
		}
	}
	return out
}

// deny answers 401 with a Basic challenge for anonymous callers, else 403.
func deny(w http.ResponseWriter, u *User) {
	if u == nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="RepoMan"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	http.Error(w, "forbidden", http.StatusForbidden)
}

// ---------- users ----------

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._@-]{1,63}$`)

func (a *App) createUser(name, pass, email, role string) error {
	if !usernameRe.MatchString(name) {
		return httpErr{400, "invalid username"}
	}
	if roleRank[role] == 0 {
		return httpErr{400, "role must be viewer, developer or admin"}
	}
	if err := validPassword(pass); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(`INSERT INTO users(username, password_hash, email, role, created_at) VALUES(?,?,?,?,?)`,
		name, string(hash), email, role, time.Now().Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return httpErr{409, "user already exists"}
	}
	return err
}

// validPassword enforces the length limits: at least 8 characters, and at most 72 bytes because
// bcrypt ignores everything after that.
func validPassword(pass string) error {
	if len(pass) < 8 {
		return httpErr{400, "password must be at least 8 characters"}
	}
	if len(pass) > 72 {
		return httpErr{400, "password must be at most 72 bytes"}
	}
	return nil
}

func (a *App) setPassword(name, pass string) error {
	if err := validPassword(pass); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if _, err = a.db.Exec(`UPDATE users SET password_hash=?, local_login=1 WHERE username=?`, string(hash), name); err != nil {
		return err
	}
	a.authMu.Lock()
	a.authCache = map[string]cachedAuth{} // the old password must stop working immediately
	a.authMu.Unlock()
	a.initialPasswordReplaced(name)
	return nil
}

// initialPasswordFile holds the generated first-run admin password until it is changed.
func (a *App) initialPasswordFile() string { return filepath.Join(a.dataDir, "initial-admin-password") }

// initialPasswordReplaced forgets the first-run password once its owner has chosen a new one.
func (a *App) initialPasswordReplaced(name string) {
	var owner string
	if a.getJSON("initial_password", &owner) && owner == name {
		a.db.Exec(`DELETE FROM settings WHERE key='initial_password'`)
		os.Remove(a.initialPasswordFile())
	}
}

// warnLegacyDefaultPassword catches installations created before 1.2, when the admin account
// started out with a well-known password. If it was never changed, say so loudly in the log and
// put the reminder back on the dashboard.
func (a *App) warnLegacyDefaultPassword() {
	if a.getJSON("initial_password", new(string)) || a.verifyPassword("admin", "admin123") == nil {
		return
	}
	a.setJSON("initial_password", "admin")
	log.Printf("WARNING: the admin account still uses the old default password \"admin123\". Sign in and change it now.")
}

// bootstrap creates the initial admin and a sensible default set of repositories on first run.
// With no password given, a random one is generated and written to the data directory: there is
// no well-known default credential to forget about.
func (a *App) bootstrap(adminPass string) error {
	var n int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		a.warnLegacyDefaultPassword()
		return nil
	}
	generated := adminPass == ""
	if generated {
		adminPass = randomPassword()
	}
	if err := a.createUser("admin", adminPass, "", roleAdmin); err != nil {
		return fmt.Errorf("creating the initial admin account (RM_ADMIN_PASSWORD): %w", err)
	}
	a.setJSON("initial_password", "admin")
	if generated {
		file := a.initialPasswordFile()
		if err := os.WriteFile(file, []byte(adminPass+"\n"), 0o600); err != nil {
			return err
		}
		abs, _ := filepath.Abs(file)
		log.Printf("================================================================")
		log.Printf(" First run: created user \"admin\" with password  %s", adminPass)
		log.Printf(" It is also saved in %s", abs)
		log.Printf(" Sign in and change it; the file is deleted when you do.")
		log.Printf("================================================================")
	} else {
		log.Printf("first run: created user \"admin\" with the password from RM_ADMIN_PASSWORD; change it after signing in and remove the variable")
	}
	now := time.Now().Unix()
	defaults := []*Repo{
		{Name: "maven-central", Format: "maven", Type: "proxy", RemoteURL: "https://repo1.maven.org/maven2", MetadataTTL: 1440},
		{Name: "maven-releases", Format: "maven", Type: "hosted"},
		{Name: "maven-snapshots", Format: "maven", Type: "hosted", AllowRedeploy: true, CleanupDays: 90},
		{Name: "maven-public", Format: "maven", Type: "group", Members: []string{"maven-releases", "maven-snapshots", "maven-central"}},
		{Name: "npm-proxy", Format: "npm", Type: "proxy", RemoteURL: "https://registry.npmjs.org", MetadataTTL: 60},
		{Name: "npm-hosted", Format: "npm", Type: "hosted"},
		{Name: "npm-public", Format: "npm", Type: "group", Members: []string{"npm-hosted", "npm-proxy"}},
		{Name: "pypi-proxy", Format: "pypi", Type: "proxy", RemoteURL: "https://pypi.org", MetadataTTL: 60},
		{Name: "pypi-hosted", Format: "pypi", Type: "hosted"},
		{Name: "pypi-public", Format: "pypi", Type: "group", Members: []string{"pypi-hosted", "pypi-proxy"}},
		{Name: "docker-hub", Format: "docker", Type: "proxy", RemoteURL: "https://registry-1.docker.io", MetadataTTL: 60},
		{Name: "docker-hosted", Format: "docker", Type: "hosted", AllowRedeploy: true},
		{Name: "go-proxy", Format: "go", Type: "proxy", RemoteURL: "https://proxy.golang.org", MetadataTTL: 60},
		{Name: "go-hosted", Format: "go", Type: "hosted"},
		{Name: "go-public", Format: "go", Type: "group", Members: []string{"go-hosted", "go-proxy"}},
		{Name: "apt-hosted", Format: "apt", Type: "hosted", Distribution: "stable"},
		{Name: "yum-hosted", Format: "yum", Type: "hosted"},
		{Name: "conda-forge", Format: "conda", Type: "proxy", RemoteURL: "https://conda.anaconda.org/conda-forge", MetadataTTL: 60},
		{Name: "conda-hosted", Format: "conda", Type: "hosted"},
		{Name: "raw-hosted", Format: "raw", Type: "hosted", AllowRedeploy: true},
	}
	for _, r := range defaults {
		r.CreatedAt = now
		if err := a.saveRepo(r); err != nil {
			return err
		}
	}
	return nil
}

// ---------- brute-force protection ----------

type loginFails struct {
	n     int
	first time.Time
}

// ponytail: in-memory per-IP counter; resets on restart, fine for a single instance
func (a *App) lockedOut(ip string) bool {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	f, ok := a.fails[ip]
	return ok && f.n >= 10 && time.Since(f.first) < 15*time.Minute
}

// passwordLogin verifies credentials sent by a client with per-IP lockout. Every password
// check from the network (web sign-in, basic auth, npm login) goes through here.
func (a *App) passwordLogin(r *http.Request, name, pass string) *User {
	ip := a.clientIP(r)
	if a.lockedOut(ip) {
		return nil
	}
	u := a.verifyPassword(name, pass)
	if u == nil && a.loginFailed(ip) {
		a.audit(&User{Username: name}, "login.lockout", ip, "10 failed sign-in attempts; blocked for 15 minutes")
	}
	return u
}

// loginFailed counts a failure and reports whether this one triggered the lockout.
func (a *App) loginFailed(ip string) bool {
	a.authMu.Lock()
	defer a.authMu.Unlock()
	f := a.fails[ip]
	if time.Since(f.first) > 15*time.Minute {
		f = loginFails{first: time.Now()}
	}
	f.n++
	a.fails[ip] = f
	return f.n == 10
}

// ---------- session endpoints ----------

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeJSON(w, 400, errBody("invalid request"))
		return
	}
	ip := a.clientIP(r)
	if a.lockedOut(ip) {
		writeJSON(w, 429, errBody("too many failed sign-in attempts; try again in 15 minutes"))
		return
	}
	u := a.passwordLogin(r, body.Username, body.Password)
	if u == nil {
		a.audit(&User{Username: body.Username}, "login.failed", body.Username, "from", ip)
		writeJSON(w, 401, errBody("invalid username or password, or account disabled"))
		return
	}
	if err := a.startSession(w, r, u); err != nil {
		writeJSON(w, 500, errBody(err.Error()))
		return
	}
	a.audit(u, "login", u.Username)
	writeJSON(w, 200, u)
}

// startSession records the sign-in and sets the web session cookie.
func (a *App) startSession(w http.ResponseWriter, r *http.Request, u *User) error {
	a.db.Exec(`UPDATE users SET last_login=? WHERE username=?`, time.Now().Unix(), u.Username)
	tok, err := a.createToken(u.Username, "web session", "session", scopeFull, sessionTTL)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: a.isHTTPS(r), MaxAge: int(sessionTTL.Seconds())})
	return nil
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.db.Exec(`DELETE FROM tokens WHERE hash=?`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(204)
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var body struct{ Current, New string }
	if err := readJSON(r, &body); err != nil {
		return nil, err
	}
	if a.hasLocalLogin(u.Username) && a.verifyPassword(u.Username, body.Current) == nil {
		return nil, httpErr{400, "current password is incorrect"}
	}
	if err := a.setPassword(u.Username, body.New); err != nil {
		return nil, err
	}
	// sign out every other browser; this one stays signed in
	keep := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		keep = hashToken(c.Value)
	}
	a.db.Exec(`DELETE FROM tokens WHERE username=? AND kind='session' AND hash<>?`, u.Username, keep)
	a.audit(u, "password.change", u.Username)
	return nil, nil
}

// ---------- JSON API plumbing ----------

type httpErr struct {
	code int
	msg  string
}

func (e httpErr) Error() string { return e.msg }

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// readJSON decodes a request body of at most 1 MB (management requests are small).
func readJSON(r *http.Request, v any) error { return readJSONLimit(r, v, 1<<20) }

func readJSONLimit(r *http.Request, v any, limit int64) error {
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit)).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return httpErr{413, "request body too large"}
		}
		return httpErr{400, "invalid JSON body: " + err.Error()}
	}
	return nil
}

// fullScope reserves account-level actions (password, tokens, SSO links) for browser sessions and
// full-access tokens, so a leaked read or write token cannot mint itself more access.
func fullScope(f apiFunc) apiFunc {
	return func(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
		if u.Scope != "" {
			return nil, httpErr{403, "this action needs a full-access token or a browser session"}
		}
		return f(w, r, u)
	}
}

type apiFunc func(w http.ResponseWriter, r *http.Request, u *User) (any, error)

// alreadyWritten is returned by handlers that wrote their own response body (CSV exports).
type alreadyWritten struct{}

var written = alreadyWritten{}

// api wraps a JSON handler with authentication and a minimum role.
func (a *App) api(role string, f apiFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := a.authenticate(r)
		if u == nil {
			writeJSON(w, 401, errBody("authentication required"))
			return
		}
		if u.Scope == scopeRead && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeJSON(w, 403, errBody("this API token is read-only"))
			return
		}
		if !u.has(role) {
			msg := fmt.Sprintf("requires %s role", role)
			if u.Scope != "" && roleRank[u.Role] >= roleRank[role] {
				msg += "; this API token's scope (" + u.Scope + ") does not allow it"
			}
			writeJSON(w, 403, errBody(msg))
			return
		}
		res, err := f(w, r, u)
		var he httpErr
		switch {
		case errors.As(err, &he):
			writeJSON(w, he.code, errBody(he.msg))
		case errors.Is(err, errNotFound), errors.Is(err, sql.ErrNoRows):
			writeJSON(w, 404, errBody("not found"))
		case err != nil:
			log.Printf("api %s %s: %v", r.Method, r.URL.Path, err)
			writeJSON(w, 500, errBody(err.Error()))
		case res == written:
		case res == nil:
			w.WriteHeader(204)
		default:
			writeJSON(w, 200, res)
		}
	}
}

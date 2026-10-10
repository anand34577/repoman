package main

// OpenID Connect sign-in (authorization code flow + PKCE) for Keycloak, Entra ID, Okta, Authentik, Google, etc.
// Standard library only: discovery, JWKS and ID-token signature checks are implemented here.
//
// Accounts are never matched by name or e-mail. An OIDC identity (issuer + subject) reaches an existing
// local account only when its owner proves it: by starting the flow while signed in (Account page), or by
// entering the local password right after the first SSO sign-in. Both require "merge" to be enabled.

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type OIDCConfig struct {
	Enabled       bool   `json:"enabled"`
	MergeEnabled  bool   `json:"mergeEnabled"` // let existing users link an SSO identity to their account
	AutoCreate    bool   `json:"autoCreate"`   // create a new account for unknown identities
	Issuer        string `json:"issuer"`       // e.g. https://keycloak.example.com/realms/corp
	ClientID      string `json:"clientId"`
	ClientSecret  string `json:"clientSecret,omitempty"` // never returned by the API
	HasSecret     bool   `json:"hasSecret"`              // output only
	Scopes        string `json:"scopes"`
	ButtonLabel   string `json:"buttonLabel"`
	UsernameClaim string `json:"usernameClaim"`
	DefaultRole   string `json:"defaultRole"` // role of auto-created accounts
}

func (a *App) oidcConfig() OIDCConfig {
	c := OIDCConfig{AutoCreate: true, Scopes: "openid profile email", ButtonLabel: "SSO", UsernameClaim: "preferred_username", DefaultRole: roleViewer}
	a.getJSON("oidc", &c)
	return c
}

type oidcMeta struct {
	Issuer  string   `json:"issuer"`
	AuthURL string   `json:"authorization_endpoint"`
	Token   string   `json:"token_endpoint"`
	JWKSURI string   `json:"jwks_uri"`
	Methods []string `json:"token_endpoint_auth_methods_supported"`
}

type oidcFlow struct {
	verifier, nonce, redirectURI, mode, username string
	exp                                          time.Time
}

// oidcIdentity is what we keep from a verified ID token.
type oidcIdentity struct{ Issuer, Subject, Email, Username string }

type oidcPending struct {
	id  oidcIdentity
	exp time.Time
}

// ponytail: flows, pending sign-ins and key caches live in memory (single instance); restart just aborts logins in flight.
type oidcState struct {
	mu      sync.Mutex
	flows   map[string]*oidcFlow
	pending map[string]*oidcPending
	meta    *oidcMeta
	metaAt  time.Time
	keys    map[string]any // kid -> *rsa.PublicKey | *ecdsa.PublicKey
	keysAt  time.Time
}

const (
	oidcFlowCookie    = "rm_oidc"
	oidcPendingCookie = "rm_oidc_pending"
	oidcFlowTTL       = 10 * time.Minute
)

func (s *oidcState) reset() {
	s.mu.Lock()
	s.meta, s.keys = nil, nil
	s.mu.Unlock()
}

func (a *App) oidcGet(ctx context.Context, u string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s returned HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

func validIssuer(iss string) error {
	u, err := url.Parse(iss)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("issuer must be an absolute URL such as https://keycloak.example.com/realms/corp")
	}
	return nil
}

// discover returns (cached) provider metadata for the configured issuer.
func (a *App) discover(ctx context.Context, issuer string) (*oidcMeta, error) {
	s := &a.oidc
	s.mu.Lock()
	if s.meta != nil && s.meta.Issuer == issuer && time.Since(s.metaAt) < time.Hour {
		m := s.meta
		s.mu.Unlock()
		return m, nil
	}
	s.mu.Unlock()
	if err := validIssuer(issuer); err != nil {
		return nil, err
	}
	var m oidcMeta
	if err := a.oidcGet(ctx, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", &m); err != nil {
		return nil, fmt.Errorf("discovery failed: %w", err)
	}
	if strings.TrimRight(m.Issuer, "/") != strings.TrimRight(issuer, "/") {
		return nil, fmt.Errorf("provider reports issuer %q, which does not match the configured issuer", m.Issuer)
	}
	if m.AuthURL == "" || m.Token == "" || m.JWKSURI == "" {
		return nil, errors.New("discovery document lacks authorization, token or jwks endpoints")
	}
	s.mu.Lock()
	s.meta, s.metaAt, s.keys = &m, time.Now(), nil
	s.mu.Unlock()
	return &m, nil
}

type jwk struct {
	Kty, Kid, Crv, N, E, X, Y, Use string
}

func b64(s string) []byte {
	b, _ := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	return b
}

func (a *App) refreshKeys(ctx context.Context, m *oidcMeta) error {
	var set struct{ Keys []jwk }
	if err := a.oidcGet(ctx, m.JWKSURI, &set); err != nil {
		return fmt.Errorf("fetching signing keys: %w", err)
	}
	keys := map[string]any{}
	for _, k := range set.Keys {
		if k.Use == "enc" {
			continue
		}
		switch k.Kty {
		case "RSA":
			n := new(big.Int).SetBytes(b64(k.N))
			e := new(big.Int).SetBytes(b64(k.E))
			if n.BitLen() >= 2048 && e.IsInt64() {
				keys[k.Kid] = &rsa.PublicKey{N: n, E: int(e.Int64())}
			}
		case "EC":
			var c elliptic.Curve
			switch k.Crv {
			case "P-256":
				c = elliptic.P256()
			case "P-384":
				c = elliptic.P384()
			}
			x, y := new(big.Int).SetBytes(b64(k.X)), new(big.Int).SetBytes(b64(k.Y))
			if c != nil && c.IsOnCurve(x, y) {
				keys[k.Kid] = &ecdsa.PublicKey{Curve: c, X: x, Y: y}
			}
		}
	}
	a.oidc.mu.Lock()
	a.oidc.keys, a.oidc.keysAt = keys, time.Now()
	a.oidc.mu.Unlock()
	return nil
}

func (a *App) jwtKey(ctx context.Context, m *oidcMeta, kid string) (any, error) {
	for attempt := 0; attempt < 2; attempt++ {
		a.oidc.mu.Lock()
		keys, at := a.oidc.keys, a.oidc.keysAt
		k, ok := keys[kid]
		if !ok && kid == "" && len(keys) == 1 {
			for _, only := range keys {
				k, ok = only, true
			}
		}
		a.oidc.mu.Unlock()
		if ok {
			return k, nil
		}
		if keys != nil && time.Since(at) < 30*time.Second { // don't let unknown kids hammer the provider
			break
		}
		if err := a.refreshKeys(ctx, m); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("ID token is signed with an unknown key")
}

// verifyIDToken checks signature (RS*/PS*/ES* only), issuer, audience, expiry and nonce.
func (a *App) verifyIDToken(ctx context.Context, cfg OIDCConfig, m *oidcMeta, raw, nonce string) (map[string]any, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed ID token")
	}
	var hdr struct{ Alg, Kid string }
	if json.Unmarshal(b64(parts[0]), &hdr) != nil {
		return nil, errors.New("malformed ID token header")
	}
	key, err := a.jwtKey(ctx, m, hdr.Kid)
	if err != nil {
		return nil, err
	}
	sig := b64(parts[2])
	signed := []byte(parts[0] + "." + parts[1])
	hashes := map[string]crypto.Hash{"256": crypto.SHA256, "384": crypto.SHA384, "512": crypto.SHA512}
	h, ok := hashes[strings.TrimLeft(hdr.Alg, "RSPE")]
	if !ok || len(hdr.Alg) != 5 {
		return nil, fmt.Errorf("unsupported ID token algorithm %q", hdr.Alg)
	}
	hh := h.New()
	hh.Write(signed)
	sum := hh.Sum(nil)
	switch pub := key.(type) {
	case *rsa.PublicKey:
		switch hdr.Alg[:2] {
		case "RS":
			err = rsa.VerifyPKCS1v15(pub, h, sum, sig)
		case "PS":
			err = rsa.VerifyPSS(pub, h, sum, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		default:
			err = errors.New("algorithm does not match key type")
		}
	case *ecdsa.PublicKey:
		size := (pub.Curve.Params().BitSize + 7) / 8
		if hdr.Alg[:2] != "ES" || len(sig) != 2*size || hdr.Alg[2:] != fmt.Sprint(pub.Curve.Params().BitSize) {
			err = errors.New("algorithm does not match key type")
		} else if !ecdsa.Verify(pub, sum, new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])) {
			err = errors.New("bad signature")
		}
	}
	if err != nil {
		return nil, errors.New("ID token signature is invalid")
	}
	var c map[string]any
	if json.Unmarshal(b64(parts[1]), &c) != nil {
		return nil, errors.New("malformed ID token payload")
	}
	if iss, _ := c["iss"].(string); iss != m.Issuer {
		return nil, errors.New("ID token issuer mismatch")
	}
	aud := map[string]bool{}
	switch v := c["aud"].(type) {
	case string:
		aud[v] = true
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				aud[s] = true
			}
		}
	}
	if !aud[cfg.ClientID] {
		return nil, errors.New("ID token was not issued for this client")
	}
	if azp, _ := c["azp"].(string); len(aud) > 1 && azp != cfg.ClientID {
		return nil, errors.New("ID token authorized party mismatch")
	}
	now := float64(time.Now().Unix())
	if exp, ok := c["exp"].(float64); !ok || now > exp+120 {
		return nil, errors.New("ID token has expired")
	}
	if nbf, ok := c["nbf"].(float64); ok && now < nbf-120 {
		return nil, errors.New("ID token is not valid yet")
	}
	if n, _ := c["nonce"].(string); subtle.ConstantTimeCompare([]byte(n), []byte(nonce)) != 1 {
		return nil, errors.New("ID token nonce mismatch")
	}
	if sub, _ := c["sub"].(string); sub == "" {
		return nil, errors.New("ID token has no subject")
	}
	return c, nil
}

// ---------- flow ----------

func pkceChallenge(verifier string) string {
	s := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(s[:])
}

func (a *App) callbackURL(r *http.Request) string { return a.baseURL(r) + "/api/auth/oidc/callback" }

func (a *App) oidcEnabled() (OIDCConfig, error) {
	c := a.oidcConfig()
	if !c.Enabled {
		return c, httpErr{404, "single sign-on is not enabled"}
	}
	return c, nil
}

// startFlow registers a flow and returns the provider URL to send the browser to.
func (a *App) startFlow(w http.ResponseWriter, r *http.Request, cfg OIDCConfig, mode, username string) (string, error) {
	m, err := a.discover(r.Context(), cfg.Issuer)
	if err != nil {
		return "", httpErr{502, err.Error()}
	}
	state := randomHex(16)
	f := &oidcFlow{verifier: randomHex(32), nonce: randomHex(16), redirectURI: a.callbackURL(r), mode: mode, username: username, exp: time.Now().Add(oidcFlowTTL)}
	s := &a.oidc
	s.mu.Lock()
	if s.flows == nil {
		s.flows, s.pending = map[string]*oidcFlow{}, map[string]*oidcPending{}
	}
	for k, v := range s.flows { // drop abandoned flows
		if time.Now().After(v.exp) {
			delete(s.flows, k)
		}
	}
	if len(s.flows) > 2000 {
		s.mu.Unlock()
		return "", httpErr{429, "too many sign-ins in progress"}
	}
	s.flows[state] = f
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: oidcFlowCookie, Value: state, Path: "/api/auth/oidc", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(f.redirectURI, "https:"), MaxAge: int(oidcFlowTTL.Seconds())})
	au, _ := url.Parse(m.AuthURL)
	q := au.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", f.redirectURI)
	q.Set("scope", cfg.Scopes)
	q.Set("state", state)
	q.Set("nonce", f.nonce)
	q.Set("code_challenge", pkceChallenge(f.verifier))
	q.Set("code_challenge_method", "S256")
	au.RawQuery = q.Encode()
	return au.String(), nil
}

func (a *App) oidcConfigPublic(w http.ResponseWriter, r *http.Request) {
	c := a.oidcConfig()
	writeJSON(w, 200, map[string]any{"enabled": c.Enabled, "mergeEnabled": c.Enabled && c.MergeEnabled, "label": c.ButtonLabel})
}

func (a *App) oidcLogin(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.oidcEnabled()
	if err == nil {
		var dest string
		if dest, err = a.startFlow(w, r, cfg, "login", ""); err == nil {
			http.Redirect(w, r, dest, http.StatusFound)
			return
		}
	}
	oidcFail(w, r, err.Error())
}

// oidcLinkStart (signed-in user): returns the provider URL. POST + JSON keeps it out of reach of cross-site requests.
func (a *App) oidcLinkStart(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	cfg, err := a.oidcEnabled()
	if err != nil {
		return nil, err
	}
	if !cfg.MergeEnabled {
		return nil, httpErr{403, "linking SSO accounts is disabled by the administrator"}
	}
	dest, err := a.startFlow(w, r, cfg, "link", u.Username)
	if err != nil {
		return nil, err
	}
	return map[string]string{"url": dest}, nil
}

func oidcFail(w http.ResponseWriter, r *http.Request, msg string) {
	http.SetCookie(w, &http.Cookie{Name: oidcFlowCookie, Path: "/api/auth/oidc", MaxAge: -1})
	http.Redirect(w, r, "/?oidc_error="+url.QueryEscape(msg), http.StatusFound)
}

var safeProviderErr = regexp.MustCompile(`^[a-z_]{1,40}$`)

func (a *App) oidcCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")
	c, cerr := r.Cookie(oidcFlowCookie)
	s := &a.oidc
	s.mu.Lock()
	f := s.flows[state]
	delete(s.flows, state) // one-time use
	s.mu.Unlock()
	if cerr != nil || state == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(state)) != 1 || f == nil || time.Now().After(f.exp) {
		oidcFail(w, r, "Sign-in session expired or was not started in this browser. Please try again.")
		return
	}
	cfg, err := a.oidcEnabled()
	if err != nil {
		oidcFail(w, r, err.Error())
		return
	}
	if e := q.Get("error"); e != "" {
		if !safeProviderErr.MatchString(e) {
			e = "error"
		}
		a.audit(nil, "oidc.login.failed", "provider", e)
		oidcFail(w, r, "The identity provider refused the sign-in ("+e+").")
		return
	}
	id, err := a.exchange(r.Context(), cfg, f, q.Get("code"))
	if err != nil {
		a.audit(nil, "oidc.login.failed", cfg.Issuer, err.Error())
		oidcFail(w, r, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcFlowCookie, Path: "/api/auth/oidc", MaxAge: -1})

	if f.mode == "link" {
		cur := a.authenticate(r)
		if cur == nil || cur.Username != f.username {
			oidcFail(w, r, "You must stay signed in as the same user to link an SSO account.")
			return
		}
		if !cfg.MergeEnabled {
			oidcFail(w, r, "Linking SSO accounts is disabled by the administrator.")
			return
		}
		if err := a.linkIdentity(cur.Username, id); err != nil {
			oidcFail(w, r, err.Error())
			return
		}
		a.audit(cur, "oidc.link", cur.Username, "subject", id.Subject)
		http.Redirect(w, r, "/account?oidc=linked", http.StatusFound)
		return
	}

	var name string
	if a.db.QueryRow(`SELECT username FROM oidc_identities WHERE issuer=? AND subject=?`, id.Issuer, id.Subject).Scan(&name) == nil {
		a.oidcSignIn(w, r, name, id)
		return
	}
	switch {
	case cfg.MergeEnabled: // let the person choose: link to an existing account (password) or create one
		pid := randomHex(24)
		s.mu.Lock()
		for k, v := range s.pending {
			if time.Now().After(v.exp) {
				delete(s.pending, k)
			}
		}
		s.pending[pid] = &oidcPending{id, time.Now().Add(oidcFlowTTL)}
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: oidcPendingCookie, Value: pid, Path: "/api/auth/oidc", HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: a.isHTTPS(r) || strings.HasPrefix(f.redirectURI, "https:"), MaxAge: int(oidcFlowTTL.Seconds())})
		http.Redirect(w, r, "/?oidc=choose", http.StatusFound)
	case cfg.AutoCreate:
		a.oidcCreate(w, r, cfg, id)
	default:
		a.audit(nil, "oidc.login.failed", id.Subject, "no linked account")
		oidcFail(w, r, "No account is linked to this SSO identity. Ask an administrator for access.")
	}
}

// exchange trades the authorization code for tokens and returns the verified identity.
func (a *App) exchange(ctx context.Context, cfg OIDCConfig, f *oidcFlow, code string) (oidcIdentity, error) {
	var zero oidcIdentity
	if code == "" {
		return zero, errors.New("the identity provider returned no authorization code")
	}
	m, err := a.discover(ctx, cfg.Issuer)
	if err != nil {
		return zero, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {f.redirectURI}, "code_verifier": {f.verifier}}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	basic := cfg.ClientSecret != "" && (len(m.Methods) == 0 || contains(m.Methods, "client_secret_basic"))
	if !basic {
		form.Set("client_id", cfg.ClientID)
		if cfg.ClientSecret != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, m.Token, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if basic {
		req.SetBasicAuth(url.QueryEscape(cfg.ClientID), url.QueryEscape(cfg.ClientSecret))
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return zero, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()
	var tok struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok)
	if resp.StatusCode != 200 || tok.IDToken == "" {
		if tok.Error != "" && safeProviderErr.MatchString(tok.Error) {
			return zero, fmt.Errorf("token request rejected by the provider (%s)", tok.Error)
		}
		return zero, fmt.Errorf("token request rejected by the provider (HTTP %d)", resp.StatusCode)
	}
	claims, err := a.verifyIDToken(ctx, cfg, m, tok.IDToken, f.nonce)
	if err != nil {
		return zero, err
	}
	str := func(k string) string { s, _ := claims[k].(string); return strings.TrimSpace(s) }
	id := oidcIdentity{Issuer: m.Issuer, Subject: str("sub"), Email: str("email")}
	for _, k := range []string{cfg.UsernameClaim, "preferred_username", "email"} {
		if id.Username = str(k); id.Username != "" {
			break
		}
	}
	if i := strings.IndexByte(id.Username, '@'); i > 0 && strings.Contains(id.Username, "@") && id.Username == id.Email {
		id.Username = id.Username[:i]
	}
	return id, nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

var badNameChars = regexp.MustCompile(`[^a-zA-Z0-9._@-]+`)

func (a *App) oidcSignIn(w http.ResponseWriter, r *http.Request, username string, id oidcIdentity) {
	var disabled bool
	if a.db.QueryRow(`SELECT disabled FROM users WHERE username=?`, username).Scan(&disabled) != nil || disabled {
		a.audit(&User{Username: username}, "oidc.login.failed", username, "account disabled")
		oidcFail(w, r, "This account is disabled.")
		return
	}
	u, err := scanUser(a.db.QueryRow(`SELECT `+userCols+` FROM users u WHERE username=?`, username))
	if err != nil {
		oidcFail(w, r, "Sign-in failed.")
		return
	}
	if err := a.startSession(w, r, u); err != nil {
		oidcFail(w, r, "Sign-in failed.")
		return
	}
	a.db.Exec(`UPDATE oidc_identities SET email=? WHERE issuer=? AND subject=?`, id.Email, id.Issuer, id.Subject)
	a.audit(u, "oidc.login", u.Username)
	http.Redirect(w, r, "/", http.StatusFound)
}

// ---------- accounts ----------

// freeUsername turns a claim into a valid, unused local username; it never reuses an existing one.
func (a *App) freeUsername(id oidcIdentity) string {
	base := strings.Trim(badNameChars.ReplaceAllString(id.Username, "_"), "._@-")
	if len(base) > 56 {
		base = base[:56]
	}
	if len(base) < 2 {
		base = strings.Trim(badNameChars.ReplaceAllString("sso-"+id.Subject[:min(8, len(id.Subject))], "_"), "._@-")
	}
	cand := base
	for i := 2; ; i++ {
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username=?`, cand).Scan(&n)
		if n == 0 && usernameRe.MatchString(cand) {
			return cand
		}
		cand = fmt.Sprintf("%s-%d", base, i)
		if i > 99 {
			cand = base + "-" + randomHex(3)
		}
	}
}

// createOIDCUser makes a new account bound to the identity. It has no usable password until the user sets one.
func (a *App) createOIDCUser(id oidcIdentity, role string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(randomHex(16)), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	name := a.freeUsername(id)
	now := time.Now().Unix()
	tx, err := a.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO users(username, password_hash, email, role, created_at, local_login) VALUES(?,?,?,?,?,0)`, name, string(hash), id.Email, role, now); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO oidc_identities(issuer, subject, username, email, created_at) VALUES(?,?,?,?,?)`, id.Issuer, id.Subject, name, id.Email, now); err != nil {
		return nil, httpErr{409, "this SSO identity is already linked to an account"}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return scanUser(a.db.QueryRow(`SELECT `+userCols+` FROM users u WHERE username=?`, name))
}

func (a *App) newUserRole(cfg OIDCConfig) string {
	if roleRank[cfg.DefaultRole] == 0 {
		return roleViewer
	}
	return cfg.DefaultRole
}

func (a *App) oidcCreate(w http.ResponseWriter, r *http.Request, cfg OIDCConfig, id oidcIdentity) {
	role := a.newUserRole(cfg)
	u, err := a.createOIDCUser(id, role)
	if err == nil {
		err = a.startSession(w, r, u)
	}
	if err != nil {
		oidcFail(w, r, "Could not create your account: "+err.Error())
		return
	}
	a.audit(u, "user.create", u.Username, role, "via sso")
	a.audit(u, "oidc.login", u.Username)
	http.Redirect(w, r, "/", http.StatusFound)
}

// linkIdentity binds the identity to username. Fails if either side is already bound to something else.
func (a *App) linkIdentity(username string, id oidcIdentity) error {
	var owner string
	if a.db.QueryRow(`SELECT username FROM oidc_identities WHERE issuer=? AND subject=?`, id.Issuer, id.Subject).Scan(&owner) == nil {
		if owner == username {
			return nil
		}
		return httpErr{409, "this SSO identity is already linked to another account"}
	}
	a.db.Exec(`DELETE FROM oidc_identities WHERE username=? AND issuer<>?`, username, id.Issuer) // identity of a previous provider
	_, err := a.db.Exec(`INSERT INTO oidc_identities(issuer, subject, username, email, created_at) VALUES(?,?,?,?,?)`,
		id.Issuer, id.Subject, username, id.Email, time.Now().Unix())
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return httpErr{409, "that account is already linked to a different SSO identity; unlink it first"}
	}
	return err
}

// ---------- chooser after the first SSO sign-in (merge enabled) ----------

func (a *App) takePending(r *http.Request, consume bool) *oidcPending {
	c, err := r.Cookie(oidcPendingCookie)
	if err != nil {
		return nil
	}
	s := &a.oidc
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending[c.Value]
	if p == nil || time.Now().After(p.exp) {
		return nil
	}
	if consume {
		delete(s.pending, c.Value)
	}
	return p
}

func (a *App) oidcPendingInfo(w http.ResponseWriter, r *http.Request) {
	cfg := a.oidcConfig()
	p := a.takePending(r, false)
	if p == nil || !cfg.Enabled {
		writeJSON(w, 404, errBody("no sign-in in progress"))
		return
	}
	writeJSON(w, 200, map[string]any{"email": p.id.Email, "username": p.id.Username, "canLink": cfg.MergeEnabled, "canCreate": cfg.AutoCreate})
}

func (a *App) oidcComplete(w http.ResponseWriter, r *http.Request) {
	var body struct{ Action, Username, Password string }
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, 400, errBody("invalid request"))
		return
	}
	cfg := a.oidcConfig()
	p := a.takePending(r, false)
	if p == nil || !cfg.Enabled {
		writeJSON(w, 404, errBody("this sign-in expired; start again"))
		return
	}
	if a.lockedOut(a.clientIP(r)) {
		writeJSON(w, 429, errBody("too many failed sign-in attempts; try again in 15 minutes"))
		return
	}
	var u *User
	var err error
	switch body.Action {
	case "link":
		if !cfg.MergeEnabled {
			writeJSON(w, 403, errBody("linking SSO accounts is disabled by the administrator"))
			return
		}
		if u = a.passwordLogin(r, body.Username, body.Password); u == nil {
			a.audit(&User{Username: body.Username}, "login.failed", body.Username, "from", a.clientIP(r), "(sso link)")
			writeJSON(w, 401, errBody("invalid username or password, or account disabled"))
			return
		}
		if err = a.linkIdentity(u.Username, p.id); err == nil {
			a.audit(u, "oidc.link", u.Username, "subject", p.id.Subject)
		}
	case "create":
		if !cfg.AutoCreate {
			writeJSON(w, 403, errBody("new accounts can't be created from SSO; link an existing account instead"))
			return
		}
		if u, err = a.createOIDCUser(p.id, a.newUserRole(cfg)); err == nil {
			a.audit(u, "user.create", u.Username, a.newUserRole(cfg), "via sso")
		}
	default:
		writeJSON(w, 400, errBody("action must be link or create"))
		return
	}
	if err == nil {
		err = a.startSession(w, r, u)
	}
	var he httpErr
	if errors.As(err, &he) {
		writeJSON(w, he.code, errBody(he.msg))
		return
	}
	if err != nil {
		writeJSON(w, 500, errBody(err.Error()))
		return
	}
	a.takePending(r, true)
	http.SetCookie(w, &http.Cookie{Name: oidcPendingCookie, Path: "/api/auth/oidc", MaxAge: -1})
	a.audit(u, "oidc.login", u.Username)
	writeJSON(w, 200, u)
}

// ---------- per-user link status ----------

func (a *App) hasLocalLogin(username string) bool {
	var n int
	a.db.QueryRow(`SELECT local_login FROM users WHERE username=?`, username).Scan(&n)
	return n == 1
}

func (a *App) oidcIdentityAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	out := map[string]any{"linked": false, "hasPassword": a.hasLocalLogin(u.Username)}
	var email string
	var at int64
	if a.db.QueryRow(`SELECT email, created_at FROM oidc_identities WHERE username=?`, u.Username).Scan(&email, &at) == nil {
		out["linked"], out["email"], out["linkedAt"] = true, email, at
	}
	return out, nil
}

func (a *App) oidcUnlinkAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	if !a.hasLocalLogin(u.Username) {
		return nil, httpErr{400, "set a password first, otherwise you would lose access to your account"}
	}
	res, err := a.db.Exec(`DELETE FROM oidc_identities WHERE username=?`, u.Username)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errNotFound
	}
	a.audit(u, "oidc.unlink", u.Username)
	return nil, nil
}

// ---------- admin configuration ----------

func (a *App) getOIDCConfigAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	c := a.oidcConfig()
	c.HasSecret, c.ClientSecret = c.ClientSecret != "", ""
	return c, nil
}

func (a *App) putOIDCConfigAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var c OIDCConfig
	if err := readJSON(r, &c); err != nil {
		return nil, err
	}
	old := a.oidcConfig()
	if c.ClientSecret == "" {
		c.ClientSecret = old.ClientSecret // blank keeps the stored secret
	}
	c.Issuer, c.ClientID = strings.TrimSpace(c.Issuer), strings.TrimSpace(c.ClientID)
	c.ButtonLabel, c.UsernameClaim = strings.TrimSpace(c.ButtonLabel), strings.TrimSpace(c.UsernameClaim)
	if c.ButtonLabel == "" {
		c.ButtonLabel = "SSO"
	}
	if c.UsernameClaim == "" {
		c.UsernameClaim = "preferred_username"
	}
	if roleRank[c.DefaultRole] == 0 {
		c.DefaultRole = roleViewer
	}
	if !contains(strings.Fields(c.Scopes), "openid") {
		c.Scopes = strings.TrimSpace("openid " + c.Scopes)
	}
	if c.Enabled {
		if c.Issuer == "" || c.ClientID == "" {
			return nil, httpErr{400, "issuer and client ID are required"}
		}
		a.oidc.reset()
		if _, err := a.discover(r.Context(), c.Issuer); err != nil {
			return nil, httpErr{400, err.Error()}
		}
	}
	if c.Issuer != old.Issuer {
		a.oidc.reset()
	}
	if err := a.setJSON("oidc", c); err != nil {
		return nil, err
	}
	a.audit(u, "settings.update", "oidc", map[bool]string{true: "enabled", false: "disabled"}[c.Enabled])
	c.HasSecret, c.ClientSecret = c.ClientSecret != "", ""
	return c, nil
}

// testOIDCAPI checks that the issuer is reachable and returns its endpoints.
func (a *App) testOIDCAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var b struct{ Issuer string }
	if err := readJSON(r, &b); err != nil {
		return nil, err
	}
	a.oidc.reset()
	m, err := a.discover(r.Context(), strings.TrimSpace(b.Issuer))
	if err != nil {
		return map[string]any{"ok": false, "message": err.Error()}, nil
	}
	var set struct{ Keys []jwk }
	if err := a.oidcGet(r.Context(), m.JWKSURI, &set); err != nil {
		return map[string]any{"ok": false, "message": "signing keys: " + err.Error()}, nil
	}
	return map[string]any{"ok": true, "message": fmt.Sprintf("Found %d signing key(s).", len(set.Keys)), "authorizationEndpoint": m.AuthURL, "tokenEndpoint": m.Token}, nil
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

// resolve finds an asset in a repository: locally for hosted, fetching and caching
// upstream for proxy, and trying members in order for group.
func (a *App) resolve(ctx context.Context, repo *Repo, p string, hdr http.Header, depth int) (*Asset, error) {
	switch repo.Type {
	case "hosted":
		x, err := a.findAsset(repo.Name, p)
		if errors.Is(err, errNotFound) && repo.Format == "docker" {
			return a.dockerLinkBlob(repo, p)
		}
		return x, err
	case "proxy":
		return a.proxyGet(ctx, repo, p, hdr)
	case "group":
		if depth > 5 { // nested groups loop guard
			return nil, errNotFound
		}
		for _, m := range a.members(repo) {
			if x, err := a.resolve(ctx, m, p, hdr, depth+1); err == nil {
				return x, nil
			}
		}
	}
	return nil, errNotFound
}

const negativeCacheTTL = 5 * time.Minute

// proxyGet serves from cache; metadata is re-fetched after MetadataTTL, falling back to stale cache if upstream fails.
func (a *App) proxyGet(ctx context.Context, repo *Repo, p string, hdr http.Header) (*Asset, error) {
	cached, err := a.findAsset(repo.Name, p)
	if err == nil && !(isMetadata(repo.Format, p) && time.Since(time.Unix(cached.UpdatedAt, 0)) > time.Duration(repo.MetadataTTL)*time.Minute) {
		return cached, nil
	}
	negKey := repo.Name + "\x00" + p
	if cached == nil {
		if exp, ok := a.negCache.Load(negKey); ok && time.Now().Before(exp.(time.Time)) {
			return nil, errNotFound
		}
	}
	// circuit breaker: while the upstream is known to be down, don't make every client wait for a timeout
	if wait := a.upstreamBackoff(repo.Name); wait > 0 {
		if cached != nil {
			return cached, nil
		}
		return nil, httpErr{http.StatusBadGateway, fmt.Sprintf("the upstream of %s is unreachable and this file is not cached yet; next attempt in %d s", repo.Name, int(wait.Seconds())+1)}
	}
	// concurrent misses for the same path share one upstream download
	v, ferr, _ := a.fetches.Do(negKey, func() (any, error) {
		return a.fetchAndStore(context.WithoutCancel(ctx), repo, p, hdr) // finish caching even if this client disconnects
	})
	if ferr != nil {
		if errors.Is(ferr, errNotFound) {
			a.negCache.Store(negKey, time.Now().Add(negativeCacheTTL))
		}
		if cached != nil {
			log.Printf("proxy %s/%s: serving stale cache: %v", repo.Name, p, ferr)
			return cached, nil
		}
		return nil, ferr
	}
	return v.(*Asset), nil
}

// ponytail: proxies download fully before serving; tee to the client if first-byte latency on huge files matters
func (a *App) fetchAndStore(ctx context.Context, repo *Repo, p string, hdr http.Header) (*Asset, error) {
	u, err := a.upstreamURL(ctx, repo, p)
	if err != nil {
		return nil, err
	}
	resp, err := a.fetchRemote(ctx, repo, u, hdr)
	if err != nil {
		a.upstreamFailed(repo.Name, err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		a.recordHealth(repo.Name, nil)
		return nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		err := upstreamStatus{u, resp.StatusCode, resp.Status}
		a.upstreamFailed(repo.Name, err)
		return nil, err
	}
	a.recordHealth(repo.Name, nil)
	comp, ver := componentOf(repo.Format, p)
	return a.putAsset(repo.Name, p, resp.Body, assetMeta{
		ContentType: resp.Header.Get("Content-Type"), Component: comp, Version: ver, Uploader: "proxy",
	})
}

// ---------- proxy health ----------

// upstreamStatus is an HTTP error answer from an upstream (as opposed to not reaching it at all).
type upstreamStatus struct {
	url    string
	code   int
	status string
}

func (e upstreamStatus) Error() string { return "upstream " + e.url + ": " + e.status }

// upstreamFailed decides whether a failed fetch says something about the upstream as a whole.
// Not reaching it, a server error or rate limiting does. A refusal of one item (401/403 for a
// private or non-existent image, 400…) does not: it would be wrong to flag the whole repository
// as down, and to alert people, because someone mistyped an image name.
func (a *App) upstreamFailed(repo string, err error) {
	var st upstreamStatus
	if errors.As(err, &st) && st.code < 500 && st.code != http.StatusTooManyRequests {
		return
	}
	a.recordHealth(repo, err)
}

// upstreamBackoff returns how long to wait before contacting a failing upstream again (0 = go ahead).
func (a *App) upstreamBackoff(repo string) time.Duration {
	v, ok := a.health.Load(repo)
	if !ok {
		return 0
	}
	h := v.(*proxyHealth)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !h.Down {
		return 0
	}
	return max(a.upstreamRetry-time.Since(h.downSince), 0)
}

type proxyHealth struct {
	LastOK      int64  `json:"lastOk"`
	LastError   string `json:"lastError,omitempty"`
	LastErrorAt int64  `json:"lastErrorAt,omitempty"`
	Down        bool   `json:"-"` // the most recent upstream request failed
	downSince   time.Time
}

// recordHealth notes the outcome of an upstream request and emits repository.degraded /
// repository.recovered when the state flips, so webhooks and notifications fire once per outage.
func (a *App) recordHealth(repo string, err error) {
	v, _ := a.health.LoadOrStore(repo, &proxyHealth{})
	h := v.(*proxyHealth)
	a.mu.Lock() // tiny critical section; reuses the app lock to avoid another mutex
	was := h.Down
	if err != nil {
		h.LastError, h.LastErrorAt, h.Down, h.downSince = err.Error(), time.Now().Unix(), true, time.Now()
	} else {
		h.LastOK, h.Down = time.Now().Unix(), false
	}
	a.mu.Unlock()
	switch {
	case err != nil && !was:
		a.audit(nil, "repository.degraded", repo, "upstream request failed; cached content is still served:", err.Error())
	case err == nil && was:
		a.audit(nil, "repository.recovered", repo, "upstream answers again")
	}
}

// status summarizes a repository for the UI: online, offline, or degraded (proxy upstream failing).
func (a *App) status(r *Repo) (string, *proxyHealth) {
	if r.Offline {
		return "offline", nil
	}
	if v, ok := a.health.Load(r.Name); ok {
		a.mu.Lock()
		h := *v.(*proxyHealth)
		a.mu.Unlock()
		if h.Down {
			return "degraded", &h
		}
		return "online", &h
	}
	return "online", nil
}

// probePath is a cheap upstream URL used by "test connection" per format.
func probePath(format string) string {
	switch format {
	case "npm":
		return "/-/ping"
	case "pypi":
		return "/simple/pip/"
	case "docker":
		return "/v2/"
	case "go":
		return "/golang.org/x/mod/@v/list"
	case "yum":
		return "/repodata/repomd.xml"
	case "conda":
		return "/noarch/repodata.json" // every channel has it; only the response headers are read
	case "apt":
		return "/dists/"
	}
	return "/"
}

type remoteTest struct {
	OK        bool   `json:"ok"`
	Status    string `json:"status"`
	LatencyMs int64  `json:"latencyMs"`
	Message   string `json:"message"`
}

func (a *App) testRemote(ctx context.Context, r *Repo) remoteTest {
	u := strings.TrimSuffix(r.RemoteURL, "/") + probePath(r.Format)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := a.fetchRemote(ctx, r, u, http.Header{})
	res := remoteTest{LatencyMs: time.Since(start).Milliseconds()}
	if err != nil {
		res.Message = err.Error()
		return res
	}
	resp.Body.Close()
	res.Status = resp.Status
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		res.Message = "Remote reachable but rejected the credentials (" + resp.Status + "). Check username and password."
	case resp.StatusCode >= 500:
		res.Message = "Remote reachable but returned a server error."
	default:
		res.OK = true
		res.Message = "Remote is reachable."
	}
	return res
}

// upstreamURL maps a cache path to the remote URL for that format's layout.
func (a *App) upstreamURL(ctx context.Context, repo *Repo, p string) (string, error) {
	base := strings.TrimSuffix(repo.RemoteURL, "/")
	switch repo.Format {
	case "npm":
		if name, ok := strings.CutSuffix(p, "/package.json"); ok && !strings.Contains(name, "/-/") {
			return base + "/" + strings.Replace(name, "/", "%2f", 1), nil
		}
	case "pypi":
		if rest, ok := strings.CutPrefix(p, "simple/"); ok {
			return base + "/simple/" + strings.TrimSuffix(rest, "index.html"), nil
		}
		if rest, ok := strings.CutPrefix(p, "packages/"); ok {
			name, file, _ := strings.Cut(rest, "/")
			return a.pypiFileURL(ctx, repo, name, file)
		}
	case "docker":
		// Docker Hub keeps official images under library/
		if strings.Contains(base, "docker.io") && strings.Count(p, "/") == 2 {
			p = "library/" + p
		}
		return base + "/v2/" + p, nil
	}
	return base + "/" + p, nil
}

var challengeParamRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

// fetchRemote GETs an upstream URL with configured credentials, handling
// registry bearer-token challenges (Docker Hub, GHCR, ...).
func (a *App) fetchRemote(ctx context.Context, repo *Repo, u string, hdr http.Header) (*http.Response, error) {
	do := func(bearer string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "RepoMan/"+version)
		if acc := hdr.Get("Accept"); acc != "" {
			req.Header.Set("Accept", acc)
		}
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		} else if repo.RemoteUsername != "" {
			req.SetBasicAuth(repo.RemoteUsername, repo.RemotePassword)
		}
		return a.client.Do(req)
	}
	resp, err := do("")
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	ch := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(strings.ToLower(ch), "bearer ") {
		return resp, nil
	}
	resp.Body.Close()
	// ponytail: fetches a fresh registry token per upstream request; cache by scope if upstream rate-limits tokens
	tok, err := a.registryToken(ctx, repo, ch)
	if err != nil {
		return nil, err
	}
	return do(tok)
}

func (a *App) registryToken(ctx context.Context, repo *Repo, challenge string) (string, error) {
	params := map[string]string{}
	for _, m := range challengeParamRe.FindAllStringSubmatch(challenge, -1) {
		params[m[1]] = m[2]
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, params["realm"], nil)
	if err != nil {
		return "", err
	}
	q := req.URL.Query()
	for _, k := range []string{"service", "scope"} {
		if params[k] != "" {
			q.Set(k, params[k])
		}
	}
	req.URL.RawQuery = q.Encode()
	if repo.RemoteUsername != "" {
		req.SetBasicAuth(repo.RemoteUsername, repo.RemotePassword)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct{ Token, AccessToken string }
	if resp.StatusCode != 200 {
		return "", upstreamStatus{"registry token service " + req.URL.Host, resp.StatusCode, resp.Status}
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return "", errors.New("registry token service " + req.URL.Host + " sent an unreadable answer")
	}
	if body.Token == "" {
		body.Token = body.AccessToken
	}
	return body.Token, nil
}

// isMetadata reports whether a path holds mutable index data subject to MetadataTTL.
func isMetadata(format, p string) bool {
	switch format {
	case "maven":
		return strings.Contains(path.Base(p), "maven-metadata.xml")
	case "npm":
		return strings.HasSuffix(p, "/package.json") && !strings.Contains(p, "/-/")
	case "pypi":
		return strings.HasPrefix(p, "simple/")
	case "docker":
		return strings.HasSuffix(p, "/tags/list") || (strings.Contains(p, "/manifests/") && !strings.Contains(p, "/manifests/sha256:"))
	case "go":
		if strings.HasPrefix(p, "sumdb/") { // only the signed tree head changes; tiles and lookups are immutable
			return strings.HasSuffix(p, "/latest")
		}
		return strings.HasSuffix(p, "/@v/list") || strings.HasSuffix(p, "/@latest")
	case "apt":
		return strings.HasPrefix(p, "dists/") && !strings.Contains(p, "/by-hash/")
	case "yum":
		return strings.HasPrefix(p, "repodata/")
	case "conda": // repodata.json(.zst/.bz2), current_repodata.json, repodata.jlap, repodata_shards index; shards/* are content-addressed
		b := path.Base(p)
		return strings.HasPrefix(b, "repodata") || strings.HasPrefix(b, "current_repodata") || b == "channeldata.json"
	}
	return false
}

// componentOf derives a component name and version from an asset path, for search and browsing.
func componentOf(format, p string) (comp, ver string) {
	segs := strings.Split(p, "/")
	n := len(segs)
	file := segs[n-1]
	switch format {
	case "maven":
		if strings.HasPrefix(file, "maven-metadata.xml") && n >= 3 {
			return strings.Join(segs[:n-2], ".") + ":" + segs[n-2], ""
		}
		if n >= 4 {
			return strings.Join(segs[:n-3], ".") + ":" + segs[n-3], segs[n-2]
		}
	case "npm":
		if name, f, ok := strings.Cut(p, "/-/"); ok {
			return name, strings.TrimSuffix(strings.TrimPrefix(f, path.Base(name)+"-"), ".tgz")
		}
		return strings.TrimSuffix(p, "/package.json"), ""
	case "pypi":
		if n == 3 && segs[0] == "packages" {
			return segs[1], pypiVersion(segs[2])
		}
		if n >= 2 && segs[0] == "simple" {
			return segs[1], ""
		}
	case "docker":
		for _, k := range []string{"/manifests/", "/blobs/", "/tags/"} {
			if img, ref, ok := strings.Cut(p, k); ok {
				if k == "/manifests/" && !strings.HasPrefix(ref, "sha256:") {
					return img, ref
				}
				return img, ""
			}
		}
	case "go":
		if strings.HasPrefix(p, "sumdb/") {
			return "", ""
		}
		return goComponent(p)
	case "apt":
		if strings.HasSuffix(file, ".deb") { // name_version_arch.deb
			if parts := strings.Split(strings.TrimSuffix(file, ".deb"), "_"); len(parts) == 3 {
				return parts[0], parts[1]
			}
		}
	case "conda":
		if m := condaFileRe.FindStringSubmatch(file); m != nil {
			return m[1], m[2]
		}
	case "yum":
		if strings.HasSuffix(file, ".rpm") { // name-version-release.arch.rpm
			base := strings.TrimSuffix(file, ".rpm")
			if i := strings.LastIndex(base, "."); i > 0 {
				base = base[:i]
			}
			if i := strings.LastIndex(base, "-"); i > 0 {
				if j := strings.LastIndex(base[:i], "-"); j > 0 {
					return base[:j], base[j+1:]
				}
			}
		}
	}
	return "", ""
}

// validPath rejects empty, absolute, non-canonical and traversal paths.
func validPath(p string) bool {
	return p != "" && path.Clean(p) == p && !strings.HasPrefix(p, "/") && p != ".." && !strings.HasPrefix(p, "../")
}

func (a *App) checkRedeploy(repo *Repo, p string) error {
	if repo.AllowRedeploy {
		return nil
	}
	if repo.Format == "maven" && (strings.Contains(p, "SNAPSHOT") || strings.Contains(path.Base(p), "maven-metadata.xml")) {
		return nil
	}
	if _, err := a.findAsset(repo.Name, p); err == nil {
		return httpErr{400, "repository does not allow updating assets: " + p}
	}
	return nil
}

func (a *App) baseURL(r *http.Request) string {
	if b := a.settings().BaseURL; b != "" {
		return strings.TrimSuffix(b, "/")
	}
	scheme, host := "http", r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if a.fromTrustedProxy(r) { // only a known reverse proxy may tell us the public scheme and host
		if p := strings.ToLower(r.Header.Get("X-Forwarded-Proto")); p == "https" || p == "http" {
			scheme = p
		}
		if fh, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Host"), ","); strings.TrimSpace(fh) != "" {
			host = strings.TrimSpace(fh)
		}
	}
	return scheme + "://" + host
}

func (a *App) serveAsset(w http.ResponseWriter, r *http.Request, x *Asset) {
	f, err := a.blobs.Open(x.Blob)
	if err != nil {
		http.Error(w, "asset content missing from blob store", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", x.ContentType)
	w.Header().Set("ETag", `"`+x.Blob+`"`)
	if r.Method == http.MethodGet {
		a.markDownloaded(x.ID)
	}
	http.ServeContent(w, r, "", time.Unix(x.UpdatedAt, 0), f)
}

func resolveError(w http.ResponseWriter, err error) {
	var he httpErr
	switch {
	case errors.Is(err, errNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.As(err, &he):
		http.Error(w, he.msg, he.code)
	default:
		http.Error(w, err.Error(), http.StatusBadGateway)
	}
}

// handleRepo serves /repository/{repo}/{path...} for all non-docker formats.
func (a *App) handleRepo(w http.ResponseWriter, r *http.Request) {
	repo, err := a.getRepo(r.PathValue("repo"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p := r.PathValue("path")
	if repo.Offline {
		http.Error(w, "repository "+repo.Name+" is offline", http.StatusServiceUnavailable)
		return
	}
	if repo.Format == "npm" && strings.HasPrefix(p, "-/user/") {
		a.npmLogin(w, r) // credentials are in the body
		return
	}
	u := a.authenticate(r)
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	if !a.can(u, repo, write) {
		deny(w, u)
		return
	}
	if write && repo.Type != "hosted" {
		http.Error(w, "cannot deploy to a "+repo.Type+" repository", http.StatusMethodNotAllowed)
		return
	}
	if write && r.Method != http.MethodDelete {
		if err := a.checkWritable(repo); err != nil {
			resolveError(w, err)
			return
		}
	}
	if (repo.Format == "apt" || repo.Format == "yum") && p == "repository.key" && !write {
		a.publicKeyHandler(w, r)
		return
	}
	switch repo.Format {
	case "npm":
		a.npmHandler(w, r, repo, p, u)
	case "pypi":
		a.pypiHandler(w, r, repo, p, u)
	case "go":
		a.goHandler(w, r, repo, p, u)
	case "apt":
		a.aptHandler(w, r, repo, p, u)
	case "yum":
		a.yumHandler(w, r, repo, p, u)
	case "conda":
		a.condaHandler(w, r, repo, p, u)
	case "docker":
		http.Error(w, "docker repositories are served at /v2/"+repo.Name+"/<image>", http.StatusNotFound)
	default:
		a.rawHandler(w, r, repo, p, u)
	}
}

// rawHandler implements plain path-based storage, used by raw and maven.
func (a *App) rawHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	if !validPath(p) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if repo.Format == "maven" && repo.Type == "group" && a.mavenGroupMetadata(w, r, repo, p) {
			return
		}
		x, err := a.resolve(r.Context(), repo, p, r.Header, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		a.serveAsset(w, r, x)
	case http.MethodPut, http.MethodPost:
		if err := a.checkRedeploy(repo, p); err != nil {
			resolveError(w, err)
			return
		}
		comp, ver := componentOf(repo.Format, p)
		if _, err := a.putAsset(repo.Name, p, r.Body, assetMeta{Component: comp, Version: ver, Uploader: u.Username}); err != nil {
			resolveError(w, err)
			return
		}
		a.audit(u, "asset.upload", repo.Name+"/"+p)
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		a.deletePath(w, r, repo, p, u)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// deletePath removes one asset via the client API (DELETE), keeping indexes consistent.
func (a *App) deletePath(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	list, err := a.removeAssets("repo=? AND path=?", repo.Name, p)
	if err != nil || len(list) == 0 {
		http.NotFound(w, r)
		return
	}
	a.audit(u, "asset.delete", repo.Name+"/"+p)
	w.WriteHeader(http.StatusNoContent)
}

// reindex rebuilds generated metadata for a hosted repository after its contents change.
func (a *App) reindex(name string) error {
	repo, err := a.getRepo(name)
	if err != nil || repo.Type != "hosted" {
		return nil
	}
	switch repo.Format {
	case "apt":
		return a.aptReindex(repo)
	case "yum":
		return a.yumReindex(repo)
	case "npm":
		return a.npmPrune(repo)
	}
	return nil
}

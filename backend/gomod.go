package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Go module proxy protocol (GOPROXY):
//   <module>/@v/list, <module>/@v/<version>.{info,mod,zip}, <module>/@latest
// Module paths arrive case-escaped ("!" + lowercase) and are stored as received.

func goComponent(p string) (comp, ver string) {
	unesc := func(m string) string {
		if u, err := module.UnescapePath(m); err == nil {
			return u
		}
		return m
	}
	mod, rest, ok := strings.Cut(p, "/@v/")
	if !ok {
		return unesc(strings.TrimSuffix(p, "/@latest")), ""
	}
	if rest == "list" {
		return unesc(mod), ""
	}
	v := strings.TrimSuffix(rest, path.Ext(rest))
	if u, err := module.UnescapeVersion(v); err == nil {
		v = u
	}
	return unesc(mod), v
}

func (a *App) goHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	if strings.HasPrefix(p, "sumdb/") {
		a.goSumDB(w, r, repo, p)
		return
	}
	mod, rest, isV := strings.Cut(p, "/@v/")
	isLatest := strings.HasSuffix(p, "/@latest")
	if !validPath(p) || (!isV && !isLatest) {
		http.Error(w, "not a Go module proxy path", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		switch {
		case isLatest:
			a.goLatest(w, r, repo, strings.TrimSuffix(p, "/@latest"))
		case rest == "list":
			vers, err := a.goVersions(r.Context(), repo, mod, 0)
			if err != nil {
				resolveError(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(strings.Join(vers, "\n") + "\n"))
		default:
			x, err := a.resolve(r.Context(), repo, p, nil, 0)
			if err != nil {
				resolveError(w, err)
				return
			}
			a.serveAsset(w, r, x)
		}
	case http.MethodPut, http.MethodPost:
		ext := path.Ext(rest)
		if !isV || (ext != ".zip" && ext != ".mod" && ext != ".info") {
			http.Error(w, "upload to <module>/@v/<version>.zip and .mod", http.StatusBadRequest)
			return
		}
		comp, ver := goComponent(p)
		if !semver.IsValid(ver) || semver.Canonical(ver) != ver {
			http.Error(w, "invalid canonical semantic version: "+ver+" (expected e.g. v1.2.3)", http.StatusBadRequest)
			return
		}
		if err := a.checkRedeploy(repo, p); err != nil {
			resolveError(w, err)
			return
		}
		ct := map[string]string{".zip": "application/zip", ".mod": "text/plain; charset=utf-8", ".info": "application/json"}[ext]
		if _, err := a.putAsset(repo.Name, p, r.Body, assetMeta{ContentType: ct, Component: comp, Version: ver, Uploader: u.Username}); err != nil {
			resolveError(w, err)
			return
		}
		// the protocol needs .info; synthesize it so uploads only require .mod and .zip
		info := strings.TrimSuffix(p, ext) + ".info"
		if _, err := a.findAsset(repo.Name, info); err != nil {
			b, _ := json.Marshal(map[string]string{"Version": ver, "Time": time.Now().UTC().Format(time.RFC3339)})
			a.putAsset(repo.Name, info, bytes.NewReader(b), assetMeta{ContentType: "application/json", Component: comp, Version: ver, Uploader: u.Username})
		}
		a.audit(u, "go.upload", repo.Name+"/"+p)
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		a.deletePath(w, r, repo, p, u)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// goSumDB relays checksum-database requests (GONOSUMDB unset) to the upstream module proxy, which
// mirrors sum.golang.org under /sumdb/. Build machines then need no route to sum.golang.org: the
// go command verifies downloads through this repository alone. Without a proxy repository the
// answer is 404, which tells the go command to contact the checksum database directly.
func (a *App) goSumDB(w http.ResponseWriter, r *http.Request, repo *Repo, p string) {
	if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !validPath(p) {
		http.NotFound(w, r)
		return
	}
	// never ask a public service about modules that are published here: their names stay private
	if _, lookup, ok := strings.Cut(p, "/lookup/"); ok {
		if mod, _, ok := strings.Cut(lookup, "@"); ok && a.goHostedModule(mod) {
			http.Error(w, "private module: not in the public checksum database (set GONOSUMDB or GOPRIVATE for "+mod+")", http.StatusNotFound)
			return
		}
	}
	for _, px := range a.goProxies(repo, 0) {
		if x, err := a.proxyGet(r.Context(), px, p, nil); err == nil {
			a.serveAsset(w, r, x)
			return
		}
	}
	http.NotFound(w, r)
}

// goProxies lists the online proxy repositories behind repo (itself, or its group members).
func (a *App) goProxies(repo *Repo, depth int) []*Repo {
	switch {
	case repo.Type == "proxy":
		return []*Repo{repo}
	case repo.Type == "group" && depth <= 5:
		var out []*Repo
		for _, m := range a.members(repo) {
			out = append(out, a.goProxies(m, depth+1)...)
		}
		return out
	}
	return nil
}

// goHostedModule reports whether any hosted Go repository holds the (case-escaped) module path.
func (a *App) goHostedModule(mod string) bool {
	var n int
	prefix := mod + "/@v/"
	a.db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM assets a JOIN repositories r ON r.name=a.repo
		WHERE r.format='go' AND r.type='hosted' AND substr(a.path, 1, ?)=? LIMIT 1)`, len(prefix), prefix).Scan(&n)
	return n > 0
}

// goVersions lists known versions of a module, merged across group members.
func (a *App) goVersions(ctx context.Context, repo *Repo, mod string, depth int) ([]string, error) {
	set := map[string]bool{}
	found := false
	switch repo.Type {
	case "hosted":
		list, err := a.assetsUnder(repo.Name, mod+"/@v/")
		if err != nil {
			return nil, err
		}
		for _, x := range list {
			if strings.HasSuffix(x.Path, ".mod") {
				found = true
				set[x.Version] = true
			}
		}
	case "proxy":
		x, err := a.proxyGet(ctx, repo, mod+"/@v/list", nil)
		if err != nil {
			return nil, err
		}
		data, err := a.readAsset(x)
		if err != nil {
			return nil, err
		}
		found = true
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			if v := strings.TrimSpace(sc.Text()); v != "" {
				set[v] = true
			}
		}
	case "group":
		if depth > 5 {
			return nil, errNotFound
		}
		for _, m := range a.members(repo) {
			if vers, err := a.goVersions(ctx, m, mod, depth+1); err == nil {
				found = true
				for _, v := range vers {
					set[v] = true
				}
			}
		}
	}
	if !found {
		return nil, errNotFound
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	semver.Sort(out)
	return out, nil
}

// goLatest prefers the highest release, then the highest pre-release; proxies defer to upstream @latest.
func (a *App) goLatest(w http.ResponseWriter, r *http.Request, repo *Repo, mod string) {
	if repo.Type != "proxy" {
		vers, _ := a.goVersions(r.Context(), repo, mod, 0)
		best := ""
		for _, v := range vers {
			if semver.Prerelease(v) == "" || semver.Prerelease(best) != "" || best == "" {
				best = v // vers is sorted ascending
			}
		}
		if best != "" {
			if ev, err := module.EscapeVersion(best); err == nil {
				if x, err := a.resolve(r.Context(), repo, mod+"/@v/"+ev+".info", nil, 0); err == nil {
					a.serveAsset(w, r, x)
					return
				}
			}
		}
	}
	x, err := a.resolve(r.Context(), repo, mod+"/@latest", nil, 0)
	if err != nil {
		resolveError(w, err)
		return
	}
	a.serveAsset(w, r, x)
}

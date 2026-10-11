package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// Cargo sparse registry protocol: config.json, the index tree (1/x, 2/xy, 3/x/xyz, ab/cd/name), crate
// downloads at api/v1/crates/<name>/<version>/download, publish (PUT api/v1/crates/new) and yank.
// Hosted repositories keep each version's index line in the Attrs of its stored crate, so the index is
// generated on request and can never drift from what is stored.

var cargoNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var cargoDownloadRe = regexp.MustCompile(`^api/v1/crates/([^/]+)/([^/]+)/download$`)

func cargoIndexPath(name string) string {
	name = strings.ToLower(name)
	switch len(name) {
	case 1, 2:
		return fmt.Sprintf("%d/%s", len(name), name)
	case 3:
		return "3/" + name[:1] + "/" + name
	}
	return name[:2] + "/" + name[2:4] + "/" + name
}

func cargoComponent(p string) (comp, ver string) {
	if m := cargoDownloadRe.FindStringSubmatch(p); m != nil {
		return m[1], m[2]
	}
	if p != "config.json" && !strings.HasPrefix(p, "api/") {
		return p[strings.LastIndex(p, "/")+1:], ""
	}
	return "", ""
}

func (a *App) cargoHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	switch {
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		a.cargoGet(w, r, repo, p)
	case r.Method == http.MethodPut && p == "api/v1/crates/new":
		a.cargoPublish(w, r, repo, u)
	case r.Method == http.MethodDelete && strings.HasSuffix(p, "/yank"):
		a.cargoYank(w, repo, strings.TrimSuffix(p, "/yank"), true)
	case r.Method == http.MethodPut && strings.HasSuffix(p, "/unyank"):
		a.cargoYank(w, repo, strings.TrimSuffix(p, "/unyank"), false)
	case r.Method == http.MethodDelete:
		a.deletePath(w, r, repo, p, u)
	default:
		http.Error(w, "not a Cargo registry path", http.StatusNotFound)
	}
}

func (a *App) cargoGet(w http.ResponseWriter, r *http.Request, repo *Repo, p string) {
	switch {
	case p == "config.json":
		base := a.baseURL(r) + "/repository/" + repo.Name
		cfg, _ := json.Marshal(map[string]any{"dl": base + "/api/v1/crates", "api": base, "auth-required": !a.can(nil, repo, false)})
		w.Header().Set("Content-Type", "application/json")
		w.Write(cfg)
	case cargoDownloadRe.MatchString(p):
		x, err := a.resolve(r.Context(), repo, p, nil, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		a.serveAsset(w, r, x)
	default:
		name, _ := cargoComponent(p)
		if name == "" || cargoIndexPath(name) != p {
			http.NotFound(w, r)
			return
		}
		lines, err := a.cargoIndex(r.Context(), repo, name, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte(strings.Join(lines, "\n") + "\n"))
	}
}

// cargoIndex returns the index lines (one JSON object per version) of a crate, merged across group members.
func (a *App) cargoIndex(ctx context.Context, repo *Repo, name string, depth int) ([]string, error) {
	switch repo.Type {
	case "hosted":
		list, err := a.assetsUnder(repo.Name, "api/v1/crates/"+strings.ToLower(name)+"/")
		if err != nil {
			return nil, err
		}
		var lines []string
		for _, x := range list {
			if len(x.Attrs) > 0 {
				lines = append(lines, string(x.Attrs))
			}
		}
		if len(lines) == 0 {
			return nil, errNotFound
		}
		return lines, nil
	case "proxy":
		x, err := a.proxyGet(ctx, repo, cargoIndexPath(name), nil)
		if err != nil {
			return nil, err
		}
		data, err := a.readAsset(x)
		if err != nil {
			return nil, err
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n"), nil
	case "group":
		if depth > 5 {
			return nil, errNotFound
		}
		seen := map[string]bool{}
		var out []string
		for _, m := range a.members(repo) {
			lines, err := a.cargoIndex(ctx, m, name, depth+1)
			if err != nil {
				continue
			}
			for _, l := range lines {
				var v struct{ Vers string }
				if json.Unmarshal([]byte(l), &v) == nil && !seen[v.Vers] {
					seen[v.Vers] = true
					out = append(out, l)
				}
			}
		}
		if len(out) == 0 {
			return nil, errNotFound
		}
		return out, nil
	}
	return nil, errNotFound
}

type cargoPublishMeta struct {
	Name string `json:"name"`
	Vers string `json:"vers"`
	Deps []struct {
		Name            string   `json:"name"`
		VersionReq      string   `json:"version_req"`
		Features        []string `json:"features"`
		Optional        bool     `json:"optional"`
		DefaultFeatures bool     `json:"default_features"`
		Target          *string  `json:"target"`
		Kind            string   `json:"kind"`
		Registry        *string  `json:"registry"`
		ExplicitName    *string  `json:"explicit_name_in_toml"`
	} `json:"deps"`
	Features    map[string][]string `json:"features"`
	Links       *string             `json:"links"`
	RustVersion *string             `json:"rust_version"`
}

func (a *App) cargoPublish(w http.ResponseWriter, r *http.Request, repo *Repo, u *User) {
	bad := func(msg string) {
		// cargo shows errors from this shape instead of the raw body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"errors": []map[string]string{{"detail": msg}}})
	}
	var n uint32
	if binary.Read(r.Body, binary.LittleEndian, &n) != nil || n == 0 || n > 4<<20 {
		bad("malformed publish request")
		return
	}
	raw := make([]byte, n)
	var clen uint32
	if _, err := io.ReadFull(r.Body, raw); err != nil || binary.Read(r.Body, binary.LittleEndian, &clen) != nil {
		bad("malformed publish request")
		return
	}
	var m cargoPublishMeta
	if json.Unmarshal(raw, &m) != nil || !cargoNameRe.MatchString(m.Name) || m.Vers == "" || strings.ContainsAny(m.Vers, "/ ") {
		bad("invalid crate name or version")
		return
	}
	p := fmt.Sprintf("api/v1/crates/%s/%s/download", strings.ToLower(m.Name), m.Vers)
	if err := a.checkRedeploy(repo, p); err != nil {
		bad(err.Error())
		return
	}
	tmp, sha, size, err := a.blobs.Write(io.LimitReader(r.Body, int64(clen)))
	if err != nil {
		resolveError(w, err)
		return
	}
	deps := []map[string]any{}
	for _, d := range m.Deps {
		e := map[string]any{"name": d.Name, "req": d.VersionReq, "features": d.Features, "optional": d.Optional,
			"default_features": d.DefaultFeatures, "target": d.Target, "kind": d.Kind, "registry": d.Registry}
		if d.ExplicitName != nil { // renamed dependency: "name" is the key in Cargo.toml, "package" the crate
			e["name"], e["package"] = *d.ExplicitName, d.Name
		}
		deps = append(deps, e)
	}
	// features using "dep:" or "pkg?/feat" belong in features2 (index format v2)
	f1, f2 := map[string][]string{}, map[string][]string{}
	for k, vs := range m.Features {
		f1[k] = vs
		for _, v := range vs {
			if strings.HasPrefix(v, "dep:") || strings.Contains(v, "?/") {
				f2[k] = vs
				delete(f1, k)
				break
			}
		}
	}
	entry := map[string]any{"name": m.Name, "vers": m.Vers, "deps": deps, "cksum": sha, "features": f1, "yanked": false, "links": m.Links, "rust_version": m.RustVersion, "v": 1}
	if len(f2) > 0 {
		entry["features2"], entry["v"] = f2, 2
	}
	attrs, _ := json.Marshal(entry)
	if _, err := a.storeTemp(repo.Name, p, tmp, sha, size, assetMeta{ContentType: "application/gzip", Component: strings.ToLower(m.Name), Version: m.Vers, Uploader: u.Username, Attrs: string(attrs)}); err != nil {
		resolveError(w, err)
		return
	}
	a.audit(u, "cargo.publish", repo.Name+"/"+m.Name+"@"+m.Vers)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"warnings":{"invalid_categories":[],"invalid_badges":[],"other":[]}}`))
}

func (a *App) cargoYank(w http.ResponseWriter, repo *Repo, base string, yank bool) {
	x, err := a.findAsset(repo.Name, base+"/download")
	if err != nil || !cargoDownloadRe.MatchString(base+"/download") {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var e map[string]any
	if json.Unmarshal(x.Attrs, &e) != nil {
		http.Error(w, "crate has no index entry", http.StatusInternalServerError)
		return
	}
	e["yanked"] = yank
	b, _ := json.Marshal(e)
	a.db.Exec(`UPDATE assets SET attrs=? WHERE id=?`, string(b), x.ID)
	a.assetGen.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true}`))
}

// cargoUpstreamURL maps a crate download to the upstream registry's dl template (config.json).
func (a *App) cargoUpstreamURL(ctx context.Context, repo *Repo, p string) (string, error) {
	m := cargoDownloadRe.FindStringSubmatch(p)
	if m == nil {
		return strings.TrimSuffix(repo.RemoteURL, "/") + "/" + p, nil
	}
	x, err := a.proxyGet(ctx, repo, "config.json", nil)
	if err != nil {
		return "", err
	}
	data, err := a.readAsset(x)
	if err != nil {
		return "", err
	}
	var cfg struct{ Dl string }
	if json.Unmarshal(data, &cfg) != nil || cfg.Dl == "" {
		return "", fmt.Errorf("upstream config.json has no dl template")
	}
	name, ver := m[1], m[2]
	if !strings.Contains(cfg.Dl, "{") {
		return cfg.Dl + "/" + name + "/" + ver + "/download", nil
	}
	ip := cargoIndexPath(name)
	prefix := ip[:strings.LastIndex(ip, "/")]
	return strings.NewReplacer("{crate}", name, "{version}", ver, "{prefix}", prefix, "{lowerprefix}", strings.ToLower(prefix)).Replace(cfg.Dl), nil
}

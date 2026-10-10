package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path"
	"strings"
	"time"
)

// npm layout: packument at "<name>/package.json", tarballs at "<name>/-/<file>.tgz".
func (a *App) npmHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case p == "-/whoami":
		if u == nil {
			deny(w, u)
			return
		}
		writeJSON(w, 200, map[string]string{"username": u.Username})
	case p == "-/ping":
		writeJSON(w, 200, map[string]any{})
	case strings.HasPrefix(p, "-/"):
		http.NotFound(w, r) // ponytail: no npm search / dist-tag API; the UI search covers browsing
	case strings.Contains(p, "/-/") && read:
		x, err := a.resolve(r.Context(), repo, p, nil, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		a.serveAsset(w, r, x)
	case r.Method == http.MethodPut && !strings.Contains(p, "/-"):
		a.npmPublish(w, r, repo, p, u)
	case read && p != "":
		a.npmPackument(w, r, repo, p)
	default:
		// ponytail: npm unpublish/deprecate not supported; delete versions from the UI
		http.Error(w, "unsupported npm operation", http.StatusMethodNotAllowed)
	}
}

// npmPackument serves package metadata with tarball URLs rewritten to point at this repository.
func (a *App) npmPackument(w http.ResponseWriter, r *http.Request, repo *Repo, name string) {
	x, err := a.resolve(r.Context(), repo, name+"/package.json", nil, 0)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errBody("not found"))
		return
	}
	data, err := a.readAsset(x)
	var doc map[string]any
	if err == nil {
		err = json.Unmarshal(data, &doc)
	}
	if err != nil {
		http.Error(w, "corrupt package metadata", http.StatusInternalServerError)
		return
	}
	prefix := a.baseURL(r) + "/repository/" + repo.Name + "/" + name + "/-/"
	versions, _ := doc["versions"].(map[string]any)
	for _, v := range versions {
		if dist, ok := v.(map[string]any)["dist"].(map[string]any); ok {
			if tb, ok := dist["tarball"].(string); ok {
				dist["tarball"] = prefix + path.Base(tb)
			}
		}
	}
	writeJSON(w, http.StatusOK, doc)
}

func (a *App) npmPublish(w http.ResponseWriter, r *http.Request, repo *Repo, name string, u *User) {
	var body struct {
		Name        string                     `json:"name"`
		Versions    map[string]json.RawMessage `json:"versions"`
		DistTags    map[string]string          `json:"dist-tags"`
		Attachments map[string]struct {
			Data string `json:"data"`
		} `json:"_attachments"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<30)).Decode(&body); err != nil || body.Name != name {
		writeJSON(w, 400, errBody("invalid publish request"))
		return
	}
	a.npmMu.Lock()
	defer a.npmMu.Unlock()

	metaPath := name + "/package.json"
	doc := map[string]any{"name": name}
	if x, err := a.findAsset(repo.Name, metaPath); err == nil {
		if data, err := a.readAsset(x); err == nil {
			json.Unmarshal(data, &doc)
		}
	}
	versions, tags, times := subMap(doc, "versions"), subMap(doc, "dist-tags"), subMap(doc, "time")
	now := time.Now().UTC().Format(time.RFC3339)
	var ver string
	for v, raw := range body.Versions {
		if _, exists := versions[v]; exists && !repo.AllowRedeploy {
			writeJSON(w, http.StatusForbidden, errBody("cannot publish over existing version "+v))
			return
		}
		var vm any
		json.Unmarshal(raw, &vm)
		versions[v], times[v], ver = vm, now, v
	}
	for t, v := range body.DistTags {
		tags[t] = v
	}
	if _, ok := times["created"]; !ok {
		times["created"] = now
	}
	times["modified"] = now

	for file, att := range body.Attachments {
		data, err := base64.StdEncoding.DecodeString(att.Data)
		if err != nil {
			writeJSON(w, 400, errBody("invalid attachment encoding"))
			return
		}
		_, err = a.putAsset(repo.Name, name+"/-/"+path.Base(file), bytes.NewReader(data),
			assetMeta{ContentType: "application/octet-stream", Component: name, Version: ver, Uploader: u.Username})
		if err != nil {
			writeJSON(w, 500, errBody(err.Error()))
			return
		}
	}
	buf, _ := json.Marshal(doc)
	if _, err := a.putAsset(repo.Name, metaPath, bytes.NewReader(buf),
		assetMeta{ContentType: "application/json", Component: name, Uploader: u.Username}); err != nil {
		writeJSON(w, 500, errBody(err.Error()))
		return
	}
	a.audit(u, "npm.publish", repo.Name+"/"+name, ver)
	writeJSON(w, http.StatusCreated, map[string]bool{"ok": true})
}

func subMap(doc map[string]any, key string) map[string]any {
	m, ok := doc[key].(map[string]any)
	if !ok {
		m = map[string]any{}
		doc[key] = m
	}
	return m
}

// npmLogin handles `npm login --auth-type=legacy` by issuing an API token.
func (a *App) npmLogin(w http.ResponseWriter, r *http.Request) {
	var body struct{ Name, Password string }
	if r.Method != http.MethodPut || json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body) != nil {
		writeJSON(w, 400, errBody("invalid login request"))
		return
	}
	u := a.passwordLogin(r, body.Name, body.Password)
	if u == nil {
		writeJSON(w, 401, errBody("invalid username or password"))
		return
	}
	tok, err := a.createToken(u.Username, "npm login", "api", scopeFull, 0)
	if err != nil {
		writeJSON(w, 500, errBody(err.Error()))
		return
	}
	a.audit(u, "token.create", "npm login")
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "id": "org.couchdb.user:" + u.Username, "token": tok})
}

// npmPrune drops versions whose tarballs were deleted (from the UI or cleanup) from hosted packuments.
func (a *App) npmPrune(repo *Repo) error {
	a.npmMu.Lock()
	defer a.npmMu.Unlock()
	metas, err := a.queryAssets(`WHERE repo=? AND path LIKE '%/package.json' AND path NOT LIKE '%/-/%'`, repo.Name)
	if err != nil {
		return err
	}
	for _, m := range metas {
		name := strings.TrimSuffix(m.Path, "/package.json")
		data, err := a.readAsset(m)
		var doc map[string]any
		if err != nil || json.Unmarshal(data, &doc) != nil {
			continue
		}
		versions, tags, times := subMap(doc, "versions"), subMap(doc, "dist-tags"), subMap(doc, "time")
		changed := false
		for v, raw := range versions {
			tb := ""
			if dist, ok := raw.(map[string]any)["dist"].(map[string]any); ok {
				tb, _ = dist["tarball"].(string)
			}
			if _, err := a.findAsset(repo.Name, name+"/-/"+path.Base(tb)); err != nil {
				delete(versions, v)
				delete(times, v)
				changed = true
			}
		}
		if !changed {
			continue
		}
		if len(versions) == 0 {
			a.deleteAssets("id=?", m.ID)
			continue
		}
		newest, newestT := "", ""
		for v := range versions {
			if t, _ := times[v].(string); t >= newestT {
				newest, newestT = v, t
			}
		}
		for t, v := range tags {
			if _, ok := versions[v.(string)]; !ok {
				delete(tags, t)
			}
		}
		if _, ok := tags["latest"]; !ok {
			tags["latest"] = newest
		}
		buf, _ := json.Marshal(doc)
		if _, err := a.putAsset(repo.Name, m.Path, bytes.NewReader(buf),
			assetMeta{ContentType: "application/json", Component: name, Uploader: m.Uploader}); err != nil {
			return err
		}
	}
	return nil
}

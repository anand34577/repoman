package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// Pub (Dart / Flutter) hosted repository API v2: package listing at api/packages/<name>, archives at
// api/archives/<name>-<version>.tar.gz and the three-step publish flow (versions/new → newUpload → newUploadFinish).
// Hosted repositories keep each version's pubspec (as JSON) in the Attrs of its archive.

const pubJSON = "application/vnd.pub.v2+json"

var pubNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,63}$`)

func pubComponent(p string) (comp, ver string) {
	if f, ok := strings.CutPrefix(p, "api/archives/"); ok {
		f = strings.TrimSuffix(f, ".tar.gz")
		if i := strings.LastIndex(f, "-"); i > 0 {
			return f[:i], f[i+1:]
		}
	}
	if n, ok := strings.CutPrefix(p, "api/packages/"); ok && !strings.Contains(n, "/") {
		return n, ""
	}
	return "", ""
}

func (a *App) pubHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	base := a.baseURL(r) + "/repository/" + repo.Name
	switch {
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && p == "api/packages/versions/new":
		if repo.Type != "hosted" || !a.can(u, repo, true) {
			deny(w, u)
			return
		}
		pubWrite(w, 200, map[string]any{"url": base + "/api/packages/versions/newUpload", "fields": map[string]string{}})
	case r.Method == http.MethodGet && p == "api/packages/versions/newUploadFinish":
		if msg := r.URL.Query().Get("error"); msg != "" {
			pubWrite(w, 400, map[string]any{"error": map[string]string{"code": "InvalidInput", "message": msg}})
			return
		}
		pubWrite(w, 200, map[string]any{"success": map[string]string{"message": "Successfully uploaded the package."}})
	case r.Method == http.MethodPost && p == "api/packages/versions/newUpload":
		finish := base + "/api/packages/versions/newUploadFinish"
		if err := a.pubPublish(r, repo, u); err != nil {
			finish += "?error=" + url.QueryEscape(err.Error())
		}
		w.Header().Set("Location", finish)
		w.WriteHeader(http.StatusNoContent)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(p, "api/archives/"):
		x, err := a.resolve(r.Context(), repo, p, nil, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		a.serveAsset(w, r, x)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.HasPrefix(p, "api/packages/") && !strings.Contains(strings.TrimPrefix(p, "api/packages/"), "/"):
		vers, err := a.pubVersions(r.Context(), repo, strings.TrimPrefix(p, "api/packages/"), base, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		sort.Slice(vers, func(i, j int) bool {
			return semver.Compare("v"+vers[i]["version"].(string), "v"+vers[j]["version"].(string)) < 0
		})
		latest := vers[len(vers)-1]
		for _, v := range vers { // the newest release, unless there are only pre-releases
			if semver.Prerelease("v"+v["version"].(string)) == "" {
				latest = v
			}
		}
		pubWrite(w, 200, map[string]any{"name": strings.TrimPrefix(p, "api/packages/"), "latest": latest, "versions": vers})
	case r.Method == http.MethodDelete:
		a.deletePath(w, r, repo, p, u)
	default:
		http.Error(w, "not a pub repository path", http.StatusNotFound)
	}
}

func pubWrite(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", pubJSON)
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// pubVersions lists a package's versions as pub's version objects, merged across group members (first wins).
func (a *App) pubVersions(ctx context.Context, repo *Repo, name, base string, depth int) ([]map[string]any, error) {
	var out []map[string]any
	switch repo.Type {
	case "hosted":
		list, err := a.assetsUnder(repo.Name, "api/archives/"+name+"-")
		if err != nil {
			return nil, err
		}
		for _, x := range list {
			var spec map[string]any
			if x.Component == name && json.Unmarshal(x.Attrs, &spec) == nil {
				out = append(out, map[string]any{"version": x.Version, "archive_url": base + "/" + x.Path, "archive_sha256": x.Blob, "pubspec": spec})
			}
		}
	case "proxy":
		x, err := a.proxyGet(ctx, repo, "api/packages/"+name, http.Header{"Accept": {pubJSON}})
		if err != nil {
			return nil, err
		}
		data, err := a.readAsset(x)
		if err != nil {
			return nil, err
		}
		var up struct{ Versions []map[string]any }
		if json.Unmarshal(data, &up) != nil {
			return nil, errors.New("unreadable package listing from upstream")
		}
		for _, v := range up.Versions {
			if au, _ := v["archive_url"].(string); au != "" { // archives are fetched (and cached) through this repository
				if i := strings.Index(au, "/api/archives/"); i >= 0 {
					v["archive_url"] = base + au[i:]
				}
			}
			if _, ok := v["version"].(string); ok {
				out = append(out, v)
			}
		}
	case "group":
		if depth > 5 {
			return nil, errNotFound
		}
		seen := map[string]bool{}
		for _, m := range a.members(repo) {
			vers, err := a.pubVersions(ctx, m, name, base, depth+1)
			if err != nil {
				continue
			}
			for _, v := range vers {
				if ver := v["version"].(string); !seen[ver] {
					seen[ver] = true
					out = append(out, v)
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, errNotFound
	}
	return out, nil
}

func (a *App) pubPublish(r *http.Request, repo *Repo, u *User) error {
	mr, err := r.MultipartReader()
	if err != nil {
		return errors.New("expected a multipart upload")
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			return errors.New("no file in the upload")
		}
		if part.FormName() != "file" {
			continue
		}
		tmp, sha, size, err := a.blobs.Write(part)
		if err != nil {
			return err
		}
		spec, err := pubspecOf(tmp)
		if err != nil {
			a.blobs.Discard(tmp)
			return err
		}
		name, _ := spec["name"].(string)
		ver, _ := spec["version"].(string)
		if !pubNameRe.MatchString(name) || !semver.IsValid("v"+ver) {
			a.blobs.Discard(tmp)
			return fmt.Errorf("pubspec.yaml needs a valid name and version (got %q %q)", name, ver)
		}
		p := "api/archives/" + name + "-" + ver + ".tar.gz"
		if err := a.checkRedeploy(repo, p); err != nil {
			a.blobs.Discard(tmp)
			return errors.New("version " + ver + " of " + name + " already exists")
		}
		attrs, _ := json.Marshal(spec)
		if _, err := a.storeTemp(repo.Name, p, tmp, sha, size, assetMeta{ContentType: "application/gzip", Component: name, Version: ver, Uploader: u.Username, Attrs: string(attrs)}); err != nil {
			return err
		}
		a.audit(u, "pub.publish", repo.Name+"/"+name+"@"+ver)
		return nil
	}
}

// pubspecOf reads pubspec.yaml from a package archive as a JSON-compatible map.
func pubspecOf(file string) (map[string]any, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, errors.New("not a .tar.gz package archive")
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, errors.New("pubspec.yaml not found in the archive")
		}
		if strings.TrimPrefix(h.Name, "./") != "pubspec.yaml" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return nil, err
		}
		var spec map[string]any
		if err := yaml.Unmarshal(data, &spec); err != nil {
			return nil, errors.New("pubspec.yaml is not valid: " + err.Error())
		}
		if _, err := json.Marshal(spec); err != nil { // e.g. non-string map keys
			return nil, errors.New("pubspec.yaml uses keys that are not strings")
		}
		return spec, nil
	}
}

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Docker Registry HTTP API v2. Image names are "<repository>/<image>", e.g.
//   docker pull localhost:8081/docker-hub/library/nginx
// Asset layout: "<image>/blobs/<digest>", "<image>/manifests/<tag|digest>".

var (
	reUpload   = regexp.MustCompile(`^(.+)/blobs/uploads/([0-9a-f]*)$`)
	reBlob     = regexp.MustCompile(`^(.+)/blobs/(sha256:[0-9a-f]{64})$`)
	reManifest = regexp.MustCompile(`^(.+)/manifests/([A-Za-z0-9_.:-]{1,128})$`)
	reTags     = regexp.MustCompile(`^(.+)/tags/list$`)
	reDigest   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func dockerErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"errors": []map[string]string{{"code": code, "message": msg}}})
}

func (a *App) handleDocker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	p := r.PathValue("path")
	u := a.authenticate(r)

	if p == "" { // version check; challenge so clients send credentials afterwards
		if u == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="RepoMan"`)
			dockerErr(w, 401, "UNAUTHORIZED", "authentication required")
			return
		}
		writeJSON(w, 200, map[string]any{})
		return
	}
	if p == "_catalog" {
		a.dockerCatalog(w, a.readableRepos(u))
		return
	}

	var kind, name, ref string
	if m := reUpload.FindStringSubmatch(p); m != nil {
		kind, name, ref = "upload", m[1], m[2]
	} else if m := reBlob.FindStringSubmatch(p); m != nil {
		kind, name, ref = "blob", m[1], m[2]
	} else if m := reManifest.FindStringSubmatch(p); m != nil {
		kind, name, ref = "manifest", m[1], m[2]
	} else if m := reTags.FindStringSubmatch(p); m != nil {
		kind, name = "tags", m[1]
	} else {
		dockerErr(w, 404, "UNSUPPORTED", "unknown endpoint")
		return
	}
	repoName, image, ok := strings.Cut(name, "/")
	repo, err := a.getRepo(repoName)
	if !ok || err != nil || repo.Format != "docker" {
		dockerErr(w, 404, "NAME_UNKNOWN", "image names must be <docker-repository>/<image>")
		return
	}
	if repo.Offline {
		dockerErr(w, 503, "UNAVAILABLE", "repository "+repo.Name+" is offline")
		return
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	if !a.can(u, repo, write) {
		if u == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="RepoMan"`)
			dockerErr(w, 401, "UNAUTHORIZED", "authentication required")
		} else {
			dockerErr(w, 403, "DENIED", "insufficient permissions")
		}
		return
	}
	if write && repo.Type != "hosted" {
		dockerErr(w, 405, "UNSUPPORTED", "cannot push to a "+repo.Type+" repository")
		return
	}
	if write && r.Method != http.MethodDelete {
		if err := a.checkWritable(repo); err != nil {
			dockerErr(w, err.(httpErr).code, "DENIED", err.Error())
			return
		}
	}

	switch kind {
	case "upload":
		a.dockerUpload(w, r, repo, name, image, ref, u)
	case "blob":
		a.dockerBlob(w, r, repo, image, ref)
	case "manifest":
		a.dockerManifest(w, r, repo, name, image, ref, u)
	case "tags":
		tags := a.dockerTags(r.Context(), repo, image, 0)
		if tags == nil {
			dockerErr(w, 404, "NAME_UNKNOWN", "repository name not known")
			return
		}
		writeJSON(w, 200, map[string]any{"name": name, "tags": tags})
	}
}

func (a *App) dockerBlob(w http.ResponseWriter, r *http.Request, repo *Repo, image, digest string) {
	p := image + "/blobs/" + digest
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		x, err := a.resolve(r.Context(), repo, p, r.Header, 0)
		if err != nil {
			dockerErr(w, 404, "BLOB_UNKNOWN", "blob unknown")
			return
		}
		w.Header().Set("Docker-Content-Digest", digest)
		x.ContentType = "application/octet-stream"
		a.serveAsset(w, r, x)
	case http.MethodDelete:
		a.deleteAssets("repo=? AND path=?", repo.Name, p)
		w.WriteHeader(http.StatusAccepted)
	default:
		dockerErr(w, 405, "UNSUPPORTED", "method not allowed")
	}
}

// dockerLinkBlob lets a hosted repo serve a layer already pushed under another image (dedupe across images).
func (a *App) dockerLinkBlob(repo *Repo, p string) (*Asset, error) {
	image, digest, ok := strings.Cut(p, "/blobs/")
	if !ok || !strings.HasPrefix(digest, "sha256:") {
		return nil, errNotFound
	}
	list, err := a.queryAssets(`WHERE repo=? AND blob=? LIMIT 1`, repo.Name, strings.TrimPrefix(digest, "sha256:"))
	if err != nil || len(list) == 0 {
		return nil, errNotFound
	}
	return a.linkAsset(repo.Name, p, list[0], assetMeta{ContentType: "application/octet-stream", Component: image, Uploader: list[0].Uploader})
}

func (a *App) dockerUpload(w http.ResponseWriter, r *http.Request, repo *Repo, name, image, id string, u *User) {
	q := r.URL.Query()
	location := func(id string) string { return "/v2/" + name + "/blobs/uploads/" + id }
	finish := func(digest string, body io.Reader) {
		if !reDigest.MatchString(digest) {
			dockerErr(w, 400, "DIGEST_INVALID", "the digest parameter must be sha256:<64 hex digits>")
			return
		}
		if _, err := a.putAsset(repo.Name, image+"/blobs/"+digest, body, assetMeta{
			ContentType: "application/octet-stream", Component: image, Uploader: u.Username,
			WantSHA: strings.TrimPrefix(digest, "sha256:"),
		}); err != nil {
			dockerErr(w, 400, "DIGEST_INVALID", err.Error())
			return
		}
		w.Header().Set("Location", "/v2/"+name+"/blobs/"+digest)
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	}
	progress := func(id string, status int) {
		var size int64
		if st, err := os.Stat(filepath.Join(a.uploads, id)); err == nil {
			size = st.Size()
		}
		w.Header().Set("Location", location(id))
		w.Header().Set("Docker-Upload-UUID", id)
		w.Header().Set("Range", "0-"+strconv.FormatInt(max(size-1, 0), 10))
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(status)
	}
	file := filepath.Join(a.uploads, id)

	switch {
	case r.Method == http.MethodPost && id == "":
		if mount := q.Get("mount"); strings.HasPrefix(mount, "sha256:") {
			if _, err := a.dockerLinkBlob(repo, image+"/blobs/"+mount); err == nil {
				w.Header().Set("Location", "/v2/"+name+"/blobs/"+mount)
				w.Header().Set("Docker-Content-Digest", mount)
				w.WriteHeader(http.StatusCreated)
				return
			}
		}
		if d := q.Get("digest"); d != "" { // monolithic upload
			finish(d, r.Body)
			return
		}
		id = randomHex(16)
		if err := os.WriteFile(filepath.Join(a.uploads, id), nil, 0o644); err != nil {
			dockerErr(w, 500, "UNKNOWN", err.Error())
			return
		}
		progress(id, http.StatusAccepted)
	case id == "":
		dockerErr(w, 404, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
	case r.Method == http.MethodPatch:
		if err := appendFile(file, r.Body); err != nil {
			dockerErr(w, 404, "BLOB_UPLOAD_UNKNOWN", err.Error())
			return
		}
		progress(id, http.StatusAccepted)
	case r.Method == http.MethodPut:
		if err := appendFile(file, r.Body); err != nil {
			dockerErr(w, 404, "BLOB_UPLOAD_UNKNOWN", err.Error())
			return
		}
		f, err := os.Open(file)
		if err != nil {
			dockerErr(w, 404, "BLOB_UPLOAD_UNKNOWN", err.Error())
			return
		}
		finish(q.Get("digest"), f)
		f.Close()
		os.Remove(file)
	case r.Method == http.MethodGet:
		if _, err := os.Stat(file); err != nil {
			dockerErr(w, 404, "BLOB_UPLOAD_UNKNOWN", "upload unknown")
			return
		}
		progress(id, http.StatusNoContent)
	case r.Method == http.MethodDelete:
		os.Remove(file)
		w.WriteHeader(http.StatusNoContent)
	default:
		dockerErr(w, 405, "UNSUPPORTED", "method not allowed")
	}
}

func appendFile(file string, r io.Reader) error {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (a *App) dockerManifest(w http.ResponseWriter, r *http.Request, repo *Repo, name, image, ref string, u *User) {
	prefix := image + "/manifests/"
	isDigest := strings.HasPrefix(ref, "sha256:")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		x, err := a.resolve(r.Context(), repo, prefix+ref, r.Header, 0)
		if err != nil {
			dockerErr(w, 404, "MANIFEST_UNKNOWN", "manifest unknown")
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:"+x.Blob)
		a.serveAsset(w, r, x)
	case http.MethodPut:
		data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			dockerErr(w, 400, "MANIFEST_INVALID", err.Error())
			return
		}
		sum := sha256.Sum256(data)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if isDigest && ref != digest {
			dockerErr(w, 400, "DIGEST_INVALID", "manifest digest mismatch")
			return
		}
		if !isDigest {
			if err := a.checkRedeploy(repo, prefix+ref); err != nil {
				dockerErr(w, 400, "DENIED", err.Error())
				return
			}
		}
		meta := assetMeta{ContentType: r.Header.Get("Content-Type"), Component: image, Uploader: u.Username}
		if _, err := a.putAsset(repo.Name, prefix+digest, bytes.NewReader(data), meta); err != nil {
			dockerErr(w, 500, "UNKNOWN", err.Error())
			return
		}
		if !isDigest {
			meta.Version = ref
			if _, err := a.putAsset(repo.Name, prefix+ref, bytes.NewReader(data), meta); err != nil {
				dockerErr(w, 500, "UNKNOWN", err.Error())
				return
			}
			a.audit(u, "docker.push", repo.Name+"/"+image+":"+ref)
		}
		w.Header().Set("Location", "/v2/"+name+"/manifests/"+digest)
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		var list []*Asset
		if isDigest { // deleting by digest removes every tag pointing at it
			list, _ = a.deleteAssets(`repo=? AND substr(path, 1, ?)=? AND blob=?`, repo.Name, len(prefix), prefix, strings.TrimPrefix(ref, "sha256:"))
		} else {
			list, _ = a.deleteAssets(`repo=? AND path=?`, repo.Name, prefix+ref)
		}
		if len(list) == 0 {
			dockerErr(w, 404, "MANIFEST_UNKNOWN", "manifest unknown")
			return
		}
		a.audit(u, "docker.delete", repo.Name+"/"+image+"@"+ref)
		w.WriteHeader(http.StatusAccepted)
	default:
		dockerErr(w, 405, "UNSUPPORTED", "method not allowed")
	}
}

// dockerTags returns nil when the image is unknown.
func (a *App) dockerTags(ctx context.Context, repo *Repo, image string, depth int) []string {
	set := map[string]bool{}
	found := false
	switch repo.Type {
	case "hosted":
		list, _ := a.assetsUnder(repo.Name, image+"/manifests/")
		for _, x := range list {
			found = true
			if x.Version != "" {
				set[x.Version] = true
			}
		}
	case "proxy":
		if x, err := a.proxyGet(ctx, repo, image+"/tags/list", nil); err == nil {
			var body struct{ Tags []string }
			if data, err := a.readAsset(x); err == nil && json.Unmarshal(data, &body) == nil {
				found = true
				for _, t := range body.Tags {
					set[t] = true
				}
			}
		}
	case "group":
		if depth > 5 {
			return nil
		}
		for _, m := range a.members(repo) {
			if tags := a.dockerTags(ctx, m, image, depth+1); tags != nil {
				found = true
				for _, t := range tags {
					set[t] = true
				}
			}
		}
	}
	if !found {
		return nil
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (a *App) dockerCatalog(w http.ResponseWriter, readable []string) {
	names := []string{}
	if len(readable) == 0 {
		writeJSON(w, 200, map[string]any{"repositories": names})
		return
	}
	rows, err := a.db.Query(`SELECT DISTINCT a.repo, a.component FROM assets a JOIN repositories r ON r.name=a.repo
		WHERE r.format='docker' AND a.component!='' AND a.path LIKE '%/manifests/%' AND a.repo IN (`+placeholders(len(readable))+`) ORDER BY 1, 2`, anySlice(readable)...)
	if err != nil {
		dockerErr(w, 500, "UNKNOWN", err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var repo, img string
		rows.Scan(&repo, &img)
		names = append(names, repo+"/"+img)
	}
	writeJSON(w, 200, map[string]any{"repositories": names})
}

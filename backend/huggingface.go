package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

// Hugging Face Hub: point HF_ENDPOINT at the repository URL. Served: model/dataset/space info
// (api/<kind>/<id>[/revision/<rev>]) and files (<id>/resolve/<rev>/<file>, with datasets/ or spaces/ in front of
// the id for those kinds). A proxy caches huggingface.co; a hosted repository stores files uploaded with
// PUT <id>/resolve/main/<file> and answers every revision with its current files (it has no git history).

var hexCommitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func hfComponent(p string) (comp, ver string) {
	if left, rest, ok := strings.Cut(p, "/resolve/"); ok {
		rev, _, _ := strings.Cut(rest, "/")
		return left, rev
	}
	return "", ""
}

func hfIsMetadata(p string) bool {
	if strings.HasPrefix(p, "api/") {
		return true
	}
	_, rest, ok := strings.Cut(p, "/resolve/")
	rev, _, _ := strings.Cut(rest, "/")
	return ok && !hexCommitRe.MatchString(rev)
}

// hfMain rewrites <id>/resolve/<rev>/<file> to the revision a hosted repository stores everything under.
func hfMain(p string) (string, bool) {
	left, rest, ok := strings.Cut(p, "/resolve/")
	_, file, ok2 := strings.Cut(rest, "/")
	if !ok || !ok2 || left == "" || file == "" {
		return "", false
	}
	return left + "/resolve/main/" + file, true
}

// hfCommit is a stable pseudo commit id for what a hosted model holds right now.
func (a *App) hfCommit(repo, id string) string {
	h := sha1.New()
	list, _ := a.assetsUnder(repo, id+"/resolve/main/")
	for _, x := range list {
		h.Write([]byte(x.Path + "\x00" + x.Blob + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (a *App) hfHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if strings.HasPrefix(p, "api/") {
			full := p
			if r.URL.RawQuery != "" {
				full += "?" + r.URL.RawQuery
			}
			data, err := a.hfInfo(r.Context(), repo, full, 0)
			if err != nil {
				resolveError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
			return
		}
		if _, ok := hfMain(p); !ok || !validPath(p) {
			http.NotFound(w, r)
			return
		}
		x, err := a.hfResolve(r.Context(), repo, p, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		_, rev := hfComponent(p)
		commit := rev
		if !hexCommitRe.MatchString(rev) {
			// upstream's commit id is on the redirect a proxy follows, so derive a stable one from the content
			sum := sha1.Sum([]byte(x.Blob))
			commit = hex.EncodeToString(sum[:])
			if holder, err := a.getRepo(x.Repo); err == nil && holder.Type == "hosted" {
				commit = a.hfCommit(x.Repo, x.Component)
			}
		}
		w.Header().Set("X-Repo-Commit", commit)
		a.serveAsset(w, r, x)
	case http.MethodPut, http.MethodPost:
		stored, ok := hfMain(p)
		if !ok || !validPath(stored) {
			http.Error(w, "upload to <org>/<model>/resolve/main/<file>", http.StatusBadRequest)
			return
		}
		if err := a.checkRedeploy(repo, stored); err != nil {
			resolveError(w, err)
			return
		}
		comp, ver := hfComponent(stored)
		if _, err := a.putAsset(repo.Name, stored, r.Body, assetMeta{Component: comp, Version: ver, Uploader: u.Username}); err != nil {
			resolveError(w, err)
			return
		}
		a.audit(u, "asset.upload", repo.Name+"/"+stored)
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if stored, ok := hfMain(p); ok {
			a.deletePath(w, r, repo, stored, u)
			return
		}
		http.NotFound(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) hfResolve(ctx context.Context, repo *Repo, p string, depth int) (*Asset, error) {
	switch repo.Type {
	case "hosted":
		stored, _ := hfMain(p)
		return a.findAsset(repo.Name, stored)
	case "proxy":
		return a.proxyGet(ctx, repo, p, nil)
	case "group":
		if depth <= 5 {
			for _, m := range a.members(repo) {
				if x, err := a.hfResolve(ctx, m, p, depth+1); err == nil {
					return x, nil
				}
			}
		}
	}
	return nil, errNotFound
}

// hfInfo answers api/<kind>/<id>[/revision/<rev>] (p may carry a query string, which only a proxy passes on).
func (a *App) hfInfo(ctx context.Context, repo *Repo, p string, depth int) ([]byte, error) {
	switch repo.Type {
	case "proxy":
		x, err := a.proxyGet(ctx, repo, p, nil)
		if err != nil {
			return nil, err
		}
		return a.readAsset(x)
	case "group":
		if depth <= 5 {
			for _, m := range a.members(repo) {
				if data, err := a.hfInfo(ctx, m, p, depth+1); err == nil {
					return data, nil
				}
			}
		}
		return nil, errNotFound
	}
	path, _, _ := strings.Cut(p, "?")
	kind, id, ok := strings.Cut(strings.TrimPrefix(path, "api/"), "/")
	id, _, _ = strings.Cut(id, "/revision/")
	if !ok || id == "" || (kind != "models" && kind != "datasets" && kind != "spaces") {
		return nil, errNotFound
	}
	key := id
	if kind != "models" {
		key = kind + "/" + id
	}
	list, err := a.assetsUnder(repo.Name, key+"/resolve/main/")
	if err != nil || len(list) == 0 {
		return nil, errNotFound
	}
	sib := []map[string]string{}
	for _, x := range list {
		sib = append(sib, map[string]string{"rfilename": strings.TrimPrefix(x.Path, key+"/resolve/main/")})
	}
	return json.Marshal(map[string]any{"id": id, "modelId": id, "sha": a.hfCommit(repo.Name, key), "private": false, "siblings": sib, "tags": []string{}})
}

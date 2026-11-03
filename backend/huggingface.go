package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var hfLinkHostRe = regexp.MustCompile(`<https?://[^/>]+/`)

// hfNotFound answers a missing item the way the Hub does: huggingface_hub tells a missing optional file
// (EntryNotFound, which transformers expects and skips) from a missing repository by the X-Error-Code header.
func hfNotFound(w http.ResponseWriter, p string, err error) {
	if errors.Is(err, errNotFound) {
		code := "EntryNotFound"
		if strings.HasPrefix(p, "api/") {
			code = "RepoNotFound"
		}
		w.Header().Set("X-Error-Code", code)
		w.Header().Set("X-Error-Message", "not found")
	}
	resolveError(w, err)
}

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
			data, link, err := a.hfInfo(r.Context(), repo, full, 0)
			if err != nil {
				hfNotFound(w, p, err)
				return
			}
			if link != "" { // listings are paged: next pages must come back through this proxy, not go to the Hub
				w.Header().Set("Link", hfLinkHostRe.ReplaceAllLiteralString(link, "<"+a.baseURL(r)+"/repository/"+repo.Name+"/"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
			return
		}
		if _, ok := hfMain(p); !ok || !validPath(p) {
			hfNotFound(w, p, errNotFound)
			return
		}
		_, rev := hfComponent(p)
		sctx, sink := streamTo(w, r)
		sink.needCommit = true
		if hexCommitRe.MatchString(rev) {
			sink.hdr.Set("X-Repo-Commit", rev)
		}
		x, err := a.hfResolve(sctx, repo, p, 0)
		if sink.handled(err) {
			return
		}
		if err != nil {
			hfNotFound(w, p, err)
			return
		}
		commit := rev
		if !hexCommitRe.MatchString(rev) {
			// upstream's commit id (kept with the cached file), else a stable one derived from the content
			sum := sha1.Sum([]byte(x.Blob))
			commit = hex.EncodeToString(sum[:])
			if c := upstreamCommitOf(x); c != "" {
				commit = c
			}
			if holder, err := a.getRepo(x.Repo); err == nil && holder.Type == "hosted" {
				commit = a.hfCommit(x.Repo, x.Component)
			}
		}
		w.Header().Set("X-Repo-Commit", commit)
		a.serveAsset(w, r, x)
	case http.MethodPut, http.MethodPost:
		if r.Method == http.MethodPost && strings.HasPrefix(p, "api/") { // paths-info: file details for a list of paths
			body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
			if err == nil {
				var data []byte
				if data, err = a.hfPost(r.Context(), repo, p, body, r.Header.Get("Content-Type"), 0); err == nil {
					w.Header().Set("Content-Type", "application/json")
					w.Write(data)
					return
				}
			}
			hfNotFound(w, p, err)
			return
		}
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
		return a.hfProxyGet(ctx, repo, p)
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

// upstreamAttr is a header the upstream sent when a proxy cached the file (upstreamCommit, upstreamEtag,
// upstreamLink); "" if there was none.
func upstreamAttr(x *Asset, key string) string {
	var at map[string]string
	json.Unmarshal(x.Attrs, &at)
	return at[key]
}

func upstreamCommitOf(x *Asset) string { return upstreamAttr(x, "upstreamCommit") }

// hfPost forwards a read-only POST of the Hub API (paths-info) to the upstream of a proxy; answers are not cached.
func (a *App) hfPost(ctx context.Context, repo *Repo, p string, body []byte, ctype string, depth int) ([]byte, error) {
	switch repo.Type {
	case "proxy":
		u, err := a.upstreamURL(ctx, repo, p)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "RepoMan/"+version)
		req.Header.Set("Content-Type", ctype)
		if repo.RemotePassword != "" {
			req.Header.Set("Authorization", "Bearer "+repo.RemotePassword)
		}
		resp, err := a.client.Do(req)
		if err != nil {
			a.upstreamFailed(repo.Name, err)
			return nil, err
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		case http.StatusNotFound, http.StatusGone:
			return nil, errNotFound
		}
		err = upstreamStatus{u, resp.StatusCode, resp.Status}
		a.upstreamFailed(repo.Name, err)
		return nil, err
	case "group":
		if depth <= 5 {
			for _, m := range a.members(repo) {
				if data, err := a.hfPost(ctx, m, p, body, ctype, depth+1); err == nil {
					return data, nil
				}
			}
		}
	}
	return nil, errNotFound
}

// hfProxyGet fetches a file through a proxy. The client first asks for <id>/resolve/main/<file> and then for the same
// file pinned to the commit it learned from that answer: the pinned request is satisfied from the branch download
// (running or finished) instead of fetching the file a second time.
func (a *App) hfProxyGet(ctx context.Context, repo *Repo, p string) (*Asset, error) {
	left, rest, _ := strings.Cut(p, "/resolve/")
	rev, file, _ := strings.Cut(rest, "/")
	if !hexCommitRe.MatchString(rev) || hfIsMetadata(p) {
		return a.proxyGet(ctx, repo, p, nil)
	}
	if _, err := a.findAsset(repo.Name, p); err == nil {
		return a.proxyGet(ctx, repo, p, nil)
	}
	main := left + "/resolve/main/" + file
	link := func(src *Asset) (*Asset, error) {
		return a.linkAsset(repo.Name, p, src, assetMeta{ContentType: src.ContentType, Component: left, Version: rev, Uploader: "proxy"})
	}
	if src, err := a.findAsset(repo.Name, main); err == nil && upstreamCommitOf(src) == rev {
		return link(src)
	}
	a.liveMu.Lock()
	d := a.live[repo.Name+"\x00"+main]
	a.liveMu.Unlock()
	if d != nil {
		if s, _ := ctx.Value(streamKey{}).(*streamSink); s != nil && !s.started {
			if d.waitHeaders(ctx) == nil {
				d.mu.Lock()
				same := d.commit == rev
				d.mu.Unlock()
				if same {
					x, err := a.serveLive(s, d)
					if err == nil && x.ID != 0 {
						return link(x)
					}
					return x, err
				}
			}
		}
	}
	return a.proxyGet(ctx, repo, p, nil)
}

// hfInfo answers api/<kind>/<id>[/revision/<rev>] (p may carry a query string, which only a proxy passes on).
func (a *App) hfInfo(ctx context.Context, repo *Repo, p string, depth int) (data []byte, link string, err error) {
	switch repo.Type {
	case "proxy":
		x, err := a.proxyGet(ctx, repo, p, nil)
		if err != nil {
			return nil, "", err
		}
		data, err = a.readAsset(x)
		return data, upstreamAttr(x, "upstreamLink"), err
	case "group":
		if depth <= 5 {
			for _, m := range a.members(repo) {
				if data, link, err := a.hfInfo(ctx, m, p, depth+1); err == nil {
					return data, link, nil
				}
			}
		}
		return nil, "", errNotFound
	}
	path, _, _ := strings.Cut(p, "?")
	kind, id, ok := strings.Cut(strings.TrimPrefix(path, "api/"), "/")
	id, _, _ = strings.Cut(id, "/revision/")
	if !ok || id == "" || (kind != "models" && kind != "datasets" && kind != "spaces") {
		return nil, "", errNotFound
	}
	key := id
	if kind != "models" {
		key = kind + "/" + id
	}
	list, err := a.assetsUnder(repo.Name, key+"/resolve/main/")
	if err != nil || len(list) == 0 {
		return nil, "", errNotFound
	}
	sib := []map[string]string{}
	for _, x := range list {
		sib = append(sib, map[string]string{"rfilename": strings.TrimPrefix(x.Path, key+"/resolve/main/")})
	}
	data, err = json.Marshal(map[string]any{"id": id, "modelId": id, "sha": a.hfCommit(repo.Name, key), "private": false, "siblings": sib, "tags": []string{}})
	return data, "", err
}

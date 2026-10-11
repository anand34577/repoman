package main

import (
	"encoding/json"
	"net/http"
	"regexp"
)

// Git LFS server (basic transfer): clients POST objects/batch, then PUT/GET objects/<oid>. Set
// lfs.url to the repository URL (or put it in .lfsconfig). Hosted only; objects are content-addressed,
// so the same file pushed from many repositories is stored once.

var lfsOidRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func lfsPath(oid string) string { return "objects/" + oid[:2] + "/" + oid[2:4] + "/" + oid }

func (a *App) lfsHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	base := a.baseURL(r) + "/repository/" + repo.Name
	oid := ""
	if len(p) == len("objects/")+64 && lfsOidRe.MatchString(p[len("objects/"):]) {
		oid = p[len("objects/"):]
	}
	switch {
	case r.Method == http.MethodPost && p == "objects/batch":
		var req struct {
			Operation string
			Objects   []struct {
				Oid  string
				Size int64
			}
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req) != nil || (req.Operation != "upload" && req.Operation != "download") {
			http.Error(w, "invalid batch request", 422)
			return
		}
		upload := req.Operation == "upload"
		if upload { // batch is a POST that handleRepo lets readers make: uploading still needs write access
			if !a.can(u, repo, true) {
				deny(w, u)
				return
			}
			if err := a.checkWritable(repo); err != nil {
				resolveError(w, err)
				return
			}
		}
		objs := []map[string]any{}
		for _, o := range req.Objects {
			e := map[string]any{"oid": o.Oid, "size": o.Size, "authenticated": true}
			_, err := a.findAsset(repo.Name, lfsPath(o.Oid))
			switch {
			case !lfsOidRe.MatchString(o.Oid):
				e["error"] = map[string]any{"code": 422, "message": "invalid object id"}
			case upload && err != nil:
				e["actions"] = map[string]any{
					"upload": map[string]any{"href": base + "/objects/" + o.Oid},
					"verify": map[string]any{"href": base + "/verify"},
				}
			case !upload && err == nil:
				e["actions"] = map[string]any{"download": map[string]any{"href": base + "/objects/" + o.Oid}}
			case !upload:
				e["error"] = map[string]any{"code": 404, "message": "object does not exist"}
			} // an upload of a stored object gets no actions: nothing to send
			objs = append(objs, e)
		}
		w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
		json.NewEncoder(w).Encode(map[string]any{"transfer": "basic", "hash_algo": "sha256", "objects": objs})
	case r.Method == http.MethodPost && p == "verify":
		var o struct {
			Oid  string
			Size int64
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&o) != nil || !lfsOidRe.MatchString(o.Oid) {
			http.Error(w, "invalid request", 422)
			return
		}
		if x, err := a.findAsset(repo.Name, lfsPath(o.Oid)); err != nil || x.Size != o.Size {
			http.Error(w, "object missing or wrong size", 404)
			return
		}
		w.WriteHeader(http.StatusOK)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && oid != "":
		x, err := a.findAsset(repo.Name, lfsPath(oid))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		a.serveAsset(w, r, x)
	case r.Method == http.MethodPut && oid != "":
		if _, err := a.putAsset(repo.Name, lfsPath(oid), r.Body, assetMeta{Uploader: u.Username, WantSHA: oid}); err != nil {
			resolveError(w, err)
			return
		}
		a.audit(u, "lfs.upload", repo.Name+"/"+oid)
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "not a Git LFS path", http.StatusNotFound)
	}
}

package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func jsonString(v any) string { b, _ := json.Marshal(v); return string(b) }

// placeholders returns "?,?,?" for n args (n >= 1).
func placeholders(n int) string { return "?" + strings.Repeat(",?", n-1) }

func anySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// pageParams reads ?offset= and ?limit= for list endpoints; limit defaults to def and is capped at 1000.
func pageParams(r *http.Request, def int) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	if limit <= 0 {
		limit = def
	}
	return min(limit, 1000), max(offset, 0)
}

func setTotal(w http.ResponseWriter, n int) { w.Header().Set("X-Total-Count", strconv.Itoa(n)) }

// maxCount caps match counts so totals stay cheap on tables with millions of rows; clients show "10,000+".
const maxCount = 10000

// setCappedTotal counts the rows of a "FROM … WHERE …" clause, stopping at maxCount+1.
func (a *App) setCappedTotal(w http.ResponseWriter, from string, args ...any) error {
	var n int
	err := a.db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 `+from+` LIMIT `+strconv.Itoa(maxCount+1)+`)`, args...).Scan(&n)
	setTotal(w, n)
	return err
}

// setPageTotal sets X-Total-Count for a page that was just read. A page that came back short is the
// last one, so the total is known without scanning the table a second time to count.
func (a *App) setPageTotal(w http.ResponseWriter, got, limit, offset int, from string, args ...any) error {
	if got < limit && (got > 0 || offset == 0) {
		setTotal(w, min(offset+got, maxCount+1))
		return nil
	}
	return a.setCappedTotal(w, from, args...)
}

// ---------- repositories ----------

type repoView struct {
	*Repo
	AssetCount int64        `json:"assetCount"`
	Size       int64        `json:"size"`
	Downloads  int64        `json:"downloads"`
	Status     string       `json:"status"` // online, offline, degraded
	Health     *proxyHealth `json:"health,omitempty"`
	Access     string       `json:"access"` // caller's level: read or write
}

var repoNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

var (
	distributionRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)
	goUploadRe     = regexp.MustCompile(`/@v/v[^/]+\.(zip|mod|info)$`)
)

func redact(r *Repo) *Repo {
	c := *r
	c.RemotePassword = ""
	return &c
}

// repoFor loads the {name} repository and checks the caller may read (or write) it.
func (a *App) repoFor(r *http.Request, u *User, write bool) (*Repo, error) {
	repo, err := a.getRepo(r.PathValue("name"))
	if err != nil || !a.can(u, repo, false) {
		return nil, errNotFound
	}
	if write && !a.can(u, repo, true) {
		return nil, httpErr{403, "you don't have write access to " + repo.Name}
	}
	return repo, nil
}

func (a *App) view(u *User, repo *Repo, stats map[string]repoView) repoView {
	v := stats[repo.Name]
	v.Repo = redact(repo)
	v.Status, v.Health = a.status(repo)
	v.Access = a.access(u, repo)
	return v
}

// repoStats returns artifact count, bytes and downloads per repository. Callers must not modify the map.
func (a *App) repoStats() (map[string]repoView, error) {
	return a.repoTotals.get("", a.assetGen.Load(), func() (map[string]repoView, error) {
		stats := map[string]repoView{}
		rows, err := a.db.Query(`SELECT repo, COUNT(*), COALESCE(SUM(size),0), COALESCE(SUM(downloads),0) FROM assets GROUP BY repo`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var v repoView
			if err := rows.Scan(&name, &v.AssetCount, &v.Size, &v.Downloads); err != nil {
				return nil, err
			}
			stats[name] = v
		}
		return stats, rows.Err()
	})
}

// repoIn restricts a query to the repositories a caller may read: " AND <col> IN (…)" with its arguments.
// When that is every repository the clause is left out, so the planner is free to walk an ORDER BY index
// instead of visiting each repository in turn.
func repoIn(col string, repos []string, all bool) (string, []any) {
	switch {
	case all:
		return "", nil
	case len(repos) == 0:
		return " AND 0", nil
	}
	return " AND " + col + " IN (" + placeholders(len(repos)) + ")", anySlice(repos)
}

// storedTotals counts the distinct content files behind the given repositories and their bytes on disk.
func (a *App) storedTotals(repos []string, all bool) (files, bytes int64, err error) {
	in, args := repoIn("repo", repos, all)
	key := "*" // every repository
	if !all {
		key = strings.Join(repos, "\x00")
	}
	v, err := a.storedBytes.get(key, a.assetGen.Load(), func() (v [2]int64, err error) {
		err = a.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size),0) FROM (SELECT MAX(size) AS size FROM assets WHERE 1`+in+` GROUP BY blob)`, args...).Scan(&v[0], &v[1])
		return v, err
	})
	return v[0], v[1], err
}

func (a *App) listReposAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repos, err := a.listRepos()
	if err != nil {
		return nil, err
	}
	stats, err := a.repoStats()
	if err != nil {
		return nil, err
	}
	out := []repoView{}
	for _, repo := range repos {
		if a.can(u, repo, false) {
			out = append(out, a.view(u, repo, stats))
		}
	}
	return out, nil
}

func (a *App) getRepoAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.repoFor(r, u, false)
	if err != nil {
		return nil, err
	}
	stats := map[string]repoView{}
	var v repoView
	a.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(size),0), COALESCE(SUM(downloads),0) FROM assets WHERE repo=?`, repo.Name).Scan(&v.AssetCount, &v.Size, &v.Downloads)
	stats[repo.Name] = v
	return a.view(u, repo, stats), nil
}

func (a *App) validateRepo(r *Repo) error {
	if !repoNameRe.MatchString(r.Name) {
		return httpErr{400, "name must start with a letter or digit and contain only lowercase letters, digits, '.', '_' or '-'"}
	}
	if !slices.Contains(formats, r.Format) {
		return httpErr{400, "unsupported format " + r.Format}
	}
	switch r.Type {
	case "hosted":
		r.RemoteURL, r.RemoteUsername, r.RemotePassword, r.Members = "", "", "", nil
		if r.Format == "apt" {
			if r.Distribution == "" {
				r.Distribution = "stable"
			}
			if !distributionRe.MatchString(r.Distribution) {
				return httpErr{400, "distribution must be lowercase, e.g. stable or bookworm"}
			}
		}
	case "proxy":
		u, err := url.Parse(r.RemoteURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return httpErr{400, "proxy repositories need a valid http(s) remote URL"}
		}
		r.Members, r.QuotaMB = nil, 0
	case "group":
		if r.Format == "apt" || r.Format == "yum" {
			return httpErr{400, r.Format + " repositories can't be grouped: their signed indexes can't be merged. Configure clients with several sources instead."}
		}
		if r.Format == "conda" {
			return httpErr{400, "conda repositories can't be grouped: public channel indexes are too large to merge (conda-forge's noarch index alone is about 29 MB compressed). List several channels in .condarc instead (conda searches them in order)."}
		}
		r.RemoteURL, r.RemoteUsername, r.RemotePassword, r.QuotaMB, r.CleanupDays, r.RetainVersions = "", "", "", 0, 0, 0
		if len(r.Members) == 0 {
			return httpErr{400, "a group needs at least one member"}
		}
		for _, m := range r.Members {
			mr, err := a.getRepo(m)
			if err != nil || m == r.Name || mr.Format != r.Format {
				return httpErr{400, "invalid group member: " + m}
			}
		}
	default:
		return httpErr{400, "type must be hosted, proxy or group"}
	}
	if r.Format != "apt" || r.Type != "hosted" {
		r.Distribution = ""
	}
	if r.MetadataTTL < 0 || r.CleanupDays < 0 || r.RetainVersions < 0 || r.QuotaMB < 0 {
		return httpErr{400, "numeric settings must not be negative"}
	}
	return nil
}

func (a *App) createRepoAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var repo Repo
	if err := readJSON(r, &repo); err != nil {
		return nil, err
	}
	if _, err := a.getRepo(repo.Name); err == nil {
		return nil, httpErr{409, "a repository named " + repo.Name + " already exists"}
	}
	if err := a.validateRepo(&repo); err != nil {
		return nil, err
	}
	repo.CreatedAt, repo.InvalidatedAt = time.Now().Unix(), 0
	if err := a.saveRepo(&repo); err != nil {
		return nil, err
	}
	if err := a.reindex(repo.Name); err != nil { // empty apt/yum indexes so clients can connect right away
		return nil, err
	}
	a.audit(u, "repository.create", repo.Name, repo.Format, repo.Type)
	return redact(&repo), nil
}

func (a *App) updateRepoAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	old, err := a.getRepo(r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	var repo Repo
	if err := readJSON(r, &repo); err != nil {
		return nil, err
	}
	// name, format and type are fixed after creation
	repo.Name, repo.Format, repo.Type, repo.CreatedAt, repo.InvalidatedAt = old.Name, old.Format, old.Type, old.CreatedAt, old.InvalidatedAt
	if repo.RemotePassword == "" && repo.RemoteUsername == old.RemoteUsername {
		repo.RemotePassword = old.RemotePassword // blank means "unchanged"
	}
	if err := a.validateRepo(&repo); err != nil {
		return nil, err
	}
	if err := a.saveRepo(&repo); err != nil {
		return nil, err
	}
	if repo.Distribution != old.Distribution {
		a.reindex(repo.Name)
	}
	a.audit(u, "repository.update", repo.Name)
	return redact(&repo), nil
}

func (a *App) deleteRepoAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.getRepo(r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	list, err := a.deleteAssets("repo=?", repo.Name)
	if err != nil {
		return nil, err
	}
	if _, err := a.db.Exec(`DELETE FROM repositories WHERE name=?`, repo.Name); err != nil {
		return nil, err
	}
	all, _ := a.listRepos()
	for _, g := range all { // drop from groups
		if i := slices.Index(g.Members, repo.Name); i >= 0 {
			g.Members = slices.Delete(g.Members, i, i+1)
			a.saveRepo(g)
		}
	}
	a.health.Delete(repo.Name)
	a.audit(u, "repository.delete", repo.Name, len(list), "assets")
	return nil, nil
}

func (a *App) testRemoteAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var repo Repo
	if err := readJSON(r, &repo); err != nil {
		return nil, err
	}
	if repo.RemotePassword == "" && repo.Name != "" { // testing a saved repo with an unchanged password
		if old, err := a.getRepo(repo.Name); err == nil && old.RemoteUsername == repo.RemoteUsername {
			repo.RemotePassword = old.RemotePassword
		}
	}
	if pu, err := url.Parse(repo.RemoteURL); err != nil || pu.Host == "" {
		return remoteTest{Message: "Enter a complete URL such as https://repo.example.com"}, nil
	}
	res := a.testRemote(r.Context(), &repo)
	if repo.Name != "" {
		if _, err := a.getRepo(repo.Name); err == nil {
			a.recordHealth(repo.Name, map[bool]error{false: errString(res.Message)}[res.OK])
		}
	}
	return res, nil
}

type errString string

func (e errString) Error() string { return string(e) }

type browseEntry struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Folder bool   `json:"folder"`
	Count  int    `json:"count,omitempty"` // assets under a folder
	Size   int64  `json:"size,omitempty"`  // bytes under a folder
	Asset  *Asset `json:"asset,omitempty"`
}

// browseAPI lists one folder: subfolders first, then files, each by name. Folders are folded in SQL, so a
// folder holding a million artifacts returns one page without loading them; ?q= filters names, ?offset=/?limit= page.
func (a *App) browseAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.repoFor(r, u, false)
	if err != nil {
		return nil, err
	}
	prefix := strings.Trim(r.URL.Query().Get("path"), "/")
	if prefix != "" {
		prefix += "/"
	}
	filter := strings.ToLower(r.URL.Query().Get("q"))
	limit, offset := pageParams(r, 1000)
	under, uargs := prefixWhere(prefix)
	start := utf8.RuneCountInString(prefix) + 1 // substr counts characters, not bytes
	rows, err := a.db.Query(`SELECT substr(rest, 1, instr(rest, '/')-1) AS dir, COUNT(*), SUM(size)
		FROM (SELECT substr(path, ?) AS rest, size FROM assets WHERE repo=?`+under+`)
		WHERE instr(rest, '/') > 0 GROUP BY dir ORDER BY dir`, append([]any{start, repo.Name}, uargs...)...)
	if err != nil {
		return nil, err
	}
	folders := []*browseEntry{}
	for rows.Next() {
		e := &browseEntry{Folder: true}
		rows.Scan(&e.Name, &e.Count, &e.Size)
		if strings.Contains(strings.ToLower(e.Name), filter) {
			e.Path = prefix + e.Name
			folders = append(folders, e)
		}
	}
	rows.Close()

	files := `WHERE repo=?` + under + ` AND instr(substr(path, ?), '/')=0 AND substr(path, ?) LIKE ? ESCAPE '\'`
	fargs := append(append([]any{repo.Name}, uargs...), start, start, "%"+likeEscape(filter)+"%")
	var nfiles int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM assets a `+files, fargs...).Scan(&nfiles); err != nil {
		return nil, err
	}
	setTotal(w, len(folders)+nfiles)

	out := folders[min(offset, len(folders)):min(offset+limit, len(folders))]
	if room := limit - len(out); room > 0 {
		list, err := a.queryAssets(files+` ORDER BY path LIMIT ? OFFSET ?`, append(fargs, room, max(0, offset-len(folders)))...)
		if err != nil {
			return nil, err
		}
		for _, x := range list {
			out = append(out, &browseEntry{Name: x.Path[len(prefix):], Path: x.Path, Asset: x})
		}
	}
	return out, nil
}

// assetAPI returns one artifact by path, for deep links into folders too large for one page.
func (a *App) assetAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.repoFor(r, u, false)
	if err != nil {
		return nil, err
	}
	return a.findAsset(repo.Name, r.URL.Query().Get("path"))
}

type versionView struct {
	Version   string `json:"version"`
	Assets    int    `json:"assets"`
	Size      int64  `json:"size"`
	Downloads int64  `json:"downloads"`
	UpdatedAt int64  `json:"updatedAt"`
}

type componentView struct {
	Name      string        `json:"name"`
	Versions  []versionView `json:"versions"`
	Assets    int           `json:"assets"`
	Size      int64         `json:"size"`
	Downloads int64         `json:"downloads"`
	UpdatedAt int64         `json:"updatedAt"`
}

var componentSort = map[string]string{
	"name": "component", "updated": "MAX(updated_at) DESC", "downloads": "SUM(downloads) DESC", "size": "SUM(size) DESC",
}

// componentsAPI groups a repository's assets by component and version, one page of components at a time.
func (a *App) componentsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.repoFor(r, u, false)
	if err != nil {
		return nil, err
	}
	limit, offset := pageParams(r, 1000)
	order := componentSort[r.URL.Query().Get("sort")]
	if order == "" {
		order = "component"
	}
	if order != "component" {
		order += ", component"
	}
	where := `WHERE repo=? AND component!='' AND uploader!='system' AND component LIKE ? ESCAPE '\'`
	args := []any{repo.Name, "%" + likeEscape(r.URL.Query().Get("q")) + "%"}
	var total int
	if err := a.db.QueryRow(`SELECT COUNT(DISTINCT component) FROM assets `+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	setTotal(w, total)
	rows, err := a.db.Query(`SELECT component, COUNT(*), SUM(size), SUM(downloads), MAX(updated_at) FROM assets `+where+`
		GROUP BY component ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	out := []*componentView{}
	byName := map[string]*componentView{}
	names := []any{}
	for rows.Next() {
		c := &componentView{Versions: []versionView{}}
		rows.Scan(&c.Name, &c.Assets, &c.Size, &c.Downloads, &c.UpdatedAt)
		out = append(out, c)
		byName[c.Name] = c
		names = append(names, c.Name)
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}
	rows, err = a.db.Query(`SELECT component, version, COUNT(*), SUM(size), SUM(downloads), MAX(updated_at) FROM assets
		WHERE repo=? AND uploader!='system' AND version!='' AND component IN (`+placeholders(len(names))+`)
		GROUP BY component, version ORDER BY component, MAX(created_at) DESC`, append([]any{repo.Name}, names...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var v versionView
		rows.Scan(&name, &v.Version, &v.Assets, &v.Size, &v.Downloads, &v.UpdatedAt)
		byName[name].Versions = append(byName[name].Versions, v)
	}
	return out, rows.Err()
}

func (a *App) deleteComponentAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.repoFor(r, u, true)
	if err != nil {
		return nil, err
	}
	comp, ver := r.URL.Query().Get("component"), r.URL.Query().Get("version")
	if comp == "" {
		return nil, httpErr{400, "component is required"}
	}
	var list []*Asset
	if ver == "" {
		list, err = a.removeAssets(`repo=? AND component=?`, repo.Name, comp)
	} else {
		list, err = a.removeAssets(`repo=? AND component=? AND version=?`, repo.Name, comp, ver)
	}
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errNotFound
	}
	a.audit(u, "component.delete", repo.Name+"/"+comp, ver, len(list), "assets")
	return map[string]int{"deleted": len(list)}, nil
}

// uploadAPI accepts browser uploads for hosted repositories of path-based or package-file formats.
func (a *App) uploadAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.repoFor(r, u, true)
	if err != nil {
		return nil, err
	}
	if repo.Type != "hosted" || !slices.Contains([]string{"raw", "maven", "apt", "yum", "go", "conda"}, repo.Format) {
		return nil, httpErr{400, "browser upload isn't available for " + repo.Format + " " + repo.Type + " repositories; use the native client shown under Setup"}
	}
	if err := a.checkWritable(repo); err != nil {
		return nil, err
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return nil, httpErr{400, "invalid upload"}
	}
	defer r.MultipartForm.RemoveAll()
	dir := strings.Trim(r.FormValue("path"), "/")
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		return nil, httpErr{400, "no files selected"}
	}
	stored := []string{}
	// apt and yum indexes are rebuilt once for the whole batch, also when a later file is rejected
	defer func() {
		if len(stored) > 0 && (repo.Format == "apt" || repo.Format == "yum") {
			if err := a.reindex(repo.Name); err != nil {
				log.Printf("upload to %s: rebuilding the index failed: %v", repo.Name, err)
			}
		}
	}()
	for _, fh := range files {
		name := path.Base(fh.Filename)
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		var x *Asset
		switch repo.Format {
		case "apt":
			x, err = a.storeDeb(repo, f, u)
		case "yum":
			target := ""
			if dir != "" {
				target = path.Join(dir, name)
			}
			x, err = a.storeRPM(repo, f, target, u)
		case "conda":
			x, err = a.ingestConda(repo, f, "", u) // subdir and file name come from the package itself
		default:
			p := path.Join(dir, name)
			if !validPath(p) {
				err = httpErr{400, "invalid path: " + p}
				break
			}
			if repo.Format == "go" && !goUploadRe.MatchString(p) {
				err = httpErr{400, "Go modules are uploaded as <module>/@v/<version>.zip and .mod (directory <module>/@v)"}
				break
			}
			if err = a.checkRedeploy(repo, p); err != nil {
				break
			}
			comp, ver := componentOf(repo.Format, p)
			x, err = a.putAsset(repo.Name, p, f, assetMeta{Component: comp, Version: ver, Uploader: u.Username})
			if err == nil {
				a.audit(u, "asset.upload", repo.Name+"/"+p)
			}
		}
		f.Close()
		if err != nil {
			code := 400
			var he httpErr
			if errors.As(err, &he) {
				code = he.code
			}
			msg := name + ": " + err.Error()
			if len(stored) > 0 {
				msg += fmt.Sprintf(" (%d earlier file(s) in this upload were stored)", len(stored))
			}
			return nil, httpErr{code, msg}
		}
		stored = append(stored, x.Path)
	}
	return map[string]any{"uploaded": stored}, nil
}

func (a *App) cleanupAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.getRepo(r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	if repo.CleanupDays <= 0 && repo.RetainVersions <= 0 {
		return nil, httpErr{400, "no cleanup policy configured for this repository"}
	}
	if r.URL.Query().Get("dryRun") != "" {
		list, err := a.cleanupCandidates(repo)
		if err != nil {
			return nil, err
		}
		var size int64
		for _, x := range list {
			size += x.Size
		}
		return map[string]any{"count": len(list), "size": size, "sample": append([]*Asset{}, list[:min(50, len(list))]...)}, nil
	}
	n, size, err := a.cleanup(repo)
	if err != nil {
		return nil, err
	}
	a.audit(u, "cleanup", repo.Name, n, "assets")
	return map[string]any{"deleted": n, "size": size}, nil
}

// invalidateAPI marks all cached proxy metadata stale and forgets cached "not found" answers.
func (a *App) invalidateAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.getRepo(r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	if repo.Type != "proxy" {
		return nil, httpErr{400, "only proxy repositories have a cache"}
	}
	// index data cached up to this second counts as stale (see proxyGet); the files keep their dates
	repo.InvalidatedAt = time.Now().Unix() + 1
	if err := a.saveRepo(repo); err != nil {
		return nil, err
	}
	a.negCache.Range(func(k, _ any) bool {
		if strings.HasPrefix(k.(string), repo.Name+"\x00") {
			a.negCache.Delete(k)
		}
		return true
	})
	if v, ok := a.health.Load(repo.Name); ok { // and try the upstream again right away
		a.mu.Lock()
		v.(*proxyHealth).downSince = time.Time{}
		a.mu.Unlock()
	}
	a.audit(u, "cache.invalidate", repo.Name)
	return nil, nil
}

func (a *App) reindexAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.getRepo(r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	if err := a.reindex(repo.Name); err != nil {
		return nil, err
	}
	a.audit(u, "repository.reindex", repo.Name)
	return nil, nil
}

func (a *App) getPermsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	rows, err := a.db.Query(`SELECT repo, username, level FROM repo_perms WHERE repo=? ORDER BY username`, r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []permRow{}
	for rows.Next() {
		var p permRow
		rows.Scan(&p.Repo, &p.Username, &p.Level)
		out = append(out, p)
	}
	return out, nil
}

// putPermsAPI replaces the repository's per-user overrides.
func (a *App) putPermsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.getRepo(r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	var perms []permRow
	if err := readJSON(r, &perms); err != nil {
		return nil, err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	tx.Exec(`DELETE FROM repo_perms WHERE repo=?`, repo.Name)
	for _, p := range perms {
		if !slices.Contains([]string{accessNone, accessRead, accessWrite}, p.Level) {
			return nil, httpErr{400, "level must be none, read or write"}
		}
		if _, err := tx.Exec(`INSERT INTO repo_perms(repo, username, level) VALUES(?,?,?)`, repo.Name, p.Username, p.Level); err != nil {
			return nil, httpErr{400, "unknown user " + p.Username}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	a.audit(u, "repository.permissions", repo.Name, len(perms), "overrides")
	return nil, nil
}

// ---------- assets / search / stats ----------

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

// Every order ends in the row id, so pages are stable, and is served by an index (see migrations).
var searchSort = map[string]string{
	"updated": "a.updated_at DESC, a.id DESC", "name": "a.component, a.path, a.id",
	"size": "a.size DESC, a.id DESC", "downloads": "a.downloads DESC, a.id DESC",
}

// sparseMatches is the number of matches up to which a search reads them all from the index and sorts
// them. A term that matches more is common enough for a scan in the requested order to fill a page quickly.
const sparseMatches = 2000

// indexedTerm reports whether the trigram index can answer a substring search for term: it needs three
// characters, and SQLite only hands a LIKE without ESCAPE to the index.
func indexedTerm(term string) bool {
	return utf8.RuneCountInString(term) >= 3 && !strings.ContainsAny(term, "%_\\\n")
}

func (a *App) searchAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	q := r.URL.Query()
	repos, all := a.readable(u)
	if len(repos) == 0 {
		setTotal(w, 0)
		return []*Asset{}, nil
	}
	limit, offset := pageParams(r, 500)
	order := searchSort[q.Get("sort")]
	if order == "" {
		order = searchSort["updated"]
	}
	like := "%" + likeEscape(q.Get("q")) + "%"
	in, inArgs := repoIn("a.repo", repos, all)
	args := append([]any{like, q.Get("repo"), q.Get("format")}, inArgs...)
	// One repository: a small one is read through its own index and sorted; in a large one, walking
	// the requested order and skipping other repositories fills a page sooner.
	repoIs := ` AND (?2='' OR a.repo=?2)`
	if stats, err := a.repoStats(); err == nil && q.Get("repo") != "" && stats[q.Get("repo")].AssetCount < 20000 {
		repoIs = ` AND a.repo=?2`
	}
	filters := repoIs + ` AND (?3='' OR r.format=?3) AND a.path NOT LIKE '%/blobs/sha256:%' AND a.uploader!='system'` + in
	// Without the index: scan assets in the requested order until the page is full.
	where := `JOIN repositories r ON r.name=a.repo
		WHERE (?1='%%' OR a.path LIKE ?1 ESCAPE '\' OR a.component LIKE ?1 ESCAPE '\')` + filters
	count := "FROM assets a " + where
	if a.searchIndex && indexedTerm(q.Get("q")) {
		// With it: read matches straight from the substring index (CROSS JOIN pins it as the driving table).
		count = `FROM assets_fts f CROSS JOIN assets a ON a.id=f.rowid JOIN repositories r ON r.name=a.repo WHERE f.text LIKE ?1` + filters
		ids, err := a.assetIDs(`SELECT a.id `+count+` LIMIT ?`, append(args, sparseMatches+1)...)
		if err != nil {
			return nil, err
		}
		if len(ids) <= sparseMatches { // few matches: that is all of them; sort them and cut out the page
			setTotal(w, len(ids))
			if len(ids) == 0 {
				return []*Asset{}, nil
			}
			list, err := a.queryAssets(`WHERE a.id IN (`+placeholders(len(ids))+`) ORDER BY `+order+` LIMIT ? OFFSET ?`, append(ids, limit, offset)...)
			if list == nil {
				list = []*Asset{}
			}
			return list, err
		}
		// a common term: the scan below fills a page quickly, and the index counts the matches
	}
	list, err := a.queryAssets(where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []*Asset{}
	}
	return list, a.setPageTotal(w, len(list), limit, offset, count, args...)
}

func (a *App) assetIDs(query string, args ...any) ([]any, error) {
	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []any
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (a *App) deleteAssetAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	list, err := a.queryAssets(`WHERE id=?`, id)
	if err != nil || len(list) == 0 {
		return nil, errNotFound
	}
	repo, err := a.getRepo(list[0].Repo)
	if err != nil || !a.can(u, repo, true) {
		return nil, httpErr{403, "you don't have write access to " + list[0].Repo}
	}
	if _, err := a.removeAssets("id=?", id); err != nil {
		return nil, err
	}
	a.audit(u, "asset.delete", list[0].Repo+"/"+list[0].Path)
	return nil, nil
}

func (a *App) statsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	type formatStat struct {
		Format string `json:"format"`
		Repos  int    `json:"repos"`
		Assets int64  `json:"assets"`
		Bytes  int64  `json:"bytes"`
	}
	var s struct {
		Repositories int             `json:"repositories"`
		Assets       int64           `json:"assets"`
		Downloads    int64           `json:"downloads"`
		StorageBytes int64           `json:"storageBytes"` // deduplicated
		LogicalBytes int64           `json:"logicalBytes"`
		Users        int             `json:"users"`
		Formats      []formatStat    `json:"formats"`
		Attention    []repoView      `json:"attention"` // offline/degraded/near quota
		Recent       []*Asset        `json:"recent"`
		Popular      []*Asset        `json:"popular"`
		Setup        map[string]bool `json:"setup"`
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&s.Users)
	all, err := a.listRepos()
	if err != nil {
		return nil, err
	}
	stats, err := a.repoStats()
	if err != nil {
		return nil, err
	}
	byFormat := map[string]*formatStat{}
	for _, f := range formats {
		byFormat[f] = &formatStat{Format: f}
	}
	// totals cover only repositories the caller can read
	var repos []string
	for _, rp := range all {
		if !a.can(u, rp, false) {
			continue
		}
		repos = append(repos, rp.Name)
		fs := byFormat[rp.Format]
		v := a.view(u, rp, stats)
		fs.Repos++
		fs.Assets += v.AssetCount
		fs.Bytes += v.Size
		s.Assets, s.Downloads, s.LogicalBytes = s.Assets+v.AssetCount, s.Downloads+v.Downloads, s.LogicalBytes+v.Size
		if v.Status != "online" || (rp.QuotaMB > 0 && v.Size*10 >= rp.QuotaMB<<20*9) {
			s.Attention = append(s.Attention, v)
		}
	}
	s.Repositories = len(repos)
	for _, f := range formats {
		s.Formats = append(s.Formats, *byFormat[f])
	}
	if _, s.StorageBytes, err = a.storedTotals(repos, len(repos) == len(all)); err != nil {
		return nil, err
	}
	in, args := repoIn("repo", repos, len(repos) == len(all))
	if s.Recent, err = a.queryAssets(`WHERE uploader NOT IN ('proxy','system') AND path NOT LIKE '%/blobs/sha256:%'`+in+` ORDER BY created_at DESC LIMIT 8`, args...); err != nil {
		return nil, err
	}
	if s.Popular, err = a.queryAssets(`WHERE downloads>0 AND uploader!='system' AND path NOT LIKE '%/blobs/sha256:%'`+in+` ORDER BY downloads DESC LIMIT 8`, args...); err != nil {
		return nil, err
	}
	var tokens, published, channels int
	var initialOwner string
	a.db.QueryRow(`SELECT COUNT(*) FROM notifiers`).Scan(&channels)
	a.db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE username=? AND kind='api'`, u.Username).Scan(&tokens)
	a.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM assets WHERE uploader NOT IN ('proxy','system') OR downloads>0)`).Scan(&published) // EXISTS stops at the first row
	s.Setup = map[string]bool{
		"passwordChanged": !a.getJSON("initial_password", &initialOwner),
		"baseUrl":         a.settings().BaseURL != "",
		"token":           tokens > 0,
		"clientConnected": published > 0,
		"teamInvited":     s.Users > 1,
		"notifications":   channels > 0,
	}
	return s, nil
}

// ---------- users ----------

func (a *App) users() ([]*User, error) {
	rows, err := a.db.Query(`SELECT ` + userCols + ` FROM users u ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		x, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

func (a *App) listUsersAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	return a.users()
}

type userBody struct {
	Username, Password, Email, Role string
	Disabled                        bool
}

func (a *App) createUserAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var b userBody
	if err := readJSON(r, &b); err != nil {
		return nil, err
	}
	if err := a.createUser(b.Username, b.Password, b.Email, b.Role); err != nil {
		return nil, err
	}
	a.audit(u, "user.create", b.Username, b.Role)
	return nil, nil
}

func (a *App) updateUserAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	name := r.PathValue("name")
	var b userBody
	if err := readJSON(r, &b); err != nil {
		return nil, err
	}
	if roleRank[b.Role] == 0 {
		return nil, httpErr{400, "invalid role"}
	}
	if name == u.Username && (b.Role != roleAdmin || b.Disabled) {
		return nil, httpErr{400, "you can't remove your own admin role or disable yourself"}
	}
	res, err := a.db.Exec(`UPDATE users SET email=?, role=?, disabled=? WHERE username=?`, b.Email, b.Role, b.Disabled, name)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errNotFound
	}
	if b.Password != "" {
		if err := a.setPassword(name, b.Password); err != nil {
			return nil, err
		}
	}
	if b.Disabled || b.Password != "" { // sign the account out everywhere
		a.db.Exec(`DELETE FROM tokens WHERE username=? AND kind='session'`, name)
	}
	a.authMu.Lock()
	a.authCache = map[string]cachedAuth{} // role/disabled changes apply immediately
	a.authMu.Unlock()
	a.audit(u, "user.update", name, b.Role, map[bool]string{true: "disabled"}[b.Disabled])
	return nil, nil
}

func (a *App) deleteUserAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	name := r.PathValue("name")
	if name == u.Username {
		return nil, httpErr{400, "you can't delete yourself"}
	}
	res, err := a.db.Exec(`DELETE FROM users WHERE username=?`, name)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errNotFound
	}
	a.authMu.Lock()
	a.authCache = map[string]cachedAuth{}
	a.authMu.Unlock()
	a.audit(u, "user.delete", name)
	return nil, nil
}

// ---------- API tokens ----------

type tokenView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	LastUsed  int64  `json:"lastUsed"`
}

func (a *App) listTokensAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	rows, err := a.db.Query(`SELECT id, name, scope, created_at, expires_at, last_used FROM tokens WHERE username=? AND kind='api' ORDER BY id DESC`, u.Username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []tokenView{}
	for rows.Next() {
		var t tokenView
		rows.Scan(&t.ID, &t.Name, &t.Scope, &t.CreatedAt, &t.ExpiresAt, &t.LastUsed)
		out = append(out, t)
	}
	return out, nil
}

func (a *App) createTokenAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var b struct {
		Name        string
		Scope       string
		ExpiresDays int
	}
	if err := readJSON(r, &b); err != nil {
		return nil, err
	}
	if b.Scope == "" {
		b.Scope = scopeFull
	}
	if !slices.Contains([]string{scopeRead, scopeWrite, scopeFull}, b.Scope) {
		return nil, httpErr{400, "scope must be read, write or full"}
	}
	if b.Name = strings.TrimSpace(b.Name); b.Name == "" || len(b.Name) > 100 {
		return nil, httpErr{400, "token name is required (at most 100 characters)"}
	}
	if b.ExpiresDays < 0 || b.ExpiresDays > 3650 {
		return nil, httpErr{400, "expiry must be between 0 (never) and 3650 days"}
	}
	tok, err := a.createToken(u.Username, b.Name, "api", b.Scope, time.Duration(b.ExpiresDays)*24*time.Hour)
	if err != nil {
		return nil, err
	}
	a.audit(u, "token.create", b.Name, "scope", b.Scope)
	return map[string]string{"token": tok, "scope": b.Scope}, nil
}

func (a *App) deleteTokenAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	res, err := a.db.Exec(`DELETE FROM tokens WHERE id=? AND username=? AND kind='api'`, r.PathValue("id"), u.Username)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errNotFound
	}
	a.audit(u, "token.delete", r.PathValue("id"))
	return nil, nil
}

// ---------- settings & audit ----------

func (a *App) getSettingsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	return a.settings(), nil
}

// validSettings normalizes and checks settings from the API or an imported configuration.
func validSettings(s Settings) (Settings, error) {
	s.BaseURL = strings.TrimSuffix(strings.TrimSpace(s.BaseURL), "/")
	if s.BaseURL != "" {
		if pu, err := url.Parse(s.BaseURL); err != nil || pu.Host == "" || (pu.Scheme != "http" && pu.Scheme != "https") {
			return s, httpErr{400, "base URL must be absolute, e.g. https://repo.example.com"}
		}
	}
	if s.AuditRetention < 0 {
		return s, httpErr{400, "retention must not be negative"}
	}
	return s, nil
}

func (a *App) putSettingsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var s Settings
	if err := readJSON(r, &s); err != nil {
		return nil, err
	}
	s, err := validSettings(s)
	if err != nil {
		return nil, err
	}
	if err := a.saveSettings(s); err != nil {
		return nil, err
	}
	a.audit(u, "settings.update", "global")
	return s, nil
}

// auditWhere filters events by ?q= (user, action or target), ?action= (a prefix) and the time range
// ?from= (inclusive) to ?to= (exclusive), both Unix seconds; 0 or absent leaves that end open.
func auditWhere(r *http.Request) (string, []any) {
	q := r.URL.Query()
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	return `FROM events WHERE (?1='%%' OR username LIKE ?1 ESCAPE '\' OR action LIKE ?1 ESCAPE '\' OR target LIKE ?1 ESCAPE '\')
			AND (?2='' OR action LIKE ?2 || '%') AND (?3<=0 OR ts>=?3) AND (?4<=0 OR ts<?4)`,
		[]any{"%" + likeEscape(q.Get("q")) + "%", q.Get("action"), from, to}
}

func (a *App) auditAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	limit, offset := pageParams(r, 1000)
	from, args := auditWhere(r)
	rows, err := a.db.Query(`SELECT id, ts, username, action, target, detail `+from+` ORDER BY id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type event struct {
		ID       int64  `json:"id"`
		TS       int64  `json:"ts"`
		Username string `json:"username"`
		Action   string `json:"action"`
		Target   string `json:"target"`
		Detail   string `json:"detail"`
	}
	out := []event{}
	for rows.Next() {
		var e event
		rows.Scan(&e.ID, &e.TS, &e.Username, &e.Action, &e.Target, &e.Detail)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, a.setPageTotal(w, len(out), limit, offset, from, args...)
}

// auditExportAPI streams every matching event as CSV, newest first.
func (a *App) auditExportAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	from, args := auditWhere(r)
	rows, err := a.db.Query(`SELECT ts, username, action, target, detail `+from+` ORDER BY id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="repoman-audit-`+time.Now().UTC().Format("2006-01-02")+`.csv"`)
	cw := csv.NewWriter(w)
	cw.Write([]string{"time", "user", "action", "target", "detail"})
	for rows.Next() {
		var ts int64
		var user, action, target, detail string
		rows.Scan(&ts, &user, &action, &target, &detail)
		cw.Write([]string{time.Unix(ts, 0).UTC().Format(time.RFC3339), csvSafe(user), action, csvSafe(target), csvSafe(detail)})
	}
	cw.Flush()
	return written, nil
}

// csvSafe stops spreadsheet apps from evaluating a cell as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s
	}
	return s
}

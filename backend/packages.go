package main

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"hash"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Package-level views: what a package is (description, licence, dependencies), who uses it, how big and how
// popular it is, and its files' checksums — the page Maven Central shows for a version.

// ---------- package metadata ----------

type pkgDep struct {
	Name     string `json:"name"`
	Version  string `json:"version,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

type pkgInfo struct {
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url,omitempty"`
	Licenses    []string `json:"licenses,omitempty"`
	SCM         string   `json:"scm,omitempty"`
	Developers  []string `json:"developers,omitempty"`
	Readme      string   `json:"readme,omitempty"`
	Deps        []pkgDep `json:"dependencies"`
}

// pomProps reads <properties> into a map; its children are arbitrary.
type pomProps map[string]string

func (p *pomProps) UnmarshalXML(d *xml.Decoder, _ xml.StartElement) error {
	*p = pomProps{}
	for {
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch t := t.(type) {
		case xml.StartElement:
			var s string
			if err := d.DecodeElement(&s, &t); err != nil {
				return err
			}
			(*p)[t.Name.Local] = strings.TrimSpace(s)
		case xml.EndElement:
			return nil
		}
	}
}

type pomDoc struct {
	GroupID     string `xml:"groupId"`
	ArtifactID  string `xml:"artifactId"`
	Version     string `xml:"version"`
	Name        string `xml:"name"`
	Description string `xml:"description"`
	URL         string `xml:"url"`
	Parent      struct {
		GroupID string `xml:"groupId"`
		Version string `xml:"version"`
	} `xml:"parent"`
	Licenses []struct {
		Name string `xml:"name"`
	} `xml:"licenses>license"`
	SCM struct {
		URL string `xml:"url"`
	} `xml:"scm"`
	Developers []struct {
		Name string `xml:"name"`
	} `xml:"developers>developer"`
	Properties pomProps `xml:"properties"`
	Deps       []struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
		Scope      string `xml:"scope"`
		Optional   string `xml:"optional"`
	} `xml:"dependencies>dependency"`
}

var propRe = regexp.MustCompile(`\$\{([^}]+)\}`)

func parsePom(data []byte) (pkgInfo, bool) {
	var d pomDoc
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	if dec.Decode(&d) != nil || d.ArtifactID == "" {
		return pkgInfo{}, false
	}
	if d.GroupID == "" {
		d.GroupID = d.Parent.GroupID
	}
	if d.Version == "" {
		d.Version = d.Parent.Version
	}
	expand := func(s string) string {
		return propRe.ReplaceAllStringFunc(s, func(m string) string {
			switch k := m[2 : len(m)-1]; k {
			case "project.version", "pom.version", "version":
				return d.Version
			case "project.groupId", "pom.groupId", "groupId":
				return d.GroupID
			case "project.artifactId":
				return d.ArtifactID
			default:
				if v, ok := d.Properties[k]; ok {
					return v
				}
			}
			return m
		})
	}
	info := pkgInfo{Description: strings.TrimSpace(d.Description), URL: strings.TrimSpace(d.URL), SCM: strings.TrimSpace(d.SCM.URL), Deps: []pkgDep{}}
	if info.Description == "" {
		info.Description = strings.TrimSpace(d.Name)
	}
	for _, l := range d.Licenses {
		info.Licenses = append(info.Licenses, strings.TrimSpace(l.Name))
	}
	for _, dv := range d.Developers {
		info.Developers = append(info.Developers, strings.TrimSpace(dv.Name))
	}
	for _, x := range d.Deps {
		g, ar := expand(strings.TrimSpace(x.GroupID)), expand(strings.TrimSpace(x.ArtifactID))
		if g == "" || ar == "" {
			continue
		}
		scope := strings.TrimSpace(x.Scope)
		if scope == "" {
			scope = "compile"
		}
		info.Deps = append(info.Deps, pkgDep{Name: g + ":" + ar, Version: expand(strings.TrimSpace(x.Version)), Scope: scope, Optional: strings.TrimSpace(x.Optional) == "true"})
	}
	return info, true
}

// npmVersionInfo reads one version's entry of a packument.
func npmVersionInfo(v map[string]any, readme string) pkgInfo {
	info := pkgInfo{Readme: readme, Deps: []pkgDep{}}
	info.Description, _ = v["description"].(string)
	info.URL, _ = v["homepage"].(string)
	switch l := v["license"].(type) {
	case string:
		info.Licenses = []string{l}
	case map[string]any:
		if t, _ := l["type"].(string); t != "" {
			info.Licenses = []string{t}
		}
	}
	switch r := v["repository"].(type) {
	case string:
		info.SCM = r
	case map[string]any:
		info.SCM, _ = r["url"].(string)
	}
	for field, scope := range map[string]string{"dependencies": "runtime", "peerDependencies": "peer", "optionalDependencies": "optional"} {
		m, _ := v[field].(map[string]any)
		for n, ver := range m {
			vs, _ := ver.(string)
			info.Deps = append(info.Deps, pkgDep{Name: n, Version: vs, Scope: scope, Optional: scope == "optional"})
		}
	}
	sort.Slice(info.Deps, func(i, j int) bool { return info.Deps[i].Name < info.Deps[j].Name })
	return info
}

func parsePackument(data []byte, version string) (pkgInfo, map[string]map[string]any, bool) {
	var doc struct {
		Readme   string                    `json:"readme"`
		DistTags map[string]string         `json:"dist-tags"`
		Versions map[string]map[string]any `json:"versions"`
	}
	if json.Unmarshal(data, &doc) != nil || len(doc.Versions) == 0 {
		return pkgInfo{}, nil, false
	}
	if version == "" {
		version = doc.DistTags["latest"]
	}
	v, ok := doc.Versions[version]
	if !ok {
		return pkgInfo{Readme: doc.Readme, Deps: []pkgDep{}}, doc.Versions, true
	}
	return npmVersionInfo(v, doc.Readme), doc.Versions, true
}

var goReqRe = regexp.MustCompile(`^\s*(?:require\s+)?([^\s()]+)\s+(v[^\s]+)(\s*//\s*indirect)?`)

func parseGoMod(data []byte) pkgInfo {
	info := pkgInfo{Deps: []pkgDep{}}
	in := false
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "require ("):
			in = true
		case t == ")":
			in = false
		case in || strings.HasPrefix(t, "require "):
			if m := goReqRe.FindStringSubmatch(t); m != nil {
				scope := "direct"
				if m[3] != "" {
					scope = "indirect"
				}
				info.Deps = append(info.Deps, pkgDep{Name: m[1], Version: m[2], Scope: scope})
			}
		}
	}
	return info
}

func condaInfo(attrs json.RawMessage) pkgInfo {
	info := pkgInfo{Deps: []pkgDep{}}
	var idx struct {
		License string   `json:"license"`
		Depends []string `json:"depends"`
	}
	if json.Unmarshal(attrs, &idx) != nil {
		return info
	}
	if idx.License != "" {
		info.Licenses = []string{idx.License}
	}
	for _, d := range idx.Depends {
		n, v, _ := strings.Cut(strings.TrimSpace(d), " ")
		info.Deps = append(info.Deps, pkgDep{Name: n, Version: strings.TrimSpace(v), Scope: "run"})
	}
	return info
}

// ---------- dependency index ----------

const maxMetaBytes = 4 << 20

// depsOf returns the dependencies declared by one asset when it is a package descriptor worth indexing:
// a Maven pom, a Go go.mod, the packument of a hosted npm package or a conda package. nil means "not one".
func (a *App) depsOf(repo *Repo, x *Asset) []pkgDep {
	if x.Size > maxMetaBytes || x.Uploader == "system" {
		return nil
	}
	switch repo.Format {
	case "maven":
		if strings.HasSuffix(x.Path, ".pom") {
			if data, err := a.readAsset(x); err == nil {
				if info, ok := parsePom(data); ok {
					return info.Deps
				}
			}
		}
	case "go":
		if strings.HasSuffix(x.Path, ".mod") && strings.Contains(x.Path, "/@v/") {
			if data, err := a.readAsset(x); err == nil {
				return parseGoMod(data).Deps
			}
		}
	case "conda":
		if len(x.Attrs) > 0 {
			return condaInfo(x.Attrs).Deps
		}
	}
	return nil
}

// indexDeps replaces the dependency rows of one asset. Caches of upstream npm metadata are skipped:
// they change on every refresh and describe packages nobody here published.
func (a *App) indexDeps(x *Asset) {
	repo, err := a.getRepo(x.Repo)
	if err != nil {
		return
	}
	type row struct {
		comp, ver string
		dep       pkgDep
	}
	var rows []row
	switch {
	case repo.Format == "npm" && repo.Type == "hosted" && strings.HasSuffix(x.Path, "/package.json") && !strings.Contains(x.Path, "/-/") && x.Size <= maxMetaBytes:
		if data, err := a.readAsset(x); err == nil {
			if _, versions, ok := parsePackument(data, ""); ok {
				for ver, v := range versions {
					for _, d := range npmVersionInfo(v, "").Deps {
						rows = append(rows, row{x.Component, ver, d})
					}
				}
			}
		}
	default:
		comp, ver := x.Component, x.Version
		if repo.Format == "conda" || repo.Format == "go" || repo.Format == "maven" {
			for _, d := range a.depsOf(repo, x) {
				rows = append(rows, row{comp, ver, d})
			}
		}
	}
	a.db.Exec(`DELETE FROM deps WHERE asset_id=?`, x.ID)
	if len(rows) == 0 {
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	for _, r := range rows {
		tx.Exec(`INSERT INTO deps(asset_id, repo, component, version, dep, dep_version, scope, optional) VALUES(?,?,?,?,?,?,?,?)`,
			x.ID, x.Repo, r.comp, r.ver, r.dep.Name, r.dep.Version, r.dep.Scope, r.dep.Optional)
	}
	tx.Commit()
}

// indexableDescriptor is the cheap path test run on every store before any database work.
func indexableDescriptor(p string) bool {
	return strings.HasSuffix(p, ".pom") || strings.HasSuffix(p, ".mod") || strings.HasSuffix(p, "/package.json") || strings.HasSuffix(p, ".conda") || strings.HasSuffix(p, ".tar.bz2")
}

// indexDepsAll (re)builds the dependency index for the whole installation.
func (a *App) indexDepsAll() (string, error) {
	last, n := int64(0), 0
	for {
		list, err := a.queryAssets(`WHERE a.id>? AND a.uploader!='system' AND (a.path LIKE '%.pom' OR a.path LIKE '%/@v/%.mod' OR a.path LIKE '%/package.json' OR a.path LIKE '%.conda' OR a.path LIKE '%.tar.bz2')
			ORDER BY a.id LIMIT 500`, last)
		if err != nil {
			return "", err
		}
		if len(list) == 0 {
			break
		}
		for _, x := range list {
			if indexableDescriptor(x.Path) {
				a.indexDeps(x)
				n++
			}
			last = x.ID
		}
	}
	a.setJSON("depsIndexed", true)
	return fmt.Sprintf("Indexed the dependencies of %d package descriptors", n), nil
}

// ---------- checksums ----------

type fileSums struct {
	MD5    string `json:"md5,omitempty"`
	SHA1   string `json:"sha1,omitempty"`
	SHA256 string `json:"sha256"`
	SHA512 string `json:"sha512,omitempty"`
	Signed bool   `json:"signed"` // a detached <file>.asc signature is stored next to it
}

// sumsOf hashes a stored file once with every algorithm clients ask for. Very large files report only the
// SHA-256 recorded when they were stored; hashing gigabytes on a page view is not worth it.
func (a *App) sumsOf(x *Asset) fileSums {
	s := fileSums{SHA256: x.Blob}
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE repo=? AND path=?`, x.Repo, x.Path+".asc").Scan(&n)
	s.Signed = n > 0 || strings.HasSuffix(x.Path, ".asc")
	if x.Size > 64<<20 {
		return s
	}
	f, err := a.blobs.Open(x.Blob)
	if err != nil {
		return s
	}
	defer f.Close()
	hs := []hash.Hash{md5.New(), sha1.New(), sha512.New()}
	if _, err := io.Copy(io.MultiWriter(hs[0], hs[1], hs[2]), f); err == nil {
		s.MD5, s.SHA1, s.SHA512 = hex.EncodeToString(hs[0].Sum(nil)), hex.EncodeToString(hs[1].Sum(nil)), hex.EncodeToString(hs[2].Sum(nil))
	}
	return s
}

func (a *App) checksumsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	x, err := a.assetAPI(w, r, u)
	if err != nil {
		return nil, err
	}
	return a.sumsOf(x.(*Asset)), nil
}

// ---------- the package page ----------

type dayCount struct {
	Day string `json:"day"`
	N   int64  `json:"n"`
}

// fillDays turns day-number counts into one entry per day, oldest first, ending today (UTC).
func fillDays(m map[int64]int64, days int) []dayCount {
	today := time.Now().Unix() / 86400
	out := make([]dayCount, 0, days)
	for d := today - int64(days) + 1; d <= today; d++ {
		out = append(out, dayCount{time.Unix(d*86400, 0).UTC().Format("2006-01-02"), m[d]})
	}
	return out
}

type packageFile struct {
	Asset *Asset   `json:"asset"`
	Sums  fileSums `json:"sums"`
}

type packageView struct {
	Repo        string        `json:"repo"`
	Format      string        `json:"format"`
	Component   string        `json:"component"`
	Version     string        `json:"version"`
	Latest      string        `json:"latest"`
	Versions    []versionView `json:"versions"`
	Files       []packageFile `json:"files"`
	Info        pkgInfo       `json:"info"`
	Dependents  int           `json:"dependents"`
	Downloads   []dayCount    `json:"downloadsDaily"`
	DownloadsTT int64         `json:"downloadsTotal"`
}

var companionExts = []string{".md5", ".sha1", ".sha256", ".sha512", ".asc"}

func isCompanion(p string) bool {
	return slices.ContainsFunc(companionExts, func(e string) bool { return strings.HasSuffix(p, e) })
}

// packageAPI describes one component (optionally one version of it): versions, files with checksums,
// descriptive metadata and dependencies read from the package's own descriptor, how many packages depend on it,
// and a month of download history.
func (a *App) packageAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.repoFor(r, u, false)
	if err != nil {
		return nil, err
	}
	comp, ver := r.URL.Query().Get("component"), r.URL.Query().Get("version")
	if comp == "" {
		return nil, httpErr{400, "component is required"}
	}
	a.flushDownloads()
	held, hargs := a.repoIs(repo)
	rows, err := a.db.Query(`SELECT version, COUNT(*), SUM(size), SUM(downloads), MAX(updated_at) FROM assets
		WHERE `+held+` AND component=? AND uploader!='system' AND version!='' GROUP BY version ORDER BY MAX(created_at) DESC, version DESC`, append(append([]any{}, hargs...), comp)...)
	if err != nil {
		return nil, err
	}
	v := &packageView{Repo: repo.Name, Format: repo.Format, Component: comp, Versions: []versionView{}, Files: []packageFile{}, Info: pkgInfo{Deps: []pkgDep{}}}
	for rows.Next() {
		var x versionView
		rows.Scan(&x.Version, &x.Assets, &x.Size, &x.Downloads, &x.UpdatedAt)
		v.Versions = append(v.Versions, x)
		v.DownloadsTT += x.Downloads
	}
	rows.Close()
	if len(v.Versions) > 0 {
		v.Latest = v.Versions[0].Version
	}
	if ver == "" {
		ver = v.Latest
	}
	v.Version = ver
	if r.URL.Query().Get("brief") != "" { // just the version list: enough to offer "latest"
		return v, nil
	}
	files, err := a.queryAssets(`WHERE `+strings.ReplaceAll(held, "repo", "a.repo")+` AND a.component=? AND a.version=? AND a.uploader!='system' ORDER BY a.path`, append(append([]any{}, hargs...), comp, ver)...)
	if err != nil {
		return nil, err
	}
	if len(v.Versions) == 0 && len(files) == 0 {
		if files, err = a.queryAssets(`WHERE `+strings.ReplaceAll(held, "repo", "a.repo")+` AND a.component=? AND a.uploader!='system' ORDER BY a.path LIMIT 50`, append(append([]any{}, hargs...), comp)...); err != nil || len(files) == 0 {
			return nil, errNotFound
		}
	}
	files = dedupePaths(files, a.holders(repo))
	for _, f := range files {
		if !isCompanion(f.Path) {
			v.Files = append(v.Files, packageFile{Asset: f, Sums: a.sumsOf(f)})
		}
	}
	v.Info = a.describe(repo, comp, ver, files)
	var inReadable string
	repos, all := a.readable(u)
	inReadable, iargs := repoIn("repo", repos, all)
	dq := `SELECT COUNT(*) FROM (SELECT 1 FROM deps WHERE dep=?` + inReadable + ` GROUP BY repo, component, version)`
	a.db.QueryRow(dq, append([]any{comp}, iargs...)...).Scan(&v.Dependents)

	days := map[int64]int64{}
	drows, err := a.db.Query(`SELECT d.day, SUM(d.n) FROM downloads_daily d JOIN assets a ON a.id=d.asset_id
		WHERE `+strings.ReplaceAll(held, "repo", "a.repo")+` AND a.component=? AND d.day>=? GROUP BY d.day`, append(append([]any{}, hargs...), comp, time.Now().Unix()/86400-29)...)
	if err == nil {
		for drows.Next() {
			var d, n int64
			drows.Scan(&d, &n)
			days[d] = n
		}
		drows.Close()
	}
	v.Downloads = fillDays(days, 30)
	return v, nil
}

// dedupePaths keeps one asset per path: the one from the earliest repository in order, which is the member a group serves.
func dedupePaths(list []*Asset, order []string) []*Asset {
	rank := map[string]int{}
	for i, n := range order {
		rank[n] = i
	}
	best := map[string]*Asset{}
	for _, x := range list {
		if cur, ok := best[x.Path]; !ok || rank[x.Repo] < rank[cur.Repo] {
			best[x.Path] = x
		}
	}
	out := make([]*Asset, 0, len(best))
	for _, x := range list {
		if best[x.Path] == x {
			out = append(out, x)
		}
	}
	return out
}

// describe reads the package descriptor that belongs to the version's files.
func (a *App) describe(repo *Repo, comp, ver string, files []*Asset) pkgInfo {
	empty := pkgInfo{Deps: []pkgDep{}}
	read := func(x *Asset) []byte {
		if x.Size > maxMetaBytes {
			return nil
		}
		b, _ := a.readAsset(x)
		return b
	}
	switch repo.Format {
	case "maven":
		for _, f := range files {
			if strings.HasSuffix(f.Path, ".pom") {
				if info, ok := parsePom(read(f)); ok {
					return info
				}
			}
		}
	case "npm":
		for _, n := range a.holders(repo) {
			if x, err := a.findAsset(n, comp+"/package.json"); err == nil {
				if info, _, ok := parsePackument(read(x), ver); ok {
					return info
				}
			}
		}
	case "go":
		for _, f := range files {
			if strings.HasSuffix(f.Path, ".mod") {
				return parseGoMod(read(f))
			}
		}
	case "conda":
		if len(files) > 0 {
			return condaInfo(files[0].Attrs)
		}
	}
	return empty
}

type dependentView struct {
	Repo       string `json:"repo"`
	Component  string `json:"component"`
	Version    string `json:"version"`
	DepVersion string `json:"depVersion"`
	Scope      string `json:"scope"`
}

// dependentsAPI lists the packages (that the caller can read) whose descriptors declare a dependency on a component.
func (a *App) dependentsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	if _, err := a.repoFor(r, u, false); err != nil {
		return nil, err
	}
	comp := r.URL.Query().Get("component")
	if comp == "" {
		return nil, httpErr{400, "component is required"}
	}
	limit, offset := pageParams(r, 100)
	repos, all := a.readable(u)
	in, iargs := repoIn("repo", repos, all)
	args := append([]any{comp}, iargs...)
	var total int
	a.db.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM deps WHERE dep=?`+in+` GROUP BY repo, component, version)`, args...).Scan(&total)
	setTotal(w, total)
	rows, err := a.db.Query(`SELECT repo, component, version, MAX(dep_version), MAX(scope) FROM deps WHERE dep=?`+in+`
		GROUP BY repo, component, version ORDER BY component, version DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dependentView{}
	for rows.Next() {
		var d dependentView
		rows.Scan(&d.Repo, &d.Component, &d.Version, &d.DepVersion, &d.Scope)
		out = append(out, d)
	}
	return out, rows.Err()
}

// assetDownloadsAPI returns an artifact's downloads per day for the last month.
func (a *App) assetDownloadsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var repoName string
	id := r.PathValue("id")
	if err := a.db.QueryRow(`SELECT repo FROM assets WHERE id=?`, id).Scan(&repoName); err != nil {
		return nil, errNotFound
	}
	repo, err := a.getRepo(repoName)
	if err != nil || !a.can(u, repo, false) {
		return nil, errNotFound
	}
	a.flushDownloads()
	days := map[int64]int64{}
	rows, err := a.db.Query(`SELECT day, n FROM downloads_daily WHERE asset_id=? AND day>=?`, id, time.Now().Unix()/86400-29)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d, n int64
		rows.Scan(&d, &n)
		days[d] = n
	}
	return fillDays(days, 30), nil
}

// ---------- search operators and saved searches ----------

var searchOpKeys = []string{"sha256", "version", "uploader", "repo", "format"}

// parseSearch splits "lodash version:4.17 repo:npm-all" into the free text and its key:value operators. A token
// whose key is not an operator ("com.acme:lib") stays part of the text.
func parseSearch(q string) (string, map[string]string) {
	ops := map[string]string{}
	var text []string
	for _, tok := range strings.Fields(q) {
		k, v, ok := strings.Cut(tok, ":")
		if ok && v != "" && slices.Contains(searchOpKeys, strings.ToLower(k)) {
			ops[strings.ToLower(k)] = v
		} else {
			text = append(text, tok)
		}
	}
	return strings.Join(text, " "), ops
}

type sqlFilter struct {
	sql  string
	args []any
}

func searchFilters(ops map[string]string) []sqlFilter {
	var out []sqlFilter
	if v := ops["version"]; v != "" {
		out = append(out, sqlFilter{" AND a.version=?", []any{v}})
	}
	if v := ops["uploader"]; v != "" {
		out = append(out, sqlFilter{" AND a.uploader=?", []any{v}})
	}
	if v := strings.ToLower(ops["sha256"]); v != "" {
		out = append(out, sqlFilter{" AND a.blob>=? AND a.blob<?", []any{v, v + "g"}}) // hex digits sort below "g": a prefix range
	}
	return out
}

type savedSearch struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Query     string `json:"query"`
	CreatedAt int64  `json:"createdAt"`
}

func (a *App) listSavedSearchesAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	rows, err := a.db.Query(`SELECT id, name, query, created_at FROM saved_searches WHERE username=? ORDER BY name`, u.Username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []savedSearch{}
	for rows.Next() {
		var s savedSearch
		rows.Scan(&s.ID, &s.Name, &s.Query, &s.CreatedAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (a *App) saveSearchAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var s savedSearch
	if err := readJSON(r, &s); err != nil {
		return nil, err
	}
	s.Name, s.Query = strings.TrimSpace(s.Name), strings.TrimSpace(s.Query)
	if s.Name == "" || len(s.Name) > 80 || len(s.Query) > 500 {
		return nil, httpErr{400, "give the search a name of up to 80 characters"}
	}
	var n int
	a.db.QueryRow(`SELECT COUNT(*) FROM saved_searches WHERE username=?`, u.Username).Scan(&n)
	if n >= 50 {
		return nil, httpErr{400, "you can save up to 50 searches; delete one first"}
	}
	s.CreatedAt = time.Now().Unix()
	res, err := a.db.Exec(`INSERT INTO saved_searches(username, name, query, created_at) VALUES(?,?,?,?)`, u.Username, s.Name, s.Query, s.CreatedAt)
	if err != nil {
		return nil, err
	}
	s.ID, _ = res.LastInsertId()
	return s, nil
}

func (a *App) deleteSavedSearchAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	res, err := a.db.Exec(`DELETE FROM saved_searches WHERE id=? AND username=?`, r.PathValue("id"), u.Username)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, errNotFound
	}
	return map[string]bool{"ok": true}, nil
}

// ---------- promotion ----------

type componentRef struct {
	Component string `json:"component"`
	Version   string `json:"version"` // empty: every version
}

// promotable formats are those whose hosted layout is plain files plus indexes this server can regenerate.
var promotable = []string{"maven", "raw", "go", "apt", "yum"}

// itemsWhere builds a condition matching the listed components/versions (alias a).
func itemsWhere(items []componentRef) (string, []any) {
	var parts []string
	var args []any
	for _, it := range items {
		if it.Version == "" {
			parts = append(parts, "(a.component=?)")
			args = append(args, it.Component)
		} else {
			parts = append(parts, "(a.component=? AND a.version=?)")
			args = append(args, it.Component, it.Version)
		}
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

type promoteReq struct {
	Target string         `json:"target"`
	Move   bool           `json:"move"`
	Items  []componentRef `json:"items"`
}

// promoteAPI copies (or moves) versions from this repository to another hosted repository of the same format,
// e.g. staging → releases. Content is shared, not duplicated.
func (a *App) promoteAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	src, err := a.repoFor(r, u, false)
	if err != nil {
		return nil, err
	}
	var req promoteReq
	if err := readJSON(r, &req); err != nil {
		return nil, err
	}
	if len(req.Items) == 0 || len(req.Items) > 500 {
		return nil, httpErr{400, "choose between 1 and 500 packages to promote"}
	}
	dst, err := a.getRepo(req.Target)
	if err != nil || !a.can(u, dst, true) {
		return nil, httpErr{400, "choose a hosted target repository you can write to"}
	}
	switch {
	case dst.Type != "hosted" || dst.Format != src.Format:
		return nil, httpErr{400, "the target must be a hosted " + src.Format + " repository"}
	case !slices.Contains(promotable, src.Format):
		return nil, httpErr{400, "promotion is not available for " + src.Format + " repositories yet; publish from your build with the native client instead"}
	case dst.Name == src.Name:
		return nil, httpErr{400, "choose a different repository as the target"}
	case req.Move && (src.Type != "hosted" || !a.can(u, src, true)):
		return nil, httpErr{403, "moving needs a hosted source repository you can write to"}
	}
	if err := a.checkWritable(dst); err != nil {
		return nil, err
	}
	held, hargs := a.repoIs(src)
	where, wargs := itemsWhere(req.Items)
	list, err := a.queryAssets(`WHERE `+strings.ReplaceAll(held, "repo", "a.repo")+` AND a.uploader!='system' AND `+where+` ORDER BY a.path`, append(append([]any{}, hargs...), wargs...)...)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errNotFound
	}
	seen := map[string]bool{}
	var todo []*Asset
	for _, x := range list { // a group can hold one path in several members: the first one wins
		if !seen[x.Path] {
			seen[x.Path] = true
			todo = append(todo, x)
		}
	}
	for _, x := range todo { // check everything before copying anything
		if err := a.checkRedeploy(dst, x.Path); err != nil {
			return nil, httpErr{409, "nothing was promoted: " + err.Error()}
		}
	}
	comps := map[string]bool{}
	for _, x := range todo {
		if _, err := a.linkAsset(dst.Name, x.Path, x, assetMeta{ContentType: x.ContentType, Component: x.Component, Version: x.Version, Uploader: u.Username, Attrs: string(x.Attrs)}); err != nil {
			return nil, err
		}
		comps[x.Component] = true
	}
	if dst.Format == "maven" {
		for c := range comps {
			if err := a.mavenRewriteMetadata(dst, c); err != nil {
				return nil, err
			}
		}
	}
	if err := a.reindex(dst.Name); err != nil {
		return nil, err
	}
	if req.Move {
		defer func() {
			if src.Format == "maven" {
				for c := range comps {
					a.mavenRewriteMetadata(src, c)
				}
			}
		}()
		for i := 0; i < len(todo); i += 500 {
			ids := []any{}
			for _, x := range todo[i:min(i+500, len(todo))] {
				ids = append(ids, x.ID)
			}
			if _, err := a.removeAssets(`id IN (`+placeholders(len(ids))+`)`, ids...); err != nil {
				return nil, err
			}
		}
	}
	verb := "promote"
	if req.Move {
		verb = "promote.move"
	}
	a.audit(u, "package."+verb, src.Name+" → "+dst.Name, len(req.Items), "packages,", len(todo), "files")
	return map[string]int{"files": len(todo), "packages": len(req.Items)}, nil
}

// mavenRewriteMetadata regenerates <group path>/<artifact>/maven-metadata.xml (and its checksums) for a hosted
// repository from the versions it now holds, so version ranges and LATEST/RELEASE see promoted releases.
func (a *App) mavenRewriteMetadata(repo *Repo, comp string) error {
	g, art, ok := strings.Cut(comp, ":")
	if !ok {
		return nil
	}
	rows, err := a.db.Query(`SELECT version FROM assets WHERE repo=? AND component=? AND version!='' AND uploader!='system' GROUP BY version ORDER BY MAX(created_at)`, repo.Name, comp)
	if err != nil {
		return err
	}
	var versions []string
	for rows.Next() {
		var v string
		rows.Scan(&v)
		versions = append(versions, v)
	}
	rows.Close()
	if len(versions) == 0 {
		return nil
	}
	release := ""
	for _, v := range versions {
		if !strings.HasSuffix(v, "-SNAPSHOT") {
			release = v
		}
	}
	doc := mavenMeta{GroupID: g, ArtifactID: art, Versioning: &mavenVersioning{
		Latest: versions[len(versions)-1], Release: release, Versions: versions, LastUpdated: time.Now().UTC().Format("20060102150405"),
	}}
	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	body = append([]byte(xml.Header), body...)
	p := strings.ReplaceAll(g, ".", "/") + "/" + art + "/maven-metadata.xml"
	meta := assetMeta{ContentType: "application/xml", Uploader: "system"}
	if _, err := a.putAsset(repo.Name, p, strings.NewReader(string(body)), meta); err != nil {
		return err
	}
	for ext, sum := range mavenChecksums {
		if ext == ".md5" || ext == ".sha1" {
			if _, err := a.putAsset(repo.Name, p+ext, strings.NewReader(sum(body)), meta); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------- bulk delete ----------

type bulkReq struct {
	IDs        []int64        `json:"ids"`
	Prefixes   []string       `json:"prefixes"` // folders: everything under each path
	Components []componentRef `json:"components"`
}

func (a *App) bulkDeleteAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.repoFor(r, u, true)
	if err != nil {
		return nil, err
	}
	if repo.Type == "group" {
		return nil, httpErr{400, "groups store nothing; delete from a member repository"}
	}
	var req bulkReq
	if err := readJSONLimit(r, &req, 4<<20); err != nil {
		return nil, err
	}
	if len(req.IDs)+len(req.Prefixes)+len(req.Components) == 0 || len(req.IDs) > 5000 || len(req.Prefixes) > 200 || len(req.Components) > 500 {
		return nil, httpErr{400, "choose something to delete (up to 5000 files, 200 folders or 500 packages at a time)"}
	}
	deleted := 0
	del := func(where string, args ...any) error {
		list, err := a.removeAssets(`repo=? AND `+where, append([]any{repo.Name}, args...)...)
		deleted += len(list)
		return err
	}
	for i := 0; i < len(req.IDs); i += 500 {
		ids := []any{}
		for _, id := range req.IDs[i:min(i+500, len(req.IDs))] {
			ids = append(ids, id)
		}
		if err := del(`id IN (`+placeholders(len(ids))+`)`, ids...); err != nil {
			return nil, err
		}
	}
	for _, p := range req.Prefixes {
		p = strings.Trim(p, "/")
		if p == "" || !validPath(p) {
			return nil, httpErr{400, "invalid folder: " + p}
		}
		clause, cargs := prefixWhere(p + "/")
		if err := del(`1=1`+clause, cargs...); err != nil {
			return nil, err
		}
	}
	if len(req.Components) > 0 {
		where, wargs := itemsWhere(req.Components)
		if err := del(strings.ReplaceAll(where, "a.", ""), wargs...); err != nil {
			return nil, err
		}
	}
	a.audit(u, "bulk.delete", repo.Name, deleted, "assets")
	return map[string]int{"deleted": deleted}, nil
}

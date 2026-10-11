package main

import (
	"bytes"
	"errors"
	"mime/multipart"
	"strings"
	"testing"
	"time"
)

// A packument whose versions are not objects used to panic the index rebuild. Rebuilds run on task
// goroutines, so one malformed publish could take the whole server down.
func TestNpmMalformedPackument(t *testing.T) {
	e := newEnv(t)
	e.must("publish with a non-object version", 400, "PUT", "/repository/npm-hosted/bad",
		strings.NewReader(`{"name":"bad","versions":{"1.0.0":"oops"},"_attachments":{}}`), admin)

	// databases written before the check may still hold such a document
	doc := `{"name":"old","versions":{"1.0.0":"oops","2.0.0":{"dist":"nope"}},"dist-tags":{"latest":7}}`
	if _, err := e.a.putAsset("npm-hosted", "old/package.json", strings.NewReader(doc), assetMeta{Component: "old", Uploader: "admin"}); err != nil {
		t.Fatal(err)
	}
	e.must("packument", 200, "GET", "/repository/npm-hosted/old", nil, admin)
	if err := e.a.reindex("npm-hosted"); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if err := e.a.runTask("rebuild-indexes"); err != nil {
		t.Fatalf("rebuild-indexes: %v", err)
	}
}

func TestTaskPanicIsContained(t *testing.T) {
	e := newEnv(t)
	taskDefs = append(taskDefs, taskDef{ID: "boom", Name: "Boom", run: func(*App) (string, error) { panic("kaboom") }})
	defer func() { taskDefs = taskDefs[:len(taskDefs)-1] }()
	if err := e.a.runTask("boom"); err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("a panicking task must report an error, got %v", err)
	}
	if e.a.tasks["boom"].Running {
		t.Fatal("task left marked as running after a panic")
	}
	e.must("unknown task", 404, "POST", "/api/tasks/no-such-task/run", nil, admin)
}

func TestDockerUploadNeedsDigest(t *testing.T) {
	e := newEnv(t)
	_, _, h := e.req("POST", "/v2/docker-hosted/app/blobs/uploads/", nil, admin)
	loc := h.Get("Location")
	if loc == "" {
		t.Fatal("no upload location")
	}
	e.must("finish without digest", 400, "PUT", loc, strings.NewReader("layer"), admin)
	e.must("monolithic upload with a bad digest", 400, "POST", "/v2/docker-hosted/app/blobs/uploads/?digest=sha256:xyz", strings.NewReader("layer"), admin)
	var n int
	e.a.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE repo='docker-hosted'`).Scan(&n)
	if n != 0 {
		t.Fatalf("%d assets stored by rejected uploads", n)
	}
}

func TestMetricsCoverReadableRepositories(t *testing.T) {
	e := newEnv(t)
	e.must("viewer", 204, "POST", "/api/users", jsonBody(map[string]string{"username": "vic", "password": "password1", "role": "viewer"}), admin)
	e.must("hide repo", 204, "PUT", "/api/repositories/raw-hosted/permissions", jsonBody([]permRow{{Username: "vic", Level: accessNone}}), admin)
	if b := e.must("metrics", 200, "GET", "/metrics", nil, "vic:password1"); strings.Contains(b, `repository="raw-hosted"`) || !strings.Contains(b, `repository="npm-hosted"`) {
		t.Fatalf("viewer metrics must list readable repositories only:\n%s", b)
	}
	if b := e.must("metrics", 200, "GET", "/metrics", nil, admin); !strings.Contains(b, `repository="raw-hosted"`) {
		t.Fatal("admin metrics lack a repository")
	}
}

func TestImportValidatesInput(t *testing.T) {
	e := newEnv(t)
	doc := `{"version":"1","settings":{"baseUrl":"not a url","anonymousAccess":true},
		"repositories":[null],"users":[null],"webhooks":[null],"notifiers":[null],
		"permissions":[{"repo":"raw-hosted","username":"admin","level":"owner"}]}`
	b := e.must("import", 200, "POST", "/api/system/import", strings.NewReader(doc), admin)
	if !strings.Contains(b, "level must be none, read or write") || !strings.Contains(b, "settings: base URL") {
		t.Fatalf("invalid entries were not reported: %s", b)
	}
	if e.a.settings().BaseURL != "" {
		t.Fatal("an invalid base URL was imported")
	}
	var n int
	e.a.db.QueryRow(`SELECT COUNT(*) FROM repo_perms`).Scan(&n)
	if n != 0 {
		t.Fatal("an invalid permission level was imported")
	}
}

// A browser upload of several packages rebuilds the index once, and what was stored before a rejected
// file stays installable.
func TestBatchUploadIndexesOnce(t *testing.T) {
	e := newEnv(t)
	upload := func(want int, files map[string][]byte, order ...string) string {
		var form bytes.Buffer
		mw := multipart.NewWriter(&form)
		for _, name := range order {
			fw, _ := mw.CreateFormFile("file", name)
			fw.Write(files[name])
		}
		mw.Close()
		return e.must("upload", want, "POST", "/api/repositories/apt-hosted/upload", &form, admin, "Content-Type", mw.FormDataContentType())
	}
	files := map[string][]byte{"a.deb": makeDeb("alpha", "1.0", "amd64"), "b.deb": makeDeb("beta", "2.0", "amd64"), "c.deb": []byte("not a package")}
	if b := upload(200, files, "a.deb", "b.deb"); strings.Count(b, "pool/main/") != 2 {
		t.Fatalf("upload result: %s", b)
	}
	pk := e.must("Packages", 200, "GET", "/repository/apt-hosted/dists/stable/main/binary-amd64/Packages", nil, admin)
	if !strings.Contains(pk, "Package: alpha") || !strings.Contains(pk, "Package: beta") {
		t.Fatalf("index lacks uploaded packages:\n%s", pk)
	}
	files["a.deb"] = makeDeb("gamma", "1.0", "amd64")
	if b := upload(400, files, "a.deb", "c.deb"); !strings.Contains(b, "1 earlier file(s)") {
		t.Fatalf("partial upload not reported: %s", b)
	}
	if pk := e.must("Packages", 200, "GET", "/repository/apt-hosted/dists/stable/main/binary-amd64/Packages", nil, admin); !strings.Contains(pk, "Package: gamma") {
		t.Fatal("the package stored before the rejected file is missing from the index")
	}
}

// Totals follow uploads and deletions at once, and a short page reports its total without a count query.
func TestTotalsStayCurrent(t *testing.T) {
	e := newEnv(t)
	total := func(path string) string {
		_, _, h := e.req("GET", path, nil, admin)
		return h.Get("X-Total-Count")
	}
	if got := total("/api/search?q=zzz"); got != "0" {
		t.Fatalf("empty search total %q", got)
	}
	for _, name := range []string{"one.txt", "two.txt", "three.txt"} {
		e.must("upload", 201, "PUT", "/repository/raw-hosted/docs/"+name, strings.NewReader(name), admin)
	}
	if b := e.must("stats", 200, "GET", "/api/stats", nil, admin); !strings.Contains(b, `"assets":3,`) {
		t.Fatalf("stats after upload: %s", b)
	}
	if b := e.must("repos", 200, "GET", "/api/repositories", nil, admin); !strings.Contains(b, `"assetCount":3`) {
		t.Fatalf("repository list after upload: %s", b)
	}
	for path, want := range map[string]string{
		"/api/search?q=docs": "3", "/api/search?q=docs&limit=2": "3", "/api/search?q=docs&limit=2&offset=2": "3",
		"/api/search?q=docs&limit=3": "3", "/api/search?q=docs&offset=50": "3", "/api/audit?action=asset.upload&limit=2": "3",
	} {
		if got := total(path); got != want {
			t.Errorf("%s: total %q, want %s", path, got, want)
		}
	}
	e.must("delete", 204, "DELETE", "/repository/raw-hosted/docs/one.txt", nil, admin)
	if b := e.must("stats", 200, "GET", "/api/stats", nil, admin); !strings.Contains(b, `"assets":2,`) {
		t.Fatalf("stats after delete: %s", b)
	}
}

func TestSlowStat(t *testing.T) {
	var s slowStat[int]
	calls := 0
	get := func(key string, gen int64, cost time.Duration) int {
		v, _ := s.get(key, gen, func() (int, error) { calls++; time.Sleep(cost); return calls, nil })
		return v
	}
	if get("a", 1, 0) != 1 || get("a", 1, 0) != 1 {
		t.Fatal("unchanged data must be served from the cache")
	}
	if get("a", 2, 0) != 2 {
		t.Fatal("a cheap aggregate must be recomputed as soon as the data changes")
	}
	if get("b", 2, 2*slowStatCost) != 3 {
		t.Fatal("another key must not share the cached value")
	}
	if get("b", 3, 0) != 3 {
		t.Fatal("an expensive aggregate should be reused for a short while after a change")
	}
	if _, err := s.get("c", 3, func() (int, error) { return 0, errors.New("x") }); err == nil {
		t.Fatal("errors must not be cached")
	}
	if get("c", 3, 0) != 4 {
		t.Fatal("the value after a failed computation must be fresh")
	}
}

// Download counters are batched in memory; they must reach the database, and cleanup must see them.
func TestDownloadCountersAreBatched(t *testing.T) {
	e := newEnv(t)
	e.must("upload", 201, "PUT", "/repository/raw-hosted/tool.bin", strings.NewReader("data"), admin)
	for i := 0; i < 3; i++ {
		e.must("download", 200, "GET", "/repository/raw-hosted/tool.bin", nil, admin)
	}
	e.must("HEAD is not a download", 200, "HEAD", "/repository/raw-hosted/tool.bin", nil, admin)
	e.a.flushDownloads()
	x, err := e.a.findAsset("raw-hosted", "tool.bin")
	if err != nil || x.Downloads != 3 || x.LastDownloaded == 0 {
		t.Fatalf("downloads = %+v, %v; want 3", x, err)
	}
	if b := e.must("repos", 200, "GET", "/api/repositories/raw-hosted", nil, admin); !strings.Contains(b, `"downloads":3`) {
		t.Fatalf("repository totals lack the downloads: %s", b)
	}

	// an old artifact that was downloaded a moment ago is in use: the cleanup policy must keep it
	e.a.db.Exec(`UPDATE assets SET updated_at=1, last_downloaded=1 WHERE repo='raw-hosted'`)
	e.must("download", 200, "GET", "/repository/raw-hosted/tool.bin", nil, admin)
	repo, _ := e.a.getRepo("raw-hosted")
	repo.CleanupDays = 30
	if list, err := e.a.cleanupCandidates(repo); err != nil || len(list) != 0 {
		t.Fatalf("cleanup would delete a file downloaded just now: %v, %v", list, err)
	}
}

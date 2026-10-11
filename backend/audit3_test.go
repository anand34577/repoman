package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// searchPaths returns the paths a search finds and the reported total.
func (e *testEnv) searchPaths(query string) ([]string, string) {
	e.t.Helper()
	c, b, h := e.req("GET", "/api/search?"+query, nil, admin)
	if c != 200 {
		e.t.Fatalf("search %s -> %d: %s", query, c, b)
	}
	var list []Asset
	json.Unmarshal([]byte(b), &list)
	paths := []string{}
	for _, x := range list {
		paths = append(paths, x.Path)
	}
	return paths, h.Get("X-Total-Count")
}

// Search is answered from the substring index. It must find exactly what a scan finds: any part of a
// path or component, in any case, and follow uploads, overwrites and deletions.
func TestSearchIndex(t *testing.T) {
	e := newEnv(t)
	if !e.a.searchIndex {
		t.Fatal("the search index was not created")
	}
	put := func(repo, p string) {
		e.must("upload "+p, 201, "PUT", "/repository/"+repo+"/"+p, strings.NewReader(p), admin)
	}
	put("maven-releases", "com/acme/widget/1.0.0/widget-1.0.0.jar")
	put("maven-releases", "com/acme/widget/1.1.0/widget-1.1.0.jar")
	put("raw-hosted", "tools/Widget_Setup.exe")
	put("raw-hosted", "tools/50%25_off.txt") // stored as 50%_off.txt
	put("raw-hosted", "docs/readme.md")

	for query, want := range map[string]int{
		"q=widget": 3, "q=WIDGET": 3, "q=idget-1.1": 1, "q=com.acme:widget": 2, // the component, not a path
		"q=acme/widget": 2, "q=readme.md": 1, "q=zzz": 0, "q=wi": 3, // two characters: scanned
		"q=50%25_off": 1, "q=50%25": 1, "q=_": 2, // LIKE wildcards are matched literally
		"q=widget&repo=raw-hosted": 1, "q=widget&format=maven": 2, "q=widget&sort=name": 3, "q=widget&sort=size": 3,
		"q=": 5, "sort=name": 5, "sort=size": 5, "sort=downloads": 5,
	} {
		paths, total := e.searchPaths(query)
		if len(paths) != want || total != fmt.Sprint(want) {
			t.Errorf("search %q: %d results (total %s), want %d: %v", query, len(paths), total, want, paths)
		}
	}
	if paths, _ := e.searchPaths("sort=name"); paths[0] != "docs/readme.md" || paths[4] != "com/acme/widget/1.1.0/widget-1.1.0.jar" {
		t.Errorf("sorted by name: %v", paths) // files without a component first, then by component and path
	}
	if paths, total := e.searchPaths("q=widget&limit=2&offset=2"); len(paths) != 1 || total != "3" {
		t.Errorf("second page: %v, total %s", paths, total)
	}

	e.must("delete", 204, "DELETE", "/repository/raw-hosted/tools/Widget_Setup.exe", nil, admin)
	if paths, _ := e.searchPaths("q=widget"); len(paths) != 2 {
		t.Errorf("after delete: %v", paths)
	}
	// the same path stored again with another component must be found under the new one only
	if _, err := e.a.putAsset("raw-hosted", "docs/readme.md", strings.NewReader("v2"), assetMeta{Component: "handbook"}); err != nil {
		t.Fatal(err)
	}
	if paths, _ := e.searchPaths("q=handbook"); len(paths) != 1 {
		t.Errorf("after overwrite: %v", paths)
	}
	e.must("delete repository", 204, "DELETE", "/api/repositories/maven-releases", nil, admin)
	if paths, _ := e.searchPaths("q=acme"); len(paths) != 0 {
		t.Errorf("after deleting the repository: %v", paths)
	}
	var orphans int
	e.a.db.QueryRow(`SELECT COUNT(*) FROM assets_fts WHERE text LIKE '%widget%'`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("%d index entries left for deleted artifacts", orphans)
	}
}

// A term that matches very many artifacts is served by a scan in the requested order, a rare one from
// the index; both must agree with each other and report capped totals.
func TestSearchCommonAndRareTerms(t *testing.T) {
	e := newEnv(t)
	tx, _ := e.a.db.Begin()
	now := time.Now().Unix()
	for i := 0; i < sparseMatches+500; i++ {
		tx.Exec(`INSERT INTO assets(repo, path, blob, size, content_type, component, version, uploader, created_at, updated_at) VALUES('raw-hosted',?,?,?,'text/plain','','','ci',?,?)`,
			fmt.Sprintf("bulk/common-%05d.bin", i), fmt.Sprintf("%064d", i), i, now, now+int64(i))
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	paths, total := e.searchPaths("q=common&limit=3")
	if total != fmt.Sprint(sparseMatches+500) || len(paths) != 3 || paths[0] != fmt.Sprintf("bulk/common-%05d.bin", sparseMatches+499) {
		t.Errorf("common term: total %s, newest first %v", total, paths)
	}
	if paths, total := e.searchPaths("q=common-00042"); total != "1" || len(paths) != 1 {
		t.Errorf("rare term: total %s, %v", total, paths)
	}
	if paths, _ := e.searchPaths("q=common&sort=size&limit=1"); len(paths) != 1 || paths[0] != fmt.Sprintf("bulk/common-%05d.bin", sparseMatches+499) {
		t.Errorf("common term by size: %v", paths)
	}
}

// A data directory from an earlier version gets its search index built on the first start.
func TestSearchIndexIsBuiltForExistingData(t *testing.T) {
	dir := t.TempDir()
	a, err := newApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.bootstrap("admin123")
	a.putAsset("raw-hosted", "legacy/old-file.txt", strings.NewReader("x"), assetMeta{})
	for _, stmt := range []string{`DROP TRIGGER assets_fts_insert`, `DROP TRIGGER assets_fts_delete`, `DROP TRIGGER assets_fts_update`, `DROP TABLE assets_fts`, `DROP VIEW assets_search`} {
		if _, err := a.db.Exec(stmt); err != nil {
			t.Fatal(stmt, err)
		}
	}
	a.db.Close()

	if a, err = newApp(dir); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer func() { srv.Close(); a.db.Close() }()
	e := &testEnv{t, a, srv}
	if paths, _ := e.searchPaths("q=old-file"); len(paths) != 1 {
		t.Fatalf("existing artifacts are not in the rebuilt index: %v", paths)
	}
}

func TestAuditDateRange(t *testing.T) {
	e := newEnv(t)
	e.a.db.Exec(`DELETE FROM events`)
	for i, ts := range []int64{1000, 2000, 3000} {
		e.a.db.Exec(`INSERT INTO events(ts, username, action, target) VALUES(?, 'admin', 'test.event', ?)`, ts, fmt.Sprint("t", i))
	}
	for query, want := range map[string]string{
		"": "3", "from=2000": "2", "to=2000": "1", "from=1000&to=3000": "2", "from=3001": "0", "from=0&to=0": "3", "from=abc": "3",
	} {
		_, _, h := e.req("GET", "/api/audit?action=test&"+query, nil, admin)
		if got := h.Get("X-Total-Count"); got != want {
			t.Errorf("audit ?%s: %s events, want %s", query, got, want)
		}
	}
	if csv := e.must("export", 200, "GET", "/api/audit/export?action=test&from=2000&to=3000", nil, admin); strings.Count(csv, "test.event") != 1 {
		t.Errorf("the export ignores the date range:\n%s", csv)
	}
}

// Invalidating a proxy's cache makes index data stale without touching the cached files: their dates
// stay (Last-Modified, "updated" sorting, and the cleanup policy's "unused for N days" rely on them).
func TestInvalidateKeepsFileDates(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("content of " + r.URL.Path))
	}))
	defer up.Close()
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "mvn-proxy", "format": "maven", "type": "proxy", "remoteUrl": up.URL, "metadataTtl": 1440}), admin)
	const meta, jar = "/repository/mvn-proxy/com/acme/lib/maven-metadata.xml", "/repository/mvn-proxy/com/acme/lib/1.0/lib-1.0.jar"
	e.must("metadata", 200, "GET", meta, nil, admin)
	e.must("jar", 200, "GET", jar, nil, admin)
	e.must("metadata again", 200, "GET", meta, nil, admin)
	if hits.Load() != 2 {
		t.Fatalf("%d upstream requests before invalidation, want 2", hits.Load())
	}
	e.must("invalidate", 204, "POST", "/api/repositories/mvn-proxy/invalidate", nil, admin)
	time.Sleep(1100 * time.Millisecond) // the cache clock has one-second resolution
	e.must("metadata after invalidation", 200, "GET", meta, nil, admin)
	e.must("jar after invalidation", 200, "GET", jar, nil, admin)
	e.must("metadata is fresh again", 200, "GET", meta, nil, admin)
	if hits.Load() != 3 {
		t.Fatalf("%d upstream requests after invalidation, want 3 (only the metadata is fetched again, once)", hits.Load())
	}
	var zeroed int
	e.a.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE repo='mvn-proxy' AND updated_at=0`).Scan(&zeroed)
	if zeroed != 0 {
		t.Fatalf("invalidation reset the date of %d cached files", zeroed)
	}
	// saving the configuration must not undo the invalidation
	e.must("update", 200, "PUT", "/api/repositories/mvn-proxy", jsonBody(map[string]any{"remoteUrl": up.URL, "metadataTtl": 1440, "description": "x"}), admin)
	if repo, _ := e.a.getRepo("mvn-proxy"); repo.InvalidatedAt == 0 {
		t.Fatal("updating the repository forgot the invalidation")
	}
}

func TestUnknownWebhook(t *testing.T) {
	e := newEnv(t)
	e.must("update", 404, "PUT", "/api/webhooks/999", jsonBody(map[string]any{"name": "x", "url": "http://127.0.0.1:1/hook"}), admin)
	e.must("delete", 404, "DELETE", "/api/webhooks/999", nil, admin)
}

// Unexpected server errors are logged; the client gets a reference, not the database's words.
func TestInternalErrorsStayInTheLog(t *testing.T) {
	e := newEnv(t)
	e.a.db.Exec(`DROP TABLE webhooks`)
	b := e.must("list", 500, "GET", "/api/webhooks", nil, admin)
	if strings.Contains(b, "webhooks") || strings.Contains(b, "SQL") || !strings.Contains(b, "server log") {
		t.Fatalf("internal error text was sent to the client: %s", b)
	}
}

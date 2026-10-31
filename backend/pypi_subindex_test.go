package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A flat upstream like download.pytorch.org/whl: <root>/<flavor>/<name>/ with no /simple/, and
// wheel links pointing at another host. One proxy of the root must serve every flavor.
func TestPypiProxySubIndexFlatLayout(t *testing.T) {
	e := newEnv(t)
	var wheelHits atomic.Int64
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wheelHits.Add(1)
		w.Write([]byte("wheel:" + r.URL.Path))
	}))
	defer files.Close()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cu126/torch/", "/cpu/torch/":
			flavor := strings.Split(r.URL.Path, "/")[1]
			w.Write([]byte(`<a href="` + files.URL + `/whl/` + flavor + `/torch-2.8.0%2B` + flavor + `-cp310-cp310-win_amd64.whl#sha256=abc" data-requires-python="&gt;=3.9">torch-2.8.0+` + flavor + `-cp310-cp310-win_amd64.whl</a>`))
		case "/":
			w.Write([]byte("index"))
		default:
			http.Error(w, "forbidden", http.StatusForbidden) // what the real index answers for /simple/…
		}
	}))
	defer up.Close()

	px, _ := e.a.getRepo("pypi-proxy")
	px.RemoteURL = up.URL
	e.a.saveRepo(px)

	for _, flavor := range []string{"cu126", "cpu"} {
		page := e.must("project page "+flavor, 200, "GET", "/repository/pypi-proxy/"+flavor+"/simple/torch/", nil, "")
		want := `href="../../packages/torch/torch-2.8.0+` + flavor + `-cp310-cp310-win_amd64.whl#sha256=abc"`
		if !strings.Contains(page, want) || !strings.Contains(page, `data-requires-python="&gt;=3.9"`) {
			t.Fatalf("%s page not rewritten:\n%s", flavor, page)
		}
		got := e.must("wheel "+flavor, 200, "GET", "/repository/pypi-proxy/"+flavor+"/packages/torch/torch-2.8.0%2B"+flavor+"-cp310-cp310-win_amd64.whl", nil, "")
		if got != "wheel:/whl/"+flavor+"/torch-2.8.0+"+flavor+"-cp310-cp310-win_amd64.whl" {
			t.Fatalf("%s wheel body %q", flavor, got)
		}
	}
	// cached: the second download does not touch the file host again
	before := wheelHits.Load()
	e.must("cached wheel", 200, "GET", "/repository/pypi-proxy/cu126/packages/torch/torch-2.8.0%2Bcu126-cp310-cp310-win_amd64.whl", nil, "")
	if wheelHits.Load() != before {
		t.Fatal("wheel was fetched again instead of served from the cache")
	}
	// the same file name in different flavors never shares a cache entry
	e.must("unknown package", 404, "GET", "/repository/pypi-proxy/cu126/simple/nope/", nil, "")
	e.must("unknown flavor", 404, "GET", "/repository/pypi-proxy/cu999/simple/torch/", nil, "")
	e.must("sub-index root is not listable", 404, "GET", "/repository/pypi-proxy/cu126/simple/", nil, "")
	e.must("traversal in the sub-index is refused", 404, "GET", "/repository/pypi-proxy/a/../../x/simple/torch/", nil, "")
	e.must("hosted repos have no sub-indexes", 404, "GET", "/repository/pypi-hosted/cu126/simple/torch/", nil, "")

	// the group exposes the same sub-indexes, and the default index keeps working
	e.must("group sub-index", 200, "GET", "/repository/pypi-public/cu126/simple/torch/", nil, "")
	e.must("default index unaffected", 404, "GET", "/repository/pypi-public/simple/torch/", nil, "")
}

// The standard layout, with and without a sub-index, keeps resolving relative links.
func TestPypiProxyStandardLayoutSubIndex(t *testing.T) {
	e := newEnv(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/simple/six/", "/extra/simple/six/":
			w.Write([]byte(`<a href="../../packages/six-1.0-py3-none-any.whl#sha256=ff">six-1.0-py3-none-any.whl</a>`))
		case "/packages/six-1.0-py3-none-any.whl":
			w.Write([]byte("root-wheel"))
		case "/extra/packages/six-1.0-py3-none-any.whl":
			w.Write([]byte("extra-wheel"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	px, _ := e.a.getRepo("pypi-proxy")
	px.RemoteURL = up.URL
	e.a.saveRepo(px)

	e.must("page", 200, "GET", "/repository/pypi-proxy/simple/six/", nil, "")
	if b := e.must("wheel", 200, "GET", "/repository/pypi-proxy/packages/six/six-1.0-py3-none-any.whl", nil, ""); b != "root-wheel" {
		t.Fatalf("root wheel %q", b)
	}
	e.must("sub page", 200, "GET", "/repository/pypi-proxy/extra/simple/six/", nil, "")
	if b := e.must("sub wheel", 200, "GET", "/repository/pypi-proxy/extra/packages/six/six-1.0-py3-none-any.whl", nil, ""); b != "extra-wheel" {
		t.Fatalf("sub wheel %q", b)
	}
}

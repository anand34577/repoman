package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A cache miss on a file streams to the first client while it is cached: the first bytes arrive
// before the upstream has finished, and the next request is served from the cache.
func TestProxyStreamsWhileCaching(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	var hits int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Length", "10")
		w.Write([]byte("hello"))
		w.(http.Flusher).Flush()
		<-release
		w.Write([]byte("world"))
	}))
	defer up.Close()
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "mvn-proxy", "format": "maven", "type": "proxy", "remoteUrl": up.URL}), admin)

	req, _ := http.NewRequest("GET", e.srv.URL+"/repository/mvn-proxy/com/acme/big/1.0/big-1.0.jar", nil)
	req.SetBasicAuth("admin", "admin123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, 5)
	got := make(chan error, 1)
	go func() { _, err := io.ReadFull(bufio.NewReader(resp.Body), first); got <- err }()
	select {
	case err := <-got:
		if err != nil || string(first) != "hello" {
			t.Fatalf("first chunk %q, %v", first, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no bytes before the upstream finished: the proxy still downloads everything first")
	}
	close(release)
	rest, _ := io.ReadAll(resp.Body)
	if string(rest) != "world" {
		t.Fatalf("rest %q", rest)
	}
	time.Sleep(200 * time.Millisecond)
	if body := e.must("cached", 200, "GET", "/repository/mvn-proxy/com/acme/big/1.0/big-1.0.jar", nil, admin); body != "helloworld" || hits != 1 {
		t.Fatalf("cached body %q after %d upstream requests", body, hits)
	}
}

// A HEAD on a cache miss answers as soon as the upstream's headers arrive, and the file still gets cached.
func TestProxyHeadDoesNotWaitForDownload(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.Write([]byte("hello"))
		w.(http.Flusher).Flush()
		<-release
		w.Write([]byte("world"))
	}))
	defer up.Close()
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "mvn-proxy", "format": "maven", "type": "proxy", "remoteUrl": up.URL}), admin)
	done := make(chan http.Header, 1)
	go func() { _, _, h := e.req("HEAD", "/repository/mvn-proxy/a/b/1.0/b-1.0.jar", nil, admin); done <- h }()
	select {
	case h := <-done:
		if h.Get("Content-Length") != "10" {
			t.Fatalf("HEAD headers %v", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("HEAD waited for the whole download")
	}
	close(release)
	time.Sleep(300 * time.Millisecond)
	e.must("cached", 200, "GET", "/repository/mvn-proxy/a/b/1.0/b-1.0.jar", nil, admin)
}

// An upstream that stops sending is given up on instead of hanging the request (and everyone waiting on it) forever.
func TestProxyStalledUpstreamTimesOut(t *testing.T) {
	e := newEnv(t)
	e.a.upstreamIdle = 300 * time.Millisecond
	stop := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		<-stop
	}))
	defer up.Close()
	defer close(stop)
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "raw-proxy", "format": "raw", "type": "proxy", "remoteUrl": up.URL}), admin)
	done := make(chan struct{})
	go func() { e.req("GET", "/repository/raw-proxy/big.bin", nil, admin); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request hung on a stalled upstream")
	}
	if _, err := e.a.findAsset("raw-proxy", "big.bin"); err == nil {
		t.Fatal("a truncated download was cached")
	}
}

func slowUpstream(release chan struct{}, hits *atomic.Int32, extra func(http.ResponseWriter)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if extra != nil {
			extra(w)
		}
		w.Header().Set("Content-Length", "10")
		w.Write([]byte("hello"))
		w.(http.Flusher).Flush()
		<-release
		w.Write([]byte("world"))
	}))
}

func getStream(t *testing.T, url string, hdr ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.SetBasicAuth("admin", "admin123")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp, ""
}

func readN(t *testing.T, resp *http.Response, n int) string {
	t.Helper()
	b := make([]byte, n)
	got := make(chan error, 1)
	go func() { _, err := io.ReadFull(resp.Body, b); got <- err }()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no bytes before the download finished")
	}
	return string(b)
}

// Clients arriving while a file downloads are served from the running download: all get bytes immediately,
// a Range request is answered with 206 from the same download, and the upstream is contacted once.
func TestConcurrentClientsShareOneDownload(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	var hits atomic.Int32
	up := slowUpstream(release, &hits, nil)
	defer up.Close()
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "mvn-proxy", "format": "maven", "type": "proxy", "remoteUrl": up.URL}), admin)
	url := e.srv.URL + "/repository/mvn-proxy/x/y/1.0/y-1.0.jar"
	r1, _ := getStream(t, url)
	defer r1.Body.Close()
	if got := readN(t, r1, 5); got != "hello" {
		t.Fatalf("first client got %q", got)
	}
	r2, _ := getStream(t, url)
	defer r2.Body.Close()
	if got := readN(t, r2, 5); got != "hello" {
		t.Fatalf("second client got %q", got)
	}
	r3, _ := getStream(t, url, "Range", "bytes=2-6")
	defer r3.Body.Close()
	if r3.StatusCode != 206 || r3.Header.Get("Content-Range") != "bytes 2-6/10" {
		t.Fatalf("range answer %d %v", r3.StatusCode, r3.Header)
	}
	if got := readN(t, r3, 3); got != "llo" {
		t.Fatalf("range client got %q", got)
	}
	var dls struct{ Active []any }
	json.Unmarshal([]byte(e.must("downloads", 200, "GET", "/api/system/downloads", nil, admin)), &dls)
	if len(dls.Active) != 1 {
		t.Fatalf("%d active downloads, want 1", len(dls.Active))
	}
	close(release)
	for _, c := range []*http.Response{r1, r2} {
		if rest, _ := io.ReadAll(c.Body); string(rest) != "world" {
			t.Fatalf("rest %q", rest)
		}
	}
	if rest, _ := io.ReadAll(r3.Body); string(rest) != "wo" {
		t.Fatalf("range rest %q", rest)
	}
	time.Sleep(200 * time.Millisecond)
	if body := e.must("cached", 200, "GET", "/repository/mvn-proxy/x/y/1.0/y-1.0.jar", nil, admin); body != "helloworld" || hits.Load() != 1 {
		t.Fatalf("cached %q after %d upstream requests", body, hits.Load())
	}
}

// Hugging Face: the commit id reaches the client on the first answer, and the pinned request that follows
// is served from the branch download instead of fetching the file again.
func TestHuggingFaceCommitAndPinnedLink(t *testing.T) {
	e := newEnv(t)
	release := make(chan struct{})
	var hits atomic.Int32
	commit := strings.Repeat("ab", 20)
	cdn := slowUpstream(release, &hits, nil)
	defer cdn.Close()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Repo-Commit", commit)
		http.Redirect(w, r, cdn.URL+"/blob", http.StatusFound)
	}))
	defer hub.Close()
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "hf", "format": "huggingface", "type": "proxy", "remoteUrl": hub.URL}), admin)
	main := e.srv.URL + "/repository/hf/org/model/resolve/main/w.bin"
	r1, _ := getStream(t, main)
	defer r1.Body.Close()
	if got := r1.Header.Get("X-Repo-Commit"); got != commit {
		t.Fatalf("commit header %q", got)
	}
	readN(t, r1, 5)
	r2, _ := getStream(t, e.srv.URL+"/repository/hf/org/model/resolve/"+commit+"/w.bin")
	defer r2.Body.Close()
	if got := readN(t, r2, 5); got != "hello" {
		t.Fatalf("pinned client got %q", got)
	}
	close(release)
	io.ReadAll(r1.Body)
	io.ReadAll(r2.Body)
	time.Sleep(300 * time.Millisecond)
	e.must("pinned cached", 200, "GET", "/repository/hf/org/model/resolve/"+commit+"/w.bin", nil, admin)
	if hits.Load() != 1 {
		t.Fatalf("%d downloads of the file, want 1", hits.Load())
	}
}

func TestDiskGuardRefusesCaching(t *testing.T) {
	e := newEnv(t)
	e.a.diskReserve = 1 << 60
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("data")) }))
	defer up.Close()
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "raw-proxy", "format": "raw", "type": "proxy", "remoteUrl": up.URL}), admin)
	if code, _, _ := e.req("GET", "/repository/raw-proxy/f.bin", nil, admin); code != 507 {
		t.Fatalf("status %d, want 507", code)
	}
}

func TestWarmCache(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte("data " + r.URL.Path)) }))
	defer up.Close()
	e.must("create proxy", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "raw-proxy", "format": "raw", "type": "proxy", "remoteUrl": up.URL}), admin)
	e.must("warm", 200, "POST", "/api/repositories/raw-proxy/warm", jsonBody(map[string]any{"paths": []string{"a.bin", "/b.bin"}}), admin)
	for i := 0; i < 50 && hits.Load() < 2; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	e.must("a", 200, "GET", "/repository/raw-proxy/a.bin", nil, admin)
	e.must("b", 200, "GET", "/repository/raw-proxy/b.bin", nil, admin)
	if hits.Load() != 2 {
		t.Fatalf("%d upstream requests, want 2", hits.Load())
	}
	e.must("bad path", 400, "POST", "/api/repositories/raw-proxy/warm", jsonBody(map[string]any{"paths": []string{"../x"}}), admin)
}

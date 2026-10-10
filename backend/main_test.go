package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"
)

// End-to-end check of every format's hosted + group paths through the real HTTP stack.
func TestEndToEnd(t *testing.T) {
	e := newEnv(t)
	a, srv := e.a, e.srv

	do := func(method, path string, body io.Reader, hdr map[string]string, auth bool) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, body)
		if auth {
			req.SetBasicAuth("admin", "admin123")
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header
	}
	expect := func(what string, got, want int, body string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s: status %d, want %d: %s", what, got, want, body)
		}
	}

	// raw: anonymous write denied, deploy, read, redeploy policy
	c, b, _ := do("PUT", "/repository/raw-hosted/docs/a.txt", strings.NewReader("hello"), nil, false)
	expect("anon put", c, 401, b)
	c, b, _ = do("PUT", "/repository/raw-hosted/docs/a.txt", strings.NewReader("hello"), nil, true)
	expect("raw put", c, 201, b)
	c, b, _ = do("GET", "/repository/raw-hosted/docs/a.txt", nil, nil, false)
	if c != 200 || b != "hello" {
		t.Fatalf("raw get: %d %q", c, b)
	}

	// maven: release redeploy blocked, metadata redeploy allowed, group resolves
	jar := "/repository/maven-releases/com/acme/lib/1.0/lib-1.0.jar"
	c, b, _ = do("PUT", jar, strings.NewReader("jar"), nil, true)
	expect("maven put", c, 201, b)
	c, b, _ = do("PUT", jar, strings.NewReader("jar2"), nil, true)
	expect("maven redeploy", c, 400, b)
	c, b, _ = do("PUT", "/repository/maven-releases/com/acme/lib/maven-metadata.xml", strings.NewReader("<m/>"), nil, true)
	expect("maven metadata", c, 201, b)
	c, b, _ = do("PUT", "/repository/maven-releases/com/acme/lib/maven-metadata.xml", strings.NewReader("<m2/>"), nil, true)
	expect("maven metadata redeploy", c, 201, b)
	c, b, _ = do("GET", "/repository/maven-public/com/acme/lib/1.0/lib-1.0.jar", nil, nil, false)
	if c != 200 || b != "jar" {
		t.Fatalf("maven group get: %d %q", c, b)
	}
	x, _ := a.findAsset("maven-releases", "com/acme/lib/1.0/lib-1.0.jar")
	if x.Component != "com.acme:lib" || x.Version != "1.0" {
		t.Fatalf("maven component: %+v", x)
	}

	// npm: publish scoped package, read via group with rewritten tarball URL
	tgz := []byte("fake-tarball")
	pub, _ := json.Marshal(map[string]any{
		"name":         "@acme/util",
		"versions":     map[string]any{"1.0.0": map[string]any{"name": "@acme/util", "version": "1.0.0", "dist": map[string]any{"tarball": "http://x/@acme/util/-/util-1.0.0.tgz"}}},
		"dist-tags":    map[string]string{"latest": "1.0.0"},
		"_attachments": map[string]any{"@acme/util-1.0.0.tgz": map[string]string{"data": base64.StdEncoding.EncodeToString(tgz)}},
	})
	c, b, _ = do("PUT", "/repository/npm-hosted/@acme%2futil", bytes.NewReader(pub), map[string]string{"Content-Type": "application/json"}, true)
	expect("npm publish", c, 201, b)
	c, b, _ = do("GET", "/repository/npm-public/@acme%2futil", nil, nil, false)
	expect("npm packument", c, 200, b)
	wantTarball := srv.URL + "/repository/npm-public/@acme/util/-/util-1.0.0.tgz"
	if !strings.Contains(b, wantTarball) {
		t.Fatalf("npm tarball not rewritten: %s", b)
	}
	c, b, _ = do("GET", strings.TrimPrefix(wantTarball, srv.URL), nil, nil, false)
	if c != 200 || b != string(tgz) {
		t.Fatalf("npm tarball: %d %q", c, b)
	}
	c, b, _ = do("PUT", "/repository/npm-hosted/-/user/org.couchdb.user:admin", strings.NewReader(`{"name":"admin","password":"admin123"}`), nil, false)
	expect("npm login", c, 201, b)
	var login struct{ Token string }
	json.Unmarshal([]byte(b), &login)
	c, b, _ = do("GET", "/repository/npm-hosted/-/whoami", nil, map[string]string{"Authorization": "Bearer " + login.Token}, false)
	if c != 200 || !strings.Contains(b, "admin") {
		t.Fatalf("npm whoami: %d %s", c, b)
	}

	// pypi: twine-style upload, simple index through group, download via relative link
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	mw.WriteField(":action", "file_upload")
	mw.WriteField("name", "My_Pkg")
	mw.WriteField("version", "0.1")
	fw, _ := mw.CreateFormFile("content", "my_pkg-0.1-py3-none-any.whl")
	fw.Write([]byte("wheel"))
	mw.Close()
	c, b, _ = do("POST", "/repository/pypi-hosted/", &form, map[string]string{"Content-Type": mw.FormDataContentType()}, true)
	expect("pypi upload", c, 200, b)
	c, b, _ = do("GET", "/repository/pypi-public/simple/my-pkg/", nil, nil, false)
	if c != 200 || !strings.Contains(b, `href="../../packages/my-pkg/my_pkg-0.1-py3-none-any.whl#sha256=`) {
		t.Fatalf("pypi simple: %d %s", c, b)
	}
	c, b, _ = do("GET", "/repository/pypi-public/packages/my-pkg/my_pkg-0.1-py3-none-any.whl", nil, nil, false)
	if c != 200 || b != "wheel" {
		t.Fatalf("pypi download: %d %q", c, b)
	}

	// docker: chunked blob upload, manifest push by tag, pull, tags list
	c, _, _ = do("GET", "/v2/", nil, nil, false)
	expect("docker ping anon", c, 401, "")
	layer := []byte("layer-data")
	sum := sha256.Sum256(layer)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	c, b, h := do("POST", "/v2/docker-hosted/team/app/blobs/uploads/", nil, nil, true)
	expect("docker upload start", c, 202, b)
	loc := h.Get("Location")
	c, b, _ = do("PATCH", loc, bytes.NewReader(layer[:4]), nil, true)
	expect("docker patch", c, 202, b)
	c, b, _ = do("PUT", loc+"?digest="+digest, bytes.NewReader(layer[4:]), nil, true)
	expect("docker upload finish", c, 201, b)
	manifest := []byte(`{"schemaVersion":2,"layers":[{"digest":"` + digest + `"}]}`)
	ct := "application/vnd.oci.image.manifest.v1+json"
	c, b, h = do("PUT", "/v2/docker-hosted/team/app/manifests/v1", bytes.NewReader(manifest), map[string]string{"Content-Type": ct}, true)
	expect("docker manifest put", c, 201, b)
	c, b, h = do("GET", "/v2/docker-hosted/team/app/manifests/v1", nil, nil, true)
	if c != 200 || b != string(manifest) || h.Get("Content-Type") != ct || h.Get("Docker-Content-Digest") == "" {
		t.Fatalf("docker manifest get: %d %s %v", c, b, h)
	}
	c, b, _ = do("GET", "/v2/docker-hosted/team/app/blobs/"+digest, nil, nil, true)
	if c != 200 || b != string(layer) {
		t.Fatalf("docker blob get: %d %q", c, b)
	}
	c, _, _ = do("HEAD", "/v2/docker-hosted/other/img/blobs/"+digest, nil, nil, true)
	expect("docker cross-image blob dedupe", c, 200, "")
	c, b, _ = do("GET", "/v2/docker-hosted/team/app/tags/list", nil, nil, true)
	if c != 200 || !strings.Contains(b, `"v1"`) {
		t.Fatalf("docker tags: %d %s", c, b)
	}

	// deleting the only asset referencing a blob removes the file
	a.deleteAssets("repo=? AND path=?", "raw-hosted", "docs/a.txt")
	hs := sha256.Sum256([]byte("hello"))
	if _, err := os.Stat(a.blobs.path(hex.EncodeToString(hs[:]))); !os.IsNotExist(err) {
		t.Fatalf("blob not garbage collected: %v", err)
	}

	// management API requires a session
	c, _, _ = do("GET", "/api/repositories", nil, nil, false)
	expect("api anon", c, 401, "")
	c, b, _ = do("GET", "/api/stats", nil, nil, true)
	expect("api stats", c, 200, b)
}

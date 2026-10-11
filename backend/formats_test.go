package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func tgz(files map[string]string) []byte {
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for n, c := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(c))})
		tw.Write([]byte(c))
	}
	tw.Close()
	gz.Close()
	return b.Bytes()
}

func TestCargoHosted(t *testing.T) {
	e := newEnv(t)
	mkRepo(e, "crates", "cargo", "hosted", nil)
	crate := []byte("crate bytes")
	meta, _ := json.Marshal(map[string]any{"name": "Acme-Lib", "vers": "0.1.0", "deps": []map[string]any{{"name": "serde", "version_req": "^1", "features": []string{}, "optional": false, "default_features": true, "kind": "normal"}}, "features": map[string][]string{"x": {"dep:serde"}}})
	var body bytes.Buffer
	binary.Write(&body, binary.LittleEndian, uint32(len(meta)))
	body.Write(meta)
	binary.Write(&body, binary.LittleEndian, uint32(len(crate)))
	body.Write(crate)
	e.must("publish", 200, "PUT", "/repository/crates/api/v1/crates/new", &body, admin)

	if c := e.must("config", 200, "GET", "/repository/crates/config.json", nil, ""); !strings.Contains(c, "/repository/crates/api/v1/crates") {
		t.Fatal(c)
	}
	sum := sha256.Sum256(crate)
	idx := e.must("index", 200, "GET", "/repository/crates/ac/me/acme-lib", nil, "")
	if !strings.Contains(idx, `"cksum":"`+hex.EncodeToString(sum[:])+`"`) || !strings.Contains(idx, `"features2"`) || !strings.Contains(idx, `"yanked":false`) {
		t.Fatal(idx)
	}
	if b := e.must("download", 200, "GET", "/repository/crates/api/v1/crates/acme-lib/0.1.0/download", nil, ""); b != string(crate) {
		t.Fatal("crate differs")
	}
	e.must("yank", 200, "DELETE", "/repository/crates/api/v1/crates/acme-lib/0.1.0/yank", nil, admin)
	if idx := e.must("index", 200, "GET", "/repository/crates/ac/me/acme-lib", nil, ""); !strings.Contains(idx, `"yanked":true`) {
		t.Fatal(idx)
	}
	e.must("wrong index path", 404, "GET", "/repository/crates/xx/acme-lib", nil, "")
}

func TestCargoProxy(t *testing.T) {
	e := newEnv(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config.json":
			w.Write([]byte(`{"dl":"http://` + r.Host + `/crates"}`))
		case "/se/rd/serde":
			w.Write([]byte(`{"name":"serde","vers":"1.0.0","deps":[{"req":">= 1, < 2"}]}` + "\n"))
		case "/crates/serde/1.0.0/download":
			w.Write([]byte("serde bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	mkRepo(e, "crates-io", "cargo", "proxy", map[string]any{"remoteUrl": up.URL})
	if idx := e.must("index", 200, "GET", "/repository/crates-io/se/rd/serde", nil, ""); !strings.Contains(idx, ">= 1, < 2") {
		t.Fatal(idx)
	}
	e.must("download", 200, "GET", "/repository/crates-io/api/v1/crates/serde/1.0.0/download", nil, "")
}

func TestPubHosted(t *testing.T) {
	e := newEnv(t)
	mkRepo(e, "dart", "pub", "hosted", nil)
	archive := tgz(map[string]string{"pubspec.yaml": "name: acme_pkg\nversion: 1.2.0\ndependencies:\n  http: ^1.0.0\n"})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "package.tar.gz")
	fw.Write(archive)
	mw.Close()

	e.must("new", 200, "GET", "/repository/dart/api/packages/versions/new", nil, admin)
	c, _, h := e.req("POST", "/repository/dart/api/packages/versions/newUpload", &body, admin, "Content-Type", mw.FormDataContentType())
	loc := h.Get("Location")
	if c != 204 || !strings.Contains(loc, "newUploadFinish") || strings.Contains(loc, "error=") {
		t.Fatalf("upload: %d %s", c, loc)
	}
	var info struct {
		Latest struct {
			Version    string
			ArchiveURL string `json:"archive_url"`
			Pubspec    map[string]any
		}
		Versions []any
	}
	json.Unmarshal([]byte(e.must("listing", 200, "GET", "/repository/dart/api/packages/acme_pkg", nil, "")), &info)
	if len(info.Versions) != 1 || info.Latest.Pubspec["name"] != "acme_pkg" || !strings.HasSuffix(info.Latest.ArchiveURL, "/repository/dart/api/archives/acme_pkg-1.2.0.tar.gz") {
		t.Fatalf("%+v", info)
	}
	if b := e.must("archive", 200, "GET", "/repository/dart/api/archives/acme_pkg-1.2.0.tar.gz", nil, ""); b != string(archive) {
		t.Fatal("archive differs")
	}
	body.Reset()
	mw = multipart.NewWriter(&body)
	fw, _ = mw.CreateFormFile("file", "x.tar.gz")
	fw.Write(archive)
	mw.Close()
	_, _, h = e.req("POST", "/repository/dart/api/packages/versions/newUpload", &body, admin, "Content-Type", mw.FormDataContentType())
	if !strings.Contains(h.Get("Location"), "error=") {
		t.Fatal("republishing a version was accepted")
	}
}

func TestHuggingFaceHosted(t *testing.T) {
	e := newEnv(t)
	mkRepo(e, "hf", "huggingface", "hosted", nil)
	e.must("upload", 201, "PUT", "/repository/hf/acme/tiny/resolve/main/config.json", strings.NewReader(`{"a":1}`), admin)
	e.must("bad path", 400, "PUT", "/repository/hf/acme/config.json", strings.NewReader("x"), admin)
	var info struct {
		Sha      string
		Siblings []struct{ Rfilename string }
	}
	json.Unmarshal([]byte(e.must("info", 200, "GET", "/repository/hf/api/models/acme/tiny", nil, "")), &info)
	if len(info.Siblings) != 1 || info.Siblings[0].Rfilename != "config.json" || len(info.Sha) != 40 {
		t.Fatalf("%+v", info)
	}
	// any revision, including the commit from the info, reaches the current file
	c, b, h := e.req("GET", "/repository/hf/acme/tiny/resolve/"+info.Sha+"/config.json", nil, "")
	if c != 200 || b != `{"a":1}` || h.Get("X-Repo-Commit") != info.Sha {
		t.Fatalf("%d %s %v", c, b, h)
	}
	e.must("missing", 404, "GET", "/repository/hf/api/models/acme/none", nil, "")
}

func TestLFS(t *testing.T) {
	e := newEnv(t)
	mkRepo(e, "lfs", "lfs", "hosted", nil)
	e.must("no proxies", 400, "POST", "/api/repositories", jsonBody(map[string]any{"name": "lfs-p", "format": "lfs", "type": "proxy", "remoteUrl": "http://x"}), admin)
	data := []byte("big file")
	sum := sha256.Sum256(data)
	oid := hex.EncodeToString(sum[:])
	batch := func(op, creds string) string {
		b, _ := json.Marshal(map[string]any{"operation": op, "objects": []map[string]any{{"oid": oid, "size": len(data)}}})
		return e.must(op, 200, "POST", "/repository/lfs/objects/batch", bytes.NewReader(b), creds)
	}
	if r := batch("download", admin); !strings.Contains(r, "does not exist") {
		t.Fatal(r)
	}
	b, _ := json.Marshal(map[string]any{"operation": "upload", "objects": []map[string]any{{"oid": oid, "size": len(data)}}})
	e.must("anonymous upload batch", 401, "POST", "/repository/lfs/objects/batch", bytes.NewReader(b), "")
	if r := batch("upload", admin); !strings.Contains(r, `"upload"`) || !strings.Contains(r, "/objects/"+oid) {
		t.Fatal(r)
	}
	e.must("wrong content", 400, "PUT", "/repository/lfs/objects/"+oid, strings.NewReader("tampered"), admin)
	e.must("put", 200, "PUT", "/repository/lfs/objects/"+oid, bytes.NewReader(data), admin)
	if r := batch("upload", admin); strings.Contains(r, `"upload"`) {
		t.Fatal("stored object offered for upload again: " + r)
	}
	if r := batch("download", admin); !strings.Contains(r, `"download"`) {
		t.Fatal(r)
	}
	e.must("verify", 200, "POST", "/repository/lfs/verify", strings.NewReader(`{"oid":"`+oid+`","size":8}`), admin)
	if got := e.must("get", 200, "GET", "/repository/lfs/objects/"+oid, nil, ""); got != string(data) {
		t.Fatal("object differs")
	}
}

// makeApk builds a minimal .apk: a control tar.gz (.PKGINFO) followed by a data tar.gz.
func makeApk(name, ver string) []byte {
	return append(tgz(map[string]string{".PKGINFO": "pkgname = " + name + "\npkgver = " + ver + "\npkgdesc = demo\narch = x86_64\nsize = 123\ndepend = busybox\ndepend = so:libc.musl-x86_64.so.1\n"}),
		tgz(map[string]string{"usr/bin/" + name: "#!/bin/sh\n"})...)
}

func TestAlpineHosted(t *testing.T) {
	e := newEnv(t)
	mkRepo(e, "apk", "alpine", "hosted", nil)
	e.must("no groups", 400, "POST", "/api/repositories", jsonBody(map[string]any{"name": "apk-g", "format": "alpine", "type": "group", "members": []string{"apk"}}), admin)
	apk := makeApk("acme", "1.0-r0")
	e.must("not an apk", 400, "PUT", "/repository/apk/v3.20/main/x86_64/x.apk", strings.NewReader("nope"), admin)
	e.must("needs a folder", 400, "PUT", "/repository/apk/x.apk", bytes.NewReader(apk), admin)
	// the stored name is canonical whatever the upload was called
	e.must("upload", 201, "PUT", "/repository/apk/v3.20/main/x86_64/whatever.apk", bytes.NewReader(apk), admin)
	if b := e.must("download", 200, "GET", "/repository/apk/v3.20/main/x86_64/acme-1.0-r0.apk", nil, ""); b != string(apk) {
		t.Fatal("apk differs")
	}
	gz, err := gzip.NewReader(strings.NewReader(e.must("index", 200, "GET", "/repository/apk/v3.20/main/x86_64/APKINDEX.tar.gz", nil, "")))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var index string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(tr)
		if h.Name == "APKINDEX" {
			index = string(b)
		}
	}
	for _, want := range []string{"C:Q1", "P:acme\n", "V:1.0-r0\n", "A:x86_64\n", "D:busybox so:libc.musl-x86_64.so.1\n", "S:" + strconv.Itoa(len(apk)) + "\n"} {
		if !strings.Contains(index, want) {
			t.Fatalf("APKINDEX lacks %q:\n%s", want, index)
		}
	}
	e.must("delete", 204, "DELETE", "/repository/apk/v3.20/main/x86_64/acme-1.0-r0.apk", nil, admin)
}

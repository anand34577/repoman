package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// legacyCondaPkg is noarch/legacy-pkg-0.1-py_0.tar.bz2 (made with Python's tarfile + bz2; Go can't write bzip2).
const legacyCondaPkg = "QlpoOTFBWSZTWQUPVoAAAMn/gM2QABBQB/+SACIkCv//32oICDAA2TYaVPaSbKeo0eo0ANNDQeUANqekDVT9QTaNAExMRkZMTTCZMmRhKaVNPUnkm1PQxAmmBPSPKNDIzKXdubzArUAEkGZhimfoZjCETykhgGGEBS+knZJJCHHL0UtllF6slrg/WEExagqRV5tyWJeOg7EV3ViQAnoVOEgEHuMEOeR3GUj30igVOC3MC5ZgBOl5ovtPHRIUKR82fRpEYsEKh7KISuu1uUy2JoNVY1FZykMmBwdwfdGzl4M3yDxiVXfARTZJAIV7Ohi9QtVCajbCQvuKZR6O9sxHiJNY+oj8CEl1FImeL0YYP8XckU4UJAFD1aAA"

// makeCondaPkg builds a .conda (v2) package: a zip with metadata.json, info-*.tar.zst and pkg-*.tar.zst.
func makeCondaPkg(t *testing.T, name, ver, build, subdir string) []byte {
	tarZst := func(files map[string]string) []byte {
		var buf bytes.Buffer
		z, _ := zstd.NewWriter(&buf)
		tw := tar.NewWriter(z)
		for n, c := range files {
			tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(c))})
			tw.Write([]byte(c))
		}
		tw.Close()
		z.Close()
		return buf.Bytes()
	}
	idx, _ := json.Marshal(map[string]any{"name": name, "version": ver, "build": build, "build_number": 0, "subdir": subdir, "depends": []string{"libc"}})
	stem := name + "-" + ver + "-" + build
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for n, c := range map[string][]byte{
		"metadata.json":             []byte(`{"conda_pkg_format_version": 2}`),
		"pkg-" + stem + ".tar.zst":  tarZst(map[string]string{"bin/" + name: "#!/bin/sh\n"}),
		"info-" + stem + ".tar.zst": tarZst(map[string]string{"info/index.json": string(idx), "info/paths.json": "{}"}),
	} {
		w, _ := zw.Create(n)
		w.Write(c)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type repodata struct {
	Info     struct{ Subdir string }
	Packages map[string]struct {
		Name, Version, MD5, SHA256 string
		Size                       int64
	}
	PackagesConda map[string]struct {
		Name, Version, Build, Subdir, SHA256, MD5 string
		Depends                                   []string
		Size                                      int64
	} `json:"packages.conda"`
}

func TestCondaHosted(t *testing.T) {
	e := newEnv(t)
	pkg := makeCondaPkg(t, "acme-tool", "1.2.0", "h1234_0", "linux-64")
	legacy, _ := base64.StdEncoding.DecodeString(legacyCondaPkg)

	e.must("garbage", 400, "PUT", "/repository/conda-hosted/linux-64/x-1-0.conda", strings.NewReader("nope"), admin)
	e.must("wrong file name", 400, "PUT", "/repository/conda-hosted/linux-64/other-1.0-0.conda", bytes.NewReader(pkg), admin)
	e.must("wrong subdir", 400, "PUT", "/repository/conda-hosted/osx-64/acme-tool-1.2.0-h1234_0.conda", bytes.NewReader(pkg), admin)
	e.must("anonymous upload", 401, "POST", "/repository/conda-hosted/", bytes.NewReader(pkg), "")
	e.must("upload to root", 201, "POST", "/repository/conda-hosted/", bytes.NewReader(pkg), admin)
	e.must("redeploy refused", 400, "PUT", "/repository/conda-hosted/linux-64/acme-tool-1.2.0-h1234_0.conda", bytes.NewReader(pkg), admin)
	// curl --upload-file ./x URL/ appends the local name, which need not be the canonical one
	e.must("upload renamed .tar.bz2 to root", 201, "PUT", "/repository/conda-hosted/my-local-copy.tar.bz2", bytes.NewReader(legacy), admin)

	var rd repodata
	json.Unmarshal([]byte(e.must("linux-64 repodata", 200, "GET", "/repository/conda-hosted/linux-64/repodata.json", nil, "")), &rd)
	got, ok := rd.PackagesConda["acme-tool-1.2.0-h1234_0.conda"]
	sum := sha256.Sum256(pkg)
	if !ok || rd.Info.Subdir != "linux-64" || got.SHA256 != hex.EncodeToString(sum[:]) || got.Size != int64(len(pkg)) ||
		got.Build != "h1234_0" || len(got.MD5) != 32 || len(got.Depends) != 1 {
		t.Fatalf("linux-64 repodata: %+v", rd)
	}
	rd = repodata{}
	json.Unmarshal([]byte(e.must("noarch repodata", 200, "GET", "/repository/conda-hosted/noarch/repodata.json", nil, "")), &rd)
	if l, ok := rd.Packages["legacy-pkg-0.1-py_0.tar.bz2"]; !ok || l.Version != "0.1" || len(rd.PackagesConda) != 0 {
		t.Fatalf("noarch repodata: %+v", rd)
	}
	if b := e.must("empty subdir still answers", 200, "GET", "/repository/conda-hosted/osx-arm64/current_repodata.json", nil, ""); !strings.Contains(b, `"subdir":"osx-arm64"`) {
		t.Fatal(b)
	}
	e.must("compressed variants are absent so clients fall back", 404, "GET", "/repository/conda-hosted/noarch/repodata.json.zst", nil, "")
	if b := e.must("download", 200, "GET", "/repository/conda-hosted/linux-64/acme-tool-1.2.0-h1234_0.conda", nil, ""); b != string(pkg) {
		t.Fatal("downloaded package differs")
	}

	e.must("delete", 204, "DELETE", "/repository/conda-hosted/linux-64/acme-tool-1.2.0-h1234_0.conda", nil, admin)
	if b := e.must("repodata after delete", 200, "GET", "/repository/conda-hosted/linux-64/repodata.json", nil, ""); strings.Contains(b, "acme-tool") {
		t.Fatal("deleted package still listed: " + b)
	}
	e.must("no conda groups", 400, "POST", "/api/repositories", jsonBody(map[string]any{"name": "conda-all", "format": "conda", "type": "group", "members": []string{"conda-hosted"}}), admin)
}

func TestCondaProxy(t *testing.T) {
	e := newEnv(t)
	var hits atomic.Int32
	var down atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if down.Load() {
			w.WriteHeader(503)
			return
		}
		switch r.URL.Path {
		case "/conda-forge/noarch/repodata.json":
			w.Write([]byte(`{"info":{"subdir":"noarch"},"packages":{},"packages.conda":{"six-1.16.0-pyh6c4a22f_0.conda":{}}}`))
		case "/conda-forge/noarch/six-1.16.0-pyh6c4a22f_0.conda":
			w.Write([]byte("package bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	e.must("create", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "cf", "format": "conda", "type": "proxy", "remoteUrl": up.URL + "/conda-forge", "metadataTtl": 0}), admin)

	e.must("repodata", 200, "GET", "/repository/cf/noarch/repodata.json", nil, "")
	e.must("package", 200, "GET", "/repository/cf/noarch/six-1.16.0-pyh6c4a22f_0.conda", nil, "")
	before := hits.Load()
	e.must("package from cache", 200, "GET", "/repository/cf/noarch/six-1.16.0-pyh6c4a22f_0.conda", nil, "")
	if hits.Load() != before {
		t.Fatal("immutable package was fetched again")
	}
	e.must("repodata revalidated (ttl 0)", 200, "GET", "/repository/cf/noarch/repodata.json", nil, "")
	if hits.Load() != before+1 {
		t.Fatal("repodata was not re-fetched after its TTL")
	}
	e.must("missing upstream", 404, "GET", "/repository/cf/linux-64/repodata.json.zst", nil, "")

	down.Store(true)
	if b := e.must("stale repodata during outage", 200, "GET", "/repository/cf/noarch/repodata.json", nil, ""); !strings.Contains(b, "six-1.16.0") {
		t.Fatal(b)
	}
	e.must("cached package during outage", 200, "GET", "/repository/cf/noarch/six-1.16.0-pyh6c4a22f_0.conda", nil, "")
	e.must("proxies are read-only", 405, "PUT", "/repository/cf/noarch/x-1-0.conda", strings.NewReader("x"), admin)
}

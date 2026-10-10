package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

const admin = "admin:admin123"

func jsonBody(v any) io.Reader { b, _ := json.Marshal(v); return bytes.NewReader(b) }

func TestGoModules(t *testing.T) {
	e := newEnv(t)
	px, _ := e.a.getRepo("go-proxy")
	px.Offline = true // keep the test off the network; offline members are skipped by groups
	e.a.saveRepo(px)

	base := "/repository/go-hosted/example.com/hello/@v/"
	e.must("invalid version", 400, "PUT", base+"1.0.mod", strings.NewReader("module example.com/hello\n"), admin)
	for _, v := range []string{"v1.0.0", "v1.1.0-beta.1"} {
		e.must("mod "+v, 201, "PUT", base+v+".mod", strings.NewReader("module example.com/hello\n"), admin)
		e.must("zip "+v, 201, "PUT", base+v+".zip", strings.NewReader("zipdata"), admin)
	}
	if b := e.must("list", 200, "GET", "/repository/go-public/example.com/hello/@v/list", nil, ""); b != "v1.0.0\nv1.1.0-beta.1\n" {
		t.Fatalf("list: %q", b)
	}
	if b := e.must("latest", 200, "GET", "/repository/go-public/example.com/hello/@latest", nil, ""); !strings.Contains(b, `"Version":"v1.0.0"`) {
		t.Fatalf("latest should prefer the release: %s", b)
	}
	if b := e.must("info", 200, "GET", "/repository/go-public/example.com/hello/@v/v1.1.0-beta.1.info", nil, ""); !strings.Contains(b, "v1.1.0-beta.1") {
		t.Fatalf("synthesized info: %s", b)
	}
	e.must("zip via group", 200, "GET", "/repository/go-public/example.com/hello/@v/v1.0.0.zip", nil, "")
	e.must("unknown module", 404, "GET", "/repository/go-public/example.com/nope/@v/list", nil, "")
	e.must("sumdb passthrough", 404, "GET", "/repository/go-public/sumdb/sum.golang.org/supported", nil, "")
}

func makeDeb(pkg, ver, arch string) []byte {
	tgz := func(name, content string) []byte {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		if name != "" {
			tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))})
			tw.Write([]byte(content))
		}
		tw.Close()
		gz.Close()
		return buf.Bytes()
	}
	control := fmt.Sprintf("Package: %s\nVersion: %s\nArchitecture: %s\nMaintainer: Test <t@example.com>\nDescription: test package\n with a long description\n", pkg, ver, arch)
	var ar bytes.Buffer
	ar.WriteString("!<arch>\n")
	for _, m := range []struct {
		name string
		data []byte
	}{{"debian-binary", []byte("2.0\n")}, {"control.tar.gz", tgz("./control", control)}, {"data.tar.gz", tgz("", "")}} {
		fmt.Fprintf(&ar, "%-16s%-12s%-6s%-6s%-8s%-10d`\n", m.name, "0", "0", "0", "100644", len(m.data))
		ar.Write(m.data)
		if len(m.data)%2 == 1 {
			ar.WriteByte('\n')
		}
	}
	return ar.Bytes()
}

func TestAptHosted(t *testing.T) {
	e := newEnv(t)
	deb := makeDeb("hello", "1:1.0-1", "amd64")
	e.must("upload deb", 201, "PUT", "/repository/apt-hosted/hello.deb", bytes.NewReader(deb), admin)
	e.must("upload arch-all deb", 201, "POST", "/repository/apt-hosted/", bytes.NewReader(makeDeb("libdata", "2.0", "all")), admin)
	e.must("reject garbage", 400, "PUT", "/repository/apt-hosted/bad.deb", strings.NewReader("nope"), admin)

	keyring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(e.must("key", 200, "GET", "/repository/apt-hosted/repository.key", nil, "")))
	if err != nil {
		t.Fatal(err)
	}
	inRelease := e.must("InRelease", 200, "GET", "/repository/apt-hosted/dists/stable/InRelease", nil, "")
	block, _ := clearsign.Decode([]byte(inRelease))
	if block == nil {
		t.Fatal("InRelease is not clearsigned")
	}
	if _, err := block.VerifySignature(keyring, nil); err != nil {
		t.Fatalf("InRelease signature: %v", err)
	}
	release := e.must("Release", 200, "GET", "/repository/apt-hosted/dists/stable/Release", nil, "")
	sig := e.must("Release.gpg", 200, "GET", "/repository/apt-hosted/dists/stable/Release.gpg", nil, "")
	if _, err := openpgp.CheckArmoredDetachedSignature(keyring, strings.NewReader(release), strings.NewReader(sig), nil); err != nil {
		t.Fatalf("Release.gpg: %v", err)
	}
	pk := e.must("Packages", 200, "GET", "/repository/apt-hosted/dists/stable/main/binary-amd64/Packages", nil, "")
	sum := sha256.Sum256([]byte(pk))
	if !strings.Contains(release, hex.EncodeToString(sum[:])) {
		t.Fatal("Release does not list the Packages checksum")
	}
	debSum := sha256.Sum256(deb)
	for _, want := range []string{"Package: hello", "Filename: pool/main/h/hello/hello_1.0-1_amd64.deb", "SHA256: " + hex.EncodeToString(debSum[:]), "Package: libdata", " with a long description"} {
		if !strings.Contains(pk, want) {
			t.Fatalf("Packages missing %q:\n%s", want, pk)
		}
	}
	e.must("download deb", 200, "GET", "/repository/apt-hosted/pool/main/h/hello/hello_1.0-1_amd64.deb", nil, "")
	// deleting a package removes it from the index
	e.must("delete deb", 204, "DELETE", "/repository/apt-hosted/pool/main/h/hello/hello_1.0-1_amd64.deb", nil, admin)
	if pk := e.must("Packages", 200, "GET", "/repository/apt-hosted/dists/stable/main/binary-amd64/Packages", nil, ""); strings.Contains(pk, "Package: hello\n") {
		t.Fatal("deleted package still indexed")
	}
}

// makeRPM builds a minimal but structurally valid RPM (lead, empty signature header, main header, payload).
func makeRPM(name, ver, rel, arch string) []byte {
	type ent struct {
		tag, typ int32
		val      any
	}
	header := func(entries []ent) []byte {
		var index, store bytes.Buffer
		for _, e := range entries {
			align := map[int32]int{3: 2, 4: 4}[e.typ]
			for align > 0 && store.Len()%align != 0 {
				store.WriteByte(0)
			}
			off, count := store.Len(), 1
			switch v := e.val.(type) {
			case string:
				store.WriteString(v + "\x00")
			case []string:
				count = len(v)
				for _, s := range v {
					store.WriteString(s + "\x00")
				}
			case []int32:
				count = len(v)
				binary.Write(&store, binary.BigEndian, v)
			case []uint16:
				count = len(v)
				binary.Write(&store, binary.BigEndian, v)
			}
			binary.Write(&index, binary.BigEndian, []int32{e.tag, e.typ, int32(off), int32(count)})
		}
		var out bytes.Buffer
		out.Write([]byte{0x8e, 0xad, 0xe8, 0x01, 0, 0, 0, 0})
		binary.Write(&out, binary.BigEndian, []int32{int32(len(entries)), int32(store.Len())})
		out.Write(index.Bytes())
		out.Write(store.Bytes())
		return out.Bytes()
	}
	var f bytes.Buffer
	lead := make([]byte, 96)
	copy(lead, []byte{0xed, 0xab, 0xee, 0xdb, 3, 0})
	f.Write(lead)
	f.Write(header(nil)) // 16 bytes: already 8-aligned
	f.Write(header([]ent{
		{1000, 6, name}, {1001, 6, ver}, {1002, 6, rel}, {1004, 9, "Hello summary"}, {1005, 9, "Hello <description> & more"},
		{1009, 4, []int32{4096}}, {1014, 6, "MIT"}, {1022, 6, arch}, {1030, 3, []uint16{0o100755, 0o40755}},
		{1047, 8, []string{name}}, {1048, 4, []int32{0, 16777226}}, {1049, 8, []string{"bash", "rpmlib(CompressedFileNames)"}},
		{1050, 8, []string{"", "3.0.4-1"}}, {1112, 4, []int32{8}}, {1113, 8, []string{ver + "-" + rel}},
		{1116, 4, []int32{0, 1}}, {1117, 8, []string{name, name}}, {1118, 8, []string{"/usr/bin/", "/usr/share/"}},
	}))
	f.WriteString("payload")
	return f.Bytes()
}

func TestYumHosted(t *testing.T) {
	e := newEnv(t)
	rpm := makeRPM("hello", "1.0", "1", "x86_64")
	e.must("upload rpm", 201, "PUT", "/repository/yum-hosted/hello.rpm", bytes.NewReader(rpm), admin)
	e.must("reject garbage", 400, "PUT", "/repository/yum-hosted/bad.rpm", strings.NewReader("nope"), admin)
	repomd := e.must("repomd", 200, "GET", "/repository/yum-hosted/repodata/repomd.xml", nil, "")
	keyring, _ := openpgp.ReadArmoredKeyRing(strings.NewReader(e.must("key", 200, "GET", "/repository/yum-hosted/repository.key", nil, "")))
	sig := e.must("repomd.asc", 200, "GET", "/repository/yum-hosted/repodata/repomd.xml.asc", nil, "")
	if _, err := openpgp.CheckArmoredDetachedSignature(keyring, strings.NewReader(repomd), strings.NewReader(sig), nil); err != nil {
		t.Fatalf("repomd signature: %v", err)
	}
	loc := regexp.MustCompile(`href="(repodata/[0-9a-f]+-primary\.xml\.gz)"`).FindStringSubmatch(repomd)
	if loc == nil {
		t.Fatalf("no primary in repomd:\n%s", repomd)
	}
	gz, err := gzip.NewReader(strings.NewReader(e.must("primary", 200, "GET", "/repository/yum-hosted/"+loc[1], nil, "")))
	if err != nil {
		t.Fatal(err)
	}
	primary, _ := io.ReadAll(gz)
	sum := sha256.Sum256(rpm)
	for _, want := range []string{`<name>hello</name>`, `<version epoch="0" ver="1.0" rel="1"/>`, hex.EncodeToString(sum[:]),
		`<location href="Packages/h/hello-1.0-1.x86_64.rpm"/>`, `<rpm:entry name="bash"/>`, `<rpm:entry name="hello" flags="EQ" epoch="0" ver="1.0" rel="1"/>`,
		`<file>/usr/bin/hello</file>`, `&lt;description&gt; &amp; more`} {
		if !strings.Contains(string(primary), want) {
			t.Fatalf("primary.xml missing %q:\n%s", want, primary)
		}
	}
	if strings.Contains(string(primary), "rpmlib(") {
		t.Fatal("rpmlib() requirements must be omitted")
	}
	e.must("download rpm", 200, "GET", "/repository/yum-hosted/Packages/h/hello-1.0-1.x86_64.rpm", nil, "")
}

func TestAccessControl(t *testing.T) {
	e := newEnv(t)
	e.must("create viewer", 204, "POST", "/api/users", jsonBody(map[string]string{"username": "dev", "password": "devpass123", "role": "viewer"}), admin)
	dev := "dev:devpass123"
	e.must("viewer cannot deploy", 403, "PUT", "/repository/raw-hosted/a.txt", strings.NewReader("x"), dev)
	e.must("grant write", 204, "PUT", "/api/repositories/raw-hosted/permissions", jsonBody([]permRow{{Username: "dev", Level: "write"}}), admin)
	e.must("override allows deploy", 201, "PUT", "/repository/raw-hosted/a.txt", strings.NewReader("x"), dev)
	e.must("deny maven", 204, "PUT", "/api/repositories/maven-releases/permissions", jsonBody([]permRow{{Username: "dev", Level: "none"}}), admin)
	e.must("no access", 403, "GET", "/repository/maven-releases/x.jar", nil, dev)
	var repos []repoView
	json.Unmarshal([]byte(e.must("list repos", 200, "GET", "/api/repositories", nil, dev)), &repos)
	for _, r := range repos {
		if r.Name == "maven-releases" {
			t.Fatal("hidden repository listed")
		}
	}

	raw, _ := e.a.getRepo("raw-hosted")
	raw.Private = true
	e.a.saveRepo(raw)
	e.must("private repo anonymous", 401, "GET", "/repository/raw-hosted/a.txt", nil, "")
	e.must("private repo member", 200, "GET", "/repository/raw-hosted/a.txt", nil, dev)

	e.must("disable user", 204, "PUT", "/api/users/dev", jsonBody(map[string]any{"role": "viewer", "disabled": true}), admin)
	e.must("disabled user", 401, "GET", "/repository/raw-hosted/a.txt", nil, dev)
}

func TestOfflineQuotaCleanup(t *testing.T) {
	e := newEnv(t)
	raw, _ := e.a.getRepo("raw-hosted")
	raw.QuotaMB = 1
	e.a.saveRepo(raw)
	e.must("under quota", 201, "PUT", "/repository/raw-hosted/big.bin", bytes.NewReader(make([]byte, 1<<20+1)), admin)
	e.must("over quota", 507, "PUT", "/repository/raw-hosted/more.bin", strings.NewReader("x"), admin)
	raw.Offline = true
	e.a.saveRepo(raw)
	e.must("offline", 503, "GET", "/repository/raw-hosted/big.bin", nil, admin)

	for _, v := range []string{"1.0", "2.0", "3.0"} {
		e.must("deploy "+v, 201, "PUT", "/repository/maven-releases/com/acme/lib/"+v+"/lib-"+v+".jar", strings.NewReader(v), admin)
		time.Sleep(1100 * time.Millisecond) // created_at has second resolution
	}
	mr, _ := e.a.getRepo("maven-releases")
	mr.RetainVersions = 2
	e.a.saveRepo(mr)
	if b := e.must("dry run", 200, "POST", "/api/repositories/maven-releases/cleanup?dryRun=1", nil, admin); !strings.Contains(b, `"count":1`) {
		t.Fatalf("dry run: %s", b)
	}
	e.must("cleanup", 200, "POST", "/api/repositories/maven-releases/cleanup", nil, admin)
	e.must("oldest version removed", 404, "GET", "/repository/maven-releases/com/acme/lib/1.0/lib-1.0.jar", nil, admin)
	e.must("newest kept", 200, "GET", "/repository/maven-releases/com/acme/lib/3.0/lib-3.0.jar", nil, admin)
}

func TestWebhooks(t *testing.T) {
	e := newEnv(t)
	got := make(chan *http.Request, 4)
	bodies := make(chan []byte, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r
		bodies <- b
	}))
	defer hook.Close()
	e.must("create webhook", 204, "POST", "/api/webhooks", jsonBody(map[string]any{
		"name": "ci", "url": hook.URL, "secret": "s3cret", "events": []string{"asset.upload"}, "repos": []string{"raw-hosted"}, "enabled": true,
	}), admin)
	e.must("unrelated repo", 201, "PUT", "/repository/maven-releases/x/y/1/y-1.jar", strings.NewReader("x"), admin)
	e.must("matching upload", 201, "PUT", "/repository/raw-hosted/file.txt", strings.NewReader("x"), admin)
	select {
	case r := <-got:
		body := <-bodies
		mac := hmac.New(sha256.New, []byte("s3cret"))
		mac.Write(body)
		if r.Header.Get("X-RepoMan-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Fatal("bad webhook signature")
		}
		if !strings.Contains(string(body), `"target":"raw-hosted/file.txt"`) {
			t.Fatalf("unexpected event: %s", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("webhook not delivered")
	}
	select {
	case <-got:
		t.Fatal("event for a filtered-out repository was delivered")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestPagination(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"b/1.txt", "b/2.txt", "a/x.txt", "é/y.txt", "é/z/deep.txt", "f1.txt", "f2.txt", "f3.txt", "élan.txt"} {
		e.must("upload "+p, 201, "PUT", "/repository/raw-hosted/"+p, strings.NewReader(p), admin)
	}
	names := func(body string) (out []string) {
		var list []browseEntry
		json.Unmarshal([]byte(body), &list)
		for _, x := range list {
			out = append(out, x.Name)
		}
		return out
	}
	page := func(q string) (string, string) {
		c, b, h := e.req("GET", "/api/repositories/raw-hosted/browse?"+q, nil, admin)
		if c != 200 {
			t.Fatalf("browse %s: %d %s", q, c, b)
		}
		return strings.Join(names(b), ","), h.Get("X-Total-Count")
	}
	// folders first, then files; pages cross the folder/file boundary
	if got, total := page("limit=4"); got != "a,b,é,f1.txt" || total != "7" {
		t.Fatalf("page 1: %s (total %s)", got, total)
	}
	if got, _ := page("limit=4&offset=4"); got != "f2.txt,f3.txt,élan.txt" {
		t.Fatalf("page 2: %s", got)
	}
	if got, total := page("path=" + url.QueryEscape("é")); got != "z,y.txt" || total != "2" {
		t.Fatalf("non-ASCII folder: %s (total %s)", got, total)
	}
	if got, total := page("q=F2"); got != "f2.txt" || total != "1" {
		t.Fatalf("filter: %s (total %s)", got, total)
	}
	e.must("deep link", 200, "GET", "/api/repositories/raw-hosted/asset?path="+url.QueryEscape("é/z/deep.txt"), nil, admin)

	_, b, h := e.req("GET", "/api/search?q=.txt&limit=2&sort=name", nil, admin)
	if h.Get("X-Total-Count") != "9" || strings.Count(b, `"id"`) != 2 {
		t.Fatalf("search page: total %s, %s", h.Get("X-Total-Count"), b)
	}
	if b := e.must("audit csv", 200, "GET", "/api/audit/export?q=raw-hosted", nil, admin); !strings.HasPrefix(b, "time,user,action,target,detail\n") || !strings.Contains(b, "raw-hosted/f1.txt") {
		t.Fatalf("audit csv: %s", b)
	}
}

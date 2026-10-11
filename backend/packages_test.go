package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

const testPom = `<project><modelVersion>4.0.0</modelVersion><groupId>com.acme</groupId><artifactId>lib</artifactId><version>1.0</version>
<description>A test library</description><url>https://acme.test</url>
<licenses><license><name>Apache-2.0</name></license></licenses>
<properties><junit.v>4.13</junit.v></properties>
<dependencies>
<dependency><groupId>junit</groupId><artifactId>junit</artifactId><version>${junit.v}</version><scope>test</scope></dependency>
<dependency><groupId>${project.groupId}</groupId><artifactId>core</artifactId><version>${project.version}</version></dependency>
</dependencies></project>`

func mavenEnv(t *testing.T) *testEnv {
	e := newEnv(t)
	for _, n := range []string{"mvn-a", "mvn-b"} {
		e.must("repo "+n, 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": n, "format": "maven", "type": "hosted"}), admin)
	}
	e.must("group", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "mvn-all", "format": "maven", "type": "group", "members": []string{"mvn-a", "mvn-b"}}), admin)
	e.must("pom", 201, "PUT", "/repository/mvn-a/com/acme/lib/1.0/lib-1.0.pom", strings.NewReader(testPom), admin)
	e.must("jar", 201, "PUT", "/repository/mvn-a/com/acme/lib/1.0/lib-1.0.jar", strings.NewReader("jar-bytes"), admin)
	e.must("sig", 201, "PUT", "/repository/mvn-a/com/acme/lib/1.0/lib-1.0.jar.asc", strings.NewReader("sig"), admin)
	e.must("jar b", 201, "PUT", "/repository/mvn-b/com/acme/lib/1.0/lib-1.0.jar", strings.NewReader("other"), admin)
	return e
}

func TestPackagePage(t *testing.T) {
	e := mavenEnv(t)
	var v packageView
	b := e.must("package", 200, "GET", "/api/repositories/mvn-all/package?component="+url.QueryEscape("com.acme:lib"), nil, admin)
	if err := json.Unmarshal([]byte(b), &v); err != nil {
		t.Fatal(err)
	}
	if v.Version != "1.0" || v.Info.Description != "A test library" || len(v.Info.Licenses) != 1 || len(v.Info.Deps) != 2 {
		t.Fatalf("package: %s", b)
	}
	if v.Info.Deps[0].Version != "4.13" || v.Info.Deps[1].Name != "com.acme:core" || v.Info.Deps[1].Version != "1.0" {
		t.Fatalf("properties not resolved: %+v", v.Info.Deps)
	}
	var jar *packageFile
	for i, f := range v.Files {
		if strings.HasSuffix(f.Asset.Path, ".asc") {
			t.Fatal("signature listed as a file")
		}
		if f.Asset.Repo == "mvn-a" && strings.HasSuffix(f.Asset.Path, ".jar") {
			jar = &v.Files[i]
		}
	}
	if jar == nil || !jar.Sums.Signed || len(jar.Sums.SHA1) != 40 || len(jar.Sums.MD5) != 32 {
		t.Fatalf("checksums: %+v", jar)
	}
	// dependents: com.acme:core is used by lib
	b = e.must("dependents", 200, "GET", "/api/repositories/mvn-a/dependents?component="+url.QueryEscape("com.acme:core"), nil, admin)
	if !strings.Contains(b, `"component":"com.acme:lib"`) {
		t.Fatalf("dependents: %s", b)
	}
	// replacing the pom replaces its rows; deleting the asset removes them
	e.must("deps removed", 200, "DELETE", "/api/repositories/mvn-a/components?component="+url.QueryEscape("com.acme:lib"), nil, admin)
	b = e.must("dependents after", 200, "GET", "/api/repositories/mvn-a/dependents?component="+url.QueryEscape("com.acme:core"), nil, admin)
	if strings.Contains(b, "lib") {
		t.Fatalf("dependents kept after delete: %s", b)
	}
	e.must("checksums", 404, "GET", "/api/repositories/mvn-a/checksums?path=nope", nil, admin)
}

func TestGroupDedupeAndSearch(t *testing.T) {
	e := mavenEnv(t)
	// the jar exists in both members: the group lists it once (member a), searching the group finds it
	b := e.must("browse", 200, "GET", "/api/repositories/mvn-all/browse?path=com/acme/lib/1.0", nil, admin)
	if strings.Count(b, `"name":"lib-1.0.jar"`) != 1 || !strings.Contains(b, `"repo":"mvn-a"`) {
		t.Fatalf("group browse: %s", b)
	}
	b = e.must("search group", 200, "GET", "/api/search?repo=mvn-all&q=lib-1.0.jar", nil, admin)
	if strings.Count(b, `"path":"com/acme/lib/1.0/lib-1.0.jar"`) != 2 {
		t.Fatalf("group search: %s", b)
	}
	b = e.must("operator", 200, "GET", "/api/search?q="+url.QueryEscape("lib version:1.0 repo:mvn-b"), nil, admin)
	if strings.Contains(b, `"repo":"mvn-a"`) || !strings.Contains(b, `"repo":"mvn-b"`) {
		t.Fatalf("operators: %s", b)
	}
	var jar Asset
	x, _ := e.a.findAsset("mvn-a", "com/acme/lib/1.0/lib-1.0.jar")
	jar = *x
	b = e.must("sha256", 200, "GET", "/api/search?q="+url.QueryEscape("sha256:"+jar.Blob[:12]), nil, admin)
	if !strings.Contains(b, `"repo":"mvn-a"`) {
		t.Fatalf("sha256 search: %s", b)
	}
}

func TestPromoteAndBulk(t *testing.T) {
	e := mavenEnv(t)
	e.must("releases", 200, "POST", "/api/repositories", jsonBody(map[string]any{"name": "mvn-rel", "format": "maven", "type": "hosted"}), admin)
	body := jsonBody(map[string]any{"target": "mvn-rel", "items": []map[string]string{{"component": "com.acme:lib", "version": "1.0"}}})
	e.must("promote", 200, "POST", "/api/repositories/mvn-a/promote", body, admin)
	e.must("copied", 200, "GET", "/repository/mvn-rel/com/acme/lib/1.0/lib-1.0.jar", nil, admin)
	e.must("source kept", 200, "GET", "/repository/mvn-a/com/acme/lib/1.0/lib-1.0.jar", nil, admin)
	if b := e.must("metadata", 200, "GET", "/repository/mvn-rel/com/acme/lib/maven-metadata.xml", nil, admin); !strings.Contains(b, "<version>1.0</version>") {
		t.Fatalf("metadata: %s", b)
	}
	// promoting again is refused: the release repository does not allow overwriting
	e.must("twice", 409, "POST", "/api/repositories/mvn-a/promote", jsonBody(map[string]any{"target": "mvn-rel", "items": []map[string]string{{"component": "com.acme:lib", "version": "1.0"}}}), admin)
	e.must("wrong format", 400, "POST", "/api/repositories/mvn-a/promote", jsonBody(map[string]any{"target": "raw-hosted", "items": []map[string]string{{"component": "com.acme:lib"}}}), admin)
	e.must("move conflicts", 409, "POST", "/api/repositories/mvn-b/promote", jsonBody(map[string]any{"target": "mvn-rel", "move": true, "items": []map[string]string{{"component": "com.acme:lib", "version": "1.0"}}}), admin) // the release repository already has it
}

func TestBulkDeleteDownloadsStorage(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"a/1.txt", "a/2.txt", "b/3.txt", "c.txt"} {
		e.must("put", 201, "PUT", "/repository/raw-hosted/"+p, strings.NewReader(p), admin)
	}
	x, _ := e.a.findAsset("raw-hosted", "c.txt")
	for i := 0; i < 3; i++ {
		e.must("get", 200, "GET", "/repository/raw-hosted/c.txt", nil, admin)
	}
	var days []dayCount
	json.Unmarshal([]byte(e.must("downloads", 200, "GET", "/api/assets/"+itoa(x.ID)+"/downloads", nil, admin)), &days)
	if len(days) != 30 || days[29].N != 3 {
		t.Fatalf("daily downloads: %+v", days)
	}
	b := e.must("bulk", 200, "POST", "/api/repositories/raw-hosted/bulk-delete", jsonBody(map[string]any{"ids": []int64{x.ID}, "prefixes": []string{"a"}}), admin)
	if !strings.Contains(b, `"deleted":3`) {
		t.Fatalf("bulk delete: %s", b)
	}
	if _, err := e.a.findAsset("raw-hosted", "b/3.txt"); err != nil {
		t.Fatal("bulk delete removed too much")
	}
	if _, err := e.a.storageSnapshot(); err != nil {
		t.Fatal(err)
	}
	b = e.must("storage", 200, "GET", "/api/storage?scan=1", nil, admin)
	if !strings.Contains(b, `"raw-hosted"`) || !strings.Contains(b, `"series":[{`) || !strings.Contains(b, `"reclaimable"`) {
		t.Fatalf("storage: %s", b)
	}
	e.must("save search", 200, "POST", "/api/saved-searches", jsonBody(map[string]any{"name": "mine", "query": "q=txt"}), admin)
	if b := e.must("list", 200, "GET", "/api/saved-searches", nil, admin); !strings.Contains(b, `"mine"`) {
		t.Fatalf("saved searches: %s", b)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestPackageFilesDedupedAcrossMembers(t *testing.T) {
	e := mavenEnv(t) // lib-1.0.jar exists in both members of mvn-all
	var v packageView
	json.Unmarshal([]byte(e.must("package", 200, "GET", "/api/repositories/mvn-all/package?component="+url.QueryEscape("com.acme:lib"), nil, admin)), &v)
	n := 0
	for _, f := range v.Files {
		if strings.HasSuffix(f.Asset.Path, "lib-1.0.jar") {
			n++
			if f.Asset.Repo != "mvn-a" {
				t.Fatalf("served from %s, want the first member", f.Asset.Repo)
			}
		}
	}
	if n != 1 {
		t.Fatalf("lib-1.0.jar listed %d times", n)
	}
}

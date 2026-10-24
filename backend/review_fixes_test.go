package main

import (
	"bytes"
	"strings"
	"testing"
)

// Deleting Maven versions rewrites maven-metadata.xml; deleting the last one removes it.
func TestMavenMetadataFollowsDeletes(t *testing.T) {
	e := newEnv(t)
	base := "/repository/maven-releases/com/acme/lib/"
	for _, v := range []string{"1.0", "2.0"} {
		e.must("jar "+v, 201, "PUT", base+v+"/lib-"+v+".jar", strings.NewReader("jar "+v), admin)
	}
	meta := `<metadata><groupId>com.acme</groupId><artifactId>lib</artifactId><versioning><versions><version>1.0</version><version>2.0</version></versions></versioning></metadata>`
	e.must("client metadata", 201, "PUT", base+"maven-metadata.xml", strings.NewReader(meta), admin)

	e.must("delete 1.0", 200, "DELETE", "/api/repositories/maven-releases/components?component=com.acme:lib&version=1.0", nil, admin)
	got := e.must("metadata", 200, "GET", base+"maven-metadata.xml", nil, admin)
	if strings.Contains(got, "<version>1.0</version>") || !strings.Contains(got, "<version>2.0</version>") {
		t.Fatalf("metadata still lists the deleted version:\n%s", got)
	}
	if sum := e.must("sha1", 200, "GET", base+"maven-metadata.xml.sha1", nil, admin); sum != mavenChecksums[".sha1"]([]byte(got)) {
		t.Fatal("checksum does not match the rewritten metadata")
	}

	e.must("delete 2.0", 200, "DELETE", "/api/repositories/maven-releases/components?component=com.acme:lib&version=2.0", nil, admin)
	e.must("metadata gone", 404, "GET", base+"maven-metadata.xml", nil, admin)
}

// An exported group whose member is another group is imported whatever their order in the file.
func TestImportNestedGroups(t *testing.T) {
	e := newEnv(t)
	doc := map[string]any{"version": "test", "repositories": []map[string]any{
		{"name": "outer", "format": "raw", "type": "group", "members": []string{"inner"}},
		{"name": "inner", "format": "raw", "type": "group", "members": []string{"raw-hosted"}},
		{"name": "broken", "format": "raw", "type": "group", "members": []string{"nowhere"}},
	}}
	b := e.must("import", 200, "POST", "/api/system/import", jsonBody(doc), admin)
	if !strings.Contains(b, "repository outer") || !strings.Contains(b, "repository inner") || !strings.Contains(b, "broken: invalid group member") {
		t.Fatalf("unexpected import report: %s", b)
	}
	e.must("outer exists", 200, "GET", "/api/repositories/outer", nil, admin)
}

// Package metadata never turns into folders outside the layout the indexes expect.
func TestPackageFieldsCannotAddFolders(t *testing.T) {
	e := newEnv(t)
	e.must("deb arch with a slash", 400, "PUT", "/repository/apt-hosted/x.deb", bytes.NewReader(makeDeb("evil", "1.0", "../../x")), admin)
	e.must("deb name with a slash", 400, "PUT", "/repository/apt-hosted/x.deb", bytes.NewReader(makeDeb("../evil", "1.0", "amd64")), admin)
	e.must("rpm name with a slash", 400, "PUT", "/repository/yum-hosted/x.rpm", bytes.NewReader(makeRPM("../repodata/evil", "1.0", "1", "x86_64")), admin)
	e.must("good deb still fine", 201, "PUT", "/repository/apt-hosted/x.deb", bytes.NewReader(makeDeb("good", "1.0", "amd64")), admin)
}

func TestCSVSafe(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "\tx": "'\tx", "\rx": "'\rx", "alice": "alice", "": ""} {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

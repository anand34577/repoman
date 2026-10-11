package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func mkRepo(e *testEnv, name, format, typ string, extra map[string]any) {
	m := map[string]any{"name": name, "format": format, "type": typ}
	for k, v := range extra {
		m[k] = v
	}
	e.must("create "+name, 200, "POST", "/api/repositories", jsonBody(m), admin)
}

// A group holds nothing itself: the dashboard must count its members once, not twice.
func TestDashboardDoesNotCountGroups(t *testing.T) {
	e := newEnv(t)
	mkRepo(e, "mv-hosted", "maven", "hosted", nil)
	mkRepo(e, "mv-group", "maven", "group", map[string]any{"members": []string{"mv-hosted"}})
	e.must("put", 201, "PUT", "/repository/mv-hosted/a/b/1/b-1.jar", strings.NewReader(strings.Repeat("x", 1000)), admin)
	var s struct {
		Assets       int64
		LogicalBytes int64
		Formats      []struct {
			Format string
			Assets int64
			Bytes  int64
		}
	}
	json.Unmarshal([]byte(e.must("stats", 200, "GET", "/api/stats", nil, admin)), &s)
	for _, f := range s.Formats {
		if f.Format == "maven" && (f.Assets != 1 || f.Bytes != 1000) {
			t.Fatalf("maven counted twice: %+v", f)
		}
	}
	if s.Assets != 1 || s.LogicalBytes != 1000 {
		t.Fatalf("totals %d assets, %d bytes", s.Assets, s.LogicalBytes)
	}
}

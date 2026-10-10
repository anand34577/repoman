package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testEnv struct {
	t   *testing.T
	a   *App
	srv *httptest.Server
}

func newEnv(t *testing.T) *testEnv {
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.bootstrap("admin123"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	t.Cleanup(func() { srv.Close(); a.db.Close() })
	return &testEnv{t, a, srv}
}

// req performs a request; creds is "user:pass" or "" for anonymous.
func (e *testEnv) req(method, path string, body io.Reader, creds string, hdr ...string) (int, string, http.Header) {
	e.t.Helper()
	r, _ := http.NewRequest(method, e.srv.URL+path, body)
	if user, pass, ok := cut(creds); ok {
		r.SetBasicAuth(user, pass)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func (e *testEnv) must(what string, wantStatus int, method, path string, body io.Reader, creds string, hdr ...string) string {
	e.t.Helper()
	c, b, _ := e.req(method, path, body, creds, hdr...)
	if c != wantStatus {
		e.t.Fatalf("%s: %s %s -> %d, want %d: %s", what, method, path, c, wantStatus, b)
	}
	return b
}

func cut(creds string) (string, string, bool) {
	for i := range creds {
		if creds[i] == ':' {
			return creds[:i], creds[i+1:], true
		}
	}
	return "", "", false
}

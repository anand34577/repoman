package main

import (
	"strings"
	"testing"
)

// Basic auth used to bypass the sign-in lockout, giving unlimited password guesses.
func TestBasicAuthLockout(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 10; i++ {
		e.must("wrong password", 401, "GET", "/api/auth/me", nil, "admin:wrong")
	}
	e.must("correct password while locked out", 401, "GET", "/api/auth/me", nil, admin)
	e.must("web sign-in while locked out", 429, "POST", "/api/auth/login", jsonBody(map[string]string{"username": "admin", "password": "admin123"}), "")
}

func TestStatsOnlyCountReadableRepos(t *testing.T) {
	e := newEnv(t)
	e.must("upload", 201, "PUT", "/repository/raw-hosted/secret.txt", strings.NewReader("hidden"), admin)
	e.must("viewer", 204, "POST", "/api/users", jsonBody(map[string]string{"username": "vic", "password": "password1", "role": "viewer"}), admin)
	e.must("hide repo", 204, "PUT", "/api/repositories/raw-hosted/permissions", jsonBody([]permRow{{Username: "vic", Level: accessNone}}), admin)
	if b := e.must("stats", 200, "GET", "/api/stats", nil, "vic:password1"); !strings.Contains(b, `"assets":0,`) {
		t.Fatalf("viewer sees assets of a hidden repository: %s", b)
	}
}

func TestInterruptedTaskIsNotStuck(t *testing.T) {
	dir := t.TempDir()
	a, _ := newApp(dir)
	a.setJSON("tasks", map[string]*taskState{"purge": {Running: true}})
	a.db.Close()
	a, _ = newApp(dir)
	defer a.db.Close()
	if a.tasks["purge"].Running {
		t.Fatal("task restored as running after restart; it could never run again")
	}
}

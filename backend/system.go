package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

func (a *App) systemInfoAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	var dbBytes int64
	for _, suffix := range []string{"", "-wal"} {
		if st, err := os.Stat(filepath.Join(a.dataDir, "repoman.db"+suffix)); err == nil {
			dbBytes += st.Size()
		}
	}
	var assets int64
	blobs, blobBytes, err := a.storedTotals(nil, true)
	if err != nil {
		return nil, err
	}
	a.db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&assets)
	abs, _ := filepath.Abs(a.dataDir)
	_, fp, _ := a.publicKey()
	req := map[string]int64{}
	for i := 1; i < len(a.requests); i++ {
		req[fmt.Sprintf("%dxx", i)] = a.requests[i].Load()
	}
	return map[string]any{
		"version": version, "commit": commit, "buildDate": buildDate, "goVersion": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"tls": a.cfg.TLSCert != "", "initialPasswordPending": a.getJSON("initial_password", new(string)),
		"startedAt": a.started.Unix(), "uptimeSeconds": int64(time.Since(a.started).Seconds()),
		"dataDir": abs, "databaseBytes": dbBytes, "blobCount": blobs, "blobBytes": blobBytes, "assets": assets,
		"goroutines": runtime.NumGoroutine(), "heapBytes": ms.HeapAlloc, "signingKeyFingerprint": fp, "requests": req,
	}, nil
}

// metrics exposes Prometheus text metrics; requires any authenticated user (e.g. basic auth with a token).
// Per-repository series cover the repositories that user may read.
func (a *App) metrics(w http.ResponseWriter, r *http.Request) {
	u := a.authenticate(r)
	if u == nil {
		deny(w, nil)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP repoman_up RepoMan is running.\n# TYPE repoman_up gauge\nrepoman_up 1\n")
	fmt.Fprintf(w, "# TYPE repoman_uptime_seconds gauge\nrepoman_uptime_seconds %d\n", int64(time.Since(a.started).Seconds()))
	fmt.Fprintf(w, "# TYPE repoman_http_responses_total counter\n")
	for i := 1; i < len(a.requests); i++ {
		fmt.Fprintf(w, "repoman_http_responses_total{class=\"%dxx\"} %d\n", i, a.requests[i].Load())
	}
	repos, err := a.listRepos()
	if err != nil {
		return
	}
	stats, err := a.repoStats()
	if err != nil {
		return
	}
	var assets, bytes, downloads strings.Builder
	for _, rp := range repos {
		if !a.can(u, rp, false) {
			continue
		}
		v := stats[rp.Name]
		labels := fmt.Sprintf(`{repository=%q,format=%q,type=%q}`, rp.Name, rp.Format, rp.Type)
		fmt.Fprintf(&assets, "repoman_repository_assets%s %d\n", labels, v.AssetCount)
		fmt.Fprintf(&bytes, "repoman_repository_bytes%s %d\n", labels, v.Size)
		fmt.Fprintf(&downloads, "repoman_repository_downloads_total%s %d\n", labels, v.Downloads)
	}
	fmt.Fprintf(w, "# TYPE repoman_repository_assets gauge\n%s# TYPE repoman_repository_bytes gauge\n%s# TYPE repoman_repository_downloads_total counter\n%s",
		assets.String(), bytes.String(), downloads.String())
}

// ---------- configuration backup ----------

type permRow struct {
	Repo     string `json:"repo"`
	Username string `json:"username"`
	Level    string `json:"level"`
}

type exportDoc struct {
	Version      string      `json:"version"`
	ExportedAt   int64       `json:"exportedAt"`
	Settings     Settings    `json:"settings"`
	Repositories []*Repo     `json:"repositories"`
	Users        []*User     `json:"users"`
	Permissions  []permRow   `json:"permissions"`
	Webhooks     []*Webhook  `json:"webhooks"`
	Notifiers    []*Notifier `json:"notifiers"`
}

// exportAPI returns server configuration (no artifacts, passwords or secrets).
func (a *App) exportAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	doc := exportDoc{Version: version, ExportedAt: time.Now().Unix(), Settings: a.settings()}
	repos, err := a.listRepos()
	if err != nil {
		return nil, err
	}
	for _, rp := range repos {
		doc.Repositories = append(doc.Repositories, redact(rp))
	}
	users, err := a.users()
	if err != nil {
		return nil, err
	}
	doc.Users = users
	rows, err := a.db.Query(`SELECT repo, username, level FROM repo_perms`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p permRow
		rows.Scan(&p.Repo, &p.Username, &p.Level)
		doc.Permissions = append(doc.Permissions, p)
	}
	rows.Close()
	if doc.Webhooks, err = a.webhooks(""); err != nil {
		return nil, err
	}
	for _, h := range doc.Webhooks {
		h.Secret, h.LastStatus, h.LastDelivery = "", "", 0
	}
	if doc.Notifiers, err = a.notifiers(""); err != nil {
		return nil, err
	}
	for _, n := range doc.Notifiers {
		n.Token, n.HasToken, n.LastStatus, n.LastDelivery = "", false, "", 0
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="repoman-config-%s.json"`, time.Now().Format("2006-01-02")))
	a.audit(u, "config.export", "system")
	return doc, nil
}

// importAPI applies an exported configuration: creates or updates repositories, creates missing
// users (with random passwords that must be reset), and restores permissions, webhooks and settings.
func (a *App) importAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	var doc exportDoc
	if err := readJSONLimit(r, &doc, 32<<20); err != nil {
		return nil, err
	}
	report := map[string][]string{"created": {}, "updated": {}, "errors": {}, "usersNeedingPassword": {}}
	sort.SliceStable(doc.Repositories, func(i, j int) bool { // groups after their members
		return doc.Repositories[i].Type != "group" && doc.Repositories[j].Type == "group"
	})
	for _, rp := range doc.Repositories {
		if rp == nil {
			continue
		}
		old, err := a.getRepo(rp.Name)
		if err == nil {
			rp.Format, rp.Type, rp.CreatedAt, rp.InvalidatedAt = old.Format, old.Type, old.CreatedAt, old.InvalidatedAt
			if rp.RemotePassword == "" {
				rp.RemotePassword = old.RemotePassword
			}
		} else {
			rp.CreatedAt, rp.InvalidatedAt = time.Now().Unix(), 0
		}
		if err := a.validateRepo(rp); err != nil {
			report["errors"] = append(report["errors"], rp.Name+": "+err.Error())
			continue
		}
		if err := a.saveRepo(rp); err != nil {
			report["errors"] = append(report["errors"], rp.Name+": "+err.Error())
			continue
		}
		a.reindex(rp.Name)
		if old != nil {
			report["updated"] = append(report["updated"], "repository "+rp.Name)
		} else {
			report["created"] = append(report["created"], "repository "+rp.Name)
		}
	}
	for _, x := range doc.Users {
		if x == nil {
			continue
		}
		var exists int
		a.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username=?`, x.Username).Scan(&exists)
		if exists > 0 {
			continue
		}
		if err := a.createUser(x.Username, randomHex(16), x.Email, x.Role); err != nil {
			report["errors"] = append(report["errors"], "user "+x.Username+": "+err.Error())
			continue
		}
		report["created"] = append(report["created"], "user "+x.Username)
		report["usersNeedingPassword"] = append(report["usersNeedingPassword"], x.Username)
	}
	for _, p := range doc.Permissions {
		if !slices.Contains([]string{accessNone, accessRead, accessWrite}, p.Level) {
			report["errors"] = append(report["errors"], fmt.Sprintf("permission %s/%s: level must be none, read or write", p.Repo, p.Username))
			continue
		}
		if _, err := a.db.Exec(`INSERT INTO repo_perms(repo, username, level) VALUES(?,?,?) ON CONFLICT(repo, username) DO UPDATE SET level=excluded.level`,
			p.Repo, p.Username, p.Level); err != nil {
			report["errors"] = append(report["errors"], fmt.Sprintf("permission %s/%s: %v", p.Repo, p.Username, err))
		}
	}
	existing, _ := a.webhooks("")
	for _, h := range doc.Webhooks {
		if h == nil {
			continue
		}
		dup := false
		for _, e := range existing {
			dup = dup || (e.Name == h.Name && e.URL == h.URL)
		}
		if dup {
			continue
		}
		if _, err := a.db.Exec(`INSERT INTO webhooks(name, url, events, repos, enabled, created_at) VALUES(?,?,?,?,?,?)`,
			h.Name, h.URL, jsonString(nonNil(h.Events)), jsonString(nonNil(h.Repos)), h.Enabled, time.Now().Unix()); err == nil {
			report["created"] = append(report["created"], "webhook "+h.Name)
		}
	}
	// tokens are never exported, so imported channels start paused until a token is entered
	channels, _ := a.notifiers("")
	for _, n := range doc.Notifiers {
		if n == nil {
			continue
		}
		dup := notifierKinds[n.Kind] == nil
		for _, e := range channels {
			dup = dup || (e.Name == n.Name && e.URL == n.URL)
		}
		if dup {
			continue
		}
		if _, err := a.db.Exec(`INSERT INTO notifiers(name, kind, url, priority, alert_priority, events, repos, enabled, created_at) VALUES(?,?,?,?,?,?,?,0,?)`,
			n.Name, n.Kind, n.URL, n.Priority, n.AlertPriority, jsonString(nonNil(n.Events)), jsonString(nonNil(n.Repos)), time.Now().Unix()); err == nil {
			report["created"] = append(report["created"], "notification channel "+n.Name+" (paused: enter its token)")
		}
	}
	if doc.Version != "" {
		if s, err := validSettings(doc.Settings); err != nil {
			report["errors"] = append(report["errors"], "settings: "+err.Error())
		} else if err := a.saveSettings(s); err != nil {
			report["errors"] = append(report["errors"], "settings: "+err.Error())
		} else {
			report["updated"] = append(report["updated"], "settings")
		}
	}
	a.audit(u, "config.import", "system", len(report["created"]), "created", len(report["updated"]), "updated")
	return report, nil
}

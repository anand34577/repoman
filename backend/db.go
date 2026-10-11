package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS repositories (
	name TEXT PRIMARY KEY,
	format TEXT NOT NULL,
	type TEXT NOT NULL,
	config TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS assets (
	id INTEGER PRIMARY KEY,
	repo TEXT NOT NULL REFERENCES repositories(name) ON DELETE CASCADE,
	path TEXT NOT NULL,
	blob TEXT NOT NULL,
	size INTEGER NOT NULL,
	content_type TEXT NOT NULL,
	component TEXT NOT NULL DEFAULT '',
	version TEXT NOT NULL DEFAULT '',
	uploader TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	last_downloaded INTEGER NOT NULL DEFAULT 0,
	downloads INTEGER NOT NULL DEFAULT 0,
	UNIQUE(repo, path)
);
CREATE TABLE IF NOT EXISTS users (
	username TEXT PRIMARY KEY,
	password_hash TEXT NOT NULL,
	email TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tokens (
	id INTEGER PRIMARY KEY,
	hash TEXT NOT NULL UNIQUE,
	username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE ON UPDATE CASCADE,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL DEFAULT 0,
	last_used INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS events (
	id INTEGER PRIMARY KEY,
	ts INTEGER NOT NULL,
	username TEXT NOT NULL,
	action TEXT NOT NULL,
	target TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS repo_perms (
	repo TEXT NOT NULL REFERENCES repositories(name) ON DELETE CASCADE,
	username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
	level TEXT NOT NULL,
	PRIMARY KEY (repo, username)
);
CREATE TABLE IF NOT EXISTS oidc_identities (
	issuer TEXT NOT NULL,
	subject TEXT NOT NULL,
	username TEXT NOT NULL UNIQUE REFERENCES users(username) ON DELETE CASCADE ON UPDATE CASCADE,
	email TEXT NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	PRIMARY KEY (issuer, subject)
);
CREATE TABLE IF NOT EXISTS notifiers (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	kind TEXT NOT NULL,
	url TEXT NOT NULL,
	token TEXT NOT NULL DEFAULT '',
	priority INTEGER NOT NULL DEFAULT 4,
	alert_priority INTEGER NOT NULL DEFAULT 8,
	events TEXT NOT NULL DEFAULT '[]',
	repos TEXT NOT NULL DEFAULT '[]',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	last_status TEXT NOT NULL DEFAULT '',
	last_delivery INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS webhooks (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	url TEXT NOT NULL,
	secret TEXT NOT NULL DEFAULT '',
	events TEXT NOT NULL DEFAULT '[]',
	repos TEXT NOT NULL DEFAULT '[]',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	last_status TEXT NOT NULL DEFAULT '',
	last_delivery INTEGER NOT NULL DEFAULT 0
);
`

// migrations add columns to databases created by earlier versions; "duplicate column" errors are expected.
var migrations = []string{
	`ALTER TABLE assets ADD COLUMN attrs TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE users ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE users ADD COLUMN last_login INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE users ADD COLUMN local_login INTEGER NOT NULL DEFAULT 1`,
	`CREATE INDEX IF NOT EXISTS events_ts ON events(ts)`,
	`ALTER TABLE tokens ADD COLUMN scope TEXT NOT NULL DEFAULT 'full'`,
	`CREATE INDEX IF NOT EXISTS assets_updated ON assets(updated_at)`,
	`CREATE INDEX IF NOT EXISTS assets_created ON assets(created_at)`,               // dashboard "recently published"
	`CREATE INDEX IF NOT EXISTS assets_repo_stats ON assets(repo, size, downloads)`, // index-only per-repo totals
	// covering indexes: the Components tab, folder sizes in Browse, "most downloaded" and "recently published"
	// read these alone instead of visiting every asset row (seconds on repositories with 100,000+ artifacts)
	`CREATE INDEX IF NOT EXISTS assets_components ON assets(repo, component, uploader, size, downloads, updated_at)`,
	`DROP INDEX IF EXISTS assets_component`, // a prefix of assets_components
	`CREATE INDEX IF NOT EXISTS assets_path_size ON assets(repo, path, size)`,
	`CREATE INDEX IF NOT EXISTS assets_downloads ON assets(downloads)`,
	`CREATE INDEX IF NOT EXISTS assets_published ON assets(created_at) WHERE uploader NOT IN ('proxy','system')`,
	// "storage used" walks distinct content files: from this index alone, in order, instead of one row
	// lookup per artifact (seconds on a large database that is not in the file cache yet)
	`CREATE INDEX IF NOT EXISTS assets_blob_size ON assets(blob, size, repo)`,
	`DROP INDEX IF EXISTS assets_blob`,                                  // a prefix of assets_blob_size
	`CREATE INDEX IF NOT EXISTS assets_name ON assets(component, path)`, // search sorted by name
	`CREATE INDEX IF NOT EXISTS assets_size ON assets(size)`,            // search sorted by size
}

// searchSchema is the substring index behind artifact search: an FTS5 trigram index over
// "<path>\n<component>", kept in step with the assets table by triggers. It stores no copy of the text
// (external content, detail=none); matches are verified against the assets table.
const searchSchema = `
CREATE VIEW IF NOT EXISTS assets_search AS SELECT id, path || char(10) || component AS text FROM assets;
CREATE VIRTUAL TABLE assets_fts USING fts5(text, content='assets_search', content_rowid='id', tokenize='trigram', detail='none');
CREATE TRIGGER IF NOT EXISTS assets_fts_insert AFTER INSERT ON assets BEGIN
	INSERT INTO assets_fts(rowid, text) VALUES(new.id, new.path || char(10) || new.component);
END;
CREATE TRIGGER IF NOT EXISTS assets_fts_delete AFTER DELETE ON assets BEGIN
	INSERT INTO assets_fts(assets_fts, rowid, text) VALUES('delete', old.id, old.path || char(10) || old.component);
END;
CREATE TRIGGER IF NOT EXISTS assets_fts_update AFTER UPDATE OF path, component ON assets
WHEN old.path IS NOT new.path OR old.component IS NOT new.component BEGIN
	INSERT INTO assets_fts(assets_fts, rowid, text) VALUES('delete', old.id, old.path || char(10) || old.component);
	INSERT INTO assets_fts(rowid, text) VALUES(new.id, new.path || char(10) || new.component);
END;
INSERT INTO assets_fts(assets_fts) VALUES('rebuild');
`

// ensureSearchIndex creates and fills the search index on the first start with this version. Search
// works without it (by scanning), so a failure here is reported and not fatal.
func ensureSearchIndex(db *sql.DB) bool {
	var n int
	if db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='assets_fts'`).Scan(&n) == nil && n > 0 {
		return true
	}
	db.QueryRow(`SELECT COUNT(*) FROM assets`).Scan(&n)
	if n > 50000 {
		log.Printf("building the search index for %d artifacts (once; about 20 seconds per million)", n)
	}
	tx, err := db.Begin()
	if err == nil {
		defer tx.Rollback()
		if _, err = tx.Exec(searchSchema); err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		log.Printf("search index unavailable, searches will scan instead: %v", err)
		return false
	}
	return true
}

func openDB(file string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(file) + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, err
		}
	}
	return db, nil
}

var errNotFound = errors.New("not found")

// ---------- repositories ----------

var formats = []string{"maven", "npm", "pypi", "docker", "go", "apt", "yum", "conda", "raw"}

type Repo struct {
	Name           string   `json:"name"`
	Format         string   `json:"format"` // see formats
	Type           string   `json:"type"`   // hosted, proxy, group
	Description    string   `json:"description,omitempty"`
	Offline        bool     `json:"offline"` // reject all client requests
	Private        bool     `json:"private"` // never readable anonymously
	RemoteURL      string   `json:"remoteUrl,omitempty"`
	RemoteUsername string   `json:"remoteUsername,omitempty"`
	RemotePassword string   `json:"remotePassword,omitempty"`
	MetadataTTL    int      `json:"metadataTtl"` // minutes before proxied metadata is re-checked
	Members        []string `json:"members,omitempty"`
	AllowRedeploy  bool     `json:"allowRedeploy"`
	Distribution   string   `json:"distribution,omitempty"` // apt hosted: suite name
	QuotaMB        int64    `json:"quotaMb"`                // hosted: refuse uploads past this size; 0 = unlimited
	CleanupDays    int      `json:"cleanupDays"`            // delete assets not downloaded for N days; 0 = off
	RetainVersions int      `json:"retainVersions"`         // keep only the newest N versions per component; 0 = off
	CreatedAt      int64    `json:"createdAt"`
	InvalidatedAt  int64    `json:"invalidatedAt,omitempty"` // proxy: index data cached before this moment is stale
}

func (a *App) getRepo(name string) (*Repo, error) {
	var cfg string
	err := a.db.QueryRow(`SELECT config FROM repositories WHERE name=?`, name).Scan(&cfg)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	r := &Repo{}
	return r, json.Unmarshal([]byte(cfg), r)
}

func (a *App) listRepos() ([]*Repo, error) {
	rows, err := a.db.Query(`SELECT config FROM repositories ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Repo
	for rows.Next() {
		var cfg string
		r := &Repo{}
		if err := rows.Scan(&cfg); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(cfg), r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (a *App) saveRepo(r *Repo) error {
	cfg, _ := json.Marshal(r)
	_, err := a.db.Exec(`INSERT INTO repositories(name, format, type, config) VALUES(?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET config=excluded.config`, r.Name, r.Format, r.Type, string(cfg))
	return err
}

// members returns the group's online member repositories (same format only, missing ones skipped).
func (a *App) members(g *Repo) []*Repo {
	var out []*Repo
	for _, n := range g.Members {
		if m, err := a.getRepo(n); err == nil && m.Format == g.Format && !m.Offline {
			out = append(out, m)
		}
	}
	return out
}

func (a *App) repoSize(name string) (n int64) {
	a.db.QueryRow(`SELECT COALESCE(SUM(size),0) FROM assets WHERE repo=?`, name).Scan(&n)
	return n
}

// checkWritable enforces offline status and storage quota before accepting uploads.
func (a *App) checkWritable(r *Repo) error {
	if r.Offline {
		return httpErr{503, "repository " + r.Name + " is offline"}
	}
	if r.QuotaMB > 0 && a.repoSize(r.Name) >= r.QuotaMB<<20 {
		// one event per hour per repository, however many uploads bounce
		if last, ok := a.quotaWarned.Load(r.Name); !ok || time.Since(last.(time.Time)) > time.Hour {
			a.quotaWarned.Store(r.Name, time.Now())
			a.audit(nil, "repository.quota", r.Name, fmt.Sprintf("uploads are refused: the %d MB storage quota is reached", r.QuotaMB))
		}
		return httpErr{507, fmt.Sprintf("repository %s has reached its %d MB storage quota", r.Name, r.QuotaMB)}
	}
	return nil
}

// ---------- assets ----------

type Asset struct {
	ID             int64           `json:"id"`
	Repo           string          `json:"repo"`
	Path           string          `json:"path"`
	Blob           string          `json:"sha256"`
	Size           int64           `json:"size"`
	ContentType    string          `json:"contentType"`
	Component      string          `json:"component"`
	Version        string          `json:"version"`
	Uploader       string          `json:"uploader"`
	CreatedAt      int64           `json:"createdAt"`
	UpdatedAt      int64           `json:"updatedAt"`
	LastDownloaded int64           `json:"lastDownloaded"`
	Downloads      int64           `json:"downloads"`
	Attrs          json.RawMessage `json:"attrs,omitempty"` // format-specific package metadata (deb control, rpm header…)
}

// All asset queries alias the table as "a".
const assetCols = `a.id, a.repo, a.path, a.blob, a.size, a.content_type, a.component, a.version, a.uploader, a.created_at, a.updated_at, a.last_downloaded, a.downloads, a.attrs`

func scanAsset(s interface{ Scan(...any) error }) (*Asset, error) {
	x := &Asset{}
	var attrs string
	err := s.Scan(&x.ID, &x.Repo, &x.Path, &x.Blob, &x.Size, &x.ContentType, &x.Component, &x.Version, &x.Uploader, &x.CreatedAt, &x.UpdatedAt, &x.LastDownloaded, &x.Downloads, &attrs)
	if attrs != "" {
		x.Attrs = json.RawMessage(attrs)
	}
	return x, err
}

func (a *App) queryAssets(where string, args ...any) ([]*Asset, error) {
	rows, err := a.db.Query(`SELECT `+assetCols+` FROM assets a `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Asset
	for rows.Next() {
		x, err := scanAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (a *App) findAsset(repo, p string) (*Asset, error) {
	x, err := scanAsset(a.db.QueryRow(`SELECT `+assetCols+` FROM assets a WHERE repo=? AND path=?`, repo, p))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return x, err
}

// prefixWhere matches paths starting with prefix as a range on the (repo, path) index instead of a scan.
func prefixWhere(prefix string) (string, []any) {
	if prefix == "" {
		return "", nil
	}
	hi := []byte(prefix)
	hi[len(hi)-1]++ // a UTF-8 byte is never 0xff
	return ` AND path >= ? AND path < ?`, []any{prefix, string(hi)}
}

// assetsUnder lists assets in repo whose path starts with prefix.
func (a *App) assetsUnder(repo, prefix string) ([]*Asset, error) {
	where, args := prefixWhere(prefix)
	return a.queryAssets(`WHERE repo=?`+where+` ORDER BY path`, append([]any{repo}, args...)...)
}

type assetMeta struct {
	ContentType, Component, Version, Uploader string
	Attrs                                     string // JSON
	WantSHA                                   string // if set, content must hash to this
}

func (a *App) putAsset(repo, p string, r io.Reader, m assetMeta) (*Asset, error) {
	tmp, sha, size, err := a.blobs.Write(r)
	if err != nil {
		return nil, err
	}
	return a.storeTemp(repo, p, tmp, sha, size, m)
}

// storeTemp commits a blob written by Blobs.Write and records it as an asset.
func (a *App) storeTemp(repo, p, tmp, sha string, size int64, m assetMeta) (*Asset, error) {
	if m.WantSHA != "" && m.WantSHA != sha {
		a.blobs.Discard(tmp)
		return nil, httpErr{400, "content digest mismatch"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.blobs.Commit(tmp, sha); err != nil {
		return nil, err
	}
	return a.upsertLocked(repo, p, sha, size, m)
}

// linkAsset points a new path at an already stored blob (no copy).
func (a *App) linkAsset(repo, p string, src *Asset, m assetMeta) (*Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.upsertLocked(repo, p, src.Blob, src.Size, m)
}

func (a *App) upsertLocked(repo, p, sha string, size int64, m assetMeta) (*Asset, error) {
	if m.ContentType == "" {
		m.ContentType = mime.TypeByExtension(path.Ext(p))
	}
	if m.ContentType == "" {
		m.ContentType = "application/octet-stream"
	}
	var old string
	a.db.QueryRow(`SELECT blob FROM assets WHERE repo=? AND path=?`, repo, p).Scan(&old)
	now := time.Now().Unix()
	a.assetGen.Add(1)
	_, err := a.db.Exec(`INSERT INTO assets(repo, path, blob, size, content_type, component, version, uploader, attrs, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(repo, path) DO UPDATE SET blob=excluded.blob, size=excluded.size, content_type=excluded.content_type,
			component=excluded.component, version=excluded.version, uploader=excluded.uploader, attrs=excluded.attrs, updated_at=excluded.updated_at`,
		repo, p, sha, size, m.ContentType, m.Component, m.Version, m.Uploader, m.Attrs, now, now)
	if err != nil {
		a.gcLocked(sha)
		return nil, err
	}
	if old != "" && old != sha {
		a.gcLocked(old)
	}
	return a.findAsset(repo, p)
}

// deleteAssets removes matching assets and garbage-collects blobs nobody references anymore.
// Use removeAssets for user-facing deletes so hosted indexes stay consistent.
func (a *App) deleteAssets(where string, args ...any) ([]*Asset, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	list, err := a.queryAssets("WHERE "+where, args...)
	if err != nil {
		return nil, err
	}
	a.assetGen.Add(1)
	for _, x := range list {
		if _, err := a.db.Exec(`DELETE FROM assets WHERE id=?`, x.ID); err != nil {
			return nil, err
		}
		a.gcLocked(x.Blob)
	}
	return list, nil
}

// removeAssets deletes assets, then rebuilds metadata (apt/yum indexes, npm packuments) of affected hosted repos.
func (a *App) removeAssets(where string, args ...any) ([]*Asset, error) {
	list, err := a.deleteAssets(where, args...)
	repos := map[string]bool{}
	for _, x := range list {
		repos[x.Repo] = true
	}
	for name := range repos {
		if err := a.reindex(name); err != nil {
			return list, err
		}
	}
	return list, err
}

func (a *App) gcLocked(sha string) {
	var n int
	if a.db.QueryRow(`SELECT COUNT(*) FROM assets WHERE blob=?`, sha).Scan(&n) == nil && n == 0 {
		a.blobs.Delete(sha)
	}
}

// dlCount is the not yet written part of an asset's download counter.
type dlCount struct{ n, last int64 }

// markDownloaded counts a download in memory. Counters are written in batches (flushDownloads), so
// serving a file never waits for the database's single writer: a busy CI farm pulling cached
// artifacts would otherwise queue every download behind one UPDATE each.
func (a *App) markDownloaded(id int64) {
	a.dlMu.Lock()
	c := a.dlPending[id]
	a.dlPending[id] = dlCount{c.n + 1, time.Now().Unix()}
	a.dlMu.Unlock()
}

// flushDownloads writes the pending download counters in one transaction.
func (a *App) flushDownloads() {
	a.dlMu.Lock()
	pending := a.dlPending
	a.dlPending = map[int64]dlCount{}
	a.dlMu.Unlock()
	if len(pending) == 0 {
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		return // shutting down
	}
	defer tx.Rollback()
	for id, c := range pending {
		if _, err := tx.Exec(`UPDATE assets SET downloads=downloads+?, last_downloaded=? WHERE id=?`, c.n, c.last, id); err != nil {
			log.Printf("recording downloads: %v", err)
			return
		}
	}
	if tx.Commit() == nil {
		a.assetGen.Add(1)
	}
}

// downloadFlusher writes download counters every couple of seconds until the database is closed.
func (a *App) downloadFlusher() {
	for range time.Tick(2 * time.Second) {
		if a.db.Ping() != nil {
			return
		}
		a.flushDownloads()
	}
}

// slowStat caches an aggregate over the assets table. One that is cheap to compute is recomputed as
// soon as assets have changed, so it is always exact. One that is expensive (slowStatCost or more) is
// recomputed at most every 20× the time it takes: half a second on a very large database means a
// refresh every ten seconds instead of on every page view.
const slowStatCost = 25 * time.Millisecond

type slowStat[T any] struct {
	mu   sync.Mutex
	key  string
	gen  int64
	at   time.Time
	cost time.Duration
	val  T
	ok   bool
}

func (s *slowStat[T]) get(key string, gen int64, compute func() (T, error)) (T, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ok && s.key == key && (s.gen == gen || (s.cost >= slowStatCost && time.Since(s.at) < min(20*s.cost, 30*time.Second))) {
		return s.val, nil
	}
	start := time.Now()
	v, err := compute()
	if err != nil {
		return v, err
	}
	s.key, s.gen, s.at, s.cost, s.val, s.ok = key, gen, time.Now(), time.Since(start), v, true
	return v, nil
}

func (a *App) readAsset(x *Asset) ([]byte, error) {
	f, err := a.blobs.Open(x.Blob)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// ---------- settings & audit ----------

type Settings struct {
	AnonymousAccess bool   `json:"anonymousAccess"` // anonymous read access to non-private repositories
	BaseURL         string `json:"baseUrl"`         // public URL override, e.g. https://repo.example.com
	Banner          string `json:"banner"`          // optional notice shown to all users in the UI
	AuditRetention  int    `json:"auditRetentionDays"`
}

func (a *App) settings() Settings {
	s := Settings{AnonymousAccess: true, AuditRetention: 90}
	a.getJSON("settings", &s)
	return s
}

func (a *App) saveSettings(s Settings) error { return a.setJSON("settings", s) }

func (a *App) getJSON(key string, v any) bool {
	var s string
	if a.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&s) != nil {
		return false
	}
	return json.Unmarshal([]byte(s), v) == nil
}

func (a *App) setJSON(key string, v any) error {
	b, _ := json.Marshal(v)
	_, err := a.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, string(b))
	return err
}

// audit records an event and forwards it to matching webhooks and notification channels.
func (a *App) audit(u *User, action, target string, detail ...any) {
	who := "system"
	if u != nil {
		who = u.Username
	}
	d := strings.TrimSpace(fmt.Sprintln(detail...))
	ts := time.Now().Unix()
	a.db.Exec(`INSERT INTO events(ts, username, action, target, detail) VALUES(?,?,?,?,?)`, ts, who, action, target, d)
	e := Event{Event: action, Actor: who, Target: target, Detail: d, Timestamp: ts}
	a.emitWebhooks(e)
	a.emitNotifications(e)
}

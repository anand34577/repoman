package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

type taskDef struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Every       time.Duration `json:"-"`
	run         func(*App) (string, error)
}

type taskState struct {
	LastRun    int64  `json:"lastRun"`
	DurationMs int64  `json:"durationMs"`
	Result     string `json:"result"`
	Error      string `json:"error,omitempty"`
	Running    bool   `json:"running"`
}

var taskDefs = []taskDef{
	{"cleanup", "Run cleanup policies", "Applies every repository's cleanup policy: delete assets unused for N days and keep only the newest N versions per component.", time.Hour, (*App).cleanupAll},
	{"purge", "Purge expired data", "Deletes expired sessions and tokens, abandoned Docker uploads, and audit events past the retention period.", time.Hour, (*App).purge},
	{"blob-compact", "Compact blob store", "Removes content files that no asset references (e.g. after a crash) and stale temporary files.", 24 * time.Hour, (*App).compactBlobs},
	{"storage-snapshot", "Record storage usage", "Stores each repository's size once a day so the Storage page can chart growth, and trims download history older than 400 days.", 24 * time.Hour, (*App).storageSnapshot},
	{"optimize-db", "Optimize database", "Runs SQLite VACUUM and ANALYZE to reclaim disk space and keep queries fast.", 7 * 24 * time.Hour, (*App).optimizeDB},
	{"blob-verify", "Verify blob store", "Checks that the content file of every asset exists on disk and reports any that are missing.", 0, (*App).verifyBlobs},
	{"deps-index", "Index package dependencies", "Reads every Maven pom, go.mod, conda package and hosted npm package to record what depends on what (the Dependents list on package pages). New uploads are indexed automatically.", 0, (*App).indexDepsAll},
	{"rebuild-indexes", "Rebuild hosted indexes", "Regenerates apt Release/Packages, yum repodata and npm package metadata for all hosted repositories.", 0, (*App).reindexAll},
}

// scheduler runs periodic tasks whose interval has elapsed.
func (a *App) scheduler() {
	var done bool
	if !a.getJSON("depsIndexed", &done) || !done { // first start with the dependency index: fill it once
		go a.runTask("deps-index")
	}
	for range time.Tick(time.Minute) {
		for _, t := range taskDefs {
			a.tasksMu.Lock()
			st := a.tasks[t.ID]
			due := t.Every > 0 && (st == nil || (!st.Running && time.Since(time.Unix(st.LastRun, 0)) >= t.Every))
			a.tasksMu.Unlock()
			if due {
				a.runTask(t.ID)
			}
		}
	}
}

func (a *App) runTask(id string) error {
	var def *taskDef
	for i := range taskDefs {
		if taskDefs[i].ID == id {
			def = &taskDefs[i]
		}
	}
	if def == nil {
		return errNotFound
	}
	a.tasksMu.Lock()
	st := a.tasks[id]
	if st == nil {
		st = &taskState{}
		a.tasks[id] = st
	}
	if st.Running {
		a.tasksMu.Unlock()
		return httpErr{409, "task is already running"}
	}
	st.Running = true
	a.tasksMu.Unlock()

	start := time.Now()
	result, err := func() (res string, err error) {
		// tasks run on their own goroutines: a panic here would otherwise take the whole server down
		defer func() {
			if p := recover(); p != nil {
				log.Printf("task %s panicked: %v\n%s", id, p, debug.Stack())
				err = fmt.Errorf("internal error: %v", p)
			}
		}()
		return def.run(a)
	}()
	a.tasksMu.Lock()
	st.Running, st.LastRun, st.DurationMs, st.Result, st.Error = false, start.Unix(), time.Since(start).Milliseconds(), result, ""
	if err != nil {
		st.Error = err.Error()
		log.Printf("task %s: %v", id, err)
	}
	a.setJSON("tasks", a.tasks)
	a.tasksMu.Unlock()
	if err != nil {
		a.audit(nil, "task.failed", id, def.Name+":", err.Error())
	}
	return err
}

func (a *App) listTasksAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	type view struct {
		taskDef
		taskState
		Schedule string `json:"schedule"`
		NextRun  int64  `json:"nextRun"`
	}
	a.tasksMu.Lock()
	defer a.tasksMu.Unlock()
	out := []view{}
	for _, t := range taskDefs {
		v := view{taskDef: t, Schedule: "Manual"}
		if st := a.tasks[t.ID]; st != nil {
			v.taskState = *st
		}
		if t.Every > 0 {
			v.Schedule = map[time.Duration]string{time.Hour: "Hourly", 24 * time.Hour: "Daily", 7 * 24 * time.Hour: "Weekly"}[t.Every]
			v.NextRun = max(v.LastRun+int64(t.Every.Seconds()), time.Now().Unix())
		}
		out = append(out, v)
	}
	return out, nil
}

func (a *App) runTaskAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	id := r.PathValue("id")
	if !slices.ContainsFunc(taskDefs, func(t taskDef) bool { return t.ID == id }) {
		return nil, errNotFound
	}
	a.tasksMu.Lock()
	running := a.tasks[id] != nil && a.tasks[id].Running
	a.tasksMu.Unlock()
	if running {
		return nil, httpErr{409, "task is already running"}
	}
	go a.runTask(id)
	a.audit(u, "task.run", id)
	return map[string]bool{"started": true}, nil
}

// ---------- cleanup policies ----------

// cleanupCandidates returns what a repository's cleanup policy would delete (generated indexes excluded).
func (a *App) cleanupCandidates(r *Repo) ([]*Asset, error) {
	a.flushDownloads() // "unused for N days" must see downloads that are still only counted in memory
	var out []*Asset
	seen := map[int64]bool{}
	add := func(list []*Asset) {
		for _, x := range list {
			if !seen[x.ID] {
				seen[x.ID] = true
				out = append(out, x)
			}
		}
	}
	if r.CleanupDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -r.CleanupDays).Unix()
		list, err := a.queryAssets(`WHERE repo=? AND uploader!='system' AND MAX(last_downloaded, updated_at) < ?`, r.Name, cutoff)
		if err != nil {
			return nil, err
		}
		add(list)
	}
	if r.RetainVersions > 0 {
		rows, err := a.db.Query(`SELECT component, version FROM assets WHERE repo=? AND component!='' AND version!='' AND uploader!='system'
			GROUP BY component, version ORDER BY component, MAX(created_at) DESC`, r.Name)
		if err != nil {
			return nil, err
		}
		var old [][2]string
		count := map[string]int{}
		for rows.Next() {
			var c, v string
			rows.Scan(&c, &v)
			if count[c]++; count[c] > r.RetainVersions {
				old = append(old, [2]string{c, v})
			}
		}
		rows.Close()
		for _, cv := range old {
			list, err := a.queryAssets(`WHERE repo=? AND component=? AND version=?`, r.Name, cv[0], cv[1])
			if err != nil {
				return nil, err
			}
			add(list)
		}
	}
	return out, nil
}

func (a *App) cleanup(r *Repo) (int, int64, error) {
	list, err := a.cleanupCandidates(r)
	if err != nil || len(list) == 0 {
		return 0, 0, err
	}
	var bytes int64
	for i := 0; i < len(list); i += 500 {
		chunk := list[i:min(i+500, len(list))]
		ids := make([]any, len(chunk))
		for j, x := range chunk {
			ids[j] = x.ID
			bytes += x.Size
		}
		if _, err := a.removeAssets(`id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`, ids...); err != nil {
			return 0, 0, err
		}
	}
	return len(list), bytes, nil
}

func (a *App) cleanupAll() (string, error) {
	repos, err := a.listRepos()
	if err != nil {
		return "", err
	}
	total, bytes := 0, int64(0)
	for _, r := range repos {
		if r.CleanupDays == 0 && r.RetainVersions == 0 {
			continue
		}
		n, b, err := a.cleanup(r)
		if err != nil {
			return "", fmt.Errorf("%s: %w", r.Name, err)
		}
		if n > 0 {
			a.audit(nil, "cleanup", r.Name, n, "assets")
		}
		total, bytes = total+n, bytes+b
	}
	return fmt.Sprintf("Deleted %d assets (%s)", total, humanBytes(bytes)), nil
}

// ---------- maintenance ----------

func (a *App) purge() (string, error) {
	now := time.Now()
	res, err := a.db.Exec(`DELETE FROM tokens WHERE expires_at > 0 AND expires_at < ?`, now.Unix())
	if err != nil {
		return "", err
	}
	tokens, _ := res.RowsAffected()
	days := a.settings().AuditRetention
	var events int64
	if days > 0 {
		res, err = a.db.Exec(`DELETE FROM events WHERE ts < ?`, now.AddDate(0, 0, -days).Unix())
		if err != nil {
			return "", err
		}
		events, _ = res.RowsAffected()
	}
	uploads := 0
	entries, _ := os.ReadDir(a.uploads)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > 24*time.Hour {
			os.Remove(filepath.Join(a.uploads, e.Name()))
			uploads++
		}
	}
	a.negCache.Range(func(k, v any) bool {
		if now.After(v.(time.Time)) {
			a.negCache.Delete(k)
		}
		return true
	})
	a.authMu.Lock() // forget finished lockouts and expired credential-cache entries
	for ip, f := range a.fails {
		if now.Sub(f.first) > 15*time.Minute {
			delete(a.fails, ip)
		}
	}
	for k, c := range a.authCache {
		if now.After(c.exp) {
			delete(a.authCache, k)
		}
	}
	a.authMu.Unlock()
	return fmt.Sprintf("Removed %d expired tokens/sessions, %d audit events, %d abandoned uploads", tokens, events, uploads), nil
}

var shaName = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (a *App) compactBlobs() (string, error) {
	removed, freed, err := a.scanBlobs(true)
	return fmt.Sprintf("Removed %d orphaned files, freed %s", removed, humanBytes(freed)), err
}

func (a *App) verifyBlobs() (string, error) {
	rows, err := a.db.Query(`SELECT blob, MIN(repo || '/' || path) FROM assets GROUP BY blob`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	checked := 0
	var missing []string
	for rows.Next() {
		var sha, example string
		rows.Scan(&sha, &example)
		checked++
		if _, err := os.Stat(a.blobs.path(sha)); err != nil {
			missing = append(missing, example)
		}
	}
	if len(missing) == 0 {
		return fmt.Sprintf("All %d content files present", checked), nil
	}
	return fmt.Sprintf("%d of %d content files missing, e.g. %s", len(missing), checked, strings.Join(missing[:min(5, len(missing))], ", ")),
		fmt.Errorf("%d missing content files", len(missing))
}

func (a *App) reindexAll() (string, error) {
	repos, err := a.listRepos()
	if err != nil {
		return "", err
	}
	n := 0
	for _, r := range repos {
		if r.Type == "hosted" && (r.Format == "apt" || r.Format == "yum" || r.Format == "npm") {
			if err := a.reindex(r.Name); err != nil {
				return "", fmt.Errorf("%s: %w", r.Name, err)
			}
			n++
		}
	}
	return fmt.Sprintf("Rebuilt indexes of %d repositories", n), nil
}

func (a *App) optimizeDB() (string, error) {
	if a.searchIndex { // merge the search index's segments; cheap when there is little to merge
		if _, err := a.db.Exec(`INSERT INTO assets_fts(assets_fts) VALUES('optimize')`); err != nil {
			return "", err
		}
	}
	if _, err := a.db.Exec(`VACUUM`); err != nil {
		return "", err
	}
	if _, err := a.db.Exec(`ANALYZE`); err != nil {
		return "", err
	}
	return "Database vacuumed and analyzed", nil
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

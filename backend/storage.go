package main

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Storage usage over time and what could be reclaimed.

// storageSnapshot records today's size of every repository (and of the whole store, as repo "") so the Storage
// page can chart growth. It runs daily; running it again the same day just refreshes that day's rows.
func (a *App) storageSnapshot() (string, error) {
	stats, err := a.repoStats()
	if err != nil {
		return "", err
	}
	day := time.Now().Unix() / 86400
	tx, err := a.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	put := `INSERT INTO storage_samples(day, repo, assets, bytes, downloads) VALUES(?,?,?,?,?)
		ON CONFLICT(day, repo) DO UPDATE SET assets=excluded.assets, bytes=excluded.bytes, downloads=excluded.downloads`
	for name, v := range stats {
		if _, err := tx.Exec(put, day, name, v.AssetCount, v.Size, v.Downloads); err != nil {
			return "", err
		}
	}
	files, bytes, err := a.storedTotals(nil, true)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(put, day, "", files, bytes, 0); err != nil {
		return "", err
	}
	tx.Exec(`DELETE FROM storage_samples WHERE day < ?`, day-800)
	tx.Exec(`DELETE FROM downloads_daily WHERE day < ?`, day-400)
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return "Recorded the size of " + strconv.Itoa(len(stats)) + " repositories (" + humanBytes(bytes) + " on disk)", nil
}

// scanBlobs walks the content store. With remove it deletes content files no asset references and stale
// temporary files; without, it only counts what that would free.
func (a *App) scanBlobs(remove bool) (files int, freed int64, err error) {
	err = filepath.WalkDir(a.blobs.dir, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		name := d.Name()
		switch {
		case strings.HasPrefix(name, "tmp-") && time.Since(info.ModTime()) > time.Hour:
			if remove {
				os.Remove(p)
			}
			files, freed = files+1, freed+info.Size()
		case shaName.MatchString(name):
			if remove {
				a.mu.Lock()
				a.gcLocked(name) // deletes only if no asset references it
				a.mu.Unlock()
				if _, serr := os.Stat(p); os.IsNotExist(serr) {
					files, freed = files+1, freed+info.Size()
				}
			} else {
				var used bool
				a.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM assets WHERE blob=?)`, name).Scan(&used)
				if !used {
					files, freed = files+1, freed+info.Size()
				}
			}
		}
		return nil
	})
	return files, freed, err
}

type storageRepo struct {
	Name      string `json:"name"`
	Format    string `json:"format"`
	Type      string `json:"type"`
	Assets    int64  `json:"assets"`
	Bytes     int64  `json:"bytes"`
	Downloads int64  `json:"downloads"`
	QuotaMB   int64  `json:"quotaMb"`
	Growth    int64  `json:"growth"` // bytes added over the chart period; 0 when there is no earlier sample
}

type storagePoint struct {
	Day   string `json:"day"`
	Bytes int64  `json:"bytes"` // distinct bytes on disk
	Logic int64  `json:"logical"`
}

// storageAPI reports where the space goes: per repository, the installation's growth over the last N days and,
// with ?scan=1, how much unreferenced content a compaction would free.
func (a *App) storageAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 7 || days > 365 {
		days = 90
	}
	repos, err := a.listRepos()
	if err != nil {
		return nil, err
	}
	stats, err := a.repoStats()
	if err != nil {
		return nil, err
	}
	since := time.Now().Unix()/86400 - int64(days)
	first := map[string]int64{}
	logical := map[int64]int64{}
	physical := map[int64]int64{}
	rows, err := a.db.Query(`SELECT day, repo, bytes FROM storage_samples WHERE day>=? ORDER BY day`, since)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d, b int64
		var name string
		rows.Scan(&d, &name, &b)
		if name == "" {
			physical[d] = b
			continue
		}
		logical[d] += b
		if _, ok := first[name]; !ok {
			first[name] = b
		}
	}
	rows.Close()
	out := struct {
		Repos       []storageRepo  `json:"repos"`
		Series      []storagePoint `json:"series"`
		Physical    int64          `json:"physical"`
		Logical     int64          `json:"logical"`
		Reclaimable *int64         `json:"reclaimable,omitempty"`
		Orphans     *int           `json:"orphans,omitempty"`
	}{Repos: []storageRepo{}, Series: []storagePoint{}}
	for _, rp := range repos {
		if rp.Type == "group" {
			continue
		}
		v := stats[rp.Name]
		s := storageRepo{Name: rp.Name, Format: rp.Format, Type: rp.Type, Assets: v.AssetCount, Bytes: v.Size, Downloads: v.Downloads, QuotaMB: rp.QuotaMB}
		if f, ok := first[rp.Name]; ok {
			s.Growth = v.Size - f
		}
		out.Logical += v.Size
		out.Repos = append(out.Repos, s)
	}
	sort.Slice(out.Repos, func(i, j int) bool { return out.Repos[i].Bytes > out.Repos[j].Bytes })
	_, out.Physical, _ = a.storedTotals(nil, true)
	var ds []int64
	for d := range physical {
		ds = append(ds, d)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	for _, d := range ds {
		out.Series = append(out.Series, storagePoint{time.Unix(d*86400, 0).UTC().Format("2006-01-02"), physical[d], logical[d]})
	}
	if r.URL.Query().Get("scan") != "" {
		n, freed, err := a.scanBlobs(false)
		if err != nil {
			return nil, err
		}
		out.Reclaimable, out.Orphans = &freed, &n
	}
	return out, nil
}

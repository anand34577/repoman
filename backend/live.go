package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A proxy cache miss starts one liveDL per file: a download that runs on its own, independent of any client,
// into a temp file that is committed to the blob store when it is complete. Every client asking for the file
// meanwhile (the first one, concurrent ones, HEAD and Range requests) is served from that temp file as it
// grows, so nobody waits for the whole download and the upstream is contacted once.
type liveDL struct {
	key, repo, path string
	started         time.Time

	mu       sync.Mutex
	notify   chan struct{} // closed and replaced on every change; readers wait on it
	hdrOK    bool          // the upstream answered; total, ctype and commit are known
	written  int64
	total    int64 // -1 when the upstream did not say
	ctype    string
	commit   string // upstream commit id, when it sent one (Hugging Face)
	etag     string // upstream's content hash, when it sent one: Hugging Face clients refuse an answer without an ETag
	complete bool   // every byte has arrived
	done     bool
	err      error
	asset    *Asset
	clients  atomic.Int32

	rw     sync.RWMutex // guards f: a chunk read holds the read lock, closing it before the commit the write lock
	f      *os.File
	doneCh chan struct{}
}

// downloadStats counts proxy downloads for the system page and /metrics.
type downloadStats struct {
	started, joined, failed, stalled, bytes atomic.Int64
}

const liveChunk = 64 << 10

func (d *liveDL) bump() { // caller holds d.mu
	close(d.notify)
	d.notify = make(chan struct{})
}

// upstreamCapture carries headers of the first upstream response of a redirect chain out of the HTTP client.
type upstreamCapture struct{ commit, etag, link string }

type captureKey struct{}

// redirectPolicy is the upstream client's CheckRedirect: the default limit, plus noting X-Repo-Commit and
// X-Linked-Etag (the content hash of a large file), which Hugging Face sends on the redirect to its CDN and not on the file itself.
func redirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if c, _ := req.Context().Value(captureKey{}).(*upstreamCapture); c != nil && req.Response != nil {
		if c.commit == "" {
			c.commit = req.Response.Header.Get("X-Repo-Commit")
		}
		if c.etag == "" {
			c.etag = req.Response.Header.Get("X-Linked-Etag")
		}
	}
	return nil
}

// joinLive returns the running download of a file, starting it when there is none. cachedAt is the update time
// of the cache entry that made the caller fetch (0 for none): a newer one means someone else just finished.
func (a *App) joinLive(repo *Repo, p string, hdr http.Header, cachedAt int64) *liveDL {
	key := repo.Name + "\x00" + p
	a.liveMu.Lock()
	defer a.liveMu.Unlock()
	if d := a.live[key]; d != nil {
		a.dl.joined.Add(1)
		return d
	}
	d := &liveDL{key: key, repo: repo.Name, path: p, started: time.Now(), notify: make(chan struct{}), doneCh: make(chan struct{}), total: -1}
	if x, err := a.findAsset(repo.Name, p); err == nil && x.UpdatedAt > cachedAt {
		d.asset, d.done, d.complete, d.hdrOK = x, true, true, true
		close(d.doneCh)
		return d
	}
	a.live[key] = d
	a.dl.started.Add(1)
	go a.runLive(d, repo, p, hdr)
	return d
}

// activeDownloads lists the downloads in flight, longest running first.
func (a *App) activeDownloads() []map[string]any {
	a.liveMu.Lock()
	list := make([]*liveDL, 0, len(a.live))
	for _, d := range a.live {
		list = append(list, d)
	}
	a.liveMu.Unlock()
	out := make([]map[string]any, 0, len(list))
	for _, d := range list {
		d.mu.Lock()
		out = append(out, map[string]any{"repo": d.repo, "path": d.path, "bytes": d.written, "total": d.total,
			"startedAt": d.started.Unix(), "clients": d.clients.Load(), "headersReceived": d.hdrOK})
		d.mu.Unlock()
	}
	sortBy(out, func(m map[string]any) int64 { return m["startedAt"].(int64) })
	return out
}

func sortBy[T any](s []T, key func(T) int64) {
	for i := 1; i < len(s); i++ { // lists are tiny (concurrent downloads)
		for j := i; j > 0 && key(s[j]) < key(s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (a *App) runLive(d *liveDL, repo *Repo, p string, hdr http.Header) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	asset, err := a.downloadLive(ctx, cancel, d, repo, p, hdr)
	if err != nil {
		a.dl.failed.Add(1)
	}
	d.mu.Lock()
	d.asset, d.err, d.done = asset, err, true
	close(d.doneCh)
	d.bump()
	d.mu.Unlock()
	a.liveMu.Lock()
	delete(a.live, d.key)
	a.liveMu.Unlock()
}

// openUpstream requests a file and classifies the answer; a 200 is returned with its body open.
func (a *App) openUpstream(ctx context.Context, repo *Repo, p string, hdr http.Header) (*http.Response, *upstreamCapture, error) {
	u, err := a.upstreamURL(ctx, repo, p)
	if err != nil {
		return nil, nil, err
	}
	capt := &upstreamCapture{}
	resp, err := a.fetchRemote(context.WithValue(ctx, captureKey{}, capt), repo, u, hdr)
	if err != nil {
		a.upstreamFailed(repo.Name, err)
		return nil, nil, err
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		resp.Body.Close()
		a.recordHealth(repo.Name, nil)
		return nil, nil, errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		err := upstreamStatus{u, resp.StatusCode, resp.Status}
		a.upstreamFailed(repo.Name, err)
		return nil, nil, err
	}
	a.recordHealth(repo.Name, nil)
	if capt.commit == "" {
		capt.commit = resp.Header.Get("X-Repo-Commit")
	}
	if capt.etag == "" {
		capt.etag = resp.Header.Get("ETag")
	}
	capt.link = resp.Header.Get("Link") // pagination of Hugging Face listings
	return resp, capt, nil
}

func (a *App) downloadLive(ctx context.Context, cancel context.CancelFunc, d *liveDL, repo *Repo, p string, hdr http.Header) (*Asset, error) {
	resp, capt, err := a.openUpstream(ctx, repo, p, hdr)
	if err != nil {
		return nil, err
	}
	commit := capt.commit
	body := newIdleBody(resp.Body, a.upstreamIdle, cancel)
	defer body.Close()
	if err := a.checkDisk(resp.ContentLength); err != nil {
		return nil, err
	}
	f, err := a.blobs.Create()
	if err != nil {
		return nil, err
	}
	d.rw.Lock()
	d.f = f
	d.rw.Unlock()
	fail := func(err error) (*Asset, error) {
		if ctx.Err() != nil && !errors.Is(err, errNotFound) {
			a.dl.stalled.Add(1)
			err = fmt.Errorf("upstream stopped sending data for %s: %w", a.upstreamIdle, err)
		}
		d.rw.Lock()
		f.Close()
		os.Remove(f.Name())
		d.f = nil
		d.rw.Unlock()
		return nil, err
	}
	comp, ver := componentOf(repo.Format, p)
	m := assetMeta{ContentType: resp.Header.Get("Content-Type"), Component: comp, Version: ver, Uploader: "proxy"}
	if repo.Format == "huggingface" { // kept so cached answers carry the same headers the upstream sent
		at := map[string]string{}
		for k, v := range map[string]string{"upstreamCommit": commit, "upstreamEtag": capt.etag, "upstreamLink": capt.link} {
			if v != "" {
				at[k] = v
			}
		}
		if len(at) > 0 {
			b, _ := json.Marshal(at)
			m.Attrs = string(b)
		}
	}
	if m.ContentType == "" { // same fallback as upsertLocked, so a streamed response and later cache hits agree
		m.ContentType = guessType(p)
	}
	d.mu.Lock()
	d.total, d.ctype, d.commit, d.etag, d.hdrOK = resp.ContentLength, m.ContentType, commit, capt.etag, true
	d.bump()
	d.mu.Unlock()

	h := sha256.New()
	buf := make([]byte, 256<<10)
	var off int64
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := f.WriteAt(buf[:n], off); werr != nil {
				return fail(werr)
			}
			h.Write(buf[:n])
			off += int64(n)
			a.dl.bytes.Add(int64(n))
			d.mu.Lock()
			d.written = off
			d.bump()
			d.mu.Unlock()
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fail(rerr)
		}
	}
	if resp.ContentLength >= 0 && off != resp.ContentLength {
		return fail(io.ErrUnexpectedEOF)
	}
	d.mu.Lock()
	d.complete = true
	d.bump()
	d.mu.Unlock()
	d.rw.Lock() // readers mid-chunk finish first; afterwards they continue from the stored blob
	err = f.Close()
	d.f = nil
	d.rw.Unlock()
	if err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	return a.storeTemp(repo.Name, p, f.Name(), hex.EncodeToString(h.Sum(nil)), off, m)
}

// wait blocks until the download is over.
func (d *liveDL) wait(ctx context.Context) (*Asset, error) {
	select {
	case <-d.doneCh:
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.asset, d.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// waitHeaders blocks until the upstream has answered (or the download failed or finished).
func (d *liveDL) waitHeaders(ctx context.Context) error {
	for {
		d.mu.Lock()
		ok, done, ch := d.hdrOK, d.done, d.notify
		d.mu.Unlock()
		if ok || done {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// readAt reads cached bytes: from the growing temp file, or once that is committed from the stored blob.
func (d *liveDL) readAt(ctx context.Context, a *App, p []byte, off int64, blob **os.File) (int, error) {
	if *blob == nil {
		d.rw.RLock()
		if d.f != nil {
			n, err := d.f.ReadAt(p, off)
			d.rw.RUnlock()
			if err == io.EOF && n > 0 {
				err = nil
			}
			return n, err
		}
		d.rw.RUnlock()
		x, err := d.wait(ctx)
		if err != nil {
			return 0, err
		}
		if *blob, err = a.blobs.Open(x.Blob); err != nil {
			return 0, err
		}
	}
	n, err := (*blob).ReadAt(p, off)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}

// parseRange understands the single-range forms clients use for resuming: bytes=a-b, a- and -n.
func parseRange(h string, total int64) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") || total <= 0 {
		return 0, 0, false
	}
	l, r, _ := strings.Cut(spec, "-")
	var err1, err2 error
	switch {
	case l == "":
		var n int64
		if n, err1 = strconv.ParseInt(r, 10, 64); err1 != nil || n <= 0 {
			return 0, 0, false
		}
		return max(total-n, 0), total - 1, true
	case r == "":
		start, err1 = strconv.ParseInt(l, 10, 64)
		end = total - 1
	default:
		start, err1 = strconv.ParseInt(l, 10, 64)
		end, err2 = strconv.ParseInt(r, 10, 64)
		end = min(end, total-1)
	}
	if err1 != nil || err2 != nil || start < 0 || start > end {
		return 0, 0, false
	}
	return start, end, true
}

// serveLive answers the request that triggered or joined a download from the download itself. A returned asset
// with s.started false means "serve it from the cache as usual" (the download was over before a byte was due,
// or this request needs the finished file); with s.started true the response is complete.
func (a *App) serveLive(s *streamSink, d *liveDL) (*Asset, error) {
	r := s.r
	d.clients.Add(1)
	defer d.clients.Add(-1)
	ctx := r.Context()
	if err := d.waitHeaders(ctx); err != nil {
		return nil, err
	}
	d.mu.Lock()
	total, ctype, commit, etag, done := d.total, d.ctype, d.commit, d.etag, d.done
	d.mu.Unlock()
	if done {
		return d.wait(ctx)
	}
	wantCommit := s.hdr.Get("X-Repo-Commit") == "" && s.needCommit
	var start, end int64 = 0, total - 1
	partial := false
	if rng := r.Header.Get("Range"); rng != "" && r.Method == http.MethodGet {
		var ok bool
		if start, end, ok = parseRange(rng, total); !ok || r.Header.Get("If-Range") != "" {
			return d.wait(ctx) // let the cache path answer (416, multi-range, unknown length…)
		}
		partial = true
	}
	if wantCommit && (commit == "" || etag == "") {
		return d.wait(ctx) // the commit id and ETag are derived from the content instead
	}
	h := s.w.Header()
	for k, v := range s.hdr {
		h[k] = v
	}
	if wantCommit {
		h.Set("X-Repo-Commit", commit)
		h.Set("ETag", etag)
	}
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", ctype)
	}
	status := http.StatusOK
	if total >= 0 {
		h.Set("Accept-Ranges", "bytes")
		h.Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		if partial {
			h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
			status = http.StatusPartialContent
		}
	}
	s.started = true
	s.w.WriteHeader(status)
	rc := http.NewResponseController(s.w)
	rc.Flush() // headers reach the client now: its timeout is for the first byte, not for the whole file
	if r.Method == http.MethodHead {
		return &Asset{}, nil
	}
	off, stop := start, end+1 // stop < 0 when the length is unknown: read until the download completes
	if total < 0 {
		stop = -1
	}
	buf := make([]byte, liveChunk)
	var blob *os.File
	defer func() {
		if blob != nil {
			blob.Close()
		}
	}()
	for stop < 0 || off < stop {
		d.mu.Lock()
		avail, complete, err, ch := d.written-off, d.complete, d.err, d.notify
		d.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if avail <= 0 {
			if complete {
				break
			}
			select {
			case <-ch:
			case <-ctx.Done():
				return &Asset{}, nil // the client left; the download carries on
			}
			continue
		}
		n := min(avail, int64(len(buf)))
		if stop >= 0 {
			n = min(n, stop-off)
		}
		read, rerr := d.readAt(ctx, a, buf[:n], off, &blob)
		if rerr != nil {
			if ctx.Err() != nil {
				return &Asset{}, nil
			}
			return nil, rerr
		}
		if _, werr := s.w.Write(buf[:read]); werr != nil {
			return &Asset{}, nil
		}
		rc.Flush()
		off += int64(read)
	}
	x, err := d.wait(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return &Asset{}, nil
		}
		return nil, err
	}
	a.markDownloaded(x.ID)
	return x, nil
}

// warmAPI fetches files into a proxy's cache ahead of use, in the background and a few at a time, so the first
// real client does not have to wait for a large download. Progress shows under the active downloads.
func (a *App) warmAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	repo, err := a.getRepo(r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	if repo.Type != "proxy" {
		return nil, httpErr{400, "only proxy repositories have a cache to warm"}
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := readJSON(r, &body); err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range body.Paths {
		if p = strings.Trim(strings.TrimSpace(p), "/"); p != "" {
			if !validPath(p) {
				return nil, httpErr{400, "invalid path: " + p}
			}
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 || len(paths) > 500 {
		return nil, httpErr{400, "give between 1 and 500 paths"}
	}
	go func() {
		sem := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, p := range paths {
			if _, err := a.findAsset(repo.Name, p); err == nil && !isMetadata(repo.Format, p) {
				continue // already cached
			}
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				if _, err := a.joinLive(repo, p, nil, 0).wait(context.Background()); err != nil {
					log.Printf("warm %s/%s: %v", repo.Name, p, err)
				}
			}()
		}
		wg.Wait()
	}()
	a.audit(u, "cache.warm", repo.Name, fmt.Sprintf("%d files", len(paths)))
	return map[string]int{"queued": len(paths)}, nil
}

func (a *App) downloadsAPI(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	return map[string]any{
		"active": a.activeDownloads(),
		"totals": map[string]int64{"started": a.dl.started.Load(), "joined": a.dl.joined.Load(), "failed": a.dl.failed.Load(),
			"stalled": a.dl.stalled.Load(), "bytes": a.dl.bytes.Load()},
	}, nil
}

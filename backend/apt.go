package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// APT (Debian/Ubuntu). Hosted layout:
//   pool/main/<prefix>/<pkg>/<pkg>_<version>_<arch>.deb
//   dists/<dist>/{Release,InRelease,Release.gpg}
//   dists/<dist>/main/binary-<arch>/Packages{,.gz}
// Proxies mirror the upstream layout verbatim (signatures stay upstream's).

type debInfo struct {
	Package      string `json:"package"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	Maintainer   string `json:"maintainer,omitempty"`
	Description  string `json:"description,omitempty"`
	Depends      string `json:"depends,omitempty"`
	Control      string `json:"control"`
	MD5          string `json:"md5"`
	SHA1         string `json:"sha1"`
}

func (a *App) aptHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !validPath(p) {
			http.NotFound(w, r)
			return
		}
		x, err := a.resolve(r.Context(), repo, p, r.Header, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		a.serveAsset(w, r, x)
	case http.MethodPut, http.MethodPost:
		if p != "" && (strings.Contains(p, "/") || !strings.HasSuffix(p, ".deb")) {
			http.Error(w, "upload .deb packages to the repository root; they are placed in pool/ automatically", http.StatusBadRequest)
			return
		}
		if _, err := a.ingestDeb(repo, r.Body, u); err != nil {
			resolveError(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		a.deletePath(w, r, repo, p, u)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ingestDeb stores an uploaded .deb under pool/ and rebuilds the signed indexes.
func (a *App) ingestDeb(repo *Repo, body io.Reader, u *User) (*Asset, error) {
	x, err := a.storeDeb(repo, body, u)
	if err != nil {
		return nil, err
	}
	return x, a.aptReindex(repo)
}

// storeDeb is ingestDeb without the index rebuild, for callers that store several packages.
func (a *App) storeDeb(repo *Repo, body io.Reader, u *User) (*Asset, error) {
	tmp, sha, size, err := a.blobs.Write(body)
	if err != nil {
		return nil, err
	}
	info, err := readDeb(tmp)
	if err != nil {
		a.blobs.Discard(tmp)
		return nil, httpErr{400, "not a valid .deb package: " + err.Error()}
	}
	ver := info.Version
	if _, v, ok := strings.Cut(ver, ":"); ok { // filenames drop the epoch
		ver = v
	}
	p := path.Join("pool/main", poolPrefix(info.Package), info.Package, fmt.Sprintf("%s_%s_%s.deb", info.Package, ver, info.Architecture))
	if err := a.checkRedeploy(repo, p); err != nil {
		a.blobs.Discard(tmp)
		return nil, err
	}
	attrs, _ := json.Marshal(info)
	x, err := a.storeTemp(repo.Name, p, tmp, sha, size, assetMeta{
		ContentType: "application/vnd.debian.binary-package", Component: info.Package, Version: info.Version,
		Uploader: u.Username, Attrs: string(attrs),
	})
	if err != nil {
		return nil, err
	}
	a.audit(u, "apt.upload", repo.Name+"/"+p)
	return x, nil
}

func poolPrefix(pkg string) string {
	if strings.HasPrefix(pkg, "lib") && len(pkg) > 3 {
		return pkg[:4]
	}
	return pkg[:1]
}

// readDeb extracts the control file from a .deb (an ar archive holding control.tar.{gz,xz,zst}).
func readDeb(file string) (*debInfo, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m5, s1 := md5.New(), sha1.New()
	if _, err := io.Copy(io.MultiWriter(m5, s1), f); err != nil {
		return nil, err
	}
	f.Seek(0, io.SeekStart)
	br := bufio.NewReader(f)
	magic := make([]byte, 8)
	if _, err := io.ReadFull(br, magic); err != nil || string(magic) != "!<arch>\n" {
		return nil, errors.New("missing ar header")
	}
	for {
		hdr := make([]byte, 60)
		if _, err := io.ReadFull(br, hdr); err != nil {
			return nil, errors.New("control archive not found")
		}
		name := strings.TrimRight(strings.TrimSpace(string(hdr[:16])), "/")
		size, err := strconv.ParseInt(strings.TrimSpace(string(hdr[48:58])), 10, 64)
		if err != nil {
			return nil, errors.New("corrupt ar member header")
		}
		if strings.HasPrefix(name, "control.tar") {
			ctrl, err := readControlTar(name, io.LimitReader(br, size))
			if err != nil {
				return nil, err
			}
			info := parseControl(ctrl)
			if info.Package == "" || info.Version == "" || info.Architecture == "" {
				return nil, errors.New("control file lacks Package, Version or Architecture")
			}
			info.MD5, info.SHA1 = hex.EncodeToString(m5.Sum(nil)), hex.EncodeToString(s1.Sum(nil))
			return info, nil
		}
		if _, err := br.Discard(int(size + size%2)); err != nil { // members are 2-byte aligned
			return nil, errors.New("truncated archive")
		}
	}
}

func readControlTar(name string, r io.Reader) (string, error) {
	switch {
	case strings.HasSuffix(name, ".gz"):
		gz, err := gzip.NewReader(r)
		if err != nil {
			return "", err
		}
		r = gz
	case strings.HasSuffix(name, ".xz"):
		x, err := xz.NewReader(r)
		if err != nil {
			return "", err
		}
		r = x
	case strings.HasSuffix(name, ".zst"):
		z, err := zstd.NewReader(r)
		if err != nil {
			return "", err
		}
		defer z.Close()
		r = z
	}
	t := tar.NewReader(r)
	for {
		h, err := t.Next()
		if err != nil {
			return "", errors.New("control file missing from control archive")
		}
		if path.Clean(h.Name) == "control" {
			b, err := io.ReadAll(io.LimitReader(t, 1<<20))
			return string(b), err
		}
	}
}

func parseControl(ctrl string) *debInfo {
	fields := map[string]string{}
	for _, line := range strings.Split(ctrl, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue // continuation of a multi-line field
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			fields[k] = strings.TrimSpace(v)
		}
	}
	return &debInfo{
		Package: fields["Package"], Version: fields["Version"], Architecture: fields["Architecture"],
		Maintainer: fields["Maintainer"], Description: fields["Description"], Depends: fields["Depends"],
		Control: strings.TrimRight(ctrl, "\n"),
	}
}

// aptReindex regenerates Packages and the signed Release/InRelease for a hosted repo.
func (a *App) aptReindex(repo *Repo) error {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	debs, err := a.assetsUnder(repo.Name, "pool/")
	if err != nil {
		return err
	}
	type entry struct{ arch, text string }
	archSet := map[string]bool{"amd64": true, "arm64": true}
	var entries []entry
	for _, x := range debs {
		var info debInfo
		if json.Unmarshal(x.Attrs, &info) != nil || info.Control == "" {
			continue
		}
		entries = append(entries, entry{info.Architecture, fmt.Sprintf("%s\nFilename: %s\nSize: %d\nMD5sum: %s\nSHA1: %s\nSHA256: %s\n",
			info.Control, x.Path, x.Size, info.MD5, info.SHA1, x.Blob)})
		if info.Architecture != "all" {
			archSet[info.Architecture] = true
		}
	}
	archs := make([]string, 0, len(archSet))
	for k := range archSet {
		archs = append(archs, k)
	}
	sort.Strings(archs)

	type file struct {
		rel  string
		data []byte
	}
	var files []file
	for _, arch := range archs {
		var buf bytes.Buffer
		for _, e := range entries {
			if e.arch == arch || e.arch == "all" {
				buf.WriteString(e.text + "\n")
			}
		}
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		zw.Write(buf.Bytes())
		zw.Close()
		dir := "main/binary-" + arch + "/"
		files = append(files, file{dir + "Packages", buf.Bytes()}, file{dir + "Packages.gz", gz.Bytes()})
	}

	dist := repo.Distribution
	if dist == "" {
		dist = "stable"
	}
	var rel strings.Builder
	fmt.Fprintf(&rel, "Origin: RepoMan\nLabel: %s\nSuite: %s\nCodename: %s\nDate: %s\nArchitectures: %s\nComponents: main\nDescription: %s\n",
		repo.Name, dist, dist, time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 UTC"), strings.Join(archs, " "), repo.Name+" (RepoMan)")
	for _, algo := range []struct {
		name string
		sum  func([]byte) string
	}{
		{"MD5Sum", func(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }},
		{"SHA1", func(b []byte) string { s := sha1.Sum(b); return hex.EncodeToString(s[:]) }},
		{"SHA256", func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }},
	} {
		rel.WriteString(algo.name + ":\n")
		for _, f := range files {
			fmt.Fprintf(&rel, " %s %d %s\n", algo.sum(f.data), len(f.data), f.rel)
		}
	}
	release := []byte(rel.String())
	inRelease, err := a.clearsign(release)
	if err != nil {
		return err
	}
	sig, err := a.detachSign(release)
	if err != nil {
		return err
	}
	files = append(files, file{"Release", release}, file{"InRelease", inRelease}, file{"Release.gpg", sig})
	for _, f := range files {
		ct := "text/plain; charset=utf-8"
		if strings.HasSuffix(f.rel, ".gz") {
			ct = "application/gzip"
		}
		if _, err := a.putAsset(repo.Name, "dists/"+dist+"/"+f.rel, bytes.NewReader(f.data), assetMeta{ContentType: ct, Uploader: "system"}); err != nil {
			return err
		}
	}
	return nil
}

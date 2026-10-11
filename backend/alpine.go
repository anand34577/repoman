package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Alpine (apk). A proxy mirrors an Alpine mirror as plain files (APKINDEX.tar.gz is refreshed after the
// metadata TTL). A hosted repository takes .apk files in any folder (typically <branch>/<repo>/<arch>/) and
// generates that folder's APKINDEX.tar.gz on request from the index lines kept in each package's Attrs.
// ponytail: the generated index is unsigned, so clients add --allow-untrusted; sign with abuild-sign if that matters.

var apkFileRe = regexp.MustCompile(`^(.+)-([0-9][^-]*-r[0-9]+)\.apk$`)

func alpineComponent(file string) (comp, ver string) {
	if m := apkFileRe.FindStringSubmatch(file); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

func (a *App) alpineHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	if repo.Type == "hosted" {
		switch {
		case (r.Method == http.MethodPut || r.Method == http.MethodPost) && strings.HasSuffix(p, ".apk"):
			if _, err := a.alpineStore(repo, p, r.Body, u); err != nil {
				resolveError(w, err)
				return
			}
			w.WriteHeader(http.StatusCreated)
			return
		case (r.Method == http.MethodGet || r.Method == http.MethodHead) && path.Base(p) == "APKINDEX.tar.gz":
			data, err := a.alpineIndex(repo, path.Dir(p))
			if err != nil {
				resolveError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			if r.Method == http.MethodGet {
				w.Write(data)
			}
			return
		}
	}
	a.rawHandler(w, r, repo, p, u)
}

// alpineStore saves an .apk in the folder of target under its canonical name <pkgname>-<pkgver>.apk
// (curl --upload-file appends whatever the local file is called).
func (a *App) alpineStore(repo *Repo, target string, body io.Reader, u *User) (*Asset, error) {
	dir := path.Dir(target)
	if dir == "." || !validPath(target) {
		return nil, httpErr{400, "upload into a folder, e.g. v3.20/main/x86_64/pkg.apk"}
	}
	tmp, sha, size, err := a.blobs.Write(body)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(tmp) // ponytail: read whole; stream the segments if packages in the hundreds of MB matter
	if err != nil {
		a.blobs.Discard(tmp)
		return nil, err
	}
	entry, name, ver, err := apkEntry(data, size)
	if err != nil {
		a.blobs.Discard(tmp)
		return nil, httpErr{400, "not a valid .apk package: " + err.Error()}
	}
	p := path.Join(dir, name+"-"+ver+".apk")
	if err := a.checkRedeploy(repo, p); err != nil {
		a.blobs.Discard(tmp)
		return nil, err
	}
	attrs, _ := json.Marshal(map[string]string{"entry": entry})
	x, err := a.storeTemp(repo.Name, p, tmp, sha, size, assetMeta{ContentType: "application/vnd.alpine.apk", Component: name, Version: ver, Uploader: u.Username, Attrs: string(attrs)})
	if err == nil {
		a.audit(u, "alpine.upload", repo.Name+"/"+p)
	}
	return x, err
}

// apkEntry builds the APKINDEX record of a package. An .apk is concatenated gzip streams: an optional
// signature, the control tar holding .PKGINFO, then the files. The record's C: is sha1 of the control stream.
func apkEntry(data []byte, size int64) (entry, name, ver string, err error) {
	rd := bytes.NewReader(data)
	for rd.Len() > 0 {
		start := len(data) - rd.Len()
		gz, err := gzip.NewReader(rd)
		if err != nil {
			return "", "", "", errors.New("not gzip data")
		}
		gz.Multistream(false)
		seg, err := io.ReadAll(gz)
		if err != nil {
			return "", "", "", err
		}
		tr := tar.NewReader(bytes.NewReader(seg))
		for {
			h, err := tr.Next()
			if err != nil {
				break
			}
			if h.Name != ".PKGINFO" {
				continue
			}
			info, _ := io.ReadAll(io.LimitReader(tr, 1<<20))
			sum := sha1.Sum(data[start : len(data)-rd.Len()])
			return apkRecord(string(info), "Q1"+base64.StdEncoding.EncodeToString(sum[:]), size)
		}
	}
	return "", "", "", errors.New(".PKGINFO not found")
}

func apkRecord(pkginfo, checksum string, size int64) (entry, name, ver string, err error) {
	kv := map[string][]string{}
	for _, l := range strings.Split(pkginfo, "\n") {
		if k, v, ok := strings.Cut(l, " = "); ok && !strings.HasPrefix(l, "#") {
			kv[k] = append(kv[k], v)
		}
	}
	one := func(k string) string { return strings.Join(kv[k], " ") }
	name, ver = one("pkgname"), one("pkgver")
	if name == "" || ver == "" {
		return "", "", "", errors.New(".PKGINFO has no pkgname or pkgver")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "C:%s\nP:%s\nV:%s\nS:%d\n", checksum, name, ver, size)
	for _, f := range [][2]string{{"A", "arch"}, {"I", "size"}, {"T", "pkgdesc"}, {"U", "url"}, {"L", "license"}, {"o", "origin"},
		{"m", "maintainer"}, {"t", "builddate"}, {"c", "commit"}, {"D", "depend"}, {"p", "provides"}, {"i", "install_if"}, {"k", "provider_priority"}} {
		if v := one(f[1]); v != "" {
			fmt.Fprintf(&b, "%s:%s\n", f[0], v)
		}
	}
	return b.String(), name, ver, nil
}

// alpineIndex renders dir/APKINDEX.tar.gz from the packages stored directly in dir.
func (a *App) alpineIndex(repo *Repo, dir string) ([]byte, error) {
	prefix := ""
	if dir != "." {
		prefix = dir + "/"
	}
	list, err := a.assetsUnder(repo.Name, prefix)
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	var idx strings.Builder
	for _, x := range list {
		var at struct{ Entry string }
		if path.Dir(x.Path) == dir && json.Unmarshal(x.Attrs, &at) == nil && at.Entry != "" {
			idx.WriteString(at.Entry + "\n")
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range [][2]string{{"DESCRIPTION", repo.Name}, {"APKINDEX", idx.String()}} {
		tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o644, Size: int64(len(f[1]))})
		tw.Write([]byte(f[1]))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes(), nil
}

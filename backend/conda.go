package main

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// Conda channels (conda, mamba, micromamba, pixi). Layout: "<subdir>/<file>" where subdir is
// noarch, linux-64, osx-arm64, win-64… Proxies pass paths straight through to the channel URL
// (e.g. https://conda.anaconda.org/conda-forge). Hosted repositories generate
// <subdir>/repodata.json on request from the info/index.json stored at upload time.

var (
	condaSubdirRe = regexp.MustCompile(`^(noarch|[a-z0-9]+-[a-z0-9_]+)$`)
	condaFileRe   = regexp.MustCompile(`^([a-z0-9_][a-z0-9_.+-]*)-([^-/]+)-([^-/]+)\.(conda|tar\.bz2)$`)
)

func (a *App) condaHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !validPath(p) {
			http.NotFound(w, r)
			return
		}
		if dir, file, ok := strings.Cut(p, "/"); ok && repo.Type == "hosted" && condaSubdirRe.MatchString(dir) &&
			(file == "repodata.json" || file == "current_repodata.json") {
			a.condaRepodata(w, repo, dir)
			return
		}
		x, err := a.resolve(r.Context(), repo, p, r.Header, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		a.serveAsset(w, r, x)
	case http.MethodPut, http.MethodPost:
		if _, err := a.ingestConda(repo, r.Body, p, u); err != nil {
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

// ingestConda stores a .conda or .tar.bz2 package at <subdir>/<name>-<version>-<build>.<ext>,
// taken from its info/index.json. A bare file name (curl --upload-file to the root appends the local
// name) is ignored; an explicit "<subdir>/<file>" must match the metadata, so a mislabeled file can't
// shadow another package.
func (a *App) ingestConda(repo *Repo, body io.Reader, target string, u *User) (*Asset, error) {
	tmp, sha, size, err := a.blobs.Write(body)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Asset, error) { a.blobs.Discard(tmp); return nil, err }
	idx, ext, err := readCondaIndex(tmp)
	if err != nil {
		return fail(httpErr{400, "not a valid conda package (.conda or .tar.bz2): " + err.Error()})
	}
	name, ver, build, subdir := idx.str("name"), idx.str("version"), idx.str("build"), idx.str("subdir")
	file := name + "-" + ver + "-" + build + ext
	if !condaFileRe.MatchString(file) || !condaSubdirRe.MatchString(subdir) {
		return fail(httpErr{400, fmt.Sprintf("info/index.json has an invalid name, version, build or subdir (%q, %q, %q, %q)", name, ver, build, subdir)})
	}
	p := subdir + "/" + file
	if strings.Contains(target, "/") && target != p {
		return fail(httpErr{400, "the file name or directory does not match the package metadata; expected " + p})
	}
	if err := a.checkRedeploy(repo, p); err != nil {
		return fail(err)
	}
	f, err := os.Open(tmp)
	if err != nil {
		return fail(err)
	}
	h := md5.New() // conda clients still read md5 from repodata
	_, err = io.Copy(h, f)
	f.Close()
	if err != nil {
		return fail(err)
	}
	idx["md5"] = hex.EncodeToString(h.Sum(nil))
	attrs, _ := json.Marshal(idx)
	x, err := a.storeTemp(repo.Name, p, tmp, sha, size, assetMeta{
		ContentType: "application/octet-stream", Component: name, Version: ver, Uploader: u.Username, Attrs: string(attrs),
	})
	if err != nil {
		return nil, err
	}
	a.audit(u, "conda.upload", repo.Name+"/"+p)
	return x, nil
}

type condaIndex map[string]any

func (c condaIndex) str(k string) string { s, _ := c[k].(string); return s }

// readCondaIndex extracts info/index.json from a package. .tar.bz2 is a bzip2 tarball;
// .conda is a zip holding info-<pkg>.tar.zst next to the payload.
func readCondaIndex(file string) (condaIndex, string, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return nil, "", errors.New("file too short")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, "", err
	}
	switch {
	case string(magic[:3]) == "BZh":
		idx, err := condaIndexFromTar(bzip2.NewReader(f))
		return idx, ".tar.bz2", err
	case string(magic) == "PK\x03\x04":
		st, err := f.Stat()
		if err != nil {
			return nil, "", err
		}
		zr, err := zip.NewReader(f, st.Size())
		if err != nil {
			return nil, "", err
		}
		for _, zf := range zr.File {
			if !strings.HasPrefix(zf.Name, "info-") || !strings.HasSuffix(zf.Name, ".tar.zst") {
				continue
			}
			rc, err := zf.Open()
			if err != nil {
				return nil, "", err
			}
			defer rc.Close()
			// bounded memory: a crafted frame must not make the server allocate gigabytes
			z, err := zstd.NewReader(rc, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20))
			if err != nil {
				return nil, "", err
			}
			defer z.Close()
			idx, err := condaIndexFromTar(z)
			return idx, ".conda", err
		}
		return nil, "", errors.New("info-*.tar.zst missing from .conda archive")
	}
	return nil, "", errors.New("unrecognized file type")
}

func condaIndexFromTar(r io.Reader) (condaIndex, error) {
	t := tar.NewReader(io.LimitReader(r, 4<<30)) // ponytail: scans until index.json; 4 GiB cap stops decompression bombs
	for {
		h, err := t.Next()
		if err != nil {
			return nil, errors.New("info/index.json missing")
		}
		if path.Clean(strings.TrimPrefix(h.Name, "./")) == "info/index.json" {
			var idx condaIndex
			if err := json.NewDecoder(io.LimitReader(t, 1<<20)).Decode(&idx); err != nil {
				return nil, errors.New("info/index.json is not valid JSON")
			}
			return idx, nil
		}
	}
}

// condaRepodata renders repodata.json for one subdir of a hosted repository. Every valid
// subdir answers (possibly empty): conda refuses a channel whose noarch/repodata.json is missing.
// ponytail: built per request from the database; store it on upload if channels get large.
func (a *App) condaRepodata(w http.ResponseWriter, repo *Repo, subdir string) {
	list, err := a.assetsUnder(repo.Name, subdir+"/")
	if err != nil {
		resolveError(w, err)
		return
	}
	tarBz2, conda := map[string]condaIndex{}, map[string]condaIndex{}
	for _, x := range list {
		var e condaIndex
		if json.Unmarshal(x.Attrs, &e) != nil || e == nil {
			continue
		}
		e["sha256"], e["size"] = x.Blob, x.Size
		if file := path.Base(x.Path); strings.HasSuffix(file, ".conda") {
			conda[file] = e
		} else {
			tarBz2[file] = e
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(map[string]any{
		"info": map[string]string{"subdir": subdir}, "packages": tarBz2, "packages.conda": conda,
		"removed": []string{}, "repodata_version": 1,
	})
}

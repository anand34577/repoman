package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// Blobs is a content-addressed file store: identical content is stored once.
type Blobs struct{ dir string }

func (b *Blobs) path(sha string) string {
	return filepath.Join(b.dir, sha[:2], sha[2:4], sha)
}

// Write streams r to a temp file, returning its path and sha256. Call Commit or Discard after.
// Extra writers receive the bytes as they arrive (used to serve a download while it is being cached).
func (b *Blobs) Write(r io.Reader, extra ...io.Writer) (tmp, sha string, size int64, err error) {
	f, err := os.CreateTemp(b.dir, "tmp-*")
	if err != nil {
		return "", "", 0, err
	}
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(append([]io.Writer{f, h}, extra...)...), r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", "", 0, err
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), size, nil
}

// Create opens a temp file in the store for a download that is written (and served) while it grows; finish it with storeTemp.
func (b *Blobs) Create() (*os.File, error) { return os.CreateTemp(b.dir, "tmp-*") }

func (b *Blobs) Commit(tmp, sha string) error {
	dst := b.path(sha)
	if _, err := os.Stat(dst); err == nil {
		return os.Remove(tmp) // already stored
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func (b *Blobs) Discard(tmp string)                { os.Remove(tmp) }
func (b *Blobs) Open(sha string) (*os.File, error) { return os.Open(b.path(sha)) }
func (b *Blobs) Delete(sha string)                 { os.Remove(b.path(sha)) }

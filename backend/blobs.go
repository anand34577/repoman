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
func (b *Blobs) Write(r io.Reader) (tmp, sha string, size int64, err error) {
	f, err := os.CreateTemp(b.dir, "tmp-*")
	if err != nil {
		return "", "", 0, err
	}
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(f, h), r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", "", 0, err
	}
	return f.Name(), hex.EncodeToString(h.Sum(nil)), size, nil
}

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

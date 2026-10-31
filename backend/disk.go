package main

import "net/http"

// checkDisk refuses a download that would leave less than diskReserve free on the data disk (size < 0: unknown).
// A download of unknown size that still fills the disk fails at the write and is cleaned up.
func (a *App) checkDisk(size int64) error {
	free, err := diskFree(a.dataDir)
	if err != nil {
		return nil // cannot tell: do not block caching
	}
	if free-max(size, 0) < a.diskReserve {
		return httpErr{http.StatusInsufficientStorage, "not enough free disk space on the server to cache this file"}
	}
	return nil
}

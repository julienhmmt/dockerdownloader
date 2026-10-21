//go:build !unix

package pipeline

import "errors"

// freeBytes is unsupported on non-unix platforms; it reports an error so the
// preflight check is skipped rather than blocking the download. Returning 0
// instead would read as a full disk.
func freeBytes(string) (uint64, error) {
	return 0, errors.New("free disk space check unsupported on this platform")
}

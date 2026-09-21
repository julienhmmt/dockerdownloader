package pipeline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// CacheEntry describes one image tarball cached under the work dir.
type CacheEntry struct {
	// TarPath is the path to the image tarball.
	TarPath string
	// Name is a readable label derived from the tarball filename. The original
	// reference is not stored on disk, so path separators show as underscores.
	Name string
	// Digest is the registry manifest digest recorded beside the tarball, or
	// "" when no .digest sidecar exists.
	Digest string
	// Size is the tarball size in bytes.
	Size int64
}

// cacheHashSuffix matches the disambiguating "-<8 hex>" tail tarballName
// appends, so it can be dropped from a display label.
var cacheHashSuffix = regexp.MustCompile(`-[0-9a-f]{8}$`)

// CacheDir returns the directory holding cached image tarballs for the
// configured work dir, or "" when no persistent work dir is set. A temporary
// work dir is removed on exit, so there is nothing to purge from it.
func (p *Pipeline) CacheDir() string {
	if p.cfg.WorkDir == "" {
		return ""
	}
	return filepath.Join(p.cfg.WorkDir, "images")
}

// ListCache returns the image tarballs cached under the work dir, sorted by
// name. A missing cache directory is not an error: it just means nothing has
// been downloaded yet. It returns an empty slice when no persistent work dir is
// configured.
func (p *Pipeline) ListCache() ([]CacheEntry, error) {
	dir := p.CacheDir()
	if dir == "" {
		return nil, nil
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cache dir: %w", err)
	}
	entries := make([]CacheEntry, 0, len(items))
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".tar") {
			continue
		}
		info, err := item.Info()
		if err != nil {
			continue
		}
		tarPath := filepath.Join(dir, item.Name())
		entries = append(entries, CacheEntry{
			TarPath: tarPath,
			Name:    cacheDisplayName(item.Name()),
			Digest:  readSidecar(digestSidecarPath(tarPath)),
			Size:    info.Size(),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// PurgeCache deletes the given cache entries — each tarball plus its .digest
// and .sha256 sidecars — and returns how many tarballs were removed and the
// bytes freed. A missing file is not an error; every entry is attempted so one
// failure cannot stop the rest, and the failures are joined.
func (p *Pipeline) PurgeCache(entries []CacheEntry) (removed int, freed int64, err error) {
	var errs []error
	for _, entry := range entries {
		size := entry.Size
		if info, statErr := os.Stat(entry.TarPath); statErr == nil {
			size = info.Size()
		}
		if rmErr := os.Remove(entry.TarPath); rmErr != nil {
			if !os.IsNotExist(rmErr) {
				errs = append(errs, fmt.Errorf("remove %s: %w", entry.TarPath, rmErr))
				continue
			}
		} else {
			removed++
			freed += size
		}
		// Sidecars are best-effort: a missing one means the entry was already
		// partly purged, and an undeletable one must not keep the tarball.
		_ = os.Remove(digestSidecarPath(entry.TarPath))
		_ = os.Remove(contentHashSidecarPath(entry.TarPath))
	}
	return removed, freed, errors.Join(errs...)
}

// readSidecar returns the trimmed contents of path, or "" when it is missing.
func readSidecar(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// cacheDisplayName turns a cached tarball filename into a readable label by
// dropping the ".tar" suffix and the "-<hash>" tail tarballName appends.
func cacheDisplayName(fileName string) string {
	return cacheHashSuffix.ReplaceAllString(strings.TrimSuffix(fileName, ".tar"), "")
}

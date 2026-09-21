package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
)

// cachePipeline builds a Pipeline whose work dir is workDir.
func cachePipeline(workDir string) *Pipeline {
	cfg := config.Default()
	cfg.WorkDir = workDir
	return New(cfg, log.Discard())
}

// seedCache writes a minimal tarball named for ref plus both resume sidecars,
// returning the tarball path.
func seedCache(t *testing.T, workDir, ref, digest string) string {
	t.Helper()
	dir := filepath.Join(workDir, "images")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	tarPath := filepath.Join(dir, tarballName(ref))
	writeMinimalTar(t, tarPath)
	require.NoError(t, os.WriteFile(digestSidecarPath(tarPath), []byte(digest), 0o600))
	require.NoError(t, os.WriteFile(contentHashSidecarPath(tarPath), []byte("sum"), 0o600))
	return tarPath
}

func TestCacheDir_EmptyWorkDir(t *testing.T) {
	assert.Empty(t, cachePipeline("").CacheDir())
}

func TestCacheDir_UsesWorkDirImages(t *testing.T) {
	work := t.TempDir()
	assert.Equal(t, filepath.Join(work, "images"), cachePipeline(work).CacheDir())
}

func TestListCache_NoWorkDir(t *testing.T) {
	entries, err := cachePipeline("").ListCache()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestListCache_MissingDirIsEmpty(t *testing.T) {
	entries, err := cachePipeline(t.TempDir()).ListCache()
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestListCache_ListsTarballsOnly(t *testing.T) {
	work := t.TempDir()
	tarPath := seedCache(t, work, "quay.io/argoproj/argocd:v2.9.3", "sha256:deadbeef\n")
	dir := filepath.Join(work, "images")
	// Noise that must not be listed as an image.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))

	entries, err := cachePipeline(work).ListCache()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, tarPath, entries[0].TarPath)
	assert.Equal(t, "quay.io_argoproj_argocd_v2.9.3", entries[0].Name)
	assert.Equal(t, "sha256:deadbeef", entries[0].Digest)
	assert.Positive(t, entries[0].Size)
}

func TestListCache_SortedByName(t *testing.T) {
	work := t.TempDir()
	seedCache(t, work, "zebra:1", "sha256:z")
	seedCache(t, work, "alpha:1", "sha256:a")

	entries, err := cachePipeline(work).ListCache()
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "alpha_1", entries[0].Name)
	assert.Equal(t, "zebra_1", entries[1].Name)
}

func TestListCache_MissingDigestSidecarIsEmpty(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, "images")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writeMinimalTar(t, filepath.Join(dir, tarballName("nginx:1.27")))

	entries, err := cachePipeline(work).ListCache()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Empty(t, entries[0].Digest)
}

func TestPurgeCache_RemovesTarballAndSidecars(t *testing.T) {
	work := t.TempDir()
	tarPath := seedCache(t, work, "nginx:1.27", "sha256:x")
	pl := cachePipeline(work)
	entries, err := pl.ListCache()
	require.NoError(t, err)
	require.Len(t, entries, 1)

	removed, freed, err := pl.PurgeCache(entries)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.Positive(t, freed)
	for _, path := range []string{tarPath, digestSidecarPath(tarPath), contentHashSidecarPath(tarPath)} {
		_, statErr := os.Stat(path)
		assert.True(t, os.IsNotExist(statErr), "%s should be gone", path)
	}
}

func TestPurgeCache_LeavesUnselectedEntries(t *testing.T) {
	work := t.TempDir()
	keep := seedCache(t, work, "keep:1", "sha256:k")
	seedCache(t, work, "drop:1", "sha256:d")
	pl := cachePipeline(work)
	entries, err := pl.ListCache()
	require.NoError(t, err)
	require.Len(t, entries, 2)

	_, _, err = pl.PurgeCache(entries[:1])
	require.NoError(t, err)
	_, statErr := os.Stat(keep)
	assert.NoError(t, statErr, "an unselected entry must survive")
	remaining, err := pl.ListCache()
	require.NoError(t, err)
	assert.Len(t, remaining, 1)
}

func TestPurgeCache_MissingFileIsNotAnError(t *testing.T) {
	removed, freed, err := cachePipeline(t.TempDir()).PurgeCache([]CacheEntry{
		{TarPath: filepath.Join(t.TempDir(), "gone.tar"), Size: 5},
	})
	require.NoError(t, err)
	assert.Zero(t, removed)
	assert.Zero(t, freed)
}

func TestCacheDisplayName(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{"strips hash suffix", "nginx_1.27-1a2b3c4d.tar", "nginx_1.27"},
		{"keeps non-hex tail", "nginx_1.27-v2.9.3.tar", "nginx_1.27-v2.9.3"},
		{"keeps short tail", "nginx_1.27-abc.tar", "nginx_1.27-abc"},
		{"no suffix", "nginx.tar", "nginx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cacheDisplayName(tt.file))
		})
	}
}

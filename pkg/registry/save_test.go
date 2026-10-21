package registry_test

import (
	"context"
	"io"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/log"
	"github.com/julienhmmt/dockerdownloader/pkg/registry"
)

// startRegistry serves an in-memory registry on 127.0.0.1 (plain HTTP). While
// failBlobs is set, every blob GET answers 404, so a pull resolves its
// manifest and then fails mid-write.
func startRegistry(t *testing.T, failBlobs *atomic.Bool) string {
	t.Helper()
	inner := ggcrregistry.New(ggcrregistry.Logger(stdlog.New(io.Discard, "", 0)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failBlobs.Load() && r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blobs/") {
			http.Error(w, "blob gone", http.StatusNotFound)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://")
}

// tempLeftovers returns any half-written temp files in dir.
func tempLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	require.NoError(t, err)
	return matches
}

func TestSave_WritesTarballAndDigest(t *testing.T) {
	var failBlobs atomic.Bool
	host := startRegistry(t, &failBlobs)
	img, err := random.Image(1024, 1)
	require.NoError(t, err)
	require.NoError(t, crane.Push(img, host+"/test/app:1"))
	want, err := img.Digest()
	require.NoError(t, err)

	dir := t.TempDir()
	destPath := filepath.Join(dir, "app.tar")
	p := registry.NewPuller("linux/amd64", "", false, log.Discard())
	digest, err := p.Save(context.Background(), host+"/test/app:1", "rgy.local/test/app:1", destPath, nil)

	require.NoError(t, err)
	assert.Equal(t, want.String(), digest)
	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
	assert.Empty(t, tempLeftovers(t, dir))
}

func TestSave_FailedWriteKeepsExistingTarball(t *testing.T) {
	var failBlobs atomic.Bool
	host := startRegistry(t, &failBlobs)
	img, err := random.Image(1024, 1)
	require.NoError(t, err)
	require.NoError(t, crane.Push(img, host+"/test/app:1"))
	failBlobs.Store(true)

	dir := t.TempDir()
	destPath := filepath.Join(dir, "app.tar")
	require.NoError(t, os.WriteFile(destPath, []byte("previous good tarball"), 0o644))
	p := registry.NewPuller("linux/amd64", "", false, log.Discard())
	_, err = p.Save(context.Background(), host+"/test/app:1", "rgy.local/test/app:1", destPath, nil)

	require.Error(t, err)
	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, "previous good tarball", string(data), "a failed re-pull must not truncate or remove the cached tarball")
	assert.Empty(t, tempLeftovers(t, dir))
}

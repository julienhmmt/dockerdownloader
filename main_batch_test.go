package main

import (
	"io"
	stdlog "log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	ggcrregistry "github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startTestRegistry serves an in-memory registry over plain HTTP on 127.0.0.1
// (go-containerregistry uses http for loopback hosts) and returns its host.
func startTestRegistry(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(ggcrregistry.New(ggcrregistry.Logger(stdlog.New(io.Discard, "", 0))))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://")
}

func pushRandomImage(t *testing.T, ref string) {
	t.Helper()
	img, err := random.Image(1024, 1)
	require.NoError(t, err)
	require.NoError(t, crane.Push(img, ref))
}

// batchFixture writes an empty config and an image list, returning the batch
// args that keep every side effect (temp work dir, output) under t.TempDir().
func batchFixture(t *testing.T, refs ...string) (args []string, tempParent, outputDir string) {
	t.Helper()
	// applyProxyEnv would route the loopback test registry through a host proxy.
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("{}\n"), 0o600))
	listPath := filepath.Join(dir, "images.yaml")
	list := "images:\n  - " + strings.Join(refs, "\n  - ") + "\n"
	require.NoError(t, os.WriteFile(listPath, []byte(list), 0o600))
	tempParent = filepath.Join(dir, "tmp")
	outputDir = filepath.Join(dir, "out")
	require.NoError(t, os.MkdirAll(tempParent, 0o755))
	args = []string{
		"-config", configPath, "-temp-dir", tempParent, "-output", outputDir,
		"-retries", "0", "-min-free-mb", "0", "-log-file", filepath.Join(dir, "dd.log"),
		listPath,
	}
	return args, tempParent, outputDir
}

func assertNoTempWorkDir(t *testing.T, tempParent string) {
	t.Helper()
	left, err := filepath.Glob(filepath.Join(tempParent, "dockerdownloader-*"))
	require.NoError(t, err)
	assert.Empty(t, left, "temporary work dir must be removed on every exit path")
}

func TestRunBatch_PartialFailureExitsNonZeroWithBundle(t *testing.T) {
	host := startTestRegistry(t)
	pushRandomImage(t, host+"/test/ok:1")
	args, tempParent, outputDir := batchFixture(t, host+"/test/ok:1", host+"/test/missing:1")

	assert.Equal(t, exitPartial, runBatch(args))

	bundles, err := filepath.Glob(filepath.Join(outputDir, "*-bundle.tar.*"))
	require.NoError(t, err)
	assert.Len(t, bundles, 1, "the images that did download are still bundled")
	assertNoTempWorkDir(t, tempParent)
}

func TestRunBatch_AllImagesOKExitsZero(t *testing.T) {
	host := startTestRegistry(t)
	pushRandomImage(t, host+"/test/ok:1")
	args, tempParent, _ := batchFixture(t, host+"/test/ok:1")

	assert.Equal(t, exitOK, runBatch(args))
	assertNoTempWorkDir(t, tempParent)
}

func TestRunBatch_AllImagesFailRemovesTempWorkDir(t *testing.T) {
	host := startTestRegistry(t)
	args, tempParent, outputDir := batchFixture(t, host+"/test/missing:1")

	assert.Equal(t, exitError, runBatch(args))

	bundles, err := filepath.Glob(filepath.Join(outputDir, "*-bundle.tar.*"))
	require.NoError(t, err)
	assert.Empty(t, bundles)
	assertNoTempWorkDir(t, tempParent)
}

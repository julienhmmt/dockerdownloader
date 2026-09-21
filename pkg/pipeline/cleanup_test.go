package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/bundle"
	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
)

func TestNewSession_PersistentWorkDir(t *testing.T) {
	work := filepath.Join(t.TempDir(), "workdir")
	cfg := config.Default()
	cfg.WorkDir = work
	pl := New(cfg, log.Discard())

	session, err := pl.NewSession()
	require.NoError(t, err)
	assert.Equal(t, work, session.WorkDir)
	assert.False(t, session.TempWorkDir, "a configured work dir must outlive the run for -resume")
	info, err := os.Stat(work)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestNewSession_TempWorkDir(t *testing.T) {
	cfg := config.Default()
	cfg.TempDir = t.TempDir()
	pl := New(cfg, log.Discard())

	session, err := pl.NewSession()
	require.NoError(t, err)
	assert.True(t, session.TempWorkDir)
	_, err = os.Stat(session.WorkDir)
	require.NoError(t, err)
}

func TestBundle_PreservesResumeTarballs(t *testing.T) {
	// A persistent work dir keeps its tarballs after bundling so -resume can
	// rebuild the archive without re-pulling every image.
	work := t.TempDir()
	out := t.TempDir()
	imagesDir := filepath.Join(work, "images")
	require.NoError(t, os.MkdirAll(imagesDir, 0o755))
	cached := filepath.Join(imagesDir, "cached.tar")
	writeMinimalTar(t, cached)

	cfg := config.Default()
	cfg.WorkDir = work
	cfg.OutputDir = out
	pl := New(cfg, log.Discard())

	_, err := pl.Bundle([]bundle.ImageEntry{{
		TarPath: cached, SourceRef: "nginx:1.27", DestRef: "rgy.local/docker.io/library/nginx:1.27",
	}})
	require.NoError(t, err)

	_, err = os.Stat(cached)
	assert.NoError(t, err, "work dir tarballs must survive bundling for -resume")
}

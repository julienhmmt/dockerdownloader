package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
)

// press applies a bound key handler and returns the concrete updated model.
func press(t *testing.T, handler func(tea.KeyPressMsg) (tea.Model, tea.Cmd), key string) model {
	t.Helper()
	got, _ := handler(keyPress(key))
	updated, ok := got.(model)
	require.True(t, ok, "update must return the concrete model")
	return updated
}

// newPurgeModel returns a model with the given persistent work dir, sized to a
// known terminal so framed views render deterministically.
func newPurgeModel(t *testing.T, workDir string) model {
	t.Helper()
	cfg := config.Default()
	cfg.WorkDir = workDir
	m := newModel(cfg, log.Discard(), testImages())
	m.width, m.height = 100, 40
	return m
}

// seedTUIcache writes a fake cached tarball (name should end in .tar) and
// returns its path.
func seedTUIcache(t *testing.T, workDir, name string) string {
	t.Helper()
	dir := filepath.Join(workDir, "images")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("tar"), 0o600))
	return path
}

func TestOpenPurge_NoWorkDirStaysOnReview(t *testing.T) {
	m := newPurgeModel(t, "")
	m.openPurge()
	assert.Equal(t, stateReview, m.state)
	assert.Contains(t, m.status, "No work_dir")
}

func TestOpenPurge_EmptyCacheStaysOnReview(t *testing.T) {
	m := newPurgeModel(t, t.TempDir())
	m.openPurge()
	assert.Equal(t, stateReview, m.state)
	assert.Contains(t, m.status, "No cached images")
}

func TestOpenPurge_ListsCache(t *testing.T) {
	work := t.TempDir()
	seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	seedTUIcache(t, work, "redis_7-5e6f7a8b.tar")

	m := newPurgeModel(t, work)
	m.openPurge()
	assert.Equal(t, statePurge, m.state)
	require.Len(t, m.cacheEntries, 2)
	assert.Equal(t, "nginx_1.27", m.cacheEntries[0].Name)
	assert.Empty(t, m.status)
}

func TestHandlePurgeKey_SpaceTogglesSelection(t *testing.T) {
	work := t.TempDir()
	seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	m := newPurgeModel(t, work)
	m.openPurge()

	m = press(t, m.handlePurgeKey, "space")
	assert.Equal(t, 1, m.countCacheSelected())
	m = press(t, m.handlePurgeKey, "space")
	assert.Equal(t, 0, m.countCacheSelected())
}

func TestHandlePurgeKey_AAllSelectsThenClears(t *testing.T) {
	work := t.TempDir()
	seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	seedTUIcache(t, work, "redis_7-5e6f7a8b.tar")
	m := newPurgeModel(t, work)
	m.openPurge()

	m = press(t, m.handlePurgeKey, "a")
	assert.Equal(t, 2, m.countCacheSelected())
	m = press(t, m.handlePurgeKey, "a")
	assert.Equal(t, 0, m.countCacheSelected())
}

func TestHandlePurgeKey_EnterWithNoneSelectedSetsStatus(t *testing.T) {
	work := t.TempDir()
	seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	m := newPurgeModel(t, work)
	m.openPurge()

	m = press(t, m.handlePurgeKey, "enter")
	assert.Equal(t, statePurge, m.state)
	assert.Contains(t, m.status, "Select at least one image")
}

func TestHandlePurgeKey_EnterGoesToConfirm(t *testing.T) {
	work := t.TempDir()
	seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	m := newPurgeModel(t, work)
	m.openPurge()
	m = press(t, m.handlePurgeKey, "space")

	m = press(t, m.handlePurgeKey, "enter")
	assert.Equal(t, statePurgeConfirm, m.state)
}

func TestHandlePurgeKey_EscReturnsToReview(t *testing.T) {
	work := t.TempDir()
	seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	m := newPurgeModel(t, work)
	m.openPurge()

	m = press(t, m.handlePurgeKey, "esc")
	assert.Equal(t, stateReview, m.state)
}

func TestHandlePurgeConfirmKey_ConfirmDeletesSelected(t *testing.T) {
	work := t.TempDir()
	path := seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	require.NoError(t, os.WriteFile(path+".digest", []byte("sha256:abc"), 0o600))

	m := newPurgeModel(t, work)
	m.openPurge()
	m = press(t, m.handlePurgeKey, "space")
	m = press(t, m.handlePurgeKey, "enter")

	m = press(t, m.handlePurgeConfirmKey, "y")
	assert.Equal(t, stateReview, m.state, "an emptied cache returns to review")
	assert.Contains(t, m.status, "Purged 1 image(s)")
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "the tarball should be gone")
	_, statErr = os.Stat(path + ".digest")
	assert.True(t, os.IsNotExist(statErr), "the sidecar should be gone")
}

func TestHandlePurgeConfirmKey_CancelKeepsFiles(t *testing.T) {
	work := t.TempDir()
	path := seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	m := newPurgeModel(t, work)
	m.openPurge()
	m = press(t, m.handlePurgeKey, "space")
	m = press(t, m.handlePurgeKey, "enter")

	m = press(t, m.handlePurgeConfirmKey, "n")
	assert.Equal(t, statePurge, m.state)
	_, statErr := os.Stat(path)
	assert.NoError(t, statErr, "cancel must not delete anything")
}

func TestViewPurge_RendersEntryAndHelp(t *testing.T) {
	work := t.TempDir()
	seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	m := newPurgeModel(t, work)
	m.openPurge()

	view := m.viewPurge()
	assert.Contains(t, view, "Cached images")
	assert.Contains(t, view, "nginx_1.27")
	// Help keys/labels are styled separately, so assert on tokens.
	assert.Contains(t, view, "enter")
	assert.Contains(t, view, "purge")
}

func TestViewPurgeConfirm_ShowsCountAndSize(t *testing.T) {
	work := t.TempDir()
	seedTUIcache(t, work, "nginx_1.27-1a2b3c4d.tar")
	m := newPurgeModel(t, work)
	m.openPurge()
	m = press(t, m.handlePurgeKey, "space")

	view := m.viewPurgeConfirm()
	assert.Contains(t, view, "Confirm purge")
	assert.Contains(t, view, "Delete 1 cached image(s)")
	assert.Contains(t, view, "confirm")
	assert.Contains(t, view, "cancel")
}

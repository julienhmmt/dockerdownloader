package tui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/bundle"
	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
	"github.com/julienhmmt/dockerdownloader/pkg/pipeline"
)

func keyPress(key string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 0, Text: key}
}

func TestNewModel_StartsOnReviewWithTheLoadedList(t *testing.T) {
	m := newModel(config.Default(), log.Discard(), testImages())
	assert.Equal(t, stateReview, m.state)
	assert.Len(t, m.reviewImages, 2)
	assert.Empty(t, m.session.WorkDir, "no work dir until a download starts")
}

func TestDoneMsg_MovesToDone(t *testing.T) {
	m := newModel(config.Default(), log.Discard(), testImages())
	m.state = stateBundling
	got, _ := m.Update(doneMsg{bundlePath: "/tmp/images-bundle.tar.gz"})
	m2 := got.(model)
	assert.Equal(t, stateDone, m2.state)
	assert.Equal(t, "/tmp/images-bundle.tar.gz", m2.bundlePath)
	assert.Empty(t, m2.errStep)
}

func TestDownloadDoneMsg_StaleWhileReviewIgnored(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	m.entries = nil
	got, cmd := m.Update(downloadDoneMsg{
		entries: []bundle.ImageEntry{{SourceRef: "x:1"}},
	})
	m2 := got.(model)
	assert.Equal(t, stateReview, m2.state)
	assert.Empty(t, m2.entries)
	assert.Nil(t, cmd)
}

func TestDownloadDoneMsg_NoFailuresGoesBundling(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloading
	got, cmd := m.Update(downloadDoneMsg{entries: []bundle.ImageEntry{{SourceRef: "x:1"}}})
	m2 := got.(model)
	assert.Equal(t, stateBundling, m2.state)
	assert.Equal(t, "bundle", m2.errStep)
	assert.Len(t, m2.entries, 1)
	assert.NotNil(t, cmd)
}

func TestDownloadDoneMsg_FailuresGoToDownloadReview(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloading
	got, cmd := m.Update(downloadDoneMsg{
		entries:  []bundle.ImageEntry{{SourceRef: "x:1"}},
		failures: []pipeline.ImageFailure{{Ref: "y:1", Err: errors.New("boom")}},
	})
	m2 := got.(model)
	assert.Equal(t, stateDownloadReview, m2.state)
	assert.Nil(t, cmd)
	assert.Len(t, m2.failures, 1)
}

func TestErrMsg_IgnoredWhenNotBusy(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	got, _ := m.Update(errMsg{err: errors.New("boom")})
	assert.Equal(t, stateReview, got.(model).state)
}

func TestErrMsg_AcceptedWhileDownloading(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloading
	got, _ := m.Update(errMsg{err: errors.New("boom")})
	m2 := got.(model)
	assert.Equal(t, stateError, m2.state)
	assert.EqualError(t, m2.err, "boom")
}

func TestHandleBusyKey_EscDownloadingReturnsReview(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloading
	m.errStep = "download"
	got, _ := m.handleBusyKey(keyPress("esc"))
	m2 := got.(model)
	assert.Equal(t, stateReview, m2.state)
	assert.Empty(t, m2.errStep)
}

func TestHandleBusyKey_EscDownloadingWithEntriesGoesDownloadReview(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloading
	m.entries = []bundle.ImageEntry{{SourceRef: "x:1"}}
	got, _ := m.handleBusyKey(keyPress("esc"))
	assert.Equal(t, stateDownloadReview, got.(model).state)
}

func TestHandleBusyKey_EscBundlingIsNoop(t *testing.T) {
	m := newTestModel()
	m.state = stateBundling
	got, _ := m.handleBusyKey(keyPress("esc"))
	assert.Equal(t, stateBundling, got.(model).state)
}

func TestCancelDownload_StaleDoneDoesNotAffectNextRun(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloading
	stale := m.activity
	got, _ := m.handleKey(keyPress("esc"))
	m2 := got.(model)
	require.Equal(t, stateReview, m2.state)
	require.NotEqual(t, stale, m2.activity, "cancel must give the next run a fresh channel")

	// The cancelled goroutine's terminal send goes to the channel it still
	// holds; it must be invisible to the next run's pump.
	stale <- downloadDoneMsg{entries: []bundle.ImageEntry{{SourceRef: "stale:1"}}}

	m2.state = stateDownloading
	m2.entries = nil
	select {
	case msg := <-m2.activity:
		t.Fatalf("new run observed a stale message: %#v", msg)
	default:
	}

	// A genuine completion for the new run is still processed.
	got3, _ := m2.Update(downloadDoneMsg{entries: []bundle.ImageEntry{{SourceRef: "fresh:1"}}})
	m3 := got3.(model)
	assert.Equal(t, stateBundling, m3.state)
	require.Len(t, m3.entries, 1)
	assert.Equal(t, "fresh:1", m3.entries[0].SourceRef)
}

func TestCancelDownload_StaleProgressIgnored(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloading
	m.downCurrent, m.downTotal = 0, 5
	stale := m.activity
	got, _ := m.handleKey(keyPress("esc"))
	m2 := got.(model)
	stale <- progressMsg{current: 3, total: 5, ref: "stale:1"}

	// The stale message sits on the abandoned channel, so the new run's
	// counters are untouched and its pump has nothing queued.
	assert.Equal(t, 0, m2.downCurrent)
	select {
	case msg := <-m2.activity:
		t.Fatalf("new run observed a stale progress message: %#v", msg)
	default:
	}

	// The pump still drains the current channel and advances counters.
	m2.state = stateDownloading
	got2, cmd := m2.Update(progressMsg{current: 1, total: 5, ref: "fresh:1"})
	m3 := got2.(model)
	assert.Equal(t, 1, m3.downCurrent)
	assert.NotNil(t, cmd, "pump must continue after a genuine progress message")
}

func TestHandleReviewKey_SpaceTogglesSelection(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	require.True(t, m.reviewImages[0].Selected)
	got, _ := m.handleReviewKey(keyPress("space"))
	m2 := got.(model)
	assert.False(t, m2.reviewImages[0].Selected)
	assert.Equal(t, 0, m2.countSelected())
}

func TestHandleReviewKey_DeleteRemovesEntry(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	got, _ := m.handleReviewKey(keyPress("d"))
	m2 := got.(model)
	require.Len(t, m2.reviewImages, 1)
	assert.Equal(t, "ghcr.io/dexidp/dex:v2.37.0", m2.reviewImages[0].Ref)
}

func TestHandleReviewKey_EnterWithNoImagesSetsStatus(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	m.reviewImages = nil
	got, cmd := m.handleReviewKey(keyPress("enter"))
	m2 := got.(model)
	assert.Equal(t, stateReview, m2.state)
	assert.Nil(t, cmd)
	assert.Contains(t, m2.status, "No images")
}

func TestHandleReviewKey_EnterWithNoneSelectedSetsStatus(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	for i := range m.reviewImages {
		m.reviewImages[i].Selected = false
	}
	got, cmd := m.handleReviewKey(keyPress("enter"))
	m2 := got.(model)
	assert.Equal(t, stateReview, m2.state)
	assert.Nil(t, cmd)
	assert.Contains(t, m2.status, "Select at least one image")
}

func TestHandleReviewKey_EnterStartsDownloadWithSelectedRefs(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	m.cfg.WorkDir = t.TempDir()
	got, cmd := m.handleReviewKey(keyPress("enter"))
	m2 := got.(model)
	assert.Equal(t, stateDownloading, m2.state)
	assert.Equal(t, 1, m2.downTotal, "only the selected image downloads")
	assert.NotEmpty(t, m2.session.WorkDir)
	assert.NotNil(t, cmd)
}

func TestHandleReviewKey_AOpensAddImage(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	got, _ := m.handleReviewKey(keyPress("a"))
	assert.Equal(t, stateAddImage, got.(model).state)
}

func TestHandleAddImageKey_AddsValidRef(t *testing.T) {
	m := newTestModel()
	m.state = stateAddImage
	m.addInput.SetValue("nginx:1.27")
	got, _ := m.handleAddImageKey(keyPress("enter"))
	m2 := got.(model)
	assert.Equal(t, stateReview, m2.state)
	require.Len(t, m2.reviewImages, 3)
	assert.Equal(t, "nginx:1.27", m2.reviewImages[2].Ref)
	assert.True(t, m2.reviewImages[2].Selected)
	assert.Equal(t, 2, m2.reviewCursor)
	assert.Empty(t, m2.status)
}

func TestHandleAddImageKey_RejectsInvalidRef(t *testing.T) {
	m := newTestModel()
	m.state = stateAddImage
	m.addInput.SetValue("not an image")
	got, _ := m.handleAddImageKey(keyPress("enter"))
	m2 := got.(model)
	assert.Equal(t, stateAddImage, m2.state, "stay on the input so the user can fix it")
	assert.Len(t, m2.reviewImages, 2)
	assert.Contains(t, m2.status, "Invalid image reference")
}

func TestHandleAddImageKey_RejectsDuplicateDestination(t *testing.T) {
	m := newTestModel()
	m.state = stateAddImage
	// "docker.io/library/nginx:1.27" and "nginx:1.27" mirror to the same dest.
	m.cfg.RegistryPrefix = "rgy01.domain.local"
	m.reviewImages = append(m.reviewImages, imagelist.Image{Ref: "nginx:1.27", Selected: true})
	m.addInput.SetValue("docker.io/library/nginx:1.27")
	got, _ := m.handleAddImageKey(keyPress("enter"))
	m2 := got.(model)
	assert.Equal(t, stateAddImage, m2.state)
	assert.Len(t, m2.reviewImages, 3, "the colliding entry must not be added")
	assert.Contains(t, m2.status, "both retag to")
}

func TestHandleAddImageKey_EscReturnsToReview(t *testing.T) {
	m := newTestModel()
	m.state = stateAddImage
	got, _ := m.handleAddImageKey(keyPress("esc"))
	assert.Equal(t, stateReview, got.(model).state)
}

func TestHandleDownloadReviewKey_RetryRestartsDownload(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloadReview
	m.failures = []pipeline.ImageFailure{{Ref: "redis:7", Err: errors.New("boom")}}
	got, cmd := m.handleDownloadReviewKey(keyPress("r"))
	m2 := got.(model)
	assert.Equal(t, stateDownloading, m2.state)
	assert.Equal(t, 1, m2.downTotal)
	assert.Empty(t, m2.failures)
	assert.NotNil(t, cmd)
}

func TestHandleDownloadReviewKey_ContinueBundlesDownloaded(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloadReview
	m.entries = []bundle.ImageEntry{{SourceRef: "x:1"}}
	m.failures = []pipeline.ImageFailure{{Ref: "redis:7", Err: errors.New("boom")}}
	got, cmd := m.handleDownloadReviewKey(keyPress("c"))
	m2 := got.(model)
	assert.Equal(t, stateBundling, m2.state)
	assert.Equal(t, "bundle", m2.errStep)
	assert.NotNil(t, cmd)
}

func TestHandleDownloadReviewKey_ContinueNoopWithoutEntries(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloadReview
	got, cmd := m.handleDownloadReviewKey(keyPress("c"))
	assert.Equal(t, stateDownloadReview, got.(model).state)
	assert.Nil(t, cmd)
}

func TestHandleDownloadReviewKey_NavigatesFailures(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloadReview
	m.failures = []pipeline.ImageFailure{{Ref: "a:1"}, {Ref: "b:2"}, {Ref: "c:3"}}

	got, _ := m.handleDownloadReviewKey(keyPress("j"))
	assert.Equal(t, 1, got.(model).failCursor)
	got, _ = got.(model).handleDownloadReviewKey(keyPress("G"))
	assert.Equal(t, 2, got.(model).failCursor)
	got, _ = got.(model).handleDownloadReviewKey(keyPress("g"))
	assert.Equal(t, 0, got.(model).failCursor)
}

func TestSelectedRefs_OnlySelected(t *testing.T) {
	imgs := []imagelist.Image{
		{Ref: "a:1", Selected: true},
		{Ref: "b:2", Selected: false},
		{Ref: "c:3", Selected: true},
	}
	assert.Equal(t, []string{"a:1", "c:3"}, selectedRefs(imgs))
}

func TestFailureRefs(t *testing.T) {
	failures := []pipeline.ImageFailure{{Ref: "a:1"}, {Ref: "b:2"}}
	assert.Equal(t, []string{"a:1", "b:2"}, failureRefs(failures))
}

func TestStartSession_ReusesExistingSession(t *testing.T) {
	m := newTestModel()
	m.cfg.WorkDir = t.TempDir()
	first, err := m.startSession()
	require.NoError(t, err)
	m.session = first
	second, err := m.startSession()
	require.NoError(t, err)
	assert.Equal(t, first.WorkDir, second.WorkDir)
}

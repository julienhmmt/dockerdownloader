package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/julienhmmt/dockerdownloader/pkg/pipeline"
)

func TestRemoveTempWorkDir_TempDirRemoved(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "img.tar"), []byte("x"), 0o644))
	removeTempWorkDir(pipeline.Session{WorkDir: dir, TempWorkDir: true})
	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "temp work dir should be removed")
}

func TestRemoveTempWorkDir_PersistentDirPreserved(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "img.tar"), []byte("x"), 0o644))
	removeTempWorkDir(pipeline.Session{WorkDir: dir})
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir(), "persistent work dir should be preserved")
	_, err = os.Stat(filepath.Join(dir, "img.tar"))
	assert.NoError(t, err, "contents of persistent work dir should be preserved")
}

func TestRemoveTempWorkDir_EmptySessionNoop(_ *testing.T) {
	removeTempWorkDir(pipeline.Session{})
}

func TestShutdown_WaitsForDownloadBeforeRemovingWorkDir(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	pass := newDownloadPass()
	require.True(t, pass.start(), "the pass's command started")
	m := model{
		ctx: ctx, cancel: cancel, download: pass,
		session: pipeline.Session{WorkDir: dir, TempWorkDir: true},
	}
	// Stand-in for a download worker: it stops only once cancelled, and checks
	// the work dir still exists at that moment.
	dirSeen := make(chan bool, 1)
	go func() {
		<-ctx.Done()
		_, err := os.Stat(dir)
		dirSeen <- err == nil
		close(pass.done)
	}()

	m.shutdown()

	assert.True(t, <-dirSeen, "work dir must outlive the in-flight download")
	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "temp work dir must be removed once the download stopped")
}

func TestSendOrCancel_ReturnsWhenContextCancelled(t *testing.T) {
	activity := make(chan tea.Msg) // unbuffered, never drained
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		sendOrCancel(ctx, activity, downloadDoneMsg{})
		close(done)
	}()
	select {
	case <-done: // good: fell through ctx.Done() instead of blocking
	case <-time.After(time.Second):
		t.Fatal("sendOrCancel blocked on a cancelled context")
	}
}

func TestSendOrCancel_DeliversWhenDrained(t *testing.T) {
	activity := make(chan tea.Msg, 1)
	ctx := context.Background()
	sendOrCancel(ctx, activity, downloadDoneMsg{})
	select {
	case got := <-activity:
		_, ok := got.(downloadDoneMsg)
		assert.True(t, ok)
	default:
		t.Fatal("message was not delivered")
	}
}

func TestShutdown_DoesNotHangWhenPassCommandNeverRan(t *testing.T) {
	m := newTestModel()
	m.session = pipeline.Session{WorkDir: t.TempDir(), TempWorkDir: true}
	// Enter queues the download command; quitting before Bubble Tea runs it
	// means its goroutine never starts and never closes done.
	got, cmd := m.startDownloadPass([]string{"x:1"})
	require.NotNil(t, cmd)
	queued := got.(model)

	finished := make(chan struct{})
	go func() {
		queued.shutdown()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown hung waiting on a pass whose command never ran")
	}
	assert.False(t, queued.download.start(), "a pass claimed by shutdown must not start afterwards")
}

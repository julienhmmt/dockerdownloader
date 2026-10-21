package tui

import (
	"context"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/julienhmmt/dockerdownloader/pkg/bundle"
	"github.com/julienhmmt/dockerdownloader/pkg/pipeline"
)

// downloadPass tracks one download pass so other code can wait for its
// workers to stop writing into the work dir. Bubble Tea may exit without ever
// running a queued command, so the command and shutdown race to claim the
// pass: shutdown only waits on done when the command really started.
type downloadPass struct {
	claim sync.Once
	ran   bool
	// done is closed once Download has returned.
	done chan struct{}
}

func newDownloadPass() *downloadPass {
	return &downloadPass{done: make(chan struct{})}
}

// start claims the pass for its command; it reports false when shutdown
// claimed it first, in which case the command must not start.
func (p *downloadPass) start() bool {
	p.claim.Do(func() { p.ran = true })
	return p.ran
}

// wait blocks until the pass's workers have stopped, or returns at once when
// its command never started (and now never will).
func (p *downloadPass) wait() {
	p.claim.Do(func() {})
	if p.ran {
		<-p.done
	}
}

// running reports whether the pass's Download has not returned yet.
func (p *downloadPass) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// downloadCmd saves the given image refs in a goroutine, streaming per-image
// progress over the model's channel. It returns immediately; progress is
// consumed by waitForActivity, and the pass ends with a downloadDoneMsg.
func downloadCmd(ctx context.Context, pl *pipeline.Pipeline, session pipeline.Session, refs []string, activity chan tea.Msg, pass *downloadPass) tea.Cmd {
	return func() tea.Msg {
		if !pass.start() {
			return nil
		}
		go func() {
			entries, failures, err := pl.Download(ctx, session, refs,
				func(current, total int, ref string, perr error) {
					// Non-blocking: per-image progress is advisory. A slow terminal
					// could otherwise stall worker goroutines holding concurrency slots.
					select {
					case activity <- progressMsg{current: current, total: total, ref: ref, err: perr}:
					default:
					}
				},
				func(ref string, written, total int64) {
					// Non-blocking: byte updates are advisory, so drop them rather
					// than stall the puller if the UI is busy draining the channel.
					select {
					case activity <- byteProgressMsg{ref: ref, written: written, total: total}:
					default:
					}
				})
			close(pass.done)
			// Terminal sends yield to context cancellation: on quit, ctx is
			// cancelled and nobody drains activity, so a blocking send would
			// leak this goroutine and hold open registry connections.
			if err != nil {
				sendOrCancel(ctx, activity, errMsg{err})
				return
			}
			sendOrCancel(ctx, activity, downloadDoneMsg{entries: entries, failures: failures})
		}()
		return waitForActivity(activity)()
	}
}

// sendOrCancel performs a terminal send that yields to context cancellation,
// so a quit (which cancels ctx and stops draining activity) cannot leak the
// download goroutine on its final send.
func sendOrCancel(ctx context.Context, activity chan tea.Msg, msg tea.Msg) {
	select {
	case activity <- msg:
	case <-ctx.Done():
	}
}

// bundleCmd assembles the downloaded entries into the final archive.
func bundleCmd(pl *pipeline.Pipeline, entries []bundle.ImageEntry) tea.Cmd {
	return func() tea.Msg {
		path, err := pl.Bundle(entries)
		if err != nil {
			return errMsg{err}
		}
		return doneMsg{bundlePath: path}
	}
}

// waitForActivity blocks on the activity channel and returns the next message.
func waitForActivity(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

// removeTempWorkDir removes the work directory when it is a temporary one
// created by NewSession. A user-configured persistent work dir (cfg.WorkDir) is
// preserved so -resume can reuse its tarballs across runs.
func removeTempWorkDir(session pipeline.Session) {
	if session.WorkDir != "" && session.TempWorkDir {
		_ = os.RemoveAll(session.WorkDir)
	}
}

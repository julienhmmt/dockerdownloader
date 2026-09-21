package tui

import (
	"github.com/julienhmmt/dockerdownloader/pkg/bundle"
	"github.com/julienhmmt/dockerdownloader/pkg/pipeline"
)

// progressMsg reports the download status of a single image.
type progressMsg struct {
	current int
	total   int
	ref     string
	err     error
}

// byteProgressMsg reports byte-level progress for the image currently pulling.
type byteProgressMsg struct {
	ref     string
	written int64
	total   int64
}

// downloadDoneMsg carries the result of a download pass: the entries that
// succeeded and the references that failed.
type downloadDoneMsg struct {
	entries  []bundle.ImageEntry
	failures []pipeline.ImageFailure
}

// doneMsg signals that the bundle has been written.
type doneMsg struct {
	bundlePath string
}

// errMsg carries a failure from any asynchronous step.
type errMsg struct {
	err error
}

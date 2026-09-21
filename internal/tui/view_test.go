package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/julienhmmt/dockerdownloader/pkg/bundle"
	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
	"github.com/julienhmmt/dockerdownloader/pkg/pipeline"
)

// testImages is a small, deterministic review list.
func testImages() []imagelist.Image {
	return []imagelist.Image{
		{Ref: "quay.io/argoproj/argocd:v2.9.3", Selected: true},
		{Ref: "ghcr.io/dexidp/dex:v2.37.0", Selected: false},
	}
}

// newTestModel returns a model sized to a known terminal so framed views
// render deterministically.
func newTestModel() model {
	m := newModel(config.Default(), log.Discard(), testImages())
	m.width, m.height = 100, 40
	return m
}

func TestViewRendersEveryScreen(t *testing.T) {
	base := newTestModel()
	base.entries = []bundle.ImageEntry{{SourceRef: "nginx:1.27"}}
	base.failures = []pipeline.ImageFailure{{Ref: "redis:7.0", Err: errors.New("not found")}}
	base.bundlePath = "archives/images-bundle.tar.gz"
	base.err = errors.New("boom")

	states := []state{
		stateReview, stateAddImage, stateDownloading, stateDownloadReview,
		stateBundling, stateDone, stateError, stateThemeMenu,
	}
	for _, s := range states {
		m := base
		m.state = s
		out := m.render()
		assert.NotEmpty(t, strings.TrimSpace(out), "state %d rendered empty", s)
	}
}

func TestViewReviewShowsSelectionAndCursor(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	m.reviewImages = []imagelist.Image{
		{Ref: "quay.io/argoproj/argocd:v2.9.3", Selected: true},
	}
	out := m.render()
	assert.Contains(t, out, "1 selected of 1")
	assert.Contains(t, out, "quay.io/argoproj/argocd:v2.9.3")
}

func TestViewReview_HoverSpansFullRow(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	m.reviewImages = []imagelist.Image{
		{Ref: "nginx:1.27", Selected: true},
		{Ref: "redis:7", Selected: false},
	}
	m.reviewCursor = 0
	out := m.render()
	// Full-width hover is applied via lipgloss Width; the row must contain the
	// focused ref and render wider than the bare text alone.
	assert.Contains(t, out, "nginx:1.27")
	assert.Equal(t, m.reviewRowWidth(), m.reviewFrameInnerWidth())
	assert.Greater(t, m.reviewRowWidth(), len("▸ [x] nginx:1.27"))
}

func TestViewReview_EmptyListCopy(t *testing.T) {
	m := newTestModel()
	m.state = stateReview
	m.reviewImages = nil
	m.cfg.ImagesFile = "images.yaml"
	out := m.render()
	assert.Contains(t, out, "No images in the list")
	assert.Contains(t, out, "images.yaml")
}

func TestRenderHelp_HighlightsKeys(t *testing.T) {
	m := newTestModel()
	out := m.renderHelp("enter select · q quit")
	// Keys are rendered in the accent (selected) style, labels in secondary.
	// Both tokens must be present; the "·" separator must also be present.
	assert.Contains(t, out, "enter")
	assert.Contains(t, out, "select")
	assert.Contains(t, out, "q")
	assert.Contains(t, out, "quit")
	assert.Contains(t, out, "·")
}

func TestRenderHelp_SingleToken(t *testing.T) {
	m := newTestModel()
	out := m.renderHelp("q")
	assert.Contains(t, out, "q")
}

func TestFrameCentersWhenSized(t *testing.T) {
	m := newTestModel()
	out := m.frame("hello")
	// A rounded border draws corner glyphs around the content.
	assert.Contains(t, out, "╭")
	assert.Contains(t, out, "╰")
	assert.Contains(t, out, "hello")
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{5 * 1024 * 1024 * 1024, "5.0 GiB"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, humanBytes(c.in), "humanBytes(%d)", c.in)
	}
}

// stripANSI removes SGR escape sequences so a styled bar can be compared by
// its visible glyphs. The bar fills with "━" and leaves a "─" track.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestMiniBar_Determinate(t *testing.T) {
	m := newModel(config.Default(), log.Discard(), nil)
	bar := stripANSI(m.miniBar(5, 10, 10))
	assert.Equal(t, "[━━━━━─────]", bar)
}

func TestMiniBar_Full(t *testing.T) {
	m := newModel(config.Default(), log.Discard(), nil)
	bar := stripANSI(m.miniBar(10, 10, 10))
	assert.Equal(t, "[━━━━━━━━━━]", bar)
}

func TestMiniBar_OverFillClamps(t *testing.T) {
	m := newModel(config.Default(), log.Discard(), nil)
	bar := stripANSI(m.miniBar(20, 10, 10))
	assert.Equal(t, "[━━━━━━━━━━]", bar)
}

func TestMiniBar_Indeterminate(t *testing.T) {
	m := newModel(config.Default(), log.Discard(), nil)
	// 1 cell per MiB; 5 MiB written -> 5 cells of a 10-cell bar.
	bar := stripANSI(m.miniBar(5*1024*1024, 0, 10))
	assert.Equal(t, "[━━━━━─────]", bar)
}

func TestByteLabel_WithTotal(t *testing.T) {
	m := newModel(config.Default(), log.Discard(), nil)
	label := m.byteLabel(1024, 2048)
	assert.Contains(t, label, "1.0 KiB")
	assert.Contains(t, label, "2.0 KiB")
}

func TestByteLabel_WithoutTotal(t *testing.T) {
	m := newModel(config.Default(), log.Discard(), nil)
	label := m.byteLabel(1024, 0)
	assert.Contains(t, label, "1.0 KiB")
}

func TestViewDone_ShowsVerifyAndImageCount(t *testing.T) {
	m := newTestModel()
	m.state = stateDone
	m.bundlePath = "archives/images-bundle.tar.gz"
	m.entries = make([]bundle.ImageEntry, 3)
	out := m.render()
	assert.Contains(t, out, "verify")
	assert.Contains(t, out, "3 images")
	assert.Contains(t, out, "load.sh")
	assert.Contains(t, out, "tar xzf archives/images-bundle.tar.gz")
}

func TestViewDone_ShowsFailuresWhenSomeImagesSkipped(t *testing.T) {
	m := newTestModel()
	m.state = stateDone
	m.bundlePath = "archives/images-bundle.tar.gz"
	m.entries = make([]bundle.ImageEntry, 2)
	m.failures = []pipeline.ImageFailure{{Ref: "redis:7", Err: errors.New("not found")}}
	out := m.render()
	assert.Contains(t, out, "2 images")
	assert.Contains(t, out, "1 failed")
}

func TestBundleExtractCmd(t *testing.T) {
	assert.Equal(t, "tar xzf a.tar.gz", bundleExtractCmd("a.tar.gz"))
	assert.Equal(t, "tar --zstd -xf a.tar.zst", bundleExtractCmd("a.tar.zst"))
	// Bare .zst (not our bundle suffix) must not pick the zstd codec.
	assert.Equal(t, "tar xzf foo.zst", bundleExtractCmd("foo.zst"))
}

func TestViewError_ShowsStepLabel(t *testing.T) {
	m := newTestModel()
	m.state = stateError
	m.errStep = "bundle"
	m.err = errors.New("disk full")
	out := m.render()
	assert.Contains(t, out, "bundle")
	assert.Contains(t, out, "disk full")
}

func TestViewDownloading_ShowsEscCancel(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloading
	out := m.render()
	assert.Contains(t, out, "esc")
	assert.Contains(t, out, "cancel")
}

func TestViewAddImage_ShowsPrompt(t *testing.T) {
	m := newTestModel()
	m.state = stateAddImage
	out := m.render()
	assert.Contains(t, out, "Add an image reference")
	assert.Contains(t, out, "enter")
}

func TestViewDownloadReview_ShowsFailuresAndRetry(t *testing.T) {
	m := newTestModel()
	m.state = stateDownloadReview
	m.entries = []bundle.ImageEntry{{SourceRef: "nginx:1.27"}}
	m.failures = []pipeline.ImageFailure{{Ref: "redis:7", Err: errors.New("manifest unknown")}}
	out := m.render()
	assert.Contains(t, out, "redis:7")
	assert.Contains(t, out, "manifest unknown")
	assert.Contains(t, out, "retry")
	assert.Contains(t, out, "continue with 1")
}

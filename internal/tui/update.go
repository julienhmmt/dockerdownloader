package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
	"github.com/julienhmmt/dockerdownloader/pkg/pipeline"
)

// Update is the Bubble Tea message dispatcher.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = typed.Width, typed.Height
		m.progress.SetWidth(max(0, min(typed.Width-4, 60)))
		return m, nil
	case tea.BackgroundColorMsg:
		// Always record host darkness; only auto applies it immediately.
		// (Named themes keep their fixed palette, but detection is stored so
		// switching back to auto does not use a stale preview value.)
		m.detectedIsDark = typed.IsDark()
		if config.ThemeIsForced(m.cfg.Theme) {
			return m, nil
		}
		m.applyTheme()
		m.bgKnown = true
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(typed)
	case progressMsg:
		if m.state != stateDownloading {
			return m, nil
		}
		m.downCurrent, m.downTotal = typed.current, typed.total
		delete(m.imageProgress, typed.ref)
		return m, waitForActivity(m.activity)
	case byteProgressMsg:
		if m.state != stateDownloading {
			return m, nil
		}
		m.imageProgress[typed.ref] = imageProgress{written: typed.written, total: typed.total}
		return m, waitForActivity(m.activity)
	case downloadDoneMsg:
		// Ignore completion after the user cancelled download and left the screen.
		if m.state != stateDownloading {
			return m, nil
		}
		m.entries = append(m.entries, typed.entries...)
		m.failures = typed.failures
		m.errStep = ""
		if len(typed.failures) == 0 {
			m.state = stateBundling
			m.errStep = "bundle"
			return m, tea.Batch(m.spinner.Tick, bundleCmd(m.pipeline, m.entries))
		}
		m.state = stateDownloadReview
		return m, nil
	case doneMsg:
		if m.state != stateBundling {
			return m, nil
		}
		m.bundlePath = typed.bundlePath
		m.state = stateDone
		m.errStep = ""
		return m, nil
	case errMsg:
		// Drop errors that arrive after cancel already left the busy state that
		// produced them (e.g. context.Canceled from a cancelled download).
		switch m.state {
		case stateDownloading, stateBundling:
			// Accept — still on the busy screen that issued the work.
		default:
			return m, nil
		}
		m.err = typed.err
		m.state = stateError
		return m, nil
	}
	return m.updateComponents(msg)
}

// updateComponents forwards a message to the focused sub-component and keeps
// the spinner animating.
func (m model) updateComponents(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	cmds := make([]tea.Cmd, 0, 2)
	m.spinner, cmd = m.spinner.Update(msg)
	cmds = append(cmds, cmd)
	if m.state == stateAddImage {
		m.addInput, cmd = m.addInput.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

// handleKey routes key presses based on the active screen.
func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.cancel()
		return m, tea.Batch(cleanupCmd(m.session), tea.Quit)
	}
	// Global theme menu — ctrl+t so it works even while typing in the
	// add-image field and never collides with single-letter bindings.
	// Re-pressing while already in the menu is a no-op (use Esc to cancel).
	if msg.String() == "ctrl+t" {
		if m.state == stateThemeMenu {
			return m, nil
		}
		// Do not interrupt in-flight busy work with a palette picker.
		if m.state == stateDownloading || m.state == stateBundling {
			return m, nil
		}
		m.openThemeMenu()
		return m, nil
	}
	switch m.state {
	case stateReview:
		return m.handleReviewKey(msg)
	case stateAddImage:
		return m.handleAddImageKey(msg)
	case stateDownloadReview:
		return m.handleDownloadReviewKey(msg)
	case stateThemeMenu:
		return m.handleThemeMenuKey(msg)
	case stateDownloading, stateBundling:
		return m.handleBusyKey(msg)
	case stateDone, stateError:
		return m.handleEndKey(msg)
	}
	return m.updateComponents(msg)
}

// handleThemeMenuKey navigates the theme picker: j/k or arrows move (with live
// preview), Enter confirms, Esc restores the prior theme.
func (m model) handleThemeMenuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, m.cancelThemeMenu()
	case "enter":
		return m, m.confirmThemeMenu()
	case "up", "k":
		if m.themeMenuCursor > 0 {
			m.themeMenuCursor--
			m.previewThemeAtCursor()
		}
		return m, nil
	case "down", "j":
		if m.themeMenuCursor < len(config.ThemeMenu)-1 {
			m.themeMenuCursor++
			m.previewThemeAtCursor()
		}
		return m, nil
	case "1", "2", "3", "4", "5", "6":
		// Digit jump: 1-based index into ThemeMenu.
		idx := int(msg.String()[0] - '1')
		if idx >= 0 && idx < len(config.ThemeMenu) {
			m.themeMenuCursor = idx
			m.previewThemeAtCursor()
		}
		return m, nil
	}
	return m, nil
}

// handleBusyKey cancels long-running ops without quitting the process.
// Bundle has no context, so Esc is a no-op during bundling; ctrl+c still quits.
func (m model) handleBusyKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "esc" {
		return m.updateComponents(msg)
	}
	switch m.state {
	case stateDownloading:
		m.cancel()
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.imageProgress = map[string]imageProgress{}
		m.errStep = ""
		if len(m.entries) > 0 {
			m.state = stateDownloadReview
		} else {
			m.state = stateReview
		}
		return m, nil
	case stateBundling:
		// Bundle ignores ctx; cancelling mid-write risks a partial archive.
		return m, nil
	}
	return m, nil
}

// handleReviewKey processes the image review checklist.
func (m model) handleReviewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.cancel()
		return m, tea.Batch(cleanupCmd(m.session), tea.Quit)
	case "up", "k":
		if m.reviewCursor > 0 {
			m.reviewCursor--
		}
	case "down", "j":
		if m.reviewCursor < len(m.reviewImages)-1 {
			m.reviewCursor++
		}
	case "pgup", "ctrl+u":
		_, visible := m.reviewViewport()
		m.reviewCursor -= visible
		if m.reviewCursor < 0 {
			m.reviewCursor = 0
		}
	case "pgdown", "ctrl+d":
		_, visible := m.reviewViewport()
		m.reviewCursor += visible
		if n := len(m.reviewImages); n > 0 && m.reviewCursor >= n {
			m.reviewCursor = n - 1
		}
	case "g", "home":
		m.reviewCursor = 0
	case "G", "end":
		if n := len(m.reviewImages); n > 0 {
			m.reviewCursor = n - 1
		}
	case "space":
		if len(m.reviewImages) > 0 {
			m.reviewImages[m.reviewCursor].Selected = !m.reviewImages[m.reviewCursor].Selected
		}
	case "a":
		m.clearStatus()
		m.addInput.SetValue("")
		m.addInput.Focus()
		m.state = stateAddImage
		return m, nil
	case "d":
		if len(m.reviewImages) > 0 {
			m.reviewImages = append(m.reviewImages[:m.reviewCursor], m.reviewImages[m.reviewCursor+1:]...)
			if m.reviewCursor >= len(m.reviewImages) && m.reviewCursor > 0 {
				m.reviewCursor--
			}
		}
	case "enter":
		// A bundle with no images has nothing to load or push, so refuse rather
		// than write a useless archive.
		if len(m.reviewImages) == 0 {
			m.setStatus("No images. Press a to add one, or list them in " + m.cfg.ImagesFile + ".")
			return m, nil
		}
		if m.countSelected() == 0 {
			m.setStatus("Select at least one image (space), or press a to add one.")
			return m, nil
		}
		m.clearStatus()
		session, err := m.startSession()
		if err != nil {
			m.err = err
			m.errStep = "work dir"
			m.state = stateError
			return m, nil
		}
		m.session = session
		refs := selectedRefs(m.reviewImages)
		m.entries, m.failures = nil, nil
		m.imageProgress = map[string]imageProgress{}
		m.state = stateDownloading
		m.downCurrent, m.downTotal = 0, len(refs)
		return m, tea.Batch(m.spinner.Tick, downloadCmd(m.ctx, m.pipeline, m.session, refs, m.activity))
	}
	m.ensureReviewCursorVisible()
	return m, nil
}

// startSession returns the run's work directory, creating it on the first
// download and reusing it for retries.
func (m model) startSession() (pipeline.Session, error) {
	if m.session.WorkDir != "" {
		return m.session, nil
	}
	return m.pipeline.NewSession()
}

// handleDownloadReviewKey processes the post-download failures screen, where the
// user can retry failed images, continue with what downloaded, or abort.
func (m model) handleDownloadReviewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r":
		refs := failureRefs(m.failures)
		m.failures = nil
		m.imageProgress = map[string]imageProgress{}
		m.state = stateDownloading
		m.errStep = "download"
		m.downCurrent, m.downTotal = 0, len(refs)
		return m, tea.Batch(m.spinner.Tick, downloadCmd(m.ctx, m.pipeline, m.session, refs, m.activity))
	case "c":
		if len(m.entries) == 0 {
			return m, nil
		}
		m.state = stateBundling
		m.errStep = "bundle"
		return m, tea.Batch(m.spinner.Tick, bundleCmd(m.pipeline, m.entries))
	case "q", "esc":
		m.cancel()
		return m, tea.Batch(cleanupCmd(m.session), tea.Quit)
	}
	return m, nil
}

// handleAddImageKey processes the add-custom-image input.
func (m model) handleAddImageKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		ref := strings.TrimSpace(m.addInput.Value())
		if ref == "" {
			m.addInput.Blur()
			m.clearStatus()
			m.state = stateReview
			m.ensureReviewCursorVisible()
			return m, nil
		}
		if !imagelist.ValidRef(ref) {
			// Stay on add screen so the user can edit; do not abort review.
			m.setStatus("Invalid image reference.")
			return m, nil
		}
		if err := imagelist.ValidateDest(append(m.reviewImages, imagelist.Image{Ref: ref}), m.cfg.RegistryPrefix); err != nil {
			m.setStatus(err.Error())
			return m, nil
		}
		m.reviewImages = append(m.reviewImages, imagelist.Image{Ref: ref, Selected: true})
		m.reviewCursor = len(m.reviewImages) - 1
		m.addInput.Blur()
		m.clearStatus()
		m.state = stateReview
		m.ensureReviewCursorVisible()
		return m, nil
	case "esc":
		m.addInput.Blur()
		m.clearStatus()
		m.state = stateReview
		m.ensureReviewCursorVisible()
		return m, nil
	}
	return m.updateComponents(msg)
}

// handleEndKey processes the terminal done/error screens.
func (m model) handleEndKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "enter":
		m.cancel()
		return m, tea.Batch(cleanupCmd(m.session), tea.Quit)
	}
	return m, nil
}

// selectedRefs returns the references of the images marked for inclusion.
func selectedRefs(imgs []imagelist.Image) []string {
	refs := make([]string, 0, len(imgs))
	for _, img := range imgs {
		if img.Selected {
			refs = append(refs, img.Ref)
		}
	}
	return refs
}

// failureRefs returns the references of the given failures.
func failureRefs(failures []pipeline.ImageFailure) []string {
	refs := make([]string, 0, len(failures))
	for _, f := range failures {
		refs = append(refs, f.Ref)
	}
	return refs
}

// countSelected returns the number of selected images.
func (m model) countSelected() int {
	count := 0
	for _, img := range m.reviewImages {
		if img.Selected {
			count++
		}
	}
	return count
}

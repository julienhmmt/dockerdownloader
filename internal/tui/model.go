// Package tui implements the terminal user interface for dockerdownloader.
package tui

import (
	"context"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/julienhmmt/dockerdownloader/pkg/bundle"
	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
	"github.com/julienhmmt/dockerdownloader/pkg/pipeline"
)

// state enumerates the screens of the application.
type state int

const (
	stateReview state = iota
	stateAddImage
	stateDownloading
	stateDownloadReview
	stateBundling
	stateDone
	stateError
	stateThemeMenu
	statePurge
	statePurgeConfirm
)

// imageProgress is the byte-level progress of one in-flight image pull.
type imageProgress struct {
	written int64
	total   int64
}

// model is the root Bubble Tea model holding all UI and domain state.
type model struct {
	cfg      config.Config
	pipeline *pipeline.Pipeline
	styles   styleSet
	logger   *log.Logger

	// ctx is cancelled on any quit/reset path so in-flight registry operations
	// abort instead of running to completion after the user leaves. cancel is
	// the matching cancel func; it is safe to call more than once.
	ctx    context.Context
	cancel context.CancelFunc

	// bgIsDark is the effective palette darkness (from the active theme).
	bgIsDark bool
	// detectedIsDark is the terminal background darkness from
	// BackgroundColorMsg. Only auto uses this; named themes ignore it. Defaults
	// to true (dark-friendly) until detection arrives.
	detectedIsDark bool
	// bgKnown is true once a named theme is active, or auto detection has
	// answered (or the user forced a preview). Used to avoid thrashing styles.
	bgKnown bool

	state    state
	width    int
	height   int
	spinner  spinner.Model
	progress progress.Model
	addInput textinput.Model

	// session is the working directory for this run. It is created when a
	// download starts, so browsing the list never leaves a work dir behind.
	session pipeline.Session

	reviewImages []imagelist.Image
	reviewCursor int
	reviewOffset int // first visible index in reviewImages (windowed list)

	activity    chan tea.Msg
	downCurrent int
	downTotal   int
	// imageProgress tracks byte-level progress per in-flight image ref,
	// so the download screen can show all concurrent pulls advancing
	// rather than flapping between refs.
	imageProgress map[string]imageProgress
	entries       []bundle.ImageEntry
	failures      []pipeline.ImageFailure
	// failCursor/failOffset window the download-review failure list so a run
	// with many failures stays navigable on a short terminal.
	failCursor int
	failOffset int
	bundlePath string
	err        error
	// errStep labels which async step failed (download, bundle) so the error
	// screen can frame the message for the user.
	errStep string
	// status is a short, ephemeral line shown under the body (not an error
	// state). Cleared on most navigation. Prefer status over stateError for
	// recoverable UX (empty selection, invalid input, soft validation).
	status string

	// Theme menu (Ctrl+T). themeMenuReturn is the screen to restore on
	// confirm/cancel; themeBeforeMenu is the theme restored if the user Escs
	// after live-previewing other palettes.
	themeMenuCursor int
	themeMenuReturn state
	themeBeforeMenu string

	// Purge screen: the cached image tarballs in the work dir, which entries
	// are selected (keyed by TarPath), and the windowed list position.
	cacheEntries  []pipeline.CacheEntry
	cacheSelected map[string]bool
	cacheCursor   int
	cacheOffset   int
}

// setStatus stores a soft status message for the next render.
func (m *model) setStatus(s string) { m.status = s }

// clearStatus clears any soft status message.
func (m *model) clearStatus() { m.status = "" }

// newModel constructs the root model from cfg and the pre-validated image list.
func newModel(cfg config.Config, logger *log.Logger, imgs []imagelist.Image) model {
	theme := config.NormalizeTheme(cfg.Theme)
	// Auto starts dark-friendly until BackgroundColorMsg arrives.
	detectedIsDark := true
	forced := config.ThemeIsForced(theme)
	styles := newStyles(theme, detectedIsDark)
	bgIsDark := styles.palette.isDark

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = lipgloss.NewStyle().Foreground(styles.palette.accent)

	fill, empty := progressColors(styles.palette)
	prog := progress.New(
		progress.WithColors(fill, empty),
		progress.WithWidth(60),
	)
	prog.EmptyColor = empty

	add := textinput.New()
	add.Placeholder = "registry/repo:tag"
	add.SetStyles(textInputStyles(styles.palette))
	add.CharLimit = 200

	ctx, cancel := context.WithCancel(context.Background())

	pl := pipeline.New(cfg, logger)
	return model{
		cfg:            cfg,
		pipeline:       pl,
		styles:         styles,
		logger:         logger,
		ctx:            ctx,
		cancel:         cancel,
		bgIsDark:       bgIsDark,
		detectedIsDark: detectedIsDark,
		bgKnown:        forced,
		state:          stateReview,
		spinner:        spin,
		progress:       prog,
		addInput:       add,
		reviewImages:   imgs,
		activity:       make(chan tea.Msg, 16),
		imageProgress:  map[string]imageProgress{},
		cacheSelected:  map[string]bool{},
	}
}

// applyTheme rebuilds styles and component chrome from the active cfg.Theme.
// Auto always follows m.detectedIsDark (terminal), never the last named theme.
func (m *model) applyTheme() {
	m.styles = newStyles(m.cfg.Theme, m.detectedIsDark)
	m.bgIsDark = m.styles.palette.isDark
	if config.ThemeIsForced(m.cfg.Theme) {
		m.bgKnown = true
	}
	m.spinner.Style = lipgloss.NewStyle().Foreground(m.styles.palette.accent)
	fill, empty := progressColors(m.styles.palette)
	m.progress.FullColor = fill
	m.progress.EmptyColor = empty
	// Text inputs keep dark-default ANSI styles unless re-themed — fix that so
	// light mode does not show a white prompt/placeholder on cream.
	m.addInput.SetStyles(textInputStyles(m.styles.palette))
}

// openThemeMenu switches to the theme picker, remembering the prior screen and
// theme so Esc can cancel a live preview.
func (m *model) openThemeMenu() {
	m.themeMenuReturn = m.state
	m.themeBeforeMenu = config.NormalizeTheme(m.cfg.Theme)
	m.themeMenuCursor = config.ThemeMenuIndex(m.cfg.Theme)
	m.clearStatus()
	m.state = stateThemeMenu
}

// previewThemeAtCursor applies the palette under the menu cursor without leaving
// the menu, so the user can see each theme before confirming.
func (m *model) previewThemeAtCursor() {
	if m.themeMenuCursor < 0 || m.themeMenuCursor >= len(config.ThemeMenu) {
		return
	}
	m.cfg.Theme = config.ThemeMenu[m.themeMenuCursor]
	m.applyTheme()
}

// confirmThemeMenu keeps the currently previewed theme and returns to the prior
// screen. Selecting auto re-requests terminal background detection.
func (m *model) confirmThemeMenu() tea.Cmd {
	m.clearStatus()
	m.setStatus("Theme: " + config.NormalizeTheme(m.cfg.Theme))
	m.state = m.themeMenuReturn
	if config.NormalizeTheme(m.cfg.Theme) == config.ThemeAuto {
		m.bgKnown = false
		return tea.RequestBackgroundColor
	}
	return nil
}

// cancelThemeMenu restores the theme active when the menu opened and returns.
// Restoring auto re-requests terminal background detection.
func (m *model) cancelThemeMenu() tea.Cmd {
	m.cfg.Theme = m.themeBeforeMenu
	m.applyTheme()
	m.clearStatus()
	m.state = m.themeMenuReturn
	if config.NormalizeTheme(m.cfg.Theme) == config.ThemeAuto {
		m.bgKnown = false
		return tea.RequestBackgroundColor
	}
	return nil
}

// Init starts the spinner and, for theme=auto, requests the terminal background.
func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if config.NormalizeTheme(m.cfg.Theme) == config.ThemeAuto {
		cmds = append(cmds, tea.RequestBackgroundColor)
	}
	return tea.Batch(cmds...)
}

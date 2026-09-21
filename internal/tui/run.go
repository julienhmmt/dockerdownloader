package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
)

// Run starts the TUI program with cfg and the validated image list, and blocks
// until the user quits. notice, when non-empty, is shown as the initial status
// line — e.g. explaining an empty list because the list file is missing. The
// alt screen is requested declaratively via the model's View (v2).
func Run(cfg config.Config, logger *log.Logger, imgs []imagelist.Image, notice string) error {
	m := newModel(cfg, logger, imgs)
	if notice != "" {
		m.setStatus(notice)
	}
	program := tea.NewProgram(m)
	_, err := program.Run()
	return err
}

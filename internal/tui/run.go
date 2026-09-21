package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/julienhmmt/dockerdownloader/pkg/config"
	"github.com/julienhmmt/dockerdownloader/pkg/imagelist"
	"github.com/julienhmmt/dockerdownloader/pkg/log"
)

// Run starts the TUI program with cfg and the validated image list, and blocks
// until the user quits. The alt screen is requested declaratively via the
// model's View (v2).
func Run(cfg config.Config, logger *log.Logger, imgs []imagelist.Image) error {
	program := tea.NewProgram(newModel(cfg, logger, imgs))
	_, err := program.Run()
	return err
}

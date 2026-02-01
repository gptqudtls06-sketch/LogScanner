package tui

import (
	"logscanner/internal/analyzer"

	tea "github.com/charmbracelet/bubbletea"
)

func Run(files []string, updates <-chan analyzer.Event, cfg Config, pauseFn func(bool)) error {
	m := initialModel(files, updates, cfg, pauseFn)
	p := tea.NewProgram(m) // AltScreen OFF (윈도우 안정)
	_, err := p.Run()
	return err
}

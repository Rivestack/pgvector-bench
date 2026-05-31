// Package tui renders the engine's event stream as an animated Bubble Tea UI.
//
// Crucial architectural rule: the UI does NOT do any timing. Every number it
// shows (p50, p95, QPS, recall) is taken verbatim from engine events whose
// timestamps were captured inside the engine's worker goroutines. The TUI's
// only job is to make those numbers nice to look at. This is what lets us
// claim the TUI run and the --json run report identical metrics.
package tui

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Rivestack/pgvector-bench/internal/engine"
)

type Presenter struct {
	prog   *tea.Program
	doneCh chan struct{}
	once   sync.Once
	runErr error
	cancel context.CancelFunc
}

func New() *Presenter {
	m := newModel()
	prog := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	return &Presenter{prog: prog, doneCh: make(chan struct{})}
}

// Send forwards an engine event into the bubbletea program.
func (p *Presenter) Send(ev engine.Event) {
	p.prog.Send(eventMsg{ev: ev})
}

// Run starts the bubbletea program. Returns when the user quits or the engine
// signals it is done.
func (p *Presenter) Run() (any, error) {
	m, err := p.prog.Run()
	p.runErr = err
	close(p.doneCh)
	return m, err
}

// SignalDone tells the TUI the engine finished successfully — it will hold
// the final summary visible for ~2 seconds and then quit itself.
func (p *Presenter) SignalDone() {
	p.prog.Send(allDoneMsg{})
}

// SignalFatal tells the TUI the engine errored — render the error then quit.
func (p *Presenter) SignalFatal(err error) {
	p.prog.Send(fatalMsg{err: err})
}

// Quit asks the TUI to exit immediately.
func (p *Presenter) Quit() {
	p.prog.Quit()
}

func (p *Presenter) WaitForExit() error {
	<-p.doneCh
	return p.runErr
}

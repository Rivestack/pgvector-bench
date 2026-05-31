package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	"github.com/charmbracelet/lipgloss"

	"github.com/Rivestack/pgvector-bench/internal/engine"
)

// eventMsg wraps an engine event for the bubbletea Update loop.
type eventMsg struct{ ev engine.Event }
type tickMsg time.Time
type allDoneMsg struct{}
type fatalMsg struct{ err error }
type autoQuitMsg struct{}

// phaseState tracks what the UI knows about a phase. Phases sit in one of:
// pending / active (animating) / complete (one-line summary, green check).
type phaseStatus int

const (
	phasePending phaseStatus = iota
	phaseActive
	phaseDone
	phaseWarned
)

type phaseLine struct {
	phase     engine.Phase
	title     string
	status    phaseStatus
	note      string
	summary   string
	progress  progress.Model
	pctDone   float64
	liveText  string
	startedAt time.Time
}

type levelBar struct {
	conc     int
	qps      float64
	target   float64 // for spring animation
	spring   harmonica.Spring
	velocity float64
	peak     bool
}

type model struct {
	spinner    spinner.Model
	width      int
	height     int
	startedAt  time.Time
	phaseOrder []engine.Phase
	phases     map[engine.Phase]*phaseLine
	facts      []engine.IntrospectFact
	throughput []*levelBar
	recallPts  []engine.RecallPoint
	finalLat   engine.LatencyResult
	finalTP    engine.ThroughputResult
	finalRec   engine.RecallResult
	warnings   []string
	finished   bool
	quitting   bool
}

func newModel() *model {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent)

	m := &model{
		spinner:   sp,
		phases:    map[engine.Phase]*phaseLine{},
		startedAt: time.Now(),
	}
	return m
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		for _, p := range m.phases {
			p.progress.Width = max(20, m.width-30)
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tickMsg:
		// Advance spring animations for throughput bars.
		for _, b := range m.throughput {
			b.qps, b.velocity = b.spring.Update(b.qps, b.velocity, b.target)
		}
		// Drive progress.FrameMsg-style internal progress updates by
		// resending current pct (progress bar handles its own anim).
		var cmds []tea.Cmd
		cmds = append(cmds, tickCmd())
		if m.finished && allBarsSettled(m.throughput) {
			// keep ticking, but slower
		}
		return m, tea.Batch(cmds...)
	case allDoneMsg:
		m.finished = true
		return m, tea.Tick(2200*time.Millisecond, func(time.Time) tea.Msg { return autoQuitMsg{} })
	case autoQuitMsg:
		return m, tea.Quit
	case fatalMsg:
		m.warnings = append(m.warnings, "FATAL: "+msg.err.Error())
		m.finished = true
		return m, tea.Tick(2500*time.Millisecond, func(time.Time) tea.Msg { return autoQuitMsg{} })
	case eventMsg:
		return m.handleEvent(msg.ev)
	case progress.FrameMsg:
		// Forward to whichever progress bar is currently active.
		var cmds []tea.Cmd
		for _, p := range m.phases {
			if p.status == phaseActive {
				pm, cmd := p.progress.Update(msg)
				p.progress = pm.(progress.Model)
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)
	}
	return m, nil
}

func (m *model) handleEvent(ev engine.Event) (tea.Model, tea.Cmd) {
	switch e := ev.(type) {
	case engine.PhaseStart:
		pl := &phaseLine{
			phase: e.Phase, title: prettyPhase(e.Phase), note: e.Note,
			status: phaseActive, startedAt: time.Now(),
			progress: progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
		}
		pl.progress.Width = max(20, m.width-30)
		m.phases[e.Phase] = pl
		m.phaseOrder = append(m.phaseOrder, e.Phase)
	case engine.PhaseEnd:
		if pl, ok := m.phases[e.Phase]; ok {
			pl.status = phaseDone
			pl.summary = e.Summary
		}
	case engine.IntrospectFact:
		m.facts = append(m.facts, e)
	case engine.Progress:
		if pl, ok := m.phases[e.Phase]; ok {
			if e.Total > 0 {
				pl.pctDone = float64(e.Done) / float64(e.Total)
				_ = pl.progress.SetPercent(pl.pctDone)
			}
			pl.liveText = formatLive(e)
		}
	case engine.ThroughputLevel:
		bar := &levelBar{
			conc:   e.Concurrency,
			target: e.QPS,
			spring: harmonica.NewSpring(harmonica.FPS(25), 6.0, 0.6),
		}
		m.throughput = append(m.throughput, bar)
		recomputePeak(m.throughput)
	case engine.RecallPoint:
		m.recallPts = append(m.recallPts, e)
	case engine.LatencyResult:
		m.finalLat = e
	case engine.ThroughputResult:
		m.finalTP = e
	case engine.RecallResult:
		m.finalRec = e
	case engine.Warn:
		m.warnings = append(m.warnings, e.Message)
	case engine.Fatal:
		m.warnings = append(m.warnings, "FATAL: "+e.Err.Error())
	}
	return m, nil
}

func (m *model) View() string {
	if m.width == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.viewHeader() + "\n\n")
	for _, ph := range m.phaseOrder {
		pl := m.phases[ph]
		if pl == nil {
			continue
		}
		b.WriteString(m.viewPhase(pl) + "\n")
		if ph == engine.PhaseIntrospect && pl.status == phaseDone && len(m.facts) > 0 {
			b.WriteString(m.viewFacts() + "\n")
		}
		if ph == engine.PhaseThroughput && len(m.throughput) > 0 {
			b.WriteString(m.viewThroughput() + "\n")
		}
		if ph == engine.PhaseRecall && len(m.recallPts) > 0 {
			b.WriteString(m.viewRecall() + "\n")
		}
	}
	for _, w := range m.warnings {
		b.WriteString(styleWarn.Render("⚠ ") + w + "\n")
	}
	if m.finished {
		b.WriteString("\n" + m.viewFinal())
	}
	b.WriteString("\n" + styleMuted.Render("press q to quit") + "\n")
	return b.String()
}

func (m *model) viewHeader() string {
	wm := gradientText("pgvector-bench", colAccent, colAccent2)
	sub := styleSubtitle.Render(" — benchmarking your pgvector setup")
	priv := styleMuted.Render("Your vectors and connection details never leave your machine.")
	return wm + sub + "\n" + priv
}

func (m *model) viewPhase(pl *phaseLine) string {
	switch pl.status {
	case phaseDone:
		left := styleOK.Render("✓ ") + styleVal.Render(pl.title)
		right := styleMuted.Render(" — " + pl.summary)
		return left + right
	case phaseActive:
		left := m.spinner.View() + " " + styleVal.Render(pl.title)
		var live string
		if pl.liveText != "" {
			live = "  " + styleMuted.Render(pl.liveText)
		}
		bar := ""
		if pl.pctDone > 0 {
			bar = "\n  " + pl.progress.View()
		}
		return left + live + bar
	}
	return styleMuted.Render("· " + pl.title)
}

func (m *model) viewFacts() string {
	if len(m.facts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range m.facts {
		k := styleKey.Render(fmt.Sprintf("    %-30s ", f.Key))
		b.WriteString(k + styleVal.Render(f.Value) + "\n")
	}
	return b.String()
}

func (m *model) viewThroughput() string {
	maxQPS := 0.0
	for _, b := range m.throughput {
		if b.target > maxQPS {
			maxQPS = b.target
		}
	}
	if maxQPS == 0 {
		return ""
	}
	width := max(20, m.width-40)
	var b strings.Builder
	for _, lvl := range m.throughput {
		frac := lvl.qps / maxQPS
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		filled := int(float64(width) * frac)
		bar := lipgloss.NewStyle().Foreground(colBarFg).Render(strings.Repeat("█", filled))
		bg := lipgloss.NewStyle().Foreground(colBarBg).Render(strings.Repeat("░", width-filled))
		label := fmt.Sprintf("  c=%-3d %s%s  %s",
			lvl.conc, bar, bg,
			styleVal.Render(fmt.Sprintf("%6.0f QPS", lvl.qps)),
		)
		if lvl.peak {
			label += "  " + stylePeak.Render("← peak")
		}
		b.WriteString(label + "\n")
	}
	return b.String()
}

func (m *model) viewRecall() string {
	var b strings.Builder
	for _, p := range m.recallPts {
		line := fmt.Sprintf("  ef_search=%-4d  recall=%.3f  p95=%5.1f ms  qps=%6.0f",
			p.EfSearch, p.Recall, p.P95Ms, p.QPS)
		b.WriteString(styleVal.Render(line) + "\n")
	}
	return b.String()
}

func (m *model) viewFinal() string {
	left := styleSection.Render("Measured on your DB") + "\n" +
		fmt.Sprintf("  p50      %s\n", styleBigNum.Render(fmt.Sprintf("%6.1f ms", m.finalLat.P50Ms))) +
		fmt.Sprintf("  p95      %s\n", styleBigNum.Render(fmt.Sprintf("%6.1f ms", m.finalLat.P95Ms))) +
		fmt.Sprintf("  p99      %s\n", styleBigNum.Render(fmt.Sprintf("%6.1f ms", m.finalLat.P99Ms))) +
		fmt.Sprintf("  peak QPS %s  @ c=%d\n",
			styleBigNum.Render(fmt.Sprintf("%6.0f", m.finalTP.PeakQPS)), m.finalTP.PeakLevel)
	if m.finalRec.Default.EfSearch > 0 {
		left += fmt.Sprintf("  recall   %s @ ef_search=%d\n",
			styleBigNum.Render(fmt.Sprintf("%6.3f", m.finalRec.Default.Recall)),
			m.finalRec.Default.EfSearch)
	}
	return styleCard.Render(left)
}

func prettyPhase(p engine.Phase) string {
	switch p {
	case engine.PhaseConnect:
		return "Connect"
	case engine.PhaseIntrospect:
		return "Introspect"
	case engine.PhaseSyntheticGen:
		return "Generate synthetic data"
	case engine.PhaseIndexBuild:
		return "Build HNSW index"
	case engine.PhaseWarmup:
		return "Warm up"
	case engine.PhaseLatency:
		return "Latency (single thread)"
	case engine.PhaseThroughput:
		return "Throughput ramp"
	case engine.PhaseRecall:
		return "Recall"
	}
	return string(p)
}

func formatLive(p engine.Progress) string {
	parts := []string{}
	if p.LiveP50Ms > 0 || p.LiveP95Ms > 0 {
		parts = append(parts, fmt.Sprintf("p50 %.1f ms · p95 %.1f ms", p.LiveP50Ms, p.LiveP95Ms))
	}
	if p.LiveQPS > 0 {
		parts = append(parts, fmt.Sprintf("%.0f QPS", p.LiveQPS))
	}
	if p.ExtraLabel != "" {
		parts = append(parts, p.ExtraLabel)
	}
	return strings.Join(parts, "  ·  ")
}

func recomputePeak(bars []*levelBar) {
	var peak float64
	var peakIdx int
	for i, b := range bars {
		if b.target > peak {
			peak = b.target
			peakIdx = i
		}
	}
	for i, b := range bars {
		b.peak = i == peakIdx
	}
}

func allBarsSettled(bars []*levelBar) bool {
	for _, b := range bars {
		if absf(b.target-b.qps) > 0.5 {
			return false
		}
	}
	return true
}

func absf(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

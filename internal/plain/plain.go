// Package plain is the non-TTY presenter: line-oriented progress and
// summary output. Used automatically when stdout is not a TTY, or with
// --plain, or whenever --json is requested (in which case ONLY the final
// JSON is emitted, on stdout).
package plain

import (
	"fmt"
	"io"
	"strings"

	"github.com/Rivestack/pgvector-bench/internal/engine"
)

type Presenter struct {
	Out        io.Writer
	JSONOnly   bool // suppress all progress chatter
	NoColor    bool
	lastPhase  engine.Phase
	progressed map[engine.Phase]int
}

func New(out io.Writer, jsonOnly, noColor bool) *Presenter {
	return &Presenter{Out: out, JSONOnly: jsonOnly, NoColor: noColor, progressed: map[engine.Phase]int{}}
}

func (p *Presenter) Handle(ev engine.Event) {
	if p.JSONOnly {
		return
	}
	switch e := ev.(type) {
	case engine.PhaseStart:
		p.lastPhase = e.Phase
		fmt.Fprintf(p.Out, "▸ %s … %s\n", titleCase(string(e.Phase)), e.Note)
	case engine.PhaseEnd:
		fmt.Fprintf(p.Out, "  ✓ %s — %s\n", titleCase(string(e.Phase)), e.Summary)
	case engine.IntrospectFact:
		fmt.Fprintf(p.Out, "    · %-32s %s\n", e.Key, e.Value)
	case engine.Progress:
		if e.Total <= 0 {
			return
		}
		pctNow := e.Done * 10 / e.Total
		if pctNow > p.progressed[e.Phase] {
			p.progressed[e.Phase] = pctNow
			live := ""
			if e.LiveP50Ms > 0 || e.LiveP95Ms > 0 {
				live = fmt.Sprintf(" p50=%.1fms p95=%.1fms", e.LiveP50Ms, e.LiveP95Ms)
			}
			if e.LiveQPS > 0 {
				live += fmt.Sprintf(" qps=%.0f", e.LiveQPS)
			}
			label := ""
			if e.ExtraLabel != "" {
				label = " " + e.ExtraLabel
			}
			fmt.Fprintf(p.Out, "    %s %d%%%s%s\n", barASCII(pctNow, 10), pctNow*10, live, label)
		}
	case engine.ThroughputLevel:
		fmt.Fprintf(p.Out, "    · c=%-3d  %7.0f QPS  p95 %5.1f ms  (%d queries / %s)\n",
			e.Concurrency, e.QPS, e.P95Ms, e.TotalQueries, e.Duration.Round(1e6))
	case engine.RecallPoint:
		fmt.Fprintf(p.Out, "    · ef_search=%-4d recall=%.3f  p95 %5.1f ms  qps %.0f\n",
			e.EfSearch, e.Recall, e.P95Ms, e.QPS)
	case engine.LatencyResult, engine.ThroughputResult, engine.RecallResult:
		// already summarized by PhaseEnd
	case engine.Warn:
		fmt.Fprintf(p.Out, "  ! %s\n", e.Message)
	case engine.Fatal:
		fmt.Fprintf(p.Out, "  ✗ %v\n", e.Err)
	}
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ReplaceAll(s[1:], "_", " ")
}

func barASCII(filled, total int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < total; i++ {
		if i < filled {
			b.WriteByte('#')
		} else {
			b.WriteByte('.')
		}
	}
	b.WriteByte(']')
	return b.String()
}

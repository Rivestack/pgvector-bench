package report

import (
	"fmt"
	"io"
)

// WriteMarkdown writes a copy-paste-ready Markdown summary suitable for
// Reddit, HN, GitHub issues, etc. Aggregate metrics only — never includes
// the connection string, hostname, or any vector content.
func WriteMarkdown(w io.Writer, out *Output) error {
	r := out.Result
	fmt.Fprintf(w, "### pgvector-bench — %s\n\n", r.Env.IndexType)
	fmt.Fprintf(w, "- Postgres %s · pgvector %s\n", shortPG(r.Env.PGVersion), r.Env.PGVectorVersion)
	fmt.Fprintf(w, "- vector(%d) · %s rows · index %s",
		r.Env.Dim, fmtInt(r.Env.RowCount), r.Env.IndexType)
	if r.Env.IndexM > 0 {
		fmt.Fprintf(w, " (m=%d, ef_construction=%d)", r.Env.IndexM, r.Env.IndexEfConstruct)
	}
	fmt.Fprintf(w, "\n\n")

	fmt.Fprintln(w, "| Metric | Measured on your DB | Rivestack NVMe (projected) |")
	fmt.Fprintln(w, "|---|---:|---:|")
	if out.Projection != nil {
		b := out.Projection.Bucket
		fmt.Fprintf(w, "| p50 | %.1f ms | %.1f ms |\n", r.Latency.P50Ms, b.P50Ms)
		fmt.Fprintf(w, "| p95 | %.1f ms | %.1f ms |\n", r.Latency.P95Ms, b.P95Ms)
		fmt.Fprintf(w, "| peak QPS | %.0f | %.0f |\n", r.Throughput.PeakQPS, b.QPS)
		fmt.Fprintf(w, "| recall@%d | %.3f | %.3f |\n", r.Config.K, r.Recall.Default.Recall, b.Recall)
	} else {
		fmt.Fprintf(w, "| p50 | %.1f ms | — |\n", r.Latency.P50Ms)
		fmt.Fprintf(w, "| p95 | %.1f ms | — |\n", r.Latency.P95Ms)
		fmt.Fprintf(w, "| peak QPS | %.0f | — |\n", r.Throughput.PeakQPS)
		if r.Recall.Default.EfSearch > 0 {
			fmt.Fprintf(w, "| recall@%d | %.3f | — |\n", r.Config.K, r.Recall.Default.Recall)
		}
	}
	fmt.Fprintln(w)
	if out.Projection != nil {
		fmt.Fprintln(w, "_Projection: nearest bucket from Rivestack reference benchmarks._")
	} else {
		fmt.Fprintln(w, "_No reference benchmark for this workload shape._")
	}
	fmt.Fprintf(w, "\n[See your projected setup on dedicated NVMe](%s) · benched with `pgvector-bench`\n", out.SwitchURL)
	return nil
}

func shortPG(s string) string {
	for i, c := range s {
		if c == ' ' && i+4 < len(s) && s[i+1] == 'o' && s[i+2] == 'n' && s[i+3] == ' ' {
			return s[:i]
		}
	}
	return s
}

func fmtInt(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	out := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, byte(c))
	}
	return string(out)
}

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Rivestack/pgvector-bench/internal/engine"
	"github.com/Rivestack/pgvector-bench/internal/plain"
	"github.com/Rivestack/pgvector-bench/internal/projection"
	"github.com/Rivestack/pgvector-bench/internal/report"
	"github.com/Rivestack/pgvector-bench/internal/tui"
)

// Version is set at build time via -ldflags.
var Version = "0.1.0-dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		// Errors are scrubbed of any pgx connection-string content
		// before reaching us via internal/engine.scrub.
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	rc := runCmd()
	root := &cobra.Command{
		Use:           "pgvector-bench",
		Short:         "Benchmark your existing pgvector setup. Your vectors and connection details never leave your machine.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Bare `pgvector-bench` (no subcommand) → delegate to `run`, so the
		// wizard kicks in at an interactive terminal. Scripted users keep
		// using `pgvector-bench run --url ...`.
		RunE: rc.RunE,
	}
	// Mirror the run flags on the root so `pgvector-bench --url ...` also works.
	root.Flags().AddFlagSet(rc.Flags())
	root.AddCommand(rc)
	return root
}

func runCmd() *cobra.Command {
	var (
		url         string
		table       string
		column      string
		metric      string
		k           int
		queries     int
		concStr     string
		efSearchStr string
		recallN     int
		synthetic   bool
		synthRows   int
		synthDim    int
		reportFmt   string
		outPath     string
		jsonOnly    bool
		plainOut    bool
		noColor     bool
	)

	cmd := &cobra.Command{
		Use:           "run",
		Short:         "Run the benchmark",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// PGVB_URL is an alternative to --url for users whose shell mangles
			// the connection string (zsh expands "?" in URLs, breaking pasted
			// connection strings unless they're quoted).
			if url == "" {
				url = os.Getenv("PGVB_URL")
			}

			cfg := &engine.Config{
				URL: url, Table: table, Column: column,
				Metric:        engine.Metric(metric),
				K:             k,
				Queries:       queries,
				Concurrency:   parseInts(concStr),
				EfSearch:      parseInts(efSearchStr),
				RecallSample:  recallN,
				Synthetic:     synthetic,
				SyntheticRows: synthRows,
				SyntheticDim:  synthDim,
			}

			// Auto-detect plain mode.
			ttyIn := term.IsTerminal(int(os.Stdin.Fd()))
			ttyOut := term.IsTerminal(int(os.Stdout.Fd()))
			useTUI := !plainOut && !jsonOnly && ttyOut
			if os.Getenv("NO_COLOR") != "" {
				noColor = true
			}

			// If the user didn't pass --url and they're at an interactive
			// terminal, run the wizard. This sidesteps the zsh-glob-expansion
			// problem with "?sslmode=…" because the URL is typed into a prompt
			// rather than a shell argument.
			if cfg.URL == "" && ttyIn && ttyOut && !jsonOnly {
				if err := runWizard(cfg); err != nil {
					return err
				}
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			var (
				present func(engine.Event)
				tuiP    *tui.Presenter
			)
			if useTUI {
				tuiP = tui.New()
				present = tuiP.Send
				go func() {
					_, _ = tuiP.Run()
				}()
			} else {
				p := plain.New(os.Stderr, jsonOnly, noColor)
				present = p.Handle
			}

			res, runErr := engine.Run(ctx, cfg, Version, present)

			if useTUI {
				if runErr != nil {
					tuiP.SignalFatal(runErr)
				} else {
					tuiP.SignalDone()
				}
				_ = tuiP.WaitForExit()
			}

			if runErr != nil {
				return runErr
			}

			ref, err := projection.Load()
			if err != nil {
				return err
			}
			out := report.Build(res, ref)

			// JSON to stdout?
			if jsonOnly {
				return report.WriteJSON(os.Stdout, out)
			}

			// Plain summary footer.
			printFooter(os.Stdout, out, noColor)

			// File outputs.
			writeFmt := func(fmt string) error {
				switch fmt {
				case "json":
					return writeFile(outPath, "json", func(w io.Writer) error { return report.WriteJSON(w, out) })
				case "html":
					return writeFile(outPath, "html", func(w io.Writer) error { return report.WriteHTML(w, out) })
				case "md", "markdown":
					return writeFile(outPath, "md", func(w io.Writer) error { return report.WriteMarkdown(w, out) })
				}
				return nil
			}
			switch reportFmt {
			case "json", "html", "md", "markdown":
				if err := writeFmt(reportFmt); err != nil {
					return err
				}
			case "both":
				for _, f := range []string{"json", "html"} {
					if err := writeFmt(f); err != nil {
						return err
					}
				}
			case "all":
				for _, f := range []string{"json", "html", "md"} {
					if err := writeFmt(f); err != nil {
						return err
					}
				}
			case "", "none":
				// nothing
			}

			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "Postgres connection string (also reads PGVB_URL env)")
	cmd.Flags().StringVar(&table, "table", "", "Target table (schema.table or table)")
	cmd.Flags().StringVar(&column, "column", "", "Vector column name")
	cmd.Flags().StringVar(&metric, "metric", "cosine", "Distance metric: cosine | l2 | ip")
	cmd.Flags().IntVar(&k, "k", 10, "Neighbors per query (LIMIT)")
	cmd.Flags().IntVar(&queries, "queries", 1000, "Benchmark queries")
	cmd.Flags().StringVar(&concStr, "concurrency", "1,8,32", "Comma-separated concurrency levels")
	cmd.Flags().StringVar(&efSearchStr, "ef-search", "", "Comma-separated ef_search sweep (HNSW)")
	cmd.Flags().IntVar(&recallN, "recall-sample", 200, "Queries used for exact-KNN ground truth")
	cmd.Flags().BoolVar(&synthetic, "synthetic", false, "Generate a synthetic dataset on the target DB")
	cmd.Flags().IntVar(&synthRows, "rows", 100000, "Synthetic row count")
	cmd.Flags().IntVar(&synthDim, "dim", 1536, "Synthetic vector dimension")
	cmd.Flags().StringVar(&reportFmt, "report", "", "Report format: json | html | md | both | all")
	cmd.Flags().StringVar(&outPath, "out", "", "Output path prefix for --report")
	cmd.Flags().BoolVar(&jsonOnly, "json", false, "Emit JSON to stdout, suppress TUI")
	cmd.Flags().BoolVar(&plainOut, "plain", false, "Force plain output (auto when stdout is not a TTY)")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "Disable color output (NO_COLOR env honored)")

	return cmd
}

func parseInts(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

func writeFile(prefix, ext string, write func(io.Writer) error) error {
	path := prefix
	if path == "" {
		path = "pgvector-bench-report"
	}
	if !strings.HasSuffix(path, "."+ext) {
		path = path + "." + ext
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := write(f); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", path)
	return nil
}

func printFooter(w io.Writer, out *report.Output, noColor bool) {
	r := out.Result
	fmt.Fprintln(w)
	fmt.Fprintln(w, "─── Measured on your DB ─────────────────────────────")
	fmt.Fprintf(w, "  p50      %6.1f ms\n", r.Latency.P50Ms)
	fmt.Fprintf(w, "  p95      %6.1f ms\n", r.Latency.P95Ms)
	fmt.Fprintf(w, "  p99      %6.1f ms\n", r.Latency.P99Ms)
	fmt.Fprintf(w, "  peak QPS %6.0f  @ concurrency=%d\n", r.Throughput.PeakQPS, r.Throughput.PeakLevel)
	if r.Recall.Default.EfSearch > 0 {
		fmt.Fprintf(w, "  recall   %6.3f @ ef_search=%d\n", r.Recall.Default.Recall, r.Recall.Default.EfSearch)
	}
	fmt.Fprintln(w)
	if out.Projection != nil {
		b := out.Projection.Bucket
		fmt.Fprintln(w, "─── Rivestack NVMe (projected) ──────────────────────")
		fmt.Fprintf(w, "  p50      %6.1f ms\n", b.P50Ms)
		fmt.Fprintf(w, "  p95      %6.1f ms\n", b.P95Ms)
		fmt.Fprintf(w, "  peak QPS %6.0f\n", b.QPS)
		fmt.Fprintf(w, "  recall   %6.3f\n", b.Recall)
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Projection from Rivestack reference benchmarks for a similar")
		fmt.Fprintln(w, "  workload shape — your numbers will vary.")
	} else {
		fmt.Fprintln(w, "─── Rivestack NVMe (projected) ──────────────────────")
		fmt.Fprintln(w, "  No reference benchmark for this workload shape.")
		fmt.Fprintln(w, "  Get a free workload review: https://rivestack.io/switch")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "→ See your projected setup on dedicated NVMe:")
	fmt.Fprintln(w, "  "+out.SwitchURL)
	fmt.Fprintln(w)
}

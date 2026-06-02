package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/Rivestack/pgvector-bench/internal/engine"
)

// runWizard prompts the user for connection + workload settings and returns
// a populated config. It exists to solve two real friction points:
//   1. Shell glob expansion of unquoted "?" in connection strings (zsh
//      rejects "?sslmode=require" before the binary ever runs). The wizard
//      reads the URL from a text input, never a shell argument.
//   2. A bare `pgvector-bench` invocation on a fresh machine — no flags
//      to memorize, no docs to alt-tab to.
func runWizard(cfg *engine.Config) error {
	// We mutate strings (not engine.Config fields directly) because huh's
	// text inputs only bind to *string. Numbers come in as strings and we
	// parse them after.
	var (
		dbURL     string
		mode      = "synthetic"
		table     string
		column    = "embedding"
		metric    = "cosine"
		k         = "10"
		queries   = "1000"
		concStr   = "1,8,32"
		efSweep   string
		recallN   = "200"
		synthRows = "100000"
		synthDim  = "1536"
		advanced  bool
	)
	if cfg.URL != "" {
		dbURL = cfg.URL
	}
	if cfg.Table != "" {
		table = cfg.Table
		mode = "existing"
	}
	if cfg.Column != "" {
		column = cfg.Column
	}

	urlField := huh.NewInput().
		Title("Postgres connection string").
		Description("postgres://user:pass@host:5432/db?sslmode=require — paste from anywhere, no shell quoting needed.").
		Placeholder("postgres://...").
		EchoMode(huh.EchoModePassword).
		Value(&dbURL).
		Validate(func(s string) error {
			s = strings.TrimSpace(s)
			if s == "" {
				return errors.New("required")
			}
			u, err := url.Parse(s)
			if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
				return errors.New("must start with postgres:// or postgresql://")
			}
			return nil
		})

	modeField := huh.NewSelect[string]().
		Title("What do you want to benchmark?").
		Options(
			huh.NewOption("An existing table I already have", "existing"),
			huh.NewOption("A synthetic dataset (we'll create it for you)", "synthetic"),
		).
		Value(&mode)

	form := huh.NewForm(
		huh.NewGroup(urlField),
		huh.NewGroup(modeField),
	)
	if err := form.Run(); err != nil {
		return err
	}

	// Branch on mode for the workload-specific questions.
	if mode == "existing" {
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Table name").
					Description("Use schema.table or just table; we'll default the schema to public.").
					Placeholder("documents").
					Value(&table).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return errors.New("required")
						}
						return nil
					}),
				huh.NewInput().
					Title("Vector column name").
					Placeholder("embedding").
					Value(&column).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return errors.New("required")
						}
						return nil
					}),
				huh.NewSelect[string]().
					Title("Distance metric").
					Description("Match the operator your app uses for ORDER BY.").
					Options(
						huh.NewOption("cosine  (<=>)  most common for embeddings", "cosine"),
						huh.NewOption("l2      (<->)  euclidean", "l2"),
						huh.NewOption("ip      (<#>)  inner product", "ip"),
					).
					Value(&metric),
			),
		).Run()
		if err != nil {
			return err
		}
	} else {
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("How many synthetic rows?").
					Description("More rows = more representative HNSW behavior, but takes longer to generate.").
					Value(&synthRows).
					Validate(intRange(1000, 50_000_000, "rows")),
				huh.NewInput().
					Title("Vector dimension").
					Description("Match your real embedding model: 384 (MiniLM), 768 (BGE), 1024 (Cohere), 1536 (OpenAI), 3072 (text-embedding-3-large).").
					Value(&synthDim).
					Validate(intRange(8, 16000, "dim")),
				huh.NewSelect[string]().
					Title("Distance metric").
					Options(
						huh.NewOption("cosine", "cosine"),
						huh.NewOption("l2", "l2"),
						huh.NewOption("ip", "ip"),
					).
					Value(&metric),
			),
		).Run()
		if err != nil {
			return err
		}
	}

	// Optional advanced parameters.
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("Customize benchmark parameters?").
				Description("Skip and we'll use sensible defaults: 1000 queries · concurrency 1,8,32 · 200 recall samples.").
				Affirmative("Customize").
				Negative("Use defaults").
				Value(&advanced),
		),
	).Run(); err != nil {
		return err
	}
	if advanced {
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Neighbors per query (k)").
					Value(&k).
					Validate(intRange(1, 1000, "k")),
				huh.NewInput().
					Title("Benchmark queries").
					Description("Total queries for the single-thread latency phase.").
					Value(&queries).
					Validate(intRange(10, 1_000_000, "queries")),
				huh.NewInput().
					Title("Concurrency levels").
					Description("Comma-separated. Each level runs 8s.").
					Value(&concStr).
					Validate(commaInts("concurrency")),
				huh.NewInput().
					Title("ef_search sweep (optional)").
					Description("Comma-separated HNSW ef_search values to compare. Leave blank for server default.").
					Value(&efSweep).
					Validate(func(s string) error {
						if strings.TrimSpace(s) == "" {
							return nil
						}
						return commaInts("ef_search")(s)
					}),
				huh.NewInput().
					Title("Recall sample size").
					Description("Number of queries to compute exact ground truth for.").
					Value(&recallN).
					Validate(intRange(10, 5000, "recall-sample")),
			),
		).Run()
		if err != nil {
			return err
		}
	}

	// Apply to cfg.
	cfg.URL = strings.TrimSpace(dbURL)
	cfg.Metric = engine.Metric(metric)
	cfg.K = atoi(k, 10)
	cfg.Queries = atoi(queries, 1000)
	cfg.Concurrency = parseInts(concStr)
	cfg.EfSearch = parseInts(efSweep)
	cfg.RecallSample = atoi(recallN, 200)
	if mode == "existing" {
		cfg.Synthetic = false
		cfg.Table = strings.TrimSpace(table)
		cfg.Column = strings.TrimSpace(column)
	} else {
		cfg.Synthetic = true
		cfg.SyntheticRows = atoi(synthRows, 100_000)
		cfg.SyntheticDim = atoi(synthDim, 1536)
	}

	// Echo the equivalent flag invocation so the user can save it.
	fmt.Println()
	fmt.Println("→ Running with:")
	fmt.Println("  " + equivalentCommand(cfg))
	fmt.Println()
	return nil
}

func intRange(lo, hi int, name string) func(string) error {
	return func(s string) error {
		s = strings.TrimSpace(s)
		n, err := strconv.Atoi(s)
		if err != nil {
			return fmt.Errorf("%s must be a number", name)
		}
		if n < lo || n > hi {
			return fmt.Errorf("%s must be between %d and %d", name, lo, hi)
		}
		return nil
	}
}

func commaInts(name string) func(string) error {
	return func(s string) error {
		for _, p := range strings.Split(s, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil || n <= 0 {
				return fmt.Errorf("%s: %q is not a positive integer", name, p)
			}
		}
		return nil
	}
}

func atoi(s string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// equivalentCommand renders the equivalent flag-driven command so a user
// can save and rerun without going through the wizard again.
func equivalentCommand(cfg *engine.Config) string {
	parts := []string{"pgvector-bench run"}
	parts = append(parts, "--url '"+engine.RedactURL(cfg.URL)+"'")
	if cfg.Synthetic {
		parts = append(parts,
			"--synthetic",
			fmt.Sprintf("--rows %d", cfg.SyntheticRows),
			fmt.Sprintf("--dim %d", cfg.SyntheticDim),
		)
	} else {
		parts = append(parts,
			"--table "+cfg.Table,
			"--column "+cfg.Column,
		)
	}
	parts = append(parts, "--metric "+string(cfg.Metric))
	if cfg.K != 10 {
		parts = append(parts, fmt.Sprintf("--k %d", cfg.K))
	}
	if cfg.Queries != 1000 {
		parts = append(parts, fmt.Sprintf("--queries %d", cfg.Queries))
	}
	if !equalIntSlice(cfg.Concurrency, []int{1, 8, 32}) {
		parts = append(parts, "--concurrency "+joinInts(cfg.Concurrency))
	}
	if len(cfg.EfSearch) > 0 {
		parts = append(parts, "--ef-search "+joinInts(cfg.EfSearch))
	}
	if cfg.RecallSample != 200 {
		parts = append(parts, fmt.Sprintf("--recall-sample %d", cfg.RecallSample))
	}
	return strings.Join(parts, " \\\n    ")
}

func joinInts(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	return strings.Join(s, ",")
}

func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

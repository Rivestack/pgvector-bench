package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Result is the typed bundle produced by a complete run. Presenters and
// reporters consume this directly; events on the channel are for live UX.
type Result struct {
	Env         *Env             `json:"env"`
	Config      RuntimeConfig    `json:"config"`
	Latency     LatencyResult    `json:"latency"`
	Throughput  ThroughputResult `json:"throughput"`
	Recall      RecallResult     `json:"recall"`
	StartedAt   time.Time        `json:"started_at"`
	FinishedAt  time.Time        `json:"finished_at"`
	ToolVersion string           `json:"tool_version"`
}

// RuntimeConfig is the user-visible subset of Config — what we ran with.
type RuntimeConfig struct {
	Metric        Metric `json:"metric"`
	K             int    `json:"k"`
	Queries       int    `json:"queries"`
	Concurrency   []int  `json:"concurrency"`
	EfSearch      []int  `json:"ef_search,omitempty"`
	RecallSample  int    `json:"recall_sample"`
	Synthetic     bool   `json:"synthetic"`
	SyntheticRows int    `json:"synthetic_rows,omitempty"`
	SyntheticDim  int    `json:"synthetic_dim,omitempty"`
}

// Run executes the full benchmark pipeline. The caller passes a context (for
// cancellation) and consumes Event values from the returned channel. Run
// closes the channel and returns; the final Result is delivered as the
// second-to-last event (it is also returned via the *Result that the engine
// builds internally — presenters can build it from events too).
func Run(parent context.Context, cfg *Config, version string, emit func(Event)) (*Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	startedAt := time.Now()

	// Pool sized for the heaviest concurrency level + headroom.
	poolCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, errors.New("invalid --url")
	}
	maxConns := int32(cfg.MaxConcurrency() + 4)
	if maxConns < 4 {
		maxConns = 4
	}
	poolCfg.MaxConns = maxConns
	poolCfg.MinConns = 1
	poolCfg.MaxConnLifetime = 30 * time.Minute

	emit(PhaseStart{Phase: PhaseConnect, Note: "opening pool"})
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, scrub(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return nil, scrub(err)
	}
	emit(PhaseEnd{Phase: PhaseConnect, Summary: "connected"})

	// Optional synthetic-data generation upfront.
	if cfg.Synthetic {
		if err := generateSynthetic(ctx, pool, cfg, emit); err != nil {
			return nil, err
		}
		defer dropSynthetic(context.Background(), pool)
	}

	// Introspect.
	emit(PhaseStart{Phase: PhaseIntrospect, Note: "examining DB"})
	env, err := introspect(ctx, pool, cfg)
	if err != nil {
		return nil, err
	}
	for _, f := range envFacts(env) {
		emit(f)
	}
	emit(PhaseEnd{Phase: PhaseIntrospect, Summary: introspectSummary(env)})

	if env.IndexType == "none" {
		emit(Warn{Message: "no HNSW/IVFFLAT index on target column — results reflect sequential scans, not ANN performance"})
	}

	// Sample query vectors.
	totalNeeded := cfg.Queries
	if cfg.RecallSample > totalNeeded {
		totalNeeded = cfg.RecallSample
	}
	queries, err := sampleQueryVectors(ctx, pool, env, totalNeeded)
	if err != nil {
		return nil, err
	}
	if len(queries) == 0 {
		return nil, errors.New("no rows in target column to sample from")
	}

	defaultEf := env.DefaultEfSearch
	if env.IndexType != "hnsw" {
		defaultEf = 0
	}

	// Latency (single-threaded).
	lat, err := runLatency(ctx, pool, env, cfg, queries, defaultEf, emit)
	if err != nil {
		return nil, err
	}

	// Throughput ramp.
	tp, err := runThroughput(ctx, pool, env, cfg, queries, defaultEf, emit)
	if err != nil {
		return nil, err
	}

	// Recall (+ ef_search sweep).
	recallQ := queries
	if len(recallQ) > cfg.RecallSample {
		recallQ = recallQ[:cfg.RecallSample]
	}
	rec, err := runRecall(ctx, pool, env, cfg, recallQ, emit)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Env: env,
		Config: RuntimeConfig{
			Metric: cfg.Metric, K: cfg.K, Queries: cfg.Queries,
			Concurrency: cfg.Concurrency, EfSearch: cfg.EfSearch,
			RecallSample: cfg.RecallSample,
			Synthetic:    cfg.Synthetic, SyntheticRows: cfg.SyntheticRows, SyntheticDim: cfg.SyntheticDim,
		},
		Latency:     lat,
		Throughput:  tp,
		Recall:      rec,
		StartedAt:   startedAt,
		FinishedAt:  time.Now(),
		ToolVersion: version,
	}
	emit(PhaseEnd{Phase: PhaseDone, Summary: "all phases complete"})
	return res, nil
}

func envFacts(env *Env) []Event {
	facts := []Event{
		IntrospectFact{Key: "Postgres", Value: shortVersion(env.PGVersion)},
		IntrospectFact{Key: "pgvector", Value: env.PGVectorVersion},
		IntrospectFact{Key: "table", Value: fmt.Sprintf("%s.%s (%s rows, %s)", env.Schema, env.Table, formatInt(env.RowCount), formatBytes(env.TableSizeBytes))},
		IntrospectFact{Key: "column", Value: fmt.Sprintf("%s vector(%d)", env.Column, env.Dim)},
	}
	if env.IndexType == "none" {
		facts = append(facts, IntrospectFact{Key: "index", Value: "none ⚠"})
	} else {
		idx := env.IndexType
		if env.IndexM > 0 {
			idx += fmt.Sprintf(" m=%d ef_construction=%d", env.IndexM, env.IndexEfConstruct)
		}
		if env.IndexLists > 0 {
			idx += fmt.Sprintf(" lists=%d", env.IndexLists)
		}
		idx += " · " + formatBytes(env.IndexSizeBytes)
		facts = append(facts, IntrospectFact{Key: "index", Value: idx})
	}
	for k, v := range env.ServerSettings {
		facts = append(facts, IntrospectFact{Key: k, Value: v})
	}
	return facts
}

func introspectSummary(env *Env) string {
	if env.IndexType == "none" {
		return fmt.Sprintf("%s.%s · no ANN index", env.Schema, env.Table)
	}
	return fmt.Sprintf("%s.%s · %s index", env.Schema, env.Table, env.IndexType)
}

func shortVersion(s string) string {
	if i := indexOf(s, " on "); i > 0 {
		return s[:i]
	}
	return s
}

func formatInt(n int64) string {
	if n < 0 {
		return fmt.Sprint(n)
	}
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

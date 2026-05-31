package engine

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runLatency runs one query at a time on a single connection and reports
// p50/p95/p99 of round-trip latency from inside the worker.
func runLatency(
	ctx context.Context,
	pool *pgxpool.Pool,
	env *Env,
	cfg *Config,
	queries [][]float32,
	efSearch int,
	emit func(Event),
) (LatencyResult, error) {
	emit(PhaseStart{Phase: PhaseLatency, Note: fmt.Sprintf("%d single-threaded queries", cfg.Queries)})

	tbl := quoteIdent(env.Schema, env.Table)
	col := quoteIdent(env.Column)
	op := cfg.Metric.Operator()
	sql := fmt.Sprintf("SELECT 1 FROM %s ORDER BY %s %s $1::vector LIMIT %d", tbl, col, op, cfg.K)

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return LatencyResult{}, scrub(err)
	}
	defer conn.Release()

	if efSearch > 0 && env.IndexType == "hnsw" {
		if _, err := conn.Exec(ctx, fmt.Sprintf("SET hnsw.ef_search = %d", efSearch)); err != nil {
			return LatencyResult{}, scrub(err)
		}
	}

	// Warmup (timings discarded).
	for i := 0; i < cfg.WarmupCount && i < len(queries); i++ {
		_, _ = conn.Exec(ctx, sql, VectorLiteral(queries[i]))
	}

	durs := make([]float64, 0, cfg.Queries)
	tickEvery := cfg.Queries / 50
	if tickEvery < 1 {
		tickEvery = 1
	}
	for i := 0; i < cfg.Queries; i++ {
		select {
		case <-ctx.Done():
			return LatencyResult{}, ctx.Err()
		default:
		}
		q := queries[i%len(queries)]
		t0 := time.Now()
		rows, err := conn.Query(ctx, sql, VectorLiteral(q))
		if err != nil {
			return LatencyResult{}, scrub(err)
		}
		// Drain quickly; we only care about server-side round-trip.
		for rows.Next() {
		}
		rows.Close()
		d := float64(time.Since(t0).Microseconds()) / 1000.0
		durs = append(durs, d)

		if i%tickEvery == 0 || i == cfg.Queries-1 {
			p50, p95, _, _ := summarize(durs)
			emit(Progress{
				Phase: PhaseLatency, Done: i + 1, Total: cfg.Queries,
				LiveP50Ms: p50, LiveP95Ms: p95,
			})
		}
	}

	p50, p95, p99, mean := summarize(durs)
	res := LatencyResult{P50Ms: p50, P95Ms: p95, P99Ms: p99, MeanMs: mean, Count: len(durs)}
	emit(res)
	emit(PhaseEnd{Phase: PhaseLatency,
		Summary: fmt.Sprintf("p50 %.1f ms · p95 %.1f ms · p99 %.1f ms", p50, p95, p99)})
	return res, nil
}

// summarize returns p50, p95, p99, mean from a slice of millisecond durations.
func summarize(durs []float64) (p50, p95, p99, mean float64) {
	if len(durs) == 0 {
		return
	}
	sorted := append([]float64(nil), durs...)
	sort.Float64s(sorted)
	p50 = pct(sorted, 50)
	p95 = pct(sorted, 95)
	p99 = pct(sorted, 99)
	var s float64
	for _, x := range durs {
		s += x
	}
	mean = s / float64(len(durs))
	return
}

func pct(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	r := p / 100 * float64(len(sorted)-1)
	lo := int(math.Floor(r))
	hi := int(math.Ceil(r))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo]*(float64(hi)-r) + sorted[hi]*(r-float64(lo))
}

// resetEfSearch resets the session ef_search to the server default for
// a connection that may be returned to the pool.
func resetEfSearch(ctx context.Context, conn *pgx.Conn) {
	_, _ = conn.Exec(ctx, "RESET hnsw.ef_search")
	_, _ = conn.Exec(ctx, "RESET ivfflat.probes")
}

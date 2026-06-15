package engine

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runRecall computes recall@k against exact-KNN ground truth for cfg.RecallSample
// query vectors. When ef_search values are provided, it sweeps and reports a
// point per value. Otherwise it reports a single point at the server default.
//
// Ground truth strategy:
//
//	Run the same ORDER BY ... LIMIT k query inside a transaction with
//	enable_indexscan / enable_indexonlyscan / enable_bitmapscan turned off so
//	pgvector falls back to a sequential scan. We verify the plan with EXPLAIN
//	once on the first query and warn if the index is still picked.
func runRecall(
	ctx context.Context,
	pool *pgxpool.Pool,
	env *Env,
	cfg *Config,
	queries [][]float32,
	selfIDs []string,
	emit func(Event),
) (RecallResult, error) {
	if env.IndexType != "hnsw" && env.IndexType != "ivfflat" {
		emit(Warn{Message: "no ANN index — skipping recall (sequential scan recall is trivially 1.0)"})
		return RecallResult{}, nil
	}
	if len(queries) == 0 {
		return RecallResult{}, nil
	}
	emit(PhaseStart{Phase: PhaseRecall,
		Note: fmt.Sprintf("%d ground-truth queries", len(queries))})

	tbl := quoteIdent(env.Schema, env.Table)
	col := quoteIdent(env.Column)
	op := cfg.Metric.Operator()

	// Determine row identity: prefer ctid (always unique, no schema assumptions).
	idExpr := "ctid"
	// Fetch k+1: query vectors are sampled from the table, so the row itself is
	// the distance-0 nearest neighbour. We drop that self-match (by ctid) from
	// both the index result and the ground truth so recall@k measures the k real
	// neighbours, not a guaranteed free hit.
	fetchK := cfg.K + 1
	annSQL := fmt.Sprintf("SELECT %s::text FROM %s ORDER BY %s %s $1::vector LIMIT %d",
		idExpr, tbl, col, op, fetchK)
	exactSQL := fmt.Sprintf("SELECT %s::text FROM %s ORDER BY %s %s $1::vector LIMIT %d",
		idExpr, tbl, col, op, fetchK)

	// Compute ground truth once per query.
	groundTruth := make([][]string, len(queries))
	{
		conn, err := pool.Acquire(ctx)
		if err != nil {
			return RecallResult{}, scrub(err)
		}
		// Use a transaction so SET LOCAL is scoped + auto-reverts on commit.
		tx, err := conn.Begin(ctx)
		if err != nil {
			conn.Release()
			return RecallResult{}, scrub(err)
		}
		for _, stmt := range []string{
			"SET LOCAL enable_indexscan = off",
			"SET LOCAL enable_indexonlyscan = off",
			"SET LOCAL enable_bitmapscan = off",
		} {
			if _, err := tx.Exec(ctx, stmt); err != nil {
				_ = tx.Rollback(ctx)
				conn.Release()
				return RecallResult{}, scrub(err)
			}
		}
		// Verify the plan once — if disable_indexscan didn't take effect,
		// recall numbers would be meaningless. Abort rather than mislead.
		if planOK, planNote := verifySeqScan(ctx, tx, exactSQL, queries[0]); !planOK {
			_ = tx.Rollback(ctx)
			conn.Release()
			emit(Warn{Message: "skipping recall: exact KNN plan still used an index (" + firstLine(planNote) + ")"})
			return RecallResult{}, nil
		}
		for i, q := range queries {
			if ctx.Err() != nil {
				_ = tx.Rollback(ctx)
				conn.Release()
				return RecallResult{}, ctx.Err()
			}
			ids, err := scanIDs(ctx, tx, exactSQL, q)
			if err != nil {
				_ = tx.Rollback(ctx)
				conn.Release()
				return RecallResult{}, err
			}
			groundTruth[i] = dropSelf(ids, selfIDs[i], cfg.K)
			if i%10 == 0 || i == len(queries)-1 {
				emit(Progress{Phase: PhaseRecall, Done: i + 1, Total: len(queries),
					ExtraLabel: "ground truth"})
			}
		}
		_ = tx.Rollback(ctx)
		conn.Release()
	}

	// Sweep ef_search (or run once at server default).
	sweep := cfg.EfSearch
	if len(sweep) == 0 {
		sweep = []int{env.DefaultEfSearch}
	}
	var out RecallResult
	for _, ef := range sweep {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		pt, err := measureRecallAt(ctx, pool, env, cfg, annSQL, queries, selfIDs, groundTruth, ef, emit)
		if err != nil {
			return out, err
		}
		out.Points = append(out.Points, pt)
		emit(pt)
	}
	if len(out.Points) > 0 {
		out.Default = out.Points[0]
		for _, p := range out.Points {
			if env.DefaultEfSearch > 0 && p.EfSearch == env.DefaultEfSearch {
				out.Default = p
			}
		}
	}
	emit(out)
	emit(PhaseEnd{Phase: PhaseRecall,
		Summary: fmt.Sprintf("recall@%d %.3f @ ef_search=%d", cfg.K, out.Default.Recall, out.Default.EfSearch)})
	return out, nil
}

func measureRecallAt(
	ctx context.Context,
	pool *pgxpool.Pool,
	env *Env,
	cfg *Config,
	annSQL string,
	queries [][]float32,
	selfIDs []string,
	groundTruth [][]string,
	efSearch int,
	emit func(Event),
) (RecallPoint, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return RecallPoint{}, scrub(err)
	}
	defer conn.Release()

	// Wrap the whole level in one transaction and use SET LOCAL so ef_search is
	// honoured even when the target is reached through a transaction pooler
	// (e.g. PgBouncer/Supabase pooler), where a session-level SET on autocommit
	// queries can land on a different backend and be silently ignored.
	tx, err := conn.Begin(ctx)
	if err != nil {
		return RecallPoint{}, scrub(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck — read-only; rollback is the cleanup
	if efSearch > 0 && env.IndexType == "hnsw" {
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", efSearch)); err != nil {
			return RecallPoint{}, scrub(err)
		}
	}

	durs := make([]float64, 0, len(queries))
	hits := 0.0
	t0Wall := time.Now()
	for i, q := range queries {
		if ctx.Err() != nil {
			return RecallPoint{}, ctx.Err()
		}
		t0 := time.Now()
		ids, err := scanIDs(ctx, tx, annSQL, q)
		if err != nil {
			return RecallPoint{}, err
		}
		durs = append(durs, float64(time.Since(t0).Microseconds())/1000.0)
		hits += jaccardIntersection(dropSelf(ids, selfIDs[i], cfg.K), groundTruth[i])
		if i%10 == 0 || i == len(queries)-1 {
			emit(Progress{Phase: PhaseRecall, Done: i + 1, Total: len(queries),
				ExtraLabel: fmt.Sprintf("ef_search=%d", efSearch)})
		}
	}
	wall := time.Since(t0Wall).Seconds()
	sort.Float64s(durs)
	return RecallPoint{
		EfSearch: efSearch,
		Recall:   hits / float64(len(queries)),
		P95Ms:    pct(durs, 95),
		QPS:      float64(len(queries)) / wall,
	}, nil
}

// dropSelf removes the query row's own ctid (the distance-0 self-match) from a
// neighbour list and trims it to k, so recall measures the k real neighbours.
func dropSelf(ids []string, self string, k int) []string {
	out := make([]string, 0, len(ids))
	removed := false
	for _, id := range ids {
		if !removed && id == self {
			removed = true
			continue
		}
		out = append(out, id)
	}
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// jaccardIntersection returns |a ∩ b| / |b| — the per-query recall ratio.
func jaccardIntersection(a, b []string) float64 {
	set := make(map[string]struct{}, len(b))
	for _, id := range b {
		set[id] = struct{}{}
	}
	hit := 0
	for _, id := range a {
		if _, ok := set[id]; ok {
			hit++
		}
	}
	if len(b) == 0 {
		return 0
	}
	return float64(hit) / float64(len(b))
}

type conniface interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func scanIDs(ctx context.Context, c conniface, sql string, v []float32) ([]string, error) {
	rows, err := c.Query(ctx, sql, VectorLiteral(v))
	if err != nil {
		return nil, scrub(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, scrub(err)
		}
		ids = append(ids, s)
	}
	return ids, rows.Err()
}

func verifySeqScan(ctx context.Context, tx pgx.Tx, sql string, v []float32) (bool, string) {
	rows, err := tx.Query(ctx, "EXPLAIN "+sql, VectorLiteral(v))
	if err != nil {
		return true, "" // skip the check, don't fail the run
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		plan += line + "\n"
	}
	if containsAny(plan, "Index Scan", "Bitmap Index Scan", "Index Only Scan") {
		return false, plan
	}
	return true, ""
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) > 0 && indexOf(s, n) >= 0 {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func indexOf(s, sub string) int {
	if sub == "" {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

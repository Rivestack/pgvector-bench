package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const syntheticTable = "pgvbench_synth"

// generateSynthetic creates a temporary-ish table named syntheticTable on the
// target URL, stream-generates vectors in batches, and builds an HNSW index.
// It updates cfg.Table/Column to point at the new table so the regular pipeline
// runs unchanged.
func generateSynthetic(
	ctx context.Context,
	pool *pgxpool.Pool,
	cfg *Config,
	emit func(Event),
) error {
	emit(PhaseStart{Phase: PhaseSyntheticGen,
		Note: fmt.Sprintf("%d rows · dim %d", cfg.SyntheticRows, cfg.SyntheticDim)})

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return scrub(err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return scrub(err)
	}
	if _, err := conn.Exec(ctx, "DROP TABLE IF EXISTS "+syntheticTable); err != nil {
		return scrub(err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(
		"CREATE TABLE %s (id bigserial PRIMARY KEY, embedding vector(%d))",
		syntheticTable, cfg.SyntheticDim)); err != nil {
		return scrub(err)
	}

	// Batch via COPY (text format) for speed.
	const batchSize = 1000
	t0 := time.Now()
	for done := 0; done < cfg.SyntheticRows; {
		batch := batchSize
		if done+batch > cfg.SyntheticRows {
			batch = cfg.SyntheticRows - done
		}
		if err := copyBatch(ctx, conn.Conn(), batch, cfg.SyntheticDim); err != nil {
			return err
		}
		done += batch
		emit(Progress{Phase: PhaseSyntheticGen, Done: done, Total: cfg.SyntheticRows})
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	emit(PhaseEnd{Phase: PhaseSyntheticGen,
		Summary: fmt.Sprintf("inserted %d rows in %s", cfg.SyntheticRows, time.Since(t0).Round(time.Millisecond))})

	// Build HNSW index.
	emit(PhaseStart{Phase: PhaseIndexBuild, Note: "CREATE INDEX ... USING hnsw"})
	t1 := time.Now()
	if _, err := conn.Exec(ctx, fmt.Sprintf(
		"CREATE INDEX ON %s USING hnsw (embedding %s) WITH (m = 16, ef_construction = 64)",
		syntheticTable, cfg.Metric.HNSWOpClass())); err != nil {
		return scrub(err)
	}
	emit(PhaseEnd{Phase: PhaseIndexBuild,
		Summary: fmt.Sprintf("built in %s", time.Since(t1).Round(time.Millisecond))})

	cfg.Table = syntheticTable
	cfg.Column = "embedding"
	return nil
}

func copyBatch(ctx context.Context, conn *pgx.Conn, n, dim int) error {
	var sb strings.Builder
	sb.Grow(n * dim * 8)
	for i := 0; i < n; i++ {
		v := RandUnitVector(dim)
		sb.WriteString(VectorLiteral(v))
		sb.WriteByte('\n')
	}
	rdr := strings.NewReader(sb.String())
	_, err := conn.PgConn().CopyFrom(ctx, rdr, fmt.Sprintf("COPY %s (embedding) FROM STDIN", syntheticTable))
	if err != nil {
		return scrub(err)
	}
	return nil
}

// dropSynthetic removes the synthetic table from the target DB. Best-effort.
func dropSynthetic(ctx context.Context, pool *pgxpool.Pool) {
	_, _ = pool.Exec(ctx, "DROP TABLE IF EXISTS "+syntheticTable)
}

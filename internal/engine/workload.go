package engine

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sampleQueryVectors returns up to n vectors sampled from the target column
// using TABLESAMPLE if the table is large, otherwise ORDER BY random().
// The sampled vectors are used both as query points and (for recall sampling)
// as the basis for exact-KNN ground truth.
//
// It also returns each vector's source ctid (selfIDs, aligned with the vectors)
// so recall can exclude the trivial self-match: a query vector taken straight
// from the table has itself as its own nearest neighbour (distance 0), which
// would otherwise be a guaranteed "free" hit in both the index and the ground
// truth and inflate recall@k.
func sampleQueryVectors(ctx context.Context, pool *pgxpool.Pool, env *Env, n int) (vecs [][]float32, selfIDs []string, err error) {
	tbl := quoteIdent(env.Schema, env.Table)
	col := quoteIdent(env.Column)

	// TABLESAMPLE is fast but doesn't give exact n; over-fetch then trim.
	var q string
	if env.RowCount > 100_000 {
		// Pick a percentage that yields roughly 4n rows, capped at 100%.
		pct := float64(n*4) / float64(env.RowCount) * 100
		if pct < 0.1 {
			pct = 0.1
		}
		if pct > 100 {
			pct = 100
		}
		q = fmt.Sprintf(
			"SELECT ctid::text, %s::text FROM %s TABLESAMPLE SYSTEM (%.4f) WHERE %s IS NOT NULL LIMIT %d",
			col, tbl, pct, col, n)
	} else {
		q = fmt.Sprintf(
			"SELECT ctid::text, %s::text FROM %s WHERE %s IS NOT NULL ORDER BY random() LIMIT %d",
			col, tbl, col, n)
	}

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, nil, fmt.Errorf("sample vectors: %w", scrub(err))
	}
	defer rows.Close()

	vecs = make([][]float32, 0, n)
	selfIDs = make([]string, 0, n)
	for rows.Next() {
		var id, s string
		if err := rows.Scan(&id, &s); err != nil {
			return nil, nil, scrub(err)
		}
		v, err := ParseVectorLiteral(s)
		if err != nil {
			return nil, nil, err
		}
		vecs = append(vecs, v)
		selfIDs = append(selfIDs, id)
	}
	return vecs, selfIDs, rows.Err()
}

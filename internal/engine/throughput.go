package engine

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const throughputDuration = 8 * time.Second

// runThroughput ramps through cfg.Concurrency levels and reports sustained QPS
// at each. Each level runs for throughputDuration; the engine picks the peak
// level and notes saturation.
func runThroughput(
	ctx context.Context,
	pool *pgxpool.Pool,
	env *Env,
	cfg *Config,
	queries [][]float32,
	efSearch int,
	emit func(Event),
) (ThroughputResult, error) {
	emit(PhaseStart{Phase: PhaseThroughput,
		Note: fmt.Sprintf("ramp %v · %s each", cfg.Concurrency, throughputDuration)})

	tbl := quoteIdent(env.Schema, env.Table)
	col := quoteIdent(env.Column)
	op := cfg.Metric.Operator()
	sql := fmt.Sprintf("SELECT 1 FROM %s ORDER BY %s %s $1::vector LIMIT %d", tbl, col, op, cfg.K)

	var out ThroughputResult
	for _, conc := range cfg.Concurrency {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		lvl, err := runOneLevel(ctx, pool, env, sql, queries, conc, efSearch, emit)
		if err != nil {
			return out, err
		}
		out.Levels = append(out.Levels, lvl)
		emit(lvl)
		if lvl.QPS > out.PeakQPS {
			out.PeakQPS = lvl.QPS
			out.PeakLevel = lvl.Concurrency
		}
	}
	// Saturation = first level where QPS gain over prior is <10%.
	out.SaturatedAt = out.PeakLevel
	for i := 1; i < len(out.Levels); i++ {
		prev := out.Levels[i-1].QPS
		cur := out.Levels[i].QPS
		if prev > 0 && (cur-prev)/prev < 0.1 {
			out.SaturatedAt = out.Levels[i-1].Concurrency
			break
		}
	}
	emit(out)
	emit(PhaseEnd{Phase: PhaseThroughput,
		Summary: fmt.Sprintf("peak %.0f QPS @ concurrency=%d", out.PeakQPS, out.PeakLevel)})
	return out, nil
}

func runOneLevel(
	ctx context.Context,
	pool *pgxpool.Pool,
	env *Env,
	sql string,
	queries [][]float32,
	conc int,
	efSearch int,
	emit func(Event),
) (ThroughputLevel, error) {
	levelCtx, cancel := context.WithTimeout(ctx, throughputDuration+5*time.Second)
	defer cancel()

	var (
		ops      atomic.Int64
		started  = time.Now()
		deadline = started.Add(throughputDuration)
		wg       sync.WaitGroup
		mu       sync.Mutex
		durs     []float64
		firstErr error
	)
	wg.Add(conc)
	for w := 0; w < conc; w++ {
		go func(workerID int) {
			defer wg.Done()
			conn, err := pool.Acquire(levelCtx)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = scrub(err)
				}
				mu.Unlock()
				return
			}
			defer conn.Release()

			if efSearch > 0 && env.IndexType == "hnsw" {
				if _, err := conn.Exec(levelCtx, fmt.Sprintf("SET hnsw.ef_search = %d", efSearch)); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = scrub(err)
					}
					mu.Unlock()
					return
				}
			}

			localDurs := make([]float64, 0, 256)
			i := workerID
			for time.Now().Before(deadline) {
				if levelCtx.Err() != nil {
					break
				}
				q := queries[i%len(queries)]
				i++
				t0 := time.Now()
				rows, err := conn.Query(levelCtx, sql, VectorLiteral(q))
				if err != nil {
					if levelCtx.Err() == nil {
						mu.Lock()
						if firstErr == nil {
							firstErr = scrub(err)
						}
						mu.Unlock()
					}
					return
				}
				for rows.Next() {
				}
				rows.Close()
				localDurs = append(localDurs, float64(time.Since(t0).Microseconds())/1000.0)
				ops.Add(1)
			}
			mu.Lock()
			durs = append(durs, localDurs...)
			mu.Unlock()
		}(w)
	}

	// Live ticker for progress events.
	tickStop := make(chan struct{})
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-tickStop:
				return
			case <-t.C:
				elapsed := time.Since(started).Seconds()
				if elapsed <= 0 {
					continue
				}
				emit(Progress{
					Phase:      PhaseThroughput,
					Done:       int(time.Since(started).Milliseconds()),
					Total:      int(throughputDuration.Milliseconds()),
					LiveQPS:    float64(ops.Load()) / elapsed,
					ExtraLabel: fmt.Sprintf("c=%d", conc),
				})
			}
		}
	}()
	wg.Wait()
	close(tickStop)
	elapsed := time.Since(started)

	if firstErr != nil {
		return ThroughputLevel{}, firstErr
	}

	sort.Float64s(durs)
	qps := float64(ops.Load()) / elapsed.Seconds()
	return ThroughputLevel{
		Concurrency:  conc,
		QPS:          qps,
		P95Ms:        pct(durs, 95),
		Duration:     elapsed,
		TotalQueries: int(ops.Load()),
	}, nil
}

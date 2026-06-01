# pgvector-bench

Benchmark your existing pgvector setup. Measures **latency**, **throughput**,
and **recall** against your own Postgres + pgvector database, with a polished
animated terminal UI and a self-contained HTML report.

> **Your vectors and connection details never leave your machine.**
> No telemetry, no signup, no outbound calls. The binary opens connections
> to the Postgres URL you pass — nothing else.

```
brew install rivestack/tap/pgvector-bench
# or
curl -fsSL https://rivestack.io/install.sh | sh
# or
go install github.com/Rivestack/pgvector-bench/cmd/pgvector-bench@latest
```

## Quickstart

```bash
# Benchmark an existing table
pgvector-bench run \
  --url postgres://user:pass@host:5432/db \
  --table documents --column embedding --metric cosine

# No data yet? Generate a synthetic dataset on the target DB
pgvector-bench run \
  --url postgres://... \
  --synthetic --rows 100000 --dim 1536

# Sweep ef_search to see the recall/latency tradeoff
pgvector-bench run --url ... --table documents --column embedding \
  --ef-search 40,100,200

# Headless: JSON to stdout
pgvector-bench run --url ... --table documents --column embedding --json
```

## What it measures

| Metric          | How                                                                                  |
| --------------- | ------------------------------------------------------------------------------------ |
| **Latency** p50/p95/p99 | Single-threaded, server-side round-trip timed inside the worker goroutine.   |
| **Throughput**  | Ramp through `--concurrency` levels (default `1,8,32`); each level runs 8s on a `pgxpool` worker pool. Reports sustained QPS per level and the peak. |
| **Recall@k**    | Computes exact ground-truth via sequential scan inside a transaction with `enable_indexscan`/`enable_indexonlyscan`/`enable_bitmapscan` off, then compares to the indexed (ANN) results. The plan is verified with `EXPLAIN` once — if the planner refuses to seq-scan, recall is skipped rather than misreported. |
| **ef_search sweep** | For each `--ef-search` value, repeats the recall measurement and reports `(ef_search, recall, p95, qps)` so you can see your own speed/quality tradeoff. |

Sample output (real run, US machine against EU DB — high p50 is network RTT):

```
✓ Connect — connected
✓ Synthetic gen — inserted 5000 rows in 1.924s
✓ Index build — built in 1.648s
✓ Introspect — public.pgvbench_synth · hnsw index
    · Postgres                         PostgreSQL 17.10
    · pgvector                         0.8.1
    · table                            public.pgvbench_synth (5,000 rows, 17.8 MiB)
    · column                           embedding vector(384)
    · index                            hnsw m=16 ef_construction=64 · 9.8 MiB
    · shared_buffers                   1GB
    · effective_cache_size             3GB
    · hnsw.ef_search                   100
✓ Latency — p50 39.1 ms · p95 40.8 ms · p99 41.8 ms
✓ Throughput — peak 69 QPS @ concurrency=4
    · c=1         11 QPS  p95 401.2 ms
    · c=4         69 QPS  p95 124.8 ms
✓ Recall — recall@10 1.000 @ ef_search=100

─── Measured on your DB ─────────────────────────────
  p50        39.1 ms
  p95        40.8 ms
  p99        41.8 ms
  peak QPS     69  @ concurrency=4
  recall    1.000 @ ef_search=100

─── Rivestack NVMe (projected) ──────────────────────
  No reference benchmark for this workload shape.
  Get a free workload review: https://rivestack.io/switch

→ See your projected setup on dedicated NVMe:
  https://rivestack.io/switch?...
```

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--url` | — | Postgres connection string (required) |
| `--table`, `--column` | — | Target table and vector column |
| `--synthetic` | off | Generate `--rows × --dim` random vectors on the target DB and benchmark them |
| `--rows`, `--dim` | 100000, 1536 | Synthetic dataset size |
| `--metric` | cosine | `cosine` &#124; `l2` &#124; `ip` |
| `--k` | 10 | Neighbors per query |
| `--queries` | 1000 | Benchmark queries |
| `--concurrency` | 1,8,32 | Comma-separated concurrency ramp |
| `--ef-search` | server default | Comma-separated HNSW `ef_search` sweep |
| `--recall-sample` | 200 | Queries used for exact-KNN ground truth |
| `--report` | none | `json` &#124; `html` &#124; `md` &#124; `both` &#124; `all` |
| `--out` | auto | Output path prefix for `--report` |
| `--json` | off | Emit JSON to stdout, suppress TUI |
| `--plain` | auto | Force plain output (auto when stdout is not a TTY) |
| `--no-color` | off | `NO_COLOR` env honored |

## Methodology — read this before you tweet

This tool tries hard to report what your **database** can do, not what the
benchmark client can do.

- **Throughput is measured with a goroutine worker pool over `pgxpool`.**
  Each worker holds one Postgres connection for the duration of the level
  and submits queries back-to-back. Reported QPS is queries-completed /
  wall-clock at each concurrency level. The peak is whatever the last level
  achieved before QPS gain over the previous level dropped below 10 %.
- **Latency is captured inside the worker goroutine**, not in the UI thread.
  The animated terminal UI and `--json` mode print the *same* numbers.
- **Recall ground truth.** For `--recall-sample` queries we open a
  transaction and `SET LOCAL enable_indexscan = off; enable_indexonlyscan = off;
  enable_bitmapscan = off`, then run `ORDER BY col <metric> $1 LIMIT k`. We
  verify with `EXPLAIN` on the first query that the planner is seq-scanning;
  if it still picks the index (rare, configuration-dependent) we skip recall
  rather than report a misleading number.
- **What we don't claim.** We don't try to detect NVMe-vs-SSD over the wire.
  We don't subtract network RTT — if your DB is across the Atlantic, your
  p95 reflects that. We don't run with prepared statements (yet) — plain
  parametrized queries, same as a typical app uses.
- **Don't write to the table during a run.** Recall identity is `ctid`,
  which is stable for the duration of a run but changes under `VACUUM FULL`
  or concurrent writes.

## How the NVMe projection works

When the run finishes we look up the nearest bucket from a small,
human-curated set of reference benchmarks that Rivestack has measured on
dedicated NVMe nodes, keyed by `(dim, rows, ef_search, index)`. If a bucket
is close enough to your workload shape, we show its numbers side-by-side
with yours, **clearly labeled as a projection**. If not, we print "No
reference benchmark for this workload shape" — we never invent numbers.

The reference file is bundled into the binary at build time and is also
hosted at `https://rivestack.io/reference.json` so the `/switch` web
calculator runs the identical nearest-bucket logic. Same data, two
surfaces. Methodology lives at
`https://rivestack.io/blog/pgvector-nvme-benchmark`.

> The reference set ships intentionally **empty** in early releases —
> until Rivestack publishes measured numbers. The "no projection" path is
> the honest default; please do not open PRs adding speculative buckets.

## Privacy

- The binary opens a Postgres connection to the `--url` you pass. Nothing
  else egresses, ever. `grep net/http` in the source returns no hits.
- The reference data used for projections is embedded in the binary, so the
  projection runs fully offline.
- Errors are scrubbed of connection strings, hostnames, and IPs before
  reaching stderr.

## Exports

- `--json` writes structured results to stdout for scripting / CI.
- `--report html` writes a single self-contained HTML file (CSS inlined via
  `go:embed`, no external requests when opened) — itself shareable.
- `--report md` writes a copy-paste-ready Markdown block for Reddit / HN.
- `--report both` writes JSON + HTML; `--report all` writes all three.

## Stretch / future

- `--compare-url` for head-to-head benchmarks of two DBs.
- A GitHub Action wrapper so teams catch recall/latency regressions in CI.

## License

MIT. See [LICENSE](./LICENSE).

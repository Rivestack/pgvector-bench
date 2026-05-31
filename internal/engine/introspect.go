package engine

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Env struct {
	PGVersion        string `json:"pg_version"`
	PGVectorVersion  string `json:"pgvector_version"`
	Schema           string `json:"schema"`
	Table            string `json:"table"`
	Column           string `json:"column"`
	Dim              int    `json:"dim"`
	RowCount         int64  `json:"row_count"`
	TableSizeBytes   int64  `json:"table_size_bytes"`
	IndexName        string `json:"index_name,omitempty"`
	IndexType        string `json:"index_type,omitempty"` // hnsw|ivfflat|none
	IndexOpClass     string `json:"index_opclass,omitempty"`
	IndexM           int    `json:"index_m,omitempty"`
	IndexEfConstruct int    `json:"index_ef_construction,omitempty"`
	IndexLists       int    `json:"index_lists,omitempty"`
	IndexSizeBytes   int64  `json:"index_size_bytes,omitempty"`

	ServerSettings map[string]string `json:"server_settings"`

	DefaultEfSearch int `json:"default_ef_search,omitempty"`
	DefaultProbes   int `json:"default_ivfflat_probes,omitempty"`
}

func introspect(ctx context.Context, pool *pgxpool.Pool, cfg *Config) (*Env, error) {
	env := &Env{ServerSettings: map[string]string{}}

	if err := pool.QueryRow(ctx, "SELECT version()").Scan(&env.PGVersion); err != nil {
		return nil, fmt.Errorf("query pg version: %w", scrub(err))
	}
	if err := pool.QueryRow(ctx,
		"SELECT extversion FROM pg_extension WHERE extname='vector'").Scan(&env.PGVectorVersion); err != nil {
		return nil, fmt.Errorf("pgvector extension not installed: %w", scrub(err))
	}

	// Resolve schema/table/column (caller may pass "schema.table" in Table).
	schema, table := splitSchemaTable(cfg.Table)
	env.Schema = schema
	env.Table = table
	env.Column = cfg.Column

	// Dim of the vector column.
	const dimQ = `
		SELECT a.atttypmod
		FROM pg_attribute a
		JOIN pg_class c ON c.oid=a.attrelid
		JOIN pg_namespace n ON n.oid=c.relnamespace
		WHERE n.nspname=$1 AND c.relname=$2 AND a.attname=$3 AND NOT a.attisdropped`
	var mod int
	if err := pool.QueryRow(ctx, dimQ, schema, table, cfg.Column).Scan(&mod); err != nil {
		return nil, fmt.Errorf("vector column %s.%s.%s not found: %w", schema, table, cfg.Column, scrub(err))
	}
	env.Dim = mod // pgvector stores dim directly in atttypmod

	// Row count + sizes.
	pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s", quoteIdent(schema, table))).Scan(&env.RowCount)
	pool.QueryRow(ctx,
		"SELECT pg_total_relation_size($1)",
		fmt.Sprintf("%s.%s", schema, table)).Scan(&env.TableSizeBytes)

	// Look for an HNSW/IVFFLAT index on this column.
	const idxQ = `
		SELECT ic.relname,
		       am.amname,
		       pg_relation_size(ic.oid),
		       pg_get_indexdef(i.indexrelid)
		FROM pg_index i
		JOIN pg_class c ON c.oid=i.indrelid
		JOIN pg_class ic ON ic.oid=i.indexrelid
		JOIN pg_am am ON am.oid=ic.relam
		JOIN pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_attribute a ON a.attrelid=i.indrelid
		  AND a.attnum = ANY(i.indkey)
		WHERE am.amname IN ('hnsw','ivfflat')
		  AND n.nspname=$1 AND c.relname=$2 AND a.attname=$3
		LIMIT 1`
	var iname, iam, idef string
	var isize int64
	if err := pool.QueryRow(ctx, idxQ, schema, table, cfg.Column).Scan(&iname, &iam, &isize, &idef); err == nil {
		env.IndexName = iname
		env.IndexType = iam
		env.IndexSizeBytes = isize
		env.IndexM, env.IndexEfConstruct, env.IndexLists, env.IndexOpClass = parseIndexDef(idef)
	} else {
		env.IndexType = "none"
	}

	// Perf-relevant server settings.
	for _, name := range []string{
		"shared_buffers", "work_mem", "maintenance_work_mem",
		"effective_cache_size", "max_parallel_workers_per_gather",
		"hnsw.ef_search", "ivfflat.probes",
	} {
		var v string
		if err := pool.QueryRow(ctx, "SHOW "+name).Scan(&v); err == nil {
			env.ServerSettings[name] = v
		}
	}
	if v, ok := env.ServerSettings["hnsw.ef_search"]; ok {
		env.DefaultEfSearch = atoiSafe(v)
	}
	if v, ok := env.ServerSettings["ivfflat.probes"]; ok {
		env.DefaultProbes = atoiSafe(v)
	}

	return env, nil
}

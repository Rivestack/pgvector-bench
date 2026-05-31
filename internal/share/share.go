// Package share implements --share: POSTs an aggregate-only payload to
// the Rivestack API and returns the hosted page id. Nothing else egresses;
// see PayloadFrom for the exact shape allowed.
package share

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Rivestack/pgvector-bench/internal/engine"
	"github.com/Rivestack/pgvector-bench/internal/report"
)

// Payload is exactly the spec's §10.4 shape. Adding fields here is a
// privacy decision and must be reviewed.
type Payload struct {
	Dim             int     `json:"dim"`
	RowsBucket      string  `json:"rows_bucket"`
	Metric          string  `json:"metric"`
	K               int     `json:"k"`
	Index           string  `json:"index"`
	M               int     `json:"m,omitempty"`
	EfConstruction  int     `json:"ef_construction,omitempty"`
	EfSearch        int     `json:"ef_search,omitempty"`
	P50Ms           float64 `json:"p50_ms"`
	P95Ms           float64 `json:"p95_ms"`
	P99Ms           float64 `json:"p99_ms"`
	QPSPeak         float64 `json:"qps_peak"`
	Recall          float64 `json:"recall"`
	PGVersion       string  `json:"pg_version"`
	PGVectorVersion string  `json:"pgvector_version"`
	ToolVersion     string  `json:"tool_version"`
}

func PayloadFrom(out *report.Output) Payload {
	r := out.Result
	return Payload{
		Dim:             r.Env.Dim,
		RowsBucket:      engine.RowsBucket(r.Env.RowCount),
		Metric:          string(r.Config.Metric),
		K:               r.Config.K,
		Index:           r.Env.IndexType,
		M:               r.Env.IndexM,
		EfConstruction:  r.Env.IndexEfConstruct,
		EfSearch:        r.Recall.Default.EfSearch,
		P50Ms:           r.Latency.P50Ms,
		P95Ms:           r.Latency.P95Ms,
		P99Ms:           r.Latency.P99Ms,
		QPSPeak:         r.Throughput.PeakQPS,
		Recall:          r.Recall.Default.Recall,
		PGVersion:       shortVersion(r.Env.PGVersion),
		PGVectorVersion: r.Env.PGVectorVersion,
		ToolVersion:     r.ToolVersion,
	}
}

type apiResp struct {
	ID string `json:"id"`
}

// Submit POSTs the aggregate payload to the share endpoint and returns the
// hosted page id. RIVESTACK_API may override the default URL for testing.
func Submit(ctx context.Context, out *report.Output) (string, error) {
	endpoint := os.Getenv("RIVESTACK_API")
	if endpoint == "" {
		endpoint = "https://api.rivestack.io/v1/bench"
	}
	body, err := json.Marshal(PayloadFrom(out))
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "pgvector-bench/"+out.Result.ToolVersion)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("share endpoint returned %d", resp.StatusCode)
	}
	var ar apiResp
	if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
		return "", err
	}
	if ar.ID == "" {
		return "", errors.New("share endpoint returned empty id")
	}
	return ar.ID, nil
}

func shortVersion(s string) string {
	for i := 0; i+4 < len(s); i++ {
		if s[i] == ' ' && s[i+1] == 'o' && s[i+2] == 'n' && s[i+3] == ' ' {
			return s[:i]
		}
	}
	return s
}

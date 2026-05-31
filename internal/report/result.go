package report

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/Rivestack/pgvector-bench/internal/engine"
	"github.com/Rivestack/pgvector-bench/internal/projection"
)

// Output is the canonical, shareable shape — what JSON/HTML reports render
// and what --share posts (filtered to the aggregate-only subset).
type Output struct {
	Result     *engine.Result      `json:"result"`
	Projection *projection.Nearest `json:"projection,omitempty"`
	SwitchURL  string              `json:"switch_url"`
	Reference  ReferenceInfo       `json:"reference"`
	Privacy    PrivacyInfo         `json:"privacy"`
}

type ReferenceInfo struct {
	GeneratedAt string `json:"generated_at"`
	MethodURL   string `json:"method_url"`
	BucketCount int    `json:"bucket_count"`
}

type PrivacyInfo struct {
	Note string `json:"note"`
}

// Build assembles the Output from an engine.Result and the reference data.
func Build(res *engine.Result, ref *projection.Reference) *Output {
	q := projection.Query{
		Dim:      res.Env.Dim,
		Rows:     res.Env.RowCount,
		Index:    res.Env.IndexType,
		EfSearch: res.Recall.Default.EfSearch,
	}
	if q.EfSearch == 0 {
		q.EfSearch = res.Env.DefaultEfSearch
	}
	near := ref.Find(q)

	out := &Output{
		Result:    res,
		SwitchURL: BuildSwitchURL(res),
		Reference: ReferenceInfo{
			GeneratedAt: ref.GeneratedAt,
			MethodURL:   ref.MethodURL,
			BucketCount: len(ref.Buckets),
		},
		Privacy: PrivacyInfo{
			Note: "Your vectors and connection details never leave your machine. " +
				"This report contains aggregate metrics only.",
		},
	}
	if near.Found {
		out.Projection = &near
	}
	return out
}

// BuildSwitchURL builds the deep link to rivestack.io/switch carrying only
// aggregate workload-shape parameters — never the connection string,
// hostname, table name, or any vector content.
func BuildSwitchURL(res *engine.Result) string {
	u, _ := url.Parse("https://rivestack.io/switch")
	q := url.Values{}
	q.Set("dims", strconv.Itoa(res.Env.Dim))
	q.Set("rows_bucket", engine.RowsBucket(res.Env.RowCount))
	q.Set("metric", string(res.Config.Metric))
	q.Set("p95", fmt.Sprintf("%.1f", res.Latency.P95Ms))
	q.Set("qps", fmt.Sprintf("%.0f", res.Throughput.PeakQPS))
	q.Set("recall", fmt.Sprintf("%.3f", res.Recall.Default.Recall))
	q.Set("index", res.Env.IndexType)
	q.Set("source", "cli")
	u.RawQuery = q.Encode()
	return u.String()
}

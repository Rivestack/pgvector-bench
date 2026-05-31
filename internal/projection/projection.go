// Package projection loads Rivestack's measured NVMe reference benchmarks
// from an embedded JSON file and finds the nearest bucket to a user's
// observed workload shape. The same reference.json is also hosted at
// rivestack.io/reference.json so the /switch web calculator returns the
// identical projection.
//
// Honesty rule: if no bucket is within tolerance, we return Nearest{Found:false}
// and the caller must NOT print a projection — only the "no reference benchmark
// for this workload shape" message.
package projection

import (
	_ "embed"
	"encoding/json"
	"math"
)

//go:embed reference.json
var rawReference []byte

type Bucket struct {
	Dim            int     `json:"dim"`
	Rows           int64   `json:"rows"`
	Index          string  `json:"index"`
	M              int     `json:"m"`
	EfConstruction int     `json:"ef_construction"`
	EfSearch       int     `json:"ef_search"`
	QPS            float64 `json:"qps"`
	P50Ms          float64 `json:"p50_ms"`
	P95Ms          float64 `json:"p95_ms"`
	Recall         float64 `json:"recall"`
}

type Reference struct {
	GeneratedAt string   `json:"generated_at"`
	MethodURL   string   `json:"method_url"`
	Buckets     []Bucket `json:"buckets"`
}

type Query struct {
	Dim      int
	Rows     int64
	Index    string
	EfSearch int
}

type Nearest struct {
	Found        bool
	Bucket       Bucket
	DistanceNorm float64
}

func Load() (*Reference, error) {
	var r Reference
	if err := json.Unmarshal(rawReference, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// MaxNorm sets the maximum normalized distance for a bucket to be considered
// "close enough". 0.5 means each dimension can differ by ~50% before we refuse
// to project. Tuned conservatively — we'd rather print "no projection" than
// mislead.
const MaxNorm = 0.5

// Find returns the nearest bucket whose normalized distance is within MaxNorm,
// or {Found:false} otherwise.
func (r *Reference) Find(q Query) Nearest {
	best := Nearest{}
	bestD := math.Inf(1)
	for _, b := range r.Buckets {
		if q.Index != "" && b.Index != q.Index {
			continue
		}
		d := dist(b, q)
		if d < bestD {
			bestD = d
			best = Nearest{Found: true, Bucket: b, DistanceNorm: d}
		}
	}
	if !best.Found || bestD > MaxNorm {
		return Nearest{}
	}
	return best
}

func dist(b Bucket, q Query) float64 {
	d := 0.0
	d += relDiff(float64(b.Dim), float64(q.Dim))
	d += relDiff(float64(b.Rows), float64(q.Rows))
	if q.EfSearch > 0 && b.EfSearch > 0 {
		d += relDiff(float64(b.EfSearch), float64(q.EfSearch))
	}
	return d / 3
}

func relDiff(a, b float64) float64 {
	denom := math.Max(math.Abs(a), math.Abs(b))
	if denom == 0 {
		return 0
	}
	return math.Abs(a-b) / denom
}

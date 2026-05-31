package engine

import "time"

type Phase string

const (
	PhaseConnect      Phase = "connect"
	PhaseIntrospect   Phase = "introspect"
	PhaseSyntheticGen Phase = "synthetic_gen"
	PhaseIndexBuild   Phase = "index_build"
	PhaseWarmup       Phase = "warmup"
	PhaseLatency      Phase = "latency"
	PhaseThroughput   Phase = "throughput"
	PhaseRecall       Phase = "recall"
	PhaseDone         Phase = "done"
)

// Event is the sum type of values emitted by the engine on its event channel.
type Event interface{ isEvent() }

type PhaseStart struct {
	Phase Phase
	Note  string
}

func (PhaseStart) isEvent() {}

type PhaseEnd struct {
	Phase   Phase
	Summary string
}

func (PhaseEnd) isEvent() {}

type Progress struct {
	Phase      Phase
	Done       int
	Total      int
	LiveP50Ms  float64
	LiveP95Ms  float64
	LiveQPS    float64
	ExtraLabel string
}

func (Progress) isEvent() {}

type IntrospectFact struct {
	Key   string
	Value string
}

func (IntrospectFact) isEvent() {}

type LatencyResult struct {
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
	MeanMs float64 `json:"mean_ms"`
	Count  int     `json:"count"`
}

func (LatencyResult) isEvent() {}

type ThroughputLevel struct {
	Concurrency  int           `json:"concurrency"`
	QPS          float64       `json:"qps"`
	P95Ms        float64       `json:"p95_ms"`
	Duration     time.Duration `json:"duration_ns"`
	TotalQueries int           `json:"total_queries"`
}

func (ThroughputLevel) isEvent() {}

type ThroughputResult struct {
	Levels      []ThroughputLevel `json:"levels"`
	PeakQPS     float64           `json:"peak_qps"`
	PeakLevel   int               `json:"peak_level"`
	SaturatedAt int               `json:"saturated_at"`
}

func (ThroughputResult) isEvent() {}

type RecallPoint struct {
	EfSearch int     `json:"ef_search"`
	Recall   float64 `json:"recall"`
	P95Ms    float64 `json:"p95_ms"`
	QPS      float64 `json:"qps"`
}

func (RecallPoint) isEvent() {}

type RecallResult struct {
	Points  []RecallPoint `json:"points"`
	Default RecallPoint   `json:"default"`
}

func (RecallResult) isEvent() {}

type Warn struct {
	Message string
}

func (Warn) isEvent() {}

type Fatal struct {
	Err error
}

func (Fatal) isEvent() {}

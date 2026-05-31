package engine

import "errors"

type Metric string

const (
	MetricCosine Metric = "cosine"
	MetricL2     Metric = "l2"
	MetricIP     Metric = "ip"
)

// Operator returns the pgvector distance operator for this metric.
func (m Metric) Operator() string {
	switch m {
	case MetricL2:
		return "<->"
	case MetricIP:
		return "<#>"
	default:
		return "<=>"
	}
}

// HNSWOpClass returns the pgvector index opclass for this metric.
func (m Metric) HNSWOpClass() string {
	switch m {
	case MetricL2:
		return "vector_l2_ops"
	case MetricIP:
		return "vector_ip_ops"
	default:
		return "vector_cosine_ops"
	}
}

type Config struct {
	URL          string
	Table        string
	Column       string
	Metric       Metric
	K            int
	Queries      int
	Concurrency  []int
	EfSearch     []int // empty => use server default
	RecallSample int
	WarmupCount  int

	Synthetic     bool
	SyntheticRows int
	SyntheticDim  int
}

func (c *Config) Validate() error {
	if c.URL == "" {
		return errors.New("--url required")
	}
	if !c.Synthetic && (c.Table == "" || c.Column == "") {
		return errors.New("--table and --column required unless --synthetic")
	}
	if c.Metric == "" {
		c.Metric = MetricCosine
	}
	switch c.Metric {
	case MetricCosine, MetricL2, MetricIP:
	default:
		return errors.New("--metric must be cosine|l2|ip")
	}
	if c.K <= 0 {
		c.K = 10
	}
	if c.Queries <= 0 {
		c.Queries = 1000
	}
	if len(c.Concurrency) == 0 {
		c.Concurrency = []int{1, 8, 32}
	}
	if c.RecallSample <= 0 {
		c.RecallSample = 200
	}
	if c.WarmupCount <= 0 {
		c.WarmupCount = 50
	}
	if c.Synthetic {
		if c.SyntheticRows <= 0 {
			c.SyntheticRows = 100000
		}
		if c.SyntheticDim <= 0 {
			c.SyntheticDim = 1536
		}
	}
	return nil
}

// MaxConcurrency returns the largest level in the concurrency list (or 1).
func (c *Config) MaxConcurrency() int {
	m := 1
	for _, n := range c.Concurrency {
		if n > m {
			m = n
		}
	}
	return m
}

package engine

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// VectorLiteral renders a float32 slice as a pgvector literal string ("[1,2,3]").
func VectorLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 8)
	b.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// ParseVectorLiteral parses a pgvector text-format vector "[1,2,3]".
func ParseVectorLiteral(s string) ([]float32, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("not a vector literal: %q", s)
	}
	s = s[1 : len(s)-1]
	if s == "" {
		return []float32{}, nil
	}
	parts := strings.Split(s, ",")
	out := make([]float32, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil {
			return nil, err
		}
		out[i] = float32(f)
	}
	return out, nil
}

// RandUnitVector samples a uniformly random vector on the unit sphere.
func RandUnitVector(dim int) []float32 {
	v := make([]float32, dim)
	buf := make([]byte, 4)
	var sum float64
	for i := 0; i < dim; i++ {
		_, _ = rand.Read(buf)
		u1 := float64(binary.LittleEndian.Uint32(buf))/float64(math.MaxUint32) + 1e-12
		_, _ = rand.Read(buf)
		u2 := float64(binary.LittleEndian.Uint32(buf)) / float64(math.MaxUint32)
		z := math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
		v[i] = float32(z)
		sum += z * z
	}
	norm := math.Sqrt(sum)
	if norm < 1e-12 {
		return v
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / norm)
	}
	return v
}

// Distance returns the metric distance between two equal-length vectors,
// matching pgvector's operator semantics (cosine, l2, ip).
func Distance(a, b []float32, m Metric) float64 {
	switch m {
	case MetricL2:
		var s float64
		for i := range a {
			d := float64(a[i]) - float64(b[i])
			s += d * d
		}
		return math.Sqrt(s)
	case MetricIP:
		// pgvector's <#> returns negative inner product.
		var s float64
		for i := range a {
			s += float64(a[i]) * float64(b[i])
		}
		return -s
	default: // cosine
		var dot, na, nb float64
		for i := range a {
			dot += float64(a[i]) * float64(b[i])
			na += float64(a[i]) * float64(a[i])
			nb += float64(b[i]) * float64(b[i])
		}
		if na == 0 || nb == 0 {
			return 1.0
		}
		return 1 - dot/(math.Sqrt(na)*math.Sqrt(nb))
	}
}

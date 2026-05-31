package engine

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// scrub strips connection strings, hostnames, IPs, and ports out of pgx
// error messages so we never leak credentials or location info into logs,
// stderr, or share payloads.
var (
	scrubURL  = regexp.MustCompile(`postgres(?:ql)?://[^\s'"]+`)
	scrubHost = regexp.MustCompile(`[a-zA-Z0-9][a-zA-Z0-9._-]*\.(?:[a-zA-Z]{2,}|[a-zA-Z]+\.[a-zA-Z]{2,})(?::\d+)?`)
	scrubIPv4 = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d+)?\b`)
	scrubDBUR = regexp.MustCompile("(?i)`user=[^`]*`")
)

func scrub(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	s = scrubURL.ReplaceAllString(s, "postgres://<redacted>")
	s = scrubIPv4.ReplaceAllString(s, "<ip>")
	s = scrubHost.ReplaceAllString(s, "<host>")
	s = scrubDBUR.ReplaceAllString(s, "`<conn>`")
	return errors.New(s)
}

// RedactURL returns the URL with the password removed, safe to print.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<redacted>"
	}
	if u.User != nil {
		if _, set := u.User.Password(); set {
			u.User = url.UserPassword(u.User.Username(), "***")
		}
	}
	return u.String()
}

func splitSchemaTable(qualified string) (schema, table string) {
	if i := strings.IndexByte(qualified, '.'); i >= 0 {
		return qualified[:i], qualified[i+1:]
	}
	return "public", qualified
}

func quoteIdent(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, `"`+strings.ReplaceAll(p, `"`, `""`)+`"`)
	}
	return strings.Join(out, ".")
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

var (
	reM     = regexp.MustCompile(`(?i)\bm\s*=\s*'?(\d+)'?`)
	reEFC   = regexp.MustCompile(`(?i)ef_construction\s*=\s*'?(\d+)'?`)
	reLists = regexp.MustCompile(`(?i)\blists\s*=\s*'?(\d+)'?`)
	reOp    = regexp.MustCompile(`(?i)\b(vector_(?:cosine|l2|ip)_ops|halfvec_\w+_ops|bit_\w+_ops|sparsevec_\w+_ops)\b`)
)

func parseIndexDef(def string) (m, efc, lists int, opclass string) {
	if x := reM.FindStringSubmatch(def); x != nil {
		m, _ = strconv.Atoi(x[1])
	}
	if x := reEFC.FindStringSubmatch(def); x != nil {
		efc, _ = strconv.Atoi(x[1])
	}
	if x := reLists.FindStringSubmatch(def); x != nil {
		lists, _ = strconv.Atoi(x[1])
	}
	if x := reOp.FindStringSubmatch(def); x != nil {
		opclass = x[1]
	}
	return
}

// RowsBucket maps an exact row count to a human bucket (1k/10k/100k/1M/...).
func RowsBucket(n int64) string {
	switch {
	case n < 1_000:
		return "<1k"
	case n < 10_000:
		return "1k"
	case n < 100_000:
		return "10k"
	case n < 1_000_000:
		return "100k"
	case n < 10_000_000:
		return "1M"
	case n < 100_000_000:
		return "10M"
	default:
		return ">100M"
	}
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

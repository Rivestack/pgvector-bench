package tui

import "github.com/charmbracelet/lipgloss"

var (
	colAccent    = lipgloss.Color("#7c3aed")
	colAccent2   = lipgloss.Color("#06b6d4")
	colMuted     = lipgloss.Color("#94a3b8")
	colDim       = lipgloss.Color("#64748b")
	colSuccess   = lipgloss.Color("#22c55e")
	colWarn      = lipgloss.Color("#f59e0b")
	colDanger    = lipgloss.Color("#ef4444")
	colHighlight = lipgloss.Color("#10b981")
	colBarFg     = lipgloss.Color("#a78bfa")
	colBarBg     = lipgloss.Color("#1f2937")
)

var (
	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAccent)
	styleSubtitle = lipgloss.NewStyle().
			Foreground(colMuted)
	styleOK = lipgloss.NewStyle().
		Foreground(colSuccess).
		Bold(true)
	styleWarn = lipgloss.NewStyle().
			Foreground(colWarn).
			Bold(true)
	styleErr = lipgloss.NewStyle().
			Foreground(colDanger).
			Bold(true)
	styleKey = lipgloss.NewStyle().
			Foreground(colDim)
	styleVal = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e5e7eb"))
	styleSection = lipgloss.NewStyle().
			Foreground(colMuted).
			Bold(true)
	styleMuted = lipgloss.NewStyle().
			Foreground(colMuted)
	stylePeak = lipgloss.NewStyle().
			Foreground(colHighlight).
			Bold(true)
	styleBigNum = lipgloss.NewStyle().
			Foreground(colAccent2).
			Bold(true)
	styleProjection = lipgloss.NewStyle().
			Foreground(colHighlight).
			Bold(true)
	styleCard = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colDim).
			Padding(0, 1)
	styleCardHL = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colHighlight).
			Padding(0, 1)
)

// gradientText paints `s` with a horizontal gradient between two colors.
// Used for the wordmark.
func gradientText(s string, from, to lipgloss.Color) string {
	if len(s) == 0 {
		return s
	}
	// lipgloss doesn't ship a per-rune gradient helper, so do it manually.
	runes := []rune(s)
	n := len(runes)
	out := ""
	fr, fg, fb := hex(from)
	tr, tg, tb := hex(to)
	for i, r := range runes {
		t := float64(i) / float64(max(1, n-1))
		rr := int(float64(fr) + t*(float64(tr)-float64(fr)))
		gg := int(float64(fg) + t*(float64(tg)-float64(fg)))
		bb := int(float64(fb) + t*(float64(tb)-float64(fb)))
		col := lipgloss.Color(rgbHex(rr, gg, bb))
		out += lipgloss.NewStyle().Foreground(col).Bold(true).Render(string(r))
	}
	return out
}

func hex(c lipgloss.Color) (int, int, int) {
	s := string(c)
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0
	}
	r := hex2(s[1:3])
	g := hex2(s[3:5])
	b := hex2(s[5:7])
	return r, g, b
}
func hex2(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		}
		n = n*16 + d
	}
	return n
}
func rgbHex(r, g, b int) string {
	const h = "0123456789abcdef"
	clamp := func(x int) int {
		if x < 0 {
			return 0
		}
		if x > 255 {
			return 255
		}
		return x
	}
	r, g, b = clamp(r), clamp(g), clamp(b)
	return string([]byte{'#', h[r>>4], h[r&15], h[g>>4], h[g&15], h[b>>4], h[b&15]})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

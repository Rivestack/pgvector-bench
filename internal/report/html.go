package report

import (
	_ "embed"
	"html/template"
	"io"
)

//go:embed template.html
var htmlTmpl string

//go:embed style.css
var htmlCSS string

type htmlData struct {
	*Output
	CSS template.CSS
}

func WriteHTML(w io.Writer, out *Output) error {
	t, err := template.New("report").Funcs(template.FuncMap{
		"pct": func(f float64) string {
			return formatPercent(f)
		},
	}).Parse(htmlTmpl)
	if err != nil {
		return err
	}
	return t.Execute(w, htmlData{Output: out, CSS: template.CSS(htmlCSS)})
}

func formatPercent(f float64) string {
	return formatFloat1(f*100) + "%"
}

func formatFloat1(f float64) string {
	// avoid pulling fmt for one helper
	if f != f { // NaN
		return "—"
	}
	out := []byte{}
	if f < 0 {
		out = append(out, '-')
		f = -f
	}
	whole := int64(f)
	out = append(out, []byte(intToString(whole))...)
	out = append(out, '.')
	frac := int64((f - float64(whole)) * 10)
	out = append(out, byte('0'+frac))
	return string(out)
}

func intToString(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

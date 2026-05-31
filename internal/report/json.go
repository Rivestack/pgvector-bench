package report

import (
	"encoding/json"
	"io"
)

func WriteJSON(w io.Writer, out *Output) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

package output

import (
	"encoding/json"
	"io"
)

// Printer writes command results. The CLI is agent-primary and emits JSON-only
// output so automation can consume every command safely.
type Printer struct {
	Out io.Writer
	Err io.Writer
}

func New(out, err io.Writer) Printer {
	return Printer{
		Out: out,
		Err: err,
	}
}

func (p Printer) PrintJSON(value any) error {
	encoder := json.NewEncoder(p.Out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

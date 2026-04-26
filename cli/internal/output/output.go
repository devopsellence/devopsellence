package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Printer writes command results. The CLI is agent-primary, so JSON is the
// default and prompt-driven affordances are disabled.
type Printer struct {
	Out  io.Writer
	Err  io.Writer
	JSON bool
}

func New(out, err io.Writer) Printer {
	return Printer{
		Out:  out,
		Err:  err,
		JSON: true,
	}
}

func (p Printer) PrintJSON(value any) error {
	encoder := json.NewEncoder(p.Out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func (p Printer) Println(args ...any) {
	if p.JSON {
		return
	}
	fmt.Fprintln(p.Out, args...)
}

func (p Printer) Printf(format string, args ...any) {
	if p.JSON {
		return
	}
	fmt.Fprintf(p.Out, format, args...)
}

func (p Printer) Errorln(args ...any) {
	if p.JSON {
		return
	}
	fmt.Fprintln(p.Err, args...)
}

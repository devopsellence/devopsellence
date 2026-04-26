package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Printer writes command results. The CLI is agent-primary, so JSON is the
// default and prompt-driven affordances are disabled.
type Printer struct {
	Out         io.Writer
	Err         io.Writer
	JSON        bool
	Interactive bool
}

func New(out, err io.Writer) Printer {
	return Printer{
		Out:         out,
		Err:         err,
		JSON:        true,
		Interactive: false,
	}
}

func (p Printer) PrintJSON(value any) error {
	encoder := json.NewEncoder(p.Out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func (p Printer) Println(args ...any) {
	fmt.Fprintln(p.Out, args...)
}

func (p Printer) Printf(format string, args ...any) {
	fmt.Fprintf(p.Out, format, args...)
}

func (p Printer) Errorln(args ...any) {
	fmt.Fprintln(p.Err, args...)
}

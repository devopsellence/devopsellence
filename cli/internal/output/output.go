package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type Printer struct {
	Out         io.Writer
	Err         io.Writer
	JSON        bool
	Interactive bool
}

func New(out, err io.Writer, jsonMode bool) Printer {
	return Printer{
		Out:         out,
		Err:         err,
		JSON:        jsonMode,
		Interactive: !jsonMode && IsTTY(out),
	}
}

func IsTTY(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(file.Fd()))
}

func (p Printer) PrintJSON(value any) error {
	encoder := json.NewEncoder(p.Out)
	encoder.SetIndent("", "  ")
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

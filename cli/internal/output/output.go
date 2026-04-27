package output

import (
	"encoding/json"
	"io"
)

// Printer writes command results. The CLI is agent-primary: final command
// results are JSON on stdout, and progress events are structured JSON on stderr.
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

func (p Printer) PrintEvent(event string, fields map[string]any) error {
	if p.Err == nil {
		return nil
	}
	payload := map[string]any{
		"schema_version": 1,
		"event":          event,
	}
	for key, value := range fields {
		payload[key] = value
	}
	encoder := json.NewEncoder(p.Err)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(payload)
}

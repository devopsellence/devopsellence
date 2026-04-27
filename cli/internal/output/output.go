package output

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

const SchemaVersion = 1

// Printer writes command results. The CLI is agent-primary: final command
// results are JSON on stdout, and progress events are structured JSON on stderr.
type Printer struct {
	Out io.Writer
	Err io.Writer
	mu  *sync.Mutex
}

func New(out, err io.Writer) Printer {
	return Printer{
		Out: out,
		Err: err,
		mu:  &sync.Mutex{},
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
	payload := map[string]any{}
	for key, value := range fields {
		payload[key] = value
	}
	payload["schema_version"] = SchemaVersion
	payload["event"] = event
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return err
	}
	if p.mu != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
	}
	_, err := p.Err.Write(buf.Bytes())
	return err
}

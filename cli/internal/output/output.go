package output

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
)

const SchemaVersion = 1

const (
	// EventStarted marks the beginning of a long-running command stream.
	EventStarted = "started"
	// EventProgress reports intermediate progress for a long-running command.
	EventProgress = "progress"
	// EventResult is the final successful event in a long-running command stream.
	EventResult = "result"
	// EventError is the final failed event for command execution errors.
	EventError = "error"
)

// Fields carries command-specific schema fields. Reserved envelope keys are
// ignored when an event or error payload is encoded.
type Fields map[string]any

// Event is the stable NDJSON envelope for streaming or long-running commands.
//
// Required fields:
//   - schema_version: output schema version
//   - event: stable event name
//
// Long-running command events should also set operation. Result events set
// ok=true. Error events set ok=false and include error.
//
// Command-specific fields are flattened into the top-level event object so
// existing agents can read events without a nested "fields" object while still
// preserving a single envelope schema.
type Event struct {
	SchemaVersion int
	Operation     string
	Event         string
	OK            *bool
	Error         *ErrorPayload
	Fields        Fields
}

// ErrorPayload is the stable structured command error schema.
type ErrorPayload struct {
	Code     string
	Message  string
	ExitCode int
	Fields   Fields
}

// Printer writes command results. The CLI is agent-primary: stdout is always
// machine-readable. Bounded commands emit one JSON document; streaming commands
// emit newline-delimited JSON events on stdout. Err is kept for command-owned
// diagnostics that are not part of the command contract.
type Printer struct {
	Out io.Writer
	Err io.Writer
	mu  *sync.Mutex
}

// Stream writes newline-delimited JSON events for one long-running operation.
type Stream struct {
	printer   Printer
	operation string
}

func New(out, err io.Writer) Printer {
	return Printer{
		Out: out,
		Err: err,
		mu:  &sync.Mutex{},
	}
}

func (e Event) MarshalJSON() ([]byte, error) {
	payload := Fields{}
	copyFields(payload, e.Fields, reservedEventKeys)
	if e.SchemaVersion == 0 {
		e.SchemaVersion = SchemaVersion
	}
	payload["schema_version"] = e.SchemaVersion
	if e.Operation != "" {
		payload["operation"] = e.Operation
	}
	payload["event"] = e.Event
	if e.OK != nil {
		payload["ok"] = *e.OK
	}
	if e.Error != nil {
		payload["error"] = e.Error
	}
	return json.Marshal(payload)
}

func (e ErrorPayload) MarshalJSON() ([]byte, error) {
	payload := Fields{}
	copyFields(payload, e.Fields, reservedErrorKeys)
	payload["code"] = e.Code
	payload["message"] = e.Message
	payload["exit_code"] = e.ExitCode
	return json.Marshal(payload)
}

func (p Printer) PrintJSON(value any) error {
	encoder := json.NewEncoder(p.Out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func (p Printer) PrintEvent(event string, fields map[string]any) error {
	if p.Out == nil {
		return nil
	}
	envelope := Event{
		SchemaVersion: SchemaVersion,
		Event:         event,
		Operation:     stringField(fields, "operation"),
		Fields:        Fields(fields),
	}
	return p.writeJSONLine(envelope)
}

func (p Printer) PrintResultEvent(operation string, fields map[string]any) error {
	ok := true
	envelope := Event{
		SchemaVersion: SchemaVersion,
		Operation:     operation,
		Event:         EventResult,
		OK:            &ok,
		Fields:        Fields(fields),
	}
	return p.writeJSONLine(envelope)
}

func (p Printer) PrintErrorEvent(operation string, err ErrorPayload) error {
	ok := false
	return p.writeJSONLine(Event{
		SchemaVersion: SchemaVersion,
		Operation:     operation,
		Event:         EventError,
		OK:            &ok,
		Error:         &err,
	})
}

func (p Printer) Stream(operation string) Stream {
	return Stream{printer: p, operation: operation}
}

func (s Stream) Event(event string, fields map[string]any) error {
	envelope := Event{
		SchemaVersion: SchemaVersion,
		Operation:     s.operation,
		Event:         event,
		Fields:        Fields(fields),
	}
	return s.printer.writeJSONLine(envelope)
}

func (s Stream) Result(fields map[string]any) error {
	return s.printer.PrintResultEvent(s.operation, fields)
}

func (p Printer) writeJSONLine(value any) error {
	if p.Out == nil {
		return nil
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if p.mu != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
	}
	_, err := p.Out.Write(buf.Bytes())
	return err
}

func copyFields(dst Fields, src Fields, reserved map[string]struct{}) {
	for key, value := range src {
		if _, ok := reserved[key]; ok {
			continue
		}
		dst[key] = value
	}
}

func stringField(fields map[string]any, key string) string {
	value, ok := fields[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

var reservedEventKeys = map[string]struct{}{
	"schema_version": {},
	"operation":      {},
	"event":          {},
	"ok":             {},
	"error":          {},
}

var reservedErrorKeys = map[string]struct{}{
	"code":      {},
	"message":   {},
	"exit_code": {},
}

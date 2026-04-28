package output

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

func TestPrintEventWritesCompactJSONLineToStdout(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	printer := New(&stdout, &stderr)

	if err := printer.PrintEvent("progress", map[string]any{"operation": "devopsellence deploy", "message": "building"}); err != nil {
		t.Fatal(err)
	}

	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no command-contract output", stderr.String())
	}
	line := bytes.TrimSpace(stdout.Bytes())
	if bytes.Contains(line, []byte("\n")) {
		t.Fatalf("event output = %q, want one JSON line", stdout.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(line, &payload); err != nil {
		t.Fatalf("event is not JSON: %v\n%s", err, line)
	}
	if payload["schema_version"] != float64(SchemaVersion) || payload["event"] != "progress" || payload["operation"] != "devopsellence deploy" || payload["message"] != "building" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestEventEnvelopeIgnoresReservedFieldOverrides(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(Event{
		SchemaVersion: SchemaVersion,
		Operation:     "devopsellence deploy",
		Event:         EventProgress,
		Fields: Fields{
			"schema_version": 99,
			"operation":      "ignored",
			"event":          "ignored",
			"ok":             false,
			"message":        "building",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatal(err)
	}
	if event["schema_version"] != float64(SchemaVersion) || event["operation"] != "devopsellence deploy" || event["event"] != EventProgress || event["ok"] != nil || event["message"] != "building" {
		t.Fatalf("event = %#v", event)
	}
}

func TestPrintErrorEventWritesStructuredErrorEnvelope(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	err := ErrorPayload{
		Code:     "command_failed",
		Message:  "boom",
		ExitCode: 1,
		Fields: Fields{
			"message":    "ignored",
			"next_steps": []string{"devopsellence status"},
		},
	}
	if writeErr := New(&stdout, nil).PrintErrorEvent("devopsellence deploy", err); writeErr != nil {
		t.Fatal(writeErr)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event["event"] != EventError || event["ok"] != false || event["operation"] != "devopsellence deploy" {
		t.Fatalf("event = %#v", event)
	}
	errorPayload := event["error"].(map[string]any)
	if errorPayload["code"] != "command_failed" || errorPayload["message"] != "boom" || errorPayload["exit_code"] != float64(1) {
		t.Fatalf("error = %#v", errorPayload)
	}
	steps := errorPayload["next_steps"].([]any)
	if len(steps) != 1 || steps[0] != "devopsellence status" {
		t.Fatalf("next_steps = %#v", steps)
	}
}

func TestStreamResultWritesNDJSONEvent(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	stream := New(&stdout, nil).Stream("devopsellence node create")
	if err := stream.Event("started", nil); err != nil {
		t.Fatal(err)
	}
	if err := stream.Result(map[string]any{"node": "prod-1"}); err != nil {
		t.Fatal(err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var events []map[string]any
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("line is not JSON: %v\n%s", err, scanner.Text())
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %#v, want 2", events)
	}
	result := events[1]
	if result["event"] != "result" || result["operation"] != "devopsellence node create" || result["ok"] != true || result["node"] != "prod-1" {
		t.Fatalf("result = %#v", result)
	}
}

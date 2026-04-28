package workflow

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

func decodeJSONOutput(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(output.Bytes(), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, output.String())
	}
	return payload
}

func jsonArrayFromMap(t *testing.T, payload map[string]any, key string) []any {
	t.Helper()
	items, ok := payload[key].([]any)
	if !ok {
		t.Fatalf("payload[%q] = %#v, want array", key, payload[key])
	}
	return items
}

func jsonMapFromAny(t *testing.T, value any) map[string]any {
	t.Helper()
	item, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want object", value)
	}
	return item
}

func decodeNDJSONOutput(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var events []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("output line is not valid JSON: %v\n%s", err, line)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatalf("output has no NDJSON events")
	}
	return events
}

func lastNDJSONEvent(t *testing.T, output *bytes.Buffer) map[string]any {
	t.Helper()
	events := decodeNDJSONOutput(t, output)
	return events[len(events)-1]
}

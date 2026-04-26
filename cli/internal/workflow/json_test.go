package workflow

import (
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

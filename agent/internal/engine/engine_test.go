package engine

import "testing"

func TestLogConfigMatchesRequiresExactOptions(t *testing.T) {
	cfg := &LogConfig{Driver: "json-file", Options: map[string]string{"max-size": "10m"}}

	if !LogConfigMatches(" json-file ", map[string]string{" max-size ": " 10m "}, cfg) {
		t.Fatal("expected matching trimmed driver and options")
	}
	if LogConfigMatches("json-file", map[string]string{"max-size": "10m", "max-file": "5"}, cfg) {
		t.Fatal("expected extra actual log option to be treated as mismatch")
	}
	if LogConfigMatches("json-file", nil, cfg) {
		t.Fatal("expected missing log option to be treated as mismatch")
	}
	if LogConfigMatches("json-file", map[string]string{"max-size": "10m"}, &LogConfig{Driver: "json-file"}) {
		t.Fatal("expected configured empty options to reject actual options")
	}
}

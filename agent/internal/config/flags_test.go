package config

import "testing"

func TestLoadRejectsUnknownFlags(t *testing.T) {
	if _, err := Load([]string{"--no-such-flag=tok"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

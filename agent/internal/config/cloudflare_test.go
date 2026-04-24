package config

import (
	"reflect"
	"strings"
	"testing"
)

var requiredFlags = []string{
	"--control-plane-base-url=https://cp.example.com",
	"--auth-state-path=/tmp/agent-auth-state.json",
}

func TestConfigDoesNotExposeCloudflareTunnelToken(t *testing.T) {
	if _, ok := reflect.TypeOf(Config{}).FieldByName("CloudflareTunnelToken"); ok {
		t.Fatal("expected cloudflare tunnel token to be removed from agent config")
	}
}

func TestLoadRejectsRemovedCloudflareTunnelTokenFileFlag(t *testing.T) {
	_, err := Load(append(requiredFlags, "--cloudflare-tunnel-token-file=/tmp/token"))
	if err == nil {
		t.Fatal("expected removed cloudflare tunnel token flag to be rejected")
	}
	if !strings.Contains(err.Error(), "cloudflare-tunnel-token-file") {
		t.Fatalf("expected error to mention removed flag, got %q", err)
	}
}

func TestLoadRejectsUnknownFlags(t *testing.T) {
	if _, err := Load([]string{"--no-such-flag=tok"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

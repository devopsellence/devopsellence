package config

import (
	"os"
	"path/filepath"
	"testing"
)

var requiredFlags = []string{
	"--control-plane-base-url=https://cp.example.com",
	"--auth-state-path=/tmp/agent-auth-state.json",
}

func TestLoadCloudflareTokenFile(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("tok-from-file\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	args := append(requiredFlags, "--cloudflare-tunnel-token-file="+tokenFile)
	cfg, err := Load(args)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CloudflareTunnelToken != "tok-from-file" {
		t.Fatalf("expected token from file, got %q", cfg.CloudflareTunnelToken)
	}
}

func TestLoadCloudflareTokenFileRejectsInsecurePerms(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("tok"), 0o644); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	args := append(requiredFlags, "--cloudflare-tunnel-token-file="+tokenFile)
	if _, err := Load(args); err == nil {
		t.Fatal("expected error for insecure permissions")
	}
}

func TestLoadRejectsUnknownFlags(t *testing.T) {
	if _, err := Load([]string{"--no-such-flag=tok"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

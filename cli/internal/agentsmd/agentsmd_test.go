package agentsmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/config"
)

func TestWriteCreatesAgentsFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path, err := Write(root, config.DefaultProjectConfig("acme", "ShopApp", "production"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "# AGENTS.md") {
		t.Fatalf("AGENTS.md = %q, want heading", text)
	}
	if !strings.Contains(text, "devopsellence deploy") {
		t.Fatalf("AGENTS.md = %q, want cli instructions", text)
	}
	if !strings.Contains(text, "devopsellence doctor") {
		t.Fatalf("AGENTS.md = %q, want doctor guidance", text)
	}
	if !strings.Contains(text, "release_command") {
		t.Fatalf("AGENTS.md = %q, want lifecycle hook guidance", text)
	}
	if !strings.Contains(text, "Organization: acme") {
		t.Fatalf("AGENTS.md = %q, want workspace defaults", text)
	}
}

func TestWriteReplacesManagedBlockAndPreservesCustomContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, FilePath)
	existing := strings.Join([]string{
		"# Team Notes",
		"",
		"Keep this line.",
		"",
		beginMarker,
		"old block",
		endMarker,
		"",
		"Custom footer.",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed AGENTS.md: %v", err)
	}

	if _, err := Write(root, config.DefaultProjectConfig("acme", "ShopApp", "staging")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Keep this line.") || !strings.Contains(text, "Custom footer.") {
		t.Fatalf("AGENTS.md = %q, want custom content preserved", text)
	}
	if strings.Contains(text, "old block") {
		t.Fatalf("AGENTS.md = %q, old managed block still present", text)
	}
	if !strings.Contains(text, "Environment: staging") {
		t.Fatalf("AGENTS.md = %q, want refreshed managed block", text)
	}
	if !strings.Contains(text, "release_command") {
		t.Fatalf("AGENTS.md = %q, want release_command lifecycle guidance", text)
	}
}

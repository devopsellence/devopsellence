package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFindsConfiguredWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "devopsellence.yml"), []byte("schema_version: 1\norganization: acme\nproject: demo\ndefault_environment: production\nbuild:\n  context: .\n  dockerfile: Dockerfile\nservices:\n  web:\n    ports:\n      - name: http\n        port: 8080\n    healthcheck:\n      path: /\n      port: 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := filepath.Join(root, "src", "api")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(start)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if result.WorkspaceRoot != root {
		t.Fatalf("WorkspaceRoot = %q, want %q", result.WorkspaceRoot, root)
	}
	if result.ProjectName != filepath.Base(root) {
		t.Fatalf("ProjectName = %q, want %q", result.ProjectName, filepath.Base(root))
	}
}

func TestDiscoverFallsBackToWorkspaceCandidate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM busybox\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := filepath.Join(root, "cmd", "server")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(start)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if result.WorkspaceRoot != root {
		t.Fatalf("WorkspaceRoot = %q, want %q", result.WorkspaceRoot, root)
	}
}

func TestDiscoverFallsBackToOriginalDirectoryWhenNothingDetected(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	start := filepath.Join(root, "apps", "react-ex")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(start)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if result.WorkspaceRoot != start {
		t.Fatalf("WorkspaceRoot = %q, want %q", result.WorkspaceRoot, start)
	}
}

func TestDiscoverInfersWebPortFromDockerfileExpose(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\nEXPOSE 8080/tcp\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if result.InferredWebPort != 8080 {
		t.Fatalf("InferredWebPort = %d, want 8080", result.InferredWebPort)
	}
}

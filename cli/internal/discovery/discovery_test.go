package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFindsRailsRootAndModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Gemfile"), []byte("source 'https://rubygems.org'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "module ShopApp\n  class Application < Rails::Application\n  end\nend\n"
	if err := os.WriteFile(filepath.Join(root, "config", "application.rb"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	start := filepath.Join(root, "app", "models")
	if err := os.MkdirAll(start, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(start)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if result.AppType != "rails" {
		t.Fatalf("AppType = %q, want rails", result.AppType)
	}
	if result.RailsRoot != root {
		t.Fatalf("RailsRoot = %q, want %q", result.RailsRoot, root)
	}
	if result.WorkspaceRoot != root {
		t.Fatalf("WorkspaceRoot = %q, want %q", result.WorkspaceRoot, root)
	}
	if result.ProjectName != "ShopApp" || result.ProjectSlug != "shop-app" {
		t.Fatalf("project discovery mismatch: %#v", result)
	}
	if result.FallbackUsed {
		t.Fatalf("FallbackUsed = true, want false")
	}
}

func TestDiscoverFallsBackToDirectoryName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Gemfile"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "application.rb"), []byte("class Application < Rails::Application\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if !result.FallbackUsed {
		t.Fatalf("FallbackUsed = false, want true")
	}
	if result.ProjectSlug == "" {
		t.Fatalf("ProjectSlug should not be empty")
	}
}

func TestDiscoverFindsGenericWorkspaceFromConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "devopsellence.yml"), []byte("schema_version: 5\norganization: acme\nproject: demo\ndefault_environment: production\nbuild:\n  context: .\n  dockerfile: Dockerfile\nservices:\n  web:\n    kind: web\n    roles: [web]\n    ports:\n      - name: http\n        port: 8080\n    healthcheck:\n      path: /\n      port: 8080\n"), 0o644); err != nil {
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
	if result.AppType != "generic" {
		t.Fatalf("AppType = %q, want generic", result.AppType)
	}
	if result.WorkspaceRoot != root {
		t.Fatalf("WorkspaceRoot = %q, want %q", result.WorkspaceRoot, root)
	}
	if result.ProjectName != filepath.Base(root) {
		t.Fatalf("ProjectName = %q, want %q", result.ProjectName, filepath.Base(root))
	}
}

func TestDiscoverFallsBackToGenericWorkspaceCandidate(t *testing.T) {
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
	if result.AppType != "generic" {
		t.Fatalf("AppType = %q, want generic", result.AppType)
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
	if result.AppType != "generic" {
		t.Fatalf("AppType = %q, want generic", result.AppType)
	}
	if result.WorkspaceRoot != start {
		t.Fatalf("WorkspaceRoot = %q, want %q", result.WorkspaceRoot, start)
	}
}

func TestDiscoverInfersWebPortFromThrustDockerfile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Gemfile"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "application.rb"), []byte("module Smoke\n  class Application < Rails::Application\n  end\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM ruby:4.0.0-slim\nEXPOSE 80\nCMD [\"./bin/thrust\", \"./bin/rails\", \"server\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if result.InferredWebPort != 80 {
		t.Fatalf("InferredWebPort = %d, want 80", result.InferredWebPort)
	}
}

func TestDiscoverInfersExplicitRailsServerPortFromDockerfile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Gemfile"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config", "application.rb"), []byte("module Smoke\n  class Application < Rails::Application\n  end\nend\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM ruby:4.0.0-slim\nEXPOSE 3000\nCMD [\"./bin/rails\", \"server\", \"-b\", \"0.0.0.0\", \"-p\", \"3000\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if result.InferredWebPort != 3000 {
		t.Fatalf("InferredWebPort = %d, want 3000", result.InferredWebPort)
	}
}

package solo

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/authority"
	"github.com/devopsellence/devopsellence/agent/internal/observability"
)

func TestFetch_FileNotExist(t *testing.T) {
	a := New("/tmp/nonexistent-desired-state.json", observability.NewLogger(0))
	_, err := a.Fetch(context.Background())
	if err != authority.ErrNoDesiredState {
		t.Fatalf("expected ErrNoDesiredState, got %v", err)
	}
}

func TestFetch_ValidDesiredState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desired-state-override.json")

	state := `{"schemaVersion":2,"revision":"abc123","environments":[{"name":"production","services":[{"name":"web","kind":"web","image":"myapp:abc123","ports":[{"name":"http","port":3000}],"healthcheck":{"path":"/up","port":3000}}]}]}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(path, observability.NewLogger(0))
	result, err := a.Fetch(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Desired.GetRevision() != "abc123" {
		t.Errorf("expected revision abc123, got %s", result.Desired.GetRevision())
	}
	services := result.Desired.GetEnvironments()[0].GetServices()
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	if services[0].GetName() != "web" {
		t.Errorf("expected service name web, got %s", services[0].GetName())
	}
}

func TestFetch_CachesOnSameFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desired-state-override.json")

	state := `{"schemaVersion":2,"revision":"v1","environments":[]}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(path, observability.NewLogger(0))

	r1, err := a.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	r2, err := a.Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if r1 != r2 {
		t.Error("expected cached result to be the same pointer")
	}
}

func TestFetch_DisabledOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desired-state-override.json")

	state := `{"enabled":false,"desired_state":{"schemaVersion":2,"revision":"v1"}}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}

	a := New(path, observability.NewLogger(0))
	_, err := a.Fetch(context.Background())
	if err != authority.ErrNoDesiredState {
		t.Fatalf("expected ErrNoDesiredState for disabled override, got %v", err)
	}
}

package desiredstate

import (
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestHashServiceDeterministic(t *testing.T) {
	service := &desiredstatepb.Service{
		Name:  "worker",
		Kind:  "worker",
		Image: "busybox",
		Env:   map[string]string{"A": "1", "B": "2"},
	}

	h1, err := HashService(service)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	h2, err := HashService(service)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("expected deterministic hash, got %s != %s", h1, h2)
	}
}

func TestHashServiceChanges(t *testing.T) {
	c1 := &desiredstatepb.Service{Name: "worker", Kind: "worker", Image: "busybox", Env: map[string]string{"A": "1"}}
	c2 := &desiredstatepb.Service{Name: "worker", Kind: "worker", Image: "busybox", Env: map[string]string{"A": "2"}}

	h1, err := HashService(c1)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	h2, err := HashService(c2)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if h1 == h2 {
		t.Fatal("expected different hash")
	}
}

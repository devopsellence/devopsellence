package desiredstate

import (
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestHashContainerDeterministic(t *testing.T) {
	c := &desiredstatepb.Container{
		ServiceName: "worker",
		Image:       "busybox",
		Env:         map[string]string{"A": "1", "B": "2"},
	}

	h1, err := HashContainer(c)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	h2, err := HashContainer(c)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("expected deterministic hash, got %s != %s", h1, h2)
	}
}

func TestHashContainerChanges(t *testing.T) {
	c1 := &desiredstatepb.Container{ServiceName: "worker", Image: "busybox", Env: map[string]string{"A": "1"}}
	c2 := &desiredstatepb.Container{ServiceName: "worker", Image: "busybox", Env: map[string]string{"A": "2"}}

	h1, err := HashContainer(c1)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	h2, err := HashContainer(c2)
	if err != nil {
		t.Fatalf("hash error: %v", err)
	}
	if h1 == h2 {
		t.Fatal("expected different hash")
	}
}

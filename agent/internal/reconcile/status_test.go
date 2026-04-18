package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/report"
)

func TestCurrentStatusPrefersErrorOverReconcilingForEnvironmentPhase(t *testing.T) {
	eng := newFakeEngine()
	stoppedName, err := desiredstate.ServiceContainerName("production", "mailers", "rev-1", "hash-mailers")
	if err != nil {
		t.Fatalf("service container name: %v", err)
	}
	eng.containers[stoppedName] = engine.ContainerState{
		Name:        stoppedName,
		Running:     false,
		Hash:        "hash-mailers",
		Environment: "production",
		Service:     "mailers",
		ServiceKind: "worker",
	}

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: time.Second})
	_, environments, err := rec.CurrentStatus(context.Background(), desiredState(
		workerService("default", nil),
		workerService("mailers", nil),
	))
	if err != nil {
		t.Fatalf("current status: %v", err)
	}
	if len(environments) != 1 {
		t.Fatalf("expected 1 environment, got %d", len(environments))
	}
	if environments[0].Phase != report.PhaseError {
		t.Fatalf("expected environment phase error, got %q", environments[0].Phase)
	}
}

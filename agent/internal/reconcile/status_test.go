package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
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

func TestCurrentStatusKeepsEnvironmentStatusesSeparate(t *testing.T) {
	eng := newFakeEngine()
	prodName, err := desiredstate.ServiceContainerName("production", "web", "rev-1", "hash-prod")
	if err != nil {
		t.Fatalf("production container name: %v", err)
	}
	stagingName, err := desiredstate.ServiceContainerName("staging", "web", "rev-2", "hash-staging")
	if err != nil {
		t.Fatalf("staging container name: %v", err)
	}
	eng.containers[prodName] = engine.ContainerState{
		Name:        prodName,
		Running:     true,
		Hash:        "hash-prod",
		Environment: "production",
		Service:     "web",
		ServiceKind: "web",
	}
	eng.containers[stagingName] = engine.ContainerState{
		Name:        stagingName,
		Running:     false,
		Hash:        "hash-staging",
		Environment: "staging",
		Service:     "web",
		ServiceKind: "web",
	}

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: time.Second})
	state := &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      "node-rev",
		Environments: []*desiredstatepb.Environment{
			{Name: "production", Revision: "rev-1", Services: []*desiredstatepb.Service{webService(3000, "/up")}},
			{Name: "staging", Revision: "rev-2", Services: []*desiredstatepb.Service{webService(3000, "/up")}},
		},
	}

	summary, environments, err := rec.CurrentStatus(context.Background(), state)
	if err != nil {
		t.Fatalf("current status: %v", err)
	}
	if summary == nil || summary.Environments != 2 || summary.Services != 2 || summary.UnhealthyServices != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if len(environments) != 2 {
		t.Fatalf("expected 2 environments, got %d", len(environments))
	}
	if environments[0].Name != "production" || environments[0].Phase != report.PhaseSettled {
		t.Fatalf("unexpected production status: %#v", environments[0])
	}
	if environments[1].Name != "staging" || environments[1].Phase != report.PhaseError {
		t.Fatalf("unexpected staging status: %#v", environments[1])
	}
}

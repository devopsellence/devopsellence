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

func TestCurrentStatusExplainsReusedPreviousRevisionService(t *testing.T) {
	eng := newFakeEngine()
	service := workerService("postgres", nil)
	hash, err := desiredstate.HashService(service)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: time.Second})
	network, err := rec.environmentNetwork("production")
	if err != nil {
		t.Fatalf("environment network: %v", err)
	}
	alias, err := desiredstate.ServiceNetworkAlias("postgres")
	if err != nil {
		t.Fatalf("service alias: %v", err)
	}
	hash = runtimeContainerHash(hash, nil, network, []string{alias})
	name, err := desiredstate.ServiceContainerName("production", "postgres", "oldsha", hash)
	if err != nil {
		t.Fatalf("service container name: %v", err)
	}
	eng.containers[name] = engine.ContainerState{
		Name:        name,
		Running:     true,
		Hash:        hash,
		Revision:    "oldsha",
		Environment: "production",
		Service:     "postgres",
		ServiceKind: "worker",
	}

	_, environments, err := rec.CurrentStatus(context.Background(), &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      "node-rev",
		Environments: []*desiredstatepb.Environment{{
			Name:     "production",
			Revision: "newsha",
			Services: []*desiredstatepb.Service{service},
		}},
	})
	if err != nil {
		t.Fatalf("current status: %v", err)
	}
	if len(environments) != 1 || len(environments[0].Services) != 1 {
		t.Fatalf("environments = %#v, want one service", environments)
	}
	status := environments[0].Services[0]
	if status.ContainerRevision != "oldsha" {
		t.Fatalf("container_revision = %q, want oldsha", status.ContainerRevision)
	}
	if status.RevisionStatus != "reused_from_previous_release" {
		t.Fatalf("revision_status = %q, want reused_from_previous_release", status.RevisionStatus)
	}
	if status.RevisionMessage == "" {
		t.Fatalf("revision_message empty, want previous-release explanation")
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

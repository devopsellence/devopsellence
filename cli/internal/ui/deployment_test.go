package ui

import (
	"context"
	"testing"
)

func TestMonitorDeploymentPollsUntilComplete(t *testing.T) {
	calls := 0
	snapshot, err := MonitorDeployment(t.Context(), nil, "ignored", 1, func(context.Context) (DeploymentSnapshot, error) {
		calls++
		return DeploymentSnapshot{Summary: DeploymentSummary{Complete: calls == 2}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if !snapshot.Summary.Complete {
		t.Fatalf("snapshot = %#v, want complete", snapshot)
	}
}

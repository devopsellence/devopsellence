package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
)

func TestDeploymentMonitorRendersPendingNodeAsActive(t *testing.T) {
	model := deploymentMonitorModel{
		renderer: DefaultRenderer(),
		spinner:  spinner.New(),
	}

	view := model.renderNode(DeploymentNode{
		Name:   "node-a",
		Phase:  "pending",
		Detail: "waiting for node to reconcile",
	})

	if !strings.Contains(view, "node-a - waiting for node to reconcile") {
		t.Fatalf("expected pending node detail in view, got %q", view)
	}
	if strings.Contains(view, "[ ]") {
		t.Fatalf("expected pending node to render as active, got %q", view)
	}
}

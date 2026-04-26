package workflow

import (
	"path/filepath"
	"testing"

	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
)

func TestSuggestedModeUsesSoloStateForWorkspace(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("acme", "shop", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(t.TempDir(), "solo-state.json")
	store := solo.NewStateStore(statePath)
	current := solo.State{
		Nodes:       map[string]config.Node{},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]desiredstate.DeploySnapshot{},
	}
	if err := current.SetNode("node-1", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(current); err != nil {
		t.Fatal(err)
	}

	app := &App{
		ConfigStore: config.NewStore(),
		SoloState:   store,
		Cwd:         workspaceRoot,
	}
	if got := app.suggestedMode(); got != ModeSolo {
		t.Fatalf("suggestedMode() = %q, want %q", got, ModeSolo)
	}
}

func TestSuggestedModeDefaultsSharedWithoutSoloState(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "shop", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	app := &App{
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}
	if got := app.suggestedMode(); got != ModeShared {
		t.Fatalf("suggestedMode() = %q, want %q", got, ModeShared)
	}
}

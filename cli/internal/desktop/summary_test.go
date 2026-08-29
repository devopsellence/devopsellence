package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
	corerelease "github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/release"
)

func TestBuildSummaryRedactsSecretsAndFiltersWorkspace(t *testing.T) {
	workspace := t.TempDir()
	otherWorkspace := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.json")
	workspaceKey, err := solo.CanonicalWorkspaceKey(workspace)
	if err != nil {
		t.Fatal(err)
	}
	otherWorkspaceKey, err := solo.CanonicalWorkspaceKey(otherWorkspace)
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspace, cfg); err != nil {
		t.Fatal(err)
	}

	store := solo.NewStateStore(statePath)
	state := solo.State{
		SchemaVersion: 1,
		Nodes: map[string]config.Node{
			"prod-1": {Host: "203.0.113.10", User: "root", SSHKey: "/tmp/private", Labels: []string{"web"}},
			"spare":  {Host: "203.0.113.11", User: "root"},
		},
		Attachments: map[string]solo.AttachmentRecord{
			workspaceKey + "\nproduction":      {WorkspaceRoot: workspace, WorkspaceKey: workspaceKey, Environment: "production", NodeNames: []string{"prod-1"}},
			otherWorkspaceKey + "\nproduction": {WorkspaceRoot: otherWorkspace, WorkspaceKey: otherWorkspaceKey, Environment: "production", NodeNames: []string{"spare"}},
		},
		Current: map[string]string{
			workspaceKey + "\nproduction": "rel-1",
		},
		Releases: map[string]corerelease.Release{
			"rel-1": {
				ID:            "rel-1",
				EnvironmentID: workspaceKey + "\nproduction",
				Revision:      "abc123",
				Snapshot:      desiredstate.DeploySnapshot{WorkspaceRoot: workspace, WorkspaceKey: workspaceKey, Environment: "production", Image: "demo:abc123"},
				Image:         corerelease.ImageRef{Reference: "demo:abc123"},
				TargetNodeIDs: []string{
					"prod-1",
				},
				CreatedAt: "2026-05-26T10:00:00Z",
			},
			"other-rel": {
				ID:            "other-rel",
				EnvironmentID: otherWorkspaceKey + "\nproduction",
				Revision:      "def456",
				Snapshot:      desiredstate.DeploySnapshot{WorkspaceRoot: otherWorkspace, WorkspaceKey: otherWorkspaceKey, Environment: "production", Image: "other:def456"},
				CreatedAt:     "2026-05-26T10:00:00Z",
			},
		},
		Secrets: map[string]solo.SecretRecord{
			workspaceKey + "\nproduction\nweb\nDATABASE_URL": {
				WorkspaceRoot: workspace,
				WorkspaceKey:  workspaceKey,
				Environment:   "production",
				ServiceName:   "web",
				Name:          "DATABASE_URL",
				Store:         solo.SecretStorePlaintext,
				Value:         "postgres://super-secret",
				UpdatedAt:     "2026-05-26T10:01:00Z",
			},
		},
	}
	if err := store.Write(state); err != nil {
		t.Fatal(err)
	}

	summary, err := BuildSummary(SummaryOptions{WorkspaceRoot: workspace, StatePath: statePath})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Project == nil || summary.Project.Project != "demo" {
		t.Fatalf("project summary = %#v", summary.Project)
	}
	if summary.State.NodeCount != 2 {
		t.Fatalf("node count = %d, want all registered nodes visible", summary.State.NodeCount)
	}
	if summary.State.AttachmentCount != 1 {
		t.Fatalf("attachment count = %d, want current workspace only", summary.State.AttachmentCount)
	}
	if summary.State.ReleaseCount != 1 || summary.State.CurrentRevision != "abc123" {
		t.Fatalf("release summary = %#v", summary.State)
	}
	if len(summary.Secrets) != 1 || summary.Secrets[0].Name != "DATABASE_URL" {
		t.Fatalf("secrets = %#v", summary.Secrets)
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "super-secret") || strings.Contains(string(payload), "/tmp/private") {
		t.Fatalf("summary leaked sensitive value: %s", payload)
	}
}

func TestHandlerServesIndexAndSummary(t *testing.T) {
	workspace := t.TempDir()
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := solo.NewStateStore(statePath).Write(solo.State{SchemaVersion: 1}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(SummaryOptions{WorkspaceRoot: workspace, StatePath: statePath})

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRec := httptest.NewRecorder()
	handler.ServeHTTP(indexRec, indexReq)
	if indexRec.Code != http.StatusOK || !strings.Contains(indexRec.Body.String(), "devopsellence solo desktop") {
		t.Fatalf("index response = %d %q", indexRec.Code, indexRec.Body.String())
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	summaryRec := httptest.NewRecorder()
	handler.ServeHTTP(summaryRec, summaryReq)
	if summaryRec.Code != http.StatusOK {
		t.Fatalf("summary status = %d body=%s", summaryRec.Code, summaryRec.Body.String())
	}
	var summary Summary
	if err := json.Unmarshal(summaryRec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.SchemaVersion != apiSchemaVersion || summary.Workspace.Mode != "solo" {
		t.Fatalf("summary = %#v", summary)
	}
}

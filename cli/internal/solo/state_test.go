package solo

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/config"
)

func TestCanonicalWorkspaceKeyResolvesSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realRoot := filepath.Join(root, "real")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(root, "link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}

	got, err := CanonicalWorkspaceKey(filepath.Join(linkRoot, "."))
	if err != nil {
		t.Fatal(err)
	}
	if got != realRoot {
		t.Fatalf("CanonicalWorkspaceKey() = %q, want %q", got, realRoot)
	}
}

func TestStateStoreRoundTrip(t *testing.T) {
	t.Parallel()

	store := NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := newState()
	if err := current.SetNode("web-a", config.SoloNode{
		Host:   "203.0.113.10",
		User:   "root",
		Labels: []string{config.DefaultWebRole},
	}); err != nil {
		t.Fatal(err)
	}
	attachment, changed, err := current.AttachNode("/workspace/demo", "production", "web-a")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("AttachNode() changed = false, want true")
	}
	current.Attachments[attachment.WorkspaceKey+"\n"+attachment.Environment] = attachment
	current.Snapshots[attachment.WorkspaceKey+"\n"+attachment.Environment] = DeploySnapshot{
		WorkspaceRoot: "/workspace/demo",
		WorkspaceKey:  attachment.WorkspaceKey,
		Environment:   "production",
		Revision:      "abc1234",
		Image:         "demo:abc1234",
		Metadata:      SnapshotMetadata{Project: "demo"},
	}
	if err := store.Write(current); err != nil {
		t.Fatal(err)
	}

	loaded, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SchemaVersion != soloStateSchemaVersion {
		t.Fatalf("schema_version = %d, want %d", loaded.SchemaVersion, soloStateSchemaVersion)
	}
	if got := loaded.Nodes["web-a"].AgentStateDir; got != "/var/lib/devopsellence" {
		t.Fatalf("agent_state_dir = %q, want default", got)
	}
	if got := loaded.Attachments[attachment.WorkspaceKey+"\nproduction"].NodeNames; !reflect.DeepEqual(got, []string{"web-a"}) {
		t.Fatalf("attachment nodes = %#v", got)
	}
	if got := loaded.Snapshots[attachment.WorkspaceKey+"\nproduction"].Image; got != "demo:abc1234" {
		t.Fatalf("snapshot image = %q", got)
	}
}

func TestStateStoreReadNormalizesLegacyData(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "solo-state.json")
	if err := os.WriteFile(path, []byte(`{
  "schema_version": 1,
  "nodes": {
    "web-a": {
      "host": "203.0.113.10",
      "user": "root",
      "labels": ["web", "web"]
    }
  },
  "attachments": {
    "/workspace/demo\nproduction": {
      "workspace_root": "/workspace/demo",
      "node_names": ["web-b", "web-a", "web-a"]
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := NewStateStore(path).Read()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Nodes["web-a"].AgentStateDir; got != "/var/lib/devopsellence" {
		t.Fatalf("agent_state_dir = %q, want default", got)
	}
	if got := loaded.Attachments["/workspace/demo\nproduction"].WorkspaceKey; got != "/workspace/demo" {
		t.Fatalf("workspace_key = %q, want /workspace/demo", got)
	}
	if got := loaded.Attachments["/workspace/demo\nproduction"].Environment; got != "production" {
		t.Fatalf("environment = %q, want production", got)
	}
	if got := loaded.Attachments["/workspace/demo\nproduction"].NodeNames; !reflect.DeepEqual(got, []string{"web-a", "web-b"}) {
		t.Fatalf("node_names = %#v", got)
	}
}

func TestStateStoreReadNormalizesLegacySnapshots(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "solo-state.json")
	if err := os.WriteFile(path, []byte(`{
  "schema_version": 1,
  "snapshots": {
    "/workspace/demo\nproduction": {
      "workspace_root": "/workspace/demo",
      "revision": "abc1234",
      "image": "demo:abc1234"
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := NewStateStore(path).Read()
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := loaded.Snapshots["/workspace/demo\nproduction"]
	if !ok {
		t.Fatalf("normalized snapshot missing: %#v", loaded.Snapshots)
	}
	if snapshot.WorkspaceKey != "/workspace/demo" {
		t.Fatalf("workspace_key = %q, want /workspace/demo", snapshot.WorkspaceKey)
	}
	if snapshot.Environment != "production" {
		t.Fatalf("environment = %q, want production", snapshot.Environment)
	}
}

func TestAttachmentKeysForNodeDoesNotMutateState(t *testing.T) {
	t.Parallel()

	current := State{
		SchemaVersion: soloStateSchemaVersion,
		Nodes: map[string]config.SoloNode{
			"web-a": {Host: "203.0.113.10", User: "root"},
		},
		Attachments: map[string]AttachmentRecord{
			"/workspace/demo\nproduction": {
				WorkspaceRoot: "/workspace/demo",
				NodeNames:     []string{"web-a", "web-a"},
			},
		},
		Snapshots: map[string]DeploySnapshot{},
	}

	keys := current.AttachmentKeysForNode("web-a")
	if want := []string{"/workspace/demo\nproduction"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("AttachmentKeysForNode() = %#v, want %#v", keys, want)
	}
	if got := current.Attachments["/workspace/demo\nproduction"].NodeNames; !reflect.DeepEqual(got, []string{"web-a", "web-a"}) {
		t.Fatalf("AttachmentKeysForNode() mutated attachment nodes: %#v", got)
	}
}

func TestAttachmentKeysForNodeTrimsInput(t *testing.T) {
	t.Parallel()

	current := State{
		SchemaVersion: soloStateSchemaVersion,
		Attachments: map[string]AttachmentRecord{
			"/workspace/demo\nproduction": {
				WorkspaceRoot: "/workspace/demo",
				NodeNames:     []string{"web-a"},
			},
		},
	}

	keys := current.AttachmentKeysForNode("  web-a  ")
	if want := []string{"/workspace/demo\nproduction"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("AttachmentKeysForNode() = %#v, want %#v", keys, want)
	}
}

func TestSetNodeRejectsMissingConnectionFields(t *testing.T) {
	t.Parallel()

	current := newState()
	if err := current.SetNode("web-a", config.SoloNode{User: "root"}); err == nil || !strings.Contains(err.Error(), "host is required") {
		t.Fatalf("expected host validation error, got %v", err)
	}
	if err := current.SetNode("web-a", config.SoloNode{Host: "203.0.113.10"}); err == nil || !strings.Contains(err.Error(), "user is required") {
		t.Fatalf("expected user validation error, got %v", err)
	}
}

func TestStateStoreReadRejectsUnsupportedSchemaVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "solo-state.json")
	if err := os.WriteFile(path, []byte(`{"schema_version": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewStateStore(path).Read()
	if err == nil || !strings.Contains(err.Error(), "unsupported solo state schema_version 2") {
		t.Fatalf("expected schema version error, got %v", err)
	}
}

func TestAttachmentCRUD(t *testing.T) {
	t.Parallel()

	current := newState()
	if err := current.SetNode("web-a", config.SoloNode{Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}}); err != nil {
		t.Fatal(err)
	}
	if err := current.SetNode("worker-a", config.SoloNode{Host: "203.0.113.11", User: "root", Labels: []string{config.DefaultWorkerRole}}); err != nil {
		t.Fatal(err)
	}

	attachment, changed, err := current.AttachNode("/workspace/demo", "production", "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("first attach changed = false")
	}
	if _, changed, err := current.AttachNode("/workspace/demo", "production", "web-a"); err != nil {
		t.Fatal(err)
	} else if !changed {
		t.Fatal("second attach changed = false")
	}
	if _, changed, err := current.AttachNode("/workspace/demo", "production", "web-a"); err != nil {
		t.Fatal(err)
	} else if changed {
		t.Fatal("duplicate attach changed = true")
	}

	nodes, err := current.AttachedNodeNames("/workspace/demo", "production")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"web-a", "worker-a"}; !reflect.DeepEqual(nodes, want) {
		t.Fatalf("AttachedNodeNames() = %#v, want %#v", nodes, want)
	}

	_, changed, err = current.DetachNode("/workspace/demo", "production", "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("detach changed = false")
	}
	nodes, err = current.AttachedNodeNames("/workspace/demo", "production")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"web-a"}; !reflect.DeepEqual(nodes, want) {
		t.Fatalf("after detach nodes = %#v, want %#v", nodes, want)
	}

	if got := attachment.WorkspaceKey; got == "" {
		t.Fatal("workspace key empty")
	}
}

func TestSaveSnapshotPersistsWorkspaceEnvironmentIdentity(t *testing.T) {
	t.Parallel()

	current := newState()
	cfg := config.DefaultProjectConfig("solo", "demo", "staging")
	snapshot, err := BuildDeploySnapshot(&cfg, "/workspace/demo", "/workspace/demo/devopsellence.yml", "demo:abc1234", "abc1234", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	key, err := current.SaveSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot, ok := current.Snapshots[key]; !ok {
		t.Fatalf("snapshot %q missing", key)
	} else {
		if snapshot.WorkspaceKey == "" {
			t.Fatal("snapshot workspace_key empty")
		}
		if snapshot.Environment != "staging" {
			t.Fatalf("snapshot environment = %q, want staging", snapshot.Environment)
		}
		if snapshot.Metadata.ConfigPath != "/workspace/demo/devopsellence.yml" {
			t.Fatalf("config path = %q", snapshot.Metadata.ConfigPath)
		}
	}
}

func TestRedactDeploySnapshotSecretsRemovesSecretValues(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Services["web"] = config.ServiceConfig{
		Kind: config.ServiceKindWeb,
		Env:  map[string]string{"PLAIN": "value"},
		SecretRefs: []config.SecretRef{
			{Name: "DATABASE_URL"},
		},
		Ports: []config.ServicePort{{Name: "http", Port: 3000}},
		Healthcheck: &config.HTTPHealthcheck{
			Path: "/up",
			Port: 3000,
		},
	}
	cfg.Tasks.Release = &config.TaskConfig{Service: "web", Command: "bin/rails db:migrate"}

	snapshot, err := BuildDeploySnapshot(&cfg, "/workspace/demo", "/workspace/demo/devopsellence.yml", "demo:abc1234", "abc1234", map[string]string{"DATABASE_URL": "postgres://secret"})
	if err != nil {
		t.Fatal(err)
	}
	redacted := RedactDeploySnapshotSecrets(snapshot, &cfg)
	if _, ok := redacted.Services[0].Env["DATABASE_URL"]; ok {
		t.Fatalf("service env still includes DATABASE_URL: %#v", redacted.Services[0].Env)
	}
	if redacted.ReleaseTask == nil {
		t.Fatal("release task missing")
	}
	if _, ok := redacted.ReleaseTask.Env["DATABASE_URL"]; ok {
		t.Fatalf("release env still includes DATABASE_URL: %#v", redacted.ReleaseTask.Env)
	}
	if got := redacted.Services[0].Env["PLAIN"]; got != "value" {
		t.Fatalf("plain env = %q, want value", got)
	}
}

func TestDetachNodeDoesNotMutatePreviouslyReturnedAttachments(t *testing.T) {
	t.Parallel()

	current := newState()
	if err := current.SetNode("web-a", config.SoloNode{Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}}); err != nil {
		t.Fatal(err)
	}
	if err := current.SetNode("worker-a", config.SoloNode{Host: "203.0.113.11", User: "root", Labels: []string{config.DefaultWorkerRole}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode("/workspace/demo", "production", "web-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode("/workspace/demo", "production", "worker-a"); err != nil {
		t.Fatal(err)
	}

	attachments := current.AttachmentsForNode("web-a")
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	before := attachments[0]
	if _, _, err := current.DetachNode("/workspace/demo", "production", "worker-a"); err != nil {
		t.Fatal(err)
	}
	if got := before.NodeNames; !reflect.DeepEqual(got, []string{"web-a", "worker-a"}) {
		t.Fatalf("returned attachment mutated to %#v", got)
	}
}

func TestAttachNodeDoesNotMutatePreviouslyReturnedAttachments(t *testing.T) {
	t.Parallel()

	current := newState()
	if err := current.SetNode("web-a", config.SoloNode{Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}}); err != nil {
		t.Fatal(err)
	}
	if err := current.SetNode("worker-a", config.SoloNode{Host: "203.0.113.11", User: "root", Labels: []string{config.DefaultWorkerRole}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode("/workspace/demo", "production", "web-a"); err != nil {
		t.Fatal(err)
	}

	attachments := current.AttachmentsForNode("web-a")
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	before := attachments[0]
	if _, _, err := current.AttachNode("/workspace/demo", "production", "worker-a"); err != nil {
		t.Fatal(err)
	}
	if got := before.NodeNames; !reflect.DeepEqual(got, []string{"web-a"}) {
		t.Fatalf("returned attachment mutated to %#v", got)
	}
}

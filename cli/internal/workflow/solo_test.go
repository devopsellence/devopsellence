package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/keygen"
	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/discovery"
	"github.com/devopsellence/cli/internal/git"
	"github.com/devopsellence/cli/internal/output"
	"github.com/devopsellence/cli/internal/solo"
	cliversion "github.com/devopsellence/cli/internal/version"
)

func TestSoloImageTagSlugifiesProjectName(t *testing.T) {
	got := soloImageTag("ShopApp", "abc1234")
	if got != "shop-app:abc1234" {
		t.Fatalf("image tag = %q, want shop-app:abc1234", got)
	}
}

func TestDockerBuildArgsUsesConfiguredPlatform(t *testing.T) {
	got, err := dockerBuildArgs("/workspace", "/workspace/Dockerfile", "shop-app:abc1234", []string{"linux/amd64"})
	if err != nil {
		t.Fatalf("dockerBuildArgs() error = %v", err)
	}
	want := []string{"build", "--platform", "linux/amd64", "-f", "/workspace/Dockerfile", "-t", "shop-app:abc1234", "/workspace"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dockerBuildArgs() = %#v, want %#v", got, want)
	}
}

func TestDockerBuildArgsRejectsMultiplePlatforms(t *testing.T) {
	_, err := dockerBuildArgs("/workspace", "/workspace/Dockerfile", "shop-app:abc1234", []string{"linux/amd64", "linux/arm64"})
	if err == nil {
		t.Fatal("expected multiple-platform error")
	}
}

func TestValidateSoloNodeScheduleSelectsReleaseNode(t *testing.T) {
	cfg := &config.ProjectConfig{
		Services: map[string]config.ServiceConfig{
			config.DefaultWebServiceName: {
				Kind:  config.ServiceKindWeb,
				Ports: []config.ServicePort{{Name: "http", Port: 3000}},
				Healthcheck: &config.HTTPHealthcheck{
					Path: "/up",
					Port: 3000,
				},
			},
			"worker": {
				Kind:    config.ServiceKindWorker,
				Command: []string{"sidekiq"},
			},
		},
		Tasks: config.TasksConfig{
			Release: &config.TaskConfig{
				Service: config.DefaultWebServiceName,
				Command: []string{"rails", "db:migrate"},
			},
		},
	}
	nodes := map[string]config.SoloNode{
		"worker-a": {Labels: []string{config.DefaultWorkerRole}},
		"web-a":    {Labels: []string{config.DefaultWebRole}},
		"web-b":    {Labels: []string{config.DefaultWebRole}},
	}
	got, err := validateSoloNodeSchedule(cfg, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if got != "web-a" {
		t.Fatalf("release node = %q, want web-a", got)
	}
}

func TestValidateSoloNodeScheduleRejectsMissingWorker(t *testing.T) {
	cfg := &config.ProjectConfig{
		Services: map[string]config.ServiceConfig{
			config.DefaultWebServiceName: {
				Kind:  config.ServiceKindWeb,
				Ports: []config.ServicePort{{Name: "http", Port: 3000}},
				Healthcheck: &config.HTTPHealthcheck{
					Path: "/up",
					Port: 3000,
				},
			},
			"worker": {
				Kind:    config.ServiceKindWorker,
				Command: []string{"sidekiq"},
			},
		},
	}
	_, err := validateSoloNodeSchedule(cfg, map[string]config.SoloNode{
		"web-a": {Labels: []string{config.DefaultWebRole}},
	})
	if err == nil || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("expected missing worker error, got %v", err)
	}
}

func TestSoloNodeCanRunUnlabeledNode(t *testing.T) {
	node := config.SoloNode{}
	if !soloNodeCanRunKind(node, config.ServiceKindWeb) || !soloNodeCanRunKind(node, config.ServiceKindWorker) {
		t.Fatal("unlabeled node should run all labels")
	}
}

func TestParseSoloLabels(t *testing.T) {
	got, err := parseSoloLabels("web,worker web")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{config.DefaultWebRole, config.DefaultWorkerRole}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %#v, want %#v", got, want)
	}
}

func TestSoloNodeDesiredStateInputsUsesOtherAttachedNodesAsPeers(t *testing.T) {
	current := solo.State{
		Nodes: map[string]config.SoloNode{
			"web-a":    {Host: "203.0.113.10", Labels: []string{config.DefaultWebRole}},
			"web-b":    {Host: "203.0.113.11", Labels: []string{config.DefaultWebRole}},
			"worker-a": {Host: "203.0.113.12", Labels: []string{config.DefaultWorkerRole}},
			"private":  {Host: "203.0.113.13", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]solo.DeploySnapshot{},
	}
	attachment, changed, err := current.AttachNode("/workspace/demo", "production", "web-a")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("AttachNode() changed = false")
	}
	for _, nodeName := range []string{"web-b", "worker-a", "private"} {
		if _, _, err := current.AttachNode("/workspace/demo", "production", nodeName); err != nil {
			t.Fatal(err)
		}
	}
	key := attachment.WorkspaceKey + "\nproduction"
	current.Snapshots[key] = solo.DeploySnapshot{
		WorkspaceRoot: "/workspace/demo",
		WorkspaceKey:  attachment.WorkspaceKey,
		Environment:   "production",
		Revision:      "abc1234",
		Image:         "demo:abc1234",
	}

	_, _, got, _, err := soloNodeDesiredStateInputs(current, "web-a")
	if err != nil {
		t.Fatal(err)
	}
	want := []solo.NodePeer{
		{Name: "private", Labels: []string{config.DefaultWebRole}, PublicAddress: "203.0.113.13"},
		{Name: "web-b", Labels: []string{config.DefaultWebRole}, PublicAddress: "203.0.113.11"},
		{Name: "worker-a", Labels: []string{config.DefaultWorkerRole}, PublicAddress: "203.0.113.12"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("peers = %#v, want %#v", got, want)
	}
}

func TestCreateProviderNodeRejectsDeprecatedHetznerSize(t *testing.T) {
	app := &App{}

	_, err := app.createProviderNode(context.Background(), SoloNodeCreateOptions{
		Name:     "prod-1",
		Provider: providerHetzner,
		Region:   defaultHetznerRegion,
		Size:     "cx22",
	}, "")
	if err == nil {
		t.Fatal("expected deprecated size error")
	}
	if !strings.Contains(err.Error(), `Hetzner size "cx22" is deprecated; use "cpx11"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestCreateProviderNodeNormalizesHetznerProviderBeforeValidation(t *testing.T) {
	app := &App{}

	_, err := app.createProviderNode(context.Background(), SoloNodeCreateOptions{
		Name:     "prod-1",
		Provider: "Hetzner",
		Region:   defaultHetznerRegion,
		Size:     "cx22",
	}, "")
	if err == nil {
		t.Fatal("expected deprecated size error")
	}
	if !strings.Contains(err.Error(), `Hetzner size "cx22" is deprecated; use "cpx11"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestReleaseNodeForSnapshotSelectsSortedEligibleNode(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Tasks.Release = &config.TaskConfig{Service: "web", Command: []string{"bin/rails", "db:migrate"}}
	snapshot, err := solo.BuildDeploySnapshot(&cfg, "/workspace/demo", "/workspace/demo/devopsellence.yml", "demo:abc1234", "abc1234", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	attachment := solo.AttachmentRecord{
		WorkspaceKey: "/workspace/demo",
		Environment:  "production",
		NodeNames:    []string{"worker-a", "web-b", "web-a"},
	}
	nodes := map[string]config.SoloNode{
		"worker-a": {Labels: []string{config.DefaultWorkerRole}},
		"web-b":    {Labels: []string{config.DefaultWebRole}},
		"web-a":    {Labels: []string{config.DefaultWebRole}},
	}

	got, err := releaseNodeForSnapshot(snapshot, attachment, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if got != "web-a" {
		t.Fatalf("release node = %q, want web-a", got)
	}
}

func TestSoloAffectedNodesForNodeIncludesCoHostedNodes(t *testing.T) {
	current := solo.State{
		Nodes: map[string]config.SoloNode{
			"node-a": {},
			"node-b": {},
			"node-c": {},
		},
		Attachments: map[string]solo.AttachmentRecord{
			"/workspace/a\nproduction": {
				WorkspaceKey: "/workspace/a",
				Environment:  "production",
				NodeNames:    []string{"node-a", "node-b"},
			},
			"/workspace/b\nproduction": {
				WorkspaceKey: "/workspace/b",
				Environment:  "production",
				NodeNames:    []string{"node-a", "node-c"},
			},
		},
	}

	got := soloAffectedNodesForNode(current, "node-a")
	want := []string{"node-a", "node-b", "node-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("affected nodes = %#v, want %#v", got, want)
	}
}

func TestSoloStatusNodesWithoutAttachmentsReturnsEmptySet(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.SoloNode{
			"node-a": {Host: "203.0.113.10", User: "root"},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]solo.DeploySnapshot{},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Printer:     output.New(io.Discard, io.Discard, true),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}
	nodes, err := app.soloStatusNodes(SoloStatusOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 0 {
		t.Fatalf("nodes = %#v, want empty", nodes)
	}
}

func TestEnsureLocalSoloSnapshotImageReturnsActionableError(t *testing.T) {
	t.Parallel()

	app := &App{
		Docker: &fakeDocker{imageMetadataErr: errors.New("Error response from daemon: No such image: demo:missing")},
	}

	err := app.ensureLocalSoloSnapshotImage(context.Background(), "demo:missing")
	if err == nil {
		t.Fatal("expected missing image error")
	}
	if !strings.Contains(err.Error(), `local snapshot image "demo:missing" is unavailable`) {
		t.Fatalf("error = %v", err)
	}
}

func TestRepublishSoloNodesReportsRemoteDockerCheck(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Printer:     output.New(io.Discard, io.Discard, false),
		Docker:      &fakeDocker{imageMetadataErr: errors.New("Error response from daemon: No such image: demo:missing")},
		ConfigStore: config.NewStore(),
	}
	current := solo.State{
		Nodes: map[string]config.SoloNode{
			"web-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				NodeNames:     []string{"web-a"},
			},
		},
		Snapshots: map[string]solo.DeploySnapshot{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				Image:         "demo:missing",
				Metadata:      solo.SnapshotMetadata{ConfigPath: filepath.Join(workspaceRoot, "devopsellence.yml")},
			},
		},
	}

	_, err := app.republishSoloNodes(context.Background(), current, []string{"web-a"})
	if err == nil {
		t.Fatal("expected republish error")
	}
	if !strings.Contains(err.Error(), "[web-a] remote docker check:") {
		t.Fatalf("error = %v", err)
	}
}

func TestDesiredStateRevisionReadsRevision(t *testing.T) {
	t.Parallel()

	revision, err := desiredStateRevision([]byte(`{"revision":"abc123"}`))
	if err != nil {
		t.Fatal(err)
	}
	if revision != "abc123" {
		t.Fatalf("revision = %q, want abc123", revision)
	}
}

func TestEnsureSoloProjectConfigWritesDefaultConfig(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	cfg, gotRoot, err := app.ensureSoloProjectConfig()
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != workspaceRoot {
		t.Fatalf("workspace root = %q, want %q", gotRoot, workspaceRoot)
	}
	if cfg == nil {
		t.Fatal("config is nil")
	}
	if cfg.Organization != "solo" {
		t.Fatalf("organization = %q, want solo", cfg.Organization)
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "devopsellence.yml")); err != nil {
		t.Fatalf("expected config file: %v", err)
	}
}

func TestSoloNodeAttachPersistsDesiredStateOnRepublishError(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.SoloNode{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots: map[string]solo.DeploySnapshot{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				Revision:      "abc1234",
				Image:         "demo:missing",
				Metadata:      solo.SnapshotMetadata{ConfigPath: filepath.Join(workspaceRoot, "devopsellence.yml")},
			},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Printer:     output.New(io.Discard, io.Discard, false),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
		Docker:      &fakeDocker{imageMetadataErr: errors.New("Error response from daemon: No such image: demo:missing")},
	}

	if err := app.SoloNodeAttach(context.Background(), SoloNodeAttachOptions{Node: "node-a"}); err == nil {
		t.Fatal("expected attach to fail")
	}

	loaded, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Attachments) != 1 {
		t.Fatalf("attachments = %#v, want persisted desired attachment", loaded.Attachments)
	}
}

func TestSoloDeployWaitsForSettledStatusBeforeSuccess(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultProjectConfigForType("solo", "demo", "production", config.AppTypeGeneric)
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, workspaceRoot)

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.SoloNode{
			"node-a": {Host: "203.0.113.10", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]solo.DeploySnapshot{},
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	statusCountPath := installFakeSoloCommands(t, []fakeSSHResponse{
		{stdout: soloStatusMissingSentinel + "\n"},
		{stdout: `{"revision":"__REVISION__","phase":"reconciling"}` + "\n"},
		{stdout: `{"revision":"__REVISION__","phase":"settled"}` + "\n"},
	})

	var stdout bytes.Buffer
	app := &App{
		Printer:            output.New(&stdout, io.Discard, false),
		SoloState:          soloState,
		ConfigStore:        config.NewStore(),
		Git:                git.Client{},
		Cwd:                workspaceRoot,
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      200 * time.Millisecond,
	}

	if err := app.SoloDeploy(context.Background(), SoloDeployOptions{}); err != nil {
		t.Fatal(err)
	}

	if got := readFakeSSHStatusCount(t, statusCountPath); got != 3 {
		t.Fatalf("status poll count = %d, want 3", got)
	}
	outputText := stdout.String()
	if !strings.Contains(outputText, "Deployed revision") {
		t.Fatalf("stdout = %q, want deploy success line", outputText)
	}
}

func TestWaitForSoloRolloutIgnoresMissingAndStaleStatusUntilExpectedRevisionSettles(t *testing.T) {
	statusCountPath := installFakeSoloCommands(t, []fakeSSHResponse{
		{stdout: soloStatusMissingSentinel + "\n"},
		{stdout: `{"revision":"stale-revision","phase":"settled"}` + "\n"},
		{stdout: `{"revision":"expected-revision","phase":"reconciling"}` + "\n"},
		{stdout: `{"revision":"expected-revision","phase":"settled"}` + "\n"},
	})

	app := &App{
		Printer:            output.New(io.Discard, io.Discard, false),
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      200 * time.Millisecond,
	}

	err := app.waitForSoloRollout(context.Background(), map[string]config.SoloNode{
		"node-a": {Host: "203.0.113.10", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence"},
	}, map[string]string{
		"node-a": "expected-revision",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFakeSSHStatusCount(t, statusCountPath); got != 4 {
		t.Fatalf("status poll count = %d, want 4", got)
	}
}

func TestWaitForSoloRolloutFailsOnExpectedRevisionErrorPhase(t *testing.T) {
	installFakeSoloCommands(t, []fakeSSHResponse{
		{stdout: `{"revision":"expected-revision","phase":"error","error":"image pull failed"}` + "\n"},
	})

	app := &App{
		Printer:            output.New(io.Discard, io.Discard, false),
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      100 * time.Millisecond,
	}

	err := app.waitForSoloRollout(context.Background(), map[string]config.SoloNode{
		"node-a": {Host: "203.0.113.10", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence"},
	}, map[string]string{
		"node-a": "expected-revision",
	})
	if err == nil {
		t.Fatal("expected rollout failure")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want ExitError", err, err)
	}
	if !strings.Contains(err.Error(), "rollout failed on node-a: image pull failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestWaitForSoloRolloutTimesOutWhenExpectedRevisionNeverSettles(t *testing.T) {
	installFakeSoloCommands(t, []fakeSSHResponse{
		{stdout: `{"revision":"expected-revision","phase":"reconciling"}` + "\n"},
		{stdout: `{"revision":"expected-revision","phase":"reconciling"}` + "\n"},
		{stdout: `{"revision":"expected-revision","phase":"reconciling"}` + "\n"},
		{stdout: `{"revision":"expected-revision","phase":"reconciling"}` + "\n"},
		{stdout: `{"revision":"expected-revision","phase":"reconciling"}` + "\n"},
		{stdout: `{"revision":"expected-revision","phase":"reconciling"}` + "\n"},
	})

	app := &App{
		Printer:            output.New(io.Discard, io.Discard, false),
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      20 * time.Millisecond,
	}

	err := app.waitForSoloRollout(context.Background(), map[string]config.SoloNode{
		"node-a": {Host: "203.0.113.10", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence"},
	}, map[string]string{
		"node-a": "expected-revision",
	})
	if err == nil {
		t.Fatal("expected timeout")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want ExitError", err, err)
	}
	if !strings.Contains(err.Error(), "timed out waiting for solo rollout") {
		t.Fatalf("error = %v", err)
	}
}

func TestWaitForSoloRolloutFailsClearlyOnStatusReadAndParseErrors(t *testing.T) {
	tests := []struct {
		name      string
		responses []fakeSSHResponse
		want      string
	}{
		{
			name: "read error",
			responses: []fakeSSHResponse{
				{stderr: "permission denied\n", exitCode: 1},
			},
			want: "[node-a] read status: ssh root@203.0.113.10:",
		},
		{
			name: "invalid json",
			responses: []fakeSSHResponse{
				{stdout: "{not-json}\n"},
			},
			want: "[node-a] read status: invalid status JSON:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			installFakeSoloCommands(t, tt.responses)

			app := &App{
				Printer:            output.New(io.Discard, io.Discard, false),
				DeployPollInterval: 5 * time.Millisecond,
				DeployTimeout:      100 * time.Millisecond,
			}

			err := app.waitForSoloRollout(context.Background(), map[string]config.SoloNode{
				"node-a": {Host: "203.0.113.10", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence"},
			}, map[string]string{
				"node-a": "expected-revision",
			})
			if err == nil {
				t.Fatal("expected failure")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestParseSoloNodeStatusPayload(t *testing.T) {
	payload := []byte(`{"phase":"settled","revision":"abc123","environments":[{"name":"production","services":[{"name":"web","state":"running"}]}]}`)

	status, raw, err := parseSoloNodeStatusPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "settled" || status.Revision != "abc123" {
		t.Fatalf("status = %#v", status)
	}
	if string(raw) != string(payload) {
		t.Fatalf("raw = %q, want %q", raw, payload)
	}
}

func TestSoloAgentInstallScriptConfiguresSoloMode(t *testing.T) {
	script := soloAgentInstallScript(soloAgentInstallScriptOptions{BaseURL: "https://example.test"})
	for _, want := range []string{
		"--mode=solo",
		`--auth-state-path="/var/lib/devopsellence/auth.json"`,
		`--desired-state-override-path="/var/lib/devopsellence/desired-state-override.json"`,
		"AGENT_BIN=/usr/local/bin/devopsellence-agent",
		`ARTIFACT_NAME="agent-$OS-$ARCH"`,
		"BASE_URL='https://example.test'",
		"$BASE_URL/agent/download",
		"$BASE_URL/agent/checksums",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install script missing %q", want)
		}
	}
}

func TestSoloAgentInstallScriptUsesConfiguredStateDir(t *testing.T) {
	script := soloAgentInstallScript(soloAgentInstallScriptOptions{
		StateDir: "/tmp/devopsellence-test-state",
		BaseURL:  "https://example.test",
	})

	for _, want := range []string{
		"STATE_DIR='/tmp/devopsellence-test-state'",
		`--auth-state-path="/tmp/devopsellence-test-state/auth.json"`,
		`--desired-state-override-path="/tmp/devopsellence-test-state/desired-state-override.json"`,
		`--envoy-bootstrap-path="/tmp/devopsellence-test-state/envoy/envoy.yaml"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install script missing %q", want)
		}
	}
}

func TestSoloAgentInstallScriptQuotesSystemdExecStartPaths(t *testing.T) {
	script := soloAgentInstallScript(soloAgentInstallScriptOptions{
		StateDir: `/tmp/devopsellence state/"quoted"%value`,
		BaseURL:  "https://example.test",
	})

	for _, want := range []string{
		`--auth-state-path="/tmp/devopsellence state/\"quoted\"%%value/auth.json"`,
		`--desired-state-override-path="/tmp/devopsellence state/\"quoted\"%%value/desired-state-override.json"`,
		`--envoy-bootstrap-path="/tmp/devopsellence state/\"quoted\"%%value/envoy/envoy.yaml"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install script missing %q", want)
		}
	}
}

func TestSoloAgentInstallScriptPinsReleasedAgentVersionWhenProvided(t *testing.T) {
	script := soloAgentInstallScript(soloAgentInstallScriptOptions{
		BaseURL:      "https://example.test",
		AgentVersion: "v0.1.1",
	})

	for _, want := range []string{
		`AGENT_VERSION='v0.1.1'`,
		`DOWNLOAD_URL="$DOWNLOAD_URL&version=$AGENT_VERSION"`,
		`CHECKSUM_URL="$CHECKSUM_URL?version=$AGENT_VERSION"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install script missing %q", want)
		}
	}
}

func TestReleasedAgentVersionForInstall(t *testing.T) {
	original := cliversion.Version
	t.Cleanup(func() { cliversion.Version = original })

	cliversion.Version = "v0.1.1"
	if got := releasedAgentVersionForInstall(); got != "v0.1.1" {
		t.Fatalf("releasedAgentVersionForInstall() = %q, want v0.1.1", got)
	}

	cliversion.Version = "feature-branch-abc1234"
	if got := releasedAgentVersionForInstall(); got != "feature-branch-abc1234" {
		t.Fatalf("releasedAgentVersionForInstall() = %q, want prerelease tag", got)
	}

	cliversion.Version = "dev"
	if got := releasedAgentVersionForInstall(); got != "" {
		t.Fatalf("releasedAgentVersionForInstall() = %q, want empty for dev build", got)
	}

	cliversion.Version = "bad version?"
	if got := releasedAgentVersionForInstall(); got != "" {
		t.Fatalf("releasedAgentVersionForInstall() = %q, want empty for unsafe version", got)
	}
}

func TestRemoteDockerCommandsSupportPasswordlessSudo(t *testing.T) {
	for _, command := range []string{remoteDockerCheckCommand(), remoteDockerLoadCommand()} {
		if !strings.Contains(command, "sudo -n docker info") {
			t.Fatalf("command missing sudo docker check: %s", command)
		}
	}
	if !strings.Contains(remoteDockerLoadCommand(), "sudo -n docker load") {
		t.Fatalf("load command missing sudo docker load: %s", remoteDockerLoadCommand())
	}
}

func TestRemoteDesiredStateOverrideCommandSupportsPasswordlessSudo(t *testing.T) {
	command := remoteDesiredStateOverrideCommand("/var/lib/devopsellence/desired-state-override.json")
	for _, want := range []string{
		"command -v devopsellence-agent",
		"sudo -n true",
		"sudo -n \"$agent_bin\" desired-state set-override",
		"--override-path '/var/lib/devopsellence/desired-state-override.json'",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("desired state command missing %q: %s", want, command)
		}
	}
}

func TestRemoteReadAndJournalCommandsSupportPasswordlessSudo(t *testing.T) {
	readCommand := remoteReadFileCommand("/var/lib/devopsellence/status.json")
	for _, want := range []string{
		"sudo -n cat '/var/lib/devopsellence/status.json'",
		"exec cat '/var/lib/devopsellence/status.json'",
	} {
		if !strings.Contains(readCommand, want) {
			t.Fatalf("read command missing %q: %s", want, readCommand)
		}
	}

	journalCommand := remoteJournalctlCommand("-u devopsellence-agent --no-pager -n 100")
	if !strings.Contains(journalCommand, "sudo -n journalctl -u devopsellence-agent --no-pager -n 100") {
		t.Fatalf("journal command missing sudo journalctl: %s", journalCommand)
	}
}

func TestRemoteReadOptionalFileCommandSupportsPasswordlessSudo(t *testing.T) {
	command := remoteReadOptionalFileCommand("/var/lib/devopsellence/status.json", soloStatusMissingSentinel)
	for _, want := range []string{
		"sudo -n test -r '/var/lib/devopsellence/status.json'",
		"exec sudo -n cat '/var/lib/devopsellence/status.json'",
		"[ -e '/var/lib/devopsellence/status.json' ]",
		"sudo -n test -e '/var/lib/devopsellence/status.json'",
		"File exists but is not readable",
		"printf '%s\\n' '__DEVOPSELLENCE_STATUS_MISSING__'",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("optional read command missing %q: %s", want, command)
		}
	}
}

func TestApplySoloRailsMasterKeyUsesConfigMasterKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "master.key"), []byte("from-master-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.ProjectConfig{
		App: config.AppConfig{Type: config.AppTypeRails},
		Services: map[string]config.ServiceConfig{
			config.DefaultWebServiceName: {
				Kind:        config.ServiceKindWeb,
				Env:         map[string]string{},
				Ports:       []config.ServicePort{{Name: "http", Port: 3000}},
				Healthcheck: &config.HTTPHealthcheck{Path: "/up", Port: 3000},
			},
			"worker": {
				Kind: config.ServiceKindWorker,
				Env:  map[string]string{},
			},
		},
	}
	secrets := map[string]string{}
	notice, err := applySoloRailsMasterKey(dir, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if secrets[railsMasterKeySecretName] != "from-master-key" {
		t.Fatalf("RAILS_MASTER_KEY = %q", secrets[railsMasterKeySecretName])
	}
	if !strings.Contains(notice, "config/master.key") {
		t.Fatalf("notice = %q, want config/master.key", notice)
	}
	for _, serviceName := range []string{config.DefaultWebServiceName, "worker"} {
		refs := cfg.Services[serviceName].SecretRefs
		if len(refs) != 1 || refs[0].Name != railsMasterKeySecretName {
			t.Fatalf("secret refs = %#v, want RAILS_MASTER_KEY", refs)
		}
	}
}

func TestApplySoloRailsMasterKeyLetsEnvOverrideMasterKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "master.key"), []byte("from-master-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.ProjectConfig{
		App: config.AppConfig{Type: config.AppTypeRails},
		Services: map[string]config.ServiceConfig{
			config.DefaultWebServiceName: {
				Kind:        config.ServiceKindWeb,
				Env:         map[string]string{},
				Ports:       []config.ServicePort{{Name: "http", Port: 3000}},
				Healthcheck: &config.HTTPHealthcheck{Path: "/up", Port: 3000},
			},
		},
	}
	secrets := map[string]string{railsMasterKeySecretName: "from-env"}
	notice, err := applySoloRailsMasterKey(dir, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if secrets[railsMasterKeySecretName] != "from-env" {
		t.Fatalf("RAILS_MASTER_KEY = %q, want from-env", secrets[railsMasterKeySecretName])
	}
	if !strings.Contains(notice, ".env") {
		t.Fatalf("notice = %q, want .env", notice)
	}
}

func TestDefaultSoloSSHPublicKeyCandidates(t *testing.T) {
	got := defaultSoloSSHPublicKeyCandidates()
	if len(got) == 0 {
		t.Fatal("expected default public key candidates")
	}
	for _, candidate := range got {
		if !strings.HasSuffix(candidate, ".pub") {
			t.Fatalf("public key candidate = %q, want .pub suffix", candidate)
		}
	}
}

func TestGeneratedWorkspaceSSHKeyPathUsesCanonicalWorkspaceKey(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	realRoot := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(realRoot, linkRoot); err != nil {
		t.Fatal(err)
	}

	gotA, err := generatedWorkspaceSSHKeyPath(realRoot)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := generatedWorkspaceSSHKeyPath(filepath.Join(linkRoot, "."))
	if err != nil {
		t.Fatal(err)
	}
	if gotA != gotB {
		t.Fatalf("generatedWorkspaceSSHKeyPath() = %q, want %q", gotB, gotA)
	}
	wantPrefix := filepath.Join(stateDir, "devopsellence", "solo", "keys") + string(os.PathSeparator)
	if !strings.HasPrefix(gotA, wantPrefix) {
		t.Fatalf("generatedWorkspaceSSHKeyPath() = %q, want prefix %q", gotA, wantPrefix)
	}
}

func TestEnsureGeneratedWorkspaceSSHKeyGeneratesAndReusesKeyPair(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	workspaceRoot := t.TempDir()
	first, err := ensureGeneratedWorkspaceSSHKey(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Generated {
		t.Fatal("first.Generated = false, want true")
	}
	if first.PublicKeyPath != first.PrivateKeyPath+".pub" {
		t.Fatalf("public key path = %q, want %q", first.PublicKeyPath, first.PrivateKeyPath+".pub")
	}
	if strings.TrimSpace(first.PublicKey) == "" {
		t.Fatal("public key empty")
	}
	if strings.TrimSpace(first.Fingerprint) == "" {
		t.Fatal("fingerprint empty")
	}

	privateInfo, err := os.Stat(first.PrivateKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if privateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key perm = %#o, want 0600", privateInfo.Mode().Perm())
	}
	publicInfo, err := os.Stat(first.PublicKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if publicInfo.Mode().Perm() != 0o644 {
		t.Fatalf("public key perm = %#o, want 0644", publicInfo.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(first.PrivateKeyPath))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("key dir perm = %#o, want 0700", dirInfo.Mode().Perm())
	}

	second, err := ensureGeneratedWorkspaceSSHKey(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if second.Generated {
		t.Fatal("second.Generated = true, want false")
	}
	if second.PrivateKeyPath != first.PrivateKeyPath {
		t.Fatalf("private key path = %q, want %q", second.PrivateKeyPath, first.PrivateKeyPath)
	}
	if second.PublicKey != first.PublicKey {
		t.Fatalf("public key = %q, want %q", second.PublicKey, first.PublicKey)
	}
	if second.Fingerprint != first.Fingerprint {
		t.Fatalf("fingerprint = %q, want %q", second.Fingerprint, first.Fingerprint)
	}
}

func TestEnsureGeneratedWorkspaceSSHKeyRejectsPartialKeypair(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	workspaceRoot := t.TempDir()
	privateKeyPath, err := generatedWorkspaceSSHKeyPath(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(privateKeyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateKeyPath, []byte("not-a-real-key"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = ensureGeneratedWorkspaceSSHKey(workspaceRoot)
	if err == nil {
		t.Fatal("expected partial keypair error")
	}
	if !strings.Contains(err.Error(), "missing public key") {
		t.Fatalf("error = %v, want missing public key", err)
	}
}

func TestEnsureGeneratedWorkspaceSSHKeyRejectsMismatchedPublicKey(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	workspaceRoot := t.TempDir()
	first, err := ensureGeneratedWorkspaceSSHKey(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}

	otherKeyPath := filepath.Join(t.TempDir(), "other_id_ed25519")
	otherPair, err := keygen.New(otherKeyPath, keygen.WithKeyType(keygen.Ed25519), keygen.WithWrite())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first.PublicKeyPath, []byte(otherPair.AuthorizedKey()+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err = ensureGeneratedWorkspaceSSHKey(workspaceRoot)
	if err == nil {
		t.Fatal("expected mismatched keypair error")
	}
	if !strings.Contains(err.Error(), "does not match private key") {
		t.Fatalf("error = %v, want key mismatch", err)
	}
}

func TestSoloSetupHetznerDefaultsToGeneratedWorkspaceKey(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var created SoloNodeCreateOptions
	app := &App{
		In:          strings.NewReader("hetzner\nprod-1\nweb\nash\ncpx11\n\n"),
		Printer:     output.New(&stdout, io.Discard, false),
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
		soloNodeCreateFn: func(_ context.Context, opts SoloNodeCreateOptions) error {
			created = opts
			return nil
		},
		soloNodeAttachFn:    func(context.Context, SoloNodeAttachOptions) error { return nil },
		soloRuntimeDoctorFn: func(context.Context, SoloDoctorOptions) error { return nil },
	}
	app.Printer.Interactive = true

	if err := app.SoloSetup(context.Background(), SoloSetupOptions{}); err != nil {
		t.Fatal(err)
	}
	if created.SSHPublicKey == "" {
		t.Fatal("generated SSH public key path empty")
	}
	if !strings.HasSuffix(created.SSHPublicKey, ".pub") {
		t.Fatalf("SSH public key path = %q, want .pub suffix", created.SSHPublicKey)
	}
	if _, err := os.Stat(created.SSHPublicKey); err != nil {
		t.Fatalf("expected generated public key: %v", err)
	}
	if _, err := os.Stat(strings.TrimSuffix(created.SSHPublicKey, ".pub")); err != nil {
		t.Fatalf("expected generated private key: %v", err)
	}
	if !strings.Contains(stdout.String(), "workspace SSH key") {
		t.Fatalf("output = %q, want workspace SSH key message", stdout.String())
	}
}

func TestSoloSetupHetznerExistingUsesPromptedPublicKeyPath(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	customPublicKey := filepath.Join(t.TempDir(), "custom.pub")
	if err := os.WriteFile(customPublicKey, []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBexample\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var created SoloNodeCreateOptions
	app := &App{
		In:          strings.NewReader("hetzner\nprod-1\nweb\nash\ncpx11\nexisting\n" + customPublicKey + "\n"),
		Printer:     output.New(io.Discard, io.Discard, false),
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
		soloNodeCreateFn: func(_ context.Context, opts SoloNodeCreateOptions) error {
			created = opts
			return nil
		},
		soloNodeAttachFn:    func(context.Context, SoloNodeAttachOptions) error { return nil },
		soloRuntimeDoctorFn: func(context.Context, SoloDoctorOptions) error { return nil },
	}
	app.Printer.Interactive = true

	if err := app.SoloSetup(context.Background(), SoloSetupOptions{}); err != nil {
		t.Fatal(err)
	}
	if created.SSHPublicKey != customPublicKey {
		t.Fatalf("SSH public key path = %q, want %q", created.SSHPublicKey, customPublicKey)
	}
}

func TestWaitForSoloSSHWithProbeReturnsLastError(t *testing.T) {
	node := config.SoloNode{User: "root", Host: "203.0.113.10"}
	wantErr := errors.New("ssh: connect to host 203.0.113.10 port 22: Connection timed out")

	err := waitForSoloSSHWithProbe(context.Background(), node, 30*time.Millisecond, 5*time.Millisecond, 1*time.Millisecond, func(context.Context) error {
		return wantErr
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "last error: ssh: connect to host 203.0.113.10 port 22: Connection timed out") {
		t.Fatalf("error = %q, want last ssh error included", err)
	}
}

func TestWaitForSoloSSHWithProbeBoundsSingleAttempt(t *testing.T) {
	node := config.SoloNode{User: "root", Host: "203.0.113.10"}

	start := time.Now()
	err := waitForSoloSSHWithProbe(context.Background(), node, 20*time.Millisecond, 5*time.Millisecond, 1*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("waitForSoloSSHWithProbe() took %s, want bounded retries", elapsed)
	}
}

func TestSoloDefaultProjectConfigUsesDiscovery(t *testing.T) {
	cfg := soloDefaultProjectConfig(discovery.Result{
		ProjectName:     "ShopApp",
		AppType:         config.AppTypeGeneric,
		InferredWebPort: 8080,
	})
	if cfg.Organization != "solo" || cfg.Project != "ShopApp" {
		t.Fatalf("config identity = org %q project %q", cfg.Organization, cfg.Project)
	}
	if cfg.App.Type != config.AppTypeGeneric {
		t.Fatalf("app.type = %q", cfg.App.Type)
	}
	web := cfg.Services[config.DefaultWebServiceName]
	if web.HTTPPort(0) != 8080 || web.Healthcheck.Port != 8080 {
		t.Fatalf("web port = %d healthcheck port = %d, want 8080", web.HTTPPort(0), web.Healthcheck.Port)
	}
}

func TestIngressSetInfersPrimaryWebService(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := config.DefaultProjectConfigForType("solo", "demo", config.DefaultEnvironment, config.AppTypeGeneric)
	if _, err := config.Write(dir, cfg); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Cwd:         dir,
		ConfigStore: config.NewStore(),
		Printer:     output.New(io.Discard, io.Discard, false),
	}
	if err := app.IngressSet(context.Background(), IngressSetOptions{
		Hosts:   []string{"demo.devopsellence.io"},
		TLSMode: "auto",
	}); err != nil {
		t.Fatalf("IngressSet() error = %v", err)
	}

	written, err := config.Load(filepath.Join(dir, config.FilePath))
	if err != nil {
		t.Fatal(err)
	}
	if written.Ingress == nil {
		t.Fatal("ingress = nil, want populated ingress config")
	}
	if written.Ingress.Service != config.DefaultWebServiceName {
		t.Fatalf("ingress.service = %q, want %q", written.Ingress.Service, config.DefaultWebServiceName)
	}
}

type fakeSSHResponse struct {
	stdout   string
	stderr   string
	exitCode int
}

func installFakeSoloCommands(t *testing.T, statusResponses []fakeSSHResponse) string {
	t.Helper()

	binDir := t.TempDir()
	statusDir := filepath.Join(t.TempDir(), "status")
	if err := os.MkdirAll(statusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for idx, response := range statusResponses {
		base := filepath.Join(statusDir, fmt.Sprintf("%d", idx+1))
		if response.stdout != "" {
			if err := os.WriteFile(base+".stdout", []byte(response.stdout), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if response.stderr != "" {
			if err := os.WriteFile(base+".stderr", []byte(response.stderr), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		code := response.exitCode
		if err := os.WriteFile(base+".code", []byte(fmt.Sprintf("%d\n", code)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	statusCountPath := filepath.Join(t.TempDir(), "status-count")
	revisionPath := filepath.Join(t.TempDir(), "desired-state.json")
	writeExecutable(t, filepath.Join(binDir, "docker"), "#!/usr/bin/env bash\nset -euo pipefail\nif [ \"$1\" = \"build\" ]; then exit 0; fi\necho \"unexpected docker command: $*\" >&2\nexit 1\n")
	writeExecutable(t, filepath.Join(binDir, "ssh"), `#!/usr/bin/env bash
set -euo pipefail
command="${!#}"

if [[ "$command" == *"desired-state set-override"* ]]; then
  cat >"$DEVOPSELLENCE_FAKE_SSH_REVISION_FILE"
  exit 0
fi

if [[ "$command" == *"docker image inspect"* ]]; then
  printf 'present\n'
  exit 0
fi

if [[ "$command" == *"docker info"* ]]; then
  exit 0
fi

if [[ "$command" == *"status.json"* ]]; then
  index=0
  if [[ -f "$DEVOPSELLENCE_FAKE_SSH_STATUS_COUNT" ]]; then
    index="$(cat "$DEVOPSELLENCE_FAKE_SSH_STATUS_COUNT")"
  fi
  index=$((index + 1))
  printf '%s' "$index" >"$DEVOPSELLENCE_FAKE_SSH_STATUS_COUNT"
  base="$DEVOPSELLENCE_FAKE_SSH_STATUS_DIR/$index"
  revision=''
  if [[ -f "$DEVOPSELLENCE_FAKE_SSH_REVISION_FILE" ]]; then
    revision="$(sed -n 's/.*"revision"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$DEVOPSELLENCE_FAKE_SSH_REVISION_FILE" | head -n1)"
  fi
  if [[ -f "$base.stdout" ]]; then
    if [[ -n "$revision" ]]; then
      sed "s/__REVISION__/$revision/g" "$base.stdout"
    else
      cat "$base.stdout"
    fi
  fi
  if [[ -f "$base.stderr" ]]; then
    cat "$base.stderr" >&2
  fi
  code=0
  if [[ -f "$base.code" ]]; then
    code="$(cat "$base.code")"
  fi
  exit "$code"
fi

echo "unexpected ssh command: $command" >&2
exit 1
`)

	t.Setenv("DEVOPSELLENCE_FAKE_SSH_STATUS_DIR", statusDir)
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_STATUS_COUNT", statusCountPath)
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_REVISION_FILE", revisionPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return statusCountPath
}

func readFakeSSHStatusCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read status count: %v", err)
	}
	var count int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &count); err != nil {
		t.Fatalf("parse status count %q: %v", data, err)
	}
	return count
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func commitTestRepo(t *testing.T, dir string) string {
	t.Helper()
	runTestCommand(t, dir, "git", "init")
	runTestCommand(t, dir, "git", "add", ".")
	runTestCommand(t, dir, "git", "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit", "-m", "init")
	return strings.TrimSpace(runTestCommand(t, dir, "git", "rev-parse", "HEAD"))
}

func runTestCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestIngressSetPreservesExistingServiceWhenFlagOmitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := config.DefaultProjectConfigForType("solo", "demo", config.DefaultEnvironment, config.AppTypeGeneric)
	cfg.Services["frontend"] = cfg.Services[config.DefaultWebServiceName]
	delete(cfg.Services, config.DefaultWebServiceName)
	cfg.Ingress = &config.IngressConfig{
		Service: "frontend",
		Hosts:   []string{"old.devopsellence.io"},
		TLS:     config.IngressTLSConfig{Mode: "manual"},
	}
	if _, err := config.Write(dir, cfg); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Cwd:         dir,
		ConfigStore: config.NewStore(),
		Printer:     output.New(io.Discard, io.Discard, false),
	}
	if err := app.IngressSet(context.Background(), IngressSetOptions{
		Hosts:   []string{"new.devopsellence.io"},
		TLSMode: "auto",
	}); err != nil {
		t.Fatalf("IngressSet() error = %v", err)
	}

	written, err := config.Load(filepath.Join(dir, config.FilePath))
	if err != nil {
		t.Fatal(err)
	}
	if written.Ingress == nil {
		t.Fatal("ingress = nil, want populated ingress config")
	}
	if written.Ingress.Service != "frontend" {
		t.Fatalf("ingress.service = %q, want frontend", written.Ingress.Service)
	}
}

func TestLineProgressWriterEmitsLines(t *testing.T) {
	var got []string
	writer := &lineProgressWriter{progress: func(line string) { got = append(got, line) }}
	if _, err := writer.Write([]byte("progress: installing Docker\nplain line\npartial")); err != nil {
		t.Fatal(err)
	}
	writer.Flush()
	want := []string{"installing Docker", "plain line", "partial"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("progress lines = %#v, want %#v", got, want)
	}
}

func TestProgressReaderReportsBytes(t *testing.T) {
	var got []string
	reader := &progressReader{
		reader:      bytes.NewBufferString("abcdef"),
		reportEvery: 3,
		nextReport:  3,
		progress:    func(line string) { got = append(got, line) },
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "abcdef" {
		t.Fatalf("data = %q", data)
	}
	if reader.Total() != 6 {
		t.Fatalf("total = %d, want 6", reader.Total())
	}
	if len(got) == 0 || !strings.Contains(got[len(got)-1], "compressed") {
		t.Fatalf("progress = %#v, want compressed progress", got)
	}
}

package workflow

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/discovery"
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
				Command: "sidekiq",
			},
		},
		Tasks: config.TasksConfig{
			Release: &config.TaskConfig{
				Service: config.DefaultWebServiceName,
				Command: "rails db:migrate",
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
				Command: "sidekiq",
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
	cfg.Tasks.Release = &config.TaskConfig{Service: "web", Command: "bin/rails db:migrate"}
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

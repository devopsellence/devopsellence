package workflow

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/devopsellence/cli/internal/discovery"
	"github.com/devopsellence/cli/internal/git"
	"github.com/devopsellence/cli/internal/output"
	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/cli/internal/solo/providers"
	"github.com/devopsellence/cli/internal/state"
	cliversion "github.com/devopsellence/cli/internal/version"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
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

func TestTailBufferExactLimitWriteIsNotTruncated(t *testing.T) {
	buf := newTailBuffer(5)

	n, err := buf.Write([]byte("abcde"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 5 {
		t.Fatalf("Write() n = %d, want 5", n)
	}
	if got := buf.String(); got != "abcde" {
		t.Fatalf("String() = %q, want %q", got, "abcde")
	}
}

func TestTailBufferExactLimitWriteAfterExistingDataIsTruncated(t *testing.T) {
	buf := newTailBuffer(5)
	if _, err := buf.Write([]byte("ab")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	n, err := buf.Write([]byte("cdefg"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 5 {
		t.Fatalf("Write() n = %d, want 5", n)
	}
	if got := buf.String(); got != "[truncated]\ncdefg" {
		t.Fatalf("String() = %q, want bounded tail", got)
	}
}

func TestTailBufferKeepsOnlyBoundedTail(t *testing.T) {
	buf := newTailBuffer(10)

	n, err := buf.Write([]byte("abcdef"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 6 {
		t.Fatalf("Write() n = %d, want 6", n)
	}
	if got := buf.String(); got != "abcdef" {
		t.Fatalf("String() = %q, want %q", got, "abcdef")
	}

	n, err = buf.Write([]byte("ghijklmnop"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 10 {
		t.Fatalf("Write() n = %d, want 10", n)
	}
	if got := buf.String(); got != "[truncated]\nghijklmnop" {
		t.Fatalf("String() = %q, want bounded tail", got)
	}
}

func TestTailBufferLargeWriteKeepsOnlyBoundedTail(t *testing.T) {
	buf := newTailBuffer(5)

	n, err := buf.Write([]byte("abcdefghijklmnopqrstuvwxyz"))
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 26 {
		t.Fatalf("Write() n = %d, want 26", n)
	}
	if got := buf.String(); got != "[truncated]\nvwxyz" {
		t.Fatalf("String() = %q, want bounded tail", got)
	}
}

func TestSSHInteractiveErrorIncludesCapturedOutput(t *testing.T) {
	err := errors.New("exit status 1")

	cases := []struct {
		name   string
		stdout string
		stderr string
		want   string
	}{
		{
			name:   "stderr and stdout",
			stdout: "  boot failed\n",
			stderr: "  permission denied\n",
			want:   "failed to run install command over SSH: exit status 1; stderr: permission denied; stdout: boot failed",
		},
		{
			name:   "stderr only",
			stderr: "  permission denied\n",
			want:   "failed to run install command over SSH: exit status 1; stderr: permission denied",
		},
		{
			name:   "stdout only",
			stdout: "  boot failed\n",
			want:   "failed to run install command over SSH: exit status 1; stdout: boot failed",
		},
		{
			name: "no captured output",
			want: "failed to run install command over SSH: exit status 1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sshInteractiveError("failed to run install command over SSH", err, tc.stdout, tc.stderr)
			if got.Error() != tc.want {
				t.Fatalf("error = %q, want %q", got.Error(), tc.want)
			}
			if !errors.Is(got, err) {
				t.Fatalf("error does not wrap original error")
			}
		})
	}
}

func TestSoloDefaultProjectConfigBootstrapsExplicitCatchAllIngress(t *testing.T) {
	t.Parallel()

	cfg := soloDefaultProjectConfig(discovery.Result{
		ProjectName:     "shop-app",
		AppType:         config.AppTypeRails,
		InferredWebPort: 3001,
	})

	if cfg.Ingress == nil {
		t.Fatal("expected bootstrapped ingress")
	}
	if len(cfg.Ingress.Rules) != 1 {
		t.Fatalf("ingress.rules = %#v, want single root rule", cfg.Ingress.Rules)
	}
	if got, want := cfg.Ingress.Rules[0].Target.Service, config.DefaultWebServiceName; got != want {
		t.Fatalf("ingress.rules[0].target.service = %q, want %q", got, want)
	}
	if got, want := cfg.Ingress.Rules[0].Target.Port, "http"; got != want {
		t.Fatalf("ingress.rules[0].target.port = %q, want %q", got, want)
	}
	if got, want := cfg.Ingress.Rules[0].Match.Host, "*"; got != want {
		t.Fatalf("ingress.rules[0].match.host = %q, want %q", got, want)
	}
	if got, want := cfg.Ingress.Rules[0].Match.PathPrefix, "/"; got != want {
		t.Fatalf("ingress.rules[0].match.path_prefix = %q, want %q", got, want)
	}
	if got, want := cfg.Ingress.Hosts, []string{"*"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ingress.hosts = %#v, want %#v", got, want)
	}
	if got, want := cfg.Ingress.TLS.Mode, "off"; got != want {
		t.Fatalf("ingress.tls.mode = %q, want %q", got, want)
	}
	if cfg.Ingress.RedirectHTTP == nil {
		t.Fatal("expected explicit ingress.redirect_http=false")
	}
	if *cfg.Ingress.RedirectHTTP {
		t.Fatal("ingress.redirect_http = true, want false")
	}
	web := cfg.Services[config.DefaultWebServiceName]
	if got, want := web.HTTPPort(0), 3001; got != want {
		t.Fatalf("web http port = %d, want %d", got, want)
	}
}

func TestValidateNodeScheduleSelectsReleaseNode(t *testing.T) {
	cfg := &config.ProjectConfig{
		Services: map[string]config.ServiceConfig{
			config.DefaultWebServiceName: {
				Ports: []config.ServicePort{{Name: "http", Port: 3000}},
				Healthcheck: &config.HTTPHealthcheck{
					Path: "/up",
					Port: 3000,
				},
			},
			"worker": {
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
	nodes := map[string]config.Node{
		"worker-a": {Labels: []string{config.DefaultWorkerRole}},
		"web-a":    {Labels: []string{config.DefaultWebRole}},
		"web-b":    {Labels: []string{config.DefaultWebRole}},
	}
	got, err := validateNodeSchedule(cfg, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if got != "web-a" {
		t.Fatalf("release node = %q, want web-a", got)
	}
}

func TestValidateNodeScheduleRejectsMissingWorker(t *testing.T) {
	cfg := &config.ProjectConfig{
		Services: map[string]config.ServiceConfig{
			config.DefaultWebServiceName: {
				Ports: []config.ServicePort{{Name: "http", Port: 3000}},
				Healthcheck: &config.HTTPHealthcheck{
					Path: "/up",
					Port: 3000,
				},
			},
			"worker": {
				Command: []string{"sidekiq"},
			},
		},
	}
	_, err := validateNodeSchedule(cfg, map[string]config.Node{
		"web-a": {Labels: []string{config.DefaultWebRole}},
	})
	if err == nil || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("expected missing worker error, got %v", err)
	}
}

func TestValidateNodeScheduleInfersKindsWithoutStoredConfigKind(t *testing.T) {
	cfg := &config.ProjectConfig{
		Services: map[string]config.ServiceConfig{
			config.DefaultWebServiceName: {
				Ports: []config.ServicePort{{Name: "http", Port: 3000}},
				Healthcheck: &config.HTTPHealthcheck{
					Path: "/up",
					Port: 3000,
				},
			},
			"worker": {
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
	nodes := map[string]config.Node{
		"worker-a": {Labels: []string{config.DefaultWorkerRole}},
		"web-a":    {Labels: []string{config.DefaultWebRole}},
	}

	got, err := validateNodeSchedule(cfg, nodes)
	if err != nil {
		t.Fatalf("validateNodeSchedule() error = %v", err)
	}
	if got != "web-a" {
		t.Fatalf("release node = %q, want web-a", got)
	}
}

func TestNodeCanRunUnlabeledNode(t *testing.T) {
	node := config.Node{}
	if !soloNodeCanRunKind(node, config.ServiceKindWeb) || !soloNodeCanRunKind(node, config.ServiceKindWorker) {
		t.Fatal("unlabeled node should run all labels")
	}
}

func TestNodeCanRunIngressRequiresAllIngressTargetServices(t *testing.T) {
	cfg := &config.ProjectConfig{
		Services: map[string]config.ServiceConfig{
			"web": {
				Ports: []config.ServicePort{{Name: "http", Port: 3000}},
			},
			"api": {
				Ports: []config.ServicePort{{Name: "metrics", Port: 9090}},
			},
		},
		Ingress: &config.IngressConfig{Rules: []config.IngressRuleConfig{
			{Target: config.IngressTargetConfig{Service: "web", Port: "http"}},
			{Target: config.IngressTargetConfig{Service: "api", Port: "metrics"}},
		}},
	}

	if soloNodeCanRunIngress(config.Node{Labels: []string{config.DefaultWebRole}}, cfg) {
		t.Fatal("web-only node should not run ingress for mixed web/worker targets")
	}
	if !soloNodeCanRunIngress(config.Node{Labels: []string{config.DefaultWebRole, config.DefaultWorkerRole}}, cfg) {
		t.Fatal("web+worker node should run ingress when it hosts all targets")
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

func TestNodeDesiredStateInputsUsesOtherAttachedNodesAsPeers(t *testing.T) {
	current := solo.State{
		Nodes: map[string]config.Node{
			"web-a":    {Host: "203.0.113.10", Labels: []string{config.DefaultWebRole}},
			"web-b":    {Host: "203.0.113.11", Labels: []string{config.DefaultWebRole}},
			"worker-a": {Host: "203.0.113.12", Labels: []string{config.DefaultWorkerRole}},
			"private":  {Host: "203.0.113.13", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]desiredstate.DeploySnapshot{},
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
	current.Snapshots[key] = desiredstate.DeploySnapshot{
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
	want := []desiredstate.NodePeer{
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
	}, "", nil)
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
	}, "", nil)
	if err == nil {
		t.Fatal("expected deprecated size error")
	}
	if !strings.Contains(err.Error(), `Hetzner size "cx22" is deprecated; use "cpx11"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestSoloNodeCreateRejectsDuplicateAfterTrimmingName(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{Nodes: map[string]config.Node{"prod-1": {Host: "203.0.113.10", User: "root"}}}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}
	app := &App{Printer: output.New(io.Discard, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}

	err := app.SoloNodeCreate(context.Background(), SoloNodeCreateOptions{Name: " prod-1 ", Host: "203.0.113.11", User: "root"})
	if err == nil || !strings.Contains(err.Error(), `solo node "prod-1" already exists`) {
		t.Fatalf("error = %v", err)
	}
}

func TestSoloNodeCreateRegistersExistingSSHNode(t *testing.T) {
	installFakeSoloCommands(t, nil)

	home := t.TempDir()
	t.Setenv("HOME", home)
	wantKey := filepath.Join(home, ".ssh", "id_ed25519")
	if err := os.MkdirAll(filepath.Dir(wantKey), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wantKey, []byte("private key"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	if err := soloState.Write(solo.State{}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}
	err := app.SoloNodeCreate(context.Background(), SoloNodeCreateOptions{
		Name:   "prod-1",
		Host:   "203.0.113.10",
		User:   "deploy",
		Port:   2222,
		SSHKey: "~/.ssh/id_ed25519",
		Labels: "web,worker",
		Attach: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	current, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	node := current.Nodes["prod-1"]
	if node.Host != "203.0.113.10" || node.User != "deploy" || node.Port != 2222 || node.SSHKey != wantKey {
		t.Fatalf("node = %#v, want ssh key %q", node, wantKey)
	}
	attached, err := current.AttachedNodeNames(workspaceRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(attached, []string{"prod-1"}) {
		t.Fatalf("attached nodes = %#v", attached)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["source"] != "existing_ssh" || payload["ssh_checked"] != true || payload["attached"] != true || payload["agent_installed"] != false {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSoloNodeCreateValidatesExistingSSHBeforeWritingState(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "ssh"), "#!/usr/bin/env bash\necho 'Permission denied (publickey).' >&2\nexit 255\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	if err := soloState.Write(solo.State{}); err != nil {
		t.Fatal(err)
	}
	app := &App{
		Printer:     output.New(io.Discard, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	err := app.SoloNodeCreate(context.Background(), SoloNodeCreateOptions{Name: "prod-1", Host: "203.0.113.10", User: "root", Port: 22})
	if err == nil {
		t.Fatal("expected SSH validation error")
	}
	if !strings.Contains(err.Error(), "node create could not validate SSH") || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("error = %v, want SSH validation context", err)
	}
	loaded, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Nodes["prod-1"]; ok {
		t.Fatalf("node was written despite SSH validation failure: %#v", loaded.Nodes)
	}
}

func TestSoloNodeCreateProviderReportsMetadataAndProgress(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	if err := soloState.Write(solo.State{}); err != nil {
		t.Fatal(err)
	}
	providerState := state.New(filepath.Join(t.TempDir(), "providers.json"))
	if err := saveProviderToken(providerState, providerHetzner, "test-token"); err != nil {
		t.Fatal(err)
	}
	fakeProvider := &fakeSoloProvider{
		createServer: providers.Server{ID: "srv-1", Name: "prod-1", Status: "running", PublicIP: "203.0.113.20"},
	}
	var stdout, stderr bytes.Buffer
	app := &App{
		Printer:       output.New(&stdout, &stderr),
		SoloState:     soloState,
		ProviderState: providerState,
		ConfigStore:   config.NewStore(),
		Cwd:           workspaceRoot,
		soloProviderFn: func(slug string) (providers.Provider, error) {
			if slug != providerHetzner {
				t.Fatalf("provider slug = %q, want hetzner", slug)
			}
			return fakeProvider, nil
		},
	}

	err := app.SoloNodeCreate(context.Background(), SoloNodeCreateOptions{Name: "prod-1", Provider: "hetzner", Region: "ash", Size: "cpx11", Image: "  "})
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["provider"] != providerHetzner || payload["provider_server_id"] != "srv-1" || payload["provider_region"] != "ash" || payload["provider_size"] != "cpx11" || payload["provider_image"] != providers.DefaultHetznerImage {
		t.Fatalf("payload = %#v, want provider metadata", payload)
	}
	if fakeProvider.createInput.Image != providers.DefaultHetznerImage {
		t.Fatalf("CreateServer image = %q, want normalized default image", fakeProvider.createInput.Image)
	}
	progress := stderr.String()
	if !strings.Contains(progress, "Creating hetzner server") || !strings.Contains(progress, "Server srv-1 ready at 203.0.113.20") {
		t.Fatalf("progress = %q, want provider create/ready events", progress)
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
	nodes := map[string]config.Node{
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
		Nodes: map[string]config.Node{
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

func TestSoloStatusIncludesPublicURLs(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "*", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "off"},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	statusJSON := `{"time":"2026-04-27T10:42:45Z","revision":"rev","phase":"settled","summary":{"environments":0,"services":0}}`
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: statusJSON}, {stdout: statusJSON}})

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
			"node-b": {Host: "203.0.113.11", User: "root", Labels: []string{config.DefaultWorkerRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				NodeNames:     []string{"node-a", "node-b"},
			},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}
	if err := app.SoloStatus(context.Background(), SoloStatusOptions{Nodes: []string{"node-a", "node-b"}}); err != nil {
		t.Fatalf("SoloStatus() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	urls := jsonArrayFromMap(t, payload, "public_urls")
	if len(urls) != 1 || urls[0] != "http://203.0.113.10/" {
		t.Fatalf("public_urls = %#v, want web node URL only", urls)
	}
}

func TestSoloStatusUsesConfiguredPublicURLsWhenNodeIsNotSettled(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "*", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "off"},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: `{"revision":"rev","phase":"error","error":"healthcheck failed"}` + "\n"}})

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				NodeNames:     []string{"node-a"},
			},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}
	if err := app.SoloStatus(context.Background(), SoloStatusOptions{Nodes: []string{"node-a"}}); err != nil {
		t.Fatalf("SoloStatus() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if _, ok := payload["public_urls"]; ok {
		t.Fatalf("payload = %#v, did not expect public_urls while node is errored", payload)
	}
	urls := jsonArrayFromMap(t, payload, "configured_public_urls")
	if len(urls) != 1 || urls[0] != "http://203.0.113.10/" {
		t.Fatalf("configured_public_urls = %#v, want node URL", urls)
	}
	warnings := jsonArrayFromMap(t, payload, "warnings")
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "not settled") {
		t.Fatalf("warnings = %#v, want not-settled warning", warnings)
	}
}

func TestSoloStatusPublicURLsUseHTTPSForManualTLS(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"app.example.com,api.example.com", "app.example.com"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: "manual"},
	}

	urls := soloStatusPublicURLs(&cfg, map[string]config.Node{
		"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
	})
	want := []string{"https://api.example.com/", "https://app.example.com/"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("public_urls = %#v, want %#v", urls, want)
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
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root"},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]desiredstate.DeploySnapshot{},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Printer:     output.New(io.Discard, io.Discard),
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

func TestSoloDoctorReturnsFailureWhenLocalChecksFail(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		Docker:      &fakeDocker{},
		SoloState:   solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json")),
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	err := app.SoloDoctor(context.Background())
	if err == nil {
		t.Fatal("SoloDoctor() error = nil, want failure")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("error = %#v, want ExitError code 1", err)
	}
	var renderedErr RenderedError
	if !errors.As(exitErr.Err, &renderedErr) {
		t.Fatalf("exit error = %#v, want RenderedError", exitErr.Err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ok"] != false {
		t.Fatalf("payload ok = %v, want false", payload["ok"])
	}
}

func TestSoloStatusReturnsFailureWhenNodeStatusReadFails(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, []fakeSSHResponse{
		{stderr: "permission denied\n", exitCode: 1},
	})

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				NodeNames:     []string{"node-a"},
			},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	err := app.SoloStatus(context.Background(), SoloStatusOptions{})
	if err == nil {
		t.Fatal("expected status failure")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("error = %#v, want ExitError code 1", err)
	}
	var renderedErr RenderedError
	if !errors.As(exitErr.Err, &renderedErr) {
		t.Fatalf("exit error = %#v, want RenderedError", exitErr.Err)
	}
	payload := decodeJSONOutput(t, &stdout)
	nodes := jsonArrayFromMap(t, payload, "nodes")
	node := jsonMapFromAny(t, nodes[0])
	if node["node"] != "node-a" || !strings.Contains(stringValueAny(node["error"]), "ssh root@203.0.113.10:") {
		t.Fatalf("node payload = %#v, want node read error", node)
	}
}

func TestSoloLogsUsesRequestedLineLimit(t *testing.T) {
	commandPath := filepath.Join(t.TempDir(), "journal-command")
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_JOURNAL_COMMAND", commandPath)
	installFakeSoloCommands(t, nil)

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root"},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState}
	if err := app.SoloLogs(context.Background(), SoloLogsOptions{Node: "node-a", Lines: 20}); err != nil {
		t.Fatalf("SoloLogs() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["limit"] != float64(20) {
		t.Fatalf("limit = %v, want 20", payload["limit"])
	}
	commandBytes, err := os.ReadFile(commandPath)
	if err != nil {
		t.Fatalf("read journal command: %v", err)
	}
	if !strings.Contains(string(commandBytes), " -n 20") {
		t.Fatalf("journal command = %q, want -n 20", commandBytes)
	}
}

func TestSoloWorkloadLogsReadsDockerLogs(t *testing.T) {
	installFakeSoloCommands(t, nil)
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root"},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}
	if err := app.SoloWorkloadLogs(context.Background(), SoloWorkloadLogsOptions{ServiceName: "web", Nodes: []string{"node-a"}, Lines: 20}); err != nil {
		t.Fatalf("SoloWorkloadLogs() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["service"] != "web" || payload["limit"] != float64(20) {
		t.Fatalf("payload = %#v, want service web limit 20", payload)
	}
	nodes := jsonArrayFromMap(t, payload, "nodes")
	node := jsonMapFromAny(t, nodes[0])
	lines := jsonArrayFromMap(t, node, "lines")
	if len(lines) < 3 || lines[1] != "app line one" {
		t.Fatalf("lines = %#v, want workload logs", lines)
	}
}

func TestSoloWorkloadLogsRequiresWorkspaceConfig(t *testing.T) {
	installFakeSoloCommands(t, nil)
	workspaceRoot := t.TempDir()
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root"},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Printer:     output.New(io.Discard, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}
	err := app.SoloWorkloadLogs(context.Background(), SoloWorkloadLogsOptions{ServiceName: "web", Nodes: []string{"node-a"}, Lines: 20})
	if err == nil || !strings.Contains(err.Error(), "no workspace selected") {
		t.Fatalf("SoloWorkloadLogs() error = %v, want no workspace selected", err)
	}
}

func TestRemoteDockerLogsCommandPreservesPerContainerFailure(t *testing.T) {
	command := remoteDockerLogsCommand("production", "web", 20)
	for _, snippet := range []string{`ps_status=$?`, `Failed to list workload containers`, `head -n 20`, `rc=0`, `inspect_status=$?`, `logs_status=$?`, `exit "$rc"`} {
		if !strings.Contains(command, snippet) {
			t.Fatalf("command = %q, want %q", command, snippet)
		}
	}
}

func TestSoloNodeDiagnoseCollectsRuntimeSnapshot(t *testing.T) {
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: `{"revision":"abc","phase":"settled"}` + "\n"}})

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState}
	if err := app.SoloNodeDiagnose(context.Background(), SoloNodeDiagnoseOptions{Node: "node-a"}); err != nil {
		t.Fatalf("SoloNodeDiagnose() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["node"] != "node-a" || payload["host"] != "203.0.113.10" {
		t.Fatalf("payload = %#v, want node identity", payload)
	}
	checks := jsonArrayFromMap(t, payload, "checks")
	if len(checks) != 3 {
		t.Fatalf("checks = %#v, want ssh/docker/agent checks", checks)
	}
	dockerPayload := jsonMapFromAny(t, payload["docker"])
	containers := jsonMapFromAny(t, dockerPayload["containers"])
	items := jsonArrayFromMap(t, containers, "items")
	if len(items) != 1 || jsonMapFromAny(t, items[0])["Names"] != "svc-production-web" {
		t.Fatalf("containers = %#v, want web container", containers)
	}
	status := jsonMapFromAny(t, payload["status"])
	if status["phase"] != "settled" {
		t.Fatalf("status = %#v, want settled phase", status)
	}
}

func TestSoloNodeDiagnoseReturnsFailureWhenSSHCheckFails(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "ssh"), `#!/usr/bin/env bash
echo "permission denied" >&2
exit 255
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Port: 22},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState}
	err := app.SoloNodeDiagnose(context.Background(), SoloNodeDiagnoseOptions{Node: "node-a"})
	if err == nil {
		t.Fatal("SoloNodeDiagnose() error = nil, want failure")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("error = %#v, want ExitError code 1", err)
	}
	var renderedErr RenderedError
	if !errors.As(exitErr.Err, &renderedErr) {
		t.Fatalf("exit error = %#v, want RenderedError", exitErr.Err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ok"] != false {
		t.Fatalf("payload ok = %#v, want false", payload["ok"])
	}
	steps := jsonArrayFromMap(t, payload, "next_steps")
	if len(steps) != 1 || steps[0] != "ssh -p 22 'root@203.0.113.10' true" {
		t.Fatalf("next_steps = %#v, want SSH recovery command", steps)
	}
}

func TestSoloAgentUninstallRequiresConfirmation(t *testing.T) {
	app := &App{SoloState: solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))}
	err := app.SoloAgentUninstall(context.Background(), SoloAgentUninstallOptions{Node: "node-a"})
	if err == nil {
		t.Fatal("SoloAgentUninstall() error = nil, want confirmation error")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %q, want --yes hint", err.Error())
	}
}

func TestSoloAgentUninstallRejectsUnsafeStateDir(t *testing.T) {
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", AgentStateDir: "/"},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	app := &App{SoloState: soloState}
	err := app.SoloAgentUninstall(context.Background(), SoloAgentUninstallOptions{Node: "node-a", Yes: true})
	if err == nil {
		t.Fatal("SoloAgentUninstall() error = nil, want unsafe state dir error")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if !strings.Contains(err.Error(), "unsafe devopsellence agent state dir") {
		t.Fatalf("error = %q, want unsafe state dir", err.Error())
	}
}

func TestSoloAgentUninstallRunsCleanupScript(t *testing.T) {
	binDir := t.TempDir()
	scriptPath := filepath.Join(t.TempDir(), "uninstall.sh")
	writeExecutable(t, filepath.Join(binDir, "ssh"), `#!/usr/bin/env bash
set -euo pipefail
command="${!#}"
if [[ "$command" == "bash -s" ]]; then
  cat >"$DEVOPSELLENCE_FAKE_UNINSTALL_SCRIPT"
  exit 0
fi
echo "unexpected ssh command: $command" >&2
exit 1
`)
	t.Setenv("DEVOPSELLENCE_FAKE_UNINSTALL_SCRIPT", scriptPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", AgentStateDir: "/var/lib/devopsellence-test"},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState}
	if err := app.SoloAgentUninstall(context.Background(), SoloAgentUninstallOptions{Node: "node-a", Yes: true}); err != nil {
		t.Fatalf("SoloAgentUninstall() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["action"] != "uninstalled" || payload["workloads_removed"] != true {
		t.Fatalf("payload = %#v, want uninstall with workload cleanup", payload)
	}
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read uninstall script: %v", err)
	}
	script := string(scriptBytes)
	for _, want := range []string{"systemctl stop devopsellence-agent", "docker ps -aq --filter label=devopsellence.managed=true", "docker ps -aq --filter label=devopsellence.system", "docker rm -f devopsellence-envoy", "rm -rf \"$STATE_DIR\""} {
		if !strings.Contains(script, want) {
			t.Fatalf("uninstall script missing %q:\n%s", want, script)
		}
	}
}

func TestSoloStatusJSONReturnsFailureWithRenderedPayload(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, []fakeSSHResponse{
		{stderr: "permission denied\n", exitCode: 1},
	})

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				NodeNames:     []string{"node-a"},
			},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	err := app.SoloStatus(context.Background(), SoloStatusOptions{})
	if err == nil {
		t.Fatal("expected status failure")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("error = %#v, want ExitError code 1", err)
	}
	var renderedErr RenderedError
	if !errors.As(exitErr.Err, &renderedErr) {
		t.Fatalf("exit error = %#v, want RenderedError", exitErr.Err)
	}

	var payload struct {
		Nodes []struct {
			Node  string `json:"node"`
			Error string `json:"error"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal stdout JSON: %v\n%s", err, stdout.String())
	}
	if len(payload.Nodes) != 1 {
		t.Fatalf("nodes = %#v, want one entry", payload.Nodes)
	}
	if payload.Nodes[0].Node != "node-a" || !strings.Contains(payload.Nodes[0].Error, "permission denied") {
		t.Fatalf("payload = %#v, want node-a permission error", payload)
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

func TestRepublishNodesReportsRemoteDockerCheck(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Printer:     output.New(io.Discard, io.Discard),
		Docker:      &fakeDocker{imageMetadataErr: errors.New("Error response from daemon: No such image: demo:missing")},
		ConfigStore: config.NewStore(),
	}
	current := solo.State{
		Nodes: map[string]config.Node{
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
		Snapshots: map[string]desiredstate.DeploySnapshot{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				Image:         "demo:missing",
				Metadata:      desiredstate.SnapshotMetadata{ConfigPath: filepath.Join(workspaceRoot, "devopsellence.yml")},
			},
		},
	}

	_, err := app.republishNodes(context.Background(), current, []string{"web-a"})
	if err == nil {
		t.Fatal("expected republish error")
	}
	if !strings.Contains(err.Error(), "[web-a] remote docker check:") {
		t.Fatalf("error = %v", err)
	}
}

func TestSoloNodeDetachSucceedsWhenAgentAlreadyUninstalled(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"prod-1": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]desiredstate.DeploySnapshot{},
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "prod-1"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "ssh"), `#!/usr/bin/env bash
set -euo pipefail
command="${!#}"
if [[ "$command" == *"desired-state set-override"* ]]; then
  echo 'devopsellence agent binary not found' >&2
  exit 127
fi
echo "unexpected ssh command: $command" >&2
exit 1
`)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	if err := app.SoloNodeDetach(context.Background(), SoloNodeDetachOptions{Node: "prod-1"}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	warnings := jsonArrayFromMap(t, payload, "warnings")
	if len(warnings) != 1 || !strings.Contains(warnings[0].(string), "agent is already uninstalled") {
		t.Fatalf("warnings = %#v, want already-uninstalled warning", warnings)
	}
	loaded, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.NodeHasAttachments("prod-1") {
		t.Fatalf("prod-1 still attached after detach: %#v", loaded.Attachments)
	}
}

func TestNodeRemoveForManualNodeForgetsLocalState(t *testing.T) {
	t.Parallel()

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"manual-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:   output.New(&stdout, io.Discard),
		SoloState: soloState,
	}

	if err := app.SoloNodeRemove(context.Background(), SoloNodeRemoveOptions{Name: "manual-a", Yes: true}); err != nil {
		t.Fatal(err)
	}

	loaded, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Nodes["manual-a"]; ok {
		t.Fatalf("manual node still present: %#v", loaded.Nodes)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["node"] != "manual-a" || payload["action"] != "forgotten" {
		t.Fatalf("payload = %#v, want forgotten manual node", payload)
	}
}

func TestNodeRemoveRejectsIncompleteProviderMetadata(t *testing.T) {
	t.Parallel()

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"managed-a": {Host: "203.0.113.10", User: "root", Provider: "hetzner"},
		},
		Attachments: map[string]solo.AttachmentRecord{},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Printer:   output.New(io.Discard, io.Discard),
		SoloState: soloState,
	}

	err := app.SoloNodeRemove(context.Background(), SoloNodeRemoveOptions{Name: "managed-a", Yes: true})
	if err == nil {
		t.Fatal("expected incomplete provider metadata error")
	}
	if !strings.Contains(err.Error(), "incomplete provider metadata") {
		t.Fatalf("error = %v, want incomplete provider metadata", err)
	}

	loaded, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Nodes["managed-a"]; !ok {
		t.Fatalf("managed node removed despite incomplete metadata: %#v", loaded.Nodes)
	}
}

func TestNodeRemoveProviderPayloadIncludesProviderMetadata(t *testing.T) {
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"managed-a": {
				Host:             "203.0.113.10",
				User:             "root",
				Provider:         "hetzner",
				ProviderServerID: "srv-1",
				ProviderRegion:   "ash",
				ProviderSize:     "cpx11",
				ProviderImage:    providers.DefaultHetznerImage,
			},
		},
		Attachments: map[string]solo.AttachmentRecord{},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}
	providerState := state.New(filepath.Join(t.TempDir(), "providers.json"))
	if err := saveProviderToken(providerState, providerHetzner, "test-token"); err != nil {
		t.Fatal(err)
	}
	fakeProvider := &fakeSoloProvider{}
	var stdout bytes.Buffer
	app := &App{
		Printer:       output.New(&stdout, io.Discard),
		SoloState:     soloState,
		ProviderState: providerState,
		soloProviderFn: func(string) (providers.Provider, error) {
			return fakeProvider, nil
		},
	}

	if err := app.SoloNodeRemove(context.Background(), SoloNodeRemoveOptions{Name: "managed-a", Yes: true}); err != nil {
		t.Fatal(err)
	}
	if fakeProvider.deletedID != "srv-1" {
		t.Fatalf("deletedID = %q, want srv-1", fakeProvider.deletedID)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["provider"] != "hetzner" || payload["provider_server_id"] != "srv-1" || payload["provider_region"] != "ash" || payload["provider_size"] != "cpx11" || payload["provider_image"] != providers.DefaultHetznerImage {
		t.Fatalf("payload = %#v, want provider metadata", payload)
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

func TestNodeAttachPersistsDesiredStateOnRepublishError(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots: map[string]desiredstate.DeploySnapshot{
			workspaceRoot + "\nproduction": {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   "production",
				Revision:      "abc1234",
				Image:         "demo:missing",
				Metadata:      desiredstate.SnapshotMetadata{ConfigPath: filepath.Join(workspaceRoot, "devopsellence.yml")},
			},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Printer:     output.New(io.Discard, io.Discard),
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
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "*", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "off"},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, workspaceRoot)

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]desiredstate.DeploySnapshot{},
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
		Printer:            output.New(&stdout, io.Discard),
		SoloState:          soloState,
		ConfigStore:        config.NewStore(),
		Git:                git.Client{},
		Cwd:                workspaceRoot,
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      time.Second,
	}

	if err := app.SoloDeploy(context.Background(), SoloDeployOptions{}); err != nil {
		t.Fatal(err)
	}

	if got := readFakeSSHStatusCount(t, statusCountPath); got != 3 {
		t.Fatalf("status poll count = %d, want 3", got)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["environment"] != "production" || payload["workload_revision"] == "" || payload["phase"] != "settled" {
		t.Fatalf("payload = %#v, want settled deploy JSON", payload)
	}
	revisions := jsonMapFromAny(t, payload["desired_state_revisions"])
	if revisions["node-a"] == "" {
		t.Fatalf("desired_state_revisions = %#v, want node revision", revisions)
	}
	urls := jsonArrayFromMap(t, payload, "public_urls")
	if len(urls) != 1 || urls[0] != "http://203.0.113.10/" {
		t.Fatalf("public_urls = %#v, want node URL", urls)
	}
	nextSteps := jsonArrayFromMap(t, payload, "next_steps")
	if len(nextSteps) != 4 || nextSteps[0] != "devopsellence status" || nextSteps[1] != "curl http://203.0.113.10/" || nextSteps[2] != "devopsellence logs --node 'node-a' --lines 100" || nextSteps[3] != "devopsellence node logs 'node-a' --lines 100" {
		t.Fatalf("next_steps = %#v, want status, curl, and logs commands", nextSteps)
	}
}

func TestSoloDeployRolloutFailureIncludesHealthcheckContext(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, workspaceRoot)

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]desiredstate.DeploySnapshot{},
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: `{"revision":"__REVISION__","phase":"error","error":"healthcheck failed"}` + "\n"}})

	var stdout bytes.Buffer
	app := &App{
		Printer:            output.New(&stdout, io.Discard),
		SoloState:          soloState,
		ConfigStore:        config.NewStore(),
		Git:                git.Client{},
		Cwd:                workspaceRoot,
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      time.Second,
	}

	err := app.SoloDeploy(context.Background(), SoloDeployOptions{})
	if err == nil {
		t.Fatal("expected deploy failure")
	}
	var rolloutErr *soloRolloutError
	if !errors.As(err, &rolloutErr) {
		t.Fatalf("error = %T %v, want soloRolloutError", err, err)
	}
	fields := rolloutErr.ErrorFields()
	healthchecks := fields["healthchecks"].([]map[string]any)
	if len(healthchecks) != 1 || healthchecks[0]["service_name"] != config.DefaultWebServiceName || healthchecks[0]["path"] != config.DefaultHealthcheckPath {
		t.Fatalf("healthchecks = %#v, want web healthcheck context", healthchecks)
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
		Printer:            output.New(io.Discard, io.Discard),
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      time.Second,
	}

	err := app.waitForSoloRollout(context.Background(), map[string]config.Node{
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
		Printer:            output.New(io.Discard, io.Discard),
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      100 * time.Millisecond,
	}

	err := app.waitForSoloRollout(context.Background(), map[string]config.Node{
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
	var rolloutErr *soloRolloutError
	if !errors.As(err, &rolloutErr) {
		t.Fatalf("error = %T %v, want soloRolloutError", err, err)
	}
	fields := rolloutErr.ErrorFields()
	steps := fields["next_steps"].([]string)
	if len(steps) != 3 || steps[1] != "devopsellence logs --node 'node-a' --lines 100" || steps[2] != "devopsellence node logs 'node-a' --lines 100" {
		t.Fatalf("next_steps = %#v, want status, workload logs, and node logs commands", steps)
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
		Printer:            output.New(io.Discard, io.Discard),
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      20 * time.Millisecond,
	}

	err := app.waitForSoloRollout(context.Background(), map[string]config.Node{
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
	var timeoutErr *soloRolloutTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("error = %T %v, want soloRolloutTimeoutError", err, err)
	}
	fields := timeoutErr.ErrorFields()
	steps := fields["next_steps"].([]string)
	if len(steps) != 3 || steps[1] != "devopsellence logs --node 'node-a' --lines 100" || steps[2] != "devopsellence node logs 'node-a' --lines 100" {
		t.Fatalf("next_steps = %#v, want status, workload logs, and node logs commands", steps)
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
				Printer:            output.New(io.Discard, io.Discard),
				DeployPollInterval: 5 * time.Millisecond,
				DeployTimeout:      100 * time.Millisecond,
			}

			err := app.waitForSoloRollout(context.Background(), map[string]config.Node{
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

func TestParseNodeStatusPayload(t *testing.T) {
	payload := []byte(`{"phase":"settled","revision":"abc123","environments":[{"name":"production","services":[{"name":"web","state":"running"}]}]}`)

	status, raw, err := parseNodeStatusPayload(payload)
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

func TestDesiredStateOverridePathDefaultsAgentStateDir(t *testing.T) {
	t.Parallel()

	got := desiredStateOverridePath(config.Node{})
	want := "/var/lib/devopsellence/desired-state-override.json"
	if got != want {
		t.Fatalf("desiredStateOverridePath() = %q, want %q", got, want)
	}
}

func TestDesiredStateOverridePathUsesConfiguredAgentStateDir(t *testing.T) {
	t.Parallel()

	got := desiredStateOverridePath(config.Node{AgentStateDir: "/tmp/devopsellence state"})
	want := "/tmp/devopsellence state/desired-state-override.json"
	if got != want {
		t.Fatalf("desiredStateOverridePath() = %q, want %q", got, want)
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

func TestResolveSoloSecretValuesUsesStoreResolver(t *testing.T) {
	root := t.TempDir()
	var current solo.State
	if _, err := current.SetSecret(root, "production", "web", "DATABASE_URL", solo.SecretMaterial{Value: "postgres://plain"}); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SetSecret(root, "production", "worker", "DATABASE_URL", solo.SecretMaterial{
		Store:     solo.SecretStoreOnePassword,
		Reference: "op://app/db/password",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SetSecret(root, "production", "jobs", "DATABASE_URL", solo.SecretMaterial{
		Store:     solo.SecretStoreOnePassword,
		Reference: "op://app/db/password",
	}); err != nil {
		t.Fatal(err)
	}
	opReads := 0
	app := &App{
		soloSecretResolveFn: func(_ context.Context, record solo.SecretRecord) (string, error) {
			if record.Store == solo.SecretStoreOnePassword {
				opReads++
				return "postgres://op", nil
			}
			return record.Value, nil
		},
	}
	cfg := config.DefaultProjectConfig("default", "demo", "production")
	web := cfg.Services["web"]
	web.SecretRefs = []config.SecretRef{{Name: "DATABASE_URL", Secret: "devopsellence://plaintext/DATABASE_URL"}}
	cfg.Services["web"] = web
	cfg.Services["worker"] = config.ServiceConfig{
		SecretRefs: []config.SecretRef{{Name: "DATABASE_URL", Secret: "devopsellence://1password/DATABASE_URL"}},
	}
	cfg.Services["jobs"] = config.ServiceConfig{
		SecretRefs: []config.SecretRef{{Name: "DATABASE_URL", Secret: "devopsellence://1password/DATABASE_URL"}},
	}

	values, err := app.resolveSoloSecretValues(context.Background(), current, root, "production", &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := values.Value("web", "DATABASE_URL"); got != "postgres://plain" {
		t.Fatalf("web DATABASE_URL = %q", got)
	}
	if got := values.Value("worker", "DATABASE_URL"); got != "postgres://op" {
		t.Fatalf("worker DATABASE_URL = %q", got)
	}
	if got := values.Value("jobs", "DATABASE_URL"); got != "postgres://op" {
		t.Fatalf("jobs DATABASE_URL = %q", got)
	}
	if opReads != 1 {
		t.Fatalf("1Password reads = %d, want 1", opReads)
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

func TestEnsureNodeCreateSSHPublicKeyGeneratesWhenNoDefaultKey(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("HOME", homeDir)

	workspaceRoot := t.TempDir()
	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard)}
	opts := SoloNodeCreateOptions{}

	if err := app.ensureSoloNodeCreateSSHPublicKey(&opts, workspaceRoot); err != nil {
		t.Fatal(err)
	}
	if opts.SSHPublicKey == "" {
		t.Fatal("SSHPublicKey empty, want generated public key path")
	}
	if !strings.HasPrefix(opts.SSHPublicKey, filepath.Join(stateDir, "devopsellence", "solo", "keys")) {
		t.Fatalf("SSHPublicKey = %q, want generated state key", opts.SSHPublicKey)
	}
	if _, err := os.Stat(opts.SSHPublicKey); err != nil {
		t.Fatalf("expected generated public key: %v", err)
	}
	if _, err := os.Stat(strings.TrimSuffix(opts.SSHPublicKey, ".pub")); err != nil {
		t.Fatalf("expected generated private key: %v", err)
	}
}

func TestEnsureNodeCreateSSHPublicKeyGeneratesWhenDefaultKeyIsEmpty(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("HOME", homeDir)

	if err := os.MkdirAll(filepath.Join(homeDir, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	defaultPublicKey := filepath.Join(homeDir, ".ssh", "id_ed25519.pub")
	if err := os.WriteFile(defaultPublicKey, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	workspaceRoot := t.TempDir()
	app := &App{Printer: output.New(io.Discard, io.Discard)}
	opts := SoloNodeCreateOptions{}

	if err := app.ensureSoloNodeCreateSSHPublicKey(&opts, workspaceRoot); err != nil {
		t.Fatal(err)
	}
	if opts.SSHPublicKey == "" {
		t.Fatal("SSHPublicKey empty, want generated public key path")
	}
	if opts.SSHPublicKey == defaultPublicKey {
		t.Fatalf("SSHPublicKey = default empty key %q, want generated workspace key", opts.SSHPublicKey)
	}
	if !strings.HasPrefix(opts.SSHPublicKey, filepath.Join(stateDir, "devopsellence", "solo", "keys")) {
		t.Fatalf("SSHPublicKey = %q, want generated state key", opts.SSHPublicKey)
	}
}

func TestEnsureNodeCreateSSHPublicKeyKeepsExplicitKey(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	customPublicKey := filepath.Join(t.TempDir(), "custom.pub")
	opts := SoloNodeCreateOptions{SSHPublicKey: customPublicKey}
	app := &App{Printer: output.New(io.Discard, io.Discard)}

	if err := app.ensureSoloNodeCreateSSHPublicKey(&opts, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if opts.SSHPublicKey != customPublicKey {
		t.Fatalf("SSHPublicKey = %q, want %q", opts.SSHPublicKey, customPublicKey)
	}
}

func TestSoloInitCreatesWorkspaceConfig(t *testing.T) {
	workspaceRoot := t.TempDir()
	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	if err := app.SoloInit(context.Background(), SoloInitOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadFromRoot(workspaceRoot); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	configPayload := jsonMapFromAny(t, payload["config"])
	if configPayload["created"] != true || configPayload["valid"] != true {
		t.Fatalf("config payload = %#v", configPayload)
	}
	stepValues := jsonArrayFromMap(t, payload, "next_steps")
	steps := make([]string, 0, len(stepValues))
	for _, value := range stepValues {
		steps = append(steps, stringValueAny(value))
	}
	nextSteps := strings.Join(steps, "\n")
	if !strings.Contains(nextSteps, "devopsellence node create prod-1 --provider hetzner --install --attach") {
		t.Fatalf("next_steps = %q, want provider-created node path", nextSteps)
	}
	runtimeContract := jsonMapFromAny(t, payload["runtime_contract"])
	if runtimeContract["service"] != "web" || runtimeContract["port"] != float64(3000) || runtimeContract["port_source"] != "default" {
		t.Fatalf("runtime_contract = %#v, want default web port contract", runtimeContract)
	}
	if runtimeContract["web_service"] != true {
		t.Fatalf("runtime_contract.web_service = %#v, want true", runtimeContract["web_service"])
	}
	if runtimeContract["healthcheck_path"] != "/" || runtimeContract["healthcheck_port"] != float64(3000) {
		t.Fatalf("runtime_contract healthcheck = %#v, want / on port 3000", runtimeContract)
	}
	requirement := stringValueAny(runtimeContract["requirement"])
	if !strings.Contains(requirement, "EXPOSE") || !strings.Contains(requirement, "devopsellence.yml") {
		t.Fatalf("runtime_contract.requirement = %q, want Dockerfile/config guidance", requirement)
	}
}

func TestSoloInitReportsConfigPortContract(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM nginx:1.27-alpine\nEXPOSE 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultProjectConfigForType("solo", "demo", "production", config.AppTypeGeneric)
	web := cfg.Services["web"]
	web.Ports = []config.ServicePort{{Name: "http", Port: 8080}}
	web.Healthcheck = &config.HTTPHealthcheck{Path: "/health", Port: 8080}
	cfg.Services["web"] = web
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	if err := app.SoloInit(context.Background(), SoloInitOptions{}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	runtimeContract := jsonMapFromAny(t, payload["runtime_contract"])
	if runtimeContract["service"] != "web" || runtimeContract["port"] != float64(8080) || runtimeContract["port_source"] != "config" {
		t.Fatalf("runtime_contract = %#v, want configured web port contract", runtimeContract)
	}
	if runtimeContract["healthcheck_path"] != "/health" || runtimeContract["healthcheck_port"] != float64(8080) {
		t.Fatalf("runtime_contract healthcheck = %#v, want /health on port 8080", runtimeContract)
	}
}

func TestSoloInitReportsNoWebServicePortContract(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfigForType("solo", "demo", "production", config.AppTypeGeneric)
	cfg.Services = map[string]config.ServiceConfig{
		"worker": {
			Command: []string{"bin/worker"},
		},
	}
	cfg.Ingress = nil
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	if err := app.SoloInit(context.Background(), SoloInitOptions{}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	runtimeContract := jsonMapFromAny(t, payload["runtime_contract"])
	if runtimeContract["web_service"] != false || runtimeContract["port_source"] != "none" {
		t.Fatalf("runtime_contract = %#v, want explicit no-web-service contract", runtimeContract)
	}
	if runtimeContract["reason"] != "no primary web service detected" {
		t.Fatalf("runtime_contract.reason = %#v, want no primary web service detected", runtimeContract["reason"])
	}
	if _, ok := runtimeContract["port"]; ok {
		t.Fatalf("runtime_contract port = %#v, want omitted", runtimeContract["port"])
	}
}

func TestSoloInitReportsDockerfileInferredPortContract(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM nginx:1.27-alpine\nEXPOSE 80\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	if err := app.SoloInit(context.Background(), SoloInitOptions{}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	runtimeContract := jsonMapFromAny(t, payload["runtime_contract"])
	if runtimeContract["service"] != "web" || runtimeContract["port"] != float64(80) || runtimeContract["port_source"] != "dockerfile" {
		t.Fatalf("runtime_contract = %#v, want inferred Dockerfile web port contract", runtimeContract)
	}
	if runtimeContract["healthcheck_port"] != float64(80) {
		t.Fatalf("runtime_contract.healthcheck_port = %#v, want 80", runtimeContract["healthcheck_port"])
	}
}

func TestSoloInitReportsReadyWhenNodeAttached(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{Nodes: map[string]config.Node{"prod-1": {Host: "203.0.113.10", User: "root"}}}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "prod-1"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloInit(context.Background(), SoloInitOptions{}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ready"] != true {
		t.Fatalf("payload = %#v", payload)
	}
	if missing := jsonArrayFromMap(t, payload, "missing"); len(missing) != 0 {
		t.Fatalf("missing = %#v, want empty", missing)
	}
}

func TestWaitForSoloSSHWithProbeReturnsLastError(t *testing.T) {
	node := config.Node{User: "root", Host: "203.0.113.10"}
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
	node := config.Node{User: "root", Host: "203.0.113.10"}

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
		Printer:     output.New(io.Discard, io.Discard),
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
	if len(written.Ingress.Rules) != 1 {
		t.Fatalf("ingress.rules = %#v, want one rule", written.Ingress.Rules)
	}
	if written.Ingress.Rules[0].Target.Service != config.DefaultWebServiceName {
		t.Fatalf("ingress.rules[0].target.service = %q, want %q", written.Ingress.Rules[0].Target.Service, config.DefaultWebServiceName)
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

if [[ "$command" == "true" ]]; then
  exit 0
fi

if [[ "$command" == *"desired-state set-override"* ]]; then
  cat >"$DEVOPSELLENCE_FAKE_SSH_REVISION_FILE"
  exit 0
fi

if [[ "$command" == *"docker image inspect"* ]]; then
  printf 'present\n'
  exit 0
fi

if [[ "$command" == *" logs --tail "* ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\n==> svc-production-web <==\napp line one\napp line two\n__DEVOPSELLENCE_STDERR__\n'
  exit 0
fi

if [[ "$command" == *"__DEVOPSELLENCE_EXIT_CODE__"* && "$command" == *"docker ps -a"* ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\n{"Names":"svc-production-web","Image":"demo:abc","Status":"Up 1 minute","Ports":"3000/tcp"}\n__DEVOPSELLENCE_STDERR__\n'
  exit 0
fi

if [[ "$command" == *"__DEVOPSELLENCE_EXIT_CODE__"* && "$command" == *"docker images"* ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\n{"Repository":"demo","Tag":"abc","ID":"sha256:abc","Size":"1MB"}\n__DEVOPSELLENCE_STDERR__\n'
  exit 0
fi

if [[ "$command" == *"__DEVOPSELLENCE_EXIT_CODE__"* && "$command" == *"docker network ls"* ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\n{"Name":"devopsellence","Driver":"bridge"}\n__DEVOPSELLENCE_STDERR__\n'
  exit 0
fi

if [[ "$command" == *"docker ps -a"* ]]; then
  printf '{"Names":"svc-production-web","Image":"demo:abc","Status":"Up 1 minute","Ports":"3000/tcp"}\n'
  exit 0
fi

if [[ "$command" == *"docker images"* ]]; then
  printf '{"Repository":"demo","Tag":"abc","ID":"sha256:abc","Size":"1MB"}\n'
  exit 0
fi

if [[ "$command" == *"docker network ls"* ]]; then
  printf '{"Name":"devopsellence","Driver":"bridge"}\n'
  exit 0
fi

if [[ "$command" == *"docker info"* ]]; then
  exit 0
fi

if [[ "$command" == *"__DEVOPSELLENCE_EXIT_CODE__"* && "$command" == *"systemctl is-active devopsellence-agent"* ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\nactive\n__DEVOPSELLENCE_STDERR__\n'
  exit 0
fi

if [[ "$command" == *"__DEVOPSELLENCE_EXIT_CODE__"* && "$command" == *"systemctl status"* ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\ndevopsellence-agent active\n__DEVOPSELLENCE_STDERR__\n'
  exit 0
fi

if [[ "$command" == *"__DEVOPSELLENCE_EXIT_CODE__"* && ( "$command" == *"ss -ltn"* || "$command" == *"netstat -ltn"* ) ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\nLISTEN 0 4096 0.0.0.0:80 0.0.0.0:*\n__DEVOPSELLENCE_STDERR__\n'
  exit 0
fi

if [[ "$command" == *"systemctl is-active devopsellence-agent"* ]]; then
  printf 'active\n'
  exit 0
fi

if [[ "$command" == *"systemctl is-active --quiet devopsellence-agent"* ]]; then
  exit 0
fi

if [[ "$command" == *"systemctl status"* ]]; then
  printf 'devopsellence-agent active\n'
  exit 0
fi

if [[ "$command" == *"ss -ltn"* ]] || [[ "$command" == *"netstat -ltn"* ]]; then
  printf 'LISTEN 0 4096 0.0.0.0:80 0.0.0.0:*\n'
  exit 0
fi

if [[ "$command" == *"journalctl"* ]]; then
  if [[ -n "${DEVOPSELLENCE_FAKE_SSH_JOURNAL_COMMAND:-}" ]]; then
    printf '%s' "$command" >"$DEVOPSELLENCE_FAKE_SSH_JOURNAL_COMMAND"
  fi
  printf 'line one\nline two\n'
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

type fakeSoloProvider struct {
	createServer providers.Server
	readServer   providers.Server
	createInput  providers.CreateServerInput
	deletedID    string
}

func (f *fakeSoloProvider) Validate(context.Context) error {
	return nil
}

func (f *fakeSoloProvider) CreateServer(_ context.Context, input providers.CreateServerInput) (providers.Server, error) {
	f.createInput = input
	return f.createServer, nil
}

func (f *fakeSoloProvider) DeleteServer(_ context.Context, id string) error {
	f.deletedID = id
	return nil
}

func (f *fakeSoloProvider) GetServer(context.Context, string) (providers.Server, error) {
	return f.readServer, nil
}

func (f *fakeSoloProvider) Ready(server providers.Server) bool {
	return server.PublicIP != "" && server.Status == "running"
}

func TestIngressSetPreservesExistingServiceWhenFlagOmitted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := config.DefaultProjectConfigForType("solo", "demo", config.DefaultEnvironment, config.AppTypeGeneric)
	cfg.Services["frontend"] = cfg.Services[config.DefaultWebServiceName]
	delete(cfg.Services, config.DefaultWebServiceName)
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"old.devopsellence.io"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "old.devopsellence.io", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: "frontend", Port: "http"},
		}},
	}

	if _, err := config.Write(dir, cfg); err != nil {
		t.Fatal(err)
	}

	app := &App{
		Cwd:         dir,
		ConfigStore: config.NewStore(),
		Printer:     output.New(io.Discard, io.Discard),
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
	if len(written.Ingress.Rules) != 1 {
		t.Fatalf("ingress.rules = %#v, want one rule", written.Ingress.Rules)
	}
	if written.Ingress.Rules[0].Target.Service != "frontend" {
		t.Fatalf("ingress.rules[0].target.service = %q, want frontend", written.Ingress.Rules[0].Target.Service)
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

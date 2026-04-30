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
	corerelease "github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/release"
)

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

const testSoloExecExitMarker = soloExecExitMarkerPrefix + "0123456789abcdef0123456789abcdef__"

func TestSoloImageTagSlugifiesProjectName(t *testing.T) {
	got := soloImageTag("ShopApp", "abc1234")
	if got != "shop-app:abc1234" {
		t.Fatalf("image tag = %q, want shop-app:abc1234", got)
	}
}

func TestSoloIDsIncludeUniqueSuffix(t *testing.T) {
	now := time.Now().UTC()
	releaseA := soloReleaseID("abc1234", now)
	releaseB := soloReleaseID("abc1234", now)
	if releaseA == releaseB {
		t.Fatalf("release IDs matched: %q", releaseA)
	}
	if releaseA >= releaseB {
		t.Fatalf("release IDs are not sortable: %q >= %q", releaseA, releaseB)
	}
	if got := strings.TrimPrefix(releaseA, "rel_abc1234_"); len(got) != 26 || got == releaseA {
		t.Fatalf("release ID = %q, want ULID suffix", releaseA)
	}
	deploymentA := soloDeploymentID(corerelease.DeploymentKindDeploy, "abc1234", now)
	deploymentB := soloDeploymentID(corerelease.DeploymentKindDeploy, "abc1234", now)
	if deploymentA == deploymentB {
		t.Fatalf("deployment IDs matched: %q", deploymentA)
	}
	if deploymentA >= deploymentB {
		t.Fatalf("deployment IDs are not sortable: %q >= %q", deploymentA, deploymentB)
	}
}

func TestPublicationReleasesFromSnapshotsDefaultsEnvironment(t *testing.T) {
	t.Parallel()

	releases := publicationReleasesFromSnapshots([]desiredstate.DeploySnapshot{{
		WorkspaceKey: " /workspace/demo ",
		Environment:  " ",
		Revision:     " abc1234 ",
		Image:        " demo:abc1234 ",
	}})
	if len(releases) != 1 {
		t.Fatalf("releases = %#v, want one release", releases)
	}
	release := releases[0]
	if release.ID != "/workspace/demo\nproduction" {
		t.Fatalf("release ID = %q, want normalized production key", release.ID)
	}
	if release.Snapshot.WorkspaceKey != "/workspace/demo" || release.Snapshot.Environment != config.DefaultEnvironment {
		t.Fatalf("snapshot = %#v, want normalized workspace/environment", release.Snapshot)
	}
}

func TestResolveStoredDeploySnapshotPreservesHistoricalReleaseShape(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfgAtRelease := config.DefaultProjectConfig("acme", "demo", config.DefaultEnvironment)
	service := cfgAtRelease.Services["web"]
	service.Env = map[string]string{"APP_MODE": "old"}
	service.SecretRefs = []config.SecretRef{{Name: "API_KEY", Secret: "devopsellence://plaintext/API_KEY"}}
	service.Healthcheck = &config.HTTPHealthcheck{Path: "/old-up", Port: 3000}
	cfgAtRelease.Services["web"] = service
	cfgAtRelease.Ingress = &config.IngressConfig{
		Hosts: []string{"old.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "old.example.com"},
			Target: config.IngressTargetConfig{Service: "web", Port: "http"},
		}},
	}

	snapshot, err := solo.BuildDeploySnapshotWithScopedSecrets(
		&cfgAtRelease,
		workspaceRoot,
		filepath.Join(workspaceRoot, config.FilePath),
		"demo:old",
		"oldrev",
		solo.ScopedSecrets{"web": {"API_KEY": "secret-value"}},
	)
	if err != nil {
		t.Fatalf("build release snapshot: %v", err)
	}
	storedSnapshot := solo.RedactDeploySnapshotSecrets(snapshot, &cfgAtRelease)

	cfgNow := config.DefaultProjectConfig("acme", "demo", config.DefaultEnvironment)
	currentService := cfgNow.Services["web"]
	currentService.Env = map[string]string{"APP_MODE": "new"}
	currentService.Healthcheck = &config.HTTPHealthcheck{Path: "/new-up", Port: 3000}
	cfgNow.Services["web"] = currentService
	cfgNow.Ingress = &config.IngressConfig{
		Hosts: []string{"new.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "new.example.com"},
			Target: config.IngressTargetConfig{Service: "web", Port: "http"},
		}},
	}
	if _, err := config.NewStore().Write(workspaceRoot, cfgNow); err != nil {
		t.Fatalf("write current config: %v", err)
	}

	current := solo.State{}
	record, err := current.SetSecret(workspaceRoot, config.DefaultEnvironment, "web", "API_KEY", solo.SecretMaterial{Store: solo.SecretStorePlaintext, Value: "secret-value"})
	if err != nil {
		t.Fatalf("set secret: %v", err)
	}
	if record.Value == "" {
		t.Fatal("test secret record did not retain plaintext value")
	}

	app := &App{ConfigStore: config.NewStore()}
	resolved, err := app.resolveStoredDeploySnapshot(context.Background(), current, storedSnapshot)
	if err != nil {
		t.Fatalf("resolve snapshot: %v", err)
	}
	if len(resolved.Services) != 1 {
		t.Fatalf("services = %#v, want one historical service", resolved.Services)
	}
	resolvedService := resolved.Services[0]
	if got := resolvedService.Env["APP_MODE"]; got != "old" {
		t.Fatalf("APP_MODE = %q, want historical value old", got)
	}
	if got := resolvedService.Env["API_KEY"]; got != "secret-value" {
		t.Fatalf("API_KEY = %q, want resolved secret value", got)
	}
	if resolvedService.Healthcheck == nil || resolvedService.Healthcheck.Path != "/old-up" {
		t.Fatalf("healthcheck = %#v, want historical /old-up", resolvedService.Healthcheck)
	}
	if resolved.Ingress == nil || len(resolved.Ingress.Hosts) != 1 || resolved.Ingress.Hosts[0] != "old.example.com" {
		t.Fatalf("ingress = %#v, want historical old.example.com", resolved.Ingress)
	}
}

func TestResolveStoredDeploySnapshotRejectsLegacySnapshotWhenSecretsExist(t *testing.T) {
	workspaceRoot := t.TempDir()
	storedSnapshot := desiredstate.DeploySnapshot{
		WorkspaceRoot: workspaceRoot,
		WorkspaceKey:  workspaceRoot,
		Environment:   config.DefaultEnvironment,
		Revision:      "oldrev",
		Image:         "demo:old",
		Services: []desiredstate.ServiceJSON{{
			Name: "web",
			Kind: config.ServiceKindWeb,
			Env:  map[string]string{"APP_MODE": "old"},
		}},
	}
	current := solo.State{}
	if _, err := current.SetSecret(workspaceRoot, config.DefaultEnvironment, "web", "API_KEY", solo.SecretMaterial{Store: solo.SecretStorePlaintext, Value: "secret-value"}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	app := &App{}
	_, err := app.resolveStoredDeploySnapshot(context.Background(), current, storedSnapshot)
	if err == nil || !strings.Contains(err.Error(), "before secret metadata was tracked") {
		t.Fatalf("resolve legacy snapshot error = %v, want secret metadata error", err)
	}
}

func TestResolveStoredDeploySnapshotDoesNotMutateStoredSnapshotSecrets(t *testing.T) {
	workspaceRoot := t.TempDir()
	storedSnapshot := desiredstate.DeploySnapshot{
		WorkspaceRoot:  workspaceRoot,
		WorkspaceKey:   workspaceRoot,
		Environment:    config.DefaultEnvironment,
		Revision:       "oldrev",
		Image:          "demo:old",
		ReleaseService: "web",
		Services: []desiredstate.ServiceJSON{{
			Name: "web",
			Kind: config.ServiceKindWeb,
			Env:  map[string]string{"APP_MODE": "old"},
		}},
		ReleaseTask: &desiredstate.TaskJSON{
			Name: "release",
			Env:  map[string]string{"TASK_MODE": "old"},
		},
		SecretRefs: map[string][]string{"web": {"API_KEY"}},
	}
	current := solo.State{
		Snapshots: map[string]desiredstate.DeploySnapshot{
			workspaceRoot + "\n" + config.DefaultEnvironment: storedSnapshot,
		},
	}
	if _, err := current.SetSecret(workspaceRoot, config.DefaultEnvironment, "web", "API_KEY", solo.SecretMaterial{Store: solo.SecretStorePlaintext, Value: "secret-value"}); err != nil {
		t.Fatalf("set secret: %v", err)
	}

	app := &App{}
	resolved, err := app.resolveStoredDeploySnapshot(context.Background(), current, current.Snapshots[workspaceRoot+"\n"+config.DefaultEnvironment])
	if err != nil {
		t.Fatalf("resolve snapshot: %v", err)
	}
	if got := resolved.Services[0].Env["API_KEY"]; got != "secret-value" {
		t.Fatalf("resolved service API_KEY = %q, want resolved secret", got)
	}
	if got := resolved.ReleaseTask.Env["API_KEY"]; got != "secret-value" {
		t.Fatalf("resolved release task API_KEY = %q, want resolved secret", got)
	}
	storedAfter := current.Snapshots[workspaceRoot+"\n"+config.DefaultEnvironment]
	if _, ok := storedAfter.Services[0].Env["API_KEY"]; ok {
		t.Fatalf("stored service snapshot env leaked resolved secret: %#v", storedAfter.Services[0].Env)
	}
	if _, ok := storedAfter.ReleaseTask.Env["API_KEY"]; ok {
		t.Fatalf("stored release task snapshot env leaked resolved secret: %#v", storedAfter.ReleaseTask.Env)
	}
}

func TestSoloReadyPublicURLsDoNotClaimAutoTLSBeforeHTTPSReadiness(t *testing.T) {
	cfg := config.DefaultProjectConfig("acme", "demo", config.DefaultEnvironment)
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"app.example.com"},
		TLS:   config.IngressTLSConfig{Mode: "auto"},
	}
	nodes := map[string]config.Node{"web-a": {Host: "203.0.113.10", Labels: []string{config.DefaultWebRole}}}

	if got := soloReadyPublicURLs(&cfg, nodes); len(got) != 0 {
		t.Fatalf("ready public URLs = %#v, want none until HTTPS readiness is verified", got)
	}
	configured := soloStatusPublicURLs(&cfg, nodes)
	if len(configured) != 1 || configured[0] != "https://app.example.com/" {
		t.Fatalf("configured public URLs = %#v, want HTTPS configured URL", configured)
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
				Command: []string{"bin/migrate"},
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
				Command: []string{"bin/migrate"},
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
	snapshot := desiredstate.DeploySnapshot{
		WorkspaceRoot: "/workspace/demo",
		WorkspaceKey:  attachment.WorkspaceKey,
		Environment:   "production",
		Revision:      "abc1234",
		Image:         "demo:abc1234",
	}
	current.Releases = map[string]corerelease.Release{
		"rel-1": {ID: "rel-1", EnvironmentID: key, Revision: "abc1234", Snapshot: snapshot},
	}
	current.Current = map[string]string{key: "rel-1"}

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

func TestSoloNodeAttachUsesSavedCurrentEnvironment(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Environments = map[string]config.EnvironmentOverlay{"staging": {}}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	if err := soloState.Write(solo.State{Nodes: map[string]config.Node{"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}}}}); err != nil {
		t.Fatal(err)
	}
	workspaceState := state.New(filepath.Join(t.TempDir(), "workspace-state.json"))
	var stdout bytes.Buffer
	app := &App{
		Printer:        output.New(&stdout, io.Discard),
		SoloState:      soloState,
		WorkspaceState: workspaceState,
		ConfigStore:    config.NewStore(),
		Cwd:            workspaceRoot,
	}
	if err := app.SetEnvironment("staging"); err != nil {
		t.Fatal(err)
	}
	if err := app.SoloNodeAttach(context.Background(), SoloNodeAttachOptions{Node: "node-a"}); err != nil {
		t.Fatal(err)
	}
	current, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	stagingNodes, err := current.AttachedNodeNames(workspaceRoot, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stagingNodes, []string{"node-a"}) {
		t.Fatalf("staging attachments = %#v, want node-a", stagingNodes)
	}
	productionNodes, err := current.AttachedNodeNames(workspaceRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	if len(productionNodes) != 0 {
		t.Fatalf("production attachments = %#v, want none", productionNodes)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["environment"] != "staging" {
		t.Fatalf("payload environment = %#v, want staging", payload["environment"])
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
	if !strings.Contains(err.Error(), "try the image default user") || !strings.Contains(err.Error(), "sudo -n true") {
		t.Fatalf("error = %v, want non-root SSH recovery guidance", err)
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
	events := decodeNDJSONOutput(t, &stdout)
	payload := events[len(events)-1]
	if payload["event"] != "result" || payload["ok"] != true {
		t.Fatalf("result event = %#v, want successful result", payload)
	}
	if payload["provider"] != providerHetzner || payload["provider_server_id"] != "srv-1" || payload["provider_region"] != "ash" || payload["provider_size"] != "cpx11" || payload["provider_image"] != providers.DefaultHetznerImage {
		t.Fatalf("payload = %#v, want provider metadata", payload)
	}
	if fakeProvider.createInput.Image != providers.DefaultHetznerImage {
		t.Fatalf("CreateServer image = %q, want normalized default image", fakeProvider.createInput.Image)
	}
	progress := stdout.String()
	if !strings.Contains(progress, "Creating hetzner server") || !strings.Contains(progress, "Server srv-1 ready at 203.0.113.20") {
		t.Fatalf("progress = %q, want provider create/ready events", progress)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want no command-contract output", stderr.String())
	}
}

func TestReleaseNodeForSnapshotSelectsSortedEligibleNode(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Tasks.Release = &config.TaskConfig{Service: "web", Command: []string{"bin/migrate"}}
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

func TestSoloAffectedNodesForNodeWithReleaseStateSkipsStatelessAttachments(t *testing.T) {
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
		Snapshots: map[string]desiredstate.DeploySnapshot{
			"/workspace/b\nproduction": {WorkspaceKey: "/workspace/b", Environment: "production"},
		},
	}

	got := soloAffectedNodesForNodeWithReleaseState(current, "node-a")
	want := []string{"node-a", "node-c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("affected nodes with release state = %#v, want %#v", got, want)
	}
	delete(current.Snapshots, "/workspace/b\nproduction")
	if got := soloAffectedNodesForNodeWithReleaseState(current, "node-a"); len(got) != 0 {
		t.Fatalf("affected nodes with no release state = %#v, want none", got)
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
	if payload["schema_version"] != float64(outputSchemaVersion) {
		t.Fatalf("schema_version = %#v, want %d", payload["schema_version"], outputSchemaVersion)
	}
	urls := jsonArrayFromMap(t, payload, "public_urls")
	if len(urls) != 1 || urls[0] != "http://203.0.113.10/" {
		t.Fatalf("public_urls = %#v, want web node URL only", urls)
	}
}

func TestSoloStatusUsesResolvedEnvironmentOverlay(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_ENVIRONMENT", "staging")
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"prod.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "prod.example.com", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "off"},
	}
	cfg.Environments = map[string]config.EnvironmentOverlay{
		"staging": {Ingress: &config.IngressConfigOverlay{
			Hosts: []string{"staging.example.com"},
			Rules: []config.IngressRuleConfig{{
				Match:  config.IngressMatchConfig{Host: "staging.example.com", PathPrefix: "/"},
				Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
			}},
		}},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: `{"revision":"rev","phase":"settled","summary":{"environments":1,"services":1}}` + "\n"}})

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{Nodes: map[string]config.Node{"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}}}}
	if _, _, err := current.AttachNode(workspaceRoot, "staging", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloStatus(context.Background(), SoloStatusOptions{}); err != nil {
		t.Fatalf("SoloStatus() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	urls := jsonArrayFromMap(t, payload, "public_urls")
	if len(urls) != 1 || urls[0] != "http://staging.example.com/" {
		t.Fatalf("public_urls = %#v, want staging host only", urls)
	}
}

func TestSoloStatusUsesVerifiedTLSPublicURLsAfterIngressCheckRecord(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"app.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "app.example.com", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "auto"},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: `{"revision":"rev","phase":"settled","summary":{"environments":1,"services":1}}` + "\n"}})

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{Nodes: map[string]config.Node{"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}}}}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	key, err := solo.EnvironmentStateKey(workspaceRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	current.IngressChecks = map[string]solo.IngressCheckRecord{
		key: {
			OK:          true,
			PublicURLs:  []string{"https://app.example.com/"},
			ExpectedIPs: []string{"203.0.113.10"},
		},
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloStatus(context.Background(), SoloStatusOptions{}); err != nil {
		t.Fatalf("SoloStatus() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	urls := jsonArrayFromMap(t, payload, "public_urls")
	if len(urls) != 1 || urls[0] != "https://app.example.com/" {
		t.Fatalf("public_urls = %#v, want verified HTTPS URL", urls)
	}
	if _, ok := payload["public_url_status"]; ok {
		t.Fatalf("payload = %#v, did not expect tls pending status after verified ingress check", payload)
	}
	if _, ok := payload["warnings"]; ok {
		t.Fatalf("payload = %#v, did not expect TLS warning after verified ingress check", payload)
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
	if payload["schema_version"] != float64(outputSchemaVersion) {
		t.Fatalf("schema_version = %#v, want %d", payload["schema_version"], outputSchemaVersion)
	}
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

func TestIngressDNSReportIncludesPublicURLsAndReadyNextSteps(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"127.0.0.1"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: "auto"},
	}

	report, err := ingressDNSReport(context.Background(), &cfg, map[string]config.Node{
		"node-a": {Host: "127.0.0.1", User: "root", Labels: []string{config.DefaultWebRole}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.OK {
		t.Fatalf("OK = false, report = %#v", report)
	}
	if !reflect.DeepEqual(report.PublicURLs, []string{"https://127.0.0.1/"}) {
		t.Fatalf("public_urls = %#v, want HTTPS loopback URL", report.PublicURLs)
	}
	if len(report.NextSteps) != 2 || report.NextSteps[0] != "devopsellence status" || report.NextSteps[1] != "curl https://127.0.0.1/" {
		t.Fatalf("next_steps = %#v, want status and curl", report.NextSteps)
	}
}

func TestIngressCheckPersistsSuccessfulReadinessRecord(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"127.0.0.1"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "127.0.0.1", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "auto"},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "127.0.0.1", User: "root", Labels: []string{config.DefaultWebRole}},
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

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.IngressCheck(context.Background(), IngressCheckOptions{}); err != nil {
		t.Fatalf("IngressCheck() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ok"] != true {
		t.Fatalf("payload ok = %v, want true", payload["ok"])
	}
	loaded, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	key, err := solo.EnvironmentStateKey(workspaceRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	record := loaded.IngressChecks[key]
	if !record.OK || !reflect.DeepEqual(record.PublicURLs, []string{"https://127.0.0.1/"}) || !reflect.DeepEqual(record.ExpectedIPs, []string{"127.0.0.1"}) || strings.TrimSpace(record.CheckedAt) == "" {
		t.Fatalf("ingress check record = %#v", record)
	}
}

func TestIngressCheckReturnsRenderedErrorAfterPrintingDNSReport(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"192.0.2.55"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "192.0.2.55", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "off"},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
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

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	err := app.IngressCheck(context.Background(), IngressCheckOptions{})
	if err == nil {
		t.Fatal("IngressCheck() error = nil, want DNS readiness failure")
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

func TestCheckIngressBeforeDeployDistinguishesMissingConcreteHostnames(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: "auto"},
	}

	app := &App{}
	err := app.checkIngressBeforeDeploy(context.Background(), &cfg, map[string]config.Node{
		"node-a": {Host: "127.0.0.1", User: "root", Labels: []string{config.DefaultWebRole}},
	}, false)
	if err == nil {
		t.Fatal("checkIngressBeforeDeploy() error = nil, want missing hostname failure")
	}
	if !strings.Contains(err.Error(), "no ingress hostnames configured") || !strings.Contains(err.Error(), "configure ingress hostnames") {
		t.Fatalf("error = %q, want missing-hostname guidance", err.Error())
	}
	if strings.Contains(err.Error(), "update DNS") {
		t.Fatalf("error = %q, did not expect DNS mismatch guidance", err.Error())
	}
}

func TestIngressCheckDoesNotWaitForMissingConcreteHostnames(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "*", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "auto"},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "127.0.0.1", User: "root", Labels: []string{config.DefaultWebRole}},
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

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}

	err := app.IngressCheck(ctx, IngressCheckOptions{Wait: time.Hour})
	if err == nil {
		t.Fatal("IngressCheck() error = nil, want missing hostname failure")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("IngressCheck() error = %v, want immediate non-retryable failure", err)
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !strings.Contains(exitErr.Err.Error(), "no ingress hostnames configured") {
		t.Fatalf("error = %#v, want no-hostname ExitError", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ok"] != false {
		t.Fatalf("payload ok = %v, want false", payload["ok"])
	}
}

func TestIngressDNSReportIncludesSSLIPHintForPublicIPWithoutConcreteHostnames(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "My App", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: "auto"},
	}

	report, err := ingressDNSReport(context.Background(), &cfg, map[string]config.Node{
		"node-a": {Host: "8.8.8.8", User: "root", Labels: []string{config.DefaultWebRole}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("OK = true, want hostname configuration guidance")
	}
	if len(report.Hints) != 1 {
		t.Fatalf("hints = %#v, want one sslip.io hint", report.Hints)
	}
	hint := report.Hints[0]
	if hint.Code != "solo_ingress_no_hostname" || hint.Severity != "suggestion" {
		t.Fatalf("hint = %#v, want no-hostname suggestion", hint)
	}
	if hint.SuggestedAction.Kind != "use_temporary_dns_hostname" || hint.SuggestedAction.Provider != "sslip.io" {
		t.Fatalf("suggested_action = %#v, want sslip.io temporary hostname", hint.SuggestedAction)
	}
	if got, want := hint.SuggestedAction.Hostname, "8.8.8.8.sslip.io"; got != want {
		t.Fatalf("suggested hostname = %q, want %q", got, want)
	}
	if !strings.Contains(hint.SuggestedAction.Command, "devopsellence ingress set --host '8.8.8.8.sslip.io' --tls-mode 'auto'") {
		t.Fatalf("command = %q, want ingress set command", hint.SuggestedAction.Command)
	}
	if len(hint.SuggestedAction.Risks) == 0 {
		t.Fatalf("risks = %#v, want explicit caveats", hint.SuggestedAction.Risks)
	}
}

func TestIngressDNSReportOmitsSSLIPHintForMultipleIngressIPs(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: "auto"},
	}

	report, err := ingressDNSReport(context.Background(), &cfg, map[string]config.Node{
		"node-a": {Host: "8.8.8.8", User: "root", Labels: []string{config.DefaultWebRole}},
		"node-b": {Host: "1.1.1.1", User: "root", Labels: []string{config.DefaultWebRole}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Hints) != 0 {
		t.Fatalf("hints = %#v, want no sslip.io hint for multiple expected ingress IPs", report.Hints)
	}
}

func TestTemporaryDNSHostnameUsesPlainIPHost(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "10.0.0.1", "production")

	got := temporaryDNSHostname(&cfg, "8.8.8.8")
	want := "8.8.8.8.sslip.io"
	if got != want {
		t.Fatalf("temporaryDNSHostname() = %q, want %q", got, want)
	}
}

func TestTemporaryDNSCommandPreservesConfiguredTLSMode(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{TLS: config.IngressTLSConfig{Mode: " OFF "}}

	got := temporaryDNSCommand(&cfg, "8.8.8.8.sslip.io")
	want := "devopsellence ingress set --host '8.8.8.8.sslip.io' --tls-mode 'off'"
	if got != want {
		t.Fatalf("temporaryDNSCommand() = %q, want %q", got, want)
	}
}

func TestTemporaryDNSIPv4AcceptsOnlyPubliclyRoutableAddresses(t *testing.T) {
	tests := map[string]bool{
		"8.8.8.8":         true,
		"0.1.2.3":         false,
		"10.0.0.1":        false,
		"100.64.0.1":      false,
		"127.0.0.1":       false,
		"169.254.1.1":     false,
		"192.0.2.10":      false,
		"198.18.0.1":      false,
		"198.51.100.10":   false,
		"203.0.113.10":    false,
		"224.0.0.1":       false,
		"255.255.255.255": false,
		"2001:db8::1":     false,
	}
	for value, want := range tests {
		t.Run(value, func(t *testing.T) {
			if got := isTemporaryDNSIPv4(value); got != want {
				t.Fatalf("isTemporaryDNSIPv4(%q) = %v, want %v", value, got, want)
			}
		})
	}
}

func TestCheckIngressBeforeDeployTreatsAutoTLSModeCaseInsensitively(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: " AUTO "},
	}

	err := (&App{}).checkIngressBeforeDeploy(context.Background(), &cfg, map[string]config.Node{
		"node-a": {Host: "8.8.8.8", User: "root", Labels: []string{config.DefaultWebRole}},
	}, false)
	if err == nil {
		t.Fatal("checkIngressBeforeDeploy() error = nil, want DNS readiness failure")
	}
	if !strings.Contains(err.Error(), "no ingress hostnames configured") {
		t.Fatalf("error = %q, want DNS readiness check to run", err.Error())
	}
}

func TestCheckIngressBeforeDeployIncludesSSLIPHintFields(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: "auto"},
	}

	err := (&App{}).checkIngressBeforeDeploy(context.Background(), &cfg, map[string]config.Node{
		"node-a": {Host: "8.8.8.8", User: "root", Labels: []string{config.DefaultWebRole}},
	}, false)
	if err == nil {
		t.Fatal("checkIngressBeforeDeploy() error = nil, want missing hostname failure")
	}
	var structured StructuredError
	if !errors.As(err, &structured) {
		t.Fatalf("error = %#v, want structured error", err)
	}
	fields := structured.ErrorFields()
	if fields["kind"] != "ingress_dns_not_ready" {
		t.Fatalf("kind = %v, want ingress_dns_not_ready", fields["kind"])
	}
	hints, ok := fields["hints"].([]ingressHint)
	if !ok || len(hints) != 1 {
		t.Fatalf("hints = %#v, want one ingress hint", fields["hints"])
	}
	if got, want := hints[0].SuggestedAction.Hostname, "8.8.8.8.sslip.io"; got != want {
		t.Fatalf("suggested hostname = %q, want %q", got, want)
	}
}

func TestIngressDNSReportBootstrapWildcardHostPromptsForRealHostnames(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"*"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: "auto"},
	}

	report, err := ingressDNSReport(context.Background(), &cfg, map[string]config.Node{
		"node-a": {Host: "127.0.0.1", User: "root", Labels: []string{config.DefaultWebRole}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("OK = true, want hostname configuration guidance")
	}
	if len(report.PublicURLs) != 0 {
		t.Fatalf("public_urls = %#v, want wildcard bootstrap host filtered out", report.PublicURLs)
	}
	if len(report.Hosts) != 0 {
		t.Fatalf("hosts = %#v, want no DNS lookup for wildcard bootstrap host", report.Hosts)
	}
	if len(report.NextSteps) != 3 || report.NextSteps[0] != "devopsellence status" || !strings.Contains(report.NextSteps[1], "ingress set") {
		t.Fatalf("next_steps = %#v, want status first and hostname guidance", report.NextSteps)
	}
	for _, step := range report.NextSteps {
		if strings.Contains(step, "*") {
			t.Fatalf("next_steps = %#v, want wildcard host omitted", report.NextSteps)
		}
	}
}

func TestIngressDNSReportIncludesRepairNextStepsWhenDNSIsNotReady(t *testing.T) {
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"192.0.2.55"},
		Rules: []config.IngressRuleConfig{{Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName}}},
		TLS:   config.IngressTLSConfig{Mode: "off"},
	}

	report, err := ingressDNSReport(context.Background(), &cfg, map[string]config.Node{
		"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatalf("OK = true, want DNS mismatch failure")
	}
	if !reflect.DeepEqual(report.PublicURLs, []string{"http://192.0.2.55/"}) {
		t.Fatalf("public_urls = %#v, want configured endpoint URL", report.PublicURLs)
	}
	if len(report.NextSteps) != 3 || report.NextSteps[0] != "devopsellence status" || report.NextSteps[1] != "update DNS records to point at expected_ips" {
		t.Fatalf("next_steps = %#v, want status first and repair guidance", report.NextSteps)
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
	checks := jsonArrayFromMap(t, payload, "checks")
	var nodesDetail string
	for _, item := range checks {
		check := jsonMapFromAny(t, item)
		if check["name"] == "nodes" {
			nodesDetail, _ = check["detail"].(string)
			break
		}
	}
	if nodesDetail != "No nodes registered in solo state. Run `devopsellence node create <name>`." {
		t.Fatalf("nodes check detail = %q, want node create guidance", nodesDetail)
	}
}

func TestSoloDoctorScopesRuntimeChecksToCurrentEnvironment(t *testing.T) {
	installFakeSoloCommands(t, nil)
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, workspaceRoot)
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root"},
			"node-b": {Host: "203.0.113.11", User: "root"},
		},
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		Docker:      &fakeDocker{},
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         workspaceRoot,
	}
	if err := app.SoloDoctor(context.Background()); err != nil {
		t.Fatalf("SoloDoctor() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	checks := jsonArrayFromMap(t, payload, "runtime_checks")
	for _, item := range checks {
		check := jsonMapFromAny(t, item)
		if check["node"] != "node-a" {
			t.Fatalf("runtime check node = %v, want only node-a", check["node"])
		}
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
	if payload["schema_version"] != float64(outputSchemaVersion) {
		t.Fatalf("schema_version = %#v, want %d", payload["schema_version"], outputSchemaVersion)
	}
	nodes := jsonArrayFromMap(t, payload, "nodes")
	node := jsonMapFromAny(t, nodes[0])
	if node["node"] != "node-a" || !strings.Contains(stringValueAny(node["error"]), "ssh root@203.0.113.10:") {
		t.Fatalf("node payload = %#v, want node read error", node)
	}
}

func TestSoloNodeListDefaultsToCurrentEnvironmentAndRedactsPrivateFields(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", SSHKey: "/secret/key", ProviderServerID: "123", Labels: []string{"web"}},
			"node-b": {Host: "203.0.113.11", User: "root", SSHKey: "/other/key"},
		},
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, Cwd: workspaceRoot}
	if err := app.SoloNodeList(context.Background(), SoloNodeListOptions{}); err != nil {
		t.Fatalf("SoloNodeList() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["scope"] != "current_environment" {
		t.Fatalf("scope = %v, want current_environment", payload["scope"])
	}
	nodes := jsonMapFromAny(t, payload["nodes"])
	if _, ok := nodes["node-b"]; ok {
		t.Fatalf("nodes = %#v, want node-b omitted", nodes)
	}
	nodeA := jsonMapFromAny(t, nodes["node-a"])
	if _, ok := nodeA["ssh_key"]; ok {
		t.Fatalf("node-a = %#v, want ssh_key redacted", nodeA)
	}
	if _, ok := nodeA["provider_server_id"]; ok {
		t.Fatalf("node-a = %#v, want provider_server_id redacted", nodeA)
	}
	if nodeA["ssh_key_configured"] != true || nodeA["provider_server_id_configured"] != true {
		t.Fatalf("node-a = %#v, want configured booleans", nodeA)
	}
	items := jsonArrayFromMap(t, payload, "node_items")
	item := jsonMapFromAny(t, items[0])
	attachments := jsonArrayFromMap(t, item, "attachments")
	attachment := jsonMapFromAny(t, attachments[0])
	if _, ok := attachment["workspace_root"]; ok {
		t.Fatalf("attachment = %#v, want workspace_root redacted", attachment)
	}
	if _, ok := attachment["workspace_key"]; ok {
		t.Fatalf("attachment = %#v, want workspace_key redacted", attachment)
	}

	stdout.Reset()
	if err := app.SoloNodeList(context.Background(), SoloNodeListOptions{All: true}); err != nil {
		t.Fatalf("SoloNodeList(--all) error = %v", err)
	}
	payload = decodeJSONOutput(t, &stdout)
	if payload["scope"] != "global" {
		t.Fatalf("scope = %v, want global", payload["scope"])
	}
	items = jsonArrayFromMap(t, payload, "node_items")
	for _, rawItem := range items {
		item := jsonMapFromAny(t, rawItem)
		if item["name"] != "node-a" {
			continue
		}
		if item["current_environment_bound"] != true {
			t.Fatalf("node-a item = %#v, want current_environment_bound", item)
		}
		attachments := jsonArrayFromMap(t, item, "attachments")
		attachment := jsonMapFromAny(t, attachments[0])
		if attachment["current_environment"] != true {
			t.Fatalf("attachment = %#v, want current_environment marker", attachment)
		}
		return
	}
	t.Fatalf("node_items = %#v, want node-a", items)
}

func TestSoloNodeListRequiresConfigForDefaultScope(t *testing.T) {
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

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, Cwd: workspaceRoot}
	err := app.SoloNodeList(context.Background(), SoloNodeListOptions{})
	if err == nil || !strings.Contains(err.Error(), "use `--all` to list all nodes") {
		t.Fatalf("SoloNodeList() error = %v, want --all guidance", err)
	}

	if err := app.SoloNodeList(context.Background(), SoloNodeListOptions{All: true}); err != nil {
		t.Fatalf("SoloNodeList(--all) error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["scope"] != "global" {
		t.Fatalf("scope = %v, want global", payload["scope"])
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

func TestSoloWorkloadLogsUsesProjectScopedRuntimeEnvironmentForCoHostedNode(t *testing.T) {
	commandPath := filepath.Join(t.TempDir(), "workload-command")
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_WORKLOAD_LOGS_COMMAND", commandPath)
	installFakeSoloCommands(t, nil)
	workspaceRoot := t.TempDir()
	otherRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(otherRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	workspaceKey, err := solo.EnvironmentStateKey(workspaceRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := solo.EnvironmentStateKey(otherRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	current.Snapshots = map[string]desiredstate.DeploySnapshot{
		workspaceKey: {WorkspaceRoot: workspaceRoot, WorkspaceKey: strings.Split(workspaceKey, "\n")[0], Environment: "production", Metadata: desiredstate.SnapshotMetadata{Project: "demo"}},
		otherKey:     {WorkspaceRoot: otherRoot, WorkspaceKey: strings.Split(otherKey, "\n")[0], Environment: "production", Metadata: desiredstate.SnapshotMetadata{Project: "api"}},
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloWorkloadLogs(context.Background(), SoloWorkloadLogsOptions{ServiceName: "web", Nodes: []string{"node-a"}, Lines: 20}); err != nil {
		t.Fatalf("SoloWorkloadLogs() error = %v", err)
	}
	runtimeEnvironment, err := soloRuntimeEnvironmentNameForNode(current, workspaceRoot, "production", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	commandBytes, err := os.ReadFile(commandPath)
	if err != nil {
		t.Fatalf("read workload command: %v", err)
	}
	command := string(commandBytes)
	if !strings.Contains(command, runtimeEnvironment) {
		t.Fatalf("workload logs command = %q, want runtime environment %q", command, runtimeEnvironment)
	}
	if strings.Contains(command, "env_candidates") || strings.Contains(command, "__DEVOPSELLENCE_SELECTED_ENVIRONMENT__") {
		t.Fatalf("workload logs command = %q, should target one clean-slate runtime environment without compatibility candidates", command)
	}
	payload := decodeJSONOutput(t, &stdout)
	nodes := jsonArrayFromMap(t, payload, "nodes")
	node := jsonMapFromAny(t, nodes[0])
	if node["runtime_environment"] != runtimeEnvironment {
		t.Fatalf("node payload = %#v, want runtime environment %q", node, runtimeEnvironment)
	}
}

func TestSoloExecRunsCommandInServiceContainer(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: `{"revision":"abc","phase":"settled","environments":[{"name":"production","services":[{"name":"web","state":"starting","container":"svc-production-web-abc"}]}]}` + "\n"}})
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	err := app.SoloExec(context.Background(), SoloExecOptions{ServiceName: "web", Command: []string{"bin/rails", "runner", "puts Rails.env"}})
	if err != nil {
		t.Fatalf("SoloExec() error = %v", err)
	}
	events := decodeNDJSONOutput(t, &stdout)
	if len(events) != 4 {
		t.Fatalf("events = %#v, want started/stdout/stderr/finished", events)
	}
	if events[0]["event"] != "started" || events[0]["operation"] != "devopsellence exec" || events[0]["target"] != "service" || events[0]["container"] != "svc-production-web-abc" {
		t.Fatalf("started event = %#v", events[0])
	}
	var sawStdout, sawStderr bool
	for _, event := range events {
		if event["event"] == "output" && event["stream"] == "stdout" && event["message"] == "service stdout" {
			sawStdout = true
		}
		if event["event"] == "output" && event["stream"] == "stderr" && event["message"] == "service stderr" {
			sawStderr = true
		}
	}
	if !sawStdout || !sawStderr {
		t.Fatalf("events = %#v, want stdout and stderr output events", events)
	}
	if events[3]["event"] != "result" || events[3]["exit_code"] != float64(0) || events[3]["ok"] != true {
		t.Fatalf("finished event = %#v", events[3])
	}
}

func TestSoloExecUsesProjectScopedRuntimeEnvironmentForCoHostedNode(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	otherRoot := t.TempDir()
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(otherRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	workspaceKey, err := solo.EnvironmentStateKey(cwd, "production")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := solo.EnvironmentStateKey(otherRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	current.Snapshots = map[string]desiredstate.DeploySnapshot{
		workspaceKey: {WorkspaceRoot: cwd, WorkspaceKey: strings.Split(workspaceKey, "\n")[0], Environment: "production", Metadata: desiredstate.SnapshotMetadata{Project: "demo"}},
		otherKey:     {WorkspaceRoot: otherRoot, WorkspaceKey: strings.Split(otherKey, "\n")[0], Environment: "production", Metadata: desiredstate.SnapshotMetadata{Project: "api"}},
	}
	runtimeEnvironment, err := soloRuntimeEnvironmentNameForNode(current, cwd, "production", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	status := fmt.Sprintf(`{"revision":"abc","phase":"settled","environments":[{"name":%q,"services":[{"name":"web","state":"running","container":"svc-production-web-abc"}]},{"name":"production","services":[{"name":"web","state":"running","container":"wrong-container"}]}]}`+"\n", runtimeEnvironment)
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: status}})
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	err = app.SoloExec(context.Background(), SoloExecOptions{ServiceName: "web", Nodes: []string{"node-a"}, Command: []string{"bin/rails", "runner", "puts Rails.env"}})
	if err != nil {
		t.Fatalf("SoloExec() error = %v", err)
	}
	events := decodeNDJSONOutput(t, &stdout)
	if len(events) == 0 || events[0]["container"] != "svc-production-web-abc" || events[0]["environment"] != runtimeEnvironment {
		t.Fatalf("started event = %#v, want co-hosted runtime env %q container", events[0], runtimeEnvironment)
	}
}

func TestSoloExecDoesNotFallBackToLogicalRuntimeEnvironment(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	workspaceKey, err := solo.EnvironmentStateKey(cwd, "production")
	if err != nil {
		t.Fatal(err)
	}
	current.Snapshots = map[string]desiredstate.DeploySnapshot{
		workspaceKey: {WorkspaceRoot: cwd, WorkspaceKey: strings.Split(workspaceKey, "\n")[0], Environment: "production", Metadata: desiredstate.SnapshotMetadata{Project: "demo"}},
	}
	status := `{"revision":"abc","phase":"settled","environments":[{"name":"production","services":[{"name":"web","state":"running","container":"svc-production-web-abc"}]}]}` + "\n"
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: status}})
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	err = app.SoloExec(context.Background(), SoloExecOptions{ServiceName: "web", Nodes: []string{"node-a"}, Command: []string{"bin/rails", "runner", "puts Rails.env"}})
	if err == nil || !strings.Contains(err.Error(), `no active container found for service "web" in environment production`) {
		t.Fatalf("SoloExec() error = %v, want clean-slate runtime environment miss", err)
	}
}

func TestSoloRuntimeEnvironmentNameStaysProjectScopedAfterPeerDetach(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	otherRoot := t.TempDir()
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	workspaceKey, err := solo.EnvironmentStateKey(cwd, "production")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := solo.EnvironmentStateKey(otherRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	current.Snapshots = map[string]desiredstate.DeploySnapshot{
		workspaceKey: {WorkspaceRoot: cwd, WorkspaceKey: strings.Split(workspaceKey, "\n")[0], Environment: "production", Metadata: desiredstate.SnapshotMetadata{Project: "demo"}},
		// The peer was detached, so its attachment is gone, but historical solo
		// state may still retain its snapshot. CLI lookups must still match the
		// project-scoped runtime name that aggregated desired-state publishes for
		// the surviving project.
		otherKey: {WorkspaceRoot: otherRoot, WorkspaceKey: strings.Split(otherKey, "\n")[0], Environment: "production", Metadata: desiredstate.SnapshotMetadata{Project: "api"}},
	}

	runtimeEnvironment, err := soloRuntimeEnvironmentNameForNode(current, cwd, "production", "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if runtimeEnvironment == "production" || !strings.HasPrefix(runtimeEnvironment, "demo-production-") {
		t.Fatalf("runtime environment = %q, want stable project-scoped name after peer detach", runtimeEnvironment)
	}
}

func TestRootExecEnvSelectsAttachedEnvironment(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	cfg, err := config.LoadFromRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Environments = map[string]config.EnvironmentOverlay{"staging": {}}
	if _, err := config.Write(cwd, *cfg); err != nil {
		t.Fatal(err)
	}
	status := `{"revision":"abc","phase":"settled","environments":[{"name":"production","services":[{"name":"web","state":"running","container":"wrong-container"}]},{"name":"staging","services":[{"name":"web","state":"running","container":"svc-production-web-abc"}]}]}` + "\n"
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: status}})
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "staging", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"exec", "--env", "staging", "web", "--", "bin/rails", "runner", "puts Rails.env"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	events := decodeNDJSONOutput(t, &stdout)
	if len(events) == 0 || events[0]["environment"] != "staging" || events[0]["container"] != "svc-production-web-abc" {
		t.Fatalf("started event = %#v, want staging container", events[0])
	}
}

func TestRootExecEnvFailsWhenSelectedEnvironmentHasNoAttachedNodes(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	cfg, err := config.LoadFromRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Environments = map[string]config.EnvironmentOverlay{"staging": {}}
	if _, err := config.Write(cwd, *cfg); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, nil)
	current := solo.State{}
	if err := current.SetNode("unrelated", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"exec", "--env", "staging", "web", "--", "true"})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no nodes selected for environment staging") {
		t.Fatalf("Execute() error = %v, want no nodes selected for staging", err)
	}
}

func TestSoloIngressCertInstallRejectsAutoTLSCohostOnSelectedNode(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	installFakeSoloCommands(t, nil)
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(certFile, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherRoot := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(otherRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(otherRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	otherKey, err := solo.EnvironmentStateKey(otherRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	current.Snapshots[otherKey] = desiredstate.DeploySnapshot{
		WorkspaceRoot: otherRoot,
		WorkspaceKey:  otherRoot,
		Environment:   "production",
		Revision:      "auto1234",
		Metadata:      desiredstate.SnapshotMetadata{Project: "auto-app"},
		Ingress:       &desiredstate.IngressJSON{Mode: "public", TLS: desiredstate.IngressTLSJSON{Mode: "auto"}, Hosts: []string{"auto.example.com"}},
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	err = app.SoloIngressCertInstall(context.Background(), SoloIngressCertInstallOptions{CertFile: certFile, KeyFile: keyFile})
	if err == nil || !strings.Contains(err.Error(), "manual TLS cert install would affect auto TLS") {
		t.Fatalf("SoloIngressCertInstall() error = %v, want auto TLS cohost guard", err)
	}
}

func TestSoloIngressCertInstallAllowsCurrentEnvironmentAutoTLSMigration(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	uploadsPath := filepath.Join(t.TempDir(), "uploads")
	scriptPath := filepath.Join(t.TempDir(), "script")
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_UPLOADS", uploadsPath)
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_SCRIPT", scriptPath)
	installFakeSoloCommands(t, nil)
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(certFile, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	currentKey, err := solo.EnvironmentStateKey(cwd, "production")
	if err != nil {
		t.Fatal(err)
	}
	current.Snapshots[currentKey] = desiredstate.DeploySnapshot{
		WorkspaceRoot: cwd,
		WorkspaceKey:  strings.Split(currentKey, "\n")[0],
		Environment:   "production",
		Revision:      "auto1234",
		Metadata:      desiredstate.SnapshotMetadata{Project: "demo"},
		Ingress:       &desiredstate.IngressJSON{Mode: "public", TLS: desiredstate.IngressTLSJSON{Mode: "auto"}, Hosts: []string{"demo.example.com"}},
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	if err := app.SoloIngressCertInstall(context.Background(), SoloIngressCertInstallOptions{CertFile: certFile, KeyFile: keyFile}); err != nil {
		t.Fatalf("SoloIngressCertInstall() error = %v, want current environment auto TLS migration allowed", err)
	}
	uploads, err := os.ReadFile(uploadsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(uploads), "/tmp/devopsellence-ingress-"); got != 2 {
		t.Fatalf("uploads = %q, want cert and key uploads", uploads)
	}
}

func TestSoloIngressCertInstallUploadsManualTLSFilesToAttachedNode(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	uploadsPath := filepath.Join(t.TempDir(), "uploads")
	scriptPath := filepath.Join(t.TempDir(), "script")
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_UPLOADS", uploadsPath)
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_SCRIPT", scriptPath)
	installFakeSoloCommands(t, nil)
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(certFile, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	if err := app.SoloIngressCertInstall(context.Background(), SoloIngressCertInstallOptions{CertFile: certFile, KeyFile: keyFile}); err != nil {
		t.Fatalf("SoloIngressCertInstall() error = %v", err)
	}
	uploads, err := os.ReadFile(uploadsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(uploads), "/tmp/devopsellence-ingress-"); got != 2 {
		t.Fatalf("uploads = %q, want two ingress TLS uploads", string(uploads))
	}
	if !strings.Contains(string(uploads), "umask 077; cat > '/tmp/devopsellence-ingress-key-") {
		t.Fatalf("uploads = %q, want private key upload to use restrictive umask", string(uploads))
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, snippet := range []string{"/var/lib/devopsellence/ingress-cert.pem", "/var/lib/devopsellence/ingress-key.pem", "systemctl restart devopsellence-agent"} {
		if !strings.Contains(string(script), snippet) {
			t.Fatalf("script = %q, want %q", string(script), snippet)
		}
	}
	if strings.Contains(string(script), "systemctl restart devopsellence-agent || true") {
		t.Fatalf("script = %q, want restart failures to be propagated", string(script))
	}
	payload := decodeJSONOutput(t, &stdout)
	if intValueAny(payload["schema_version"]) != outputSchemaVersion {
		t.Fatalf("output = %#v, want JSON payload", payload)
	}
}

func TestSoloIngressCertInstallWithExplicitNodeDoesNotRequireWorkspaceConfig(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	uploadsPath := filepath.Join(t.TempDir(), "uploads")
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_UPLOADS", uploadsPath)
	installFakeSoloCommands(t, nil)
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(certFile, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	if err := app.SoloIngressCertInstall(context.Background(), SoloIngressCertInstallOptions{CertFile: certFile, KeyFile: keyFile, Nodes: []string{"node-a"}}); err != nil {
		t.Fatalf("SoloIngressCertInstall() error = %v, want explicit --node to work outside a workspace", err)
	}
	uploads, err := os.ReadFile(uploadsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(uploads), "/tmp/devopsellence-ingress-"); got != 2 {
		t.Fatalf("uploads = %q, want cert and key uploads", uploads)
	}
}

func TestSoloIngressCertInstallFailsWhenAgentRestartFails(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_RESTART_FAIL", "1")
	installFakeSoloCommands(t, nil)
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(certFile, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := solo.State{}
	if err := current.SetNode("node-a", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	err := app.SoloIngressCertInstall(context.Background(), SoloIngressCertInstallOptions{CertFile: certFile, KeyFile: keyFile})
	if err == nil || !strings.Contains(err.Error(), "[node-a] install ingress cert") {
		t.Fatalf("SoloIngressCertInstall() error = %v, want node install failure", err)
	}
}

func TestSoloIngressCertInstallFailsWhenCurrentEnvironmentHasNoAttachedNodes(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	installFakeSoloCommands(t, nil)
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	keyFile := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(certFile, []byte("cert"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	current := solo.State{}
	if err := current.SetNode("unrelated", config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	err := app.SoloIngressCertInstall(context.Background(), SoloIngressCertInstallOptions{CertFile: certFile, KeyFile: keyFile})
	if err == nil || !strings.Contains(err.Error(), "no nodes selected for environment production") {
		t.Fatalf("SoloIngressCertInstall() error = %v, want no nodes selected for production", err)
	}
}

func TestSoloExecPreservesRemoteExitCodeInErrorEvent(t *testing.T) {
	installFakeSoloCommands(t, nil)
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{Nodes: map[string]config.Node{
		"node-a": {Host: "203.0.113.10", User: "root"},
	}}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState}
	err := app.SoloNodeExec(context.Background(), SoloNodeExecOptions{Node: "node-a", Command: []string{"missing-command"}})
	if err == nil {
		t.Fatal("SoloNodeExec() error = nil, want remote failure")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 127 {
		t.Fatalf("error = %#v, want ExitError code 127", err)
	}
	events := decodeNDJSONOutput(t, &stdout)
	last := events[len(events)-1]
	if last["event"] != "error" || last["ok"] != false {
		t.Fatalf("last event = %#v, want error", last)
	}
	errorPayload := jsonMapFromAny(t, last["error"])
	if errorPayload["exit_code"] != float64(127) {
		t.Fatalf("error payload = %#v, want remote exit 127", errorPayload)
	}
}

func TestSoloExecRequiresNodeWhenServiceHasMultipleContainers(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	status := `{"revision":"abc","phase":"settled","environments":[{"name":"production","services":[{"name":"web","state":"running","container":"svc-production-web-abc"}]}]}` + "\n"
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: status}, {stdout: status}})
	current := solo.State{}
	for _, nodeName := range []string{"node-a", "node-b"} {
		if err := current.SetNode(nodeName, config.Node{Host: "203.0.113.10", User: "root"}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := current.AttachNode(cwd, "production", nodeName); err != nil {
			t.Fatal(err)
		}
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := NewApp(bytes.NewBuffer(nil), &stdout, io.Discard, cwd)
	err := app.SoloExec(context.Background(), SoloExecOptions{ServiceName: "web", Command: []string{"true"}})
	if err == nil || !strings.Contains(err.Error(), "select a single node with --node <node>") {
		t.Fatalf("SoloExec() error = %v, want single-node guidance", err)
	}
}

func TestSoloNodeExecRunsSSHCommand(t *testing.T) {
	installFakeSoloCommands(t, nil)
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{Nodes: map[string]config.Node{
		"node-a": {Host: "203.0.113.10", User: "root"},
	}}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState}
	err := app.SoloNodeExec(context.Background(), SoloNodeExecOptions{Node: "node-a", Command: []string{"uptime"}})
	if err != nil {
		t.Fatalf("SoloNodeExec() error = %v", err)
	}
	events := decodeNDJSONOutput(t, &stdout)
	if len(events) != 3 {
		t.Fatalf("events = %#v, want started/output/finished", events)
	}
	if events[0]["event"] != "started" || events[0]["operation"] != "devopsellence node exec" || events[0]["target"] != "node" {
		t.Fatalf("started event = %#v", events[0])
	}
	if events[1]["stream"] != "stdout" || events[1]["message"] != "node stdout" {
		t.Fatalf("output event = %#v", events[1])
	}
	if events[2]["exit_code"] != float64(0) {
		t.Fatalf("finished event = %#v", events[2])
	}
}

func TestSoloExecEventWriterSuppressesOnlyWrapperStderrNewline(t *testing.T) {
	var stdout bytes.Buffer
	exitCode := -1
	target := soloExecTarget{Kind: "node", Node: "node-a", Command: []string{"sh", "-c", "printf '\\n' >&2"}}
	writer := &soloExecEventWriter{
		stream:     output.New(&stdout, io.Discard).Stream("devopsellence node exec"),
		target:     target,
		streamName: "stderr",
		exitCode:   &exitCode,
		exitMarker: testSoloExecExitMarker,
	}

	if _, err := writer.Write([]byte("\n\n" + testSoloExecExitMarker + "0\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exitCode)
	}
	events := decodeNDJSONOutput(t, &stdout)
	if len(events) != 1 || events[0]["stream"] != "stderr" || events[0]["message"] != "" {
		t.Fatalf("events = %#v, want one blank stderr line", events)
	}
}

func TestSoloExecEventWriterReturnsProcessedBytesOnStreamError(t *testing.T) {
	exitCode := -1
	writer := &soloExecEventWriter{
		stream:     output.New(errorWriter{}, io.Discard).Stream("devopsellence node exec"),
		target:     soloExecTarget{Kind: "node", Node: "node-a", Command: []string{"uptime"}},
		streamName: "stdout",
		exitCode:   &exitCode,
		exitMarker: testSoloExecExitMarker,
	}

	n, err := writer.Write([]byte("abc\nnext"))
	if err == nil {
		t.Fatal("Write() error = nil, want stream write error")
	}
	if n != 3 {
		t.Fatalf("Write() n = %d, want 3", n)
	}
}

func TestSoloExecEventWriterRequiresExactExitMarker(t *testing.T) {
	var stdout bytes.Buffer
	exitCode := -1
	writer := &soloExecEventWriter{
		stream:     output.New(&stdout, io.Discard).Stream("devopsellence node exec"),
		target:     soloExecTarget{Kind: "node", Node: "node-a", Command: []string{"echo"}},
		streamName: "stderr",
		exitCode:   &exitCode,
		exitMarker: testSoloExecExitMarker,
	}

	input := testSoloExecExitMarker + "0 trailing text\n" + testSoloExecExitMarker + "7\n"
	if _, err := writer.Write([]byte(input)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if exitCode != 7 {
		t.Fatalf("exit code = %d, want 7", exitCode)
	}
	events := decodeNDJSONOutput(t, &stdout)
	if len(events) != 1 || events[0]["message"] != testSoloExecExitMarker+"0 trailing text" {
		t.Fatalf("events = %#v, want non-exact marker emitted as stderr", events)
	}
}

func TestSoloExecEventWriterDoesNotEmitBlankLineAfterTruncation(t *testing.T) {
	var stdout bytes.Buffer
	exitCode := -1
	writer := &soloExecEventWriter{
		stream:     output.New(&stdout, io.Discard).Stream("devopsellence node exec"),
		target:     soloExecTarget{Kind: "node", Node: "node-a", Command: []string{"yes"}},
		streamName: "stdout",
		exitCode:   &exitCode,
		exitMarker: testSoloExecExitMarker,
	}

	line := strings.Repeat("x", soloExecMaxLineBytes+8) + "\n"
	if _, err := writer.Write([]byte(line)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	output := stdout.String()
	if strings.Count(output, "\n") != 1 {
		t.Fatalf("output line count = %d, want one truncated output event", strings.Count(output, "\n"))
	}
	for _, snippet := range []string{`"event":"output"`, `"stream":"stdout"`, `"truncated":true`} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("output = %q, want %q", output, snippet)
		}
	}
}

func TestRemoteDockerExecCommandReportsMissingCommand(t *testing.T) {
	command := remoteDockerExecCommand("svc-production-web-abc", nil)
	for _, snippet := range []string{"missing command after --", "exit 2"} {
		if !strings.Contains(command, snippet) {
			t.Fatalf("command = %q, want %q", command, snippet)
		}
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
	command := remoteDockerLogsCommand("demo-production-abc123", "web", 20)
	for _, snippet := range []string{`ps_status=$?`, `Failed to list workload containers`, `__DEVOPSELLENCE_NO_WORKLOAD_CONTAINERS__`, `head -n 20`, `rc=0`, `inspect_status=$?`, `logs_status=$?`, `exit "$rc"`, `demo-production-abc123`} {
		if !strings.Contains(command, snippet) {
			t.Fatalf("command = %q, want %q", command, snippet)
		}
	}
	for _, forbidden := range []string{`env_candidates`, `while IFS= read -r env`, `__DEVOPSELLENCE_SELECTED_ENVIRONMENT__`} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("command = %q, should not contain compatibility candidate machinery %q", command, forbidden)
		}
	}
}

func TestRemoteDockerLogsCommandHandlesRuntimeEnvironmentWithWhitespace(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "docker"), `#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  info)
    exit 0
    ;;
  ps)
    saw_env=0
    saw_service=0
    for arg in "$@"; do
      if [[ "$arg" == "label=devopsellence.environment=prod us" ]]; then
        saw_env=1
      fi
      if [[ "$arg" == "label=devopsellence.service=web" ]]; then
        saw_service=1
      fi
    done
    if [[ "$saw_env" == "1" && "$saw_service" == "1" ]]; then
      printf 'container-1\n'
    fi
    exit 0
    ;;
  inspect)
    printf '/svc-prod-us\n'
    exit 0
    ;;
  logs)
    printf 'log line for spaced env\n'
    exit 0
    ;;
esac
printf 'unexpected docker command: %s\n' "$*" >&2
exit 1
`)
	cmd := exec.Command("sh", "-c", remoteDockerLogsCommand("prod us", "web", 20))
	cmd.Env = append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("remote docker logs command failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "__DEVOPSELLENCE_SELECTED_ENVIRONMENT__") || !strings.Contains(string(out), "==> svc-prod-us <==") || !strings.Contains(string(out), "log line for spaced env") {
		t.Fatalf("output = %q, want logs for whitespace runtime environment without compatibility marker", out)
	}
}

func TestSoloWorkloadLogsFallsBackToAgentLogsWhenContainersMissing(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_FAKE_SSH_WORKLOAD_LOGS_EMPTY", "1")
	installFakeSoloCommands(t, nil)
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{Nodes: map[string]config.Node{"node-a": {Host: "203.0.113.10", User: "root"}}}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	err := app.SoloWorkloadLogs(context.Background(), SoloWorkloadLogsOptions{ServiceName: "web", Lines: 20})
	if err == nil {
		t.Fatal("SoloWorkloadLogs() error = nil, want failure with fallback payload")
	}
	payload := decodeJSONOutput(t, &stdout)
	nodes := jsonArrayFromMap(t, payload, "nodes")
	node := jsonMapFromAny(t, nodes[0])
	if node["fallback"] != "devopsellence_agent_logs" {
		t.Fatalf("node payload = %#v, want agent-log fallback", node)
	}
	lines := jsonArrayFromMap(t, node, "fallback_lines")
	if len(lines) == 0 || !strings.Contains(stringValueAny(lines[0]), "agent captured failure") {
		t.Fatalf("fallback_lines = %#v, want captured failure", lines)
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

func TestNodeAttachPersistsAttachmentBeforeRepublishError(t *testing.T) {
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
	attached, err := loaded.AttachedNodeNames(workspaceRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(attached, []string{"node-a"}) {
		t.Fatalf("attached = %#v, want persisted attachment after failed republish", attached)
	}
}

func TestNodeLabelRemovePersistsLabelsBeforeRepublishError(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole, config.DefaultWorkerRole}},
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
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
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

	if err := app.SoloNodeLabelRemove(context.Background(), SoloNodeLabelRemoveOptions{Node: "node-a", Labels: config.DefaultWorkerRole}); err == nil {
		t.Fatal("expected label remove to fail")
	}

	loaded, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	labels := loaded.Nodes["node-a"].Labels
	if !reflect.DeepEqual(labels, []string{config.DefaultWebRole}) {
		t.Fatalf("labels = %#v, want persisted labels after failed republish", labels)
	}
}

func TestSoloSupportBundleWritesRedactedEvidence(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	web := cfg.Services[config.DefaultWebServiceName]
	web.Env = map[string]string{"INLINE_TOKEN": "inline-config-secret"}
	web.SecretRefs = []config.SecretRef{{Name: "CONFIG_SECRET", Secret: "op://config/item/field"}}
	cfg.Services[config.DefaultWebServiceName] = web
	cfg.Tasks.Release = &config.TaskConfig{Service: config.DefaultWebServiceName, Command: []string{"bin/migrate"}, Env: map[string]string{"RELEASE_TOKEN": "release-config-secret"}}
	cfg.Environments = map[string]config.EnvironmentOverlay{
		"staging": {
			Services: map[string]config.ServiceConfigOverlay{
				config.DefaultWebServiceName: {Env: map[string]string{"STAGING_TOKEN": "staging-config-secret"}},
			},
		},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, workspaceRoot)

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Port: 22, SSHKey: "-----BEGIN OPENSSH PRIVATE KEY-----private-key-material", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
		Snapshots:   map[string]desiredstate.DeploySnapshot{},
		Releases:    map[string]corerelease.Release{},
		Secrets:     map[string]solo.SecretRecord{},
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-a"); err != nil {
		t.Fatal(err)
	}
	stateKey, err := solo.EnvironmentStateKey(workspaceRoot, "production")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := desiredstate.DeploySnapshot{
		WorkspaceRoot: workspaceRoot,
		Environment:   "production",
		Revision:      "abc1234",
		Image:         "demo:abc1234",
		Services: []desiredstate.ServiceJSON{{
			Name:  config.DefaultWebServiceName,
			Image: "demo:abc1234",
			Env: map[string]string{
				"APP_REVISION":        "snapshot-inline-value",
				"SNAPSHOT_SECRET_ENV": "snapshot-secret-value",
			},
		}},
		ReleaseTask: &desiredstate.TaskJSON{
			Name:  "release",
			Image: "demo:abc1234",
			Env:   map[string]string{"RELEASE_INLINE": "snapshot-release-value"},
		},
	}
	current.Snapshots[stateKey] = snapshot
	current.Releases["rel-abc1234"] = corerelease.Release{
		ID:            "rel-abc1234",
		EnvironmentID: stateKey,
		Revision:      "abc1234",
		Snapshot:      snapshot,
		Image:         corerelease.ImageRef{Reference: "demo:abc1234"},
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := current.SetSecret(workspaceRoot, "production", config.DefaultWebServiceName, "DEMO_SECRET", solo.SecretMaterial{Store: solo.SecretStorePlaintext, Value: "super-secret-value"}); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SetSecret(workspaceRoot, "production", config.DefaultWebServiceName, "OP_SECRET", solo.SecretMaterial{Store: solo.SecretStoreOnePassword, Reference: "op://vault/item/field"}); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "bundle.json")
	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloSupportBundle(context.Background(), SoloSupportBundleOptions{Output: outPath}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{
		"super-secret-value",
		"op://vault/item/field",
		"inline-config-secret",
		"op://config/item/field",
		"release-config-secret",
		"staging-config-secret",
		"-----BEGIN OPENSSH PRIVATE KEY-----private-key-material",
		"snapshot-inline-value",
		"snapshot-secret-value",
		"snapshot-release-value",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("support bundle leaked %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, "[REDACTED]") || !strings.Contains(text, "devopsellence_solo_support_bundle") {
		t.Fatalf("support bundle missing redacted evidence: %s", text)
	}
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("bundle mode = %o, want 0600", got)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("parse command output: %v\n%s", err, stdout.String())
	}
	if got := result["action"]; got != "support_bundle" {
		t.Fatalf("action = %v, want support_bundle", got)
	}
}

func TestSoloSupportBundleUsesActiveEnvironmentForAttachedNodes(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_ENVIRONMENT", "staging")

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Environments = map[string]config.EnvironmentOverlay{"staging": {}}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, workspaceRoot)

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-prod":    {Host: "203.0.113.10", User: "root", Port: 22, Labels: []string{config.DefaultWebRole}},
			"node-staging": {Host: "203.0.113.11", User: "root", Port: 22, Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{},
	}
	if _, _, err := current.AttachNode(workspaceRoot, "production", "node-prod"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(workspaceRoot, "staging", "node-staging"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "bundle.json")
	app := &App{Printer: output.New(io.Discard, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloSupportBundle(context.Background(), SoloSupportBundleOptions{Output: outPath}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle map[string]any
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("parse support bundle: %v\n%s", err, string(data))
	}
	if got := bundle["environment"]; got != "staging" {
		t.Fatalf("environment = %v, want staging", got)
	}
	attached, ok := bundle["attached_nodes"].([]any)
	if !ok || len(attached) != 1 || attached[0] != "node-staging" {
		t.Fatalf("attached_nodes = %#v, want staging node only", bundle["attached_nodes"])
	}
}

func TestSoloSecretsCommandsUseCurrentSoloEnvironment(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_ENVIRONMENT", "staging")

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Environments = map[string]config.EnvironmentOverlay{
		"staging": {},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	if err := soloState.Write(solo.State{}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloSecretsSet(context.Background(), SoloSecretsSetOptions{Key: "DOGFOOD_SECRET", Value: "staging-secret", ServiceName: "web", Store: solo.SecretStorePlaintext}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if got := payload["environment"]; got != "staging" {
		t.Fatalf("set environment = %v, want staging", got)
	}

	current, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	productionSecrets, err := current.ListSecrets(workspaceRoot, "production", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(productionSecrets) != 0 {
		t.Fatalf("production secrets = %#v, want none", productionSecrets)
	}
	stagingSecrets, err := current.ListSecrets(workspaceRoot, "staging", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(stagingSecrets) != 1 || stagingSecrets[0].Name != "DOGFOOD_SECRET" || stagingSecrets[0].Store != solo.SecretStorePlaintext {
		t.Fatalf("staging secrets = %#v, want DOGFOOD_SECRET", stagingSecrets)
	}

	stdout.Reset()
	if err := app.SoloSecretsList(context.Background(), SoloSecretsListOptions{ServiceName: "web"}); err != nil {
		t.Fatal(err)
	}
	payload = decodeJSONOutput(t, &stdout)
	if got := payload["environment"]; got != "staging" {
		t.Fatalf("list environment = %v, want staging", got)
	}
	items, ok := payload["secrets"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("listed secrets = %#v, want one staging secret", payload["secrets"])
	}
	listed, ok := items[0].(map[string]any)
	if !ok || listed["name"] != "DOGFOOD_SECRET" || listed["stored"] != true {
		t.Fatalf("listed secret = %#v, want stored DOGFOOD_SECRET", items[0])
	}

	stdout.Reset()
	if err := app.SoloSecretsDelete(context.Background(), SoloSecretsDeleteOptions{Key: "DOGFOOD_SECRET", ServiceName: "web"}); err != nil {
		t.Fatal(err)
	}
	payload = decodeJSONOutput(t, &stdout)
	if got := payload["environment"]; got != "staging" {
		t.Fatalf("delete environment = %v, want staging", got)
	}
	current, err = soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	stagingSecrets, err = current.ListSecrets(workspaceRoot, "staging", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(stagingSecrets) != 0 {
		t.Fatalf("staging secrets after delete = %#v, want none", stagingSecrets)
	}
}

func TestSoloSecretsCommandsUpdateEnvironmentOverlaySecretRefs(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_ENVIRONMENT", "staging")

	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Environments = map[string]config.EnvironmentOverlay{
		"staging": {
			Services: map[string]config.ServiceConfigOverlay{
				"web": {
					SecretRefs: []config.SecretRef{{Name: "EXISTING_SECRET", Secret: "devopsellence://plaintext/EXISTING_SECRET"}},
				},
			},
		},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	if err := soloState.Write(solo.State{}); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloSecretsSet(context.Background(), SoloSecretsSetOptions{Key: "DOGFOOD_SECRET", Value: "staging-secret", ServiceName: "web", Store: solo.SecretStorePlaintext}); err != nil {
		t.Fatal(err)
	}

	updated, err := config.LoadFromRoot(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	stagingWeb := updated.Environments["staging"].Services["web"]
	if !secretRefsContain(stagingWeb.SecretRefs, "EXISTING_SECRET") {
		t.Fatalf("staging overlay secret refs = %#v, want existing ref preserved", stagingWeb.SecretRefs)
	}
	if !secretRefsContain(stagingWeb.SecretRefs, "DOGFOOD_SECRET") {
		t.Fatalf("staging overlay secret refs = %#v, want DOGFOOD_SECRET added", stagingWeb.SecretRefs)
	}
	if secretRefsContain(updated.Services["web"].SecretRefs, "DOGFOOD_SECRET") {
		t.Fatalf("top-level web secret refs = %#v, want env-overlay secret ref update only", updated.Services["web"].SecretRefs)
	}
	resolved, err := config.ResolveEnvironmentConfig(*updated, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if !secretRefsContain(resolved.Services["web"].SecretRefs, "DOGFOOD_SECRET") {
		t.Fatalf("resolved staging web secret refs = %#v, want DOGFOOD_SECRET available", resolved.Services["web"].SecretRefs)
	}

	stdout.Reset()
	if err := app.SoloSecretsDelete(context.Background(), SoloSecretsDeleteOptions{Key: "DOGFOOD_SECRET", ServiceName: "web"}); err != nil {
		t.Fatal(err)
	}
	updated, err = config.LoadFromRoot(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	stagingWeb = updated.Environments["staging"].Services["web"]
	if secretRefsContain(stagingWeb.SecretRefs, "DOGFOOD_SECRET") {
		t.Fatalf("staging overlay secret refs after delete = %#v, want DOGFOOD_SECRET removed", stagingWeb.SecretRefs)
	}
	if !secretRefsContain(stagingWeb.SecretRefs, "EXISTING_SECRET") {
		t.Fatalf("staging overlay secret refs after delete = %#v, want existing ref preserved", stagingWeb.SecretRefs)
	}
}

func secretRefsContain(refs []config.SecretRef, name string) bool {
	for _, ref := range refs {
		if ref.Name == name {
			return true
		}
	}
	return false
}

func TestSoloDeployDryRunPlansWithoutSideEffects(t *testing.T) {
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
	commitTestRepo(t, workspaceRoot)

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Port: 22, Labels: []string{config.DefaultWebRole}},
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
	before, err := os.ReadFile(soloState.Path)
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Git: git.Client{}, Cwd: workspaceRoot}
	if err := app.SoloDeploy(context.Background(), SoloDeployOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(soloState.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("solo state changed during dry-run")
	}
	events := decodeNDJSONOutput(t, &stdout)
	payload := events[len(events)-1]
	if payload["phase"] != "planned" || payload["action"] != "deploy" || payload["dry_run"] != true {
		t.Fatalf("payload = %#v, want deploy dry-run plan", payload)
	}
	if payload["release_id"] != nil || payload["deployment_id"] != nil || payload["desired_state_revisions"] != nil {
		t.Fatalf("payload = %#v, dry-run must not create release/deployment/publication revisions", payload)
	}
}

func TestSoloDeployDryRunUsesResolvedEnvironmentOverlay(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_ENVIRONMENT", "staging")
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"prod.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "prod.example.com", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
		}},
		TLS: config.IngressTLSConfig{Mode: "off"},
	}
	cfg.Environments = map[string]config.EnvironmentOverlay{
		"staging": {
			Ingress: &config.IngressConfigOverlay{
				Hosts: []string{"staging.example.com"},
				Rules: []config.IngressRuleConfig{{
					Match:  config.IngressMatchConfig{Host: "staging.example.com", PathPrefix: "/"},
					Target: config.IngressTargetConfig{Service: config.DefaultWebServiceName, Port: "http"},
				}},
			},
		},
	}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	commitTestRepo(t, workspaceRoot)

	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := solo.State{Nodes: map[string]config.Node{"node-a": {Host: "203.0.113.10", User: "root", Port: 22, Labels: []string{config.DefaultWebRole}}}}
	if _, _, err := current.AttachNode(workspaceRoot, "staging", "node-a"); err != nil {
		t.Fatal(err)
	}
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Git: git.Client{}, Cwd: workspaceRoot}
	if err := app.SoloDeploy(context.Background(), SoloDeployOptions{DryRun: true}); err != nil {
		t.Fatal(err)
	}
	events := decodeNDJSONOutput(t, &stdout)
	payload := events[len(events)-1]
	if payload["environment"] != "staging" {
		t.Fatalf("payload = %#v, want staging environment", payload)
	}
	urls := jsonArrayFromMap(t, payload, "configured_public_urls")
	if len(urls) != 1 || urls[0] != "http://staging.example.com/" {
		t.Fatalf("configured_public_urls = %#v, want staging host only", urls)
	}
}

func TestSoloDeployWaitsForSettledStatusBeforeSuccess(t *testing.T) {
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

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
	events := decodeNDJSONOutput(t, &stdout)
	payload := events[len(events)-1]
	if payload["event"] != "result" || payload["ok"] != true {
		t.Fatalf("result event = %#v, want successful result", payload)
	}
	if payload["environment"] != "production" || payload["workload_revision"] == "" || payload["phase"] != "settled" {
		t.Fatalf("payload = %#v, want settled deploy JSON", payload)
	}
	if payload["release_id"] == "" || payload["deployment_id"] == "" {
		t.Fatalf("payload = %#v, want release and deployment ids", payload)
	}
	revisions := jsonMapFromAny(t, payload["desired_state_revisions"])
	if revisions["node-a"] == "" {
		t.Fatalf("desired_state_revisions = %#v, want node revision", revisions)
	}
	updatedState, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(updatedState.Releases) != 1 || len(updatedState.Deployments) != 1 {
		t.Fatalf("state releases=%#v deployments=%#v, want one release and deployment", updatedState.Releases, updatedState.Deployments)
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

func TestSoloReleaseListReturnsCurrentReleaseHistory(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := soloReleaseWorkflowState(workspaceRoot)
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
	if err := app.SoloReleaseList(context.Background(), SoloReleaseListOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if environmentID, ok := payload["environment_id"].(string); !ok || strings.Contains(environmentID, "\n") || !strings.Contains(environmentID, "#production") {
		t.Fatalf("environment_id = %#v, want newline-free workspace/environment identifier", payload["environment_id"])
	}
	if payload["current_release_id"] != "rel-2" {
		t.Fatalf("payload = %#v, want current release rel-2", payload)
	}
	releases := jsonArrayFromMap(t, payload, "releases")
	if len(releases) != 1 {
		t.Fatalf("releases = %#v, want limit 1", releases)
	}
	release := jsonMapFromAny(t, releases[0])
	if release["id"] != "rel-2" || release["revision"] != "bbb2222" || release["current"] != true {
		t.Fatalf("release = %#v, want current rel-2", release)
	}
	targets := jsonArrayFromMap(t, release, "target_nodes")
	if !reflect.DeepEqual(targets, []any{"node-a", "node-c"}) {
		t.Fatalf("target_nodes = %#v, want node-a/node-c", targets)
	}

	stdout.Reset()
	if err := app.SoloReleaseList(context.Background(), SoloReleaseListOptions{Limit: 0}); err != nil {
		t.Fatal(err)
	}
	payload = decodeJSONOutput(t, &stdout)
	releases = jsonArrayFromMap(t, payload, "releases")
	if len(releases) != 2 {
		t.Fatalf("releases = %#v, want unlimited result", releases)
	}
}

func TestSoloReleaseListUsesCurrentSoloEnvironment(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_ENVIRONMENT", "staging")
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Environments = map[string]config.EnvironmentOverlay{"staging": {}}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := soloReleaseWorkflowStateForEnvironment(workspaceRoot, "staging")
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloReleaseList(context.Background(), SoloReleaseListOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["environment"] != "staging" {
		t.Fatalf("environment = %#v, want staging", payload["environment"])
	}
	if environmentID, ok := payload["environment_id"].(string); !ok || !strings.Contains(environmentID, "#staging") {
		t.Fatalf("environment_id = %#v, want staging identifier", payload["environment_id"])
	}
	if payload["current_release_id"] != "rel-2" {
		t.Fatalf("current_release_id = %#v, want staging current release rel-2", payload["current_release_id"])
	}
}

func TestSoloReleaseListEnvironmentIDUsesCanonicalWorkspaceKey(t *testing.T) {
	realWorkspace := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(realWorkspace, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(realWorkspace, cfg); err != nil {
		t.Fatal(err)
	}
	linkWorkspace := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realWorkspace, linkWorkspace); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := soloReleaseWorkflowState(realWorkspace)
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{
		Printer:     output.New(&stdout, io.Discard),
		SoloState:   soloState,
		ConfigStore: config.NewStore(),
		Cwd:         linkWorkspace,
	}
	if err := app.SoloReleaseList(context.Background(), SoloReleaseListOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}
	payload := decodeJSONOutput(t, &stdout)
	want := realWorkspace + "#production"
	if payload["environment_id"] != want {
		t.Fatalf("environment_id = %#v, want canonical %q", payload["environment_id"], want)
	}
}

func TestSoloReleaseRollbackDryRunPlansWithoutSideEffects(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := soloReleaseWorkflowState(workspaceRoot)
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(soloState.Path)
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloReleaseRollback(context.Background(), SoloReleaseRollbackOptions{Selector: "aaa1111", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(soloState.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("solo state changed during rollback dry-run")
	}
	events := decodeNDJSONOutput(t, &stdout)
	payload := events[len(events)-1]
	if payload["phase"] != "planned" || payload["action"] != "rollback" || payload["dry_run"] != true {
		t.Fatalf("payload = %#v, want rollback dry-run plan", payload)
	}
	if payload["release_id"] != "rel-1" || payload["rolled_back_from"] != "rel-2" {
		t.Fatalf("payload = %#v, want selected rollback release", payload)
	}
	if payload["deployment_id"] != nil || payload["desired_state_revisions"] != nil {
		t.Fatalf("payload = %#v, dry-run must not create deployment/publication revisions", payload)
	}
}

func TestSoloReleaseRollbackDryRunUsesCurrentSoloEnvironment(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_ENVIRONMENT", "staging")
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Environments = map[string]config.EnvironmentOverlay{"staging": {}}
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := soloReleaseWorkflowStateForEnvironment(workspaceRoot, "staging")
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(soloState.Path)
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Cwd: workspaceRoot}
	if err := app.SoloReleaseRollback(context.Background(), SoloReleaseRollbackOptions{Selector: "aaa1111", DryRun: true}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(soloState.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("solo state changed during staging rollback dry-run")
	}
	events := decodeNDJSONOutput(t, &stdout)
	payload := events[len(events)-1]
	if payload["environment"] != "staging" || payload["release_id"] != "rel-1" || payload["rolled_back_from"] != "rel-2" {
		t.Fatalf("payload = %#v, want staging rollback from rel-2 to rel-1", payload)
	}
}

func TestSoloReleaseRollbackUsesSelectedReleaseTargets(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := soloReleaseWorkflowState(workspaceRoot)
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, []fakeSSHResponse{
		{stdout: soloStatusMissingSentinel + "\n"},
		{stdout: `{"revision":"__REVISION__","phase":"reconciling"}` + "\n"},
		{stdout: `{"revision":"__REVISION__","phase":"settled"}` + "\n"},
	})

	var stdout bytes.Buffer
	app := &App{
		Printer:            output.New(&stdout, io.Discard),
		SoloState:          soloState,
		ConfigStore:        config.NewStore(),
		Cwd:                workspaceRoot,
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      time.Second,
	}
	if err := app.SoloReleaseRollback(context.Background(), SoloReleaseRollbackOptions{Selector: "aaa1111"}); err != nil {
		t.Fatal(err)
	}
	events := decodeNDJSONOutput(t, &stdout)
	payload := events[len(events)-1]
	if payload["release_id"] != "rel-1" || payload["rolled_back_from"] != "rel-2" || payload["phase"] != "settled" {
		t.Fatalf("payload = %#v, want settled rollback to rel-1", payload)
	}
	nodes := jsonArrayFromMap(t, payload, "nodes")
	if !reflect.DeepEqual(nodes, []any{"node-a"}) {
		t.Fatalf("nodes = %#v, want only selected release target", nodes)
	}
	revisions := jsonMapFromAny(t, payload["desired_state_revisions"])
	if len(revisions) != 1 || revisions["node-a"] == "" {
		t.Fatalf("desired_state_revisions = %#v, want only node-a", revisions)
	}
	updatedState, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	key := workspaceRoot + "\nproduction"
	if updatedState.Current[key] != "rel-1" {
		t.Fatalf("current release = %q, want rel-1", updatedState.Current[key])
	}
	var deployment corerelease.Deployment
	for _, candidate := range updatedState.Deployments {
		deployment = candidate
	}
	if deployment.Status != corerelease.DeploymentStatusSettled || !reflect.DeepEqual(deployment.TargetNodeIDs, []string{"node-a"}) {
		t.Fatalf("deployment = %#v, want settled target node-a", deployment)
	}
}

func TestSoloReleaseRollbackRepublishFailureDoesNotSwitchCurrentRelease(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := soloReleaseWorkflowState(workspaceRoot)
	oldRelease := current.Releases["rel-1"]
	oldRelease.Snapshot.Ingress = &desiredstate.IngressJSON{
		TLS:   desiredstate.IngressTLSJSON{Mode: "auto"},
		Hosts: []string{"app.example.com"},
		Routes: []desiredstate.IngressRouteJSON{
			{Match: desiredstate.IngressMatchJSON{Hostname: "app.example.com", PathPrefix: "/"}, Target: desiredstate.IngressTargetJSON{Environment: "production", Service: "web", Port: "http"}},
			{Match: desiredstate.IngressMatchJSON{Hostname: "app.example.com", PathPrefix: "/"}, Target: desiredstate.IngressTargetJSON{Environment: "production", Service: "web", Port: "metrics"}},
		},
	}
	current.Releases["rel-1"] = oldRelease
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	app := &App{Printer: output.New(&stdout, io.Discard), SoloState: soloState, ConfigStore: config.NewStore(), Git: git.Client{}, Cwd: workspaceRoot}
	err := app.SoloReleaseRollback(context.Background(), SoloReleaseRollbackOptions{Selector: "aaa1111"})
	if err == nil {
		t.Fatal("expected republish failure")
	}
	updatedState, readErr := soloState.Read()
	if readErr != nil {
		t.Fatal(readErr)
	}
	key := workspaceRoot + "\nproduction"
	if updatedState.Current[key] != "rel-2" {
		t.Fatalf("current release = %q, want rel-2 preserved after republish failure", updatedState.Current[key])
	}
	if updatedState.Snapshots[key].Revision != "bbb2222" {
		t.Fatalf("snapshot revision = %q, want current snapshot preserved", updatedState.Snapshots[key].Revision)
	}
}

func TestSoloReleaseRollbackPersistsSelectedReleaseOnRolloutFailure(t *testing.T) {
	workspaceRoot := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	if _, err := config.Write(workspaceRoot, cfg); err != nil {
		t.Fatal(err)
	}
	soloState := solo.NewStateStore(filepath.Join(t.TempDir(), "solo-state.json"))
	current := soloReleaseWorkflowState(workspaceRoot)
	if err := soloState.Write(current); err != nil {
		t.Fatal(err)
	}
	installFakeSoloCommands(t, []fakeSSHResponse{
		{stdout: `{"revision":"__REVISION__","phase":"error","error":"healthcheck failed"}` + "\n"},
	})

	var stdout bytes.Buffer
	app := &App{
		Printer:            output.New(&stdout, io.Discard),
		SoloState:          soloState,
		ConfigStore:        config.NewStore(),
		Cwd:                workspaceRoot,
		DeployPollInterval: 5 * time.Millisecond,
		DeployTimeout:      time.Second,
	}
	err := app.SoloReleaseRollback(context.Background(), SoloReleaseRollbackOptions{Selector: "aaa1111"})
	if err == nil {
		t.Fatal("expected rollout failure")
	}
	updatedState, readErr := soloState.Read()
	if readErr != nil {
		t.Fatal(readErr)
	}
	key := workspaceRoot + "\nproduction"
	if updatedState.Current[key] != "rel-1" {
		t.Fatalf("current release = %q, want selected rollback release rel-1", updatedState.Current[key])
	}
	var deployment corerelease.Deployment
	for _, candidate := range updatedState.Deployments {
		deployment = candidate
	}
	if deployment.Status != corerelease.DeploymentStatusFailed || !strings.Contains(deployment.StatusMessage, "rollout failed") {
		t.Fatalf("deployment = %#v, want failed rollout deployment", deployment)
	}
}

func TestSoloRollbackTargetNodeNamesRejectsEmptyIntersection(t *testing.T) {
	_, err := soloRollbackTargetNodeNames([]string{"node-b"}, corerelease.Release{
		Revision:      "aaa1111",
		TargetNodeIDs: []string{"node-a"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not target any currently attached nodes") {
		t.Fatalf("error = %v, want empty intersection error", err)
	}
}

func TestSoloRollbackTargetNodeNamesDefaultsEmptyTargetsToAttachedNodes(t *testing.T) {
	targets, err := soloRollbackTargetNodeNames([]string{" node-b ", "", "node-a", "node-b"}, corerelease.Release{
		Revision: "aaa1111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(targets, []string{"node-b", "node-a"}) {
		t.Fatalf("targets = %#v, want normalized attached nodes", targets)
	}
}

func soloReleaseWorkflowState(workspaceRoot string) solo.State {
	return soloReleaseWorkflowStateForEnvironment(workspaceRoot, "production")
}

func soloReleaseWorkflowStateForEnvironment(workspaceRoot, environment string) solo.State {
	key := workspaceRoot + "\n" + environment
	oldSnapshot := desiredstate.DeploySnapshot{
		WorkspaceRoot: workspaceRoot,
		WorkspaceKey:  workspaceRoot,
		Environment:   environment,
		Revision:      "aaa1111",
		Image:         "demo:aaa1111",
		Services:      []desiredstate.ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "demo:aaa1111"}},
	}
	currentSnapshot := desiredstate.DeploySnapshot{
		WorkspaceRoot: workspaceRoot,
		WorkspaceKey:  workspaceRoot,
		Environment:   environment,
		Revision:      "bbb2222",
		Image:         "demo:bbb2222",
		Services:      []desiredstate.ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "demo:bbb2222"}},
	}
	return solo.State{
		Nodes: map[string]config.Node{
			"node-a": {Host: "203.0.113.10", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence", Labels: []string{config.DefaultWebRole}},
			"node-b": {Host: "203.0.113.11", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence", Labels: []string{config.DefaultWebRole}},
			"node-c": {Host: "203.0.113.12", User: "root", Port: 22, AgentStateDir: "/var/lib/devopsellence", Labels: []string{config.DefaultWebRole}},
		},
		Attachments: map[string]solo.AttachmentRecord{
			key: {
				WorkspaceRoot: workspaceRoot,
				WorkspaceKey:  workspaceRoot,
				Environment:   environment,
				NodeNames:     []string{"node-a", "node-b", "node-c"},
			},
		},
		Snapshots: map[string]desiredstate.DeploySnapshot{key: currentSnapshot},
		Releases: map[string]corerelease.Release{
			"rel-1": {
				ID:            "rel-1",
				EnvironmentID: key,
				Revision:      "aaa1111",
				Snapshot:      oldSnapshot,
				Image:         corerelease.ImageRef{Reference: "demo:aaa1111"},
				TargetNodeIDs: []string{"node-a"},
				CreatedAt:     "2026-04-28T12:01:00Z",
			},
			"rel-2": {
				ID:            "rel-2",
				EnvironmentID: key,
				Revision:      "bbb2222",
				Snapshot:      currentSnapshot,
				Image:         corerelease.ImageRef{Reference: "demo:bbb2222"},
				TargetNodeIDs: []string{"node-a", "node-c"},
				CreatedAt:     "2026-04-28T12:02:00Z",
			},
		},
		Current:     map[string]string{key: "rel-2"},
		Deployments: map[string]corerelease.Deployment{},
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
	installFakeSoloCommands(t, []fakeSSHResponse{
		{stdout: soloStatusMissingSentinel + "\n"},
		{stdout: `{"revision":"__REVISION__","phase":"error","error":"healthcheck failed"}` + "\n"},
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
	loaded, err := soloState.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Deployments) != 1 {
		t.Fatalf("deployments = %#v, want one failed deployment", loaded.Deployments)
	}
	var deployment corerelease.Deployment
	for _, candidate := range loaded.Deployments {
		deployment = candidate
	}
	if deployment.Status != corerelease.DeploymentStatusFailed || !strings.HasPrefix(deployment.StatusMessage, "rollout failed:") {
		t.Fatalf("deployment = %#v, want rollout failure status", deployment)
	}
	if deployment.PublicationResult == nil || deployment.PublicationResult.Status != corerelease.PublicationStatusWritten || deployment.PublicationResult.ErrorMessage != "" {
		t.Fatalf("publication result = %#v, want written result without rollout error", deployment.PublicationResult)
	}
	if len(deployment.PublicationResult.NodeResults) != 1 || deployment.PublicationResult.NodeResults[0].PublishedAt != "" {
		t.Fatalf("node results = %#v, want no synthetic published_at", deployment.PublicationResult.NodeResults)
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

func TestWaitForSoloRolloutIgnoresExpectedRevisionStatusUntilRemoteStatusTimeAdvances(t *testing.T) {
	statusCountPath := installFakeSoloCommands(t, []fakeSSHResponse{
		{stdout: `{"time":"2026-04-29T08:28:51Z","revision":"expected-revision","phase":"settled"}` + "\n"},
		{stdout: `{"time":"2026-04-29T08:29:40Z","revision":"expected-revision","phase":"settled"}` + "\n"},
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
	}, map[string]string{"node-a": "2026-04-29T08:28:51Z"})
	if err != nil {
		t.Fatal(err)
	}
	if got := readFakeSSHStatusCount(t, statusCountPath); got != 2 {
		t.Fatalf("status poll count = %d, want stale same-revision status ignored before fresh status", got)
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
	if !strings.Contains(nextSteps, "devopsellence node list --all # solo node names are global on this machine") {
		t.Fatalf("next_steps = %q, want node-list collision guidance", nextSteps)
	}
	if !strings.Contains(nextSteps, "devopsellence node create <node-name> --provider hetzner --install --attach") {
		t.Fatalf("next_steps = %q, want provider-created node path", nextSteps)
	}
	runtimeContract := jsonMapFromAny(t, payload["runtime_contract"])
	if runtimeContract["service"] != "web" || runtimeContract["port"] != float64(3000) || runtimeContract["port_source"] != "default" {
		t.Fatalf("runtime_contract = %#v, want default web port contract", runtimeContract)
	}
	if runtimeContract["web_service"] != true {
		t.Fatalf("runtime_contract.web_service = %#v, want true", runtimeContract["web_service"])
	}
	if runtimeContract["healthcheck_path"] != config.DefaultHealthcheckPath || runtimeContract["healthcheck_port"] != float64(3000) {
		t.Fatalf("runtime_contract healthcheck = %#v, want %s on port 3000", runtimeContract, config.DefaultHealthcheckPath)
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
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
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
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
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
		InferredWebPort: 8080,
	})
	if cfg.Organization != "solo" || cfg.Project != "ShopApp" {
		t.Fatalf("config identity = org %q project %q", cfg.Organization, cfg.Project)
	}
	web := cfg.Services[config.DefaultWebServiceName]
	if web.HTTPPort(0) != 8080 || web.Healthcheck.Port != 8080 {
		t.Fatalf("web port = %d healthcheck port = %d, want 8080", web.HTTPPort(0), web.Healthcheck.Port)
	}
}

func TestIngressSetInfersPrimaryWebService(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := config.DefaultProjectConfig("solo", "demo", config.DefaultEnvironment)
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

if [[ "$command" == "umask 077; cat > "* || "$command" == cat\ \>* ]]; then
  if [[ "$command" == "umask 077; cat > "* ]]; then
    target="${command#umask 077; cat > }"
  else
    target="${command#cat > }"
  fi
  target="${target#\'}"
  target="${target%\'}"
  cat >"$target"
  if [[ -n "${DEVOPSELLENCE_FAKE_SSH_UPLOADS:-}" ]]; then
    printf '%s\n' "$command" >>"$DEVOPSELLENCE_FAKE_SSH_UPLOADS"
  fi
  exit 0
fi

if [[ "$command" == "bash -s" ]]; then
  script="$(cat)"
  if [[ -n "${DEVOPSELLENCE_FAKE_SSH_SCRIPT:-}" ]]; then
    printf '%s' "$script" >"$DEVOPSELLENCE_FAKE_SSH_SCRIPT"
  fi
  if [[ -n "${DEVOPSELLENCE_FAKE_SSH_RESTART_FAIL:-}" && "$script" == *"systemctl restart devopsellence-agent"* && "$script" != *"systemctl restart devopsellence-agent || true"* ]]; then
    printf 'systemctl restart devopsellence-agent failed\n' >&2
    exit 1
  fi
  exit 0
fi

if [[ "$command" == rm\ -f\ * ]]; then
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

exec_marker=""
if [[ "$command" =~ (__DEVOPSELLENCE_EXEC_EXIT_CODE__[0-9a-f]+__) ]]; then
  exec_marker="${BASH_REMATCH[1]}"
fi

if [[ -n "$exec_marker" && "$command" == *"svc-production-web-abc"* ]]; then
  printf 'service stdout\n'
  printf 'service stderr\n%s0\n' "$exec_marker" >&2
  exit 0
fi

if [[ -n "$exec_marker" && "$command" == *"'uptime'"* ]]; then
  printf 'node stdout\n'
  printf '%s0\n' "$exec_marker" >&2
  exit 0
fi

if [[ -n "$exec_marker" && "$command" == *"'missing-command'"* ]]; then
  printf 'missing-command: command not found\n' >&2
  printf '%s127\n' "$exec_marker" >&2
  exit 0
fi

if [[ "$command" == *" logs --tail "* && -n "${DEVOPSELLENCE_FAKE_SSH_WORKLOAD_LOGS_EMPTY:-}" ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__1\n__DEVOPSELLENCE_STDOUT__\n\n__DEVOPSELLENCE_STDERR__\n__DEVOPSELLENCE_NO_WORKLOAD_CONTAINERS__\nNo workload containers found for service web in environment production\n'
  exit 0
fi

if [[ "$command" == *" logs --tail "* ]]; then
  if [[ -n "${DEVOPSELLENCE_FAKE_SSH_WORKLOAD_LOGS_COMMAND:-}" ]]; then
    printf '%s' "$command" >"$DEVOPSELLENCE_FAKE_SSH_WORKLOAD_LOGS_COMMAND"
  fi
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\n==> svc-production-web <==\napp line one\napp line two\n__DEVOPSELLENCE_STDERR__\n'
  exit 0
fi

if [[ "$command" == *"__DEVOPSELLENCE_EXIT_CODE__"* && "$command" == *"journalctl"* ]]; then
  printf '__DEVOPSELLENCE_EXIT_CODE__0\n__DEVOPSELLENCE_STDOUT__\nagent captured failure\n__DEVOPSELLENCE_STDERR__\n'
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
	cfg := config.DefaultProjectConfig("solo", "demo", config.DefaultEnvironment)
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

package workflow

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/discovery"
)

func TestDirectImageTagSlugifiesProjectName(t *testing.T) {
	got := directImageTag("ShopApp", "abc1234")
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

func TestValidateDirectNodeScheduleSelectsReleaseNode(t *testing.T) {
	cfg := &config.ProjectConfig{
		Web: config.ServiceConfig{Port: 3000},
		Worker: &config.ServiceConfig{
			Command: "sidekiq",
		},
		ReleaseCommand: "rails db:migrate",
	}
	nodes := map[string]config.DirectNode{
		"worker-a": {Labels: []string{config.DirectLabelWorker}},
		"web-a":    {Labels: []string{config.DirectLabelWeb}},
		"web-b":    {Labels: []string{config.DirectLabelWeb}},
	}
	got, err := validateDirectNodeSchedule(cfg, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if got != "web-a" {
		t.Fatalf("release node = %q, want web-a", got)
	}
}

func TestValidateDirectNodeScheduleRejectsMissingWorker(t *testing.T) {
	cfg := &config.ProjectConfig{
		Web:    config.ServiceConfig{Port: 3000},
		Worker: &config.ServiceConfig{Command: "sidekiq"},
	}
	_, err := validateDirectNodeSchedule(cfg, map[string]config.DirectNode{
		"web-a": {Labels: []string{config.DirectLabelWeb}},
	})
	if err == nil || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("expected missing worker error, got %v", err)
	}
}

func TestDirectNodeCanRunLegacyUnlabeledNode(t *testing.T) {
	node := config.DirectNode{}
	if !directNodeCanRun(node, config.DirectLabelWeb) || !directNodeCanRun(node, config.DirectLabelWorker) {
		t.Fatal("legacy unlabeled node should run all labels")
	}
}

func TestParseDirectLabels(t *testing.T) {
	got, err := parseDirectLabels("web,worker web")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{config.DirectLabelWeb, config.DirectLabelWorker}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("labels = %#v, want %#v", got, want)
	}
}

func TestDirectAgentInstallScriptConfiguresDirectMode(t *testing.T) {
	script := directAgentInstallScript(directAgentInstallScriptOptions{BaseURL: "https://example.test"})
	for _, want := range []string{
		"--mode=direct",
		"--auth-state-path=/var/lib/devopsellence/auth.json",
		"--desired-state-override-path=/var/lib/devopsellence/desired-state-override.json",
		"AGENT_BIN=/usr/local/bin/devopsellence-agent",
		"BASE_URL='https://example.test'",
		"$BASE_URL/agent/download",
		"$BASE_URL/agent/checksums",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install script missing %q", want)
		}
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

func TestApplyDirectRailsMasterKeyUsesConfigMasterKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "master.key"), []byte("from-master-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.ProjectConfig{
		App: config.AppConfig{Type: config.AppTypeRails},
		Web: config.ServiceConfig{Env: map[string]string{}},
		Worker: &config.ServiceConfig{
			Env: map[string]string{},
		},
	}
	secrets := map[string]string{}
	notice, err := applyDirectRailsMasterKey(dir, cfg, secrets)
	if err != nil {
		t.Fatal(err)
	}
	if secrets[railsMasterKeySecretName] != "from-master-key" {
		t.Fatalf("RAILS_MASTER_KEY = %q", secrets[railsMasterKeySecretName])
	}
	if !strings.Contains(notice, "config/master.key") {
		t.Fatalf("notice = %q, want config/master.key", notice)
	}
	for _, refs := range [][]config.SecretRef{cfg.Web.SecretRefs, cfg.Worker.SecretRefs} {
		if len(refs) != 1 || refs[0].Name != railsMasterKeySecretName {
			t.Fatalf("secret refs = %#v, want RAILS_MASTER_KEY", refs)
		}
	}
}

func TestApplyDirectRailsMasterKeyLetsEnvOverrideMasterKey(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "master.key"), []byte("from-master-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.ProjectConfig{
		App: config.AppConfig{Type: config.AppTypeRails},
		Web: config.ServiceConfig{Env: map[string]string{}},
	}
	secrets := map[string]string{railsMasterKeySecretName: "from-env"}
	notice, err := applyDirectRailsMasterKey(dir, cfg, secrets)
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

func TestDefaultDirectSSHPublicKeyCandidates(t *testing.T) {
	got := defaultDirectSSHPublicKeyCandidates()
	if len(got) == 0 {
		t.Fatal("expected default public key candidates")
	}
	for _, candidate := range got {
		if !strings.HasSuffix(candidate, ".pub") {
			t.Fatalf("public key candidate = %q, want .pub suffix", candidate)
		}
	}
}

func TestDirectDefaultProjectConfigUsesDiscovery(t *testing.T) {
	cfg := directDefaultProjectConfig(discovery.Result{
		ProjectName:     "ShopApp",
		AppType:         config.AppTypeGeneric,
		InferredWebPort: 8080,
	})
	if cfg.Organization != "direct" || cfg.Project != "ShopApp" {
		t.Fatalf("config identity = org %q project %q", cfg.Organization, cfg.Project)
	}
	if cfg.App.Type != config.AppTypeGeneric {
		t.Fatalf("app.type = %q", cfg.App.Type)
	}
	if cfg.Web.Port != 8080 || cfg.Web.Healthcheck.Port != 8080 {
		t.Fatalf("web port = %d healthcheck port = %d, want 8080", cfg.Web.Port, cfg.Web.Healthcheck.Port)
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

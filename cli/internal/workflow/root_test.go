package workflow

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/solo"
)

func TestRootVersionCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if stdout.Len() == 0 {
		t.Fatal("version command wrote no output")
	}
}

func TestRootVersionCommandJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"--json", "version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("version --json output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if payload["schema_version"] != float64(outputSchemaVersion) {
		t.Fatalf("schema_version = %v, want %d", payload["schema_version"], outputSchemaVersion)
	}
	if strings.TrimSpace(payload["version"].(string)) == "" {
		t.Fatalf("version = %v, want non-empty string", payload["version"])
	}
}

func TestRootModeFlagIsNotGlobal(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"--mode", "solo", "version"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag: --mode") {
		t.Fatalf("error = %v, want unknown flag: --mode", err)
	}
}

func TestSetupModeFlagPersistsWorkspaceMode(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"setup", "--mode", "solo"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want solo setup to require interactive terminal")
	}
	if !strings.Contains(err.Error(), "solo setup requires an interactive terminal") {
		t.Fatalf("error = %v, want solo setup path", err)
	}

	app := NewApp(bytes.NewBuffer(nil), &stdout, &stdout, false, cwd)
	mode, ok, modeErr := app.savedMode()
	if modeErr != nil {
		t.Fatalf("savedMode error = %v", modeErr)
	}
	if !ok || mode != ModeSolo {
		t.Fatalf("saved mode = %q, %v; want solo, true", mode, ok)
	}
}

func TestRootSecretSetRejectsExplicitEmptyValue(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestWorkspaceWithMode(t, ModeShared)
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"secret", "set", "SECRET_KEY_BASE", "--service", "web", "--value", ""})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "secret value is required") {
		t.Fatalf("error = %v, want secret value is required", err)
	}
}

func TestRootSoloSecretSetHonorsEnvironmentAndService(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestSoloWorkspace(t)
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"secret", "set", "DATABASE_URL", "--env", "staging", "--service", "web", "--value", "postgres://staging"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	current, err := solo.NewStateStore(solo.DefaultStatePath()).Read()
	if err != nil {
		t.Fatal(err)
	}
	values, err := current.ScopedSecretValues(cwd, "staging")
	if err != nil {
		t.Fatal(err)
	}
	if got := values.Value("web", "DATABASE_URL"); got != "postgres://staging" {
		t.Fatalf("web DATABASE_URL = %q", got)
	}
	if got := values.Value("worker", "DATABASE_URL"); got != "" {
		t.Fatalf("worker DATABASE_URL = %q", got)
	}
	cfg, err := config.LoadFromRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	refs := cfg.Services["web"].SecretRefs
	if len(refs) != 1 || refs[0].Name != "DATABASE_URL" || refs[0].Secret != "devopsellence://plaintext/DATABASE_URL" {
		t.Fatalf("secret refs = %#v", refs)
	}
}

func TestRootSoloSecretSetAcceptsOnePasswordReference(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestSoloWorkspace(t)
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"secret", "set", "DATABASE_URL", "--env", "staging", "--service", "web", "--store", "1password", "--op-ref", "op://app/db/password"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	current, err := solo.NewStateStore(solo.DefaultStatePath()).Read()
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := current.ListSecrets(cwd, "staging", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 1 {
		t.Fatalf("secrets = %#v", secrets)
	}
	if secrets[0].Store != solo.SecretStoreOnePassword || secrets[0].Reference != "op://app/db/password" {
		t.Fatalf("secret = %#v", secrets[0])
	}
	cfg, err := config.LoadFromRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	refs := cfg.Services["web"].SecretRefs
	if len(refs) != 1 || refs[0].Name != "DATABASE_URL" || refs[0].Secret != "op://app/db/password" {
		t.Fatalf("secret refs = %#v", refs)
	}
}

func TestRootHelpShowsModeFirstFlows(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	text := stdout.String()
	for _, snippet := range []string{
		"devopsellence mode use solo",
		"devopsellence setup",
		"devopsellence deploy",
		"context",
		"mode",
		"node",
		"provider",
		"secret",
	} {
		if !strings.Contains(text, snippet) {
			t.Fatalf("help output missing %q: %q", snippet, text)
		}
	}
	for _, hidden := range []string{
		"init",
		"direct",
		"project     ",
		"org         ",
		"env         ",
		"server",
	} {
		if strings.Contains(text, hidden) {
			t.Fatalf("help output unexpectedly showed legacy command %q: %q", hidden, text)
		}
	}
}

func TestNodeRegisterHelpSignalsTrialPolicy(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "register", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "paid orgs only") {
		t.Fatalf("help output = %q, want paid orgs only hint", stdout.String())
	}
	if !strings.Contains(stdout.String(), "signs in if needed") {
		t.Fatalf("help output = %q, want auto sign-in hint", stdout.String())
	}
	if !strings.Contains(stdout.String(), "initializes the current app if needed") {
		t.Fatalf("help output = %q, want auto init hint", stdout.String())
	}
}

func TestNodeCreateRunsInSharedMode(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestWorkspaceWithMode(t, ModeShared)
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "create", "prod-1", "--deploy"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want shared node create to run")
	}
	if !strings.Contains(err.Error(), "only available in solo mode") {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(err.Error(), "not available in shared mode") {
		t.Fatalf("Execute() still used old shared-mode guard: %v", err)
	}
}

func rootTestWorkspaceWithMode(t *testing.T, mode Mode) string {
	t.Helper()
	cwd := t.TempDir()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"mode", "use", string(mode)})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("mode use error = %v", err)
	}
	return cwd
}

func rootTestSoloWorkspace(t *testing.T) string {
	t.Helper()
	cwd := rootTestWorkspaceWithMode(t, ModeSolo)
	if err := os.WriteFile(filepath.Join(cwd, "devopsellence.yml"), []byte(strings.Join([]string{
		"schema_version: 6",
		"organization: solo",
		"project: demo",
		"default_environment: production",
		"build:",
		"  context: .",
		"  dockerfile: Dockerfile",
		"services:",
		"  web:",
		"    ports:",
		"      - name: http",
		"        port: 3000",
		"    healthcheck:",
		"      path: /up",
		"      port: 3000",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return cwd
}

func TestNodeHelpShowsSharedAndSoloActions(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, snippet := range []string{"register", "create", "attach", "detach", "remove", "logs"} {
		if !strings.Contains(stdout.String(), snippet) {
			t.Fatalf("help output = %q, want %q command", stdout.String(), snippet)
		}
	}
}

func TestNodeCreateHelpUsesCurrentHetznerDefaults(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "create", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, snippet := range []string{`default "` + defaultHetznerRegion + `"`, `default "` + defaultHetznerSize + `"`} {
		if !strings.Contains(stdout.String(), snippet) {
			t.Fatalf("help output = %q, want %q", stdout.String(), snippet)
		}
	}
}

func TestIngressSetHelpShowsServiceFlag(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"ingress", "set", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, snippet := range []string{"--service string", "Ingress service name"} {
		if !strings.Contains(stdout.String(), snippet) {
			t.Fatalf("help output = %q, want %q", stdout.String(), snippet)
		}
	}
}

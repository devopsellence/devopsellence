package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/solo"
	cliversion "github.com/devopsellence/cli/internal/version"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
)

func TestRootVersionCommand(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		args := args
		t.Run(strings.Join(args, " "), func(t *testing.T) {

			var stdout bytes.Buffer
			cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
			cmd.SetOut(&stdout)
			cmd.SetErr(&stdout)
			cmd.SetArgs(args)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			payload := decodeJSONOutput(t, &stdout)
			if payload["schema_version"] != float64(outputSchemaVersion) {
				t.Fatalf("schema_version = %v, want %d", payload["schema_version"], outputSchemaVersion)
			}
			if strings.TrimSpace(payload["version"].(string)) == "" {
				t.Fatalf("version = %v, want non-empty string", payload["version"])
			}
		})
	}
}

func TestRootVersionCommandIncludesReleaseProvenanceFields(t *testing.T) {
	oldVersion, oldCommit, oldDate := cliversion.Version, cliversion.Commit, cliversion.Date
	t.Cleanup(func() {
		cliversion.Version = oldVersion
		cliversion.Commit = oldCommit
		cliversion.Date = oldDate
	})
	cliversion.Version = "v0.2.0-preview"
	cliversion.Commit = "edbbd8e9688c"
	cliversion.Date = "2026-04-29T19:38:29Z"

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["version_number"] != "v0.2.0-preview" || payload["commit"] != "edbbd8e9688c" || payload["built_at"] != "2026-04-29T19:38:29Z" {
		t.Fatalf("payload = %#v, want split version provenance fields", payload)
	}
	if payload["release_url"] != "https://github.com/devopsellence/devopsellence/releases/tag/v0.2.0-preview" {
		t.Fatalf("release_url = %#v, want GitHub release tag URL", payload["release_url"])
	}
	if payload["checksums_url"] != "https://github.com/devopsellence/devopsellence/releases/download/v0.2.0-preview/cli-SHA256SUMS" {
		t.Fatalf("checksums_url = %#v, want CLI checksums asset URL", payload["checksums_url"])
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

func TestInitModeFlagPersistsWorkspaceModeAndWritesConfig(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := t.TempDir()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"init", "--mode", "solo"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	app := NewApp(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	mode, ok, modeErr := app.savedMode()
	if modeErr != nil {
		t.Fatalf("savedMode error = %v", modeErr)
	}
	if !ok || mode != ModeSolo {
		t.Fatalf("saved mode = %q, %v; want solo, true", mode, ok)
	}
	if _, err := config.LoadFromRoot(cwd); err != nil {
		t.Fatal(err)
	}
}

func TestRootSoloSupportBundleHelpDocumentsEnvironmentResolution(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"support", "bundle", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	help := stdout.String()
	want := "Environment resolution uses --env first, then DEVOPSELLENCE_ENVIRONMENT, then the saved workspace environment, then the project default environment."
	if !strings.Contains(help, want) {
		t.Fatalf("help = %q, want environment resolution text %q", help, want)
	}
}

func TestRootSoloContextEnvListDoesNotRequireAuth(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cwd := rootTestWorkspaceWithMode(t, ModeSolo)
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Environments = map[string]config.EnvironmentOverlay{"staging": {}}
	if _, err := config.Write(cwd, cfg); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"context", "env", "list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["mode"] != "solo" {
		t.Fatalf("payload = %#v, want solo mode", payload)
	}
	environments := jsonArrayFromMap(t, payload, "environments")
	if len(environments) != 2 {
		t.Fatalf("environments = %#v, want production and staging", environments)
	}
}

func TestModeCommandDefaultsToShow(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestWorkspaceWithMode(t, ModeSolo)
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"mode"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["mode"] != "solo" || payload["set"] != true {
		t.Fatalf("payload = %#v, want current solo mode", payload)
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
	cmd.SetArgs([]string{"secret", "set", "DATABASE_URL", "--env", "staging", "--service", " web ", "--value", "postgres://staging"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	setPayload := decodeJSONOutput(t, &stdout)
	if setPayload["schema_version"] != float64(outputSchemaVersion) {
		t.Fatalf("schema_version = %#v, want %d", setPayload["schema_version"], outputSchemaVersion)
	}
	if setPayload["secret_ref"] != "devopsellence://plaintext/DATABASE_URL" {
		t.Fatalf("secret_ref = %#v, want plaintext config ref", setPayload["secret_ref"])
	}
	if setPayload["reference"] != nil {
		t.Fatalf("reference = %#v, want omitted for plaintext secret", setPayload["reference"])
	}
	if setPayload["state_path"] != solo.DefaultStatePath() {
		t.Fatalf("state_path = %#v, want solo state path", setPayload["state_path"])
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

	web := cfg.Services["web"]
	web.SecretRefs = append(web.SecretRefs, config.SecretRef{Name: "ONLY_IN_CONFIG", Secret: "devopsellence://plaintext/ONLY_IN_CONFIG"})
	cfg.Services["web"] = web
	if _, err := config.Write(cwd, *cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SetSecret(cwd, "staging", "web", "ONLY_IN_STORE", solo.SecretMaterial{Value: "store-only"}); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	cmd = NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"secret", "list", "--env", "staging", "--service", "web"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("list Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	secrets := jsonArrayFromMap(t, payload, "secrets")
	if len(secrets) != 3 {
		t.Fatalf("secrets = %#v, want 3 entries", secrets)
	}
	seen := map[string]map[string]any{}
	for _, value := range secrets {
		item := jsonMapFromAny(t, value)
		seen[stringValueAny(item["name"])] = item
	}
	for name, want := range map[string]map[string]any{
		"DATABASE_URL":   {"configured": true, "stored": true, "available_to_service": true, "store": "plaintext"},
		"ONLY_IN_CONFIG": {"configured": true, "stored": false, "available_to_service": true, "store": "plaintext"},
		"ONLY_IN_STORE":  {"configured": false, "stored": true, "available_to_service": false, "store": "plaintext"},
	} {
		item := seen[name]
		if item == nil {
			t.Fatalf("secret %s missing from %#v", name, secrets)
		}
		for key, expected := range want {
			if item[key] != expected {
				t.Fatalf("secret %s %s = %#v, want %#v", name, key, item[key], expected)
			}
		}
	}
}

func TestRootSoloSecretSetFromStdinUpdatesConfigAndResolvedConfig(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestSoloWorkspace(t)
	cmd := NewRootCommand(strings.NewReader("super-secret\n"), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"secret", "set", "DOGFOOD_SECRET", "--service", "web", "--stdin"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	setPayload := decodeJSONOutput(t, &stdout)
	if setPayload["schema_version"] != float64(outputSchemaVersion) || setPayload["config_updated"] != true {
		t.Fatalf("secret set payload = %#v, want schema_version and config_updated=true", setPayload)
	}

	cfg, err := config.LoadFromRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	refs := cfg.Services["web"].SecretRefs
	if len(refs) != 1 || refs[0].Name != "DOGFOOD_SECRET" || refs[0].Secret != "devopsellence://plaintext/DOGFOOD_SECRET" {
		t.Fatalf("secret refs = %#v, want DOGFOOD_SECRET plaintext ref", refs)
	}

	stdout.Reset()
	cmd = NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"config", "resolve"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config resolve error = %v", err)
	}
	if !strings.Contains(stdout.String(), "DOGFOOD_SECRET") || !strings.Contains(stdout.String(), "devopsellence://plaintext/DOGFOOD_SECRET") {
		t.Fatalf("resolved config = %s, want DOGFOOD_SECRET secret ref", stdout.String())
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

func TestRootSoloSecretSetRejectsEnvConflict(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestSoloWorkspace(t)
	cfg, err := config.LoadFromRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	web := cfg.Services["web"]
	web.Env = map[string]string{"DATABASE_URL": "postgres://static"}
	cfg.Services["web"] = web
	if _, err := config.Write(cwd, *cfg); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"secret", "set", "DATABASE_URL", "--service", "web", "--value", "postgres://secret"})

	err = cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want env conflict")
	}
	if !strings.Contains(err.Error(), "already defines DATABASE_URL in env") {
		t.Fatalf("error = %v, want env conflict", err)
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
		"devopsellence init --mode solo",
		"devopsellence node create prod-1 --host 203.0.113.10 --user root --ssh-key ~/.ssh/id_ed25519",
		"devopsellence agent install prod-1",
		"devopsellence node attach prod-1",
		"devopsellence deploy",
		"context",
		"init",
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
		"setup",
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

func TestNodeCreateRejectsRemovedDeployFlag(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestWorkspaceWithMode(t, ModeShared)
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "create", "prod-1", "--deploy"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag: --deploy") {
		t.Fatalf("Execute() error = %v, want unknown deploy flag", err)
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
		"schema_version: 1",
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

func TestSupportBundleAcceptsEnvFlag(t *testing.T) {
	cwd := rootTestWorkspaceWithMode(t, ModeSolo)
	cfg := config.DefaultProjectConfig("solo", "demo", "production")
	cfg.Environments = map[string]config.EnvironmentOverlay{"staging": {}}
	if _, err := config.Write(cwd, cfg); err != nil {
		t.Fatal(err)
	}
	current := solo.State{Nodes: map[string]config.Node{
		"node-prod":    {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
		"node-staging": {Host: "203.0.113.11", User: "root", Labels: []string{config.DefaultWebRole}},
	}}
	if _, _, err := current.AttachNode(cwd, "production", "node-prod"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := current.AttachNode(cwd, "staging", "node-staging"); err != nil {
		t.Fatal(err)
	}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "support.json")
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"support", "bundle", "--env", "staging", "--output", outPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\n%s", err, stdout.String())
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["environment"] != "staging" || strings.TrimSpace(stringValueAny(payload["environment_id"])) == "" {
		t.Fatalf("payload = %#v, want staging environment and environment_id", payload)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle map[string]any
	if err := json.Unmarshal(data, &bundle); err != nil {
		t.Fatalf("parse bundle: %v\n%s", err, string(data))
	}
	if bundle["environment"] != "staging" || strings.TrimSpace(stringValueAny(bundle["environment_id"])) == "" {
		t.Fatalf("bundle = %#v, want staging environment and environment_id", bundle)
	}
	attached := jsonArrayFromMap(t, bundle, "attached_nodes")
	if len(attached) != 1 || attached[0] != "node-staging" {
		t.Fatalf("attached_nodes = %#v, want staging node only", attached)
	}
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
	for _, snippet := range []string{"register", "create", "attach", "detach", "remove", "logs", "exec"} {
		if !strings.Contains(stdout.String(), snippet) {
			t.Fatalf("help output = %q, want %q command", stdout.String(), snippet)
		}
	}
}

func TestExecReturnsStructuredUnsupportedInSharedMode(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestWorkspaceWithMode(t, ModeShared)
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"exec", "web", "--", "bin/rails", "runner", "puts Rails.env"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want unsupported error")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want ExitError", err, err)
	}
	var unsupported UnsupportedOperationError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %T %v, want UnsupportedOperationError", err, err)
	}
	fields := unsupported.ErrorFields()
	if fields["kind"] != "unsupported" || fields["mode"] != "shared" {
		t.Fatalf("fields = %#v, want shared unsupported", fields)
	}
}

func TestNodeExecReturnsStructuredUnsupportedInSharedMode(t *testing.T) {
	var stdout bytes.Buffer
	cwd := rootTestWorkspaceWithMode(t, ModeShared)
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "exec", "web", "--", "bin/rails", "runner", "puts Rails.env"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want unsupported error")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want ExitError", err, err)
	}
	var unsupported UnsupportedOperationError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error = %T %v, want UnsupportedOperationError", err, err)
	}
	fields := unsupported.ErrorFields()
	if fields["kind"] != "unsupported" || fields["mode"] != "shared" {
		t.Fatalf("fields = %#v, want shared unsupported", fields)
	}
}

func TestUnsupportedOperationErrorUsesFallbackOperation(t *testing.T) {
	err := UnsupportedOperationError{Mode: " shared ", Reason: " not here "}
	if got, want := err.Error(), "operation is not supported in shared mode: not here"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	fields := err.ErrorFields()
	if fields["operation"] != "operation" || fields["mode"] != "shared" || fields["reason"] != "not here" {
		t.Fatalf("ErrorFields() = %#v, want normalized fallback operation fields", fields)
	}

	err = UnsupportedOperationError{Operation: " exec ", Mode: " shared "}
	if got, want := err.Error(), "exec is not supported in shared mode"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestExecReturnsMissingCommandAfterSeparator(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, rootTestSoloWorkspace(t))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"exec", "web", "--"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want missing command")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %T %v, want ExitError code 2", err, err)
	}
	if !strings.Contains(err.Error(), "missing command after --") {
		t.Fatalf("error = %v, want missing command after --", err)
	}
}

func TestExecReturnsMissingServiceBeforeSeparator(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, rootTestSoloWorkspace(t))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"exec", "--", "printenv", "API_TOKEN"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want missing service")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %T %v, want ExitError code 2", err, err)
	}
	if !strings.Contains(err.Error(), "missing service before --") || !strings.Contains(err.Error(), "devopsellence exec <service> -- <command>") {
		t.Fatalf("error = %v, want missing service syntax hint", err)
	}
}

func TestNodeExecReturnsMissingCommandAfterSeparator(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, rootTestSoloWorkspace(t))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "exec", "node-a", "--"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want missing command")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %T %v, want ExitError code 2", err, err)
	}
	if !strings.Contains(err.Error(), "missing command after --") {
		t.Fatalf("error = %v, want missing command after --", err)
	}
}

func TestNodeExecReturnsMissingNodeBeforeSeparator(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, rootTestSoloWorkspace(t))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "exec", "--", "uptime"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want missing node")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %T %v, want ExitError code 2", err, err)
	}
	if !strings.Contains(err.Error(), "missing node before --") || !strings.Contains(err.Error(), "devopsellence node exec <node> -- <command>") {
		t.Fatalf("error = %v, want missing node syntax hint", err)
	}
}

func TestSecretSetHelpPrefersStdinForValues(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"secret", "set", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "--stdin") || !strings.Contains(output, "prefer --stdin") {
		t.Fatalf("help output = %q, want stdin guidance", output)
	}
	if strings.Contains(output, "--value super-secret") {
		t.Fatalf("help output = %q, leaked unsafe literal example", output)
	}
}

func TestAgentInstallHelpDocumentsNDJSONProgress(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"agent", "install", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "newline-delimited JSON progress events") || !strings.Contains(output, "final result event") {
		t.Fatalf("help output = %q, want NDJSON progress contract", output)
	}
}

func TestReleaseRollbackHelpClarifiesSelectorSource(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"release", "rollback", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"release list", "release id", "workload revision", "HEAD~1 is not supported"} {
		if !strings.Contains(output, want) {
			t.Fatalf("help output = %q, want %q", output, want)
		}
	}
}

func TestNodeDiagnoseAcceptsSoloNodeName(t *testing.T) {
	cwd := rootTestSoloWorkspace(t)
	installFakeSoloCommands(t, []fakeSSHResponse{{stdout: `{"revision":"abc","phase":"settled"}` + "\n"}})
	current := solo.State{Nodes: map[string]config.Node{
		"node-a": {Host: "203.0.113.10", User: "root", Labels: []string{config.DefaultWebRole}},
	}}
	if err := solo.NewStateStore(solo.DefaultStatePath()).Write(current); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "diagnose", "node-a"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["node"] != "node-a" {
		t.Fatalf("payload = %#v, want solo node diagnosis", payload)
	}
}

func TestNodeLogsHelpDocumentsBoundedJSONSnapshot(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"node", "logs", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	for _, snippet := range []string{"bounded JSON snapshot", "--lines"} {
		if !strings.Contains(output, snippet) {
			t.Fatalf("help output = %q, want %q", output, snippet)
		}
	}
	if strings.Contains(output, "--follow") {
		t.Fatalf("help output = %q, did not expect --follow", output)
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

func TestIngressCertInstallHelpDocumentsManualTLSProvisioning(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"ingress", "cert", "install", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, snippet := range []string{
		"Install manual TLS certificate files",
		"--cert-file string",
		"--key-file string",
		"--node strings",
		"/var/lib/devopsellence/ingress-cert.pem",
		"devopsellence ingress set --tls-mode manual",
	} {
		if !strings.Contains(stdout.String(), snippet) {
			t.Fatalf("help output = %q, want %q", stdout.String(), snippet)
		}
	}
}

func TestExecHelpDocumentsEnvironmentOverride(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"exec", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, snippet := range []string{"--env string", "Environment name override"} {
		if !strings.Contains(stdout.String(), snippet) {
			t.Fatalf("help output = %q, want %q", stdout.String(), snippet)
		}
	}
}

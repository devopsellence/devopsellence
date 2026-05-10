package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/cli/internal/state"
	cliversion "github.com/devopsellence/cli/internal/version"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
)

func installFakeVibeTools(t *testing.T, agents ...string) string {
	t.Helper()
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "mise"), "#!/usr/bin/env bash\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "rails"), `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" != "new" ]; then
  echo "unexpected rails command: $*" >&2
  exit 1
fi
target="$2"
mkdir -p "$target/.agents/skills/devopsellence-rails-app" "$target/app/controllers" "$target/config"
printf '%s\n' '---
name: devopsellence-rails-app
description: Fake test skill.
---

# Fake Rails App Skill
' > "$target/.agents/skills/devopsellence-rails-app/SKILL.md"
printf '%s\n' '[tools]' 'ruby = "3.4"' 'node = "24"' > "$target/.mise.toml"
printf '%s\n' 'coverage/' > "$target/.gitignore"
printf '%s\n' 'FROM ruby:3.4' > "$target/Dockerfile"
printf '%s\n' 'name: fake' > "$target/devopsellence.yml"
`)
	writeExecutable(t, filepath.Join(binDir, "git"), `#!/usr/bin/env bash
set -euo pipefail
cwd="$PWD"
while [ "$#" -gt 0 ]; do
  case "$1" in
    -C)
      cwd="$2"
      shift 2
      ;;
    -c)
      shift 2
      ;;
    *)
      break
      ;;
  esac
done
case "${1:-}" in
  init)
    mkdir -p "$cwd/.git"
    ;;
  rev-parse)
    test -f "$cwd/.git/fake-head"
    ;;
  add)
    exit 0
    ;;
  commit)
    mkdir -p "$cwd/.git"
    touch "$cwd/.git/fake-head"
    ;;
  *)
    echo "unexpected git command: $*" >&2
    exit 1
    ;;
esac
`)
	requestedAgents := map[string]bool{}
	for _, agent := range agents {
		requestedAgents[agent] = true
	}
	for _, agent := range vibeAgentPreference {
		if !requestedAgents[agent] {
			writeExecutable(t, filepath.Join(binDir, agent), "#!/usr/bin/env bash\nexit 127\n")
		}
	}
	for _, agent := range agents {
		writeExecutable(t, filepath.Join(binDir, agent), `#!/usr/bin/env bash
set -euo pipefail
if [ -n "${VIBE_AGENT_ARGS_FILE:-}" ]; then
  printf '%s\n' "$@" > "$VIBE_AGENT_ARGS_FILE"
fi
exit 0
`)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return binDir
}

func setFakeVibeHome(t *testing.T, cwd string) string {
	t.Helper()
	home := filepath.Join(cwd, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(cwd, "state"))
	return home
}

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

func TestRootModeUseIncludesLocalStateMetadata(t *testing.T) {
	stateHome := filepath.Join(t.TempDir(), "devopsellence-state")
	t.Setenv(state.HomeEnv, stateHome)
	t.Setenv(state.FallbackHomeEnv, filepath.Join(t.TempDir(), "xdg-state"))

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"mode", "use", "solo"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["state_home_env"] != state.HomeEnv || payload["state_home_fallback_env"] != state.FallbackHomeEnv {
		t.Fatalf("state env metadata = %#v", payload)
	}
	if payload["workspace_state_path"] != filepath.Join(stateHome, "devopsellence", "workspace.json") {
		t.Fatalf("workspace_state_path = %#v", payload["workspace_state_path"])
	}
	if payload["solo_state_path"] != filepath.Join(stateHome, "devopsellence", "solo", "state.json") {
		t.Fatalf("solo_state_path = %#v", payload["solo_state_path"])
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

func TestRootSkillInstallWritesBundledSkill(t *testing.T) {
	skillsDir := t.TempDir()
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"skill", "install", "--dir", skillsDir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["schema_version"] != float64(outputSchemaVersion) || payload["action"] != "installed" || payload["skill"] != "devopsellence" || payload["source"] != "embedded" {
		t.Fatalf("payload = %#v, want embedded skill install result", payload)
	}
	path := filepath.Join(skillsDir, "devopsellence", "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected bundled skill at %s: %v", path, err)
	}
	if payload["path"] != filepath.Join(skillsDir, "devopsellence") {
		t.Fatalf("path = %#v, want %q", payload["path"], filepath.Join(skillsDir, "devopsellence"))
	}
	paths := jsonArrayFromMap(t, payload, "paths")
	if len(paths) != 1 {
		t.Fatalf("paths = %#v, want one explicit install target", paths)
	}
}

func TestRootSkillListIncludesRailsAppSkill(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"skill", "list"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	skills, ok := payload["skills"].([]any)
	if !ok {
		t.Fatalf("skills = %#v, want array", payload["skills"])
	}
	var ids []string
	for _, raw := range skills {
		skill, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("skill = %#v, want object", raw)
		}
		ids = append(ids, skill["id"].(string))
	}
	for _, want := range []string{"devopsellence", "rails-app"} {
		if !stringSliceContains(ids, want) {
			t.Fatalf("skill ids = %v, missing %q", ids, want)
		}
	}
}

func TestRootSkillInstallWritesRailsAppSkill(t *testing.T) {
	skillsDir := t.TempDir()
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"skill", "install", "rails-app", "--dir", skillsDir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["id"] != "rails-app" || payload["skill"] != "devopsellence-rails-app" || payload["source"] != "embedded" {
		t.Fatalf("payload = %#v, want rails-app install result", payload)
	}
	path := filepath.Join(skillsDir, "devopsellence-rails-app", "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected bundled skill at %s: %v", path, err)
	}
}

func TestRootSkillInstallUnknownSkillIsUsageError(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"skill", "install", "unknown-pack"})

	err := cmd.Execute()
	var exitErr ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Execute() error = %T %[1]v, want ExitError", err)
	}
	if exitErr.Code != 2 {
		t.Fatalf("ExitError.Code = %d, want 2", exitErr.Code)
	}
}

func TestRootSkillInstallDefaultsToProjectSkillDirs(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "devopsellence.yml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cwd, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"skill", "install"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	agentsPath := filepath.Join(cwd, ".agents", "skills", "devopsellence")
	claudePath := filepath.Join(cwd, ".claude", "skills", "devopsellence")
	if payload["path"] != agentsPath {
		t.Fatalf("path = %#v, want %q", payload["path"], agentsPath)
	}
	if _, err := os.Stat(filepath.Join(agentsPath, "SKILL.md")); err != nil {
		t.Fatalf("expected project agents skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claudePath, "SKILL.md")); err != nil {
		t.Fatalf("expected project claude skill: %v", err)
	}
	paths := jsonArrayFromMap(t, payload, "paths")
	if len(paths) != 2 {
		t.Fatalf("paths = %#v, want agents and claude targets", paths)
	}
}

func TestRootSkillInstallRejectsDirWithGlobalAsUsageError(t *testing.T) {
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"skill", "install", "--dir", t.TempDir(), "--global"})

	err := cmd.Execute()
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
}

func TestRootSkillInstallRequiresWorkspaceForDefaultProjectInstall(t *testing.T) {
	cwd := filepath.Join(string(os.PathSeparator), "devopsellence-no-workspace-"+strings.ReplaceAll(t.Name(), "/", "-"))

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"skill", "install"})

	err := cmd.Execute()
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if !strings.Contains(err.Error(), "devopsellence.yml") || !strings.Contains(err.Error(), "--global") || !strings.Contains(err.Error(), "--dir <path>") {
		t.Fatalf("error = %v, want workspace/global/dir guidance", err)
	}
}

func TestRootVibePreparesRailsAppWorkspace(t *testing.T) {
	cwd := t.TempDir()
	home := setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "my-app",
		"--ai-agent", "Codex",
		"--idea", "A tiny CRM for solo consultants",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	projectsDir := filepath.Join(home, defaultVibeProjectsDirName)
	appDir := filepath.Join(projectsDir, "my-app")
	if payload["directory"] != appDir || payload["projects_dir"] != projectsDir || payload["ai_agent"] != "codex" || payload["agent_effort"] != "high" || payload["agent_autonomy"] != "builder" || payload["app_stack"] != "rails-app" || payload["launch_requested"] != false {
		t.Fatalf("payload = %#v, want prepared codex rails workspace", payload)
	}
	intent := jsonMapFromAny(t, payload["deployment_intent"])
	if intent["deploy_goal"] != "deploy-ready" || intent["devopsellence_mode"] != "solo" || intent["server_strategy"] != "none" {
		t.Fatalf("deployment_intent = %#v, want solo deploy-ready defaults", intent)
	}
	if payload["template_version"] != defaultVibeTemplateVersion || payload["template_url"] != vibeTemplateURL(defaultVibeTemplateVersion) || payload["initial_commit"] != true {
		t.Fatalf("payload = %#v, want pinned template and initial commit", payload)
	}
	if payload["skill_id"] != "rails-app" || payload["skill_name"] != "devopsellence-rails-app" || payload["launched"] != false {
		t.Fatalf("payload = %#v, want stable skill metadata and no launched agent", payload)
	}
	for _, path := range []string{
		filepath.Join(appDir, ".git"),
		filepath.Join(appDir, ".mise.toml"),
		filepath.Join(appDir, ".agents", "skills", "devopsellence", "SKILL.md"),
		filepath.Join(appDir, ".agents", "skills", "devopsellence-rails-app", "SKILL.md"),
		filepath.Join(appDir, ".agents", "devopsellence-vibe.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	promptPath := filepath.Join(appDir, ".agents", "prompts", "devopsellence-vibe.md")
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "/goal") || !strings.Contains(string(prompt), "A tiny CRM") || !strings.Contains(string(prompt), "Deployment intent") || !strings.Contains(string(prompt), "Agent autonomy") || !strings.Contains(string(prompt), "sequencing the work yourself") || !strings.Contains(string(prompt), "Before any production mutation") || !strings.Contains(string(prompt), "Rails 8.1") {
		t.Fatalf("prompt = %q, want seeded codex prompt", prompt)
	}
	nextCommands := jsonArrayFromMap(t, payload, "next_commands")
	if !jsonArrayContains(nextCommands, "codex --sandbox 'workspace-write' --ask-for-approval 'on-request' -c 'model_reasoning_effort=\"high\"' 'Read .agents/prompts/devopsellence-vibe.md and follow it.'") {
		t.Fatalf("next_commands = %#v, want prompt-file agent command", nextCommands)
	}
	manifestData, err := os.ReadFile(filepath.Join(appDir, ".agents", "devopsellence-vibe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest vibeManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(manifest.SkillDir) || filepath.IsAbs(manifest.PromptPath) || manifest.AppStack != "rails-app" || manifest.AgentEffort != "high" || manifest.AgentAutonomy != "builder" || manifest.TemplateVersion != defaultVibeTemplateVersion || manifest.DeploymentIntent.DeployGoal != "deploy-ready" {
		t.Fatalf("manifest = %#v, want repo-relative paths", manifest)
	}
}

func TestRootVibeHonorsExplicitRelativePath(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "./my-app",
		"--ai-agent", "generic",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	appDir := filepath.Join(cwd, "my-app")
	if payload["directory"] != appDir || payload["projects_dir"] != "" {
		t.Fatalf("payload = %#v, want explicit relative path under cwd", payload)
	}
	if _, err := os.Stat(filepath.Join(appDir, ".agents", "devopsellence-vibe.json")); err != nil {
		t.Fatalf("expected explicit path app: %v", err)
	}
}

func TestRootVibeUsesConfiguredProjectsDir(t *testing.T) {
	cwd := t.TempDir()
	home := setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "my-app",
		"--projects-dir", "~/custom-projects",
		"--ai-agent", "generic",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	projectsDir := filepath.Join(home, "custom-projects")
	appDir := filepath.Join(projectsDir, "my-app")
	if payload["directory"] != appDir || payload["projects_dir"] != projectsDir {
		t.Fatalf("payload = %#v, want configured projects dir", payload)
	}
}

func TestRootVibeUsesProjectsDirEnvironment(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	envProjectsDir := filepath.Join(cwd, "env-projects")
	t.Setenv("DEVOPSELLENCE_PROJECTS_DIR", envProjectsDir)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "my-app",
		"--ai-agent", "generic",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["directory"] != filepath.Join(envProjectsDir, "my-app") || payload["projects_dir"] != envProjectsDir {
		t.Fatalf("payload = %#v, want env projects dir", payload)
	}
}

func TestRootVibeWizardOnlyAsksForAppIdea(t *testing.T) {
	cwd := t.TempDir()
	home := setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	input := bytes.NewBufferString("A tiny CRM for consultants\n")
	cmd := NewRootCommand(input, &stdout, &stderr, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{
		"vibe", "crm-app",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Ctrl+C") || !strings.Contains(stderr.String(), "App idea") {
		t.Fatalf("stderr = %q, want vibe intake and app idea prompt", stderr.String())
	}
	for _, unwanted := range []string{"Agent freedom", "Server plan", "External services", "devopsellence mode"} {
		if strings.Contains(stderr.String(), unwanted) {
			t.Fatalf("stderr = %q, want no %q prompt", stderr.String(), unwanted)
		}
	}
	payload := decodeJSONOutput(t, &stdout)
	appDir := filepath.Join(home, defaultVibeProjectsDirName, "crm-app")
	if payload["directory"] != appDir {
		t.Fatalf("payload = %#v, want app under projects dir", payload)
	}
	if payload["ai_agent"] != "generic" || payload["agent_autonomy"] != "builder" {
		t.Fatalf("payload = %#v, want generic builder defaults", payload)
	}
	intent := jsonMapFromAny(t, payload["deployment_intent"])
	if intent["deploy_goal"] != "deploy-ready" || intent["server_strategy"] != "none" || intent["first_workflow"] != "derive from the app idea" {
		t.Fatalf("deployment_intent = %#v, want deploy-ready defaults", intent)
	}
	services := jsonArrayFromMap(t, intent, "services")
	if len(services) != 1 || services[0] != "later" {
		t.Fatalf("services = %#v, want later by default", services)
	}
	manifestData, err := os.ReadFile(filepath.Join(appDir, ".agents", "devopsellence-vibe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest vibeManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.AgentAutonomy != "builder" || manifest.DeploymentIntent.DeployGoal != "deploy-ready" {
		t.Fatalf("manifest = %#v, want builder deploy-ready defaults", manifest)
	}
	promptData, err := os.ReadFile(filepath.Join(appDir, ".agents", "prompts", "devopsellence-vibe.md"))
	if err != nil {
		t.Fatal(err)
	}
	prompt := string(promptData)
	for _, want := range []string{
		"A tiny CRM for consultants",
		"sequencing the work yourself",
		"Make the app deploy-ready",
		"No server is selected yet",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRootVibeHetznerAuthStatusDoesNotLeakToken(t *testing.T) {
	cwd := t.TempDir()
	home := setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	t.Setenv("DEVOPSELLENCE_HETZNER_API_TOKEN", "super-secret-token")
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "crm-app",
		"--ai-agent", "generic",
		"--idea", "A tiny CRM",
		"--server", "hetzner",
		"--server-target", "prod-1",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	intent := jsonMapFromAny(t, payload["deployment_intent"])
	if intent["provider_auth_status"] != "available" || intent["provider_auth_source"] != "DEVOPSELLENCE_HETZNER_API_TOKEN" {
		t.Fatalf("deployment_intent = %#v, want env auth status without token", intent)
	}
	appDir := filepath.Join(home, defaultVibeProjectsDirName, "crm-app")
	for _, path := range []string{
		filepath.Join(appDir, ".agents", "devopsellence-vibe.json"),
		filepath.Join(appDir, ".agents", "prompts", "devopsellence-vibe.md"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "super-secret-token") {
			t.Fatalf("%s leaked provider token", path)
		}
	}
	if strings.Contains(stdout.String(), "super-secret-token") {
		t.Fatalf("stdout leaked provider token: %s", stdout.String())
	}
}

func TestRootVibeAppendsSecretPatternsToExistingGitignore(t *testing.T) {
	cwd := t.TempDir()
	installFakeVibeTools(t)
	appDir := filepath.Join(cwd, "existing-app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".gitignore"), []byte("coverage/\n!.env.example\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "./existing-app",
		"--ai-agent", "generic",
		"--idea", "A tiny uptime API",
		"--no-launch",
		"--force",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(appDir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	gitignore := string(data)
	for _, want := range []string{"coverage/", ".env", ".env.*", "!.env.example"} {
		if !strings.Contains(gitignore, want) {
			t.Fatalf(".gitignore = %q, missing %q", gitignore, want)
		}
	}
	if strings.Index(gitignore, ".env.*") > strings.Index(gitignore, "!.env.example") {
		t.Fatalf(".gitignore = %q, want .env.* before !.env.example", gitignore)
	}

	stdout.Reset()
	cmd = NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "./existing-app",
		"--ai-agent", "generic",
		"--idea", "A tiny uptime API",
		"--no-launch",
		"--force",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() second run error = %v", err)
	}
	data, err = os.ReadFile(filepath.Join(appDir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != gitignore {
		t.Fatalf(".gitignore changed on second run:\nfirst=%q\nsecond=%q", gitignore, data)
	}
}

func TestRootVibeNoAgentUsesGeneric(t *testing.T) {
	cwd := t.TempDir()
	home := setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	input := bytes.NewBuffer(nil)
	cmd := NewRootCommand(input, &stdout, &stderr, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "rails-app",
		"--idea", "A tiny uptime page",
		"--no-agent",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ai_agent"] != "generic" || payload["app_stack"] != "rails-app" || payload["launch_requested"] != false {
		t.Fatalf("payload = %#v, want generic rails app workspace", payload)
	}
	path := filepath.Join(home, defaultVibeProjectsDirName, "rails-app", ".agents", "skills", "devopsellence-rails-app", "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected rails app skill at %s: %v", path, err)
	}
}

func TestRootVibeLaunchReportsSuccess(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	installFakeVibeTools(t, "codex")
	argsPath := filepath.Join(cwd, "agent-args.txt")
	t.Setenv("VIBE_AGENT_ARGS_FILE", argsPath)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "launched-app",
		"--ai-agent", "codex",
		"--idea", "Launch this app",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["launch_requested"] != true || payload["launched"] != true {
		t.Fatalf("payload = %#v, want successful launch reported", payload)
	}
	data, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "--sandbox\nworkspace-write\n--ask-for-approval\non-request\n-c\nmodel_reasoning_effort=\"high\"\nRead .agents/prompts/devopsellence-vibe.md and follow it.\n" {
		t.Fatalf("agent args = %q, want codex high-effort prompt-file launch", data)
	}
}

func TestRootVibeRejectsMissingLaunchAgentBeforeScaffold(t *testing.T) {
	cwd := t.TempDir()
	home := setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "missing-agent",
		"--ai-agent", "codex",
		"--idea", "Launch this app",
	})

	err := cmd.Execute()
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, defaultVibeProjectsDirName, "missing-agent")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("generated app exists after missing agent preflight: %v", statErr)
	}
}

func TestRootVibeRejectsUnreadyLaunchAgentBeforeScaffold(t *testing.T) {
	cwd := t.TempDir()
	home := setFakeVibeHome(t, cwd)
	binDir := installFakeVibeTools(t, "codex")
	writeExecutable(t, filepath.Join(binDir, "codex"), `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "login" ] && [ "${2:-}" = "status" ]; then
  exit 1
fi
exit 0
`)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "unready-agent",
		"--ai-agent", "codex",
		"--idea", "Launch this app",
	})

	err := cmd.Execute()
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if !strings.Contains(err.Error(), "setup check failed") {
		t.Fatalf("error = %v, want setup-check guidance", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, defaultVibeProjectsDirName, "unready-agent")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("generated app exists after unready agent preflight: %v", statErr)
	}
}

func TestPrepareVibeDirectoryRejectsFileAsUsageError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := prepareVibeDirectory(path, false)
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
}

func TestRootVibeAutoSelectsUsableAgentByPreference(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	installFakeVibeTools(t, "opencode", "pi", "claude", "codex")
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "preferred-agent",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ai_agent"] != "codex" {
		t.Fatalf("payload = %#v, want codex selected first", payload)
	}
	detected := jsonArrayFromMap(t, payload, "detected_agents")
	want := []string{"codex", "claude", "pi", "opencode"}
	if len(detected) != len(want) {
		t.Fatalf("detected_agents = %#v, want %v", detected, want)
	}
	for i, item := range detected {
		if item != want[i] {
			t.Fatalf("detected_agents = %#v, want %v", detected, want)
		}
	}
}

func TestRootVibeSkipsUnreadyAutoDetectedAgent(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	binDir := installFakeVibeTools(t, "codex", "claude")
	writeExecutable(t, filepath.Join(binDir, "codex"), `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "login" ] && [ "${2:-}" = "status" ]; then
  exit 1
fi
exit 0
`)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "fallback-agent",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ai_agent"] != "claude" {
		t.Fatalf("payload = %#v, want claude after codex setup probe fails", payload)
	}
	detected := jsonArrayFromMap(t, payload, "detected_agents")
	if len(detected) != 1 || detected[0] != "claude" {
		t.Fatalf("detected_agents = %#v, want only claude", detected)
	}
}

func TestRootVibeNoLaunchExplicitAgentSkipsProbe(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	binDir := installFakeVibeTools(t, "codex")
	writeExecutable(t, filepath.Join(binDir, "codex"), `#!/usr/bin/env bash
set -euo pipefail
if [ "${1:-}" = "login" ] && [ "${2:-}" = "status" ]; then
  exit 1
fi
exit 0
`)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "manual-agent",
		"--ai-agent", "codex",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	payload := decodeJSONOutput(t, &stdout)
	if payload["ai_agent"] != "codex" || payload["launch_requested"] != false {
		t.Fatalf("payload = %#v, want explicit codex without launch", payload)
	}
	detected := jsonArrayFromMap(t, payload, "detected_agents")
	if len(detected) != 0 {
		t.Fatalf("detected_agents = %#v, want no setup probes for explicit no-launch agent", detected)
	}
}

func TestRootVibeRejectsUnsupportedAgentEffort(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "my-app",
		"--ai-agent", "generic",
		"--agent-effort", "max",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	err := cmd.Execute()
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if !strings.Contains(err.Error(), "unsupported agent effort") {
		t.Fatalf("error = %v, want unsupported effort guidance", err)
	}
}

func TestRootVibeRejectsUnsupportedAgentAutonomy(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "my-app",
		"--ai-agent", "generic",
		"--autonomy", "reckless",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	err := cmd.Execute()
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if !strings.Contains(err.Error(), "unsupported agent autonomy") {
		t.Fatalf("error = %v, want unsupported autonomy guidance", err)
	}
}

func TestRootVibeRejectsUnsupportedDeployGoal(t *testing.T) {
	cwd := t.TempDir()
	setFakeVibeHome(t, cwd)
	installFakeVibeTools(t)
	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, cwd)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{
		"vibe", "my-app",
		"--ai-agent", "generic",
		"--deploy-goal", "ship-it",
		"--idea", "A tiny uptime page",
		"--no-launch",
	})

	err := cmd.Execute()
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if !strings.Contains(err.Error(), "unsupported deploy goal") {
		t.Fatalf("error = %v, want unsupported deploy goal guidance", err)
	}
}

func TestVibeAgentCommandIncludesEffort(t *testing.T) {
	tests := []struct {
		agent    string
		effort   string
		autonomy string
		want     string
	}{
		{
			agent:    "codex",
			effort:   "high",
			autonomy: "builder",
			want:     "codex --sandbox 'workspace-write' --ask-for-approval 'on-request' -c 'model_reasoning_effort=\"high\"' 'Read .agents/prompts/devopsellence-vibe.md and follow it.'",
		},
		{
			agent:    "claude",
			effort:   "high",
			autonomy: "builder",
			want:     "claude --permission-mode 'auto' --effort high 'Read .agents/prompts/devopsellence-vibe.md and follow it.'",
		},
		{
			agent:    "pi",
			effort:   "high",
			autonomy: "builder",
			want:     "pi --thinking high 'Read .agents/prompts/devopsellence-vibe.md and follow it.'",
		},
		{
			agent:    "opencode",
			effort:   "high",
			autonomy: "builder",
			want:     "opencode --prompt 'Read .agents/prompts/devopsellence-vibe.md and follow it.'",
		},
		{
			agent:    "codex",
			effort:   "default",
			autonomy: "careful",
			want:     "codex --sandbox 'workspace-write' --ask-for-approval 'untrusted' 'Read .agents/prompts/devopsellence-vibe.md and follow it.'",
		},
		{
			agent:    "codex",
			effort:   "high",
			autonomy: "full-access",
			want:     "codex --dangerously-bypass-approvals-and-sandbox -c 'model_reasoning_effort=\"high\"' 'Read .agents/prompts/devopsellence-vibe.md and follow it.'",
		},
		{
			agent:    "claude",
			effort:   "high",
			autonomy: "full-access",
			want:     "claude --dangerously-skip-permissions --effort high 'Read .agents/prompts/devopsellence-vibe.md and follow it.'",
		},
		{
			agent:    "generic",
			effort:   "high",
			autonomy: "full-access",
			want:     "cat .agents/prompts/devopsellence-vibe.md",
		},
	}
	for _, tt := range tests {
		got := vibeAgentCommand(tt.agent, tt.effort, tt.autonomy)
		if got != tt.want {
			t.Fatalf("vibeAgentCommand(%q, %q, %q) = %q, want %q", tt.agent, tt.effort, tt.autonomy, got, tt.want)
		}
	}
}

func TestNormalizeVibeAgentAutonomy(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: "", want: defaultVibeAgentAutonomy},
		{name: "default", value: "default", want: defaultVibeAgentAutonomy},
		{name: "careful", value: "careful", want: "careful"},
		{name: "builder", value: "builder", want: "builder"},
		{name: "full", value: "full", want: "full-access"},
		{name: "full access", value: "full access", want: "full-access"},
		{name: "full_access", value: "full_access", want: "full-access"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeVibeAgentAutonomy(tt.value)
			if err != nil {
				t.Fatalf("normalizeVibeAgentAutonomy(%q) error = %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeVibeAgentAutonomy(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestNormalizeVibeLater(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "empty", value: "", want: vibeDomainLater},
		{name: "none", value: "none", want: vibeDomainLater},
		{name: "no", value: "no", want: vibeDomainLater},
		{name: "lower later", value: "later", want: vibeDomainLater},
		{name: "title later", value: "Later", want: vibeDomainLater},
		{name: "upper later", value: "LATER", want: vibeDomainLater},
		{name: "domain", value: "crm.example.com", want: "crm.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeVibeLater(tt.value); got != tt.want {
				t.Fatalf("normalizeVibeLater(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestTruncateVibeTextPreservesUTF8(t *testing.T) {
	got := truncateVibeText("abå😊cd", 4)
	if got != "abå😊" {
		t.Fatalf("truncateVibeText() = %q, want rune-boundary truncation", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncateVibeText() returned invalid UTF-8: %q", got)
	}
}

func stringSliceContains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
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
	warnings := jsonArrayFromMap(t, setPayload, "warnings")
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want plaintext storage warning", warnings)
	}
	warning := stringValueAny(warnings[0])
	for _, want := range []string{
		"stored unencrypted in the local devopsellence solo state file",
		"demos or local operator-managed deployments only",
		"devopsellence secret set 'DATABASE_URL' --service 'web' --env 'staging' --store 1password --op-ref 'op://<vault>/<item>/<field>'",
	} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning = %q, want %q", warning, want)
		}
	}
	if strings.Contains(warning, "postgres://staging") {
		t.Fatalf("warning leaks secret value: %q", warning)
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

func TestAgentUpgradeHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	cmd := NewRootCommand(bytes.NewBuffer(nil), &stdout, &stdout, t.TempDir())
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"agent", "upgrade", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"Upgrade the agent", "--agent-binary", "--base-url"} {
		if !strings.Contains(output, want) {
			t.Fatalf("help output = %q, want %q", output, want)
		}
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

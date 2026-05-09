package workflow

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/devopsellence/cli/internal/agentskill"
	"github.com/devopsellence/cli/internal/version"
)

type VibeOptions struct {
	Directory       string
	AIAgent         string
	Idea            string
	TemplateVersion string
	Launch          bool
	NoAgent         bool
	Force           bool
}

type vibeManifest struct {
	SchemaVersion   int      `json:"schema_version"`
	AIAgent         string   `json:"ai_agent"`
	DetectedAgents  []string `json:"detected_agents"`
	AppStack        string   `json:"app_stack"`
	TemplateURL     string   `json:"template_url"`
	TemplateVersion string   `json:"template_version"`
	SkillDir        string   `json:"skill_dir"`
	PromptPath      string   `json:"prompt_path"`
	Idea            string   `json:"idea"`
	NextCommands    []string `json:"next_commands"`
}

const (
	vibeAppStack               = "rails-app"
	defaultVibeTemplateVersion = "v0.1.0"
)

var vibeSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)

func (a *App) Vibe(ctx context.Context, opts VibeOptions) error {
	opts.AIAgent = strings.ToLower(strings.TrimSpace(opts.AIAgent))
	reader := bufio.NewReader(a.In)
	detectedAgents := a.detectVibeAgents()
	if opts.NoAgent {
		opts.AIAgent = "generic"
		opts.Launch = false
	}
	if opts.AIAgent == "" && len(detectedAgents) == 1 {
		opts.AIAgent = detectedAgents[0]
	}
	if opts.AIAgent == "" && len(detectedAgents) == 0 {
		opts.AIAgent = "generic"
		opts.Launch = false
	}
	if opts.AIAgent == "" {
		agent, err := a.askVibeQuestion(reader, "AI agent "+vibeAgentChoiceHint(detectedAgents))
		if err != nil {
			return err
		}
		opts.AIAgent = strings.ToLower(strings.TrimSpace(agent))
	}
	if opts.AIAgent == "" {
		return ExitError{Code: 2, Err: errors.New("missing ai agent; choose codex, claude, pi, or generic")}
	}
	if !supportedVibeAgent(opts.AIAgent) {
		return ExitError{Code: 2, Err: fmt.Errorf("unsupported ai agent %q; use codex, claude, pi, or generic", opts.AIAgent)}
	}
	if opts.AIAgent == "generic" {
		opts.Launch = false
	}
	if strings.TrimSpace(opts.Idea) == "" {
		idea, err := a.askVibeQuestion(reader, "App idea")
		if err != nil {
			return err
		}
		opts.Idea = idea
	}
	if strings.TrimSpace(opts.Idea) == "" {
		return ExitError{Code: 2, Err: errors.New("missing app idea")}
	}
	if len(opts.Idea) > 4096 {
		return ExitError{Code: 2, Err: errors.New("app idea is too long; keep it under 4096 characters")}
	}
	opts.TemplateVersion = strings.TrimSpace(opts.TemplateVersion)
	if opts.TemplateVersion == "" {
		opts.TemplateVersion = defaultVibeTemplateVersion
	}
	templateURL := vibeTemplateURL(opts.TemplateVersion)
	if err := a.ensureVibeTools(); err != nil {
		return err
	}

	target := strings.TrimSpace(opts.Directory)
	if target == "" {
		target = vibeSlug(opts.Idea)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(a.Cwd, target)
	}
	if err := prepareVibeDirectory(target, opts.Force); err != nil {
		return err
	}
	if err := a.generateVibeRailsApp(ctx, target, templateURL, opts.Force); err != nil {
		return err
	}

	agentsDir := filepath.Join(target, ".agents")
	skillsDir := filepath.Join(agentsDir, "skills")
	promptsDir := filepath.Join(agentsDir, "prompts")
	if err := os.MkdirAll(promptsDir, 0o755); err != nil {
		return fmt.Errorf("create prompts dir: %w", err)
	}
	if _, err := agentskill.Install(agentskill.InstallOptions{SkillsDir: skillsDir, Skill: agentskill.Name}, version.String()); err != nil {
		return err
	}
	if err := ensureVibeRailsAppSkill(target); err != nil {
		return err
	}
	if err := ensureVibeGitignore(target); err != nil {
		return err
	}

	prompt := vibePrompt(opts.AIAgent, templateURL, opts.Idea)
	promptPath := filepath.Join(promptsDir, "devopsellence-vibe.md")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}

	agentCommand := vibeAgentCommand(opts.AIAgent)
	manifestNextCommands := []string{agentCommand}
	manifest := vibeManifest{
		SchemaVersion:   outputSchemaVersion,
		AIAgent:         opts.AIAgent,
		DetectedAgents:  detectedAgents,
		AppStack:        vibeAppStack,
		TemplateURL:     templateURL,
		TemplateVersion: opts.TemplateVersion,
		SkillDir:        filepath.Join(".agents", "skills"),
		PromptPath:      filepath.Join(".agents", "prompts", "devopsellence-vibe.md"),
		Idea:            opts.Idea,
		NextCommands:    manifestNextCommands,
	}
	nextCommands := []string{"cd " + shellQuote(target), agentCommand}
	manifestPath := filepath.Join(agentsDir, "devopsellence-vibe.json")
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write vibe manifest: %w", err)
	}
	if err := ensureGitRepository(ctx, target); err != nil {
		return err
	}
	initialCommit, err := ensureInitialVibeCommit(ctx, target)
	if err != nil {
		return err
	}

	payload := map[string]any{
		"schema_version":   outputSchemaVersion,
		"action":           "initialized",
		"directory":        target,
		"ai_agent":         opts.AIAgent,
		"detected_agents":  detectedAgents,
		"app_stack":        vibeAppStack,
		"template_url":     templateURL,
		"template_version": opts.TemplateVersion,
		"skill":            agentskill.RailsAppName,
		"skill_dir":        skillsDir,
		"prompt_path":      promptPath,
		"manifest_path":    manifestPath,
		"initial_commit":   initialCommit,
		"launch_requested": opts.Launch,
		"next_commands":    nextCommands,
	}
	if err := a.Printer.PrintJSON(payload); err != nil {
		return err
	}
	if opts.Launch {
		return a.launchVibeAgent(ctx, opts.AIAgent, target)
	}
	return nil
}

func (a *App) askVibeQuestion(reader *bufio.Reader, label string) (string, error) {
	_, _ = fmt.Fprintf(a.Printer.Err, "%s: ", label)
	answer, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(answer) == "" {
		return "", ExitError{Code: 2, Err: fmt.Errorf("missing %s; pass it with a flag for non-interactive use", strings.ToLower(label))}
	}
	return strings.TrimSpace(answer), nil
}

func supportedVibeAgent(agent string) bool {
	switch agent {
	case "codex", "claude", "pi", "generic":
		return true
	default:
		return false
	}
}

func (a *App) detectVibeAgents() []string {
	var agents []string
	for _, name := range []string{"codex", "claude", "pi"} {
		if _, err := a.LookPath(name); err == nil {
			agents = append(agents, name)
		}
	}
	return agents
}

func vibeAgentChoiceHint(agents []string) string {
	choices := append([]string(nil), agents...)
	choices = append(choices, "generic")
	return "(" + strings.Join(choices, ", ") + ")"
}

func prepareVibeDirectory(path string, force bool) error {
	entries, err := os.ReadDir(path)
	if err == nil {
		if len(entries) > 0 && !force {
			return ExitError{Code: 2, Err: fmt.Errorf("%s is not empty; choose another directory or pass --force", path)}
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect directory: %w", err)
	}
	parent := filepath.Dir(path)
	if parent == "." || parent == path {
		return nil
	}
	return os.MkdirAll(parent, 0o755)
}

func ensureGitRepository(ctx context.Context, path string) error {
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat .git: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "init")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func ensureInitialVibeCommit(ctx context.Context, path string) (bool, error) {
	if err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--quiet", "--verify", "HEAD").Run(); err == nil {
		return false, nil
	} else {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || (exitErr.ExitCode() != 1 && exitErr.ExitCode() != 128) {
			return false, fmt.Errorf("inspect git HEAD: %w", err)
		}
	}
	if output, err := exec.CommandContext(ctx, "git", "-C", path, "add", ".").CombinedOutput(); err != nil {
		return false, fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(output)))
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "-c", "user.name=devopsellence", "-c", "user.email=devopsellence@example.invalid", "commit", "-m", "Initial devopsellence Rails app")
	if output, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return true, nil
}

func ensureVibeGitignore(path string) error {
	gitignore := filepath.Join(path, ".gitignore")
	required := []string{".env", ".env.*", "!.env.example", "node_modules/", "dist/", "tmp/", "log/"}
	if data, err := os.ReadFile(gitignore); err == nil {
		requiredSet := map[string]bool{}
		for _, line := range required {
			requiredSet[line] = true
		}

		var next []string
		content := strings.TrimRight(string(data), "\n")
		if content != "" {
			for _, line := range strings.Split(content, "\n") {
				if requiredSet[strings.TrimSpace(line)] {
					continue
				}
				next = append(next, line)
			}
		}
		for len(next) > 0 && strings.TrimSpace(next[len(next)-1]) == "" {
			next = next[:len(next)-1]
		}
		if len(next) > 0 {
			next = append(next, "")
		}
		next = append(next, required...)

		updated := strings.Join(next, "\n") + "\n"
		if updated == string(data) {
			return nil
		}
		return os.WriteFile(gitignore, []byte(updated), 0o644)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	return os.WriteFile(gitignore, []byte(strings.Join(required, "\n")+"\n"), 0o644)
}

func (a *App) ensureVibeTools() error {
	if _, err := a.LookPath("mise"); err != nil {
		return ExitError{Code: 2, Err: errors.New("mise not found; install mise before running devopsellence vibe")}
	}
	if _, err := a.LookPath("rails"); err != nil {
		return ExitError{Code: 2, Err: errors.New("rails not found; install Rails with mise-managed Ruby before running devopsellence vibe")}
	}
	if _, err := a.LookPath("git"); err != nil {
		return ExitError{Code: 2, Err: errors.New("git not found; install git before running devopsellence vibe")}
	}
	return nil
}

func (a *App) generateVibeRailsApp(ctx context.Context, target, templateURL string, force bool) error {
	args := []string{"new", target, "-d", "postgresql", "-m", templateURL}
	if force {
		args = append(args, "--force")
	}
	cmd := exec.CommandContext(ctx, "rails", args...)
	cmd.Dir = a.Cwd
	cmd.Stdin = a.In
	cmd.Stdout = a.Printer.Err
	cmd.Stderr = a.Printer.Err
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rails new: %w", err)
	}
	return nil
}

func ensureVibeRailsAppSkill(target string) error {
	path := filepath.Join(target, ".agents", "skills", agentskill.RailsAppName, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("rails template did not install %s at %s", agentskill.RailsAppName, path)
		}
		return fmt.Errorf("inspect rails app skill: %w", err)
	}
	return nil
}

func vibeTemplateURL(version string) string {
	return "https://raw.githubusercontent.com/devopsellence/rails-app-template/" + version + "/template.rb"
}

func vibePrompt(agent, templateURL, idea string) string {
	var firstLine string
	switch agent {
	case "codex":
		firstLine = "/goal Build this app idea into a deployable Rails product using the installed devopsellence Rails app skill."
	case "claude":
		firstLine = "Run a tight build-test-deploy loop for this Rails app idea using the installed devopsellence Rails app skill."
	case "pi":
		firstLine = "Use the installed devopsellence Rails app skill as the operating instructions for this app build."
	default:
		firstLine = "Build this Rails app idea using the installed devopsellence Rails app skill."
	}
	return strings.Join([]string{
		firstLine,
		"",
		"App idea:",
		idea,
		"",
		"Rails template: " + templateURL,
		"",
		"Use .agents/skills/devopsellence-rails-app for app-building guidance.",
		"Use .agents/skills/devopsellence for deploy, secrets, logs, status, rollback, and node operations.",
		"Stay inside the blessed Rails baseline: Rails 8.1, PostgreSQL, Hotwire, Tailwind, Solid Queue/Cache/Cable, Active Storage, Sentry, OpenTelemetry, Minitest, Docker, and mise.",
		"Do not add Redis, Sidekiq, React, GraphQL, Elasticsearch, Kubernetes, or an admin framework unless the product need is explicit.",
		"Before any production mutation, run devopsellence deploy --dry-run.",
		"After deploy, report devopsellence status, app logs, node logs, and HTTPS evidence when ingress is configured.",
		"",
	}, "\n")
}

func vibeAgentCommand(agent string) string {
	switch agent {
	case "codex":
		return `codex "$(cat .agents/prompts/devopsellence-vibe.md)"`
	case "claude":
		return `claude "$(cat .agents/prompts/devopsellence-vibe.md)"`
	case "pi":
		return `pi "$(cat .agents/prompts/devopsellence-vibe.md)"`
	default:
		return "cat .agents/prompts/devopsellence-vibe.md"
	}
}

func (a *App) launchVibeAgent(ctx context.Context, agent, cwd string) error {
	if agent == "generic" {
		return nil
	}
	binary := agent
	if _, err := a.LookPath(binary); err != nil {
		return ExitError{Code: 2, Err: fmt.Errorf("%s not found; rerun with --no-launch and start it manually from .agents/prompts/devopsellence-vibe.md", binary)}
	}
	prompt, err := os.ReadFile(filepath.Join(cwd, ".agents", "prompts", "devopsellence-vibe.md"))
	if err != nil {
		return fmt.Errorf("read vibe prompt: %w", err)
	}
	cmd := exec.CommandContext(ctx, binary, string(prompt))
	cmd.Dir = cwd
	cmd.Stdin = a.In
	cmd.Stdout = a.Printer.Err
	cmd.Stderr = a.Printer.Err
	return cmd.Run()
}

func vibeSlug(input string) string {
	slug := strings.ToLower(strings.TrimSpace(input))
	slug = vibeSlugPattern.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "vibe-app"
	}
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	return slug
}

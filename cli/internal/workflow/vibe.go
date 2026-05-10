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
	"time"

	"github.com/devopsellence/cli/internal/agentskill"
	"github.com/devopsellence/cli/internal/version"
)

type VibeOptions struct {
	Directory         string
	AIAgent           string
	AgentEffort       string
	AgentAutonomy     string
	Idea              string
	FirstWorkflow     string
	DeployGoal        string
	DevopsellenceMode string
	ServerStrategy    string
	ServerTarget      string
	Domain            string
	TLSEmail          string
	Services          string
	ProjectsDir       string
	TemplateVersion   string
	Launch            bool
	NoAgent           bool
	Force             bool
}

type vibeManifest struct {
	SchemaVersion    int                  `json:"schema_version"`
	AIAgent          string               `json:"ai_agent"`
	AgentEffort      string               `json:"agent_effort"`
	AgentAutonomy    string               `json:"agent_autonomy"`
	DetectedAgents   []string             `json:"detected_agents"`
	AppStack         string               `json:"app_stack"`
	TemplateURL      string               `json:"template_url"`
	TemplateVersion  string               `json:"template_version"`
	SkillDir         string               `json:"skill_dir"`
	PromptPath       string               `json:"prompt_path"`
	Idea             string               `json:"idea"`
	DeploymentIntent vibeDeploymentIntent `json:"deployment_intent"`
	NextCommands     []string             `json:"next_commands"`
}

type vibeDeploymentIntent struct {
	FirstWorkflow      string   `json:"first_workflow"`
	DeployGoal         string   `json:"deploy_goal"`
	DevopsellenceMode  string   `json:"devopsellence_mode"`
	ServerStrategy     string   `json:"server_strategy"`
	ServerTarget       string   `json:"server_target,omitempty"`
	Provider           string   `json:"provider,omitempty"`
	ProviderAuthStatus string   `json:"provider_auth_status,omitempty"`
	ProviderAuthSource string   `json:"provider_auth_source,omitempty"`
	Domain             string   `json:"domain"`
	TLSEmail           string   `json:"tls_email,omitempty"`
	Services           []string `json:"services"`
}

const (
	vibeAppStack                 = "rails-app"
	defaultVibeProjectsDirName   = "devopsellence-projects"
	defaultVibeAgentEffort       = "high"
	defaultVibeAgentAutonomy     = "builder"
	defaultVibeDeployGoal        = "deploy-ready"
	defaultVibeMode              = "solo"
	defaultVibeServerStrategy    = "none"
	defaultVibeTemplateVersion   = "v0.1.3"
	vibeDomainLater              = "later"
	vibePromptInstruction        = "Read .agents/prompts/devopsellence-vibe.md and follow it."
	defaultVibeAgentProbeTimeout = 5 * time.Second
)

var vibeSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)
var vibeAgentPreference = []string{"codex", "claude", "pi", "opencode"}

var errVibeAgentProbeTimeout = errors.New("agent readiness probe timed out")

func (a *App) Vibe(ctx context.Context, opts VibeOptions) error {
	opts.AIAgent = strings.ToLower(strings.TrimSpace(opts.AIAgent))
	agentEffort, err := normalizeVibeAgentEffort(opts.AgentEffort)
	if err != nil {
		return err
	}
	opts.AgentEffort = agentEffort
	agentAutonomy, err := normalizeVibeAgentAutonomy(opts.AgentAutonomy)
	if err != nil {
		return err
	}
	opts.AgentAutonomy = agentAutonomy
	reader := bufio.NewReader(a.In)
	wizardMode := strings.TrimSpace(opts.Idea) == ""
	if wizardMode {
		_, _ = fmt.Fprintln(a.Printer.Err, "devopsellence vibe intake. Press Ctrl+C anytime before scaffolding to stop.")
	}
	var detectedAgents []string
	if opts.NoAgent {
		opts.AIAgent = "generic"
		opts.Launch = false
	} else {
		detectedAgents = a.detectVibeAgents(ctx)
	}
	if opts.AIAgent == "" && len(detectedAgents) > 0 {
		opts.AIAgent = detectedAgents[0]
	}
	if opts.AIAgent == "" {
		opts.AIAgent = "generic"
		opts.Launch = false
	}
	if !supportedVibeAgent(opts.AIAgent) {
		return ExitError{Code: 2, Err: fmt.Errorf("unsupported ai agent %q; use codex, claude, pi, opencode, or generic", opts.AIAgent)}
	}
	if opts.AIAgent == "generic" {
		opts.Launch = false
	}
	if opts.Launch {
		if err := a.ensureVibeAgentUsable(ctx, opts.AIAgent); err != nil {
			return err
		}
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
	intent, err := a.resolveVibeDeploymentIntent(opts)
	if err != nil {
		return err
	}
	opts.TemplateVersion = strings.TrimSpace(opts.TemplateVersion)
	if opts.TemplateVersion == "" {
		opts.TemplateVersion = defaultVibeTemplateVersion
	}
	templateURL := vibeTemplateURL(opts.TemplateVersion)
	if err := a.ensureVibeTools(); err != nil {
		return err
	}

	target, projectsDir, err := resolveVibeTarget(a.Cwd, opts.Directory, opts.Idea, opts.ProjectsDir)
	if err != nil {
		return err
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

	prompt := vibePrompt(opts.AIAgent, opts.AgentAutonomy, templateURL, opts.Idea, intent)
	promptPath := filepath.Join(promptsDir, "devopsellence-vibe.md")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}

	agentCommand := vibeAgentCommand(opts.AIAgent, opts.AgentEffort, opts.AgentAutonomy)
	nextCommands := vibeNextCommands(target, agentCommand, intent)
	manifest := vibeManifest{
		SchemaVersion:    outputSchemaVersion,
		AIAgent:          opts.AIAgent,
		AgentEffort:      opts.AgentEffort,
		AgentAutonomy:    opts.AgentAutonomy,
		DetectedAgents:   detectedAgents,
		AppStack:         vibeAppStack,
		TemplateURL:      templateURL,
		TemplateVersion:  opts.TemplateVersion,
		SkillDir:         filepath.Join(".agents", "skills"),
		PromptPath:       filepath.Join(".agents", "prompts", "devopsellence-vibe.md"),
		Idea:             opts.Idea,
		DeploymentIntent: intent,
		NextCommands:     nextCommands,
	}
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
		"schema_version":    outputSchemaVersion,
		"action":            "initialized",
		"directory":         target,
		"projects_dir":      projectsDir,
		"ai_agent":          opts.AIAgent,
		"agent_effort":      opts.AgentEffort,
		"agent_autonomy":    opts.AgentAutonomy,
		"detected_agents":   detectedAgents,
		"app_stack":         vibeAppStack,
		"template_url":      templateURL,
		"template_version":  opts.TemplateVersion,
		"skill_id":          agentskill.RailsAppID,
		"skill_name":        agentskill.RailsAppName,
		"skill":             agentskill.RailsAppName,
		"skill_dir":         skillsDir,
		"prompt_path":       promptPath,
		"manifest_path":     manifestPath,
		"deployment_intent": intent,
		"initial_commit":    initialCommit,
		"launch_requested":  opts.Launch,
		"launched":          false,
		"next_commands":     nextCommands,
	}
	var launchErr error
	if opts.Launch {
		launchErr = a.launchVibeAgent(ctx, opts.AIAgent, opts.AgentEffort, opts.AgentAutonomy, target)
		payload["launched"] = launchErr == nil
		if launchErr != nil {
			payload["launch_error"] = launchErr.Error()
		}
	}
	if err := a.Printer.PrintJSON(payload); err != nil {
		return err
	}
	return launchErr
}

func (a *App) askVibeQuestion(reader *bufio.Reader, label string) (string, error) {
	_, _ = fmt.Fprintf(a.Printer.Err, "%s: ", label)
	answer, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(answer) == "" {
		return "", ExitError{Code: 2, Err: fmt.Errorf("missing %s; pass it with a flag for non-interactive use", strings.ToLower(label))}
	}
	return strings.TrimSpace(answer), nil
}

func (a *App) resolveVibeDeploymentIntent(opts VibeOptions) (vibeDeploymentIntent, error) {
	firstWorkflow := strings.TrimSpace(opts.FirstWorkflow)
	deployGoal := strings.TrimSpace(opts.DeployGoal)
	mode := strings.TrimSpace(opts.DevopsellenceMode)
	serverStrategy := strings.TrimSpace(opts.ServerStrategy)
	serverTarget := strings.TrimSpace(opts.ServerTarget)
	domain := strings.TrimSpace(opts.Domain)
	tlsEmail := strings.TrimSpace(opts.TLSEmail)
	services := strings.TrimSpace(opts.Services)

	firstWorkflow = firstNonEmpty(strings.TrimSpace(firstWorkflow), "derive from the app idea")
	deployGoal, err := normalizeVibeDeployGoal(deployGoal)
	if err != nil {
		return vibeDeploymentIntent{}, err
	}
	mode, err = normalizeVibeMode(mode)
	if err != nil {
		return vibeDeploymentIntent{}, err
	}
	serverStrategy, err = normalizeVibeServerStrategy(serverStrategy)
	if err != nil {
		return vibeDeploymentIntent{}, err
	}
	if serverStrategy == "existing" {
		serverTarget = firstNonEmpty(serverTarget, "existing server to be confirmed")
	}
	if serverStrategy == "hetzner" {
		serverTarget = firstNonEmpty(serverTarget, "prod-1")
	}
	domain = normalizeVibeLater(domain)
	parsedServices, err := normalizeVibeServices(services)
	if err != nil {
		return vibeDeploymentIntent{}, err
	}
	intent := vibeDeploymentIntent{
		FirstWorkflow:     truncateVibeText(firstWorkflow, 2048),
		DeployGoal:        deployGoal,
		DevopsellenceMode: mode,
		ServerStrategy:    serverStrategy,
		ServerTarget:      truncateVibeText(serverTarget, 512),
		Domain:            truncateVibeText(domain, 512),
		TLSEmail:          truncateVibeText(tlsEmail, 512),
		Services:          parsedServices,
	}
	if intent.ServerStrategy == "hetzner" {
		intent.Provider = providerHetzner
		_, source, err := providerToken(a.ProviderState, providerHetzner)
		if err != nil {
			return vibeDeploymentIntent{}, err
		}
		if strings.TrimSpace(source) == "" {
			intent.ProviderAuthStatus = "missing"
		} else {
			intent.ProviderAuthStatus = "available"
			intent.ProviderAuthSource = source
		}
	}
	return intent, nil
}

func normalizeVibeDeployGoal(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = defaultVibeDeployGoal
	}
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "build", "build-only":
		return "build-only", nil
	case "deploy-ready", "prepare", "prepare-solo", "prepare-deploy":
		return "deploy-ready", nil
	case "dry-run", "deploy-dry-run":
		return "dry-run", nil
	case "deploy", "deploy-with-approval":
		return "deploy-with-approval", nil
	default:
		return "", ExitError{Code: 2, Err: fmt.Errorf("unsupported deploy goal %q; use build-only, deploy-ready, dry-run, or deploy-with-approval", value)}
	}
}

func normalizeVibeMode(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = defaultVibeMode
	}
	switch value {
	case "solo":
		return "solo", nil
	case "shared-later", "shared":
		return "shared-later", nil
	case "decide-later", "later", "decide":
		return "decide-later", nil
	default:
		return "", ExitError{Code: 2, Err: fmt.Errorf("unsupported devopsellence mode %q; use solo, shared-later, or decide-later", value)}
	}
}

func normalizeVibeServerStrategy(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = defaultVibeServerStrategy
	}
	switch value {
	case "none", "later", "no-server":
		return "none", nil
	case "existing", "existing-server":
		return "existing", nil
	case "hetzner", "provision-hetzner":
		return "hetzner", nil
	default:
		return "", ExitError{Code: 2, Err: fmt.Errorf("unsupported server plan %q; use none, existing, or hetzner", value)}
	}
}

func normalizeVibeLater(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, vibeDomainLater) || strings.EqualFold(value, "none") || strings.EqualFold(value, "no") {
		return vibeDomainLater
	}
	return value
}

func normalizeVibeServices(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{"later"}, nil
	}
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})
	seen := map[string]bool{}
	var services []string
	for _, part := range parts {
		service := strings.ToLower(strings.TrimSpace(part))
		if service == "" {
			continue
		}
		service = strings.ReplaceAll(service, "_", "-")
		switch service {
		case "later", "managed-postgres", "object-storage", "email", "cloudflare-dns":
		default:
			return nil, ExitError{Code: 2, Err: fmt.Errorf("unsupported external service %q; use later, managed-postgres, object-storage, email, or cloudflare-dns", service)}
		}
		if !seen[service] {
			seen[service] = true
			services = append(services, service)
		}
	}
	if len(services) == 0 {
		return []string{"later"}, nil
	}
	if len(services) > 1 && seen["later"] {
		filtered := services[:0]
		for _, service := range services {
			if service != "later" {
				filtered = append(filtered, service)
			}
		}
		services = filtered
	}
	return services, nil
}

func truncateVibeText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return strings.TrimSpace(string(runes[:max]))
}

func supportedVibeAgent(agent string) bool {
	switch agent {
	case "codex", "claude", "pi", "opencode", "generic":
		return true
	default:
		return false
	}
}

func (a *App) detectVibeAgents(ctx context.Context) []string {
	var agents []string
	for _, name := range vibeAgentPreference {
		if a.probeVibeAgent(ctx, name) == nil {
			agents = append(agents, name)
		}
	}
	return agents
}

func (a *App) ensureVibeAgentUsable(ctx context.Context, agent string) error {
	err := a.probeVibeAgent(ctx, agent)
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return ExitError{Code: 2, Err: fmt.Errorf("%s not found; rerun with --no-launch and start it manually from .agents/prompts/devopsellence-vibe.md", agent)}
	}
	if errors.Is(err, errVibeAgentProbeTimeout) {
		return ExitError{Code: 2, Err: fmt.Errorf("%s setup check timed out after %s; set DEVOPSELLENCE_VIBE_AGENT_PROBE_TIMEOUT=10s, or rerun with --no-launch and start it manually from .agents/prompts/devopsellence-vibe.md", agent, vibeAgentProbeTimeout())}
	}
	return ExitError{Code: 2, Err: fmt.Errorf("%s setup check failed (%v); check its login/config, or rerun with --no-launch and start it manually from .agents/prompts/devopsellence-vibe.md", agent, err)}
}

func (a *App) probeVibeAgent(ctx context.Context, agent string) error {
	if agent == "" || agent == "generic" {
		return errors.New("missing agent")
	}
	path, err := a.LookPath(agent)
	if err != nil {
		return err
	}
	args := vibeAgentProbeArgs(agent)
	timeout := vibeAgentProbeTimeout()
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, path, args...)
	err = cmd.Run()
	if probeCtx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%w after %s", errVibeAgentProbeTimeout, timeout)
	}
	return err
}

func vibeAgentProbeTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv("DEVOPSELLENCE_VIBE_AGENT_PROBE_TIMEOUT"))
	if value == "" {
		return defaultVibeAgentProbeTimeout
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return defaultVibeAgentProbeTimeout
	}
	return duration
}

func vibeAgentProbeArgs(agent string) []string {
	switch agent {
	case "codex":
		return []string{"login", "status"}
	case "claude":
		return []string{"auth", "status"}
	case "opencode":
		return []string{"providers", "list"}
	default:
		return []string{"--version"}
	}
}

func normalizeVibeAgentEffort(effort string) (string, error) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return defaultVibeAgentEffort, nil
	}
	switch effort {
	case "default", "low", "medium", "high", "xhigh":
		return effort, nil
	default:
		return "", ExitError{Code: 2, Err: fmt.Errorf("unsupported agent effort %q; use default, low, medium, high, or xhigh", effort)}
	}
}

func normalizeVibeAgentAutonomy(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.Join(strings.Fields(value), "-")
	if value == "" || value == "default" {
		value = defaultVibeAgentAutonomy
	}
	switch value {
	case "careful":
		return "careful", nil
	case "builder", "build":
		return "builder", nil
	case "full", "full-access":
		return "full-access", nil
	default:
		return "", ExitError{Code: 2, Err: fmt.Errorf("unsupported agent autonomy %q; use careful, builder, or full-access", value)}
	}
}

func resolveVibeTarget(cwd, directory, idea, projectsDir string) (string, string, error) {
	target := strings.TrimSpace(directory)
	if target == "" {
		target = vibeSlug(idea)
	}
	if isExplicitVibePath(target) {
		expanded, err := expandVibePath(cwd, target)
		return expanded, "", err
	}
	resolvedProjectsDir, err := resolveVibeProjectsDir(cwd, projectsDir)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(resolvedProjectsDir, target), resolvedProjectsDir, nil
}

func isExplicitVibePath(path string) bool {
	if filepath.IsAbs(path) || path == "." || path == ".." || strings.HasPrefix(path, "~") {
		return true
	}
	return strings.Contains(path, "/") || strings.Contains(path, `\`)
}

func resolveVibeProjectsDir(cwd, configured string) (string, error) {
	dir := strings.TrimSpace(configured)
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv("DEVOPSELLENCE_PROJECTS_DIR"))
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", ExitError{Code: 2, Err: errors.New("cannot determine home directory; pass --projects-dir")}
		}
		dir = filepath.Join(home, defaultVibeProjectsDirName)
	}
	return expandVibePath(cwd, dir)
}

func expandVibePath(cwd, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return "", ExitError{Code: 2, Err: errors.New("cannot determine home directory")}
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	} else if strings.HasPrefix(path, "~") {
		return "", ExitError{Code: 2, Err: fmt.Errorf("unsupported home-relative path %q; use ~/path or an absolute path", path)}
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Clean(filepath.Join(cwd, path)), nil
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
		return ExitError{Code: 2, Err: fmt.Errorf("%s is not an inspectable directory: %w", path, err)}
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

func vibePrompt(agent, autonomy, templateURL, idea string, intent vibeDeploymentIntent) string {
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
	lines := []string{
		firstLine,
		"",
		"App idea:",
		idea,
		"",
		"Rails template: " + templateURL,
		"",
		"Deployment intent:",
		"- First workflow: " + intent.FirstWorkflow,
		"- devopsellence mode: " + intent.DevopsellenceMode,
		"- Build/deploy goal: " + intent.DeployGoal,
		"- Server plan: " + vibePromptServerPlan(intent),
		"- Domain: " + intent.Domain,
		"- TLS email: " + firstNonEmpty(intent.TLSEmail, "ask before configuring ingress"),
		"- External services: " + strings.Join(intent.Services, ", "),
		"",
		"Agent autonomy:",
		"- Level: " + vibeAgentAutonomyLabel(autonomy),
	}
	lines = append(lines, vibeAgentAutonomyPromptLines(autonomy)...)
	lines = append(lines,
		"",
		"Use .agents/skills/devopsellence-rails-app for app-building guidance.",
		"Use .agents/skills/devopsellence for deploy, secrets, logs, status, rollback, and node operations.",
		"Start by deriving the MVP and sequencing the work yourself. Write a short implementation plan, then begin building without asking the user to choose the task order unless product ambiguity blocks progress.",
		"Stay inside the blessed Rails baseline: Rails 8.1, PostgreSQL, Hotwire, Tailwind, Solid Queue/Cache/Cable, Active Storage, Sentry, OpenTelemetry, Minitest, Docker, and mise.",
		"Do not add Redis, Sidekiq, React, GraphQL, Elasticsearch, Kubernetes, or an admin framework unless the product need is explicit.",
		"",
		"Deployment rules:",
		"- Do not write provider tokens, API keys, passwords, or secret values into prompts, manifests, git, logs, or commits.",
		"- Before any production mutation, run devopsellence deploy --dry-run and summarize the plan.",
		"- Ask the user before provisioning nodes, changing DNS, setting secrets, or running a real deploy.",
	)
	lines = append(lines, vibeDeployGoalPromptLines(intent)...)
	lines = append(lines, vibeServerPromptLines(intent)...)
	lines = append(lines, vibeServicesPromptLines(intent)...)
	lines = append(lines,
		"- After deploy, report devopsellence status, app logs, node logs, and HTTPS evidence when ingress is configured.",
		"",
	)
	return strings.Join(lines, "\n")
}

func vibePromptServerPlan(intent vibeDeploymentIntent) string {
	switch intent.ServerStrategy {
	case "existing":
		return "existing server/node " + firstNonEmpty(intent.ServerTarget, "to be confirmed")
	case "hetzner":
		return "provision Hetzner node " + firstNonEmpty(intent.ServerTarget, "prod-1") + " (auth " + firstNonEmpty(intent.ProviderAuthStatus, "unknown") + ")"
	default:
		return "no server yet"
	}
}

func vibeAgentAutonomyLabel(autonomy string) string {
	switch autonomy {
	case "careful":
		return "careful"
	case "full-access":
		return "full access"
	default:
		return "builder"
	}
}

func vibeAgentAutonomyPromptLines(autonomy string) []string {
	switch autonomy {
	case "careful":
		return []string{
			"- Ask before most meaningful changes. Keep edits small and explain the next step before changing behavior.",
			"- Low-risk read-only inspection is okay without pausing.",
		}
	case "full-access":
		return []string{
			"- The agent command may run without sandbox or approval prompts. This is only appropriate inside an isolated VM, container, or disposable devbox.",
			"- Even with full access, ask before reading secrets, spending money, provisioning infrastructure, changing DNS, deploying to production, deleting data, or running destructive git commands.",
		}
	default:
		return []string{
			"- Edit project files and run local build/test commands without pausing for every step.",
			"- Ask before reading secrets, spending money, provisioning infrastructure, changing DNS, deploying to production, deleting data, or running destructive git commands.",
		}
	}
}

func vibeDeployGoalPromptLines(intent vibeDeploymentIntent) []string {
	switch intent.DeployGoal {
	case "build-only":
		return []string{"- Build and test the product locally. Do not initialize, provision, dry-run, or deploy devopsellence unless the user asks."}
	case "dry-run":
		return []string{"- After the app is ready, prepare devopsellence solo and run only devopsellence deploy --dry-run, then report what would happen."}
	case "deploy-with-approval":
		return []string{"- After the app is ready, prepare devopsellence solo, run devopsellence deploy --dry-run, ask for approval, and only then run devopsellence deploy."}
	case "deploy-ready":
		return []string{"- Make the app deploy-ready with devopsellence solo config, but stop before real deploy unless the user explicitly approves."}
	default:
		return []string{"- Make the app deploy-ready, but stop before real deploy unless the user explicitly approves."}
	}
}

func vibeServerPromptLines(intent vibeDeploymentIntent) []string {
	switch intent.ServerStrategy {
	case "existing":
		return []string{
			"- Target existing server/node: " + firstNonEmpty(intent.ServerTarget, "ask the user which server to use") + ".",
			"- If the node is not already registered, ask for SSH host/user/key details before running devopsellence node create.",
		}
	case "hetzner":
		lines := []string{
			"- Target provider: Hetzner.",
			"- Desired node name: " + firstNonEmpty(intent.ServerTarget, "prod-1") + ".",
		}
		if intent.ProviderAuthStatus == "available" {
			lines = append(lines, "- Hetzner auth appears available from "+intent.ProviderAuthSource+". Do not print or inspect the token value.")
		} else {
			lines = append(lines,
				"- Hetzner auth is missing. Stop before provisioning and ask the user to run `devopsellence provider login hetzner --token <token>` or set `DEVOPSELLENCE_HETZNER_API_TOKEN`/`HCLOUD_TOKEN`.",
			)
		}
		return lines
	default:
		return []string{"- No server is selected yet. Do not create or attach nodes until the user chooses existing server or Hetzner provisioning."}
	}
}

func vibeServicesPromptLines(intent vibeDeploymentIntent) []string {
	if len(intent.Services) == 0 || (len(intent.Services) == 1 && intent.Services[0] == "later") {
		return []string{"- External services are later. Keep the initial app local/portable and mark service follow-ups explicitly."}
	}
	var lines []string
	for _, service := range intent.Services {
		switch service {
		case "managed-postgres":
			lines = append(lines, "- Plan managed PostgreSQL before production data; keep local development simple until credentials are provided through devopsellence secrets.")
		case "object-storage":
			lines = append(lines, "- Plan S3-compatible object storage for uploads; do not commit access keys.")
		case "email":
			lines = append(lines, "- Plan transactional email provider setup; keep API keys in devopsellence secrets.")
		case "cloudflare-dns":
			lines = append(lines, "- Plan Cloudflare DNS changes only after the user confirms the zone and approves DNS mutation.")
		}
	}
	return lines
}

func vibeNextCommands(target, agentCommand string, intent vibeDeploymentIntent) []string {
	commands := []string{"cd " + shellQuote(target)}
	if intent.ServerStrategy == "hetzner" && intent.ProviderAuthStatus == "missing" {
		commands = append(commands, "devopsellence provider login hetzner --token <token>")
	}
	return append(commands, agentCommand)
}

func vibeAgentCommand(agent, effort, autonomy string) string {
	if agent == "generic" {
		return "cat .agents/prompts/devopsellence-vibe.md"
	}
	parts := []string{agent}
	args := append(vibeAgentAutonomyArgs(agent, autonomy), vibeAgentEffortArgs(agent, effort)...)
	args = append(args, vibeAgentPromptArgs(agent)...)
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || arg == effort {
			parts = append(parts, arg)
		} else {
			parts = append(parts, shellQuote(arg))
		}
	}
	return strings.Join(parts, " ")
}

func vibeAgentAutonomyArgs(agent, autonomy string) []string {
	switch agent {
	case "codex":
		switch autonomy {
		case "careful":
			return []string{"--sandbox", "workspace-write", "--ask-for-approval", "untrusted"}
		case "full-access":
			return []string{"--dangerously-bypass-approvals-and-sandbox"}
		default:
			return []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}
		}
	case "claude":
		switch autonomy {
		case "careful":
			return []string{"--permission-mode", "default"}
		case "full-access":
			return []string{"--dangerously-skip-permissions"}
		default:
			return []string{"--permission-mode", "auto"}
		}
	default:
		return nil
	}
}

func vibeAgentPromptArgs(agent string) []string {
	if agent == "opencode" {
		return []string{"--prompt", vibePromptInstruction}
	}
	return []string{vibePromptInstruction}
}

func vibeAgentEffortArgs(agent, effort string) []string {
	if effort == "" || effort == "default" || agent == "generic" {
		return nil
	}
	switch agent {
	case "codex":
		return []string{"-c", `model_reasoning_effort="` + effort + `"`}
	case "claude":
		return []string{"--effort", effort}
	case "pi":
		return []string{"--thinking", effort}
	default:
		return nil
	}
}

func (a *App) launchVibeAgent(ctx context.Context, agent, effort, autonomy, cwd string) error {
	if agent == "generic" {
		return nil
	}
	binary := agent
	if _, err := a.LookPath(binary); err != nil {
		return ExitError{Code: 2, Err: fmt.Errorf("%s not found; rerun with --no-launch and start it manually from .agents/prompts/devopsellence-vibe.md", binary)}
	}
	args := append(vibeAgentAutonomyArgs(agent, autonomy), vibeAgentEffortArgs(agent, effort)...)
	args = append(args, vibeAgentPromptArgs(agent)...)
	cmd := exec.CommandContext(ctx, binary, args...)
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

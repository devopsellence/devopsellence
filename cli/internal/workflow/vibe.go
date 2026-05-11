package workflow

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/devopsellence/cli/internal/agentskill"
	"github.com/devopsellence/cli/internal/version"
	"golang.org/x/term"
)

type VibeOptions struct {
	Directory         string
	Stack             string
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
	AppStackName     string               `json:"app_stack_name"`
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
	vibeRailsAppStack            = "rails-app"
	vibeRailsAppStackName        = "Rails app"
	vibeIndexPHPStack            = "index-php"
	vibeIndexPHPStackName        = "index.php"
	defaultVibeStack             = vibeRailsAppStack
	defaultVibeProjectsDirName   = "devopsellence-projects"
	defaultVibeAgentEffort       = "high"
	defaultVibeAgentAutonomy     = "builder"
	defaultVibeDeployGoal        = "deploy-ready"
	defaultVibeMode              = "solo"
	defaultVibeServerStrategy    = "none"
	defaultVibeTemplateRef       = "master"
	vibeDomainLater              = "later"
	vibePromptInstruction        = "Read .agents/prompts/devopsellence-vibe.md and follow it."
	defaultVibeAgentProbeTimeout = 5 * time.Second
	minVibeIdeaLength            = 10
	maxVibeIdeaLength            = 4096
)

type vibeStackSpec struct {
	ID        string
	Name      string
	SkillID   string
	SkillName string
}

var vibeSlugPattern = regexp.MustCompile(`[^a-z0-9]+`)
var vibeAgentPreference = []string{"codex", "claude", "pi", "opencode"}

var errVibeAgentProbeTimeout = errors.New("agent readiness probe timed out")

func normalizeVibeStack(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	switch normalized {
	case "", "rails", "rails-app":
		return vibeRailsAppStack, nil
	case "index.php", "index-php", "php", "php-sqlite", "plain-php", "onefile-php":
		return vibeIndexPHPStack, nil
	default:
		return "", ExitError{Code: 2, Err: fmt.Errorf("unsupported vibe stack %q; use rails-app or index-php", value)}
	}
}

func vibeStack(id string) vibeStackSpec {
	switch id {
	case vibeIndexPHPStack:
		return vibeStackSpec{
			ID:        vibeIndexPHPStack,
			Name:      vibeIndexPHPStackName,
			SkillID:   agentskill.IndexPHPAppID,
			SkillName: agentskill.IndexPHPAppName,
		}
	default:
		return vibeStackSpec{
			ID:        vibeRailsAppStack,
			Name:      vibeRailsAppStackName,
			SkillID:   agentskill.RailsAppID,
			SkillName: agentskill.RailsAppName,
		}
	}
}

func (a *App) Vibe(ctx context.Context, opts VibeOptions) error {
	stack, err := normalizeVibeStack(opts.Stack)
	if err != nil {
		return err
	}
	stackSpec := vibeStack(stack)
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
	wizardMode := strings.TrimSpace(opts.Idea) == ""
	if wizardMode {
		_, _ = fmt.Fprintln(a.Printer.Err, "devopsellence vibe intake. Press Ctrl+C anytime before scaffolding to stop.")
	}
	detectedAgents := []string{}
	if opts.NoAgent {
		opts.AIAgent = "generic"
		opts.Launch = false
	} else if opts.AIAgent == "" {
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
		idea, err := a.askVibeQuestion(ctx, "App idea")
		if err != nil {
			return err
		}
		opts.Idea = idea
	}
	if strings.TrimSpace(opts.Idea) == "" {
		return ExitError{Code: 2, Err: errors.New("missing app idea")}
	}
	if utf8.RuneCountInString(opts.Idea) < minVibeIdeaLength {
		return ExitError{Code: 2, Err: fmt.Errorf("app idea is too short; write at least %d characters", minVibeIdeaLength)}
	}
	if utf8.RuneCountInString(opts.Idea) > maxVibeIdeaLength {
		return ExitError{Code: 2, Err: fmt.Errorf("app idea is too long; keep it under %d characters", maxVibeIdeaLength)}
	}
	intent, err := a.resolveVibeDeploymentIntent(opts)
	if err != nil {
		return err
	}
	templateVersion := defaultVibeTemplateVersion()
	templateURL := vibeTemplateURL(stackSpec.ID, templateVersion)
	if err := a.ensureVibeTools(stackSpec.ID); err != nil {
		return err
	}

	target, projectsDir, err := resolveVibeTarget(a.Cwd, opts.Directory, opts.Idea, opts.ProjectsDir)
	if err != nil {
		return err
	}
	if err := prepareVibeDirectory(target, opts.Force); err != nil {
		return err
	}
	if err := a.generateVibeApp(ctx, stackSpec.ID, target, templateURL, opts.Force); err != nil {
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
	if _, err := agentskill.Install(agentskill.InstallOptions{SkillsDir: skillsDir, Skill: stackSpec.SkillID}, version.String()); err != nil {
		return err
	}
	if err := ensureVibeAppSkill(target, stackSpec.SkillName); err != nil {
		return err
	}
	if err := ensureVibeGitignore(target); err != nil {
		return err
	}

	prompt := vibePrompt(opts.AIAgent, opts.AgentAutonomy, stackSpec, templateURL, opts.Idea, intent)
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
		AppStack:         stackSpec.ID,
		AppStackName:     stackSpec.Name,
		TemplateURL:      templateURL,
		TemplateVersion:  templateVersion,
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
	initialCommit, err := ensureInitialVibeCommit(ctx, target, vibeAppKind(stackSpec))
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
		"app_stack":         stackSpec.ID,
		"app_stack_name":    stackSpec.Name,
		"template_url":      templateURL,
		"template_version":  templateVersion,
		"skill_id":          stackSpec.SkillID,
		"skill_name":        stackSpec.SkillName,
		"skill":             stackSpec.SkillName,
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

func (a *App) askVibeQuestion(ctx context.Context, label string) (string, error) {
	prompt := label + ": "
	answer, terminal, err := a.askVibeTerminalQuestion(ctx, prompt)
	if !terminal {
		_, _ = fmt.Fprint(a.Printer.Err, prompt)
		answer, err = readVibeLine(ctx, bufio.NewReader(a.In))
	}
	if errors.Is(err, context.Canceled) {
		return "", vibeCanceledError()
	}
	if err != nil && strings.TrimSpace(answer) == "" {
		return "", ExitError{Code: 2, Err: fmt.Errorf("missing %s; pass it with a flag for non-interactive use", strings.ToLower(label))}
	}
	return strings.TrimSpace(answer), nil
}

func (a *App) askVibeTerminalQuestion(ctx context.Context, prompt string) (string, bool, error) {
	file, ok := a.In.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", false, nil
	}
	oldState, err := term.MakeRaw(int(file.Fd()))
	if err != nil {
		return "", false, nil
	}
	defer func() {
		_ = term.Restore(int(file.Fd()), oldState)
	}()
	if err := ctx.Err(); err != nil {
		return "", true, err
	}
	answer, err := readVibeTerminalLine(file, firstNonNilWriter(a.Printer.Err), prompt)
	if errors.Is(err, io.EOF) {
		_, _ = fmt.Fprintln(a.Printer.Err)
		return "", true, context.Canceled
	}
	return answer, true, err
}

func readVibeTerminalLine(reader io.Reader, writer io.Writer, prompt string) (string, error) {
	line, err := term.NewTerminal(readWriter{Reader: reader, Writer: writer}, prompt).ReadLine()
	if errors.Is(err, term.ErrPasteIndicator) {
		err = nil
	}
	return line, err
}

func readVibeLine(ctx context.Context, reader *bufio.Reader) (string, error) {
	type result struct {
		answer string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		answer, err := reader.ReadString('\n')
		done <- result{answer: answer, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-done:
		return result.answer, result.err
	}
}

func vibeCanceledError() error {
	return ExitError{Code: 130, Err: RenderedError{Err: context.Canceled}}
}

type readWriter struct {
	io.Reader
	io.Writer
}

func firstNonNilWriter(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
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

func ensureInitialVibeCommit(ctx context.Context, path, appKind string) (bool, error) {
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
	message := "Initial devopsellence " + appKind
	cmd := exec.CommandContext(ctx, "git", "-C", path, "-c", "user.name=devopsellence", "-c", "user.email=devopsellence@example.invalid", "commit", "-m", message)
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

func (a *App) ensureVibeTools(stack string) error {
	if _, err := a.LookPath("mise"); err != nil {
		return ExitError{Code: 2, Err: errors.New("mise not found; install mise before running devopsellence vibe: https://mise.jdx.dev/getting-started.html")}
	}
	if stack == vibeRailsAppStack {
		if _, err := a.LookPath("rails"); err != nil {
			return ExitError{Code: 2, Err: errors.New("rails not found; install Rails with mise-managed Ruby before running devopsellence vibe")}
		}
	}
	if _, err := a.LookPath("git"); err != nil {
		return ExitError{Code: 2, Err: errors.New("git not found; install git before running devopsellence vibe")}
	}
	return nil
}

func (a *App) generateVibeApp(ctx context.Context, stack, target, templateURL string, force bool) error {
	switch stack {
	case vibeIndexPHPStack:
		return generateVibeIndexPHPApp(target)
	default:
		return a.generateVibeRailsApp(ctx, target, templateURL, force)
	}
}

func (a *App) generateVibeRailsApp(ctx context.Context, target, templateURL string, force bool) error {
	args := []string{"new", target, "-d", "sqlite3", "-m", templateURL}
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

func generateVibeIndexPHPApp(target string) error {
	appName := vibeSlug(filepath.Base(target))
	files := map[string]string{
		".mise.toml":            vibeIndexPHPMiseTOML,
		".gitignore":            vibeIndexPHPGitignore,
		"README.md":             strings.ReplaceAll(vibeIndexPHPREADME, "{{APP_NAME}}", appName),
		"Dockerfile":            vibeIndexPHPDockerfile,
		"devopsellence.yml":     strings.ReplaceAll(vibeIndexPHPDevopsellenceYAML, "{{APP_NAME}}", appName),
		"public/index.php":      vibeIndexPHPIndex,
		"scripts/backup-sqlite": vibeIndexPHPBackupScript,
		"scripts/check":         vibeIndexPHPCheckScript,
	}
	for path, data := range files {
		fullPath := filepath.Join(target, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return fmt.Errorf("create %s parent: %w", path, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(path, "scripts/") {
			mode = 0o755
		}
		if err := os.WriteFile(fullPath, []byte(data), mode); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(target, "data"), 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	return nil
}

const vibeIndexPHPMiseTOML = `[tools]
php = "8.4"
`

const vibeIndexPHPGitignore = `.env
.env.*
!.env.example
data/*.sqlite
data/*.sqlite-*
data/*.db
data/*.db-*
backups/
`

const vibeIndexPHPDockerfile = `FROM nginx:latest

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
  && apt-get install -y --no-install-recommends php8.4-fpm php8.4-sqlite3 sqlite3 \
  && rm -rf /var/lib/apt/lists/* \
  && php_fpm="$(find /usr/sbin -maxdepth 1 -name 'php-fpm*' | sort -V | tail -1)" \
  && php_version="${php_fpm#/usr/sbin/php-fpm}" \
  && sed -i 's|^listen = .*|listen = 127.0.0.1:9000|' "/etc/php/${php_version}/fpm/pool.d/www.conf" \
  && printf '\nenv[APP_ENV] = $APP_ENV\nenv[DB_PATH] = $DB_PATH\n' >> "/etc/php/${php_version}/fpm/pool.d/www.conf" \
  && mkdir -p /app/data /var/www/html \
  && chown -R www-data:www-data /app/data /var/www/html

RUN cat > /etc/nginx/conf.d/default.conf <<'NGINX'
server {
    listen 80;
    server_name _;
    root /var/www/html;
    index index.php;

    location / {
        try_files $uri /index.php$is_args$args;
    }

    location ~ \.php$ {
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_param DOCUMENT_ROOT $document_root;
        fastcgi_pass 127.0.0.1:9000;
    }
}
NGINX

RUN cat > /usr/local/bin/start-index-php <<'SH'
#!/usr/bin/env sh
set -eu
mkdir -p /app/data
chown -R www-data:www-data /app/data
php_fpm="$(find /usr/sbin -maxdepth 1 -name 'php-fpm*' | sort -V | tail -1)"
"$php_fpm" -D
exec nginx -g 'daemon off;'
SH

RUN chmod +x /usr/local/bin/start-index-php

WORKDIR /var/www/html
COPY public/ /var/www/html/

ENV DB_PATH=/app/data/app.sqlite
EXPOSE 80
CMD ["start-index-php"]
`

const vibeIndexPHPDevopsellenceYAML = `schema_version: 1
organization: solo
project: {{APP_NAME}}
default_environment: production

build:
  context: .
  dockerfile: Dockerfile
  platforms:
    - linux/amd64

services:
  web:
    ports:
      - name: http
        port: 80
    healthcheck:
      path: /healthz
      port: 80
    volumes:
      - source: {{APP_NAME}}-data
        target: /app/data
    env:
      APP_ENV: production
      DB_PATH: /app/data/app.sqlite
`

const vibeIndexPHPREADME = `# {{APP_NAME}}

Tiny ` + "`index.php`" + ` app generated by devopsellence vibe.

## Local

` + "```sh" + `
mise install
php -S 127.0.0.1:8000 -t public
` + "```" + `

## Check

` + "```sh" + `
scripts/check
` + "```" + `

## Deploy

` + "```sh" + `
devopsellence deploy --dry-run
` + "```" + `

The SQLite database lives at ` + "`data/app.sqlite`" + ` locally and at ` + "`/app/data/app.sqlite`" + ` in production. Keep one writable web node while using SQLite.
`

const vibeIndexPHPIndex = `<?php
declare(strict_types=1);

$dbPath = getenv('DB_PATH') ?: dirname(__DIR__) . '/data/app.sqlite';
$dbDir = dirname($dbPath);
if (!is_dir($dbDir)) {
    mkdir($dbDir, 0775, true);
}

$db = new PDO('sqlite:' . $dbPath, null, null, [
    PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
    PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
]);
$db->exec('PRAGMA journal_mode=WAL');
$db->exec('PRAGMA busy_timeout=5000');
$db->exec('CREATE TABLE IF NOT EXISTS notes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    body TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)');

$path = parse_url($_SERVER['REQUEST_URI'] ?? '/', PHP_URL_PATH) ?: '/';
if ($path === '/healthz') {
    header('Content-Type: application/json');
    echo json_encode(['ok' => true]) . "\n";
    exit;
}

if (($_SERVER['REQUEST_METHOD'] ?? 'GET') === 'POST') {
    $body = trim((string)($_POST['body'] ?? ''));
    if ($body !== '') {
        $stmt = $db->prepare('INSERT INTO notes (body) VALUES (:body)');
        $stmt->execute(['body' => $body]);
    }
    header('Location: /', true, 303);
    exit;
}

$notes = $db->query('SELECT id, body, created_at FROM notes ORDER BY id DESC LIMIT 20')->fetchAll();

function h(string $value): string
{
    return htmlspecialchars($value, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8');
}
?><!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>index.php</title>
  <style>
    :root { color-scheme: light dark; font-family: ui-sans-serif, system-ui, sans-serif; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: Canvas; color: CanvasText; }
    main { width: min(720px, calc(100vw - 32px)); padding: 48px 0; }
    h1 { font-size: clamp(2rem, 7vw, 4.5rem); margin: 0 0 8px; letter-spacing: 0; }
    p { margin: 0 0 24px; color: color-mix(in srgb, CanvasText 72%, Canvas); }
    form { display: flex; gap: 8px; margin-bottom: 24px; }
    input { flex: 1; min-width: 0; padding: 12px 14px; border: 1px solid color-mix(in srgb, CanvasText 20%, Canvas); border-radius: 6px; font: inherit; }
    button { padding: 12px 16px; border: 0; border-radius: 6px; font: inherit; font-weight: 700; background: #1f7a4d; color: white; cursor: pointer; }
    ol { list-style: none; padding: 0; margin: 0; display: grid; gap: 8px; }
    li { border: 1px solid color-mix(in srgb, CanvasText 14%, Canvas); border-radius: 6px; padding: 12px 14px; }
    small { display: block; margin-top: 4px; color: color-mix(in srgb, CanvasText 58%, Canvas); }
  </style>
</head>
<body>
  <main>
    <h1>index.php</h1>
    <p>One PHP file, SQLite, one VM. Start small; split files only when the product earns it.</p>
    <form method="post">
      <input name="body" autocomplete="off" maxlength="240" placeholder="Ship a note" required>
      <button type="submit">Add</button>
    </form>
    <ol>
      <?php foreach ($notes as $note): ?>
        <li><?= h($note['body']) ?><small><?= h($note['created_at']) ?></small></li>
      <?php endforeach; ?>
    </ol>
  </main>
</body>
</html>
`

const vibeIndexPHPBackupScript = `#!/usr/bin/env bash
set -euo pipefail

db="${DB_PATH:-data/app.sqlite}"
if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 not found; install sqlite3 before running backups" >&2
  exit 127
fi
mkdir -p backups
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
sqlite3 "$db" ".backup 'backups/app-$stamp.sqlite'"
echo "backups/app-$stamp.sqlite"
`

const vibeIndexPHPCheckScript = `#!/usr/bin/env bash
set -euo pipefail

if command -v php >/dev/null 2>&1; then
  php -l public/index.php
elif command -v docker >/dev/null 2>&1; then
  docker run --rm -v "$PWD:/app" -w /app php:8.4-cli php -l public/index.php
else
  echo "php not found; install it with mise or run Docker before checking" >&2
  exit 127
fi
`

func ensureVibeAppSkill(target, skillName string) error {
	path := filepath.Join(target, ".agents", "skills", skillName, "SKILL.md")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("vibe scaffold did not install %s at %s", skillName, path)
		}
		return fmt.Errorf("inspect app skill: %w", err)
	}
	return nil
}

func vibeTemplateURL(stack, version string) string {
	switch stack {
	case vibeIndexPHPStack:
		return "https://github.com/devopsellence/devopsellence/tree/" + version + "/vibe-templates/index-php"
	default:
		return "https://raw.githubusercontent.com/devopsellence/devopsellence/" + version + "/vibe-templates/rails-app/template.rb"
	}
}

func defaultVibeTemplateVersion() string {
	value := strings.TrimSpace(version.Version)
	if value == "" || value == "dev" || value == "unknown" {
		return defaultVibeTemplateRef
	}
	return value
}

func vibePrompt(agent, autonomy string, stack vibeStackSpec, templateURL, idea string, intent vibeDeploymentIntent) string {
	var firstLine string
	appKind := vibeAppKind(stack)
	switch agent {
	case "codex":
		firstLine = "/goal Build this app idea into a deployable " + appKind + " using the installed " + stack.SkillName + " skill."
	case "claude":
		firstLine = "Run a tight build-test-deploy loop for this " + appKind + " idea using the installed " + stack.SkillName + " skill."
	case "pi":
		firstLine = "Use the installed " + stack.SkillName + " skill as the operating instructions for this app build."
	default:
		firstLine = "Build this " + appKind + " idea using the installed " + stack.SkillName + " skill."
	}
	lines := []string{
		firstLine,
		"",
		"App idea:",
		idea,
		"",
		"App stack: " + stack.Name + " (" + stack.ID + ")",
		"Template: " + templateURL,
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
		"Use .agents/skills/"+stack.SkillName+" for app-building guidance.",
		"Use .agents/skills/devopsellence for deploy, secrets, logs, status, rollback, and node operations.",
		vibePlanApprovalPromptLine(autonomy),
	)
	lines = append(lines, vibeStackPromptLines(stack.ID)...)
	lines = append(lines,
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

func vibeAppKind(stack vibeStackSpec) string {
	if strings.HasSuffix(strings.ToLower(stack.Name), " app") {
		return stack.Name
	}
	return stack.Name + " app"
}

func vibePlanApprovalPromptLine(autonomy string) string {
	if autonomy == "full-access" {
		return "Start by deriving the MVP and sequencing the work yourself. Write a short implementation plan, then begin building without asking the user to choose the task order unless product ambiguity blocks progress."
	}
	return "Start by deriving the MVP and sequencing the work yourself. Write a short implementation plan, then ask the user to confirm before changing app behavior or adding product code."
}

func vibeStackPromptLines(stack string) []string {
	switch stack {
	case vibeIndexPHPStack:
		return []string{
			"Stay inside the index.php baseline: PHP 8.4, nginx latest with PHP-FPM, one public/index.php entrypoint, PDO SQLite, no build step, Docker, and mise.",
			"Use jQuery only when it keeps the product simpler than plain JavaScript. Do not add Laravel, Symfony, React, Next.js, Vite, Node build tooling, Redis, or Postgres unless the product need is explicit.",
			"Keep the PHP security baseline explicit: PDO prepared statements for user-controlled SQL values, htmlspecialchars for HTML output, POST for state changes, CSRF tokens before sessions/auth/destructive forms, safe upload validation, and password_hash/password_verify if auth is added.",
			"Keep SQLite on one writable node with a persistent volume. Treat managed PostgreSQL, email, monitoring, and multi-node writes as stack-expansion follow-ups once the MVP needs them.",
			"When the MVP needs a real domain, public traffic, media, or abuse protection, prefer Cloudflare as the first edge/services expansion: domains, DNS, CDN caching, image resizing, R2 object storage, Stream video, Turnstile, WAF/DDoS, and Tunnel in front of the VPS.",
			"Keep the app runtime on a normal VPS, commonly Hetzner in solo mode; Cloudflare is the edge/services layer, not a second deployment system.",
			"Start as one file; split files only when the product earns it.",
		}
	default:
		return []string{
			"Stay inside the blessed Rails MVP baseline: Rails 8.1, SQLite by default, Hotwire, Tailwind, Solid Queue/Cache/Cable, Active Storage only when needed, Minitest, Docker, and mise.",
			"Treat managed PostgreSQL, backup/restore, object storage, Sentry, OpenTelemetry, Cloudflare DNS, and other external services as stack-expansion follow-ups once the MVP needs them.",
			"Do not add Redis, Sidekiq, React, GraphQL, Elasticsearch, Kubernetes, or an admin framework unless the product need is explicit.",
		}
	}
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

package workflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devopsellence/cli/internal/api"
	"github.com/devopsellence/cli/internal/auth"
	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/discovery"
	"github.com/devopsellence/cli/internal/docker"
	"github.com/devopsellence/cli/internal/git"
	"github.com/devopsellence/cli/internal/output"
	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/cli/internal/state"
	"github.com/devopsellence/cli/internal/ui"

	"gopkg.in/yaml.v3"
)

const OutputSchemaVersion = 1

const outputSchemaVersion = OutputSchemaVersion

var digestRefPattern = regexp.MustCompile(`\A(.+)@(sha256:[0-9a-f]{64})\z`)

var errOrganizationNotFound = errors.New("organization not found")

const (
	defaultDeployProgressPollInterval = 500 * time.Millisecond
	defaultDeployProgressTimeout      = 10 * time.Minute
	defaultNodeDiagnosePollInterval   = 500 * time.Millisecond
	defaultNodeDiagnoseWaitTimeout    = 20 * time.Second
	deployEnvVarsOverrideEnv          = "DEVOPSELLENCE_ENV_VARS"
)

type ExitError struct {
	Code int
	Err  error
}

func (e ExitError) Error() string {
	return e.Err.Error()
}

type App struct {
	In                  io.Reader
	Printer             output.Printer
	Auth                *auth.Manager
	API                 *api.Client
	State               *state.Store
	WorkspaceState      *state.Store
	ProviderState       *state.Store
	SoloState           *solo.StateStore
	ConfigStore         config.Store
	Docker              DockerClient
	Git                 git.Client
	Cwd                 string
	ExecutablePath      func() (string, error)
	LookPath            func(string) (string, error)
	Symlink             func(string, string) error
	DeployPollInterval  time.Duration
	DeployTimeout       time.Duration
	soloNodeCreateFn    func(context.Context, SoloNodeCreateOptions) error
	soloNodeAttachFn    func(context.Context, SoloNodeAttachOptions) error
	soloRuntimeDoctorFn func(context.Context, SoloDoctorOptions) error
	soloSecretResolveFn func(context.Context, solo.SecretRecord) (string, error)
}

type deployTimings struct {
	BuildPush time.Duration
	Total     time.Duration
}

type runtimeValueOverrides struct {
	All      map[string]string
	Services map[string]map[string]string
}

type deployBuildHeartbeat struct {
	mu      sync.Mutex
	stage   string
	started time.Time
}

type authCall func(func(string) error) error

type authSession struct {
	app    *App
	notify func(string)

	mu         sync.Mutex
	cond       *sync.Cond
	token      string
	refreshing bool
}

type DockerClient interface {
	Installed() bool
	DaemonReachable() bool
	Login(ctx context.Context, registryHost, username, password, configDir string) error
	WithTemporaryConfig(ctx context.Context, fn func(string) error) error
	BuildAndPush(ctx context.Context, contextPath, dockerfile, target string, platforms []string, configDir string, update, log func(string)) (string, error)
	ImageMetadata(ctx context.Context, reference string) (docker.ImageMetadata, error)
}

type InitOptions struct {
	Organization   string
	ProjectName    string
	Environment    string
	NonInteractive bool
}

type ConfigResolveOptions struct {
	Environment string
}

type DeployOptions struct {
	Organization   string
	Project        string
	Image          string
	Environment    string
	NonInteractive bool
}

type DeleteOptions struct {
	Organization string
	Project      string
	Environment  string
}

type StatusOptions struct {
	Organization string
	Project      string
	Environment  string
}

type ClaimOptions struct {
	Email string
}

type WhoamiOptions struct{}

type OrganizationListOptions struct{}

type OrganizationUseOptions struct {
	Name string
}

type OrganizationRegistryShowOptions struct {
	Organization string
}

type OrganizationRegistrySetOptions struct {
	Organization        string
	RegistryHost        string
	RepositoryNamespace string
	Username            string
	Password            string
	PasswordProvided    bool
	PasswordStdin       bool
	ExpiresAt           string
}

type ProjectListOptions struct {
	Organization string
}

type ProjectCreateOptions struct {
	Organization string
	Name         string
}

type ProjectDeleteOptions struct {
	Organization string
	Name         string
}

type ProjectUseOptions struct {
	Organization string
	Name         string
}

type EnvironmentListOptions struct {
	Organization string
	Project      string
}

type EnvironmentCreateOptions struct {
	Organization    string
	Project         string
	Name            string
	IngressStrategy string
}

type EnvironmentUseOptions struct {
	Organization string
	Project      string
	Name         string
}

type EnvironmentOpenOptions struct {
	Organization string
	Project      string
	Environment  string
}

type EnvironmentIngressOptions struct {
	Organization    string
	Project         string
	Environment     string
	IngressStrategy string
}

type NodeBootstrapOptions struct {
	Organization string
	Project      string
	Environment  string
	Unassigned   bool
}

type nodeBootstrapToken struct {
	Organization api.Organization
	Workspace    Workspace
	Initialized  *initializedWorkspace
	Result       map[string]any
}

type NodeListOptions struct {
	Organization string
}

type NodeAssignOptions struct {
	NodeID       int
	Organization string
	Project      string
	Environment  string
}

type NodeUnassignOptions struct {
	NodeID int
}

type NodeDeleteOptions struct {
	NodeID int
}

type NodeLabelSetOptions struct {
	NodeID int
	Labels string
}

type NodeDiagnoseOptions struct {
	NodeID int
	Wait   time.Duration
}

type SecretSetOptions struct {
	Organization  string
	Project       string
	Environment   string
	ServiceName   string
	Name          string
	Value         string
	ValueProvided bool
	ValueStdin    bool
}

type SecretListOptions struct {
	Organization string
	Project      string
	Environment  string
}

type SecretDeleteOptions struct {
	Organization string
	Project      string
	Environment  string
	ServiceName  string
	Name         string
}

type listedSecret struct {
	ServiceName string `json:"service_name"`
	Name        string `json:"name"`
	SecretRef   string `json:"secret_ref,omitempty"`
	Store       string `json:"store,omitempty"`
	Reference   string `json:"reference,omitempty"`
	Configured  bool   `json:"configured"`
	Stored      bool   `json:"stored"`
	Exposed     bool   `json:"exposed"`
}

type TokenCreateOptions struct {
	Name string
}

type TokenListOptions struct{}

type TokenRevokeOptions struct {
	ID int
}

type aliasInstallResult struct {
	AliasName  string
	AliasPath  string
	TargetPath string
}

type initializedWorkspace struct {
	Discovered     discovery.Result
	Config         config.ProjectConfig
	ConfigPath     string
	CreatedConfig  bool
	Organization   api.Organization
	Project        api.Project
	Environment    api.Environment
	CreatedOrg     bool
	CreatedProject bool
	CreatedEnv     bool
}

type resolvedDeployTarget struct {
	Organization   api.Organization
	Project        api.Project
	Environment    api.Environment
	CreatedOrg     bool
	CreatedProject bool
	CreatedEnv     bool
}

func NewApp(in io.Reader, out, err io.Writer, cwd string) *App {
	apiBase := firstNonEmpty(os.Getenv("DEVOPSELLENCE_BASE_URL"), authDefaultBase())
	loginBase := apiBase
	store := state.New(state.DefaultPath(filepath.Join("devopsellence", "auth.json")))
	workspaceStore := state.New(state.DefaultPath(filepath.Join("devopsellence", "workspace.json")))
	providerStore := state.New(state.DefaultPath(filepath.Join("devopsellence", "providers.json")))
	soloStateStore := solo.NewStateStore(solo.DefaultStatePath())
	return &App{
		In:                 in,
		Printer:            output.New(out, err),
		Auth:               auth.New(store, apiBase, loginBase),
		API:                api.New(apiBase),
		State:              store,
		WorkspaceState:     workspaceStore,
		ProviderState:      providerStore,
		SoloState:          soloStateStore,
		ConfigStore:        config.NewStore(),
		Docker:             docker.Runner{},
		Git:                git.Client{},
		Cwd:                cwd,
		ExecutablePath:     os.Executable,
		LookPath:           exec.LookPath,
		Symlink:            os.Symlink,
		DeployPollInterval: defaultDeployProgressPollInterval,
		DeployTimeout:      defaultDeployProgressTimeout,
	}
}

func (a *App) Login(ctx context.Context) error {
	tokens, err := a.Auth.Login(ctx, a.authLoginEvent)
	if err != nil {
		return ExitError{Code: 1, Err: err}
	}
	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"signed_in":      true,
		"api_base":       firstNonEmpty(tokens.APIBase, a.API.BaseURL),
	})
}

func (a *App) authLoginEvent(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	encoder := json.NewEncoder(a.Printer.Err)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(map[string]any{
		"schema_version": outputSchemaVersion,
		"operation":      "auth.login",
		"event":          "progress",
		"message":        message,
	})
}

func (a *App) Logout() error {
	deleted, err := a.Auth.Logout()
	if err != nil {
		return ExitError{Code: 1, Err: err}
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"deleted":        deleted,
		})
	}
	if deleted {
		a.Printer.Println("Signed out.")
		return nil
	}
	a.Printer.Println("Not signed in.")
	return nil
}

func (a *App) Whoami(ctx context.Context, _ WhoamiOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}

	result := map[string]any{
		"schema_version": outputSchemaVersion,
		"ok":             true,
		"account_kind":   firstNonEmpty(tokens.AccountKind, "unknown"),
		"auth_mode":      authMode(tokens),
		"api_base":       firstNonEmpty(tokens.APIBase, a.API.BaseURL),
	}
	if strings.TrimSpace(tokens.ExpiresAt) != "" {
		result["expires_at"] = tokens.ExpiresAt
	}
	if trialState := trialState(tokens); trialState != "" {
		result["trial_state"] = trialState
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}

	a.Printer.Println("Signed in.")
	a.Printer.Println("Account kind:", firstNonEmpty(tokens.AccountKind, "unknown"))
	a.Printer.Println("Auth mode:", authMode(tokens))
	a.Printer.Println("API base:", firstNonEmpty(tokens.APIBase, a.API.BaseURL))
	if strings.TrimSpace(tokens.ExpiresAt) != "" {
		a.Printer.Println("Expires at:", tokens.ExpiresAt)
	}
	if trialState := trialState(tokens); trialState != "" {
		a.Printer.Println("Trial state:", trialState)
	}
	return nil
}

func (a *App) AliasLFG(_ context.Context) error {
	result, err := a.installAlias("lfg")
	if err != nil {
		return ExitError{Code: 1, Err: err}
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"created":        true,
			"alias":          result.AliasName,
			"alias_path":     result.AliasPath,
			"target_path":    result.TargetPath,
		})
	}
	a.Printer.Println("Created lfg alias at " + result.AliasPath + ".")
	return nil
}

func (a *App) installAlias(aliasName string) (aliasInstallResult, error) {
	if existingPath, err := a.LookPath(aliasName); err == nil {
		return aliasInstallResult{}, fmt.Errorf("%s already exists at %s", aliasName, existingPath)
	} else if !errors.Is(err, exec.ErrNotFound) {
		return aliasInstallResult{}, fmt.Errorf("check %s on PATH: %w", aliasName, err)
	}

	targetPath, err := a.ExecutablePath()
	if err != nil {
		return aliasInstallResult{}, fmt.Errorf("resolve current executable: %w", err)
	}
	resolvedTargetPath, err := filepath.EvalSymlinks(targetPath)
	if err == nil {
		targetPath = resolvedTargetPath
	} else if !os.IsNotExist(err) {
		return aliasInstallResult{}, fmt.Errorf("resolve current executable symlinks: %w", err)
	}

	aliasPath := filepath.Join(filepath.Dir(targetPath), aliasName)
	if _, err := os.Lstat(aliasPath); err == nil {
		return aliasInstallResult{}, fmt.Errorf("%s already exists at %s", aliasName, aliasPath)
	} else if !os.IsNotExist(err) {
		return aliasInstallResult{}, fmt.Errorf("check alias path %s: %w", aliasPath, err)
	}

	if err := a.Symlink(filepath.Base(targetPath), aliasPath); err != nil {
		return aliasInstallResult{}, fmt.Errorf("create alias at %s: %w", aliasPath, err)
	}

	return aliasInstallResult{
		AliasName:  aliasName,
		AliasPath:  aliasPath,
		TargetPath: targetPath,
	}, nil
}

func (a *App) Init(ctx context.Context, opts InitOptions) error {
	renderer := ui.DefaultRenderer()
	tokens, err := a.ensureAuth(ctx, true)
	if err != nil {
		return err
	}
	var initialized initializedWorkspace
	var result map[string]any
	run := func(runCtx context.Context, update, _ func(string)) error {
		ctx := runCtx
		var initErr error
		initialized, initErr = a.initializeWorkspace(ctx, func(fn func(string) error) error {
			return a.callWithAuthRetry(ctx, &tokens.AccessToken, update, fn)
		}, opts, update)
		if initErr != nil {
			return initErr
		}
		result = map[string]any{
			"schema_version":       outputSchemaVersion,
			"organization_id":      initialized.Organization.ID,
			"organization_name":    initialized.Organization.Name,
			"organization_created": initialized.CreatedOrg,
			"project_id":           initialized.Project.ID,
			"project_name":         initialized.Project.Name,
			"project_created":      initialized.CreatedProject,
			"environment_id":       initialized.Environment.ID,
			"environment_name":     initialized.Environment.Name,
			"environment_created":  initialized.CreatedEnv,
			"config_path":          initialized.ConfigPath,
			"project_slug":         initialized.Discovered.ProjectSlug,
			"app_type":             initialized.Discovered.AppType,
			"fallback_used":        initialized.Discovered.FallbackUsed,
			"config":               initialized.Config,
		}
		return nil
	}

	err = run(ctx, func(string) {}, func(string) {})
	if err != nil {
		return err
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}

	a.Printer.Println(renderer.Success("Initialized " + initialized.Discovered.ProjectName))
	if initialized.Discovered.AppType == config.AppTypeRails && initialized.Discovered.FallbackUsed {
		a.Printer.Errorln("Could not infer Rails module name; using directory name", fmt.Sprintf("%q.", initialized.Discovered.ProjectName))
	}
	a.Printer.Println(ui.RenderCard(ui.Card{
		Title: "Workspace",
		Rows: []ui.Row{
			{Label: "Organization", Value: initialized.Organization.Name},
			{Label: "Project", Value: initialized.Project.Name},
			{Label: "Environment", Value: initialized.Environment.Name},
			{Label: "Config", Value: result["config_path"].(string)},
		},
	}))
	if initialized.CreatedOrg {
		a.Printer.Println("Created organization", initialized.Organization.Name)
	}
	if initialized.CreatedProject {
		a.Printer.Println("Created project", initialized.Project.Name)
	}
	if initialized.CreatedEnv {
		a.Printer.Println("Created environment", initialized.Environment.Name)
	}
	if initialized.Discovered.AppType == config.AppTypeGeneric {
		a.Printer.Println("Generic app detected. Review", result["config_path"].(string), "and adjust build/web settings before deploy if needed.")
	}
	return nil
}

func (a *App) Deploy(ctx context.Context, opts DeployOptions) error {
	renderer := ui.DefaultRenderer()
	startedAt := time.Now()
	var result map[string]any
	var accessToken string
	var deployTokens auth.Tokens
	var buildPushDuration time.Duration
	var autoInitSummary string
	run := func(runCtx context.Context, update, log func(string)) error {
		ctx := runCtx
		update("Checking Docker availability…")
		if strings.TrimSpace(opts.Image) == "" {
			if !a.Docker.Installed() {
				return errors.New("Docker CLI not found. Install a Docker-compatible local engine (for example Docker Desktop or OrbStack), or deploy a prebuilt image with:\n\n  devopsellence deploy --image docker.io/mccutchen/go-httpbin@sha256:809250d14e94397f4729f617931068a9ea048231fc1a11c9e3c7cb8c28bbab8d")
			}
			if !a.Docker.DaemonReachable() {
				return errors.New("Docker Engine is not running or not reachable. Start your Docker-compatible local engine and try again, or deploy a prebuilt image with:\n\n  devopsellence deploy --image docker.io/mccutchen/go-httpbin@sha256:809250d14e94397f4729f617931068a9ea048231fc1a11c9e3c7cb8c28bbab8d")
			}
		}

		update("Inspecting workspace…")
		discovered, err := discovery.Discover(a.Cwd)
		if err != nil {
			return err
		}

		var cfg config.ProjectConfig
		var initialized *initializedWorkspace
		existing, err := config.LoadFromRoot(discovered.WorkspaceRoot)
		if err != nil {
			if !strings.Contains(err.Error(), "schema_version") {
				return err
			}
			existing = nil
		}
		update("Checking git state…")
		if err := a.ensureDeployWorktreeClean(discovered, existing); err != nil {
			return err
		}

		preflightCfg := deployPreflightConfig(discovered, existing, opts)
		preflight, err := a.runDeployReadOnlyPreflight(ctx, opts, discovered.WorkspaceRoot, preflightCfg, update)
		if err != nil {
			return err
		}
		deployTokens = preflight.Tokens
		accessToken = preflight.Tokens.AccessToken
		session := newAuthSession(a, preflight.Tokens.AccessToken, update)
		withAuth := func(fn func(string) error) error {
			return session.Call(ctx, fn)
		}
		sha := preflight.GitSHA

		if existing == nil {
			update("No config found. Initializing workspace…")
			workspace, initErr := a.initializeWorkspace(ctx, withAuth, InitOptions{
				Organization:   opts.Organization,
				ProjectName:    opts.Project,
				Environment:    opts.Environment,
				NonInteractive: opts.NonInteractive,
			}, update)
			if initErr != nil {
				return initErr
			}
			discovered = workspace.Discovered
			cfg = workspace.Config
			initialized = &workspace
			autoInitSummary = "initialized workspace automatically (" + workspace.ConfigPath + ")"
		} else {
			update("Loading config…")
			cfg = *existing
		}
		selectedEnvironment := a.effectiveEnvironment(opts.Environment, &cfg)
		resolvedCfg, err := config.ResolveEnvironmentConfig(cfg, selectedEnvironment)
		if err != nil {
			return ExitError{Code: 1, Err: err}
		}
		cfg = resolvedCfg
		a.warnAboutPrebuiltImageConfig(opts, cfg)
		a.API.BaseURL = firstNonEmpty(preflight.Tokens.APIBase, a.API.BaseURL)

		envVarOverrides, err := parseRuntimeValueOverrides(os.Getenv(deployEnvVarsOverrideEnv), deployEnvVarsOverrideEnv)
		if err != nil {
			return err
		}
		if err := validateRuntimeOverrides(cfg, envVarOverrides, deployEnvVarsOverrideEnv); err != nil {
			return err
		}
		cfg = applyEnvVarOverrides(cfg, envVarOverrides)

		var (
			org     api.Organization
			project api.Project
			env     api.Environment
		)
		if initialized != nil {
			org = initialized.Organization
			project = initialized.Project
			env = initialized.Environment
		} else {
			update("Resolving deploy target…")
			target, err := a.resolveDeployTarget(ctx, withAuth, resolveDeployTargetInput{
				Organization: firstNonEmpty(opts.Organization, cfg.Organization),
				Project:      firstNonEmpty(opts.Project, cfg.Project),
				Environment:  cfg.DefaultEnvironment,
			}, update)
			if err != nil {
				return err
			}
			org = target.Organization
			project = target.Project
			env = target.Environment
		}

		repository, digest, resolvedBuildPushDuration, inferredImagePort, err := a.resolveImage(
			ctx,
			withAuth,
			discovered.WorkspaceRoot,
			project.ID,
			cfg,
			sha,
			firstNonEmpty(opts.Image),
			update,
			log,
		)
		if err != nil {
			return err
		}
		buildPushDuration = resolvedBuildPushDuration
		if updatedCfg, persisted, notice, inferErr := a.applyInferredHealthcheckConfig(discovered.WorkspaceRoot, cfg, initialized, inferredImagePort); inferErr != nil {
			return inferErr
		} else {
			cfg = updatedCfg
			if persisted != "" {
				autoInitSummary = joinNotices(autoInitSummary, persisted)
			}
			if notice != "" {
				update(notice)
			}
		}

		update("Creating release…")
		var release map[string]any
		if err := withAuth(func(token string) error {
			var callErr error
			release, callErr = a.API.CreateRelease(ctx, token, project.ID, api.ReleaseCreateRequest{
				GitSHA:          sha,
				ImageRepository: repository,
				ImageDigest:     digest,
				Services:        servicePayloads(cfg.Services),
				Tasks:           taskPayloads(cfg.Tasks),
				Ingress:         ingressPayload(cfg),
			})
			return callErr
		}); err != nil {
			return err
		}

		update("Publishing release…")
		releaseID := intFromMap(release, "id")
		requestToken, tokenErr := randomRequestToken()
		if tokenErr != nil {
			return ExitError{Code: 1, Err: fmt.Errorf("generate publish request token: %w", tokenErr)}
		}
		var publish map[string]any
		if err := withAuth(func(token string) error {
			var callErr error
			publish, callErr = a.publishReleaseWithRetry(ctx, token, releaseID, env.ID, requestToken)
			return callErr
		}); err != nil {
			return err
		}
		result = map[string]any{
			"schema_version": outputSchemaVersion,
			"organization": map[string]any{
				"id":   org.ID,
				"name": org.Name,
			},
			"project": map[string]any{
				"id":   project.ID,
				"name": project.Name,
			},
			"environment": map[string]any{
				"id":   env.ID,
				"name": env.Name,
			},
			"git_sha": sha,
			"image": map[string]any{
				"repository": repository,
				"digest":     digest,
			},
			"release_id":       releaseID,
			"deployment_id":    intFromMap(publish, "deployment_id"),
			"assigned_nodes":   intFromMap(publish, "assigned_nodes"),
			"status":           stringFromMap(publish, "status"),
			"status_message":   stringFromMap(publish, "status_message"),
			"public_url":       publicURL(publish),
			"trial_expires_at": stringFromMap(publish, "trial_expires_at"),
		}
		accessToken = session.AccessToken()
		return nil
	}

	var err error
	err = run(ctx, func(string) {}, func(string) {})
	if err != nil {
		return wrapError(err)
	}
	if !a.Printer.JSON && autoInitSummary != "" {
		a.Printer.Println("Deploy:", autoInitSummary)
	}
	if !a.Printer.JSON && stringFromMap(result, "public_url") != "" {
		a.Printer.Println("Ingress URL:", stringFromMap(result, "public_url"))
	}

	progress, err := a.waitForDeployment(ctx, accessToken, result)
	if err != nil {
		return wrapError(err)
	}
	timings := deployTimings{
		BuildPush: buildPushDuration,
		Total:     time.Since(startedAt),
	}
	result["rollout"] = deploymentProgressMap(progress)
	result["timings"] = map[string]any{
		"build_push_seconds":    timings.BuildPush.Seconds(),
		"control_plane_seconds": maxDuration(timings.Total-timings.BuildPush, 0).Seconds(),
		"total_seconds":         timings.Total.Seconds(),
	}
	if stringFromMap(result, "public_url") == "" && progress.Ingress != nil && progress.Ingress.PublicURL != "" {
		result["public_url"] = progress.Ingress.PublicURL
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	a.Printer.Println(renderer.Success("Deploy complete."))
	rows := []ui.Row{
		{Label: "Project", Value: nestedString(result, "project", "name")},
		{Label: "Environment", Value: nestedString(result, "environment", "name")},
		{Label: "Git SHA", Value: stringFromMap(result, "git_sha")},
		{Label: "Rollout", Value: fmt.Sprintf("%d/%d settled", progress.Summary.Settled, progress.Summary.AssignedNodes)},
		{Label: "Image Build/Push", Value: formatDuration(timings.BuildPush)},
		{Label: "Control Plane", Value: formatDuration(maxDuration(timings.Total-timings.BuildPush, 0))},
		{Label: "Total", Value: formatDuration(timings.Total)},
		{Label: "URL", Value: stringFromMap(result, "public_url")},
	}
	if trialExpiresAt := stringFromMap(result, "trial_expires_at"); trialExpiresAt != "" {
		rows = append(rows, ui.Row{Label: "Trial Expires", Value: trialExpiresAt})
	}
	a.Printer.Println(ui.RenderCard(ui.Card{
		Title: "Release",
		Rows:  rows,
	}))
	if warning := stringFromMap(result, "warning"); warning != "" {
		a.Printer.Println("Warning:", warning)
	}
	if a.shouldPrintClaimReminder(deployTokens, result) {
		a.Printer.Println("Claim this account before local state is lost: devopsellence auth claim --email you@example.com")
		_ = a.markClaimReminderShown(deployTokens.AnonymousID)
	}
	return nil
}

type deployReadOnlyPreflight struct {
	Tokens auth.Tokens
	GitSHA string
}

func deployPreflightConfig(discovered discovery.Result, existing *config.ProjectConfig, opts DeployOptions) config.ProjectConfig {
	if existing != nil {
		return *existing
	}
	return config.DefaultProjectConfigForType("", discovered.ProjectName, firstNonEmpty(opts.Environment, config.DefaultEnvironment), discovered.AppType)
}

func (a *App) runDeployReadOnlyPreflight(ctx context.Context, opts DeployOptions, workspaceRoot string, cfg config.ProjectConfig, update func(string)) (deployReadOnlyPreflight, error) {
	type authResult struct {
		tokens auth.Tokens
		err    error
	}
	type shaResult struct {
		sha string
		err error
	}
	authCh := make(chan authResult, 1)
	shaCh := make(chan shaResult, 1)
	validateCh := make(chan error, 1)

	update("Checking session…")
	go func() {
		tokens, err := a.ensureAuth(ctx, true)
		authCh <- authResult{tokens: tokens, err: err}
	}()

	update("Reading git commit…")
	go func() {
		sha, err := a.Git.CurrentSHA(workspaceRoot)
		shaCh <- shaResult{sha: sha, err: err}
	}()

	update("Validating deploy inputs…")
	go func() {
		validateCh <- a.validateDeployInputs(opts, workspaceRoot, cfg)
	}()

	authRes := <-authCh
	if authRes.err != nil {
		return deployReadOnlyPreflight{}, authRes.err
	}
	shaRes := <-shaCh
	if shaRes.err != nil {
		return deployReadOnlyPreflight{}, shaRes.err
	}
	if err := <-validateCh; err != nil {
		return deployReadOnlyPreflight{}, err
	}
	return deployReadOnlyPreflight{Tokens: authRes.tokens, GitSHA: shaRes.sha}, nil
}

func (a *App) validateDeployInputs(opts DeployOptions, workspaceRoot string, cfg config.ProjectConfig) error {
	if strings.TrimSpace(opts.Image) != "" {
		match := digestRefPattern.FindStringSubmatch(strings.TrimSpace(opts.Image))
		if len(match) != 3 {
			return ExitError{Code: 2, Err: errors.New("--image must include a digest ref like app@sha256:...")}
		}
		return nil
	}

	if !a.Docker.Installed() {
		return errors.New("Docker CLI not found. Install a Docker-compatible local engine (for example Docker Desktop or OrbStack), or deploy a prebuilt image with:\n\n  devopsellence deploy --image docker.io/mccutchen/go-httpbin@sha256:809250d14e94397f4729f617931068a9ea048231fc1a11c9e3c7cb8c28bbab8d")
	}
	if !a.Docker.DaemonReachable() {
		return errors.New("Docker Engine is not running or not reachable. Start your Docker-compatible local engine and try again, or deploy a prebuilt image with:\n\n  devopsellence deploy --image docker.io/mccutchen/go-httpbin@sha256:809250d14e94397f4729f617931068a9ea048231fc1a11c9e3c7cb8c28bbab8d")
	}

	contextPath := filepath.Join(workspaceRoot, cfg.Build.Context)
	dockerfilePath := filepath.Join(workspaceRoot, cfg.Build.Dockerfile)
	if _, err := os.Stat(contextPath); err != nil {
		return fmt.Errorf("build context not found: %s", contextPath)
	}
	if _, err := os.Stat(dockerfilePath); err != nil {
		return fmt.Errorf("dockerfile not found: %s", dockerfilePath)
	}
	if _, err := os.Stat(filepath.Join(contextPath, "Gemfile")); err == nil {
		if _, err := os.Stat(filepath.Join(contextPath, "Gemfile.lock")); err != nil {
			return ExitError{Code: 1, Err: errors.New("Gemfile.lock not found — run `bundle install` first")}
		}
	}
	return nil
}

func (a *App) Delete(ctx context.Context, opts DeleteOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	workspace, err := a.resolveWorkspace(ctx, tokens.AccessToken, opts.Organization, opts.Project, opts.Environment, false)
	if err != nil {
		return err
	}
	result, err := a.API.DeleteEnvironment(ctx, tokens.AccessToken, workspace.Environment.ID)
	if err != nil {
		return wrapError(err)
	}
	result["schema_version"] = outputSchemaVersion
	result["organization"] = map[string]any{"id": workspace.Organization.ID, "name": workspace.Organization.Name}
	result["project"] = map[string]any{"id": workspace.Project.ID, "name": workspace.Project.Name}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	a.Printer.Println("Deleted environment", workspace.Environment.Name+".")
	a.Printer.Println(
		"Customer nodes unassigned:", fmt.Sprintf("%d", len(anySlice(result["customer_node_ids"]))),
		"Managed servers scheduled for delete:", fmt.Sprintf("%d", len(anySlice(result["managed_node_ids"]))),
	)
	return nil
}

func (a *App) ensureDeployWorktreeClean(discovered discovery.Result, existing *config.ProjectConfig) error {
	entries, err := a.Git.StatusEntries(discovered.WorkspaceRoot, deployDirtyIgnorePaths(a.ConfigStore, discovered, existing))
	if err != nil {
		return ExitError{Code: 1, Err: err}
	}
	if len(entries) == 0 {
		return nil
	}

	if message := initGeneratedFilesCommitMessage(discovered, entries); message != "" {
		return ExitError{Code: 1, Err: errors.New(message)}
	}

	message := "git worktree has uncommitted changes. commit or stash before deploy:\n\n" + strings.Join(limitGitStatusEntries(entries, 10), "\n")
	if len(entries) > 10 {
		message += fmt.Sprintf("\n\nshowing first 10 of %d entries", len(entries))
	}
	return ExitError{Code: 1, Err: errors.New(message)}
}

func initGeneratedFilesCommitMessage(discovered discovery.Result, entries []string) string {
	if len(entries) == 0 {
		return ""
	}

	configPath := config.PathForType(discovered.WorkspaceRoot, discovered.AppType)
	relativeConfigPath, err := filepath.Rel(discovered.WorkspaceRoot, configPath)
	if err != nil {
		return ""
	}

	expected := map[string]struct{}{relativeConfigPath: {}}
	seen := map[string]struct{}{}
	for _, entry := range entries {
		path := strings.TrimSpace(strings.TrimPrefix(entry, "??"))
		path = strings.TrimSpace(strings.TrimPrefix(path, "A"))
		path = strings.TrimSpace(strings.TrimPrefix(path, "M"))
		if _, ok := expected[path]; !ok {
			return ""
		}
		seen[path] = struct{}{}
	}
	if len(seen) == 0 {
		return ""
	}

	paths := make([]string, 0, len(seen))
	for _, path := range []string{relativeConfigPath} {
		if _, ok := seen[path]; ok {
			paths = append(paths, path)
		}
	}
	return "workspace contains devopsellence setup files that are not committed yet. commit them before deploy:\n\n  git add " + strings.Join(paths, " ") + "\n  git commit -m \"Set up devopsellence\""
}

func deployDirtyIgnorePaths(store config.Store, discovered discovery.Result, existing *config.ProjectConfig) []string {
	var paths []string
	if existing != nil {
		return paths
	}

	configPath := store.PathForType(discovered.WorkspaceRoot, discovered.AppType)
	relativePath, err := filepath.Rel(discovered.WorkspaceRoot, configPath)
	if err == nil {
		paths = append(paths, relativePath)
	}
	return paths
}

func limitGitStatusEntries(entries []string, limit int) []string {
	if len(entries) <= limit {
		return entries
	}
	return entries[:limit]
}

func (a *App) waitForDeployment(ctx context.Context, token string, result map[string]any) (api.DeploymentProgress, error) {
	deploymentID := intFromMap(result, "deployment_id")
	if deploymentID <= 0 {
		return api.DeploymentProgress{}, ExitError{Code: 1, Err: errors.New("publish did not return a deployment id")}
	}

	timeout := a.DeployTimeout
	if timeout <= 0 {
		timeout = defaultDeployProgressTimeout
	}
	pollInterval := a.DeployPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultDeployProgressPollInterval
	}

	rolloutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return a.waitForDeploymentPlain(rolloutCtx, token, deploymentID, pollInterval)
}

func (a *App) waitForDeploymentPlain(ctx context.Context, token string, deploymentID int, pollInterval time.Duration) (api.DeploymentProgress, error) {
	var latest api.DeploymentProgress
	lastSummary := ""
	lastMilestone := ""
	nodeStates := map[int]string{}

	for {
		err := a.callWithAuthRetry(ctx, &token, nil, func(accessToken string) error {
			var callErr error
			latest, callErr = a.API.DeploymentProgress(ctx, accessToken, deploymentID)
			return callErr
		})
		if err != nil {
			if isTransientServerError(err) {
				select {
				case <-ctx.Done():
					if errors.Is(ctx.Err(), context.DeadlineExceeded) {
						return latest, ExitError{Code: 1, Err: rolloutTimeoutError(latest)}
					}
					return latest, ctx.Err()
				case <-time.After(pollInterval):
					continue
				}
			}
			return latest, err
		}

		if !a.Printer.JSON {
			summary := fmt.Sprintf("rollout pending=%d reconciling=%d settled=%d error=%d",
				latest.Summary.Pending,
				latest.Summary.Reconciling,
				latest.Summary.Settled,
				latest.Summary.Error,
			)
			if detail := deploymentStatusDetail(latest); detail != "" {
				summary += " - " + detail
			}
			if summary != lastSummary {
				a.Printer.Println(summary)
				lastSummary = summary
			}
			if milestone := rolloutMilestone(latest); milestone != "" && milestone != lastMilestone {
				a.Printer.Println("milestone:", milestone)
				lastMilestone = milestone
			}
			for _, node := range latest.Nodes {
				state := node.Phase + "|" + firstNonEmpty(node.Error, node.Message)
				if nodeStates[node.ID] == state {
					continue
				}
				a.Printer.Println(" ", firstNonEmpty(node.Name, fmt.Sprintf("node-%d", node.ID))+":", nodePhaseDetail(node))
				nodeStates[node.ID] = state
			}
		}

		if latest.Summary.Complete {
			return latest, nil
		}
		if latest.Summary.Failed {
			return latest, rolloutOutcome(latest)
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return latest, ExitError{Code: 1, Err: rolloutTimeoutError(latest)}
			}
			return latest, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func (a *App) Status(ctx context.Context, opts StatusOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	workspace, err := a.resolveWorkspace(ctx, tokens.AccessToken, opts.Organization, opts.Project, opts.Environment, false)
	if err != nil {
		return err
	}
	status, err := a.API.EnvironmentStatus(ctx, tokens.AccessToken, workspace.Environment.ID)
	if err != nil {
		return wrapError(err)
	}
	status["schema_version"] = outputSchemaVersion
	status["project_id"] = workspace.Project.ID
	if a.Printer.JSON {
		return a.Printer.PrintJSON(status)
	}
	rows := []ui.Row{
		{Label: "Organization", Value: nestedString(status, "organization", "name")},
		{Label: "Project", Value: nestedString(status, "project", "name")},
		{Label: "Environment", Value: nestedString(status, "environment", "name")},
		{Label: "Ingress", Value: nestedString(status, "environment", "ingress_strategy")},
		{Label: "Assigned", Value: fmt.Sprintf("%d", intFromMap(status, "assigned_nodes"))},
		{Label: "Release", Value: formatRelease(status["current_release"])},
		{Label: "Deployment", Value: formatDeployment(status["latest_deployment"])},
		{Label: "URL", Value: nestedString(status, "ingress", "public_url")},
	}
	if trialExpiresAt := stringFromMap(status, "trial_expires_at"); trialExpiresAt != "" {
		rows = append(rows, ui.Row{Label: "Trial Expires", Value: trialExpiresAt})
	}
	a.Printer.Println(ui.RenderCard(ui.Card{
		Title: "Environment",
		Rows:  rows,
	}))
	if warning := stringFromMap(status, "warning"); warning != "" {
		a.Printer.Println("Warning:", warning)
	}
	return nil
}

func (a *App) Doctor(ctx context.Context) error {
	checks := []map[string]any{}
	addCheck := func(name string, fn func() (string, error)) {
		detail, err := fn()
		check := map[string]any{"name": name, "detail": detail, "ok": err == nil}
		if err != nil {
			check["detail"] = err.Error()
		}
		checks = append(checks, check)
	}

	discovered, discoveryErr := discovery.Discover(a.Cwd)
	addCheck("workspace", func() (string, error) {
		if discoveryErr != nil {
			return "", discoveryErr
		}
		return discovered.AppType + " @ " + discovered.WorkspaceRoot, nil
	})
	addCheck("git", func() (string, error) {
		if discoveryErr != nil {
			return "", discoveryErr
		}
		return a.Git.CurrentSHA(discovered.WorkspaceRoot)
	})
	addCheck("docker_cli", func() (string, error) {
		if !a.Docker.Installed() {
			return "", errors.New("Docker CLI not found.")
		}
		return "docker installed", nil
	})
	addCheck("docker_daemon", func() (string, error) {
		if !a.Docker.DaemonReachable() {
			return "", errors.New("Docker daemon not reachable.")
		}
		return "docker daemon reachable", nil
	})

	var cfg config.Project
	var selectedEnvironment string
	addCheck("config", func() (string, error) {
		if discoveryErr != nil {
			return "", discoveryErr
		}
		loaded, err := a.ConfigStore.Fetch(discovered.WorkspaceRoot)
		if err != nil {
			return "", err
		}
		cfg = loaded
		selectedEnvironment = a.effectiveEnvironment("", &cfg)
		resolved, err := config.ResolveEnvironmentConfig(cfg, selectedEnvironment)
		if err != nil {
			return "", err
		}
		cfg = resolved
		return cfg.Organization + " / " + cfg.Project + " / " + selectedEnvironment, nil
	})

	var tokens auth.Tokens
	addCheck("auth", func() (string, error) {
		var err error
		tokens, err = a.Auth.ReadState()
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(tokens.AccessToken) == "" && strings.TrimSpace(tokens.RefreshToken) == "" {
			return "", errors.New("Not signed in. Run `devopsellence auth login`.")
		}
		if a.Auth.AccessTokenValid(tokens) {
			return "access token ready", nil
		}
		refreshed, err := a.Auth.Refresh(ctx, tokens)
		if err != nil {
			return "", errors.New("Session expired. Run `devopsellence auth login`.")
		}
		tokens = refreshed
		return "access token refreshed", nil
	})

	addCheck("organization", func() (string, error) {
		if strings.TrimSpace(cfg.Organization) == "" {
			return "", errors.New("organization unavailable")
		}
		org, err := a.findOrganizationByName(ctx, tokens.AccessToken, cfg.Organization)
		if err != nil {
			return "", err
		}
		return org.Name, nil
	})

	addCheck("environment", func() (string, error) {
		if strings.TrimSpace(cfg.Organization) == "" {
			return "", errors.New("environment unavailable")
		}
		org, err := a.findOrganizationByName(ctx, tokens.AccessToken, cfg.Organization)
		if err != nil {
			return "", err
		}
		_, env, err := a.findProjectEnvironment(ctx, tokens.AccessToken, org.ID, cfg.Project, selectedEnvironment)
		if err != nil {
			return "", err
		}
		return cfg.Project + " / " + env.Name, nil
	})

	ok := true
	for _, check := range checks {
		if passed, _ := check["ok"].(bool); !passed {
			ok = false
			break
		}
	}

	result := map[string]any{
		"schema_version": outputSchemaVersion,
		"ok":             ok,
		"checks":         checks,
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	for _, check := range checks {
		prefix := "FAIL"
		if check["ok"] == true {
			prefix = "OK"
		}
		a.Printer.Println(prefix, fmt.Sprintf("%v:", check["name"]), check["detail"])
	}
	return nil
}

func (a *App) ConfigResolve(opts ConfigResolveOptions) error {
	_, resolved, selectedEnvironment, err := a.resolvedWorkspaceConfig(opts.Environment)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version":       outputSchemaVersion,
			"selected_environment": selectedEnvironment,
			"config":               resolved,
		})
	}
	data, err := yaml.Marshal(resolved)
	if err != nil {
		return ExitError{Code: 1, Err: err}
	}
	fmt.Fprint(a.Printer.Out, string(data))
	return nil
}

func (a *App) NodeBootstrap(ctx context.Context, opts NodeBootstrapOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}

	var bootstrap nodeBootstrapToken
	run := func(ctx context.Context, update, _ func(string)) error {
		var err error
		bootstrap, err = a.createNodeBootstrapToken(ctx, &tokens, opts, update)
		if err != nil {
			return err
		}
		return nil
	}

	if err := run(ctx, func(string) {}, func(string) {}); err != nil {
		return err
	}

	result := bootstrap.Result
	organization := bootstrap.Organization
	workspace := bootstrap.Workspace
	result["schema_version"] = outputSchemaVersion
	result["organization_id"] = organization.ID
	result["organization_name"] = organization.Name
	if opts.Unassigned {
		result["assignment_mode"] = "unassigned"
	} else {
		result["project_name"] = workspace.Project.Name
		result["environment_id"] = workspace.Environment.ID
		result["environment_name"] = workspace.Environment.Name
		result["assignment_mode"] = firstNonEmpty(stringFromMap(result, "assignment_mode"), "environment")
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	if !opts.Unassigned && workspace.Discovery.AppType == config.AppTypeRails && workspace.Discovery.FallbackUsed {
		a.Printer.Errorln("Could not infer Rails module name; using directory name", fmt.Sprintf("%q.", workspace.Discovery.ProjectName))
	}
	if bootstrap.Initialized != nil {
		a.Printer.Println(ui.DefaultRenderer().Muted("Initialized workspace config for this app automatically."))
	}

	expiresAt := stringFromMap(result, "expires_at")
	if t, err := time.Parse(time.RFC3339, expiresAt); err == nil {
		local := t.Local()
		expiresAt = local.Format("Jan 2, 2006 at 3:04 PM ") + local.Format("MST")
	}

	rows := []ui.Row{
		{Label: "Organization", Value: organization.Name},
	}
	if opts.Unassigned {
		rows = append(rows, ui.Row{Label: "Assignment", Value: "Unassigned"})
	} else {
		rows = append(rows,
			ui.Row{Label: "Project", Value: workspace.Project.Name},
			ui.Row{Label: "Environment", Value: workspace.Environment.Name},
		)
	}
	rows = append(rows, ui.Row{Label: "Token expires", Value: expiresAt})
	a.Printer.Println(ui.RenderCard(ui.Card{Rows: rows}))
	a.Printer.Println("\n" + ui.DefaultRenderer().Muted("⚡ Run on your server to install the devopsellence agent and register it:"))
	r := ui.DefaultRenderer()
	a.Printer.Println(r.Accent(stringFromMap(result, "install_command")))
	a.Printer.Println("")
	if opts.Unassigned {
		a.Printer.Println(r.Muted("· Registers the node without assigning it to an environment"))
		a.Printer.Println(r.Muted("· Later: run `devopsellence node attach <id>` to attach it"))
	} else {
		a.Printer.Println(r.Muted("· Registers the node and auto-assigns it to the selected environment"))
	}
	a.Printer.Println(r.Muted("· Installs Docker Engine if absent (auto-install: Ubuntu 22.04/24.04 only)"))
	a.Printer.Println(r.Muted("  └ Other Linux distros: install Docker Engine manually before running"))
	a.Printer.Println(r.Muted("· Downloads and verifies the devopsellence agent binary"))
	a.Printer.Println(r.Muted("· Registers and starts a systemd service"))
	a.Printer.Println(r.Muted("· Requires: Linux x86_64 or arm64, sudo access"))
	a.Printer.Println("")
	return nil
}

func (a *App) createNodeBootstrapToken(ctx context.Context, tokens *auth.Tokens, opts NodeBootstrapOptions, update func(string)) (nodeBootstrapToken, error) {
	var (
		workspace    Workspace
		initialized  *initializedWorkspace
		organization api.Organization
		err          error
	)
	if opts.Unassigned {
		update("Resolving organization...")
		organization, err = a.resolveNodeBootstrapOrganization(ctx, tokens.AccessToken, opts.Organization)
		if err != nil {
			return nodeBootstrapToken{}, err
		}
	} else {
		workspace, initialized, err = a.ensureNodeBootstrapWorkspace(ctx, &tokens.AccessToken, opts, update)
		if err != nil {
			return nodeBootstrapToken{}, err
		}
		organization = workspace.Organization
	}
	if strings.TrimSpace(organization.PlanTier) == "trial" {
		return nodeBootstrapToken{}, ExitError{Code: 1, Err: errors.New("manual node management is unavailable for trial organizations; `devopsellence node register` is only available in paid organizations")}
	}

	update("Generating bootstrap token...")
	environmentID := 0
	if !opts.Unassigned {
		environmentID = workspace.Environment.ID
	}
	var result map[string]any
	err = a.callWithAuthRetry(ctx, &tokens.AccessToken, update, func(accessToken string) error {
		var callErr error
		result, callErr = a.API.CreateNodeBootstrapToken(ctx, accessToken, organization.ID, environmentID)
		return callErr
	})
	if err != nil {
		return nodeBootstrapToken{}, wrapError(err)
	}
	return nodeBootstrapToken{
		Organization: organization,
		Workspace:    workspace,
		Initialized:  initialized,
		Result:       result,
	}, nil
}

func (a *App) NodeList(ctx context.Context, opts NodeListOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	organization, err := a.resolveOrganizationReadOnly(ctx, tokens.AccessToken, opts.Organization)
	if err != nil {
		return err
	}

	nodes, err := a.API.ListNodes(ctx, tokens.AccessToken, organization.ID)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"organization": map[string]any{
				"id":   organization.ID,
				"name": organization.Name,
			},
			"nodes": nodes,
		})
	}
	if len(nodes) == 0 {
		a.Printer.Println("No nodes.")
		return nil
	}
	for _, node := range nodes {
		var assignment string
		if strings.TrimSpace(node.RevokedAt) != "" {
			assignment = " [revoked]"
		} else {
			envName := nestedString(node.Environment, "name")
			projectName := nestedString(node.Environment, "project_name")
			if envName != "" {
				if projectName != "" {
					assignment = " project=" + projectName + " env=" + envName
				} else {
					assignment = " env=" + envName
				}
			} else {
				assignment = " [unassigned]"
			}
		}
		a.Printer.Println(fmt.Sprintf("node #%d  %s  labels=%s%s", node.ID, firstNonEmpty(node.Name, "(unnamed)"), strings.Join(node.Labels, ","), assignment))
	}
	return nil
}

func (a *App) NodeAssign(ctx context.Context, opts NodeAssignOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	if opts.NodeID <= 0 {
		return ExitError{Code: 2, Err: errors.New("node id required: node attach <id>")}
	}
	workspace, err := a.resolveWorkspace(ctx, tokens.AccessToken, opts.Organization, opts.Project, opts.Environment, false)
	if err != nil {
		return err
	}
	var onProgress func(string)
	if !a.Printer.JSON {
		onProgress = func(msg string) { a.Printer.Println(msg) }
	}
	result, err := a.API.CreateEnvironmentAssignment(ctx, tokens.AccessToken, workspace.Environment.ID, opts.NodeID, onProgress)
	if err != nil {
		return wrapError(err)
	}
	result["schema_version"] = outputSchemaVersion
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	if workspace.Discovery.AppType == config.AppTypeRails && workspace.Discovery.FallbackUsed {
		a.Printer.Errorln("Could not infer Rails module name; using directory name", fmt.Sprintf("%q.", workspace.Discovery.ProjectName))
	}
	a.Printer.Println(
		"Assigned node #" + strconv.Itoa(intFromMap(result, "node_id")) +
			" to env #" + strconv.Itoa(intFromMap(result, "environment_id")) +
			" (desired state: " + stringFromMap(result, "desired_state_uri") + ").",
	)
	return nil
}

func (a *App) NodeUnassign(ctx context.Context, opts NodeUnassignOptions) error {
	if opts.NodeID <= 0 {
		return ExitError{Code: 2, Err: errors.New("node id required: node detach <id>")}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	result, err := a.API.DeleteNodeAssignment(ctx, tokens.AccessToken, opts.NodeID)
	if err != nil {
		return wrapError(err)
	}
	result["schema_version"] = outputSchemaVersion
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	if managed, _ := result["managed"].(bool); managed {
		a.Printer.Println("Unassigned managed node #" + strconv.Itoa(intFromMap(result, "id")) + "; server scheduled for delete.")
		return nil
	}
	a.Printer.Println("Unassigned node #" + strconv.Itoa(intFromMap(result, "id")) + " from env #" + strconv.Itoa(intFromMap(result, "environment_id")) + ".")
	a.Printer.Println("Next step: run `devopsellence-agent uninstall --purge-runtime` on the node when you are ready to remove it.")
	return nil
}

func (a *App) NodeDelete(ctx context.Context, opts NodeDeleteOptions) error {
	if opts.NodeID <= 0 {
		return ExitError{Code: 2, Err: errors.New("node id required: node remove <id>")}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	result, err := a.API.DeleteNode(ctx, tokens.AccessToken, opts.NodeID)
	if err != nil {
		return wrapError(err)
	}
	result["schema_version"] = outputSchemaVersion
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	if managed, _ := result["managed"].(bool); managed {
		a.Printer.Println("Delete requested for managed node #" + strconv.Itoa(intFromMap(result, "id")) + "; server scheduled for delete.")
		return nil
	}
	a.Printer.Println("Removed node #" + strconv.Itoa(intFromMap(result, "id")) + ".")
	a.Printer.Println("If the agent is still installed, run `devopsellence-agent uninstall --purge-runtime` on the machine to clean it up.")
	return nil
}

func (a *App) NodeLabelSet(ctx context.Context, opts NodeLabelSetOptions) error {
	if opts.NodeID <= 0 {
		return ExitError{Code: 2, Err: errors.New("missing required option: --node")}
	}
	if strings.TrimSpace(opts.Labels) == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --labels")}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	result, err := a.API.UpdateNodeLabels(ctx, tokens.AccessToken, opts.NodeID, opts.Labels)
	if err != nil {
		return wrapError(err)
	}
	result["schema_version"] = outputSchemaVersion
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	a.Printer.Println("Updated node #"+strconv.Itoa(intFromMap(result, "id"))+" labels:", strings.Join(stringSlice(result["labels"]), ","))
	return nil
}

func (a *App) NodeDiagnose(ctx context.Context, opts NodeDiagnoseOptions) error {
	if opts.NodeID <= 0 {
		return ExitError{Code: 2, Err: errors.New("node id required: node diagnose <id>")}
	}

	waitTimeout := opts.Wait
	if waitTimeout <= 0 {
		waitTimeout = defaultNodeDiagnoseWaitTimeout
	}

	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}

	request, err := a.API.CreateNodeDiagnoseRequest(ctx, tokens.AccessToken, opts.NodeID)
	if err != nil {
		return wrapError(err)
	}

	deadline := time.NewTimer(waitTimeout)
	ticker := time.NewTicker(defaultNodeDiagnosePollInterval)
	defer deadline.Stop()
	defer ticker.Stop()

	for nodeDiagnosePending(request.Status) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ExitError{Code: 1, Err: fmt.Errorf("timed out waiting for diagnose request %d (last status: %s)", request.ID, request.Status)}
		case <-ticker.C:
			request, err = a.API.GetNodeDiagnoseRequest(ctx, tokens.AccessToken, request.ID)
			if err != nil {
				return wrapError(err)
			}
		}
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"request":        request,
		})
	}

	a.printNodeDiagnose(request)
	if request.Status == "failed" {
		return ExitError{Code: 1, Err: errors.New(firstNonEmpty(request.ErrorMessage, "node diagnose failed"))}
	}
	return nil
}

func (a *App) SecretSet(ctx context.Context, opts SecretSetOptions) error {
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --service")}
	}
	if strings.TrimSpace(opts.Name) == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --name")}
	}
	value, err := a.secretValue(opts)
	if err != nil {
		return err
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	workspace, err := a.resolveWorkspace(ctx, tokens.AccessToken, opts.Organization, opts.Project, opts.Environment, false)
	if err != nil {
		return err
	}
	if err := a.requireConfigurableSecretRef(workspace.Discovery.WorkspaceRoot, serviceName, opts.Name); err != nil {
		return err
	}
	result, err := a.API.UpsertEnvironmentSecret(ctx, tokens.AccessToken, workspace.Environment.ID, serviceName, opts.Name, value)
	if err != nil {
		return wrapError(err)
	}
	configUpdated := false
	configUpdateErr := ""
	if ref := stringFromMap(result, "secret_ref"); ref != "" {
		updated, err := a.upsertWorkspaceSecretRef(workspace.Discovery.WorkspaceRoot, serviceName, config.SecretRef{Name: opts.Name, Secret: ref})
		if err != nil {
			configUpdateErr = err.Error()
		} else {
			configUpdated = updated
		}
		result["config_updated"] = updated
		result["config_path"] = a.ConfigStore.PathFor(workspace.Discovery.WorkspaceRoot)
		if configUpdateErr != "" {
			result["config_error"] = configUpdateErr
		}
	}
	result["schema_version"] = outputSchemaVersion
	if a.Printer.JSON {
		if err := a.Printer.PrintJSON(result); err != nil {
			return err
		}
		if configUpdateErr != "" {
			return ExitError{Code: 1, Err: fmt.Errorf("secret saved, but devopsellence.yml was not updated: %s", configUpdateErr)}
		}
		return nil
	}
	if workspace.Discovery.AppType == config.AppTypeRails && workspace.Discovery.FallbackUsed {
		a.Printer.Errorln("Could not infer Rails module name; using directory name", fmt.Sprintf("%q.", workspace.Discovery.ProjectName))
	}
	a.Printer.Println("Saved secret", stringFromMap(result, "name"), "for", stringFromMap(result, "service_name")+".")
	a.Printer.Println("Ref:", stringFromMap(result, "secret_ref"))
	if configUpdateErr != "" {
		a.Printer.Errorln("Secret saved, but devopsellence.yml was not updated:", configUpdateErr)
		return ExitError{Code: 1, Err: fmt.Errorf("secret saved, but devopsellence.yml was not updated: %s", configUpdateErr)}
	}
	if configUpdated {
		a.Printer.Println("Updated:", a.ConfigStore.PathFor(workspace.Discovery.WorkspaceRoot))
	}
	return nil
}

func (a *App) SecretList(ctx context.Context, opts SecretListOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	workspace, err := a.resolveWorkspace(ctx, tokens.AccessToken, opts.Organization, opts.Project, opts.Environment, false)
	if err != nil {
		return err
	}
	secrets, err := a.API.ListEnvironmentSecrets(ctx, tokens.AccessToken, workspace.Environment.ID)
	if err != nil {
		return wrapError(err)
	}
	items, err := a.sharedSecretListItems(workspace.Discovery.WorkspaceRoot, secrets)
	if err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"secrets":        items,
		})
	}
	if workspace.Discovery.AppType == config.AppTypeRails && workspace.Discovery.FallbackUsed {
		a.Printer.Errorln("Could not infer Rails module name; using directory name", fmt.Sprintf("%q.", workspace.Discovery.ProjectName))
	}
	if len(items) == 0 {
		a.Printer.Println("No secrets configured.")
		return nil
	}
	for _, item := range items {
		a.Printer.Println(formatListedSecret(item))
	}
	return nil
}

func (a *App) SecretDelete(ctx context.Context, opts SecretDeleteOptions) error {
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --service")}
	}
	if strings.TrimSpace(opts.Name) == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --name")}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	workspace, err := a.resolveWorkspace(ctx, tokens.AccessToken, opts.Organization, opts.Project, opts.Environment, false)
	if err != nil {
		return err
	}
	if err := a.requireConfiguredService(workspace.Discovery.WorkspaceRoot, serviceName); err != nil {
		return err
	}
	result, err := a.API.DeleteEnvironmentSecret(ctx, tokens.AccessToken, workspace.Environment.ID, serviceName, opts.Name)
	if err != nil {
		return wrapError(err)
	}
	configUpdated, err := a.removeWorkspaceSecretRef(workspace.Discovery.WorkspaceRoot, serviceName, opts.Name)
	configUpdateErr := ""
	if err != nil {
		configUpdateErr = err.Error()
		configUpdated = false
	}
	result["config_updated"] = configUpdated
	result["config_path"] = a.ConfigStore.PathFor(workspace.Discovery.WorkspaceRoot)
	if configUpdateErr != "" {
		result["config_error"] = configUpdateErr
	}
	result["schema_version"] = outputSchemaVersion
	if a.Printer.JSON {
		if err := a.Printer.PrintJSON(result); err != nil {
			return err
		}
		if configUpdateErr != "" {
			return ExitError{Code: 1, Err: fmt.Errorf("secret deleted, but devopsellence.yml was not updated: %s", configUpdateErr)}
		}
		return nil
	}
	if workspace.Discovery.AppType == config.AppTypeRails && workspace.Discovery.FallbackUsed {
		a.Printer.Errorln("Could not infer Rails module name; using directory name", fmt.Sprintf("%q.", workspace.Discovery.ProjectName))
	}
	a.Printer.Println("Deleted secret", stringFromMap(result, "name"), "for", stringFromMap(result, "service_name")+".")
	if configUpdateErr != "" {
		a.Printer.Errorln("Secret deleted, but devopsellence.yml was not updated:", configUpdateErr)
		return ExitError{Code: 1, Err: fmt.Errorf("secret deleted, but devopsellence.yml was not updated: %s", configUpdateErr)}
	}
	if configUpdated {
		a.Printer.Println("Updated:", a.ConfigStore.PathFor(workspace.Discovery.WorkspaceRoot))
	}
	return nil
}

func (a *App) sharedSecretListItems(workspaceRoot string, secrets []api.EnvironmentSecret) ([]listedSecret, error) {
	cfg, err := a.ConfigStore.Read(workspaceRoot)
	if err != nil {
		return nil, wrapError(err)
	}
	items := map[string]listedSecret{}
	if cfg != nil {
		for _, serviceName := range cfg.ServiceNames() {
			for _, ref := range cfg.Services[serviceName].SecretRefs {
				key := secretListKey(serviceName, ref.Name)
				items[key] = listedSecret{
					ServiceName: serviceName,
					Name:        ref.Name,
					SecretRef:   strings.TrimSpace(ref.Secret),
					Store:       secretListStore(strings.TrimSpace(ref.Secret), "managed"),
					Configured:  true,
					Exposed:     true,
				}
			}
		}
	}
	for _, secret := range secrets {
		key := secretListKey(secret.ServiceName, secret.Name)
		item := items[key]
		if item.ServiceName == "" {
			item = listedSecret{
				ServiceName: secret.ServiceName,
				Name:        secret.Name,
				Store:       "managed",
			}
		}
		if strings.TrimSpace(item.SecretRef) == "" {
			item.SecretRef = strings.TrimSpace(secret.SecretRef)
		}
		item.Reference = strings.TrimSpace(secret.SecretRef)
		item.Stored = true
		items[key] = item
	}
	return sortListedSecrets(items), nil
}

func secretListKey(serviceName, name string) string {
	return strings.TrimSpace(serviceName) + "\x00" + strings.TrimSpace(name)
}

func secretListStore(secretRef, fallback string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(secretRef)), "op://") {
		return solo.SecretStoreOnePassword
	}
	store, _, ok := parseDevopsellenceSecretRef(secretRef)
	if ok {
		return store
	}
	return fallback
}

func sortListedSecrets(items map[string]listedSecret) []listedSecret {
	result := make([]listedSecret, 0, len(items))
	for _, item := range items {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ServiceName != result[j].ServiceName {
			return result[i].ServiceName < result[j].ServiceName
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func formatListedSecret(item listedSecret) string {
	parts := []string{
		item.ServiceName,
		item.Name,
		"exposed=" + yesNo(item.Exposed),
		"configured=" + yesNo(item.Configured),
		"stored=" + yesNo(item.Stored),
	}
	if item.Store != "" {
		parts = append(parts, "store="+item.Store)
	}
	line := strings.Join(parts, " ")
	if item.SecretRef != "" {
		line += " -> " + item.SecretRef
	}
	return line
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func (a *App) requireConfiguredService(workspaceRoot, serviceName string) error {
	serviceName = strings.TrimSpace(serviceName)
	cfg, err := a.ConfigStore.Read(workspaceRoot)
	if err != nil {
		return wrapError(err)
	}
	if cfg == nil {
		return ExitError{Code: 2, Err: errors.New("missing devopsellence.yml; run `devopsellence setup` first")}
	}
	if _, ok := cfg.Services[serviceName]; !ok {
		return ExitError{Code: 2, Err: fmt.Errorf("service %q not found in devopsellence.yml", serviceName)}
	}
	return nil
}

func (a *App) requireConfigurableSecretRef(workspaceRoot, serviceName, name string) error {
	serviceName = strings.TrimSpace(serviceName)
	cfg, err := a.ConfigStore.Read(workspaceRoot)
	if err != nil {
		return wrapError(err)
	}
	if cfg == nil {
		return ExitError{Code: 2, Err: errors.New("missing devopsellence.yml; run `devopsellence setup` first")}
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return ExitError{Code: 2, Err: fmt.Errorf("service %q not found in devopsellence.yml", serviceName)}
	}
	if serviceSecretRefConflict(service, name) {
		return ExitError{Code: 2, Err: fmt.Errorf("service %q already defines %s in env; remove it before adding a secret_ref with the same name", serviceName, name)}
	}
	return nil
}

func (a *App) upsertWorkspaceSecretRef(workspaceRoot, serviceName string, ref config.SecretRef) (bool, error) {
	serviceName = strings.TrimSpace(serviceName)
	cfg, err := a.ConfigStore.Read(workspaceRoot)
	if err != nil {
		return false, wrapError(err)
	}
	if cfg == nil {
		return false, ExitError{Code: 2, Err: errors.New("missing devopsellence.yml; run `devopsellence setup` first")}
	}
	if _, ok := cfg.Services[serviceName]; !ok {
		return false, ExitError{Code: 2, Err: fmt.Errorf("service %q not found in devopsellence.yml", serviceName)}
	}
	changed, err := ensureServiceSecretRef(cfg, serviceName, ref)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) removeWorkspaceSecretRef(workspaceRoot, serviceName, name string) (bool, error) {
	serviceName = strings.TrimSpace(serviceName)
	cfg, err := a.ConfigStore.Read(workspaceRoot)
	if err != nil {
		return false, wrapError(err)
	}
	if cfg == nil {
		return false, ExitError{Code: 2, Err: errors.New("missing devopsellence.yml; run `devopsellence setup` first")}
	}
	if _, ok := cfg.Services[serviceName]; !ok {
		return false, ExitError{Code: 2, Err: fmt.Errorf("service %q not found in devopsellence.yml", serviceName)}
	}
	if !removeServiceSecretRef(cfg, serviceName, name) {
		return false, nil
	}
	if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) Claim(ctx context.Context, opts ClaimOptions) error {
	email := strings.TrimSpace(opts.Email)
	if email == "" {
		return ExitError{Code: 1, Err: errors.New("claim email is required")}
	}

	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	result, err := a.API.StartAccountClaim(ctx, tokens.AccessToken, email)
	if err != nil {
		return wrapError(err)
	}
	result["schema_version"] = outputSchemaVersion
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	a.Printer.Println("Claim email sent to", stringFromMap(result, "email")+".")
	return nil
}

func (a *App) TokenCreate(ctx context.Context, opts TokenCreateOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	name := firstNonEmpty(opts.Name, "deploy")
	result, err := a.API.CreateToken(ctx, tokens.AccessToken, tokens.RefreshToken, name)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"token":          result.Token,
			"name":           result.Name,
			"created_at":     result.CreatedAt,
		})
	}
	a.Printer.Println("Token:", result.Token)
	a.Printer.Println("Name:", result.Name)
	a.Printer.Errorln("Save this token — it will not be shown again.")
	return nil
}

func (a *App) TokenList(ctx context.Context, _ TokenListOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	result, err := a.API.ListTokens(ctx, tokens.AccessToken)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"tokens":         result,
		})
	}
	if len(result) == 0 {
		a.Printer.Println("No tokens.")
		return nil
	}
	for _, token := range result {
		notes := []string{}
		if token.Current {
			notes = append(notes, "current")
		}
		if strings.TrimSpace(token.RevokedAt) != "" {
			notes = append(notes, "revoked")
		}
		if strings.TrimSpace(token.LastUsedAt) != "" {
			notes = append(notes, "last_used="+token.LastUsedAt)
		}
		suffix := ""
		if len(notes) > 0 {
			suffix = " [" + strings.Join(notes, ", ") + "]"
		}
		a.Printer.Println(fmt.Sprintf("#%d  %s  created=%s%s", token.ID, token.Name, token.CreatedAt, suffix))
	}
	return nil
}

func (a *App) TokenRevoke(ctx context.Context, opts TokenRevokeOptions) error {
	if opts.ID <= 0 {
		return ExitError{Code: 2, Err: errors.New("token id required: auth token revoke <id>")}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	result, err := a.API.RevokeToken(ctx, tokens.AccessToken, opts.ID)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"token":          result,
		})
	}
	a.Printer.Println(fmt.Sprintf("Revoked token #%d %s.", result.ID, result.Name))
	return nil
}

func (a *App) OrganizationList(ctx context.Context, _ OrganizationListOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	organizations, err := a.API.ListOrganizations(ctx, tokens.AccessToken)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organizations":  organizations,
		})
	}
	if len(organizations) == 0 {
		a.Printer.Println("No organizations.")
		return nil
	}
	for _, organization := range organizations {
		parts := []string{organization.Name}
		if strings.TrimSpace(organization.Role) != "" {
			parts = append(parts, "role="+organization.Role)
		}
		if strings.TrimSpace(organization.PlanTier) != "" {
			parts = append(parts, "plan="+organization.PlanTier)
		}
		a.Printer.Println(strings.Join(parts, "  "))
	}
	return nil
}

func (a *App) OrganizationUse(ctx context.Context, opts OrganizationUseOptions) error {
	discovered, cfg, err := a.requiredWorkspaceConfig()
	if err != nil {
		return wrapError(err)
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	organization, err := a.resolveOrganizationReadOnly(ctx, tokens.AccessToken, opts.Name)
	if err != nil {
		return err
	}
	cfg.Organization = organization.Name
	if _, err := a.ConfigStore.Write(discovered.WorkspaceRoot, cfg); err != nil {
		return wrapError(err)
	}
	_ = a.rememberOrganization(organization.ID)
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization": map[string]any{
				"id":   organization.ID,
				"name": organization.Name,
			},
			"config_path": a.ConfigStore.PathFor(discovered.WorkspaceRoot),
		})
	}
	a.Printer.Println("Using organization", organization.Name+".")
	return nil
}

func (a *App) OrganizationRegistryShow(ctx context.Context, opts OrganizationRegistryShowOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	existing, _ := a.workspaceConfigOrNil()
	organization, err := a.resolveOrganizationReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Organization })))
	if err != nil {
		return err
	}
	registryConfig, err := a.API.GetOrganizationRegistry(ctx, tokens.AccessToken, organization.ID)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization":   organization,
			"registry":       registryConfig,
		})
	}
	if !registryConfig.Configured {
		a.Printer.Println("Registry not configured for", organization.Name+".")
		return nil
	}
	a.Printer.Println("Registry host:", registryConfig.RegistryHost)
	a.Printer.Println("Namespace:", registryConfig.RepositoryNamespace)
	a.Printer.Println("Username:", registryConfig.Username)
	if strings.TrimSpace(registryConfig.ExpiresAt) != "" {
		a.Printer.Println("Expires at:", registryConfig.ExpiresAt)
	}
	return nil
}

func (a *App) OrganizationRegistrySet(ctx context.Context, opts OrganizationRegistrySetOptions) error {
	if strings.TrimSpace(opts.RegistryHost) == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --host")}
	}
	if strings.TrimSpace(opts.RepositoryNamespace) == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --namespace")}
	}
	if strings.TrimSpace(opts.Username) == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --username")}
	}
	password, err := a.registryPassword(opts)
	if err != nil {
		return err
	}

	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	existing, _ := a.workspaceConfigOrNil()
	organization, err := a.resolveOrganizationReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Organization })))
	if err != nil {
		return err
	}
	request := map[string]any{
		"registry_host":        opts.RegistryHost,
		"repository_namespace": opts.RepositoryNamespace,
		"username":             opts.Username,
		"password":             password,
	}
	if strings.TrimSpace(opts.ExpiresAt) != "" {
		request["expires_at"] = opts.ExpiresAt
	}

	registryConfig, err := a.API.UpsertOrganizationRegistry(ctx, tokens.AccessToken, organization.ID, request)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization":   organization,
			"registry":       registryConfig,
		})
	}
	a.Printer.Println("Updated registry config for", organization.Name+".")
	return nil
}

func (a *App) ProjectList(ctx context.Context, opts ProjectListOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	existing, _ := a.workspaceConfigOrNil()
	organization, err := a.resolveOrganizationReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Organization })))
	if err != nil {
		return err
	}
	projects, err := a.API.ListProjects(ctx, tokens.AccessToken, organization.ID)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization":   organization,
			"projects":       projects,
		})
	}
	if len(projects) == 0 {
		a.Printer.Println("No projects.")
		return nil
	}
	for _, project := range projects {
		a.Printer.Println(project.Name)
	}
	return nil
}

func (a *App) ProjectCreate(ctx context.Context, opts ProjectCreateOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return ExitError{Code: 2, Err: errors.New("project name required: devopsellence context project create <name>")}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	existing, _ := a.workspaceConfigOrNil()
	organization, err := a.resolveOrganizationReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Organization })))
	if err != nil {
		return err
	}
	project, err := a.API.CreateProject(ctx, tokens.AccessToken, organization.ID, opts.Name)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization":   organization,
			"project":        project,
		})
	}
	a.Printer.Println("Created project", project.Name+".")
	return nil
}

func (a *App) ProjectDelete(ctx context.Context, opts ProjectDeleteOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return ExitError{Code: 2, Err: errors.New("project name required: project delete <name>")}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	existing, _ := a.workspaceConfigOrNil()
	organization, err := a.resolveOrganizationReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Organization })))
	if err != nil {
		return err
	}
	project, err := a.findProjectByName(ctx, tokens.AccessToken, organization.ID, opts.Name)
	if err != nil {
		return wrapError(err)
	}
	result, err := a.API.DeleteProject(ctx, tokens.AccessToken, project.ID)
	if err != nil {
		return wrapError(err)
	}
	result["schema_version"] = outputSchemaVersion
	result["organization"] = organization
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	a.Printer.Println("Deleted project", project.Name+".")
	return nil
}

func (a *App) ProjectUse(ctx context.Context, opts ProjectUseOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return ExitError{Code: 2, Err: errors.New("project name required: project use <name>")}
	}
	discovered, cfg, err := a.requiredWorkspaceConfig()
	if err != nil {
		return wrapError(err)
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	organization, err := a.resolveOrganizationReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, cfg.Organization))
	if err != nil {
		return err
	}
	project, err := a.findProjectByName(ctx, tokens.AccessToken, organization.ID, opts.Name)
	if err != nil {
		return wrapError(err)
	}
	cfg.Organization = organization.Name
	cfg.Project = project.Name
	if _, err := a.ConfigStore.Write(discovered.WorkspaceRoot, cfg); err != nil {
		return wrapError(err)
	}
	_ = a.rememberOrganization(organization.ID)
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization":   organization,
			"project":        project,
			"config_path":    a.ConfigStore.PathFor(discovered.WorkspaceRoot),
		})
	}
	a.Printer.Println("Using project", project.Name+".")
	return nil
}

func (a *App) EnvironmentList(ctx context.Context, opts EnvironmentListOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	existing, _ := a.workspaceConfigOrNil()
	organization, project, err := a.resolveProjectReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Organization })), firstNonEmpty(opts.Project, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Project })))
	if err != nil {
		return err
	}
	environments, err := a.API.ListEnvironments(ctx, tokens.AccessToken, project.ID)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization":   organization,
			"project":        project,
			"environments":   environments,
		})
	}
	if len(environments) == 0 {
		a.Printer.Println("No environments.")
		return nil
	}
	for _, environment := range environments {
		a.Printer.Println(environment.Name)
	}
	return nil
}

func (a *App) EnvironmentCreate(ctx context.Context, opts EnvironmentCreateOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return ExitError{Code: 2, Err: errors.New("environment name required: env create <name>")}
	}
	ingressStrategy, err := normalizeIngressStrategy(opts.IngressStrategy)
	if err != nil {
		return ExitError{Code: 2, Err: err}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	existing, _ := a.workspaceConfigOrNil()
	organization, project, err := a.resolveProjectReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Organization })), firstNonEmpty(opts.Project, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Project })))
	if err != nil {
		return err
	}
	environment, err := a.API.CreateEnvironment(ctx, tokens.AccessToken, project.ID, opts.Name, ingressStrategy)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization":   organization,
			"project":        project,
			"environment":    environment,
		})
	}
	if environment.IngressStrategy != "" {
		a.Printer.Println("Created environment", environment.Name+" with ingress", environment.IngressStrategy+".")
		return nil
	}
	a.Printer.Println("Created environment", environment.Name+".")
	return nil
}

func (a *App) EnvironmentIngress(ctx context.Context, opts EnvironmentIngressOptions) error {
	ingressStrategy, err := normalizeIngressStrategy(opts.IngressStrategy)
	if err != nil {
		return ExitError{Code: 2, Err: err}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	workspace, err := a.resolveWorkspace(ctx, tokens.AccessToken, opts.Organization, opts.Project, opts.Environment, false)
	if err != nil {
		return err
	}
	environment, err := a.API.UpdateEnvironmentIngressStrategy(ctx, tokens.AccessToken, workspace.Environment.ID, ingressStrategy)
	if err != nil {
		return wrapError(err)
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"organization":   workspace.Organization,
			"project":        workspace.Project,
			"environment":    environment,
		})
	}
	a.Printer.Println("Updated environment", environment.Name, "ingress to", environment.IngressStrategy+".")
	return nil
}

func (a *App) EnvironmentUse(ctx context.Context, opts EnvironmentUseOptions) error {
	if strings.TrimSpace(opts.Name) == "" {
		return ExitError{Code: 2, Err: errors.New("environment name required: env use <name>")}
	}
	_, cfg, err := a.requiredWorkspaceConfig()
	if err != nil {
		return wrapError(err)
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	organization, project, err := a.resolveProjectReadOnly(ctx, tokens.AccessToken, firstNonEmpty(opts.Organization, cfg.Organization), firstNonEmpty(opts.Project, cfg.Project))
	if err != nil {
		return err
	}
	environment, err := a.findEnvironmentByName(ctx, tokens.AccessToken, project.ID, opts.Name)
	if err != nil {
		return wrapError(err)
	}
	if err := a.SetEnvironment(environment.Name); err != nil {
		return wrapError(err)
	}
	_ = a.rememberOrganization(organization.ID)
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version":      outputSchemaVersion,
			"ok":                  true,
			"organization":        organization,
			"project":             project,
			"environment":         environment,
			"workspace_key":       a.modeWorkspaceKey(),
			"default_environment": cfg.DefaultEnvironment,
		})
	}
	a.Printer.Println("Using environment", environment.Name+".")
	return nil
}

func (a *App) EnvironmentOpen(ctx context.Context, opts EnvironmentOpenOptions) error {
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	workspace, err := a.resolveWorkspace(ctx, tokens.AccessToken, opts.Organization, opts.Project, opts.Environment, false)
	if err != nil {
		return err
	}
	status, err := a.API.EnvironmentStatus(ctx, tokens.AccessToken, workspace.Environment.ID)
	if err != nil {
		return wrapError(err)
	}
	publicURL := nestedString(status, "ingress", "public_url")
	if strings.TrimSpace(publicURL) == "" {
		return ExitError{Code: 1, Err: errors.New("environment has no public URL")}
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             true,
			"url":            publicURL,
			"organization":   workspace.Organization,
			"project":        workspace.Project,
			"environment":    workspace.Environment,
		})
	}
	if err := a.Auth.OpenURL(publicURL); err != nil {
		return wrapError(err)
	}
	a.Printer.Println("Opened", publicURL)
	return nil
}

func (a *App) ensureAuth(ctx context.Context, allowAnonymousCreate bool) (auth.Tokens, error) {
	tokens, err := a.Auth.EnsureAuthenticated(ctx, allowAnonymousCreate, func(string) {})
	if err != nil {
		return auth.Tokens{}, ExitError{Code: 1, Err: err}
	}
	a.API.BaseURL = firstNonEmpty(tokens.APIBase, a.API.BaseURL)
	return tokens, nil
}

type resolveDeployTargetInput struct {
	Organization string
	Project      string
	Environment  string
}

func (a *App) resolveDeployTarget(ctx context.Context, callAuth authCall, input resolveDeployTargetInput, update func(string)) (resolvedDeployTarget, error) {
	request := func(preferredOrganizationID int) (api.DeployTargetResponse, error) {
		var response api.DeployTargetResponse
		err := callAuth(func(accessToken string) error {
			var callErr error
			response, callErr = a.API.ResolveDeployTarget(ctx, accessToken, input.Organization, input.Project, input.Environment, preferredOrganizationID)
			return callErr
		})
		return response, err
	}

	preferredOrganizationID, _ := a.lastOrganizationID()
	response, err := request(preferredOrganizationID)
	if err != nil {
		return resolvedDeployTarget{}, ExitError{Code: 1, Err: err}
	}
	_ = a.rememberOrganization(response.Organization.ID)
	return resolvedDeployTarget{
		Organization:   response.Organization,
		Project:        response.Project,
		Environment:    response.Environment,
		CreatedOrg:     response.OrganizationCreated,
		CreatedProject: response.ProjectCreated,
		CreatedEnv:     response.EnvironmentCreated,
	}, nil
}

func parseRuntimeValueOverrides(raw string, fieldName string) (runtimeValueOverrides, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return runtimeValueOverrides{}, nil
	}

	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		return runtimeValueOverrides{}, ExitError{Code: 2, Err: fmt.Errorf("%s must be valid JSON: %w", fieldName, err)}
	}

	object, ok := parsed.(map[string]any)
	if !ok {
		return runtimeValueOverrides{}, ExitError{Code: 2, Err: fmt.Errorf("%s must be a JSON object", fieldName)}
	}

	if values, err := stringMapFromAnyMap(object, fieldName); err == nil {
		return runtimeValueOverrides{All: values}, nil
	}

	overrides := runtimeValueOverrides{}
	overrides.Services = map[string]map[string]string{}
	for key, value := range object {
		scope := strings.TrimSpace(key)
		values, err := stringMapFromScopedValue(value, fieldName, scope)
		if err != nil {
			return runtimeValueOverrides{}, err
		}

		switch strings.ToLower(scope) {
		case "all":
			overrides.All = values
		default:
			overrides.Services[scope] = values
		}
	}

	return overrides, nil
}

func stringMapFromScopedValue(value any, fieldName string, scope string) (map[string]string, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, ExitError{Code: 2, Err: fmt.Errorf("%s.%s must be a JSON object", fieldName, scope)}
	}
	return stringMapFromAnyMap(object, fieldName+"."+scope)
}

func stringMapFromAnyMap(object map[string]any, fieldName string) (map[string]string, error) {
	values := map[string]string{}
	for key, value := range object {
		name := strings.TrimSpace(key)
		if name == "" {
			return nil, ExitError{Code: 2, Err: fmt.Errorf("%s keys must be present", fieldName)}
		}
		text, ok := value.(string)
		if !ok {
			return nil, ExitError{Code: 2, Err: fmt.Errorf("%s.%s must be a string", fieldName, name)}
		}
		values[name] = text
	}
	return values, nil
}

func validateRuntimeOverrides(cfg config.ProjectConfig, overrides runtimeValueOverrides, fieldName string) error {
	for serviceName := range overrides.Services {
		if _, ok := cfg.Services[serviceName]; !ok {
			return ExitError{Code: 2, Err: fmt.Errorf("%s.%s requires a matching service in devopsellence.yml", fieldName, serviceName)}
		}
	}
	return nil
}

func applyEnvVarOverrides(cfg config.ProjectConfig, overrides runtimeValueOverrides) config.ProjectConfig {
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		service.Env = mergeStringMaps(service.Env, overrides.All, overrides.Services[serviceName])
		cfg.Services[serviceName] = service
	}
	return cfg
}

func mergeStringMaps(parts ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, part := range parts {
		for key, value := range part {
			merged[key] = value
		}
	}
	return merged
}

func (a *App) callWithAuthRetry(ctx context.Context, token *string, notify func(string), fn func(string) error) error {
	if token == nil {
		return errors.New("missing access token")
	}
	err := fn(*token)
	if !authRetryable(err) {
		return err
	}

	tokens, authErr := a.reauthenticate(ctx, notify)
	if authErr != nil {
		return authErr
	}
	*token = tokens.AccessToken
	a.API.BaseURL = firstNonEmpty(tokens.APIBase, a.API.BaseURL)
	return fn(*token)
}

func newAuthSession(app *App, token string, notify func(string)) *authSession {
	session := &authSession{
		app:    app,
		notify: notify,
		token:  token,
	}
	session.cond = sync.NewCond(&session.mu)
	return session
}

func (s *authSession) AccessToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

func (s *authSession) Call(ctx context.Context, fn func(string) error) error {
	token := s.AccessToken()
	err := fn(token)
	if !authRetryable(err) {
		return err
	}

	token, err = s.refresh(ctx, token)
	if err != nil {
		return err
	}
	return fn(token)
}

func (s *authSession) refresh(ctx context.Context, staleToken string) (string, error) {
	s.mu.Lock()
	for s.refreshing {
		s.cond.Wait()
		if s.token != staleToken && strings.TrimSpace(s.token) != "" {
			token := s.token
			s.mu.Unlock()
			return token, nil
		}
	}
	if s.token != staleToken && strings.TrimSpace(s.token) != "" {
		token := s.token
		s.mu.Unlock()
		return token, nil
	}
	s.refreshing = true
	s.mu.Unlock()

	tokens, err := s.app.reauthenticate(ctx, s.notify)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.token = tokens.AccessToken
		s.app.API.BaseURL = firstNonEmpty(tokens.APIBase, s.app.API.BaseURL)
	}
	s.refreshing = false
	s.cond.Broadcast()
	if err != nil {
		return "", err
	}
	return s.token, nil
}

func (a *App) reauthenticate(ctx context.Context, notify func(string)) (auth.Tokens, error) {
	if notify == nil {
		notify = func(string) {}
	}
	notify("Session expired. Refreshing sign-in…")

	tokens, err := a.Auth.ReadState()
	if err == nil && strings.TrimSpace(tokens.RefreshToken) != "" {
		refreshed, refreshErr := a.Auth.Refresh(ctx, tokens)
		if refreshErr == nil {
			return refreshed, nil
		}
	}
	if err == nil && strings.TrimSpace(tokens.AnonymousID) != "" && strings.TrimSpace(tokens.AnonymousSecret) != "" {
		notify("Session expired. Restoring anonymous trial session…")
		bootstrapped, bootstrapErr := a.Auth.BootstrapAnonymous(ctx, tokens, notify)
		if bootstrapErr == nil {
			return bootstrapped, nil
		}
	}

	return auth.Tokens{}, errors.New("session expired. Run `devopsellence auth login`.")
}

func authRetryable(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *api.StatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == 401 {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "invalid access token") ||
		strings.Contains(message, "invalid_token") ||
		strings.Contains(message, "unauthorized")
}

// isTransientServerError returns true for server-side transient errors that are safe to retry
// without side effects (e.g. 502 Bad Gateway, 503 Service Unavailable, 504 Gateway Timeout).
func isTransientServerError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *api.StatusError
	if errors.As(err, &statusErr) {
		switch statusErr.StatusCode {
		case 502, 503, 504:
			return true
		}
	}
	return false
}

func deploymentProgressMap(progress api.DeploymentProgress) map[string]any {
	return map[string]any{
		"id":             progress.ID,
		"sequence":       progress.Sequence,
		"status":         progress.Status,
		"status_message": progress.StatusMessage,
		"error_message":  progress.ErrorMessage,
		"published_at":   progress.PublishedAt,
		"finished_at":    progress.FinishedAt,
		"environment": map[string]any{
			"id":   progress.Environment.ID,
			"name": progress.Environment.Name,
		},
		"release": progress.Release,
		"summary": map[string]any{
			"assigned_nodes": progress.Summary.AssignedNodes,
			"pending":        progress.Summary.Pending,
			"reconciling":    progress.Summary.Reconciling,
			"settled":        progress.Summary.Settled,
			"error":          progress.Summary.Error,
			"active":         progress.Summary.Active,
			"complete":       progress.Summary.Complete,
			"failed":         progress.Summary.Failed,
		},
		"nodes": progress.Nodes,
	}
}

func rolloutOutcome(progress api.DeploymentProgress) error {
	if progress.Summary.Complete {
		return nil
	}
	if !progress.Summary.Failed {
		return nil
	}

	failures := make([]string, 0, len(progress.Nodes))
	for _, node := range progress.Nodes {
		if node.Phase != "error" {
			continue
		}
		failures = append(failures, firstNonEmpty(node.Name, fmt.Sprintf("node-%d", node.ID))+": "+firstNonEmpty(node.Error, node.Message, "deploy failed"))
	}
	if len(failures) == 0 {
		if detail := deploymentStatusDetail(progress); detail != "" {
			return ExitError{Code: 1, Err: errors.New(detail)}
		}
		return ExitError{Code: 1, Err: errors.New("deploy failed on one or more nodes")}
	}
	return ExitError{Code: 1, Err: errors.New(strings.Join(failures, "; "))}
}

func rolloutTimeoutError(progress api.DeploymentProgress) error {
	if progress.ID == 0 {
		return errors.New("timed out waiting for deployment progress")
	}
	message := fmt.Sprintf("timed out waiting for deployment %d rollout (%d/%d settled, %d errors)",
		progress.ID,
		progress.Summary.Settled,
		progress.Summary.AssignedNodes,
		progress.Summary.Error,
	)
	if detail := deploymentStatusDetail(progress); detail != "" {
		message += ": " + detail
	}
	return errors.New(message)
}

func nodePhaseDetail(node api.DeploymentProgressNode) string {
	detail := firstNonEmpty(node.Error, node.Message)
	if detail == "" {
		return node.Phase
	}
	return node.Phase + " - " + detail
}

func (a *App) publishReleaseWithRetry(ctx context.Context, token string, releaseID, environmentID int, requestToken string) (map[string]any, error) {
	publish, err := a.API.PublishRelease(ctx, token, releaseID, environmentID, requestToken)
	if err == nil {
		return publish, nil
	}

	var statusErr *api.StatusError
	if errors.As(err, &statusErr) && statusErr.StatusCode == 524 {
		return a.API.PublishRelease(ctx, token, releaseID, environmentID, requestToken)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return a.API.PublishRelease(ctx, token, releaseID, environmentID, requestToken)
	}

	return nil, err
}

func randomRequestToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (a *App) resolveOrganization(ctx context.Context, token, input string) (api.Organization, bool, error) {
	orgs, err := a.API.ListOrganizations(ctx, token)
	if err != nil {
		return api.Organization{}, false, ExitError{Code: 1, Err: err}
	}
	if match, ok := findOrganization(orgs, input); ok {
		_ = a.rememberOrganization(match.ID)
		return match, false, nil
	}
	if strings.TrimSpace(input) != "" {
		org, err := a.API.CreateOrganization(ctx, token, strings.TrimSpace(input))
		if err != nil {
			return api.Organization{}, false, ExitError{Code: 1, Err: err}
		}
		_ = a.rememberOrganization(org.ID)
		return org, true, nil
	}
	if len(orgs) == 0 {
		org, err := a.API.CreateOrganization(ctx, token, "default")
		if err != nil {
			return api.Organization{}, false, ExitError{Code: 1, Err: err}
		}
		_ = a.rememberOrganization(org.ID)
		return org, true, nil
	}
	if len(orgs) == 1 {
		_ = a.rememberOrganization(orgs[0].ID)
		return orgs[0], false, nil
	}
	if lastID, ok := a.lastOrganizationID(); ok {
		for _, org := range orgs {
			if org.ID == lastID {
				return org, false, nil
			}
		}
	}
	return api.Organization{}, false, ExitError{Code: 2, Err: errors.New("multiple organizations available; pass --org")}
}

func (a *App) resolveOrganizationReadOnly(ctx context.Context, token, input string) (api.Organization, error) {
	orgs, err := a.API.ListOrganizations(ctx, token)
	if err != nil {
		return api.Organization{}, ExitError{Code: 1, Err: err}
	}
	if match, ok := findOrganization(orgs, input); ok {
		_ = a.rememberOrganization(match.ID)
		return match, nil
	}
	if len(orgs) == 0 {
		return api.Organization{}, ExitError{Code: 2, Err: errors.New("no organizations found. run `devopsellence setup --mode shared` first")}
	}
	if len(orgs) == 1 {
		_ = a.rememberOrganization(orgs[0].ID)
		return orgs[0], nil
	}
	if lastID, ok := a.lastOrganizationID(); ok {
		for _, org := range orgs {
			if org.ID == lastID {
				return org, nil
			}
		}
	}
	return api.Organization{}, ExitError{Code: 2, Err: errors.New("multiple organizations found. pass --org")}
}

func (a *App) resolveNodeBootstrapOrganization(ctx context.Context, token, input string) (api.Organization, error) {
	resolvedInput := strings.TrimSpace(input)
	if resolvedInput == "" {
		discovered, err := discovery.Discover(a.Cwd)
		if err == nil {
			existing, loadErr := config.LoadFromRoot(discovered.WorkspaceRoot)
			if loadErr == nil && existing != nil {
				resolvedInput = strings.TrimSpace(existing.Organization)
			}
		}
	}

	return a.resolveOrganizationReadOnly(ctx, token, resolvedInput)
}

func (a *App) findOrganizationByName(ctx context.Context, token, name string) (api.Organization, error) {
	orgs, err := a.API.ListOrganizations(ctx, token)
	if err != nil {
		return api.Organization{}, err
	}
	if match, ok := findOrganization(orgs, name); ok {
		return match, nil
	}
	return api.Organization{}, fmt.Errorf("organization %q not found for the current user: %w", name, errOrganizationNotFound)
}

func (a *App) ensureProjectEnvironment(ctx context.Context, token string, organizationID int, projectName, environmentName string) (api.Project, bool, api.Environment, bool, error) {
	projects, err := a.API.ListProjects(ctx, token, organizationID)
	if err != nil {
		return api.Project{}, false, api.Environment{}, false, ExitError{Code: 1, Err: err}
	}
	project, createdProject := findOrCreateProject(projects, projectName)
	if createdProject {
		project, err = a.API.CreateProject(ctx, token, organizationID, projectName)
		if err != nil {
			return api.Project{}, false, api.Environment{}, false, ExitError{Code: 1, Err: err}
		}
	}

	environments, err := a.API.ListEnvironments(ctx, token, project.ID)
	if err != nil {
		return api.Project{}, false, api.Environment{}, false, ExitError{Code: 1, Err: err}
	}
	env, createdEnv := findOrCreateEnvironment(environments, environmentName)
	if createdEnv {
		env, err = a.API.CreateEnvironment(ctx, token, project.ID, environmentName, "")
		if err != nil {
			return api.Project{}, false, api.Environment{}, false, ExitError{Code: 1, Err: err}
		}
	}
	return project, createdProject, env, createdEnv, nil
}

type Workspace struct {
	Organization api.Organization
	Project      api.Project
	Environment  api.Environment
	Discovery    discovery.Result
}

func (a *App) resolveWorkspace(ctx context.Context, token, organizationInput, projectInput, environmentName string, autoCreateDefault bool) (Workspace, error) {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return Workspace{}, wrapError(err)
	}

	existing, err := config.LoadFromRoot(discovered.WorkspaceRoot)
	if err != nil && !strings.Contains(err.Error(), "schema_version") {
		return Workspace{}, wrapError(err)
	}
	if err != nil {
		existing = nil
	}

	projectName := firstNonEmpty(projectInput, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Project }), discovered.ProjectName)
	orgInput := strings.TrimSpace(organizationInput)
	if orgInput == "" && existing != nil {
		orgInput = existing.Organization
	}
	resolvedEnvironmentName := a.effectiveEnvironment(environmentName, existing)

	var organization api.Organization
	if autoCreateDefault {
		organization, _, err = a.resolveOrganization(ctx, token, orgInput)
	} else {
		organization, err = a.resolveOrganizationReadOnly(ctx, token, orgInput)
	}
	if err != nil {
		return Workspace{}, err
	}

	var (
		project     api.Project
		environment api.Environment
	)
	if autoCreateDefault {
		project, _, environment, _, err = a.ensureProjectEnvironment(ctx, token, organization.ID, projectName, resolvedEnvironmentName)
		if err != nil {
			return Workspace{}, err
		}
	} else {
		project, environment, err = a.findProjectEnvironment(ctx, token, organization.ID, projectName, resolvedEnvironmentName)
		if err != nil {
			return Workspace{}, wrapError(err)
		}
	}

	return Workspace{
		Organization: organization,
		Project:      project,
		Environment:  environment,
		Discovery:    discovered,
	}, nil
}

func (a *App) ensureNodeBootstrapWorkspace(ctx context.Context, accessToken *string, opts NodeBootstrapOptions, update func(string)) (Workspace, *initializedWorkspace, error) {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return Workspace{}, nil, wrapError(err)
	}

	existing, err := config.LoadFromRoot(discovered.WorkspaceRoot)
	if err != nil {
		if !strings.Contains(err.Error(), "schema_version") {
			return Workspace{}, nil, wrapError(err)
		}
		existing = nil
	}
	if existing == nil {
		update("Initializing workspace…")
		initialized, err := a.initializeWorkspace(ctx, func(fn func(string) error) error {
			return a.callWithAuthRetry(ctx, accessToken, update, fn)
		}, InitOptions{
			Organization:   opts.Organization,
			ProjectName:    opts.Project,
			Environment:    opts.Environment,
			NonInteractive: true,
		}, update)
		if err != nil {
			return Workspace{}, nil, err
		}
		return Workspace{
			Organization: initialized.Organization,
			Project:      initialized.Project,
			Environment:  initialized.Environment,
			Discovery:    initialized.Discovered,
		}, &initialized, nil
	}

	update("Resolving workspace…")
	workspace, err := a.resolveWorkspace(ctx, *accessToken, opts.Organization, opts.Project, opts.Environment, true)
	if err != nil {
		return Workspace{}, nil, err
	}
	return workspace, nil, nil
}

func (a *App) initializeWorkspace(ctx context.Context, callAuth authCall, opts InitOptions, update func(string)) (initializedWorkspace, error) {
	update("Inspecting workspace…")
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return initializedWorkspace{}, ExitError{Code: 1, Err: err}
	}

	update("Loading existing config…")
	existing, err := config.LoadFromRoot(discovered.WorkspaceRoot)
	if err != nil {
		if !strings.Contains(err.Error(), "schema_version") {
			return initializedWorkspace{}, ExitError{Code: 1, Err: err}
		}
		existing = nil
	}

	projectName := firstNonEmpty(opts.ProjectName, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Project }), discovered.ProjectName)
	environmentName := a.effectiveEnvironment(opts.Environment, existing)
	orgInput := firstNonEmpty(opts.Organization, safeConfigValue(existing, func(cfg *config.Project) string { return cfg.Organization }))

	update("Resolving deploy target…")
	target, err := a.resolveDeployTarget(ctx, callAuth, resolveDeployTargetInput{
		Organization: orgInput,
		Project:      projectName,
		Environment:  environmentName,
	}, update)
	if err != nil {
		return initializedWorkspace{}, err
	}
	org := target.Organization
	project := target.Project
	environment := target.Environment

	update("Writing config…")
	projectConfig := config.DefaultProjectConfigForType(org.Name, project.Name, environment.Name, discovered.AppType)
	if existing == nil && discovered.InferredWebPort > 0 {
		projectConfig = setPrimaryWebServicePort(projectConfig, discovered.InferredWebPort)
	}
	if existing == nil {
		projectConfig = applySharedBootstrapIngress(projectConfig, target.Environment.IngressHosts)
	}
	if existing != nil {
		projectConfig.Build = existing.Build
		projectConfig.Services = existing.Services
		projectConfig.Tasks = existing.Tasks
		projectConfig.Ingress = existing.Ingress
		projectConfig.App = existing.App
		projectConfig.Organization = org.Name
		projectConfig.Project = project.Name
		projectConfig.DefaultEnvironment = environment.Name
	}
	written, err := config.Write(discovered.WorkspaceRoot, projectConfig)
	if err != nil {
		return initializedWorkspace{}, ExitError{Code: 1, Err: err}
	}

	return initializedWorkspace{
		Discovered:     discovered,
		Config:         written,
		ConfigPath:     a.ConfigStore.PathFor(discovered.WorkspaceRoot),
		CreatedConfig:  existing == nil,
		Organization:   org,
		Project:        project,
		Environment:    environment,
		CreatedOrg:     target.CreatedOrg,
		CreatedProject: target.CreatedProject,
		CreatedEnv:     target.CreatedEnv,
	}, nil
}

func (a *App) findProjectEnvironment(ctx context.Context, token string, organizationID int, projectName, environmentName string) (api.Project, api.Environment, error) {
	projects, err := a.API.ListProjects(ctx, token, organizationID)
	if err != nil {
		return api.Project{}, api.Environment{}, ExitError{Code: 1, Err: err}
	}
	project, ok := findProject(projects, projectName)
	if !ok {
		return api.Project{}, api.Environment{}, ExitError{Code: 1, Err: fmt.Errorf("project %q not found", projectName)}
	}

	environments, err := a.API.ListEnvironments(ctx, token, project.ID)
	if err != nil {
		return api.Project{}, api.Environment{}, ExitError{Code: 1, Err: err}
	}
	environment, ok := findEnvironment(environments, environmentName)
	if !ok {
		return api.Project{}, api.Environment{}, ExitError{Code: 1, Err: fmt.Errorf("environment %q not found", environmentName)}
	}

	return project, environment, nil
}

func (a *App) findProjectByName(ctx context.Context, token string, organizationID int, projectName string) (api.Project, error) {
	projects, err := a.API.ListProjects(ctx, token, organizationID)
	if err != nil {
		return api.Project{}, ExitError{Code: 1, Err: err}
	}
	project, ok := findProject(projects, projectName)
	if !ok {
		return api.Project{}, ExitError{Code: 1, Err: fmt.Errorf("project %q not found", projectName)}
	}
	return project, nil
}

func (a *App) findEnvironmentByName(ctx context.Context, token string, projectID int, environmentName string) (api.Environment, error) {
	environments, err := a.API.ListEnvironments(ctx, token, projectID)
	if err != nil {
		return api.Environment{}, ExitError{Code: 1, Err: err}
	}
	environment, ok := findEnvironment(environments, environmentName)
	if !ok {
		return api.Environment{}, ExitError{Code: 1, Err: fmt.Errorf("environment %q not found", environmentName)}
	}
	return environment, nil
}

func (a *App) resolveProjectReadOnly(ctx context.Context, token, organizationInput, projectInput string) (api.Organization, api.Project, error) {
	organization, err := a.resolveOrganizationReadOnly(ctx, token, organizationInput)
	if err != nil {
		return api.Organization{}, api.Project{}, err
	}
	projects, err := a.API.ListProjects(ctx, token, organization.ID)
	if err != nil {
		return api.Organization{}, api.Project{}, ExitError{Code: 1, Err: err}
	}
	projectName := strings.TrimSpace(projectInput)
	if projectName != "" {
		project, ok := findProject(projects, projectName)
		if !ok {
			return api.Organization{}, api.Project{}, ExitError{Code: 1, Err: fmt.Errorf("project %q not found", projectName)}
		}
		return organization, project, nil
	}
	switch len(projects) {
	case 0:
		return api.Organization{}, api.Project{}, ExitError{Code: 1, Err: errors.New("no projects found. run `devopsellence context project create <name>` first")}
	case 1:
		return organization, projects[0], nil
	default:
		return api.Organization{}, api.Project{}, ExitError{Code: 2, Err: errors.New("multiple projects found. pass --project")}
	}
}

func (a *App) resolveImage(ctx context.Context, callAuth authCall, workspaceRoot string, projectID int, cfg config.Project, sha, explicitImage string, update, log func(string)) (string, string, time.Duration, int, error) {
	if strings.TrimSpace(explicitImage) != "" {
		match := digestRefPattern.FindStringSubmatch(strings.TrimSpace(explicitImage))
		if len(match) != 3 {
			return "", "", 0, 0, ExitError{Code: 2, Err: errors.New("--image must include a digest ref like app@sha256:...")}
		}
		update("Using explicit image digest…")
		return match[1], match[2], 0, 0, nil
	}

	contextPath := filepath.Join(workspaceRoot, cfg.Build.Context)
	dockerfilePath := filepath.Join(workspaceRoot, cfg.Build.Dockerfile)
	repository := discovery.Slugify(cfg.Project)
	update("Requesting registry push credentials…")
	var pushAuth api.GARPushAuth
	if err := callAuth(func(accessToken string) error {
		var callErr error
		pushAuth, callErr = a.API.RequestRegistryPushAuth(ctx, accessToken, projectID, repository)
		return callErr
	}); err != nil {
		return "", "", 0, 0, err
	}
	targetRepository := firstNonEmpty(pushAuth.ImageRepository, repository)
	repositoryPath := firstNonEmpty(pushAuth.RepositoryPath, pushAuth.GARRepositoryPath)
	targetReference := repositoryPath + "/" + targetRepository + ":" + sha
	var digest string
	var buildPushDuration time.Duration
	var inferredPort int
	heartbeat := &deployBuildHeartbeat{}
	if err := a.Docker.WithTemporaryConfig(ctx, func(configDir string) error {
		update("Logging into container registry…")
		username := firstNonEmpty(pushAuth.DockerUsername, "oauth2accesstoken")
		password := firstNonEmpty(pushAuth.DockerPassword, pushAuth.AccessToken)
		if err := a.Docker.Login(ctx, pushAuth.RegistryHost, username, password, configDir); err != nil {
			return err
		}
		buildPushStartedAt := time.Now()
		heartbeat.start(ctx, update)
		pushedDigest, err := a.Docker.BuildAndPush(ctx, contextPath, dockerfilePath, targetReference, cfg.Build.Platforms, configDir, func(message string) {
			heartbeat.setStage(message)
			update(message)
		}, log)
		heartbeat.stop()
		if err != nil {
			return err
		}
		buildPushDuration = time.Since(buildPushStartedAt)
		digest = pushedDigest
		if metadata, err := a.Docker.ImageMetadata(ctx, targetReference); err == nil {
			inferredPort = firstExposedPort(metadata.ExposedPorts)
		}
		return nil
	}); err != nil {
		return "", "", 0, 0, err
	}
	return targetRepository, digest, buildPushDuration, inferredPort, nil
}

func (h *deployBuildHeartbeat) start(ctx context.Context, update func(string)) {
	if update == nil {
		return
	}
	h.mu.Lock()
	h.started = time.Now()
	if h.stage == "" {
		h.stage = "Preparing image build…"
	}
	h.mu.Unlock()

	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.mu.Lock()
				stage := h.stage
				started := h.started
				h.mu.Unlock()
				if started.IsZero() {
					return
				}
				update(fmt.Sprintf("%s still working after %s…", stage, time.Since(started).Round(time.Second)))
			}
		}
	}()
}

func (h *deployBuildHeartbeat) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.started = time.Time{}
}

func (h *deployBuildHeartbeat) setStage(stage string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.TrimSpace(stage) != "" {
		h.stage = stage
	}
}

func (a *App) optionalWorkspaceConfig() (discovery.Result, *config.Project, error) {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return discovery.Result{}, nil, err
	}
	loaded, err := config.LoadFromRoot(discovered.WorkspaceRoot)
	if err != nil && !strings.Contains(err.Error(), "schema_version") {
		return discovery.Result{}, nil, err
	}
	if err != nil {
		loaded = nil
	}
	return discovered, loaded, nil
}

func (a *App) workspaceConfigOrNil() (*config.Project, error) {
	_, cfg, err := a.optionalWorkspaceConfig()
	return cfg, err
}

func (a *App) requiredWorkspaceConfig() (discovery.Result, config.Project, error) {
	discovered, loaded, err := a.optionalWorkspaceConfig()
	if err != nil {
		return discovery.Result{}, config.Project{}, err
	}
	if loaded == nil {
		return discovery.Result{}, config.Project{}, errors.New("project not initialized. run `devopsellence setup` first")
	}
	return discovered, *loaded, nil
}

func (a *App) resolvedWorkspaceConfig(explicitEnvironment string) (discovery.Result, config.Project, string, error) {
	discovered, loaded, err := a.optionalWorkspaceConfig()
	if err != nil {
		return discovery.Result{}, config.Project{}, "", err
	}
	if loaded == nil {
		return discovery.Result{}, config.Project{}, "", errors.New("project not initialized. run `devopsellence setup` first")
	}
	selectedEnvironment := a.effectiveEnvironment(explicitEnvironment, loaded)
	resolved, err := config.ResolveEnvironmentConfig(*loaded, selectedEnvironment)
	if err != nil {
		return discovery.Result{}, config.Project{}, "", err
	}
	return discovered, resolved, selectedEnvironment, nil
}

func (a *App) applyInferredHealthcheckConfig(workspaceRoot string, cfg config.ProjectConfig, initialized *initializedWorkspace, inferredPort int) (config.ProjectConfig, string, string, error) {
	if initialized == nil || !initialized.CreatedConfig || inferredPort <= 0 {
		return cfg, "", "", nil
	}
	if !shouldApplyInferredImagePort(cfg, inferredPort) {
		return cfg, "", "", nil
	}

	cfg = setPrimaryWebServicePort(cfg, inferredPort)

	written, err := config.Write(workspaceRoot, cfg)
	if err != nil {
		return cfg, "", "", ExitError{Code: 1, Err: err}
	}
	initialized.Config = written
	return written,
		"updated " + initialized.ConfigPath + " from built image metadata",
		fmt.Sprintf("Detected container port %d from built image metadata. Using %s.ports.http=%d and healthcheck.port=%d.", inferredPort, primaryWebServiceName(cfg), inferredPort, inferredPort),
		nil
}

func shouldApplyInferredImagePort(cfg config.ProjectConfig, inferredPort int) bool {
	if inferredPort <= 0 {
		return false
	}
	service, ok := primaryWebService(cfg)
	if !ok {
		return false
	}
	if service.HTTPPort(0) == inferredPort {
		return false
	}
	if service.HTTPPort(0) != config.DefaultWebPort {
		return false
	}
	if service.Healthcheck != nil && service.Healthcheck.Port != 0 && service.Healthcheck.Port != config.DefaultWebPort {
		return false
	}
	return true
}

func inferredHealthcheckPath(cfg config.ProjectConfig) string {
	service, ok := primaryWebService(cfg)
	if ok && service.Healthcheck != nil && strings.TrimSpace(service.Healthcheck.Path) != "" {
		return service.Healthcheck.Path
	}
	if cfg.App.Type == config.AppTypeGeneric {
		return "/"
	}
	return config.DefaultHealthcheckPath
}

func primaryWebService(cfg config.ProjectConfig) (config.ServiceConfig, bool) {
	name, ok := cfg.PrimaryWebServiceName()
	if !ok {
		return config.ServiceConfig{}, false
	}
	service, ok := cfg.Services[name]
	return service, ok
}

func primaryWebServiceName(cfg config.ProjectConfig) string {
	name, ok := cfg.PrimaryWebServiceName()
	if !ok {
		return config.DefaultWebServiceName
	}
	return name
}

func setPrimaryWebServicePort(cfg config.ProjectConfig, port int) config.ProjectConfig {
	name, ok := cfg.PrimaryWebServiceName()
	if !ok {
		return cfg
	}
	service := cfg.Services[name]
	found := false
	for i := range service.Ports {
		if service.Ports[i].Name == "http" {
			service.Ports[i].Port = port
			found = true
			break
		}
	}
	if !found {
		service.Ports = append(service.Ports, config.ServicePort{Name: "http", Port: port})
	}
	if service.Healthcheck == nil {
		service.Healthcheck = &config.HTTPHealthcheck{Path: inferredHealthcheckPath(cfg), Port: port}
	} else {
		service.Healthcheck.Port = port
		if strings.TrimSpace(service.Healthcheck.Path) == "" {
			service.Healthcheck.Path = inferredHealthcheckPath(cfg)
		}
	}
	cfg.Services[name] = service
	return cfg
}

func applyBootstrapIngress(cfg config.ProjectConfig, hosts []string) config.ProjectConfig {
	serviceName, ok := cfg.PrimaryWebServiceName()
	if !ok {
		return cfg
	}
	resolvedHosts := normalizeIngressHosts(hosts)
	if len(resolvedHosts) == 0 {
		resolvedHosts = []string{"*"}
	}
	rules := make([]config.IngressRuleConfig, 0, len(resolvedHosts))
	for _, host := range resolvedHosts {
		rules = append(rules, config.IngressRuleConfig{
			Match:  config.IngressMatchConfig{Host: host, PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: serviceName, Port: "http"},
		})
	}
	cfg.Ingress = &config.IngressConfig{
		Hosts: resolvedHosts,
		Rules: rules,
		TLS: config.IngressTLSConfig{
			Mode: "off",
		},
		RedirectHTTP: configBoolPtr(false),
	}
	return cfg
}

func applySharedBootstrapIngress(cfg config.ProjectConfig, hosts []string) config.ProjectConfig {
	serviceName, ok := cfg.PrimaryWebServiceName()
	if !ok {
		return cfg
	}
	resolvedHosts := normalizeIngressHostsKeepOrder(hosts)
	if len(resolvedHosts) == 0 {
		return cfg
	}
	rules := make([]config.IngressRuleConfig, 0, len(resolvedHosts))
	for _, host := range resolvedHosts {
		rules = append(rules, config.IngressRuleConfig{
			Match:  config.IngressMatchConfig{Host: host, PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: serviceName, Port: "http"},
		})
	}
	cfg.Ingress = &config.IngressConfig{
		Hosts: resolvedHosts,
		Rules: rules,
		TLS: config.IngressTLSConfig{
			Mode: "off",
		},
		RedirectHTTP: configBoolPtr(false),
	}
	return cfg
}

func firstExposedPort(ports []int) int {
	for _, port := range ports {
		if port > 0 {
			return port
		}
	}
	return 0
}

func joinNotices(parts ...string) string {
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}
	return strings.Join(values, "; ")
}

func (a *App) secretValue(opts SecretSetOptions) (string, error) {
	if opts.ValueStdin {
		data, err := io.ReadAll(a.In)
		if err != nil {
			return "", ExitError{Code: 1, Err: err}
		}
		value := strings.TrimRight(string(data), "\r\n")
		if strings.TrimSpace(value) == "" {
			return "", ExitError{Code: 2, Err: errors.New("secret value is required")}
		}
		return value, nil
	}
	if strings.TrimSpace(opts.Value) != "" {
		return opts.Value, nil
	}
	if opts.ValueProvided {
		return "", ExitError{Code: 2, Err: errors.New("secret value is required")}
	}
	return "", ExitError{Code: 2, Err: errors.New("missing required option: --value or --stdin")}
}

func (a *App) registryPassword(opts OrganizationRegistrySetOptions) (string, error) {
	if opts.PasswordStdin {
		data, err := io.ReadAll(a.In)
		if err != nil {
			return "", ExitError{Code: 1, Err: err}
		}
		value := strings.TrimRight(string(data), "\r\n")
		if strings.TrimSpace(value) == "" {
			return "", ExitError{Code: 2, Err: errors.New("registry password is required")}
		}
		return value, nil
	}
	if strings.TrimSpace(opts.Password) != "" {
		return opts.Password, nil
	}
	if opts.PasswordProvided {
		return "", ExitError{Code: 2, Err: errors.New("registry password is required")}
	}
	return "", ExitError{Code: 2, Err: errors.New("missing required option: --password or --password-stdin")}
}

func (a *App) rememberOrganization(id int) error {
	if a.State == nil {
		return nil
	}
	return a.State.Update(func(current map[string]any) (map[string]any, error) {
		current["last_organization_id"] = id
		return current, nil
	})
}

func (a *App) shouldPrintClaimReminder(tokens auth.Tokens, deployResult map[string]any) bool {
	if strings.TrimSpace(stringFromMap(deployResult, "trial_expires_at")) == "" {
		return false
	}
	if strings.TrimSpace(tokens.AccountKind) != "anonymous" {
		return false
	}
	anonymousID := strings.TrimSpace(tokens.AnonymousID)
	if anonymousID == "" || a.State == nil {
		return anonymousID != ""
	}
	current, err := a.State.Read()
	if err != nil {
		return true
	}
	return stringFromAny(current["last_claim_reminder_anonymous_id"]) != anonymousID
}

func (a *App) markClaimReminderShown(anonymousID string) error {
	if a.State == nil || strings.TrimSpace(anonymousID) == "" {
		return nil
	}
	return a.State.Update(func(current map[string]any) (map[string]any, error) {
		current["last_claim_reminder_anonymous_id"] = anonymousID
		current["last_claim_reminder_at"] = time.Now().UTC().Format(time.RFC3339)
		return current, nil
	})
}

func (a *App) lastOrganizationID() (int, bool) {
	if a.State == nil {
		return 0, false
	}
	current, err := a.State.Read()
	if err != nil {
		return 0, false
	}
	switch value := current["last_organization_id"].(type) {
	case float64:
		return int(value), true
	case int:
		return value, true
	}
	return 0, false
}

func wrapError(err error) error {
	var exitErr ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	return ExitError{Code: 1, Err: err}
}

func stringFromAny(value any) string {
	text, _ := value.(string)
	return text
}

func authDefaultBase() string {
	return api.DefaultBaseURL
}

func authMode(tokens auth.Tokens) string {
	if strings.TrimSpace(os.Getenv("DEVOPSELLENCE_TOKEN")) != "" {
		return "token"
	}
	if strings.TrimSpace(tokens.RefreshToken) != "" || strings.TrimSpace(tokens.AccessToken) != "" {
		return "session"
	}
	return "unknown"
}

func trialState(tokens auth.Tokens) string {
	switch strings.TrimSpace(tokens.AccountKind) {
	case "anonymous":
		return "trial"
	case "human":
		return "claimed"
	default:
		return ""
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeIngressStrategy(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "", "tunnel":
		return strings.TrimSpace(value), nil
	case "direct_dns":
		return "direct_dns", nil
	default:
		return "", fmt.Errorf("unsupported ingress strategy %q: use tunnel or direct_dns", value)
	}
}

func safeConfigValue(cfg *config.Project, fn func(*config.Project) string) string {
	if cfg == nil {
		return ""
	}
	return fn(cfg)
}

func findOrganization(orgs []api.Organization, input string) (api.Organization, bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return api.Organization{}, false
	}
	if id, err := strconv.Atoi(trimmed); err == nil {
		for _, org := range orgs {
			if org.ID == id {
				return org, true
			}
		}
	}
	for _, org := range orgs {
		if org.Name == trimmed {
			return org, true
		}
	}
	return api.Organization{}, false
}

func findOrCreateProject(projects []api.Project, name string) (api.Project, bool) {
	project, ok := findProject(projects, name)
	if ok {
		return project, false
	}
	return api.Project{Name: name}, true
}

func findOrCreateEnvironment(environments []api.Environment, name string) (api.Environment, bool) {
	environment, ok := findEnvironment(environments, name)
	if ok {
		return environment, false
	}
	return api.Environment{Name: name}, true
}

func findProject(projects []api.Project, name string) (api.Project, bool) {
	for _, project := range projects {
		if project.Name == name {
			return project, true
		}
	}
	return api.Project{}, false
}

func findEnvironment(environments []api.Environment, name string) (api.Environment, bool) {
	for _, environment := range environments {
		if environment.Name == name {
			return environment, true
		}
	}
	return api.Environment{}, false
}

func servicePayload(_ string, service *config.Service) map[string]any {
	if service == nil {
		return nil
	}
	payload := map[string]any{
		"image":       strings.TrimSpace(service.Image),
		"env":         cloneEnv(service.Env),
		"secret_refs": service.SecretRefs,
		"ports":       service.Ports,
		"volumes":     service.Volumes,
	}
	if len(service.Command) > 0 {
		payload["command"] = service.Command
	}
	if len(service.Args) > 0 {
		payload["args"] = service.Args
	}
	if service.Healthcheck != nil && strings.TrimSpace(service.Healthcheck.Path) != "" {
		payload["healthcheck"] = map[string]any{
			"path": service.Healthcheck.Path,
			"port": service.Healthcheck.Port,
		}
	}
	return payload
}

func servicePayloads(services map[string]config.ServiceConfig) map[string]any {
	payloads := map[string]any{}
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		service := services[name]
		payloads[name] = servicePayload(name, &service)
	}
	return payloads
}

func taskPayloads(tasks config.TasksConfig) map[string]any {
	payloads := map[string]any{}
	if tasks.Release != nil {
		payload := map[string]any{
			"service": strings.TrimSpace(tasks.Release.Service),
			"env":     cloneEnv(tasks.Release.Env),
		}
		if len(tasks.Release.Command) > 0 {
			payload["command"] = tasks.Release.Command
		}
		if len(tasks.Release.Args) > 0 {
			payload["args"] = tasks.Release.Args
		}
		payloads["release"] = payload
	}
	if len(payloads) == 0 {
		return nil
	}
	return payloads
}

func ingressPayload(cfg config.ProjectConfig) map[string]any {
	if cfg.Ingress == nil || len(cfg.Ingress.Hosts) == 0 || len(cfg.Ingress.Rules) == 0 {
		return nil
	}
	payload := map[string]any{
		"hosts": append([]string(nil), cfg.Ingress.Hosts...),
	}
	rules := make([]map[string]any, 0, len(cfg.Ingress.Rules))
	for _, rule := range cfg.Ingress.Rules {
		pathPrefix := strings.TrimSpace(rule.Match.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "/"
		}
		rules = append(rules, map[string]any{
			"match": map[string]any{
				"host":        strings.TrimSpace(rule.Match.Host),
				"path_prefix": pathPrefix,
			},
			"target": map[string]any{
				"service": strings.TrimSpace(rule.Target.Service),
				"port":    strings.TrimSpace(rule.Target.Port),
			},
		})
	}
	payload["rules"] = rules
	if cfg.Ingress.RedirectHTTP != nil {
		payload["redirect_http"] = *cfg.Ingress.RedirectHTTP
	}
	if tls := map[string]any{
		"mode":             strings.TrimSpace(cfg.Ingress.TLS.Mode),
		"email":            strings.TrimSpace(cfg.Ingress.TLS.Email),
		"ca_directory_url": strings.TrimSpace(cfg.Ingress.TLS.CADirectoryURL),
	}; tls["mode"] != "" || tls["email"] != "" || tls["ca_directory_url"] != "" {
		payload["tls"] = tls
	}
	return payload
}

func cloneEnv(env map[string]string) map[string]string {
	clone := map[string]string{}
	for key, value := range env {
		clone[key] = value
	}
	return clone
}

func intFromMap(value map[string]any, key string) int {
	switch current := value[key].(type) {
	case float64:
		return int(current)
	case int:
		return current
	default:
		return 0
	}
}

func stringFromMap(value map[string]any, key string) string {
	current, _ := value[key].(string)
	return current
}

func anySlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func maxDuration(left, right time.Duration) time.Duration {
	if left > right {
		return left
	}
	return right
}

func formatDuration(value time.Duration) string {
	if value <= 0 {
		return "0s"
	}
	return value.Round(10 * time.Millisecond).String()
}

func nestedString(value map[string]any, path ...string) string {
	current := any(value)
	for _, part := range path {
		next, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = next[part]
	}
	text, _ := current.(string)
	return text
}

func publicURL(payload map[string]any) string {
	if value := stringFromMap(payload, "public_url"); value != "" {
		return value
	}
	return nestedString(payload, "ingress", "public_url")
}

func formatRelease(value any) string {
	entry, ok := value.(map[string]any)
	if !ok || len(entry) == 0 {
		return "none"
	}
	return fmt.Sprintf("#%d %s", intFromMap(entry, "id"), stringFromMap(entry, "git_sha"))
}

func formatDeployment(value any) string {
	entry, ok := value.(map[string]any)
	if !ok || len(entry) == 0 {
		return "none"
	}
	text := fmt.Sprintf("#%d %s", intFromMap(entry, "id"), stringFromMap(entry, "status"))
	if detail := firstNonEmpty(stringFromMap(entry, "error_message"), stringFromMap(entry, "status_message")); detail != "" {
		text += " - " + detail
	}
	return text
}

func deploymentStatusDetail(progress api.DeploymentProgress) string {
	return firstNonEmpty(progress.ErrorMessage, progress.StatusMessage)
}

func rolloutMilestone(progress api.DeploymentProgress) string {
	status := strings.TrimSpace(progress.StatusMessage)
	switch status {
	case "waiting for managed capacity":
		return "deploy accepted; waiting for warm capacity"
	case "booting managed node":
		return "managed capacity requested; waiting for the node to boot"
	case "claiming node bundle":
		return "warm capacity available; claiming a node bundle"
	case "publishing desired state":
		return "capacity claimed; publishing desired state to the node"
	case "waiting for node reconcile":
		if progress.Summary.Pending > 0 || progress.Summary.Reconciling > 0 {
			return "node claimed; waiting for the agent to apply the new revision"
		}
		return "node claimed; waiting for agent acknowledgement"
	case "rollout settled":
		return "new revision is healthy"
	case "publish failed":
		return "rollout failed"
	}
	if progress.Summary.Settled > 0 && progress.Summary.Error == 0 && progress.Summary.Pending == 0 && progress.Summary.Reconciling == 0 {
		return "new revision is healthy"
	}
	if progress.Summary.Error > 0 {
		return "rollout hit a node error"
	}
	return ""
}

func nodeDiagnosePending(status string) bool {
	switch strings.TrimSpace(status) {
	case "pending", "claimed":
		return true
	default:
		return false
	}
}

func (a *App) printNodeDiagnose(request api.NodeDiagnoseRequest) {
	a.Printer.Println("Node diagnose #" + strconv.Itoa(request.ID) + " for node #" + strconv.Itoa(request.Node.ID) + " (" + firstNonEmpty(request.Node.Name, "unnamed") + ")")
	a.Printer.Println("Status:", request.Status)
	if request.CompletedAt != "" {
		a.Printer.Println("Completed:", request.CompletedAt)
	}
	if request.ErrorMessage != "" {
		a.Printer.Println("Error:", request.ErrorMessage)
	}
	if request.Result == nil {
		return
	}

	result := request.Result
	a.Printer.Println("Collected:", result.CollectedAt)
	a.Printer.Println("Agent:", result.AgentVersion)
	a.Printer.Println(
		"Summary:",
		fmt.Sprintf("status=%s total=%d running=%d stopped=%d unhealthy=%d logs=%d",
			result.Summary.Status,
			result.Summary.Total,
			result.Summary.Running,
			result.Summary.Stopped,
			result.Summary.Unhealthy,
			result.Summary.LogsIncluded,
		),
	)

	for _, container := range result.Containers {
		name := firstNonEmpty(container.Service, container.System, container.Name)
		line := name + " container=" + container.Name + " running=" + strconv.FormatBool(container.Running)
		if container.Health != "" {
			line += " health=" + container.Health
		}
		if container.Hash != "" {
			line += " hash=" + container.Hash
		}
		a.Printer.Println(line)
		if container.LogTail != "" {
			for _, logLine := range strings.Split(strings.TrimRight(container.LogTail, "\n"), "\n") {
				a.Printer.Println("  " + logLine)
			}
		}
	}
}

func (a *App) warnAboutPrebuiltImageConfig(opts DeployOptions, cfg config.ProjectConfig) {
	if strings.TrimSpace(opts.Image) == "" || a.Printer.JSON {
		return
	}
	if cfg.App.Type != config.AppTypeRails {
		return
	}

	a.Printer.Errorln("Using --image skips the local build.")
	a.Printer.Errorln("Deploy will still use devopsellence.yml for port and healthcheck settings.")
	a.Printer.Errorln("If this image is not a Rails image, update devopsellence.yml before deploy.")
}

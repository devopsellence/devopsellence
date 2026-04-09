package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devopsellence/cli/internal/api"
	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/direct"
	"github.com/devopsellence/cli/internal/direct/providers"
	"github.com/devopsellence/cli/internal/discovery"
)

type DirectDeployOptions struct {
	Nodes []string
}

type DirectStatusOptions struct {
	Nodes []string
}

type DirectSecretsSetOptions struct {
	Key        string
	Value      string
	ValueStdin bool
}

type DirectSecretsListOptions struct{}

type DirectSecretsDeleteOptions struct {
	Key string
}

type DirectNodeListOptions struct{}

type DirectLogsOptions struct {
	Node   string
	Follow bool
}

type DirectNodeLabelSetOptions struct {
	Node   string
	Labels string
}

type DirectAgentInstallOptions struct {
	Node        string
	AgentBinary string
	BaseURL     string
}

type DirectDoctorOptions struct {
	Nodes []string
}

type DirectServerCreateOptions struct {
	Name         string
	Provider     string
	Region       string
	Size         string
	Image        string
	Labels       string
	SSHPublicKey string
	Install      bool
	Deploy       bool
}

type DirectServerDeleteOptions struct {
	Name string
	Yes  bool
}

type DirectSetupOptions struct{}

func (a *App) DirectDeploy(ctx context.Context, opts DirectDeployOptions) error {
	cfg, workspaceRoot, err := a.loadDirectConfig()
	if err != nil {
		return err
	}

	nodes, err := a.resolveNodes(cfg.Direct, opts.Nodes)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes configured; add direct.nodes to devopsellence.yml")
	}
	releaseNode, err := validateDirectNodeSchedule(cfg, nodes)
	if err != nil {
		return err
	}

	// Get git SHA for revision and image tag.
	sha, err := a.Git.CurrentSHA(workspaceRoot)
	if err != nil {
		return fmt.Errorf("get git SHA: %w", err)
	}
	shortSHA := sha
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	imageTag := directImageTag(cfg.Project, shortSHA)

	// Build Docker image.
	if !a.Printer.JSON {
		a.Printer.Println("Building image " + imageTag + " ...")
	}
	buildCtx := filepath.Join(workspaceRoot, cfg.Build.Context)
	dockerfile := filepath.Join(workspaceRoot, cfg.Build.Dockerfile)
	if err := dockerBuild(ctx, buildCtx, dockerfile, imageTag, cfg.Build.Platforms); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	// Load local secrets and build desired state.
	secrets, err := direct.LoadSecrets(workspaceRoot)
	if err != nil {
		return fmt.Errorf("load secrets: %w", err)
	}
	if notice, err := applyDirectRailsMasterKey(workspaceRoot, cfg, secrets); err != nil {
		return err
	} else if notice != "" && !a.Printer.JSON {
		a.Printer.Println("Rails: " + notice)
	}

	// Deploy to each node in parallel.
	var mu sync.Mutex
	var errs []string
	var wg sync.WaitGroup

	for _, name := range sortedDirectNodeNames(nodes) {
		node := nodes[name]
		wg.Add(1)
		go func(name string, node config.DirectNode) {
			defer wg.Done()
			desiredStateJSON, err := direct.BuildDesiredStateForLabels(cfg, imageTag, shortSHA, secrets, node.Labels, name == releaseNode)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("[%s] build desired state: %s", name, err))
				mu.Unlock()
				return
			}

			if !a.Printer.JSON {
				a.Printer.Println(fmt.Sprintf("[%s] Transferring image...", name))
			}
			if err := transferImage(ctx, node, imageTag, a.directProgress(name)); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("[%s] image transfer: %s", name, err))
				mu.Unlock()
				return
			}

			if !a.Printer.JSON {
				a.Printer.Println(fmt.Sprintf("[%s] Writing desired state...", name))
			}
			overridePath := filepath.Join(node.AgentStateDir, "desired-state-override.json")
			cmd := remoteDesiredStateOverrideCommand(overridePath)
			if _, err := direct.RunSSH(ctx, node, cmd, strings.NewReader(string(desiredStateJSON))); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("[%s] write desired state: %s", name, err))
				mu.Unlock()
				return
			}

			if !a.Printer.JSON {
				a.Printer.Println(fmt.Sprintf("[%s] Deployed %s", name, shortSHA))
			}
		}(name, node)
	}

	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("deploy errors:\n  %s", strings.Join(errs, "\n  "))
	}

	if a.Printer.JSON {
		nodeNames := make([]string, 0, len(nodes))
		nodeNames = append(nodeNames, sortedDirectNodeNames(nodes)...)
		return a.Printer.PrintJSON(map[string]any{
			"revision": shortSHA,
			"image":    imageTag,
			"nodes":    nodeNames,
		})
	}
	a.Printer.Println(fmt.Sprintf("Deployed revision %s to %d node(s)", shortSHA, len(nodes)))
	return nil
}

func validateDirectNodeSchedule(cfg *config.ProjectConfig, nodes map[string]config.DirectNode) (string, error) {
	webNode := ""
	workerNode := ""
	for _, name := range sortedDirectNodeNames(nodes) {
		node := nodes[name]
		if webNode == "" && directNodeCanRun(node, config.DirectLabelWeb) {
			webNode = name
		}
		if workerNode == "" && directNodeCanRun(node, config.DirectLabelWorker) {
			workerNode = name
		}
	}
	if webNode == "" {
		return "", fmt.Errorf("direct deploy requires at least one selected node labeled %q", config.DirectLabelWeb)
	}
	if cfg.Worker != nil && workerNode == "" {
		return "", fmt.Errorf("direct deploy requires at least one selected node labeled %q because worker is configured", config.DirectLabelWorker)
	}
	if cfg.ReleaseCommand == "" {
		return "", nil
	}
	return webNode, nil
}

func directNodeCanRun(node config.DirectNode, label string) bool {
	if node.Labels == nil {
		return true
	}
	for _, nodeLabel := range node.Labels {
		if strings.TrimSpace(nodeLabel) == label {
			return true
		}
	}
	return false
}

func sortedDirectNodeNames(nodes map[string]config.DirectNode) []string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *App) DirectStatus(ctx context.Context, opts DirectStatusOptions) error {
	cfg, _, err := a.loadDirectConfig()
	if err != nil {
		return err
	}

	nodes, err := a.resolveNodes(cfg.Direct, opts.Nodes)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes configured")
	}

	var jsonResults []map[string]any

	for name, node := range nodes {
		statusPath := filepath.Join(node.AgentStateDir, "status.json")
		out, err := direct.RunSSH(ctx, node, remoteReadFileCommand(statusPath), nil)
		if err != nil {
			if a.Printer.JSON {
				jsonResults = append(jsonResults, map[string]any{
					"node":  name,
					"error": err.Error(),
				})
			} else {
				a.Printer.Printf("[%s] error: %s\n", name, err)
			}
			continue
		}

		if a.Printer.JSON {
			var raw json.RawMessage
			if err := json.Unmarshal([]byte(out), &raw); err != nil {
				jsonResults = append(jsonResults, map[string]any{
					"node":  name,
					"error": fmt.Sprintf("invalid status JSON: %s", err),
				})
				continue
			}
			jsonResults = append(jsonResults, map[string]any{
				"node":   name,
				"status": raw,
			})
		} else {
			var status struct {
				Phase      string `json:"phase"`
				Revision   string `json:"revision"`
				Error      string `json:"error,omitempty"`
				Containers []struct {
					Name  string `json:"name"`
					State string `json:"state"`
				} `json:"containers"`
			}
			if err := json.Unmarshal([]byte(out), &status); err != nil {
				a.Printer.Printf("[%s] parse error: %s\n", name, err)
				continue
			}
			line := fmt.Sprintf("[%s] phase=%s revision=%s", name, status.Phase, status.Revision)
			if status.Error != "" {
				line += " error=" + status.Error
			}
			for _, c := range status.Containers {
				line += fmt.Sprintf(" %s=%s", c.Name, c.State)
			}
			a.Printer.Println(line)
		}
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"nodes": jsonResults})
	}
	return nil
}

func (a *App) DirectSecretsSet(_ context.Context, opts DirectSecretsSetOptions) error {
	if opts.ValueStdin {
		data, err := io.ReadAll(a.In)
		if err != nil {
			return err
		}
		opts.Value = strings.TrimRight(string(data), "\r\n")
	}
	if strings.TrimSpace(opts.Key) == "" {
		return ExitError{Code: 2, Err: errors.New("secret name is required")}
	}
	if strings.TrimSpace(opts.Value) == "" {
		return ExitError{Code: 2, Err: errors.New("secret value is required")}
	}
	_, workspaceRoot, err := a.loadDirectConfig()
	if err != nil {
		return err
	}
	if err := direct.SaveSecret(workspaceRoot, opts.Key, opts.Value); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"key": opts.Key, "action": "saved"})
	}
	a.Printer.Println(fmt.Sprintf("Secret %q saved", opts.Key))
	return nil
}

func (a *App) DirectSecretsList(_ context.Context, _ DirectSecretsListOptions) error {
	_, workspaceRoot, err := a.loadDirectConfig()
	if err != nil {
		return err
	}
	keys, err := direct.ListSecrets(workspaceRoot)
	if err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"keys": keys})
	}
	if len(keys) == 0 {
		a.Printer.Println("No secrets configured")
		return nil
	}
	for _, k := range keys {
		a.Printer.Println(k)
	}
	return nil
}

func (a *App) DirectNodeList(_ context.Context, _ DirectNodeListOptions) error {
	cfg, _, err := a.loadDirectConfig()
	if err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"nodes":          cfg.Direct.Nodes,
		})
	}
	names := sortedDirectNodeNames(cfg.Direct.Nodes)
	if len(names) == 0 {
		a.Printer.Println("No nodes.")
		return nil
	}
	for _, name := range names {
		node := cfg.Direct.Nodes[name]
		a.Printer.Println(fmt.Sprintf("%s  host=%s  labels=%s", name, node.Host, strings.Join(node.Labels, ",")))
	}
	return nil
}

func (a *App) DirectSecretsDelete(_ context.Context, opts DirectSecretsDeleteOptions) error {
	_, workspaceRoot, err := a.loadDirectConfig()
	if err != nil {
		return err
	}
	if err := direct.DeleteSecret(workspaceRoot, opts.Key); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"key": opts.Key, "action": "deleted"})
	}
	a.Printer.Println(fmt.Sprintf("Secret %q deleted", opts.Key))
	return nil
}

func (a *App) DirectLogs(ctx context.Context, opts DirectLogsOptions) error {
	cfg, _, err := a.loadDirectConfig()
	if err != nil {
		return err
	}

	node, ok := cfg.Direct.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found in config", opts.Node)
	}

	if opts.Follow {
		// Stream directly to the user's terminal — do not buffer.
		return direct.RunSSHInteractive(ctx, node, remoteJournalctlCommand("-u devopsellence-agent -f"), a.Printer.Out, a.Printer.Out)
	}

	out, err := direct.RunSSH(ctx, node, remoteJournalctlCommand("-u devopsellence-agent --no-pager -n 100"), nil)
	if err != nil {
		return err
	}
	a.Printer.Printf("%s", out)
	return nil
}

func (a *App) DirectNodeLabelSet(_ context.Context, opts DirectNodeLabelSetOptions) error {
	cfg, workspaceRoot, err := a.loadDirectConfig()
	if err != nil {
		return err
	}
	node, ok := cfg.Direct.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found in config", opts.Node)
	}
	labels, err := parseDirectLabels(opts.Labels)
	if err != nil {
		return err
	}
	node.Labels = labels
	cfg.Direct.Nodes[opts.Node] = node
	if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"node":        opts.Node,
			"labels":      labels,
			"config_path": a.ConfigStore.PathFor(workspaceRoot),
		})
	}
	a.Printer.Println("Updated direct node " + opts.Node + " labels: " + strings.Join(labels, ","))
	return nil
}

func (a *App) DirectAgentInstall(ctx context.Context, opts DirectAgentInstallOptions) error {
	cfg, _, err := a.loadDirectConfig()
	if err != nil {
		return err
	}
	node, ok := cfg.Direct.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found in config", opts.Node)
	}
	if !a.Printer.JSON {
		a.Printer.Println("Installing direct agent on " + opts.Node + "...")
	}
	if err := installDirectAgent(ctx, node, opts, a.directProgress(opts.Node)); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"node": opts.Node, "action": "installed"})
	}
	a.Printer.Println("Installed direct agent on " + opts.Node)
	return nil
}

func (a *App) DirectDoctor(ctx context.Context, opts DirectDoctorOptions) error {
	cfg, _, err := a.loadDirectConfig()
	if err != nil {
		return err
	}
	nodes, err := a.resolveNodes(cfg.Direct, opts.Nodes)
	if err != nil {
		return err
	}
	results := make([]map[string]any, 0, len(nodes))
	failed := false
	for _, name := range sortedDirectNodeNames(nodes) {
		node := nodes[name]
		checks := []struct {
			name string
			cmd  string
		}{
			{name: "ssh", cmd: "true"},
			{name: "docker", cmd: remoteDockerCheckCommand()},
			{name: "agent", cmd: "systemctl is-active --quiet devopsellence-agent"},
		}
		for _, check := range checks {
			err := direct.RunSSHInteractive(ctx, node, check.cmd, io.Discard, io.Discard)
			ok := err == nil
			if !ok {
				failed = true
			}
			results = append(results, map[string]any{"node": name, "check": check.name, "ok": ok})
			if !a.Printer.JSON {
				state := "ok"
				if !ok {
					state = "fail"
				}
				a.Printer.Println(fmt.Sprintf("[%s] %s=%s", name, check.name, state))
			}
		}
	}
	if a.Printer.JSON {
		if err := a.Printer.PrintJSON(map[string]any{"checks": results}); err != nil {
			return err
		}
		if failed {
			return ExitError{Code: 1, Err: fmt.Errorf("direct doctor failed")}
		}
		return nil
	}
	if failed {
		return ExitError{Code: 1, Err: fmt.Errorf("direct doctor failed")}
	}
	return nil
}

func (a *App) SoloDoctor(ctx context.Context) error {
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

	var cfg *config.ProjectConfig
	addCheck("config", func() (string, error) {
		if discoveryErr != nil {
			return "", discoveryErr
		}
		var err error
		cfg, err = a.ConfigStore.Read(discovered.WorkspaceRoot)
		if err != nil {
			return "", err
		}
		if cfg == nil {
			return "", errors.New("No config found. Run `devopsellence setup`.")
		}
		return a.ConfigStore.PathFor(discovered.WorkspaceRoot), nil
	})

	addCheck("nodes", func() (string, error) {
		if cfg == nil {
			return "", errors.New("No solo nodes configured yet. Run `devopsellence setup`.")
		}
		if cfg.Direct == nil || len(cfg.Direct.Nodes) == 0 {
			return "", errors.New("No solo nodes configured yet. Run `devopsellence setup`.")
		}
		return fmt.Sprintf("%d node(s) configured", len(cfg.Direct.Nodes)), nil
	})

	ok := true
	for _, check := range checks {
		if passed, _ := check["ok"].(bool); !passed {
			ok = false
			break
		}
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ok":             ok,
			"checks":         checks,
		})
	}
	for _, check := range checks {
		prefix := "FAIL"
		if check["ok"] == true {
			prefix = "OK"
		}
		a.Printer.Println(prefix, fmt.Sprintf("%v:", check["name"]), check["detail"])
	}
	if ok && cfg != nil && cfg.Direct != nil && len(cfg.Direct.Nodes) > 0 {
		return a.DirectDoctor(ctx, DirectDoctorOptions{})
	}
	return nil
}

func (a *App) DirectServerCreate(ctx context.Context, opts DirectServerCreateOptions) error {
	cfg, workspaceRoot, err := a.loadProjectConfigForDirectUpdate()
	if err != nil {
		return err
	}
	if opts.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if opts.Deploy && a.Printer.JSON {
		return fmt.Errorf("direct server create --deploy is not supported with --json")
	}
	if cfg.Direct != nil {
		if _, ok := cfg.Direct.Nodes[opts.Name]; ok {
			return fmt.Errorf("direct node %q already exists", opts.Name)
		}
	}
	labels, err := parseDirectLabels(firstNonEmpty(opts.Labels, strings.Join(config.DirectDefaultLabels, ",")))
	if err != nil {
		return err
	}
	providerSlug := firstNonEmpty(opts.Provider, "hetzner")
	if opts.Region == "" {
		opts.Region = "ash"
	}
	if opts.Size == "" {
		opts.Size = "cx22"
	}
	provider, err := providers.Resolve(providerSlug)
	if err != nil {
		return err
	}
	sshPublicKey, sshPublicKeyPath, err := readDirectSSHPublicKey(opts.SSHPublicKey)
	if err != nil {
		return err
	}
	if !a.Printer.JSON {
		a.Printer.Println("Creating " + providerSlug + " server " + opts.Name + "...")
	}
	server, err := provider.CreateServer(ctx, providers.CreateServerInput{
		Name:         opts.Name,
		Region:       opts.Region,
		Size:         opts.Size,
		Image:        opts.Image,
		SSHPublicKey: sshPublicKey,
	})
	if err != nil {
		return err
	}
	server, err = waitForDirectProviderServer(ctx, provider, server)
	if err != nil {
		return err
	}
	if server.PublicIP == "" {
		return fmt.Errorf("created server %s but provider did not return a public IPv4 address", server.ID)
	}
	node := config.DirectNode{
		Host:             server.PublicIP,
		User:             "root",
		Port:             22,
		AgentStateDir:    "/var/lib/devopsellence",
		Labels:           labels,
		Provider:         providerSlug,
		ProviderServerID: server.ID,
		ProviderRegion:   opts.Region,
		ProviderSize:     opts.Size,
		ProviderImage:    opts.Image,
	}
	if strings.TrimSpace(sshPublicKeyPath) != "" {
		node.SSHKey = strings.TrimSuffix(sshPublicKeyPath, ".pub")
		if _, err := os.Stat(node.SSHKey); err != nil {
			node.SSHKey = ""
		}
	}
	if err := a.writeDirectNode(workspaceRoot, cfg, opts.Name, node); err != nil {
		return err
	}
	if opts.Install || opts.Deploy {
		if !a.Printer.JSON {
			a.Printer.Println("Waiting for SSH on " + opts.Name + "...")
		}
		if err := waitForDirectSSH(ctx, node, 3*time.Minute); err != nil {
			return err
		}
		if err := installDirectAgent(ctx, node, DirectAgentInstallOptions{}, a.directProgress(opts.Name)); err != nil {
			return err
		}
	}
	if opts.Deploy {
		if err := a.DirectDeploy(ctx, DirectDeployOptions{Nodes: []string{opts.Name}}); err != nil {
			return err
		}
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"node":               opts.Name,
			"host":               node.Host,
			"labels":             labels,
			"provider":           providerSlug,
			"provider_server_id": server.ID,
			"config_path":        a.ConfigStore.PathFor(workspaceRoot),
		})
	}
	a.Printer.Println("Created direct node " + opts.Name + " at " + node.Host)
	return nil
}

func (a *App) DirectServerDelete(ctx context.Context, opts DirectServerDeleteOptions) error {
	if !opts.Yes {
		return fmt.Errorf("direct server delete requires --yes")
	}
	cfg, workspaceRoot, err := a.loadDirectConfig()
	if err != nil {
		return err
	}
	node, ok := cfg.Direct.Nodes[opts.Name]
	if !ok {
		return fmt.Errorf("node %q not found in config", opts.Name)
	}
	if node.Provider == "" || node.ProviderServerID == "" {
		return fmt.Errorf("node %q does not have provider metadata; refusing provider delete", opts.Name)
	}
	provider, err := providers.Resolve(node.Provider)
	if err != nil {
		return err
	}
	if err := provider.DeleteServer(ctx, node.ProviderServerID); err != nil {
		return err
	}
	delete(cfg.Direct.Nodes, opts.Name)
	if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"node": opts.Name, "action": "deleted"})
	}
	a.Printer.Println("Deleted direct server " + opts.Name)
	return nil
}

func (a *App) DirectSetup(ctx context.Context, _ DirectSetupOptions) error {
	if !a.Printer.Interactive {
		return fmt.Errorf("direct setup requires an interactive terminal; use direct server create or edit devopsellence.yml")
	}
	mode, err := a.promptLine("Server source (existing/hetzner)", "existing")
	if err != nil {
		return err
	}
	name, err := a.promptLine("Node name", "prod-1")
	if err != nil {
		return err
	}
	labels, err := a.promptLine("Labels", strings.Join(config.DirectDefaultLabels, ","))
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(mode), "hetzner") {
		region, err := a.promptLine("Hetzner region", "ash")
		if err != nil {
			return err
		}
		size, err := a.promptLine("Hetzner size", "cx22")
		if err != nil {
			return err
		}
		sshPublicKey, err := a.promptLine("SSH public key path", defaultDirectSSHPublicKeyPath())
		if err != nil {
			return err
		}
		if err := a.DirectServerCreate(ctx, DirectServerCreateOptions{
			Name:         name,
			Provider:     "hetzner",
			Region:       region,
			Size:         size,
			Labels:       labels,
			SSHPublicKey: sshPublicKey,
			Install:      true,
		}); err != nil {
			return err
		}
		return a.DirectDoctor(ctx, DirectDoctorOptions{Nodes: []string{name}})
	}
	host, err := a.promptLine("Host", "")
	if err != nil {
		return err
	}
	user, err := a.promptLine("SSH user", "root")
	if err != nil {
		return err
	}
	sshKey, err := a.promptLine("SSH private key path", defaultDirectSSHPrivateKeyPath())
	if err != nil {
		return err
	}
	cfg, workspaceRoot, err := a.loadProjectConfigForDirectUpdate()
	if err != nil {
		return err
	}
	parsedLabels, err := parseDirectLabels(labels)
	if err != nil {
		return err
	}
	node := config.DirectNode{
		Host:          host,
		User:          user,
		SSHKey:        strings.TrimSpace(sshKey),
		Port:          22,
		AgentStateDir: "/var/lib/devopsellence",
		Labels:        parsedLabels,
	}
	if err := a.writeDirectNode(workspaceRoot, cfg, name, node); err != nil {
		return err
	}
	if err := installDirectAgent(ctx, node, DirectAgentInstallOptions{}, a.directProgress(name)); err != nil {
		return err
	}
	return a.DirectDoctor(ctx, DirectDoctorOptions{Nodes: []string{name}})
}

func (a *App) directProgress(nodeName string) func(string) {
	if a.Printer.JSON {
		return func(string) {}
	}
	return func(message string) {
		a.Printer.Println("[" + nodeName + "] " + message)
	}
}

// loadDirectConfig discovers the workspace root, loads devopsellence.yml,
// and validates that direct config is present.
func (a *App) loadDirectConfig() (*config.ProjectConfig, string, error) {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return nil, "", err
	}
	workspaceRoot := discovered.WorkspaceRoot
	cfg, err := a.ConfigStore.Read(workspaceRoot)
	if err != nil {
		return nil, "", err
	}
	if cfg == nil {
		return nil, "", fmt.Errorf("no devopsellence.yml found; run `devopsellence setup --mode solo`")
	}
	if cfg.Direct == nil || len(cfg.Direct.Nodes) == 0 {
		return nil, "", fmt.Errorf("no direct.nodes configured in devopsellence.yml")
	}
	return cfg, workspaceRoot, nil
}

func (a *App) loadProjectConfigForDirectUpdate() (*config.ProjectConfig, string, error) {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return nil, "", err
	}
	cfg, err := a.ConfigStore.Read(discovered.WorkspaceRoot)
	if err != nil {
		return nil, "", err
	}
	if cfg == nil {
		cfg = directDefaultProjectConfig(discovered)
	}
	return cfg, discovered.WorkspaceRoot, nil
}

func directDefaultProjectConfig(discovered discovery.Result) *config.ProjectConfig {
	cfg := config.DefaultProjectConfigForType("direct", discovered.ProjectName, config.DefaultEnvironment, discovered.AppType)
	if discovered.InferredWebPort > 0 {
		cfg.Web.Port = discovered.InferredWebPort
		if cfg.Web.Healthcheck != nil {
			cfg.Web.Healthcheck.Port = discovered.InferredWebPort
		}
	}
	return &cfg
}

func (a *App) writeDirectNode(workspaceRoot string, cfg *config.ProjectConfig, name string, node config.DirectNode) error {
	if cfg.Direct == nil {
		cfg.Direct = &config.DirectConfig{Nodes: map[string]config.DirectNode{}}
	}
	if cfg.Direct.Nodes == nil {
		cfg.Direct.Nodes = map[string]config.DirectNode{}
	}
	cfg.Direct.Nodes[name] = node
	_, err := a.ConfigStore.Write(workspaceRoot, *cfg)
	return err
}

// resolveNodes filters nodes by the given names, or returns all if names is empty.
// Returns an error if any requested name is not present in the config.
func (a *App) resolveNodes(dc *config.DirectConfig, names []string) (map[string]config.DirectNode, error) {
	if len(names) == 0 {
		return dc.Nodes, nil
	}
	result := make(map[string]config.DirectNode, len(names))
	var unknown []string
	for _, name := range names {
		if node, ok := dc.Nodes[name]; ok {
			result[name] = node
		} else {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		available := make([]string, 0, len(dc.Nodes))
		for name := range dc.Nodes {
			available = append(available, name)
		}
		return nil, fmt.Errorf("unknown node(s): %s (available: %s)", strings.Join(unknown, ", "), strings.Join(available, ", "))
	}
	return result, nil
}

// shellQuote returns a POSIX shell safe single-quoted string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func parseDirectLabels(value string) ([]string, error) {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	seen := map[string]bool{}
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		label := strings.TrimSpace(part)
		if label == "" || seen[label] {
			continue
		}
		switch label {
		case config.DirectLabelWeb, config.DirectLabelWorker:
		default:
			return nil, fmt.Errorf("unsupported direct node label %q: use web or worker", label)
		}
		seen[label] = true
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("at least one direct node label is required")
	}
	return labels, nil
}

func applyDirectRailsMasterKey(workspaceRoot string, cfg *config.ProjectConfig, secrets map[string]string) (string, error) {
	if cfg == nil || cfg.App.Type != config.AppTypeRails {
		return "", nil
	}
	if secrets == nil {
		secrets = map[string]string{}
	}

	source := ".env"
	if strings.TrimSpace(secrets[railsMasterKeySecretName]) == "" {
		keyPath := filepath.Join(workspaceRoot, railsMasterKeyRelativePath)
		data, err := os.ReadFile(keyPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", nil
			}
			return "", fmt.Errorf("read Rails master key: %w", err)
		}
		value := strings.TrimSpace(string(data))
		if value == "" {
			return "", fmt.Errorf("Rails app detected, but %s is empty", keyPath)
		}
		secrets[railsMasterKeySecretName] = value
		source = railsMasterKeyRelativePath
	}

	services := []string{}
	if addServiceSecretRef(&cfg.Web, railsMasterKeySecretName) {
		services = append(services, "web")
	}
	if cfg.Worker != nil && addServiceSecretRef(cfg.Worker, railsMasterKeySecretName) {
		services = append(services, "worker")
	}
	if len(services) == 0 {
		return "", nil
	}
	return fmt.Sprintf("using RAILS_MASTER_KEY from %s for %s.", source, strings.Join(services, ", ")), nil
}

func addServiceSecretRef(svc *config.ServiceConfig, name string) bool {
	if svc == nil {
		return false
	}
	if _, ok := svc.Env[name]; ok {
		return false
	}
	for _, ref := range svc.SecretRefs {
		if ref.Name == name {
			return false
		}
	}
	svc.SecretRefs = append(svc.SecretRefs, config.SecretRef{Name: name})
	return true
}

func installDirectAgent(ctx context.Context, node config.DirectNode, opts DirectAgentInstallOptions, progress func(string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	if strings.TrimSpace(opts.AgentBinary) != "" {
		remotePath := fmt.Sprintf("/tmp/devopsellence-agent-%d", time.Now().UnixNano())
		progress("Uploading agent binary...")
		file, err := os.Open(opts.AgentBinary)
		if err != nil {
			return fmt.Errorf("open agent binary: %w", err)
		}
		defer file.Close()
		if err := direct.RunSSHStream(ctx, node, "cat > "+shellQuote(remotePath), file); err != nil {
			return fmt.Errorf("upload agent binary: %w", err)
		}
		defer direct.RunSSHInteractive(ctx, node, "rm -f "+shellQuote(remotePath), io.Discard, io.Discard)
		progress("Installing Docker, agent, and systemd service...")
		return runDirectAgentInstallScript(ctx, node, directAgentInstallScript(directAgentInstallScriptOptions{
			LocalBinaryPath: remotePath,
		}), progress)
	}

	baseURL := strings.TrimRight(firstNonEmpty(opts.BaseURL, os.Getenv("DEVOPSELLENCE_BASE_URL"), api.DefaultBaseURL), "/")
	progress("Installing Docker, agent, and systemd service...")
	return runDirectAgentInstallScript(ctx, node, directAgentInstallScript(directAgentInstallScriptOptions{
		BaseURL: baseURL,
	}), progress)
}

func runDirectAgentInstallScript(ctx context.Context, node config.DirectNode, script string, progress func(string)) error {
	writer := &lineProgressWriter{progress: progress}
	err := direct.RunSSHInteractiveWithStdin(ctx, node, "bash -s", strings.NewReader(script), writer, writer)
	writer.Flush()
	return err
}

type directAgentInstallScriptOptions struct {
	BaseURL         string
	LocalBinaryPath string
}

func directAgentInstallScript(opts directAgentInstallScriptOptions) string {
	stateDir := "/var/lib/devopsellence"
	authStatePath := stateDir + "/auth.json"
	overridePath := stateDir + "/desired-state-override.json"
	envoyBootstrapPath := stateDir + "/envoy/envoy.yaml"
	localBinary := strings.TrimSpace(opts.LocalBinaryPath)
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	return fmt.Sprintf(`set -euo pipefail

STATE_DIR=%s
AGENT_BIN=/usr/local/bin/devopsellence-agent
SERVICE_FILE=/etc/systemd/system/devopsellence-agent.service
BASE_URL=%s
LOCAL_BINARY=%s

if [ "$(id -u)" -ne 0 ]; then
  SUDO=sudo
else
  SUDO=
fi

run_root() {
  if [ -n "$SUDO" ]; then
    "$SUDO" "$@"
  else
    "$@"
  fi
}

docker_ready() {
  command -v docker >/dev/null 2>&1 && run_root docker info >/dev/null 2>&1
}

detect_supported_ubuntu() {
  [ -r /etc/os-release ] || return 1
  . /etc/os-release
  [ "${ID:-}" = "ubuntu" ] || return 1
  case "${VERSION_CODENAME:-}" in
    jammy|noble) printf '%%s\n' "$VERSION_CODENAME" ;;
    *) return 1 ;;
  esac
}

install_docker_for_supported_ubuntu() {
  codename="$(detect_supported_ubuntu)" || {
    echo "Docker Engine is required. Automatic install supports Ubuntu 22.04 and 24.04." >&2
    exit 1
  }
  run_root apt-get update
  run_root apt-get install -y ca-certificates curl
  run_root install -m 0755 -d /etc/apt/keyrings
  run_root curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  run_root chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${codename} stable" | run_root tee /etc/apt/sources.list.d/docker.list >/dev/null
  run_root apt-get update
  run_root apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  run_root systemctl enable --now docker
}

if ! command -v docker >/dev/null 2>&1; then
  echo "progress: installing Docker Engine"
  install_docker_for_supported_ubuntu
else
  echo "progress: Docker command found"
fi
if ! docker_ready; then
  echo "progress: starting Docker Engine"
  run_root systemctl enable --now docker || true
fi
if ! docker_ready; then
  echo "Docker Engine is unavailable after install/start" >&2
  exit 1
fi

run_root mkdir -p "$STATE_DIR" "$STATE_DIR/envoy"
TMP_BIN="$(mktemp)"
TMP_SUMS="$(mktemp)"
cleanup() {
  rm -f "$TMP_BIN" "$TMP_SUMS"
}
trap cleanup EXIT

if [ -n "$LOCAL_BINARY" ]; then
  echo "progress: installing uploaded agent binary"
  run_root install -m 0755 "$LOCAL_BINARY" "$AGENT_BIN"
else
  echo "progress: downloading agent binary"
  OS=linux
  ARCH_RAW="$(uname -m)"
  case "$ARCH_RAW" in
    x86_64|amd64) ARCH=amd64 ;;
    arm64|aarch64) ARCH=arm64 ;;
    *) echo "unsupported architecture: $ARCH_RAW" >&2; exit 1 ;;
  esac
  ARTIFACT_NAME="$OS-$ARCH"
  curl -fsSL "$BASE_URL/agent/download?os=$OS&arch=$ARCH" -o "$TMP_BIN"
  curl -fsSL "$BASE_URL/agent/checksums" -o "$TMP_SUMS"
  expected="$(awk -v name="$ARTIFACT_NAME" '$2 == name { print $1; exit }' "$TMP_SUMS")"
  if [ -z "$expected" ]; then
    echo "missing checksum entry for $ARTIFACT_NAME" >&2
    exit 1
  fi
  actual="$(sha256sum "$TMP_BIN" | awk '{print $1}')"
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for downloaded agent" >&2
    exit 1
  fi
  chmod +x "$TMP_BIN"
  run_root install -m 0755 "$TMP_BIN" "$AGENT_BIN"
fi

run_root tee "$SERVICE_FILE" >/dev/null <<EOF_SERVICE
[Unit]
Description=devopsellence agent
After=network-online.target docker.service docker.socket
Wants=network-online.target docker.service docker.socket

[Service]
ExecStart=$AGENT_BIN --mode=direct --auth-state-path=%s --desired-state-override-path=%s --envoy-bootstrap-path=%s
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF_SERVICE
echo "progress: starting devopsellence-agent service"
run_root systemctl daemon-reload
run_root systemctl stop devopsellence-agent || true
run_root systemctl enable --now devopsellence-agent
echo "progress: checking devopsellence-agent service"
run_root systemctl is-active --quiet devopsellence-agent
`, shellQuote(stateDir), shellQuote(baseURL), shellQuote(localBinary), authStatePath, overridePath, envoyBootstrapPath)
}

func waitForDirectProviderServer(ctx context.Context, provider providers.Provider, server providers.Server) (providers.Server, error) {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if provider.Ready(server) {
			return server, nil
		}
		if time.Now().After(deadline) {
			return providers.Server{}, fmt.Errorf("server %s did not become ready", server.ID)
		}
		select {
		case <-ctx.Done():
			return providers.Server{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		next, err := provider.GetServer(ctx, server.ID)
		if err != nil {
			return providers.Server{}, err
		}
		server = next
	}
}

func waitForDirectSSH(ctx context.Context, node config.DirectNode, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := direct.RunSSH(ctx, node, "true", nil); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for SSH on %s@%s", node.User, node.Host)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func readDirectSSHPublicKey(path string) (string, string, error) {
	path = strings.TrimSpace(path)
	candidates := []string{path}
	if path == "" {
		candidates = defaultDirectSSHPublicKeyCandidates()
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		data, err := os.ReadFile(candidate)
		if err == nil {
			value := strings.TrimSpace(string(data))
			if value == "" {
				return "", "", fmt.Errorf("SSH public key file %s is empty", candidate)
			}
			return value, candidate, nil
		}
		if path != "" {
			return "", "", fmt.Errorf("read SSH public key: %w", err)
		}
	}
	return "", "", fmt.Errorf("no SSH public key found; pass --ssh-public-key")
}

func defaultDirectSSHPrivateKeyPath() string {
	for _, candidate := range defaultDirectSSHPrivateKeyCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func defaultDirectSSHPublicKeyPath() string {
	for _, candidate := range defaultDirectSSHPublicKeyCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func defaultDirectSSHPublicKeyCandidates() []string {
	keys := defaultDirectSSHPrivateKeyCandidates()
	candidates := make([]string, 0, len(keys))
	for _, key := range keys {
		candidates = append(candidates, key+".pub")
	}
	return candidates
}

func defaultDirectSSHPrivateKeyCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_rsa"),
	}
}

type lineProgressWriter struct {
	mu       sync.Mutex
	progress func(string)
	buf      strings.Builder
}

func (w *lineProgressWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range string(p) {
		switch r {
		case '\n':
			w.flushLocked()
		case '\r':
		default:
			w.buf.WriteRune(r)
		}
	}
	return len(p), nil
}

func (w *lineProgressWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked()
}

func (w *lineProgressWriter) flushLocked() {
	line := strings.TrimSpace(w.buf.String())
	w.buf.Reset()
	if line == "" || w.progress == nil {
		return
	}
	line = strings.TrimSpace(strings.TrimPrefix(line, "progress:"))
	w.progress(line)
}

// dockerBuild runs `docker build` locally.
func dockerBuild(ctx context.Context, contextPath, dockerfile, tag string, platforms []string) error {
	args, err := dockerBuildArgs(contextPath, dockerfile, tag, platforms)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func dockerBuildArgs(contextPath, dockerfile, tag string, platforms []string) ([]string, error) {
	if len(platforms) != 1 {
		return nil, fmt.Errorf("direct deploy requires exactly one build platform, got %d", len(platforms))
	}
	return []string{"build", "--platform", platforms[0], "-f", dockerfile, "-t", tag, contextPath}, nil
}

func directImageTag(project, revision string) string {
	return fmt.Sprintf("%s:%s", discovery.Slugify(project), revision)
}

// transferImage pipes `docker save | gzip` through ssh to `gunzip | docker load`.
func transferImage(ctx context.Context, node config.DirectNode, imageTag string, progress func(string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	progress("Checking remote Docker...")
	if _, err := direct.RunSSH(ctx, node, remoteDockerCheckCommand(), nil); err != nil {
		return fmt.Errorf("remote docker check: %w", err)
	}
	// docker save <tag> | gzip | ssh ... 'gunzip | docker load'
	progress("Starting local docker save...")
	saveCmd := exec.CommandContext(ctx, "docker", "save", imageTag)
	gzipCmd := exec.CommandContext(ctx, "gzip")

	var err error
	gzipCmd.Stdin, err = saveCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("pipe docker save to gzip: %w", err)
	}

	pr, pw := io.Pipe()
	gzipCmd.Stdout = pw

	if err := saveCmd.Start(); err != nil {
		return fmt.Errorf("start docker save: %w", err)
	}
	if err := gzipCmd.Start(); err != nil {
		return fmt.Errorf("start gzip: %w", err)
	}

	// Close the write end of the pipe when gzip finishes.
	gzipErrCh := make(chan error, 1)
	go func() {
		gzipErrCh <- gzipCmd.Wait()
		pw.Close()
	}()

	progress("Streaming compressed image to remote Docker...")
	meter := &progressReader{
		reader:      pr,
		progress:    progress,
		reportEvery: 16 * 1024 * 1024,
		nextReport:  16 * 1024 * 1024,
	}
	stopTicker := startProgressTicker(ctx, func(string) {
		progress(fmt.Sprintf("Still transferring image... %s compressed sent", formatBytes(meter.Total())))
	}, 5*time.Second, "")
	sshErr := direct.RunSSHStream(ctx, node, remoteDockerLoadCommand(), meter)
	stopTicker()

	// If SSH failed (e.g., auth error, network), close the read end of the pipe
	// to unblock gzip's writes. Without this, gzip blocks on pw.Write() forever
	// because nobody is reading pr, which hangs the entire pipeline.
	if sshErr != nil {
		pr.CloseWithError(sshErr)
	}

	// Wait for local producers to finish so we don't leak processes.
	if saveErr := saveCmd.Wait(); saveErr != nil {
		return fmt.Errorf("docker save: %w", saveErr)
	}
	if gzipErr := <-gzipErrCh; gzipErr != nil {
		return fmt.Errorf("gzip image stream: %w", gzipErr)
	}

	if sshErr != nil {
		return fmt.Errorf("transfer to %s@%s: %w", node.User, node.Host, sshErr)
	}
	progress("Image transfer complete (" + formatBytes(meter.Total()) + " compressed).")
	return nil
}

func remoteDockerCheckCommand() string {
	return "if docker info >/dev/null 2>&1; then exit 0; fi; if command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then exit 0; fi; echo 'Docker is not reachable. Add this SSH user to the docker group or enable passwordless sudo for docker.' >&2; docker info >/dev/null 2>&1"
}

func remoteDockerLoadCommand() string {
	return "if docker info >/dev/null 2>&1; then gunzip | docker load; elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then gunzip | sudo -n docker load; else echo 'Docker is not reachable. Add this SSH user to the docker group or enable passwordless sudo for docker.' >&2; docker info >/dev/null 2>&1; exit 1; fi"
}

func remoteReadFileCommand(path string) string {
	quotedPath := shellQuote(path)
	return fmt.Sprintf("if [ -r %[1]s ]; then exec cat %[1]s; fi; if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then exec sudo -n cat %[1]s; fi; exec cat %[1]s", quotedPath)
}

func remoteJournalctlCommand(args string) string {
	return fmt.Sprintf("if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then exec sudo -n journalctl %s; fi; exec journalctl %s", args, args)
}

func remoteDesiredStateOverrideCommand(overridePath string) string {
	quotedPath := shellQuote(overridePath)
	return fmt.Sprintf("agent_bin=$(command -v devopsellence-agent || command -v devopsellence || true); if [ -z \"$agent_bin\" ]; then echo 'devopsellence agent binary not found' >&2; exit 127; fi; override_dir=$(dirname -- %[1]s); if [ \"$(id -u)\" = 0 ] || [ -w \"$override_dir\" ]; then exec \"$agent_bin\" desired-state set-override --file - --override-path %[1]s; fi; if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then exec sudo -n \"$agent_bin\" desired-state set-override --file - --override-path %[1]s; fi; echo 'Cannot write desired state override. Make the SSH user able to write the agent state directory or enable passwordless sudo.' >&2; exit 1", quotedPath)
}

type progressReader struct {
	reader      io.Reader
	progress    func(string)
	reportEvery int64
	nextReport  int64

	mu    sync.Mutex
	total int64
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.add(int64(n))
	}
	return n, err
}

func (r *progressReader) add(n int64) {
	r.mu.Lock()
	r.total += n
	total := r.total
	shouldReport := r.reportEvery > 0 && total >= r.nextReport
	if shouldReport {
		for r.nextReport <= total {
			r.nextReport += r.reportEvery
		}
	}
	r.mu.Unlock()
	if shouldReport && r.progress != nil {
		r.progress("Transferred " + formatBytes(total) + " compressed...")
	}
}

func (r *progressReader) Total() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}

func formatBytes(bytes int64) string {
	const mib = 1024 * 1024
	if bytes < mib {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f MiB", float64(bytes)/mib)
}

func startProgressTicker(ctx context.Context, progress func(string), interval time.Duration, message string) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				progress(message)
			}
		}
	}()
	return func() { close(done) }
}

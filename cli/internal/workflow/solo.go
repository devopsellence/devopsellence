package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devopsellence/cli/internal/api"
	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/discovery"
	"github.com/devopsellence/cli/internal/output"
	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/cli/internal/solo/providers"
	"github.com/devopsellence/cli/internal/ui"
	cliversion "github.com/devopsellence/cli/internal/version"
)

type SoloDeployOptions struct {
	Nodes        []string
	SkipDNSCheck bool
}

type SoloStatusOptions struct {
	Nodes []string
}

type SoloSecretsSetOptions struct {
	Key        string
	Value      string
	ValueStdin bool
}

type SoloSecretsListOptions struct{}

type SoloSecretsDeleteOptions struct {
	Key string
}

type SoloNodeListOptions struct{}

type SoloNodeAttachOptions struct {
	Node        string
	Environment string
}

type SoloNodeDetachOptions struct {
	Node        string
	Environment string
}

type SoloLogsOptions struct {
	Node   string
	Follow bool
}

type SoloNodeLabelSetOptions struct {
	Node   string
	Labels string
}

type SoloAgentInstallOptions struct {
	Node        string
	AgentBinary string
	BaseURL     string
}

type SoloDoctorOptions struct {
	Nodes []string
}

type SoloNodeCreateOptions struct {
	Name         string
	Provider     string
	Region       string
	Size         string
	Image        string
	Labels       string
	SSHPublicKey string
	NoInstall    bool
	Deploy       bool
}

type SoloNodeRemoveOptions struct {
	Name string
	Yes  bool
}

type SharedNodeCreateOptions struct {
	SoloNodeCreateOptions
	NodeBootstrapOptions
}

type providerNodeCreateResult struct {
	Node         config.SoloNode
	Server       providers.Server
	Labels       []string
	ProviderSlug string
}

type SoloSetupOptions struct{}

type IngressSetOptions struct {
	Hosts               []string
	Service             string
	TLSMode             string
	TLSEmail            string
	TLSCADirectoryURL   string
	RedirectHTTP        bool
	RedirectHTTPChanged bool
}

type IngressCheckOptions struct {
	Wait time.Duration
}

func (a *App) createProviderNode(ctx context.Context, opts SoloNodeCreateOptions, projectName string) (providerNodeCreateResult, error) {
	if opts.Name == "" {
		return providerNodeCreateResult{}, fmt.Errorf("node name is required")
	}
	labels, err := parseSoloLabels(firstNonEmpty(opts.Labels, strings.Join(config.SoloDefaultLabels, ",")))
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	providerSlug, err := normalizeProvider(firstNonEmpty(opts.Provider, providerHetzner))
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	if opts.Region == "" {
		opts.Region = defaultHetznerRegion
	}
	if opts.Size == "" {
		opts.Size = defaultHetznerSize
	}
	if providerSlug == providerHetzner && strings.EqualFold(strings.TrimSpace(opts.Size), "cx22") {
		return providerNodeCreateResult{}, fmt.Errorf("Hetzner size %q is deprecated; use %q", opts.Size, defaultHetznerSize)
	}
	if err := a.ensureInteractiveProviderLogin(ctx, providerSlug); err != nil {
		return providerNodeCreateResult{}, err
	}
	provider, err := a.resolveSoloProvider(providerSlug)
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	sshPublicKey, sshPublicKeyPath, err := readSoloSSHPublicKey(opts.SSHPublicKey)
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	if !a.Printer.JSON {
		a.Printer.Println("Creating " + providerSlug + " server " + opts.Name + "...")
	}
	providerLabels := map[string]string{}
	if strings.TrimSpace(projectName) != "" {
		providerLabels["devopsellence_project"] = discovery.Slugify(projectName)
	}
	server, err := provider.CreateServer(ctx, providers.CreateServerInput{
		Name:         opts.Name,
		Region:       opts.Region,
		Size:         opts.Size,
		Image:        opts.Image,
		SSHPublicKey: sshPublicKey,
		Labels:       providerLabels,
	})
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	server, err = waitForSoloProviderServer(ctx, provider, server)
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	if server.PublicIP == "" {
		return providerNodeCreateResult{}, fmt.Errorf("created server %s but provider did not return a public IPv4 address", server.ID)
	}
	node := config.SoloNode{
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
	return providerNodeCreateResult{
		Node:         node,
		Server:       server,
		Labels:       labels,
		ProviderSlug: providerSlug,
	}, nil
}

func (a *App) SoloDeploy(ctx context.Context, opts SoloDeployOptions) error {
	if len(opts.Nodes) > 0 {
		return fmt.Errorf("solo deploy --nodes is no longer supported; attach or detach nodes instead")
	}
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	environmentName := soloEnvironmentName(cfg, "")
	attachedNodeNames, err := current.AttachedNodeNames(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	nodes, err := a.resolveSoloNodes(current, attachedNodeNames)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes attached to %s; run `devopsellence node attach <name>`", environmentName)
	}
	if _, err := validateSoloNodeSchedule(cfg, nodes); err != nil {
		return err
	}
	if err := a.checkIngressBeforeDeploy(ctx, cfg, nodes, opts.SkipDNSCheck); err != nil {
		return err
	}

	sha, err := a.Git.CurrentSHA(workspaceRoot)
	if err != nil {
		return fmt.Errorf("get git SHA: %w", err)
	}
	shortSHA := sha
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	imageTag := soloImageTag(cfg.Project, shortSHA)
	if !a.Printer.JSON {
		a.Printer.Println("Building image " + imageTag + " ...")
	}
	buildCtx := filepath.Join(workspaceRoot, cfg.Build.Context)
	dockerfile := filepath.Join(workspaceRoot, cfg.Build.Dockerfile)
	if err := dockerBuild(ctx, buildCtx, dockerfile, imageTag, cfg.Build.Platforms); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	secrets, err := solo.LoadSecrets(workspaceRoot)
	if err != nil {
		return fmt.Errorf("load secrets: %w", err)
	}
	if notice, err := applySoloRailsMasterKey(workspaceRoot, cfg, secrets); err != nil {
		return err
	} else if notice != "" && !a.Printer.JSON {
		a.Printer.Println("Rails: " + notice)
	}

	snapshot, err := solo.BuildDeploySnapshot(cfg, workspaceRoot, a.ConfigStore.PathFor(workspaceRoot), imageTag, shortSHA, secrets)
	if err != nil {
		return err
	}
	if _, err := current.SaveSnapshot(snapshot); err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}

	if err := a.republishSoloNodes(ctx, current, attachedNodeNames); err != nil {
		return err
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"revision":    shortSHA,
			"image":       imageTag,
			"environment": environmentName,
			"nodes":       sortedSoloNodeNames(nodes),
		})
	}
	a.Printer.Println(fmt.Sprintf("Deployed revision %s to %d node(s)", shortSHA, len(nodes)))
	return nil
}

func validateSoloNodeSchedule(cfg *config.ProjectConfig, nodes map[string]config.SoloNode) (string, error) {
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		scheduled := false
		for _, nodeName := range sortedSoloNodeNames(nodes) {
			if soloNodeCanRunKind(nodes[nodeName], service.Kind) {
				scheduled = true
				break
			}
		}
		if !scheduled {
			return "", fmt.Errorf("solo deploy requires at least one selected node labeled %q for service %q", service.Kind, serviceName)
		}
	}
	release := cfg.ReleaseTask()
	if release == nil {
		return "", nil
	}
	for _, nodeName := range sortedSoloNodeNames(nodes) {
		if soloNodeCanRunKind(nodes[nodeName], cfg.Services[release.Service].Kind) {
			return nodeName, nil
		}
	}
	return "", fmt.Errorf("solo deploy requires at least one selected node labeled for release task service %q", release.Service)
}

func soloNodeCanRunKind(node config.SoloNode, kind string) bool {
	if node.Labels == nil {
		return true
	}
	for _, nodeLabel := range node.Labels {
		if strings.TrimSpace(nodeLabel) == strings.TrimSpace(kind) {
			return true
		}
	}
	return false
}

func soloNodeCanRunIngress(node config.SoloNode, cfg *config.ProjectConfig) bool {
	if cfg == nil || cfg.Ingress == nil {
		return false
	}
	service, ok := cfg.Services[cfg.Ingress.Service]
	if !ok {
		return false
	}
	return soloNodeCanRunKind(node, service.Kind)
}

func sortedSoloNodeNames(nodes map[string]config.SoloNode) []string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *App) readSoloState() (solo.State, error) {
	if a.SoloState == nil {
		return solo.State{}, fmt.Errorf("solo state store is required")
	}
	return a.SoloState.Read()
}

func (a *App) writeSoloState(current solo.State) error {
	if a.SoloState == nil {
		return fmt.Errorf("solo state store is required")
	}
	return a.SoloState.Write(current)
}

func (a *App) resolveSoloNodes(current solo.State, names []string) (map[string]config.SoloNode, error) {
	if len(names) == 0 {
		result := make(map[string]config.SoloNode, len(current.Nodes))
		for name, node := range current.Nodes {
			result[name] = node
		}
		return result, nil
	}
	result := make(map[string]config.SoloNode, len(names))
	unknown := []string{}
	for _, name := range names {
		if node, ok := current.Nodes[name]; ok {
			result[name] = node
		} else {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("unknown node(s): %s (available: %s)", strings.Join(unknown, ", "), strings.Join(current.NodeNames(), ", "))
	}
	return result, nil
}

func (a *App) republishSoloNodes(ctx context.Context, current solo.State, nodeNames []string) error {
	if len(nodeNames) == 0 {
		return nil
	}
	var mu sync.Mutex
	var errs []string
	var wg sync.WaitGroup

	nodes, err := a.resolveSoloNodes(current, nodeNames)
	if err != nil {
		return err
	}
	for _, nodeName := range sortedSoloNodeNames(nodes) {
		node := nodes[nodeName]
		wg.Add(1)
		go func(name string, node config.SoloNode) {
			defer wg.Done()
			snapshots, releaseNodes, peers, images, err := soloNodeDesiredStateInputs(current, name)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("[%s] build desired state inputs: %s", name, err))
				mu.Unlock()
				return
			}
			for _, image := range images {
				if !a.Printer.JSON {
					a.Printer.Println(fmt.Sprintf("[%s] Transferring image %s...", name, image))
				}
				if err := transferImage(ctx, node, image, a.soloProgress(name)); err != nil {
					mu.Lock()
					errs = append(errs, fmt.Sprintf("[%s] image transfer: %s", name, err))
					mu.Unlock()
					return
				}
			}
			desiredStateJSON, err := solo.BuildAggregatedDesiredState(name, node, snapshots, releaseNodes, peers)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("[%s] build desired state: %s", name, err))
				mu.Unlock()
				return
			}
			if !a.Printer.JSON {
				a.Printer.Println(fmt.Sprintf("[%s] Writing desired state...", name))
			}
			overridePath := filepath.Join(node.AgentStateDir, "desired-state-override.json")
			cmd := remoteDesiredStateOverrideCommand(overridePath)
			if _, err := solo.RunSSH(ctx, node, cmd, strings.NewReader(string(desiredStateJSON))); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("[%s] write desired state: %s", name, err))
				mu.Unlock()
				return
			}
		}(nodeName, node)
	}
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("deploy errors:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

func soloNodeDesiredStateInputs(current solo.State, nodeName string) ([]solo.DeploySnapshot, map[string]string, []solo.NodePeer, []string, error) {
	snapshots := []solo.DeploySnapshot{}
	releaseNodes := map[string]string{}
	peerMap := map[string]solo.NodePeer{}
	imageSet := map[string]bool{}

	for _, key := range current.AttachmentKeysForNode(nodeName) {
		attachment := current.Attachments[key]
		snapshot, ok := current.Snapshots[key]
		if !ok {
			continue
		}
		snapshots = append(snapshots, snapshot)
		if image := strings.TrimSpace(snapshot.Image); image != "" && !imageSet[image] {
			imageSet[image] = true
		}
		releaseNode, err := releaseNodeForSnapshot(snapshot, attachment, current.Nodes)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if releaseNode != "" {
			releaseNodes[key] = releaseNode
		}
		for _, peerName := range attachment.NodeNames {
			if peerName == nodeName {
				continue
			}
			peerNode, ok := current.Nodes[peerName]
			if !ok || strings.TrimSpace(peerNode.Host) == "" {
				continue
			}
			peerMap[peerName] = solo.NodePeer{
				Name:          peerName,
				Labels:        append([]string(nil), peerNode.Labels...),
				PublicAddress: peerNode.Host,
			}
		}
	}

	peers := make([]solo.NodePeer, 0, len(peerMap))
	for _, name := range sortedSoloPeerNames(peerMap) {
		peers = append(peers, peerMap[name])
	}
	images := make([]string, 0, len(imageSet))
	for image := range imageSet {
		images = append(images, image)
	}
	sort.Strings(images)
	return snapshots, releaseNodes, peers, images, nil
}

func releaseNodeForSnapshot(snapshot solo.DeploySnapshot, attachment solo.AttachmentRecord, nodes map[string]config.SoloNode) (string, error) {
	if snapshot.ReleaseTask == nil {
		return "", nil
	}
	nodeNames := append([]string(nil), attachment.NodeNames...)
	sort.Strings(nodeNames)
	for _, nodeName := range nodeNames {
		node, ok := nodes[nodeName]
		if ok && soloNodeCanRunKind(node, snapshot.ReleaseServiceKind) {
			return nodeName, nil
		}
	}
	return "", fmt.Errorf("environment %s in %s requires at least one attached node labeled %q for release task service %q", snapshot.Environment, snapshot.WorkspaceKey, snapshot.ReleaseServiceKind, snapshot.ReleaseService)
}

func sortedSoloPeerNames(peers map[string]solo.NodePeer) []string {
	names := make([]string, 0, len(peers))
	for name := range peers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *App) SoloStatus(ctx context.Context, opts SoloStatusOptions) error {
	nodes, err := a.soloStatusNodes(opts)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes attached to the current environment")
	}

	var jsonResults []map[string]any

	for name, node := range nodes {
		statusPath := filepath.Join(node.AgentStateDir, "status.json")
		out, err := solo.RunSSH(ctx, node, remoteReadFileCommand(statusPath), nil)
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
				Phase        string `json:"phase"`
				Revision     string `json:"revision"`
				Error        string `json:"error,omitempty"`
				Environments []struct {
					Name     string `json:"name"`
					Services []struct {
						Name  string `json:"name"`
						State string `json:"state"`
					} `json:"services"`
				} `json:"environments"`
			}
			if err := json.Unmarshal([]byte(out), &status); err != nil {
				a.Printer.Printf("[%s] parse error: %s\n", name, err)
				continue
			}
			line := fmt.Sprintf("[%s] phase=%s revision=%s", name, status.Phase, status.Revision)
			if status.Error != "" {
				line += " error=" + status.Error
			}
			for _, environment := range status.Environments {
				for _, service := range environment.Services {
					line += fmt.Sprintf(" %s/%s=%s", environment.Name, service.Name, service.State)
				}
			}
			a.Printer.Println(line)
		}
	}

	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"nodes": jsonResults})
	}
	return nil
}

func (a *App) SoloSecretsSet(_ context.Context, opts SoloSecretsSetOptions) error {
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
	_, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	if err := solo.SaveSecret(workspaceRoot, opts.Key, opts.Value); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"key": opts.Key, "action": "saved"})
	}
	a.Printer.Println(fmt.Sprintf("Secret %q saved", opts.Key))
	return nil
}

func (a *App) SoloSecretsList(_ context.Context, _ SoloSecretsListOptions) error {
	_, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	keys, err := solo.ListSecrets(workspaceRoot)
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

func (a *App) SoloNodeList(_ context.Context, _ SoloNodeListOptions) error {
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	currentWorkspaceKey := ""
	currentEnvironment := ""
	if discovered, cfg, cfgErr := a.optionalWorkspaceConfig(); cfgErr == nil && cfg != nil {
		currentWorkspaceKey, _ = solo.CanonicalWorkspaceKey(discovered.WorkspaceRoot)
		currentEnvironment = soloEnvironmentName(cfg, "")
	}
	type nodeListItem struct {
		Name                    string                  `json:"name"`
		Node                    config.SoloNode         `json:"node"`
		Attachments             []solo.AttachmentRecord `json:"attachments,omitempty"`
		CurrentEnvironmentBound bool                    `json:"current_environment_bound,omitempty"`
	}
	items := make([]nodeListItem, 0, len(current.Nodes))
	for _, name := range current.NodeNames() {
		attachments := current.AttachmentsForNode(name)
		bound := false
		for _, attachment := range attachments {
			if attachment.WorkspaceKey == currentWorkspaceKey && attachment.Environment == currentEnvironment {
				bound = true
				break
			}
		}
		items = append(items, nodeListItem{
			Name:                    name,
			Node:                    current.Nodes[name],
			Attachments:             attachments,
			CurrentEnvironmentBound: bound,
		})
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"nodes":          items,
		})
	}
	if len(items) == 0 {
		a.Printer.Println("No nodes.")
		return nil
	}
	for _, item := range items {
		line := fmt.Sprintf("%s  host=%s  labels=%s  attachments=%d", item.Name, item.Node.Host, strings.Join(item.Node.Labels, ","), len(item.Attachments))
		if item.CurrentEnvironmentBound {
			line += "  current=yes"
		}
		a.Printer.Println(line)
	}
	return nil
}

func (a *App) SoloNodeAttach(ctx context.Context, opts SoloNodeAttachOptions) error {
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	environmentName := soloEnvironmentName(cfg, opts.Environment)
	attachment, changed, err := a.attachSoloNode(&current, workspaceRoot, environmentName, opts.Node)
	if err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if _, ok := current.Snapshots[attachment.WorkspaceKey+"\n"+attachment.Environment]; ok {
		if err := a.republishSoloNodes(ctx, current, attachment.NodeNames); err != nil {
			return err
		}
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"node":        opts.Node,
			"environment": environmentName,
			"changed":     changed,
		})
	}
	if changed {
		a.Printer.Println("Attached solo node " + opts.Node + " to " + environmentName)
	} else {
		a.Printer.Println("Solo node " + opts.Node + " already attached to " + environmentName)
	}
	return nil
}

func (a *App) SoloNodeDetach(ctx context.Context, opts SoloNodeDetachOptions) error {
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	environmentName := soloEnvironmentName(cfg, opts.Environment)
	nodeNamesBefore, err := current.AttachedNodeNames(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	if len(nodeNamesBefore) == 0 {
		return fmt.Errorf("environment %s has no attached nodes", environmentName)
	}
	if _, changed, err := current.DetachNode(workspaceRoot, environmentName, opts.Node); err != nil {
		return err
	} else if !changed {
		return fmt.Errorf("node %q is not attached to %s", opts.Node, environmentName)
	}
	affectedNodeNames := append([]string{opts.Node}, nodeNamesBefore...)
	affectedNodeNames = normalizeSoloNames(affectedNodeNames)
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if err := a.republishSoloNodes(ctx, current, affectedNodeNames); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"node":        opts.Node,
			"environment": environmentName,
			"changed":     true,
		})
	}
	a.Printer.Println("Detached solo node " + opts.Node + " from " + environmentName)
	return nil
}

func (a *App) SoloSecretsDelete(_ context.Context, opts SoloSecretsDeleteOptions) error {
	_, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	if err := solo.DeleteSecret(workspaceRoot, opts.Key); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"key": opts.Key, "action": "deleted"})
	}
	a.Printer.Println(fmt.Sprintf("Secret %q deleted", opts.Key))
	return nil
}

func (a *App) SoloLogs(ctx context.Context, opts SoloLogsOptions) error {
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	node, ok := current.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found", opts.Node)
	}

	if opts.Follow {
		// Stream directly to the user's terminal — do not buffer.
		return solo.RunSSHInteractive(ctx, node, remoteJournalctlCommand("-u devopsellence-agent -f"), a.Printer.Out, a.Printer.Out)
	}

	out, err := solo.RunSSH(ctx, node, remoteJournalctlCommand("-u devopsellence-agent --no-pager -n 100"), nil)
	if err != nil {
		return err
	}
	a.Printer.Printf("%s", out)
	return nil
}

func (a *App) SoloNodeLabelSet(ctx context.Context, opts SoloNodeLabelSetOptions) error {
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	node, ok := current.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found", opts.Node)
	}
	labels, err := parseSoloLabels(opts.Labels)
	if err != nil {
		return err
	}
	node.Labels = labels
	current.Nodes[opts.Node] = solo.NormalizeNode(node)
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if err := a.republishSoloNodes(ctx, current, soloAffectedNodesForNode(current, opts.Node)); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"node":   opts.Node,
			"labels": labels,
		})
	}
	a.Printer.Println("Updated solo node " + opts.Node + " labels: " + strings.Join(labels, ","))
	return nil
}

func (a *App) SoloAgentInstall(ctx context.Context, opts SoloAgentInstallOptions) error {
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	node, ok := current.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found", opts.Node)
	}
	if !a.Printer.JSON {
		a.Printer.Println("Installing solo agent on " + opts.Node + "...")
	}
	if err := a.installSoloAgent(ctx, opts.Node, node, opts); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"node": opts.Node, "action": "installed"})
	}
	a.Printer.Println("Installed solo agent on " + opts.Node)
	return nil
}

func (a *App) SoloRuntimeDoctor(ctx context.Context, opts SoloDoctorOptions) error {
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	nodes, err := a.resolveSoloNodes(current, opts.Nodes)
	if err != nil {
		return err
	}
	results := make([]map[string]any, 0, len(nodes))
	failed := false
	for _, name := range sortedSoloNodeNames(nodes) {
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
			err := solo.RunSSHInteractive(ctx, node, check.cmd, io.Discard, io.Discard)
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
			return ExitError{Code: 1, Err: fmt.Errorf("solo doctor failed")}
		}
		return nil
	}
	if failed {
		return ExitError{Code: 1, Err: fmt.Errorf("solo doctor failed")}
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
	var current solo.State
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
		var err error
		current, err = a.readSoloState()
		if err != nil {
			return "", err
		}
		if len(current.Nodes) == 0 {
			return "", errors.New("No solo nodes registered yet. Run `devopsellence node create` or `devopsellence setup`.")
		}
		return fmt.Sprintf("%d node(s) registered", len(current.Nodes)), nil
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
	if ok && len(current.Nodes) > 0 {
		return a.SoloRuntimeDoctor(ctx, SoloDoctorOptions{})
	}
	return nil
}

func (a *App) SoloNodeCreate(ctx context.Context, opts SoloNodeCreateOptions) error {
	cfg, workspaceRoot, err := a.loadProjectConfigForSoloUpdate()
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	if opts.Name == "" {
		return fmt.Errorf("node name is required")
	}
	if opts.Deploy && a.Printer.JSON {
		return fmt.Errorf("node create --deploy is not supported with --json")
	}
	if _, ok := current.Nodes[opts.Name]; ok {
		return fmt.Errorf("solo node %q already exists", opts.Name)
	}
	created, err := a.createProviderNode(ctx, opts, cfg.Project)
	if err != nil {
		return err
	}
	if err := current.SetNode(opts.Name, created.Node); err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if !opts.NoInstall || opts.Deploy {
		if !a.Printer.JSON {
			a.Printer.Println("Waiting for SSH on " + opts.Name + "...")
		}
		if err := waitForSoloSSH(ctx, created.Node, 3*time.Minute); err != nil {
			return err
		}
		if err := a.installSoloAgent(ctx, opts.Name, created.Node, SoloAgentInstallOptions{}); err != nil {
			return err
		}
	}
	if opts.Deploy {
		if _, _, err := a.attachSoloNode(&current, workspaceRoot, soloEnvironmentName(cfg, ""), opts.Name); err != nil {
			return err
		}
		if err := a.writeSoloState(current); err != nil {
			return err
		}
		if err := a.SoloDeploy(ctx, SoloDeployOptions{}); err != nil {
			return err
		}
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"node":               opts.Name,
			"host":               created.Node.Host,
			"labels":             created.Labels,
			"provider":           created.ProviderSlug,
			"provider_server_id": created.Server.ID,
			"config_path":        a.ConfigStore.PathFor(workspaceRoot),
		})
	}
	a.Printer.Println("Created solo node " + opts.Name + " at " + created.Node.Host)
	return nil
}

func (a *App) SharedNodeCreate(ctx context.Context, opts SharedNodeCreateOptions) error {
	if opts.Name == "" {
		return fmt.Errorf("node name is required")
	}
	if opts.Deploy {
		return fmt.Errorf("node create --deploy is only available in solo mode")
	}

	var bootstrap nodeBootstrapToken
	if !opts.NoInstall {
		tokens, err := a.ensureAuth(ctx, a.Printer.Interactive, false)
		if err != nil {
			return err
		}
		run := func(ctx context.Context, update, _ func(string)) error {
			var err error
			bootstrap, err = a.createNodeBootstrapToken(ctx, &tokens, opts.NodeBootstrapOptions, update)
			return err
		}
		if !a.Printer.JSON && a.Printer.Interactive {
			if err := ui.RunTask(ctx, a.Printer.Out, "Node Bootstrap", run); err != nil {
				return err
			}
		} else {
			if err := run(ctx, func(string) {}, func(string) {}); err != nil {
				return err
			}
		}
	}

	projectName := opts.Project
	if !opts.Unassigned && bootstrap.Workspace.Project.Name != "" {
		projectName = bootstrap.Workspace.Project.Name
	}
	created, err := a.createProviderNode(ctx, opts.SoloNodeCreateOptions, projectName)
	if err != nil {
		return err
	}
	if opts.NoInstall {
		if a.Printer.JSON {
			return a.Printer.PrintJSON(map[string]any{
				"schema_version":     outputSchemaVersion,
				"node":               opts.Name,
				"host":               created.Node.Host,
				"labels":             created.Labels,
				"provider":           created.ProviderSlug,
				"provider_server_id": created.Server.ID,
				"registered":         false,
			})
		}
		a.Printer.Println("Created shared node " + opts.Name + " at " + created.Node.Host + " without installing the agent")
		return nil
	}

	installCommand := strings.TrimSpace(stringFromMap(bootstrap.Result, "install_command"))
	if installCommand == "" {
		return fmt.Errorf("node bootstrap response did not include install_command")
	}
	if !a.Printer.JSON {
		a.Printer.Println("Waiting for SSH on " + opts.Name + "...")
	}
	if err := waitForSoloSSH(ctx, created.Node, 3*time.Minute); err != nil {
		return err
	}
	if !a.Printer.JSON {
		a.Printer.Println("Installing and registering devopsellence agent on " + opts.Name + "...")
	}
	stdout, stderr := a.Printer.Out, a.Printer.Err
	if a.Printer.JSON {
		stdout, stderr = io.Discard, io.Discard
	}
	if err := solo.RunSSHInteractive(ctx, created.Node, installCommand, stdout, stderr); err != nil {
		return err
	}

	if a.Printer.JSON {
		result := map[string]any{
			"schema_version":     outputSchemaVersion,
			"node":               opts.Name,
			"host":               created.Node.Host,
			"labels":             created.Labels,
			"provider":           created.ProviderSlug,
			"provider_server_id": created.Server.ID,
			"organization_id":    bootstrap.Organization.ID,
			"organization_name":  bootstrap.Organization.Name,
			"registered":         true,
		}
		if opts.Unassigned {
			result["assignment_mode"] = "unassigned"
		} else {
			result["assignment_mode"] = firstNonEmpty(stringFromMap(bootstrap.Result, "assignment_mode"), "environment")
			result["project_name"] = bootstrap.Workspace.Project.Name
			result["environment_id"] = bootstrap.Workspace.Environment.ID
			result["environment_name"] = bootstrap.Workspace.Environment.Name
		}
		return a.Printer.PrintJSON(result)
	}
	a.Printer.Println("Created shared node " + opts.Name + " at " + created.Node.Host)
	return nil
}

func (a *App) SoloNodeRemove(ctx context.Context, opts SoloNodeRemoveOptions) error {
	if !opts.Yes {
		return fmt.Errorf("node remove requires --yes")
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	node, ok := current.Nodes[opts.Name]
	if !ok {
		return fmt.Errorf("node %q not found", opts.Name)
	}
	if current.NodeHasAttachments(opts.Name) {
		return fmt.Errorf("node %q still has attached environments; detach it first", opts.Name)
	}
	if node.Provider == "" || node.ProviderServerID == "" {
		return fmt.Errorf("node %q does not have provider metadata; refusing provider delete", opts.Name)
	}
	provider, err := a.resolveSoloProvider(node.Provider)
	if err != nil {
		return err
	}
	if err := provider.DeleteServer(ctx, node.ProviderServerID); err != nil {
		return err
	}
	current.RemoveNode(opts.Name)
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{"node": opts.Name, "action": "deleted"})
	}
	a.Printer.Println("Removed solo node " + opts.Name)
	return nil
}

func (a *App) SoloSetup(ctx context.Context, _ SoloSetupOptions) error {
	if !a.Printer.Interactive {
		return fmt.Errorf("solo setup requires an interactive terminal; use node create or edit devopsellence.yml")
	}
	mode, err := a.promptLine("Node source (existing/hetzner)", "existing")
	if err != nil {
		return err
	}
	name, err := a.promptLine("Node name", "prod-1")
	if err != nil {
		return err
	}
	labels, err := a.promptLine("Labels", strings.Join(config.SoloDefaultLabels, ","))
	if err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(mode), "hetzner") {
		region, err := a.promptLine("Hetzner region", defaultHetznerRegion)
		if err != nil {
			return err
		}
		size, err := a.promptLine("Hetzner size", defaultHetznerSize)
		if err != nil {
			return err
		}
		sshPublicKey, err := a.promptLine("SSH public key path", defaultSoloSSHPublicKeyPath())
		if err != nil {
			return err
		}
		if err := a.SoloNodeCreate(ctx, SoloNodeCreateOptions{
			Name:         name,
			Provider:     "hetzner",
			Region:       region,
			Size:         size,
			Labels:       labels,
			SSHPublicKey: sshPublicKey,
		}); err != nil {
			return err
		}
		if err := a.SoloNodeAttach(ctx, SoloNodeAttachOptions{Node: name}); err != nil {
			return err
		}
		return a.SoloRuntimeDoctor(ctx, SoloDoctorOptions{Nodes: []string{name}})
	}
	host, err := a.promptLine("Host", "")
	if err != nil {
		return err
	}
	user, err := a.promptLine("SSH user", "root")
	if err != nil {
		return err
	}
	sshKey, err := a.promptLine("SSH private key path", defaultSoloSSHPrivateKeyPath())
	if err != nil {
		return err
	}
	cfg, workspaceRoot, err := a.loadProjectConfigForSoloUpdate()
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	parsedLabels, err := parseSoloLabels(labels)
	if err != nil {
		return err
	}
	node := config.SoloNode{
		Host:          host,
		User:          user,
		SSHKey:        strings.TrimSpace(sshKey),
		Port:          22,
		AgentStateDir: "/var/lib/devopsellence",
		Labels:        parsedLabels,
	}
	if err := current.SetNode(name, node); err != nil {
		return err
	}
	if _, _, err := a.attachSoloNode(&current, workspaceRoot, soloEnvironmentName(cfg, ""), name); err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if err := a.installSoloAgent(ctx, name, node, SoloAgentInstallOptions{}); err != nil {
		return err
	}
	return a.SoloRuntimeDoctor(ctx, SoloDoctorOptions{Nodes: []string{name}})
}

func (a *App) IngressSet(_ context.Context, opts IngressSetOptions) error {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return err
	}
	cfg, err := a.ConfigStore.Read(discovered.WorkspaceRoot)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = soloDefaultProjectConfig(discovered)
	}
	hosts := normalizeIngressHosts(opts.Hosts)
	if len(hosts) == 0 {
		return fmt.Errorf("ingress set requires at least one --host")
	}
	serviceName := strings.TrimSpace(opts.Service)
	if serviceName == "" && cfg.Ingress != nil {
		serviceName = strings.TrimSpace(cfg.Ingress.Service)
	}
	if serviceName == "" {
		var ok bool
		serviceName, ok = cfg.PrimaryWebServiceName()
		if !ok {
			return fmt.Errorf("ingress set requires --service when the primary web service cannot be inferred")
		}
	}
	tlsMode := strings.TrimSpace(opts.TLSMode)
	if tlsMode == "" {
		tlsMode = "auto"
	}
	switch tlsMode {
	case "auto", "off", "manual":
	default:
		return fmt.Errorf("ingress tls mode must be auto, off, or manual")
	}
	redirectHTTP := tlsMode == "auto"
	if opts.RedirectHTTPChanged {
		redirectHTTP = opts.RedirectHTTP
	}
	cfg.Ingress = &config.IngressConfig{
		Hosts:   hosts,
		Service: serviceName,
		TLS: config.IngressTLSConfig{
			Mode:           tlsMode,
			Email:          strings.TrimSpace(opts.TLSEmail),
			CADirectoryURL: strings.TrimSpace(opts.TLSCADirectoryURL),
		},
		RedirectHTTP: redirectHTTP,
	}
	written, err := a.ConfigStore.Write(discovered.WorkspaceRoot, *cfg)
	if err != nil {
		return err
	}
	if a.Printer.JSON {
		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"ingress":        written.Ingress,
			"config_path":    a.ConfigStore.PathFor(discovered.WorkspaceRoot),
		})
	}
	a.Printer.Println("Ingress hosts:", strings.Join(written.Ingress.Hosts, ","))
	a.Printer.Println("TLS:", written.Ingress.TLS.Mode)
	a.Printer.Println("Config:", a.ConfigStore.PathFor(discovered.WorkspaceRoot))
	return nil
}

func (a *App) IngressCheck(ctx context.Context, opts IngressCheckOptions) error {
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	nodeNames, err := current.AttachedNodeNames(workspaceRoot, soloEnvironmentName(cfg, ""))
	if err != nil {
		return err
	}
	nodes, err := a.resolveSoloNodes(current, nodeNames)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(opts.Wait)
	for {
		report, err := ingressDNSReport(ctx, cfg, nodes)
		if err != nil {
			return err
		}
		if report.OK || opts.Wait <= 0 || time.Now().After(deadline) {
			if a.Printer.JSON {
				if err := a.Printer.PrintJSON(report); err != nil {
					return err
				}
			} else {
				printIngressDNSReport(a.Printer, report)
			}
			if !report.OK {
				return ExitError{Code: 1, Err: fmt.Errorf("ingress DNS is not ready")}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *App) soloProgress(nodeName string) func(string) {
	if a.Printer.JSON {
		return func(string) {}
	}
	return func(message string) {
		a.Printer.Println("[" + nodeName + "] " + message)
	}
}

func (a *App) installSoloAgent(ctx context.Context, nodeName string, node config.SoloNode, opts SoloAgentInstallOptions) error {
	reporter := newSoloInstallReporter(ctx, a.Printer, nodeName)
	defer reporter.Close()
	return installSoloAgent(ctx, node, opts, reporter)
}

func (a *App) loadSoloProjectConfig() (*config.ProjectConfig, string, error) {
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
	return cfg, workspaceRoot, nil
}

func (a *App) loadProjectConfigForSoloUpdate() (*config.ProjectConfig, string, error) {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return nil, "", err
	}
	cfg, err := a.ConfigStore.Read(discovered.WorkspaceRoot)
	if err != nil {
		return nil, "", err
	}
	if cfg == nil {
		cfg = soloDefaultProjectConfig(discovered)
	}
	return cfg, discovered.WorkspaceRoot, nil
}

func soloEnvironmentName(cfg *config.ProjectConfig, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	if cfg == nil || strings.TrimSpace(cfg.DefaultEnvironment) == "" {
		return config.DefaultEnvironment
	}
	return strings.TrimSpace(cfg.DefaultEnvironment)
}

func (a *App) soloStatusNodes(opts SoloStatusOptions) (map[string]config.SoloNode, error) {
	current, err := a.readSoloState()
	if err != nil {
		return nil, err
	}
	if len(opts.Nodes) > 0 {
		return a.resolveSoloNodes(current, opts.Nodes)
	}
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return nil, err
	}
	nodeNames, err := current.AttachedNodeNames(workspaceRoot, soloEnvironmentName(cfg, ""))
	if err != nil {
		return nil, err
	}
	return a.resolveSoloNodes(current, nodeNames)
}

func (a *App) attachSoloNode(current *solo.State, workspaceRoot, environmentName, nodeName string) (solo.AttachmentRecord, bool, error) {
	if current == nil {
		return solo.AttachmentRecord{}, false, fmt.Errorf("solo state is required")
	}
	return current.AttachNode(workspaceRoot, environmentName, nodeName)
}

func soloAffectedNodesForNode(current solo.State, nodeName string) []string {
	affected := []string{nodeName}
	for _, key := range current.AttachmentKeysForNode(nodeName) {
		attachment := current.Attachments[key]
		affected = append(affected, attachment.NodeNames...)
	}
	return normalizeSoloNames(affected)
}

func soloDefaultProjectConfig(discovered discovery.Result) *config.ProjectConfig {
	cfg := config.DefaultProjectConfigForType("solo", discovered.ProjectName, config.DefaultEnvironment, discovered.AppType)
	if discovered.InferredWebPort > 0 {
		if serviceName, ok := cfg.PrimaryWebServiceName(); ok {
			service := cfg.Services[serviceName]
			for i := range service.Ports {
				if service.Ports[i].Name == "http" {
					service.Ports[i].Port = discovered.InferredWebPort
				}
			}
			if service.Healthcheck != nil {
				service.Healthcheck.Port = discovered.InferredWebPort
			}
			cfg.Services[serviceName] = service
		}
	}
	return &cfg
}

type ingressDNSReportResult struct {
	SchemaVersion int                    `json:"schema_version"`
	OK            bool                   `json:"ok"`
	ExpectedIPs   []string               `json:"expected_ips"`
	Hosts         []ingressDNSHostResult `json:"hosts"`
}

type ingressDNSHostResult struct {
	Host     string   `json:"host"`
	OK       bool     `json:"ok"`
	Resolved []string `json:"resolved,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func (a *App) checkIngressBeforeDeploy(ctx context.Context, cfg *config.ProjectConfig, nodes map[string]config.SoloNode, skip bool) error {
	if skip || cfg == nil || cfg.Ingress == nil || cfg.Ingress.TLS.Mode != "auto" {
		return nil
	}
	report, err := ingressDNSReport(ctx, cfg, nodes)
	if err != nil {
		return err
	}
	if report.OK {
		return nil
	}
	if !a.Printer.JSON {
		printIngressDNSReport(a.Printer, report)
	}
	return fmt.Errorf("ingress DNS is not ready; update DNS or pass --skip-dns-check")
}

func ingressDNSReport(ctx context.Context, cfg *config.ProjectConfig, selected map[string]config.SoloNode) (ingressDNSReportResult, error) {
	hosts := []string{}
	if cfg != nil && cfg.Ingress != nil {
		hosts = normalizeIngressHosts(cfg.Ingress.Hosts)
	}
	if len(hosts) == 0 {
		return ingressDNSReportResult{}, fmt.Errorf("ingress.hosts is not configured")
	}
	expected := webNodeIPs(cfg, selected)
	if len(expected) == 0 {
		return ingressDNSReportResult{}, fmt.Errorf("no web nodes configured")
	}
	report := ingressDNSReportResult{
		SchemaVersion: outputSchemaVersion,
		OK:            true,
		ExpectedIPs:   expected,
		Hosts:         make([]ingressDNSHostResult, 0, len(hosts)),
	}
	for _, host := range hosts {
		result := ingressDNSHostResult{Host: host}
		resolved, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil {
			result.Error = err.Error()
		}
		result.Resolved = normalizeIngressHosts(resolved)
		result.Missing = missingStrings(expected, result.Resolved)
		result.OK = err == nil && len(result.Missing) == 0
		if !result.OK {
			report.OK = false
		}
		report.Hosts = append(report.Hosts, result)
	}
	return report, nil
}

func printIngressDNSReport(printer output.Printer, report ingressDNSReportResult) {
	printer.Println("Expected IPs:", strings.Join(report.ExpectedIPs, ","))
	for _, host := range report.Hosts {
		state := "ok"
		if !host.OK {
			state = "fail"
		}
		line := fmt.Sprintf("%s  %s", state, host.Host)
		if len(host.Resolved) > 0 {
			line += " resolved=" + strings.Join(host.Resolved, ",")
		}
		if len(host.Missing) > 0 {
			line += " missing=" + strings.Join(host.Missing, ",")
		}
		if host.Error != "" {
			line += " error=" + host.Error
		}
		printer.Println(line)
	}
}

func webNodeIPs(cfg *config.ProjectConfig, selected map[string]config.SoloNode) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	ips := []string{}
	for _, name := range sortedSoloNodeNames(selected) {
		node := selected[name]
		if !soloNodeCanRunIngress(node, cfg) {
			continue
		}
		host := strings.TrimSpace(node.Host)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		ips = append(ips, host)
	}
	sort.Strings(ips)
	return ips
}

func normalizeIngressHosts(values []string) []string {
	seen := map[string]bool{}
	normalized := []string{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			normalized = append(normalized, part)
		}
	}
	sort.Strings(normalized)
	return normalized
}

func normalizeSoloNames(values []string) []string {
	seen := map[string]bool{}
	normalized := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func missingStrings(want, have []string) []string {
	haveSet := map[string]bool{}
	for _, value := range have {
		haveSet[value] = true
	}
	missing := []string{}
	for _, value := range want {
		if !haveSet[value] {
			missing = append(missing, value)
		}
	}
	return missing
}

// shellQuote returns a POSIX shell safe single-quoted string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func parseSoloLabels(value string) ([]string, error) {
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
		seen[label] = true
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("at least one solo node label is required")
	}
	return labels, nil
}

func applySoloRailsMasterKey(workspaceRoot string, cfg *config.ProjectConfig, secrets map[string]string) (string, error) {
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
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		if service.Kind == config.ServiceKindAccessory {
			continue
		}
		if addServiceSecretRef(&service, railsMasterKeySecretName) {
			cfg.Services[serviceName] = service
			services = append(services, serviceName)
		}
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

func installSoloAgent(ctx context.Context, node config.SoloNode, opts SoloAgentInstallOptions, reporter soloInstallReporter) error {
	if strings.TrimSpace(opts.AgentBinary) != "" {
		remotePath := fmt.Sprintf("/tmp/devopsellence-agent-%d", time.Now().UnixNano())
		reporter.Progress("Uploading agent binary...")
		file, err := os.Open(opts.AgentBinary)
		if err != nil {
			return fmt.Errorf("open agent binary: %w", err)
		}
		defer file.Close()
		if err := solo.RunSSHStream(ctx, node, "cat > "+shellQuote(remotePath), file); err != nil {
			return fmt.Errorf("upload agent binary: %w", err)
		}
		defer solo.RunSSHInteractive(ctx, node, "rm -f "+shellQuote(remotePath), io.Discard, io.Discard)
		reporter.Progress("Installing Docker, agent, and systemd service...")
		return runSoloAgentInstallScript(ctx, node, soloAgentInstallScript(soloAgentInstallScriptOptions{
			StateDir:        firstNonEmpty(node.AgentStateDir, "/var/lib/devopsellence"),
			LocalBinaryPath: remotePath,
		}), reporter)
	}

	baseURL := strings.TrimRight(firstNonEmpty(opts.BaseURL, os.Getenv("DEVOPSELLENCE_BASE_URL"), api.DefaultBaseURL), "/")
	reporter.Progress("Installing Docker, agent, and systemd service...")
	return runSoloAgentInstallScript(ctx, node, soloAgentInstallScript(soloAgentInstallScriptOptions{
		StateDir:     firstNonEmpty(node.AgentStateDir, "/var/lib/devopsellence"),
		BaseURL:      baseURL,
		AgentVersion: releasedAgentVersionForInstall(),
	}), reporter)
}

func runSoloAgentInstallScript(ctx context.Context, node config.SoloNode, script string, reporter soloInstallReporter) error {
	writer := reporter.Stream()
	return solo.RunSSHInteractiveWithStdin(ctx, node, "bash -s", strings.NewReader(script), writer, writer)
}

type soloAgentInstallScriptOptions struct {
	StateDir        string
	BaseURL         string
	AgentVersion    string
	LocalBinaryPath string
}

// Accept stable tags, semver-style prereleases, and workflow-generated
// prerelease tags such as branch-name-abcdef1 while keeping the value safe
// for query-string use in the install script.
var releaseVersionPattern = regexp.MustCompile(`^[0-9A-Za-z._-]+$`)

func soloAgentInstallScript(opts soloAgentInstallScriptOptions) string {
	stateDir := strings.TrimSpace(opts.StateDir)
	if stateDir == "" {
		stateDir = "/var/lib/devopsellence"
	}
	authStatePath := stateDir + "/auth.json"
	overridePath := stateDir + "/desired-state-override.json"
	envoyBootstrapPath := stateDir + "/envoy/envoy.yaml"
	agentVersion := strings.TrimSpace(opts.AgentVersion)
	localBinary := strings.TrimSpace(opts.LocalBinaryPath)
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	return fmt.Sprintf(`set -euo pipefail

STATE_DIR=%s
AGENT_BIN=/usr/local/bin/devopsellence-agent
SERVICE_FILE=/etc/systemd/system/devopsellence-agent.service
BASE_URL=%s
AGENT_VERSION=%s
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
  ARTIFACT_NAME="agent-$OS-$ARCH"
  DOWNLOAD_URL="$BASE_URL/agent/download?os=$OS&arch=$ARCH"
  CHECKSUM_URL="$BASE_URL/agent/checksums"
  if [ -n "$AGENT_VERSION" ]; then
    DOWNLOAD_URL="$DOWNLOAD_URL&version=$AGENT_VERSION"
    CHECKSUM_URL="$CHECKSUM_URL?version=$AGENT_VERSION"
  fi
  curl -fsSL "$DOWNLOAD_URL" -o "$TMP_BIN"
  curl -fsSL "$CHECKSUM_URL" -o "$TMP_SUMS"
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
ExecStart=$AGENT_BIN --mode=solo --auth-state-path=%s --desired-state-override-path=%s --envoy-bootstrap-path=%s
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
`, shellQuote(stateDir), shellQuote(baseURL), shellQuote(agentVersion), shellQuote(localBinary), systemdQuoteArg(authStatePath), systemdQuoteArg(overridePath), systemdQuoteArg(envoyBootstrapPath))
}

func releasedAgentVersionForInstall() string {
	version := strings.TrimSpace(cliversion.Version)
	if version != "" && version != "dev" && releaseVersionPattern.MatchString(version) {
		return version
	}
	return ""
}

func systemdQuoteArg(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, `%`, `%%`)
	return `"` + escaped + `"`
}

func waitForSoloProviderServer(ctx context.Context, provider providers.Provider, server providers.Server) (providers.Server, error) {
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

func waitForSoloSSH(ctx context.Context, node config.SoloNode, timeout time.Duration) error {
	return waitForSoloSSHWithProbe(ctx, node, timeout, 10*time.Second, 2*time.Second, func(ctx context.Context) error {
		_, err := solo.RunSSH(ctx, node, "true", nil)
		return err
	})
}

func waitForSoloSSHWithProbe(ctx context.Context, node config.SoloNode, timeout, probeTimeout, retryInterval time.Duration, probe func(context.Context) error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		err := probe(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for SSH on %s@%s: last error: %w", node.User, node.Host, lastErr)
			}
			return fmt.Errorf("timed out waiting for SSH on %s@%s", node.User, node.Host)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}
	}
}

func readSoloSSHPublicKey(path string) (string, string, error) {
	path = strings.TrimSpace(path)
	candidates := []string{path}
	if path == "" {
		candidates = defaultSoloSSHPublicKeyCandidates()
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

func defaultSoloSSHPrivateKeyPath() string {
	for _, candidate := range defaultSoloSSHPrivateKeyCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func defaultSoloSSHPublicKeyPath() string {
	for _, candidate := range defaultSoloSSHPublicKeyCandidates() {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func defaultSoloSSHPublicKeyCandidates() []string {
	keys := defaultSoloSSHPrivateKeyCandidates()
	candidates := make([]string, 0, len(keys))
	for _, key := range keys {
		candidates = append(candidates, key+".pub")
	}
	return candidates
}

func defaultSoloSSHPrivateKeyCandidates() []string {
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
		return nil, fmt.Errorf("solo deploy requires exactly one build platform, got %d", len(platforms))
	}
	return []string{"build", "--platform", platforms[0], "-f", dockerfile, "-t", tag, contextPath}, nil
}

func soloImageTag(project, revision string) string {
	return fmt.Sprintf("%s:%s", discovery.Slugify(project), revision)
}

// transferImage pipes `docker save | gzip` through ssh to `gunzip | docker load`.
func transferImage(ctx context.Context, node config.SoloNode, imageTag string, progress func(string)) error {
	if progress == nil {
		progress = func(string) {}
	}
	progress("Checking remote Docker...")
	if _, err := solo.RunSSH(ctx, node, remoteDockerCheckCommand(), nil); err != nil {
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
	sshErr := solo.RunSSHStream(ctx, node, remoteDockerLoadCommand(), meter)
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

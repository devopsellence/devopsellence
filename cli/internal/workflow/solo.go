package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devopsellence/cli/internal/api"
	"github.com/devopsellence/cli/internal/discovery"
	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/cli/internal/solo/providers"
	cliversion "github.com/devopsellence/cli/internal/version"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
)

type SoloDeployOptions struct {
	SkipDNSCheck bool
}

type SoloStatusOptions struct {
	Nodes []string
}

type SoloSecretsSetOptions struct {
	Key         string
	ServiceName string
	Environment string
	Store       string
	Reference   string
	Value       string
	ValueStdin  bool
}

type SoloSecretsListOptions struct {
	ServiceName string
	Environment string
}

type SoloSecretsDeleteOptions struct {
	Key         string
	ServiceName string
	Environment string
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
	Node  string
	Lines int
}

type SoloNodeDiagnoseOptions struct {
	Node string
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

type SoloAgentUninstallOptions struct {
	Node          string
	Yes           bool
	KeepWorkloads bool
}

type SoloDoctorOptions struct {
	Nodes []string
}

type SoloNodeCreateOptions struct {
	Name         string
	Provider     string
	Host         string
	User         string
	Port         int
	SSHKey       string
	Region       string
	Size         string
	Image        string
	Labels       string
	SSHPublicKey string
	Install      bool
	Attach       bool
}

type SoloNodeRemoveOptions struct {
	Name string
	Yes  bool
}

type SharedSoloNodeCreateOptions struct {
	SoloNodeCreateOptions
	NodeBootstrapOptions
}

type providerNodeCreateResult struct {
	Node         config.Node
	Server       providers.Server
	Labels       []string
	ProviderSlug string
}

type SoloInitOptions struct{}

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

type soloNodeStatus struct {
	Phase        string                      `json:"phase"`
	Revision     string                      `json:"revision"`
	Error        string                      `json:"error,omitempty"`
	Environments []soloNodeStatusEnvironment `json:"environments"`
}

type soloNodeStatusEnvironment struct {
	Name     string                  `json:"name"`
	Services []soloNodeStatusService `json:"services"`
}

type soloNodeStatusService struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type soloNodeStatusResult struct {
	Missing bool
	Raw     json.RawMessage
	Status  soloNodeStatus
}

func (a *App) createProviderNode(ctx context.Context, opts SoloNodeCreateOptions, projectName string) (providerNodeCreateResult, error) {
	if opts.Name == "" {
		return providerNodeCreateResult{}, fmt.Errorf("node name is required")
	}
	labels, err := parseSoloLabels(firstNonEmpty(opts.Labels, strings.Join(config.DefaultNodeLabels, ",")))
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	if strings.TrimSpace(opts.Provider) == "" {
		return providerNodeCreateResult{}, ExitError{Code: 2, Err: fmt.Errorf("node create requires --provider")}
	}
	providerSlug, err := normalizeProvider(opts.Provider)
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
	if err := a.ensureProviderTokenConfigured(ctx, providerSlug); err != nil {
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
	node := config.Node{
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

func existingSSHNodeFromCreateOptions(opts SoloNodeCreateOptions) (config.Node, []string, error) {
	labels, err := parseSoloLabels(firstNonEmpty(opts.Labels, strings.Join(config.DefaultNodeLabels, ",")))
	if err != nil {
		return config.Node{}, nil, err
	}
	user := strings.TrimSpace(opts.User)
	if user == "" {
		user = "root"
	}
	sshKey, err := expandSoloSSHKeyPath(opts.SSHKey)
	if err != nil {
		return config.Node{}, nil, err
	}
	port := opts.Port
	if port < 1 || port > 65535 {
		return config.Node{}, nil, ExitError{Code: 2, Err: fmt.Errorf("ssh port must be between 1 and 65535")}
	}
	node := config.Node{
		Host:          strings.TrimSpace(opts.Host),
		User:          user,
		Port:          port,
		SSHKey:        sshKey,
		AgentStateDir: "/var/lib/devopsellence",
		Labels:        labels,
	}
	return node, labels, nil
}

func expandSoloSSHKeyPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if path == "~" {
		return "", fmt.Errorf("ssh key path %q must reference a private key file, not the home directory", path)
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ssh key path: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("ssh key path %q does not exist", path)
		}
		return "", fmt.Errorf("stat ssh key path %q: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("ssh key path %q must be a file, not a directory", path)
	}
	return path, nil
}

func (a *App) SoloDeploy(ctx context.Context, opts SoloDeployOptions) error {
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
	if len(attachedNodeNames) == 0 {
		return fmt.Errorf("no nodes attached to %s; run `devopsellence node attach <name>`", environmentName)
	}
	nodes, err := a.resolveNodes(current, attachedNodeNames)
	if err != nil {
		return err
	}
	if _, err := validateNodeSchedule(cfg, nodes); err != nil {
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

	buildCtx := filepath.Join(workspaceRoot, cfg.Build.Context)
	dockerfile := filepath.Join(workspaceRoot, cfg.Build.Dockerfile)
	deployProgress := a.soloProgress("devopsellence deploy", map[string]any{"image": imageTag})
	deployProgress("Building local Docker image...")
	if err := dockerBuild(ctx, buildCtx, dockerfile, imageTag, cfg.Build.Platforms); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	deployProgress("Local Docker image built.")

	secrets, err := a.resolveSoloSecretValues(ctx, current, workspaceRoot, environmentName, cfg)
	if err != nil {
		return fmt.Errorf("load secrets: %w", err)
	}

	snapshot, err := solo.BuildDeploySnapshotWithScopedSecrets(cfg, workspaceRoot, a.ConfigStore.PathFor(workspaceRoot), imageTag, shortSHA, secrets)
	if err != nil {
		return err
	}
	if _, err := current.SaveSnapshot(solo.RedactDeploySnapshotSecrets(snapshot, cfg)); err != nil {
		return err
	}
	// Persist desired local state first so follow-up republish operations can
	// recover cleanly from partial remote updates.
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	desiredStateRevisions, err := a.republishNodes(ctx, current, attachedNodeNames)
	if err != nil {
		return err
	}
	if err := a.waitForSoloRollout(ctx, nodes, desiredStateRevisions); err != nil {
		var rolloutErr *soloRolloutError
		if errors.As(err, &rolloutErr) {
			rolloutErr.Healthchecks = soloDeployHealthcheckDetails(cfg)
			return ExitError{Code: 1, Err: rolloutErr}
		}
		var timeoutErr *soloRolloutTimeoutError
		if errors.As(err, &timeoutErr) {
			timeoutErr.Healthchecks = soloDeployHealthcheckDetails(cfg)
			return ExitError{Code: 1, Err: timeoutErr}
		}
		return err
	}

	payload := map[string]any{
		"schema_version":          outputSchemaVersion,
		"workload_revision":       shortSHA,
		"desired_state_revisions": desiredStateRevisions,
		"image":                   imageTag,
		"environment":             environmentName,
		"nodes":                   sortedNodeNames(nodes),
		"phase":                   "settled",
	}
	if urls := soloStatusPublicURLs(cfg, nodes); len(urls) > 0 {
		payload["public_urls"] = urls
		payload["next_steps"] = append([]string{"devopsellence status", "curl " + urls[0]}, soloNodeLogNextSteps(nodes)...)
	} else {
		payload["next_steps"] = append([]string{"devopsellence status"}, soloNodeLogNextSteps(nodes)...)
	}
	return a.Printer.PrintJSON(payload)

}

func validateNodeSchedule(cfg *config.ProjectConfig, nodes map[string]config.Node) (string, error) {
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		serviceKind := config.InferredServiceKind(serviceName, service)
		scheduled := false
		for _, nodeName := range sortedNodeNames(nodes) {
			if soloNodeCanRunKind(nodes[nodeName], serviceKind) {
				scheduled = true
				break
			}
		}
		if !scheduled {
			return "", fmt.Errorf("solo deploy requires at least one selected node labeled %q for service %q", serviceKind, serviceName)
		}
	}
	release := cfg.ReleaseTask()
	if release == nil {
		return "", nil
	}
	for _, nodeName := range sortedNodeNames(nodes) {
		if soloNodeCanRunKind(nodes[nodeName], config.InferredServiceKind(release.Service, cfg.Services[release.Service])) {
			return nodeName, nil
		}
	}
	return "", fmt.Errorf("solo deploy requires at least one selected node labeled for release task service %q", release.Service)
}

func soloNodeCanRunKind(node config.Node, kind string) bool {
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

func soloNodeCanRunIngress(node config.Node, cfg *config.ProjectConfig) bool {
	if cfg == nil || cfg.Ingress == nil {
		return false
	}
	serviceNames := map[string]bool{}
	for _, rule := range cfg.Ingress.Rules {
		serviceName := strings.TrimSpace(rule.Target.Service)
		if serviceName == "" || serviceNames[serviceName] {
			continue
		}
		serviceNames[serviceName] = true
		service, ok := cfg.Services[serviceName]
		if !ok {
			return false
		}
		kind := config.InferredServiceKind(serviceName, service)
		if !soloNodeCanRunKind(node, kind) {
			return false
		}
	}
	return len(serviceNames) > 0
}

func sortedNodeNames(nodes map[string]config.Node) []string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parseNodeStatusPayload(data []byte) (soloNodeStatus, json.RawMessage, error) {
	var status soloNodeStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return soloNodeStatus{}, nil, err
	}
	return status, json.RawMessage(data), nil
}

func readNodeStatus(ctx context.Context, node config.Node) (soloNodeStatusResult, error) {
	statusPath := path.Join(firstNonEmpty(node.AgentStateDir, "/var/lib/devopsellence"), "status.json")
	out, err := solo.RunSSH(ctx, node, remoteReadOptionalFileCommand(statusPath, soloStatusMissingSentinel), nil)
	if err != nil {
		return soloNodeStatusResult{}, err
	}
	if strings.TrimSpace(out) == soloStatusMissingSentinel {
		return soloNodeStatusResult{Missing: true}, nil
	}
	status, raw, err := parseNodeStatusPayload([]byte(out))
	if err != nil {
		return soloNodeStatusResult{}, fmt.Errorf("invalid status JSON: %w", err)
	}
	return soloNodeStatusResult{
		Raw:    raw,
		Status: status,
	}, nil
}

type soloRolloutError struct {
	Node         string
	Message      string
	Healthchecks []map[string]any
}

func (e *soloRolloutError) Error() string {
	if e == nil {
		return "rollout failed"
	}
	return fmt.Sprintf("rollout failed on %s: %s", e.Node, e.Message)
}

func (e *soloRolloutError) ErrorFields() map[string]any {
	if e == nil {
		return map[string]any{}
	}
	fields := map[string]any{
		"node": e.Node,
		"next_steps": []string{
			"devopsellence status",
			"devopsellence node logs " + shellQuote(e.Node) + " --lines 100",
		},
	}
	if len(e.Healthchecks) > 0 {
		fields["healthchecks"] = e.Healthchecks
	}
	return fields
}

type soloRolloutTimeoutError struct {
	Summary      string
	Nodes        []string
	Healthchecks []map[string]any
}

func (e *soloRolloutTimeoutError) Error() string {
	if e == nil {
		return "timed out waiting for solo rollout"
	}
	return "timed out waiting for solo rollout: " + e.Summary
}

func (e *soloRolloutTimeoutError) ErrorFields() map[string]any {
	if e == nil {
		return map[string]any{}
	}
	steps := []string{"devopsellence status"}
	for _, node := range e.Nodes {
		steps = append(steps, "devopsellence node logs "+shellQuote(node)+" --lines 100")
	}
	fields := map[string]any{"next_steps": steps}
	if len(e.Healthchecks) > 0 {
		fields["healthchecks"] = e.Healthchecks
	}
	return fields
}

func soloDeployHealthcheckDetails(cfg *config.ProjectConfig) []map[string]any {
	if cfg == nil {
		return nil
	}
	details := []map[string]any{}
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		if service.Healthcheck == nil {
			continue
		}
		details = append(details, map[string]any{
			"service_name": serviceName,
			"path":         service.Healthcheck.Path,
			"port":         service.Healthcheck.Port,
		})
	}
	return details
}

func (a *App) waitForSoloRollout(ctx context.Context, nodes map[string]config.Node, expectedRevisions map[string]string) error {
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

	latestSummary := "rollout pending"
	nodeNames := sortedNodeNames(nodes)
	for {
		pendingCount := 0
		reconcilingCount := 0
		settledCount := 0
		details := []string{}

		for _, name := range nodeNames {
			expectedRevision := strings.TrimSpace(expectedRevisions[name])
			if expectedRevision == "" {
				return fmt.Errorf("missing desired state revision for node %s", name)
			}

			result, err := readNodeStatus(rolloutCtx, nodes[name])
			if err != nil {
				if errors.Is(rolloutCtx.Err(), context.DeadlineExceeded) {
					return ExitError{Code: 1, Err: &soloRolloutTimeoutError{Summary: latestSummary, Nodes: nodeNames}}
				}
				return fmt.Errorf("[%s] read status: %w", name, err)
			}

			switch {
			case result.Missing:
				pendingCount++
				details = append(details, name+"=missing")
			case strings.TrimSpace(result.Status.Revision) != expectedRevision:
				pendingCount++
				details = append(details, fmt.Sprintf("%s=revision:%s", name, firstNonEmpty(strings.TrimSpace(result.Status.Revision), "none")))
			default:
				switch strings.TrimSpace(result.Status.Phase) {
				case "settled":
					settledCount++
				case "error":
					message := firstNonEmpty(strings.TrimSpace(result.Status.Error), "node reported phase=error")
					return ExitError{Code: 1, Err: &soloRolloutError{Node: name, Message: message}}
				default:
					reconcilingCount++
					details = append(details, fmt.Sprintf("%s=%s", name, firstNonEmpty(strings.TrimSpace(result.Status.Phase), "reconciling")))
				}
			}
		}

		latestSummary = fmt.Sprintf("rollout pending=%d reconciling=%d settled=%d", pendingCount, reconcilingCount, settledCount)
		if len(details) > 0 {
			latestSummary += " - " + strings.Join(details, ", ")
		}

		if pendingCount == 0 && reconcilingCount == 0 {
			return nil
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-rolloutCtx.Done():
			timer.Stop()
			if errors.Is(rolloutCtx.Err(), context.DeadlineExceeded) {
				return ExitError{Code: 1, Err: &soloRolloutTimeoutError{Summary: latestSummary, Nodes: nodeNames}}
			}
			return rolloutCtx.Err()
		case <-timer.C:
		}
	}
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

func (a *App) resolveNodes(current solo.State, names []string) (map[string]config.Node, error) {
	if len(names) == 0 {
		result := make(map[string]config.Node, len(current.Nodes))
		for name, node := range current.Nodes {
			result[name] = node
		}
		return result, nil
	}
	result := make(map[string]config.Node, len(names))
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

type soloRepublishErrorEntry struct {
	node      string
	operation string
	err       error
}

func (e soloRepublishErrorEntry) Error() string {
	return fmt.Sprintf("[%s] %s: %v", e.node, e.operation, e.err)
}

func (e soloRepublishErrorEntry) Unwrap() error {
	return e.err
}

type soloRepublishError struct {
	entries []soloRepublishErrorEntry
}

func (e *soloRepublishError) Error() string {
	parts := make([]string, 0, len(e.entries))
	for _, entry := range e.entries {
		parts = append(parts, entry.Error())
	}
	return fmt.Sprintf("republish errors:\n  %s", strings.Join(parts, "\n  "))
}

func (e *soloRepublishError) Unwrap() []error {
	wrapped := make([]error, 0, len(e.entries))
	for _, entry := range e.entries {
		wrapped = append(wrapped, entry)
	}
	return wrapped
}

func appendSoloRepublishError(mu *sync.Mutex, errs *[]soloRepublishErrorEntry, node, operation string, err error) {
	if err == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	*errs = append(*errs, soloRepublishErrorEntry{node: node, operation: operation, err: err})
}

func (a *App) republishNodes(ctx context.Context, current solo.State, nodeNames []string) (map[string]string, error) {
	if len(nodeNames) == 0 {
		return map[string]string{}, nil
	}
	type preparedNodeState struct {
		snapshots    []desiredstate.DeploySnapshot
		releaseNodes map[string]string
		peers        []desiredstate.NodePeer
		images       []string
	}
	type localImageCheck struct {
		once sync.Once
		err  error
	}
	var mu sync.Mutex
	var errs []soloRepublishErrorEntry
	revisions := map[string]string{}
	var wg sync.WaitGroup

	nodes, err := a.resolveNodes(current, nodeNames)
	if err != nil {
		return nil, err
	}
	sortedNames := sortedNodeNames(nodes)
	prepared := make(map[string]preparedNodeState, len(sortedNames))
	resolvedSnapshotCache := map[string]desiredstate.DeploySnapshot{}
	for _, nodeName := range sortedNames {
		inputs, err := a.preparedNodeDesiredStateInputs(ctx, current, nodeName, nodes[nodeName], resolvedSnapshotCache)
		if err != nil {
			return nil, err
		}
		prepared[nodeName] = inputs
	}
	var localImageChecksMu sync.Mutex
	localImageChecks := map[string]*localImageCheck{}
	for _, nodeName := range sortedNames {
		node := nodes[nodeName]
		inputs := prepared[nodeName]
		wg.Add(1)
		go func(name string, node config.Node, inputs preparedNodeState) {
			defer wg.Done()
			if len(inputs.images) > 0 {
				if _, err := solo.RunSSH(ctx, node, remoteDockerCheckCommand(), nil); err != nil {
					appendSoloRepublishError(&mu, &errs, name, "remote docker check", err)
					return
				}
			}
			for _, image := range inputs.images {
				present, err := remoteNodeHasImage(ctx, node, image)
				if err != nil {
					appendSoloRepublishError(&mu, &errs, name, "remote image check", err)
					return
				}
				if present {
					continue
				}
				localImageChecksMu.Lock()
				check, ok := localImageChecks[image]
				if !ok {
					check = &localImageCheck{}
					localImageChecks[image] = check
				}
				localImageChecksMu.Unlock()
				check.once.Do(func() {
					check.err = a.ensureLocalSoloSnapshotImage(ctx, image)
				})
				if check.err != nil {
					appendSoloRepublishError(&mu, &errs, name, "local image precheck", check.err)
					return
				}
				if err := transferImage(ctx, node, image, a.soloProgress("devopsellence deploy", map[string]any{"node": name, "image": image, "step": "image_transfer"})); err != nil {
					appendSoloRepublishError(&mu, &errs, name, "image transfer", err)
					return
				}
			}
			publication, err := desiredstate.PlanNodePublication(desiredstate.NodePublicationInput{
				NodeName:     name,
				CurrentNode:  node,
				Snapshots:    inputs.snapshots,
				ReleaseNodes: inputs.releaseNodes,
				NodePeers:    inputs.peers,
			})
			if err != nil {
				appendSoloRepublishError(&mu, &errs, name, "build desired state", err)
				return
			}
			desiredStateJSON := publication.DesiredStateJSON
			overridePath := desiredStateOverridePath(node)
			cmd := remoteDesiredStateOverrideCommand(overridePath)
			if _, err := solo.RunSSH(ctx, node, cmd, strings.NewReader(string(desiredStateJSON))); err != nil {
				appendSoloRepublishError(&mu, &errs, name, "write desired state", err)
				return
			}
			revision, err := desiredStateRevision(desiredStateJSON)
			if err != nil {
				appendSoloRepublishError(&mu, &errs, name, "parse desired state revision", err)
				return
			}
			mu.Lock()
			revisions[name] = revision
			mu.Unlock()
		}(nodeName, node, inputs)
	}
	wg.Wait()
	if len(errs) > 0 {
		return nil, &soloRepublishError{entries: errs}
	}
	return revisions, nil
}

func (a *App) resolveStoredDeploySnapshot(ctx context.Context, current solo.State, snapshot desiredstate.DeploySnapshot) (desiredstate.DeploySnapshot, error) {
	cfg, err := a.ConfigStore.Read(snapshot.WorkspaceRoot)
	if err != nil {
		return desiredstate.DeploySnapshot{}, err
	}
	if cfg == nil {
		return desiredstate.DeploySnapshot{}, fmt.Errorf("missing devopsellence.yml for %s", snapshot.WorkspaceRoot)
	}
	secrets, err := a.resolveSoloSecretValues(ctx, current, snapshot.WorkspaceRoot, snapshot.Environment, cfg)
	if err != nil {
		return desiredstate.DeploySnapshot{}, fmt.Errorf("load secrets for %s: %w", snapshot.WorkspaceRoot, err)
	}
	configPath := strings.TrimSpace(snapshot.Metadata.ConfigPath)
	if configPath == "" {
		configPath = a.ConfigStore.PathFor(snapshot.WorkspaceRoot)
	}
	return solo.BuildDeploySnapshotWithScopedSecrets(cfg, snapshot.WorkspaceRoot, configPath, snapshot.Image, snapshot.Revision, secrets)
}

func (a *App) resolveSoloSecretValues(ctx context.Context, current solo.State, workspaceRoot, environment string, cfg *config.ProjectConfig) (solo.ScopedSecrets, error) {
	if cfg == nil {
		return solo.ScopedSecrets{}, nil
	}
	records, err := current.SecretRecords(workspaceRoot, environment)
	if err != nil {
		return nil, err
	}
	local := map[string]solo.SecretRecord{}
	for _, record := range records {
		local[record.ServiceName+"\x00"+record.Name] = record
	}
	values := solo.ScopedSecrets{}
	cache := map[string]string{}
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		for _, ref := range service.SecretRefs {
			value, err := a.resolveSoloSecretRef(ctx, serviceName, ref, local, cache)
			if err != nil {
				return nil, err
			}
			values.Set(serviceName, ref.Name, value)
		}
	}
	return values, nil
}

func (a *App) resolveSoloSecretRef(ctx context.Context, serviceName string, ref config.SecretRef, local map[string]solo.SecretRecord, cache map[string]string) (string, error) {
	source := strings.TrimSpace(ref.Secret)
	if source == "" {
		return "", fmt.Errorf("missing secret reference for %s.%s", serviceName, ref.Name)
	}
	if strings.HasPrefix(strings.ToLower(source), "op://") {
		return a.resolveOnePasswordSecretCached(ctx, source, cache)
	}
	store, name, ok := parseDevopsellenceSecretRef(source)
	if !ok {
		return "", fmt.Errorf("unsupported solo secret reference %q for %s.%s", source, serviceName, ref.Name)
	}
	record, ok := local[serviceName+"\x00"+name]
	if !ok {
		return "", fmt.Errorf("missing local solo secret %s for service %s in %s store", name, serviceName, store)
	}
	recordStore, err := solo.NormalizeSecretStore(record.Store)
	if err != nil {
		return "", err
	}
	if recordStore != store {
		return "", fmt.Errorf("local solo secret %s for service %s uses %s store, config references %s", name, serviceName, recordStore, store)
	}
	if recordStore == solo.SecretStoreOnePassword && strings.TrimSpace(record.Reference) != "" {
		return a.resolveSoloSecretRecordCached(ctx, record, cache)
	}
	return a.resolveSoloSecretRecord(ctx, record)
}

func (a *App) resolveSoloSecretRecordCached(ctx context.Context, record solo.SecretRecord, cache map[string]string) (string, error) {
	reference := strings.TrimSpace(record.Reference)
	if reference == "" {
		return a.resolveSoloSecretRecord(ctx, record)
	}
	if value, ok := cache[reference]; ok {
		return value, nil
	}
	value, err := a.resolveSoloSecretRecord(ctx, record)
	if err != nil {
		return "", err
	}
	cache[reference] = value
	return value, nil
}

func (a *App) resolveOnePasswordSecretCached(ctx context.Context, reference string, cache map[string]string) (string, error) {
	if value, ok := cache[reference]; ok {
		return value, nil
	}
	value, err := a.resolveOnePasswordSecret(ctx, reference)
	if err != nil {
		return "", err
	}
	cache[reference] = value
	return value, nil
}

func parseDevopsellenceSecretRef(value string) (string, string, bool) {
	const prefix = "devopsellence://"
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), prefix) {
		return "", "", false
	}
	parts := strings.SplitN(value[len(prefix):], "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	store, err := solo.NormalizeSecretStore(parts[0])
	if err != nil {
		return "", "", false
	}
	name := strings.TrimSpace(parts[1])
	if name == "" {
		return "", "", false
	}
	return store, name, true
}

func (a *App) resolveSoloSecretRecord(ctx context.Context, record solo.SecretRecord) (string, error) {
	if a.soloSecretResolveFn != nil {
		return a.soloSecretResolveFn(ctx, record)
	}
	store, err := solo.NormalizeSecretStore(record.Store)
	if err != nil {
		return "", err
	}
	switch store {
	case solo.SecretStorePlaintext:
		return record.Value, nil
	case solo.SecretStoreOnePassword:
		return a.resolveOnePasswordSecret(ctx, record.Reference)
	default:
		return "", fmt.Errorf("unsupported secret store %q", store)
	}
}

func (a *App) resolveOnePasswordSecret(ctx context.Context, reference string) (string, error) {
	if strings.TrimSpace(reference) == "" {
		return "", errors.New("1Password secret reference is required")
	}
	lookPath := a.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	opPath, err := lookPath("op")
	if err != nil {
		return "", fmt.Errorf("1Password CLI `op` not found; install and sign in to 1Password CLI before deploying secrets from 1Password: %w", err)
	}
	cmd := exec.CommandContext(ctx, opPath, "read", reference)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return "", fmt.Errorf("read 1Password secret %s: %w: %s", reference, err, detail)
		}
		return "", fmt.Errorf("read 1Password secret %s: %w", reference, err)
	}
	value := strings.TrimRight(string(out), "\r\n")
	if value == "" {
		return "", fmt.Errorf("read 1Password secret %s: empty value", reference)
	}
	return value, nil
}

func (a *App) preparedNodeDesiredStateInputs(ctx context.Context, current solo.State, nodeName string, node config.Node, resolvedSnapshotCache map[string]desiredstate.DeploySnapshot) (struct {
	snapshots    []desiredstate.DeploySnapshot
	releaseNodes map[string]string
	peers        []desiredstate.NodePeer
	images       []string
}, error) {
	storedSnapshots, releaseNodes, peers, _, err := soloNodeDesiredStateInputs(current, nodeName)
	if err != nil {
		return struct {
			snapshots    []desiredstate.DeploySnapshot
			releaseNodes map[string]string
			peers        []desiredstate.NodePeer
			images       []string
		}{}, fmt.Errorf("build desired state inputs: %w", err)
	}
	resolvedSnapshots := make([]desiredstate.DeploySnapshot, 0, len(storedSnapshots))
	imageSet := map[string]bool{}
	for _, snapshot := range storedSnapshots {
		key := strings.TrimSpace(snapshot.WorkspaceKey) + "\n" + strings.TrimSpace(snapshot.Environment)
		resolvedSnapshot, ok := resolvedSnapshotCache[key]
		if !ok {
			resolvedSnapshot, err = a.resolveStoredDeploySnapshot(ctx, current, snapshot)
			if err != nil {
				return struct {
					snapshots    []desiredstate.DeploySnapshot
					releaseNodes map[string]string
					peers        []desiredstate.NodePeer
					images       []string
				}{}, fmt.Errorf("hydrate snapshot: %w", err)
			}
			resolvedSnapshotCache[key] = resolvedSnapshot
		}
		resolvedSnapshots = append(resolvedSnapshots, resolvedSnapshot)
		if snapshotNeedsImageOnNode(resolvedSnapshot, node, releaseNodes[key] == nodeName) {
			if image := strings.TrimSpace(resolvedSnapshot.Image); image != "" {
				imageSet[image] = true
			}
		}
	}
	images := make([]string, 0, len(imageSet))
	for image := range imageSet {
		images = append(images, image)
	}
	sort.Strings(images)
	return struct {
		snapshots    []desiredstate.DeploySnapshot
		releaseNodes map[string]string
		peers        []desiredstate.NodePeer
		images       []string
	}{
		snapshots:    resolvedSnapshots,
		releaseNodes: releaseNodes,
		peers:        peers,
		images:       images,
	}, nil
}

func snapshotNeedsImageOnNode(snapshot desiredstate.DeploySnapshot, node config.Node, runReleaseTask bool) bool {
	for _, service := range snapshot.Services {
		if soloNodeCanRunKind(node, service.Kind) {
			return true
		}
	}
	return snapshot.ReleaseTask != nil && runReleaseTask
}

func desiredStateRevision(data []byte) (string, error) {
	var payload struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Revision) == "" {
		return "", errors.New("revision is missing")
	}
	return payload.Revision, nil
}

func remoteNodeHasImage(ctx context.Context, node config.Node, imageTag string) (bool, error) {
	out, err := solo.RunSSH(ctx, node, remoteDockerImageInspectCommand(imageTag), nil)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "present", nil
}

func (a *App) soloProgress(operation string, fields map[string]any) func(string) {
	return func(message string) {
		message = strings.TrimSpace(message)
		if message == "" {
			return
		}
		payload := map[string]any{
			"operation": operation,
			"message":   message,
		}
		for key, value := range fields {
			payload[key] = value
		}
		_ = a.Printer.PrintEvent("progress", payload)
	}
}

func soloNodeLogNextSteps(nodes map[string]config.Node) []string {
	steps := []string{}
	for _, nodeName := range sortedNodeNames(nodes) {
		steps = append(steps, "devopsellence node logs "+shellQuote(nodeName)+" --lines 100")
	}
	return steps
}

func soloDoctorNextSteps(nodes map[string]config.Node) []string {
	steps := []string{}
	for _, nodeName := range sortedNodeNames(nodes) {
		node := nodes[nodeName]
		steps = append(steps,
			"devopsellence agent install "+shellQuote(nodeName),
			"devopsellence node diagnose "+shellQuote(nodeName),
			fmt.Sprintf("ssh -p %d %s true", node.Port, shellQuote(node.User+"@"+node.Host)),
		)
	}
	return steps
}

func (a *App) ensureLocalSoloSnapshotImage(ctx context.Context, imageTag string) error {
	if strings.TrimSpace(imageTag) == "" {
		return nil
	}
	if a.Docker == nil {
		return errors.New("docker client is not configured")
	}
	if _, err := a.Docker.ImageMetadata(ctx, imageTag); err != nil {
		return fmt.Errorf("local snapshot image %q is unavailable; rebuild or redeploy the attached workspace before republishing solo node state: %w", imageTag, err)
	}
	return nil
}

func soloNodeDesiredStateInputs(current solo.State, nodeName string) ([]desiredstate.DeploySnapshot, map[string]string, []desiredstate.NodePeer, []string, error) {
	snapshots := []desiredstate.DeploySnapshot{}
	releaseNodes := map[string]string{}
	peerMap := map[string]desiredstate.NodePeer{}
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
			peerMap[peerName] = desiredstate.NodePeer{
				Name:          peerName,
				Labels:        append([]string(nil), peerNode.Labels...),
				PublicAddress: peerNode.Host,
			}
		}
	}

	peers := make([]desiredstate.NodePeer, 0, len(peerMap))
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

func releaseNodeForSnapshot(snapshot desiredstate.DeploySnapshot, attachment solo.AttachmentRecord, nodes map[string]config.Node) (string, error) {
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

func sortedSoloPeerNames(peers map[string]desiredstate.NodePeer) []string {
	names := make([]string, 0, len(peers))
	for name := range peers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (a *App) SoloStatus(ctx context.Context, opts SoloStatusOptions) error {
	nodes, cfg, err := a.soloStatusSelection(opts)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes attached to the current environment")
	}

	var jsonResults []map[string]any
	readErrors := 0
	allSettled := true

	for name, node := range nodes {
		result, err := readNodeStatus(ctx, node)
		if err != nil {
			readErrors++
			allSettled = false

			jsonResults = append(jsonResults, map[string]any{
				"node":  name,
				"error": err.Error(),
			})

			continue
		}

		if result.Missing {
			allSettled = false
			message := "no deploy status yet; run `devopsellence deploy`"

			jsonResults = append(jsonResults, map[string]any{
				"node":    name,
				"status":  nil,
				"message": message,
			})

			continue
		}

		if strings.TrimSpace(result.Status.Phase) != "settled" {
			allSettled = false
		}
		jsonResults = append(jsonResults, map[string]any{
			"node":   name,
			"status": result.Raw,
		})

	}

	payload := map[string]any{"nodes": jsonResults}
	if urls := soloStatusPublicURLs(cfg, nodes); len(urls) > 0 {
		if allSettled {
			payload["public_urls"] = urls
		} else {
			payload["configured_public_urls"] = urls
			payload["warnings"] = []string{"public URLs are configured, but one or more nodes are not settled; check node status before testing reachability"}
		}
	}
	if err := a.Printer.PrintJSON(payload); err != nil {
		return err
	}
	if readErrors > 0 {
		return ExitError{Code: 1, Err: RenderedError{Err: fmt.Errorf("status failed for %d node(s)", readErrors)}}
	}
	return nil
}

func soloStatusPublicURLs(cfg *config.ProjectConfig, nodes map[string]config.Node) []string {
	if cfg == nil || len(nodes) == 0 {
		return nil
	}
	scheme := "http"
	if cfg.Ingress != nil {
		tlsMode := strings.TrimSpace(cfg.Ingress.TLS.Mode)
		if strings.EqualFold(tlsMode, "auto") || strings.EqualFold(tlsMode, "manual") {
			scheme = "https"
		}
	}
	hosts := []string{}
	if cfg.Ingress != nil {
		for _, host := range normalizeIngressHosts(cfg.Ingress.Hosts) {
			if host == "*" {
				continue
			}
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		for _, name := range sortedNodeNames(nodes) {
			node := nodes[name]
			if !soloNodeCanRunIngress(node, cfg) {
				continue
			}
			host := strings.TrimSpace(node.Host)
			if host != "" {
				hosts = append(hosts, host)
			}
		}
	}
	urls := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil && strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		url := scheme + "://" + host + "/"
		if seen[url] {
			continue
		}
		seen[url] = true
		urls = append(urls, url)
	}
	sort.Strings(urls)
	return urls
}

func (a *App) SoloSecretsSet(_ context.Context, opts SoloSecretsSetOptions) error {
	store, err := soloSecretStore(opts)
	if err != nil {
		return err
	}
	if opts.ValueStdin && store != solo.SecretStorePlaintext {
		return ExitError{Code: 2, Err: errors.New("--stdin is only supported for plaintext solo secrets")}
	}
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
	material, err := soloSecretMaterial(store, opts)
	if err != nil {
		return err
	}
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	environmentName := soloEnvironmentName(cfg, opts.Environment)
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --service")}
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return ExitError{Code: 2, Err: fmt.Errorf("service %q not found in devopsellence.yml", serviceName)}
	}
	if serviceSecretRefConflict(service, opts.Key) {
		return ExitError{Code: 2, Err: fmt.Errorf("service %q already defines %s in env; remove it before adding a secret_ref with the same name", serviceName, opts.Key)}
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	record, err := current.SetSecret(workspaceRoot, environmentName, serviceName, opts.Key, material)
	if err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	configUpdated, err := ensureServiceSecretRef(cfg, serviceName, soloSecretConfigRef(record))
	if err != nil {
		return fmt.Errorf("secret saved locally but update devopsellence.yml failed: %w", err)
	}
	if configUpdated {
		if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
			return fmt.Errorf("secret saved locally but update devopsellence.yml failed: %w", err)
		}
	}

	payload := map[string]any{
		"key":            record.Name,
		"service_name":   record.ServiceName,
		"environment":    record.Environment,
		"store":          record.Store,
		"secret_ref":     soloSecretConfigRef(record).Secret,
		"config_updated": configUpdated,
		"config_path":    a.ConfigStore.PathFor(workspaceRoot),
		"action":         "saved",
	}
	if strings.TrimSpace(record.Reference) != "" {
		payload["reference"] = record.Reference
	}
	if a.SoloState != nil && strings.TrimSpace(a.SoloState.Path) != "" {
		payload["state_path"] = a.SoloState.Path
	}
	return a.Printer.PrintJSON(payload)

}

func soloSecretStore(opts SoloSecretsSetOptions) (string, error) {
	store := opts.Store
	if strings.TrimSpace(store) == "" && strings.TrimSpace(opts.Reference) != "" {
		store = solo.SecretStoreOnePassword
	}
	normalized, err := solo.NormalizeSecretStore(store)
	if err != nil {
		return "", ExitError{Code: 2, Err: err}
	}
	return normalized, nil
}

func soloSecretMaterial(store string, opts SoloSecretsSetOptions) (solo.SecretMaterial, error) {
	value := strings.TrimSpace(opts.Value)
	reference := strings.TrimSpace(opts.Reference)
	switch store {
	case solo.SecretStorePlaintext:
		if reference != "" {
			return solo.SecretMaterial{}, ExitError{Code: 2, Err: errors.New("--op-ref requires --store 1password")}
		}
		if value == "" {
			return solo.SecretMaterial{}, ExitError{Code: 2, Err: errors.New("secret value is required")}
		}
		return solo.SecretMaterial{Store: store, Value: opts.Value}, nil
	case solo.SecretStoreOnePassword:
		if value != "" {
			return solo.SecretMaterial{}, ExitError{Code: 2, Err: errors.New("1Password solo secrets use --op-ref instead of --value")}
		}
		if reference == "" {
			return solo.SecretMaterial{}, ExitError{Code: 2, Err: errors.New("missing required option: --op-ref")}
		}
		if !strings.HasPrefix(strings.ToLower(reference), "op://") {
			return solo.SecretMaterial{}, ExitError{Code: 2, Err: errors.New("1Password secret reference must start with op://")}
		}
		return solo.SecretMaterial{Store: store, Reference: reference}, nil
	default:
		return solo.SecretMaterial{}, ExitError{Code: 2, Err: fmt.Errorf("unsupported secret store %q", store)}
	}
}

func soloSecretConfigRef(record solo.SecretRecord) config.SecretRef {
	secret := "devopsellence://" + record.Store + "/" + record.Name
	if strings.TrimSpace(record.Reference) != "" {
		secret = strings.TrimSpace(record.Reference)
	}
	return config.SecretRef{Name: record.Name, Secret: secret}
}

func serviceSecretRefConflict(service config.ServiceConfig, name string) bool {
	name = strings.TrimSpace(name)
	if _, ok := service.Env[name]; !ok {
		return false
	}
	for _, existing := range service.SecretRefs {
		if existing.Name == name {
			return false
		}
	}
	return true
}

func ensureServiceSecretRef(cfg *config.ProjectConfig, serviceName string, ref config.SecretRef) (bool, error) {
	serviceName = strings.TrimSpace(serviceName)
	if cfg == nil {
		return false, nil
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return false, nil
	}
	for i, existing := range service.SecretRefs {
		if existing.Name == ref.Name {
			if existing.Secret == ref.Secret {
				return false, nil
			}
			service.SecretRefs[i] = ref
			cfg.Services[serviceName] = service
			return true, nil
		}
	}
	if serviceSecretRefConflict(service, ref.Name) {
		return false, fmt.Errorf("service %q already defines %s in env; remove it before adding a secret_ref with the same name", serviceName, ref.Name)
	}
	service.SecretRefs = append(service.SecretRefs, ref)
	cfg.Services[serviceName] = service
	return true, nil
}

func removeServiceSecretRef(cfg *config.ProjectConfig, serviceName, name string) bool {
	serviceName = strings.TrimSpace(serviceName)
	if cfg == nil {
		return false
	}
	service, ok := cfg.Services[serviceName]
	if !ok {
		return false
	}
	filtered := make([]config.SecretRef, 0, len(service.SecretRefs))
	changed := false
	for _, existing := range service.SecretRefs {
		if existing.Name == name {
			changed = true
			continue
		}
		filtered = append(filtered, existing)
	}
	if !changed {
		return false
	}
	service.SecretRefs = filtered
	cfg.Services[serviceName] = service
	return true
}

func (a *App) SoloSecretsList(_ context.Context, opts SoloSecretsListOptions) error {
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	environmentName := soloEnvironmentName(cfg, opts.Environment)
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	secrets, err := current.ListSecrets(workspaceRoot, environmentName, opts.ServiceName)
	if err != nil {
		return err
	}
	items := soloSecretListItems(cfg, secrets, opts.ServiceName)

	return a.Printer.PrintJSON(map[string]any{"schema_version": outputSchemaVersion, "environment": environmentName, "secrets": items})

}

func soloSecretListItems(cfg *config.ProjectConfig, secrets []solo.SecretRecord, serviceFilter string) []listedSecret {
	items := map[string]listedSecret{}
	filter := strings.TrimSpace(serviceFilter)
	if cfg != nil {
		for _, serviceName := range cfg.ServiceNames() {
			if filter != "" && serviceName != filter {
				continue
			}
			for _, ref := range cfg.Services[serviceName].SecretRefs {
				key := secretListKey(serviceName, ref.Name)
				secretRef := strings.TrimSpace(ref.Secret)
				items[key] = listedSecret{
					ServiceName:        serviceName,
					Name:               ref.Name,
					SecretRef:          secretRef,
					Store:              secretListStore(secretRef, "configured"),
					Configured:         true,
					AvailableToService: true,
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
				Store:       secret.Store,
			}
		}
		if strings.TrimSpace(item.SecretRef) == "" {
			item.SecretRef = soloSecretConfigRef(secret).Secret
		}
		item.Reference = strings.TrimSpace(secret.Reference)
		item.Store = firstNonEmpty(item.Store, secret.Store)
		item.Stored = true
		items[key] = item
	}
	return sortListedSecrets(items)
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
		Node                    config.Node             `json:"node"`
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

	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"nodes":          current.Nodes,
		"node_items":     items,
	})

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
	attachment, changed, err := a.attachNode(&current, workspaceRoot, environmentName, opts.Node)
	if err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if _, ok := current.Snapshots[attachment.WorkspaceKey+"\n"+attachment.Environment]; ok {
		if _, err := a.republishNodes(ctx, current, attachment.NodeNames); err != nil {
			return err
		}
	}

	return a.Printer.PrintJSON(map[string]any{
		"node":        opts.Node,
		"environment": environmentName,
		"changed":     changed,
	})

}

func (a *App) runSoloNodeAttach(ctx context.Context, opts SoloNodeAttachOptions) error {
	if a.soloNodeAttachFn != nil {
		return a.soloNodeAttachFn(ctx, opts)
	}
	return a.SoloNodeAttach(ctx, opts)
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
	remainingNodeNames := make([]string, 0, len(nodeNamesBefore))
	for _, name := range nodeNamesBefore {
		if name != opts.Node {
			remainingNodeNames = append(remainingNodeNames, name)
		}
	}
	remainingNodeNames = normalizeSoloNames(remainingNodeNames)
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if _, err := a.republishNodes(ctx, current, remainingNodeNames); err != nil {
		return err
	}
	warnings := []string{}
	if _, err := a.republishNodes(ctx, current, []string{opts.Node}); err != nil {
		if current.NodeHasAttachments(opts.Node) || !soloRepublishMissingAgentError(err) {
			return err
		}
		warnings = append(warnings, "remote desired state was not cleared because the agent is already uninstalled on the node")
	}

	payload := map[string]any{
		"node":        opts.Node,
		"environment": environmentName,
		"changed":     true,
	}
	if len(warnings) > 0 {
		payload["warnings"] = warnings
	}
	return a.Printer.PrintJSON(payload)

}

func soloRepublishMissingAgentError(err error) bool {
	if err == nil {
		return false
	}
	var sshErr *solo.SSHError
	if !errors.As(err, &sshErr) {
		return false
	}
	exitCode, ok := sshErr.ExitCode()
	return ok && exitCode == 127 && strings.Contains(sshErr.Stderr, soloRemoteAgentBinaryNotFoundMessage)
}

func (a *App) SoloSecretsDelete(_ context.Context, opts SoloSecretsDeleteOptions) error {
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	environmentName := soloEnvironmentName(cfg, opts.Environment)
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --service")}
	}
	if _, ok := cfg.Services[serviceName]; !ok {
		return ExitError{Code: 2, Err: fmt.Errorf("service %q not found in devopsellence.yml", serviceName)}
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	record, err := current.DeleteSecret(workspaceRoot, environmentName, serviceName, opts.Key)
	if err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	configUpdated := removeServiceSecretRef(cfg, serviceName, opts.Key)
	if configUpdated {
		if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
			return fmt.Errorf("secret deleted locally but update devopsellence.yml failed: %w", err)
		}
	}

	return a.Printer.PrintJSON(map[string]any{"key": record.Name, "service_name": record.ServiceName, "environment": record.Environment, "config_updated": configUpdated, "config_path": a.ConfigStore.PathFor(workspaceRoot), "action": "deleted"})

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

	linesLimit := opts.Lines
	if linesLimit < 1 || linesLimit > soloLogsMaxLines {
		return ExitError{Code: 2, Err: fmt.Errorf("--lines must be between 1 and %d", soloLogsMaxLines)}
	}
	journalArgs := fmt.Sprintf("-u devopsellence-agent --no-pager -n %d", linesLimit)

	out, err := solo.RunSSH(ctx, node, remoteJournalctlCommand(journalArgs), nil)
	if err != nil {
		return err
	}
	lines := splitNonFinalEmptyLines(out)
	return a.Printer.PrintJSON(map[string]any{"node": opts.Node, "lines": lines, "limit": linesLimit})
}

func (a *App) SoloNodeDiagnose(ctx context.Context, opts SoloNodeDiagnoseOptions) error {
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	node, ok := current.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found", opts.Node)
	}

	payload := map[string]any{
		"schema_version": outputSchemaVersion,
		"node":           opts.Node,
		"host":           node.Host,
		"user":           node.User,
		"port":           node.Port,
	}
	checks := a.soloDiagnoseChecks(ctx, node)
	payload["checks"] = checks
	diagnoseOK := soloChecksOK(checks)
	payload["ok"] = diagnoseOK
	if soloCheckFailed(checks, "ssh") {
		payload["next_steps"] = []string{fmt.Sprintf("ssh -p %d %s true", node.Port, shellQuote(node.User+"@"+node.Host))}
		return a.printSoloDiagnoseResult(payload, diagnoseOK)
	}
	agent := map[string]any{
		"active": collectRemoteText(ctx, node, "systemctl is-active devopsellence-agent"),
		"status": collectRemoteLines(ctx, node, remoteSystemctlStatusCommand("devopsellence-agent", 40)),
	}
	payload["agent"] = agent
	dockerSnapshot := map[string]any{
		"containers": collectRemoteJSONLines(ctx, node, remoteDockerPSJSONCommand(), soloDiagnoseDockerItemLimit),
		"images":     collectRemoteJSONLines(ctx, node, remoteDockerImagesJSONCommand(), soloDiagnoseDockerItemLimit),
		"networks":   collectRemoteJSONLines(ctx, node, remoteDockerNetworksJSONCommand(), soloDiagnoseDockerItemLimit),
	}
	payload["docker"] = dockerSnapshot
	ports := collectRemoteLimitedLines(ctx, node, remoteListeningPortsCommand(), soloDiagnosePortsLineLimit)
	payload["ports"] = ports
	if !diagnosticSectionsOK(agent, dockerSnapshot, ports) {
		diagnoseOK = false
		payload["ok"] = false
	}
	statusResult, statusErr := readNodeStatus(ctx, node)
	if statusErr != nil {
		payload["status_error"] = statusErr.Error()
		diagnoseOK = false
		payload["ok"] = false
	} else if statusResult.Missing {
		payload["status_missing"] = true
	} else {
		var statusPayload any
		if err := json.Unmarshal(statusResult.Raw, &statusPayload); err == nil {
			payload["status"] = statusPayload
		} else {
			payload["status"] = string(statusResult.Raw)
		}
	}
	payload["next_steps"] = []string{
		"devopsellence status",
		"devopsellence node logs " + shellQuote(opts.Node) + " --lines 100",
	}
	return a.printSoloDiagnoseResult(payload, diagnoseOK)
}

func diagnosticSectionsOK(sections ...map[string]any) bool {
	for _, section := range sections {
		if !diagnosticSectionOK(section) {
			return false
		}
	}
	return true
}

func diagnosticSectionOK(section map[string]any) bool {
	if section["ok"] == false {
		return false
	}
	for _, value := range section {
		child, ok := value.(map[string]any)
		if ok && !diagnosticSectionOK(child) {
			return false
		}
	}
	return true
}

func (a *App) printSoloDiagnoseResult(payload map[string]any, ok bool) error {
	if err := a.Printer.PrintJSON(payload); err != nil {
		return err
	}
	if !ok {
		nodeName, _ := payload["node"].(string)
		if strings.TrimSpace(nodeName) == "" {
			nodeName = "node"
		}
		return ExitError{Code: 1, Err: RenderedError{Err: fmt.Errorf("solo node diagnose failed for %s", nodeName)}}
	}
	return nil
}

func (a *App) soloDiagnoseChecks(ctx context.Context, node config.Node) []map[string]any {
	checks := []struct {
		name string
		cmd  string
	}{
		{name: "ssh", cmd: "true"},
		{name: "docker", cmd: remoteDockerCheckCommand()},
		{name: "agent", cmd: "systemctl is-active --quiet devopsellence-agent"},
	}
	results := make([]map[string]any, 0, len(checks))
	for _, check := range checks {
		out, err := solo.RunSSH(ctx, node, check.cmd, nil)
		result := map[string]any{"check": check.name, "ok": err == nil}
		if err != nil {
			result["detail"] = err.Error()
		} else if detail := strings.TrimSpace(out); detail != "" {
			result["detail"] = detail
		}
		results = append(results, result)
	}
	return results
}

func soloChecksOK(checks []map[string]any) bool {
	for _, check := range checks {
		if check["ok"] == false {
			return false
		}
	}
	return true
}

func soloCheckFailed(checks []map[string]any, name string) bool {
	for _, check := range checks {
		if check["check"] == name && check["ok"] == false {
			return true
		}
	}
	return false
}

func splitNonFinalEmptyLines(out string) []string {
	lines := strings.Split(out, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

type remoteDiagnosticResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

func runRemoteDiagnostic(ctx context.Context, node config.Node, command string) remoteDiagnosticResult {
	wrapped := fmt.Sprintf(`stdout_file="$(mktemp)" || exit 1
stderr_file="$(mktemp)" || { rm -f "$stdout_file"; exit 1; }
(
%s
) >"$stdout_file" 2>"$stderr_file"
rc=$?
printf '__DEVOPSELLENCE_EXIT_CODE__%%s\n' "$rc"
printf '__DEVOPSELLENCE_STDOUT__\n'
cat "$stdout_file"
printf '\n__DEVOPSELLENCE_STDERR__\n'
cat "$stderr_file"
rm -f "$stdout_file" "$stderr_file"
exit 0`, command)
	out, err := solo.RunSSH(ctx, node, wrapped, nil)
	if err != nil {
		return remoteDiagnosticResult{Err: err}
	}
	return parseRemoteDiagnostic(out)
}

func parseRemoteDiagnostic(out string) remoteDiagnosticResult {
	const exitPrefix = "__DEVOPSELLENCE_EXIT_CODE__"
	const stdoutMarker = "\n__DEVOPSELLENCE_STDOUT__\n"
	const stderrMarker = "\n__DEVOPSELLENCE_STDERR__\n"
	lineEnd := strings.IndexByte(out, '\n')
	if lineEnd < 0 || !strings.HasPrefix(out[:lineEnd], exitPrefix) {
		return remoteDiagnosticResult{ExitCode: 1, Stdout: out, Err: fmt.Errorf("remote diagnostic wrapper returned unexpected output")}
	}
	exitCode, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(out[:lineEnd], exitPrefix)))
	if err != nil {
		return remoteDiagnosticResult{ExitCode: 1, Stdout: out, Err: fmt.Errorf("parse remote diagnostic exit code: %w", err)}
	}
	rest := out[lineEnd:]
	if !strings.HasPrefix(rest, stdoutMarker) {
		return remoteDiagnosticResult{ExitCode: exitCode, Stdout: rest, Err: fmt.Errorf("remote diagnostic wrapper missing stdout marker")}
	}
	rest = strings.TrimPrefix(rest, stdoutMarker)
	stderrIndex := strings.LastIndex(rest, stderrMarker)
	if stderrIndex < 0 {
		return remoteDiagnosticResult{ExitCode: exitCode, Stdout: rest, Err: fmt.Errorf("remote diagnostic wrapper missing stderr marker")}
	}
	return remoteDiagnosticResult{
		ExitCode: exitCode,
		Stdout:   strings.TrimSuffix(rest[:stderrIndex], "\n"),
		Stderr:   rest[stderrIndex+len(stderrMarker):],
	}
}

func collectRemoteText(ctx context.Context, node config.Node, command string) map[string]any {
	diag := runRemoteDiagnostic(ctx, node, command)
	result := map[string]any{"ok": diag.Err == nil && diag.ExitCode == 0}
	if diag.Err != nil {
		result["error"] = diag.Err.Error()
	} else if diag.ExitCode != 0 {
		result["exit_code"] = diag.ExitCode
		result["error"] = strings.TrimSpace(diag.Stderr)
	}
	if value := strings.TrimSpace(diag.Stdout); value != "" {
		result["value"] = value
	}
	if stderr := strings.TrimSpace(diag.Stderr); stderr != "" && diag.ExitCode == 0 {
		result["stderr"] = stderr
	}
	return result
}

func collectRemoteLines(ctx context.Context, node config.Node, command string) map[string]any {
	diag := runRemoteDiagnostic(ctx, node, command)
	result := map[string]any{"ok": diag.Err == nil && diag.ExitCode == 0}
	if diag.Err != nil {
		result["error"] = diag.Err.Error()
	} else if diag.ExitCode != 0 {
		result["exit_code"] = diag.ExitCode
		result["error"] = strings.TrimSpace(diag.Stderr)
	}
	result["lines"] = splitNonFinalEmptyLines(diag.Stdout)
	if stderr := strings.TrimSpace(diag.Stderr); stderr != "" && diag.ExitCode == 0 {
		result["stderr"] = stderr
	}
	return result
}

func collectRemoteLimitedLines(ctx context.Context, node config.Node, command string, limit int) map[string]any {
	result := collectRemoteLines(ctx, node, command)
	result["limit"] = limit
	result["truncated"] = false
	lines, _ := result["lines"].([]string)
	filtered := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == soloDiagnoseTruncatedMarker {
			result["truncated"] = true
			continue
		}
		filtered = append(filtered, line)
	}
	result["lines"] = filtered
	return result
}

func collectRemoteJSONLines(ctx context.Context, node config.Node, command string, limit int) map[string]any {
	diag := runRemoteDiagnostic(ctx, node, command)
	result := map[string]any{"ok": diag.Err == nil && diag.ExitCode == 0, "limit": limit, "truncated": false}
	if diag.Err != nil {
		result["error"] = diag.Err.Error()
		return result
	}
	if diag.ExitCode != 0 {
		result["exit_code"] = diag.ExitCode
		result["error"] = strings.TrimSpace(diag.Stderr)
	}
	items := []any{}
	for _, line := range splitNonFinalEmptyLines(diag.Stdout) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == soloDiagnoseTruncatedMarker {
			result["truncated"] = true
			continue
		}
		var item map[string]any
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			items = append(items, line)
		} else {
			items = append(items, item)
		}
	}
	result["items"] = items
	return result
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
	if _, err := a.republishNodes(ctx, current, soloAffectedNodesForNode(current, opts.Node)); err != nil {
		return err
	}

	return a.Printer.PrintJSON(map[string]any{
		"node":   opts.Node,
		"labels": labels,
	})

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

	if err := a.installSoloAgent(ctx, opts.Node, node, opts); err != nil {
		return err
	}

	return a.Printer.PrintJSON(map[string]any{"node": opts.Node, "action": "installed"})

}

func (a *App) SoloAgentUninstall(ctx context.Context, opts SoloAgentUninstallOptions) error {
	if !opts.Yes {
		return ExitError{Code: 2, Err: errors.New("agent uninstall requires --yes; rerun with --yes to confirm remote cleanup")}
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	node, ok := current.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found", opts.Node)
	}
	stateDir, err := safeSoloAgentStateDir(firstNonEmpty(node.AgentStateDir, "/var/lib/devopsellence"))
	if err != nil {
		return ExitError{Code: 2, Err: err}
	}
	stdout := newTailBuffer(sshOutputTailLimit)
	stderr := newTailBuffer(sshOutputTailLimit)
	script := soloAgentUninstallScript(soloAgentUninstallScriptOptions{
		StateDir:      stateDir,
		KeepWorkloads: opts.KeepWorkloads,
	})
	if err := solo.RunSSHInteractiveWithStdin(ctx, node, "bash -s", strings.NewReader(script), stdout, stderr); err != nil {
		return sshInteractiveError("failed to run uninstall script over SSH", err, stdout.String(), stderr.String())
	}
	return a.Printer.PrintJSON(map[string]any{
		"node":              opts.Node,
		"action":            "uninstalled",
		"workloads_removed": !opts.KeepWorkloads,
		"state_removed":     !opts.KeepWorkloads,
	})

}

func (a *App) SoloRuntimeDoctor(ctx context.Context, opts SoloDoctorOptions) error {
	results, failed, err := a.soloRuntimeDoctorChecks(ctx, opts)
	if err != nil {
		return err
	}

	if err := a.Printer.PrintJSON(map[string]any{"checks": results}); err != nil {
		return err
	}
	if failed {
		return ExitError{Code: 1, Err: RenderedError{Err: fmt.Errorf("solo doctor failed")}}
	}
	return nil
}

func (a *App) soloRuntimeDoctorChecks(ctx context.Context, opts SoloDoctorOptions) ([]map[string]any, bool, error) {
	current, err := a.readSoloState()
	if err != nil {
		return nil, false, err
	}
	nodes, err := a.resolveNodes(current, opts.Nodes)
	if err != nil {
		return nil, false, err
	}
	results := make([]map[string]any, 0, len(nodes)*3)
	failed := false
	for _, name := range sortedNodeNames(nodes) {
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
			out, err := solo.RunSSH(ctx, node, check.cmd, nil)
			ok := err == nil
			result := map[string]any{"node": name, "check": check.name, "ok": ok}
			if ok {
				if detail := strings.TrimSpace(out); detail != "" {
					result["detail"] = detail
				}
			} else {
				failed = true
				result["detail"] = strings.TrimSpace(err.Error())
			}
			results = append(results, result)
		}
	}
	return results, failed, nil
}

func (a *App) runSoloRuntimeDoctor(ctx context.Context, opts SoloDoctorOptions) error {
	if a.soloRuntimeDoctorFn != nil {
		return a.soloRuntimeDoctorFn(ctx, opts)
	}
	return a.SoloRuntimeDoctor(ctx, opts)
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
			return "", errors.New("No config found. Run `devopsellence init --mode solo`.")
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
			return "", errors.New("No solo nodes registered yet. Run `devopsellence node create`.")
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

	payload := map[string]any{
		"schema_version": outputSchemaVersion,
		"ok":             ok,
		"checks":         checks,
	}
	if ok && len(current.Nodes) > 0 {
		runtimeChecks, runtimeFailed, err := a.soloRuntimeDoctorChecks(ctx, SoloDoctorOptions{})
		if err != nil {
			return err
		}
		payload["runtime_checks"] = runtimeChecks
		payload["ok"] = !runtimeFailed
		if runtimeFailed {
			payload["next_steps"] = soloDoctorNextSteps(current.Nodes)
		}
		if err := a.Printer.PrintJSON(payload); err != nil {
			return err
		}
		if runtimeFailed {
			return ExitError{Code: 1, Err: RenderedError{Err: fmt.Errorf("solo doctor failed")}}
		}
		return nil
	}

	if err := a.Printer.PrintJSON(payload); err != nil {
		return err
	}
	if !ok {
		return ExitError{Code: 1, Err: RenderedError{Err: fmt.Errorf("solo doctor failed")}}
	}
	return nil

}

func (a *App) SoloNodeCreate(ctx context.Context, opts SoloNodeCreateOptions) error {
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	nodeName := strings.TrimSpace(opts.Name)
	if nodeName == "" {
		return fmt.Errorf("node name is required")
	}
	opts.Name = nodeName
	if _, ok := current.Nodes[nodeName]; ok {
		return fmt.Errorf("solo node %q already exists", nodeName)
	}
	hasProvider := strings.TrimSpace(opts.Provider) != ""
	hasHost := strings.TrimSpace(opts.Host) != ""
	if hasProvider == hasHost {
		if hasProvider {
			return ExitError{Code: 2, Err: fmt.Errorf("node create accepts either --provider or --host, not both")}
		}
		return ExitError{Code: 2, Err: fmt.Errorf("node create requires --provider or --host")}
	}

	var node config.Node
	var labels []string
	result := map[string]any{
		"schema_version": outputSchemaVersion,
		"node":           nodeName,
		"config_path":    a.ConfigStore.PathFor(workspaceRoot),
	}
	if hasHost {
		node, labels, err = existingSSHNodeFromCreateOptions(opts)
		if err != nil {
			return err
		}
		result["source"] = "existing_ssh"
	} else {
		if err := a.ensureSoloNodeCreateSSHPublicKey(&opts, workspaceRoot); err != nil {
			return err
		}
		created, createErr := a.createProviderNode(ctx, opts, cfg.Project)
		if createErr != nil {
			return createErr
		}
		node = created.Node
		labels = created.Labels
		result["source"] = "provider"
		result["provider"] = created.ProviderSlug
		result["provider_server_id"] = created.Server.ID
	}
	if err := current.SetNode(nodeName, node); err != nil {
		return err
	}
	attached := false
	var attachment solo.AttachmentRecord
	if opts.Attach {
		environmentName := soloEnvironmentName(cfg, "")
		var attachErr error
		attachment, _, attachErr = a.attachNode(&current, workspaceRoot, environmentName, nodeName)
		if attachErr != nil {
			return attachErr
		}
		attached = true
		result["environment"] = environmentName
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	installed := false
	if opts.Install {
		if err := waitForSoloSSH(ctx, node, 3*time.Minute); err != nil {
			return err
		}
		if err := a.installSoloAgent(ctx, nodeName, node, SoloAgentInstallOptions{}); err != nil {
			return err
		}
		installed = true
	}

	if attached {
		if _, ok := current.Snapshots[attachment.WorkspaceKey+"\n"+attachment.Environment]; ok {
			if _, err := a.republishNodes(ctx, current, attachment.NodeNames); err != nil {
				return err
			}
		}
	}

	result["host"] = node.Host
	result["labels"] = labels
	result["agent_installed"] = installed
	result["attached"] = attached
	return a.Printer.PrintJSON(result)

}

func (a *App) runSoloNodeCreate(ctx context.Context, opts SoloNodeCreateOptions) error {
	if a.soloNodeCreateFn != nil {
		return a.soloNodeCreateFn(ctx, opts)
	}
	return a.SoloNodeCreate(ctx, opts)
}

func (a *App) ensureSoloNodeCreateSSHPublicKey(opts *SoloNodeCreateOptions, workspaceRoot string) error {
	if strings.TrimSpace(opts.SSHPublicKey) != "" {
		return nil
	}
	if _, _, err := readSoloSSHPublicKey(""); err == nil {
		return nil
	}
	generatedKey, err := ensureGeneratedWorkspaceSSHKey(workspaceRoot)
	if err != nil {
		return err
	}
	opts.SSHPublicKey = generatedKey.PublicKeyPath

	return nil
}

func (a *App) SharedSoloNodeCreate(ctx context.Context, opts SharedSoloNodeCreateOptions) error {
	if opts.Name == "" {
		return fmt.Errorf("node name is required")
	}
	if strings.TrimSpace(opts.Host) != "" {
		return ExitError{Code: 2, Err: fmt.Errorf("node create --host is only available in solo mode")}
	}
	if strings.TrimSpace(opts.SSHKey) != "" {
		return ExitError{Code: 2, Err: fmt.Errorf("node create --ssh-key is only available in solo mode")}
	}
	if opts.Install {
		return ExitError{Code: 2, Err: fmt.Errorf("node create --install is only available in solo mode")}
	}
	if opts.Attach {
		return ExitError{Code: 2, Err: fmt.Errorf("node create --attach is only available in solo mode")}
	}
	if strings.TrimSpace(opts.Provider) == "" {
		return ExitError{Code: 2, Err: fmt.Errorf("node create requires --provider in shared mode")}
	}
	tokens, err := a.ensureAuth(ctx, false)
	if err != nil {
		return err
	}
	var bootstrap nodeBootstrapToken
	run := func(ctx context.Context, update, _ func(string)) error {
		var err error
		bootstrap, err = a.createNodeBootstrapToken(ctx, &tokens, opts.NodeBootstrapOptions, update)
		return err
	}
	if err := run(ctx, func(string) {}, func(string) {}); err != nil {
		return err
	}

	projectName := opts.Project
	if !opts.Unassigned && bootstrap.Workspace.Project.Name != "" {
		projectName = bootstrap.Workspace.Project.Name
	}
	if err := a.ensureSoloNodeCreateSSHPublicKey(&opts.SoloNodeCreateOptions, bootstrap.Workspace.Discovery.WorkspaceRoot); err != nil {
		return err
	}
	created, err := a.createProviderNode(ctx, opts.SoloNodeCreateOptions, projectName)
	if err != nil {
		return err
	}

	installCommand := strings.TrimSpace(stringFromMap(bootstrap.Result, "install_command"))
	if installCommand == "" {
		return fmt.Errorf("node bootstrap response did not include install_command")
	}

	if err := waitForSoloSSH(ctx, created.Node, 3*time.Minute); err != nil {
		return err
	}

	sshStdout := newTailBuffer(sshOutputTailLimit)
	sshStderr := newTailBuffer(sshOutputTailLimit)
	if err := solo.RunSSHInteractive(ctx, created.Node, installCommand, sshStdout, sshStderr); err != nil {
		return sshInteractiveError("failed to run install command over SSH", err, sshStdout.String(), sshStderr.String())
	}

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

const sshOutputTailLimit = 64 * 1024

const (
	soloLogsDefaultLines                 = 100
	soloLogsMaxLines                     = 1000
	soloDiagnoseDockerItemLimit          = 100
	soloDiagnosePortsLineLimit           = 200
	soloDiagnoseTruncatedMarker          = "__DEVOPSELLENCE_TRUNCATED__"
	soloRemoteAgentBinaryNotFoundMessage = "devopsellence agent binary not found"
)

type tailBuffer struct {
	limit     int
	buf       []byte
	truncated bool
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.limit <= 0 {
		if n > 0 {
			b.truncated = true
		}
		return n, nil
	}
	if len(p) >= b.limit {
		if len(p) > b.limit || len(b.buf) > 0 {
			b.truncated = true
		}
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		return n, nil
	}
	if overflow := len(b.buf) + len(p) - b.limit; overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:len(b.buf)-overflow]
		b.truncated = true
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

func (b *tailBuffer) String() string {
	if !b.truncated {
		return string(b.buf)
	}
	return "[truncated]\n" + string(b.buf)
}

func sshInteractiveError(prefix string, err error, stdout string, stderr string) error {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)

	switch {
	case stderr != "" && stdout != "":
		return fmt.Errorf("%s: %w; stderr: %s; stdout: %s", prefix, err, stderr, stdout)
	case stderr != "":
		return fmt.Errorf("%s: %w; stderr: %s", prefix, err, stderr)
	case stdout != "":
		return fmt.Errorf("%s: %w; stdout: %s", prefix, err, stdout)
	default:
		return fmt.Errorf("%s: %w", prefix, err)
	}
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
	provider := strings.TrimSpace(node.Provider)
	providerServerID := strings.TrimSpace(node.ProviderServerID)
	if provider == "" && providerServerID == "" {
		knownHostsRemoved, knownHostsErr := solo.RemoveKnownHosts(node)
		current.RemoveNode(opts.Name)
		if err := a.writeSoloState(current); err != nil {
			return err
		}

		payload := map[string]any{
			"node":                opts.Name,
			"action":              "forgotten",
			"known_hosts_removed": knownHostsRemoved,
			"note":                "manual SSH node forgotten locally; this command did not contact the VM",
			"remote_cleanup": map[string]any{
				"performed": false,
				"if_needed": fmt.Sprintf("devopsellence agent uninstall %s --yes", shellQuote(opts.Name)),
			},
		}
		if knownHostsErr != nil {
			payload["known_hosts_error"] = knownHostsErr.Error()
			payload["warnings"] = []string{"manual SSH node forgotten locally, but SSH known_hosts cleanup failed"}
		}
		return a.Printer.PrintJSON(payload)

	}
	if provider == "" || providerServerID == "" {
		return fmt.Errorf("node %q has incomplete provider metadata; refusing provider delete", opts.Name)
	}
	resolvedProvider, err := a.resolveSoloProvider(provider)
	if err != nil {
		return err
	}
	if err := resolvedProvider.DeleteServer(ctx, providerServerID); err != nil {
		return err
	}
	knownHostsRemoved, knownHostsErr := solo.RemoveKnownHosts(node)
	current.RemoveNode(opts.Name)
	if err := a.writeSoloState(current); err != nil {
		return err
	}

	payload := map[string]any{"node": opts.Name, "action": "deleted", "known_hosts_removed": knownHostsRemoved}
	if knownHostsErr != nil {
		payload["known_hosts_error"] = knownHostsErr.Error()
		payload["warnings"] = []string{"provider node deleted and local state removed, but SSH known_hosts cleanup failed"}
	}
	return a.Printer.PrintJSON(payload)

}

func (a *App) SoloInit(context.Context, SoloInitOptions) error {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return err
	}
	cfg, err := a.ConfigStore.Read(discovered.WorkspaceRoot)
	if err != nil {
		return err
	}
	created := false
	if cfg == nil {
		cfg = soloDefaultProjectConfig(discovered)
		written, writeErr := a.ConfigStore.Write(discovered.WorkspaceRoot, *cfg)
		if writeErr != nil {
			return writeErr
		}
		cfg = &written
		created = true
	}
	configPath := a.ConfigStore.PathFor(discovered.WorkspaceRoot)
	environmentName := soloEnvironmentName(cfg, "")
	ready := false
	if a.SoloState != nil {
		current, stateErr := a.readSoloState()
		if stateErr != nil {
			return stateErr
		}
		attached, attachErr := current.AttachedNodeNames(discovered.WorkspaceRoot, environmentName)
		if attachErr != nil {
			return attachErr
		}
		ready = len(attached) > 0
	}
	missing := []string{}
	if !ready {
		missing = append(missing, "node")
	}
	nextSteps := []string{
		"git init # if this app is not already a git checkout",
		"git add . # include devopsellence.yml and app files",
		"git commit -m 'initial deploy' # devopsellence deploys the current commit",
		"devopsellence node create prod-1 --host <host> --user root --ssh-key <path>",
		"devopsellence agent install prod-1",
		"devopsellence node attach prod-1",
		"devopsellence doctor",
		"devopsellence deploy",
	}
	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"mode":           string(ModeSolo),
		"workspace_root": discovered.WorkspaceRoot,
		"project_slug":   discovered.ProjectSlug,
		"app_type":       discovered.AppType,
		"fallback_used":  discovered.FallbackUsed,
		"config": map[string]any{
			"path":           configPath,
			"created":        created,
			"valid":          true,
			"schema_version": cfg.SchemaVersion,
		},
		"environment": environmentName,
		"ready":       ready,
		"missing":     missing,
		"next_steps":  nextSteps,
	})
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
	if serviceName == "" && cfg.Ingress != nil && len(cfg.Ingress.Rules) > 0 {
		serviceName = strings.TrimSpace(cfg.Ingress.Rules[0].Target.Service)
	}
	if serviceName == "" {
		var ok bool
		serviceName, ok = cfg.PrimaryWebServiceName()
		if !ok {
			return fmt.Errorf("ingress set requires --service when the primary web service cannot be inferred")
		}
	}
	rules := make([]config.IngressRuleConfig, 0, len(hosts))
	for _, host := range hosts {
		rules = append(rules, config.IngressRuleConfig{
			Match:  config.IngressMatchConfig{Host: host, PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: serviceName, Port: "http"},
		})
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
		Hosts: hosts,
		Rules: rules,
		TLS: config.IngressTLSConfig{
			Mode:           tlsMode,
			Email:          strings.TrimSpace(opts.TLSEmail),
			CADirectoryURL: strings.TrimSpace(opts.TLSCADirectoryURL),
		},
		RedirectHTTP: configBoolPtr(redirectHTTP),
	}
	written, err := a.ConfigStore.Write(discovered.WorkspaceRoot, *cfg)
	if err != nil {
		return err
	}

	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"ingress":        written.Ingress,
		"config_path":    a.ConfigStore.PathFor(discovered.WorkspaceRoot),
	})

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
	if len(nodeNames) == 0 {
		return fmt.Errorf("no nodes are attached to the current environment")
	}
	nodes, err := a.resolveNodes(current, nodeNames)
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

			if err := a.Printer.PrintJSON(report); err != nil {
				return err
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

func (a *App) installSoloAgent(ctx context.Context, nodeName string, node config.Node, opts SoloAgentInstallOptions) error {
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
		return nil, "", fmt.Errorf("no devopsellence.yml found; run `devopsellence init --mode solo`")
	}
	return cfg, workspaceRoot, nil
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

func (a *App) soloStatusNodes(opts SoloStatusOptions) (map[string]config.Node, error) {
	nodes, _, err := a.soloStatusSelection(opts)
	return nodes, err
}

func (a *App) soloStatusSelection(opts SoloStatusOptions) (map[string]config.Node, *config.ProjectConfig, error) {
	current, err := a.readSoloState()
	if err != nil {
		return nil, nil, err
	}
	if len(opts.Nodes) > 0 {
		nodes, err := a.resolveNodes(current, opts.Nodes)
		if err != nil {
			return nil, nil, err
		}
		_, cfg, cfgErr := a.optionalWorkspaceConfig()
		if cfgErr != nil {
			cfg = nil
		}
		return nodes, cfg, nil
	}
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return nil, nil, err
	}
	nodeNames, err := current.AttachedNodeNames(workspaceRoot, soloEnvironmentName(cfg, ""))
	if err != nil {
		return nil, nil, err
	}
	if len(nodeNames) == 0 {
		return map[string]config.Node{}, cfg, nil
	}
	nodes, err := a.resolveNodes(current, nodeNames)
	return nodes, cfg, err
}

func (a *App) attachNode(current *solo.State, workspaceRoot, environmentName, nodeName string) (solo.AttachmentRecord, bool, error) {
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
	cfg = applyBootstrapIngress(cfg, nil)
	return &cfg
}

func configBoolPtr(value bool) *bool {
	return &value
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

const soloStatusMissingSentinel = "__DEVOPSELLENCE_STATUS_MISSING__"

func (a *App) checkIngressBeforeDeploy(ctx context.Context, cfg *config.ProjectConfig, nodes map[string]config.Node, skip bool) error {
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

	return fmt.Errorf("ingress DNS is not ready; update DNS or pass --skip-dns-check")
}

func ingressDNSReport(ctx context.Context, cfg *config.ProjectConfig, selected map[string]config.Node) (ingressDNSReportResult, error) {
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

func webNodeIPs(cfg *config.ProjectConfig, selected map[string]config.Node) []string {
	if cfg == nil {
		return nil
	}
	seen := map[string]bool{}
	ips := []string{}
	for _, name := range sortedNodeNames(selected) {
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

func normalizeIngressHostsKeepOrder(values []string) []string {
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

func installSoloAgent(ctx context.Context, node config.Node, opts SoloAgentInstallOptions, reporter soloInstallReporter) error {
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

func runSoloAgentInstallScript(ctx context.Context, node config.Node, script string, reporter soloInstallReporter) error {
	err := solo.RunSSHInteractiveWithStdin(ctx, node, "bash -s", strings.NewReader(script), reporter.Stdout(), reporter.Stderr())
	if err != nil {
		return sshInteractiveError("failed to run install script over SSH", err, reporter.CapturedStdout(), reporter.CapturedStderr())
	}
	return nil
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

type soloAgentUninstallScriptOptions struct {
	StateDir      string
	KeepWorkloads bool
}

func safeSoloAgentStateDir(value string) (string, error) {
	stateDir := path.Clean(strings.TrimSpace(value))
	if stateDir == "." || stateDir == "/" || !path.IsAbs(stateDir) {
		return "", fmt.Errorf("unsafe devopsellence agent state dir %q", value)
	}
	if !strings.Contains(stateDir, "devopsellence") {
		return "", fmt.Errorf("unsafe devopsellence agent state dir %q: path must contain devopsellence", value)
	}
	return stateDir, nil
}

func soloAgentUninstallScript(opts soloAgentUninstallScriptOptions) string {
	stateDir, err := safeSoloAgentStateDir(firstNonEmpty(opts.StateDir, "/var/lib/devopsellence"))
	if err != nil {
		// Keep this function side-effect-free for tests/callers; the runtime script
		// also refuses unsafe values before any rm -rf operation.
		stateDir = "/var/lib/devopsellence"
	}
	keepWorkloads := "0"
	if opts.KeepWorkloads {
		keepWorkloads = "1"
	}
	return fmt.Sprintf(`set -euo pipefail

STATE_DIR=%s
KEEP_WORKLOADS=%s
AGENT_BIN=/usr/local/bin/devopsellence-agent
SERVICE_FILE=/etc/systemd/system/devopsellence-agent.service

case "$STATE_DIR" in
  ""|"/"|"/."|"/..") echo "refusing unsafe devopsellence state dir: $STATE_DIR" >&2; exit 1 ;;
esac
case "$STATE_DIR" in
  *devopsellence*) ;;
  *) echo "refusing unsafe devopsellence state dir without devopsellence in path: $STATE_DIR" >&2; exit 1 ;;
esac

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

echo "progress: stopping devopsellence-agent service"
run_root systemctl stop devopsellence-agent || true
run_root systemctl disable devopsellence-agent || true
run_root rm -f "$SERVICE_FILE"
run_root systemctl daemon-reload || true

if [ "$KEEP_WORKLOADS" != "1" ] && command -v docker >/dev/null 2>&1; then
  echo "progress: removing devopsellence-managed containers"
  container_ids="$(run_root docker ps -aq --filter label=devopsellence.managed=true || true)"
  if [ -n "$container_ids" ]; then
    # shellcheck disable=SC2086
    run_root docker rm -f $container_ids
  fi
  system_container_ids="$(run_root docker ps -aq --filter label=devopsellence.system || true)"
  if [ -n "$system_container_ids" ]; then
    # shellcheck disable=SC2086
    run_root docker rm -f $system_container_ids
  fi
  run_root docker rm -f devopsellence-envoy || true
  run_root docker network rm devopsellence || true
fi

if [ "$KEEP_WORKLOADS" != "1" ]; then
  echo "progress: removing devopsellence agent state"
  run_root rm -rf "$STATE_DIR"
fi

run_root rm -f "$AGENT_BIN"
`, shellQuote(stateDir), shellQuote(keepWorkloads))
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

func waitForSoloSSH(ctx context.Context, node config.Node, timeout time.Duration) error {
	return waitForSoloSSHWithProbe(ctx, node, timeout, 10*time.Second, 2*time.Second, func(ctx context.Context) error {
		_, err := solo.RunSSH(ctx, node, "true", nil)
		return err
	})
}

func waitForSoloSSHWithProbe(ctx context.Context, node config.Node, timeout, probeTimeout, retryInterval time.Duration, probe func(context.Context) error) error {
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
func transferImage(ctx context.Context, node config.Node, imageTag string, progress func(string)) error {
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

func remoteDockerImageInspectCommand(imageTag string) string {
	quotedImage := shellQuote(imageTag)
	return fmt.Sprintf("if docker image inspect %s >/dev/null 2>&1; then echo present; elif command -v sudo >/dev/null 2>&1 && sudo -n docker image inspect %s >/dev/null 2>&1; then echo present; else echo missing; fi", quotedImage, quotedImage)
}

func remoteReadFileCommand(path string) string {
	quotedPath := shellQuote(path)
	return fmt.Sprintf("if [ -r %[1]s ]; then exec cat %[1]s; fi; if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then exec sudo -n cat %[1]s; fi; exec cat %[1]s", quotedPath)
}

func remoteReadOptionalFileCommand(path, missingSentinel string) string {
	quotedPath := shellQuote(path)
	quotedSentinel := shellQuote(missingSentinel)
	return fmt.Sprintf("if [ -r %[1]s ]; then exec cat %[1]s; fi; if command -v sudo >/dev/null 2>&1 && sudo -n test -r %[1]s >/dev/null 2>&1; then exec sudo -n cat %[1]s; fi; if [ -e %[1]s ]; then echo 'File exists but is not readable; grant read access or enable passwordless sudo.' >&2; exit 1; fi; if command -v sudo >/dev/null 2>&1 && sudo -n test -e %[1]s >/dev/null 2>&1; then echo 'File exists but is not readable; grant read access or enable passwordless sudo.' >&2; exit 1; fi; printf '%%s\\n' %[2]s", quotedPath, quotedSentinel)
}

func remoteJournalctlCommand(args string) string {
	return fmt.Sprintf("if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then exec sudo -n journalctl %s; fi; exec journalctl %s", args, args)
}

func remoteSystemctlStatusCommand(unit string, lines int) string {
	args := fmt.Sprintf("status --no-pager -l -n %d %s", lines, shellQuote(unit))
	return fmt.Sprintf("if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then sudo -n systemctl %s; else systemctl %s; fi", args, args)
}

func remoteDockerPSJSONCommand() string {
	return withRemoteLineLimit("if docker info >/dev/null 2>&1; then docker ps -a --format '{{json .}}'; elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then sudo -n docker ps -a --format '{{json .}}'; else echo 'Docker is not reachable' >&2; exit 1; fi", soloDiagnoseDockerItemLimit)
}

func remoteDockerImagesJSONCommand() string {
	return withRemoteLineLimit("if docker info >/dev/null 2>&1; then docker images --format '{{json .}}'; elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then sudo -n docker images --format '{{json .}}'; else echo 'Docker is not reachable' >&2; exit 1; fi", soloDiagnoseDockerItemLimit)
}

func remoteDockerNetworksJSONCommand() string {
	return withRemoteLineLimit("if docker info >/dev/null 2>&1; then docker network ls --format '{{json .}}'; elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then sudo -n docker network ls --format '{{json .}}'; else echo 'Docker is not reachable' >&2; exit 1; fi", soloDiagnoseDockerItemLimit)
}

func withRemoteLineLimit(command string, limit int) string {
	pipeline := fmt.Sprintf("( %s ) | awk -v marker=%s 'NR <= %d { print } NR == %d { print marker; exit }'", command, shellQuote(soloDiagnoseTruncatedMarker), limit, limit+1)
	script := pipeline + `; status=$?; if [ "$status" -eq 0 ] || [ "$status" -eq 141 ]; then exit 0; fi; exit "$status"`
	return "if command -v bash >/dev/null 2>&1; then exec bash -o pipefail -c " + shellQuote(script) + "; fi; echo 'bash is required for bounded diagnostic output' >&2; exit 1"
}

func remoteListeningPortsCommand() string {
	return withRemoteLineLimit("if command -v ss >/dev/null 2>&1; then ss -ltnp || ss -ltn; elif command -v netstat >/dev/null 2>&1; then netstat -ltnp || netstat -ltn; else echo 'no ss or netstat available'; fi", soloDiagnosePortsLineLimit)
}

func desiredStateOverridePath(node config.Node) string {
	return path.Join(firstNonEmpty(node.AgentStateDir, "/var/lib/devopsellence"), "desired-state-override.json")
}

func remoteDesiredStateOverrideCommand(overridePath string) string {
	quotedPath := shellQuote(overridePath)
	return fmt.Sprintf("agent_bin=$(command -v devopsellence-agent || command -v devopsellence || true); if [ -z \"$agent_bin\" ]; then echo %[2]s >&2; exit 127; fi; override_dir=$(dirname -- %[1]s); if [ \"$(id -u)\" = 0 ] || [ -w \"$override_dir\" ]; then exec \"$agent_bin\" desired-state set-override --file - --override-path %[1]s; fi; if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then exec sudo -n \"$agent_bin\" desired-state set-override --file - --override-path %[1]s; fi; echo 'Cannot write desired state override. Make the SSH user able to write the agent state directory or enable passwordless sudo.' >&2; exit 1", quotedPath, shellQuote(soloRemoteAgentBinaryNotFoundMessage))
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

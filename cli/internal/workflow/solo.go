package workflow

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/devopsellence/cli/internal/output"
	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/cli/internal/solo/providers"
	cliversion "github.com/devopsellence/cli/internal/version"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
	corerelease "github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/release"
	"github.com/oklog/ulid/v2"
)

type SoloDeployOptions struct {
	SkipDNSCheck bool
	DryRun       bool
}

type SoloReleaseListOptions struct {
	Limit int
}

type SoloReleaseRollbackOptions struct {
	Selector string
	DryRun   bool
}

type SoloStatusOptions struct {
	Nodes []string
}

var (
	soloULIDMu      sync.Mutex
	soloULIDEntropy = ulid.Monotonic(rand.Reader, 0)
)

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

type SoloNodeListOptions struct {
	All bool
}

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

type SoloWorkloadLogsOptions struct {
	ServiceName string
	Nodes       []string
	Lines       int
}

type SoloExecOptions struct {
	ServiceName string
	Environment string
	Nodes       []string
	Command     []string
}

type SoloIngressCertInstallOptions struct {
	CertFile string
	KeyFile  string
	Nodes    []string
}

type SoloNodeExecOptions struct {
	Node    string
	Command []string
}

type SoloNodeDiagnoseOptions struct {
	Node string
}

type SoloNodeLabelSetOptions struct {
	Node   string
	Labels string
}

type SoloNodeLabelListOptions struct {
	Node string
}

type SoloNodeLabelRemoveOptions struct {
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

type SoloSupportBundleOptions struct {
	Output string
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
	Time         string                      `json:"time,omitempty"`
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
	Name      string `json:"name"`
	State     string `json:"state"`
	Container string `json:"container"`
}

type soloNodeStatusResult struct {
	Missing bool
	Raw     json.RawMessage
	Status  soloNodeStatus
}

func (a *App) createProviderNode(ctx context.Context, opts SoloNodeCreateOptions, projectName string, progress func(string)) (providerNodeCreateResult, error) {
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
	providerImage := strings.TrimSpace(opts.Image)
	if providerSlug == providerHetzner && providerImage == "" {
		providerImage = providers.DefaultHetznerImage
	}
	if err := a.ensureProviderTokenConfigured(ctx, providerSlug); err != nil {
		return providerNodeCreateResult{}, err
	}
	provider, err := a.resolveSoloProvider(providerSlug)
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	if progress != nil {
		progress(fmt.Sprintf("Creating %s server %q in %s (%s)...", providerSlug, opts.Name, opts.Region, opts.Size))
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
		Image:        providerImage,
		SSHPublicKey: sshPublicKey,
		Labels:       providerLabels,
	})
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	if progress != nil {
		progress(fmt.Sprintf("Waiting for %s server %s to become ready...", providerSlug, server.ID))
	}
	server, err = waitForSoloProviderServer(ctx, provider, server, progress)
	if err != nil {
		return providerNodeCreateResult{}, err
	}
	if server.PublicIP == "" {
		return providerNodeCreateResult{}, fmt.Errorf("created server %s but provider did not return a public IPv4 address", server.ID)
	}
	if progress != nil {
		progress(fmt.Sprintf("Server %s ready at %s.", server.ID, server.PublicIP))
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
		ProviderImage:    providerImage,
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
	stream := a.Printer.Stream("devopsellence deploy")
	if err := stream.Event("started", map[string]any{}); err != nil {
		return err
	}
	cfg, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig("")
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
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

	if opts.DryRun {
		payload := map[string]any{
			"action":            "deploy",
			"dry_run":           true,
			"workload_revision": shortSHA,
			"image":             imageTag,
			"environment":       environmentName,
			"nodes":             sortedNodeNames(nodes),
			"phase":             "planned",
			"side_effects": map[string]bool{
				"build":       false,
				"ssh":         false,
				"publish":     false,
				"state_write": false,
			},
			"next_steps": []string{"devopsellence deploy"},
		}
		if urls := soloStatusPublicURLs(cfg, nodes); len(urls) > 0 {
			payload["configured_public_urls"] = urls
			if len(soloReadyPublicURLs(cfg, nodes)) == 0 {
				payload["public_url_status"] = soloPublicURLStatus(cfg)
				payload["warnings"] = []string{soloPublicURLWarning(cfg)}
			}
		}
		return stream.Result(payload)
	}

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
	redactedSnapshot := solo.RedactDeploySnapshotSecrets(snapshot, cfg)
	environmentID, err := solo.EnvironmentStateKey(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	releaseID := soloReleaseID(shortSHA, now)
	release, err := corerelease.NewRelease(corerelease.ReleaseCreateInput{
		ID:            releaseID,
		EnvironmentID: environmentID,
		Revision:      shortSHA,
		Snapshot:      redactedSnapshot,
		Image:         corerelease.ImageRef{Reference: imageTag},
		TargetNodeIDs: attachedNodeNames,
		CreatedAt:     now,
	})
	if err != nil {
		return err
	}
	deployment, err := corerelease.NewDeployment(corerelease.DeploymentCreateInput{
		ID:            soloDeploymentID(corerelease.DeploymentKindDeploy, shortSHA, now),
		EnvironmentID: release.EnvironmentID,
		ReleaseID:     release.ID,
		Kind:          corerelease.DeploymentKindDeploy,
		Sequence:      nextSoloDeploymentSequence(current, release.EnvironmentID),
		TargetNodeIDs: attachedNodeNames,
		CreatedAt:     now,
	})
	if err != nil {
		return err
	}
	deployment.Status = corerelease.DeploymentStatusRunning
	deployment.StatusMessage = "publishing desired state"

	publishState := cloneSoloState(current)
	if _, err := publishState.SaveRelease(release); err != nil {
		return err
	}
	statusBaselines := a.soloNodeStatusTimes(ctx, nodes)
	desiredStateRevisions, err := a.republishNodes(ctx, publishState, attachedNodeNames)
	if err != nil {
		return err
	}
	if _, err := current.SaveRelease(release); err != nil {
		return err
	}
	if err := current.SaveDeployment(deployment); err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if err := a.waitForSoloRollout(ctx, nodes, desiredStateRevisions, statusBaselines); err != nil {
		resultErr := err
		var rolloutErr *soloRolloutError
		if errors.As(err, &rolloutErr) {
			rolloutErr.Healthchecks = soloDeployHealthcheckDetails(cfg)
			resultErr = ExitError{Code: 1, Err: rolloutErr}
		}
		var timeoutErr *soloRolloutTimeoutError
		if errors.As(err, &timeoutErr) {
			timeoutErr.Healthchecks = soloDeployHealthcheckDetails(cfg)
			resultErr = ExitError{Code: 1, Err: timeoutErr}
		}
		if persistErr := a.persistSoloDeploymentRolloutFailure(current, deployment, desiredStateRevisions, resultErr); persistErr != nil {
			return errors.Join(resultErr, fmt.Errorf("persist deployment failure: %w", persistErr))
		}
		return resultErr
	}
	deployment.Status = corerelease.DeploymentStatusSettled
	deployment.StatusMessage = "release settled"
	deployment.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	deployment.PublicationResult = soloDeploymentPublicationResult(desiredStateRevisions, nil)
	if err := current.SaveDeployment(deployment); err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}

	payload := map[string]any{
		"release_id":              release.ID,
		"deployment_id":           deployment.ID,
		"workload_revision":       shortSHA,
		"desired_state_revisions": desiredStateRevisions,
		"image":                   imageTag,
		"environment":             environmentName,
		"nodes":                   sortedNodeNames(nodes),
		"phase":                   "settled",
	}
	if urls := soloReadyPublicURLs(cfg, nodes); len(urls) > 0 {
		payload["public_urls"] = urls
		payload["next_steps"] = append([]string{"devopsellence status", "curl " + urls[0]}, soloNodeLogNextSteps(nodes)...)
	} else if urls := soloStatusPublicURLs(cfg, nodes); len(urls) > 0 {
		payload["configured_public_urls"] = urls
		payload["public_url_status"] = soloPublicURLStatus(cfg)
		payload["warnings"] = []string{soloPublicURLWarning(cfg)}
		payload["next_steps"] = append([]string{"devopsellence status"}, soloNodeLogNextSteps(nodes)...)
	} else {
		payload["next_steps"] = append([]string{"devopsellence status"}, soloNodeLogNextSteps(nodes)...)
	}
	return stream.Result(payload)

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
			"devopsellence logs --node " + shellQuote(e.Node) + " --lines 100",
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
		steps = append(steps, "devopsellence logs --node "+shellQuote(node)+" --lines 100")
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

func (a *App) waitForSoloRollout(ctx context.Context, nodes map[string]config.Node, expectedRevisions map[string]string, previousStatusTimes ...map[string]string) error {
	timeout := a.DeployTimeout
	if timeout <= 0 {
		timeout = defaultDeployProgressTimeout
	}
	pollInterval := a.DeployPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultDeployProgressPollInterval
	}
	statusBaselines := map[string]string{}
	if len(previousStatusTimes) > 0 && previousStatusTimes[0] != nil {
		statusBaselines = previousStatusTimes[0]
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
			case !soloNodeStatusAdvancedAfter(result.Status, statusBaselines[name]):
				pendingCount++
				details = append(details, fmt.Sprintf("%s=status_time:%s", name, firstNonEmpty(strings.TrimSpace(result.Status.Time), "none")))
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

func (a *App) soloNodeStatusTimes(ctx context.Context, nodes map[string]config.Node) map[string]string {
	baselines := map[string]string{}
	for _, name := range sortedNodeNames(nodes) {
		result, err := readNodeStatus(ctx, nodes[name])
		if err != nil || result.Missing {
			continue
		}
		if observed := strings.TrimSpace(result.Status.Time); observed != "" {
			baselines[name] = observed
		}
	}
	return baselines
}

func soloNodeStatusAdvancedAfter(status soloNodeStatus, previousStatusTime string) bool {
	previousStatusTime = strings.TrimSpace(previousStatusTime)
	if previousStatusTime == "" {
		return true
	}
	observed := strings.TrimSpace(status.Time)
	if observed == "" {
		return true
	}
	parsedObserved, err := time.Parse(time.RFC3339Nano, observed)
	if err != nil {
		return true
	}
	parsedPrevious, err := time.Parse(time.RFC3339Nano, previousStatusTime)
	if err != nil {
		return true
	}
	return parsedObserved.After(parsedPrevious)
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

func (a *App) currentSoloAttachment(current solo.State) (*config.ProjectConfig, string, string, []string, bool, error) {
	discovered, cfg, err := a.optionalWorkspaceConfig()
	if err != nil {
		return nil, "", "", nil, false, err
	}
	if cfg == nil {
		return nil, discovered.WorkspaceRoot, "", nil, false, nil
	}
	environmentName := a.effectiveEnvironment("", cfg)
	nodeNames, err := current.AttachedNodeNames(discovered.WorkspaceRoot, environmentName)
	if err != nil {
		return nil, "", "", nil, true, err
	}
	return cfg, discovered.WorkspaceRoot, environmentName, nodeNames, true, nil
}

func soloNodeListNode(node config.Node) map[string]any {
	item := map[string]any{
		"host": node.Host,
		"user": node.User,
	}
	if node.Port != 0 {
		item["port"] = node.Port
	}
	if len(node.Labels) > 0 {
		item["labels"] = node.Labels
	}
	if strings.TrimSpace(node.Provider) != "" {
		item["provider"] = node.Provider
	}
	if strings.TrimSpace(node.ProviderRegion) != "" {
		item["provider_region"] = node.ProviderRegion
	}
	if strings.TrimSpace(node.ProviderSize) != "" {
		item["provider_size"] = node.ProviderSize
	}
	if strings.TrimSpace(node.ProviderImage) != "" {
		item["provider_image"] = node.ProviderImage
	}
	if strings.TrimSpace(node.SSHKey) != "" {
		item["ssh_key_configured"] = true
	}
	if strings.TrimSpace(node.ProviderServerID) != "" {
		item["provider_server_id_configured"] = true
	}
	return item
}

func soloNodeListAttachments(attachments []solo.AttachmentRecord, currentWorkspaceKey, currentEnvironment string) []map[string]any {
	items := make([]map[string]any, 0, len(attachments))
	for _, attachment := range attachments {
		item := map[string]any{
			"environment": attachment.Environment,
			"node_names":  attachment.NodeNames,
		}
		if attachment.WorkspaceKey == currentWorkspaceKey && attachment.Environment == currentEnvironment {
			item["current_environment"] = true
		}
		items = append(items, item)
	}
	return items
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
			publication, err := corerelease.PlanPublication(corerelease.PublicationPlanInput{
				NodeName:     name,
				Node:         node,
				Releases:     publicationReleasesFromSnapshots(inputs.snapshots),
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
			mu.Lock()
			revisions[name] = publication.Revision
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
	snapshot = cloneDeploySnapshot(snapshot)
	records, err := current.SecretRecords(snapshot.WorkspaceRoot, snapshot.Environment)
	if err != nil {
		return desiredstate.DeploySnapshot{}, err
	}
	local := map[string]solo.SecretRecord{}
	for _, record := range records {
		local[record.ServiceName+"\x00"+record.Name] = record
	}
	if len(local) > 0 && len(snapshot.SecretRefs) == 0 {
		return desiredstate.DeploySnapshot{}, fmt.Errorf("stored release snapshot for %s (%s) was created before secret metadata was tracked; run `devopsellence deploy` to create a fresh release before rollback or republish", snapshot.WorkspaceRoot, snapshot.Environment)
	}
	cache := map[string]string{}
	for i := range snapshot.Services {
		serviceName := strings.TrimSpace(snapshot.Services[i].Name)
		for _, secretName := range snapshot.SecretRefs[serviceName] {
			secretName = strings.TrimSpace(secretName)
			if secretName == "" {
				continue
			}
			record, ok := local[serviceName+"\x00"+secretName]
			if !ok {
				return desiredstate.DeploySnapshot{}, fmt.Errorf("missing local solo secret %s for service %s", secretName, serviceName)
			}
			value, err := a.resolveSoloSecretRecordCached(ctx, record, cache)
			if err != nil {
				return desiredstate.DeploySnapshot{}, fmt.Errorf("resolve secret %s for service %s: %w", secretName, serviceName, err)
			}
			if snapshot.Services[i].Env == nil {
				snapshot.Services[i].Env = map[string]string{}
			}
			snapshot.Services[i].Env[secretName] = value
		}
	}
	if snapshot.ReleaseTask != nil {
		serviceName := strings.TrimSpace(snapshot.ReleaseService)
		for _, secretName := range snapshot.SecretRefs[serviceName] {
			secretName = strings.TrimSpace(secretName)
			if secretName == "" {
				continue
			}
			record, ok := local[serviceName+"\x00"+secretName]
			if !ok {
				return desiredstate.DeploySnapshot{}, fmt.Errorf("missing local solo secret %s for release task service %s", secretName, serviceName)
			}
			value, err := a.resolveSoloSecretRecordCached(ctx, record, cache)
			if err != nil {
				return desiredstate.DeploySnapshot{}, fmt.Errorf("resolve secret %s for release task service %s: %w", secretName, serviceName, err)
			}
			if snapshot.ReleaseTask.Env == nil {
				snapshot.ReleaseTask.Env = map[string]string{}
			}
			snapshot.ReleaseTask.Env[secretName] = value
		}
	}
	return snapshot, nil
}

func cloneDeploySnapshot(snapshot desiredstate.DeploySnapshot) desiredstate.DeploySnapshot {
	cloned := snapshot
	if snapshot.Services != nil {
		cloned.Services = append([]desiredstate.ServiceJSON(nil), snapshot.Services...)
		for i := range cloned.Services {
			cloned.Services[i].Entrypoint = append([]string(nil), snapshot.Services[i].Entrypoint...)
			cloned.Services[i].Command = append([]string(nil), snapshot.Services[i].Command...)
			cloned.Services[i].Env = cloneStringMap(snapshot.Services[i].Env)
			cloned.Services[i].Ports = append([]desiredstate.ServicePortJSON(nil), snapshot.Services[i].Ports...)
			cloned.Services[i].VolumeMounts = append([]desiredstate.VolumeMountJSON(nil), snapshot.Services[i].VolumeMounts...)
			if snapshot.Services[i].Healthcheck != nil {
				healthcheck := *snapshot.Services[i].Healthcheck
				cloned.Services[i].Healthcheck = &healthcheck
			}
		}
	}
	if snapshot.ReleaseTask != nil {
		releaseTask := *snapshot.ReleaseTask
		releaseTask.Entrypoint = append([]string(nil), snapshot.ReleaseTask.Entrypoint...)
		releaseTask.Command = append([]string(nil), snapshot.ReleaseTask.Command...)
		releaseTask.Env = cloneStringMap(snapshot.ReleaseTask.Env)
		releaseTask.VolumeMounts = append([]desiredstate.VolumeMountJSON(nil), snapshot.ReleaseTask.VolumeMounts...)
		cloned.ReleaseTask = &releaseTask
	}
	if snapshot.Ingress != nil {
		ingress := *snapshot.Ingress
		ingress.Hosts = append([]string(nil), snapshot.Ingress.Hosts...)
		ingress.Routes = append([]desiredstate.IngressRouteJSON(nil), snapshot.Ingress.Routes...)
		cloned.Ingress = &ingress
	}
	if snapshot.SecretRefs != nil {
		cloned.SecretRefs = make(map[string][]string, len(snapshot.SecretRefs))
		for serviceName, refs := range snapshot.SecretRefs {
			cloned.SecretRefs[serviceName] = append([]string(nil), refs...)
		}
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
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
	if strings.TrimSpace(snapshot.Image) != "" && len(snapshot.Services) == 0 && snapshot.ReleaseTask == nil {
		return true
	}
	for _, service := range snapshot.Services {
		if soloNodeCanRunKind(node, service.Kind) {
			return true
		}
	}
	return snapshot.ReleaseTask != nil && runReleaseTask
}

func publicationReleasesFromSnapshots(snapshots []desiredstate.DeploySnapshot) []corerelease.Release {
	releases := make([]corerelease.Release, 0, len(snapshots))
	for _, snapshot := range snapshots {
		workspaceKey := strings.TrimSpace(snapshot.WorkspaceKey)
		environment := strings.TrimSpace(snapshot.Environment)
		if environment == "" {
			environment = config.DefaultEnvironment
		}
		snapshot.WorkspaceKey = workspaceKey
		snapshot.Environment = environment
		releases = append(releases, corerelease.Release{
			ID:       workspaceKey + "\n" + environment,
			Revision: strings.TrimSpace(snapshot.Revision),
			Snapshot: snapshot,
			Image:    corerelease.ImageRef{Reference: strings.TrimSpace(snapshot.Image)},
		})
	}
	return releases
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
		steps = append(steps, "devopsellence logs --node "+shellQuote(nodeName)+" --lines 100")
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
		releaseID := strings.TrimSpace(current.Current[key])
		release, ok := current.Releases[releaseID]
		snapshot := release.Snapshot
		if !ok {
			var snapshotOK bool
			snapshot, snapshotOK = current.Snapshots[key]
			if !snapshotOK {
				continue
			}
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

func soloAttachmentHasReleaseState(current solo.State, attachment solo.AttachmentRecord) bool {
	key := attachment.WorkspaceKey + "\n" + attachment.Environment
	if strings.TrimSpace(current.Current[key]) != "" {
		return true
	}
	_, ok := current.Snapshots[key]
	return ok
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
	verifiedPublicURLs := a.soloVerifiedPublicURLs(cfg, nodes)
	localReleaseKnown := len(opts.Nodes) > 0
	expectedRevision := ""
	if len(opts.Nodes) == 0 {
		if current, stateErr := a.readSoloState(); stateErr == nil {
			_, workspaceRoot, environmentName, configErr := a.loadResolvedSoloProjectConfig("")
			if configErr == nil {
				_, currentRelease, hasCurrent, releaseErr := current.CurrentRelease(workspaceRoot, environmentName)
				if releaseErr != nil {
					return releaseErr
				}
				if hasCurrent {
					localReleaseKnown = true
					expectedRevision = strings.TrimSpace(currentRelease.Revision)
				}
			}
		}
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

		if !localReleaseKnown {
			allSettled = false
			jsonResults = append(jsonResults, map[string]any{
				"node":    name,
				"status":  nil,
				"message": "remote agent has status, but this workspace has no current local release yet; run `devopsellence deploy`",
			})
			continue
		}
		if expectedRevision != "" && strings.TrimSpace(result.Status.Revision) != expectedRevision {
			allSettled = false
			jsonResults = append(jsonResults, map[string]any{
				"node":    name,
				"status":  nil,
				"message": fmt.Sprintf("remote agent status revision %q does not match current local release %q; run `devopsellence deploy`", strings.TrimSpace(result.Status.Revision), expectedRevision),
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

	payload := map[string]any{
		"schema_version": outputSchemaVersion,
		"nodes":          jsonResults,
	}
	if len(verifiedPublicURLs) > 0 {
		if allSettled {
			payload["public_urls"] = verifiedPublicURLs
		} else {
			payload["configured_public_urls"] = verifiedPublicURLs
			payload["warnings"] = []string{"public URLs are configured, but one or more nodes are not settled; check node status before testing reachability"}
		}
	} else if urls := soloStatusPublicURLs(cfg, nodes); len(urls) > 0 {
		payload["configured_public_urls"] = urls
		payload["public_url_status"] = soloPublicURLStatus(cfg)
		payload["warnings"] = []string{soloPublicURLWarning(cfg)}
	}
	if err := a.Printer.PrintJSON(payload); err != nil {
		return err
	}
	if readErrors > 0 {
		return ExitError{Code: 1, Err: RenderedError{Err: fmt.Errorf("status failed for %d node(s)", readErrors)}}
	}
	return nil
}

func (a *App) SoloReleaseList(ctx context.Context, opts SoloReleaseListOptions) error {
	_, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig("")
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	_, currentRelease, hasCurrent, err := current.CurrentRelease(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	releases, err := current.ReleaseHistory(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	displayEnvironmentID, err := soloDisplayEnvironmentID(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	limit := opts.Limit
	if limit > 0 && len(releases) > limit {
		releases = releases[:limit]
	}
	items := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		items = append(items, map[string]any{
			"id":            release.ID,
			"revision":      release.Revision,
			"config_digest": release.ConfigDigest,
			"image":         release.Image.Reference,
			"created_at":    release.CreatedAt,
			"current":       hasCurrent && release.ID == currentRelease.ID,
			"target_nodes":  release.TargetNodeIDs,
		})
	}
	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"environment":    environmentName,
		"environment_id": displayEnvironmentID,
		"current_release_id": func() string {
			if hasCurrent {
				return currentRelease.ID
			}
			return ""
		}(),
		"releases": items,
	})
}

func (a *App) SoloReleaseRollback(ctx context.Context, opts SoloReleaseRollbackOptions) error {
	stream := a.Printer.Stream("devopsellence release rollback")
	if err := stream.Event("started", map[string]any{}); err != nil {
		return err
	}
	_, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig("")
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	attachedNodeNames, err := current.AttachedNodeNames(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	if len(attachedNodeNames) == 0 {
		return fmt.Errorf("no nodes attached to %s; run `devopsellence node attach <name>`", environmentName)
	}
	environmentID, currentRelease, hasCurrent, err := current.CurrentRelease(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	if !hasCurrent {
		return fmt.Errorf("no current release for %s; run `devopsellence deploy` first", environmentName)
	}
	releases, err := current.ReleaseHistory(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	selected, err := corerelease.SelectRollbackRelease(releases, currentRelease.ID, opts.Selector)
	if err != nil {
		return err
	}
	rollbackTargetNodeNames, err := soloRollbackTargetNodeNames(attachedNodeNames, selected)
	if err != nil {
		return err
	}
	nodes, err := a.resolveNodes(current, rollbackTargetNodeNames)
	if err != nil {
		return err
	}
	if opts.DryRun {
		return stream.Result(map[string]any{
			"action":            "rollback",
			"dry_run":           true,
			"release_id":        selected.ID,
			"rolled_back_from":  currentRelease.ID,
			"workload_revision": selected.Revision,
			"environment":       environmentName,
			"nodes":             sortedNodeNames(nodes),
			"phase":             "planned",
			"selected_image":    selected.Image.Reference,
			"selector":          opts.Selector,
			"side_effects": map[string]bool{
				"ssh":         false,
				"publish":     false,
				"state_write": false,
			},
			"next_steps": []string{"devopsellence release rollback " + selected.ID},
		})
	}
	now := time.Now().UTC()
	deployment, err := corerelease.NewDeployment(corerelease.DeploymentCreateInput{
		ID:            soloDeploymentID(corerelease.DeploymentKindRollback, selected.Revision, now),
		EnvironmentID: environmentID,
		ReleaseID:     selected.ID,
		Kind:          corerelease.DeploymentKindRollback,
		Sequence:      nextSoloDeploymentSequence(current, environmentID),
		TargetNodeIDs: rollbackTargetNodeNames,
		CreatedAt:     now,
	})
	if err != nil {
		return err
	}
	if err := current.SaveDeployment(deployment); err != nil {
		return err
	}
	deployment.Status = corerelease.DeploymentStatusRunning
	deployment.StatusMessage = "publishing desired state"
	if err := current.SaveDeployment(deployment); err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}

	publishState := cloneSoloState(current)
	if _, err := publishState.SaveRelease(selected); err != nil {
		return err
	}
	statusBaselines := a.soloNodeStatusTimes(ctx, nodes)
	desiredStateRevisions, err := a.republishNodes(ctx, publishState, rollbackTargetNodeNames)
	if err != nil {
		if persistErr := a.persistSoloDeploymentFailure(current, deployment, desiredStateRevisions, err); persistErr != nil {
			return errors.Join(err, fmt.Errorf("persist deployment failure: %w", persistErr))
		}
		return err
	}
	if _, err := current.SaveRelease(selected); err != nil {
		return err
	}
	if err := current.SaveDeployment(deployment); err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if err := a.waitForSoloRollout(ctx, nodes, desiredStateRevisions, statusBaselines); err != nil {
		if persistErr := a.persistSoloDeploymentRolloutFailure(current, deployment, desiredStateRevisions, err); persistErr != nil {
			return errors.Join(err, fmt.Errorf("persist deployment failure: %w", persistErr))
		}
		return err
	}
	deployment.Status = corerelease.DeploymentStatusSettled
	deployment.StatusMessage = "rollback settled"
	deployment.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	deployment.PublicationResult = soloDeploymentPublicationResult(desiredStateRevisions, nil)
	if err := current.SaveDeployment(deployment); err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	return stream.Result(map[string]any{
		"release_id":              selected.ID,
		"deployment_id":           deployment.ID,
		"rolled_back_from":        currentRelease.ID,
		"workload_revision":       selected.Revision,
		"desired_state_revisions": desiredStateRevisions,
		"environment":             environmentName,
		"nodes":                   sortedNodeNames(nodes),
		"phase":                   "settled",
	})
}

func soloReadyPublicURLs(cfg *config.ProjectConfig, nodes map[string]config.Node) []string {
	if ingressRequiresTLSReadiness(cfg) {
		return nil
	}
	return soloStatusPublicURLs(cfg, nodes)
}

func soloStatusPublicURLs(cfg *config.ProjectConfig, nodes map[string]config.Node) []string {
	if cfg == nil || len(nodes) == 0 {
		return nil
	}
	scheme := ingressURLScheme(cfg)
	hosts := concreteIngressHosts(cfg)
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
	return publicURLsForHosts(scheme, hosts)
}

func ingressConfiguredPublicURLs(cfg *config.ProjectConfig) []string {
	return publicURLsForHosts(ingressURLScheme(cfg), concreteIngressHosts(cfg))
}

func ingressRequiresTLSReadiness(cfg *config.ProjectConfig) bool {
	if cfg == nil || cfg.Ingress == nil {
		return false
	}
	tlsMode := strings.TrimSpace(cfg.Ingress.TLS.Mode)
	return strings.EqualFold(tlsMode, "auto") || strings.EqualFold(tlsMode, "manual")
}

func soloPublicURLStatus(cfg *config.ProjectConfig) string {
	if ingressRequiresTLSReadiness(cfg) {
		return "configured_tls_pending"
	}
	return "configured_pending"
}

func soloPublicURLWarning(cfg *config.ProjectConfig) string {
	if ingressRequiresTLSReadiness(cfg) {
		return "HTTPS public URLs are configured, but TLS readiness has not been verified yet; use `devopsellence ingress check --wait 2m` before treating them as reachable"
	}
	return "public URLs are configured, but one or more nodes are not settled; check node status before testing reachability"
}

func ingressURLScheme(cfg *config.ProjectConfig) string {
	if cfg != nil && cfg.Ingress != nil {
		tlsMode := strings.TrimSpace(cfg.Ingress.TLS.Mode)
		if strings.EqualFold(tlsMode, "auto") || strings.EqualFold(tlsMode, "manual") {
			return "https"
		}
	}
	return "http"
}

func concreteIngressHosts(cfg *config.ProjectConfig) []string {
	if cfg == nil || cfg.Ingress == nil {
		return nil
	}
	hosts := []string{}
	for _, host := range normalizeIngressHosts(cfg.Ingress.Hosts) {
		if host == "*" {
			continue
		}
		hosts = append(hosts, host)
	}
	return hosts
}

func publicURLsForHosts(scheme string, hosts []string) []string {
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
	environmentName := a.effectiveEnvironment(opts.Environment, cfg)
	resolved, err := config.ResolveEnvironmentConfig(*cfg, environmentName)
	if err != nil {
		return err
	}
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --service")}
	}
	service, ok := resolved.Services[serviceName]
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
	configUpdated, err := ensureServiceSecretRefForEnvironment(cfg, environmentName, serviceName, soloSecretConfigRef(record))
	if err != nil {
		return fmt.Errorf("secret saved locally but update devopsellence.yml failed: %w", err)
	}
	if configUpdated {
		if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
			return fmt.Errorf("secret saved locally but update devopsellence.yml failed: %w", err)
		}
	}

	payload := map[string]any{
		"schema_version": outputSchemaVersion,
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
	if record.Store == solo.SecretStorePlaintext {
		payload["warnings"] = []string{"solo plaintext secrets are stored locally in the devopsellence solo state file; use --store 1password --op-ref for production secrets"}
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

func ensureServiceSecretRefForEnvironment(cfg *config.ProjectConfig, environmentName, serviceName string, ref config.SecretRef) (bool, error) {
	if environmentServiceSecretRefsOverride(cfg, environmentName, serviceName) {
		return ensureEnvironmentServiceSecretRef(cfg, environmentName, serviceName, ref)
	}
	return ensureServiceSecretRef(cfg, serviceName, ref)
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
	updated, changed, err := ensureSecretRef(service.SecretRefs, ref, serviceName, service)
	if err != nil || !changed {
		return changed, err
	}
	service.SecretRefs = updated
	cfg.Services[serviceName] = service
	return true, nil
}

func ensureEnvironmentServiceSecretRef(cfg *config.ProjectConfig, environmentName, serviceName string, ref config.SecretRef) (bool, error) {
	serviceName = strings.TrimSpace(serviceName)
	if cfg == nil {
		return false, nil
	}
	environmentName = normalizedSoloEnvironmentName(environmentName, cfg)
	resolved, err := config.ResolveEnvironmentConfig(*cfg, environmentName)
	if err != nil {
		return false, err
	}
	service, ok := resolved.Services[serviceName]
	if !ok {
		return false, nil
	}
	updated, changed, err := ensureSecretRef(service.SecretRefs, ref, serviceName, service)
	if err != nil || !changed {
		return changed, err
	}
	overlay := cfg.Environments[environmentName]
	serviceOverlay := overlay.Services[serviceName]
	serviceOverlay.SecretRefs = updated
	overlay.Services[serviceName] = serviceOverlay
	cfg.Environments[environmentName] = overlay
	return true, nil
}

func ensureSecretRef(existingRefs []config.SecretRef, ref config.SecretRef, serviceName string, service config.ServiceConfig) ([]config.SecretRef, bool, error) {
	for i, existing := range existingRefs {
		if existing.Name == ref.Name {
			if existing.Secret == ref.Secret {
				return existingRefs, false, nil
			}
			updated := append([]config.SecretRef(nil), existingRefs...)
			updated[i] = ref
			return updated, true, nil
		}
	}
	if serviceSecretRefConflict(service, ref.Name) {
		return existingRefs, false, fmt.Errorf("service %q already defines %s in env; remove it before adding a secret_ref with the same name", serviceName, ref.Name)
	}
	updated := append([]config.SecretRef(nil), existingRefs...)
	updated = append(updated, ref)
	return updated, true, nil
}

func removeServiceSecretRefForEnvironment(cfg *config.ProjectConfig, environmentName, serviceName, name string) bool {
	if environmentServiceSecretRefsOverride(cfg, environmentName, serviceName) {
		return removeEnvironmentServiceSecretRef(cfg, environmentName, serviceName, name)
	}
	return removeServiceSecretRef(cfg, serviceName, name)
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
	filtered, changed := removeSecretRef(service.SecretRefs, name)
	if !changed {
		return false
	}
	service.SecretRefs = filtered
	cfg.Services[serviceName] = service
	return true
}

func removeEnvironmentServiceSecretRef(cfg *config.ProjectConfig, environmentName, serviceName, name string) bool {
	serviceName = strings.TrimSpace(serviceName)
	if cfg == nil {
		return false
	}
	environmentName = normalizedSoloEnvironmentName(environmentName, cfg)
	overlay, ok := cfg.Environments[environmentName]
	if !ok || overlay.Services == nil {
		return false
	}
	serviceOverlay, ok := overlay.Services[serviceName]
	if !ok || serviceOverlay.SecretRefs == nil {
		return false
	}
	filtered, changed := removeSecretRef(serviceOverlay.SecretRefs, name)
	if !changed {
		return false
	}
	serviceOverlay.SecretRefs = filtered
	overlay.Services[serviceName] = serviceOverlay
	cfg.Environments[environmentName] = overlay
	return true
}

func removeSecretRef(existingRefs []config.SecretRef, name string) ([]config.SecretRef, bool) {
	filtered := make([]config.SecretRef, 0, len(existingRefs))
	changed := false
	for _, existing := range existingRefs {
		if existing.Name == name {
			changed = true
			continue
		}
		filtered = append(filtered, existing)
	}
	return filtered, changed
}

func environmentServiceSecretRefsOverride(cfg *config.ProjectConfig, environmentName, serviceName string) bool {
	if cfg == nil {
		return false
	}
	environmentName = normalizedSoloEnvironmentName(environmentName, cfg)
	overlay, ok := cfg.Environments[environmentName]
	if !ok || overlay.Services == nil {
		return false
	}
	serviceOverlay, ok := overlay.Services[strings.TrimSpace(serviceName)]
	return ok && serviceOverlay.SecretRefs != nil
}

func normalizedSoloEnvironmentName(environmentName string, cfg *config.ProjectConfig) string {
	environmentName = strings.TrimSpace(environmentName)
	if environmentName == "" && cfg != nil {
		environmentName = strings.TrimSpace(cfg.DefaultEnvironment)
	}
	if environmentName == "" {
		environmentName = config.DefaultEnvironment
	}
	return environmentName
}

func (a *App) SoloSecretsList(_ context.Context, opts SoloSecretsListOptions) error {
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	environmentName := a.effectiveEnvironment(opts.Environment, cfg)
	resolved, err := config.ResolveEnvironmentConfig(*cfg, environmentName)
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	secrets, err := current.ListSecrets(workspaceRoot, environmentName, opts.ServiceName)
	if err != nil {
		return err
	}
	items := soloSecretListItems(&resolved, secrets, opts.ServiceName)

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

func (a *App) SoloNodeList(_ context.Context, opts SoloNodeListOptions) error {
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	currentWorkspaceKey := ""
	currentEnvironment := ""
	nodeNames := current.NodeNames()
	scope := "global"
	_, workspaceRoot, environmentName, attached, hasWorkspace, cfgErr := a.currentSoloAttachment(current)
	if cfgErr != nil {
		if !opts.All {
			return cfgErr
		}
	} else if hasWorkspace {
		workspaceKey, keyErr := solo.CanonicalWorkspaceKey(workspaceRoot)
		if keyErr != nil {
			if !opts.All {
				return keyErr
			}
		} else {
			currentWorkspaceKey = workspaceKey
			currentEnvironment = environmentName
			if !opts.All {
				nodeNames = attached
				scope = "current_environment"
			}
		}
	} else if !opts.All && workspaceRoot != "" {
		return errors.New("Current workspace configuration could not be determined. Run `devopsellence init --mode solo` or use `--all` to list all nodes.")
	}
	type nodeListItem struct {
		Name                    string           `json:"name"`
		Node                    map[string]any   `json:"node"`
		Attachments             []map[string]any `json:"attachments,omitempty"`
		CurrentEnvironmentBound bool             `json:"current_environment_bound,omitempty"`
	}
	items := make([]nodeListItem, 0, len(nodeNames))
	nodes := make(map[string]map[string]any, len(nodeNames))
	for _, name := range nodeNames {
		node, ok := current.Nodes[name]
		if !ok {
			continue
		}
		attachments := current.AttachmentsForNode(name)
		bound := false
		for _, attachment := range attachments {
			if attachment.WorkspaceKey == currentWorkspaceKey && attachment.Environment == currentEnvironment {
				bound = true
				break
			}
		}
		listedNode := soloNodeListNode(node)
		nodes[name] = listedNode
		items = append(items, nodeListItem{
			Name:                    name,
			Node:                    listedNode,
			Attachments:             soloNodeListAttachments(attachments, currentWorkspaceKey, currentEnvironment),
			CurrentEnvironmentBound: bound,
		})
	}

	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"scope":          scope,
		"nodes":          nodes,
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
	environmentName := a.effectiveEnvironment(opts.Environment, cfg)
	attachment, changed, err := a.attachNode(&current, workspaceRoot, environmentName, opts.Node)
	if err != nil {
		return err
	}
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if soloAttachmentHasReleaseState(current, attachment) {
		if _, err := a.republishNodes(ctx, current, attachment.NodeNames); err != nil {
			return err
		}
	}

	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"node":           opts.Node,
		"environment":    environmentName,
		"changed":        changed,
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
	environmentName := a.effectiveEnvironment(opts.Environment, cfg)
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
		"schema_version": outputSchemaVersion,
		"node":           opts.Node,
		"environment":    environmentName,
		"changed":        true,
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
	environmentName := a.effectiveEnvironment(opts.Environment, cfg)
	resolved, err := config.ResolveEnvironmentConfig(*cfg, environmentName)
	if err != nil {
		return err
	}
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		return ExitError{Code: 2, Err: errors.New("missing required option: --service")}
	}
	if _, ok := resolved.Services[serviceName]; !ok {
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
	configUpdated := removeServiceSecretRefForEnvironment(cfg, environmentName, serviceName, opts.Key)
	if configUpdated {
		if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
			return fmt.Errorf("secret deleted locally but update devopsellence.yml failed: %w", err)
		}
	}

	return a.Printer.PrintJSON(map[string]any{"schema_version": outputSchemaVersion, "key": record.Name, "service_name": record.ServiceName, "environment": record.Environment, "config_updated": configUpdated, "config_path": a.ConfigStore.PathFor(workspaceRoot), "action": "deleted"})

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
	return a.Printer.PrintJSON(map[string]any{"schema_version": outputSchemaVersion, "node": opts.Node, "lines": lines, "limit": linesLimit})
}

func (a *App) SoloWorkloadLogs(ctx context.Context, opts SoloWorkloadLogsOptions) error {
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		serviceName = "web"
	}
	linesLimit := opts.Lines
	if linesLimit < 1 || linesLimit > soloLogsMaxLines {
		return ExitError{Code: 2, Err: fmt.Errorf("--lines must be between 1 and %d", soloLogsMaxLines)}
	}
	nodes, cfg, err := a.soloStatusSelection(SoloStatusOptions{Nodes: opts.Nodes})
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes selected; attach a node or pass --node")
	}
	if cfg == nil {
		return fmt.Errorf("no workspace selected; attach a workspace or run this command from a workspace")
	}
	environmentName := a.effectiveEnvironment("", cfg)
	workspaceRoot, err := a.soloCurrentWorkspaceRoot()
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	results := make([]map[string]any, 0, len(nodes))
	ok := true
	for _, nodeName := range sortedNodeNames(nodes) {
		runtimeEnvironmentName, err := soloRuntimeEnvironmentNameForNode(current, workspaceRoot, environmentName, nodeName)
		if err != nil {
			return err
		}
		diag := runRemoteDiagnostic(ctx, nodes[nodeName], remoteDockerLogsCommand(runtimeEnvironmentName, serviceName, linesLimit))
		lines := splitNonFinalEmptyLines(diag.Stdout)
		entry := map[string]any{
			"node":                nodeName,
			"service":             serviceName,
			"runtime_environment": runtimeEnvironmentName,
			"ok":                  diag.Err == nil && diag.ExitCode == 0,
			"lines":               lines,
		}
		if diag.Err != nil {
			entry["error"] = diag.Err.Error()
			ok = false
		} else if diag.ExitCode != 0 {
			entry["exit_code"] = diag.ExitCode
			entry["error"] = workloadLogsErrorMessage(diag)
			if noWorkloadContainers(diag) {
				fallback := runRemoteDiagnostic(ctx, nodes[nodeName], remoteJournalctlCommand(fmt.Sprintf("-u devopsellence-agent --no-pager -n %d", linesLimit)))
				entry["fallback"] = "devopsellence_agent_logs"
				entry["fallback_lines"] = splitNonFinalEmptyLines(fallback.Stdout)
				if fallback.Err != nil {
					entry["fallback_error"] = fallback.Err.Error()
				} else if fallback.ExitCode != 0 {
					entry["fallback_exit_code"] = fallback.ExitCode
					entry["fallback_error"] = diagnosticErrorMessage(fallback)
				}
			}
			ok = false
		}
		results = append(results, entry)
	}
	payload := map[string]any{
		"schema_version": outputSchemaVersion,
		"environment":    environmentName,
		"service":        serviceName,
		"limit":          linesLimit,
		"nodes":          results,
	}
	if err := a.Printer.PrintJSON(payload); err != nil {
		return err
	}
	if !ok {
		return ExitError{Code: 1, Err: RenderedError{Err: fmt.Errorf("workload logs failed")}}
	}
	return nil
}

func (a *App) SoloNodeExec(ctx context.Context, opts SoloNodeExecOptions) error {
	command, err := remoteUserCommand(opts.Command)
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	node, ok := current.Nodes[opts.Node]
	if !ok {
		return fmt.Errorf("node %q not found", opts.Node)
	}
	target := soloExecTarget{
		Kind:    "node",
		Node:    opts.Node,
		Command: append([]string(nil), opts.Command...),
	}
	return a.runSoloExecCommand(ctx, node, target, command)
}

func (a *App) SoloExec(ctx context.Context, opts SoloExecOptions) error {
	serviceName := strings.TrimSpace(opts.ServiceName)
	if serviceName == "" {
		return ExitError{Code: 2, Err: errors.New("service name is required")}
	}
	if _, err := remoteUserCommand(opts.Command); err != nil {
		return err
	}
	cfg, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig(opts.Environment)
	if err != nil {
		return err
	}
	if _, ok := cfg.Services[serviceName]; !ok {
		return ExitError{Code: 2, Err: fmt.Errorf("service %q not found in devopsellence.yml", serviceName)}
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	nodeNames := append([]string(nil), opts.Nodes...)
	if len(nodeNames) == 0 {
		nodeNames, err = current.AttachedNodeNames(workspaceRoot, environmentName)
		if err != nil {
			return err
		}
		if len(nodeNames) == 0 {
			return fmt.Errorf("no nodes selected for environment %s; attach a node or pass --node", environmentName)
		}
	}
	nodes, err := a.resolveNodes(current, nodeNames)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes selected for environment %s; attach a node or pass --node", environmentName)
	}
	candidates := []soloExecTarget{}
	for _, nodeName := range sortedNodeNames(nodes) {
		result, err := readNodeStatus(ctx, nodes[nodeName])
		if err != nil {
			return fmt.Errorf("[%s] read status: %w", nodeName, err)
		}
		if result.Missing {
			continue
		}
		runtimeEnvironmentName, err := soloRuntimeEnvironmentNameForNode(current, workspaceRoot, environmentName, nodeName)
		if err != nil {
			return err
		}
		container := statusServiceContainer(result.Status, runtimeEnvironmentName, serviceName)
		if container == "" {
			continue
		}
		candidates = append(candidates, soloExecTarget{
			Kind:        "service",
			Node:        nodeName,
			Environment: runtimeEnvironmentName,
			Service:     serviceName,
			Container:   container,
			Command:     append([]string(nil), opts.Command...),
		})
	}
	if len(candidates) == 0 {
		return fmt.Errorf("no active container found for service %q in environment %s", serviceName, environmentName)
	}
	if len(candidates) > 1 {
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.Node)
		}
		return ExitError{Code: 2, Err: fmt.Errorf("service %q is running on multiple nodes (%s); select a single node with --node <node>", serviceName, strings.Join(names, ", "))}
	}
	target := candidates[0]
	return a.runSoloExecCommand(ctx, nodes[target.Node], target, remoteDockerExecCommand(target.Container, opts.Command))
}

type soloExecTarget struct {
	Kind        string
	Node        string
	Environment string
	Service     string
	Container   string
	Command     []string
}

const (
	soloExecExitMarkerPrefix = "__DEVOPSELLENCE_EXEC_EXIT_CODE__"
	// Keep raw messages well below 1 MiB. NDJSON encoding can expand control
	// characters up to six bytes each before consumers scan the line.
	soloExecMaxLineBytes = 128 * 1024
)

func (a *App) runSoloExecCommand(ctx context.Context, node config.Node, target soloExecTarget, command string) error {
	operation := soloExecOperation(target.Kind)
	stream := a.Printer.Stream(operation)
	if err := a.printSoloExecEvent(stream, output.EventStarted, target, nil); err != nil {
		return err
	}

	exitCode := -1
	exitMarker, err := newSoloExecExitMarker()
	if err != nil {
		if renderErr := a.printSoloExecError(operation, target, 1, err); renderErr != nil {
			return renderErr
		}
		return ExitError{Code: 1, Err: RenderedError{Err: err}}
	}
	stdout := &soloExecEventWriter{stream: stream, target: target, streamName: "stdout", exitCode: &exitCode, exitMarker: exitMarker}
	stderr := &soloExecEventWriter{stream: stream, target: target, streamName: "stderr", exitCode: &exitCode, exitMarker: exitMarker}
	err = solo.RunSSHInteractive(ctx, node, remoteExecWrapper(command, exitMarker), stdout, stderr)
	if flushErr := stdout.Flush(); flushErr != nil && err == nil {
		err = flushErr
	}
	if flushErr := stderr.Flush(); flushErr != nil && err == nil {
		err = flushErr
	}
	if stdout.Err() != nil && err == nil {
		err = stdout.Err()
	}
	if stderr.Err() != nil && err == nil {
		err = stderr.Err()
	}
	if err != nil {
		if renderErr := a.printSoloExecError(operation, target, 1, err); renderErr != nil {
			return renderErr
		}
		return ExitError{Code: 1, Err: RenderedError{Err: err}}
	}
	if exitCode < 0 {
		err := errors.New("exec did not report a remote exit code")
		if renderErr := a.printSoloExecError(operation, target, 1, err); renderErr != nil {
			return renderErr
		}
		return ExitError{Code: 1, Err: RenderedError{Err: err}}
	}
	if exitCode != 0 {
		processCode := exitCode
		if processCode < 1 || processCode > 255 {
			processCode = 1
		}
		message := fmt.Sprintf("exec failed with exit code %d", exitCode)
		if err := a.printSoloExecError(operation, target, exitCode, errors.New(message)); err != nil {
			return err
		}
		return ExitError{Code: processCode, Err: RenderedError{Err: fmt.Errorf("exec failed with exit code %d", exitCode)}}
	}
	fields := soloExecFields(target)
	fields["exit_code"] = exitCode
	return stream.Result(fields)
}

func soloExecOperation(kind string) string {
	if kind == "node" {
		return "devopsellence node exec"
	}
	return "devopsellence exec"
}

func (a *App) printSoloExecEvent(stream output.Stream, event string, target soloExecTarget, fields map[string]any) error {
	payload := soloExecFields(target)
	for key, value := range fields {
		payload[key] = value
	}
	return stream.Event(event, payload)
}

func (a *App) printSoloExecError(operation string, target soloExecTarget, exitCode int, err error) error {
	return a.Printer.PrintErrorEvent(operation, output.ErrorPayload{
		Code:     "command_failed",
		Message:  err.Error(),
		ExitCode: exitCode,
		Fields:   output.Fields(soloExecFields(target)),
	})
}

type soloExecEventWriter struct {
	stream     output.Stream
	target     soloExecTarget
	streamName string
	exitCode   *int
	exitMarker string
	buf        bytes.Buffer
	dropping   bool
	err        error
	emptyLines int
}

func (w *soloExecEventWriter) Write(p []byte) (int, error) {
	written := 0
	for _, b := range p {
		if b == '\n' {
			if w.dropping {
				w.dropping = false
				written++
				continue
			}
			if err := w.flushLine(false); err != nil {
				w.err = err
				return written, err
			}
			w.dropping = false
			written++
			continue
		}
		if w.dropping {
			written++
			continue
		}
		if w.buf.Len() >= soloExecMaxLineBytes {
			if err := w.flushLine(true); err != nil {
				w.err = err
				return written, err
			}
			w.dropping = true
			written++
			continue
		}
		_ = w.buf.WriteByte(b)
		written++
	}
	return len(p), nil
}

func (w *soloExecEventWriter) Flush() error {
	if w.dropping {
		w.buf.Reset()
		w.dropping = false
		return nil
	}
	if w.emptyLines > 0 {
		if err := w.flushEmptyLines(w.emptyLines); err != nil {
			w.err = err
			return err
		}
		w.emptyLines = 0
	}
	if w.buf.Len() == 0 {
		return nil
	}
	return w.flushLine(false)
}

func (w *soloExecEventWriter) Err() error {
	return w.err
}

func (w *soloExecEventWriter) flushLine(truncated bool) error {
	line := w.buf.String()
	w.buf.Reset()
	if w.streamName == "stderr" {
		code, isExitMarker, err := parseSoloExecExitCodeLine(line, w.exitMarker)
		if err != nil {
			return err
		}
		if isExitMarker {
			if w.emptyLines > 1 {
				if err := w.flushEmptyLines(w.emptyLines - 1); err != nil {
					return err
				}
			}
			w.emptyLines = 0
			*w.exitCode = code
			return nil
		}
	}
	if w.streamName == "stderr" && line == "" && !truncated {
		w.emptyLines++
		return nil
	}
	if w.emptyLines > 0 {
		if err := w.flushEmptyLines(w.emptyLines); err != nil {
			return err
		}
		w.emptyLines = 0
	}
	return w.emitLine(line, truncated)
}

func (w *soloExecEventWriter) flushEmptyLines(count int) error {
	for i := 0; i < count; i++ {
		if err := w.emitLine("", false); err != nil {
			return err
		}
	}
	return nil
}

func (w *soloExecEventWriter) emitLine(line string, truncated bool) error {
	fields := map[string]any{
		"stream":  w.streamName,
		"message": line,
	}
	if truncated {
		fields["truncated"] = true
	}
	payload := soloExecFields(w.target)
	for key, value := range fields {
		payload[key] = value
	}
	return w.stream.Event("output", payload)
}

func parseSoloExecExitCodeLine(line, marker string) (int, bool, error) {
	normalized := strings.TrimSuffix(line, "\r")
	if !strings.HasPrefix(normalized, marker) {
		return 0, false, nil
	}
	rawCode := strings.TrimPrefix(normalized, marker)
	if rawCode == "" {
		return 0, false, nil
	}
	for _, ch := range rawCode {
		if ch < '0' || ch > '9' {
			return 0, false, nil
		}
	}
	code, err := strconv.Atoi(rawCode)
	if err != nil {
		return 0, true, fmt.Errorf("parse remote exec exit code: %w", err)
	}
	return code, true, nil
}

func newSoloExecExitMarker() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate exec marker nonce: %w", err)
	}
	return soloExecExitMarkerPrefix + hex.EncodeToString(nonce[:]) + "__", nil
}

func soloExecFields(target soloExecTarget) map[string]any {
	payload := map[string]any{
		"target":  target.Kind,
		"node":    target.Node,
		"command": target.Command,
	}
	if target.Environment != "" {
		payload["environment"] = target.Environment
	}
	if target.Service != "" {
		payload["service"] = target.Service
	}
	if target.Container != "" {
		payload["container"] = target.Container
	}
	return payload
}

func statusServiceContainer(status soloNodeStatus, environmentName, serviceName string) string {
	for _, environment := range status.Environments {
		if strings.TrimSpace(environment.Name) != environmentName {
			continue
		}
		for _, service := range environment.Services {
			if strings.TrimSpace(service.Name) != serviceName {
				continue
			}
			container := strings.TrimSpace(service.Container)
			if container == "" {
				return ""
			}
			switch strings.TrimSpace(service.State) {
			case "running", "starting", "unhealthy":
				return container
			default:
				return ""
			}
		}
	}
	return ""
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
		"devopsellence logs --node " + shellQuote(opts.Node) + " --lines 100",
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

func diagnosticErrorMessage(diag remoteDiagnosticResult) string {
	if message := strings.TrimSpace(diag.Stderr); message != "" {
		return message
	}
	if message := strings.TrimSpace(diag.Stdout); message != "" {
		return message
	}
	return fmt.Sprintf("command exited with code %d", diag.ExitCode)
}

func noWorkloadContainers(diag remoteDiagnosticResult) bool {
	return strings.Contains(diag.Stderr, soloNoWorkloadContainersSentinel)
}

func workloadLogsErrorMessage(diag remoteDiagnosticResult) string {
	if !noWorkloadContainers(diag) {
		return diagnosticErrorMessage(diag)
	}
	lines := []string{}
	for _, line := range splitNonFinalEmptyLines(diag.Stderr) {
		if strings.TrimSpace(line) == soloNoWorkloadContainersSentinel {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}
	return "No workload containers found"
}

func collectRemoteText(ctx context.Context, node config.Node, command string) map[string]any {
	diag := runRemoteDiagnostic(ctx, node, command)
	result := map[string]any{"ok": diag.Err == nil && diag.ExitCode == 0}
	if diag.Err != nil {
		result["error"] = diag.Err.Error()
	} else if diag.ExitCode != 0 {
		result["exit_code"] = diag.ExitCode
		result["error"] = diagnosticErrorMessage(diag)
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
		result["error"] = diagnosticErrorMessage(diag)
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
		result["error"] = diagnosticErrorMessage(diag)
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
	if _, err := a.republishNodes(ctx, current, soloAffectedNodesForNodeWithReleaseState(current, opts.Node)); err != nil {
		return err
	}

	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"node":           opts.Node,
		"labels":         labels,
	})

}

func (a *App) SoloNodeLabelList(_ context.Context, opts SoloNodeLabelListOptions) error {
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.Node) != "" {
		node, ok := current.Nodes[opts.Node]
		if !ok {
			return fmt.Errorf("node %q not found", opts.Node)
		}
		return a.Printer.PrintJSON(map[string]any{"schema_version": outputSchemaVersion, "node": opts.Node, "labels": solo.NormalizeNode(node).Labels})
	}
	items := make([]map[string]any, 0, len(current.Nodes))
	for name, node := range current.Nodes {
		items = append(items, map[string]any{"node": name, "labels": solo.NormalizeNode(node).Labels})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["node"].(string) < items[j]["node"].(string) })
	return a.Printer.PrintJSON(map[string]any{"schema_version": outputSchemaVersion, "nodes": items})
}

func (a *App) SoloNodeLabelRemove(ctx context.Context, opts SoloNodeLabelRemoveOptions) error {
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
	remove := map[string]bool{}
	for _, label := range labels {
		remove[label] = true
	}
	kept := make([]string, 0, len(node.Labels))
	for _, label := range node.Labels {
		if !remove[label] {
			kept = append(kept, label)
		}
	}
	node.Labels = kept
	current.Nodes[opts.Node] = solo.NormalizeNode(node)
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	if _, err := a.republishNodes(ctx, current, soloAffectedNodesForNodeWithReleaseState(current, opts.Node)); err != nil {
		return err
	}
	return a.Printer.PrintJSON(map[string]any{"schema_version": outputSchemaVersion, "node": opts.Node, "labels": current.Nodes[opts.Node].Labels, "removed": labels})
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

	return a.Printer.PrintResultEvent("devopsellence agent install", map[string]any{"node": opts.Node, "action": "installed"})

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
	return a.Printer.PrintResultEvent("devopsellence agent uninstall", map[string]any{
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

	if err := a.Printer.PrintJSON(map[string]any{"schema_version": outputSchemaVersion, "checks": results}); err != nil {
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
		return discovered.WorkspaceRoot, nil
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
		if cfg == nil {
			return "", errors.New("No config found. Run `devopsellence init --mode solo`.")
		}
		if len(current.Nodes) == 0 {
			return "", errors.New("No nodes registered in solo state. Run `devopsellence node create <name>`.")
		}
		environmentName := a.effectiveEnvironment("", cfg)
		nodeNames, err := current.AttachedNodeNames(discovered.WorkspaceRoot, environmentName)
		if err != nil {
			return "", err
		}
		if len(nodeNames) == 0 {
			return "", errors.New("No nodes attached to the current environment. Run `devopsellence node attach <name>`.")
		}
		return fmt.Sprintf("%d node(s) attached to %s", len(nodeNames), environmentName), nil
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
	if ok && cfg != nil {
		environmentName := a.effectiveEnvironment("", cfg)
		nodeNames, err := current.AttachedNodeNames(discovered.WorkspaceRoot, environmentName)
		if err != nil {
			return err
		}
		runtimeChecks, runtimeFailed, err := a.soloRuntimeDoctorChecks(ctx, SoloDoctorOptions{Nodes: nodeNames})
		if err != nil {
			return err
		}
		payload["runtime_checks"] = runtimeChecks
		payload["ok"] = !runtimeFailed
		if runtimeFailed {
			nodes, err := a.resolveNodes(current, nodeNames)
			if err != nil {
				return err
			}
			payload["next_steps"] = soloDoctorNextSteps(nodes)
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
		"node":        nodeName,
		"config_path": a.ConfigStore.PathFor(workspaceRoot),
	}
	if hasHost {
		node, labels, err = existingSSHNodeFromCreateOptions(opts)
		if err != nil {
			return err
		}
		if err := validateSoloNodeSSH(ctx, node); err != nil {
			return fmt.Errorf("node create could not validate SSH for %s@%s:%d; fix --host/--user/--ssh-key or SSH access, then retry. If root login is disabled, try the image default user such as ubuntu, debian, or ec2-user and verify passwordless sudo with `ssh <user>@<host> sudo -n true`: %w", node.User, node.Host, node.Port, err)
		}
		result["source"] = "existing_ssh"
		result["ssh_checked"] = true
	} else {
		if err := a.ensureSoloNodeCreateSSHPublicKey(&opts, workspaceRoot); err != nil {
			return err
		}
		progress := a.soloProgress("devopsellence node create", map[string]any{"node": nodeName, "provider": strings.TrimSpace(opts.Provider)})
		created, createErr := a.createProviderNode(ctx, opts, cfg.Project, progress)
		if createErr != nil {
			return createErr
		}
		node = created.Node
		labels = created.Labels
		result["source"] = "provider"
		result["provider"] = created.ProviderSlug
		result["provider_server_id"] = created.Server.ID
		result["provider_region"] = node.ProviderRegion
		result["provider_size"] = node.ProviderSize
		if node.ProviderImage != "" {
			result["provider_image"] = node.ProviderImage
		}
	}
	if err := current.SetNode(nodeName, node); err != nil {
		return err
	}
	attached := false
	var attachment solo.AttachmentRecord
	if opts.Attach {
		environmentName := a.effectiveEnvironment("", cfg)
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
		if soloAttachmentHasReleaseState(current, attachment) {
			if _, err := a.republishNodes(ctx, current, attachment.NodeNames); err != nil {
				return err
			}
		}
	}

	result["host"] = node.Host
	result["labels"] = labels
	result["agent_installed"] = installed
	result["attached"] = attached
	return a.Printer.PrintResultEvent("devopsellence node create", result)

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
	progress := a.soloProgress("devopsellence node create", map[string]any{"node": opts.Name, "provider": strings.TrimSpace(opts.Provider)})
	created, err := a.createProviderNode(ctx, opts.SoloNodeCreateOptions, projectName, progress)
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
	if strings.TrimSpace(created.Node.ProviderRegion) != "" {
		result["provider_region"] = created.Node.ProviderRegion
	}
	if strings.TrimSpace(created.Node.ProviderSize) != "" {
		result["provider_size"] = created.Node.ProviderSize
	}
	if strings.TrimSpace(created.Node.ProviderImage) != "" {
		result["provider_image"] = created.Node.ProviderImage
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
	soloWorkloadLogsContainerLimit       = 20
	soloDiagnoseDockerItemLimit          = 100
	soloDiagnosePortsLineLimit           = 200
	soloDiagnoseTruncatedMarker          = "__DEVOPSELLENCE_TRUNCATED__"
	soloNoWorkloadContainersSentinel     = "__DEVOPSELLENCE_NO_WORKLOAD_CONTAINERS__"
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
		current.RemoveNode(opts.Name)
		if err := a.writeSoloState(current); err != nil {
			return err
		}
		knownHostsRemoved, knownHostsErr := solo.RemoveKnownHosts(node)

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
		return a.Printer.PrintResultEvent("devopsellence node remove", payload)

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
	current.RemoveNode(opts.Name)
	if err := a.writeSoloState(current); err != nil {
		return err
	}
	knownHostsRemoved, knownHostsErr := solo.RemoveKnownHosts(node)

	payload := map[string]any{
		"node":                opts.Name,
		"action":              "deleted",
		"known_hosts_removed": knownHostsRemoved,
		"provider":            provider,
		"provider_server_id":  providerServerID,
	}
	if strings.TrimSpace(node.ProviderRegion) != "" {
		payload["provider_region"] = node.ProviderRegion
	}
	if strings.TrimSpace(node.ProviderSize) != "" {
		payload["provider_size"] = node.ProviderSize
	}
	if strings.TrimSpace(node.ProviderImage) != "" {
		payload["provider_image"] = node.ProviderImage
	}
	if knownHostsErr != nil {
		payload["known_hosts_error"] = knownHostsErr.Error()
		payload["warnings"] = []string{"provider node deleted and local state removed, but SSH known_hosts cleanup failed"}
	}
	return a.Printer.PrintResultEvent("devopsellence node remove", payload)

}

func (a *App) SoloSupportBundle(_ context.Context, opts SoloSupportBundleOptions) error {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return err
	}
	cfg, err := a.ConfigStore.Read(discovered.WorkspaceRoot)
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	environmentName := ""
	attachedNodes := []string{}
	if cfg != nil {
		environmentName = a.effectiveEnvironment("", cfg)
		attachedNodes, _ = current.AttachedNodeNames(discovered.WorkspaceRoot, environmentName)
	}
	bundle := map[string]any{
		"schema_version": outputSchemaVersion,
		"kind":           "devopsellence_solo_support_bundle",
		"generated_at":   time.Now().UTC().Format(time.RFC3339),
		"cli_version":    cliversion.String(),
		"workspace": map[string]any{
			"root": discovered.WorkspaceRoot,
			"slug": discovered.ProjectSlug,
		},
		"environment":    environmentName,
		"config":         redactProjectConfigForSupport(cfg),
		"solo_state":     redactSoloStateForSupport(current),
		"attached_nodes": attachedNodes,
		"recommended_commands": []string{
			"devopsellence doctor",
			"devopsellence status",
			"devopsellence release list",
			"devopsellence node list --all",
			"devopsellence node diagnose <node>",
			"devopsellence node logs <node> --lines 200",
		},
	}
	outputPath := strings.TrimSpace(opts.Output)
	if outputPath == "" {
		outputPath = filepath.Join(discovered.WorkspaceRoot, ".devopsellence-support-bundle.json")
	}
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(discovered.WorkspaceRoot, outputPath)
	}
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if err := writePrivateFileAtomic(outputPath, data); err != nil {
		return err
	}
	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"action":         "support_bundle",
		"path":           outputPath,
		"redacted":       true,
		"next_steps": []string{
			"Attach this JSON file to a support/debugging issue when it is safe to share machine paths and node hostnames/IPs.",
			"Run devopsellence node diagnose <node> for live remote runtime details.",
		},
	})
}

func redactSoloStateForSupport(current solo.State) solo.State {
	data, err := json.Marshal(current)
	if err == nil {
		var clone solo.State
		if err := json.Unmarshal(data, &clone); err == nil {
			current = clone
		}
	}
	for key, node := range current.Nodes {
		if strings.TrimSpace(node.SSHKey) != "" {
			node.SSHKey = "[REDACTED]"
		}
		current.Nodes[key] = node
	}
	for key, snapshot := range current.Snapshots {
		current.Snapshots[key] = redactDeploySnapshotForSupport(snapshot)
	}
	for key, release := range current.Releases {
		release.Snapshot = redactDeploySnapshotForSupport(release.Snapshot)
		current.Releases[key] = release
	}
	for key, record := range current.Secrets {
		record.Value = "[REDACTED]"
		if strings.TrimSpace(record.Reference) != "" {
			record.Reference = "[REDACTED]"
		}
		current.Secrets[key] = record
	}
	return current
}

func redactDeploySnapshotForSupport(snapshot desiredstate.DeploySnapshot) desiredstate.DeploySnapshot {
	for i := range snapshot.Services {
		snapshot.Services[i].Env = redactStringMapValues(snapshot.Services[i].Env)
	}
	if snapshot.ReleaseTask != nil {
		snapshot.ReleaseTask.Env = redactStringMapValues(snapshot.ReleaseTask.Env)
	}
	return snapshot
}

func redactProjectConfigForSupport(cfg *config.ProjectConfig) *config.ProjectConfig {
	if cfg == nil {
		return nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		clone := *cfg
		cfg = &clone
	} else {
		var clone config.ProjectConfig
		if err := json.Unmarshal(data, &clone); err == nil {
			cfg = &clone
		} else {
			clone := *cfg
			cfg = &clone
		}
	}
	for name, service := range cfg.Services {
		service.Env = redactStringMapValues(service.Env)
		service.SecretRefs = redactConfigSecretRefs(service.SecretRefs)
		cfg.Services[name] = service
	}
	if cfg.Tasks.Release != nil {
		cfg.Tasks.Release.Env = redactStringMapValues(cfg.Tasks.Release.Env)
	}
	for environmentName, overlay := range cfg.Environments {
		for serviceName, service := range overlay.Services {
			service.Env = redactStringMapValues(service.Env)
			service.SecretRefs = redactConfigSecretRefs(service.SecretRefs)
			overlay.Services[serviceName] = service
		}
		if overlay.Tasks != nil && overlay.Tasks.Release != nil {
			overlay.Tasks.Release.Env = redactStringMapValues(overlay.Tasks.Release.Env)
		}
		cfg.Environments[environmentName] = overlay
	}
	return cfg
}

func redactStringMapValues(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	redacted := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(value) == "" {
			redacted[key] = value
			continue
		}
		redacted[key] = "[REDACTED]"
	}
	return redacted
}

func redactConfigSecretRefs(refs []config.SecretRef) []config.SecretRef {
	if refs == nil {
		return nil
	}
	redacted := make([]config.SecretRef, len(refs))
	copy(redacted, refs)
	for i := range redacted {
		if strings.TrimSpace(redacted[i].Secret) != "" {
			redacted[i].Secret = "[REDACTED]"
		}
	}
	return redacted
}

func writePrivateFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return os.Chmod(path, 0o600)
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
		"devopsellence node list --all # solo node names are global on this machine",
		"devopsellence node create <node-name> --host <host> --user root --ssh-key <path>",
		"devopsellence agent install <node-name>",
		"devopsellence node attach <node-name>",
		"# or let devopsellence create a Hetzner node:",
		"devopsellence provider login hetzner --token <token>",
		"devopsellence provider status hetzner",
		"devopsellence node create <node-name> --provider hetzner --install --attach",
		"devopsellence doctor",
		"devopsellence deploy",
	}
	return a.Printer.PrintJSON(map[string]any{
		"schema_version":   outputSchemaVersion,
		"mode":             string(ModeSolo),
		"workspace_root":   discovered.WorkspaceRoot,
		"project_slug":     discovered.ProjectSlug,
		"runtime_contract": soloInitRuntimeContract(*cfg, discovered, created),
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

func soloInitRuntimeContract(cfg config.ProjectConfig, discovered discovery.Result, created bool) map[string]any {
	serviceName, ok := cfg.PrimaryWebServiceName()
	if !ok {
		return map[string]any{
			"web_service": false,
			"port_source": "none",
			"reason":      "no primary web service detected",
			"requirement": "no web port contract applies unless a service exposes an http port or healthcheck in devopsellence.yml",
		}
	}
	service := cfg.Services[serviceName]
	port := service.HTTPPort(0)
	source := "default"
	switch {
	case !created:
		source = "config"
	case discovered.InferredWebPort > 0 && port == discovered.InferredWebPort:
		source = "dockerfile"
	case port != config.DefaultWebPort:
		source = "config"
	}
	contract := map[string]any{
		"web_service": true,
		"service":     serviceName,
		"port":        port,
		"port_source": source,
		"requirement": "the container must listen on this port; add EXPOSE to the Dockerfile or edit devopsellence.yml if it listens elsewhere",
	}
	if service.Healthcheck != nil {
		contract["healthcheck_path"] = service.Healthcheck.Path
		contract["healthcheck_port"] = service.Healthcheck.Port
	}
	return contract
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

func (a *App) SoloIngressCertInstall(ctx context.Context, opts SoloIngressCertInstallOptions) error {
	certFile := strings.TrimSpace(opts.CertFile)
	keyFile := strings.TrimSpace(opts.KeyFile)
	if certFile == "" {
		return ExitError{Code: 2, Err: errors.New("--cert-file is required")}
	}
	if keyFile == "" {
		return ExitError{Code: 2, Err: errors.New("--key-file is required")}
	}
	if err := validateReadableFile(certFile, "cert-file"); err != nil {
		return err
	}
	if err := validateReadableFile(keyFile, "key-file"); err != nil {
		return err
	}

	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	nodeNames := append([]string(nil), opts.Nodes...)
	currentKey := ""
	if len(nodeNames) == 0 {
		_, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig("")
		if err != nil {
			return err
		}
		currentKey, err = solo.EnvironmentStateKey(workspaceRoot, environmentName)
		if err != nil {
			return err
		}
		nodeNames, err = current.AttachedNodeNames(workspaceRoot, environmentName)
		if err != nil {
			return err
		}
		if len(nodeNames) == 0 {
			return fmt.Errorf("no nodes selected for environment %s; attach a node or pass --node", environmentName)
		}
	} else if _, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig(""); err == nil {
		currentKey, err = solo.EnvironmentStateKey(workspaceRoot, environmentName)
		if err != nil {
			return err
		}
	}
	nodes, err := a.resolveNodes(current, nodeNames)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("no nodes selected; attach a node or pass --node")
	}
	if err := validateSoloManualTLSInstallSafe(current, currentKey, sortedNodeNames(nodes)); err != nil {
		return err
	}

	installed := make([]map[string]any, 0, len(nodes))
	for _, nodeName := range sortedNodeNames(nodes) {
		node := nodes[nodeName]
		certPath, keyPath, err := installSoloIngressCert(ctx, node, certFile, keyFile)
		if err != nil {
			return fmt.Errorf("[%s] install ingress cert: %w", nodeName, err)
		}
		installed = append(installed, map[string]any{
			"node":      nodeName,
			"cert_path": certPath,
			"key_path":  keyPath,
		})
	}
	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"nodes":          installed,
		"next_steps": []string{
			"devopsellence ingress set --tls-mode manual --host <hostname>",
			"devopsellence deploy",
			"curl -vk https://<hostname>/",
		},
	})
}

func validateSoloManualTLSInstallSafe(current solo.State, currentKey string, nodeNames []string) error {
	currentKey = strings.TrimSpace(currentKey)
	for _, nodeName := range normalizeSoloNames(nodeNames) {
		for _, key := range current.AttachmentKeysForNode(nodeName) {
			if strings.TrimSpace(key) == currentKey {
				continue
			}
			snapshot, ok := current.Snapshots[key]
			if !ok {
				releaseID := strings.TrimSpace(current.Current[key])
				if releaseID != "" {
					if release, releaseOK := current.Releases[releaseID]; releaseOK {
						snapshot = release.Snapshot
						ok = true
					}
				}
			}
			if !ok || snapshot.Ingress == nil || normalizedSoloSnapshotIngressMode(snapshot.Ingress.Mode) != "public" {
				continue
			}
			if normalizedSoloSnapshotTLSMode(snapshot.Ingress.TLS.Mode) != "auto" {
				continue
			}
			project := strings.TrimSpace(snapshot.Metadata.Project)
			if project == "" {
				project = strings.TrimSpace(snapshot.WorkspaceRoot)
			}
			if project == "" {
				project = key
			}
			return fmt.Errorf("manual TLS cert install would affect auto TLS environment %s on node %s; move manual TLS to a dedicated node or switch all co-hosted public ingress on that node to manual/off first", project, nodeName)
		}
	}
	return nil
}

func normalizedSoloSnapshotIngressMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "public"
	}
	return mode
}

func normalizedSoloSnapshotTLSMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "auto"
	}
	return mode
}

func validateReadableFile(filePath, label string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s %q is a directory", label, filePath)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", label, err)
	}
	return file.Close()
}

func installSoloIngressCert(ctx context.Context, node config.Node, certFile, keyFile string) (string, string, error) {
	stateDir := firstNonEmpty(node.AgentStateDir, "/var/lib/devopsellence")
	certPath := path.Join(stateDir, "ingress-cert.pem")
	keyPath := path.Join(stateDir, "ingress-key.pem")
	nonce := time.Now().UnixNano()
	remoteCert := fmt.Sprintf("/tmp/devopsellence-ingress-cert-%d.pem", nonce)
	remoteKey := fmt.Sprintf("/tmp/devopsellence-ingress-key-%d.pem", nonce)
	if err := uploadSoloFile(ctx, node, certFile, remoteCert, false); err != nil {
		return certPath, keyPath, fmt.Errorf("upload cert: %w", err)
	}
	defer solo.RunSSHInteractive(ctx, node, "rm -f "+shellQuote(remoteCert), io.Discard, io.Discard)
	if err := uploadSoloFile(ctx, node, keyFile, remoteKey, true); err != nil {
		return certPath, keyPath, fmt.Errorf("upload key: %w", err)
	}
	defer solo.RunSSHInteractive(ctx, node, "rm -f "+shellQuote(remoteKey), io.Discard, io.Discard)
	script := fmt.Sprintf(`set -euo pipefail
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
run_root install -d -m 0755 %s
run_root install -m 0644 %s %s
run_root install -m 0600 %s %s
run_root systemctl restart devopsellence-agent
`, shellQuote(stateDir), shellQuote(remoteCert), shellQuote(certPath), shellQuote(remoteKey), shellQuote(keyPath))
	if err := solo.RunSSHInteractiveWithStdin(ctx, node, "bash -s", strings.NewReader(script), io.Discard, io.Discard); err != nil {
		return certPath, keyPath, err
	}
	return certPath, keyPath, nil
}

func uploadSoloFile(ctx context.Context, node config.Node, localPath, remotePath string, private bool) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()
	command := "cat > " + shellQuote(remotePath)
	if private {
		command = "umask 077; " + command
	}
	return solo.RunSSHStream(ctx, node, command, file)
}

func (a *App) IngressCheck(ctx context.Context, opts IngressCheckOptions) error {
	cfg, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig("")
	if err != nil {
		return err
	}
	current, err := a.readSoloState()
	if err != nil {
		return err
	}
	nodeNames, err := current.AttachedNodeNames(workspaceRoot, environmentName)
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
		if report.OK || !ingressDNSReportRetryable(report) || opts.Wait <= 0 || time.Now().After(deadline) {
			if report.OK {
				if err := recordSuccessfulSoloIngressCheck(&current, workspaceRoot, environmentName, report); err != nil {
					return err
				}
				if err := a.writeSoloState(current); err != nil {
					return err
				}
			}

			if err := a.Printer.PrintJSON(report); err != nil {
				return err
			}

			if !report.OK {
				return ExitError{Code: 1, Err: RenderedError{Err: ingressDNSReportError(report)}}
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

func recordSuccessfulSoloIngressCheck(current *solo.State, workspaceRoot, environmentName string, report ingressDNSReportResult) error {
	if current == nil {
		return errors.New("solo state is required")
	}
	key, err := solo.EnvironmentStateKey(workspaceRoot, environmentName)
	if err != nil {
		return err
	}
	if current.IngressChecks == nil {
		current.IngressChecks = map[string]solo.IngressCheckRecord{}
	}
	current.IngressChecks[key] = solo.IngressCheckRecord{
		OK:            true,
		PublicURLs:    append([]string(nil), report.PublicURLs...),
		ExpectedIPs:   append([]string(nil), report.ExpectedIPs...),
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
		WorkspaceRoot: workspaceRoot,
		Environment:   environmentName,
	}
	return nil
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

func (a *App) loadResolvedSoloProjectConfig(explicitEnvironment string) (*config.ProjectConfig, string, string, error) {
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return nil, "", "", err
	}
	selectedEnvironment := a.effectiveEnvironment(explicitEnvironment, cfg)
	resolved, err := config.ResolveEnvironmentConfig(*cfg, selectedEnvironment)
	if err != nil {
		return nil, "", "", err
	}
	return &resolved, workspaceRoot, selectedEnvironment, nil
}

func cloneSoloState(current solo.State) solo.State {
	cloned := current
	if current.Nodes != nil {
		cloned.Nodes = make(map[string]config.Node, len(current.Nodes))
		for key, value := range current.Nodes {
			cloned.Nodes[key] = value
		}
	}
	if current.Attachments != nil {
		cloned.Attachments = make(map[string]solo.AttachmentRecord, len(current.Attachments))
		for key, value := range current.Attachments {
			cloned.Attachments[key] = value
		}
	}
	if current.Snapshots != nil {
		cloned.Snapshots = make(map[string]desiredstate.DeploySnapshot, len(current.Snapshots))
		for key, value := range current.Snapshots {
			cloned.Snapshots[key] = value
		}
	}
	if current.Releases != nil {
		cloned.Releases = make(map[string]corerelease.Release, len(current.Releases))
		for key, value := range current.Releases {
			cloned.Releases[key] = value
		}
	}
	if current.Current != nil {
		cloned.Current = make(map[string]string, len(current.Current))
		for key, value := range current.Current {
			cloned.Current[key] = value
		}
	}
	if current.Deployments != nil {
		cloned.Deployments = make(map[string]corerelease.Deployment, len(current.Deployments))
		for key, value := range current.Deployments {
			cloned.Deployments[key] = value
		}
	}
	if current.Secrets != nil {
		cloned.Secrets = make(map[string]solo.SecretRecord, len(current.Secrets))
		for key, value := range current.Secrets {
			cloned.Secrets[key] = value
		}
	}
	if current.IngressChecks != nil {
		cloned.IngressChecks = make(map[string]solo.IngressCheckRecord, len(current.IngressChecks))
		for key, value := range current.IngressChecks {
			cloned.IngressChecks[key] = value
		}
	}
	return cloned
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

func (a *App) soloCurrentWorkspaceRoot() (string, error) {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return "", err
	}
	return discovered.WorkspaceRoot, nil
}

func soloRuntimeEnvironmentNameForNode(current solo.State, workspaceRoot, logicalEnvironment, _ string) (string, error) {
	logicalEnvironment = strings.TrimSpace(logicalEnvironment)
	if logicalEnvironment == "" {
		logicalEnvironment = config.DefaultEnvironment
	}
	currentKey, err := solo.EnvironmentStateKey(workspaceRoot, logicalEnvironment)
	if err != nil {
		return "", err
	}
	currentSnapshot, ok := current.Snapshots[currentKey]
	if !ok {
		return logicalEnvironment, nil
	}
	base := defaultSoloSnapshotEnvironment(currentSnapshot, logicalEnvironment)
	return uniqueSoloRuntimeEnvironmentName(currentSnapshot, base), nil
}

func defaultSoloSnapshotEnvironment(snapshot desiredstate.DeploySnapshot, fallback string) string {
	if value := strings.TrimSpace(snapshot.Environment); value != "" {
		return value
	}
	if value := strings.TrimSpace(fallback); value != "" {
		return value
	}
	return config.DefaultEnvironment
}

func uniqueSoloRuntimeEnvironmentName(snapshot desiredstate.DeploySnapshot, base string) string {
	hashSource := strings.TrimSpace(snapshot.WorkspaceKey)
	if hashSource == "" {
		hashSource = strings.TrimSpace(snapshot.WorkspaceRoot)
	}
	sum := sha256.Sum256([]byte(hashSource))
	suffix := hex.EncodeToString(sum[:4])
	project := soloRuntimeEnvironmentToken(snapshot.Metadata.Project)
	if project == "" {
		return base + "-" + suffix
	}
	return project + "-" + base + "-" + suffix
}

func soloRuntimeEnvironmentToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func soloDisplayEnvironmentID(workspaceRoot, environment string) (string, error) {
	key, err := solo.EnvironmentStateKey(workspaceRoot, environment)
	if err != nil {
		return "", err
	}
	return strings.Replace(key, "\n", "#", 1), nil
}

func (a *App) soloEnvironmentCreate(opts EnvironmentCreateOptions) error {
	environmentName := strings.TrimSpace(opts.Name)
	cfg, workspaceRoot, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	created := false
	if environmentName != soloEnvironmentName(cfg, "") {
		if cfg.Environments == nil {
			cfg.Environments = map[string]config.EnvironmentOverlay{}
		}
		if _, ok := cfg.Environments[environmentName]; !ok {
			cfg.Environments[environmentName] = config.EnvironmentOverlay{}
			if _, err := a.ConfigStore.Write(workspaceRoot, *cfg); err != nil {
				return err
			}
			created = true
		}
	}
	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"ok":             true,
		"mode":           string(ModeSolo),
		"environment":    map[string]any{"name": environmentName},
		"created":        created,
		"config_path":    a.ConfigStore.PathFor(workspaceRoot),
	})
}

func (a *App) soloEnvironmentUse(opts EnvironmentUseOptions) error {
	environmentName := strings.TrimSpace(opts.Name)
	cfg, _, err := a.loadSoloProjectConfig()
	if err != nil {
		return err
	}
	if environmentName != soloEnvironmentName(cfg, "") {
		if _, ok := cfg.Environments[environmentName]; !ok {
			return ExitError{Code: 2, Err: fmt.Errorf("environment %q is not defined in devopsellence.yml; run `devopsellence context env create %s` or add environments.%s", environmentName, environmentName, environmentName)}
		}
	}
	if err := a.SetEnvironment(environmentName); err != nil {
		return wrapError(err)
	}
	return a.Printer.PrintJSON(map[string]any{
		"schema_version":      outputSchemaVersion,
		"ok":                  true,
		"mode":                string(ModeSolo),
		"environment":         map[string]any{"name": environmentName},
		"workspace_key":       a.modeWorkspaceKey(),
		"default_environment": cfg.DefaultEnvironment,
	})
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
		} else if resolved, _, _, resolveErr := a.loadResolvedSoloProjectConfig(""); resolveErr == nil {
			cfg = resolved
		}
		return nodes, cfg, nil
	}
	cfg, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig("")
	if err != nil {
		return nil, nil, err
	}
	nodeNames, err := current.AttachedNodeNames(workspaceRoot, environmentName)
	if err != nil {
		return nil, nil, err
	}
	if len(nodeNames) == 0 {
		return map[string]config.Node{}, cfg, nil
	}
	nodes, err := a.resolveNodes(current, nodeNames)
	return nodes, cfg, err
}

func (a *App) soloVerifiedPublicURLs(cfg *config.ProjectConfig, nodes map[string]config.Node) []string {
	if !ingressRequiresTLSReadiness(cfg) {
		return soloReadyPublicURLs(cfg, nodes)
	}
	current, err := a.readSoloState()
	if err != nil {
		return nil
	}
	_, workspaceRoot, environmentName, err := a.loadResolvedSoloProjectConfig("")
	if err != nil {
		return nil
	}
	return soloVerifiedIngressPublicURLs(current, workspaceRoot, environmentName, cfg, nodes)
}

func soloVerifiedIngressPublicURLs(current solo.State, workspaceRoot, environmentName string, cfg *config.ProjectConfig, nodes map[string]config.Node) []string {
	key, err := solo.EnvironmentStateKey(workspaceRoot, environmentName)
	if err != nil {
		return nil
	}
	record, ok := current.IngressChecks[key]
	if !ok || !record.OK {
		return nil
	}
	urls := soloStatusPublicURLs(cfg, nodes)
	if len(urls) == 0 || !sameStringSet(record.PublicURLs, urls) {
		return nil
	}
	expectedIPs := webNodeIPs(cfg, nodes)
	if len(expectedIPs) > 0 && !sameStringSet(record.ExpectedIPs, expectedIPs) {
		return nil
	}
	return urls
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
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

func soloAffectedNodesForNodeWithReleaseState(current solo.State, nodeName string) []string {
	affected := []string{}
	for _, key := range current.AttachmentKeysForNode(nodeName) {
		attachment := current.Attachments[key]
		if !soloAttachmentHasReleaseState(current, attachment) {
			continue
		}
		affected = append(affected, attachment.NodeNames...)
	}
	return normalizeSoloNames(affected)
}

func soloDefaultProjectConfig(discovered discovery.Result) *config.ProjectConfig {
	cfg := config.DefaultProjectConfig("solo", discovered.ProjectName, config.DefaultEnvironment)
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
	PublicURLs    []string               `json:"public_urls,omitempty"`
	ExpectedIPs   []string               `json:"expected_ips"`
	Hosts         []ingressDNSHostResult `json:"hosts"`
	Hints         []ingressHint          `json:"hints,omitempty"`
	NextSteps     []string               `json:"next_steps,omitempty"`
}

type ingressDNSHostResult struct {
	Host     string   `json:"host"`
	OK       bool     `json:"ok"`
	Resolved []string `json:"resolved,omitempty"`
	Missing  []string `json:"missing,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type ingressHint struct {
	Code            string            `json:"code"`
	Severity        string            `json:"severity"`
	Message         string            `json:"message"`
	SuggestedAction ingressHintAction `json:"suggested_action"`
}

type ingressHintAction struct {
	Kind     string   `json:"kind"`
	Provider string   `json:"provider"`
	Hostname string   `json:"hostname"`
	Command  string   `json:"command"`
	Risks    []string `json:"risks"`
}

type ingressDNSReadinessError struct {
	report  ingressDNSReportResult
	message string
}

func (e ingressDNSReadinessError) Error() string {
	return e.message
}

func (e ingressDNSReadinessError) ErrorFields() map[string]any {
	fields := map[string]any{
		"kind":         "ingress_dns_not_ready",
		"ok":           e.report.OK,
		"expected_ips": e.report.ExpectedIPs,
	}
	if len(e.report.Hosts) > 0 {
		fields["hosts"] = e.report.Hosts
	}
	if len(e.report.Hints) > 0 {
		fields["hints"] = e.report.Hints
	}
	if len(e.report.NextSteps) > 0 {
		fields["next_steps"] = e.report.NextSteps
	}
	return fields
}

const soloStatusMissingSentinel = "__DEVOPSELLENCE_STATUS_MISSING__"

func (a *App) checkIngressBeforeDeploy(ctx context.Context, cfg *config.ProjectConfig, nodes map[string]config.Node, skip bool) error {
	if skip || cfg == nil || cfg.Ingress == nil || !strings.EqualFold(strings.TrimSpace(cfg.Ingress.TLS.Mode), "auto") {
		return nil
	}
	report, err := ingressDNSReport(ctx, cfg, nodes)
	if err != nil {
		return err
	}
	if report.OK {
		return nil
	}
	reportErr := ingressDNSReportError(report)
	if len(report.Hosts) == 0 {
		message := fmt.Sprintf("%s; configure ingress hostnames or pass --skip-dns-check", reportErr)
		return ingressDNSReadinessError{report: report, message: message}
	}

	message := fmt.Sprintf("%s; update DNS or pass --skip-dns-check", reportErr)
	return ingressDNSReadinessError{report: report, message: message}
}

func ingressDNSReportRetryable(report ingressDNSReportResult) bool {
	return !report.OK && len(report.Hosts) > 0
}

func ingressDNSReportError(report ingressDNSReportResult) error {
	if len(report.Hosts) == 0 {
		return fmt.Errorf("no ingress hostnames configured")
	}
	return fmt.Errorf("ingress DNS is not ready")
}

func ingressDNSReport(ctx context.Context, cfg *config.ProjectConfig, selected map[string]config.Node) (ingressDNSReportResult, error) {
	hosts := concreteIngressHosts(cfg)
	expected := webNodeIPs(cfg, selected)
	if len(expected) == 0 {
		return ingressDNSReportResult{}, fmt.Errorf("no web nodes configured")
	}
	report := ingressDNSReportResult{
		SchemaVersion: outputSchemaVersion,
		OK:            true,
		PublicURLs:    ingressConfiguredPublicURLs(cfg),
		ExpectedIPs:   expected,
		Hosts:         make([]ingressDNSHostResult, 0, len(hosts)),
	}
	if len(hosts) == 0 {
		report.OK = false
		report.Hints = temporaryDNSHints(cfg, expected)
		report.NextSteps = []string{"devopsellence status", "devopsellence ingress set --host <hostname> --service <service>", "devopsellence ingress check --wait 5m"}
		return report, nil
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
	if len(report.PublicURLs) > 0 {
		if report.OK {
			report.NextSteps = []string{"devopsellence status", "curl " + report.PublicURLs[0]}
		} else {
			report.NextSteps = []string{"devopsellence status", "update DNS records to point at expected_ips", "devopsellence ingress check --wait 5m"}
		}
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

func temporaryDNSHints(cfg *config.ProjectConfig, expectedIPs []string) []ingressHint {
	if len(expectedIPs) != 1 {
		return nil
	}
	hints := []ingressHint{}
	for _, ip := range expectedIPs {
		if !isTemporaryDNSIPv4(ip) {
			continue
		}
		hostname := temporaryDNSHostname(cfg, ip)
		hints = append(hints, ingressHint{
			Code:     "solo_ingress_no_hostname",
			Severity: "suggestion",
			Message:  "No concrete ingress hostname is configured. For day-0 ingress, an operator agent can use a temporary sslip.io hostname that points at this node IP.",
			SuggestedAction: ingressHintAction{
				Kind:     "use_temporary_dns_hostname",
				Provider: "sslip.io",
				Hostname: hostname,
				Command:  temporaryDNSCommand(cfg, hostname),
				Risks: []string{
					"third_party_dns_dependency",
					"breaks_if_public_ip_changes",
					"not_recommended_as_long_term_production_domain",
				},
			},
		})
	}
	return hints
}

func temporaryDNSHostname(_ *config.ProjectConfig, ip string) string {
	return strings.TrimSpace(ip) + ".sslip.io"
}

func temporaryDNSCommand(cfg *config.ProjectConfig, hostname string) string {
	return "devopsellence ingress set --host " + shellQuote(hostname) + " --tls-mode " + shellQuote(temporaryDNSTLSMode(cfg))
}

func temporaryDNSTLSMode(cfg *config.ProjectConfig) string {
	if cfg != nil && cfg.Ingress != nil {
		mode := strings.ToLower(strings.TrimSpace(cfg.Ingress.TLS.Mode))
		switch mode {
		case "auto", "manual", "off":
			return mode
		}
	}
	return "auto"
}

func isTemporaryDNSIPv4(value string) bool {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return false
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	if !ip4.IsGlobalUnicast() || ip4.Equal(net.IPv4bcast) {
		return false
	}
	return !isSpecialUseIPv4(ip4)
}

func isSpecialUseIPv4(ip net.IP) bool {
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return true
	}
	switch {
	case ip4[0] == 0:
		return true
	case ip4[0] == 100 && ip4[1]&0xc0 == 64:
		return true
	case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0:
		return true
	case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2:
		return true
	case ip4[0] == 192 && ip4[1] == 88 && ip4[2] == 99:
		return true
	case ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19):
		return true
	case ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100:
		return true
	case ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113:
		return true
	case ip4[0] >= 240:
		return true
	default:
		return false
	}
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

func waitForSoloProviderServer(ctx context.Context, provider providers.Provider, server providers.Server, progress func(string)) (providers.Server, error) {
	deadline := time.Now().Add(3 * time.Minute)
	lastStatus := firstNonEmpty(server.Status, "unknown")
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
		status := firstNonEmpty(server.Status, "unknown")
		if progress != nil && status != lastStatus {
			progress(fmt.Sprintf("Provider server %s status: %s", server.ID, status))
		}
		lastStatus = status
	}
}

func waitForSoloSSH(ctx context.Context, node config.Node, timeout time.Duration) error {
	return waitForSoloSSHWithProbe(ctx, node, timeout, 10*time.Second, 2*time.Second, func(ctx context.Context) error {
		_, err := solo.RunSSH(ctx, node, "true", nil)
		return err
	})
}

func validateSoloNodeSSH(ctx context.Context, node config.Node) error {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := solo.RunSSH(probeCtx, node, "true", nil)
	return err
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

func soloReleaseID(revision string, now time.Time) string {
	revision = strings.TrimSpace(revision)
	if revision == "" {
		revision = "unknown"
	}
	return fmt.Sprintf("rel_%s_%s", sanitizeSoloIDPart(revision), soloULID(now))
}

func soloDeploymentID(kind, revision string, now time.Time) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = corerelease.DeploymentKindDeploy
	}
	revision = strings.TrimSpace(revision)
	if revision == "" {
		revision = "unknown"
	}
	return fmt.Sprintf("dep_%s_%s_%s", sanitizeSoloIDPart(kind), sanitizeSoloIDPart(revision), soloULID(now))
}

func soloULID(now time.Time) string {
	soloULIDMu.Lock()
	defer soloULIDMu.Unlock()
	id, err := ulid.New(ulid.Timestamp(now.UTC()), soloULIDEntropy)
	if err != nil {
		return ulid.Make().String()
	}
	return id.String()
}

func sanitizeSoloIDPart(value string) string {
	value = strings.TrimSpace(value)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

func nextSoloDeploymentSequence(current solo.State, environmentID string) int {
	sequence := 0
	for _, deployment := range current.Deployments {
		if deployment.EnvironmentID == environmentID && deployment.Sequence > sequence {
			sequence = deployment.Sequence
		}
	}
	return sequence + 1
}

func soloDeploymentPublicationResult(revisions map[string]string, publishErr error) *corerelease.DeploymentPublicationResult {
	result := &corerelease.DeploymentPublicationResult{
		Status: corerelease.PublicationStatusWritten,
	}
	if publishErr != nil {
		result.Status = corerelease.PublicationStatusFailed
		result.ErrorMessage = publishErr.Error()
	}
	for _, nodeName := range sortedMapKeys(revisions) {
		result.NodeResults = append(result.NodeResults, corerelease.DesiredStatePublication{
			NodeName: nodeName,
			Revision: revisions[nodeName],
			Status:   corerelease.PublicationStatusWritten,
		})
	}
	return result
}

func (a *App) persistSoloDeploymentFailure(current solo.State, deployment corerelease.Deployment, revisions map[string]string, failure error) error {
	deployment.Status = corerelease.DeploymentStatusFailed
	deployment.StatusMessage = "deployment failed"
	deployment.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	deployment.PublicationResult = soloDeploymentPublicationResult(revisions, failure)
	if err := current.SaveDeployment(deployment); err != nil {
		return err
	}
	return a.writeSoloState(current)
}

func (a *App) persistSoloDeploymentRolloutFailure(current solo.State, deployment corerelease.Deployment, revisions map[string]string, failure error) error {
	deployment.Status = corerelease.DeploymentStatusFailed
	deployment.StatusMessage = "rollout failed: " + failure.Error()
	deployment.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	deployment.PublicationResult = soloDeploymentPublicationResult(revisions, nil)
	if err := current.SaveDeployment(deployment); err != nil {
		return err
	}
	return a.writeSoloState(current)
}

func soloRollbackTargetNodeNames(attachedNodeNames []string, release corerelease.Release) ([]string, error) {
	normalizedAttached := make([]string, 0, len(attachedNodeNames))
	attached := make(map[string]bool, len(attachedNodeNames))
	for _, nodeName := range attachedNodeNames {
		nodeName = strings.TrimSpace(nodeName)
		if nodeName != "" && !attached[nodeName] {
			normalizedAttached = append(normalizedAttached, nodeName)
			attached[nodeName] = true
		}
	}
	normalizedTargets := make([]string, 0, len(release.TargetNodeIDs))
	targeted := make(map[string]bool, len(release.TargetNodeIDs))
	for _, nodeName := range release.TargetNodeIDs {
		nodeName = strings.TrimSpace(nodeName)
		if nodeName != "" && !targeted[nodeName] {
			normalizedTargets = append(normalizedTargets, nodeName)
			targeted[nodeName] = true
		}
	}
	if len(normalizedTargets) == 0 {
		if len(normalizedAttached) == 0 {
			return nil, fmt.Errorf("selected rollback release %s does not target any currently attached nodes", release.Revision)
		}
		return normalizedAttached, nil
	}
	targets := make([]string, 0, len(normalizedTargets))
	for _, nodeName := range normalizedTargets {
		if attached[nodeName] {
			targets = append(targets, nodeName)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("selected rollback release %s does not target any currently attached nodes", release.Revision)
	}
	return targets, nil
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

func remoteDockerLogsCommand(environmentName string, serviceName string, lines int) string {
	environmentName = strings.TrimSpace(environmentName)
	if environmentName == "" {
		environmentName = config.DefaultEnvironment
	}
	environment := shellQuote(environmentName)
	service := shellQuote(serviceName)
	return fmt.Sprintf(`if docker info >/dev/null 2>&1; then docker_cmd=docker; elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then docker_cmd="sudo -n docker"; else echo 'Docker is not reachable' >&2; exit 1; fi
environment=%s
service=%s
ids=$($docker_cmd ps -a -q --filter label=devopsellence.managed=true --filter "label=devopsellence.environment=$environment" --filter "label=devopsellence.service=$service" 2>&1)
ps_status=$?
if [ "$ps_status" -ne 0 ]; then
  echo "Failed to list workload containers for service $service in environment $environment" >&2
  if [ -n "$ids" ]; then
    printf '%%s\n' "$ids" >&2
  fi
  exit "$ps_status"
fi
ids=$(printf '%%s\n' "$ids" | sed '/^$/d' | head -n %d)
if [ -z "$ids" ]; then echo "%s" >&2; echo "No workload containers found for service $service in environment $environment" >&2; exit 1; fi
rc=0
for id in $ids; do
  name=$($docker_cmd inspect --format '{{.Name}}' "$id" 2>/dev/null | sed 's#^/##')
  inspect_status=$?
  if [ "$inspect_status" -ne 0 ]; then
    rc=$inspect_status
    name="$id"
  fi
  echo "==> $name <=="
  $docker_cmd logs --tail %d "$id" 2>&1
  logs_status=$?
  if [ "$logs_status" -ne 0 ]; then
    rc=$logs_status
  fi
done
	exit "$rc"`, environment, service, soloWorkloadLogsContainerLimit, soloNoWorkloadContainersSentinel, lines)
}

func remoteUserCommand(args []string) (string, error) {
	if len(args) == 0 {
		return "", ExitError{Code: 2, Err: errors.New("missing command after --")}
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " "), nil
}

func remoteDockerExecCommand(container string, args []string) string {
	command, err := remoteUserCommand(args)
	if err != nil {
		return fmt.Sprintf("echo %s >&2; exit %d", shellQuote(err.Error()), remoteCommandExitCode(err))
	}
	return fmt.Sprintf("if docker info >/dev/null 2>&1; then docker_cmd=docker; elif command -v sudo >/dev/null 2>&1 && sudo -n docker info >/dev/null 2>&1; then docker_cmd=\"sudo -n docker\"; else echo 'Docker is not reachable' >&2; exit 1; fi\nexec $docker_cmd exec %s %s", shellQuote(container), command)
}

func remoteCommandExitCode(err error) int {
	var exitErr ExitError
	if errors.As(err, &exitErr) && exitErr.Code != 0 {
		return exitErr.Code
	}
	return 1
}

func remoteExecWrapper(command, exitMarker string) string {
	return fmt.Sprintf("(%s); rc=$?; printf '\\n%s%%s\\n' \"$rc\" >&2; exit 0", command, exitMarker)
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

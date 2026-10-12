package desktop

import (
	"sort"
	"strings"

	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
)

const apiSchemaVersion = 1

type SummaryOptions struct {
	WorkspaceRoot string
	StatePath     string
}

type Summary struct {
	SchemaVersion int                 `json:"schema_version"`
	Workspace     WorkspaceSummary    `json:"workspace"`
	Project       *ProjectSummary     `json:"project,omitempty"`
	State         SoloStateSummary    `json:"state"`
	Nodes         []NodeSummary       `json:"nodes"`
	Attachments   []AttachmentSummary `json:"attachments"`
	Releases      []ReleaseSummary    `json:"releases"`
	Secrets       []SecretSummary     `json:"secrets"`
	NextSteps     []NextStep          `json:"next_steps,omitempty"`
}

type WorkspaceSummary struct {
	Root string `json:"root"`
	Key  string `json:"key"`
	Mode string `json:"mode"`
}

type ProjectSummary struct {
	Organization       string   `json:"organization"`
	Project            string   `json:"project"`
	DefaultEnvironment string   `json:"default_environment"`
	Services           []string `json:"services"`
	Environments       []string `json:"environments,omitempty"`
}

type SoloStateSummary struct {
	Path              string `json:"path"`
	NodeCount         int    `json:"node_count"`
	AttachmentCount   int    `json:"attachment_count"`
	ReleaseCount      int    `json:"release_count"`
	SecretRefCount    int    `json:"secret_ref_count"`
	CurrentReleaseID  string `json:"current_release_id,omitempty"`
	CurrentRevision   string `json:"current_revision,omitempty"`
	CurrentDeployment string `json:"current_deployment_id,omitempty"`
}

type NodeSummary struct {
	Name     string   `json:"name"`
	Host     string   `json:"host"`
	User     string   `json:"user,omitempty"`
	Port     int      `json:"port,omitempty"`
	Provider string   `json:"provider,omitempty"`
	Region   string   `json:"region,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Attached bool     `json:"attached"`
}

type AttachmentSummary struct {
	Environment string   `json:"environment"`
	NodeNames   []string `json:"node_names"`
}

type ReleaseSummary struct {
	ID          string   `json:"id"`
	Environment string   `json:"environment"`
	Revision    string   `json:"revision"`
	Image       string   `json:"image,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	Current     bool     `json:"current"`
	NodeNames   []string `json:"node_names,omitempty"`
}

type SecretSummary struct {
	Environment string `json:"environment"`
	ServiceName string `json:"service_name"`
	Name        string `json:"name"`
	Store       string `json:"store,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type NextStep struct {
	Label   string `json:"label"`
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

func BuildSummary(opts SummaryOptions) (Summary, error) {
	workspaceRoot := strings.TrimSpace(opts.WorkspaceRoot)
	workspaceKey, err := solo.CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return Summary{}, err
	}
	statePath := strings.TrimSpace(opts.StatePath)
	if statePath == "" {
		statePath = solo.DefaultStatePath()
	}
	store := solo.NewStateStore(statePath)
	current, err := store.Read()
	if err != nil {
		return Summary{}, err
	}

	cfg, err := config.LoadFromRoot(workspaceRoot)
	if err != nil {
		return Summary{}, err
	}

	summary := Summary{
		SchemaVersion: apiSchemaVersion,
		Workspace: WorkspaceSummary{
			Root: workspaceRoot,
			Key:  workspaceKey,
			Mode: "solo",
		},
		State: SoloStateSummary{Path: statePath},
	}
	if cfg != nil {
		summary.Project = projectSummary(*cfg)
	}

	attachmentNodeNames := map[string]bool{}
	currentReleaseID := ""
	currentDeploymentID := ""
	for key, attachment := range current.Attachments {
		if attachment.WorkspaceKey != workspaceKey {
			continue
		}
		nodeNames := append([]string(nil), attachment.NodeNames...)
		sort.Strings(nodeNames)
		summary.Attachments = append(summary.Attachments, AttachmentSummary{
			Environment: attachment.Environment,
			NodeNames:   nodeNames,
		})
		for _, nodeName := range nodeNames {
			attachmentNodeNames[nodeName] = true
		}
		if id := strings.TrimSpace(current.Current[key]); id != "" && currentReleaseID == "" {
			currentReleaseID = id
		}
	}
	sort.Slice(summary.Attachments, func(i, j int) bool {
		return summary.Attachments[i].Environment < summary.Attachments[j].Environment
	})

	nodeNames := make([]string, 0, len(current.Nodes))
	for name := range current.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)
	for _, name := range nodeNames {
		node := current.Nodes[name]
		summary.Nodes = append(summary.Nodes, NodeSummary{
			Name:     name,
			Host:     node.Host,
			User:     node.User,
			Port:     node.Port,
			Provider: node.Provider,
			Region:   node.ProviderRegion,
			Labels:   append([]string(nil), node.Labels...),
			Attached: attachmentNodeNames[name],
		})
	}

	releaseIDs := make([]string, 0, len(current.Releases))
	for id, release := range current.Releases {
		if release.Snapshot.WorkspaceKey != workspaceKey {
			continue
		}
		releaseIDs = append(releaseIDs, id)
	}
	sort.Slice(releaseIDs, func(i, j int) bool {
		return current.Releases[releaseIDs[i]].CreatedAt > current.Releases[releaseIDs[j]].CreatedAt
	})
	for _, id := range releaseIDs {
		release := current.Releases[id]
		image := release.Image.Reference
		if image == "" {
			image = release.Snapshot.Image
		}
		nodeNames := make([]string, 0, len(release.TargetNodeIDs))
		for _, nodeName := range release.TargetNodeIDs {
			nodeNames = append(nodeNames, nodeName)
		}
		sort.Strings(nodeNames)
		summary.Releases = append(summary.Releases, ReleaseSummary{
			ID:          id,
			Environment: release.Snapshot.Environment,
			Revision:    release.Revision,
			Image:       image,
			CreatedAt:   release.CreatedAt,
			Current:     id == currentReleaseID,
			NodeNames:   nodeNames,
		})
		if id == currentReleaseID {
			summary.State.CurrentRevision = release.Revision
		}
	}

	deploymentIDs := make([]string, 0, len(current.Deployments))
	for id, deployment := range current.Deployments {
		if deployment.ReleaseID == currentReleaseID {
			deploymentIDs = append(deploymentIDs, id)
		}
	}
	sort.Slice(deploymentIDs, func(i, j int) bool {
		return current.Deployments[deploymentIDs[i]].CreatedAt > current.Deployments[deploymentIDs[j]].CreatedAt
	})
	if len(deploymentIDs) > 0 {
		currentDeploymentID = deploymentIDs[0]
	}

	secretKeys := make([]string, 0, len(current.Secrets))
	for key, secret := range current.Secrets {
		if secret.WorkspaceKey != workspaceKey {
			continue
		}
		secretKeys = append(secretKeys, key)
	}
	sort.Slice(secretKeys, func(i, j int) bool {
		left := current.Secrets[secretKeys[i]]
		right := current.Secrets[secretKeys[j]]
		return strings.Join([]string{left.Environment, left.ServiceName, left.Name}, "\x00") < strings.Join([]string{right.Environment, right.ServiceName, right.Name}, "\x00")
	})
	for _, key := range secretKeys {
		secret := current.Secrets[key]
		summary.Secrets = append(summary.Secrets, SecretSummary{
			Environment: secret.Environment,
			ServiceName: secret.ServiceName,
			Name:        secret.Name,
			Store:       secret.Store,
			UpdatedAt:   secret.UpdatedAt,
		})
	}

	summary.State.NodeCount = len(summary.Nodes)
	summary.State.AttachmentCount = len(summary.Attachments)
	summary.State.ReleaseCount = len(summary.Releases)
	summary.State.SecretRefCount = len(summary.Secrets)
	summary.State.CurrentReleaseID = currentReleaseID
	summary.State.CurrentDeployment = currentDeploymentID
	summary.NextSteps = nextSteps(summary)
	return summary, nil
}

func projectSummary(cfg config.ProjectConfig) *ProjectSummary {
	services := cfg.ServiceNames()
	environments := make([]string, 0, len(cfg.Environments))
	for name := range cfg.Environments {
		environments = append(environments, name)
	}
	sort.Strings(environments)
	return &ProjectSummary{
		Organization:       cfg.Organization,
		Project:            cfg.Project,
		DefaultEnvironment: cfg.DefaultEnvironment,
		Services:           services,
		Environments:       environments,
	}
}

func nextSteps(summary Summary) []NextStep {
	steps := []NextStep{}
	if summary.Project == nil {
		steps = append(steps, NextStep{Label: "Initialize solo workspace", Command: "devopsellence init --mode solo", Reason: "No devopsellence.yml was found in this workspace."})
	}
	if summary.State.NodeCount == 0 {
		steps = append(steps, NextStep{Label: "Register a node", Command: "devopsellence node create prod-1 --host <ip> --user root --ssh-key ~/.ssh/id_ed25519", Reason: "Solo mode needs at least one SSH node."})
	}
	if summary.State.NodeCount > 0 && summary.State.AttachmentCount == 0 {
		steps = append(steps, NextStep{Label: "Attach a node", Command: "devopsellence node attach <node>", Reason: "A node exists but this workspace has no environment attachment."})
	}
	if summary.Project != nil && summary.State.AttachmentCount > 0 && summary.State.ReleaseCount == 0 {
		steps = append(steps, NextStep{Label: "Deploy", Command: "devopsellence deploy", Reason: "The workspace is configured and attached but has no release history yet."})
	}
	if summary.State.ReleaseCount > 0 {
		steps = append(steps, NextStep{Label: "Check status", Command: "devopsellence status", Reason: "Verify the latest release has settled on attached nodes."})
	}
	return steps
}

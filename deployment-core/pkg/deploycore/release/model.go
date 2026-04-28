package release

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
)

const (
	DeploymentKindDeploy     = "deploy"
	DeploymentKindRollback   = "rollback"
	DeploymentKindRepublish  = "republish"
	DeploymentStatusPending  = "pending"
	DeploymentStatusRunning  = "running"
	DeploymentStatusSettled  = "settled"
	DeploymentStatusFailed   = "failed"
	PublicationStatusPending = "pending"
	PublicationStatusWritten = "written"
	PublicationStatusFailed  = "failed"
)

type Environment struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	CurrentReleaseID string   `json:"current_release_id,omitempty"`
	NodeIDs          []string `json:"node_ids,omitempty"`
}

type Node struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Config config.Node `json:"config"`
}

type Release struct {
	ID            string                      `json:"id"`
	EnvironmentID string                      `json:"environment_id"`
	Revision      string                      `json:"revision"`
	ConfigDigest  string                      `json:"config_digest,omitempty"`
	Snapshot      desiredstate.DeploySnapshot `json:"snapshot"`
	Image         ImageRef                    `json:"image"`
	TargetNodeIDs []string                    `json:"target_node_ids,omitempty"`
	CreatedAt     string                      `json:"created_at"`
	CommandResult *OperationResult            `json:"command_result,omitempty"`
}

type ImageRef struct {
	Repository string `json:"repository,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Reference  string `json:"reference"`
}

type Deployment struct {
	ID                string                       `json:"id"`
	EnvironmentID     string                       `json:"environment_id"`
	ReleaseID         string                       `json:"release_id"`
	Kind              string                       `json:"kind"`
	Sequence          int                          `json:"sequence"`
	TargetNodeIDs     []string                     `json:"target_node_ids,omitempty"`
	Status            string                       `json:"status"`
	StatusMessage     string                       `json:"status_message,omitempty"`
	CreatedAt         string                       `json:"created_at"`
	FinishedAt        string                       `json:"finished_at,omitempty"`
	CommandResult     *OperationResult             `json:"command_result,omitempty"`
	PublicationResult *DeploymentPublicationResult `json:"publication_result,omitempty"`
}

type DeploymentPublicationResult struct {
	Status       string                    `json:"status"`
	NodeResults  []DesiredStatePublication `json:"node_results,omitempty"`
	ErrorMessage string                    `json:"error_message,omitempty"`
}

type DesiredStatePublication struct {
	NodeID      string `json:"node_id"`
	NodeName    string `json:"node_name"`
	Revision    string `json:"revision"`
	Sequence    int    `json:"sequence"`
	Status      string `json:"status"`
	URI         string `json:"uri,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Error       string `json:"error,omitempty"`
}

type OperationResult struct {
	Status      string `json:"status"`
	ExitCode    int    `json:"exit_code,omitempty"`
	Message     string `json:"message,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type ReleaseCreateInput struct {
	ID            string
	EnvironmentID string
	Revision      string
	ConfigDigest  string
	Snapshot      desiredstate.DeploySnapshot
	Image         ImageRef
	TargetNodeIDs []string
	CreatedAt     time.Time
	CommandResult *OperationResult
}

func NewRelease(input ReleaseCreateInput) (Release, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return Release{}, errors.New("release id is required")
	}
	environmentID := strings.TrimSpace(input.EnvironmentID)
	if environmentID == "" {
		return Release{}, errors.New("environment id is required")
	}
	revision := strings.TrimSpace(input.Revision)
	if revision == "" {
		revision = strings.TrimSpace(input.Snapshot.Revision)
	}
	if revision == "" {
		return Release{}, errors.New("release revision is required")
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	snapshot, err := cloneDeploySnapshot(input.Snapshot)
	if err != nil {
		return Release{}, fmt.Errorf("clone release snapshot: %w", err)
	}
	snapshot.Revision = revision
	targets := normalizeStrings(input.TargetNodeIDs)
	return Release{
		ID:            id,
		EnvironmentID: environmentID,
		Revision:      revision,
		ConfigDigest:  strings.TrimSpace(input.ConfigDigest),
		Snapshot:      snapshot,
		Image:         normalizeImage(input.Image, snapshot.Image),
		TargetNodeIDs: targets,
		CreatedAt:     createdAt.UTC().Format(time.RFC3339Nano),
		CommandResult: input.CommandResult,
	}, nil
}

type DeploymentCreateInput struct {
	ID            string
	EnvironmentID string
	ReleaseID     string
	Kind          string
	Sequence      int
	TargetNodeIDs []string
	CreatedAt     time.Time
}

func NewDeployment(input DeploymentCreateInput) (Deployment, error) {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		return Deployment{}, errors.New("deployment id is required")
	}
	environmentID := strings.TrimSpace(input.EnvironmentID)
	if environmentID == "" {
		return Deployment{}, errors.New("environment id is required")
	}
	releaseID := strings.TrimSpace(input.ReleaseID)
	if releaseID == "" {
		return Deployment{}, errors.New("release id is required")
	}
	kind := strings.TrimSpace(input.Kind)
	if kind == "" {
		kind = DeploymentKindDeploy
	}
	switch kind {
	case DeploymentKindDeploy, DeploymentKindRollback, DeploymentKindRepublish:
	default:
		return Deployment{}, fmt.Errorf("unsupported deployment kind %q", kind)
	}
	if input.Sequence <= 0 {
		return Deployment{}, errors.New("deployment sequence must be greater than zero")
	}
	createdAt := input.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return Deployment{
		ID:            id,
		EnvironmentID: environmentID,
		ReleaseID:     releaseID,
		Kind:          kind,
		Sequence:      input.Sequence,
		TargetNodeIDs: normalizeStrings(input.TargetNodeIDs),
		Status:        DeploymentStatusPending,
		StatusMessage: "waiting to publish desired state",
		CreatedAt:     createdAt.UTC().Format(time.RFC3339),
	}, nil
}

func SelectRollbackRelease(releases []Release, currentReleaseID, selector string) (Release, error) {
	currentReleaseID = strings.TrimSpace(currentReleaseID)
	selector = strings.TrimSpace(selector)
	candidates := append([]Release(nil), releases...)

	if selector == "" {
		if currentReleaseID == "" {
			return Release{}, errors.New("current release is required")
		}
		createdAtByID := make(map[string]time.Time, len(candidates))
		for _, candidate := range candidates {
			createdAt, err := parseReleaseCreatedAt(candidate.CreatedAt)
			if err != nil {
				if candidate.ID != "" {
					return Release{}, fmt.Errorf("release %q has invalid created_at: %w", candidate.ID, err)
				}
				return Release{}, err
			}
			createdAtByID[candidate.ID] = createdAt
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if !createdAtByID[candidates[i].ID].Equal(createdAtByID[candidates[j].ID]) {
				return createdAtByID[candidates[i].ID].After(createdAtByID[candidates[j].ID])
			}
			if candidates[i].ID != candidates[j].ID {
				return candidates[i].ID > candidates[j].ID
			}
			return candidates[i].Revision > candidates[j].Revision
		})
		currentIndex := -1
		for i, candidate := range candidates {
			if candidate.ID == currentReleaseID {
				currentIndex = i
				break
			}
		}
		if currentIndex == -1 {
			return Release{}, fmt.Errorf("current release %q not found", currentReleaseID)
		}
		for i := currentIndex + 1; i < len(candidates); i++ {
			if candidates[i].ID != "" {
				return candidates[i], nil
			}
		}
		return Release{}, errors.New("no previous release found")
	}

	matches := []Release{}
	for _, candidate := range candidates {
		if candidate.ID == selector || candidate.Revision == selector || strings.HasPrefix(candidate.Revision, selector) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return Release{}, fmt.Errorf("release %q not found", selector)
	}
	if len(matches) > 1 {
		return Release{}, fmt.Errorf("release selector %q is ambiguous", selector)
	}
	return matches[0], nil
}

func parseReleaseCreatedAt(createdAt string) (time.Time, error) {
	createdAt = strings.TrimSpace(createdAt)
	if createdAt == "" {
		return time.Time{}, errors.New("release created_at is required")
	}
	parsedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("must be RFC3339: %w", err)
	}
	return parsedAt, nil
}

type PublicationPlanInput struct {
	NodeName     string
	Node         config.Node
	Releases     []Release
	ReleaseNodes map[string]string
	NodePeers    []desiredstate.NodePeer
}

type PublicationPlan struct {
	NodeName         string
	DesiredStateJSON []byte
	Revision         string
}

func PlanPublication(input PublicationPlanInput) (PublicationPlan, error) {
	snapshots := make([]desiredstate.DeploySnapshot, 0, len(input.Releases))
	for _, rel := range input.Releases {
		snapshots = append(snapshots, rel.Snapshot)
	}
	publication, err := desiredstate.PlanNodePublication(desiredstate.NodePublicationInput{
		NodeName:     input.NodeName,
		CurrentNode:  input.Node,
		Snapshots:    snapshots,
		ReleaseNodes: input.ReleaseNodes,
		NodePeers:    input.NodePeers,
	})
	if err != nil {
		return PublicationPlan{}, err
	}
	revision, err := desiredStateRevision(publication.DesiredStateJSON)
	if err != nil {
		return PublicationPlan{}, err
	}
	return PublicationPlan{
		NodeName:         publication.NodeName,
		DesiredStateJSON: publication.DesiredStateJSON,
		Revision:         revision,
	}, nil
}

func normalizeImage(image ImageRef, fallback string) ImageRef {
	image.Repository = strings.TrimSpace(image.Repository)
	image.Digest = strings.TrimSpace(image.Digest)
	image.Reference = strings.TrimSpace(image.Reference)
	if image.Reference == "" {
		image.Reference = strings.TrimSpace(fallback)
	}
	return image
}

func cloneDeploySnapshot(snapshot desiredstate.DeploySnapshot) (desiredstate.DeploySnapshot, error) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return desiredstate.DeploySnapshot{}, err
	}
	var cloned desiredstate.DeploySnapshot
	if err := json.Unmarshal(data, &cloned); err != nil {
		return desiredstate.DeploySnapshot{}, err
	}
	return cloned, nil
}

func normalizeStrings(values []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(values))
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

func desiredStateRevision(data []byte) (string, error) {
	var payload struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	if strings.TrimSpace(payload.Revision) == "" {
		return "", errors.New("desired state revision is missing")
	}
	return strings.TrimSpace(payload.Revision), nil
}

package release

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
)

type Service struct {
	Store Store
}

type RollbackInput struct {
	Environment  EnvironmentRef
	Selector     string
	DeploymentID string
	Sequence     int
	Now          time.Time
}

type RollbackResult struct {
	Release      Release
	Deployment   Deployment
	Publications []DesiredStatePublication
}

func (s Service) Rollback(ctx context.Context, input RollbackInput) (RollbackResult, error) {
	if s.Store == nil {
		return RollbackResult{}, errors.New("release store is required")
	}
	var result RollbackResult
	err := s.Store.WithEnvironmentLock(ctx, input.Environment, func(ctx context.Context, tx Tx) error {
		environment, err := tx.Environment(ctx, input.Environment)
		if err != nil {
			return err
		}
		selected, err := rollbackRelease(ctx, tx, environment, input.Selector)
		if err != nil {
			return err
		}
		nodes, err := tx.Nodes(ctx, environment.ID)
		if err != nil {
			return err
		}
		targetNodes := targetNodesForRelease(nodes, selected)
		targetIDs := make([]string, 0, len(targetNodes))
		for _, node := range targetNodes {
			targetIDs = append(targetIDs, node.ID)
		}
		deployment, err := NewDeployment(DeploymentCreateInput{
			ID:            input.DeploymentID,
			EnvironmentID: environment.ID,
			ReleaseID:     selected.ID,
			Kind:          DeploymentKindRollback,
			Sequence:      input.Sequence,
			TargetNodeIDs: targetIDs,
			CreatedAt:     input.Now,
		})
		if err != nil {
			return err
		}
		deployment, err = tx.CreateDeployment(ctx, deployment)
		if err != nil {
			return err
		}
		if len(targetNodes) == 0 {
			err := errors.New("no target nodes match release")
			if updateErr := failDeployment(ctx, tx, &deployment, "no target nodes match release", nil, err, input.Now); updateErr != nil {
				return errors.Join(err, updateErr)
			}
			result = RollbackResult{Release: selected, Deployment: deployment}
			return err
		}
		deployment.Status = DeploymentStatusRunning
		deployment.StatusMessage = "publishing desired state"
		if err := tx.UpdateDeployment(ctx, deployment); err != nil {
			return err
		}
		releaseNodes := releaseNodesForPublication(selected, targetNodes)
		publications := []DesiredStatePublication{}
		for _, node := range targetNodes {
			plan, err := PlanPublication(PublicationPlanInput{
				NodeName:     node.Name,
				Node:         node.Config,
				Releases:     []Release{selected},
				ReleaseNodes: releaseNodes,
				NodePeers:    nodePeersForPublication(nodes, node.Name),
			})
			if err != nil {
				failure := err
				publication := DesiredStatePublication{
					NodeID:   node.ID,
					NodeName: node.Name,
					Status:   PublicationStatusFailed,
					Error:    failure.Error(),
				}
				publications = append(publications, publication)
				if updateErr := failDeployment(ctx, tx, &deployment, "desired state publish failed", publications, failure, input.Now); updateErr != nil {
					return errors.Join(failure, updateErr)
				}
				result = RollbackResult{Release: selected, Deployment: deployment, Publications: publications}
				return failure
			}
			publication, err := tx.PublishDesiredState(ctx, node, plan)
			if err != nil {
				failure := err
				publication = DesiredStatePublication{
					NodeID:   node.ID,
					NodeName: node.Name,
					Revision: plan.Revision,
					Status:   PublicationStatusFailed,
					Error:    failure.Error(),
				}
				publications = append(publications, publication)
				if updateErr := failDeployment(ctx, tx, &deployment, "desired state publish failed", publications, failure, input.Now); updateErr != nil {
					return errors.Join(failure, updateErr)
				}
				result = RollbackResult{Release: selected, Deployment: deployment, Publications: publications}
				return failure
			}
			publications = append(publications, publication)
		}
		if err := tx.SetCurrentRelease(ctx, environment.ID, selected.ID); err != nil {
			if updateErr := failDeployment(ctx, tx, &deployment, "failed to set current release", publications, err, input.Now); updateErr != nil {
				return errors.Join(err, updateErr)
			}
			result = RollbackResult{Release: selected, Deployment: deployment, Publications: publications}
			return err
		}
		deployment.Status = DeploymentStatusSettled
		deployment.StatusMessage = "rollback published"
		deployment.FinishedAt = finishTime(input.Now)
		deployment.PublicationResult = &DeploymentPublicationResult{
			Status:      PublicationStatusWritten,
			NodeResults: publications,
		}
		if err := tx.UpdateDeployment(ctx, deployment); err != nil {
			return err
		}
		result = RollbackResult{Release: selected, Deployment: deployment, Publications: publications}
		return nil
	})
	return result, err
}

func rollbackRelease(ctx context.Context, tx Tx, environment Environment, selector string) (Release, error) {
	releases, err := tx.Releases(ctx, environment.ID, ReleaseListOptions{})
	if err != nil {
		return Release{}, err
	}
	return SelectRollbackRelease(releases, environment.CurrentReleaseID, selector)
}

func failDeployment(ctx context.Context, tx Tx, deployment *Deployment, message string, publications []DesiredStatePublication, failure error, now time.Time) error {
	deployment.Status = DeploymentStatusFailed
	deployment.StatusMessage = message
	deployment.FinishedAt = finishTime(now)
	deployment.PublicationResult = &DeploymentPublicationResult{
		Status:       PublicationStatusFailed,
		NodeResults:  publications,
		ErrorMessage: failure.Error(),
	}
	return tx.UpdateDeployment(ctx, *deployment)
}

func finishTime(now time.Time) string {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.UTC().Format(time.RFC3339)
}

func targetNodesForRelease(nodes []Node, release Release) []Node {
	if len(release.TargetNodeIDs) == 0 {
		return append([]Node(nil), nodes...)
	}
	wanted := map[string]bool{}
	for _, id := range release.TargetNodeIDs {
		wanted[id] = true
	}
	targets := []Node{}
	for _, node := range nodes {
		if wanted[node.ID] {
			targets = append(targets, node)
		}
	}
	return targets
}

func releaseNodesForPublication(release Release, targetNodes []Node) map[string]string {
	if release.Snapshot.ReleaseTask == nil {
		return nil
	}
	nodeNames := make([]string, 0, len(targetNodes))
	nodesByName := make(map[string]Node, len(targetNodes))
	for _, node := range targetNodes {
		name := strings.TrimSpace(node.Name)
		if name == "" {
			continue
		}
		nodeNames = append(nodeNames, name)
		nodesByName[name] = node
	}
	sort.Strings(nodeNames)
	for _, name := range nodeNames {
		node := nodesByName[name]
		if nodeCanRunKind(node.Config, release.Snapshot.ReleaseServiceKind) {
			return map[string]string{releaseSnapshotKey(release.Snapshot): name}
		}
	}
	return nil
}

func nodePeersForPublication(nodes []Node, currentNodeName string) []desiredstate.NodePeer {
	currentNodeName = strings.TrimSpace(currentNodeName)
	peers := make([]desiredstate.NodePeer, 0, len(nodes))
	for _, node := range nodes {
		name := strings.TrimSpace(node.Name)
		host := strings.TrimSpace(node.Config.Host)
		if name == "" || name == currentNodeName || host == "" {
			continue
		}
		peers = append(peers, desiredstate.NodePeer{
			Name:          name,
			Labels:        append([]string(nil), node.Config.Labels...),
			PublicAddress: host,
		})
	}
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].Name < peers[j].Name
	})
	return peers
}

func nodeCanRunKind(node config.Node, kind string) bool {
	if node.Labels == nil {
		return true
	}
	kind = strings.TrimSpace(kind)
	for _, label := range node.Labels {
		if strings.TrimSpace(label) == kind {
			return true
		}
	}
	return false
}

func releaseSnapshotKey(snapshot desiredstate.DeploySnapshot) string {
	environment := strings.TrimSpace(snapshot.Environment)
	if environment == "" {
		environment = config.DefaultEnvironment
	}
	return strings.TrimSpace(snapshot.WorkspaceKey) + "\n" + environment
}

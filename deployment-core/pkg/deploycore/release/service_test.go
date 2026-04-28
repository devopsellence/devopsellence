package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
)

func TestServiceRollbackPublishesSelectedReleaseThroughStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		environment: Environment{ID: "env-1", Name: "production", CurrentReleaseID: "rel-2"},
		nodes: []Node{{
			ID:     "node-1",
			Name:   "web-a",
			Config: config.Node{Labels: []string{config.DefaultWebRole}},
		}},
		releases: []Release{
			{
				ID:            "rel-2",
				EnvironmentID: "env-1",
				Revision:      "bbb2222",
				CreatedAt:     "2026-04-28T12:02:00Z",
				Snapshot: desiredstate.DeploySnapshot{
					WorkspaceKey: "/workspace/shop",
					Environment:  "production",
					Revision:     "bbb2222",
					Services:     []desiredstate.ServiceJSON{{Name: "web", Kind: "web", Image: "shop:bbb2222"}},
				},
			},
			{
				ID:            "rel-1",
				EnvironmentID: "env-1",
				Revision:      "aaa1111",
				CreatedAt:     "2026-04-28T12:01:00Z",
				Snapshot: desiredstate.DeploySnapshot{
					WorkspaceKey: "/workspace/shop",
					Environment:  "production",
					Revision:     "aaa1111",
					Services:     []desiredstate.ServiceJSON{{Name: "web", Kind: "web", Image: "shop:aaa1111"}},
				},
			},
		},
	}

	result, err := (Service{Store: store}).Rollback(ctx, RollbackInput{
		Environment:  EnvironmentRef{ID: "env-1"},
		DeploymentID: "dep-1",
		Sequence:     7,
		Now:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Release.ID != "rel-1" {
		t.Fatalf("rollback release = %s, want rel-1", result.Release.ID)
	}
	if store.currentReleaseID != "rel-1" {
		t.Fatalf("current release = %s, want rel-1", store.currentReleaseID)
	}
	if result.Deployment.Kind != DeploymentKindRollback || result.Deployment.Status != DeploymentStatusSettled {
		t.Fatalf("deployment = %#v", result.Deployment)
	}
	if len(store.updatedDeployments) < 2 || store.updatedDeployments[0].Status != DeploymentStatusRunning {
		t.Fatalf("updated deployments = %#v, want running state persisted before publish", store.updatedDeployments)
	}
	if len(store.publications) != 1 || store.publications[0].NodeName != "web-a" || store.publications[0].Revision == "" {
		t.Fatalf("publications = %#v", store.publications)
	}
}

func TestServiceRollbackFailsWhenReleaseTargetsNoCurrentNodes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		environment: Environment{ID: "env-1", Name: "production", CurrentReleaseID: "rel-2"},
		nodes: []Node{{
			ID:     "node-1",
			Name:   "web-a",
			Config: config.Node{Labels: []string{config.DefaultWebRole}},
		}},
		releases: []Release{
			{ID: "rel-2", EnvironmentID: "env-1", Revision: "bbb2222", CreatedAt: "2026-04-28T12:02:00Z"},
			{
				ID:            "rel-1",
				EnvironmentID: "env-1",
				Revision:      "aaa1111",
				CreatedAt:     "2026-04-28T12:01:00Z",
				TargetNodeIDs: []string{"missing-node"},
				Snapshot: desiredstate.DeploySnapshot{
					WorkspaceKey: "/workspace/shop",
					Environment:  "production",
					Revision:     "aaa1111",
					Services:     []desiredstate.ServiceJSON{{Name: "web", Kind: "web", Image: "shop:aaa1111"}},
				},
			},
		},
	}

	_, err := (Service{Store: store}).Rollback(ctx, RollbackInput{
		Environment:  EnvironmentRef{ID: "env-1"},
		DeploymentID: "dep-1",
		Sequence:     7,
		Now:          now,
	})
	if err == nil || err.Error() != "no target nodes match release" {
		t.Fatalf("Rollback() error = %v, want no target nodes", err)
	}
	if store.currentReleaseID != "" {
		t.Fatalf("current release = %s, want unchanged", store.currentReleaseID)
	}
	if len(store.updatedDeployments) != 1 || store.updatedDeployments[0].Status != DeploymentStatusFailed {
		t.Fatalf("updated deployments = %#v, want failed deployment", store.updatedDeployments)
	}
}

func TestServiceRollbackUsesCoreSelectionForExplicitSelector(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		environment: Environment{ID: "env-1", Name: "production", CurrentReleaseID: "rel-2"},
		nodes: []Node{{
			ID:     "node-1",
			Name:   "web-a",
			Config: config.Node{Labels: []string{config.DefaultWebRole}},
		}},
		releases: []Release{
			{
				ID:            "rel-1",
				EnvironmentID: "env-1",
				Revision:      "aaa1111",
				CreatedAt:     "2026-04-28T12:01:00Z",
				Snapshot: desiredstate.DeploySnapshot{
					WorkspaceKey: "/workspace/shop",
					Environment:  "production",
					Revision:     "aaa1111",
					Services:     []desiredstate.ServiceJSON{{Name: "web", Kind: "web", Image: "shop:aaa1111"}},
				},
			},
		},
	}

	result, err := (Service{Store: store}).Rollback(ctx, RollbackInput{
		Environment:  EnvironmentRef{ID: "env-1"},
		Selector:     "aaa1111",
		DeploymentID: "dep-1",
		Sequence:     7,
		Now:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Release.ID != "rel-1" {
		t.Fatalf("rollback release = %s, want rel-1", result.Release.ID)
	}
	if store.releaseCalls != 0 || store.releaseListCalls != 1 {
		t.Fatalf("release calls = %d, list calls = %d; want core release selection", store.releaseCalls, store.releaseListCalls)
	}
}

func TestServiceRollbackPlansPublicationWithPlacementAndPeers(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		environment: Environment{ID: "env-1", Name: "production", CurrentReleaseID: "rel-2"},
		nodes: []Node{
			{
				ID:     "node-1",
				Name:   "web-a",
				Config: config.Node{Host: "203.0.113.10", Labels: []string{config.DefaultWebRole}},
			},
			{
				ID:     "node-2",
				Name:   "web-b",
				Config: config.Node{Host: "203.0.113.11", Labels: []string{config.DefaultWebRole}},
			},
		},
		releases: []Release{
			{ID: "rel-2", EnvironmentID: "env-1", Revision: "bbb2222", CreatedAt: "2026-04-28T12:02:00Z"},
			{
				ID:            "rel-1",
				EnvironmentID: "env-1",
				Revision:      "aaa1111",
				CreatedAt:     "2026-04-28T12:01:00Z",
				TargetNodeIDs: []string{"node-1", "node-2"},
				Snapshot: desiredstate.DeploySnapshot{
					WorkspaceKey:       "/workspace/shop",
					Environment:        "production",
					Revision:           "aaa1111",
					Services:           []desiredstate.ServiceJSON{{Name: "web", Kind: "web", Image: "shop:aaa1111"}},
					ReleaseTask:        &desiredstate.TaskJSON{Name: "release", Image: "shop:aaa1111"},
					ReleaseService:     "web",
					ReleaseServiceKind: config.DefaultWebRole,
				},
			},
		},
	}

	_, err := (Service{Store: store}).Rollback(ctx, RollbackInput{
		Environment:  EnvironmentRef{ID: "env-1"},
		DeploymentID: "dep-1",
		Sequence:     7,
		Now:          now,
	})
	if err != nil {
		t.Fatal(err)
	}
	var webAPlan PublicationPlan
	for _, plan := range store.plans {
		if plan.NodeName == "web-a" {
			webAPlan = plan
			break
		}
	}
	if webAPlan.NodeName == "" {
		t.Fatalf("plans = %#v, want web-a plan", store.plans)
	}
	var payload struct {
		NodePeers []struct {
			Name          string `json:"name"`
			PublicAddress string `json:"publicAddress"`
		} `json:"nodePeers"`
		Environments []struct {
			Tasks []struct {
				Name string `json:"name"`
			} `json:"tasks"`
		} `json:"environments"`
	}
	if err := json.Unmarshal(webAPlan.DesiredStateJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.NodePeers) != 1 || payload.NodePeers[0].Name != "web-b" || payload.NodePeers[0].PublicAddress != "203.0.113.11" {
		t.Fatalf("node peers = %#v, want web-b peer", payload.NodePeers)
	}
	if len(payload.Environments) != 1 || len(payload.Environments[0].Tasks) != 1 || payload.Environments[0].Tasks[0].Name != "release" {
		t.Fatalf("environments = %#v, want release task on selected node", payload.Environments)
	}
}

func TestTargetNodesForReleaseMatchesNodeIDsOnly(t *testing.T) {
	nodes := []Node{
		{ID: "node-1", Name: "web-a"},
		{ID: "node-2", Name: "node-1"},
	}
	targets := targetNodesForRelease(nodes, Release{
		TargetNodeIDs: []string{"node-1"},
	})
	if len(targets) != 1 || targets[0].ID != "node-1" {
		t.Fatalf("targets = %#v, want node-1 only", targets)
	}
}

func TestNodePeersForPublicationSkipsCurrentAndEmptyHost(t *testing.T) {
	peers := nodePeersForPublication([]Node{
		{Name: " web-a ", Config: config.Node{Host: "203.0.113.10", Labels: []string{config.DefaultWebRole}}},
		{Name: "web-b", Config: config.Node{Host: " 203.0.113.11 ", Labels: []string{config.DefaultWebRole}}},
		{Name: "web-c", Config: config.Node{}},
	}, "web-a")
	if len(peers) != 1 || peers[0].Name != "web-b" || peers[0].PublicAddress != "203.0.113.11" {
		t.Fatalf("peers = %#v, want only web-b with trimmed host", peers)
	}
}

func TestServiceRollbackPersistsFailureWhenSetCurrentReleaseFails(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{
		environment:   Environment{ID: "env-1", Name: "production", CurrentReleaseID: "rel-2"},
		setCurrentErr: errors.New("set current failed"),
		nodes: []Node{{
			ID:     "node-1",
			Name:   "web-a",
			Config: config.Node{Labels: []string{config.DefaultWebRole}},
		}},
		releases: []Release{
			{ID: "rel-2", EnvironmentID: "env-1", Revision: "bbb2222", CreatedAt: "2026-04-28T12:02:00Z"},
			{
				ID:            "rel-1",
				EnvironmentID: "env-1",
				Revision:      "aaa1111",
				CreatedAt:     "2026-04-28T12:01:00Z",
				Snapshot: desiredstate.DeploySnapshot{
					WorkspaceKey: "/workspace/shop",
					Environment:  "production",
					Revision:     "aaa1111",
					Services:     []desiredstate.ServiceJSON{{Name: "web", Kind: "web", Image: "shop:aaa1111"}},
				},
			},
		},
	}

	result, err := (Service{Store: store}).Rollback(ctx, RollbackInput{
		Environment:  EnvironmentRef{ID: "env-1"},
		DeploymentID: "dep-1",
		Sequence:     7,
		Now:          now,
	})
	if err == nil || err.Error() != "set current failed" {
		t.Fatalf("Rollback() error = %v, want set current failed", err)
	}
	if result.Deployment.Status != DeploymentStatusFailed || result.Deployment.StatusMessage != "failed to set current release" {
		t.Fatalf("deployment = %#v, want failed set-current state", result.Deployment)
	}
	if result.Deployment.FinishedAt == "" || result.Deployment.PublicationResult == nil || result.Deployment.PublicationResult.ErrorMessage != "set current failed" {
		t.Fatalf("publication result = %#v, finished_at = %q", result.Deployment.PublicationResult, result.Deployment.FinishedAt)
	}
	if len(result.Publications) != 1 || len(result.Deployment.PublicationResult.NodeResults) != 1 || len(store.publications) != 1 {
		t.Fatalf("publications = %#v, result = %#v", result.Publications, result.Deployment.PublicationResult)
	}
	if got := store.updatedDeployments[len(store.updatedDeployments)-1]; got.Status != DeploymentStatusFailed || got.StatusMessage != "failed to set current release" {
		t.Fatalf("last updated deployment = %#v, want failed set-current state", got)
	}
}

type fakeStore struct {
	environment        Environment
	nodes              []Node
	releases           []Release
	deployments        []Deployment
	updatedDeployments []Deployment
	currentReleaseID   string
	publications       []DesiredStatePublication
	plans              []PublicationPlan
	releaseListCalls   int
	releaseCalls       int
	setCurrentErr      error
	publishErr         error
}

func (s *fakeStore) WithEnvironmentLock(ctx context.Context, ref EnvironmentRef, fn func(context.Context, Tx) error) error {
	return fn(ctx, s)
}

func (s *fakeStore) Environment(context.Context, EnvironmentRef) (Environment, error) {
	return s.environment, nil
}

func (s *fakeStore) Nodes(context.Context, string) ([]Node, error) {
	return s.nodes, nil
}

func (s *fakeStore) Releases(context.Context, string, ReleaseListOptions) ([]Release, error) {
	s.releaseListCalls++
	return s.releases, nil
}

func (s *fakeStore) Release(_ context.Context, _ string, selector ReleaseSelector) (Release, error) {
	s.releaseCalls++
	for _, release := range s.releases {
		if release.ID == selector.Selector {
			return release, nil
		}
		if selector.Selector != "" && (release.Revision == selector.Selector || strings.HasPrefix(release.Revision, selector.Selector)) {
			return release, nil
		}
	}
	return Release{}, fmt.Errorf("release not found")
}

func (s *fakeStore) CreateRelease(_ context.Context, release Release) (Release, error) {
	s.releases = append(s.releases, release)
	return release, nil
}

func (s *fakeStore) CreateDeployment(_ context.Context, deployment Deployment) (Deployment, error) {
	s.deployments = append(s.deployments, deployment)
	return deployment, nil
}

func (s *fakeStore) UpdateDeployment(_ context.Context, deployment Deployment) error {
	s.updatedDeployments = append(s.updatedDeployments, deployment)
	for i := range s.deployments {
		if s.deployments[i].ID == deployment.ID {
			s.deployments[i] = deployment
			return nil
		}
	}
	s.deployments = append(s.deployments, deployment)
	return nil
}

func (s *fakeStore) SetCurrentRelease(_ context.Context, _, releaseID string) error {
	if s.setCurrentErr != nil {
		return s.setCurrentErr
	}
	s.currentReleaseID = releaseID
	return nil
}

func (s *fakeStore) PublishDesiredState(_ context.Context, node Node, plan PublicationPlan) (DesiredStatePublication, error) {
	if s.publishErr != nil {
		return DesiredStatePublication{}, s.publishErr
	}
	s.plans = append(s.plans, plan)
	publication := DesiredStatePublication{
		NodeID:   node.ID,
		NodeName: node.Name,
		Revision: plan.Revision,
		Status:   PublicationStatusWritten,
	}
	s.publications = append(s.publications, publication)
	return publication, nil
}

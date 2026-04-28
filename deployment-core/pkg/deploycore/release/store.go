package release

import "context"

type EnvironmentRef struct {
	ID   string
	Name string
}

type ReleaseSelector struct {
	Selector string
}

type ReleaseListOptions struct {
	// Limit caps returned releases when greater than zero. Limit <= 0 means no limit.
	Limit int
}

type Store interface {
	WithEnvironmentLock(ctx context.Context, ref EnvironmentRef, fn func(context.Context, Tx) error) error
}

type Tx interface {
	Environment(ctx context.Context, ref EnvironmentRef) (Environment, error)
	Nodes(ctx context.Context, environmentID string) ([]Node, error)
	Releases(ctx context.Context, environmentID string, opts ReleaseListOptions) ([]Release, error)
	Release(ctx context.Context, environmentID string, selector ReleaseSelector) (Release, error)
	CreateRelease(ctx context.Context, release Release) (Release, error)
	CreateDeployment(ctx context.Context, deployment Deployment) (Deployment, error)
	UpdateDeployment(ctx context.Context, deployment Deployment) error
	SetCurrentRelease(ctx context.Context, environmentID, releaseID string) error
	PublishDesiredState(ctx context.Context, node Node, plan PublicationPlan) (DesiredStatePublication, error)
}

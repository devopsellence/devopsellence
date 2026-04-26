package ui

import (
	"context"
	"io"
	"time"
)

type DeploymentSummary struct {
	AssignedNodes int
	Pending       int
	Reconciling   int
	Settled       int
	Error         int
	Complete      bool
	Failed        bool
}

type DeploymentNode struct {
	Name       string
	Phase      string
	Detail     string
	ReportedAt string
}

type DeploymentSnapshot struct {
	Project       string
	Environment   string
	Revision      string
	PublicURL     string
	Status        string
	StatusMessage string
	Summary       DeploymentSummary
	Nodes         []DeploymentNode
}

func MonitorDeployment(ctx context.Context, _ io.Writer, _ string, interval time.Duration, fetch func(context.Context) (DeploymentSnapshot, error)) (DeploymentSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	var latest DeploymentSnapshot
	for {
		snapshot, err := fetch(ctx)
		if err != nil {
			return latest, err
		}
		latest = snapshot
		if snapshot.Summary.Complete || snapshot.Summary.Failed {
			return snapshot, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return latest, ctx.Err()
		case <-timer.C:
		}
	}
}

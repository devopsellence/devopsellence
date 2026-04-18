package reconcile

import (
	"context"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/report"
)

func (r *Reconciler) CurrentStatus(ctx context.Context, desired *desiredstatepb.DesiredState) (*report.Summary, []report.EnvironmentStatus, error) {
	if desired == nil {
		return nil, nil, nil
	}

	existing, err := r.engine.ListManaged(ctx)
	if err != nil {
		return nil, nil, err
	}

	existingByService := map[string][]engine.ContainerState{}
	for _, container := range existing {
		if container.Service == "" {
			continue
		}
		existingByService[containerServiceKey(container)] = append(existingByService[containerServiceKey(container)], container)
	}

	summary := &report.Summary{}
	environments := make([]report.EnvironmentStatus, 0, len(desired.Environments))

	for _, environment := range desired.Environments {
		if environment == nil {
			continue
		}
		envStatus := report.EnvironmentStatus{
			Name:     environment.GetName(),
			Revision: environmentRevision(desired, environment),
			Phase:    report.PhaseSettled,
			Services: make([]report.ServiceStatus, 0, len(environment.Services)),
		}
		summary.Environments++

		for _, service := range environment.Services {
			if service == nil {
				continue
			}
			summary.Services++
			current := pickCurrentContainer(existingByService[runtimeServiceKey(environment.GetName(), service.GetName())])
			serviceStatus := r.serviceStatus(ctx, service, current)
			if serviceStatus.Phase == report.PhaseError {
				summary.UnhealthyServices++
			}
			if phaseSeverity(serviceStatus.Phase) > phaseSeverity(envStatus.Phase) {
				envStatus.Phase = serviceStatus.Phase
			}
			envStatus.Services = append(envStatus.Services, serviceStatus)
		}

		environments = append(environments, envStatus)
	}

	return summary, environments, nil
}

func (r *Reconciler) serviceStatus(ctx context.Context, service *desiredstatepb.Service, current *engine.ContainerState) report.ServiceStatus {
	serviceStatus := report.ServiceStatus{
		Name:  service.GetName(),
		Kind:  desiredstate.ServiceKind(service),
		Phase: report.PhaseReconciling,
		State: "missing",
	}

	if current == nil {
		return serviceStatus
	}

	serviceStatus.Container = current.Name
	serviceStatus.Hash = current.Hash

	if !current.Running {
		serviceStatus.State = "stopped"
		serviceStatus.Phase = report.PhaseError
		return serviceStatus
	}

	serviceStatus.State = "running"
	serviceStatus.Phase = report.PhaseSettled

	info, err := r.engine.Inspect(ctx, current.Name)
	if err != nil {
		return serviceStatus
	}

	if info.Health != "" {
		serviceStatus.Health = info.Health
		switch info.Health {
		case "healthy":
		case "starting":
			serviceStatus.State = "starting"
			serviceStatus.Phase = report.PhaseReconciling
		case "unhealthy":
			serviceStatus.State = "unhealthy"
			serviceStatus.Phase = report.PhaseError
		}
	}

	return serviceStatus
}

func environmentRevision(desired *desiredstatepb.DesiredState, environment *desiredstatepb.Environment) string {
	if environment == nil {
		return ""
	}
	if environment.GetRevision() != "" {
		return environment.GetRevision()
	}
	if desired == nil {
		return ""
	}
	return desired.GetRevision()
}

func pickCurrentContainer(containers []engine.ContainerState) *engine.ContainerState {
	if len(containers) == 0 {
		return nil
	}
	best := &containers[0]
	for i := range containers {
		candidate := &containers[i]
		if candidate.Running && !best.Running {
			best = candidate
		}
	}
	return best
}

func phaseSeverity(phase report.Phase) int {
	switch phase {
	case report.PhaseError:
		return 2
	case report.PhaseReconciling:
		return 1
	default:
		return 0
	}
}

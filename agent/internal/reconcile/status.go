package reconcile

import (
	"context"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/report"
)

func (r *Reconciler) CurrentStatus(ctx context.Context, desired *desiredstatepb.DesiredState) (*report.Summary, []report.EnvironmentStatus, []report.ContainerStatus, error) {
	if desired == nil {
		return nil, nil, nil, nil
	}

	existing, err := r.engine.ListManaged(ctx)
	if err != nil {
		return nil, nil, nil, err
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
	containers := []report.ContainerStatus{}
	multiEnvironment := countDesiredEnvironments(desired) > 1

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
			current := pickCurrentContainer(existingByService[environment.GetName()+"/"+service.GetName()])
			serviceStatus, containerStatus := r.serviceStatus(ctx, multiEnvironment, environment.GetName(), service, current)
			if serviceStatus.Phase == report.PhaseError {
				summary.UnhealthyServices++
			}
			if serviceStatus.Phase != report.PhaseSettled && envStatus.Phase == report.PhaseSettled {
				envStatus.Phase = serviceStatus.Phase
			}
			envStatus.Services = append(envStatus.Services, serviceStatus)
			containers = append(containers, containerStatus)
		}

		environments = append(environments, envStatus)
	}

	return summary, environments, containers, nil
}

func (r *Reconciler) serviceStatus(ctx context.Context, multiEnvironment bool, environmentName string, service *desiredstatepb.Service, current *engine.ContainerState) (report.ServiceStatus, report.ContainerStatus) {
	serviceName := service.GetName()
	serviceStatus := report.ServiceStatus{
		Name:  serviceName,
		Kind:  desiredstate.ServiceKind(service),
		Phase: report.PhaseReconciling,
		State: "missing",
	}
	containerStatus := report.ContainerStatus{
		Name:  legacyContainerStatusName(multiEnvironment, environmentName, serviceName),
		State: "missing",
	}

	if current == nil {
		return serviceStatus, containerStatus
	}

	serviceStatus.Container = current.Name
	serviceStatus.Hash = current.Hash
	containerStatus.Hash = current.Hash

	if !current.Running {
		serviceStatus.State = "stopped"
		serviceStatus.Phase = report.PhaseError
		containerStatus.State = "stopped"
		return serviceStatus, containerStatus
	}

	serviceStatus.State = "running"
	serviceStatus.Phase = report.PhaseSettled
	containerStatus.State = "running"

	info, err := r.engine.Inspect(ctx, current.Name)
	if err != nil {
		return serviceStatus, containerStatus
	}

	if info.Health != "" {
		serviceStatus.Health = info.Health
		switch info.Health {
		case "healthy":
		case "starting":
			serviceStatus.State = "starting"
			serviceStatus.Phase = report.PhaseReconciling
			containerStatus.State = "starting"
		case "unhealthy":
			serviceStatus.State = "unhealthy"
			serviceStatus.Phase = report.PhaseError
			containerStatus.State = "unhealthy"
		}
	}

	return serviceStatus, containerStatus
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

func countDesiredEnvironments(desired *desiredstatepb.DesiredState) int {
	if desired == nil {
		return 0
	}
	count := 0
	for _, environment := range desired.Environments {
		if environment != nil {
			count++
		}
	}
	return count
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

func legacyContainerStatusName(multiEnvironment bool, environmentName, serviceName string) string {
	if !multiEnvironment {
		return serviceName
	}
	return environmentName + "/" + serviceName
}

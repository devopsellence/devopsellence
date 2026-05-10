package reconcile

import (
	"context"
	"strings"

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
			expectedHash := r.expectedRuntimeServiceHash(environment.GetName(), service.GetName(), service)
			serviceStatus := r.serviceStatus(ctx, service, current, envStatus.Revision, expectedHash)
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

func (r *Reconciler) serviceStatus(ctx context.Context, service *desiredstatepb.Service, current *engine.ContainerState, environmentRevision, expectedHash string) report.ServiceStatus {
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
	serviceStatus.ContainerRevision = strings.TrimSpace(current.Revision)
	serviceStatus.Hash = current.Hash
	serviceStatus.RevisionStatus, serviceStatus.RevisionMessage = serviceRevisionStatus(current, environmentRevision, expectedHash)

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

func (r *Reconciler) expectedRuntimeServiceHash(environmentName, serviceName string, service *desiredstatepb.Service) string {
	hash, _, _, err := r.runtimeServiceHash(desiredstate.RuntimeService{
		EnvironmentName: environmentName,
		ServiceName:     serviceName,
		Service:         service,
	})
	if err != nil {
		return ""
	}
	return hash
}

func serviceRevisionStatus(current *engine.ContainerState, environmentRevision, expectedHash string) (string, string) {
	if current == nil {
		return "", ""
	}
	containerRevision := strings.TrimSpace(current.Revision)
	environmentRevision = strings.TrimSpace(environmentRevision)
	if containerRevision == "" || environmentRevision == "" || containerRevision == environmentRevision {
		return "", ""
	}
	if strings.TrimSpace(expectedHash) != "" && strings.TrimSpace(current.Hash) == strings.TrimSpace(expectedHash) {
		return "reused_from_previous_release", "container was reused from an earlier release because this service config did not change; environment revision is the current rollout revision"
	}
	return "previous_release", "container is still labeled with an earlier release revision; environment revision is the current desired rollout revision"
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

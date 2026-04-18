package desiredstate

import (
	"fmt"
	"strings"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

const (
	CurrentSchemaVersion   = 2
	DefaultEnvironmentName = "default"
	ServiceKindWeb         = "web"
	ServiceKindWorker      = "worker"
	DefaultHTTPPortName    = "http"
)

type RuntimeService struct {
	EnvironmentName     string
	EnvironmentRevision string
	ServiceName         string
	ServiceKind         string
	Service             *desiredstatepb.Service
}

type RuntimeTask struct {
	EnvironmentName     string
	EnvironmentRevision string
	Task                *desiredstatepb.Task
}

func RuntimeServices(state *desiredstatepb.DesiredState) []RuntimeService {
	if state == nil {
		return nil
	}

	services := []RuntimeService{}
	for _, env := range state.Environments {
		if env == nil {
			continue
		}
		envName := strings.TrimSpace(env.Name)
		envRevision := strings.TrimSpace(env.Revision)
		if envRevision == "" {
			envRevision = strings.TrimSpace(state.Revision)
		}
		for _, service := range env.Services {
			if service == nil {
				continue
			}
			name := strings.TrimSpace(service.Name)
			kind := normalizedServiceKind(service)
			services = append(services, RuntimeService{
				EnvironmentName:     envName,
				EnvironmentRevision: envRevision,
				ServiceName:         name,
				ServiceKind:         kind,
				Service:             service,
			})
		}
	}
	return services
}

func RuntimeTasks(state *desiredstatepb.DesiredState) []RuntimeTask {
	if state == nil {
		return nil
	}
	tasks := []RuntimeTask{}
	if len(state.Environments) > 0 {
		for _, env := range state.Environments {
			if env == nil {
				continue
			}
			envName := strings.TrimSpace(env.Name)
			envRevision := strings.TrimSpace(env.Revision)
			if envRevision == "" {
				envRevision = strings.TrimSpace(state.Revision)
			}
			for _, task := range env.Tasks {
				if task == nil {
					continue
				}
				tasks = append(tasks, RuntimeTask{
					EnvironmentName:     envName,
					EnvironmentRevision: envRevision,
					Task:                task,
				})
			}
		}
		return tasks
	}
	return tasks
}

func normalizedServiceKind(service *desiredstatepb.Service) string {
	if service == nil {
		return ""
	}
	kind := strings.TrimSpace(service.Kind)
	if kind != "" {
		return kind
	}
	name := strings.TrimSpace(service.Name)
	if name == ServiceKindWeb {
		return ServiceKindWeb
	}
	if name == ServiceKindWorker {
		return ServiceKindWorker
	}
	return ServiceKindWorker
}

func ServiceHTTPPort(service *desiredstatepb.Service, fallback uint16) uint16 {
	if service == nil {
		return fallback
	}
	for _, port := range service.Ports {
		if port == nil || port.Port == 0 {
			continue
		}
		if strings.TrimSpace(port.Name) == DefaultHTTPPortName {
			return uint16(port.Port)
		}
	}
	for _, port := range service.Ports {
		if port != nil && port.Port > 0 {
			return uint16(port.Port)
		}
	}
	return fallback
}

func IngressTargetPort(target *desiredstatepb.IngressTarget) string {
	if target == nil {
		return DefaultHTTPPortName
	}
	port := strings.TrimSpace(target.Port)
	if port == "" {
		return DefaultHTTPPortName
	}
	return port
}

func EnvoyClusterName(environmentName, serviceName, portName string) string {
	return sanitize(fmt.Sprintf("env-%s-%s-%s", environmentName, serviceName, portName))
}

func ServiceKind(service *desiredstatepb.Service) string {
	return normalizedServiceKind(service)
}

func ScopedKey(parts ...string) string {
	var b strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		fmt.Fprintf(&b, "%d:%s|", len(part), part)
	}
	return b.String()
}

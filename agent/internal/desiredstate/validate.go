package desiredstate

import (
	"fmt"
	"strings"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

const (
	ingressModeTunnel = "tunnel"
	ingressModePublic = "public"
)

func Validate(state *desiredstatepb.DesiredState) error {
	if state == nil {
		return fmt.Errorf("desired state is nil")
	}
	if state.Revision == "" {
		return fmt.Errorf("revision required")
	}
	if state.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("schema_version must be %d", CurrentSchemaVersion)
	}
	if err := validateEnvironments(state); err != nil {
		return err
	}

	if state.Ingress != nil {
		if err := validateIngress(state); err != nil {
			return err
		}
	}

	return nil
}

func validateEnvironments(state *desiredstatepb.DesiredState) error {
	seenEnvironments := map[string]struct{}{}
	seenSanitizedEnvironments := map[string]string{}
	for i, env := range state.Environments {
		if env == nil {
			return fmt.Errorf("environment[%d]: required", i)
		}
		name := strings.TrimSpace(env.Name)
		if name == "" {
			return fmt.Errorf("environment[%d]: name required", i)
		}
		sanitizedName, err := validateSanitizedName(fmt.Sprintf("environment[%s]", name), "name", name)
		if err != nil {
			return err
		}
		if _, ok := seenEnvironments[name]; ok {
			return fmt.Errorf("environment[%s]: duplicate name", name)
		}
		if existing, ok := seenSanitizedEnvironments[sanitizedName]; ok {
			return fmt.Errorf("environment[%s]: sanitized name collides with environment[%s]", name, existing)
		}
		seenEnvironments[name] = struct{}{}
		seenSanitizedEnvironments[sanitizedName] = name
		seenServices := map[string]struct{}{}
		seenSanitizedServices := map[string]string{}
		for j, service := range env.Services {
			if err := validateService(name, j, service); err != nil {
				return err
			}
			serviceName := strings.TrimSpace(service.Name)
			sanitizedServiceName, err := validateSanitizedName(fmt.Sprintf("environment[%s].service[%s]", name, serviceName), "name", serviceName)
			if err != nil {
				return err
			}
			if _, ok := seenServices[serviceName]; ok {
				return fmt.Errorf("environment[%s].service[%s]: duplicate name", name, serviceName)
			}
			if existing, ok := seenSanitizedServices[sanitizedServiceName]; ok {
				return fmt.Errorf("environment[%s].service[%s]: sanitized name collides with service[%s]", name, serviceName, existing)
			}
			seenServices[serviceName] = struct{}{}
			seenSanitizedServices[sanitizedServiceName] = serviceName
		}
		for _, task := range env.Tasks {
			if err := validateTask("environment["+name+"].task", task); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateService(environmentName string, index int, service *desiredstatepb.Service) error {
	prefix := fmt.Sprintf("environment[%s].service[%d]", environmentName, index)
	if service == nil {
		return fmt.Errorf("%s: required", prefix)
	}
	name := strings.TrimSpace(service.Name)
	if name == "" {
		return fmt.Errorf("%s.name required", prefix)
	}
	prefix = fmt.Sprintf("environment[%s].service[%s]", environmentName, name)
	if _, err := validateSanitizedName(prefix, "name", name); err != nil {
		return err
	}
	if service.Image == "" {
		return fmt.Errorf("%s.image required", prefix)
	}
	switch strings.TrimSpace(service.Kind) {
	case "", ServiceKindWeb, ServiceKindWorker, ServiceKindAccessory:
	default:
		return fmt.Errorf("%s.kind unsupported: %q", prefix, service.Kind)
	}
	for k := range service.Env {
		if k == "" {
			return fmt.Errorf("%s.env key empty", prefix)
		}
	}
	for k, v := range service.SecretRefs {
		if k == "" {
			return fmt.Errorf("%s.secret_refs key empty", prefix)
		}
		if v == "" {
			return fmt.Errorf("%s.secret_refs[%s] empty", prefix, k)
		}
		if _, ok := service.Env[k]; ok {
			return fmt.Errorf("%s.env key %q conflicts with secret_ref", prefix, k)
		}
	}
	for _, mount := range service.VolumeMounts {
		if mount.Source == "" {
			return fmt.Errorf("%s.volume_mount source required", prefix)
		}
		if mount.Target == "" {
			return fmt.Errorf("%s.volume_mount target required", prefix)
		}
		if mount.Target[0] != '/' {
			return fmt.Errorf("%s.volume_mount target must be absolute", prefix)
		}
	}
	for _, port := range service.Ports {
		if port == nil {
			continue
		}
		portName := strings.TrimSpace(port.Name)
		if portName == "" {
			return fmt.Errorf("%s.ports: name required", prefix)
		}
		if _, err := validateSanitizedName(fmt.Sprintf("%s.ports[%s]", prefix, portName), "name", portName); err != nil {
			return err
		}
		if port.Port == 0 {
			return fmt.Errorf("%s.ports[%s].port required", prefix, port.Name)
		}
	}
	if err := validateUniquePortNames(prefix, service.Ports); err != nil {
		return err
	}
	if normalizedServiceKind(service) == ServiceKindWeb {
		if ServiceHTTPPort(service, 0) == 0 {
			return fmt.Errorf("%s: http port required", prefix)
		}
		if service.Healthcheck == nil {
			return fmt.Errorf("%s: healthcheck required", prefix)
		}
		if service.Healthcheck.Path == "" {
			return fmt.Errorf("%s.healthcheck.path required", prefix)
		}
		if service.Healthcheck.Port == 0 {
			return fmt.Errorf("%s.healthcheck.port required", prefix)
		}
	}
	return nil
}

func validateIngress(state *desiredstatepb.DesiredState) error {
	if len(ingressHosts(state.Ingress)) == 0 {
		return fmt.Errorf("ingress: hosts required")
	}
	if len(state.Ingress.Routes) > 0 {
		if err := validateIngressRoutes(state); err != nil {
			return err
		}
	} else if !hasWebService(state) {
		return fmt.Errorf("ingress requires web service")
	}
	switch normalizedIngressMode(state.Ingress) {
	case ingressModeTunnel:
		if state.Ingress.TunnelToken == "" && state.Ingress.TunnelTokenSecretRef == "" {
			return fmt.Errorf("ingress: tunnel_token or tunnel_token_secret_ref required")
		}
	case ingressModePublic:
		if state.Ingress.Tls != nil {
			switch strings.TrimSpace(state.Ingress.Tls.Mode) {
			case "", "auto", "manual", "off":
			default:
				return fmt.Errorf("ingress.tls: unsupported mode %q", state.Ingress.Tls.Mode)
			}
		}
	default:
		return fmt.Errorf("ingress: unsupported mode %q", state.Ingress.Mode)
	}
	return nil
}

func validateIngressRoutes(state *desiredstatepb.DesiredState) error {
	targets := map[string]*desiredstatepb.Service{}
	hosts := map[string]struct{}{}
	for _, host := range ingressHosts(state.Ingress) {
		hosts[host] = struct{}{}
	}
	for _, service := range RuntimeServices(state) {
		targets[ScopedKey(service.EnvironmentName, service.ServiceName)] = service.Service
	}
	seen := map[string]struct{}{}
	for i, route := range state.Ingress.Routes {
		if route == nil {
			return fmt.Errorf("ingress.routes[%d]: required", i)
		}
		if route.Match == nil {
			return fmt.Errorf("ingress.routes[%d].match required", i)
		}
		hostname := strings.TrimSpace(route.Match.Hostname)
		if hostname == "" {
			return fmt.Errorf("ingress.routes[%d].match.hostname required", i)
		}
		if _, ok := hosts[hostname]; !ok {
			return fmt.Errorf("ingress.routes[%d].match.hostname %q missing from ingress.hosts", i, hostname)
		}
		pathPrefix := strings.TrimSpace(route.Match.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "/"
		}
		if !strings.HasPrefix(pathPrefix, "/") {
			return fmt.Errorf("ingress.routes[%d].match.path_prefix must start with /", i)
		}
		if route.Target == nil {
			return fmt.Errorf("ingress.routes[%d].target required", i)
		}
		env := strings.TrimSpace(route.Target.Environment)
		serviceName := strings.TrimSpace(route.Target.Service)
		if env == "" {
			return fmt.Errorf("ingress.routes[%d].target.environment required", i)
		}
		if serviceName == "" {
			return fmt.Errorf("ingress.routes[%d].target.service required", i)
		}
		service := targets[ScopedKey(env, serviceName)]
		if service == nil {
			return fmt.Errorf("ingress.routes[%d].target references missing service %s/%s", i, env, serviceName)
		}
		portName := strings.TrimSpace(route.Target.Port)
		if portName == "" {
			return fmt.Errorf("ingress.routes[%d].target.port is required", i)
		}
		if !serviceHasPort(service, portName) {
			return fmt.Errorf("ingress.routes[%d].target references missing port %s/%s:%s", i, env, serviceName, portName)
		}
		key := hostname + "\x00" + pathPrefix
		if _, ok := seen[key]; ok {
			return fmt.Errorf("ingress.routes[%d]: duplicate route for %s%s", i, hostname, pathPrefix)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func serviceHasPort(service *desiredstatepb.Service, name string) bool {
	if service == nil {
		return false
	}
	for _, port := range service.Ports {
		if port == nil {
			continue
		}
		if strings.TrimSpace(port.Name) == name && port.Port > 0 {
			return true
		}
	}
	return false
}

func hasWebService(state *desiredstatepb.DesiredState) bool {
	for _, service := range RuntimeServices(state) {
		if service.ServiceKind == ServiceKindWeb {
			return true
		}
	}
	return false
}

func validateTask(name string, task *desiredstatepb.Task) error {
	if task == nil {
		return nil
	}
	if task.Name == "" {
		return fmt.Errorf("%s.name required", name)
	}
	if task.Image == "" {
		return fmt.Errorf("%s.image required", name)
	}
	if len(task.Entrypoint) == 0 && len(task.Command) == 0 {
		return fmt.Errorf("%s.entrypoint or %s.command required", name, name)
	}
	for key := range task.Env {
		if key == "" {
			return fmt.Errorf("%s.env key empty", name)
		}
	}
	for key, value := range task.SecretRefs {
		if key == "" {
			return fmt.Errorf("%s.secret_refs key empty", name)
		}
		if value == "" {
			return fmt.Errorf("%s.secret_refs[%s] empty", name, key)
		}
		if _, ok := task.Env[key]; ok {
			return fmt.Errorf("%s.env key %q conflicts with secret_ref", name, key)
		}
	}
	for _, mount := range task.VolumeMounts {
		if mount.Source == "" {
			return fmt.Errorf("%s.volume_mount source required", name)
		}
		if mount.Target == "" {
			return fmt.Errorf("%s.volume_mount target required", name)
		}
		if mount.Target[0] != '/' {
			return fmt.Errorf("%s.volume_mount target must be absolute", name)
		}
	}
	return nil
}

func normalizedIngressMode(ingress *desiredstatepb.Ingress) string {
	if ingress == nil {
		return ingressModeTunnel
	}

	switch strings.TrimSpace(ingress.Mode) {
	case "":
		if strings.TrimSpace(ingress.TunnelToken) != "" || strings.TrimSpace(ingress.TunnelTokenSecretRef) != "" {
			return ingressModeTunnel
		}
		return ingressModePublic
	case ingressModeTunnel:
		return ingressModeTunnel
	case ingressModePublic:
		return ingressModePublic
	default:
		return strings.TrimSpace(ingress.Mode)
	}
}

func ingressHosts(ingress *desiredstatepb.Ingress) []string {
	if ingress == nil {
		return nil
	}
	hosts := make([]string, 0, len(ingress.Hosts))
	for _, host := range ingress.Hosts {
		host = strings.TrimSpace(host)
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func validateUniquePortNames(prefix string, ports []*desiredstatepb.ServicePort) error {
	seen := map[string]struct{}{}
	seenSanitized := map[string]string{}
	for _, port := range ports {
		if port == nil {
			continue
		}
		name := strings.TrimSpace(port.Name)
		if _, ok := seen[name]; ok {
			return fmt.Errorf("%s.ports[%s]: duplicate name", prefix, name)
		}
		sanitizedName, err := validateSanitizedName(fmt.Sprintf("%s.ports[%s]", prefix, name), "name", name)
		if err != nil {
			return err
		}
		if existing, ok := seenSanitized[sanitizedName]; ok {
			return fmt.Errorf("%s.ports[%s]: sanitized name collides with port[%s]", prefix, name, existing)
		}
		seen[name] = struct{}{}
		seenSanitized[sanitizedName] = name
	}
	return nil
}

func validateSanitizedName(prefix, field, value string) (string, error) {
	sanitizedValue := sanitize(value)
	if sanitizedValue == "" {
		return "", fmt.Errorf("%s.%s sanitizes to empty", prefix, field)
	}
	return sanitizedValue, nil
}

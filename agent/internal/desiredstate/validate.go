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
	if err := validateTask("release_command", state.ReleaseCommand); err != nil {
		return err
	}

	seen := map[string]struct{}{}
	hasWeb := false
	for i, c := range state.Containers {
		if c.ServiceName == "" {
			return fmt.Errorf("container[%d]: service_name required", i)
		}
		if c.ServiceName != "web" && c.ServiceName != "worker" {
			return fmt.Errorf("container[%s]: unsupported service_name", c.ServiceName)
		}
		if c.Image == "" {
			return fmt.Errorf("container[%s]: image required", c.ServiceName)
		}
		if _, ok := seen[c.ServiceName]; ok {
			return fmt.Errorf("container[%s]: duplicate service_name", c.ServiceName)
		}
		seen[c.ServiceName] = struct{}{}
		if c.ServiceName == "web" {
			hasWeb = true
		}
		for k := range c.Env {
			if k == "" {
				return fmt.Errorf("container[%s]: env key empty", c.ServiceName)
			}
		}
		for k, v := range c.SecretRefs {
			if k == "" {
				return fmt.Errorf("container[%s]: secret_refs key empty", c.ServiceName)
			}
			if v == "" {
				return fmt.Errorf("container[%s]: secret_refs[%s] empty", c.ServiceName, k)
			}
			if _, ok := c.Env[k]; ok {
				return fmt.Errorf("container[%s]: env key %q conflicts with secret_ref", c.ServiceName, k)
			}
		}
		for _, mount := range c.VolumeMounts {
			if mount.Source == "" {
				return fmt.Errorf("container[%s]: volume_mount source required", c.ServiceName)
			}
			if mount.Target == "" {
				return fmt.Errorf("container[%s]: volume_mount target required", c.ServiceName)
			}
			if mount.Target[0] != '/' {
				return fmt.Errorf("container[%s]: volume_mount target must be absolute", c.ServiceName)
			}
		}
		if c.ServiceName == "web" {
			if c.Port == 0 {
				return fmt.Errorf("container[%s]: port required", c.ServiceName)
			}
			if c.Healthcheck == nil {
				return fmt.Errorf("container[%s]: healthcheck required", c.ServiceName)
			}
			if c.Healthcheck.Path == "" {
				return fmt.Errorf("container[%s]: healthcheck.path required", c.ServiceName)
			}
			if c.Healthcheck.Port == 0 {
				return fmt.Errorf("container[%s]: healthcheck.port required", c.ServiceName)
			}
		} else if c.Healthcheck != nil {
			return fmt.Errorf("container[%s]: healthcheck unsupported", c.ServiceName)
		}
	}

	if state.Ingress != nil {
		if !hasWeb {
			return fmt.Errorf("ingress requires web container")
		}
		if len(ingressHosts(state.Ingress)) == 0 {
			return fmt.Errorf("ingress: hosts required")
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
	}

	return nil
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
	case "", ingressModeTunnel:
		return ingressModeTunnel
	case ingressModePublic:
		return strings.TrimSpace(ingress.Mode)
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

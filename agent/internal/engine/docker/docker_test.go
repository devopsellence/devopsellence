package docker

import (
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/moby/moby/api/types/container"
)

func TestBuildContainerCreateConfigSetsNetworkModeForManagedNetwork(t *testing.T) {
	spec := engine.ContainerSpec{
		Name:    "devopsellence-envoy",
		Image:   "envoyproxy/envoy:latest",
		Network: "devopsellence",
		Ports: []engine.PortBinding{{
			ContainerPort: 8443,
			HostPort:      8443,
			Protocol:      "tcp",
		}},
	}

	_, hostCfg, networkingConfig, err := buildContainerCreateConfig(spec)
	if err != nil {
		t.Fatalf("buildContainerCreateConfig returned error: %v", err)
	}
	if hostCfg.NetworkMode != container.NetworkMode("devopsellence") {
		t.Fatalf("expected network mode devopsellence, got %q", hostCfg.NetworkMode)
	}
	if networkingConfig == nil {
		t.Fatalf("expected networking config")
	}
	if _, ok := networkingConfig.EndpointsConfig["devopsellence"]; !ok {
		t.Fatalf("expected endpoint config for managed network")
	}
}

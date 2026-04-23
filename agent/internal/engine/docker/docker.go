package docker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
)

type Engine struct {
	client *client.Client
}

func New(socketPath string) (*Engine, error) {
	host := socketPath
	if !strings.HasPrefix(socketPath, "unix://") {
		host = "unix://" + socketPath
	}

	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}

	return &Engine{client: cli}, nil
}

func (e *Engine) ListManaged(ctx context.Context) ([]engine.ContainerState, error) {
	filters := make(client.Filters).Add("label", engine.LabelManaged+"=true")
	result, err := e.client.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: filters,
	})
	if err != nil {
		return nil, err
	}

	states := make([]engine.ContainerState, 0, len(result.Items))
	for _, c := range result.Items {
		name := containerName(c.Names)
		states = append(states, engine.ContainerState{
			Name:        name,
			Image:       c.Image,
			Running:     c.State == "running",
			Hash:        c.Labels[engine.LabelHash],
			Environment: c.Labels[engine.LabelEnvironment],
			Service:     c.Labels[engine.LabelService],
			ServiceKind: c.Labels[engine.LabelServiceKind],
			System:      c.Labels[engine.LabelSystem],
		})
	}

	return states, nil
}

func (e *Engine) CreateAndStart(ctx context.Context, spec engine.ContainerSpec) error {
	cfg, hostCfg, networkingConfig, err := buildContainerCreateConfig(spec)
	if err != nil {
		return err
	}

	resp, err := e.client.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           cfg,
		HostConfig:       hostCfg,
		NetworkingConfig: networkingConfig,
		Name:             spec.Name,
	})
	if err != nil {
		return err
	}

	_, err = e.client.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	return err
}

func buildContainerCreateConfig(spec engine.ContainerSpec) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	env := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}

	cfg := &container.Config{
		Image:      spec.Image,
		Entrypoint: spec.Entrypoint,
		Cmd:        spec.Command,
		Env:        env,
		Labels:     spec.Labels,
	}
	if spec.Health != nil && len(spec.Health.Test) > 0 {
		cfg.Healthcheck = &container.HealthConfig{
			Test:        spec.Health.Test,
			Interval:    spec.Health.Interval,
			Timeout:     spec.Health.Timeout,
			StartPeriod: spec.Health.StartPeriod,
			Retries:     spec.Health.Retries,
		}
	}

	hostCfg := &container.HostConfig{
		Binds:      spec.Binds,
		ExtraHosts: spec.ExtraHosts,
	}
	if spec.Network != "" {
		hostCfg.NetworkMode = container.NetworkMode(spec.Network)
	}
	if spec.Restart != nil && spec.Restart.Name != "" {
		hostCfg.RestartPolicy = container.RestartPolicy{
			Name:              container.RestartPolicyMode(spec.Restart.Name),
			MaximumRetryCount: spec.Restart.MaxRetries,
		}
	}

	var networkingConfig *network.NetworkingConfig
	if spec.Network != "" {
		networkingConfig = &network.NetworkingConfig{
			EndpointsConfig: map[string]*network.EndpointSettings{
				spec.Network: {},
			},
		}
	}

	if len(spec.Ports) > 0 {
		cfg.ExposedPorts = network.PortSet{}
		hostCfg.PortBindings = network.PortMap{}
		for _, port := range spec.Ports {
			proto := network.IPProtocol(port.Protocol)
			if proto == "" {
				proto = network.TCP
			}
			containerPort, ok := network.PortFrom(port.ContainerPort, proto)
			if !ok {
				return nil, nil, nil, fmt.Errorf("invalid port binding for %s", spec.Name)
			}
			cfg.ExposedPorts[containerPort] = struct{}{}
			hostCfg.PortBindings[containerPort] = []network.PortBinding{{
				HostPort: fmt.Sprintf("%d", port.HostPort),
			}}
		}
	}

	return cfg, hostCfg, networkingConfig, nil
}

func (e *Engine) Start(ctx context.Context, name string) error {
	_, err := e.client.ContainerStart(ctx, name, client.ContainerStartOptions{})
	return err
}

func (e *Engine) Wait(ctx context.Context, name string) (int64, error) {
	waitResult := e.client.ContainerWait(ctx, name, client.ContainerWaitOptions{
		Condition: container.WaitConditionNotRunning,
	})
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case err := <-waitResult.Error:
		if err != nil {
			return 0, err
		}
		return 0, nil
	case result := <-waitResult.Result:
		return int64(result.StatusCode), nil
	}
}

func (e *Engine) Stop(ctx context.Context, name string, timeout time.Duration) error {
	seconds := int(timeout.Seconds())
	_, err := e.client.ContainerStop(ctx, name, client.ContainerStopOptions{
		Timeout: &seconds,
	})
	return err
}

func (e *Engine) Remove(ctx context.Context, name string) error {
	_, err := e.client.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
	return err
}

func (e *Engine) ImageExists(ctx context.Context, image string) (bool, error) {
	_, err := e.client.ImageInspect(ctx, image)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (e *Engine) PullImage(ctx context.Context, image string, auth *engine.RegistryAuth) error {
	opts := client.ImagePullOptions{}
	if auth != nil {
		encoded, err := encodeRegistryAuth(auth)
		if err != nil {
			return err
		}
		opts.RegistryAuth = encoded
	}

	resp, err := e.client.ImagePull(ctx, image, opts)
	if err != nil {
		return err
	}
	defer resp.Close()

	_, err = io.Copy(io.Discard, resp)
	return err
}

func (e *Engine) Inspect(ctx context.Context, name string) (engine.ContainerInfo, error) {
	res, err := e.client.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return engine.ContainerInfo{}, err
	}

	publishedPorts := []engine.PortBinding{}
	if res.Container.HostConfig != nil {
		for containerPort, bindings := range res.Container.HostConfig.PortBindings {
			containerPortSpec := strings.TrimSpace(fmt.Sprint(containerPort))
			containerPortParts := strings.SplitN(containerPortSpec, "/", 2)
			containerPortValue, parseContainerErr := strconv.Atoi(containerPortParts[0])
			if parseContainerErr != nil {
				continue
			}
			proto := string(network.TCP)
			if len(containerPortParts) == 2 && strings.TrimSpace(containerPortParts[1]) != "" {
				proto = strings.TrimSpace(containerPortParts[1])
			}
			for _, binding := range bindings {
				hostPortValue := containerPortValue
				if binding.HostPort != "" {
					if parsedHostPort, parseHostErr := strconv.Atoi(binding.HostPort); parseHostErr == nil {
						hostPortValue = parsedHostPort
					}
				}
				publishedPorts = append(publishedPorts, engine.PortBinding{
					ContainerPort: uint16(containerPortValue),
					HostPort:      uint16(hostPortValue),
					Protocol:      proto,
				})
			}
		}
	}

	info := engine.ContainerInfo{
		Name:            strings.TrimPrefix(res.Container.Name, "/"),
		Running:         res.Container.State != nil && res.Container.State.Running,
		HasHealthcheck:  res.Container.State != nil && res.Container.State.Health != nil,
		PublishHostPort: len(publishedPorts) > 0,
		PublishedPorts:  publishedPorts,
		NetworkIP:       map[string]string{},
	}
	if res.Container.State != nil && res.Container.State.Health != nil {
		info.Health = string(res.Container.State.Health.Status)
	}
	if res.Container.NetworkSettings != nil {
		for name, settings := range res.Container.NetworkSettings.Networks {
			if settings.IPAddress.IsValid() {
				info.NetworkIP[name] = settings.IPAddress.String()
			}
		}
	}

	return info, nil
}

func (e *Engine) EnsureNetwork(ctx context.Context, name string) error {
	_, err := e.client.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return err
	}
	_, err = e.client.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver:     "bridge",
		Attachable: true,
		Labels: map[string]string{
			engine.LabelManaged: "true",
		},
	})
	return err
}

func (e *Engine) Logs(ctx context.Context, name string, tail int) ([]byte, error) {
	tailStr := "all"
	if tail > 0 {
		tailStr = fmt.Sprintf("%d", tail)
	}
	rc, err := e.client.ContainerLogs(ctx, name, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tailStr,
	})
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return demuxLogs(rc)
}

// demuxLogs strips Docker's 8-byte per-frame multiplexing headers and
// returns the raw combined stdout+stderr content.
// Frame format: [stream(1) 0 0 0 size(4 big-endian)] [payload(size)]
func demuxLogs(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 {
			continue
		}
		if _, err := io.CopyN(&buf, r, int64(size)); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	name := names[0]
	return strings.TrimPrefix(name, "/")
}

func encodeRegistryAuth(auth *engine.RegistryAuth) (string, error) {
	payload, err := json.Marshal(registry.AuthConfig{
		Username:      auth.Username,
		Password:      auth.Password,
		ServerAddress: auth.ServerAddress,
	})
	if err != nil {
		return "", fmt.Errorf("encode registry auth: %w", err)
	}
	return base64.URLEncoding.EncodeToString(payload), nil
}

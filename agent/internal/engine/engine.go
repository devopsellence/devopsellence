package engine

import (
	"context"
	"time"
)

const (
	LabelManaged  = "devopsellence.managed"
	LabelService  = "devopsellence.service"
	LabelHash     = "devopsellence.hash"
	LabelRevision = "devopsellence.revision"
	LabelSystem   = "devopsellence.system"
)

type Engine interface {
	ListManaged(ctx context.Context) ([]ContainerState, error)
	CreateAndStart(ctx context.Context, spec ContainerSpec) error
	Start(ctx context.Context, name string) error
	Wait(ctx context.Context, name string) (int64, error)
	Stop(ctx context.Context, name string, timeout time.Duration) error
	Remove(ctx context.Context, name string) error
	ImageExists(ctx context.Context, image string) (bool, error)
	PullImage(ctx context.Context, image string, auth *RegistryAuth) error
	Inspect(ctx context.Context, name string) (ContainerInfo, error)
	EnsureNetwork(ctx context.Context, name string) error
	// Logs returns the last tail lines of combined stdout+stderr for the
	// named container. Pass tail=0 for all output.
	Logs(ctx context.Context, name string, tail int) ([]byte, error)
}

type RegistryAuth struct {
	Username      string
	Password      string
	ServerAddress string
}

type ContainerSpec struct {
	Name       string
	Image      string
	Entrypoint []string
	Command    []string
	Env        map[string]string
	Labels     map[string]string
	Health     *Healthcheck
	Restart    *RestartPolicy
	Network    string
	Binds      []string
	Ports      []PortBinding
	ExtraHosts []string // e.g. ["host.docker.internal:host-gateway"]
}

type Healthcheck struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	StartPeriod time.Duration
	Retries     int
}

type RestartPolicy struct {
	Name       string
	MaxRetries int
}

type PortBinding struct {
	ContainerPort uint16
	HostPort      uint16
	Protocol      string
}

type ContainerState struct {
	Name    string
	Image   string
	Running bool
	Hash    string
	Service string
	System  string
}

type ContainerInfo struct {
	Name            string
	Running         bool
	Health          string
	HasHealthcheck  bool
	PublishHostPort bool
	NetworkIP       map[string]string
}

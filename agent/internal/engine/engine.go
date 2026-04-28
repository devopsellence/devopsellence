package engine

import (
	"context"
	"time"
)

const (
	LabelManaged     = "devopsellence.managed"
	LabelEnvironment = "devopsellence.environment"
	LabelService     = "devopsellence.service"
	LabelServiceKind = "devopsellence.service_kind"
	LabelHash        = "devopsellence.hash"
	LabelRevision    = "devopsellence.revision"
	LabelSystem      = "devopsellence.system"
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
	Log        *LogConfig
	Network    string
	Binds      []string
	Ports      []PortBinding
	ExtraHosts []string // e.g. ["host.docker.internal:host-gateway"]
}

// LogConfig describes per-container Docker logging configuration.
type LogConfig struct {
	Driver  string
	Options map[string]string
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
	Name        string
	Image       string
	Running     bool
	Managed     bool
	Hash        string
	Environment string
	Service     string
	ServiceKind string
	System      string
}

// ImageState describes a local Docker image known to the engine.
type ImageState struct {
	ID          string
	RepoTags    []string
	RepoDigests []string
	Size        int64
}

// ImageDelete reports an image delete/untag operation returned by Docker.
type ImageDelete struct {
	Deleted  string
	Untagged string
}

type ContainerInfo struct {
	Name            string
	Running         bool
	Health          string
	HasHealthcheck  bool
	PublishHostPort bool
	PublishedPorts  []PortBinding
	NetworkIP       map[string]string
	LogPath         string
	LogDriver       string
	LogOptions      map[string]string
}

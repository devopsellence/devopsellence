package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
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

// LogConfigHash returns a stable hash fragment for cfg.
func LogConfigHash(cfg *LogConfig) string {
	if cfg == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(cfg.Driver))
	builder.WriteByte(0)
	keys := make([]string, 0, len(cfg.Options))
	for key := range cfg.Options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString(strings.TrimSpace(key))
		builder.WriteByte(0)
		builder.WriteString(strings.TrimSpace(cfg.Options[key]))
		builder.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

// LogConfigMatches reports whether an inspected Docker log config matches cfg.
func LogConfigMatches(driver string, options map[string]string, cfg *LogConfig) bool {
	if cfg == nil {
		return true
	}
	if strings.TrimSpace(driver) != strings.TrimSpace(cfg.Driver) {
		return false
	}
	actual := normalizeLogOptions(options)
	expected := normalizeLogOptions(cfg.Options)
	if len(actual) != len(expected) {
		return false
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func normalizeLogOptions(options map[string]string) map[string]string {
	if len(options) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(options))
	for key, value := range options {
		normalized[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return normalized
}

// CloneLogConfig returns a deep copy of cfg.
func CloneLogConfig(cfg *LogConfig) *LogConfig {
	if cfg == nil {
		return nil
	}
	cloned := *cfg
	if len(cfg.Options) > 0 {
		cloned.Options = make(map[string]string, len(cfg.Options))
		for key, value := range cfg.Options {
			cloned.Options[key] = value
		}
	} else {
		cloned.Options = nil
	}
	return &cloned
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

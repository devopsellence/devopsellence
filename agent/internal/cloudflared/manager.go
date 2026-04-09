package cloudflared

import (
	"context"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	cerrdefs "github.com/containerd/errdefs"
)

type Config struct {
	Image          string
	ContainerName  string
	NetworkName    string
	TunnelToken    string
	Healthcheck    *engine.Healthcheck
	StartupTimeout time.Duration
	RestartPolicy  string
}

const (
	defaultImage         = "docker.io/cloudflare/cloudflared@sha256:404528c1cd63c3eb882c257ae524919e4376115e6fe57befca8d603656a91a4c"
	defaultContainerName = "cloudflared"
	defaultRestartPolicy = "unless-stopped"
)

func DefaultImageRef() string {
	return defaultImage
}

type Manager struct {
	engine engine.Engine
	config Config
	logger *slog.Logger

	lastAppliedFingerprint string
}

func New(engine engine.Engine, config Config, logger *slog.Logger) *Manager {
	if config.Image == "" {
		config.Image = defaultImage
	}
	if config.ContainerName == "" {
		config.ContainerName = defaultContainerName
	}
	if config.RestartPolicy == "" {
		config.RestartPolicy = defaultRestartPolicy
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 10 * time.Second
	}
	if config.Healthcheck == nil {
		config.Healthcheck = defaultHealthcheck()
	}
	return &Manager{
		engine: engine,
		config: config,
		logger: logger,
	}
}

func (m *Manager) Reconcile(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	token := strings.TrimSpace(m.config.TunnelToken)
	fingerprint := ""
	if ingress != nil {
		token = strings.TrimSpace(ingress.TunnelToken)
		fingerprint = ingressFingerprint(ingress)
	}
	if token == "" {
		m.lastAppliedFingerprint = ""
		return m.ensureAbsent(ctx)
	}
	if fingerprint == "" {
		fingerprint = "static-token"
	}

	if err := m.engine.EnsureNetwork(ctx, m.config.NetworkName); err != nil {
		return fmt.Errorf("ensure network: %w", err)
	}
	if err := m.ensureImage(ctx); err != nil {
		return err
	}

	info, err := m.engine.Inspect(ctx, m.config.ContainerName)
	if err != nil {
		if !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("inspect cloudflared: %w", err)
		}
		if err := m.createCloudflared(ctx, token); err != nil {
			return err
		}
		if err := m.waitReady(ctx); err != nil {
			return err
		}
		m.lastAppliedFingerprint = fingerprint
		m.logger.Info("cloudflared started", "name", m.config.ContainerName)
		return nil
	}
	if info.Running && m.config.Healthcheck != nil && !info.HasHealthcheck {
		if err := m.engine.Remove(ctx, m.config.ContainerName); err != nil {
			return fmt.Errorf("remove cloudflared (missing healthcheck): %w", err)
		}
		info.Running = false
	}

	if info.Running && m.lastAppliedFingerprint == fingerprint {
		return nil
	}

	if err := m.engine.Remove(ctx, m.config.ContainerName); err != nil {
		if !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove cloudflared: %w", err)
		}
	}
	if err := m.createCloudflared(ctx, token); err != nil {
		return err
	}
	if err := m.waitReady(ctx); err != nil {
		return err
	}
	m.lastAppliedFingerprint = fingerprint
	m.logger.Info("cloudflared started", "name", m.config.ContainerName)
	return nil
}

func (m *Manager) ensureImage(ctx context.Context) error {
	return engine.EnsureImage(ctx, m.engine, m.config.Image)
}

func (m *Manager) ensureAbsent(ctx context.Context) error {
	err := m.engine.Remove(ctx, m.config.ContainerName)
	if err == nil || cerrdefs.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("remove cloudflared: %w", err)
}

func (m *Manager) createCloudflared(ctx context.Context, tunnelToken string) error {
	cmd := []string{"tunnel", "--no-autoupdate", "run"}
	env := map[string]string{}

	env["TUNNEL_TOKEN"] = tunnelToken

	spec := engine.ContainerSpec{
		Name:    m.config.ContainerName,
		Image:   m.config.Image,
		Command: cmd,
		Env:     env,
		Labels: map[string]string{
			engine.LabelSystem: "cloudflared",
		},
		Network: m.config.NetworkName,
		Health:  m.config.Healthcheck,
		Restart: engine.RestartPolicyFromString(m.config.RestartPolicy),
	}

	if err := m.engine.CreateAndStart(ctx, spec); err != nil {
		return fmt.Errorf("create cloudflared: %w", err)
	}

	return nil
}

func (m *Manager) waitReady(ctx context.Context) error {
	return engine.WaitReady(ctx, m.engine, m.config.ContainerName, "cloudflared", m.config.StartupTimeout, m.config.Healthcheck != nil)
}

func defaultHealthcheck() *engine.Healthcheck {
	return &engine.Healthcheck{
		Test:        []string{"CMD", "cloudflared", "--version"},
		Interval:    2 * time.Second,
		Timeout:     2 * time.Second,
		StartPeriod: 1 * time.Second,
		Retries:     3,
	}
}

func ingressFingerprint(ingress *desiredstatepb.Ingress) string {
	if ingress == nil {
		return ""
	}
	return strings.TrimSpace(ingress.Hostname) + "|" + strings.TrimSpace(ingress.TunnelToken)
}

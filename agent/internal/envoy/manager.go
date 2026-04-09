package envoy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	cerrdefs "github.com/containerd/errdefs"
)

const (
	ingressModeTunnel    = "tunnel"
	ingressModeDirectDNS = "direct_dns"
)

type Config struct {
	Image           string
	ContainerName   string
	NetworkName     string
	BootstrapPath   string
	SocketUID       int
	SocketGID       int
	Port            uint16
	PublicHTTPPort  uint16
	PublicHTTPSPort uint16
	TLSCertPath     string
	TLSKeyPath      string
	ClusterName     string
	Healthcheck     *engine.Healthcheck
	StartupTimeout  time.Duration
	RestartPolicy   string
	RouteTimeout    time.Duration
	RouteInterval   time.Duration
	HTTPClient      *http.Client
}

type Manager struct {
	engine          engine.Engine
	config          Config
	logger          *slog.Logger
	xds             *xdsServer
	http            *http.Client
	lastIngress     *desiredstatepb.Ingress
	lastEndpoint    *endpointState
	snapshotVersion atomic.Int64
}

func New(engine engine.Engine, config Config, logger *slog.Logger) *Manager {
	if config.PublicHTTPPort == 0 {
		config.PublicHTTPPort = 8080
	}
	if config.PublicHTTPSPort == 0 {
		config.PublicHTTPSPort = 8443
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 10 * time.Second
	}
	if config.RouteTimeout <= 0 {
		config.RouteTimeout = 10 * time.Second
	}
	if config.RouteInterval <= 0 {
		config.RouteInterval = 250 * time.Millisecond
	}
	if config.SocketUID == 0 && config.SocketGID == 0 {
		config.SocketUID = os.Getuid()
		config.SocketGID = os.Getgid()
	}
	if config.Healthcheck == nil {
		config.Healthcheck = defaultHealthcheck()
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Second}
	}
	return &Manager{
		engine: engine,
		config: config,
		logger: logger,
		xds:    newXDSServer(logger, config.SocketUID, config.SocketGID),
		http:   httpClient,
	}
}

func (m *Manager) Ensure(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	directDNSListener, err := m.directDNSListenerConfig(ingress)
	if err != nil {
		return err
	}

	// Start the xDS server once (idempotent); binds a Unix socket alongside the bootstrap.
	socketPath := xdsSocketPath(m.config.BootstrapPath)
	if err := m.xds.Start(ctx, socketPath); err != nil {
		return fmt.Errorf("start xds server: %w", err)
	}

	// Push current snapshot so Envoy gets config immediately on connect.
	// If lastEndpoint is nil (no web service yet), Envoy will get an empty EDS;
	// UpdateEDS will push the full snapshot once the web container becomes healthy.
	if err := m.applySnapshot(directDNSListener); err != nil {
		return err
	}

	if err := m.engine.EnsureNetwork(ctx, m.config.NetworkName); err != nil {
		return fmt.Errorf("ensure network: %w", err)
	}

	bootstrapChanged, err := ensureBootstrap(m.config.BootstrapPath, m.config.NetworkName)
	if err != nil {
		return err
	}
	if err := m.ensureImage(ctx); err != nil {
		return err
	}

	info, err := m.engine.Inspect(ctx, m.config.ContainerName)
	if err != nil {
		if !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("inspect envoy: %w", err)
		}
		if err := m.createEnvoy(ctx, ingress); err != nil {
			return err
		}
		if err := m.waitReady(ctx); err != nil {
			return err
		}
		m.lastIngress = cloneIngress(ingress)
		m.logger.Info("envoy started", "name", m.config.ContainerName, "port", m.config.Port)
		return nil
	}

	if info.Running && info.PublishHostPort != publishHostPortForIngress(ingress) {
		if err := m.engine.Remove(ctx, m.config.ContainerName); err != nil {
			return fmt.Errorf("remove envoy (publish mode changed): %w", err)
		}
		info.Running = false
	}
	if info.Running && m.config.Healthcheck != nil && !info.HasHealthcheck {
		if err := m.engine.Remove(ctx, m.config.ContainerName); err != nil {
			return fmt.Errorf("remove envoy (missing healthcheck): %w", err)
		}
		info.Running = false
	}
	if info.Running && bootstrapChanged {
		if err := m.engine.Remove(ctx, m.config.ContainerName); err != nil {
			return fmt.Errorf("remove envoy (bootstrap changed): %w", err)
		}
		info.Running = false
	}

	if info.Running {
		m.lastIngress = cloneIngress(ingress)
		return nil
	}

	if err := m.engine.Remove(ctx, m.config.ContainerName); err != nil {
		if !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("remove envoy: %w", err)
		}
	}
	if err := m.createEnvoy(ctx, ingress); err != nil {
		return err
	}
	if err := m.waitReady(ctx); err != nil {
		return err
	}
	m.lastIngress = cloneIngress(ingress)
	m.logger.Info("envoy started", "name", m.config.ContainerName, "port", m.config.Port)
	return nil
}

func (m *Manager) ensureImage(ctx context.Context) error {
	return engine.EnsureImage(ctx, m.engine, m.config.Image)
}

// UpdateEDS pushes a new xDS snapshot that routes traffic to the given endpoint.
func (m *Manager) UpdateEDS(ctx context.Context, address string, port uint16) error {
	m.lastEndpoint = &endpointState{address: address, port: port}
	directDNSListener, err := m.directDNSListenerConfig(m.lastIngress)
	if err != nil {
		return err
	}
	return m.applySnapshot(directDNSListener)
}

func (m *Manager) WaitForRoute(ctx context.Context, path string) error {
	if err := m.waitForRouteOnce(ctx, path); err == nil {
		return nil
	} else {
		m.logger.Warn("envoy route did not become ready; restarting envoy once", "error", err)
		if restartErr := m.restart(ctx); restartErr != nil {
			return fmt.Errorf("envoy route not ready (%v); restart failed: %w", err, restartErr)
		}
		if retryErr := m.waitForRouteOnce(ctx, path); retryErr != nil {
			return fmt.Errorf("envoy route still not ready after restart: %w", retryErr)
		}
		return nil
	}
}

func (m *Manager) applySnapshot(directDNS *directDNSListenerConfig) error {
	version := fmt.Sprintf("%d", m.snapshotVersion.Add(1))
	snap, err := buildSnapshot(version, snapshotParams{
		port:        m.config.Port,
		clusterName: m.config.ClusterName,
		directDNS:   directDNS,
		endpoint:    m.lastEndpoint,
	})
	if err != nil {
		return fmt.Errorf("build xds snapshot: %w", err)
	}
	if err := m.xds.Apply(snap); err != nil {
		return fmt.Errorf("apply xds snapshot: %w", err)
	}
	return nil
}

func (m *Manager) createEnvoy(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	bootstrapDir := dirOf(m.config.BootstrapPath)
	mounts := []string{fmt.Sprintf("%s:%s:ro", bootstrapDir, bootstrapDir)}
	for _, path := range []string{m.config.TLSCertPath, m.config.TLSKeyPath} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		dir := dirOf(path)
		if dir == bootstrapDir || containsMount(mounts, dir) {
			continue
		}
		mounts = append(mounts, fmt.Sprintf("%s:%s:ro", dir, dir))
	}

	spec := engine.ContainerSpec{
		Name:    m.config.ContainerName,
		Image:   m.config.Image,
		Command: []string{"-c", m.config.BootstrapPath, "--log-level", "warning", "--log-path", "/dev/stderr"},
		Labels: map[string]string{
			engine.LabelSystem: "envoy",
		},
		Network: m.config.NetworkName,
		Binds:   mounts,
		Health:  m.config.Healthcheck,
		Restart: engine.RestartPolicyFromString(m.config.RestartPolicy),
	}
	spec.Ports = portBindingsForIngress(m.config.Port, ingress)

	if err := m.engine.CreateAndStart(ctx, spec); err != nil {
		return fmt.Errorf("create envoy: %w", err)
	}

	return nil
}

func (m *Manager) waitReady(ctx context.Context) error {
	return engine.WaitReady(ctx, m.engine, m.config.ContainerName, "envoy", m.config.StartupTimeout, m.config.Healthcheck != nil)
}

func (m *Manager) waitForRouteOnce(ctx context.Context, path string) error {
	info, err := m.engine.Inspect(ctx, m.config.ContainerName)
	if err != nil {
		return fmt.Errorf("inspect envoy: %w", err)
	}
	ip := info.NetworkIP[m.config.NetworkName]
	if ip == "" {
		return fmt.Errorf("envoy missing network ip on %s", m.config.NetworkName)
	}

	target := "http://" + net.JoinHostPort(ip, strconv.Itoa(int(m.config.Port))) + normalizedRoutePath(path)
	deadline := time.Now().Add(m.config.RouteTimeout)
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return fmt.Errorf("build route probe: %w", err)
		}
		resp, err := m.http.Do(req)
		if err != nil {
			lastErr = err
		} else {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256))
			resp.Body.Close()
			if readErr != nil {
				lastErr = fmt.Errorf("read route probe body: %w", readErr)
			} else if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				return nil
			} else {
				detail := strings.TrimSpace(string(body))
				if detail != "" {
					lastErr = fmt.Errorf("http %d: %s", resp.StatusCode, detail)
				} else {
					lastErr = fmt.Errorf("http %d", resp.StatusCode)
				}
			}
		}

		if time.Now().After(deadline) {
			break
		}

		timer := time.NewTimer(m.config.RouteInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("route probe timed out")
	}
	return fmt.Errorf("wait for envoy route %s: %w", target, lastErr)
}

func (m *Manager) restart(ctx context.Context) error {
	if err := m.engine.Remove(ctx, m.config.ContainerName); err != nil {
		return fmt.Errorf("remove envoy for restart: %w", err)
	}
	if err := m.createEnvoy(ctx, m.lastIngress); err != nil {
		return err
	}
	if err := m.waitReady(ctx); err != nil {
		return err
	}
	m.logger.Info("envoy restarted", "name", m.config.ContainerName, "port", m.config.Port)
	return nil
}

func defaultHealthcheck() *engine.Healthcheck {
	return &engine.Healthcheck{
		Test:        []string{"CMD", "envoy", "--version"},
		Interval:    2 * time.Second,
		Timeout:     2 * time.Second,
		StartPeriod: 1 * time.Second,
		Retries:     3,
	}
}

func normalizedRoutePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "/"
	}
	if strings.HasPrefix(trimmed, "/") {
		return trimmed
	}
	return "/" + trimmed
}

func (m *Manager) directDNSListenerConfig(ingress *desiredstatepb.Ingress) (*directDNSListenerConfig, error) {
	if normalizedIngressMode(ingress) != ingressModeDirectDNS {
		return nil, nil
	}
	if strings.TrimSpace(m.config.TLSCertPath) == "" {
		return nil, fmt.Errorf("direct_dns ingress requires --envoy-tls-cert-path")
	}
	if strings.TrimSpace(m.config.TLSKeyPath) == "" {
		return nil, fmt.Errorf("direct_dns ingress requires --envoy-tls-key-path")
	}
	certificatePEM, err := os.ReadFile(m.config.TLSCertPath)
	if err != nil {
		return nil, fmt.Errorf("read direct_dns tls cert: %w", err)
	}
	privateKeyPEM, err := os.ReadFile(m.config.TLSKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read direct_dns tls key: %w", err)
	}

	return &directDNSListenerConfig{
		HTTPPort:       m.config.PublicHTTPPort,
		HTTPSPort:      m.config.PublicHTTPSPort,
		CertificatePEM: certificatePEM,
		PrivateKeyPEM:  privateKeyPEM,
	}, nil
}

func publishHostPortForIngress(ingress *desiredstatepb.Ingress) bool {
	return len(portBindingsForIngress(0, ingress)) > 0
}

func portBindingsForIngress(defaultPort uint16, ingress *desiredstatepb.Ingress) []engine.PortBinding {
	switch normalizedIngressMode(ingress) {
	case ingressModeDirectDNS:
		return []engine.PortBinding{
			{
				ContainerPort: 8080,
				HostPort:      80,
				Protocol:      "tcp",
			},
			{
				ContainerPort: 8443,
				HostPort:      443,
				Protocol:      "tcp",
			},
		}
	case ingressModeTunnel:
		if ingress != nil {
			return nil
		}
		if defaultPort == 0 {
			return nil
		}
		return []engine.PortBinding{{
			ContainerPort: defaultPort,
			HostPort:      defaultPort,
			Protocol:      "tcp",
		}}
	default:
		return nil
	}
}

func dirOf(path string) string {
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[:idx]
	}
	return "."
}

func containsMount(mounts []string, dir string) bool {
	needle := fmt.Sprintf("%s:%s:ro", dir, dir)
	for _, mount := range mounts {
		if mount == needle {
			return true
		}
	}
	return false
}

func cloneIngress(ingress *desiredstatepb.Ingress) *desiredstatepb.Ingress {
	if ingress == nil {
		return nil
	}

	copy := *ingress
	return &copy
}

func normalizedIngressMode(ingress *desiredstatepb.Ingress) string {
	if ingress == nil {
		return ingressModeTunnel
	}

	mode := strings.TrimSpace(ingress.Mode)
	if mode == "" {
		return ingressModeTunnel
	}
	return mode
}

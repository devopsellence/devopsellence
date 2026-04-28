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

	cerrdefs "github.com/containerd/errdefs"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
)

const (
	ingressModeInternal = "internal"
	ingressModePublic   = "public"
)

type Config struct {
	Image               string
	ContainerName       string
	NetworkName         string
	BootstrapPath       string
	SocketUID           int
	SocketGID           int
	Port                uint16
	PublicHTTPPort      uint16
	PublicHTTPSPort     uint16
	PublicHTTPHostPort  uint16
	PublicHTTPSHostPort uint16
	TLSCertPath         string
	TLSKeyPath          string
	ClusterName         string
	ChallengeHost       string
	ChallengePort       uint16
	Healthcheck         *engine.Healthcheck
	StartupTimeout      time.Duration
	RestartPolicy       string
	RouteTimeout        time.Duration
	RouteInterval       time.Duration
	HTTPClient          *http.Client
}

type Manager struct {
	engine          engine.Engine
	config          Config
	logger          *slog.Logger
	xds             *xdsServer
	http            *http.Client
	lastIngress     *desiredstatepb.Ingress
	lastEndpoint    *endpointState
	lastEndpoints   map[string]*endpointState
	snapshotVersion atomic.Int64
}

func New(engine engine.Engine, config Config, logger *slog.Logger) *Manager {
	if config.PublicHTTPPort == 0 {
		config.PublicHTTPPort = 8080
	}
	if config.PublicHTTPSPort == 0 {
		config.PublicHTTPSPort = 8443
	}
	if config.PublicHTTPHostPort == 0 {
		config.PublicHTTPHostPort = 80
	}
	if config.PublicHTTPSHostPort == 0 {
		config.PublicHTTPSHostPort = 443
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
	if config.ChallengeHost == "" {
		config.ChallengeHost = "host.docker.internal"
	}
	if config.ChallengePort == 0 {
		config.ChallengePort = 15980
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Second}
	}
	return &Manager{
		engine:        engine,
		config:        config,
		logger:        logger,
		xds:           newXDSServer(logger, config.SocketUID, config.SocketGID),
		http:          httpClient,
		lastEndpoints: map[string]*endpointState{},
	}
}

func (m *Manager) Ensure(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	publicIngressListener, err := m.publicIngressListenerConfig(ingress)
	if err != nil {
		return err
	}
	desiredPorts := portBindingsForIngress(m.config.Port, m.config.PublicHTTPHostPort, m.config.PublicHTTPSHostPort, ingress, publicIngressListener)

	// Start the xDS server once (idempotent); binds a Unix socket alongside the bootstrap.
	socketPath := xdsSocketPath(m.config.BootstrapPath)
	if err := m.xds.Start(ctx, socketPath); err != nil {
		return fmt.Errorf("start xds server: %w", err)
	}

	// Push current snapshot so Envoy gets config immediately on connect.
	// If lastEndpoint is nil (no web service yet), Envoy will get an empty EDS;
	// UpdateEDS will push the full snapshot once the web container becomes healthy.
	if err := m.applySnapshot(publicIngressListener); err != nil {
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
		if err := m.createEnvoy(ctx, ingress, desiredPorts, publicIngressListener != nil); err != nil {
			return err
		}
		if err := m.waitReady(ctx); err != nil {
			return err
		}
		m.lastIngress = cloneIngress(ingress)
		m.logger.Info("envoy started", "name", m.config.ContainerName, "port", m.config.Port)
		return nil
	}

	if info.Running && !samePublishedPorts(info.PublishedPorts, desiredPorts) {
		if err := m.engine.Remove(ctx, m.config.ContainerName); err != nil {
			return fmt.Errorf("remove envoy (published ports changed): %w", err)
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
	if err := m.createEnvoy(ctx, ingress, desiredPorts, publicIngressListener != nil); err != nil {
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
	return m.UpdateClusterEDS(ctx, m.config.ClusterName, address, port)
}

func (m *Manager) UpdateClusterEDS(ctx context.Context, clusterName string, address string, port uint16) error {
	if strings.TrimSpace(clusterName) == "" {
		clusterName = m.config.ClusterName
	}
	endpoint := &endpointState{address: address, port: port}
	m.lastEndpoint = endpoint
	m.lastEndpoints[clusterName] = endpoint
	if m.shouldMirrorToDefaultCluster(clusterName) {
		m.lastEndpoints[m.config.ClusterName] = endpoint
	}
	publicIngressListener, err := m.publicIngressListenerConfig(m.lastIngress)
	if err != nil {
		return err
	}
	return m.applySnapshot(publicIngressListener)
}

func (m *Manager) shouldMirrorToDefaultCluster(clusterName string) bool {
	if clusterName == m.config.ClusterName {
		return true
	}
	return m.lastIngress == nil || len(m.lastIngress.Routes) == 0
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

func (m *Manager) applySnapshot(publicIngress *publicIngressListenerConfig) error {
	version := fmt.Sprintf("%d", m.snapshotVersion.Add(1))
	snap, err := buildSnapshot(version, snapshotParams{
		port:          m.config.Port,
		clusterName:   m.config.ClusterName,
		publicIngress: publicIngress,
		endpoints:     m.snapshotEndpoints(),
	})
	if err != nil {
		return fmt.Errorf("build xds snapshot: %w", err)
	}
	if err := m.xds.Apply(snap); err != nil {
		return fmt.Errorf("apply xds snapshot: %w", err)
	}
	return nil
}

func (m *Manager) snapshotEndpoints() map[string]*endpointState {
	endpoints := map[string]*endpointState{}
	if endpoint := m.lastEndpoints[m.config.ClusterName]; endpoint != nil {
		endpoints[m.config.ClusterName] = endpoint
	} else if m.lastEndpoint != nil {
		endpoints[m.config.ClusterName] = m.lastEndpoint
	}
	if m.lastIngress != nil {
		for _, route := range m.lastIngress.Routes {
			clusterName := ingressRouteClusterName(route)
			if clusterName == "" {
				continue
			}
			if endpoint := m.lastEndpoints[clusterName]; endpoint != nil {
				endpoints[clusterName] = endpoint
			}
		}
	}
	if len(endpoints) == 0 {
		m.lastEndpoints = map[string]*endpointState{}
		return nil
	}
	m.lastEndpoints = endpoints
	cloned := make(map[string]*endpointState, len(endpoints))
	for cluster, endpoint := range endpoints {
		cloned[cluster] = endpoint
	}
	return cloned
}

func (m *Manager) createEnvoy(ctx context.Context, ingress *desiredstatepb.Ingress, desiredPorts []engine.PortBinding, publicIngressEnabled bool) error {
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
	if publicIngressEnabled {
		spec.ExtraHosts = []string{"host.docker.internal:host-gateway"}
	}
	spec.Ports = desiredPorts

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
	publicIngressListener, err := m.publicIngressListenerConfig(m.lastIngress)
	if err != nil {
		return err
	}
	desiredPorts := portBindingsForIngress(m.config.Port, m.config.PublicHTTPHostPort, m.config.PublicHTTPSHostPort, m.lastIngress, publicIngressListener)
	if err := m.createEnvoy(ctx, m.lastIngress, desiredPorts, publicIngressListener != nil); err != nil {
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

func (m *Manager) publicIngressListenerConfig(ingress *desiredstatepb.Ingress) (*publicIngressListenerConfig, error) {
	switch normalizedIngressMode(ingress) {
	case ingressModePublic:
	default:
		return nil, nil
	}
	tlsMode := ingressTLSMode(ingress)
	tlsDesired := tlsMode != "off"
	tlsEnabled := false
	challengeEnabled := tlsMode == "auto"
	var certificatePEM []byte
	var privateKeyPEM []byte
	if tlsDesired {
		if strings.TrimSpace(m.config.TLSCertPath) == "" {
			return nil, fmt.Errorf("public ingress requires --envoy-tls-cert-path")
		}
		if strings.TrimSpace(m.config.TLSKeyPath) == "" {
			return nil, fmt.Errorf("public ingress requires --envoy-tls-key-path")
		}
		var err error
		certificatePEM, err = os.ReadFile(m.config.TLSCertPath)
		if err != nil {
			if tlsMode == "auto" && os.IsNotExist(err) {
				return &publicIngressListenerConfig{
					HTTPPort:         m.config.PublicHTTPPort,
					HTTPSPort:        m.config.PublicHTTPSPort,
					Hosts:            ingressHosts(ingress),
					TLSEnabled:       false,
					ChallengeEnabled: challengeEnabled,
					RedirectHTTP:     false,
					ChallengeHost:    m.config.ChallengeHost,
					ChallengePort:    m.config.ChallengePort,
					Routes:           cloneIngressRoutes(ingress),
				}, nil
			}
			return nil, fmt.Errorf("read public ingress tls cert: %w", err)
		}
		privateKeyPEM, err = os.ReadFile(m.config.TLSKeyPath)
		if err != nil {
			if tlsMode == "auto" && os.IsNotExist(err) {
				return &publicIngressListenerConfig{
					HTTPPort:         m.config.PublicHTTPPort,
					HTTPSPort:        m.config.PublicHTTPSPort,
					Hosts:            ingressHosts(ingress),
					TLSEnabled:       false,
					ChallengeEnabled: challengeEnabled,
					RedirectHTTP:     false,
					ChallengeHost:    m.config.ChallengeHost,
					ChallengePort:    m.config.ChallengePort,
					Routes:           cloneIngressRoutes(ingress),
				}, nil
			}
			return nil, fmt.Errorf("read public ingress tls key: %w", err)
		}
		tlsEnabled = true
	}

	return &publicIngressListenerConfig{
		HTTPPort:         m.config.PublicHTTPPort,
		HTTPSPort:        m.config.PublicHTTPSPort,
		Hosts:            ingressHosts(ingress),
		TLSEnabled:       tlsEnabled,
		ChallengeEnabled: challengeEnabled,
		RedirectHTTP:     tlsEnabled && ingress.GetRedirectHttp(),
		ChallengeHost:    m.config.ChallengeHost,
		ChallengePort:    m.config.ChallengePort,
		CertificatePEM:   certificatePEM,
		PrivateKeyPEM:    privateKeyPEM,
		Routes:           cloneIngressRoutes(ingress),
	}, nil
}

func cloneIngressRoutes(ingress *desiredstatepb.Ingress) []*desiredstatepb.IngressRoute {
	if ingress == nil || len(ingress.Routes) == 0 {
		return nil
	}
	return append([]*desiredstatepb.IngressRoute(nil), ingress.Routes...)
}

func portBindingsForIngress(defaultPort, httpHostPort, httpsHostPort uint16, ingress *desiredstatepb.Ingress, publicIngress *publicIngressListenerConfig) []engine.PortBinding {
	switch normalizedIngressMode(ingress) {
	case ingressModePublic:
		ports := []engine.PortBinding{{
			ContainerPort: 8080,
			HostPort:      httpHostPort,
			Protocol:      "tcp",
		}}
		if publicIngress != nil && publicIngress.TLSEnabled {
			ports = append(ports, engine.PortBinding{
				ContainerPort: 8443,
				HostPort:      httpsHostPort,
				Protocol:      "tcp",
			})
		}
		return ports
	case ingressModeInternal:
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

func samePublishedPorts(current, desired []engine.PortBinding) bool {
	if len(current) != len(desired) {
		return false
	}
	currentCounts := map[string]int{}
	for _, port := range current {
		currentCounts[portBindingKey(port)]++
	}
	for _, port := range desired {
		key := portBindingKey(port)
		if currentCounts[key] == 0 {
			return false
		}
		currentCounts[key]--
	}
	for _, count := range currentCounts {
		if count != 0 {
			return false
		}
	}
	return true
}

func portBindingKey(port engine.PortBinding) string {
	proto := strings.TrimSpace(port.Protocol)
	if proto == "" {
		proto = "tcp"
	}
	return fmt.Sprintf("%d:%d/%s", port.HostPort, port.ContainerPort, proto)
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
		return ingressModeInternal
	}

	mode := strings.TrimSpace(ingress.Mode)
	if mode != "" {
		return mode
	}
	return ingressModePublic
}

func ingressTLSMode(ingress *desiredstatepb.Ingress) string {
	if ingress == nil || ingress.Tls == nil {
		return "auto"
	}
	mode := strings.TrimSpace(ingress.Tls.Mode)
	if mode == "" {
		return "auto"
	}
	return mode
}

func ingressHosts(ingress *desiredstatepb.Ingress) []string {
	if ingress == nil {
		return nil
	}
	seen := map[string]bool{}
	hosts := []string{}
	for _, host := range ingress.Hosts {
		host = strings.TrimSpace(host)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	return hosts
}

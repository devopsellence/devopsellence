package envoy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	hcmv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	"google.golang.org/protobuf/encoding/protojson"
)

type fakeEngine struct {
	inspectInfo engine.ContainerInfo
	inspectErr  error

	createdSpec  *engine.ContainerSpec
	removed      []string
	createHealth string
	imageExists  bool
	pulledImage  string
	pullErr      error
}

func (f *fakeEngine) ListManaged(ctx context.Context) ([]engine.ContainerState, error) {
	return nil, nil
}

func (f *fakeEngine) CreateAndStart(ctx context.Context, spec engine.ContainerSpec) error {
	f.createdSpec = &spec
	health := "healthy"
	if f.createHealth != "" {
		health = f.createHealth
	}
	networkIP := "172.18.0.2"
	if existing := f.inspectInfo.NetworkIP[spec.Network]; existing != "" {
		networkIP = existing
	}
	f.inspectErr = nil
	f.inspectInfo = engine.ContainerInfo{
		Name:            spec.Name,
		Running:         true,
		Health:          health,
		HasHealthcheck:  spec.Health != nil,
		PublishHostPort: len(spec.Ports) > 0,
		PublishedPorts:  append([]engine.PortBinding(nil), spec.Ports...),
		NetworkIP:       map[string]string{spec.Network: networkIP},
	}
	return nil
}

func (f *fakeEngine) Start(ctx context.Context, name string) error {
	f.inspectErr = nil
	f.inspectInfo.Running = true
	return nil
}

func (f *fakeEngine) Wait(ctx context.Context, name string) (int64, error) {
	return 0, nil
}

func (f *fakeEngine) Stop(ctx context.Context, name string, timeout time.Duration) error {
	return nil
}

func (f *fakeEngine) Remove(ctx context.Context, name string) error {
	f.removed = append(f.removed, name)
	return nil
}

func (f *fakeEngine) ImageExists(ctx context.Context, image string) (bool, error) {
	return f.imageExists, nil
}

func (f *fakeEngine) PullImage(ctx context.Context, image string, auth *engine.RegistryAuth) error {
	f.pulledImage = image
	return f.pullErr
}

func (f *fakeEngine) Inspect(ctx context.Context, name string) (engine.ContainerInfo, error) {
	if f.inspectErr != nil {
		return engine.ContainerInfo{}, f.inspectErr
	}
	return f.inspectInfo, nil
}

func (f *fakeEngine) Logs(_ context.Context, _ string, _ int) ([]byte, error) {
	return nil, nil
}

func (f *fakeEngine) EnsureNetwork(ctx context.Context, name string) error {
	return nil
}

func tempBootstrapPath(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "envoy")
	return filepath.Join(dir, "envoy.yaml")
}

func TestEnsureCreatesEnvoyWithDefaults(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		LogConfig:      &engine.LogConfig{Driver: "json-file", Options: map[string]string{"max-size": "10m", "max-file": "5"}},
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called")
	}
	if len(eng.createdSpec.Entrypoint) != 0 {
		t.Fatalf("unexpected entrypoint override: %+v", eng.createdSpec.Entrypoint)
	}
	cmd := strings.Join(eng.createdSpec.Command, " ")
	if !strings.Contains(cmd, "--log-level warning") || !strings.Contains(cmd, "--log-path /dev/stderr") {
		t.Fatalf("unexpected command: %s", cmd)
	}
	if eng.createdSpec.Health == nil || len(eng.createdSpec.Health.Test) == 0 {
		t.Fatal("expected default healthcheck")
	}
	if eng.createdSpec.Restart == nil || eng.createdSpec.Restart.Name != "unless-stopped" {
		t.Fatalf("unexpected restart policy: %+v", eng.createdSpec.Restart)
	}
	if eng.createdSpec.Labels[engine.LabelManaged] != "true" || eng.createdSpec.Labels[engine.LabelSystem] != "envoy" {
		t.Fatalf("unexpected labels: %#v", eng.createdSpec.Labels)
	}
	if eng.createdSpec.Log == nil || eng.createdSpec.Log.Options["max-size"] != "10m" || eng.createdSpec.Log.Options["max-file"] != "5" {
		t.Fatalf("unexpected log config: %#v", eng.createdSpec.Log)
	}
	if len(eng.createdSpec.Ports) != 1 {
		t.Fatalf("expected envoy host port published, got %+v", eng.createdSpec.Ports)
	}
	if eng.pulledImage != "docker.io/envoyproxy/envoy:v1.37.0" {
		t.Fatalf("expected image pull, got %q", eng.pulledImage)
	}
	data, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	contents := string(data)
	if !strings.Contains(contents, "dynamic_resources:") {
		t.Fatalf("expected dynamic_resources in bootstrap, got %s", contents)
	}
	if !strings.Contains(contents, "xds_cluster") {
		t.Fatalf("expected xds_cluster in bootstrap, got %s", contents)
	}
	if !strings.Contains(contents, "cluster: devopsellence") {
		t.Fatalf("expected cluster in bootstrap, got %s", contents)
	}
	if !strings.Contains(contents, "xds.sock") {
		t.Fatalf("expected xds socket path in bootstrap, got %s", contents)
	}
	if !strings.Contains(contents, "pipe:") {
		t.Fatalf("expected pipe address in bootstrap, got %s", contents)
	}
}

func TestEnsureSkipsHostPortPublishWhenDisabled(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), &desiredstatepb.Ingress{Hosts: []string{"abc123.devopsellence.io"}, TunnelToken: "tok"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called")
	}
	if len(eng.createdSpec.Ports) != 0 {
		t.Fatalf("expected envoy host port publish disabled, got %+v", eng.createdSpec.Ports)
	}
}

func TestEnsureRecreatesWhenMissingHealthcheck(t *testing.T) {
	eng := &fakeEngine{
		inspectInfo: engine.ContainerInfo{
			Name:           "devopsellence-envoy",
			Running:        true,
			Health:         "",
			HasHealthcheck: false,
		},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(eng.removed) == 0 {
		t.Fatal("expected envoy removal to recreate with healthcheck")
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called")
	}
}

func TestEnsureRecreatesWhenPublishedPortsChange(t *testing.T) {
	eng := &fakeEngine{
		inspectInfo: engine.ContainerInfo{
			Name:            "devopsellence-envoy",
			Running:         true,
			Health:          "healthy",
			HasHealthcheck:  true,
			PublishHostPort: true,
			PublishedPorts: []engine.PortBinding{{
				ContainerPort: 8080,
				HostPort:      80,
				Protocol:      "tcp",
			}},
		},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), &desiredstatepb.Ingress{Hosts: []string{"abc123.devopsellence.io"}, TunnelToken: "tok"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(eng.removed) == 0 {
		t.Fatal("expected envoy removal to recreate without host port publish")
	}
	if eng.createdSpec == nil || len(eng.createdSpec.Ports) != 0 {
		t.Fatalf("expected recreated envoy without host port publish, got %+v", eng.createdSpec)
	}
}

func TestEnsureFailsWhenEnvoyUnhealthy(t *testing.T) {
	eng := &fakeEngine{
		inspectErr:   cerrdefs.ErrNotFound,
		createHealth: "unhealthy",
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err == nil {
		t.Fatal("expected error when envoy is unhealthy")
	}
}

func TestEnsureRewritesStaleBootstrap(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	if err := os.MkdirAll(filepath.Dir(bootstrapPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	const customBootstrap = "custom: true\n"
	if err := os.WriteFile(bootstrapPath, []byte(customBootstrap), 0o644); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	data, err := os.ReadFile(bootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if string(data) == customBootstrap {
		t.Fatalf("expected bootstrap to be rewritten")
	}
	if !strings.Contains(string(data), "dynamic_resources:") {
		t.Fatalf("expected dynamic_resources in rewritten bootstrap, got %q", string(data))
	}
}

func TestEnsureRestartsRunningEnvoyWhenBootstrapChanges(t *testing.T) {
	eng := &fakeEngine{
		inspectInfo: engine.ContainerInfo{
			Name:            "devopsellence-envoy",
			Running:         true,
			Health:          "healthy",
			HasHealthcheck:  true,
			PublishHostPort: true,
			NetworkIP:       map[string]string{"devopsellence": "172.18.0.2"},
		},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	if err := os.MkdirAll(filepath.Dir(bootstrapPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(bootstrapPath, []byte("custom: true\n"), 0o644); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(eng.removed) == 0 {
		t.Fatal("expected running envoy to be recreated when bootstrap changes")
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called after bootstrap change")
	}
}

func TestEnsureSkipsPullWhenImageAlreadyPresent(t *testing.T) {
	eng := &fakeEngine{
		inspectErr:  cerrdefs.ErrNotFound,
		imageExists: true,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if eng.pulledImage != "" {
		t.Fatalf("expected no image pull, got %q", eng.pulledImage)
	}
}

func TestEnsureFailsWhenImagePullFails(t *testing.T) {
	eng := &fakeEngine{
		inspectErr: cerrdefs.ErrNotFound,
		pullErr:    context.DeadlineExceeded,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err == nil {
		t.Fatal("expected error when image pull fails")
	}
	if eng.createdSpec != nil {
		t.Fatal("unexpected container create after failed pull")
	}
}

func TestEnsurePublishesConfiguredPublicPorts(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	certPath := filepath.Join(t.TempDir(), "ingress.crt")
	keyPath := filepath.Join(t.TempDir(), "ingress.key")
	if err := os.WriteFile(certPath, []byte("cert"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	mgr := New(eng, Config{
		Image:               "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:       "devopsellence-envoy",
		NetworkName:         "devopsellence",
		BootstrapPath:       bootstrapPath,
		Port:                8000,
		PublicHTTPHostPort:  18080,
		PublicHTTPSHostPort: 18443,
		TLSCertPath:         certPath,
		TLSKeyPath:          keyPath,
		ClusterName:         "devopsellence_web",
		RestartPolicy:       "unless-stopped",
		StartupTimeout:      2 * time.Second,
	}, logger)

	ingress := &desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"abc123.devopsellence.io"},
	}
	if err := mgr.Ensure(context.Background(), ingress); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called")
	}
	if len(eng.createdSpec.Ports) != 2 {
		t.Fatalf("expected host publish on configured ports, got %+v", eng.createdSpec.Ports)
	}
	if eng.createdSpec.Ports[0].ContainerPort != 8080 || eng.createdSpec.Ports[1].ContainerPort != 8443 {
		t.Fatalf("expected container ports 8080/8443, got %+v", eng.createdSpec.Ports)
	}
	if eng.createdSpec.Ports[0].HostPort != 18080 || eng.createdSpec.Ports[1].HostPort != 18443 {
		t.Fatalf("expected host ports 18080/18443, got %+v", eng.createdSpec.Ports)
	}
}

func TestEnsurePublishesPublicPorts(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	certPath := filepath.Join(t.TempDir(), "ingress.crt")
	keyPath := filepath.Join(t.TempDir(), "ingress.key")
	if err := os.WriteFile(certPath, []byte("cert"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		TLSCertPath:    certPath,
		TLSKeyPath:     keyPath,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	ingress := &desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"abc123.devopsellence.io"},
	}
	if err := mgr.Ensure(context.Background(), ingress); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called")
	}
	if len(eng.createdSpec.Ports) != 2 {
		t.Fatalf("expected host publish on 8443, got %+v", eng.createdSpec.Ports)
	}
	if eng.createdSpec.Ports[0].HostPort != 80 || eng.createdSpec.Ports[1].HostPort != 443 {
		t.Fatalf("expected host ports 80/443, got %+v", eng.createdSpec.Ports)
	}
}

func TestEnsureTreatsBlankModeWithoutTunnelTokenAsPublicIngress(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	certPath := filepath.Join(t.TempDir(), "ingress.crt")
	keyPath := filepath.Join(t.TempDir(), "ingress.key")
	if err := os.WriteFile(certPath, []byte("cert"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		TLSCertPath:    certPath,
		TLSKeyPath:     keyPath,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	ingress := &desiredstatepb.Ingress{
		Hosts: []string{"abc123.devopsellence.io"},
	}
	if err := mgr.Ensure(context.Background(), ingress); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called")
	}
	if len(eng.createdSpec.Ports) != 2 {
		t.Fatalf("expected host publish on 80/443, got %+v", eng.createdSpec.Ports)
	}
	if eng.createdSpec.Ports[0].HostPort != 80 || eng.createdSpec.Ports[1].HostPort != 443 {
		t.Fatalf("expected host ports 80/443, got %+v", eng.createdSpec.Ports)
	}
}

func TestEnsureAutoTLSPublishesOnlyHTTPUntilCertificateExists(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		TLSCertPath:    filepath.Join(t.TempDir(), "ingress.crt"),
		TLSKeyPath:     filepath.Join(t.TempDir(), "ingress.key"),
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	ingress := &desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"abc123.devopsellence.io"},
		Tls:   &desiredstatepb.IngressTLS{Mode: "auto"},
	}
	if err := mgr.Ensure(context.Background(), ingress); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called")
	}
	if len(eng.createdSpec.Ports) != 1 || eng.createdSpec.Ports[0].HostPort != 80 {
		t.Fatalf("expected only host port 80 before cert is ready, got %+v", eng.createdSpec.Ports)
	}
}

func TestEnsureRecreatesEnvoyWhenAutoTLSCertificateBecomesReady(t *testing.T) {
	eng := &fakeEngine{
		inspectInfo: engine.ContainerInfo{
			Name:            "devopsellence-envoy",
			Running:         true,
			Health:          "healthy",
			HasHealthcheck:  true,
			PublishHostPort: true,
			PublishedPorts: []engine.PortBinding{{
				ContainerPort: 8080,
				HostPort:      80,
				Protocol:      "tcp",
			}},
			NetworkIP: map[string]string{"devopsellence": "172.18.0.2"},
		},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	certDir := t.TempDir()
	certPath := filepath.Join(certDir, "ingress.crt")
	keyPath := filepath.Join(certDir, "ingress.key")
	if err := os.WriteFile(certPath, []byte("cert"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		TLSCertPath:    certPath,
		TLSKeyPath:     keyPath,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	ingress := &desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"abc123.devopsellence.io"},
		Tls:   &desiredstatepb.IngressTLS{Mode: "auto"},
	}
	if err := mgr.Ensure(context.Background(), ingress); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(eng.removed) == 0 {
		t.Fatal("expected envoy removal to recreate with https publish")
	}
	if eng.createdSpec == nil || len(eng.createdSpec.Ports) != 2 {
		t.Fatalf("expected recreated envoy with ports 80/443, got %+v", eng.createdSpec)
	}
}

func TestPublicListenerConfigLoadsTLSMaterials(t *testing.T) {
	eng := &fakeEngine{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	certPath := filepath.Join(t.TempDir(), "ingress.crt")
	keyPath := filepath.Join(t.TempDir(), "ingress.key")
	if err := os.WriteFile(certPath, []byte("cert-pem"), 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte("key-pem"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	mgr := New(eng, Config{
		Image:         "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName: "devopsellence-envoy",
		NetworkName:   "devopsellence",
		BootstrapPath: bootstrapPath,
		Port:          8000,
		TLSCertPath:   certPath,
		TLSKeyPath:    keyPath,
		ClusterName:   "devopsellence_web",
	}, logger)

	listener, err := mgr.publicIngressListenerConfig(&desiredstatepb.Ingress{Mode: "public", Hosts: []string{"abc123.devopsellence.io"}})
	if err != nil {
		t.Fatalf("public listener config: %v", err)
	}

	if string(listener.CertificatePEM) != "cert-pem" {
		t.Fatalf("unexpected cert pem: %q", string(listener.CertificatePEM))
	}
	if string(listener.PrivateKeyPEM) != "key-pem" {
		t.Fatalf("unexpected key pem: %q", string(listener.PrivateKeyPEM))
	}
}

func TestBuildHTTPSListenerInlinesTLSMaterials(t *testing.T) {
	listener, err := buildHTTPSListener(&publicIngressListenerConfig{
		HTTPSPort:      8443,
		CertificatePEM: []byte("cert-pem"),
		PrivateKeyPEM:  []byte("key-pem"),
	}, "devopsellence_web")
	if err != nil {
		t.Fatalf("build https listener: %v", err)
	}

	encoded, err := protojson.Marshal(listener)
	if err != nil {
		t.Fatalf("marshal listener: %v", err)
	}
	json := string(encoded)
	if !strings.Contains(json, "inlineBytes") {
		t.Fatalf("expected inlineBytes in listener config, got %s", json)
	}
	if strings.Contains(json, "filename") {
		t.Fatalf("expected no filename datasource in listener config, got %s", json)
	}
}

func TestBuildHTTPSListenerPreservesHTTPSMetadataForUpstream(t *testing.T) {
	listener, err := buildHTTPSListener(&publicIngressListenerConfig{
		HTTPSPort:      8443,
		CertificatePEM: []byte("cert-pem"),
		PrivateKeyPEM:  []byte("key-pem"),
	}, "devopsellence_web")
	if err != nil {
		t.Fatalf("build https listener: %v", err)
	}

	hcm := decodeHCM(t, listener)
	if hcm.GetSchemeHeaderTransformation().GetSchemeToOverwrite() != "https" {
		t.Fatalf("expected scheme overwrite https, got %q", hcm.GetSchemeHeaderTransformation().GetSchemeToOverwrite())
	}

	virtualHosts := hcm.GetRouteConfig().GetVirtualHosts()
	if len(virtualHosts) != 1 {
		t.Fatalf("expected one virtual host, got %d", len(virtualHosts))
	}
	headers := virtualHosts[0].GetRequestHeadersToAdd()
	if len(headers) != 1 {
		t.Fatalf("expected one request header override, got %d", len(headers))
	}
	header := headers[0]
	if header.GetHeader().GetKey() != "x-forwarded-proto" {
		t.Fatalf("expected x-forwarded-proto header, got %q", header.GetHeader().GetKey())
	}
	if header.GetHeader().GetValue() != "https" {
		t.Fatalf("expected x-forwarded-proto=https, got %q", header.GetHeader().GetValue())
	}
	if header.GetAppendAction() != corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD {
		t.Fatalf("expected overwrite append action, got %v", header.GetAppendAction())
	}
}

func TestEnsureUpdatesXDSSnapshotWithEndpoint(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	versionBefore := mgr.snapshotVersion.Load()

	if err := mgr.UpdateEDS(context.Background(), "10.0.0.1", 3000); err != nil {
		t.Fatalf("update eds: %v", err)
	}
	if mgr.snapshotVersion.Load() <= versionBefore {
		t.Fatal("expected snapshot version to increment after UpdateEDS")
	}
	if mgr.lastEndpoint == nil || mgr.lastEndpoint.address != "10.0.0.1" || mgr.lastEndpoint.port != 3000 {
		t.Fatalf("unexpected last endpoint: %+v", mgr.lastEndpoint)
	}
}

func TestUpdateClusterEDSWithRoutesKeepsClustersIndependent(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	ingress := &desiredstatepb.Ingress{
		Hosts:       []string{"app.example.com", "staging.example.com"},
		Mode:        "tunnel",
		TunnelToken: "tok",
		Routes: []*desiredstatepb.IngressRoute{
			{
				Match:  &desiredstatepb.IngressMatch{Hostname: "app.example.com", PathPrefix: "/"},
				Target: &desiredstatepb.IngressTarget{Environment: "production", Service: "web", Port: "http"},
			},
			{
				Match:  &desiredstatepb.IngressMatch{Hostname: "staging.example.com", PathPrefix: "/"},
				Target: &desiredstatepb.IngressTarget{Environment: "staging", Service: "web", Port: "http"},
			},
		},
	}
	if err := mgr.Ensure(context.Background(), ingress); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if err := mgr.UpdateEDS(context.Background(), "10.0.0.9", 9000); err != nil {
		t.Fatalf("update default eds: %v", err)
	}

	if err := mgr.UpdateClusterEDS(context.Background(), "env-production-web-http", "10.0.0.1", 3000); err != nil {
		t.Fatalf("update production eds: %v", err)
	}
	if err := mgr.UpdateClusterEDS(context.Background(), "env-staging-web-http", "10.0.0.2", 4000); err != nil {
		t.Fatalf("update staging eds: %v", err)
	}

	prodEndpoint := mgr.lastEndpoints["env-production-web-http"]
	if prodEndpoint == nil {
		t.Fatal("expected production endpoint to be present")
	}
	if prodEndpoint.address != "10.0.0.1" || prodEndpoint.port != 3000 {
		t.Fatalf("production endpoint was overwritten: got %s:%d, want 10.0.0.1:3000",
			prodEndpoint.address, prodEndpoint.port)
	}

	stagingEndpoint := mgr.lastEndpoints["env-staging-web-http"]
	if stagingEndpoint == nil {
		t.Fatal("expected staging endpoint to be present")
	}
	if stagingEndpoint.address != "10.0.0.2" || stagingEndpoint.port != 4000 {
		t.Fatalf("staging endpoint wrong: got %s:%d, want 10.0.0.2:4000",
			stagingEndpoint.address, stagingEndpoint.port)
	}

	defaultEndpoint := mgr.lastEndpoints["devopsellence_web"]
	if defaultEndpoint == nil {
		t.Fatal("expected default cluster endpoint to be preserved")
	}
	if defaultEndpoint.address != "10.0.0.9" || defaultEndpoint.port != 9000 {
		t.Fatalf("route cluster update overwrote the default cluster: got %s:%d, want 10.0.0.9:9000",
			defaultEndpoint.address, defaultEndpoint.port)
	}
}

func TestUpdateClusterEDSWithoutRoutesMirrorsToDefaultCluster(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           8000,
		ClusterName:    "devopsellence_web",
		RestartPolicy:  "unless-stopped",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Ensure(context.Background(), nil); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := mgr.UpdateClusterEDS(context.Background(), "env-production-web-http", "10.0.0.1", 3000); err != nil {
		t.Fatalf("update production eds: %v", err)
	}

	defaultEndpoint := mgr.lastEndpoints["devopsellence_web"]
	if defaultEndpoint == nil {
		t.Fatal("expected default cluster endpoint to be present")
	}
	if defaultEndpoint.address != "10.0.0.1" || defaultEndpoint.port != 3000 {
		t.Fatalf("unexpected default cluster endpoint: got %s:%d, want 10.0.0.1:3000",
			defaultEndpoint.address, defaultEndpoint.port)
	}
	if _, ok := mgr.lastEndpoints["env-production-web-http"]; ok {
		t.Fatal("expected named cluster to be pruned when ingress routes are absent")
	}
}

func TestWaitForRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/up" {
			t.Fatalf("path = %q, want /up", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	host, portString, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("atoi port: %v", err)
	}

	eng := &fakeEngine{
		inspectInfo: engine.ContainerInfo{
			Name:            "devopsellence-envoy",
			Running:         true,
			PublishHostPort: false,
			NetworkIP:       map[string]string{"devopsellence": host},
		},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:         "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName: "devopsellence-envoy",
		NetworkName:   "devopsellence",
		BootstrapPath: bootstrapPath,
		Port:          uint16(port),
		ClusterName:   "devopsellence_web",
		HTTPClient:    server.Client(),
		RouteTimeout:  time.Second,
		RouteInterval: 10 * time.Millisecond,
	}, logger)

	if err := mgr.WaitForRoute(context.Background(), "/up"); err != nil {
		t.Fatalf("wait for route: %v", err)
	}
}

func TestWaitForRouteRestartsAndIncludesBody(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "no healthy upstream", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	host, portString, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatalf("atoi port: %v", err)
	}

	eng := &fakeEngine{
		inspectInfo: engine.ContainerInfo{
			Name:            "devopsellence-envoy",
			Running:         true,
			HasHealthcheck:  true,
			Health:          "healthy",
			PublishHostPort: false,
			NetworkIP:       map[string]string{"devopsellence": host},
		},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	bootstrapPath := tempBootstrapPath(t)
	mgr := New(eng, Config{
		Image:          "docker.io/envoyproxy/envoy:v1.37.0",
		ContainerName:  "devopsellence-envoy",
		NetworkName:    "devopsellence",
		BootstrapPath:  bootstrapPath,
		Port:           uint16(port),
		ClusterName:    "devopsellence_web",
		HTTPClient:     server.Client(),
		RouteTimeout:   30 * time.Millisecond,
		RouteInterval:  10 * time.Millisecond,
		StartupTimeout: time.Second,
	}, logger)

	err = mgr.WaitForRoute(context.Background(), "/")
	if err == nil {
		t.Fatal("expected wait for route error")
	}
	if !strings.Contains(err.Error(), "no healthy upstream") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eng.removed) == 0 {
		t.Fatal("expected envoy restart before failure")
	}
	if requests < 2 {
		t.Fatalf("expected multiple route probes, got %d", requests)
	}
}

func decodeHCM(t *testing.T, listener *listenerv3.Listener) *hcmv3.HttpConnectionManager {
	t.Helper()

	if len(listener.GetFilterChains()) != 1 {
		t.Fatalf("expected one filter chain, got %d", len(listener.GetFilterChains()))
	}
	filters := listener.GetFilterChains()[0].GetFilters()
	if len(filters) != 1 {
		t.Fatalf("expected one filter, got %d", len(filters))
	}

	var hcm hcmv3.HttpConnectionManager
	if err := filters[0].GetTypedConfig().UnmarshalTo(&hcm); err != nil {
		t.Fatalf("unmarshal hcm: %v", err)
	}
	return &hcm
}

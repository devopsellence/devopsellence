package cloudflared

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	cerrdefs "github.com/containerd/errdefs"
)

type fakeEngine struct {
	inspectInfo engine.ContainerInfo
	inspectErr  error

	createdSpec  *engine.ContainerSpec
	removed      []string
	createHealth string
	imageExists  bool
	pulledImages []string
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
	f.inspectErr = nil
	f.inspectInfo = engine.ContainerInfo{
		Name:           spec.Name,
		Running:        true,
		Health:         health,
		HasHealthcheck: spec.Health != nil,
		NetworkIP:      map[string]string{spec.Network: "172.18.0.3"},
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
	f.inspectErr = cerrdefs.ErrNotFound
	f.inspectInfo = engine.ContainerInfo{}
	return nil
}

func (f *fakeEngine) ImageExists(ctx context.Context, image string) (bool, error) {
	return f.imageExists, nil
}

func (f *fakeEngine) PullImage(ctx context.Context, image string, auth *engine.RegistryAuth) error {
	f.pulledImages = append(f.pulledImages, image)
	f.imageExists = true
	return nil
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

func TestReconcileNoopWhenNoTokenConfigured(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	mgr := New(eng, Config{NetworkName: "devopsellence"}, logger)

	if err := mgr.Reconcile(context.Background(), nil); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if eng.createdSpec != nil {
		t.Fatal("unexpected container create")
	}
}

func TestReconcileCreatesCloudflaredWithIngressToken(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound, imageExists: true}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	mgr := New(eng, Config{
		NetworkName:    "devopsellence",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Reconcile(context.Background(), &desiredstatepb.Ingress{
		Hostname:    "abc123.devopsellence.io",
		TunnelToken: "tok",
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if eng.createdSpec == nil {
		t.Fatal("expected CreateAndStart to be called")
	}
	if eng.createdSpec.Image != defaultImage {
		t.Fatalf("expected default image, got %s", eng.createdSpec.Image)
	}
	if eng.createdSpec.Name != defaultContainerName {
		t.Fatalf("expected default name, got %s", eng.createdSpec.Name)
	}
	cmd := strings.Join(eng.createdSpec.Command, " ")
	if !strings.Contains(cmd, "tunnel") || !strings.Contains(cmd, "run") {
		t.Fatalf("unexpected command: %s", cmd)
	}
	if eng.createdSpec.Env["TUNNEL_TOKEN"] != "tok" {
		t.Fatal("expected TUNNEL_TOKEN env")
	}
	if eng.createdSpec.Health == nil || len(eng.createdSpec.Health.Test) == 0 {
		t.Fatal("expected default healthcheck")
	}
	if eng.createdSpec.Restart == nil || eng.createdSpec.Restart.Name != defaultRestartPolicy {
		t.Fatalf("unexpected restart policy: %+v", eng.createdSpec.Restart)
	}
}

func TestReconcilePullsCloudflaredImageWhenMissing(t *testing.T) {
	eng := &fakeEngine{inspectErr: cerrdefs.ErrNotFound}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	mgr := New(eng, Config{
		NetworkName:    "devopsellence",
		StartupTimeout: 2 * time.Second,
	}, logger)

	if err := mgr.Reconcile(context.Background(), &desiredstatepb.Ingress{
		Hostname:    "abc123.devopsellence.io",
		TunnelToken: "tok",
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(eng.pulledImages) != 1 || eng.pulledImages[0] != defaultImage {
		t.Fatalf("expected pull for %s, got %v", defaultImage, eng.pulledImages)
	}
}

func TestReconcileNoopWhenRunningSameIngress(t *testing.T) {
	eng := &fakeEngine{
		inspectInfo: engine.ContainerInfo{
			Name:           defaultContainerName,
			Running:        true,
			Health:         "healthy",
			HasHealthcheck: true,
		},
		imageExists: true,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	mgr := New(eng, Config{
		NetworkName: "devopsellence",
	}, logger)
	mgr.lastAppliedFingerprint = ingressFingerprint(&desiredstatepb.Ingress{
		Hostname:    "abc123.devopsellence.io",
		TunnelToken: "tok",
	})

	if err := mgr.Reconcile(context.Background(), &desiredstatepb.Ingress{
		Hostname:    "abc123.devopsellence.io",
		TunnelToken: "tok",
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if eng.createdSpec != nil {
		t.Fatal("unexpected container create")
	}
}

func TestReconcileRemovesRunningCloudflaredWhenIngressMissing(t *testing.T) {
	eng := &fakeEngine{
		inspectInfo: engine.ContainerInfo{
			Name:           defaultContainerName,
			Running:        true,
			Health:         "healthy",
			HasHealthcheck: true,
		},
		imageExists: true,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))
	mgr := New(eng, Config{NetworkName: "devopsellence"}, logger)

	if err := mgr.Reconcile(context.Background(), nil); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(eng.removed) != 1 || eng.removed[0] != defaultContainerName {
		t.Fatalf("expected removal, got %v", eng.removed)
	}
}

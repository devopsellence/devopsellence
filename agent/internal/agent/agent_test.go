package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/authority"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/lifecycle"
	"github.com/devopsellence/devopsellence/agent/internal/observability"
	"github.com/devopsellence/devopsellence/agent/internal/reconcile"
	"github.com/devopsellence/devopsellence/agent/internal/report"
	"github.com/devopsellence/devopsellence/agent/internal/report/file"
	"github.com/prometheus/client_golang/prometheus"
)

type captureReporter struct {
	calls []report.Status
}

func (c *captureReporter) Report(_ context.Context, s report.Status) error {
	c.calls = append(c.calls, s)
	return nil
}

// fakeAuthority is a simple in-memory authority for testing.
type fakeAuthority struct {
	desired  *desiredstatepb.DesiredState
	sequence int64
	err      error
}

func (f *fakeAuthority) Fetch(_ context.Context) (*authority.FetchResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &authority.FetchResult{
		Desired:  f.desired,
		Sequence: f.sequence,
	}, nil
}

func desiredWithWeb(revision string) *desiredstatepb.DesiredState {
	return &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      revision,
		Environments: []*desiredstatepb.Environment{{
			Name:     "production",
			Revision: revision,
			Services: []*desiredstatepb.Service{{
				Name:        "web",
				Kind:        "web",
				Image:       "busybox",
				Command:     []string{"sh"},
				Ports:       []*desiredstatepb.ServicePort{{Name: "http", Port: 3000}},
				Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 3000, Retries: 1, TimeoutSeconds: 1},
			}},
		}},
	}
}

type flakyReporter struct {
	calls     []report.Status
	failures  int
	failError error
}

func (f *flakyReporter) Report(_ context.Context, s report.Status) error {
	f.calls = append(f.calls, s)
	if f.failures > 0 {
		f.failures--
		if f.failError != nil {
			return f.failError
		}
		return errors.New("report failed")
	}
	return nil
}

type fakeEngine struct {
	containers map[string]engine.ContainerState
	images     map[string]bool
	created    []engine.ContainerSpec
	waitCalls  []string
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		containers: map[string]engine.ContainerState{},
		images:     map[string]bool{},
	}
}

func (f *fakeEngine) ListManaged(ctx context.Context) ([]engine.ContainerState, error) {
	out := make([]engine.ContainerState, 0, len(f.containers))
	for _, c := range f.containers {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeEngine) CreateAndStart(ctx context.Context, spec engine.ContainerSpec) error {
	f.created = append(f.created, spec)
	f.containers[spec.Name] = engine.ContainerState{
		Name:        spec.Name,
		Image:       spec.Image,
		Running:     true,
		Hash:        spec.Labels[engine.LabelHash],
		Environment: spec.Labels[engine.LabelEnvironment],
		Service:     spec.Labels[engine.LabelService],
		ServiceKind: spec.Labels[engine.LabelServiceKind],
	}
	return nil
}

func (f *fakeEngine) Start(ctx context.Context, name string) error {
	c := f.containers[name]
	c.Running = true
	f.containers[name] = c
	return nil
}

func (f *fakeEngine) Wait(ctx context.Context, name string) (int64, error) {
	f.waitCalls = append(f.waitCalls, name)
	return 0, nil
}

func (f *fakeEngine) Stop(ctx context.Context, name string, timeout time.Duration) error {
	c := f.containers[name]
	c.Running = false
	f.containers[name] = c
	return nil
}

func (f *fakeEngine) Remove(ctx context.Context, name string) error {
	delete(f.containers, name)
	return nil
}

func (f *fakeEngine) ImageExists(ctx context.Context, image string) (bool, error) {
	return f.images[image], nil
}

func (f *fakeEngine) PullImage(ctx context.Context, image string, auth *engine.RegistryAuth) error {
	f.images[image] = true
	return nil
}

func (f *fakeEngine) Inspect(ctx context.Context, name string) (engine.ContainerInfo, error) {
	c := f.containers[name]
	return engine.ContainerInfo{
		Name:      c.Name,
		Running:   c.Running,
		Health:    "healthy",
		NetworkIP: map[string]string{"devopsellence": "172.18.0.10"},
	}, nil
}

func (f *fakeEngine) EnsureNetwork(ctx context.Context, name string) error {
	return nil
}

func (f *fakeEngine) Logs(_ context.Context, _ string, _ int) ([]byte, error) {
	return nil, nil
}

type fakeEnvoy struct {
	updated bool
}

func (f *fakeEnvoy) Ensure(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	return nil
}

func (f *fakeEnvoy) UpdateEDS(ctx context.Context, address string, port uint16) error {
	return f.UpdateClusterEDS(ctx, "devopsellence_web", address, port)
}

func (f *fakeEnvoy) UpdateClusterEDS(ctx context.Context, clusterName string, address string, port uint16) error {
	f.updated = true
	return nil
}

func (f *fakeEnvoy) WaitForRoute(ctx context.Context, path string) error {
	return nil
}

type staticHTTPProber struct{}

func (staticHTTPProber) Get(ctx context.Context, target string, timeout time.Duration) (int, error) {
	return 200, nil
}

func TestAgentReconcileE2E(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{}))

	desired := desiredWithWeb("rev-1")
	desired.Environments[0].Services[0].Command = []string{"sh", "-c", "echo hi && sleep 1"}
	desired.Environments[0].Services[0].Env = map[string]string{"A": "B", "SECRET": "value"}

	authority := &fakeAuthority{desired: desired, sequence: 1}
	eng := newFakeEngine()
	eng.images["busybox"] = true
	envoyManager := &fakeEnvoy{}
	reconciler := reconcile.New(eng, reconcile.Options{
		Network:     "devopsellence",
		StopTimeout: 10 * time.Second,
		WebPort:     3000,
		Envoy:       envoyManager,
		HTTPProber:  staticHTTPProber{},
	})
	reporter := file.New(filepath.Join(dir, "status.json"), logger)
	metrics := observability.NewMetrics(prometheus.NewRegistry())

	ag := New(authority, reconciler, reporter, 10*time.Second, logger, metrics, nil)
	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if len(eng.containers) != 1 {
		t.Fatal("expected one container to be created")
	}

	statusData, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var status report.Status
	if err := json.Unmarshal(statusData, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Phase != report.PhaseSettled {
		t.Fatalf("unexpected phase: %s", status.Phase)
	}
	if !envoyManager.updated {
		t.Fatal("expected envoy EDS update")
	}
}

func TestSuppressesDuplicateSettledReportForSameSequence(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	desired := desiredWithWeb("rev-1")

	eng := newFakeEngine()
	eng.images["busybox"] = true
	reconciler := reconcile.New(eng, reconcile.Options{
		Network:    "devopsellence",
		WebPort:    3000,
		Envoy:      &fakeEnvoy{},
		HTTPProber: staticHTTPProber{},
	})
	reporter := &captureReporter{}
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	ag := New(&fakeAuthority{desired: desired, sequence: 7}, reconciler, reporter, time.Minute, logger, metrics, nil)

	// First reconcile — container is created, report expected.
	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if len(reporter.calls) != 1 {
		t.Fatalf("after first reconcile: got %d report(s), want 1", len(reporter.calls))
	}
	if reporter.calls[0].Phase != report.PhaseSettled {
		t.Fatalf("unexpected phase: %s", reporter.calls[0].Phase)
	}

	// Second reconcile on the same desired-state sequence should stay quiet.
	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(reporter.calls) != 1 {
		t.Fatalf("after second reconcile: got %d report(s), want 1", len(reporter.calls))
	}
}

func TestReportsSettledAgainForNewSequenceWithSameDesiredState(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	desired := desiredWithWeb("rev-1")

	eng := newFakeEngine()
	eng.images["busybox"] = true
	reconciler := reconcile.New(eng, reconcile.Options{
		Network:    "devopsellence",
		WebPort:    3000,
		Envoy:      &fakeEnvoy{},
		HTTPProber: staticHTTPProber{},
	})
	reporter := &captureReporter{}
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	auth := &fakeAuthority{desired: desired, sequence: 7}
	ag := New(auth, reconciler, reporter, time.Minute, logger, metrics, nil)

	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	auth.sequence = 8
	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	if len(reporter.calls) != 2 {
		t.Fatalf("got %d report(s), want 2", len(reporter.calls))
	}
	if reporter.calls[1].Phase != report.PhaseSettled {
		t.Fatalf("second phase = %s, want %s", reporter.calls[1].Phase, report.PhaseSettled)
	}
	if reporter.calls[1].Message != "created=0 updated=0 removed=0 unchanged=1" {
		t.Fatalf("second message = %q", reporter.calls[1].Message)
	}
}

func TestRetriesDuplicateReportAfterReporterFailure(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	desired := desiredWithWeb("rev-1")

	eng := newFakeEngine()
	eng.images["busybox"] = true
	reconciler := reconcile.New(eng, reconcile.Options{
		Network:    "devopsellence",
		WebPort:    3000,
		Envoy:      &fakeEnvoy{},
		HTTPProber: staticHTTPProber{},
	})
	reporter := &flakyReporter{failures: 1, failError: errors.New("post failed")}
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	ag := New(&fakeAuthority{desired: desired, sequence: 7}, reconciler, reporter, time.Minute, logger, metrics, nil)

	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if len(reporter.calls) != 1 {
		t.Fatalf("after first reconcile: got %d report(s), want 1", len(reporter.calls))
	}

	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(reporter.calls) != 2 {
		t.Fatalf("after second reconcile: got %d report(s), want 2", len(reporter.calls))
	}
}

func TestReportOnReconcileError(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	reconcileErr := errors.New("docker daemon unavailable")
	auth := &fakeAuthority{err: reconcileErr}
	eng := newFakeEngine()
	reconciler := reconcile.New(eng, reconcile.Options{
		Network: "devopsellence",
		WebPort: 3000,
		Envoy:   &fakeEnvoy{},
	})
	reporter := &captureReporter{}
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	ag := New(auth, reconciler, reporter, time.Minute, logger, metrics, nil)

	if err := ag.reconcileOnce(context.Background()); err == nil {
		t.Fatal("expected reconcile to return an error")
	}
	if len(reporter.calls) != 1 {
		t.Fatalf("got %d report(s), want 1", len(reporter.calls))
	}
	if reporter.calls[0].Phase != report.PhaseError {
		t.Fatalf("unexpected phase: %s", reporter.calls[0].Phase)
	}
	if reporter.calls[0].Error != reconcileErr.Error() {
		t.Fatalf("unexpected error field: %s", reporter.calls[0].Error)
	}
}

func TestNoReportWhenNoDesiredState(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	auth := &fakeAuthority{err: authority.ErrNoDesiredState}
	eng := newFakeEngine()
	reconciler := reconcile.New(eng, reconcile.Options{
		Network: "devopsellence",
		WebPort: 3000,
		Envoy:   &fakeEnvoy{},
	})
	reporter := &captureReporter{}
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	ag := New(auth, reconciler, reporter, time.Minute, logger, metrics, nil)

	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reporter.calls) != 0 {
		t.Fatalf("got %d report(s), want 0 (no desired state is not a reportable event)", len(reporter.calls))
	}
}

func TestReleaseCommandRunsBeforeReconcilingRuntimeContainers(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	desired := desiredWithWeb("rev-1")
	desired.Environments[0].Services[0].Command = []string{"sh", "-c", "sleep 1"}
	desired.Environments[0].Tasks = []*desiredstatepb.Task{{
		Name:    "release_command",
		Image:   "busybox",
		Command: []string{"sh", "-c", "echo migrate"},
	}}

	eng := newFakeEngine()
	eng.images["busybox"] = true
	reconciler := reconcile.New(eng, reconcile.Options{
		Network:    "devopsellence",
		WebPort:    3000,
		Envoy:      &fakeEnvoy{},
		HTTPProber: staticHTTPProber{},
	})
	reporter := &captureReporter{}
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	store := lifecycle.NewStore(filepath.Join(t.TempDir(), "lifecycle-state.json"))
	ag := New(&fakeAuthority{desired: desired, sequence: 9}, reconciler, reporter, time.Minute, logger, metrics, store)

	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(eng.waitCalls) != 1 {
		t.Fatalf("wait calls = %d, want 1", len(eng.waitCalls))
	}
	if len(reporter.calls) != 2 {
		t.Fatalf("reports = %d, want 2", len(reporter.calls))
	}
	if reporter.calls[0].Task == nil || reporter.calls[0].Task.Name != "release_command" {
		t.Fatalf("expected release_command task report, got %#v", reporter.calls[0].Task)
	}
	if reporter.calls[1].Phase != report.PhaseSettled || reporter.calls[1].Task != nil {
		t.Fatalf("expected final settled runtime report, got %#v", reporter.calls[1])
	}
	if len(eng.containers) != 1 {
		t.Fatalf("expected runtime container after release command, got %#v", eng.containers)
	}
}

func TestEnvironmentTasksAreSatisfiedPerEnvironment(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	task := func() *desiredstatepb.Task {
		return &desiredstatepb.Task{
			Name:    "release_command",
			Image:   "busybox",
			Command: []string{"sh", "-c", "echo migrate"},
		}
	}
	desired := &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      "rev-1",
		Environments: []*desiredstatepb.Environment{
			{Name: "production", Revision: "rev-1", Tasks: []*desiredstatepb.Task{task()}},
			{Name: "staging", Revision: "rev-1", Tasks: []*desiredstatepb.Task{task()}},
		},
	}

	eng := newFakeEngine()
	eng.images["busybox"] = true
	reconciler := reconcile.New(eng, reconcile.Options{Network: "devopsellence", WebPort: 3000})
	reporter := &captureReporter{}
	metrics := observability.NewMetrics(prometheus.NewRegistry())
	store := lifecycle.NewStore(filepath.Join(t.TempDir(), "lifecycle-state.json"))
	ag := New(&fakeAuthority{desired: desired, sequence: 9}, reconciler, reporter, time.Minute, logger, metrics, store)

	if err := ag.reconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(eng.waitCalls) != 2 {
		t.Fatalf("wait calls = %d, want 2", len(eng.waitCalls))
	}
}

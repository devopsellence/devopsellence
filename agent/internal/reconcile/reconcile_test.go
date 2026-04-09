package reconcile

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
)

type fakeEngine struct {
	containers   map[string]engine.ContainerState
	images       map[string]bool
	created      []engine.ContainerSpec
	started      []string
	removed      []string
	stopped      []string
	pulled       []string
	inspectCalls int
	networkIP    map[string]string
	ops          []string
	waitExitCode int64
	logsOutput   []byte
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
	f.ops = append(f.ops, "create:"+spec.Name)
	f.containers[spec.Name] = engine.ContainerState{
		Name:    spec.Name,
		Image:   spec.Image,
		Running: true,
		Hash:    spec.Labels[engine.LabelHash],
	}
	return nil
}

func (f *fakeEngine) Start(ctx context.Context, name string) error {
	f.started = append(f.started, name)
	f.ops = append(f.ops, "start:"+name)
	c := f.containers[name]
	c.Running = true
	f.containers[name] = c
	return nil
}

func (f *fakeEngine) Wait(ctx context.Context, name string) (int64, error) {
	f.ops = append(f.ops, "wait:"+name)
	return f.waitExitCode, nil
}

func (f *fakeEngine) Stop(ctx context.Context, name string, timeout time.Duration) error {
	f.stopped = append(f.stopped, name)
	f.ops = append(f.ops, "stop:"+name)
	c := f.containers[name]
	c.Running = false
	f.containers[name] = c
	return nil
}

func (f *fakeEngine) Remove(ctx context.Context, name string) error {
	f.removed = append(f.removed, name)
	f.ops = append(f.ops, "remove:"+name)
	delete(f.containers, name)
	return nil
}

func (f *fakeEngine) ImageExists(ctx context.Context, image string) (bool, error) {
	return f.images[image], nil
}

func (f *fakeEngine) PullImage(ctx context.Context, image string, auth *engine.RegistryAuth) error {
	f.pulled = append(f.pulled, image)
	f.ops = append(f.ops, "pull:"+image)
	f.images[image] = true
	return nil
}

func (f *fakeEngine) Inspect(ctx context.Context, name string) (engine.ContainerInfo, error) {
	f.inspectCalls++
	networkIP := f.networkIP
	if networkIP == nil {
		networkIP = map[string]string{"devopsellence": "172.18.0.2"}
	}
	c := f.containers[name]
	return engine.ContainerInfo{
		Name:      c.Name,
		Running:   c.Running,
		NetworkIP: networkIP,
	}, nil
}

func (f *fakeEngine) EnsureNetwork(ctx context.Context, name string) error {
	return nil
}

func (f *fakeEngine) Logs(_ context.Context, _ string, _ int) ([]byte, error) {
	return f.logsOutput, nil
}

type fakeEnvoyManager struct {
	engine             *fakeEngine
	updated            bool
	updateCalls        int
	updateInspectCalls int
	lastPort           uint16
	waitCalls          int
	waitPath           string
	waitErr            error
	ingress            *desiredstatepb.Ingress
}

func (f *fakeEnvoyManager) Ensure(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	f.ingress = ingress
	return nil
}

func (f *fakeEnvoyManager) UpdateEDS(ctx context.Context, address string, port uint16) error {
	f.updated = true
	f.updateCalls++
	f.lastPort = port
	if f.engine != nil {
		f.updateInspectCalls = f.engine.inspectCalls
		f.engine.ops = append(f.engine.ops, "envoy:update")
	}
	return nil
}

func (f *fakeEnvoyManager) WaitForRoute(ctx context.Context, path string) error {
	f.waitCalls++
	f.waitPath = path
	return f.waitErr
}

func TestReconcileWebUsesDesiredPortWhenPresent(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     3000,
		Envoy:       envoyManager,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})

	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "busybox",
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
		}},
	}

	if _, err := rec.Reconcile(context.Background(), desired); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if envoyManager.lastPort != 80 {
		t.Fatalf("expected envoy port 80, got %d", envoyManager.lastPort)
	}
	if envoyManager.waitPath != "/up" {
		t.Fatalf("expected envoy wait path /up, got %q", envoyManager.waitPath)
	}
}

func TestReconcileEnsuresIngressCertificateForDirectDNSIngress(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	envoyManager := &fakeEnvoyManager{engine: eng}
	ingressCertManager := &fakeIngressCertManager{}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     3000,
		Envoy:       envoyManager,
		IngressCert: ingressCertManager,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})

	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Ingress: &desiredstatepb.Ingress{
			Mode:     "direct_dns",
			Hostname: "abc123.devopsellence.io",
		},
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "busybox",
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
		}},
	}

	if _, err := rec.Reconcile(context.Background(), desired); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if ingressCertManager.calls != 1 {
		t.Fatalf("expected ingress cert ensure call, got %d", ingressCertManager.calls)
	}
}

type fakeCloudflaredManager struct {
	ingresses []*desiredstatepb.Ingress
}

func (f *fakeCloudflaredManager) Reconcile(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	f.ingresses = append(f.ingresses, ingress)
	return nil
}

type fakeImagePullAuth struct {
	auth *engine.RegistryAuth
	err  error
}

func (f *fakeImagePullAuth) AuthForImage(ctx context.Context, image string) (*engine.RegistryAuth, error) {
	return f.auth, f.err
}

type fakeIngressCertManager struct {
	calls     int
	ingress   *desiredstatepb.Ingress
	ensureErr error
}

func (f *fakeIngressCertManager) Ensure(ctx context.Context, ingress *desiredstatepb.Ingress) error {
	f.calls++
	f.ingress = ingress
	return f.ensureErr
}

type fakeHTTPProber struct {
	statuses []int
	errs     []error
	targets  []string
	calls    int
}

func (f *fakeHTTPProber) Get(ctx context.Context, target string, timeout time.Duration) (int, error) {
	f.calls++
	f.targets = append(f.targets, target)
	idx := f.calls - 1
	if idx < len(f.errs) && f.errs[idx] != nil {
		return 0, f.errs[idx]
	}
	if idx < len(f.statuses) {
		return f.statuses[idx], nil
	}
	if len(f.statuses) > 0 {
		return f.statuses[len(f.statuses)-1], nil
	}
	return 200, nil
}

func TestReconcileCreate(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second, WebPort: 3000})
	desired := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox"}},
	}

	result, err := rec.Reconcile(context.Background(), desired)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("expected created=1 got %d", result.Created)
	}
	hash, err := desiredstate.HashContainer(desired.Containers[0])
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	name, err := desiredstate.ContainerName("worker", "rev-1", hash)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	if _, ok := eng.containers[name]; !ok {
		t.Fatal("expected container created")
	}
}

func TestRunTaskTruncatesLogOutput(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	eng.waitExitCode = 3
	eng.logsOutput = []byte(strings.Repeat("x", 700))

	rec := New(eng, Options{Network: "devopsellence"})
	_, err := rec.RunTask(context.Background(), "rev-1", &desiredstatepb.Task{
		Name:  "init",
		Image: "busybox",
	})
	if err == nil {
		t.Fatalf("expected task error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "...(truncated)") {
		t.Fatalf("expected truncated marker in error message, got %q", msg)
	}
	if len(msg) > 650 {
		t.Fatalf("expected bounded error length, got %d chars", len(msg))
	}
}

func TestSummarizeTaskOutputSanitizesControlCharacters(t *testing.T) {
	got := summarizeTaskOutput([]byte("line1\x00\nline2\tline3\r\n"))
	if strings.ContainsRune(got, '\x00') {
		t.Fatalf("expected control characters to be removed, got %q", got)
	}
	if strings.Contains(got, "\n") || strings.Contains(got, "\t") || strings.Contains(got, "\r") {
		t.Fatalf("expected normalized whitespace, got %q", got)
	}
	if got != "line1 line2 line3" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestSummarizeTaskOutputDropsInvalidUTF8(t *testing.T) {
	got := summarizeTaskOutput([]byte("ok\xff\xfe\nstill-ok"))
	if got != "ok still-ok" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestReconcileRestart(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true

	c := &desiredstatepb.Container{ServiceName: "worker", Image: "busybox"}
	hash, err := desiredstate.HashContainer(c)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	name, err := desiredstate.ContainerName("worker", "rev-1", hash)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	eng.containers[name] = engine.ContainerState{Name: name, Image: "busybox", Running: false, Hash: hash, Service: "worker"}

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second, WebPort: 3000})
	desired := &desiredstatepb.DesiredState{Revision: "rev-1", Containers: []*desiredstatepb.Container{c}}

	result, err := rec.Reconcile(context.Background(), desired)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Updated != 1 {
		t.Fatalf("expected updated=1 got %d", result.Updated)
	}
	if len(eng.started) != 1 {
		t.Fatalf("expected start called")
	}
}

func TestReconcileUpdate(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true

	old := &desiredstatepb.Container{ServiceName: "worker", Image: "busybox", Env: map[string]string{"A": "1"}}
	oldHash, err := desiredstate.HashContainer(old)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	oldName, err := desiredstate.ContainerName("worker", "rev-1", oldHash)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	eng.containers[oldName] = engine.ContainerState{Name: oldName, Image: "busybox", Running: true, Hash: oldHash, Service: "worker"}

	newDesired := &desiredstatepb.DesiredState{Revision: "rev-1", Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox", Env: map[string]string{"A": "2"}}}}

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second, WebPort: 3000})
	result, err := rec.Reconcile(context.Background(), newDesired)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Updated != 1 {
		t.Fatalf("expected updated=1 got %d", result.Updated)
	}
	if len(eng.removed) != 1 {
		t.Fatalf("expected remove called")
	}
	if len(eng.created) != 1 {
		t.Fatalf("expected create called")
	}
}

func TestReconcileRemoveExtra(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	eng.containers["extra"] = engine.ContainerState{Name: "extra", Image: "busybox", Running: true, Hash: "x", Service: "worker"}

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second, WebPort: 3000})
	desired := &desiredstatepb.DesiredState{Revision: "rev-1"}

	result, err := rec.Reconcile(context.Background(), desired)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Removed != 1 {
		t.Fatalf("expected removed=1 got %d", result.Removed)
	}
}

func TestReconcileMissingImage(t *testing.T) {
	eng := newFakeEngine()
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second, WebPort: 3000})
	desired := &desiredstatepb.DesiredState{Revision: "rev-1", Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "missing"}}}

	if _, err := rec.Reconcile(context.Background(), desired); err == nil {
		t.Fatal("expected error")
	}
}

func TestReconcileWebWaitsForHealthy(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true
	eng.networkIP = map[string]string{"devopsellence": "172.18.0.10"}

	envoyManager := &fakeEnvoyManager{engine: eng}
	prober := &fakeHTTPProber{statuses: []int{503, 200}}
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 2 * time.Second, WebPort: 80, Envoy: envoyManager, HTTPProber: prober})
	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "httpbin",
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 8080, Retries: 2, TimeoutSeconds: 1},
		}},
	}

	if _, err := rec.Reconcile(context.Background(), desired); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !envoyManager.updated {
		t.Fatal("expected envoy update after healthy")
	}
	if prober.calls != 2 {
		t.Fatalf("expected 2 probe attempts, got %d", prober.calls)
	}
	if got := prober.targets[len(prober.targets)-1]; got != "http://172.18.0.10:8080/up" {
		t.Fatalf("unexpected probe target: %s", got)
	}
}

func TestReconcilePullsMissingImageWhenRemotePullAuthConfigured(t *testing.T) {
	eng := newFakeEngine()
	rec := New(eng, Options{
		Network:       "devopsellence",
		StopTimeout:   10 * time.Second,
		WebPort:       3000,
		ImagePullAuth: &fakeImagePullAuth{},
	})
	desired := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "us-central1-docker.pkg.dev/devopsellence/sub-1/app:rev-1"}},
	}

	result, err := rec.Reconcile(context.Background(), desired)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("expected created=1 got %d", result.Created)
	}
	if len(eng.pulled) != 1 {
		t.Fatalf("expected image pull, got %v", eng.pulled)
	}
}

func TestReconcileWebPassesIngressToCloudflared(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	envoyManager := &fakeEnvoyManager{}
	cloudflared := &fakeCloudflaredManager{}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     80,
		Envoy:       envoyManager,
		Cloudflared: cloudflared,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})
	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Ingress: &desiredstatepb.Ingress{
			Hostname:    "abc123.devopsellence.io",
			TunnelToken: "tok",
		},
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "httpbin",
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
		}},
	}

	if _, err := rec.Reconcile(context.Background(), desired); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(cloudflared.ingresses) != 1 {
		t.Fatalf("expected cloudflared reconcile, got %d", len(cloudflared.ingresses))
	}
	if got := cloudflared.ingresses[0].Hostname; got != "abc123.devopsellence.io" {
		t.Fatalf("unexpected ingress hostname: %s", got)
	}
	if envoyManager.ingress == nil || envoyManager.ingress.Hostname != "abc123.devopsellence.io" {
		t.Fatalf("unexpected envoy ingress: %+v", envoyManager.ingress)
	}
}

func TestReconcileWebIgnoresTypedNilCloudflaredManager(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	envoyManager := &fakeEnvoyManager{}
	var cloudflared *fakeCloudflaredManager
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     80,
		Envoy:       envoyManager,
		Cloudflared: cloudflared,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})
	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "httpbin",
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
		}},
	}

	if _, err := rec.Reconcile(context.Background(), desired); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func TestReconcileWithoutWebClearsCloudflared(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true

	cloudflared := &fakeCloudflaredManager{}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     80,
		Cloudflared: cloudflared,
	})
	desired := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox"}},
	}

	if _, err := rec.Reconcile(context.Background(), desired); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(cloudflared.ingresses) != 1 || cloudflared.ingresses[0] != nil {
		t.Fatalf("expected nil ingress reconcile, got %+v", cloudflared.ingresses)
	}
}

func TestReconcileWebUnhealthyDoesNotUpdateEDS(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 2 * time.Second, WebPort: 80, Envoy: envoyManager, HTTPProber: &fakeHTTPProber{statuses: []int{503}}})
	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "httpbin",
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
		}},
	}

	if _, err := rec.Reconcile(context.Background(), desired); err == nil {
		t.Fatal("expected error")
	}
	if envoyManager.updated {
		t.Fatal("expected no envoy update on unhealthy container")
	}
}

func TestReconcileWebUpdateCutsOverBeforeRemovingOldContainer(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true
	eng.networkIP = map[string]string{"devopsellence": "172.18.0.20"}

	old := &desiredstatepb.Container{
		ServiceName: "web",
		Image:       "httpbin",
		Env:         map[string]string{"VERSION": "old"},
		Port:        80,
		Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
	}
	oldHash, err := desiredstate.HashContainer(old)
	if err != nil {
		t.Fatalf("hash old: %v", err)
	}
	oldName, err := desiredstate.ContainerName("web", "rev-1", oldHash)
	if err != nil {
		t.Fatalf("old name: %v", err)
	}
	eng.containers[oldName] = engine.ContainerState{Name: oldName, Image: "httpbin", Running: true, Hash: oldHash, Service: "web"}

	newDesired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "httpbin",
			Env:         map[string]string{"VERSION": "new"},
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 2, TimeoutSeconds: 1},
		}},
	}

	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 2 * time.Second, WebPort: 80, Envoy: envoyManager, HTTPProber: &fakeHTTPProber{statuses: []int{503, 200}}})
	result, err := rec.Reconcile(context.Background(), newDesired)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Updated != 1 {
		t.Fatalf("expected updated=1 got %d", result.Updated)
	}

	ops := strings.Join(eng.ops, ",")
	createIdx := strings.Index(ops, "create:")
	envoyIdx := strings.Index(ops, "envoy:update")
	removeIdx := strings.Index(ops, "remove:"+oldName)
	if createIdx == -1 || envoyIdx == -1 || removeIdx == -1 {
		t.Fatalf("unexpected ops: %s", ops)
	}
	if !(createIdx < envoyIdx && envoyIdx < removeIdx) {
		t.Fatalf("expected create -> envoy update -> remove old, got %s", ops)
	}
}

func TestReconcileWebUpdateFailureKeepsOldContainer(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	old := &desiredstatepb.Container{
		ServiceName: "web",
		Image:       "httpbin",
		Env:         map[string]string{"VERSION": "old"},
		Port:        80,
		Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
	}
	oldHash, err := desiredstate.HashContainer(old)
	if err != nil {
		t.Fatalf("hash old: %v", err)
	}
	oldName, err := desiredstate.ContainerName("web", "rev-1", oldHash)
	if err != nil {
		t.Fatalf("old name: %v", err)
	}
	eng.containers[oldName] = engine.ContainerState{Name: oldName, Image: "httpbin", Running: true, Hash: oldHash, Service: "web"}

	newDesired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "httpbin",
			Env:         map[string]string{"VERSION": "new"},
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
		}},
	}

	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 2 * time.Second, WebPort: 80, Envoy: envoyManager, HTTPProber: &fakeHTTPProber{errs: []error{fmt.Errorf("dial tcp refused")}}})
	if _, err := rec.Reconcile(context.Background(), newDesired); err == nil {
		t.Fatal("expected error")
	}
	if _, ok := eng.containers[oldName]; !ok {
		t.Fatal("expected old container to remain after failed cutover")
	}
	if envoyManager.updated {
		t.Fatal("expected no envoy update on failed cutover")
	}
}

func TestReconcileWebDrainDelayBlocksOldContainerStop(t *testing.T) {
	// Verify that the drain delay is applied between EDS update and SIGTERM:
	// if the context is cancelled during the delay the old container must
	// NOT have been stopped.
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	old := &desiredstatepb.Container{
		ServiceName: "web",
		Image:       "httpbin",
		Env:         map[string]string{"VERSION": "old"},
		Port:        80,
		Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
	}
	oldHash, _ := desiredstate.HashContainer(old)
	oldName, _ := desiredstate.ContainerName("web", "rev-1", oldHash)
	eng.containers[oldName] = engine.ContainerState{Name: oldName, Image: "httpbin", Running: true, Hash: oldHash, Service: "web"}

	newDesired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "httpbin",
			Env:         map[string]string{"VERSION": "new"},
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
		}},
	}

	envoyManager := &fakeEnvoyManager{}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		DrainDelay:  10 * time.Second, // long delay — context will cancel during it
		WebPort:     80,
		Envoy:       envoyManager,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})

	// Cancel the context shortly after the test starts. The probe and EDS
	// update are instant (fake), so the cancel fires during the DrainDelay
	// sleep, before any SIGTERM is sent.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := rec.Reconcile(ctx, newDesired)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if len(eng.stopped) != 0 {
		t.Fatalf("expected old container NOT stopped during drain delay, but got stops: %v", eng.stopped)
	}
	if !envoyManager.updated {
		t.Fatal("expected EDS to have been updated before the drain delay")
	}
}

func TestReconcileWebUnchangedRunningSkipsHealthcheckAndDrainDelay(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	current := &desiredstatepb.Container{
		ServiceName: "web",
		Image:       "httpbin",
		Port:        80,
		Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
	}
	currentHash, err := desiredstate.HashContainer(current)
	if err != nil {
		t.Fatalf("hash current: %v", err)
	}
	currentName, err := desiredstate.ContainerName("web", "rev-1", currentHash)
	if err != nil {
		t.Fatalf("current name: %v", err)
	}
	eng.containers[currentName] = engine.ContainerState{Name: currentName, Image: "httpbin", Running: true, Hash: currentHash, Service: "web"}

	prober := &fakeHTTPProber{statuses: []int{200}}
	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		DrainDelay:  10 * time.Second,
		WebPort:     80,
		Envoy:       envoyManager,
		HTTPProber:  prober,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := rec.Reconcile(ctx, &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{current},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Unchanged != 1 {
		t.Fatalf("expected unchanged=1 got %d", result.Unchanged)
	}
	if prober.calls != 0 {
		t.Fatalf("expected unchanged running container to skip health probe, got %d calls", prober.calls)
	}
	if len(eng.stopped) != 0 {
		t.Fatalf("expected no stops, got %v", eng.stopped)
	}
	if envoyManager.updateCalls != 1 {
		t.Fatalf("expected one EDS update attempt, got %d", envoyManager.updateCalls)
	}
}

func TestReconcileWebContainerSpecHasNoDockerHealthcheck(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     80,
		Envoy:       &fakeEnvoyManager{},
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})
	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "httpbin",
			Port:        80,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80, Retries: 1, TimeoutSeconds: 1},
		}},
	}

	if _, err := rec.Reconcile(context.Background(), desired); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(eng.created) != 1 {
		t.Fatalf("expected one created container, got %d", len(eng.created))
	}
	if eng.created[0].Health != nil {
		t.Fatalf("expected no docker healthcheck, got %#v", eng.created[0].Health)
	}
}

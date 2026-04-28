package reconcile

import (
	"context"
	"fmt"
	"reflect"
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
	networks     []string
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

func (f *fakeEngine) ListManaged(context.Context) ([]engine.ContainerState, error) {
	out := make([]engine.ContainerState, 0, len(f.containers))
	for _, c := range f.containers {
		out = append(out, c)
	}
	return out, nil
}

func (f *fakeEngine) CreateAndStart(_ context.Context, spec engine.ContainerSpec) error {
	f.created = append(f.created, spec)
	f.ops = append(f.ops, "create:"+spec.Name)
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

func (f *fakeEngine) Start(_ context.Context, name string) error {
	f.started = append(f.started, name)
	f.ops = append(f.ops, "start:"+name)
	c := f.containers[name]
	c.Running = true
	f.containers[name] = c
	return nil
}

func (f *fakeEngine) Wait(_ context.Context, name string) (int64, error) {
	f.ops = append(f.ops, "wait:"+name)
	return f.waitExitCode, nil
}

func (f *fakeEngine) Stop(_ context.Context, name string, _ time.Duration) error {
	f.stopped = append(f.stopped, name)
	f.ops = append(f.ops, "stop:"+name)
	c := f.containers[name]
	c.Running = false
	f.containers[name] = c
	return nil
}

func (f *fakeEngine) Remove(_ context.Context, name string) error {
	f.removed = append(f.removed, name)
	f.ops = append(f.ops, "remove:"+name)
	delete(f.containers, name)
	return nil
}

func (f *fakeEngine) ImageExists(_ context.Context, image string) (bool, error) {
	return f.images[image], nil
}

func (f *fakeEngine) PullImage(_ context.Context, image string, _ *engine.RegistryAuth) error {
	f.pulled = append(f.pulled, image)
	f.ops = append(f.ops, "pull:"+image)
	f.images[image] = true
	return nil
}

func (f *fakeEngine) Inspect(_ context.Context, name string) (engine.ContainerInfo, error) {
	f.inspectCalls++
	c := f.containers[name]
	networkIP := f.networkIP
	if networkIP == nil {
		networkIP = map[string]string{"devopsellence": "172.18.0.2"}
		if environmentNetwork, err := desiredstate.EnvironmentNetworkName("devopsellence", c.Environment); err == nil {
			networkIP[environmentNetwork] = "172.19.0.2"
		}
	}
	return engine.ContainerInfo{
		Name:      c.Name,
		Running:   c.Running,
		NetworkIP: networkIP,
	}, nil
}

func (f *fakeEngine) EnsureNetwork(_ context.Context, name string) error {
	f.networks = append(f.networks, name)
	return nil
}

func (f *fakeEngine) ConnectNetwork(context.Context, string, string) error {
	return nil
}

func (f *fakeEngine) DisconnectNetwork(context.Context, string, string) error {
	return nil
}

func (f *fakeEngine) Logs(context.Context, string, int) ([]byte, error) {
	return f.logsOutput, nil
}

type fakeEnvoyManager struct {
	engine             *fakeEngine
	updated            bool
	updateCalls        int
	updateInspectCalls int
	lastCluster        string
	lastPort           uint16
	waitCalls          int
	waitPath           string
	waitErr            error
	ingress            *desiredstatepb.Ingress
	workloadNetworks   []string
}

func (f *fakeEnvoyManager) Ensure(_ context.Context, ingress *desiredstatepb.Ingress, workloadNetworks ...string) error {
	f.ingress = ingress
	f.workloadNetworks = append([]string(nil), workloadNetworks...)
	return nil
}

func (f *fakeEnvoyManager) UpdateEDS(ctx context.Context, address string, port uint16) error {
	return f.UpdateClusterEDS(ctx, "devopsellence_web", address, port)
}

func (f *fakeEnvoyManager) UpdateClusterEDS(_ context.Context, clusterName string, _ string, port uint16) error {
	f.updated = true
	f.updateCalls++
	f.lastCluster = clusterName
	f.lastPort = port
	if f.engine != nil {
		f.updateInspectCalls = f.engine.inspectCalls
		f.engine.ops = append(f.engine.ops, "envoy:update")
	}
	return nil
}

func (f *fakeEnvoyManager) WaitForRoute(_ context.Context, path string) error {
	f.waitCalls++
	f.waitPath = path
	return f.waitErr
}

type fakeImagePullAuth struct {
	auth *engine.RegistryAuth
	err  error
}

func (f *fakeImagePullAuth) AuthForImage(context.Context, string) (*engine.RegistryAuth, error) {
	return f.auth, f.err
}

type fakeIngressCertManager struct {
	calls     int
	ingress   *desiredstatepb.Ingress
	nodePeers []*desiredstatepb.NodePeer
	ensureErr error
}

func (f *fakeIngressCertManager) Ensure(_ context.Context, ingress *desiredstatepb.Ingress, nodePeers []*desiredstatepb.NodePeer) error {
	f.calls++
	f.ingress = ingress
	f.nodePeers = nodePeers
	return f.ensureErr
}

type fakeHTTPProber struct {
	statuses []int
	errs     []error
	targets  []string
	calls    int
}

func (f *fakeHTTPProber) Get(_ context.Context, target string, _ time.Duration) (int, error) {
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

func TestReconcileCreatesMultipleWorkersInOneEnvironment(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second, WebPort: 3000})

	result, err := rec.Reconcile(context.Background(), desiredState(
		workerService("default", map[string]string{"QUEUE": "default"}),
		workerService("mailers", map[string]string{"QUEUE": "mailers"}),
	))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Created != 2 {
		t.Fatalf("expected created=2 got %d", result.Created)
	}
	if len(eng.created) != 2 {
		t.Fatalf("expected 2 created specs, got %d", len(eng.created))
	}
	for _, spec := range eng.created {
		if spec.Labels[engine.LabelEnvironment] != "production" {
			t.Fatalf("missing environment label: %#v", spec.Labels)
		}
		if spec.Labels[engine.LabelService] == "" {
			t.Fatalf("missing service label: %#v", spec.Labels)
		}
	}
}

func TestReconcileKeepsSameServiceNameInDifferentEnvironmentsSeparate(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second, WebPort: 3000})

	state := &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      "node-plan-1",
		Environments: []*desiredstatepb.Environment{
			{Name: "blog-prod", Revision: "blog-rev", Services: []*desiredstatepb.Service{workerService("worker", nil)}},
			{Name: "docs-prod", Revision: "docs-rev", Services: []*desiredstatepb.Service{workerService("worker", nil)}},
		},
	}

	result, err := rec.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Created != 2 {
		t.Fatalf("expected created=2 got %d", result.Created)
	}
	seen := map[string]bool{}
	networks := map[string]bool{}
	for _, spec := range eng.created {
		seen[spec.Labels[engine.LabelEnvironment]+"/"+spec.Labels[engine.LabelService]] = true
		networks[spec.Network] = true
	}
	if !seen["blog-prod/worker"] || !seen["docs-prod/worker"] {
		t.Fatalf("unexpected services: %#v", seen)
	}
	if !networks["devopsellence-env-blog-prod"] || !networks["devopsellence-env-docs-prod"] || networks["devopsellence"] {
		t.Fatalf("unexpected container networks: %#v", networks)
	}
}

func TestReconcileWebUsesDesiredPortWhenPresent(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true
	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     3000,
		Envoy:       envoyManager,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})

	if _, err := rec.Reconcile(context.Background(), desiredState(webService(80, "/up"))); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if envoyManager.lastPort != 80 {
		t.Fatalf("expected envoy port 80, got %d", envoyManager.lastPort)
	}
	if envoyManager.lastCluster != "env-production-web-http" {
		t.Fatalf("expected env-specific cluster, got %q", envoyManager.lastCluster)
	}
	if envoyManager.waitPath != "/up" {
		t.Fatalf("expected envoy wait path /up, got %q", envoyManager.waitPath)
	}
	if !reflect.DeepEqual(envoyManager.workloadNetworks, []string{"devopsellence-env-production"}) {
		t.Fatalf("unexpected envoy workload networks: %#v", envoyManager.workloadNetworks)
	}
}

func TestReconcileEnsuresIngressCertificateForPublicIngress(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true
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

	state := desiredState(webService(80, "/up"))
	state.Ingress = publicIngress()
	state.NodePeers = []*desiredstatepb.NodePeer{{
		Name:          "node-b",
		Labels:        []string{"web"},
		PublicAddress: "198.51.100.11",
	}}

	if _, err := rec.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if ingressCertManager.calls != 1 {
		t.Fatalf("expected ingress cert ensure call, got %d", ingressCertManager.calls)
	}
	if len(ingressCertManager.nodePeers) != 1 || ingressCertManager.nodePeers[0].GetPublicAddress() != "198.51.100.11" {
		t.Fatalf("node peers = %#v", ingressCertManager.nodePeers)
	}
}

func TestReconcileContinuesServingHTTPWhenAutoTLSProvisioningFails(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true
	envoyManager := &fakeEnvoyManager{engine: eng}
	ingressCertManager := &fakeIngressCertManager{ensureErr: fmt.Errorf("acme unavailable")}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     3000,
		Envoy:       envoyManager,
		IngressCert: ingressCertManager,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})

	state := desiredState(webService(80, "/up"))
	state.Ingress = publicIngress()

	result, err := rec.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("expected web service creation to continue, got %+v", result)
	}
	if len(eng.created) != 1 || eng.created[0].Labels[engine.LabelService] != "web" {
		t.Fatalf("expected created web container, got %+v", eng.created)
	}
}

func TestReconcileFailsWhenManualTLSProvisioningFails(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true
	envoyManager := &fakeEnvoyManager{engine: eng}
	ingressCertManager := &fakeIngressCertManager{ensureErr: fmt.Errorf("missing certificate")}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     3000,
		Envoy:       envoyManager,
		IngressCert: ingressCertManager,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})

	state := desiredState(webService(80, "/up"))
	state.Ingress = publicIngress()
	state.Ingress.Tls = &desiredstatepb.IngressTLS{Mode: "manual"}

	if _, err := rec.Reconcile(context.Background(), state); err == nil {
		t.Fatal("expected reconcile error")
	}
	if len(eng.created) != 0 {
		t.Fatalf("expected no web container create on manual tls failure, got %+v", eng.created)
	}
}

func TestRunTaskTruncatesLogOutput(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	eng.waitExitCode = 3
	eng.logsOutput = []byte(strings.Repeat("x", 700))

	rec := New(eng, Options{Network: "devopsellence"})
	_, err := rec.RunTask(context.Background(), desiredstate.DefaultEnvironmentName, "rev-1", &desiredstatepb.Task{
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

	service := workerService("worker", nil)
	hash, err := desiredstate.HashService(service)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	name, err := desiredstate.ServiceContainerName("production", "worker", "rev-1", hash)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	eng.containers[name] = engine.ContainerState{Name: name, Image: "busybox", Running: false, Hash: hash, Environment: "production", Service: "worker", ServiceKind: "worker"}

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second, WebPort: 3000})
	result, err := rec.Reconcile(context.Background(), desiredState(service))
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

	old := workerService("worker", map[string]string{"A": "1"})
	oldHash, err := desiredstate.HashService(old)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	oldName, err := desiredstate.ServiceContainerName("production", "worker", "rev-1", oldHash)
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	eng.containers[oldName] = engine.ContainerState{Name: oldName, Image: "busybox", Running: true, Hash: oldHash, Environment: "production", Service: "worker", ServiceKind: "worker"}

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second})
	result, err := rec.Reconcile(context.Background(), desiredState(workerService("worker", map[string]string{"A": "2"})))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Updated != 1 {
		t.Fatalf("expected updated=1 got %d", result.Updated)
	}
	if len(eng.removed) != 1 || len(eng.created) != 1 {
		t.Fatalf("expected remove/create, removed=%v created=%d", eng.removed, len(eng.created))
	}
}

func TestReconcileRemoveExtra(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	eng.containers["extra"] = engine.ContainerState{Name: "extra", Image: "busybox", Running: true, Hash: "x", Environment: "production", Service: "worker"}

	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second})
	result, err := rec.Reconcile(context.Background(), &desiredstatepb.DesiredState{SchemaVersion: 2, Revision: "rev-1"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Removed != 1 {
		t.Fatalf("expected removed=1 got %d", result.Removed)
	}
}

func TestReconcileMissingImage(t *testing.T) {
	eng := newFakeEngine()
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 10 * time.Second})
	if _, err := rec.Reconcile(context.Background(), desiredState(&desiredstatepb.Service{Name: "worker", Kind: "worker", Image: "missing"})); err == nil {
		t.Fatal("expected error")
	}
}

func TestReconcilePullsMissingImageWhenRemotePullAuthConfigured(t *testing.T) {
	eng := newFakeEngine()
	rec := New(eng, Options{
		Network:       "devopsellence",
		StopTimeout:   10 * time.Second,
		ImagePullAuth: &fakeImagePullAuth{},
	})
	result, err := rec.Reconcile(context.Background(), desiredState(&desiredstatepb.Service{
		Name:  "worker",
		Kind:  "worker",
		Image: "us-central1-docker.pkg.dev/devopsellence/sub-1/app:rev-1",
	}))
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

func TestOptionsDoesNotExposeCloudflaredManager(t *testing.T) {
	if _, ok := reflect.TypeOf(Options{}).FieldByName("Cloudflared"); ok {
		t.Fatal("expected built-in cloudflared management to be removed from reconciler options")
	}
}

func TestReconcileWebWithPublicIngressConfiguresEnvoy(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	envoyManager := &fakeEnvoyManager{}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     80,
		Envoy:       envoyManager,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})
	state := desiredState(webService(80, "/up"))
	state.Ingress = publicIngress()

	if _, err := rec.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if envoyManager.ingress == nil || strings.Join(envoyManager.ingress.Hosts, ",") != "abc123.devopsellence.io" {
		t.Fatalf("unexpected envoy ingress: %+v", envoyManager.ingress)
	}
}

func TestReconcileBlankModeContinuesServingHTTPWhenAutoTLSProvisioningFails(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true
	envoyManager := &fakeEnvoyManager{engine: eng}
	ingressCertManager := &fakeIngressCertManager{ensureErr: fmt.Errorf("acme unavailable")}
	rec := New(eng, Options{
		Network:     "devopsellence",
		StopTimeout: 2 * time.Second,
		WebPort:     3000,
		Envoy:       envoyManager,
		IngressCert: ingressCertManager,
		HTTPProber:  &fakeHTTPProber{statuses: []int{200}},
	})

	state := desiredState(webService(80, "/up"))
	state.Ingress = blankModeIngress()

	result, err := rec.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("expected web service creation to continue, got %+v", result)
	}
}

func TestReconcileWebUnhealthyDoesNotUpdateEDS(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true

	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 2 * time.Second, WebPort: 80, Envoy: envoyManager, HTTPProber: &fakeHTTPProber{statuses: []int{503}}})
	if _, err := rec.Reconcile(context.Background(), desiredState(webService(80, "/up"))); err == nil {
		t.Fatal("expected error")
	}
	if envoyManager.updated {
		t.Fatal("expected no envoy update on unhealthy service")
	}
}

func TestReconcileWebUpdateCutsOverBeforeRemovingOldContainer(t *testing.T) {
	eng := newFakeEngine()
	eng.images["httpbin"] = true
	eng.networkIP = map[string]string{"devopsellence": "172.18.0.20", "devopsellence-env-production": "172.19.0.20"}

	old := webService(80, "/up")
	old.Env = map[string]string{"VERSION": "old"}
	oldHash, err := desiredstate.HashService(old)
	if err != nil {
		t.Fatalf("hash old: %v", err)
	}
	oldName, err := desiredstate.ServiceContainerName("production", "web", "rev-1", oldHash)
	if err != nil {
		t.Fatalf("old name: %v", err)
	}
	eng.containers[oldName] = engine.ContainerState{Name: oldName, Image: "httpbin", Running: true, Hash: oldHash, Environment: "production", Service: "web", ServiceKind: "web"}

	newService := webService(80, "/up")
	newService.Env = map[string]string{"VERSION": "new"}
	newService.Healthcheck.Retries = 2

	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 2 * time.Second, WebPort: 80, Envoy: envoyManager, HTTPProber: &fakeHTTPProber{statuses: []int{503, 200}}})
	result, err := rec.Reconcile(context.Background(), desiredState(newService))
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

	old := webService(80, "/up")
	old.Env = map[string]string{"VERSION": "old"}
	oldHash, err := desiredstate.HashService(old)
	if err != nil {
		t.Fatalf("hash old: %v", err)
	}
	oldName, err := desiredstate.ServiceContainerName("production", "web", "rev-1", oldHash)
	if err != nil {
		t.Fatalf("old name: %v", err)
	}
	eng.containers[oldName] = engine.ContainerState{Name: oldName, Image: "httpbin", Running: true, Hash: oldHash, Environment: "production", Service: "web", ServiceKind: "web"}

	newService := webService(80, "/up")
	newService.Env = map[string]string{"VERSION": "new"}
	envoyManager := &fakeEnvoyManager{engine: eng}
	rec := New(eng, Options{Network: "devopsellence", StopTimeout: 2 * time.Second, WebPort: 80, Envoy: envoyManager, HTTPProber: &fakeHTTPProber{errs: []error{fmt.Errorf("dial tcp refused")}}})
	if _, err := rec.Reconcile(context.Background(), desiredState(newService)); err == nil {
		t.Fatal("expected error")
	}
	if _, ok := eng.containers[oldName]; !ok {
		t.Fatal("expected old container to remain after failed cutover")
	}
	if envoyManager.updated {
		t.Fatal("expected no envoy update on failed cutover")
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
	if _, err := rec.Reconcile(context.Background(), desiredState(webService(80, "/up"))); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(eng.created) != 1 {
		t.Fatalf("expected one created container, got %d", len(eng.created))
	}
	if eng.created[0].Health != nil {
		t.Fatalf("expected no docker healthcheck, got %#v", eng.created[0].Health)
	}
}

func TestReconcileAppliesLogConfigToRuntimeContainers(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	rec := New(eng, Options{
		Network: "devopsellence",
		LogConfig: &engine.LogConfig{Driver: "json-file", Options: map[string]string{
			"max-size": "10m",
			"max-file": "5",
		}},
	})
	if _, err := rec.Reconcile(context.Background(), desiredState(workerService("worker", nil))); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(eng.created) != 1 {
		t.Fatalf("expected one created container, got %d", len(eng.created))
	}
	if eng.created[0].Log == nil || eng.created[0].Log.Options["max-size"] != "10m" || eng.created[0].Log.Options["max-file"] != "5" {
		t.Fatalf("unexpected log config: %#v", eng.created[0].Log)
	}
}

func TestReconcileRecreatesRuntimeContainerWhenLogConfigChanges(t *testing.T) {
	eng := newFakeEngine()
	eng.images["busybox"] = true
	service := workerService("worker", nil)
	oldHash, err := desiredstate.HashService(service)
	if err != nil {
		t.Fatalf("hash service: %v", err)
	}
	oldName, err := desiredstate.ServiceContainerName("production", "worker", "rev-1", oldHash)
	if err != nil {
		t.Fatalf("container name: %v", err)
	}
	eng.containers[oldName] = engine.ContainerState{
		Name:        oldName,
		Image:       "busybox",
		Running:     true,
		Managed:     true,
		Hash:        oldHash,
		Environment: "production",
		Service:     "worker",
		ServiceKind: "worker",
	}

	rec := New(eng, Options{
		Network: "devopsellence",
		LogConfig: &engine.LogConfig{Driver: "json-file", Options: map[string]string{
			"max-size": "10m",
			"max-file": "5",
		}},
	})
	result, err := rec.Reconcile(context.Background(), desiredState(service))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Updated != 1 || result.Removed != 1 {
		t.Fatalf("result = %#v, want updated=1 removed=1", result)
	}
	if !containsString(eng.removed, oldName) {
		t.Fatalf("expected old container removed; removed=%#v", eng.removed)
	}
	if len(eng.created) != 1 || eng.created[0].Name == oldName {
		t.Fatalf("expected replacement container with log-config hash, created=%#v", eng.created)
	}
}

func TestReconcileOnlyProtectsPersistentEnvoyContainer(t *testing.T) {
	eng := newFakeEngine()
	eng.containers["devopsellence-envoy"] = engine.ContainerState{
		Name:    "devopsellence-envoy",
		Image:   "envoy:latest",
		Running: true,
		Managed: true,
		System:  "envoy",
	}
	eng.containers["release-task"] = engine.ContainerState{
		Name:    "release-task",
		Image:   "busybox",
		Running: false,
		Managed: true,
		System:  "release",
	}
	eng.containers["task-called-envoy"] = engine.ContainerState{
		Name:    "task-called-envoy",
		Image:   "busybox",
		Running: false,
		Managed: true,
		System:  "envoy",
	}
	eng.images["busybox"] = true
	rec := New(eng, Options{Network: "devopsellence"})
	if _, err := rec.Reconcile(context.Background(), desiredState(workerService("worker", nil))); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for _, name := range eng.removed {
		if name == "devopsellence-envoy" {
			t.Fatalf("envoy should not be removed; removed=%#v", eng.removed)
		}
	}
	if !containsString(eng.removed, "release-task") {
		t.Fatalf("expected orphaned task container to be removed; removed=%#v", eng.removed)
	}
	if !containsString(eng.removed, "task-called-envoy") {
		t.Fatalf("expected task named envoy to be removed; removed=%#v", eng.removed)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func desiredState(services ...*desiredstatepb.Service) *desiredstatepb.DesiredState {
	return &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      "node-plan-1",
		Environments: []*desiredstatepb.Environment{{
			Name:     "production",
			Revision: "rev-1",
			Services: services,
		}},
	}
}

func workerService(name string, env map[string]string) *desiredstatepb.Service {
	return &desiredstatepb.Service{
		Name:    name,
		Kind:    "worker",
		Image:   "busybox",
		Command: []string{"sleep", "3600"},
		Env:     env,
	}
}

func webService(port uint32, healthPath string) *desiredstatepb.Service {
	return &desiredstatepb.Service{
		Name:  "web",
		Kind:  "web",
		Image: "httpbin",
		Ports: []*desiredstatepb.ServicePort{{Name: "http", Port: port}},
		Healthcheck: &desiredstatepb.Healthcheck{
			Path:           healthPath,
			Port:           port,
			Retries:        1,
			TimeoutSeconds: 1,
		},
	}
}

func publicIngress() *desiredstatepb.Ingress {
	return &desiredstatepb.Ingress{
		Mode:   "public",
		Hosts:  []string{"abc123.devopsellence.io"},
		Routes: []*desiredstatepb.IngressRoute{ingressRoute("abc123.devopsellence.io")},
	}
}

func blankModeIngress() *desiredstatepb.Ingress {
	return &desiredstatepb.Ingress{
		Hosts:  []string{"abc123.devopsellence.io"},
		Routes: []*desiredstatepb.IngressRoute{ingressRoute("abc123.devopsellence.io")},
	}
}

func ingressRoute(hostname string) *desiredstatepb.IngressRoute {
	return &desiredstatepb.IngressRoute{
		Match:  &desiredstatepb.IngressMatch{Hostname: hostname},
		Target: &desiredstatepb.IngressTarget{Environment: "production", Service: "web", Port: "http"},
	}
}

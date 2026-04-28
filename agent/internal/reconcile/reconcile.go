package reconcile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/httpx"
)

const webServiceName = "web"

const ingressModePublic = "public"

type EnvoyManager interface {
	Ensure(ctx context.Context, ingress *desiredstatepb.Ingress, workloadNetworks ...string) error
	UpdateEDS(ctx context.Context, address string, port uint16) error
	UpdateClusterEDS(ctx context.Context, clusterName string, address string, port uint16) error
	WaitForRoute(ctx context.Context, path string) error
}

type EnvoyWorkloadNetworkSyncer interface {
	SyncWorkloadNetworks(ctx context.Context, workloadNetworks ...string) error
}

type ImagePullAuthProvider interface {
	AuthForImage(ctx context.Context, image string) (*engine.RegistryAuth, error)
}

type IngressCertManager interface {
	Ensure(ctx context.Context, ingress *desiredstatepb.Ingress, nodePeers []*desiredstatepb.NodePeer) error
}

type HTTPProber interface {
	Get(ctx context.Context, target string, timeout time.Duration) (int, error)
}

type Options struct {
	Network                      string
	StopTimeout                  time.Duration
	DrainDelay                   time.Duration
	WebPort                      uint16
	LogConfig                    *engine.LogConfig
	ProtectedEnvoyContainerNames []string
	Envoy                        EnvoyManager
	ImagePullAuth                ImagePullAuthProvider
	IngressCert                  IngressCertManager
	HTTPProber                   HTTPProber
	Logger                       *slog.Logger
}

type Reconciler struct {
	engine engine.Engine
	opts   Options
}

type Result struct {
	Created   int
	Updated   int
	Removed   int
	Unchanged int
}

type TaskResult struct {
	ExitCode int64
}

func New(eng engine.Engine, opts Options) *Reconciler {
	if opts.HTTPProber == nil {
		opts.HTTPProber = newDefaultHTTPProber()
	}
	return &Reconciler{engine: eng, opts: opts}
}

func (r *Reconciler) Reconcile(ctx context.Context, desired *desiredstatepb.DesiredState) (Result, error) {
	result := Result{}
	runtimeServices := desiredstate.RuntimeServices(desired)
	workloadNetworks, err := r.ensureRuntimeNetworks(ctx, runtimeServices)
	if err != nil {
		return result, err
	}
	if len(workloadNetworks) == 0 {
		if err := r.syncEnvoyWorkloadNetworks(ctx, workloadNetworks); err != nil {
			return result, err
		}
	}

	desiredByService := map[string]desiredstate.RuntimeService{}
	for _, service := range runtimeServices {
		desiredByService[runtimeServiceKey(service.EnvironmentName, service.ServiceName)] = service
	}

	existing, err := r.engine.ListManaged(ctx)
	if err != nil {
		return result, err
	}

	existingByService := map[string][]engine.ContainerState{}
	for _, c := range existing {
		if c.Service == "" {
			continue
		}
		existingByService[containerServiceKey(c)] = append(existingByService[containerServiceKey(c)], c)
	}

	for serviceKey, c := range desiredByService {
		serviceResult, err := r.reconcileService(ctx, desired.GetIngress(), desired.GetNodePeers(), c, existingByService[serviceKey], workloadNetworks)
		result.Created += serviceResult.Created
		result.Updated += serviceResult.Updated
		result.Removed += serviceResult.Removed
		result.Unchanged += serviceResult.Unchanged
		if err != nil {
			return result, err
		}
	}
	for _, c := range existing {
		if _, ok := desiredByService[containerServiceKey(c)]; ok {
			continue
		}
		if c.Name == "" || isPersistentEnvoyContainer(c, r.opts.ProtectedEnvoyContainerNames) {
			continue
		}
		if err := r.stopAndRemove(ctx, c); err != nil {
			return result, err
		}
		result.Removed++
	}

	return result, nil
}

func (r *Reconciler) RunTask(ctx context.Context, environmentName string, revision string, task *desiredstatepb.Task) (TaskResult, error) {
	result := TaskResult{}
	if task == nil {
		return result, nil
	}
	network, err := r.ensureEnvironmentNetwork(ctx, environmentName)
	if err != nil {
		return result, err
	}

	name, _, spec, err := r.specForTask(environmentName, task, revision, network)
	if err != nil {
		return result, err
	}
	if err := r.ensureImage(ctx, spec.Image); err != nil {
		return result, err
	}
	if err := r.engine.CreateAndStart(ctx, spec); err != nil {
		return result, fmt.Errorf("create task container %s: %w", name, err)
	}

	defer func() {
		r.tearDownTaskContainer(name)
	}()

	exitCode, err := r.engine.Wait(ctx, name)
	if err != nil {
		return result, fmt.Errorf("wait for task container %s: %w", name, err)
	}
	result.ExitCode = exitCode
	if exitCode != 0 {
		output, _ := r.engine.Logs(context.Background(), name, 100)
		message := fmt.Sprintf("task %s exited with code %d", task.GetName(), exitCode)
		if summarized := summarizeTaskOutput(output); summarized != "" {
			message = message + ": " + summarized
		}
		return result, errors.New(message)
	}

	return result, nil
}

func (r *Reconciler) reconcileService(ctx context.Context, ingress *desiredstatepb.Ingress, nodePeers []*desiredstatepb.NodePeer, desired desiredstate.RuntimeService, existing []engine.ContainerState, workloadNetworks []string) (Result, error) {
	result := Result{}
	isWeb := desired.ServiceKind == desiredstate.ServiceKindWeb
	name, hash, spec, err := r.specForService(desired)
	if err != nil {
		return result, err
	}

	if err := r.ensureImage(ctx, spec.Image); err != nil {
		return result, err
	}

	if isWeb {
		if r.opts.Envoy == nil {
			return result, fmt.Errorf("envoy manager required for web service")
		}
		if err := r.opts.Envoy.Ensure(ctx, ingress, workloadNetworks...); err != nil {
			return result, fmt.Errorf("ensure envoy: %w", err)
		}
		if r.opts.IngressCert != nil {
			if err := r.opts.IngressCert.Ensure(ctx, ingress, nodePeers); err != nil {
				if ingressAutoTLSErrorIsNonFatal(ingress) {
					logger := r.opts.Logger
					if logger == nil {
						logger = slog.Default()
					}
					logger.Warn("auto tls provisioning failed; serving http until certificate is ready", "error", err)
				} else {
					return result, fmt.Errorf("ensure ingress certificate: %w", err)
				}
			}
		}
		if err := r.opts.Envoy.Ensure(ctx, ingress, workloadNetworks...); err != nil {
			return result, fmt.Errorf("ensure envoy: %w", err)
		}
	}

	if isWeb {
		return r.reconcileWebService(ctx, desired, existing, name, hash, spec)
	}

	var current *engine.ContainerState
	for i := range existing {
		if existing[i].Hash == hash {
			current = &existing[i]
			break
		}
	}

	if current != nil {
		if current.Running {
			result.Unchanged++
		} else {
			if err := r.engine.Start(ctx, current.Name); err != nil {
				return result, fmt.Errorf("start container %s: %w", current.Name, err)
			}
			result.Updated++
		}
		for _, extra := range existing {
			if extra.Name != current.Name {
				if err := r.stopAndRemove(ctx, extra); err != nil {
					return result, err
				}
				result.Removed++
			}
		}
	} else {
		for _, extra := range existing {
			if err := r.stopAndRemove(ctx, extra); err != nil {
				return result, err
			}
			result.Removed++
		}
		if err := r.engine.CreateAndStart(ctx, spec); err != nil {
			return result, fmt.Errorf("create container %s: %w", name, err)
		}
		if len(existing) > 0 {
			result.Updated++
		} else {
			result.Created++
		}
		current = &engine.ContainerState{Name: name, Hash: hash, Environment: desired.EnvironmentName, Service: desired.ServiceName, ServiceKind: desired.ServiceKind, Running: true}
	}

	return result, nil
}

func (r *Reconciler) ensureImage(ctx context.Context, image string) error {
	exists, err := r.engine.ImageExists(ctx, image)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", image, err)
	}
	if !exists {
		if r.opts.ImagePullAuth == nil {
			return fmt.Errorf("image not found locally: %s", image)
		}
		var auth *engine.RegistryAuth
		auth, err = r.opts.ImagePullAuth.AuthForImage(ctx, image)
		if err != nil {
			return fmt.Errorf("resolve image pull auth for %s: %w", image, err)
		}
		if err := r.engine.PullImage(ctx, image, auth); err != nil {
			return fmt.Errorf("pull image %s: %w", image, err)
		}
	}
	return nil
}

func (r *Reconciler) reconcileWebService(ctx context.Context, desired desiredstate.RuntimeService, existing []engine.ContainerState, name, hash string, spec engine.ContainerSpec) (Result, error) {
	result := Result{}

	var current *engine.ContainerState
	for i := range existing {
		if existing[i].Hash == hash {
			current = &existing[i]
			break
		}
	}

	if current != nil {
		stale := staleContainers(existing, current.Name)
		// Desired container already exists with the right hash.
		if current.Running {
			result.Unchanged++
			if err := r.cutoverWeb(ctx, current.Name, desired, false); err != nil {
				return result, err
			}
		} else {
			if err := r.engine.Start(ctx, current.Name); err != nil {
				return result, fmt.Errorf("start container %s: %w", current.Name, err)
			}
			result.Updated++
			if err := r.cutoverWeb(ctx, current.Name, desired, true); err != nil {
				return result, err
			}
		}

		if len(stale) > 0 {
			// Step 2: Wait for Envoy to reload the EDS file before signalling
			// any stale containers to drain.
			if err := sleepWithContext(ctx, r.opts.DrainDelay); err != nil {
				return result, err
			}
			// Step 3: Drain and remove stale containers (different hash).
			for _, extra := range stale {
				if err := r.stopAndRemove(ctx, extra); err != nil {
					return result, err
				}
				result.Removed++
			}
		}
		return result, nil
	}

	// New container needed — different hash means image or config changed.

	// Step 1: Start the new container.
	if err := r.engine.CreateAndStart(ctx, spec); err != nil {
		return result, fmt.Errorf("create container %s: %w", name, err)
	}

	// If anything below fails, tear down the new container so we never
	// leave a half-started container running alongside the old one.
	// Logs are collected before removal so operators can debug the failure.
	cutoverComplete := false
	defer func() {
		if cutoverComplete {
			return
		}
		r.tearDownFailedContainer(name)
	}()

	// Step 2: Wait for new container to pass health checks, then rewrite
	// EDS so Envoy routes traffic exclusively to the new container.
	if err := r.cutoverWeb(ctx, name, desired, true); err != nil {
		return result, err
	}
	cutoverComplete = true

	if len(existing) > 0 {
		// Step 3: Give Envoy time to reload the EDS file before we send
		// SIGTERM to the old container. Without this gap, Envoy could still
		// be routing new requests to the old IP while the old process stops
		// accepting connections, causing brief 502s.
		if err := sleepWithContext(ctx, r.opts.DrainDelay); err != nil {
			return result, err
		}

		// Step 4: Drain old containers. Docker sends SIGTERM, waits
		// StopTimeout for in-flight requests to complete, then SIGKILL.
		for _, extra := range existing {
			if err := r.stopAndRemove(ctx, extra); err != nil {
				return result, err
			}
			result.Removed++
		}
	}

	if len(existing) > 0 {
		result.Updated++
	} else {
		result.Created++
	}
	return result, nil
}

func (r *Reconciler) cutoverWeb(ctx context.Context, name string, desired desiredstate.RuntimeService, waitForHealthy bool) error {
	ip, err := r.webContainerIP(ctx, name, desired, waitForHealthy)
	if err != nil {
		return err
	}

	clusterName := desiredstate.EnvoyClusterName(desired.EnvironmentName, desired.ServiceName, desiredstate.DefaultHTTPPortName)
	if err := r.opts.Envoy.UpdateClusterEDS(ctx, clusterName, ip, desiredstate.ServiceHTTPPort(desired.Service, r.opts.WebPort)); err != nil {
		return fmt.Errorf("update envoy eds: %w", err)
	}
	if err := r.opts.Envoy.WaitForRoute(ctx, desired.Service.GetHealthcheck().GetPath()); err != nil {
		return fmt.Errorf("wait for envoy route: %w", err)
	}
	return nil
}

func staleContainers(existing []engine.ContainerState, currentName string) []engine.ContainerState {
	stale := make([]engine.ContainerState, 0, len(existing))
	for _, extra := range existing {
		if extra.Name == currentName {
			continue
		}
		stale = append(stale, extra)
	}
	return stale
}

func runtimeServiceKey(environmentName, serviceName string) string {
	environmentName = strings.TrimSpace(environmentName)
	if environmentName == "" {
		environmentName = desiredstate.DefaultEnvironmentName
	}
	return desiredstate.ScopedKey(environmentName, serviceName)
}

func containerServiceKey(c engine.ContainerState) string {
	return runtimeServiceKey(c.Environment, c.Service)
}

func (r *Reconciler) syncEnvoyWorkloadNetworks(ctx context.Context, workloadNetworks []string) error {
	if r.opts.Envoy == nil {
		return nil
	}
	syncer, ok := r.opts.Envoy.(EnvoyWorkloadNetworkSyncer)
	if !ok {
		return nil
	}
	if err := syncer.SyncWorkloadNetworks(ctx, workloadNetworks...); err != nil {
		return fmt.Errorf("sync envoy workload networks: %w", err)
	}
	return nil
}

func (r *Reconciler) ensureRuntimeNetworks(ctx context.Context, services []desiredstate.RuntimeService) ([]string, error) {
	if r.opts.Network == "" {
		return nil, nil
	}
	if err := r.engine.EnsureNetwork(ctx, r.opts.Network); err != nil {
		return nil, fmt.Errorf("ensure network %s: %w", r.opts.Network, err)
	}
	seen := map[string]bool{}
	webSeen := map[string]bool{}
	networks := []string{}
	for _, service := range services {
		network, err := r.environmentNetwork(service.EnvironmentName)
		if err != nil {
			return nil, err
		}
		if network == "" {
			continue
		}
		if !seen[network] {
			if err := r.engine.EnsureNetwork(ctx, network); err != nil {
				return nil, fmt.Errorf("ensure network %s: %w", network, err)
			}
			seen[network] = true
		}
		if service.ServiceKind == desiredstate.ServiceKindWeb && !webSeen[network] {
			networks = append(networks, network)
			webSeen[network] = true
		}
	}
	return networks, nil
}

func (r *Reconciler) ensureEnvironmentNetwork(ctx context.Context, environmentName string) (string, error) {
	network, err := r.environmentNetwork(environmentName)
	if err != nil {
		return "", err
	}
	if network == "" {
		return "", nil
	}
	if err := r.engine.EnsureNetwork(ctx, network); err != nil {
		return "", fmt.Errorf("ensure network %s: %w", network, err)
	}
	return network, nil
}

func (r *Reconciler) environmentNetwork(environmentName string) (string, error) {
	if r.opts.Network == "" {
		return "", nil
	}
	return desiredstate.EnvironmentNetworkName(r.opts.Network, environmentName)
}

func runtimeContainerHash(baseHash string, logConfig *engine.LogConfig, network string) string {
	logHash := engine.LogConfigHash(logConfig)
	if logHash == "" && network == "" {
		return baseHash
	}
	sum := sha256.Sum256([]byte(baseHash + "\x00" + logHash + "\x00" + strings.TrimSpace(network)))
	return hex.EncodeToString(sum[:])
}

func isPersistentEnvoyContainer(c engine.ContainerState, protectedNames []string) bool {
	if strings.TrimSpace(c.System) != "envoy" {
		return false
	}
	name := strings.TrimSpace(c.Name)
	for _, protected := range protectedNames {
		if name == strings.TrimSpace(protected) {
			return true
		}
	}
	return len(protectedNames) == 0 && name == "devopsellence-envoy"
}

// tearDownFailedContainer stops a container that failed to become healthy,
// collects its last 100 log lines into the agent's structured log stream so
// operators can diagnose the failure, then removes the container.
// Uses a background context so a cancelled reconcile context does not
// prevent cleanup.
func (r *Reconciler) tearDownFailedContainer(name string) {
	ctx := context.Background()
	_ = r.engine.Stop(ctx, name, r.opts.StopTimeout)

	if logs, err := r.engine.Logs(ctx, name, 100); err == nil && len(logs) > 0 {
		lines := strings.TrimRight(string(logs), "\n")
		logger := r.opts.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("web container failed health checks — last 100 log lines",
			"container", name,
			"output", lines,
		)
	} else if info, inspectErr := r.engine.Inspect(ctx, name); inspectErr == nil {
		logger := r.opts.Logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("web container failed health checks — no log output; inspect details",
			"container", name,
			"state", containerInspectSummary(info),
		)
	}

	_ = r.engine.Remove(ctx, name)
}

func (r *Reconciler) tearDownTaskContainer(name string) {
	ctx := context.Background()
	_ = r.engine.Remove(ctx, name)
}

func (r *Reconciler) stopAndRemove(ctx context.Context, c engine.ContainerState) error {
	if c.Running {
		if err := r.engine.Stop(ctx, c.Name, r.opts.StopTimeout); err != nil {
			return fmt.Errorf("stop container %s: %w", c.Name, err)
		}
	}
	if err := r.engine.Remove(ctx, c.Name); err != nil {
		return fmt.Errorf("remove container %s: %w", c.Name, err)
	}
	return nil
}

func (r *Reconciler) webContainerIP(ctx context.Context, name string, desired desiredstate.RuntimeService, waitForHealthy bool) (string, error) {
	if waitForHealthy {
		return r.waitHealthy(ctx, name, desired)
	}

	info, err := r.engine.Inspect(ctx, name)
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w", name, err)
	}
	if !info.Running {
		return "", fmt.Errorf("container %s not running (%s)", name, containerInspectSummary(info))
	}

	network, err := r.environmentNetwork(desired.EnvironmentName)
	if err != nil {
		return "", err
	}
	ip := info.NetworkIP[network]
	if ip == "" {
		return "", fmt.Errorf("container %s missing network ip on %s", name, network)
	}
	return ip, nil
}

func (r *Reconciler) waitHealthy(ctx context.Context, name string, desired desiredstate.RuntimeService) (string, error) {
	healthcheck := desired.Service.GetHealthcheck()
	if healthcheck == nil {
		return "", fmt.Errorf("container %s missing healthcheck", name)
	}

	startPeriod := secondsToDuration(healthcheck.StartPeriodSeconds)
	if startPeriod > 0 {
		if err := sleepWithContext(ctx, startPeriod); err != nil {
			return "", err
		}
	}

	attempts := int(healthcheck.Retries)
	if attempts <= 0 {
		attempts = 1
	}

	interval := secondsToDuration(healthcheck.IntervalSeconds)
	timeout := secondsToDuration(healthcheck.TimeoutSeconds)
	if timeout <= 0 {
		timeout = 1 * time.Second
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		info, err := r.engine.Inspect(ctx, name)
		if err != nil {
			return "", fmt.Errorf("inspect container %s: %w", name, err)
		}
		if !info.Running {
			return "", fmt.Errorf("container %s not running (%s)", name, containerInspectSummary(info))
		}

		network, err := r.environmentNetwork(desired.EnvironmentName)
		if err != nil {
			return "", err
		}
		ip := info.NetworkIP[network]
		if ip == "" {
			lastErr = fmt.Errorf("container %s missing network ip on %s", name, network)
		} else {
			target := probeTarget(ip, healthcheck.Port, healthcheck.Path)
			status, probeErr := r.opts.HTTPProber.Get(ctx, target, timeout)
			if probeErr == nil && status >= 200 && status < 400 {
				return ip, nil
			}
			if probeErr != nil {
				lastErr = fmt.Errorf("http probe %s failed: %w", target, probeErr)
			} else {
				lastErr = fmt.Errorf("http probe %s returned %d", target, status)
			}
		}

		if attempt == attempts-1 {
			break
		}
		if interval > 0 {
			if err := sleepWithContext(ctx, interval); err != nil {
				return "", err
			}
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("healthcheck failed for %s", name)
	}
	return "", lastErr
}

func containerInspectSummary(info engine.ContainerInfo) string {
	parts := []string{}
	if status := strings.TrimSpace(info.StateStatus); status != "" {
		parts = append(parts, "status="+status)
	}
	if !info.Running {
		parts = append(parts, fmt.Sprintf("exit_code=%d", info.ExitCode))
	}
	if stateErr := strings.TrimSpace(info.StateError); stateErr != "" {
		parts = append(parts, "error="+stateErr)
	}
	if finishedAt := strings.TrimSpace(info.FinishedAt); finishedAt != "" {
		parts = append(parts, "finished_at="+finishedAt)
	}
	if len(info.Entrypoint) > 0 {
		parts = append(parts, "entrypoint="+strings.Join(info.Entrypoint, " "))
	}
	if len(info.Command) > 0 {
		parts = append(parts, "cmd="+strings.Join(info.Command, " "))
	}
	if len(parts) == 0 {
		if info.Running {
			return "running=true"
		}
		return "running=false"
	}
	return strings.Join(parts, " ")
}

func (r *Reconciler) specForService(runtime desiredstate.RuntimeService) (string, string, engine.ContainerSpec, error) {
	service := runtime.Service
	network, err := r.environmentNetwork(runtime.EnvironmentName)
	if err != nil {
		return "", "", engine.ContainerSpec{}, err
	}
	hash, err := desiredstate.HashService(service)
	if err != nil {
		return "", "", engine.ContainerSpec{}, fmt.Errorf("hash service %s/%s: %w", runtime.EnvironmentName, runtime.ServiceName, err)
	}

	hash = runtimeContainerHash(hash, r.opts.LogConfig, network)
	name, err := desiredstate.ServiceContainerName(runtime.EnvironmentName, runtime.ServiceName, runtime.EnvironmentRevision, hash)
	if err != nil {
		return "", "", engine.ContainerSpec{}, err
	}

	env := make(map[string]string, len(service.Env))
	for k, v := range service.Env {
		env[k] = v
	}

	labels := map[string]string{
		engine.LabelManaged:     "true",
		engine.LabelEnvironment: runtime.EnvironmentName,
		engine.LabelService:     runtime.ServiceName,
		engine.LabelServiceKind: runtime.ServiceKind,
		engine.LabelHash:        hash,
		engine.LabelRevision:    runtime.EnvironmentRevision,
	}

	spec := engine.ContainerSpec{
		Name:       name,
		Image:      service.Image,
		Entrypoint: service.Entrypoint,
		Command:    service.Command,
		Env:        env,
		Binds:      volumeBinds(service.VolumeMounts),
		Labels:     labels,
		Log:        engine.CloneLogConfig(r.opts.LogConfig),
		Network:    network,
	}

	return name, hash, spec, nil
}

func (r *Reconciler) specForTask(environmentName string, task *desiredstatepb.Task, revision string, network string) (string, string, engine.ContainerSpec, error) {
	hash, err := desiredstate.HashTask(task)
	if err != nil {
		return "", "", engine.ContainerSpec{}, fmt.Errorf("hash task %s: %w", task.GetName(), err)
	}

	name, err := desiredstate.ContainerName(task.GetName(), revision, hash)
	if err != nil {
		return "", "", engine.ContainerSpec{}, err
	}

	env := make(map[string]string, len(task.Env))
	for k, v := range task.Env {
		env[k] = v
	}

	spec := engine.ContainerSpec{
		Name:       name,
		Image:      task.Image,
		Entrypoint: task.Entrypoint,
		Command:    task.Command,
		Env:        env,
		Binds:      volumeBinds(task.VolumeMounts),
		Labels: map[string]string{
			engine.LabelManaged:     "true",
			engine.LabelEnvironment: environmentName,
			engine.LabelHash:        hash,
			engine.LabelRevision:    revision,
			engine.LabelSystem:      task.GetName(),
		},
		Log:     engine.CloneLogConfig(r.opts.LogConfig),
		Network: network,
	}

	return name, hash, spec, nil
}

func volumeBinds(mounts []*desiredstatepb.VolumeMount) []string {
	if len(mounts) == 0 {
		return nil
	}

	binds := make([]string, 0, len(mounts))
	for _, mount := range mounts {
		if mount == nil || mount.Source == "" || mount.Target == "" {
			continue
		}
		binds = append(binds, mount.Source+":"+mount.Target)
	}
	return binds
}

func normalizedIngressMode(ingress *desiredstatepb.Ingress) string {
	if ingress == nil {
		return ""
	}

	mode := strings.TrimSpace(ingress.Mode)
	if mode != "" {
		return mode
	}
	return ingressModePublic
}

func ingressAutoTLSErrorIsNonFatal(ingress *desiredstatepb.Ingress) bool {
	if ingress == nil {
		return false
	}
	if normalizedIngressMode(ingress) != ingressModePublic {
		return false
	}
	tls := ingress.GetTls()
	if tls == nil {
		return true
	}
	mode := strings.TrimSpace(tls.GetMode())
	return mode == "" || mode == "auto"
}

func secondsToDuration(seconds int64) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func probeTarget(ip string, port uint32, path string) string {
	if path == "" {
		path = "/"
	}
	return "http://" + net.JoinHostPort(ip, strconv.FormatUint(uint64(port), 10)) + path
}

type defaultHTTPProber struct {
	client *http.Client
}

func newDefaultHTTPProber() defaultHTTPProber {
	return defaultHTTPProber{client: httpx.NewClient(0)}
}

func (p defaultHTTPProber) Get(ctx context.Context, target string, timeout time.Duration) (int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

const maxTaskLogSnippetLen = 512

func summarizeTaskOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(bytes.ToValidUTF8(output, nil)))
	if trimmed == "" {
		return ""
	}
	trimmed = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 32 {
			return -1
		}
		return r
	}, trimmed)
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	runes := []rune(trimmed)
	if len(runes) <= maxTaskLogSnippetLen {
		return trimmed
	}
	return string(runes[:maxTaskLogSnippetLen]) + "...(truncated)"
}

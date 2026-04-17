package reconcile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/httpx"
)

const webServiceName = "web"

const (
	ingressModeTunnel = "tunnel"
)

type EnvoyManager interface {
	Ensure(ctx context.Context, ingress *desiredstatepb.Ingress) error
	UpdateEDS(ctx context.Context, address string, port uint16) error
	WaitForRoute(ctx context.Context, path string) error
}

type CloudflaredManager interface {
	Reconcile(ctx context.Context, ingress *desiredstatepb.Ingress) error
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
	Network       string
	StopTimeout   time.Duration
	DrainDelay    time.Duration
	WebPort       uint16
	Envoy         EnvoyManager
	Cloudflared   CloudflaredManager
	ImagePullAuth ImagePullAuthProvider
	IngressCert   IngressCertManager
	HTTPProber    HTTPProber
	Logger        *slog.Logger
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
	// Normalise typed-nil interfaces so callers can use == nil checks safely.
	if isNilInterface(opts.Cloudflared) {
		opts.Cloudflared = nil
	}
	if opts.HTTPProber == nil {
		opts.HTTPProber = newDefaultHTTPProber()
	}
	return &Reconciler{engine: eng, opts: opts}
}

func (r *Reconciler) Reconcile(ctx context.Context, desired *desiredstatepb.DesiredState) (Result, error) {
	result := Result{}
	if r.opts.Network != "" {
		if err := r.engine.EnsureNetwork(ctx, r.opts.Network); err != nil {
			return result, fmt.Errorf("ensure network: %w", err)
		}
	}

	desiredByService := map[string]*desiredstatepb.Container{}
	for _, c := range desired.Containers {
		desiredByService[c.ServiceName] = c
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
		existingByService[c.Service] = append(existingByService[c.Service], c)
	}

	for service, c := range desiredByService {
		serviceResult, err := r.reconcileService(ctx, desired.Revision, desired.GetIngress(), desired.GetNodePeers(), c, existingByService[service])
		result.Created += serviceResult.Created
		result.Updated += serviceResult.Updated
		result.Removed += serviceResult.Removed
		result.Unchanged += serviceResult.Unchanged
		if err != nil {
			return result, err
		}
	}
	if _, ok := desiredByService[webServiceName]; !ok && r.opts.Cloudflared != nil {
		if err := r.opts.Cloudflared.Reconcile(ctx, nil); err != nil {
			return result, fmt.Errorf("reconcile cloudflared: %w", err)
		}
	}

	for _, c := range existing {
		if _, ok := desiredByService[c.Service]; ok {
			continue
		}
		if c.Name == "" {
			continue
		}
		if err := r.stopAndRemove(ctx, c); err != nil {
			return result, err
		}
		result.Removed++
	}

	return result, nil
}

func (r *Reconciler) RunTask(ctx context.Context, revision string, task *desiredstatepb.Task) (TaskResult, error) {
	result := TaskResult{}
	if task == nil {
		return result, nil
	}
	if r.opts.Network != "" {
		if err := r.engine.EnsureNetwork(ctx, r.opts.Network); err != nil {
			return result, fmt.Errorf("ensure network: %w", err)
		}
	}

	name, _, spec, err := r.specForTask(task, revision)
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

func (r *Reconciler) reconcileService(ctx context.Context, revision string, ingress *desiredstatepb.Ingress, nodePeers []*desiredstatepb.NodePeer, desired *desiredstatepb.Container, existing []engine.ContainerState) (Result, error) {
	result := Result{}
	isWeb := desired.ServiceName == webServiceName
	name, hash, spec, err := r.specFor(desired, revision)
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
		if err := r.opts.Envoy.Ensure(ctx, ingress); err != nil {
			return result, fmt.Errorf("ensure envoy: %w", err)
		}
		if r.opts.IngressCert != nil {
			if err := r.opts.IngressCert.Ensure(ctx, ingress, nodePeers); err != nil {
				return result, fmt.Errorf("ensure ingress certificate: %w", err)
			}
		}
		if err := r.opts.Envoy.Ensure(ctx, ingress); err != nil {
			return result, fmt.Errorf("ensure envoy: %w", err)
		}
		if r.opts.Cloudflared != nil {
			tunnelIngress := ingress
			if normalizedIngressMode(ingress) != ingressModeTunnel {
				tunnelIngress = nil
			}
			if err := r.opts.Cloudflared.Reconcile(ctx, tunnelIngress); err != nil {
				return result, fmt.Errorf("reconcile cloudflared: %w", err)
			}
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
		current = &engine.ContainerState{Name: name, Hash: hash, Service: desired.ServiceName, Running: true}
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

func (r *Reconciler) reconcileWebService(ctx context.Context, desired *desiredstatepb.Container, existing []engine.ContainerState, name, hash string, spec engine.ContainerSpec) (Result, error) {
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

func (r *Reconciler) cutoverWeb(ctx context.Context, name string, desired *desiredstatepb.Container, waitForHealthy bool) error {
	ip, err := r.webContainerIP(ctx, name, desired, waitForHealthy)
	if err != nil {
		return err
	}

	if err := r.opts.Envoy.UpdateEDS(ctx, ip, webPort(desired, r.opts.WebPort)); err != nil {
		return fmt.Errorf("update envoy eds: %w", err)
	}
	if err := r.opts.Envoy.WaitForRoute(ctx, desired.GetHealthcheck().GetPath()); err != nil {
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

func (r *Reconciler) webContainerIP(ctx context.Context, name string, desired *desiredstatepb.Container, waitForHealthy bool) (string, error) {
	if waitForHealthy {
		return r.waitHealthy(ctx, name, desired)
	}

	info, err := r.engine.Inspect(ctx, name)
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w", name, err)
	}
	if !info.Running {
		return "", fmt.Errorf("container %s not running", name)
	}

	ip := info.NetworkIP[r.opts.Network]
	if ip == "" {
		return "", fmt.Errorf("container %s missing network ip on %s", name, r.opts.Network)
	}
	return ip, nil
}

func (r *Reconciler) waitHealthy(ctx context.Context, name string, desired *desiredstatepb.Container) (string, error) {
	healthcheck := desired.GetHealthcheck()
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
			return "", fmt.Errorf("container %s not running", name)
		}

		ip := info.NetworkIP[r.opts.Network]
		if ip == "" {
			lastErr = fmt.Errorf("container %s missing network ip on %s", name, r.opts.Network)
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

func (r *Reconciler) specFor(c *desiredstatepb.Container, revision string) (string, string, engine.ContainerSpec, error) {
	hash, err := desiredstate.HashContainer(c)
	if err != nil {
		return "", "", engine.ContainerSpec{}, fmt.Errorf("hash container %s: %w", c.ServiceName, err)
	}

	name, err := desiredstate.ContainerName(c.ServiceName, revision, hash)
	if err != nil {
		return "", "", engine.ContainerSpec{}, err
	}

	env := make(map[string]string, len(c.Env))
	for k, v := range c.Env {
		env[k] = v
	}

	labels := map[string]string{
		engine.LabelManaged:  "true",
		engine.LabelService:  c.ServiceName,
		engine.LabelHash:     hash,
		engine.LabelRevision: revision,
	}

	spec := engine.ContainerSpec{
		Name:       name,
		Image:      c.Image,
		Entrypoint: c.Entrypoint,
		Command:    c.Command,
		Env:        env,
		Binds:      volumeBinds(c.VolumeMounts),
		Labels:     labels,
		Network:    r.opts.Network,
	}

	return name, hash, spec, nil
}

func (r *Reconciler) specForTask(task *desiredstatepb.Task, revision string) (string, string, engine.ContainerSpec, error) {
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
			engine.LabelManaged:  "true",
			engine.LabelHash:     hash,
			engine.LabelRevision: revision,
			engine.LabelSystem:   task.GetName(),
		},
		Network: r.opts.Network,
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
		return ingressModeTunnel
	}

	mode := strings.TrimSpace(ingress.Mode)
	if mode == "" {
		return ingressModeTunnel
	}
	return mode
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

func webPort(container *desiredstatepb.Container, fallback uint16) uint16 {
	if container != nil && container.Port > 0 {
		return uint16(container.Port)
	}
	return fallback
}

func isNilInterface(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
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

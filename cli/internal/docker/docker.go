package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var digestPattern = regexp.MustCompile(`(?i)\bdigest:\s*(sha256:[0-9a-f]{64})\b`)
var pushStatusInterval = 20 * time.Second

type ContainerStatus struct {
	Exists  bool
	Running bool
	Status  string
	Image   string
}

type ImageMetadata struct {
	ExposedPorts []int
}

type Runner struct{}

func (Runner) Installed() bool {
	_, err := run(context.Background(), nil, "", nil, "docker", "--version")
	return err == nil
}

func (Runner) DaemonReachable() bool {
	_, err := run(context.Background(), nil, "", nil, "docker", "info", "--format", "{{.ServerVersion}}")
	return err == nil
}

func (Runner) Login(ctx context.Context, registryHost, username, password, configDir string) error {
	_, err := run(ctx, map[string]string{"DOCKER_CONFIG": configDir}, password, nil, "docker", "login", "-u", username, "--password-stdin", registryHost)
	if err != nil {
		return fmt.Errorf("docker login failed: %w", err)
	}
	return nil
}

func (Runner) Build(ctx context.Context, contextPath, dockerfile, target string) error {
	_, err := run(ctx, nil, "", nil, "docker", "build", "-f", dockerfile, "-t", target, contextPath)
	if err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

func (Runner) BuildAndPush(ctx context.Context, contextPath, dockerfile, target string, platforms []string, configDir string, update, log func(string)) (string, error) {
	if len(platforms) == 0 {
		return "", errors.New("docker build platforms are required")
	}
	if len(platforms) == 1 {
		if update != nil {
			update("Building container image…")
		}
		_, err := run(ctx, nil, "", log, "docker", "build", "--platform", platforms[0], "-f", dockerfile, "-t", target, contextPath)
		if err != nil {
			return "", fmt.Errorf("docker build failed: %w", err)
		}
		if update != nil {
			update("Pushing image to registry…")
		}
		return (Runner{}).Push(ctx, target, configDir, update, log)
	}
	if update != nil {
		update("Building and pushing multi-platform image…")
	}
	args := []string{
		"buildx", "build",
		"--platform", strings.Join(platforms, ","),
		"-f", dockerfile,
		"-t", target,
		"--push",
		contextPath,
	}
	_, err := run(ctx, buildxEnv(configDir), "", log, "docker", args...)
	if err != nil {
		return "", fmt.Errorf(
			"docker buildx build failed: %w\n\nmulti-platform builds require Docker Buildx; ensure `docker buildx` is installed and set up on this machine",
			err,
		)
	}

	if update != nil {
		update("Resolving pushed image digest…")
	}
	digest, err := resolveRemoteDigest(ctx, target, configDir)
	if err != nil {
		return "", err
	}
	return digest, nil
}

func (Runner) Tag(ctx context.Context, source, target string) error {
	_, err := run(ctx, nil, "", nil, "docker", "tag", source, target)
	if err != nil {
		return fmt.Errorf("docker tag failed: %w", err)
	}
	return nil
}

func (Runner) Push(ctx context.Context, reference, configDir string, update, log func(string)) (string, error) {
	type pushResult struct {
		output string
		err    error
	}

	done := make(chan pushResult, 1)
	go func() {
		output, err := run(ctx, map[string]string{"DOCKER_CONFIG": configDir}, "", log, "docker", "push", reference)
		done <- pushResult{output: output, err: err}
	}()

	if update != nil {
		ticker := time.NewTicker(pushStatusInterval)
		defer ticker.Stop()
		startedAt := time.Now()
		for {
			select {
			case res := <-done:
				return digestFromPushResult(reference, res.output, res.err)
			case <-ticker.C:
				update(pushProgressMessage(reference, time.Since(startedAt)))
			}
		}
	}

	res := <-done
	return digestFromPushResult(reference, res.output, res.err)
}

func digestFromPushResult(reference, output string, err error) (string, error) {
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		return "", formatPushError(reference, err)
	}
	match := digestPattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return "", errors.New("docker push succeeded but no digest was reported")
	}
	return match[1], nil
}

func pushProgressMessage(reference string, elapsed time.Duration) string {
	elapsed = elapsed.Round(time.Second)
	if elapsed < time.Second {
		elapsed = time.Second
	}
	message := fmt.Sprintf("Pushing image to registry… still uploading after %s.", elapsed)
	if isArtifactRegistryReference(reference) {
		return message + " If this stalls, check local Docker connectivity to Artifact Registry (IPv4/IPv6, VPN, firewall). Press Ctrl+C to cancel."
	}
	return message + " Press Ctrl+C to cancel."
}

func formatPushError(reference string, err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if !isArtifactRegistryReference(reference) {
		return fmt.Errorf("docker push failed: %w", err)
	}
	switch {
	case containsAny(text, "client.timeout exceeded while awaiting headers", "network is unreachable", "use of closed network connection", "\neof", "eof\n", " eof"):
		return fmt.Errorf("docker push failed: %w\n\nArtifact Registry upload failed before the image was committed. This usually points to the local Docker network path, often broken IPv6 to `*.pkg.dev`. Retry after fixing Docker networking or forcing Docker over IPv4.", err)
	case strings.Contains(text, "unauthorized: authentication failed"):
		return fmt.Errorf("docker push failed: %w\n\nArtifact Registry push credentials likely expired while Docker kept retrying uploads. Fix the underlying network issue, then rerun deploy to fetch fresh push credentials.", err)
	default:
		return fmt.Errorf("docker push failed: %w", err)
	}
}

func isArtifactRegistryReference(reference string) bool {
	return strings.Contains(reference, ".pkg.dev")
}

func containsAny(text string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func (Runner) WithTemporaryConfig(ctx context.Context, fn func(string) error) error {
	dir, err := os.MkdirTemp("", "devopsellence-docker-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	return fn(dir)
}

func (Runner) ContainerStatus(ctx context.Context, name string) (ContainerStatus, error) {
	output, err := run(ctx, nil, "", nil, "docker", "inspect", name, "--format", "{{json .State}}|{{json .Config.Image}}")
	if err != nil {
		return ContainerStatus{Exists: false, Status: "missing"}, nil
	}
	parts := bytes.SplitN([]byte(output), []byte("|"), 2)
	if len(parts) != 2 {
		return ContainerStatus{Exists: true, Status: "unknown"}, nil
	}
	var state struct {
		Running bool   `json:"Running"`
		Status  string `json:"Status"`
	}
	var image string
	if err := json.Unmarshal(parts[0], &state); err != nil {
		return ContainerStatus{Exists: true, Status: "unknown"}, nil
	}
	if err := json.Unmarshal(parts[1], &image); err != nil {
		image = ""
	}
	return ContainerStatus{
		Exists:  true,
		Running: state.Running,
		Status:  state.Status,
		Image:   image,
	}, nil
}

func (Runner) ImageMetadata(ctx context.Context, reference string) (ImageMetadata, error) {
	output, err := run(ctx, nil, "", nil, "docker", "image", "inspect", reference, "--format", "{{json .Config.ExposedPorts}}")
	if err != nil {
		return ImageMetadata{}, err
	}
	return parseImageMetadata(output)
}

func parseImageMetadata(output string) (ImageMetadata, error) {
	output = strings.TrimSpace(output)
	if output == "" || output == "null" {
		return ImageMetadata{}, nil
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return ImageMetadata{}, err
	}

	ports := make([]int, 0, len(raw))
	for key := range raw {
		port, ok := parseExposedPort(key)
		if !ok {
			continue
		}
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ImageMetadata{ExposedPorts: ports}, nil
}

func parseExposedPort(value string) (int, bool) {
	base := strings.TrimSpace(value)
	if base == "" {
		return 0, false
	}
	if slash := strings.IndexByte(base, '/'); slash >= 0 {
		base = base[:slash]
	}
	port, err := strconv.Atoi(base)
	if err != nil || port <= 0 {
		return 0, false
	}
	return port, true
}

func resolveRemoteDigest(ctx context.Context, reference, configDir string) (string, error) {
	output, err := run(ctx, buildxEnv(configDir), "", nil, "docker", "buildx", "imagetools", "inspect", reference)
	if err != nil {
		return "", fmt.Errorf("docker buildx imagetools inspect failed: %w", err)
	}
	match := digestPattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return "", errors.New("docker buildx imagetools inspect succeeded but no digest was reported")
	}
	return match[1], nil
}

func buildxEnv(configDir string) map[string]string {
	env := map[string]string{"DOCKER_CONFIG": configDir}
	if buildxConfig := defaultBuildxConfigDir(); buildxConfig != "" {
		env["BUILDX_CONFIG"] = buildxConfig
	}
	return env
}

func defaultBuildxConfigDir() string {
	if path := strings.TrimSpace(os.Getenv("BUILDX_CONFIG")); path != "" {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".docker", "buildx")
}

type captureWriter struct {
	mu     sync.Mutex
	full   bytes.Buffer
	line   bytes.Buffer
	stream func(string)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.full.Write(p); err != nil {
		return 0, err
	}
	for _, b := range p {
		switch b {
		case '\n', '\r':
			w.flushLineLocked()
		default:
			w.line.WriteByte(b)
		}
	}
	return len(p), nil
}

func (w *captureWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLineLocked()
}

func (w *captureWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.full.String()
}

func (w *captureWriter) flushLineLocked() {
	line := strings.TrimSpace(w.line.String())
	w.line.Reset()
	if line == "" || w.stream == nil {
		return
	}
	w.stream(line)
}

func run(ctx context.Context, env map[string]string, stdin string, stream func(string), name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	writer := &captureWriter{stream: stream}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Run(); err != nil {
		writer.Flush()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", errors.New("docker CLI not found")
		}
		text := strings.TrimSpace(writer.String())
		if text == "" {
			text = err.Error()
		}
		return "", errors.New(text)
	}
	writer.Flush()
	return writer.String(), nil
}

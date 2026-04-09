package docker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunnerLoginUsesBareRegistryHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test")
	}

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"" + argsPath + "\"\ncat >/dev/null\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	if err := (Runner{}).Login(context.Background(), "example.pkg.dev", "oauth2accesstoken", "token", dir); err != nil {
		t.Fatalf("login: %v", err)
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	args := strings.Fields(string(argsBytes))
	if got := args[len(args)-1]; got != "example.pkg.dev" {
		t.Fatalf("registry host = %q, want bare host", got)
	}
}

func TestRunnerInstalledUsesDockerVersionWithoutDaemon(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test")
	}

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	dockerPath := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\n' \"$@\" > \"" + argsPath + "\"\necho 'Docker version 29.2.1'\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	if !(Runner{}).Installed() {
		t.Fatal("Installed() = false, want true")
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if got := strings.TrimSpace(string(argsBytes)); got != "--version" {
		t.Fatalf("docker args = %q, want --version", got)
	}
}

func TestRunnerBuildAndPushUsesDockerBuildForSinglePlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.txt")
	dockerPath := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1 $2" in
  "build --platform")
    exit 0
    ;;
  "push registry.example/app:test")
    echo "latest: digest: sha256:1111111111111111111111111111111111111111111111111111111111111111 size: 1234"
    exit 0
    ;;
  *)
    echo "unexpected command: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	digest, err := (Runner{}).BuildAndPush(
		context.Background(),
		".",
		"Dockerfile",
		"registry.example/app:test",
		[]string{"linux/amd64"},
		dir,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("build and push: %v", err)
	}
	if digest != "sha256:1111111111111111111111111111111111111111111111111111111111111111" {
		t.Fatalf("digest = %q", digest)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "build --platform linux/amd64 -f Dockerfile -t registry.example/app:test .") {
		t.Fatalf("single-platform build command missing from log:\n%s", log)
	}
	if strings.Contains(log, "buildx build") {
		t.Fatalf("single-platform path should not use buildx:\n%s", log)
	}
	if !strings.Contains(log, "push registry.example/app:test") {
		t.Fatalf("push command missing from log:\n%s", log)
	}
}

func TestRunnerBuildAndPushUsesBuildxForMultiplePlatforms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test")
	}

	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.txt")
	dockerPath := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
case "$1 $2" in
  "buildx build")
    exit 0
    ;;
  "buildx imagetools")
    echo "Name: registry.example/app:test"
    echo "MediaType: application/vnd.oci.image.index.v1+json"
    echo "Digest: sha256:2222222222222222222222222222222222222222222222222222222222222222"
    exit 0
    ;;
  *)
    echo "unexpected command: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	digest, err := (Runner{}).BuildAndPush(
		context.Background(),
		".",
		"Dockerfile",
		"registry.example/app:test",
		[]string{"linux/amd64", "linux/arm64"},
		dir,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("build and push: %v", err)
	}
	if digest != "sha256:2222222222222222222222222222222222222222222222222222222222222222" {
		t.Fatalf("digest = %q", digest)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "buildx build --platform linux/amd64,linux/arm64 -f Dockerfile -t registry.example/app:test --push .") {
		t.Fatalf("multi-platform buildx command missing from log:\n%s", log)
	}
	if !strings.Contains(log, "buildx imagetools inspect registry.example/app:test") {
		t.Fatalf("imagetools inspect missing from log:\n%s", log)
	}
}

func TestRunnerBuildAndPushReportsStageUpdates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test")
	}

	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "docker")
	script := `#!/bin/sh
case "$1" in
  build)
    exit 0
    ;;
  push)
    echo "latest: digest: sha256:1111111111111111111111111111111111111111111111111111111111111111 size: 1234"
    exit 0
    ;;
  *)
    echo "unexpected command: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	var updates []string
	_, err := (Runner{}).BuildAndPush(
		context.Background(),
		".",
		"Dockerfile",
		"registry.example/app:test",
		[]string{"linux/amd64"},
		dir,
		func(message string) {
			updates = append(updates, message)
		},
		nil,
	)
	if err != nil {
		t.Fatalf("build and push: %v", err)
	}
	if strings.Join(updates, "|") != "Building container image…|Pushing image to registry…" {
		t.Fatalf("updates = %#v", updates)
	}
}

func TestRunnerBuildAndPushReportsLongPushStatus(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test")
	}

	originalInterval := pushStatusInterval
	pushStatusInterval = 10 * time.Millisecond
	t.Cleanup(func() {
		pushStatusInterval = originalInterval
	})

	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "docker")
	script := `#!/bin/sh
case "$1" in
  build)
    exit 0
    ;;
  push)
    sleep 0.05
    echo "latest: digest: sha256:1111111111111111111111111111111111111111111111111111111111111111 size: 1234"
    exit 0
    ;;
  *)
    echo "unexpected command: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	var updates []string
	_, err := (Runner{}).BuildAndPush(
		context.Background(),
		".",
		"Dockerfile",
		"northamerica-northeast1-docker.pkg.dev/example/app:test",
		[]string{"linux/amd64"},
		dir,
		func(message string) {
			updates = append(updates, message)
		},
		nil,
	)
	if err != nil {
		t.Fatalf("build and push: %v", err)
	}

	for _, update := range updates {
		if strings.Contains(update, "still uploading after") {
			return
		}
	}
	t.Fatalf("expected long-push update, got %#v", updates)
}

func TestRunnerPushFormatsArtifactRegistryNetworkErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test")
	}

	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "docker")
	script := `#!/bin/sh
echo "Client.Timeout exceeded while awaiting headers" >&2
echo "EOF" >&2
exit 1
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	_, err := (Runner{}).Push(context.Background(), "northamerica-northeast1-docker.pkg.dev/example/app:test", dir, nil, nil)
	if err == nil {
		t.Fatal("expected push to fail")
	}
	if !strings.Contains(err.Error(), "Artifact Registry upload failed before the image was committed") {
		t.Fatalf("expected Artifact Registry hint, got %v", err)
	}
}

func TestRunnerImageMetadataReadsExposedPorts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell wrapper test")
	}

	dir := t.TempDir()
	dockerPath := filepath.Join(dir, "docker")
	script := `#!/bin/sh
case "$1 $2 $3" in
  "image inspect example:test")
    echo '{"80/tcp":{},"443/tcp":{},"abc":{}}'
    exit 0
    ;;
  *)
    echo "unexpected command: $*" >&2
    exit 1
    ;;
esac
`
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}

	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+originalPath)

	metadata, err := (Runner{}).ImageMetadata(context.Background(), "example:test")
	if err != nil {
		t.Fatalf("ImageMetadata() error = %v", err)
	}
	if got := metadata.ExposedPorts; len(got) != 2 || got[0] != 80 || got[1] != 443 {
		t.Fatalf("ExposedPorts = %#v, want [80 443]", got)
	}
}

func TestParseImageMetadataIgnoresNull(t *testing.T) {
	t.Parallel()

	metadata, err := parseImageMetadata("null")
	if err != nil {
		t.Fatalf("parseImageMetadata() error = %v", err)
	}
	if len(metadata.ExposedPorts) != 0 {
		t.Fatalf("ExposedPorts = %#v, want empty", metadata.ExposedPorts)
	}
}

package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/envoy"
)

const (
	ModeShared = "shared"
	ModeSolo   = "solo"
)

type Config struct {
	Mode                         string
	ShowVersion                  bool
	MetricsAddr                  string
	DockerSock                   string
	NetworkName                  string
	PrefetchSystemImages         bool
	StopTimeout                  time.Duration
	DrainDelay                   time.Duration
	ReconcileInterval            time.Duration
	LogLevel                     slog.Level
	StatusPath                   string
	LifecycleStatePath           string
	DesiredStateCachePath        string
	DesiredStateOverridePath     string
	EnvoyImage                   string
	EnvoyContainer               string
	EnvoyBootstrapPath           string
	EnvoyPort                    uint16
	EnvoyUID                     int
	EnvoyGID                     int
	EnvoyTLSCertPath             string
	EnvoyTLSKeyPath              string
	EnvoyPublicHTTPPublishPort   uint16
	EnvoyPublicHTTPSPublishPort  uint16
	IngressCertRenewBefore       time.Duration
	EnvoyRestartPolicy           string
	WebPort                      uint16
	CloudflareTunnelToken        string
	ControlPlaneBaseURL          string
	BootstrapToken               string
	NodeName                     string
	CloudInitInstanceDataPath    string
	AuthStatePath                string
	AuthCheckInterval            time.Duration
	TokenRefreshSkew             time.Duration
	GCSAPIEndpoint               string
	SecretManagerEndpoint        string
	GoogleMetadataEndpoint       string
	GoogleSTSEndpoint            string
	GoogleIAMCredentialsEndpoint string
	GoogleScopes                 []string
}

const DefaultEnvoyImage = "docker.io/envoyproxy/envoy@sha256:d9b4a70739d92b3e28cd407f106b0e90d55df453d7d87773efd22b4429777fe8"

func Load(args []string) (*Config, error) {
	fs := flag.NewFlagSet("devopsellence", flag.ContinueOnError)

	var mode string
	var showVersion bool
	var metricsAddr string
	var dockerSock string
	var networkName string
	var prefetchSystemImages bool
	var stopTimeout time.Duration
	var drainDelay time.Duration
	var reconcileInterval time.Duration
	var logLevel string
	var desiredStateCachePath string
	var desiredStateOverridePath string
	var envoyImage string
	var envoyContainer string
	var envoyBootstrapPath string
	var envoyPort uint
	var envoyUID int
	var envoyGID int
	var envoyTLSCertPath string
	var envoyTLSKeyPath string
	var envoyPublicHTTPPort uint
	var envoyPublicHTTPSPort uint
	var ingressCertRenewBefore time.Duration
	var envoyRestartPolicy string
	var webPort uint
	var cloudflareTunnelTokenFile string
	var controlPlaneBaseURL string
	var bootstrapToken string
	var nodeName string
	var cloudInitInstanceDataPath string
	var authStatePath string
	var authCheckInterval time.Duration
	var tokenRefreshSkew time.Duration
	var gcsAPIEndpoint string
	var secretManagerEndpoint string
	var googleMetadataEndpoint string
	var googleSTSEndpoint string
	var googleIAMCredentialsEndpoint string
	var googleScopesCSV string

	defaultNodeName, err := os.Hostname()
	if err != nil || strings.TrimSpace(defaultNodeName) == "" {
		defaultNodeName = "devopsellence-node"
	}

	fs.StringVar(&mode, "mode", ModeShared, "agent mode: shared or solo (solo skips control plane and GCP)")
	fs.BoolVar(&showVersion, "version", false, "print build version and exit")
	fs.StringVar(&metricsAddr, "metrics-addr", "127.0.0.1:9102", "metrics listen address")
	fs.StringVar(&dockerSock, "docker-sock", "/var/run/docker.sock", "docker socket path")
	fs.StringVar(&networkName, "network", "devopsellence", "docker network name for envoy and workloads")
	fs.BoolVar(&prefetchSystemImages, "prefetch-system-images", envBoolOrDefault("DEVOPSELLENCE_PREFETCH_SYSTEM_IMAGES", true), "prefetch pinned system images once the node becomes assignment-eligible")
	fs.DurationVar(&stopTimeout, "stop-timeout", 10*time.Second, "graceful stop timeout: how long to wait for a container to exit after SIGTERM before sending SIGKILL")
	fs.DurationVar(&drainDelay, "drain-delay", 1*time.Second, "time to wait after Envoy EDS update before sending SIGTERM to the old web container; allows Envoy to reload its endpoint config before the old process stops accepting connections")
	fs.DurationVar(&reconcileInterval, "reconcile-interval", 2*time.Second, "reconcile interval")
	fs.StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn, error")
	fs.StringVar(&desiredStateCachePath, "desired-state-cache-path", "", "path to persisted last-known-good desired state cache (defaults next to auth state)")
	fs.StringVar(&desiredStateOverridePath, "desired-state-override-path", "", "path to optional emergency local desired state override (defaults next to auth state)")
	fs.StringVar(&envoyImage, "envoy-image", DefaultEnvoyImage, "envoy image reference")
	fs.StringVar(&envoyContainer, "envoy-container", "devopsellence-envoy", "envoy container name")
	fs.StringVar(&envoyBootstrapPath, "envoy-bootstrap-path", envOrDefault("DEVOPSELLENCE_ENVOY_BOOTSTRAP_PATH", envoy.DefaultBootstrapPath), "path to the rendered envoy bootstrap file")
	fs.UintVar(&envoyPort, "envoy-port", 8000, "envoy listener port (host+container)")
	fs.IntVar(&envoyUID, "envoy-uid", 101, "numeric uid used by the envoy container process")
	fs.IntVar(&envoyGID, "envoy-gid", 101, "numeric gid used by the envoy container process")
	fs.StringVar(&envoyTLSCertPath, "envoy-tls-cert-path", "", "path to PEM certificate chain for the public HTTPS listener")
	fs.StringVar(&envoyTLSKeyPath, "envoy-tls-key-path", "", "path to PEM private key for the public HTTPS listener")
	fs.UintVar(&envoyPublicHTTPPort, "envoy-public-http-port", 80, "host port to publish for public HTTP ingress")
	fs.UintVar(&envoyPublicHTTPSPort, "envoy-public-https-port", 443, "host port to publish for public HTTPS ingress")
	fs.DurationVar(&ingressCertRenewBefore, "ingress-cert-renew-before", 30*24*time.Hour, "renew public TLS certificates before expiry by this duration")
	fs.StringVar(&envoyRestartPolicy, "envoy-restart-policy", "unless-stopped", "envoy restart policy: no, always, unless-stopped, on-failure")
	fs.UintVar(&webPort, "web-port", 3000, "web service port inside container")
	fs.StringVar(&cloudflareTunnelTokenFile, "cloudflare-tunnel-token-file", "", "path to file containing cloudflared tunnel token")
	fs.StringVar(&controlPlaneBaseURL, "control-plane-base-url", envOrDefault("DEVOPSELLENCE_BASE_URL", ""), "control plane base url")
	fs.StringVar(&bootstrapToken, "bootstrap-token", envOrDefault("DEVOPSELLENCE_BOOTSTRAP_TOKEN", ""), "one-time bootstrap token")
	fs.StringVar(&nodeName, "node-name", defaultNodeName, "node name sent during bootstrap")
	fs.StringVar(&cloudInitInstanceDataPath, "cloud-init-instance-data-path", envOrDefault("DEVOPSELLENCE_CLOUD_INIT_INSTANCE_DATA_PATH", "/run/cloud-init/instance-data.json"), "path to cloud-init instance-data.json; empty disables managed instance id bootstrap hint")
	fs.StringVar(&authStatePath, "auth-state-path", "/var/lib/devopsellence/agent-auth-state.json", "auth state persistence file path")
	fs.DurationVar(&authCheckInterval, "auth-check-interval", 30*time.Second, "auth refresh check interval")
	fs.DurationVar(&tokenRefreshSkew, "token-refresh-skew", 2*time.Minute, "refresh before token expiry by this duration")
	fs.StringVar(&gcsAPIEndpoint, "gcs-api-endpoint", envOrDefault("DEVOPSELLENCE_GCS_API_ENDPOINT", "https://storage.googleapis.com"), "google cloud storage api base endpoint")
	fs.StringVar(&secretManagerEndpoint, "secretmanager-endpoint", envOrDefault("DEVOPSELLENCE_SECRETMANAGER_ENDPOINT", "https://secretmanager.googleapis.com/v1"), "google secret manager api base endpoint")
	fs.StringVar(&googleMetadataEndpoint, "google-metadata-endpoint", "http://metadata.google.internal/computeMetadata/v1", "google metadata server base endpoint; empty disables direct metadata token fetch")
	fs.StringVar(&googleSTSEndpoint, "google-sts-endpoint", "https://sts.googleapis.com/v1/token", "google sts token endpoint")
	fs.StringVar(&googleIAMCredentialsEndpoint, "google-iamcredentials-endpoint", "https://iamcredentials.googleapis.com/v1", "google iam credentials api base endpoint")
	fs.StringVar(&googleScopesCSV, "google-scopes", "https://www.googleapis.com/auth/cloud-platform", "comma separated google oauth scopes")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg := &Config{
		Mode:                         mode,
		ShowVersion:                  showVersion,
		MetricsAddr:                  metricsAddr,
		DockerSock:                   dockerSock,
		NetworkName:                  networkName,
		PrefetchSystemImages:         prefetchSystemImages,
		StopTimeout:                  stopTimeout,
		DrainDelay:                   drainDelay,
		ReconcileInterval:            reconcileInterval,
		EnvoyImage:                   envoyImage,
		EnvoyContainer:               envoyContainer,
		EnvoyBootstrapPath:           strings.TrimSpace(envoyBootstrapPath),
		EnvoyPort:                    uint16(envoyPort),
		EnvoyUID:                     envoyUID,
		EnvoyGID:                     envoyGID,
		EnvoyTLSCertPath:             strings.TrimSpace(envoyTLSCertPath),
		EnvoyTLSKeyPath:              strings.TrimSpace(envoyTLSKeyPath),
		EnvoyPublicHTTPPublishPort:   uint16(envoyPublicHTTPPort),
		EnvoyPublicHTTPSPublishPort:  uint16(envoyPublicHTTPSPort),
		IngressCertRenewBefore:       ingressCertRenewBefore,
		EnvoyRestartPolicy:           envoyRestartPolicy,
		WebPort:                      uint16(webPort),
		ControlPlaneBaseURL:          strings.TrimRight(strings.TrimSpace(controlPlaneBaseURL), "/"),
		BootstrapToken:               strings.TrimSpace(bootstrapToken),
		NodeName:                     strings.TrimSpace(nodeName),
		CloudInitInstanceDataPath:    strings.TrimSpace(cloudInitInstanceDataPath),
		AuthStatePath:                strings.TrimSpace(authStatePath),
		DesiredStateCachePath:        strings.TrimSpace(desiredStateCachePath),
		DesiredStateOverridePath:     strings.TrimSpace(desiredStateOverridePath),
		AuthCheckInterval:            authCheckInterval,
		TokenRefreshSkew:             tokenRefreshSkew,
		GCSAPIEndpoint:               strings.TrimRight(strings.TrimSpace(gcsAPIEndpoint), "/"),
		SecretManagerEndpoint:        strings.TrimRight(strings.TrimSpace(secretManagerEndpoint), "/"),
		GoogleMetadataEndpoint:       strings.TrimRight(strings.TrimSpace(googleMetadataEndpoint), "/"),
		GoogleSTSEndpoint:            strings.TrimSpace(googleSTSEndpoint),
		GoogleIAMCredentialsEndpoint: strings.TrimRight(strings.TrimSpace(googleIAMCredentialsEndpoint), "/"),
		GoogleScopes:                 parseCSV(googleScopesCSV),
	}

	lvl, err := parseLevel(logLevel)
	if err != nil {
		return nil, err
	}
	cfg.LogLevel = lvl

	if cfg.ShowVersion {
		return cfg, nil
	}

	if err := validateUint16Flag("--envoy-port", envoyPort); err != nil {
		return nil, err
	}
	if err := validateUint16Flag("--web-port", webPort); err != nil {
		return nil, err
	}
	if err := validateUint16Flag("--envoy-public-http-port", envoyPublicHTTPPort); err != nil {
		return nil, err
	}
	if err := validateUint16Flag("--envoy-public-https-port", envoyPublicHTTPSPort); err != nil {
		return nil, err
	}

	if cfg.Mode != ModeShared && cfg.Mode != ModeSolo {
		return nil, fmt.Errorf("--mode must be %q or %q", ModeShared, ModeSolo)
	}

	if cfg.AuthStatePath == "" {
		return nil, errors.New("--auth-state-path is required")
	}

	// Derive paths from AuthStatePath directory.
	stateDir := filepath.Dir(cfg.AuthStatePath)
	cfg.StatusPath = filepath.Join(stateDir, "status.json")
	cfg.LifecycleStatePath = filepath.Join(stateDir, "lifecycle-state.json")
	if cfg.DesiredStateCachePath == "" {
		cfg.DesiredStateCachePath = filepath.Join(stateDir, "desired-state-cache.json")
	}
	if cfg.DesiredStateOverridePath == "" {
		cfg.DesiredStateOverridePath = filepath.Join(stateDir, "desired-state-override.json")
	}
	if cfg.EnvoyTLSCertPath == "" {
		cfg.EnvoyTLSCertPath = filepath.Join(stateDir, "ingress-cert.pem")
	}
	if cfg.EnvoyTLSKeyPath == "" {
		cfg.EnvoyTLSKeyPath = filepath.Join(stateDir, "ingress-key.pem")
	}

	if cfg.Mode == ModeSolo {
		// Solo path: only basic validation, no control plane or GCP requirements.
		return cfg, nil
	}

	// Normal mode: full validation.
	if cfg.ControlPlaneBaseURL == "" {
		return nil, errors.New("--control-plane-base-url (or DEVOPSELLENCE_BASE_URL) is required")
	}
	if cfg.NodeName == "" {
		return nil, errors.New("--node-name is required")
	}
	if cfg.EnvoyUID < 0 {
		return nil, errors.New("--envoy-uid cannot be negative")
	}
	if cfg.EnvoyGID < 0 {
		return nil, errors.New("--envoy-gid cannot be negative")
	}
	if cfg.AuthCheckInterval <= 0 {
		return nil, errors.New("--auth-check-interval must be positive")
	}
	if cfg.IngressCertRenewBefore < 0 {
		return nil, errors.New("--ingress-cert-renew-before cannot be negative")
	}
	if cfg.TokenRefreshSkew < 0 {
		return nil, errors.New("--token-refresh-skew cannot be negative")
	}
	if cfg.GoogleSTSEndpoint == "" {
		return nil, errors.New("--google-sts-endpoint is required")
	}
	if cfg.GoogleIAMCredentialsEndpoint == "" {
		return nil, errors.New("--google-iamcredentials-endpoint is required")
	}
	if len(cfg.GoogleScopes) == 0 {
		return nil, errors.New("--google-scopes requires at least one scope")
	}

	if cloudflareTunnelTokenFile != "" {
		token, err := readSecretFile(cloudflareTunnelTokenFile)
		if err != nil {
			return nil, fmt.Errorf("load cloudflare tunnel token file: %w", err)
		}
		cfg.CloudflareTunnelToken = token
	}

	return cfg, nil
}

func readSecretFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("insecure permissions on %s: require owner-only (0600/0400)", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("file is empty: %s", path)
	}
	return value, nil
}

func parseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid log level: %s", level)
	}
}

func validateUint16Flag(name string, value uint) error {
	const maxUint16Value = uint(^uint16(0))
	if value > maxUint16Value {
		return fmt.Errorf("%s must be in range 0-%d, got %d", name, maxUint16Value, value)
	}
	return nil
}

func parseCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch value {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRequiresBaseURL(t *testing.T) {
	if _, err := Load([]string{}); err == nil {
		t.Fatal("expected error without base url")
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load([]string{
		"--control-plane-base-url=https://cp.example.com",
		"--auth-state-path=/tmp/agent-auth-state.json",
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DockerSock == "" || cfg.MetricsAddr == "" {
		t.Fatal("expected defaults for docker sock and metrics addr")
	}
	if !cfg.PrefetchSystemImages {
		t.Fatal("expected system image prefetch to be enabled by default")
	}
	if cfg.NetworkName == "" || cfg.EnvoyPort == 0 {
		t.Fatal("expected envoy defaults")
	}
	if cfg.EnvoyImage != DefaultEnvoyImage {
		t.Fatalf("expected pinned envoy default image, got %s", cfg.EnvoyImage)
	}
	if cfg.EnvoyBootstrapPath != "/etc/devopsellence/envoy/envoy.yaml" {
		t.Fatalf("unexpected envoy bootstrap path default: %s", cfg.EnvoyBootstrapPath)
	}
	if cfg.EnvoyRestartPolicy == "" {
		t.Fatal("expected envoy restart policy default")
	}
	if cfg.WebPort == 0 || cfg.EnvoyPort == 0 {
		t.Fatal("expected default ports")
	}
	if cfg.EnvoyPort != 8000 {
		t.Fatalf("expected default envoy port 8000, got %d", cfg.EnvoyPort)
	}
	if cfg.EnvoyPublicHTTPPort != 80 || cfg.EnvoyPublicHTTPSPort != 443 {
		t.Fatalf("unexpected envoy public publish ports: http=%d https=%d", cfg.EnvoyPublicHTTPPort, cfg.EnvoyPublicHTTPSPort)
	}
	if cfg.EnvoyUID != 101 || cfg.EnvoyGID != 101 {
		t.Fatalf("unexpected envoy uid/gid defaults: %d:%d", cfg.EnvoyUID, cfg.EnvoyGID)
	}
	if cfg.EnvoyTLSCertPath != "/tmp/ingress-cert.pem" || cfg.EnvoyTLSKeyPath != "/tmp/ingress-key.pem" {
		t.Fatalf("unexpected default ingress tls paths: cert=%s key=%s", cfg.EnvoyTLSCertPath, cfg.EnvoyTLSKeyPath)
	}
	if cfg.IngressCertRenewBefore != 30*24*time.Hour {
		t.Fatalf("unexpected default renew window: %s", cfg.IngressCertRenewBefore)
	}
	if cfg.StatusPath == "" {
		t.Fatal("expected status path")
	}
	if cfg.DesiredStateCachePath != "/tmp/desired-state-cache.json" {
		t.Fatalf("expected default desired state cache path, got %s", cfg.DesiredStateCachePath)
	}
	if cfg.DesiredStateOverridePath != "/tmp/desired-state-override.json" {
		t.Fatalf("expected default desired state override path, got %s", cfg.DesiredStateOverridePath)
	}
	if cfg.GoogleMetadataEndpoint != "http://metadata.google.internal/computeMetadata/v1" {
		t.Fatalf("unexpected metadata endpoint default: %s", cfg.GoogleMetadataEndpoint)
	}
	if cfg.CloudInitInstanceDataPath != "/run/cloud-init/instance-data.json" {
		t.Fatalf("unexpected cloud-init instance data path default: %s", cfg.CloudInitInstanceDataPath)
	}
	if cfg.GCSAPIEndpoint != "https://storage.googleapis.com" {
		t.Fatalf("unexpected gcs api endpoint default: %s", cfg.GCSAPIEndpoint)
	}
	if cfg.SecretManagerEndpoint != "https://secretmanager.googleapis.com/v1" {
		t.Fatalf("unexpected secret manager endpoint default: %s", cfg.SecretManagerEndpoint)
	}
}

func TestLoadRejectsOutOfRangeEnvoyPublicPorts(t *testing.T) {
	_, err := Load([]string{
		"--control-plane-base-url=https://cp.example.com",
		"--auth-state-path=/tmp/agent-auth-state.json",
		"--envoy-public-http-port=70000",
	})
	if err == nil {
		t.Fatal("expected error for out-of-range envoy public http port")
	}
	if got := err.Error(); got == "" || !strings.Contains(got, "--envoy-public-http-port") {
		t.Fatalf("expected error to mention flag, got %q", got)
	}
}

func TestLoadVersionSkipsValidation(t *testing.T) {
	cfg, err := Load([]string{"--version", "--envoy-public-http-port=70000"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.ShowVersion {
		t.Fatal("expected version flag to be set")
	}
}

func TestLoadRequiresBaseURLInRemote(t *testing.T) {
	if _, err := Load([]string{"--auth-state-path=/tmp/state.json"}); err == nil {
		t.Fatal("expected error without base url")
	}
}

func TestLoadFullConfig(t *testing.T) {
	cfg, err := Load([]string{
		"--control-plane-base-url=https://cp.example.com",
		"--auth-state-path=/tmp/agent-auth-state.json",
		"--desired-state-cache-path=/var/lib/devopsellence/custom-cache.json",
		"--desired-state-override-path=/var/lib/devopsellence/custom-override.json",
		"--prefetch-system-images=false",
		"--envoy-bootstrap-path=/var/lib/devopsellence/envoy/envoy.yaml",
		"--envoy-public-http-port=18080",
		"--envoy-public-https-port=18443",
		"--gcs-api-endpoint=https://fake-gcs.example.test",
		"--secretmanager-endpoint=https://fake-secretmanager.example.test/v1/",
		"--cloud-init-instance-data-path=",
		"--google-metadata-endpoint=",
		"--google-scopes=https://www.googleapis.com/auth/cloud-platform,https://www.googleapis.com/auth/devstorage.read_only",
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ControlPlaneBaseURL != "https://cp.example.com" {
		t.Fatalf("unexpected base url: %s", cfg.ControlPlaneBaseURL)
	}
	if len(cfg.GoogleScopes) != 2 {
		t.Fatalf("expected 2 scopes, got %v", cfg.GoogleScopes)
	}
	if cfg.StatusPath == "" {
		t.Fatal("expected status path")
	}
	if cfg.DesiredStateCachePath != "/var/lib/devopsellence/custom-cache.json" {
		t.Fatalf("unexpected desired state cache path: %s", cfg.DesiredStateCachePath)
	}
	if cfg.DesiredStateOverridePath != "/var/lib/devopsellence/custom-override.json" {
		t.Fatalf("unexpected desired state override path: %s", cfg.DesiredStateOverridePath)
	}
	if cfg.GoogleMetadataEndpoint != "" {
		t.Fatalf("expected metadata endpoint disabled, got %s", cfg.GoogleMetadataEndpoint)
	}
	if cfg.CloudInitInstanceDataPath != "" {
		t.Fatalf("expected cloud-init instance data path disabled, got %s", cfg.CloudInitInstanceDataPath)
	}
	if cfg.PrefetchSystemImages {
		t.Fatal("expected system image prefetch to be disabled")
	}
	if cfg.EnvoyBootstrapPath != "/var/lib/devopsellence/envoy/envoy.yaml" {
		t.Fatalf("unexpected envoy bootstrap path: %s", cfg.EnvoyBootstrapPath)
	}
	if cfg.EnvoyPublicHTTPPort != 18080 || cfg.EnvoyPublicHTTPSPort != 18443 {
		t.Fatalf("unexpected envoy public publish ports: http=%d https=%d", cfg.EnvoyPublicHTTPPort, cfg.EnvoyPublicHTTPSPort)
	}
	if cfg.GCSAPIEndpoint != "https://fake-gcs.example.test" {
		t.Fatalf("unexpected gcs api endpoint: %s", cfg.GCSAPIEndpoint)
	}
	if cfg.SecretManagerEndpoint != "https://fake-secretmanager.example.test/v1" {
		t.Fatalf("unexpected secret manager endpoint: %s", cfg.SecretManagerEndpoint)
	}
}

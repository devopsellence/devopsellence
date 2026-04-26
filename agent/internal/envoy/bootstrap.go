package envoy

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

const DefaultBootstrapPath = "/etc/devopsellence/envoy/envoy.yaml"

type originListenerConfig struct {
	Port     uint16
	CertPath string
	KeyPath  string
}

type publicIngressListenerConfig struct {
	HTTPPort         uint16
	HTTPSPort        uint16
	Hosts            []string
	TLSEnabled       bool
	ChallengeEnabled bool
	RedirectHTTP     bool
	ChallengeHost    string
	ChallengePort    uint16
	CertificatePEM   []byte
	PrivateKeyPEM    []byte
	Routes           []*desiredstatepb.IngressRoute
}

// xdsSocketName is the Unix socket filename placed alongside the bootstrap YAML.
// The node agent binds this socket; Envoy connects to it through the shared volume mount.
const xdsSocketName = "xds.sock"

func xdsSocketPath(bootstrapPath string) string {
	return filepath.Join(filepath.Dir(bootstrapPath), xdsSocketName)
}

func ensureBootstrap(path, networkName string) (bool, error) {
	socketPath := xdsSocketPath(path)
	contents := []byte(defaultBootstrap(networkName, socketPath))

	info, err := os.Stat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("envoy bootstrap is not a regular file: %s", path)
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return false, fmt.Errorf("read envoy bootstrap: %w", readErr)
		}
		if bytes.Equal(existing, contents) {
			return false, nil
		}
	} else {
		if !os.IsNotExist(err) {
			return false, fmt.Errorf("stat envoy bootstrap: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("mkdir envoy bootstrap dir: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".envoy-bootstrap-*.tmp")
	if err != nil {
		return false, fmt.Errorf("create temp envoy bootstrap: %w", err)
	}
	defer os.Remove(file.Name())

	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("write envoy bootstrap: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return false, fmt.Errorf("sync envoy bootstrap: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("close envoy bootstrap: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return false, fmt.Errorf("rename envoy bootstrap: %w", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		return false, fmt.Errorf("chmod envoy bootstrap: %w", err)
	}
	return true, nil
}

func defaultBootstrap(networkName, socketPath string) string {
	return fmt.Sprintf(`node:
  id: %s
  cluster: %s

dynamic_resources:
  ads_config:
    api_type: GRPC
    transport_api_version: V3
    grpc_services:
      - envoy_grpc:
          cluster_name: xds_cluster
  lds_config:
    resource_api_version: V3
    ads: {}
  cds_config:
    resource_api_version: V3
    ads: {}

static_resources:
  clusters:
    - name: xds_cluster
      type: STATIC
      connect_timeout: 5s
      typed_extension_protocol_options:
        envoy.extensions.upstreams.http.v3.HttpProtocolOptions:
          "@type": type.googleapis.com/envoy.extensions.upstreams.http.v3.HttpProtocolOptions
          explicit_http_config:
            http2_protocol_options: {}
      load_assignment:
        cluster_name: xds_cluster
        endpoints:
          - lb_endpoints:
              - endpoint:
                  address:
                    pipe:
                      path: %s

admin:
  access_log:
    - name: envoy.access_loggers.file
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.access_loggers.file.v3.FileAccessLog
        path: /dev/stdout
        log_format:
          text_format_source:
            inline_string: "[%%START_TIME%%] admin %%REQ(:METHOD)%% %%REQ(PATH)%% %%RESPONSE_CODE%%\n"
  address:
    socket_address:
      address: 127.0.0.1
      port_value: 9901

overload_manager:
  refresh_interval: 0.25s
  resource_monitors:
    - name: envoy.resource_monitors.global_downstream_max_connections
      typed_config:
        "@type": type.googleapis.com/envoy.extensions.resource_monitors.downstream_connections.v3.DownstreamConnectionsConfig
        max_active_downstream_connections: 10000
`, xdsNodeID, networkName, socketPath)
}

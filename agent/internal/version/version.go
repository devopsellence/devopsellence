package version

import (
	"fmt"
	"runtime"
	"strings"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

const (
	CapabilitiesHeader            = "devopsellence-agent-capabilities"
	DesiredStatePointerCapability = "desired_state_pointer.v1"
	DiagnoseCapability            = "diagnose.v1"
	DirectDNSIngressCapability    = "direct_dns_ingress.v1"
	RegistryPullAuthHTTP          = "registry_pull_auth_http.v1"
	ReleaseTaskCapability         = "release_task.v1"
	SecretRefHTTPCapability       = "secret_ref_https.v1"
	DesiredStateHTTPCapability    = "desired_state_https.v1"
)

func String() string {
	return fmt.Sprintf("devopsellence %s (commit %s, built %s)", Version, Commit, BuildTime)
}

func UserAgent() string {
	v := strings.TrimSpace(Version)
	if v == "" {
		v = "dev"
	}
	return fmt.Sprintf("devopsellence-agent/%s (%s; %s)", v, runtime.GOOS, runtime.GOARCH)
}

func CapabilityHeaderValue() string {
	return strings.Join([]string{
		DesiredStatePointerCapability,
		DesiredStateHTTPCapability,
		DiagnoseCapability,
		DirectDNSIngressCapability,
		RegistryPullAuthHTTP,
		ReleaseTaskCapability,
		SecretRefHTTPCapability,
	}, ",")
}

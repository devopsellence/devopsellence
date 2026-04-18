package solo

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/devopsellence/cli/internal/config"
)

// Desired-state JSON types matching the agent protobuf schema (camelCase keys).
// We use plain encoding/json rather than importing protobuf.

type desiredStateJSON struct {
	Revision       string          `json:"revision,omitempty"`
	Containers     []containerJSON `json:"containers,omitempty"`
	Ingress        *ingressJSON    `json:"ingress,omitempty"`
	ReleaseCommand *taskJSON       `json:"releaseCommand,omitempty"`
	NodePeers      []nodePeerJSON  `json:"nodePeers,omitempty"`
}

type containerJSON struct {
	ServiceName  string            `json:"serviceName"`
	Image        string            `json:"image"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Healthcheck  *healthcheckJSON  `json:"healthcheck,omitempty"`
	Port         int               `json:"port,omitempty"`
	VolumeMounts []volumeMountJSON `json:"volumeMounts,omitempty"`
}

type healthcheckJSON struct {
	Path               string `json:"path,omitempty"`
	Port               int    `json:"port,omitempty"`
	IntervalSeconds    int64  `json:"intervalSeconds,omitempty"`
	TimeoutSeconds     int64  `json:"timeoutSeconds,omitempty"`
	Retries            int32  `json:"retries,omitempty"`
	StartPeriodSeconds int64  `json:"startPeriodSeconds,omitempty"`
}

type volumeMountJSON struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type taskJSON struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	VolumeMounts []volumeMountJSON `json:"volumeMounts,omitempty"`
}

type ingressJSON struct {
	Hosts        []string       `json:"hosts,omitempty"`
	Mode         string         `json:"mode,omitempty"`
	TLS          ingressTLSJSON `json:"tls,omitempty"`
	RedirectHTTP bool           `json:"redirectHttp,omitempty"`
}

type ingressTLSJSON struct {
	Mode           string `json:"mode,omitempty"`
	Email          string `json:"email,omitempty"`
	CADirectoryURL string `json:"caDirectoryUrl,omitempty"`
}

type NodePeer struct {
	Name          string
	Labels        []string
	PublicAddress string
}

type nodePeerJSON struct {
	Name          string   `json:"name,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	PublicAddress string   `json:"publicAddress,omitempty"`
}

// BuildDesiredState produces desired-state JSON from a ProjectConfig, image tag,
// git revision, and pre-resolved secrets. Secrets are merged into env vars;
// no secret_refs appear in the output.
func BuildDesiredState(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string) ([]byte, error) {
	return BuildDesiredStateForLabels(cfg, imageTag, revision, secrets, nil, cfg.ReleaseCommand != "")
}

// BuildDesiredStateForLabels produces desired-state JSON for one solo node.
// A nil labels slice preserves the legacy solo behavior: run all configured
// services. A non-nil labels slice schedules only matching services.
func BuildDesiredStateForLabels(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string, labels []string, includeReleaseCommand bool) ([]byte, error) {
	return BuildDesiredStateForNode(cfg, imageTag, revision, secrets, labels, false, includeReleaseCommand)
}

// BuildDesiredStateForNode produces desired-state JSON for one node, including
// public ingress only when the node has the web label.
func BuildDesiredStateForNode(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string, labels []string, webNode bool, includeReleaseCommand bool, nodePeers ...[]NodePeer) ([]byte, error) {
	ds := desiredStateJSON{
		Revision: revision,
	}
	if len(nodePeers) > 0 {
		ds.NodePeers = buildNodePeers(nodePeers[0])
	}

	if labels == nil || hasLabel(labels, config.NodeLabelWeb) {
		webContainer, err := buildContainer("web", cfg.Web, imageTag, secrets)
		if err != nil {
			return nil, fmt.Errorf("build web container: %w", err)
		}
		ds.Containers = append(ds.Containers, webContainer)
	}

	if webNode && cfg.Ingress != nil && (labels == nil || hasLabel(labels, config.NodeLabelWeb)) {
		ds.Ingress = buildIngress(cfg.Ingress)
	}

	if cfg.Worker != nil && (labels == nil || hasLabel(labels, config.NodeLabelWorker)) {
		workerContainer, err := buildContainer("worker", *cfg.Worker, imageTag, secrets)
		if err != nil {
			return nil, fmt.Errorf("build worker container: %w", err)
		}
		ds.Containers = append(ds.Containers, workerContainer)
	}

	if includeReleaseCommand && cfg.ReleaseCommand != "" {
		env, err := mergeEnv(cfg.Web.Env, cfg.Web.SecretRefs, secrets)
		if err != nil {
			return nil, fmt.Errorf("build release command: %w", err)
		}
		var vols []volumeMountJSON
		for _, v := range cfg.Web.Volumes {
			vols = append(vols, volumeMountJSON{Source: v.Source, Target: v.Target})
		}
		ds.ReleaseCommand = &taskJSON{
			Name:         "release",
			Image:        imageTag,
			Command:      shellCommand(cfg.ReleaseCommand),
			Env:          env,
			VolumeMounts: vols,
		}
	}

	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal desired state: %w", err)
	}
	return data, nil
}

func buildIngress(ingress *config.IngressConfig) *ingressJSON {
	if ingress == nil || len(ingress.Hosts) == 0 {
		return nil
	}
	mode := strings.TrimSpace(ingress.TLS.Mode)
	if mode == "" {
		mode = "auto"
	}
	return &ingressJSON{
		Hosts: append([]string(nil), ingress.Hosts...),
		Mode:  "public",
		TLS: ingressTLSJSON{
			Mode:           mode,
			Email:          strings.TrimSpace(ingress.TLS.Email),
			CADirectoryURL: strings.TrimSpace(ingress.TLS.CADirectoryURL),
		},
		RedirectHTTP: ingress.RedirectHTTP,
	}
}

func buildNodePeers(peers []NodePeer) []nodePeerJSON {
	out := make([]nodePeerJSON, 0, len(peers))
	for _, peer := range peers {
		name := strings.TrimSpace(peer.Name)
		address := strings.TrimSpace(peer.PublicAddress)
		labels := normalizedLabels(peer.Labels)
		if name == "" && address == "" && len(labels) == 0 {
			continue
		}
		out = append(out, nodePeerJSON{
			Name:          name,
			Labels:        labels,
			PublicAddress: address,
		})
	}
	return out
}

func normalizedLabels(labels []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, label := range labels {
		if strings.TrimSpace(label) == want {
			return true
		}
	}
	return false
}

func buildContainer(serviceName string, svc config.ServiceConfig, imageTag string, secrets map[string]string) (containerJSON, error) {
	env, err := mergeEnv(svc.Env, svc.SecretRefs, secrets)
	if err != nil {
		return containerJSON{}, err
	}
	c := containerJSON{
		ServiceName: serviceName,
		Image:       imageTag,
		Env:         env,
	}

	if svc.Entrypoint != "" {
		c.Entrypoint = shellCommand(svc.Entrypoint)
	}
	if svc.Command != "" {
		c.Command = shellCommand(svc.Command)
	}

	if svc.Healthcheck != nil {
		c.Healthcheck = &healthcheckJSON{
			Path:               svc.Healthcheck.Path,
			Port:               svc.Healthcheck.Port,
			IntervalSeconds:    5,
			TimeoutSeconds:     2,
			Retries:            3,
			StartPeriodSeconds: 1,
		}
	}

	if svc.Port > 0 {
		c.Port = svc.Port
	}

	for _, v := range svc.Volumes {
		c.VolumeMounts = append(c.VolumeMounts, volumeMountJSON{
			Source: v.Source,
			Target: v.Target,
		})
	}

	return c, nil
}

// mergeEnv combines static env, secret_refs resolved via the secrets map, into
// a single env map. Secret values override static env for the same key.
// Returns an error if any secret_ref references a secret not present in the secrets map.
func mergeEnv(env map[string]string, secretRefs []config.SecretRef, secrets map[string]string) (map[string]string, error) {
	merged := make(map[string]string, len(env)+len(secretRefs))
	for k, v := range env {
		merged[k] = v
	}
	var missing []string
	for _, ref := range secretRefs {
		if val, ok := secrets[ref.Name]; ok {
			merged[ref.Name] = val
		} else {
			missing = append(missing, ref.Name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing secrets: %s (run `devopsellence secret set <name>` to add them)", strings.Join(missing, ", "))
	}
	return merged, nil
}

// shellCommand wraps a command string as shell -c invocation.
func shellCommand(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	return []string{"sh", "-c", cmd}
}

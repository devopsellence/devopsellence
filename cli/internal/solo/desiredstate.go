package solo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/devopsellence/cli/internal/config"
)

// Desired-state JSON types matching the agent protobuf schema (camelCase keys).
// We use plain encoding/json rather than importing protobuf.

type desiredStateJSON struct {
	SchemaVersion int               `json:"schemaVersion,omitempty"`
	Revision      string            `json:"revision,omitempty"`
	Environments  []environmentJSON `json:"environments,omitempty"`
	Ingress       *ingressJSON      `json:"ingress,omitempty"`
	NodePeers     []nodePeerJSON    `json:"nodePeers,omitempty"`
}

type environmentJSON struct {
	Name     string        `json:"name"`
	Revision string        `json:"revision,omitempty"`
	Services []serviceJSON `json:"services,omitempty"`
	Tasks    []taskJSON    `json:"tasks,omitempty"`
}

type serviceJSON struct {
	Name         string            `json:"name"`
	Kind         string            `json:"kind,omitempty"`
	Image        string            `json:"image"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Healthcheck  *healthcheckJSON  `json:"healthcheck,omitempty"`
	Ports        []servicePortJSON `json:"ports,omitempty"`
	VolumeMounts []volumeMountJSON `json:"volumeMounts,omitempty"`
}

type servicePortJSON struct {
	Name string `json:"name,omitempty"`
	Port int    `json:"port,omitempty"`
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
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	VolumeMounts []volumeMountJSON `json:"volumeMounts,omitempty"`
}

type ingressJSON struct {
	Hosts        []string           `json:"hosts,omitempty"`
	Mode         string             `json:"mode,omitempty"`
	TLS          ingressTLSJSON     `json:"tls,omitempty"`
	RedirectHTTP bool               `json:"redirectHttp,omitempty"`
	Routes       []ingressRouteJSON `json:"routes,omitempty"`
}

type ingressTLSJSON struct {
	Mode           string `json:"mode,omitempty"`
	Email          string `json:"email,omitempty"`
	CADirectoryURL string `json:"caDirectoryUrl,omitempty"`
}

type ingressRouteJSON struct {
	Match  ingressMatchJSON  `json:"match"`
	Target ingressTargetJSON `json:"target"`
}

type ingressMatchJSON struct {
	Hostname   string `json:"hostname"`
	PathPrefix string `json:"pathPrefix,omitempty"`
}

type ingressTargetJSON struct {
	Environment string `json:"environment"`
	Service     string `json:"service"`
	Port        string `json:"port,omitempty"`
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
	return BuildDesiredStateForLabels(cfg, imageTag, revision, secrets, nil, cfg.ReleaseTask() != nil)
}

// BuildDesiredStateForLabels produces desired-state JSON for one solo node.
// A nil labels slice runs all configured services. A non-nil labels slice
// schedules only matching services.
func BuildDesiredStateForLabels(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string, labels []string, includeReleaseTask bool) ([]byte, error) {
	return BuildDesiredStateForNode(cfg, imageTag, revision, secrets, labels, false, includeReleaseTask)
}

// BuildDesiredStateForNode produces desired-state JSON for one node, including
// public ingress only when the node has the web label.
func BuildDesiredStateForNode(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string, labels []string, ingressNode bool, includeReleaseTask bool, nodePeers ...[]NodePeer) ([]byte, error) {
	ds := desiredStateJSON{
		SchemaVersion: 2,
		Revision:      revision,
	}
	if len(nodePeers) > 0 {
		ds.NodePeers = buildNodePeers(nodePeers[0])
	}

	environment := environmentJSON{
		Name:     strings.TrimSpace(cfg.DefaultEnvironment),
		Revision: revision,
	}
	if environment.Name == "" {
		environment.Name = config.DefaultEnvironment
	}

	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		if !shouldScheduleService(labels, service.Kind) {
			continue
		}
		rendered, err := buildService(serviceName, service, imageTag, secrets)
		if err != nil {
			return nil, fmt.Errorf("build service %s: %w", serviceName, err)
		}
		environment.Services = append(environment.Services, rendered)
	}

	if ingressNode && cfg.Ingress != nil && shouldScheduleIngress(labels, cfg) {
		ds.Ingress = buildIngress(cfg.Ingress, environment.Name)
	}

	if includeReleaseTask && cfg.ReleaseTask() != nil && shouldScheduleReleaseTask(labels, cfg) {
		releaseTask, err := buildReleaseTask(cfg, imageTag, secrets)
		if err != nil {
			return nil, fmt.Errorf("build release task: %w", err)
		}
		environment.Tasks = append(environment.Tasks, releaseTask)
	}
	ds.Environments = append(ds.Environments, environment)

	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal desired state: %w", err)
	}
	return data, nil
}

func BuildAggregatedDesiredState(nodeName string, currentNode config.SoloNode, snapshots []DeploySnapshot, releaseNodes map[string]string, nodePeers []NodePeer) ([]byte, error) {
	ds := desiredStateJSON{
		SchemaVersion: 2,
	}
	if len(nodePeers) > 0 {
		ds.NodePeers = buildNodePeers(nodePeers)
	}

	attached := append([]DeploySnapshot(nil), snapshots...)
	sort.Slice(attached, func(i, j int) bool {
		if attached[i].WorkspaceKey != attached[j].WorkspaceKey {
			return attached[i].WorkspaceKey < attached[j].WorkspaceKey
		}
		return attached[i].Environment < attached[j].Environment
	})

	mergedIngress, err := mergeIngressForNode(currentNode.Labels, attached)
	if err != nil {
		return nil, err
	}
	ds.Ingress = mergedIngress

	for _, snapshot := range attached {
		environment := environmentJSON{
			Name:     defaultEnvironmentName(snapshot.Environment),
			Revision: strings.TrimSpace(snapshot.Revision),
		}
		for _, service := range snapshot.Services {
			if !shouldScheduleService(currentNode.Labels, service.Kind) {
				continue
			}
			environment.Services = append(environment.Services, service)
		}
		if snapshot.ReleaseTask != nil && strings.TrimSpace(releaseNodes[snapshotKey(snapshot)]) == nodeName && shouldScheduleService(currentNode.Labels, snapshot.ReleaseServiceKind) {
			environment.Tasks = append(environment.Tasks, *snapshot.ReleaseTask)
		}
		ds.Environments = append(ds.Environments, environment)
	}

	revision, err := syntheticRevision(ds)
	if err != nil {
		return nil, err
	}
	ds.Revision = revision

	data, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal desired state: %w", err)
	}
	return data, nil
}

func buildIngress(ingress *config.IngressConfig, environmentName string) *ingressJSON {
	if ingress == nil || len(ingress.Hosts) == 0 {
		return nil
	}
	mode := strings.TrimSpace(ingress.TLS.Mode)
	if mode == "" {
		mode = "auto"
	}
	routes := make([]ingressRouteJSON, 0, len(ingress.Hosts))
	for _, host := range ingress.Hosts {
		routes = append(routes, ingressRouteJSON{
			Match: ingressMatchJSON{
				Hostname: host,
			},
			Target: ingressTargetJSON{
				Environment: environmentName,
				Service:     ingress.Service,
				Port:        "http",
			},
		})
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
		Routes:       routes,
	}
}

func mergeIngressForNode(labels []string, snapshots []DeploySnapshot) (*ingressJSON, error) {
	var merged *ingressJSON
	hostSet := map[string]bool{}
	routeSet := map[string]bool{}

	for _, snapshot := range snapshots {
		if snapshot.Ingress == nil || !shouldScheduleService(labels, snapshot.IngressServiceKind) {
			continue
		}
		if merged == nil {
			merged = &ingressJSON{
				Mode:         snapshot.Ingress.Mode,
				TLS:          snapshot.Ingress.TLS,
				RedirectHTTP: snapshot.Ingress.RedirectHTTP,
			}
		} else if merged.TLS != snapshot.Ingress.TLS || merged.RedirectHTTP != snapshot.Ingress.RedirectHTTP || merged.Mode != snapshot.Ingress.Mode {
			return nil, fmt.Errorf("cannot merge ingress for co-hosted environments with different TLS settings")
		}
		for _, host := range snapshot.Ingress.Hosts {
			if hostSet[host] {
				continue
			}
			hostSet[host] = true
			merged.Hosts = append(merged.Hosts, host)
		}
		for _, route := range snapshot.Ingress.Routes {
			key := route.Match.Hostname + "\n" + route.Match.PathPrefix + "\n" + route.Target.Environment + "\n" + route.Target.Service + "\n" + route.Target.Port
			if routeSet[key] {
				continue
			}
			routeSet[key] = true
			merged.Routes = append(merged.Routes, route)
		}
	}
	if merged == nil {
		return nil, nil
	}
	sort.Strings(merged.Hosts)
	sort.Slice(merged.Routes, func(i, j int) bool {
		left := merged.Routes[i]
		right := merged.Routes[j]
		if left.Match.Hostname != right.Match.Hostname {
			return left.Match.Hostname < right.Match.Hostname
		}
		if left.Target.Environment != right.Target.Environment {
			return left.Target.Environment < right.Target.Environment
		}
		if left.Target.Service != right.Target.Service {
			return left.Target.Service < right.Target.Service
		}
		return left.Match.PathPrefix < right.Match.PathPrefix
	})
	return merged, nil
}

func syntheticRevision(ds desiredStateJSON) (string, error) {
	copyValue := ds
	copyValue.Revision = ""
	data, err := json.Marshal(copyValue)
	if err != nil {
		return "", fmt.Errorf("marshal synthetic revision input: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func snapshotKey(snapshot DeploySnapshot) string {
	return strings.TrimSpace(snapshot.WorkspaceKey) + "\n" + defaultEnvironmentName(snapshot.Environment)
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

func shouldScheduleService(labels []string, kind string) bool {
	if labels == nil {
		return true
	}
	return hasLabel(labels, kind)
}

func shouldScheduleIngress(labels []string, cfg *config.ProjectConfig) bool {
	if cfg == nil || cfg.Ingress == nil {
		return false
	}
	service, ok := cfg.Services[cfg.Ingress.Service]
	if !ok {
		return false
	}
	return shouldScheduleService(labels, service.Kind)
}

func shouldScheduleReleaseTask(labels []string, cfg *config.ProjectConfig) bool {
	release := cfg.ReleaseTask()
	if release == nil {
		return false
	}
	service, ok := cfg.Services[release.Service]
	if !ok {
		return false
	}
	return shouldScheduleService(labels, service.Kind)
}

func hasLabel(labels []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, label := range labels {
		if strings.TrimSpace(label) == want {
			return true
		}
	}
	return false
}

func buildService(serviceName string, svc config.ServiceConfig, imageTag string, secrets map[string]string) (serviceJSON, error) {
	env, err := mergeEnv(svc.Env, svc.SecretRefs, secrets)
	if err != nil {
		return serviceJSON{}, err
	}
	image := strings.TrimSpace(svc.Image)
	if image == "" {
		image = imageTag
	}
	c := serviceJSON{
		Name:  serviceName,
		Kind:  svc.Kind,
		Image: image,
		Env:   env,
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

	for _, port := range svc.Ports {
		c.Ports = append(c.Ports, servicePortJSON{Name: port.Name, Port: port.Port})
	}

	for _, v := range svc.Volumes {
		c.VolumeMounts = append(c.VolumeMounts, volumeMountJSON{
			Source: v.Source,
			Target: v.Target,
		})
	}

	return c, nil
}

func buildReleaseTask(cfg *config.ProjectConfig, imageTag string, secrets map[string]string) (taskJSON, error) {
	release := cfg.ReleaseTask()
	if release == nil {
		return taskJSON{}, fmt.Errorf("release task not configured")
	}
	service, ok := cfg.Services[release.Service]
	if !ok {
		return taskJSON{}, fmt.Errorf("service %q not found", release.Service)
	}
	baseEnv := mergeStringMaps(service.Env, release.Env)
	env, err := mergeEnv(baseEnv, service.SecretRefs, secrets)
	if err != nil {
		return taskJSON{}, err
	}
	image := strings.TrimSpace(service.Image)
	if image == "" {
		image = imageTag
	}
	task := taskJSON{
		Name:  "release",
		Image: image,
		Env:   env,
	}
	if strings.TrimSpace(release.Entrypoint) != "" {
		task.Entrypoint = shellCommand(release.Entrypoint)
	} else if strings.TrimSpace(service.Entrypoint) != "" {
		task.Entrypoint = shellCommand(service.Entrypoint)
	}
	if strings.TrimSpace(release.Command) != "" {
		task.Command = shellCommand(release.Command)
	} else if strings.TrimSpace(service.Command) != "" {
		task.Command = shellCommand(service.Command)
	}
	for _, v := range service.Volumes {
		task.VolumeMounts = append(task.VolumeMounts, volumeMountJSON{Source: v.Source, Target: v.Target})
	}
	return task, nil
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

func mergeStringMaps(parts ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, part := range parts {
		for key, value := range part {
			merged[key] = value
		}
	}
	return merged
}

// shellCommand wraps a command string as shell -c invocation.
func shellCommand(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	return []string{"sh", "-c", cmd}
}

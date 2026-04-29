package desiredstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
)

// Desired-state JSON types matching the agent protobuf schema (camelCase keys).
// We use plain encoding/json rather than importing protobuf.

type DesiredStateJSON struct {
	SchemaVersion int               `json:"schemaVersion,omitempty"`
	Revision      string            `json:"revision,omitempty"`
	Environments  []EnvironmentJSON `json:"environments,omitempty"`
	Ingress       *IngressJSON      `json:"ingress,omitempty"`
	NodePeers     []NodePeerJSON    `json:"nodePeers,omitempty"`
}

type EnvironmentJSON struct {
	Name     string        `json:"name"`
	Revision string        `json:"revision,omitempty"`
	Services []ServiceJSON `json:"services,omitempty"`
	Tasks    []TaskJSON    `json:"tasks,omitempty"`
}

type ServiceJSON struct {
	Name         string            `json:"name"`
	Kind         string            `json:"kind,omitempty"`
	Image        string            `json:"image"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Healthcheck  *HealthcheckJSON  `json:"healthcheck,omitempty"`
	Ports        []ServicePortJSON `json:"ports,omitempty"`
	VolumeMounts []VolumeMountJSON `json:"volumeMounts,omitempty"`
}

type ServicePortJSON struct {
	Name string `json:"name,omitempty"`
	Port int    `json:"port,omitempty"`
}

type HealthcheckJSON struct {
	Path               string `json:"path,omitempty"`
	Port               int    `json:"port,omitempty"`
	IntervalSeconds    int64  `json:"intervalSeconds,omitempty"`
	TimeoutSeconds     int64  `json:"timeoutSeconds,omitempty"`
	Retries            int32  `json:"retries,omitempty"`
	StartPeriodSeconds int64  `json:"startPeriodSeconds,omitempty"`
}

type VolumeMountJSON struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type TaskJSON struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	Entrypoint   []string          `json:"entrypoint,omitempty"`
	Command      []string          `json:"command,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	VolumeMounts []VolumeMountJSON `json:"volumeMounts,omitempty"`
}

type IngressJSON struct {
	Hosts        []string           `json:"hosts,omitempty"`
	Mode         string             `json:"mode,omitempty"`
	TLS          IngressTLSJSON     `json:"tls,omitempty"`
	RedirectHTTP bool               `json:"redirectHttp,omitempty"`
	Routes       []IngressRouteJSON `json:"routes,omitempty"`
}

type IngressTLSJSON struct {
	Mode           string `json:"mode,omitempty"`
	Email          string `json:"email,omitempty"`
	CADirectoryURL string `json:"caDirectoryUrl,omitempty"`
}

type IngressRouteJSON struct {
	Match  IngressMatchJSON  `json:"match"`
	Target IngressTargetJSON `json:"target"`
}

type IngressMatchJSON struct {
	Hostname   string `json:"hostname"`
	PathPrefix string `json:"pathPrefix,omitempty"`
}

type IngressTargetJSON struct {
	Environment string `json:"environment"`
	Service     string `json:"service"`
	Port        string `json:"port,omitempty"`
}

type NodePeer struct {
	Name          string
	Labels        []string
	PublicAddress string
}

type NodePeerJSON struct {
	Name          string   `json:"name,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	PublicAddress string   `json:"publicAddress,omitempty"`
}

type ScopedSecrets map[string]map[string]string

func (s ScopedSecrets) ValuesForService(serviceName string) map[string]string {
	merged := map[string]string{}
	for key, value := range s[""] {
		merged[key] = value
	}
	for key, value := range s[serviceName] {
		merged[key] = value
	}
	return merged
}

type SnapshotMetadata struct {
	ConfigPath string `json:"config_path,omitempty"`
	Project    string `json:"project,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type DeploySnapshot struct {
	WorkspaceRoot      string              `json:"workspace_root"`
	WorkspaceKey       string              `json:"workspace_key"`
	Environment        string              `json:"environment"`
	Revision           string              `json:"revision"`
	Image              string              `json:"image"`
	Services           []ServiceJSON       `json:"services,omitempty"`
	ReleaseTask        *TaskJSON           `json:"release_task,omitempty"`
	ReleaseService     string              `json:"release_service,omitempty"`
	ReleaseServiceKind string              `json:"release_service_kind,omitempty"`
	Ingress            *IngressJSON        `json:"ingress,omitempty"`
	IngressService     string              `json:"ingress_service,omitempty"`
	IngressServiceKind string              `json:"ingress_service_kind,omitempty"`
	SecretRefs         map[string][]string `json:"secret_refs,omitempty"`
	Metadata           SnapshotMetadata    `json:"metadata,omitempty"`
}

// BuildDesiredState produces desired-state JSON from a ProjectConfig, image tag,
// git revision, and pre-resolved secrets. Secrets are merged into env vars;
// no secret_refs appear in the output.
func BuildDesiredState(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string) ([]byte, error) {
	return buildDesiredStateForNode(cfg, imageTag, revision, func(string) map[string]string { return secrets }, nil, false, cfg.ReleaseTask() != nil)
}

func BuildDesiredStateWithScopedSecrets(cfg *config.ProjectConfig, imageTag, revision string, secrets ScopedSecrets) ([]byte, error) {
	return BuildDesiredStateForNodeWithScopedSecrets(cfg, imageTag, revision, secrets, nil, false, cfg.ReleaseTask() != nil)
}

// BuildDesiredStateForLabels produces desired-state JSON for one solo node.
// A nil labels slice runs all configured services. A non-nil labels slice
// schedules only matching services.
func BuildDesiredStateForLabels(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string, labels []string, includeReleaseTask bool) ([]byte, error) {
	return buildDesiredStateForNode(cfg, imageTag, revision, func(string) map[string]string { return secrets }, labels, false, includeReleaseTask)
}

// BuildDesiredStateForNode produces desired-state JSON for one node, including
// public ingress only when the node has the web label.
func BuildDesiredStateForNode(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string, labels []string, ingressNode bool, includeReleaseTask bool, nodePeers ...[]NodePeer) ([]byte, error) {
	return buildDesiredStateForNode(cfg, imageTag, revision, func(string) map[string]string { return secrets }, labels, ingressNode, includeReleaseTask, nodePeers...)
}

func BuildDesiredStateForNodeWithScopedSecrets(cfg *config.ProjectConfig, imageTag, revision string, secrets ScopedSecrets, labels []string, ingressNode bool, includeReleaseTask bool, nodePeers ...[]NodePeer) ([]byte, error) {
	return buildDesiredStateForNode(cfg, imageTag, revision, secrets.ValuesForService, labels, ingressNode, includeReleaseTask, nodePeers...)
}

func buildDesiredStateForNode(cfg *config.ProjectConfig, imageTag, revision string, secretsForService func(string) map[string]string, labels []string, ingressNode bool, includeReleaseTask bool, nodePeers ...[]NodePeer) ([]byte, error) {
	ds := DesiredStateJSON{
		SchemaVersion: 2,
		Revision:      revision,
	}
	if len(nodePeers) > 0 {
		ds.NodePeers = buildNodePeers(nodePeers[0])
	}

	environment := EnvironmentJSON{
		Name:     strings.TrimSpace(cfg.DefaultEnvironment),
		Revision: revision,
	}
	if environment.Name == "" {
		environment.Name = config.DefaultEnvironment
	}

	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		serviceKind := effectiveServiceKind(serviceName, service)
		if !shouldScheduleService(labels, serviceKind) {
			continue
		}
		rendered, err := buildService(serviceName, service, imageTag, secretsForService(serviceName))
		if err != nil {
			return nil, fmt.Errorf("build service %s: %w", serviceName, err)
		}
		environment.Services = append(environment.Services, rendered)
	}

	if ingressNode && cfg.Ingress != nil && shouldScheduleIngress(labels, cfg) {
		ds.Ingress = buildIngress(cfg.Ingress, environment.Name)
	}

	if includeReleaseTask && cfg.ReleaseTask() != nil && shouldScheduleReleaseTask(labels, cfg) {
		releaseTask, err := buildReleaseTask(cfg, imageTag, secretsForService(cfg.ReleaseTask().Service))
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

type NodePublicationInput struct {
	NodeName     string
	CurrentNode  config.Node
	Snapshots    []DeploySnapshot
	ReleaseNodes map[string]string
	NodePeers    []NodePeer
}

type NodePublication struct {
	NodeName         string
	DesiredStateJSON []byte
}

func PlanNodePublication(input NodePublicationInput) (NodePublication, error) {
	data, err := buildAggregatedDesiredState(input.NodeName, input.CurrentNode, input.Snapshots, input.ReleaseNodes, input.NodePeers)
	if err != nil {
		return NodePublication{}, err
	}
	return NodePublication{NodeName: strings.TrimSpace(input.NodeName), DesiredStateJSON: data}, nil
}

func buildAggregatedDesiredState(nodeName string, currentNode config.Node, snapshots []DeploySnapshot, releaseNodes map[string]string, nodePeers []NodePeer) ([]byte, error) {
	ds := DesiredStateJSON{
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
	environmentNames := aggregatedEnvironmentNames(attached)

	mergedIngress, err := mergeIngressForNode(currentNode.Labels, attached, environmentNames)
	if err != nil {
		return nil, err
	}
	ds.Ingress = mergedIngress

	for _, snapshot := range attached {
		environmentName := environmentNames[snapshotKey(snapshot)]
		environment := EnvironmentJSON{
			Name:     environmentName,
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

func aggregatedEnvironmentNames(snapshots []DeploySnapshot) map[string]string {
	names := map[string]string{}
	for _, snapshot := range snapshots {
		key := snapshotKey(snapshot)
		base := defaultEnvironmentName(snapshot.Environment)
		// Solo nodes can host multiple projects with the same logical environment
		// name. Keep the runtime environment name project-scoped even after peers
		// attach/detach so republishing one project does not rename and recreate a
		// healthy co-hosted project's containers.
		names[key] = uniqueAggregatedEnvironmentName(snapshot, base)
	}
	return names
}

func uniqueAggregatedEnvironmentName(snapshot DeploySnapshot, base string) string {
	hashSource := strings.TrimSpace(snapshot.WorkspaceKey)
	if hashSource == "" {
		hashSource = strings.TrimSpace(snapshot.WorkspaceRoot)
	}
	sum := sha256.Sum256([]byte(hashSource))
	suffix := hex.EncodeToString(sum[:4])
	project := normalizeEnvironmentNameToken(snapshot.Metadata.Project)
	if project == "" {
		return base + "-" + suffix
	}
	return project + "-" + base + "-" + suffix
}

func normalizeEnvironmentNameToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func buildIngress(ingress *config.IngressConfig, environmentName string) *IngressJSON {
	if ingress == nil || len(ingress.Hosts) == 0 || len(ingress.Rules) == 0 {
		return nil
	}
	mode := strings.TrimSpace(ingress.TLS.Mode)
	if mode == "" {
		mode = "auto"
	}
	routes := make([]IngressRouteJSON, 0, len(ingress.Rules))
	for _, rule := range ingress.Rules {
		pathPrefix := strings.TrimSpace(rule.Match.PathPrefix)
		if pathPrefix == "" {
			pathPrefix = "/"
		}
		routes = append(routes, IngressRouteJSON{
			Match: IngressMatchJSON{
				Hostname:   strings.TrimSpace(rule.Match.Host),
				PathPrefix: pathPrefix,
			},
			Target: IngressTargetJSON{
				Environment: environmentName,
				Service:     strings.TrimSpace(rule.Target.Service),
				Port:        strings.TrimSpace(rule.Target.Port),
			},
		})
	}
	redirectHTTP := true
	if ingress.RedirectHTTP != nil {
		redirectHTTP = *ingress.RedirectHTTP
	}
	return &IngressJSON{
		Hosts: append([]string(nil), ingress.Hosts...),
		Mode:  "public",
		TLS: IngressTLSJSON{
			Mode:           mode,
			Email:          strings.TrimSpace(ingress.TLS.Email),
			CADirectoryURL: strings.TrimSpace(ingress.TLS.CADirectoryURL),
		},
		RedirectHTTP: redirectHTTP,
		Routes:       routes,
	}
}

func mergeIngressForNode(labels []string, snapshots []DeploySnapshot, environmentNames map[string]string) (*IngressJSON, error) {
	var merged *IngressJSON
	hostSet := map[string]bool{}
	routeSet := map[string]string{}

	for _, snapshot := range snapshots {
		if snapshot.Ingress == nil || !snapshotShouldScheduleIngress(labels, snapshot) {
			continue
		}
		if merged == nil {
			merged = &IngressJSON{
				Mode:         snapshot.Ingress.Mode,
				TLS:          snapshot.Ingress.TLS,
				RedirectHTTP: snapshot.Ingress.RedirectHTTP,
			}
		} else if merged.TLS != snapshot.Ingress.TLS || merged.RedirectHTTP != snapshot.Ingress.RedirectHTTP || normalizedIngressMode(merged.Mode) != normalizedIngressMode(snapshot.Ingress.Mode) {
			differingSettings := []string{}
			if merged.TLS != snapshot.Ingress.TLS {
				differingSettings = append(differingSettings, "TLS")
			}
			if normalizedIngressMode(merged.Mode) != normalizedIngressMode(snapshot.Ingress.Mode) {
				differingSettings = append(differingSettings, "mode")
			}
			if merged.RedirectHTTP != snapshot.Ingress.RedirectHTTP {
				differingSettings = append(differingSettings, "redirect_http")
			}
			return nil, fmt.Errorf("cannot merge ingress for co-hosted environments with different settings: %s", strings.Join(differingSettings, ", "))
		}
		for _, host := range snapshot.Ingress.Hosts {
			if hostSet[host] {
				continue
			}
			hostSet[host] = true
			merged.Hosts = append(merged.Hosts, host)
		}
		for _, route := range snapshot.Ingress.Routes {
			routeCopy := route
			if environmentName := environmentNames[snapshotKey(snapshot)]; environmentName != "" {
				routeCopy.Target.Environment = environmentName
			}
			routeKey := routeCopy.Match.Hostname + "\n" + routeCopy.Match.PathPrefix
			if existing, ok := routeSet[routeKey]; ok {
				currentTarget := routeCopy.Target.Environment + "\n" + routeCopy.Target.Service + "\n" + routeCopy.Target.Port
				if existing == currentTarget {
					continue
				}
				return nil, fmt.Errorf("cannot merge ingress for co-hosted environments with duplicate route: %s%s", routeCopy.Match.Hostname, routeCopy.Match.PathPrefix)
			}
			routeSet[routeKey] = routeCopy.Target.Environment + "\n" + routeCopy.Target.Service + "\n" + routeCopy.Target.Port
			merged.Routes = append(merged.Routes, routeCopy)
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
		if left.Match.PathPrefix != right.Match.PathPrefix {
			return left.Match.PathPrefix < right.Match.PathPrefix
		}
		return left.Target.Port < right.Target.Port
	})
	return merged, nil
}

func normalizedIngressMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "public"
	}
	return mode
}

func snapshotShouldScheduleIngress(labels []string, snapshot DeploySnapshot) bool {
	serviceKinds := map[string]string{}
	for _, service := range snapshot.Services {
		serviceKinds[strings.TrimSpace(service.Name)] = strings.TrimSpace(service.Kind)
	}
	serviceNames := ingressTargetServiceNamesFromRoutes(snapshot.Ingress)
	if len(serviceNames) == 0 {
		return false
	}
	for _, serviceName := range serviceNames {
		kind, ok := serviceKinds[serviceName]
		if !ok || !shouldScheduleService(labels, kind) {
			return false
		}
	}
	return true
}

func ingressTargetServiceNames(ingress *config.IngressConfig) []string {
	serviceSet := map[string]bool{}
	serviceNames := []string{}
	if ingress == nil {
		return serviceNames
	}
	for _, rule := range ingress.Rules {
		serviceName := strings.TrimSpace(rule.Target.Service)
		if serviceName == "" || serviceSet[serviceName] {
			continue
		}
		serviceSet[serviceName] = true
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Strings(serviceNames)
	return serviceNames
}

func ingressTargetServiceNamesFromRoutes(ingress *IngressJSON) []string {
	serviceSet := map[string]bool{}
	serviceNames := []string{}
	if ingress == nil {
		return serviceNames
	}
	for _, route := range ingress.Routes {
		serviceName := strings.TrimSpace(route.Target.Service)
		if serviceName == "" || serviceSet[serviceName] {
			continue
		}
		serviceSet[serviceName] = true
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Strings(serviceNames)
	return serviceNames
}

func syntheticRevision(ds DesiredStateJSON) (string, error) {
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

func buildNodePeers(peers []NodePeer) []NodePeerJSON {
	out := make([]NodePeerJSON, 0, len(peers))
	for _, peer := range peers {
		name := strings.TrimSpace(peer.Name)
		address := strings.TrimSpace(peer.PublicAddress)
		labels := normalizedLabels(peer.Labels)
		if name == "" && address == "" && len(labels) == 0 {
			continue
		}
		out = append(out, NodePeerJSON{
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
	serviceNames := ingressTargetServiceNames(cfg.Ingress)
	if len(serviceNames) == 0 {
		return false
	}
	for _, serviceName := range serviceNames {
		service, ok := cfg.Services[serviceName]
		if !ok {
			return false
		}
		if !shouldScheduleService(labels, effectiveServiceKind(serviceName, service)) {
			return false
		}
	}
	return true
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
	return shouldScheduleService(labels, effectiveServiceKind(release.Service, service))
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

func effectiveServiceKind(name string, svc config.ServiceConfig) string {
	return config.InferredServiceKind(name, svc)
}

func buildService(serviceName string, svc config.ServiceConfig, imageTag string, secrets map[string]string) (ServiceJSON, error) {
	env, err := mergeEnv(svc.Env, svc.SecretRefs, secrets)
	if err != nil {
		return ServiceJSON{}, err
	}
	image := strings.TrimSpace(svc.Image)
	if image == "" {
		image = imageTag
	}
	c := ServiceJSON{
		Name:  serviceName,
		Kind:  effectiveServiceKind(serviceName, svc),
		Image: image,
		Env:   env,
	}

	if len(svc.Command) > 0 {
		c.Entrypoint = append([]string(nil), svc.Command...)
	}
	if len(svc.Args) > 0 {
		c.Command = append([]string(nil), svc.Args...)
	}

	if svc.Healthcheck != nil {
		c.Healthcheck = &HealthcheckJSON{
			Path:               svc.Healthcheck.Path,
			Port:               svc.Healthcheck.Port,
			IntervalSeconds:    5,
			TimeoutSeconds:     2,
			Retries:            3,
			StartPeriodSeconds: 1,
		}
	}

	for _, port := range svc.Ports {
		c.Ports = append(c.Ports, ServicePortJSON{Name: port.Name, Port: port.Port})
	}

	for _, v := range svc.Volumes {
		c.VolumeMounts = append(c.VolumeMounts, VolumeMountJSON{
			Source: v.Source,
			Target: v.Target,
		})
	}

	return c, nil
}

func buildReleaseTask(cfg *config.ProjectConfig, imageTag string, secrets map[string]string) (TaskJSON, error) {
	release := cfg.ReleaseTask()
	if release == nil {
		return TaskJSON{}, fmt.Errorf("release task not configured")
	}
	service, ok := cfg.Services[release.Service]
	if !ok {
		return TaskJSON{}, fmt.Errorf("service %q not found", release.Service)
	}
	baseEnv := mergeStringMaps(service.Env, release.Env)
	env, err := mergeEnv(baseEnv, service.SecretRefs, secrets)
	if err != nil {
		return TaskJSON{}, err
	}
	image := strings.TrimSpace(service.Image)
	if image == "" {
		image = imageTag
	}
	task := TaskJSON{
		Name:  "release",
		Image: image,
		Env:   env,
	}
	if len(release.Command) > 0 {
		task.Entrypoint = append([]string(nil), release.Command...)
	} else if len(service.Command) > 0 {
		task.Entrypoint = append([]string(nil), service.Command...)
	}
	if len(release.Args) > 0 {
		task.Command = append([]string(nil), release.Args...)
	} else if len(service.Args) > 0 {
		task.Command = append([]string(nil), service.Args...)
	}
	for _, v := range service.Volumes {
		task.VolumeMounts = append(task.VolumeMounts, VolumeMountJSON{Source: v.Source, Target: v.Target})
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

func BuildService(serviceName string, svc config.ServiceConfig, imageTag string, secrets map[string]string) (ServiceJSON, error) {
	return buildService(serviceName, svc, imageTag, secrets)
}

func BuildReleaseTask(cfg *config.ProjectConfig, imageTag string, secrets map[string]string) (TaskJSON, error) {
	return buildReleaseTask(cfg, imageTag, secrets)
}

func BuildIngress(ingress *config.IngressConfig, environmentName string) *IngressJSON {
	return buildIngress(ingress, environmentName)
}

func MergeIngressForNode(labels []string, snapshots []DeploySnapshot, environmentNames map[string]string) (*IngressJSON, error) {
	return mergeIngressForNode(labels, snapshots, environmentNames)
}

func AggregatedEnvironmentNames(snapshots []DeploySnapshot) map[string]string {
	return aggregatedEnvironmentNames(snapshots)
}

func defaultEnvironmentName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.DefaultEnvironment
	}
	return name
}

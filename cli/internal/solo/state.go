package solo

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devopsellence/cli/internal/state"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
	corerelease "github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/release"
)

const soloStateSchemaVersion = 1

type StateStore struct {
	Path string
}

type State struct {
	SchemaVersion int                                    `json:"schema_version"`
	Nodes         map[string]config.Node                 `json:"nodes,omitempty"`
	Attachments   map[string]AttachmentRecord            `json:"attachments,omitempty"`
	Snapshots     map[string]desiredstate.DeploySnapshot `json:"snapshots,omitempty"`
	Releases      map[string]corerelease.Release         `json:"releases,omitempty"`
	Current       map[string]string                      `json:"current_releases,omitempty"`
	Deployments   map[string]corerelease.Deployment      `json:"deployments,omitempty"`
	Secrets       map[string]SecretRecord                `json:"secrets,omitempty"`
}

type AttachmentRecord struct {
	WorkspaceRoot string   `json:"workspace_root"`
	WorkspaceKey  string   `json:"workspace_key"`
	Environment   string   `json:"environment"`
	NodeNames     []string `json:"node_names,omitempty"`
}

type SecretRecord struct {
	WorkspaceRoot string `json:"workspace_root"`
	WorkspaceKey  string `json:"workspace_key"`
	Environment   string `json:"environment"`
	ServiceName   string `json:"service_name"`
	Name          string `json:"name"`
	Store         string `json:"store,omitempty"`
	Value         string `json:"value"`
	Reference     string `json:"reference,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func DefaultStatePath() string {
	return state.DefaultPath(filepath.Join("devopsellence", "solo", "state.json"))
}

func NewStateStore(path string) *StateStore {
	return &StateStore{Path: path}
}

func (s *StateStore) Read() (State, error) {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return newState(), nil
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return newState(), nil
	}
	if err != nil {
		return State{}, err
	}
	var current State
	if err := json.Unmarshal(data, &current); err != nil {
		return State{}, err
	}
	if err := validateStateSchemaVersion(current.SchemaVersion); err != nil {
		return State{}, err
	}
	current.SchemaVersion = soloStateSchemaVersion
	current.ensureDefaults()
	current, err = normalizeState(current)
	if err != nil {
		return State{}, err
	}
	return current, nil
}

func (s *StateStore) Write(current State) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return errors.New("solo state store path is required")
	}
	if err := validateStateSchemaVersion(current.SchemaVersion); err != nil {
		return err
	}
	current.SchemaVersion = soloStateSchemaVersion
	current.ensureDefaults()
	var err error
	current, err = normalizeState(current)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomicPrivate(s.Path, data); err != nil {
		return err
	}
	return nil
}

func writeFileAtomicPrivate(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return os.Chmod(path, 0o600)
}

func (s *StateStore) Update(fn func(*State) error) error {
	current, err := s.Read()
	if err != nil {
		return err
	}
	if err := fn(&current); err != nil {
		return err
	}
	return s.Write(current)
}

func validateStateSchemaVersion(version int) error {
	switch version {
	case 0, soloStateSchemaVersion:
		return nil
	default:
		return fmt.Errorf("unsupported solo state schema_version %d", version)
	}
}

func CanonicalWorkspaceKey(workspaceRoot string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && strings.TrimSpace(resolved) != "" {
		abs = resolved
	}
	return filepath.Clean(abs), nil
}

func EnvironmentStateKey(workspaceRoot, environment string) (string, error) {
	workspaceKey, err := CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return "", err
	}
	environment = strings.TrimSpace(environment)
	if environment == "" {
		environment = config.DefaultEnvironment
	}
	return workspaceKey + "\n" + environment, nil
}

func BuildDeploySnapshot(cfg *config.ProjectConfig, workspaceRoot, configPath, imageTag, revision string, secrets map[string]string) (desiredstate.DeploySnapshot, error) {
	return buildDeploySnapshot(cfg, workspaceRoot, configPath, imageTag, revision, func(string) map[string]string { return secrets })
}

func BuildDeploySnapshotWithScopedSecrets(cfg *config.ProjectConfig, workspaceRoot, configPath, imageTag, revision string, secrets ScopedSecrets) (desiredstate.DeploySnapshot, error) {
	return buildDeploySnapshot(cfg, workspaceRoot, configPath, imageTag, revision, secrets.ValuesForService)
}

func buildDeploySnapshot(cfg *config.ProjectConfig, workspaceRoot, configPath, imageTag, revision string, secretsForService func(string) map[string]string) (desiredstate.DeploySnapshot, error) {
	if cfg == nil {
		return desiredstate.DeploySnapshot{}, fmt.Errorf("config is required")
	}
	workspaceKey, err := CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return desiredstate.DeploySnapshot{}, err
	}
	environmentName := strings.TrimSpace(cfg.DefaultEnvironment)
	if environmentName == "" {
		environmentName = config.DefaultEnvironment
	}
	snapshot := desiredstate.DeploySnapshot{
		WorkspaceRoot: workspaceRoot,
		WorkspaceKey:  workspaceKey,
		Environment:   environmentName,
		Revision:      strings.TrimSpace(revision),
		Image:         strings.TrimSpace(imageTag),
		Metadata: desiredstate.SnapshotMetadata{
			ConfigPath: strings.TrimSpace(configPath),
			Project:    strings.TrimSpace(cfg.Project),
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		},
	}
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		for _, ref := range service.SecretRefs {
			name := strings.TrimSpace(ref.Name)
			if name == "" {
				continue
			}
			if snapshot.SecretRefs == nil {
				snapshot.SecretRefs = map[string][]string{}
			}
			snapshot.SecretRefs[serviceName] = append(snapshot.SecretRefs[serviceName], name)
		}
		rendered, err := desiredstate.BuildService(serviceName, service, imageTag, secretsForService(serviceName))
		if err != nil {
			return desiredstate.DeploySnapshot{}, fmt.Errorf("build service %s: %w", serviceName, err)
		}
		snapshot.Services = append(snapshot.Services, rendered)
	}
	if cfg.ReleaseTask() != nil {
		releaseTask, err := desiredstate.BuildReleaseTask(cfg, imageTag, secretsForService(cfg.ReleaseTask().Service))
		if err != nil {
			return desiredstate.DeploySnapshot{}, fmt.Errorf("build release task: %w", err)
		}
		snapshot.ReleaseTask = &releaseTask
		snapshot.ReleaseService = cfg.ReleaseTask().Service
		if service, ok := cfg.Services[cfg.ReleaseTask().Service]; ok {
			snapshot.ReleaseServiceKind = config.InferredServiceKind(cfg.ReleaseTask().Service, service)
		}
	}
	if cfg.Ingress != nil {
		snapshot.Ingress = desiredstate.BuildIngress(cfg.Ingress, environmentName)
		if serviceName, ok := ingressSnapshotService(cfg); ok {
			snapshot.IngressService = serviceName
			if service, ok := cfg.Services[serviceName]; ok {
				snapshot.IngressServiceKind = config.InferredServiceKind(serviceName, service)
			}
		}
	}
	return snapshot, nil
}

func ingressSnapshotService(cfg *config.ProjectConfig) (string, bool) {
	if cfg == nil || cfg.Ingress == nil {
		return "", false
	}
	serviceNames := map[string]struct{}{}
	for _, rule := range cfg.Ingress.Rules {
		serviceName := strings.TrimSpace(rule.Target.Service)
		if serviceName == "" {
			continue
		}
		serviceNames[serviceName] = struct{}{}
	}
	if len(serviceNames) != 1 {
		return "", false
	}
	for serviceName := range serviceNames {
		return serviceName, true
	}
	return "", false
}

func RedactDeploySnapshotSecrets(snapshot desiredstate.DeploySnapshot, cfg *config.ProjectConfig) desiredstate.DeploySnapshot {
	if cfg == nil {
		return snapshot
	}
	serviceSecretRefs := map[string][]string{}
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		for _, ref := range service.SecretRefs {
			serviceSecretRefs[serviceName] = append(serviceSecretRefs[serviceName], ref.Name)
		}
	}
	for i := range snapshot.Services {
		snapshot.Services[i].Env = redactedEnv(snapshot.Services[i].Env, serviceSecretRefs[snapshot.Services[i].Name])
	}
	if snapshot.ReleaseTask != nil && cfg.ReleaseTask() != nil {
		snapshot.ReleaseTask.Env = redactedEnv(snapshot.ReleaseTask.Env, serviceSecretRefs[cfg.ReleaseTask().Service])
	}
	return snapshot
}

func redactedEnv(env map[string]string, secretNames []string) map[string]string {
	if len(env) == 0 || len(secretNames) == 0 {
		return env
	}
	redacted := make(map[string]string, len(env))
	for key, value := range env {
		redacted[key] = value
	}
	for _, name := range secretNames {
		delete(redacted, name)
	}
	return redacted
}

func newState() State {
	return State{
		SchemaVersion: soloStateSchemaVersion,
		Nodes:         map[string]config.Node{},
		Attachments:   map[string]AttachmentRecord{},
		Snapshots:     map[string]desiredstate.DeploySnapshot{},
		Releases:      map[string]corerelease.Release{},
		Current:       map[string]string{},
		Deployments:   map[string]corerelease.Deployment{},
		Secrets:       map[string]SecretRecord{},
	}
}

func (s *State) ensureDefaults() {
	if s.SchemaVersion == 0 {
		s.SchemaVersion = soloStateSchemaVersion
	}
	if s.Nodes == nil {
		s.Nodes = map[string]config.Node{}
	}
	if s.Attachments == nil {
		s.Attachments = map[string]AttachmentRecord{}
	}
	if s.Snapshots == nil {
		s.Snapshots = map[string]desiredstate.DeploySnapshot{}
	}
	if s.Releases == nil {
		s.Releases = map[string]corerelease.Release{}
	}
	if s.Current == nil {
		s.Current = map[string]string{}
	}
	if s.Deployments == nil {
		s.Deployments = map[string]corerelease.Deployment{}
	}
	if s.Secrets == nil {
		s.Secrets = map[string]SecretRecord{}
	}
}

func normalizeState(current State) (State, error) {
	nodeNames := make([]string, 0, len(current.Nodes))
	for name := range current.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)
	normalizedNodes := make(map[string]config.Node, len(current.Nodes))
	for _, name := range nodeNames {
		node := current.Nodes[name]
		normalized, err := normalizeAndValidateNode(name, node)
		if err != nil {
			return State{}, err
		}
		normalizedNodes[name] = normalized
	}
	current.Nodes = normalizedNodes

	attachmentKeys := make([]string, 0, len(current.Attachments))
	for key := range current.Attachments {
		attachmentKeys = append(attachmentKeys, key)
	}
	sort.Strings(attachmentKeys)
	normalizedAttachments := make(map[string]AttachmentRecord, len(current.Attachments))
	for _, key := range attachmentKeys {
		normalizedKey, attachment, err := normalizeAttachmentRecord(key, current.Attachments[key])
		if err != nil {
			return State{}, fmt.Errorf("normalize attachment %q: %w", key, err)
		}
		if existing, ok := normalizedAttachments[normalizedKey]; ok {
			attachment.NodeNames = normalizeNodeNames(append(existing.NodeNames, attachment.NodeNames...))
			if strings.TrimSpace(attachment.WorkspaceRoot) == "" {
				attachment.WorkspaceRoot = existing.WorkspaceRoot
			}
		}
		normalizedAttachments[normalizedKey] = attachment
	}
	current.Attachments = normalizedAttachments

	snapshotKeys := make([]string, 0, len(current.Snapshots))
	for key := range current.Snapshots {
		snapshotKeys = append(snapshotKeys, key)
	}
	sort.Strings(snapshotKeys)
	normalizedSnapshots := make(map[string]desiredstate.DeploySnapshot, len(current.Snapshots))
	for _, key := range snapshotKeys {
		normalizedKey, snapshot, err := normalizeSnapshotRecord(key, current.Snapshots[key])
		if err != nil {
			return State{}, fmt.Errorf("normalize snapshot %q: %w", key, err)
		}
		normalizedSnapshots[normalizedKey] = snapshot
	}
	current.Snapshots = normalizedSnapshots

	releaseIDs := make([]string, 0, len(current.Releases))
	for id := range current.Releases {
		releaseIDs = append(releaseIDs, id)
	}
	sort.Strings(releaseIDs)
	normalizedReleases := make(map[string]corerelease.Release, len(current.Releases))
	for _, id := range releaseIDs {
		release := current.Releases[id]
		release.ID = strings.TrimSpace(firstNonEmpty(release.ID, id))
		release.EnvironmentID = strings.TrimSpace(release.EnvironmentID)
		release.Revision = strings.TrimSpace(release.Revision)
		release.CreatedAt = strings.TrimSpace(release.CreatedAt)
		if release.ID == "" {
			return State{}, fmt.Errorf("release %q id is required", id)
		}
		if release.EnvironmentID == "" {
			return State{}, fmt.Errorf("release %q environment_id is required", id)
		}
		if release.Revision == "" {
			return State{}, fmt.Errorf("release %q revision is required", id)
		}
		if _, err := time.Parse(time.RFC3339Nano, release.CreatedAt); err != nil {
			return State{}, fmt.Errorf("release %q has invalid created_at: %w", id, err)
		}
		normalizedKey, snapshot, err := normalizeSnapshotRecord(release.EnvironmentID, release.Snapshot)
		if err != nil {
			return State{}, fmt.Errorf("release %q snapshot is invalid: %w", id, err)
		}
		release.EnvironmentID = normalizedKey
		release.Snapshot = snapshot
		release.Snapshot.Revision = release.Revision
		if _, exists := normalizedReleases[release.ID]; exists {
			return State{}, fmt.Errorf("duplicate release id %q after normalization", release.ID)
		}
		normalizedReleases[release.ID] = release
	}
	current.Releases = normalizedReleases

	currentKeys := make([]string, 0, len(current.Current))
	for key := range current.Current {
		currentKeys = append(currentKeys, key)
	}
	sort.Strings(currentKeys)
	normalizedCurrent := make(map[string]string, len(current.Current))
	for _, key := range currentKeys {
		_, workspaceKey, err := normalizeWorkspaceIdentity(key, "", "")
		if err != nil {
			return State{}, fmt.Errorf("normalize current release %q: %w", key, err)
		}
		_, keyEnvironment := splitEnvironmentStateKey(key)
		environment := defaultEnvironmentName(keyEnvironment)
		releaseID := strings.TrimSpace(current.Current[key])
		if releaseID == "" {
			continue
		}
		normalizedKey := workspaceKey + "\n" + environment
		release, ok := current.Releases[releaseID]
		if !ok {
			return State{}, fmt.Errorf("current release %q references missing release %q", key, releaseID)
		}
		if release.EnvironmentID != normalizedKey {
			return State{}, fmt.Errorf("current release %q references release %q for different workspace/environment %q", key, releaseID, release.EnvironmentID)
		}
		if existingReleaseID, exists := normalizedCurrent[normalizedKey]; exists {
			if existingReleaseID != releaseID {
				return State{}, fmt.Errorf("duplicate current release for workspace/environment %q after normalization", normalizedKey)
			}
			continue
		}
		normalizedCurrent[normalizedKey] = releaseID
	}
	current.Current = normalizedCurrent

	deploymentIDs := make([]string, 0, len(current.Deployments))
	for id := range current.Deployments {
		deploymentIDs = append(deploymentIDs, id)
	}
	sort.Strings(deploymentIDs)
	normalizedDeployments := make(map[string]corerelease.Deployment, len(current.Deployments))
	for _, id := range deploymentIDs {
		deployment := current.Deployments[id]
		deployment.ID = strings.TrimSpace(firstNonEmpty(deployment.ID, id))
		deployment.EnvironmentID = strings.TrimSpace(deployment.EnvironmentID)
		deployment.ReleaseID = strings.TrimSpace(deployment.ReleaseID)
		deployment.Kind = strings.TrimSpace(deployment.Kind)
		deployment.Status = strings.TrimSpace(deployment.Status)
		if deployment.ID == "" {
			return State{}, fmt.Errorf("deployment %q id is required", id)
		}
		if deployment.EnvironmentID == "" {
			return State{}, fmt.Errorf("deployment %q environment_id is required", id)
		}
		if deployment.ReleaseID == "" {
			return State{}, fmt.Errorf("deployment %q release_id is required", id)
		}
		release, ok := current.Releases[deployment.ReleaseID]
		if !ok {
			return State{}, fmt.Errorf("deployment %q references missing release %q", id, deployment.ReleaseID)
		}
		if release.EnvironmentID != deployment.EnvironmentID {
			return State{}, fmt.Errorf("deployment %q references release %q for different workspace/environment %q", id, deployment.ReleaseID, release.EnvironmentID)
		}
		switch deployment.Kind {
		case corerelease.DeploymentKindDeploy, corerelease.DeploymentKindRollback, corerelease.DeploymentKindRepublish:
		default:
			return State{}, fmt.Errorf("deployment %q has unsupported kind %q", id, deployment.Kind)
		}
		switch deployment.Status {
		case corerelease.DeploymentStatusPending, corerelease.DeploymentStatusRunning, corerelease.DeploymentStatusSettled, corerelease.DeploymentStatusFailed:
		default:
			return State{}, fmt.Errorf("deployment %q has unsupported status %q", id, deployment.Status)
		}
		if deployment.Sequence <= 0 {
			return State{}, fmt.Errorf("deployment %q sequence must be greater than zero", id)
		}
		if _, exists := normalizedDeployments[deployment.ID]; exists {
			return State{}, fmt.Errorf("duplicate deployment id %q after normalization", deployment.ID)
		}
		normalizedDeployments[deployment.ID] = deployment
	}
	current.Deployments = normalizedDeployments

	secretKeys := make([]string, 0, len(current.Secrets))
	for key := range current.Secrets {
		secretKeys = append(secretKeys, key)
	}
	sort.Strings(secretKeys)
	normalizedSecrets := make(map[string]SecretRecord, len(current.Secrets))
	for _, key := range secretKeys {
		normalizedKey, secret, err := normalizeSecretRecord(key, current.Secrets[key])
		if err != nil {
			return State{}, fmt.Errorf("normalize secret %q: %w", key, err)
		}
		normalizedSecrets[normalizedKey] = secret
	}
	current.Secrets = normalizedSecrets

	return current, nil
}

func normalizeAttachmentRecord(key string, attachment AttachmentRecord) (string, AttachmentRecord, error) {
	workspaceRoot, workspaceKey, err := normalizeWorkspaceIdentity(key, attachment.WorkspaceRoot, attachment.WorkspaceKey)
	if err != nil {
		return "", AttachmentRecord{}, err
	}
	_, keyEnvironment := splitEnvironmentStateKey(key)
	environment := defaultEnvironmentName(firstNonEmpty(attachment.Environment, keyEnvironment))
	attachment.NodeNames = normalizeNodeNames(attachment.NodeNames)
	attachment.WorkspaceRoot = workspaceRoot
	attachment.WorkspaceKey = workspaceKey
	attachment.Environment = environment
	return workspaceKey + "\n" + environment, attachment, nil
}

func normalizeSnapshotRecord(key string, snapshot desiredstate.DeploySnapshot) (string, desiredstate.DeploySnapshot, error) {
	workspaceRoot, workspaceKey, err := normalizeWorkspaceIdentity(key, snapshot.WorkspaceRoot, snapshot.WorkspaceKey)
	if err != nil {
		return "", desiredstate.DeploySnapshot{}, err
	}
	_, keyEnvironment := splitEnvironmentStateKey(key)
	snapshot.WorkspaceRoot = workspaceRoot
	snapshot.WorkspaceKey = workspaceKey
	snapshot.Environment = defaultEnvironmentName(firstNonEmpty(snapshot.Environment, keyEnvironment))
	return snapshot.WorkspaceKey + "\n" + snapshot.Environment, snapshot, nil
}

func normalizeSecretRecord(key string, secret SecretRecord) (string, SecretRecord, error) {
	workspaceRoot, workspaceKey, err := normalizeWorkspaceIdentity(key, secret.WorkspaceRoot, secret.WorkspaceKey)
	if err != nil {
		return "", SecretRecord{}, err
	}
	_, keyEnvironment, keyService, keyName := splitSecretStateKey(key)
	secret.WorkspaceRoot = workspaceRoot
	secret.WorkspaceKey = workspaceKey
	secret.Environment = defaultEnvironmentName(firstNonEmpty(secret.Environment, keyEnvironment))
	secret.ServiceName = strings.TrimSpace(firstNonEmpty(secret.ServiceName, keyService))
	secret.Name = strings.TrimSpace(firstNonEmpty(secret.Name, keyName))
	normalizedStore, err := NormalizeSecretStore(secret.Store)
	if err != nil {
		return "", SecretRecord{}, err
	}
	secret.Store = normalizedStore
	if secret.ServiceName == "" {
		return "", SecretRecord{}, errors.New("service name is required")
	}
	if secret.Name == "" {
		return "", SecretRecord{}, errors.New("secret name is required")
	}
	reference, err := validateSecretMaterial(secret.Store, secret.Value, secret.Reference)
	if err != nil {
		return "", SecretRecord{}, err
	}
	secret.Reference = reference
	return secretKey(secret.WorkspaceKey, secret.Environment, secret.ServiceName, secret.Name), secret, nil
}

func normalizeWorkspaceIdentity(key, workspaceRoot, workspaceKey string) (string, string, error) {
	keyWorkspace, _ := splitEnvironmentStateKey(key)
	workspaceSource := firstNonEmpty(workspaceRoot, workspaceKey, keyWorkspace)
	if strings.TrimSpace(workspaceSource) == "" {
		return "", "", errors.New("workspace identity is required")
	}
	canonicalKey, err := CanonicalWorkspaceKey(workspaceSource)
	if err != nil {
		return "", "", err
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = canonicalKey
	}
	return workspaceRoot, canonicalKey, nil
}

func splitEnvironmentStateKey(key string) (string, string) {
	parts := strings.SplitN(key, "\n", 2)
	workspace := ""
	environment := ""
	if len(parts) > 0 {
		workspace = strings.TrimSpace(parts[0])
	}
	if len(parts) == 2 {
		environment = strings.TrimSpace(parts[1])
	}
	return workspace, environment
}

func splitSecretStateKey(key string) (string, string, string, string) {
	parts := strings.SplitN(key, "\n", 4)
	values := [4]string{}
	for i := range parts {
		values[i] = strings.TrimSpace(parts[i])
	}
	return values[0], values[1], values[2], values[3]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func NormalizeNode(node config.Node) config.Node {
	if node.Port == 0 {
		node.Port = 22
	}
	if strings.TrimSpace(node.AgentStateDir) == "" {
		node.AgentStateDir = "/var/lib/devopsellence"
	}
	labels := normalizeNodeLabels(node.Labels)
	if len(labels) == 0 {
		labels = append([]string(nil), config.DefaultNodeLabels...)
	}
	node.Labels = labels
	return node
}

func normalizeNodeLabels(labels []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		normalized = append(normalized, label)
	}
	return normalized
}

func (s *State) NodeNames() []string {
	s.ensureDefaults()
	names := make([]string, 0, len(s.Nodes))
	for name := range s.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *State) SetNode(name string, node config.Node) error {
	s.ensureDefaults()
	name = strings.TrimSpace(name)
	normalized, err := normalizeAndValidateNode(name, node)
	if err != nil {
		return err
	}
	s.Nodes[name] = normalized
	return nil
}

func (s *State) RemoveNode(name string) {
	s.ensureDefaults()
	delete(s.Nodes, strings.TrimSpace(name))
}

func (s *State) Attachment(workspaceRoot, environment string) (string, AttachmentRecord, bool, error) {
	s.ensureDefaults()
	key, err := EnvironmentStateKey(workspaceRoot, environment)
	if err != nil {
		return "", AttachmentRecord{}, false, err
	}
	record, ok := s.Attachments[key]
	return key, record, ok, nil
}

func (s *State) Snapshot(workspaceRoot, environment string) (string, desiredstate.DeploySnapshot, bool, error) {
	s.ensureDefaults()
	key, err := EnvironmentStateKey(workspaceRoot, environment)
	if err != nil {
		return "", desiredstate.DeploySnapshot{}, false, err
	}
	snapshot, ok := s.Snapshots[key]
	return key, snapshot, ok, nil
}

func (s *State) SaveSnapshot(snapshot desiredstate.DeploySnapshot) (string, error) {
	s.ensureDefaults()
	key, err := EnvironmentStateKey(snapshot.WorkspaceRoot, snapshot.Environment)
	if err != nil {
		return "", err
	}
	snapshot.WorkspaceKey, _ = splitEnvironmentStateKey(key)
	s.Snapshots[key] = snapshot
	return key, nil
}

func (s *State) SaveRelease(release corerelease.Release) (string, error) {
	s.ensureDefaults()
	if strings.TrimSpace(release.ID) == "" {
		return "", errors.New("release id is required")
	}
	if strings.TrimSpace(release.Revision) == "" {
		return "", errors.New("release revision is required")
	}
	if strings.TrimSpace(release.Snapshot.WorkspaceRoot) == "" {
		return "", errors.New("release snapshot workspace_root is required")
	}
	if strings.TrimSpace(release.Snapshot.Environment) == "" {
		return "", errors.New("release snapshot environment is required")
	}
	key, err := EnvironmentStateKey(release.Snapshot.WorkspaceRoot, release.Snapshot.Environment)
	if err != nil {
		return "", err
	}
	release.EnvironmentID = key
	release.Snapshot.WorkspaceKey, _ = splitEnvironmentStateKey(key)
	release.Snapshot.Environment = defaultEnvironmentName(release.Snapshot.Environment)
	release.Snapshot.Revision = release.Revision
	s.Releases[release.ID] = release
	s.Current[key] = release.ID
	s.Snapshots[key] = release.Snapshot
	return key, nil
}

func (s *State) CurrentRelease(workspaceRoot, environment string) (string, corerelease.Release, bool, error) {
	s.ensureDefaults()
	key, err := EnvironmentStateKey(workspaceRoot, environment)
	if err != nil {
		return "", corerelease.Release{}, false, err
	}
	releaseID := strings.TrimSpace(s.Current[key])
	if releaseID == "" {
		return key, corerelease.Release{}, false, nil
	}
	release, ok := s.Releases[releaseID]
	return key, release, ok, nil
}

func (s *State) ReleaseHistory(workspaceRoot, environment string) ([]corerelease.Release, error) {
	s.ensureDefaults()
	key, err := EnvironmentStateKey(workspaceRoot, environment)
	if err != nil {
		return nil, err
	}
	releases := []corerelease.Release{}
	for _, release := range s.Releases {
		if release.EnvironmentID == key {
			releases = append(releases, release)
		}
	}
	createdAtByID := make(map[string]time.Time, len(releases))
	for _, release := range releases {
		createdAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(release.CreatedAt))
		if err != nil {
			return nil, fmt.Errorf("release %q has invalid created_at: %w", release.ID, err)
		}
		createdAtByID[release.ID] = createdAt
	}
	sort.SliceStable(releases, func(i, j int) bool {
		if !createdAtByID[releases[i].ID].Equal(createdAtByID[releases[j].ID]) {
			return createdAtByID[releases[i].ID].After(createdAtByID[releases[j].ID])
		}
		return releases[i].ID > releases[j].ID
	})
	return releases, nil
}

func (s *State) SaveDeployment(deployment corerelease.Deployment) error {
	s.ensureDefaults()
	if strings.TrimSpace(deployment.ID) == "" {
		return errors.New("deployment id is required")
	}
	s.Deployments[deployment.ID] = deployment
	return nil
}

func (s *State) AttachNode(workspaceRoot, environment, nodeName string) (AttachmentRecord, bool, error) {
	s.ensureDefaults()
	nodeName = strings.TrimSpace(nodeName)
	if _, ok := s.Nodes[nodeName]; !ok {
		return AttachmentRecord{}, false, fmt.Errorf("node %q not found", nodeName)
	}
	key, attachment, _, err := s.Attachment(workspaceRoot, environment)
	if err != nil {
		return AttachmentRecord{}, false, err
	}
	workspaceKey, keyEnvironment := splitEnvironmentStateKey(key)
	attachment.WorkspaceRoot = firstNonEmpty(attachment.WorkspaceRoot, strings.TrimSpace(workspaceRoot), workspaceKey)
	attachment.WorkspaceKey = workspaceKey
	attachment.Environment = defaultEnvironmentName(environment)
	if strings.TrimSpace(environment) == "" {
		attachment.Environment = defaultEnvironmentName(keyEnvironment)
	}
	before := len(attachment.NodeNames)
	nodeNames := append([]string(nil), attachment.NodeNames...)
	nodeNames = append(nodeNames, nodeName)
	attachment.NodeNames = normalizeNodeNames(nodeNames)
	s.Attachments[key] = attachment
	return attachment, len(attachment.NodeNames) != before, nil
}

func (s *State) DetachNode(workspaceRoot, environment, nodeName string) (AttachmentRecord, bool, error) {
	s.ensureDefaults()
	key, attachment, ok, err := s.Attachment(workspaceRoot, environment)
	if err != nil {
		return AttachmentRecord{}, false, err
	}
	if !ok {
		return AttachmentRecord{}, false, nil
	}
	filtered := make([]string, 0, len(attachment.NodeNames))
	changed := false
	for _, current := range attachment.NodeNames {
		if strings.TrimSpace(current) == strings.TrimSpace(nodeName) {
			changed = true
			continue
		}
		filtered = append(filtered, current)
	}
	attachment.NodeNames = normalizeNodeNames(filtered)
	if len(attachment.NodeNames) == 0 {
		delete(s.Attachments, key)
		return AttachmentRecord{}, changed, nil
	}
	s.Attachments[key] = attachment
	return attachment, changed, nil
}

func (s *State) AttachmentKeysForNode(nodeName string) []string {
	s.ensureDefaults()
	nodeName = strings.TrimSpace(nodeName)
	keys := []string{}
	for key, attachment := range s.Attachments {
		for _, current := range attachment.NodeNames {
			if current == nodeName {
				keys = append(keys, key)
				break
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func (s *State) AttachmentsForNode(nodeName string) []AttachmentRecord {
	keys := s.AttachmentKeysForNode(nodeName)
	result := make([]AttachmentRecord, 0, len(keys))
	for _, key := range keys {
		result = append(result, s.Attachments[key])
	}
	return result
}

func (s *State) AttachedNodeNames(workspaceRoot, environment string) ([]string, error) {
	_, attachment, ok, err := s.Attachment(workspaceRoot, environment)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return append([]string(nil), attachment.NodeNames...), nil
}

func (s *State) NodeHasAttachments(nodeName string) bool {
	return len(s.AttachmentKeysForNode(nodeName)) > 0
}

func normalizeNodeNames(names []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		normalized = append(normalized, name)
	}
	sort.Strings(normalized)
	return normalized
}

func defaultEnvironmentName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.DefaultEnvironment
	}
	return name
}

func normalizeAndValidateNode(name string, node config.Node) (config.Node, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.Node{}, fmt.Errorf("node name is required")
	}
	node = NormalizeNode(node)
	if strings.TrimSpace(node.Host) == "" {
		return config.Node{}, fmt.Errorf("node %q host is required", name)
	}
	if strings.TrimSpace(node.User) == "" {
		return config.Node{}, fmt.Errorf("node %q user is required", name)
	}
	if node.Port <= 0 || node.Port > 65535 {
		return config.Node{}, fmt.Errorf("node %q port must be between 1 and 65535", name)
	}
	return node, nil
}

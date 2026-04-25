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

	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/state"
)

const soloStateSchemaVersion = 1

type StateStore struct {
	Path string
}

type State struct {
	SchemaVersion int                         `json:"schema_version"`
	Nodes         map[string]config.SoloNode  `json:"nodes,omitempty"`
	Attachments   map[string]AttachmentRecord `json:"attachments,omitempty"`
	Snapshots     map[string]DeploySnapshot   `json:"snapshots,omitempty"`
}

type AttachmentRecord struct {
	WorkspaceRoot string   `json:"workspace_root"`
	WorkspaceKey  string   `json:"workspace_key"`
	Environment   string   `json:"environment"`
	NodeNames     []string `json:"node_names,omitempty"`
}

type SnapshotMetadata struct {
	AppType    string `json:"app_type,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
	Project    string `json:"project,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

type DeploySnapshot struct {
	WorkspaceRoot      string           `json:"workspace_root"`
	WorkspaceKey       string           `json:"workspace_key"`
	Environment        string           `json:"environment"`
	Revision           string           `json:"revision"`
	Image              string           `json:"image"`
	Services           []serviceJSON    `json:"services,omitempty"`
	ReleaseTask        *taskJSON        `json:"release_task,omitempty"`
	ReleaseService     string           `json:"release_service,omitempty"`
	ReleaseServiceKind string           `json:"release_service_kind,omitempty"`
	Ingress            *ingressJSON     `json:"ingress,omitempty"`
	IngressService     string           `json:"ingress_service,omitempty"`
	IngressServiceKind string           `json:"ingress_service_kind,omitempty"`
	Metadata           SnapshotMetadata `json:"metadata,omitempty"`
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
	if err := os.WriteFile(s.Path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(s.Path, 0o600)
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

func BuildDeploySnapshot(cfg *config.ProjectConfig, workspaceRoot, configPath, imageTag, revision string, secrets map[string]string) (DeploySnapshot, error) {
	if cfg == nil {
		return DeploySnapshot{}, fmt.Errorf("config is required")
	}
	workspaceKey, err := CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return DeploySnapshot{}, err
	}
	environmentName := strings.TrimSpace(cfg.DefaultEnvironment)
	if environmentName == "" {
		environmentName = config.DefaultEnvironment
	}
	snapshot := DeploySnapshot{
		WorkspaceRoot: workspaceRoot,
		WorkspaceKey:  workspaceKey,
		Environment:   environmentName,
		Revision:      strings.TrimSpace(revision),
		Image:         strings.TrimSpace(imageTag),
		Metadata: SnapshotMetadata{
			AppType:    strings.TrimSpace(cfg.App.Type),
			ConfigPath: strings.TrimSpace(configPath),
			Project:    strings.TrimSpace(cfg.Project),
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		},
	}
	for _, serviceName := range cfg.ServiceNames() {
		service := cfg.Services[serviceName]
		rendered, err := buildService(serviceName, service, imageTag, secrets)
		if err != nil {
			return DeploySnapshot{}, fmt.Errorf("build service %s: %w", serviceName, err)
		}
		snapshot.Services = append(snapshot.Services, rendered)
	}
	if cfg.ReleaseTask() != nil {
		releaseTask, err := buildReleaseTask(cfg, imageTag, secrets)
		if err != nil {
			return DeploySnapshot{}, fmt.Errorf("build release task: %w", err)
		}
		snapshot.ReleaseTask = &releaseTask
		snapshot.ReleaseService = cfg.ReleaseTask().Service
		if service, ok := cfg.Services[cfg.ReleaseTask().Service]; ok {
			snapshot.ReleaseServiceKind = config.InferredServiceKind(cfg.ReleaseTask().Service, service)
		}
	}
	if cfg.Ingress != nil {
		snapshot.Ingress = buildIngress(cfg.Ingress, environmentName)
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

func RedactDeploySnapshotSecrets(snapshot DeploySnapshot, cfg *config.ProjectConfig) DeploySnapshot {
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
		Nodes:         map[string]config.SoloNode{},
		Attachments:   map[string]AttachmentRecord{},
		Snapshots:     map[string]DeploySnapshot{},
	}
}

func (s *State) ensureDefaults() {
	if s.SchemaVersion == 0 {
		s.SchemaVersion = soloStateSchemaVersion
	}
	if s.Nodes == nil {
		s.Nodes = map[string]config.SoloNode{}
	}
	if s.Attachments == nil {
		s.Attachments = map[string]AttachmentRecord{}
	}
	if s.Snapshots == nil {
		s.Snapshots = map[string]DeploySnapshot{}
	}
}

func normalizeState(current State) (State, error) {
	nodeNames := make([]string, 0, len(current.Nodes))
	for name := range current.Nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames)
	normalizedNodes := make(map[string]config.SoloNode, len(current.Nodes))
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
	normalizedSnapshots := make(map[string]DeploySnapshot, len(current.Snapshots))
	for _, key := range snapshotKeys {
		normalizedKey, snapshot, err := normalizeSnapshotRecord(key, current.Snapshots[key])
		if err != nil {
			return State{}, fmt.Errorf("normalize snapshot %q: %w", key, err)
		}
		normalizedSnapshots[normalizedKey] = snapshot
	}
	current.Snapshots = normalizedSnapshots

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

func normalizeSnapshotRecord(key string, snapshot DeploySnapshot) (string, DeploySnapshot, error) {
	workspaceRoot, workspaceKey, err := normalizeWorkspaceIdentity(key, snapshot.WorkspaceRoot, snapshot.WorkspaceKey)
	if err != nil {
		return "", DeploySnapshot{}, err
	}
	_, keyEnvironment := splitEnvironmentStateKey(key)
	snapshot.WorkspaceRoot = workspaceRoot
	snapshot.WorkspaceKey = workspaceKey
	snapshot.Environment = defaultEnvironmentName(firstNonEmpty(snapshot.Environment, keyEnvironment))
	return snapshot.WorkspaceKey + "\n" + snapshot.Environment, snapshot, nil
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func NormalizeNode(node config.SoloNode) config.SoloNode {
	if node.Port == 0 {
		node.Port = 22
	}
	if strings.TrimSpace(node.AgentStateDir) == "" {
		node.AgentStateDir = "/var/lib/devopsellence"
	}
	labels := normalizeNodeLabels(node.Labels)
	if len(labels) == 0 {
		labels = append([]string(nil), config.SoloDefaultLabels...)
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

func (s *State) SetNode(name string, node config.SoloNode) error {
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

func (s *State) Snapshot(workspaceRoot, environment string) (string, DeploySnapshot, bool, error) {
	s.ensureDefaults()
	key, err := EnvironmentStateKey(workspaceRoot, environment)
	if err != nil {
		return "", DeploySnapshot{}, false, err
	}
	snapshot, ok := s.Snapshots[key]
	return key, snapshot, ok, nil
}

func (s *State) SaveSnapshot(snapshot DeploySnapshot) (string, error) {
	s.ensureDefaults()
	key, err := EnvironmentStateKey(snapshot.WorkspaceRoot, snapshot.Environment)
	if err != nil {
		return "", err
	}
	snapshot.WorkspaceKey, _ = splitEnvironmentStateKey(key)
	s.Snapshots[key] = snapshot
	return key, nil
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

func normalizeAndValidateNode(name string, node config.SoloNode) (config.SoloNode, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return config.SoloNode{}, fmt.Errorf("node name is required")
	}
	node = NormalizeNode(node)
	if strings.TrimSpace(node.Host) == "" {
		return config.SoloNode{}, fmt.Errorf("node %q host is required", name)
	}
	if strings.TrimSpace(node.User) == "" {
		return config.SoloNode{}, fmt.Errorf("node %q user is required", name)
	}
	if node.Port <= 0 || node.Port > 65535 {
		return config.SoloNode{}, fmt.Errorf("node %q port must be between 1 and 65535", name)
	}
	return node, nil
}

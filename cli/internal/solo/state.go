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
	if current.SchemaVersion == 0 {
		current.SchemaVersion = soloStateSchemaVersion
	}
	current.ensureDefaults()
	current = normalizeState(current)
	return current, nil
}

func (s *StateStore) Write(current State) error {
	if s == nil || strings.TrimSpace(s.Path) == "" {
		return errors.New("solo state store path is required")
	}
	current.ensureDefaults()
	current = normalizeState(current)
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
			snapshot.ReleaseServiceKind = service.Kind
		}
	}
	if cfg.Ingress != nil {
		snapshot.Ingress = buildIngress(cfg.Ingress, environmentName)
		snapshot.IngressService = cfg.Ingress.Service
		if service, ok := cfg.Services[cfg.Ingress.Service]; ok {
			snapshot.IngressServiceKind = service.Kind
		}
	}
	return snapshot, nil
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

func normalizeState(current State) State {
	for name, node := range current.Nodes {
		current.Nodes[name] = NormalizeNode(node)
	}
	for key, attachment := range current.Attachments {
		current.Attachments[key] = normalizeAttachmentRecord(key, attachment)
	}
	return current
}

func normalizeAttachmentRecord(key string, attachment AttachmentRecord) AttachmentRecord {
	attachment.NodeNames = normalizeNodeNames(attachment.NodeNames)
	if attachment.WorkspaceKey == "" {
		attachment.WorkspaceKey = strings.TrimSpace(strings.SplitN(key, "\n", 2)[0])
	}
	if attachment.Environment == "" {
		parts := strings.SplitN(key, "\n", 2)
		if len(parts) == 2 {
			attachment.Environment = strings.TrimSpace(parts[1])
		}
	}
	return attachment
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
	if name == "" {
		return fmt.Errorf("node name is required")
	}
	s.Nodes[name] = NormalizeNode(node)
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
	snapshot.WorkspaceKey, _ = CanonicalWorkspaceKey(snapshot.WorkspaceRoot)
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
	workspaceKey, _ := CanonicalWorkspaceKey(workspaceRoot)
	attachment.WorkspaceRoot = workspaceRoot
	attachment.WorkspaceKey = workspaceKey
	attachment.Environment = defaultEnvironmentName(environment)
	before := len(attachment.NodeNames)
	attachment.NodeNames = append(attachment.NodeNames, nodeName)
	attachment.NodeNames = normalizeNodeNames(attachment.NodeNames)
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
	filtered := attachment.NodeNames[:0]
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

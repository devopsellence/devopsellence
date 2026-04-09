package desiredstatecache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/auth"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"google.golang.org/protobuf/encoding/protojson"
)

const schemaVersion = 1

type Entry struct {
	SchemaVersion           int    `json:"schema_version"`
	SavedAt                 string `json:"saved_at"`
	Mode                    string `json:"mode,omitempty"`
	URI                     string `json:"uri,omitempty"`
	Sequence                int64  `json:"sequence"`
	NodeID                  int64  `json:"node_id,omitempty"`
	EnvironmentID           int64  `json:"environment_id,omitempty"`
	OrganizationBundleToken string `json:"organization_bundle_token,omitempty"`
	EnvironmentBundleToken  string `json:"environment_bundle_token,omitempty"`
	NodeBundleToken         string `json:"node_bundle_token,omitempty"`
	DesiredStateJSON        string `json:"desired_state_json"`
}

type Store struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

func New(path string) *Store {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	return &Store{
		path: path,
		now:  time.Now,
	}
}

func (s *Store) Save(snapshot auth.DesiredStateSnapshot, sequence int64, desired *desiredstatepb.DesiredState) error {
	if s == nil || desired == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	desiredJSON, err := protojson.Marshal(desired)
	if err != nil {
		return fmt.Errorf("marshal desired state cache payload: %w", err)
	}

	entry := Entry{
		SchemaVersion:           schemaVersion,
		SavedAt:                 s.now().UTC().Format(time.RFC3339),
		Mode:                    snapshot.Target.Mode,
		URI:                     snapshot.Target.URI,
		Sequence:                sequence,
		NodeID:                  snapshot.NodeID,
		EnvironmentID:           snapshot.EnvironmentID,
		OrganizationBundleToken: snapshot.Target.OrganizationBundleToken,
		EnvironmentBundleToken:  snapshot.Target.EnvironmentBundleToken,
		NodeBundleToken:         snapshot.Target.NodeBundleToken,
		DesiredStateJSON:        string(desiredJSON),
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal desired state cache entry: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir desired state cache dir: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write desired state cache temp file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace desired state cache file: %w", err)
	}
	return nil
}

func (s *Store) Load() (*Entry, *desiredstatepb.DesiredState, error) {
	if s == nil {
		return nil, nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read desired state cache: %w", err)
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, nil, fmt.Errorf("parse desired state cache entry: %w", err)
	}
	if entry.SchemaVersion != schemaVersion {
		return nil, nil, fmt.Errorf("unsupported desired state cache schema version %d", entry.SchemaVersion)
	}
	if strings.TrimSpace(entry.DesiredStateJSON) == "" {
		return nil, nil, fmt.Errorf("desired state cache entry missing desired_state_json")
	}

	var desired desiredstatepb.DesiredState
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(entry.DesiredStateJSON), &desired); err != nil {
		return nil, nil, fmt.Errorf("parse desired state cache payload: %w", err)
	}

	return &entry, &desired, nil
}

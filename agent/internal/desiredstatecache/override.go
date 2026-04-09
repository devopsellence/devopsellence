package desiredstatecache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"google.golang.org/protobuf/encoding/protojson"
)

func LoadOverride(path string) (*desiredstatepb.DesiredState, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read local desired state override: %w", err)
	}
	return ParseOverride(data)
}

func ParseOverride(data []byte) (*desiredstatepb.DesiredState, bool, error) {
	if len(data) == 0 {
		return nil, false, fmt.Errorf("parse local desired state override: empty file")
	}

	payload := data
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err == nil {
		if enabledRaw, ok := document["enabled"]; ok {
			var enabled bool
			if err := json.Unmarshal(enabledRaw, &enabled); err == nil && !enabled {
				return nil, false, nil
			}
		}
		if desiredStateRaw, ok := document["desired_state"]; ok && len(desiredStateRaw) > 0 {
			payload = desiredStateRaw
		}
	}

	var desired desiredstatepb.DesiredState
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(payload, &desired); err != nil {
		return nil, false, fmt.Errorf("parse local desired state override: %w", err)
	}
	return &desired, true, nil
}

func WriteOverride(path string, data []byte) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("override path required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir local desired state override dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write local desired state override temp file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace local desired state override file: %w", err)
	}
	return nil
}

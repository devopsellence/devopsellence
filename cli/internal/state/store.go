package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Store struct {
	Path string
}

func New(path string) *Store {
	return &Store{Path: path}
}

func DefaultPath(rel string) string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return rel
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, rel)
}

func (s *Store) Read() (map[string]any, error) {
	if s == nil || s.Path == "" {
		return map[string]any{}, nil
	}

	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}

	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	if value == nil {
		return map[string]any{}, nil
	}
	return value, nil
}

func (s *Store) Write(value map[string]any) error {
	if s == nil || s.Path == "" {
		return errors.New("state store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(s.Path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(s.Path, 0o600)
}

func (s *Store) Update(fn func(map[string]any) (map[string]any, error)) error {
	current, err := s.Read()
	if err != nil {
		return err
	}
	next, err := fn(current)
	if err != nil {
		return err
	}
	if next == nil {
		next = current
	}
	return s.Write(next)
}

func (s *Store) Delete() (bool, error) {
	if s == nil || s.Path == "" {
		return false, nil
	}
	if err := os.Remove(s.Path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

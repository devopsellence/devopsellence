package lifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devopsellence/devopsellence/agent/internal/fileaccess"
)

type Record struct {
	Sequence int64  `json:"sequence"`
	Hash     string `json:"hash"`
}

type Store struct {
	path string
}

type fileState struct {
	Tasks map[string]Record `json:"tasks"`
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Satisfied(name string, sequence int64, hash string) bool {
	state, err := s.load()
	if err != nil {
		return false
	}
	record, ok := state.Tasks[name]
	return ok && record.Sequence == sequence && record.Hash == hash
}

func (s *Store) MarkSatisfied(name string, sequence int64, hash string) error {
	state, err := s.load()
	if err != nil {
		return err
	}
	state.Tasks[name] = Record{Sequence: sequence, Hash: hash}
	return s.save(state)
}

func (s *Store) load() (*fileState, error) {
	state := &fileState{Tasks: map[string]Record{}}
	if s == nil || s.path == "" {
		return state, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(data, state); err != nil {
		return &fileState{Tasks: map[string]Record{}}, nil
	}
	if state.Tasks == nil {
		state.Tasks = map[string]Record{}
	}
	return state, nil
}

func (s *Store) save(state *fileState) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := fileaccess.EnsureDirMode(filepath.Dir(s.path), 0o751); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

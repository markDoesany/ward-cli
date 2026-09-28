package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type State struct {
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type Store struct{ Path string }

func New(dir string) *Store { return &Store{Path: filepath.Join(dir, "state.json")} }

func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read state: %w", err)
	}
	var value State
	if err := json.Unmarshal(data, &value); err != nil {
		return State{}, fmt.Errorf("parse state: %w", err)
	}
	return value, nil
}

func (s *Store) Save(value State) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := AtomicWrite(s.Path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	return nil
}

// AtomicWrite writes a file through a synced temporary file and replacement.
// It is used for state and configuration edits so an interrupted write cannot
// leave a partially written YAML or JSON document behind.
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ward-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary file mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}

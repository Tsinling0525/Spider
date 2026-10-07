package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Each agentd instance must have its own store. The store contains sensitive
// mail and calendar data and is never used for credentials.
type fileStore struct{ path string }

func (s fileStore) load() (map[string]Run, error) {
	runs := make(map[string]Run)
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return runs, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &runs); err != nil {
		return nil, err
	}
	if runs == nil {
		return nil, errors.New("invalid agent run store")
	}
	return runs, nil
}

func (s fileStore) save(runs map[string]Run) error {
	b, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".agent-runs-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	// Make the renamed entry durable before an approved external action starts.
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func cloneRun(run Run) Run {
	b, _ := json.Marshal(run)
	var copy Run
	_ = json.Unmarshal(b, &copy)
	return copy
}

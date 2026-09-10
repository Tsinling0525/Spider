package oauth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/oauth2"
)

type FileTokenStore struct {
	path string
	mu   sync.RWMutex
}

func NewFileTokenStore(path string) *FileTokenStore {
	return &FileTokenStore{path: path}
}

func (s *FileTokenStore) Load() (*oauth2.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(b, &token); err != nil {
		return nil, err
	}
	if !token.Valid() && token.RefreshToken == "" {
		return nil, errors.New("stored OAuth token is invalid")
	}
	return &token, nil
}

func (s *FileTokenStore) Save(token *oauth2.Token) error {
	if token == nil {
		return errors.New("token is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(token)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

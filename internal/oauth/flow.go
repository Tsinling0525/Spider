package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/gmail/v1"
)

type Flow struct {
	config *oauth2.Config
	store  *FileTokenStore
	mu     sync.Mutex
	states map[string]pendingState
}

type pendingState struct {
	expiresAt time.Time
	sendScope bool
}

func NewFlow(config *oauth2.Config, store *FileTokenStore) *Flow {
	return &Flow{config: config, store: store, states: make(map[string]pendingState)}
}

func (f *Flow) Start(sendScope bool) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	f.mu.Lock()
	for key, item := range f.states {
		if time.Now().After(item.expiresAt) {
			delete(f.states, key)
		}
	}
	f.states[state] = pendingState{expiresAt: time.Now().Add(10 * time.Minute), sendScope: sendScope}
	f.mu.Unlock()
	config := f.scopedConfig(sendScope)
	return config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("include_granted_scopes", "true")), nil
}

func (f *Flow) Complete(ctx context.Context, state, code string) error {
	f.mu.Lock()
	pending, ok := f.states[state]
	delete(f.states, state)
	f.mu.Unlock()
	if !ok || time.Now().After(pending.expiresAt) {
		return errors.New("invalid or expired OAuth state")
	}
	if code == "" {
		return errors.New("OAuth code is missing")
	}
	token, err := f.scopedConfig(pending.sendScope).Exchange(ctx, code)
	if err != nil {
		return err
	}
	if token.RefreshToken == "" {
		if existing, loadErr := f.store.Load(); loadErr == nil {
			token.RefreshToken = existing.RefreshToken
		}
	}
	return f.store.Save(token)
}

func (f *Flow) Connected() bool {
	_, err := f.store.Load()
	return err == nil
}

func (f *Flow) scopedConfig(sendScope bool) *oauth2.Config {
	copy := *f.config
	copy.Scopes = []string{gmail.GmailReadonlyScope}
	if sendScope {
		copy.Scopes = append(copy.Scopes, gmail.GmailSendScope)
	}
	return &copy
}

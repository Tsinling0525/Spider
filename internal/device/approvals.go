package device

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}
type Approval struct {
	ID        string    `json:"id"`
	Node      string    `json:"node_id"`
	Principal string    `json:"principal_id"`
	Text      string    `json:"text"`
	Options   []Option  `json:"options"`
	Expires   time.Time `json:"expires_at"`
	Revision  int       `json:"revision"`
	Choice    string    `json:"choice,omitempty"`
}
type Store struct {
	mu    sync.Mutex
	path  string
	items map[string]Approval
}

func NewStore(path string) (*Store, error) {
	s := &Store{path: path, items: map[string]Approval{}}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		if err = json.Unmarshal(b, &s.items); err != nil {
			return nil, err
		}
	}
	if s.items == nil {
		return nil, errors.New("invalid approval store")
	}
	return s, nil
}
func (s *Store) save() error {
	b, err := json.Marshal(s.items)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path+".tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(s.path+".tmp", s.path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (s *Store) Create(a Approval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.ID == "" || len(a.ID) > 128 || a.Text == "" || len(a.Text) > 2048 || a.Node == "" || a.Principal == "" || len(a.Options) < 1 || len(a.Options) > 4 || !a.Expires.After(time.Now()) || a.Expires.After(time.Now().Add(24*time.Hour)) {
		return errors.New("invalid approval")
	}
	ids := map[string]bool{}
	for _, o := range a.Options {
		if o.ID == "" || len(o.ID) > 128 || o.Label == "" || len(o.Label) > 80 || ids[o.ID] {
			return errors.New("invalid option")
		}
		ids[o.ID] = true
	}
	if _, ok := s.items[a.ID]; ok {
		return errors.New("approval already exists")
	}
	a.Revision = 1
	a.Choice = ""
	s.items[a.ID] = a
	if err := s.save(); err != nil {
		delete(s.items, a.ID)
		return err
	}
	return nil
}
func (s *Store) Pending(node, principal string) []Approval {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Approval
	for _, a := range s.items {
		if a.Node == node && a.Principal == principal && a.Choice == "" && a.Expires.After(time.Now()) {
			out = append(out, a)
		}
	}
	return out
}
func (s *Store) Get(id string) (Approval, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.items[id]
	return a, ok
}
func (s *Store) Choose(id, node, principal, option string, revision int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.items[id]
	if !ok || a.Node != node || a.Principal != principal || a.Revision != revision || !a.Expires.After(time.Now()) || a.Choice != "" {
		return errors.New("approval is no longer actionable")
	}
	valid := false
	for _, o := range a.Options {
		valid = valid || o.ID == option
	}
	if !valid {
		return errors.New("option not offered")
	}
	old := a
	a.Choice = option
	s.items[id] = a
	if err := s.save(); err != nil {
		s.items[id] = old
		return err
	}
	return nil
}
func projection(a Approval) map[string]any {
	actions := []any{}
	for _, o := range a.Options {
		actions = append(actions, map[string]any{"action_id": o.ID, "label": o.Label, "decision_id": a.ID, "decision_revision": a.Revision, "option_id": o.ID, "actionable": true})
	}
	return map[string]any{"schema_version": "surface.channel-record.v0", "cursor": 1, "channel": "definition", "payload": map[string]any{"schema_version": "surface.definition.v0", "situation_id": nil, "surface_id": "surface:device:" + a.ID, "definition_rev": a.Revision, "node_id": a.Node, "surface_class": "compact", "intensity": "interruptive", "locus": "focus", "expires_at": a.Expires.UTC().Format(time.RFC3339), "view": map[string]any{"kind": "stack", "component_id": "approval", "semantic_role": "primary", "priority": "required", "children": []any{map[string]any{"kind": "text", "component_id": "question", "semantic_role": "primary", "priority": "required", "value": a.Text}, map[string]any{"kind": "action_row", "component_id": "actions", "semantic_role": "primary", "priority": "required", "actions": actions}}}}}
}

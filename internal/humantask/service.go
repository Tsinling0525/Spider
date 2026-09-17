package humantask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("human task not found")
	ErrConflict = errors.New("human task changed or is no longer actionable")
)

type Input struct {
	Type               string         `json:"type"`
	OutputVariableName string         `json:"output_variable_name"`
	Default            map[string]any `json:"default,omitempty"`
	OptionSource       map[string]any `json:"option_source,omitempty"`
}

type Action struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ButtonStyle string `json:"button_style,omitempty"`
}

type Pause struct {
	WorkflowRunID         string         `json:"workflow_run_id"`
	FormID                string         `json:"form_id"`
	FormToken             string         `json:"form_token"`
	NodeID                string         `json:"node_id"`
	NodeTitle             string         `json:"node_title"`
	FormContent           string         `json:"form_content"`
	Inputs                []Input        `json:"inputs"`
	Actions               []Action       `json:"actions"`
	ResolvedDefaultValues map[string]any `json:"resolved_default_values,omitempty"`
	ExpirationTime        int64          `json:"expiration_time,omitempty"`
}

type Task struct {
	ID                    string         `json:"id"`
	WorkflowRunID         string         `json:"workflow_run_id"`
	FormID                string         `json:"form_id"`
	NodeID                string         `json:"node_id"`
	NodeTitle             string         `json:"node_title"`
	FormContent           string         `json:"form_content"`
	Inputs                []Input        `json:"inputs"`
	Actions               []Action       `json:"actions"`
	ResolvedDefaultValues map[string]any `json:"resolved_default_values,omitempty"`
	Status                string         `json:"status"`
	Version               int64          `json:"version"`
	CreatedAt             time.Time      `json:"created_at"`
	UpdatedAt             time.Time      `json:"updated_at"`
	ExpiresAt             *time.Time     `json:"expires_at,omitempty"`
	ResolvedAt            *time.Time     `json:"resolved_at,omitempty"`
	SelectedAction        string         `json:"selected_action,omitempty"`
	FailureReason         string         `json:"failure_reason,omitempty"`
	FormToken             string         `json:"-"`
}

type record struct {
	Task
	FormToken string `json:"form_token"`
}

type Resolver interface {
	SubmitHumanInput(context.Context, string, string, map[string]any) error
}

type Decision struct {
	Action          string         `json:"action"`
	Inputs          map[string]any `json:"inputs"`
	ExpectedVersion int64          `json:"expected_version"`
	IdempotencyKey  string         `json:"idempotency_key"`
}

type persisted struct {
	Tasks     []record          `json:"tasks"`
	Decisions map[string]string `json:"decisions,omitempty"`
}

type Service struct {
	mu        sync.Mutex
	path      string
	resolver  Resolver
	tasks     map[string]record
	decisions map[string]string
	now       func() time.Time
}

func NewService(path string, resolver Resolver) (*Service, error) {
	s := &Service{
		path: path, resolver: resolver, tasks: make(map[string]record),
		decisions: make(map[string]string), now: func() time.Time { return time.Now().UTC() },
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) RecordPause(_ context.Context, pause Pause) error {
	if strings.TrimSpace(pause.WorkflowRunID) == "" || strings.TrimSpace(pause.FormID) == "" || strings.TrimSpace(pause.FormToken) == "" {
		return errors.New("Dify pause is missing workflow_run_id, form_id, or form_token")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := taskID(pause.WorkflowRunID, pause.FormID)
	if _, exists := s.tasks[id]; exists {
		return nil
	}
	now := s.now()
	var expiresAt *time.Time
	if pause.ExpirationTime > 0 {
		value := time.Unix(pause.ExpirationTime, 0).UTC()
		expiresAt = &value
	}
	task := Task{
		ID: id, WorkflowRunID: pause.WorkflowRunID, FormID: pause.FormID,
		NodeID: pause.NodeID, NodeTitle: pause.NodeTitle, FormContent: pause.FormContent,
		Inputs: append([]Input(nil), pause.Inputs...), Actions: append([]Action(nil), pause.Actions...),
		ResolvedDefaultValues: cloneMap(pause.ResolvedDefaultValues), Status: "pending", Version: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: expiresAt,
	}
	s.tasks[id] = record{Task: task, FormToken: pause.FormToken}
	return s.saveLocked()
}

func (s *Service) List() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.expireLocked()
	if changed {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	result := make([]Task, 0, len(s.tasks))
	for _, item := range s.tasks {
		result = append(result, publicTask(item))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (s *Service) Get(id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expireLocked() {
		_ = s.saveLocked()
	}
	item, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return publicTask(item), nil
}

func (s *Service) Decide(ctx context.Context, id string, decision Decision) (Task, error) {
	if strings.TrimSpace(decision.Action) == "" || strings.TrimSpace(decision.IdempotencyKey) == "" {
		return Task{}, errors.New("action and idempotency_key are required")
	}
	s.mu.Lock()
	if priorID, ok := s.decisions[decision.IdempotencyKey]; ok {
		item := s.tasks[priorID]
		s.mu.Unlock()
		return publicTask(item), nil
	}
	if s.expireLocked() {
		_ = s.saveLocked()
	}
	item, ok := s.tasks[id]
	if !ok {
		s.mu.Unlock()
		return Task{}, ErrNotFound
	}
	if item.Status != "pending" || item.Version != decision.ExpectedVersion || !hasAction(item.Actions, decision.Action) {
		s.mu.Unlock()
		return Task{}, ErrConflict
	}
	item.Status = "submitting"
	item.Version++
	item.UpdatedAt = s.now()
	s.tasks[id] = item
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return Task{}, err
	}
	token := item.FormToken
	s.mu.Unlock()

	err := s.resolver.SubmitHumanInput(ctx, token, decision.Action, decision.Inputs)

	s.mu.Lock()
	defer s.mu.Unlock()
	item = s.tasks[id]
	item.Version++
	item.UpdatedAt = s.now()
	if err != nil {
		item.Status = "failed"
		item.FailureReason = err.Error()
	} else {
		item.Status = "resumed"
		item.SelectedAction = decision.Action
		item.FailureReason = ""
		resolvedAt := item.UpdatedAt
		item.ResolvedAt = &resolvedAt
		s.decisions[decision.IdempotencyKey] = id
	}
	s.tasks[id] = item
	if saveErr := s.saveLocked(); saveErr != nil {
		return Task{}, saveErr
	}
	if err != nil {
		return publicTask(item), fmt.Errorf("submit Dify human input: %w", err)
	}
	return publicTask(item), nil
}

func (s *Service) expireLocked() bool {
	now := s.now()
	changed := false
	for id, item := range s.tasks {
		if item.Status == "pending" && item.ExpiresAt != nil && !item.ExpiresAt.After(now) {
			item.Status = "expired"
			item.Version++
			item.UpdatedAt = now
			s.tasks[id] = item
			changed = true
		}
	}
	return changed
}

func (s *Service) load() error {
	contents, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read human tasks: %w", err)
	}
	var state persisted
	if err := json.Unmarshal(contents, &state); err != nil {
		return fmt.Errorf("decode human tasks: %w", err)
	}
	for _, item := range state.Tasks {
		s.tasks[item.ID] = item
	}
	for key, id := range state.Decisions {
		s.decisions[key] = id
	}
	return nil
}

func (s *Service) saveLocked() error {
	state := persisted{Tasks: make([]record, 0, len(s.tasks)), Decisions: make(map[string]string, len(s.decisions))}
	for _, item := range s.tasks {
		state.Tasks = append(state.Tasks, item)
	}
	for key, id := range s.decisions {
		state.Decisions[key] = id
	}
	sort.Slice(state.Tasks, func(i, j int) bool { return state.Tasks[i].CreatedAt.Before(state.Tasks[j].CreatedAt) })
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.path); err != nil {
		return err
	}
	return os.Chmod(s.path, 0o600)
}

func taskID(workflowRunID, formID string) string {
	sum := sha256.Sum256([]byte(workflowRunID + "\x00" + formID))
	return "human-task-" + hex.EncodeToString(sum[:12])
}

func hasAction(actions []Action, id string) bool {
	for _, action := range actions {
		if action.ID == id {
			return true
		}
	}
	return false
}

func publicTask(item record) Task {
	task := item.Task
	task.FormToken = ""
	task.Inputs = append([]Input(nil), item.Inputs...)
	task.Actions = append([]Action(nil), item.Actions...)
	task.ResolvedDefaultValues = cloneMap(item.ResolvedDefaultValues)
	return task
}

func cloneMap(source map[string]any) map[string]any {
	if source == nil {
		return map[string]any{}
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

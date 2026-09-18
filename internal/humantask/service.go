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
	ErrNotFound  = errors.New("human task not found")
	ErrConflict  = errors.New("human task changed or is no longer actionable")
	ErrForbidden = errors.New("reviewer is not allowed to change this human task")
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
	Assignee              string         `json:"assignee,omitempty"`
	ClaimedAt             *time.Time     `json:"claimed_at,omitempty"`
	Priority              string         `json:"priority"`
	SLAStatus             string         `json:"sla_status"`
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
	Reviewer        string         `json:"reviewer,omitempty"`
}

type Claim struct {
	Assignee        string `json:"assignee"`
	ExpectedVersion int64  `json:"expected_version"`
	Actor           string `json:"-"`
}

type Event struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"task_id"`
	Type         string    `json:"type"`
	Actor        string    `json:"actor"`
	At           time.Time `json:"at"`
	FromAssignee string    `json:"from_assignee,omitempty"`
	ToAssignee   string    `json:"to_assignee,omitempty"`
	Action       string    `json:"action,omitempty"`
}

type Snapshot struct {
	Tasks    []Task `json:"tasks"`
	Revision int64  `json:"revision"`
}

type persisted struct {
	Tasks     []record          `json:"tasks"`
	Decisions map[string]string `json:"decisions,omitempty"`
	Events    []Event           `json:"events,omitempty"`
}

type Service struct {
	mu          sync.Mutex
	path        string
	resolver    Resolver
	tasks       map[string]record
	decisions   map[string]string
	now         func() time.Time
	revision    int64
	subscribers map[chan struct{}]struct{}
	events      []Event
	eventSeq    int64
}

func NewService(path string, resolver Resolver) (*Service, error) {
	s := &Service{
		path: path, resolver: resolver, tasks: make(map[string]record),
		decisions: make(map[string]string), now: func() time.Time { return time.Now().UTC() },
		revision: 1, subscribers: make(map[chan struct{}]struct{}),
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
	s.appendEventLocked(Event{TaskID: id, Type: "created", Actor: "system", At: now})
	return s.commitLocked()
}

func (s *Service) List() ([]Task, error) {
	snapshot, err := s.Snapshot()
	return snapshot.Tasks, err
}

func (s *Service) Snapshot() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.expireLocked()
	if changed {
		if err := s.commitLocked(); err != nil {
			return Snapshot{}, err
		}
	}
	result := make([]Task, 0, len(s.tasks))
	for _, item := range s.tasks {
		result = append(result, publicTask(item, s.now()))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return Snapshot{Tasks: result, Revision: s.revision}, nil
}

func (s *Service) Get(id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expireLocked() {
		_ = s.commitLocked()
	}
	item, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return publicTask(item, s.now()), nil
}

func (s *Service) History(id string) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; !ok {
		return nil, ErrNotFound
	}
	result := make([]Event, 0)
	for _, event := range s.events {
		if event.TaskID == id {
			result = append(result, event)
		}
	}
	return result, nil
}

func (s *Service) Changes(ctx context.Context, after int64, wait time.Duration) (Snapshot, error) {
	updates, cancel := s.Subscribe()
	defer cancel()
	snapshot, err := s.Snapshot()
	if err != nil || snapshot.Revision > after || wait <= 0 {
		return snapshot, err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-timer.C:
	case <-updates:
	}
	return s.Snapshot()
}

func (s *Service) Subscribe() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updates := make(chan struct{}, 1)
	s.subscribers[updates] = struct{}{}
	return updates, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subscribers[updates]; ok {
			delete(s.subscribers, updates)
			close(updates)
		}
	}
}

func (s *Service) Claim(id string, claim Claim) (Task, error) {
	assignee := strings.TrimSpace(claim.Assignee)
	actor := strings.TrimSpace(claim.Actor)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expireLocked() {
		_ = s.commitLocked()
	}
	item, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	if item.Status != "pending" || item.Version != claim.ExpectedVersion {
		return Task{}, ErrConflict
	}
	if actor == "" || (item.Assignee == "" && assignee != actor) || (item.Assignee != "" && item.Assignee != actor) {
		return Task{}, ErrForbidden
	}
	if assignee == item.Assignee {
		return publicTask(item, s.now()), nil
	}
	now := s.now()
	previous := item.Assignee
	item.Assignee = assignee
	item.Version++
	item.UpdatedAt = now
	if assignee == "" {
		item.ClaimedAt = nil
	} else {
		item.ClaimedAt = &now
	}
	s.tasks[id] = item
	eventType := "transferred"
	if previous == "" {
		eventType = "claimed"
	} else if assignee == "" {
		eventType = "released"
	}
	s.appendEventLocked(Event{TaskID: id, Type: eventType, Actor: actor, At: now, FromAssignee: previous, ToAssignee: assignee})
	if err := s.commitLocked(); err != nil {
		return Task{}, err
	}
	return publicTask(item, now), nil
}

func (s *Service) Decide(ctx context.Context, id string, decision Decision) (Task, error) {
	if strings.TrimSpace(decision.Action) == "" || strings.TrimSpace(decision.IdempotencyKey) == "" {
		return Task{}, errors.New("action and idempotency_key are required")
	}
	s.mu.Lock()
	if priorID, ok := s.decisions[decision.IdempotencyKey]; ok {
		item := s.tasks[priorID]
		s.mu.Unlock()
		return publicTask(item, s.now()), nil
	}
	if s.expireLocked() {
		_ = s.commitLocked()
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
	if strings.TrimSpace(decision.Reviewer) == "" || item.Assignee != strings.TrimSpace(decision.Reviewer) {
		s.mu.Unlock()
		return Task{}, ErrForbidden
	}
	item.Status = "submitting"
	item.Version++
	item.UpdatedAt = s.now()
	s.tasks[id] = item
	s.appendEventLocked(Event{TaskID: id, Type: "submission_started", Actor: decision.Reviewer, At: item.UpdatedAt, Action: decision.Action})
	if err := s.commitLocked(); err != nil {
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
		s.appendEventLocked(Event{TaskID: id, Type: "submission_failed", Actor: decision.Reviewer, At: item.UpdatedAt, Action: decision.Action})
	} else {
		item.Status = "resumed"
		item.SelectedAction = decision.Action
		item.FailureReason = ""
		resolvedAt := item.UpdatedAt
		item.ResolvedAt = &resolvedAt
		s.decisions[decision.IdempotencyKey] = id
		s.appendEventLocked(Event{TaskID: id, Type: "resolved", Actor: decision.Reviewer, At: item.UpdatedAt, Action: decision.Action})
	}
	s.tasks[id] = item
	if saveErr := s.commitLocked(); saveErr != nil {
		return Task{}, saveErr
	}
	if err != nil {
		return publicTask(item, s.now()), fmt.Errorf("submit Dify human input: %w", err)
	}
	return publicTask(item, s.now()), nil
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
			s.appendEventLocked(Event{TaskID: id, Type: "expired", Actor: "system", At: now})
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
	s.events = append([]Event(nil), state.Events...)
	for _, event := range s.events {
		var sequence int64
		_, _ = fmt.Sscanf(event.ID, "human-task-event-%d", &sequence)
		if sequence > s.eventSeq {
			s.eventSeq = sequence
		}
	}
	if len(s.events) == 0 {
		for _, item := range state.Tasks {
			s.appendEventLocked(Event{TaskID: item.ID, Type: "created", Actor: "system", At: item.CreatedAt})
		}
	}
	recovered := false
	for id, item := range s.tasks {
		if item.Status != "submitting" {
			continue
		}
		now := s.now()
		item.Status = "failed"
		item.Version++
		item.UpdatedAt = now
		item.FailureReason = "submission was interrupted; the Dify outcome is unknown and requires manual reconciliation"
		s.tasks[id] = item
		s.appendEventLocked(Event{TaskID: id, Type: "submission_interrupted", Actor: "system", At: now})
		recovered = true
	}
	if recovered {
		return s.saveLocked()
	}
	return nil
}

func (s *Service) saveLocked() error {
	state := persisted{Tasks: make([]record, 0, len(s.tasks)), Decisions: make(map[string]string, len(s.decisions)), Events: append([]Event(nil), s.events...)}
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

func (s *Service) appendEventLocked(event Event) {
	s.eventSeq++
	event.ID = fmt.Sprintf("human-task-event-%06d", s.eventSeq)
	s.events = append(s.events, event)
}

func (s *Service) commitLocked() error {
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.revision++
	for updates := range s.subscribers {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
	return nil
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

func publicTask(item record, now time.Time) Task {
	task := item.Task
	task.FormToken = ""
	task.Inputs = append([]Input(nil), item.Inputs...)
	task.Actions = append([]Action(nil), item.Actions...)
	task.ResolvedDefaultValues = cloneMap(item.ResolvedDefaultValues)
	task.Priority, task.SLAStatus = urgency(task, now)
	return task
}

func urgency(task Task, now time.Time) (string, string) {
	if task.Status == "expired" {
		return "high", "overdue"
	}
	if task.Status != "pending" && task.Status != "submitting" {
		return "normal", "complete"
	}
	if task.ExpiresAt == nil {
		return "normal", "none"
	}
	remaining := task.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return "high", "overdue"
	}
	if remaining <= 30*time.Minute {
		return "high", "at-risk"
	}
	return "normal", "on-track"
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

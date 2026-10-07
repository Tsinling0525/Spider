package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tsinling0525/Spider/internal/agent"
)

type memoryBackendFunc func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Message, error)

func (fn memoryBackendFunc) Next(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Message, error) {
	return fn(ctx, messages, tools)
}

func memoryService(t *testing.T, backend agent.Backend) *Service {
	t.Helper()
	s, err := NewService(filepath.Join(t.TempDir(), "state.json"), backend)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.StopMemory)
	return s
}

func memoryCall(t *testing.T, s *Service, method string, params map[string]any) map[string]any {
	t.Helper()
	raw, err := s.memoryRPC(context.Background(), method, params, nil, nil)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func waitMemoryJob(t *testing.T, s *Service) MemoryJob {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		job := s.memoryStatus()
		if job.Status != "running" {
			return job
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("memory job timed out")
	return MemoryJob{}
}

func TestMemoryManagementHistoryAndRecall(t *testing.T) {
	var recalled atomic.Bool
	backend := memoryBackendFunc(func(_ context.Context, messages []agent.Message, _ []agent.ToolSpec) (agent.Message, error) {
		if strings.Contains(messages[0].Content, "Python") && strings.Contains(messages[0].Content, "untrusted reference data") {
			recalled.Store(true)
		}
		return agent.Message{Role: "assistant", Content: "已经根据你的编程偏好整理建议。"}, nil
	})
	s := memoryService(t, backend)
	created := memoryCall(t, s, "create_atom", map[string]any{"assertion": "我喜欢使用 Python 编程", "kind": "Preference", "importance": "high", "entity_name": "我", "entity_type": "User"})
	atom := created["atom"].(map[string]any)
	id := atom["id"].(string)
	if memoryCall(t, s, "list_atoms", map[string]any{"query": "Python"})["total"] != float64(1) {
		t.Fatal("atom missing from search")
	}
	if memoryCall(t, s, "memory_search", map[string]any{"query": "Python", "maxResults": 5})["total"] == float64(0) {
		t.Fatal("recall missing")
	}
	for _, method := range []string{"stats_counts", "stats_growth", "stats_atom_kinds", "terminal_about_me", "terminal_current_focus", "terminal_things_you_told_me", "terminal_recent_stories", "terminal_entities", "tree", "list_entities", "list_episodes", "list_candidates", "list_journal", "storage_check"} {
		memoryCall(t, s, method, nil)
	}
	c, err := s.createConversation()
	if err != nil {
		t.Fatal(err)
	}
	c, err = s.send(context.Background(), c.ID, c.Version, "按照我对 Python 的偏好给一个学习建议", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !recalled.Load() || c.MemoryError != "" {
		t.Fatalf("chat did not use memory: %+v", c)
	}
	counts := memoryCall(t, s, "stats_counts", nil)
	if counts["raw_events"] != float64(3) {
		t.Fatalf("capture count: %v", counts)
	}
	if err := s.captureMemory(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if memoryCall(t, s, "stats_counts", nil)["raw_events"] != float64(3) {
		t.Fatal("capture was not idempotent")
	}
	replacement := memoryCall(t, s, "replace_atom", map[string]any{"atom_id": id, "assertion": "我现在更喜欢使用 Go 编程", "reason": "更新偏好"})
	newID := replacement["atom"].(map[string]any)["id"].(string)
	old := memoryCall(t, s, "get_atom", map[string]any{"atom_id": id})
	if old["deprecated_at"] == nil || old["superseded_by"] != newID {
		t.Fatal("replacement lost history")
	}
	memoryCall(t, s, "deprecate_atom", map[string]any{"atom_id": newID, "reason": "撤回"})
	if memoryCall(t, s, "list_atoms", nil)["total"] != float64(0) {
		t.Fatal("deprecated atom still listed")
	}
	if memoryCall(t, s, "list_atoms", map[string]any{"include_deprecated": true})["total"] != float64(2) {
		t.Fatal("history lost")
	}
	config := s.memoryConfig()
	config.Enabled = false
	beforeDisabled := memoryCall(t, s, "stats_counts", nil)["raw_events"]
	if err := s.saveMemoryConfig(config); err != nil {
		t.Fatal(err)
	}
	c, err = s.send(context.Background(), c.ID, c.Version, "这段对话不应该写入记忆库", nil)
	if err != nil {
		t.Fatal(err)
	}
	if memoryCall(t, s, "stats_counts", nil)["raw_events"] != beforeDisabled {
		t.Fatal("disabled memory captured chat")
	}
	saved, err := NewService(s.path, backend)
	if err != nil || saved.memoryConfig().Enabled {
		t.Fatalf("configuration did not persist: %v", err)
	}
}

func TestMemoryExtractionUsesHostModelAndPersistsIncrementalState(t *testing.T) {
	s := memoryService(t, nil)
	c, _ := s.createConversation()
	eventID := "spider_" + c.ID + "_0"
	content := "请记住我喜欢使用 Python 编程"
	c.Messages = []agent.Message{{Role: "user", Content: content}}
	if err := s.captureMemory(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	backend := memoryBackendFunc(func(_ context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Message, error) {
		calls.Add(1)
		if len(tools) != 0 {
			t.Fatal("memory model received connector tools")
		}
		output, _ := json.Marshal(map[string]any{"candidates": []map[string]any{{"candidate_type": "Preference", "title": "编程偏好", "assertion": content, "verbatim_quote": content, "quote_event_id": eventID, "subject": map[string]any{"name": "我", "entity_type": "User"}, "source_refs": []string{eventID}, "confidence": "high", "importance": "high", "recommended_action": "promote", "promotion_reason": "用户明确要求记住"}}})
		return agent.Message{Role: "assistant", Content: string(output)}, nil
	})
	params := map[string]any{"session_id": c.ID, "promote": false, "regen_pages": false}
	raw, err := s.memoryRPC(context.Background(), "extract", params, backend, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"candidates": 1`)) {
		t.Fatalf("extraction failed: %s", raw)
	}
	before := calls.Load()
	if _, err := s.memoryRPC(context.Background(), "extract", params, backend, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before {
		t.Fatal("process restart repeated extraction")
	}
	items := memoryCall(t, s, "list_candidates", nil)["items"].([]any)
	id := items[0].(map[string]any)["id"].(string)
	memoryCall(t, s, "get_candidate", map[string]any{"id": id})
	memoryCall(t, s, "promote_candidate", map[string]any{"id": id})
	if memoryCall(t, s, "list_atoms", nil)["total"] != float64(1) {
		t.Fatal("promotion failed")
	}
	if _, err := s.memoryRPC(context.Background(), "reject_candidate", map[string]any{"id": id}, nil, nil); !errors.Is(err, errInvalid) {
		t.Fatalf("promoted candidate was rejected: %v", err)
	}
}

func TestMemoryPortableRoundTripAndMaintenance(t *testing.T) {
	s := memoryService(t, nil)
	memoryCall(t, s, "create_atom", map[string]any{"assertion": "我的测试偏好是咖啡", "kind": "Preference", "entity_name": "我", "entity_type": "User"})
	path := filepath.Join(t.TempDir(), "memory.hmpkg")
	memoryCall(t, s, "pack", map[string]any{"package_path": path})
	if err := validateMemoryPackage(path); err != nil {
		t.Fatal(err)
	}
	target := memoryService(t, nil)
	preview := memoryCall(t, target, "adopt", map[string]any{"package_path": path, "dry_run": true})
	if preview["applied"] == float64(0) {
		t.Fatalf("empty preview: %v", preview)
	}
	if memoryCall(t, target, "stats_counts", nil)["atoms"] != float64(0) {
		t.Fatal("preview wrote data")
	}
	memoryCall(t, target, "adopt", map[string]any{"package_path": path, "dry_run": false})
	if memoryCall(t, target, "stats_counts", nil)["atoms"] != float64(1) {
		t.Fatal("import lost atom")
	}
	if !memoryCall(t, target, "doctor", nil)["all_passed"].(bool) {
		t.Fatal("imported memory is unhealthy")
	}
	if _, err := s.startMemoryJob("slim", ""); err != nil {
		t.Fatal(err)
	}
	job := waitMemoryJob(t, s)
	if job.Status != "done" {
		t.Fatalf("slim failed: %+v", job)
	}
	var result struct {
		Backup string `json:"backup_path"`
	}
	if err := json.Unmarshal(job.Result, &result); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(result.Backup)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("private backup missing: %v", err)
	}
	if memoryCall(t, s, "stats_counts", nil)["atoms"] != float64(1) {
		t.Fatal("slim pruned memory")
	}
}

func TestMemoryRoutesAuthorizationAndMaintenanceGate(t *testing.T) {
	s := memoryService(t, nil)
	handler := NewHandler(s, HTTPOptions{APIKey: "fixture"})
	request := func(method, path, body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if request("POST", "/v1/dashboard/memory/rpc", `{"method":"create_atom","params":{"assertion":"x"}}`, "").Code != http.StatusUnauthorized {
		t.Fatal("unauthorized memory mutation")
	}
	if response := request("POST", "/v1/dashboard/memory/rpc", `{"method":"adopt","params":{"package_path":"/etc/passwd"}}`, "Bearer fixture"); response.Code != 400 {
		t.Fatalf("filesystem method exposed: %s", response.Body)
	}
	if request("PUT", "/v1/dashboard/memory/config", `{"extract_idle_seconds":1}`, "Bearer fixture").Code != 400 {
		t.Fatal("unsafe schedule accepted")
	}
	c, _ := s.createConversation()
	s.mu.Lock()
	s.memoryPaused = true
	s.mu.Unlock()
	if _, _, _, err := s.startTurn(c.ID, c.Version, "message", nil); !errors.Is(err, errBusy) {
		t.Fatal("chat admitted during maintenance")
	}
	s.mu.Lock()
	s.memoryPaused = false
	s.mu.Unlock()
	if response := request("GET", "/v1/dashboard/memory/stats/counts", "", "Bearer fixture"); response.Code != 200 {
		t.Fatalf("stats route failed: %s", response.Body)
	}
}

func TestMemoryBackgroundPipelineAndIdleScheduling(t *testing.T) {
	s := memoryService(t, nil)
	c, _ := s.createConversation()
	content := "请记住我喜欢 Python，今天项目上线了很开心"
	eventID := "spider_" + c.ID + "_0"
	c.Messages = []agent.Message{{Role: "user", Content: content}}
	s.mu.Lock()
	s.state.Conversations[c.ID] = c
	s.mu.Unlock()
	if err := s.captureMemory(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	s.backend = memoryBackendFunc(func(_ context.Context, messages []agent.Message, _ []agent.ToolSpec) (agent.Message, error) {
		prompt := messages[len(messages)-1].Content
		var output any
		switch {
		case strings.Contains(prompt, "You are an Entity Page Summarizer"):
			output = map[string]any{"headline": "喜欢 Python 的开发者", "summary_markdown": "用户喜欢 Python，项目已上线。", "topics": []string{"Python", "开发"}}
		case strings.Contains(prompt, "You are an Episode Extractor"):
			output = map[string]any{"episodes": []map[string]any{{"summary": "今天项目上线，用户很开心", "verbatim_quote": content, "quote_event_id": eventID, "source_refs": []string{eventID}, "occurred_at": c.UpdatedAt.Format(time.RFC3339), "emotion": "happy", "intensity": 4, "people": []string{}, "topics": []string{"项目"}}}}
		default:
			output = map[string]any{"candidates": []map[string]any{{"candidate_type": "Preference", "title": "编程偏好", "assertion": "我喜欢 Python", "verbatim_quote": "我喜欢 Python", "quote_event_id": eventID, "source_refs": []string{eventID}, "subject": map[string]any{"name": "我", "entity_type": "User"}, "confidence": "high", "importance": "medium", "recommended_action": "promote"}}}
		}
		raw, _ := json.Marshal(output)
		return agent.Message{Role: "assistant", Content: string(raw)}, nil
	})
	s.memory.mu.Lock()
	s.memory.pending[c.ID] = time.Now().Add(-10 * time.Minute)
	s.memory.mu.Unlock()
	s.memoryTick(time.Now())
	job := waitMemoryJob(t, s)
	if job.Status != "done" {
		t.Fatalf("background extraction failed: %+v", job)
	}
	counts := memoryCall(t, s, "stats_counts", nil)
	if counts["atoms"] != float64(1) || counts["episodes"] != float64(1) || counts["dirty_pages"] != float64(0) {
		t.Fatalf("pipeline incomplete: %v; %s", counts, job.Result)
	}
	entities := memoryCall(t, s, "list_entities", nil)["items"].([]any)
	entity := entities[0].(map[string]any)
	detail := memoryCall(t, s, "get_entity", map[string]any{"id": entity["id"]})
	if detail["page"].(map[string]any)["summary_markdown"] != "用户喜欢 Python，项目已上线。" {
		t.Fatal("entity summary missing")
	}
	if _, err := s.startMemoryJob("regenerate_pages", ""); err != nil {
		t.Fatal(err)
	}
	if job := waitMemoryJob(t, s); job.Status != "done" {
		t.Fatalf("page job failed: %+v", job)
	}
}

func TestMemoryExtractsImportedSessionsOutsideConversationStore(t *testing.T) {
	s := memoryService(t, nil)
	memoryCall(t, s, "capture", map[string]any{"host": "octop", "session_id": "imported-octop-session", "events": []map[string]any{{"id": "imported-event", "event_type": "user_message", "content": "请记住我喜欢在周末读书"}}})
	s.backend = memoryBackendFunc(func(_ context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Message, error) {
		return agent.Message{Role: "assistant", Content: `{"candidates":[],"episodes":[]}`}, nil
	})
	if _, err := s.startMemoryJob("extract", ""); err != nil {
		t.Fatal(err)
	}
	job := waitMemoryJob(t, s)
	if job.Status != "done" || !bytes.Contains(job.Result, []byte("imported-octop-session")) {
		t.Fatalf("imported session skipped: %+v", job)
	}
}

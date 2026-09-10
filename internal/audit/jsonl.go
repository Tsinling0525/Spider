package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	maildomain "github.com/Tsinling0525/Spider/internal/mail"
)

type JSONLStore struct {
	path     string
	mu       sync.Mutex
	receipts map[string]maildomain.SendReceipt
}

func NewJSONLStore(path string) (*JSONLStore, error) {
	if path == "" {
		return nil, errors.New("audit path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	s := &JSONLStore{path: path, receipts: make(map[string]maildomain.SendReceipt)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *JSONLStore) load() error {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 2*1024*1024)
	for scanner.Scan() {
		var event maildomain.AuditEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "message.sent" || event.IdempotencyKey == "" {
			continue
		}
		data, err := json.Marshal(event.Payload)
		if err != nil {
			continue
		}
		var payload maildomain.SentAuditPayload
		if json.Unmarshal(data, &payload) == nil && payload.Receipt.ProviderMessageID != "" {
			s.receipts[event.IdempotencyKey] = payload.Receipt
			continue
		}
		var legacyReceipt maildomain.SendReceipt
		if json.Unmarshal(data, &legacyReceipt) == nil && legacyReceipt.ProviderMessageID != "" {
			s.receipts[event.IdempotencyKey] = legacyReceipt
		}
	}
	return scanner.Err()
}

func (s *JSONLStore) Append(_ context.Context, event maildomain.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(f)
	err = encoder.Encode(event)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if event.Type == "message.sent" && event.IdempotencyKey != "" {
		data, _ := json.Marshal(event.Payload)
		var payload maildomain.SentAuditPayload
		if json.Unmarshal(data, &payload) == nil && payload.Receipt.ProviderMessageID != "" {
			s.receipts[event.IdempotencyKey] = payload.Receipt
		}
	}
	return nil
}

func (s *JSONLStore) Receipt(_ context.Context, key string) (maildomain.SendReceipt, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts[key]
	return receipt, ok, nil
}

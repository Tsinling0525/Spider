package mail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrHumanConfirmationRequired = errors.New("human confirmation is required")
	ErrIdempotencyKeyRequired    = errors.New("idempotency key is required")
)

type Service struct {
	provider Provider
	drafter  Drafter
	audit    AuditStore
	sendMu   sync.Mutex
}

func NewService(provider Provider, drafter Drafter, audit AuditStore) *Service {
	return &Service{provider: provider, drafter: drafter, audit: audit}
}

func (s *Service) ListThreads(ctx context.Context, opts ListOptions) (ThreadPage, error) {
	if opts.PageSize <= 0 || opts.PageSize > 100 {
		opts.PageSize = 25
	}
	return s.provider.ListThreads(ctx, opts)
}

func (s *Service) GetThread(ctx context.Context, id string) (Thread, error) {
	if strings.TrimSpace(id) == "" {
		return Thread{}, errors.New("thread id is required")
	}
	return s.provider.GetThread(ctx, id)
}

func (s *Service) GenerateDraft(ctx context.Context, req DraftRequest) (Draft, error) {
	thread, err := s.GetThread(ctx, req.ThreadID)
	if err != nil {
		return Draft{}, err
	}
	draft, err := s.drafter.Generate(ctx, req, thread)
	if err != nil {
		_ = s.audit.Append(ctx, AuditEvent{
			Type: "draft.failed", At: time.Now().UTC().Format(time.RFC3339Nano), ThreadID: req.ThreadID,
			Payload: map[string]string{"error": err.Error()},
		})
		return Draft{}, err
	}
	if draft.ID == "" {
		draft.ID = newID()
	}
	draft.ThreadID = req.ThreadID
	draft.CreatedAt = time.Now().UTC()
	if draft.Warnings == nil {
		draft.Warnings = []string{}
	}
	if err := s.audit.Append(ctx, AuditEvent{
		Type: "draft.generated", At: draft.CreatedAt.Format(time.RFC3339Nano), ThreadID: req.ThreadID,
		DraftID: draft.ID, WorkflowRunID: draft.WorkflowRunID, Payload: draft,
	}); err != nil {
		return Draft{}, fmt.Errorf("audit draft: %w", err)
	}
	return draft, nil
}

func (s *Service) Send(ctx context.Context, req SendRequest) (SendReceipt, error) {
	if !req.HumanConfirmed {
		return SendReceipt{}, ErrHumanConfirmationRequired
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		return SendReceipt{}, ErrIdempotencyKeyRequired
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if receipt, ok, err := s.audit.Receipt(ctx, req.IdempotencyKey); err != nil {
		return SendReceipt{}, err
	} else if ok {
		return receipt, nil
	}
	if len(req.To) == 0 || strings.TrimSpace(req.BodyText) == "" {
		return SendReceipt{}, errors.New("recipient and body are required")
	}
	receipt, err := s.provider.Send(ctx, req)
	if err != nil {
		_ = s.audit.Append(ctx, AuditEvent{
			Type: "message.failed", At: time.Now().UTC().Format(time.RFC3339Nano), ThreadID: req.ThreadID,
			DraftID: req.DraftID, IdempotencyKey: req.IdempotencyKey, Payload: map[string]string{"error": err.Error()},
		})
		return SendReceipt{}, err
	}
	if err := s.audit.Append(ctx, AuditEvent{
		Type: "message.sent", At: receipt.SentAt.Format(time.RFC3339Nano), ThreadID: receipt.ThreadID,
		DraftID: req.DraftID, IdempotencyKey: req.IdempotencyKey, Payload: SentAuditPayload{Request: req, Receipt: receipt},
	}); err != nil {
		return SendReceipt{}, fmt.Errorf("message sent but audit failed: %w", err)
	}
	return receipt, nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

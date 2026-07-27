package pipeline

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

var (
	// ErrSessionIDRequired indicates that a Turn cannot be allocated without a session.
	ErrSessionIDRequired = errors.New("session id is required")
	// ErrLanguageConfigUnavailable indicates that no active language configuration was captured.
	ErrLanguageConfigUnavailable = errors.New("active language configuration is required")
	// ErrLanguageConfigSessionMismatch indicates that a reader returned another session's configuration.
	ErrLanguageConfigSessionMismatch = errors.New("language configuration session mismatch")
)

// TurnAllocator creates the member-3-owned identifiers used by the pipeline.
type TurnAllocator interface {
	Next(ctx context.Context, sessionID string) (turnID string, sequenceNo int64, err error)
}

// MemoryTurnAllocator is a deterministic in-memory allocator for the pipeline skeleton.
type MemoryTurnAllocator struct {
	mu        sync.Mutex
	sequences map[string]int64
}

// NewMemoryTurnAllocator constructs an empty per-session allocator.
func NewMemoryTurnAllocator() *MemoryTurnAllocator {
	return &MemoryTurnAllocator{sequences: make(map[string]int64)}
}

// Next allocates the next sequence and Turn ID for a session.
func (a *MemoryTurnAllocator) Next(ctx context.Context, sessionID string) (string, int64, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	if sessionID == "" {
		return "", 0, ErrSessionIDRequired
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.sequences[sessionID]++
	sequenceNo := a.sequences[sessionID]
	return fmt.Sprintf("turn_%s_%06d", sessionID, sequenceNo), sequenceNo, nil
}

// TurnOpenRequest contains immutable metadata captured when a Turn begins.
type TurnOpenRequest struct {
	SessionID string
	AccountID string
	TraceID   string
	StartedAt time.Time
}

// TurnContext carries the allocated Turn ID and its language snapshot through processing.
type TurnContext struct {
	ID             string
	SessionID      string
	AccountID      string
	TraceID        string
	SequenceNo     int64
	LanguageConfig session.LanguageConfigSnapshot
	StartedAt      time.Time
}

// TurnOpener allocates a Turn and captures member 2's configuration once.
type TurnOpener struct {
	allocator TurnAllocator
	languages session.LanguageConfigReader
}

// NewTurnOpener wires the allocator and language configuration boundary.
func NewTurnOpener(allocator TurnAllocator, languages session.LanguageConfigReader) *TurnOpener {
	return &TurnOpener{allocator: allocator, languages: languages}
}

// OpenTurn allocates the Turn ID before reading a copied language configuration.
func (o *TurnOpener) OpenTurn(ctx context.Context, request TurnOpenRequest) (TurnContext, error) {
	if request.SessionID == "" {
		return TurnContext{}, ErrSessionIDRequired
	}
	if o == nil || o.allocator == nil || o.languages == nil {
		return TurnContext{}, errors.New("Turn opener dependencies are required")
	}

	turnID, sequenceNo, err := o.allocator.Next(ctx, request.SessionID)
	if err != nil {
		return TurnContext{}, fmt.Errorf("allocate Turn: %w", err)
	}
	config, err := o.languages.GetCurrentConfig(ctx, request.SessionID)
	if err != nil {
		return TurnContext{}, fmt.Errorf("read language configuration: %w", err)
	}
	if config.SessionID != request.SessionID {
		return TurnContext{}, fmt.Errorf("%w: got %q for %q", ErrLanguageConfigSessionMismatch, config.SessionID, request.SessionID)
	}
	if config.Status == "" || config.Status != "active" || len(config.LanguagePairs) == 0 {
		return TurnContext{}, ErrLanguageConfigUnavailable
	}
	config.LanguagePairs = append([]session.LanguagePair(nil), config.LanguagePairs...)
	startedAt := request.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	return TurnContext{
		ID:             turnID,
		SessionID:      request.SessionID,
		AccountID:      request.AccountID,
		TraceID:        request.TraceID,
		SequenceNo:     sequenceNo,
		LanguageConfig: config,
		StartedAt:      startedAt,
	}, nil
}

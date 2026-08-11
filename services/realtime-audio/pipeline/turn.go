package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

var (
	// ErrSessionIDRequired indicates that a Turn cannot be allocated without a session.
	ErrSessionIDRequired = errors.New("session id is required")
	// ErrLanguageConfigUnavailable indicates that no active language configuration was captured.
	ErrLanguageConfigUnavailable = errors.New("active language configuration is required")
	// ErrLanguageConfigSessionMismatch indicates that a reader returned another session's configuration.
	ErrLanguageConfigSessionMismatch = errors.New("language configuration session mismatch")
	// ErrTurnModeUnavailable indicates that no executable runtime mode was captured for the Turn.
	ErrTurnModeUnavailable = errors.New("Turn mode snapshot is required")
	// ErrTurnModeSessionMismatch indicates that a reader returned another session's runtime mode.
	ErrTurnModeSessionMismatch = errors.New("Turn mode snapshot session mismatch")
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

// TurnModeSnapshot is the immutable runtime identity and mode captured when a Turn opens.
// Generation is scoped to RuntimeInstanceID and must not be compared across runtime instances.
type TurnModeSnapshot struct {
	SessionID         string
	RuntimeInstanceID string
	Mode              realtimev1.Mode
	Generation        int64
}

// TurnModeReader returns the authoritative mode state for the active runtime.
type TurnModeReader interface {
	GetTurnMode(ctx context.Context, sessionID string) (TurnModeSnapshot, error)
}

// TurnContext carries the allocated Turn ID and its immutable snapshots through processing.
type TurnContext struct {
	ID             string
	SessionID      string
	AccountID      string
	TraceID        string
	SequenceNo     int64
	LanguageConfig session.LanguageConfigSnapshot
	Mode           TurnModeSnapshot
	StartedAt      time.Time
}

// TurnOpener allocates a Turn and captures language and runtime mode state once.
type TurnOpener struct {
	allocator TurnAllocator
	languages session.LanguageConfigReader
	modes     TurnModeReader
}

// NewTurnOpener wires the allocator and immutable Turn snapshot boundaries.
func NewTurnOpener(allocator TurnAllocator, languages session.LanguageConfigReader, modes TurnModeReader) *TurnOpener {
	return &TurnOpener{allocator: allocator, languages: languages, modes: modes}
}

// OpenTurn allocates the Turn ID before reading copied language and mode snapshots.
func (o *TurnOpener) OpenTurn(ctx context.Context, request TurnOpenRequest) (TurnContext, error) {
	if request.SessionID == "" {
		return TurnContext{}, ErrSessionIDRequired
	}
	if o == nil || o.allocator == nil || o.languages == nil || o.modes == nil {
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
	if !validLanguageConfig(config) {
		return TurnContext{}, ErrLanguageConfigUnavailable
	}
	mode, err := o.modes.GetTurnMode(ctx, request.SessionID)
	if err != nil {
		return TurnContext{}, fmt.Errorf("read Turn mode: %w", err)
	}
	if mode.SessionID != request.SessionID {
		return TurnContext{}, fmt.Errorf("%w: got %q for %q", ErrTurnModeSessionMismatch, mode.SessionID, request.SessionID)
	}
	if mode.RuntimeInstanceID == "" || !mode.Mode.Valid() || mode.Generation < 1 {
		return TurnContext{}, ErrTurnModeUnavailable
	}
	config.LanguagePairs = append([]session.LanguagePair(nil), config.LanguagePairs...)
	config.OutputRoutes = append([]session.OutputRoute(nil), config.OutputRoutes...)
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
		Mode:           mode,
		StartedAt:      startedAt,
	}, nil
}

func validLanguageConfig(config session.LanguageConfigSnapshot) bool {
	if config.Status != "active" || config.Version <= 0 || len(config.LanguagePairs) == 0 {
		return false
	}
	for _, pair := range config.LanguagePairs {
		source := strings.TrimSpace(pair.Source)
		target := strings.TrimSpace(pair.Target)
		if source == "" || target == "" || source != pair.Source || target != pair.Target || source == target {
			return false
		}
	}
	return true
}

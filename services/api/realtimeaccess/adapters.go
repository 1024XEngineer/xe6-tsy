package realtimeaccess

import (
	"context"
	"errors"
	"fmt"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
)

var ErrInvalidDependency = errors.New("invalid realtime access dependency")

type languageConfigAdapter struct {
	reader languages.LanguageConfigReader
}

func NewLanguageConfigReader(reader languages.LanguageConfigReader) (sessions.LanguageConfigReader, error) {
	if reader == nil {
		return nil, ErrInvalidDependency
	}
	return languageConfigAdapter{reader: reader}, nil
}

func (a languageConfigAdapter) GetCurrentConfig(
	ctx context.Context,
	sessionID string,
) (sessions.LanguageConfigSnapshot, error) {
	snapshot, err := a.reader.GetCurrentConfig(ctx, sessionID)
	if err != nil {
		return sessions.LanguageConfigSnapshot{}, mapLanguageError(err)
	}
	status, err := mapLanguageStatus(snapshot.Status)
	if err != nil {
		return sessions.LanguageConfigSnapshot{}, err
	}
	if snapshot.SessionID != sessionID {
		return sessions.LanguageConfigSnapshot{}, sessions.ErrLanguageConfigNotReady
	}
	return sessions.LanguageConfigSnapshot{
		SessionID:         snapshot.SessionID,
		Version:           snapshot.Version,
		LanguagePairCount: len(snapshot.LanguagePairs),
		Status:            status,
	}, nil
}

type connectionClient interface {
	GetConnection(context.Context, string) (realtimev1.ConnectionSnapshot, error)
}

type webRTCConnectionAdapter struct {
	client connectionClient
}

func NewWebRTCConnectionReader(client connectionClient) (sessions.WebRTCConnectionReader, error) {
	if client == nil {
		return nil, ErrInvalidDependency
	}
	return webRTCConnectionAdapter{client: client}, nil
}

func (a webRTCConnectionAdapter) GetConnectionState(
	ctx context.Context,
	sessionID string,
) (sessions.WebRTCConnectionSnapshot, error) {
	snapshot, err := a.client.GetConnection(ctx, sessionID)
	if err != nil {
		return sessions.WebRTCConnectionSnapshot{}, mapConnectionError(err)
	}
	state, err := mapConnectionState(snapshot.State)
	if err != nil {
		return sessions.WebRTCConnectionSnapshot{}, err
	}
	if snapshot.SessionID != sessionID ||
		snapshot.ConnectionID == "" ||
		snapshot.UpdatedAt.IsZero() {
		return sessions.WebRTCConnectionSnapshot{}, sessions.ErrWebRTCUnavailable
	}
	return sessions.WebRTCConnectionSnapshot{
		SessionID:       snapshot.SessionID,
		ConnectionID:    snapshot.ConnectionID,
		ConnectionState: state,
		UpdatedAt:       snapshot.UpdatedAt,
	}, nil
}

type lifecycleClient interface {
	Start(context.Context, string, realtimev1.StartRequest) (realtimev1.RuntimeSnapshot, error)
	Stop(context.Context, string, realtimev1.StopRequest) (realtimev1.RuntimeSnapshot, error)
	GetRuntimeState(context.Context, string) (realtimev1.RuntimeSnapshot, error)
}

type realtimeLifecycleAdapter struct {
	client lifecycleClient
}

func NewRealtimeLifecycle(client lifecycleClient) (sessions.RealtimeLifecycle, error) {
	if client == nil {
		return nil, ErrInvalidDependency
	}
	return realtimeLifecycleAdapter{client: client}, nil
}

func (a realtimeLifecycleAdapter) Start(
	ctx context.Context,
	command sessions.StartRealtimeCommand,
) (sessions.RuntimeSnapshot, error) {
	snapshot, err := a.client.Start(ctx, command.SessionID, realtimev1.StartRequest{
		OperationID: command.OperationID,
		TraceID:     command.TraceID,
		StartedBy:   command.StartedBy,
	})
	if err != nil {
		return sessions.RuntimeSnapshot{}, mapStartError(err)
	}
	return mapRuntimeSnapshot(snapshot, command.SessionID)
}

func (a realtimeLifecycleAdapter) Stop(
	ctx context.Context,
	command sessions.StopRealtimeCommand,
) (sessions.RuntimeSnapshot, error) {
	reason, err := mapEndReason(command.Reason)
	if err != nil {
		return sessions.RuntimeSnapshot{}, err
	}
	snapshot, err := a.client.Stop(ctx, command.SessionID, realtimev1.StopRequest{
		TraceID: command.TraceID,
		Reason:  reason,
		EndedAt: command.EndedAt,
	})
	if err != nil {
		return sessions.RuntimeSnapshot{}, mapStopError(err)
	}
	return mapRuntimeSnapshot(snapshot, command.SessionID)
}

func (a realtimeLifecycleAdapter) GetRuntimeState(
	ctx context.Context,
	sessionID string,
) (sessions.RuntimeSnapshot, error) {
	snapshot, err := a.client.GetRuntimeState(ctx, sessionID)
	if err != nil {
		return sessions.RuntimeSnapshot{}, mapRuntimeError(err)
	}
	return mapRuntimeSnapshot(snapshot, sessionID)
}

func mapLanguageError(err error) error {
	switch {
	case errors.Is(err, languages.ErrNoActiveConfig):
		return sessions.ErrLanguageConfigNotReady
	case errors.Is(err, languages.ErrNotImplemented):
		return sessions.ErrNotImplemented
	default:
		return err
	}
}

func mapLanguageStatus(value string) (sessions.LanguageConfigStatus, error) {
	switch value {
	case languages.StatusActive:
		return sessions.LanguageConfigActive, nil
	case languages.StatusSuperseded:
		return sessions.LanguageConfigSuperseded, nil
	case languages.StatusExpired:
		return sessions.LanguageConfigExpired, nil
	default:
		return "", sessions.ErrLanguageConfigNotReady
	}
}

func mapConnectionError(err error) error {
	switch {
	case errors.Is(err, controlplane.ErrConnectionNotFound):
		return sessions.ErrWebRTCNotReady
	case errors.Is(err, controlplane.ErrClientRequest):
		return sessions.ErrInvalidRequest
	case errors.Is(err, controlplane.ErrClientUnauthorized):
		return sessions.ErrUnauthorized
	case errors.Is(err, controlplane.ErrClientDependency):
		return sessions.ErrInvalidDependency
	default:
		return preserveBoundaryError(err, sessions.ErrWebRTCUnavailable)
	}
}

func mapConnectionState(value realtimev1.ConnectionState) (sessions.ConnectionState, error) {
	switch value {
	case realtimev1.ConnectionNew:
		return sessions.ConnectionNew, nil
	case realtimev1.ConnectionConnecting:
		return sessions.ConnectionConnecting, nil
	case realtimev1.ConnectionConnected:
		return sessions.ConnectionConnected, nil
	case realtimev1.ConnectionDisconnected:
		return sessions.ConnectionDisconnected, nil
	case realtimev1.ConnectionFailed:
		return sessions.ConnectionFailed, nil
	case realtimev1.ConnectionClosed:
		return sessions.ConnectionClosed, nil
	default:
		return "", sessions.ErrWebRTCUnavailable
	}
}

func mapStartError(err error) error {
	switch {
	case errors.Is(err, controlplane.ErrRuntimeOperationConflict):
		return sessions.ErrRealtimeAlreadyRunning
	case errors.Is(err, controlplane.ErrClientRequest):
		return sessions.ErrInvalidRequest
	case errors.Is(err, controlplane.ErrClientUnauthorized):
		return sessions.ErrUnauthorized
	case errors.Is(err, controlplane.ErrClientDependency):
		return sessions.ErrInvalidDependency
	default:
		return preserveBoundaryError(err, sessions.ErrRealtimeStartFailed)
	}
}

func mapStopError(err error) error {
	switch {
	case errors.Is(err, controlplane.ErrClientRequest):
		return sessions.ErrInvalidRequest
	case errors.Is(err, controlplane.ErrClientUnauthorized):
		return sessions.ErrUnauthorized
	case errors.Is(err, controlplane.ErrClientDependency):
		return sessions.ErrInvalidDependency
	default:
		return preserveBoundaryError(err, sessions.ErrRealtimeStopFailed)
	}
}

func mapRuntimeError(err error) error {
	switch {
	case errors.Is(err, controlplane.ErrRuntimeNotFound):
		return sessions.ErrRuntimeSnapshotNotFound
	case errors.Is(err, controlplane.ErrClientRequest):
		return sessions.ErrInvalidRequest
	case errors.Is(err, controlplane.ErrClientUnauthorized):
		return sessions.ErrUnauthorized
	case errors.Is(err, controlplane.ErrClientDependency):
		return sessions.ErrInvalidDependency
	default:
		return preserveBoundaryError(err, sessions.ErrRuntimeUnavailable)
	}
}

func mapEndReason(value sessions.EndReason) (string, error) {
	switch value {
	case sessions.EndReasonUserRequested:
		return string(sessions.EndReasonUserRequested), nil
	case sessions.EndReasonOperatorCancelled:
		return string(sessions.EndReasonOperatorCancelled), nil
	case sessions.EndReasonClientDisconnected:
		return string(sessions.EndReasonClientDisconnected), nil
	default:
		return "", sessions.ErrInvalidRequest
	}
}

func mapRuntimeSnapshot(
	snapshot realtimev1.RuntimeSnapshot,
	sessionID string,
) (sessions.RuntimeSnapshot, error) {
	state, err := mapRuntimeState(snapshot.RuntimeState)
	if err != nil {
		return sessions.RuntimeSnapshot{}, err
	}
	if snapshot.SessionID != sessionID || snapshot.UpdatedAt.IsZero() {
		return sessions.RuntimeSnapshot{}, sessions.ErrRuntimeUnavailable
	}
	return sessions.RuntimeSnapshot{
		SessionID:         snapshot.SessionID,
		StartOperationID:  snapshot.StartOperationID,
		RuntimeState:      state,
		CurrentTurnID:     snapshot.CurrentTurnID,
		CurrentPlaybackID: snapshot.CurrentPlaybackID,
		LastErrorCode:     snapshot.LastErrorCode,
		UpdatedAt:         snapshot.UpdatedAt,
	}, nil
}

func mapRuntimeState(value realtimev1.RuntimeState) (sessions.RuntimeState, error) {
	switch value {
	case realtimev1.RuntimeStopped:
		return sessions.RuntimeStopped, nil
	case realtimev1.RuntimeStarting:
		return sessions.RuntimeStarting, nil
	case realtimev1.RuntimeListening:
		return sessions.RuntimeListening, nil
	case realtimev1.RuntimeASRProcessing:
		return sessions.RuntimeASRProcessing, nil
	case realtimev1.RuntimeTranslating:
		return sessions.RuntimeTranslating, nil
	case realtimev1.RuntimeTTSProcessing:
		return sessions.RuntimeTTSProcessing, nil
	case realtimev1.RuntimePlaying:
		return sessions.RuntimePlaying, nil
	case realtimev1.RuntimeStopping:
		return sessions.RuntimeStopping, nil
	case realtimev1.RuntimeFailed:
		return sessions.RuntimeFailed, nil
	default:
		return "", sessions.ErrRuntimeUnavailable
	}
}

func preserveBoundaryError(err error, boundary error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, boundary):
		return err
	default:
		return fmt.Errorf("%w: %w", boundary, err)
	}
}

var (
	_ sessions.LanguageConfigReader   = languageConfigAdapter{}
	_ sessions.WebRTCConnectionReader = webRTCConnectionAdapter{}
	_ sessions.RealtimeLifecycle      = realtimeLifecycleAdapter{}
)

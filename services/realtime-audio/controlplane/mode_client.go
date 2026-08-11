package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

// GetModeState reads the authoritative business-mode state without treating it
// as part of the media RuntimeSnapshot lifecycle.
func (c *Client) GetModeState(ctx context.Context, sessionID string) (realtimev1.ModeStateSnapshot, error) {
	if strings.TrimSpace(sessionID) == "" {
		return realtimev1.ModeStateSnapshot{}, ErrClientRequest
	}
	token, err := c.ticket(ctx, sessionID)
	if err != nil {
		return realtimev1.ModeStateSnapshot{}, err
	}
	endpoint, err := url.JoinPath(c.baseURL, "realtime/v1/sessions", sessionID, "mode")
	if err != nil {
		return realtimev1.ModeStateSnapshot{}, fmt.Errorf("%w: build mode endpoint: %v", ErrClientDependency, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return realtimev1.ModeStateSnapshot{}, fmt.Errorf("%w: build mode request: %v", ErrClientRequest, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return realtimev1.ModeStateSnapshot{}, preserveContextError(ctx, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err))
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxClientResponseBytes+1)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return realtimev1.ModeStateSnapshot{}, decodeClientError(response.StatusCode, reader)
	}
	var state realtimev1.ModeStateSnapshot
	if err := json.NewDecoder(reader).Decode(&state); err != nil {
		return realtimev1.ModeStateSnapshot{}, fmt.Errorf("%w: decode mode response: %v", ErrInvalidResponse, err)
	}
	if !validModeState(state, sessionID) {
		return realtimev1.ModeStateSnapshot{}, ErrInvalidResponse
	}
	return state, nil
}

// SwitchMode applies an idempotent generation compare-and-switch to one active
// runtime. It never invokes lifecycle Stop/Start or signaling operations.
func (c *Client) SwitchMode(
	ctx context.Context,
	sessionID string,
	command realtimev1.SwitchModeCommand,
) (realtimev1.SwitchModeResult, error) {
	if !validClientModeCommand(sessionID, command) {
		return realtimev1.SwitchModeResult{}, ErrClientRequest
	}
	token, err := c.ticket(ctx, sessionID)
	if err != nil {
		return realtimev1.SwitchModeResult{}, err
	}
	encoded, err := json.Marshal(command)
	if err != nil {
		return realtimev1.SwitchModeResult{}, fmt.Errorf("%w: encode mode request: %v", ErrClientRequest, err)
	}
	endpoint, err := url.JoinPath(c.baseURL, "realtime/v1/sessions", sessionID, "mode")
	if err != nil {
		return realtimev1.SwitchModeResult{}, fmt.Errorf("%w: build mode endpoint: %v", ErrClientDependency, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return realtimev1.SwitchModeResult{}, fmt.Errorf("%w: build mode request: %v", ErrClientRequest, err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "mode:"+command.OperationID)
	response, err := c.http.Do(request)
	if err != nil {
		return realtimev1.SwitchModeResult{}, preserveContextError(ctx, fmt.Errorf("%w: %v", ErrDependencyUnavailable, err))
	}
	defer response.Body.Close()
	reader := io.LimitReader(response.Body, maxClientResponseBytes+1)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return realtimev1.SwitchModeResult{}, decodeClientError(response.StatusCode, reader)
	}
	var result realtimev1.SwitchModeResult
	if err := json.NewDecoder(reader).Decode(&result); err != nil {
		return realtimev1.SwitchModeResult{}, fmt.Errorf("%w: decode mode switch response: %v", ErrInvalidResponse, err)
	}
	if !validModeResult(result, command) {
		return realtimev1.SwitchModeResult{}, ErrInvalidResponse
	}
	return result, nil
}

func validClientModeCommand(sessionID string, command realtimev1.SwitchModeCommand) bool {
	return strings.TrimSpace(sessionID) != "" && command.SessionID == sessionID &&
		strings.TrimSpace(command.RuntimeInstanceID) != "" && strings.TrimSpace(command.OperationID) != "" &&
		strings.TrimSpace(command.TraceID) != "" && command.ExpectedGeneration >= 1 && command.TargetMode.Valid()
}

func validModeState(state realtimev1.ModeStateSnapshot, sessionID string) bool {
	return state.SessionID == sessionID && strings.TrimSpace(state.RuntimeInstanceID) != "" &&
		state.ActiveMode.Valid() && state.Generation >= 1 && state.Phase.Valid() && !state.UpdatedAt.IsZero()
}

func validModeResult(result realtimev1.SwitchModeResult, command realtimev1.SwitchModeCommand) bool {
	if result.OperationID != command.OperationID || !result.Status.Valid() ||
		!validModeState(result.State, command.SessionID) || result.State.RuntimeInstanceID != command.RuntimeInstanceID ||
		result.State.ActiveMode != command.TargetMode || result.State.Phase != realtimev1.ModePhaseActive ||
		result.State.LastOperationID == nil || *result.State.LastOperationID != command.OperationID {
		return false
	}
	switch result.Status {
	case realtimev1.ModeSwitchApplied:
		return result.State.Generation == command.ExpectedGeneration+1
	case realtimev1.ModeSwitchUnchanged:
		return result.State.Generation == command.ExpectedGeneration
	default:
		return false
	}
}

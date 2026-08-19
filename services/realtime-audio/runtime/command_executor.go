package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"golang.org/x/text/language"
)

var (
	ErrCommandLanguageClarification = errors.New("command language direction requires clarification")
	ErrCommandLanguageInvalid       = errors.New("command language direction is invalid")
	ErrCommandLanguageSession       = errors.New("command language configuration belongs to another session")
	ErrCommandConfiguratorRequired  = errors.New("command language configurator is required")
	ErrCommandConfigResultInvalid   = errors.New("command language configuration result is invalid")
)

// commandExecutor applies validated intents through existing control-plane boundaries. Language
// configuration remains API-owned and must complete before the realtime mode CAS is attempted.
type commandExecutor struct {
	manager      *Manager
	languages    session.LanguageConfigReader
	configurator command.LanguageConfigurator
}

func (e commandExecutor) ExecuteCommand(ctx context.Context, request command.ExecuteRequest) (command.ExecutionResult, error) {
	if err := validateExecutableCommand(request); err != nil {
		return command.ExecutionResult{}, err
	}
	if request.Command.Action == command.ActionAssistantQuery {
		return e.executeAssistantQuery(ctx, request)
	}
	var languageConfig *command.AppliedLanguageConfig
	if request.Command.Action == command.ActionActivateMode {
		var err error
		languageConfig, err = e.prepareInterpretation(ctx, request)
		if err != nil {
			return command.ExecutionResult{}, err
		}
	}
	state, err := e.manager.GetModeState(ctx, request.SessionID)
	if err != nil {
		return command.ExecutionResult{}, err
	}
	operationID := "wake_word_" + request.CommandID
	result, err := e.manager.SwitchMode(ctx, realtimev1.SwitchModeCommand{
		SessionID:          request.SessionID,
		RuntimeInstanceID:  state.RuntimeInstanceID,
		OperationID:        operationID,
		TraceID:            operationID,
		ExpectedGeneration: state.Generation,
		TargetMode:         request.Command.TargetMode,
	})
	if err != nil {
		return command.ExecutionResult{}, err
	}
	return command.ExecutionResult{
		Status: result.Status, State: result.State, LanguageConfig: languageConfig,
	}, nil
}

func validateExecutableCommand(request command.ExecuteRequest) error {
	if request.SessionID == "" || request.CommandID == "" || !request.Command.TargetMode.Valid() {
		return ErrModeCommandInvalid
	}
	switch request.Command.Action {
	case command.ActionActivateMode:
		if request.Command.TargetMode != realtimev1.ModeInterpretation {
			return ErrModeCommandInvalid
		}
	case command.ActionReturnToAssistant:
		if request.Command.TargetMode != realtimev1.ModeAssistant || request.Command.Arguments != (command.Arguments{}) {
			return ErrModeCommandInvalid
		}
	case command.ActionAssistantQuery:
		if request.Command.TargetMode != realtimev1.ModeAssistant || request.Command.Arguments != (command.Arguments{}) || strings.TrimSpace(request.Command.Text) == "" {
			return ErrModeCommandInvalid
		}
	default:
		return ErrModeCommandInvalid
	}
	return nil
}

// executeAssistantQuery reuses the registered assistant handler after Command ASR. It accepts a
// query only while assistant mode is active and never changes the mode generation; interpretation
// sessions must explicitly return to assistant mode to prevent assistant and translation output
// from sharing one spoken turn.
func (e commandExecutor) executeAssistantQuery(ctx context.Context, request command.ExecuteRequest) (command.ExecutionResult, error) {
	e.manager.mu.Lock()
	item := e.manager.entries[request.SessionID]
	if item == nil || !item.active || item.stopping || item.terminal || item.finished {
		e.manager.mu.Unlock()
		return command.ExecutionResult{}, session.ErrRuntimeNotFound
	}
	accountID := item.request.AccountID
	state := item.mode.Snapshot()
	e.manager.mu.Unlock()
	if state.ActiveMode != realtimev1.ModeAssistant {
		return command.ExecutionResult{}, errors.Join(command.ErrClarificationRequired, ErrModeNotAvailable)
	}

	turn, err := e.manager.commandOpener.OpenTurn(ctx, pipeline.TurnOpenRequest{
		SessionID: request.SessionID,
		AccountID: accountID,
		TraceID:   "wake_word_" + request.CommandID,
		StartedAt: e.manager.deps.Now(),
	})
	if err != nil {
		return command.ExecutionResult{}, fmt.Errorf("open assistant command Turn: %w", err)
	}
	if turn.Mode.Mode != realtimev1.ModeAssistant {
		return command.ExecutionResult{}, errors.Join(command.ErrClarificationRequired, ErrModeNotAvailable)
	}
	result := asr.FinalResult{
		Text: request.Command.Text, SourceLanguage: asr.NormalizeLanguage(request.Language),
	}
	if err := e.manager.router.HandleASRFinal(ctx, turn, result); err != nil {
		return command.ExecutionResult{}, fmt.Errorf("handle assistant command: %w", err)
	}
	state, err = e.manager.GetModeState(ctx, request.SessionID)
	if err != nil {
		return command.ExecutionResult{}, err
	}
	return command.ExecutionResult{Status: realtimev1.ModeSwitchUnchanged, State: state}, nil
}

func (e commandExecutor) prepareInterpretation(ctx context.Context, request command.ExecuteRequest) (*command.AppliedLanguageConfig, error) {
	arguments := request.Command.Arguments
	var snapshot session.LanguageConfigSnapshot
	if strings.TrimSpace(arguments.SourceLanguage) == "" || strings.TrimSpace(arguments.TargetLanguage) == "" {
		var err error
		snapshot, err = e.languages.GetCurrentConfig(ctx, request.SessionID)
		if err != nil {
			return nil, fmt.Errorf("read current command language configuration: %w", err)
		}
		if snapshot.SessionID != "" && snapshot.SessionID != request.SessionID {
			return nil, fmt.Errorf("%w: got %q for %q", ErrCommandLanguageSession, snapshot.SessionID, request.SessionID)
		}
	}
	arguments, explicit, err := resolveLanguageArguments(arguments, snapshot)
	if err != nil {
		if errors.Is(err, ErrCommandLanguageClarification) {
			return nil, errors.Join(command.ErrClarificationRequired, err)
		}
		if errors.Is(err, ErrCommandLanguageInvalid) {
			return nil, errors.Join(command.ErrUnsupported, err)
		}
		return nil, err
	}
	if !explicit {
		return nil, nil
	}
	// An explicit language direction cannot proceed without the API-owned writer. Keeping the
	// check at this boundary also protects tests and programmatic Manager construction.
	if e.configurator == nil {
		return nil, ErrCommandConfiguratorRequired
	}
	result, err := e.configurator.Configure(ctx, languagesv1.CommandConfigRequest{
		SessionID: request.SessionID, CommandID: request.CommandID,
		SourceLanguage: arguments.SourceLanguage, TargetLanguage: arguments.TargetLanguage,
	})
	if err != nil {
		return nil, fmt.Errorf("configure command language direction: %w", err)
	}
	if result.SessionID != request.SessionID || result.CommandID != request.CommandID || result.Version <= 0 {
		return nil, fmt.Errorf("%w: got session %q command %q version %d",
			ErrCommandConfigResultInvalid, result.SessionID, result.CommandID, result.Version)
	}
	return &command.AppliedLanguageConfig{
		SourceLanguage: arguments.SourceLanguage,
		TargetLanguage: arguments.TargetLanguage,
		Version:        result.Version,
	}, nil
}

// resolveLanguageArguments normalizes explicit BCP-47 slots. A complete explicit pair can
// bootstrap API-owned configuration without an existing snapshot; an incomplete pair may use an
// active snapshot only when exactly one configured direction matches. It never guesses a pair.
func resolveLanguageArguments(arguments command.Arguments, snapshot session.LanguageConfigSnapshot) (command.Arguments, bool, error) {
	sourceRaw := strings.TrimSpace(arguments.SourceLanguage)
	targetRaw := strings.TrimSpace(arguments.TargetLanguage)
	explicit := sourceRaw != "" || targetRaw != ""
	source, err := normalizeLanguageTag(sourceRaw)
	if err != nil {
		return command.Arguments{}, true, err
	}
	target, err := normalizeLanguageTag(targetRaw)
	if err != nil {
		return command.Arguments{}, true, err
	}
	if source != "" && target != "" {
		if source == target {
			return command.Arguments{}, true, ErrCommandLanguageInvalid
		}
		return command.Arguments{SourceLanguage: source, TargetLanguage: target}, true, nil
	}
	if !validActiveLanguageSnapshot(snapshot) {
		return command.Arguments{}, explicit, ErrCommandLanguageClarification
	}
	if !explicit {
		return command.Arguments{}, false, nil
	}
	source, target, err = completeLanguageDirection(source, target, snapshot.LanguagePairs)
	if err != nil {
		return command.Arguments{}, true, err
	}
	return command.Arguments{SourceLanguage: source, TargetLanguage: target}, true, nil
}

func normalizeLanguageTag(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	tag, err := language.Parse(raw)
	if err != nil || tag == language.Und {
		return "", ErrCommandLanguageInvalid
	}
	canonical := tag.String()
	if !strings.Contains(canonical, "-") {
		// Semantic providers commonly return valid primary-only tags such as zh or ja. The
		// language catalog and downstream providers use concrete locale codes, so apply the same
		// bounded defaults as ASR only when no script or region was supplied. Explicit variants
		// remain untouched and are still authorized by the API-owned language catalog.
		canonical = asr.NormalizeLanguage(canonical)
	}
	return canonical, nil
}

func completeLanguageDirection(source, target string, pairs []session.LanguagePair) (string, string, error) {
	matches := make([]session.LanguagePair, 0, 1)
	for _, pair := range pairs {
		pairSource, sourceErr := normalizeLanguageTag(strings.TrimSpace(pair.Source))
		pairTarget, targetErr := normalizeLanguageTag(strings.TrimSpace(pair.Target))
		if sourceErr != nil || targetErr != nil || pairSource == pairTarget {
			continue
		}
		if (source != "" && pairSource == source) || (target != "" && pairTarget == target) {
			matches = append(matches, session.LanguagePair{Source: pairSource, Target: pairTarget})
		}
	}
	if len(matches) != 1 {
		return "", "", ErrCommandLanguageClarification
	}
	return matches[0].Source, matches[0].Target, nil
}

func validActiveLanguageSnapshot(snapshot session.LanguageConfigSnapshot) bool {
	if snapshot.SessionID == "" || snapshot.Version <= 0 || snapshot.Status != "active" || len(snapshot.LanguagePairs) == 0 {
		return false
	}
	for _, pair := range snapshot.LanguagePairs {
		source, sourceErr := normalizeLanguageTag(strings.TrimSpace(pair.Source))
		target, targetErr := normalizeLanguageTag(strings.TrimSpace(pair.Target))
		if sourceErr != nil || targetErr != nil || source == target {
			return false
		}
	}
	return true
}

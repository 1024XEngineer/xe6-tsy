// Package metrics exposes bounded, process-local realtime operational counters.
package metrics

import (
	"strings"
	"sync/atomic"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/command"
)

// Registry owns monotonic counters for one realtime-audio process. Counters
// reset when the process restarts; monitoring must calculate rates from deltas.
type Registry struct {
	modeCommandsTotal               atomic.Uint64
	modeCommandsAppliedResponse     atomic.Uint64
	modeCommandsUnchangedResponse   atomic.Uint64
	modeCommandsGenerationConflict  atomic.Uint64
	modeCommandsRuntimeMismatch     atomic.Uint64
	modeCommandsOperationConflict   atomic.Uint64
	modeCommandsModeUnavailable     atomic.Uint64
	modeCommandsEventUnavailable    atomic.Uint64
	modeCommandsOtherFailure        atomic.Uint64
	modeChangePublicationsAttempted atomic.Uint64
	modeChangePublicationsAccepted  atomic.Uint64
	modeChangePublicationsFailed    atomic.Uint64
	asrFailures                     atomic.Uint64
	assistantFailures               atomic.Uint64
	translationFailures             atomic.Uint64
	ttsFailures                     atomic.Uint64
	dataChannelFailures             atomic.Uint64
	runtimesStarted                 atomic.Uint64
	runtimesStopped                 atomic.Uint64
	commandInterpretations          atomic.Uint64
	commandInterpretationFailures   atomic.Uint64
	commandInterpretationNanos      atomic.Uint64
	commandOutcomesApplied          atomic.Uint64
	commandOutcomesUnchanged        atomic.Uint64
	commandOutcomesClarification    atomic.Uint64
	commandOutcomesUnsupported      atomic.Uint64
	commandOutcomesFailed           atomic.Uint64
	commandFailuresCapture          atomic.Uint64
	commandFailuresASR              atomic.Uint64
	commandFailuresInterpretation   atomic.Uint64
	commandFailuresNotAllowed       atomic.Uint64
	commandFailuresExecution        atomic.Uint64
	commandFailuresCanceled         atomic.Uint64
}

var defaultRegistry = NewRegistry()

// NewRegistry creates an isolated counter set. Tests and independently
// embedded handlers should use their own registry instead of sharing Default.
func NewRegistry() *Registry {
	return &Registry{}
}

// Default returns the process-wide registry used by production wiring.
func Default() *Registry {
	return defaultRegistry
}

// ModeCommandSnapshot groups mutually exclusive command response outcomes.
// Total is the denominator; after a completed observation it equals the sum of
// the remaining fields.
type ModeCommandSnapshot struct {
	Total              uint64 `json:"total"`
	AppliedResponse    uint64 `json:"applied_response"`
	UnchangedResponse  uint64 `json:"unchanged_response"`
	GenerationConflict uint64 `json:"generation_conflict"`
	RuntimeMismatch    uint64 `json:"runtime_mismatch"`
	OperationConflict  uint64 `json:"operation_conflict"`
	ModeUnavailable    uint64 `json:"mode_unavailable"`
	EventUnavailable   uint64 `json:"event_unavailable"`
	OtherFailure       uint64 `json:"other_failure"`
}

// ModeChangePublicationSnapshot counts durable acceptance attempts for actual
// state transitions. After completed observations, Attempted equals Accepted
// plus Failed.
type ModeChangePublicationSnapshot struct {
	Attempted uint64 `json:"attempted"`
	Accepted  uint64 `json:"accepted"`
	Failed    uint64 `json:"failed"`
}

// Snapshot is one internally consistent-enough point-in-time view of monotonic
// counters. Individual fields can advance while the snapshot is assembled.
type Snapshot struct {
	ModeCommands           ModeCommandSnapshot           `json:"mode_commands"`
	ModeChangePublications ModeChangePublicationSnapshot `json:"mode_change_publications"`
	ProviderFailures       ProviderFailureSnapshot       `json:"provider_failures"`
	DataChannelFailures    uint64                        `json:"data_channel_failures"`
	RuntimesStarted        uint64                        `json:"runtimes_started"`
	RuntimesStopped        uint64                        `json:"runtimes_stopped"`
	SemanticCommands       SemanticCommandSnapshot       `json:"semantic_commands"`
}

// SemanticCommandSnapshot contains bounded counters only. InterpretationDurationMilliseconds is
// cumulative, so monitoring derives average latency from it and Interpretations over a time window.
type SemanticCommandSnapshot struct {
	Interpretations                    uint64 `json:"interpretations"`
	InterpretationFailures             uint64 `json:"interpretation_failures"`
	InterpretationDurationMilliseconds uint64 `json:"interpretation_duration_milliseconds"`
	Applied                            uint64 `json:"applied"`
	Unchanged                          uint64 `json:"unchanged"`
	ClarificationRequired              uint64 `json:"clarification_required"`
	Unsupported                        uint64 `json:"unsupported"`
	Failed                             uint64 `json:"failed"`
	CaptureFailures                    uint64 `json:"capture_failures"`
	ASRFailures                        uint64 `json:"asr_failures"`
	InterpretationStageFailures        uint64 `json:"interpretation_stage_failures"`
	NotAllowedFailures                 uint64 `json:"not_allowed_failures"`
	ExecutionFailures                  uint64 `json:"execution_failures"`
	Canceled                           uint64 `json:"canceled"`
}

type ProviderFailureSnapshot struct {
	ASR         uint64 `json:"asr"`
	Assistant   uint64 `json:"assistant"`
	Translation uint64 `json:"translation"`
	TTS         uint64 `json:"tts"`
}

// RecordProviderFailure counts provider boundary failures without labels.
// Structured logs carry stage/provider details for diagnosis.
func (r *Registry) RecordProviderFailure(stage, _ string) {
	if r == nil {
		return
	}
	switch {
	case strings.HasPrefix(stage, "asr_"):
		r.asrFailures.Add(1)
	case strings.HasPrefix(stage, "assistant_"):
		r.assistantFailures.Add(1)
	case stage == "translation":
		r.translationFailures.Add(1)
	case strings.HasPrefix(stage, "tts_"):
		r.ttsFailures.Add(1)
	}
}

func (r *Registry) RecordDataChannelFailure() {
	if r != nil {
		r.dataChannelFailures.Add(1)
	}
}

func (r *Registry) RecordRuntimeStarted() {
	if r != nil {
		r.runtimesStarted.Add(1)
	}
}
func (r *Registry) RecordRuntimeStopped() {
	if r != nil {
		r.runtimesStopped.Add(1)
	}
}

func (r *Registry) RecordCommandInterpretation(duration time.Duration, failed bool) {
	if r == nil {
		return
	}
	r.commandInterpretations.Add(1)
	if duration > 0 {
		r.commandInterpretationNanos.Add(uint64(duration))
	}
	if failed {
		r.commandInterpretationFailures.Add(1)
	}
}

func (r *Registry) RecordCommandOutcome(status realtimev1.CommandResultStatus, failure command.Failure) {
	if r == nil {
		return
	}
	switch status {
	case realtimev1.CommandResultApplied:
		r.commandOutcomesApplied.Add(1)
	case realtimev1.CommandResultUnchanged:
		r.commandOutcomesUnchanged.Add(1)
	case realtimev1.CommandResultClarificationRequired:
		r.commandOutcomesClarification.Add(1)
	case realtimev1.CommandResultUnsupported:
		r.commandOutcomesUnsupported.Add(1)
	default:
		r.commandOutcomesFailed.Add(1)
	}
	switch failure {
	case command.FailureWindowExpired, command.FailureNoSpeech, command.FailureInvalidAudio:
		r.commandFailuresCapture.Add(1)
	case command.FailureASR:
		r.commandFailuresASR.Add(1)
	case command.FailureInterpretation:
		r.commandFailuresInterpretation.Add(1)
	case command.FailureNotAllowed:
		r.commandFailuresNotAllowed.Add(1)
	case command.FailureExecution:
		r.commandFailuresExecution.Add(1)
	case command.FailureCanceled:
		r.commandFailuresCanceled.Add(1)
	}
}

// Current returns the latest process-local counter values.
func (r *Registry) Current() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	return Snapshot{
		ModeCommands: ModeCommandSnapshot{
			Total:              r.modeCommandsTotal.Load(),
			AppliedResponse:    r.modeCommandsAppliedResponse.Load(),
			UnchangedResponse:  r.modeCommandsUnchangedResponse.Load(),
			GenerationConflict: r.modeCommandsGenerationConflict.Load(),
			RuntimeMismatch:    r.modeCommandsRuntimeMismatch.Load(),
			OperationConflict:  r.modeCommandsOperationConflict.Load(),
			ModeUnavailable:    r.modeCommandsModeUnavailable.Load(),
			EventUnavailable:   r.modeCommandsEventUnavailable.Load(),
			OtherFailure:       r.modeCommandsOtherFailure.Load(),
		},
		ModeChangePublications: ModeChangePublicationSnapshot{
			Attempted: r.modeChangePublicationsAttempted.Load(),
			Accepted:  r.modeChangePublicationsAccepted.Load(),
			Failed:    r.modeChangePublicationsFailed.Load(),
		},
		ProviderFailures: ProviderFailureSnapshot{
			ASR: r.asrFailures.Load(), Assistant: r.assistantFailures.Load(),
			Translation: r.translationFailures.Load(), TTS: r.ttsFailures.Load(),
		},
		DataChannelFailures: r.dataChannelFailures.Load(),
		RuntimesStarted:     r.runtimesStarted.Load(),
		RuntimesStopped:     r.runtimesStopped.Load(),
		SemanticCommands: SemanticCommandSnapshot{
			Interpretations: r.commandInterpretations.Load(), InterpretationFailures: r.commandInterpretationFailures.Load(),
			InterpretationDurationMilliseconds: r.commandInterpretationNanos.Load() / uint64(time.Millisecond),
			Applied:                            r.commandOutcomesApplied.Load(), Unchanged: r.commandOutcomesUnchanged.Load(),
			ClarificationRequired: r.commandOutcomesClarification.Load(), Unsupported: r.commandOutcomesUnsupported.Load(),
			Failed: r.commandOutcomesFailed.Load(), CaptureFailures: r.commandFailuresCapture.Load(),
			ASRFailures: r.commandFailuresASR.Load(), InterpretationStageFailures: r.commandFailuresInterpretation.Load(),
			NotAllowedFailures: r.commandFailuresNotAllowed.Load(), ExecutionFailures: r.commandFailuresExecution.Load(),
			Canceled: r.commandFailuresCanceled.Load(),
		},
	}
}

var _ command.Observer = (*Registry)(nil)

// Package metrics exposes bounded, process-local realtime operational counters.
package metrics

import (
	"strings"
	"sync/atomic"
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
	modeCommandsTransitionPending   atomic.Uint64
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
	TransitionPending  uint64 `json:"transition_pending"`
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
			TransitionPending:  r.modeCommandsTransitionPending.Load(),
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
	}
}

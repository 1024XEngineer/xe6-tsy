// Package languagesv1 defines shared language-configuration contracts.
package languagesv1

// InterpretationOutputMode describes how translated output is presented for a
// bilingual face-to-face session. Translation and Final Turn persistence are
// unchanged by this value; it only selects TTS versus automatic delivery.
type InterpretationOutputMode string

const (
	InterpretationOutputModeBidirectional InterpretationOutputMode = "bidirectional"
	InterpretationOutputModeSingle        InterpretationOutputMode = "single"
)

// Valid reports whether the mode belongs to the shared language contract.
func (m InterpretationOutputMode) Valid() bool {
	switch m {
	case InterpretationOutputModeBidirectional, InterpretationOutputModeSingle:
		return true
	default:
		return false
	}
}

package localruntime

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

// NoopPipeline satisfies session.PipelineManager without starting ASR/TTS workers.
// It unblocks control-plane Start/Stop while media providers are still optional.
type NoopPipeline struct{}

func (NoopPipeline) Start(_ context.Context, _ session.SessionSnapshot) error {
	return nil
}

func (NoopPipeline) Stop(_ context.Context, _ string) error {
	return nil
}

func (NoopPipeline) PipelineActive(string) bool {
	return true
}
